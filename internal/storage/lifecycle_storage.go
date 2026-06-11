package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file implements the storage-layer primitives the lifecycle engine
// relies on (LIFECYCLE_ENGINE_DESIGN §4). The guarded deletion methods all
// follow the same shape: run lock + state guards and the row deletion inside a
// single real BEGIN IMMEDIATE transaction (withImmediateTx), then unlink the
// backing file only after the commit succeeds. Ordering "delete rows → commit
// → unlink file" means a crash can only ever leave an orphan file (invisible,
// GC-able), never a dangling row whose file is missing.

// ListLifecycleObjectKeys returns object keys in a bucket using keyset
// pagination over the DISTINCT union of objects and object_versions, so
// pre-versioning objects (no version rows) are not missed.
func (fs *FileSystem) ListLifecycleObjectKeys(ctx context.Context, bucket, afterKey string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := fs.metadata.db.QueryContext(ctx, `
		SELECT key FROM (
			SELECT key FROM objects WHERE bucket = ? AND key > ?
			UNION
			SELECT key FROM object_versions WHERE bucket = ? AND key > ?
		)
		ORDER BY key LIMIT ?
	`, bucket, afterKey, bucket, afterKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// GetObjectVersionsForKey returns every version of a key ordered by
// (last_modified DESC, version_id DESC); element 0 is the deterministic latest.
func (fs *FileSystem) GetObjectVersionsForKey(ctx context.Context, bucket, key string) ([]ObjectVersion, error) {
	rows, err := fs.metadata.db.QueryContext(ctx, `
		SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
		FROM object_versions WHERE bucket = ? AND key = ?
		ORDER BY last_modified DESC, version_id DESC
	`, bucket, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ObjectVersion
	for rows.Next() {
		var v ObjectVersion
		var metadataStr string
		if err := rows.Scan(&v.Key, &v.VersionID, &v.Size, &v.LastModified, &v.ETag, &v.ContentType, &metadataStr, &v.IsDeleteMarker); err != nil {
			return nil, err
		}
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &v.Metadata); err != nil {
				return nil, err
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ExpireObjectVersionGuarded physically deletes one noncurrent version under
// the lock + state guards (design §4.4).
func (fs *FileSystem) ExpireObjectVersionGuarded(ctx context.Context, bucket, key, versionID string, guards ExpireGuards, now time.Time) (ExpireOutcome, error) {
	nowUTC := now.UTC()
	outcome := ExpireNotFound

	err := fs.metadata.withImmediateTx(ctx, func(conn *sql.Conn) error {
		// Lock guard (always on, fail-closed). Reads compare in Go on UTC.
		locked, err := versionLocked(ctx, conn, bucket, key, versionID, nowUTC)
		if err != nil {
			return err
		}
		if locked {
			outcome = ExpireSkippedLocked
			return nil
		}

		// State guards: read all versions for the key, decide in Go using the
		// (last_modified DESC, version_id DESC) ranking convention.
		vs, err := scanVersionGuardRows(ctx, conn, bucket, key)
		if err != nil {
			return err
		}
		if guards.RequireNoncurrent && !isStillNoncurrent(vs, versionID) {
			outcome = ExpireSkippedStateChanged
			return nil
		}
		if guards.RequireAllVersionsAreDeleteMarkers && anyRealVersion(vs) {
			outcome = ExpireSkippedStateChanged
			return nil
		}

		res, err := conn.ExecContext(ctx,
			`DELETE FROM object_versions WHERE bucket = ? AND key = ? AND version_id = ?`,
			bucket, key, versionID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			outcome = ExpireNotFound
			return nil
		}
		if err := deleteLockACLTagRowsTx(ctx, conn, bucket, key, versionID); err != nil {
			return err
		}
		outcome = ExpireExpired
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrBusy) {
			return ExpireSkippedBusy, nil
		}
		return outcome, err
	}

	if outcome == ExpireExpired {
		if rmErr := os.Remove(fs.versionFilePath(bucket, key, versionID)); rmErr != nil && !os.IsNotExist(rmErr) {
			return ExpireExpired, fmt.Errorf("unlink version file: %w", rmErr)
		}
	}
	return outcome, nil
}

// CreateExpirationDeleteMarker creates an expiration delete marker over the
// current version (design §1-D, §4.4). Data is never destroyed, so retention /
// legal hold do not block it.
func (fs *FileSystem) CreateExpirationDeleteMarker(ctx context.Context, bucket, key, expectedCurrentVersionID string) (string, ExpireOutcome, error) {
	// Snapshot a pre-versioning current object into the null version first.
	// Idempotent no-op once any version row exists for the key.
	if err := fs.snapshotNullVersionIfNeeded(ctx, bucket, key); err != nil {
		return "", ExpireNotFound, err
	}

	markerID := generateVersionID()
	nullMeta, _ := json.Marshal(map[string]string(nil))
	outcome := ExpireSkippedStateChanged

	err := fs.metadata.withImmediateTx(ctx, func(conn *sql.Conn) error {
		vs, err := scanVersionGuardRows(ctx, conn, bucket, key)
		if err != nil {
			return err
		}
		// CAS: latest must still be the version we planned against and not a DM.
		top := topRankedGuard(vs)
		if top == nil || top.versionID != expectedCurrentVersionID || top.isDeleteMarker {
			outcome = ExpireSkippedStateChanged
			return nil
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO object_versions (bucket, key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker)
			VALUES (?, ?, ?, 0, ?, '', '', ?, 1)
		`, bucket, key, markerID, time.Now().UTC(), string(nullMeta)); err != nil {
			return err
		}
		// Drop the current pointer; version data remains under .versions/.
		if _, err := conn.ExecContext(ctx, `DELETE FROM objects WHERE bucket = ? AND key = ?`, bucket, key); err != nil {
			return err
		}
		outcome = ExpireExpired
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrBusy) {
			return "", ExpireSkippedBusy, nil
		}
		return "", outcome, err
	}

	if outcome == ExpireExpired {
		currentPath := filepath.Join(fs.dataDir, bucket, key)
		if rmErr := os.Remove(currentPath); rmErr != nil && !os.IsNotExist(rmErr) {
			return markerID, ExpireExpired, fmt.Errorf("unlink current file: %w", rmErr)
		}
		return markerID, ExpireExpired, nil
	}
	return "", outcome, nil
}

// ExpireCurrentObjectGuarded physically deletes the current object of a
// non-versioned bucket under the null-version lock guard + a last_modified CAS.
func (fs *FileSystem) ExpireCurrentObjectGuarded(ctx context.Context, bucket, key string, expectedLastModified, now time.Time) (ExpireOutcome, error) {
	nowUTC := now.UTC()
	expLM := expectedLastModified.UTC()
	outcome := ExpireNotFound

	err := fs.metadata.withImmediateTx(ctx, func(conn *sql.Conn) error {
		locked, err := versionLocked(ctx, conn, bucket, key, "", nowUTC)
		if err != nil {
			return err
		}
		if locked {
			outcome = ExpireSkippedLocked
			return nil
		}

		var lm time.Time
		err = conn.QueryRowContext(ctx,
			`SELECT last_modified FROM objects WHERE bucket = ? AND key = ?`, bucket, key).Scan(&lm)
		if errors.Is(err, sql.ErrNoRows) {
			outcome = ExpireNotFound
			return nil
		}
		if err != nil {
			return err
		}
		if !lm.UTC().Equal(expLM) {
			outcome = ExpireSkippedStateChanged
			return nil
		}

		res, err := conn.ExecContext(ctx, `DELETE FROM objects WHERE bucket = ? AND key = ?`, bucket, key)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			outcome = ExpireNotFound
			return nil
		}
		if err := deleteLockACLTagRowsTx(ctx, conn, bucket, key, ""); err != nil {
			return err
		}
		outcome = ExpireExpired
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrBusy) {
			return ExpireSkippedBusy, nil
		}
		return outcome, err
	}

	if outcome == ExpireExpired {
		currentPath := filepath.Join(fs.dataDir, bucket, key)
		if rmErr := os.Remove(currentPath); rmErr != nil && !os.IsNotExist(rmErr) {
			return ExpireExpired, fmt.Errorf("unlink current file: %w", rmErr)
		}
	}
	return outcome, nil
}

// RecordLifecycleRun upserts a per-bucket observability summary.
func (fs *FileSystem) RecordLifecycleRun(ctx context.Context, bucket string, lastRunAt time.Time, actions, skippedLocked, errs int) error {
	_, err := fs.metadata.db.ExecContext(ctx, `
		INSERT INTO lifecycle_runs (bucket, last_run_at, actions, skipped_locked, errors)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(bucket) DO UPDATE SET
			last_run_at    = excluded.last_run_at,
			actions        = excluded.actions,
			skipped_locked = excluded.skipped_locked,
			errors         = excluded.errors
	`, bucket, lastRunAt.UTC(), actions, skippedLocked, errs)
	return err
}

// LifecycleOrphanGC removes orphan files under the .versions and .uploads trees
// whose mtime is older than cutoff (design §1-E). It builds the set of paths
// still referenced by rows first, then removes anything in those trees not in
// the set. Current object files (dataDir/bucket/key) are intentionally out of
// scope, and .tmp-* GC is restricted to the internal .versions/.uploads trees
// so a user key literally named ".tmp-..." can never be mistaken for scratch.
func (fs *FileSystem) LifecycleOrphanGC(ctx context.Context, cutoff time.Time) (int, int, error) {
	referencedVersions, err := fs.referencedVersionPaths(ctx)
	if err != nil {
		return 0, 0, err
	}
	referencedUploads, err := fs.referencedUploadDirs(ctx)
	if err != nil {
		return 0, 0, err
	}
	buckets, err := fs.lifecycleBucketNames(ctx)
	if err != nil {
		return 0, 0, err
	}

	scanned, removed := 0, 0

	for _, b := range buckets {
		if err := ctx.Err(); err != nil {
			return scanned, removed, err
		}
		root := filepath.Join(fs.dataDir, b, ".versions")
		s, r, err := gcWalkVersionFiles(root, referencedVersions, cutoff)
		scanned += s
		removed += r
		if err != nil {
			return scanned, removed, err
		}
	}

	s, r, err := gcWalkUploads(filepath.Join(fs.dataDir, ".uploads"), referencedUploads, cutoff)
	scanned += s
	removed += r
	if err != nil {
		return scanned, removed, err
	}
	return scanned, removed, nil
}

func (fs *FileSystem) referencedVersionPaths(ctx context.Context) (map[string]struct{}, error) {
	rows, err := fs.metadata.db.QueryContext(ctx, `SELECT bucket, key, version_id FROM object_versions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var b, k, v string
		if err := rows.Scan(&b, &k, &v); err != nil {
			return nil, err
		}
		set[fs.versionFilePath(b, k, v)] = struct{}{}
	}
	return set, rows.Err()
}

func (fs *FileSystem) referencedUploadDirs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := fs.metadata.db.QueryContext(ctx, `SELECT upload_id FROM multipart_uploads`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[filepath.Join(fs.dataDir, ".uploads", id)] = struct{}{}
	}
	return set, rows.Err()
}

func (fs *FileSystem) lifecycleBucketNames(ctx context.Context) ([]string, error) {
	rows, err := fs.metadata.db.QueryContext(ctx, `SELECT name FROM buckets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// gcWalkVersionFiles removes orphan version files and stale .tmp-* scratch files
// under a bucket's .versions tree.
func gcWalkVersionFiles(root string, referenced map[string]struct{}, cutoff time.Time) (int, int, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	scanned, removed := 0, 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		scanned++
		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil // raced with another remover
			}
			return err
		}
		if !info.ModTime().Before(cutoff) {
			return nil // within grace window
		}
		isTmp := strings.HasPrefix(d.Name(), ".tmp-")
		if _, ok := referenced[path]; ok && !isTmp {
			return nil // live version file
		}
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
		removed++
		return nil
	})
	return scanned, removed, err
}

// gcWalkUploads removes orphan upload directories and stale .tmp-* files at the
// top level of the .uploads tree.
func gcWalkUploads(root string, referenced map[string]struct{}, cutoff time.Time) (int, int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	scanned, removed := 0, 0
	for _, e := range entries {
		scanned++
		path := filepath.Join(root, e.Name())
		info, err := e.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return scanned, removed, err
		}
		if !info.ModTime().Before(cutoff) {
			continue // within grace window
		}
		if e.IsDir() {
			if _, ok := referenced[path]; ok {
				continue // live upload
			}
			if rmErr := os.RemoveAll(path); rmErr != nil {
				return scanned, removed, rmErr
			}
			removed++
			continue
		}
		if strings.HasPrefix(e.Name(), ".tmp-") {
			if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
				return scanned, removed, rmErr
			}
			removed++
		}
	}
	return scanned, removed, nil
}

// --- transaction-scoped helpers -------------------------------------------

// versionLocked reports whether (bucket, key, versionID) is protected by an
// active legal hold or retention. Any retention mode counts — the engine never
// bypasses GOVERNANCE. All reads compare in Go on UTC, never via SQL DATETIME
// string comparison.
func versionLocked(ctx context.Context, conn *sql.Conn, bucket, key, versionID string, nowUTC time.Time) (bool, error) {
	var status string
	err := conn.QueryRowContext(ctx,
		`SELECT status FROM object_legal_hold WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID).Scan(&status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if status == "ON" {
		return true, nil
	}

	var mode string
	var retainUntil time.Time
	err = conn.QueryRowContext(ctx,
		`SELECT mode, retain_until_date FROM object_retention WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID).Scan(&mode, &retainUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return retainUntil.UTC().After(nowUTC), nil
}

// deleteLockACLTagRowsTx removes the retention/legal-hold/acl/tag rows for a
// version inside the current transaction (mirrors DeleteObjectLockRows +
// DeleteObjectACLTagRows but on the pinned conn).
func deleteLockACLTagRowsTx(ctx context.Context, conn *sql.Conn, bucket, key, versionID string) error {
	for _, q := range []string{
		`DELETE FROM object_retention WHERE bucket = ? AND key = ? AND version_id = ?`,
		`DELETE FROM object_legal_hold WHERE bucket = ? AND key = ? AND version_id = ?`,
		`DELETE FROM object_acls WHERE bucket = ? AND key = ? AND version_id = ?`,
		`DELETE FROM object_tags WHERE bucket = ? AND key = ? AND version_id = ?`,
	} {
		if _, err := conn.ExecContext(ctx, q, bucket, key, versionID); err != nil {
			return err
		}
	}
	return nil
}

// guardVersion is the minimal projection needed for the in-transaction state
// guards.
type guardVersion struct {
	versionID      string
	lastModified   time.Time
	isDeleteMarker bool
}

func scanVersionGuardRows(ctx context.Context, conn *sql.Conn, bucket, key string) ([]guardVersion, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT version_id, last_modified, is_delete_marker FROM object_versions WHERE bucket = ? AND key = ?`,
		bucket, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []guardVersion
	for rows.Next() {
		var g guardVersion
		if err := rows.Scan(&g.versionID, &g.lastModified, &g.isDeleteMarker); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// guardRanksAbove reports whether a sorts before b under
// (last_modified DESC, version_id DESC) — the shared latest/noncurrent ordering.
func guardRanksAbove(a, b *guardVersion) bool {
	if !a.lastModified.Equal(b.lastModified) {
		return a.lastModified.After(b.lastModified)
	}
	return a.versionID > b.versionID
}

func topRankedGuard(vs []guardVersion) *guardVersion {
	if len(vs) == 0 {
		return nil
	}
	top := &vs[0]
	for i := 1; i < len(vs); i++ {
		if guardRanksAbove(&vs[i], top) {
			top = &vs[i]
		}
	}
	return top
}

// isStillNoncurrent reports whether vid is not the current (top-ranked) version
// — i.e. a newer version still exists, so a physical delete is safe.
func isStillNoncurrent(vs []guardVersion, vid string) bool {
	top := topRankedGuard(vs)
	if top == nil {
		return false
	}
	return top.versionID != vid
}

func anyRealVersion(vs []guardVersion) bool {
	for i := range vs {
		if !vs[i].isDeleteMarker {
			return true
		}
	}
	return false
}
