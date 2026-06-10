package storage

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// newTestMetadata creates a Metadata store backed by a temp SQLite DB.
func newTestMetadata(t *testing.T) *Metadata {
	t.Helper()
	dir := t.TempDir()
	m, err := NewMetadata(dir + "/metadata.db")
	if err != nil {
		t.Fatalf("NewMetadata() error = %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestListObjects_LIKEEscape covers CR-6: a prefix containing LIKE wildcards must not
// match keys it should not match.
func TestListObjects_LIKEEscape(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	objs := []*Object{
		{Key: "a_b/key1", Size: 1, ETag: "e1", ContentType: "text/plain"},
		{Key: "aXb/key2", Size: 1, ETag: "e2", ContentType: "text/plain"},
	}
	for _, o := range objs {
		o.LastModified = now
		if err := m.PutObject(ctx, "b", o); err != nil {
			t.Fatalf("PutObject(%q): %v", o.Key, err)
		}
	}

	// Query with prefix "a_b" — must only return "a_b/key1", not "aXb/key2".
	results, err := m.ListObjects(ctx, "b", "a_b", "", 1000)
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListObjects returned %d results, want 1: %v", len(results), results)
	}
	if results[0].Key != "a_b/key1" {
		t.Errorf("ListObjects returned key %q, want %q", results[0].Key, "a_b/key1")
	}
}

// TestForeignKeyCascade covers H-13: deleting a bucket cascades to child objects
// when foreign_keys pragma is enabled.
func TestForeignKeyCascade(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "cascade-bucket", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	obj := &Object{Key: "child/obj", Size: 5, ETag: "etag", ContentType: "text/plain", LastModified: now}
	if err := m.PutObject(ctx, "cascade-bucket", obj); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// Verify the object exists before delete.
	objs, err := m.ListObjects(ctx, "cascade-bucket", "", "", 1000)
	if err != nil {
		t.Fatalf("ListObjects pre-delete: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object before delete, got %d", len(objs))
	}

	// Delete the parent bucket directly via SQL (simulates a hard delete with FK enabled).
	if _, err := m.db.ExecContext(ctx, `DELETE FROM buckets WHERE name = ?`, "cascade-bucket"); err != nil {
		t.Fatalf("DELETE bucket: %v", err)
	}

	// Objects must have been cascade-deleted.
	var count int
	row := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM objects WHERE bucket = ?`, "cascade-bucket")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("COUNT objects: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 objects after cascade delete, got %d (H-13: foreign_keys pragma missing)", count)
	}
}

// openLegacyDB creates a SQLite DB at dbPath populated with the pre-#39
// (bucket, key)-keyed Object Lock schema, so that NewMetadata's startup
// migration can be exercised. It seeds a bucket, a couple of objects with
// versions, and legacy retention / legal hold rows (including a COMPLIANCE
// row). The caller passes dbPath to NewMetadata afterwards.
func openLegacyDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE buckets (name TEXT PRIMARY KEY, creation_date DATETIME NOT NULL)`,
		`CREATE TABLE objects (
			bucket TEXT NOT NULL, key TEXT NOT NULL, size INTEGER NOT NULL,
			last_modified DATETIME NOT NULL, etag TEXT NOT NULL, content_type TEXT NOT NULL,
			metadata TEXT, PRIMARY KEY (bucket, key),
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE)`,
		`CREATE TABLE object_versions (
			bucket TEXT NOT NULL, key TEXT NOT NULL, version_id TEXT NOT NULL,
			size INTEGER NOT NULL, last_modified DATETIME NOT NULL, etag TEXT NOT NULL,
			content_type TEXT NOT NULL, metadata TEXT, is_delete_marker INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bucket, key, version_id),
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE)`,
		// Legacy (bucket, key)-keyed lock tables with the dangerous FK to objects.
		`CREATE TABLE object_retention (
			bucket TEXT NOT NULL, key TEXT NOT NULL, mode TEXT NOT NULL,
			retain_until_date DATETIME NOT NULL, PRIMARY KEY (bucket, key),
			FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE)`,
		`CREATE TABLE object_legal_hold (
			bucket TEXT NOT NULL, key TEXT NOT NULL, status TEXT NOT NULL,
			PRIMARY KEY (bucket, key),
			FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("legacy DDL %q: %v", s, err)
		}
	}

	now := time.Now()
	// Bucket + objects + versions.
	if _, err := db.Exec(`INSERT INTO buckets (name, creation_date) VALUES (?, ?)`, "b", now); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	for _, k := range []string{"gov-key", "comp-key", "hold-key"} {
		if _, err := db.Exec(`INSERT INTO objects (bucket, key, size, last_modified, etag, content_type, metadata)
			VALUES (?, ?, 1, ?, 'e', 'text/plain', '')`, "b", k, now); err != nil {
			t.Fatalf("seed object %q: %v", k, err)
		}
	}
	// gov-key has a real version so backfill binds the lock to it; comp-key /
	// hold-key have no version rows so they backfill to '' (null version).
	if _, err := db.Exec(`INSERT INTO object_versions (bucket, key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker)
		VALUES (?, ?, ?, 1, ?, 'e', 'text/plain', '', 0)`, "b", "gov-key", "ver-1", now); err != nil {
		t.Fatalf("seed version: %v", err)
	}

	retain := now.Add(24 * time.Hour)
	if _, err := db.Exec(`INSERT INTO object_retention (bucket, key, mode, retain_until_date) VALUES (?, ?, 'GOVERNANCE', ?)`, "b", "gov-key", retain); err != nil {
		t.Fatalf("seed gov retention: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO object_retention (bucket, key, mode, retain_until_date) VALUES (?, ?, 'COMPLIANCE', ?)`, "b", "comp-key", retain); err != nil {
		t.Fatalf("seed compliance retention: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO object_legal_hold (bucket, key, status) VALUES (?, ?, 'ON')`, "b", "hold-key"); err != nil {
		t.Fatalf("seed legal hold: %v", err)
	}
}

// TestObjectLockMigration_LegacyToV2 verifies the #39 startup migration:
// legacy (bucket, key) lock rows are migrated to the per-version schema,
// COMPLIANCE rows survive, version_id is backfilled (current version or ” for
// null), and the legacy tables are renamed to *_legacy_v1 (not dropped).
func TestObjectLockMigration_LegacyToV2(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/metadata.db"
	openLegacyDB(t, dbPath)

	m, err := NewMetadata(dbPath)
	if err != nil {
		t.Fatalf("NewMetadata (migration) error = %v", err)
	}
	defer m.Close()

	// gov-key retention is bound to the real version ver-1.
	mode, until, err := m.GetObjectRetention(ctx, "b", "gov-key", "ver-1")
	if err != nil {
		t.Fatalf("GetObjectRetention(gov-key, ver-1): %v", err)
	}
	if mode != "GOVERNANCE" || until == nil {
		t.Errorf("gov-key retention not migrated to version ver-1: mode=%q until=%v", mode, until)
	}

	// comp-key COMPLIANCE retention is bound to the null version ''.
	mode, until, err = m.GetObjectRetention(ctx, "b", "comp-key", "")
	if err != nil {
		t.Fatalf("GetObjectRetention(comp-key, ''): %v", err)
	}
	if mode != "COMPLIANCE" || until == nil {
		t.Errorf("comp-key COMPLIANCE retention not migrated to null version: mode=%q until=%v", mode, until)
	}

	// hold-key legal hold is bound to the null version ''.
	status, err := m.GetObjectLegalHold(ctx, "b", "hold-key", "")
	if err != nil {
		t.Fatalf("GetObjectLegalHold(hold-key, ''): %v", err)
	}
	if status != "ON" {
		t.Errorf("hold-key legal hold not migrated to null version: status=%q", status)
	}

	// Legacy tables must be retained (renamed, not dropped).
	for _, tbl := range []string{"object_retention_legacy_v1", "object_legal_hold_legacy_v1"} {
		var name string
		err := m.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err != nil {
			t.Errorf("legacy table %q missing after migration: %v", tbl, err)
		}
	}

	// user_version must be stamped.
	var uv int
	if err := m.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if uv != objectLockSchemaVersion {
		t.Errorf("user_version = %d, want %d", uv, objectLockSchemaVersion)
	}

	// A physical backup must have been created.
	if _, err := os.Stat(dbPath + ".pre-lockv2.bak"); err != nil {
		t.Errorf("expected pre-migration backup file: %v", err)
	}
}

// TestObjectLockMigration_FailsOnInvalidLegacyMode verifies the migration is
// fail-closed: a legacy retention row with an invalid mode aborts startup
// rather than silently dropping data.
func TestObjectLockMigration_FailsOnInvalidLegacyMode(t *testing.T) {
	dbPath := t.TempDir() + "/metadata.db"
	openLegacyDB(t, dbPath)

	// Inject an invalid-mode legacy row.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen legacy db: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO object_retention (bucket, key, mode, retain_until_date) VALUES (?, ?, 'BOGUS', ?)`, "b", "gov-key-2", time.Now()); err != nil {
		// gov-key-2 has no object row; add one to satisfy the FK.
		_, _ = db.Exec(`INSERT INTO objects (bucket, key, size, last_modified, etag, content_type, metadata) VALUES ('b','gov-key-2',1,?, 'e','text/plain','')`, time.Now())
		if _, err2 := db.Exec(`INSERT INTO object_retention (bucket, key, mode, retain_until_date) VALUES (?, ?, 'BOGUS', ?)`, "b", "gov-key-2", time.Now()); err2 != nil {
			t.Fatalf("inject invalid mode: %v", err2)
		}
	}
	db.Close()

	m, err := NewMetadata(dbPath)
	if err == nil {
		m.Close()
		t.Fatal("expected NewMetadata to fail closed on invalid legacy retention mode, got nil error")
	}
}

// TestPutObject_DoesNotCascadeDeleteLockRows verifies the #39 FK fix: an
// overwrite PUT no longer cascade-deletes lock rows for other versions of the
// same key. A retention on a (real) version survives an unrelated null-version
// overwrite.
func TestPutObject_DoesNotCascadeDeleteLockRows(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// Seed a real version row and a lock on it.
	ver := &ObjectVersion{Key: "k", VersionID: "v-1", Size: 1, LastModified: now, ETag: "e", ContentType: "text/plain"}
	if err := m.PutObjectVersion(ctx, "b", ver); err != nil {
		t.Fatalf("PutObjectVersion: %v", err)
	}
	if err := m.PutObjectRetention(ctx, "b", "k", "v-1", "GOVERNANCE", now.Add(time.Hour)); err != nil {
		t.Fatalf("PutObjectRetention: %v", err)
	}

	// Overwrite the null version of the same key via PutObject (non-versioning
	// path). The legacy code would cascade-delete the v-1 lock row.
	obj := &Object{Key: "k", Size: 2, LastModified: now, ETag: "e2", ContentType: "text/plain"}
	if err := m.PutObject(ctx, "b", obj); err != nil {
		t.Fatalf("PutObject overwrite: %v", err)
	}

	// The v-1 retention row must still be present.
	mode, until, err := m.GetObjectRetention(ctx, "b", "k", "v-1")
	if err != nil {
		t.Fatalf("GetObjectRetention after overwrite: %v", err)
	}
	if mode != "GOVERNANCE" || until == nil {
		t.Errorf("v-1 retention was lost on overwrite (FK cascade regression): mode=%q until=%v", mode, until)
	}
}

// TestPutObject_NullVersionGuardBlocksLockedOverwrite verifies the storage-layer
// fail-closed guard (#39, 論点B): an overwrite PUT is rejected when the null
// version carries an active retention, and allowed once that retention expires.
func TestPutObject_NullVersionGuardBlocksLockedOverwrite(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	obj := &Object{Key: "k", Size: 1, LastModified: now, ETag: "e", ContentType: "text/plain"}
	if err := m.PutObject(ctx, "b", obj); err != nil {
		t.Fatalf("PutObject seed: %v", err)
	}
	// Active retention on the null version.
	if err := m.PutObjectRetention(ctx, "b", "k", "", "GOVERNANCE", now.Add(time.Hour)); err != nil {
		t.Fatalf("PutObjectRetention: %v", err)
	}

	// Overwrite must be refused.
	obj2 := &Object{Key: "k", Size: 2, LastModified: now, ETag: "e2", ContentType: "text/plain"}
	if err := m.PutObject(ctx, "b", obj2); err != ErrObjectLocked {
		t.Fatalf("expected ErrObjectLocked on locked null-version overwrite, got %v", err)
	}

	// Expire the retention; overwrite must now succeed and prune the stale row.
	if err := m.PutObjectRetention(ctx, "b", "k", "", "GOVERNANCE", now.Add(-time.Hour)); err != nil {
		t.Fatalf("PutObjectRetention expire: %v", err)
	}
	if err := m.PutObject(ctx, "b", obj2); err != nil {
		t.Fatalf("PutObject after expiry: %v", err)
	}
	mode, _, err := m.GetObjectRetention(ctx, "b", "k", "")
	if err != nil {
		t.Fatalf("GetObjectRetention after expiry overwrite: %v", err)
	}
	if mode != "" {
		t.Errorf("expired null-version retention row should be pruned, still present: mode=%q", mode)
	}
}

// TestPutObjectCurrentPointer_SkipsNullVersionGuard verifies the versioned write
// path (#39 fix round 1): PutObjectCurrentPointer updates the live objects row
// WITHOUT the null-version Object Lock guard, so creating a new version always
// succeeds even when the null version carries an active retention or legal hold.
// The locked null-version rows must remain intact.
func TestPutObjectCurrentPointer_SkipsNullVersionGuard(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	obj := &Object{Key: "k", Size: 1, LastModified: now, ETag: "e", ContentType: "text/plain"}
	if err := m.PutObject(ctx, "b", obj); err != nil {
		t.Fatalf("PutObject seed: %v", err)
	}
	// Active retention AND legal hold on the null version.
	if err := m.PutObjectRetention(ctx, "b", "k", "", "GOVERNANCE", now.Add(time.Hour)); err != nil {
		t.Fatalf("PutObjectRetention: %v", err)
	}
	if err := m.PutObjectLegalHold(ctx, "b", "k", "", "ON"); err != nil {
		t.Fatalf("PutObjectLegalHold: %v", err)
	}

	// The guarded path is rejected (sanity check the guard is still active).
	obj2 := &Object{Key: "k", Size: 2, LastModified: now, ETag: "e2", ContentType: "text/plain"}
	if err := m.PutObject(ctx, "b", obj2); err != ErrObjectLocked {
		t.Fatalf("expected guarded PutObject to be refused, got %v", err)
	}

	// The versioned current-pointer update must succeed regardless of the lock.
	if err := m.PutObjectCurrentPointer(ctx, "b", obj2); err != nil {
		t.Fatalf("PutObjectCurrentPointer must not be blocked by null-version lock, got %v", err)
	}

	// The locked null-version rows must remain intact.
	mode, until, err := m.GetObjectRetention(ctx, "b", "k", "")
	if err != nil {
		t.Fatalf("GetObjectRetention after current-pointer update: %v", err)
	}
	if mode != "GOVERNANCE" || until == nil {
		t.Errorf("null-version retention must survive versioned write: mode=%q until=%v", mode, until)
	}
	hold, err := m.GetObjectLegalHold(ctx, "b", "k", "")
	if err != nil {
		t.Fatalf("GetObjectLegalHold after current-pointer update: %v", err)
	}
	if hold != "ON" {
		t.Errorf("null-version legal hold must survive versioned write: status=%q", hold)
	}
}

func TestApplyObjectLockOnVersion_Atomic(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	ver := &ObjectVersion{Key: "k", VersionID: "v-1", Size: 1, LastModified: now, ETag: "e", ContentType: "text/plain"}
	if err := m.PutObjectVersion(ctx, "b", ver); err != nil {
		t.Fatalf("PutObjectVersion: %v", err)
	}

	retainUntil := now.Add(24 * time.Hour)
	err := m.ApplyObjectLockOnVersion(ctx, "b", "k", "v-1",
		&ObjectRetention{Mode: ObjectLockRetentionModeGovernance, RetainUntilDate: &retainUntil},
		&ObjectLegalHold{Status: ObjectLegalHoldStatusOn},
	)
	if err != nil {
		t.Fatalf("ApplyObjectLockOnVersion: %v", err)
	}

	mode, until, err := m.GetObjectRetention(ctx, "b", "k", "v-1")
	if err != nil {
		t.Fatalf("GetObjectRetention: %v", err)
	}
	if mode != "GOVERNANCE" || until == nil {
		t.Fatalf("retention not applied: mode=%q until=%v", mode, until)
	}
	hold, err := m.GetObjectLegalHold(ctx, "b", "k", "v-1")
	if err != nil {
		t.Fatalf("GetObjectLegalHold: %v", err)
	}
	if hold != "ON" {
		t.Fatalf("legal hold not applied: status=%q", hold)
	}
}

func TestRollbackNewObjectVersion_RestoresPriorCurrent(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	v1 := &ObjectVersion{Key: "k", VersionID: "v-1", Size: 1, LastModified: now, ETag: "e1", ContentType: "text/plain"}
	if err := m.PutObjectVersion(ctx, "b", v1); err != nil {
		t.Fatalf("PutObjectVersion v-1: %v", err)
	}
	obj1 := &Object{Key: "k", Size: 1, LastModified: now, ETag: "e1", ContentType: "text/plain"}
	if err := m.PutObjectCurrentPointer(ctx, "b", obj1); err != nil {
		t.Fatalf("PutObjectCurrentPointer v-1: %v", err)
	}

	v2 := &ObjectVersion{Key: "k", VersionID: "v-2", Size: 2, LastModified: now.Add(time.Second), ETag: "e2", ContentType: "text/plain"}
	if err := m.PutObjectVersion(ctx, "b", v2); err != nil {
		t.Fatalf("PutObjectVersion v-2: %v", err)
	}
	obj2 := &Object{Key: "k", Size: 2, LastModified: now.Add(time.Second), ETag: "e2", ContentType: "text/plain"}
	if err := m.PutObjectCurrentPointer(ctx, "b", obj2); err != nil {
		t.Fatalf("PutObjectCurrentPointer v-2: %v", err)
	}

	prior, err := m.RollbackNewObjectVersion(ctx, "b", "k", "v-2")
	if err != nil {
		t.Fatalf("RollbackNewObjectVersion: %v", err)
	}
	if prior == nil || prior.VersionID != "v-1" {
		t.Fatalf("expected prior v-1, got %+v", prior)
	}

	got, err := m.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject after rollback: %v", err)
	}
	if got == nil || got.ETag != "e1" {
		t.Fatalf("current pointer not restored: %+v", got)
	}
	if ver, _ := m.GetObjectVersion(ctx, "b", "k", "v-2"); ver != nil {
		t.Fatal("rolled-back version row must be deleted")
	}
}
