package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// --- helpers ---------------------------------------------------------------

// lifecycleTestBucket creates a versioning-enabled bucket.
func lifecycleTestBucket(t *testing.T, fs *FileSystem, bucket string) {
	t.Helper()
	ctx := context.Background()
	if err := fs.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("CreateBucket(%q): %v", bucket, err)
	}
	if err := fs.PutBucketVersioning(ctx, bucket, VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning(%q): %v", bucket, err)
	}
}

// putVersion writes a new version and returns its version ID.
func putVersion(t *testing.T, fs *FileSystem, bucket, key, content string) string {
	t.Helper()
	_, vid, err := fs.PutObjectVersioned(context.Background(), bucket, key,
		bytes.NewReader([]byte(content)), int64(len(content)), "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned(%q): %v", key, err)
	}
	return vid
}

func insertRetention(t *testing.T, fs *FileSystem, bucket, key, versionID, mode string, until time.Time) {
	t.Helper()
	_, err := fs.metadata.db.Exec(
		`INSERT INTO object_retention (bucket, key, version_id, mode, retain_until_date) VALUES (?, ?, ?, ?, ?)`,
		bucket, key, versionID, mode, until)
	if err != nil {
		t.Fatalf("insertRetention: %v", err)
	}
}

func insertLegalHold(t *testing.T, fs *FileSystem, bucket, key, versionID, status string) {
	t.Helper()
	_, err := fs.metadata.db.Exec(
		`INSERT INTO object_legal_hold (bucket, key, version_id, status) VALUES (?, ?, ?, ?)`,
		bucket, key, versionID, status)
	if err != nil {
		t.Fatalf("insertLegalHold: %v", err)
	}
}

func versionRowExists(t *testing.T, fs *FileSystem, bucket, key, versionID string) bool {
	t.Helper()
	v, err := fs.metadata.GetObjectVersion(context.Background(), bucket, key, versionID)
	if err != nil {
		t.Fatalf("GetObjectVersion: %v", err)
	}
	return v != nil
}

func versionFileExists(t *testing.T, fs *FileSystem, bucket, key, versionID string) bool {
	t.Helper()
	_, err := os.Stat(fs.versionFilePath(bucket, key, versionID))
	return err == nil
}

// --- regression: the top-level constraint (I1) -----------------------------

// TestExpireGuarded_LockedVersionsAreNeverDeleted is the direct check of the
// design's overriding constraint: a version under active COMPLIANCE/GOVERNANCE
// retention or legal hold must survive ExpireObjectVersionGuarded untouched.
func TestExpireGuarded_LockedVersionsAreNeverDeleted(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	future := now.Add(time.Hour)

	cases := []struct {
		name  string
		setup func(fs *FileSystem, bucket, key, vid string)
	}{
		{"compliance-future", func(fs *FileSystem, b, k, v string) {
			insertRetention(t, fs, b, k, v, "COMPLIANCE", future)
		}},
		{"governance-future", func(fs *FileSystem, b, k, v string) {
			insertRetention(t, fs, b, k, v, "GOVERNANCE", future)
		}},
		{"legal-hold-on", func(fs *FileSystem, b, k, v string) {
			insertLegalHold(t, fs, b, k, v, "ON")
		}},
		{"legal-hold-on-with-expired-retention", func(fs *FileSystem, b, k, v string) {
			// Even with retention expired, legal hold alone must block.
			insertRetention(t, fs, b, k, v, "GOVERNANCE", now.Add(-time.Hour))
			insertLegalHold(t, fs, b, k, v, "ON")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newTestFileSystem(t)
			lifecycleTestBucket(t, fs, "b")
			v1 := putVersion(t, fs, "b", "k", "v1-data")
			_ = putVersion(t, fs, "b", "k", "v2-data") // v1 becomes noncurrent
			tc.setup(fs, "b", "k", v1)

			outcome, err := fs.ExpireObjectVersionGuarded(ctx, "b", "k", v1,
				ExpireGuards{RequireNoncurrent: true}, now)
			if err != nil {
				t.Fatalf("ExpireObjectVersionGuarded: %v", err)
			}
			if outcome != ExpireSkippedLocked {
				t.Fatalf("outcome = %v, want SkippedLocked", outcome)
			}
			if !versionRowExists(t, fs, "b", "k", v1) {
				t.Error("locked version row was deleted (I1 violation)")
			}
			if !versionFileExists(t, fs, "b", "k", v1) {
				t.Error("locked version file was deleted (I1 violation)")
			}
			if _, err := fs.GetObjectVersioned(ctx, "b", "k", v1); err != nil {
				t.Errorf("GetObjectVersioned(locked) = %v, want retrievable", err)
			}
		})
	}
}

// TestExpireGuarded_ExpiredRetentionAllowsDeletion confirms the converse: once
// retention has passed and no legal hold remains, the version + its lock/file
// rows are fully removed.
func TestExpireGuarded_ExpiredRetentionAllowsDeletion(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	fs := newTestFileSystem(t)
	lifecycleTestBucket(t, fs, "b")
	v1 := putVersion(t, fs, "b", "k", "v1-data")
	_ = putVersion(t, fs, "b", "k", "v2-data")
	insertRetention(t, fs, "b", "k", v1, "COMPLIANCE", now.Add(-time.Hour)) // expired

	outcome, err := fs.ExpireObjectVersionGuarded(ctx, "b", "k", v1,
		ExpireGuards{RequireNoncurrent: true}, now)
	if err != nil {
		t.Fatalf("ExpireObjectVersionGuarded: %v", err)
	}
	if outcome != ExpireExpired {
		t.Fatalf("outcome = %v, want Expired", outcome)
	}
	if versionRowExists(t, fs, "b", "k", v1) {
		t.Error("expired version row still present")
	}
	if versionFileExists(t, fs, "b", "k", v1) {
		t.Error("expired version file still present")
	}
	// Retention row must have been cleaned too.
	var n int
	if err := fs.metadata.db.QueryRow(
		`SELECT COUNT(*) FROM object_retention WHERE bucket='b' AND key='k' AND version_id=?`, v1).Scan(&n); err != nil {
		t.Fatalf("count retention: %v", err)
	}
	if n != 0 {
		t.Errorf("orphan retention rows = %d, want 0", n)
	}
}

// TestExpireGuarded_TimezoneNotationRobust checks that a retain_until_date
// stored in a non-UTC location is still compared correctly (Go-side UTC
// comparison, never SQL string comparison).
func TestExpireGuarded_TimezoneNotationRobust(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	jst := time.FixedZone("JST", 9*3600)
	// One hour in the future, expressed in JST.
	futureJST := now.Add(time.Hour).In(jst)

	fs := newTestFileSystem(t)
	lifecycleTestBucket(t, fs, "b")
	v1 := putVersion(t, fs, "b", "k", "v1")
	_ = putVersion(t, fs, "b", "k", "v2")
	insertRetention(t, fs, "b", "k", v1, "COMPLIANCE", futureJST)

	outcome, err := fs.ExpireObjectVersionGuarded(ctx, "b", "k", v1,
		ExpireGuards{RequireNoncurrent: true}, now)
	if err != nil {
		t.Fatalf("ExpireObjectVersionGuarded: %v", err)
	}
	if outcome != ExpireSkippedLocked {
		t.Fatalf("outcome = %v, want SkippedLocked (JST future is still future)", outcome)
	}
}

// TestExpireGuarded_StateGuards covers the concurrency CAS guards.
func TestExpireGuarded_StateGuards(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("require-noncurrent-but-target-is-current", func(t *testing.T) {
		fs := newTestFileSystem(t)
		lifecycleTestBucket(t, fs, "b")
		v1 := putVersion(t, fs, "b", "k", "only") // single version => current
		outcome, err := fs.ExpireObjectVersionGuarded(ctx, "b", "k", v1,
			ExpireGuards{RequireNoncurrent: true}, now)
		if err != nil {
			t.Fatalf("ExpireObjectVersionGuarded: %v", err)
		}
		if outcome != ExpireSkippedStateChanged {
			t.Fatalf("outcome = %v, want SkippedStateChanged", outcome)
		}
		if !versionRowExists(t, fs, "b", "k", v1) {
			t.Error("current version wrongly deleted")
		}
	})

	t.Run("require-all-DM-but-real-version-present", func(t *testing.T) {
		fs := newTestFileSystem(t)
		lifecycleTestBucket(t, fs, "b")
		v1 := putVersion(t, fs, "b", "k", "real")
		// Create a delete marker over it via the storage method.
		_, _, err := fs.CreateExpirationDeleteMarker(ctx, "b", "k", v1)
		if err != nil {
			t.Fatalf("CreateExpirationDeleteMarker: %v", err)
		}
		// Try EODM cleanup on v1 with the all-DM guard: must skip (v1 is real).
		outcome, err := fs.ExpireObjectVersionGuarded(ctx, "b", "k", v1,
			ExpireGuards{RequireAllVersionsAreDeleteMarkers: true}, now)
		if err != nil {
			t.Fatalf("ExpireObjectVersionGuarded: %v", err)
		}
		if outcome != ExpireSkippedStateChanged {
			t.Fatalf("outcome = %v, want SkippedStateChanged", outcome)
		}
		if !versionRowExists(t, fs, "b", "k", v1) {
			t.Error("real version wrongly removed under all-DM guard")
		}
	})
}

// TestExpireGuarded_Idempotent verifies re-deleting an already-removed version
// is a harmless NotFound (crash recovery / multi-runner safety, I3).
func TestExpireGuarded_Idempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	fs := newTestFileSystem(t)
	lifecycleTestBucket(t, fs, "b")
	v1 := putVersion(t, fs, "b", "k", "v1")
	_ = putVersion(t, fs, "b", "k", "v2")

	out1, err := fs.ExpireObjectVersionGuarded(ctx, "b", "k", v1,
		ExpireGuards{RequireNoncurrent: true}, now)
	if err != nil || out1 != ExpireExpired {
		t.Fatalf("first delete outcome=%v err=%v, want Expired", out1, err)
	}
	out2, err := fs.ExpireObjectVersionGuarded(ctx, "b", "k", v1,
		ExpireGuards{RequireNoncurrent: true}, now)
	if err != nil {
		t.Fatalf("second delete err = %v", err)
	}
	if out2 != ExpireNotFound {
		t.Fatalf("second delete outcome = %v, want NotFound", out2)
	}
}

// TestCreateExpirationDeleteMarker_CAS covers the delete-marker CAS: wrong
// expected version skips, correct one creates exactly one marker and leaves the
// underlying version retrievable, and a repeat call does not double-mark.
func TestCreateExpirationDeleteMarker_CAS(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	lifecycleTestBucket(t, fs, "b")
	v1 := putVersion(t, fs, "b", "k", "data")

	// Wrong expected version => skip, no marker.
	_, outcome, err := fs.CreateExpirationDeleteMarker(ctx, "b", "k", "not-the-current")
	if err != nil {
		t.Fatalf("CreateExpirationDeleteMarker(wrong): %v", err)
	}
	if outcome != ExpireSkippedStateChanged {
		t.Fatalf("wrong-expected outcome = %v, want SkippedStateChanged", outcome)
	}

	// Correct expected version => one marker.
	markerID, outcome, err := fs.CreateExpirationDeleteMarker(ctx, "b", "k", v1)
	if err != nil {
		t.Fatalf("CreateExpirationDeleteMarker(correct): %v", err)
	}
	if outcome != ExpireExpired || markerID == "" {
		t.Fatalf("correct outcome = %v markerID=%q, want Expired + id", outcome, markerID)
	}
	// Underlying version still retrievable (data not destroyed).
	if _, err := fs.GetObjectVersioned(ctx, "b", "k", v1); err != nil {
		t.Errorf("GetObjectVersioned(v1) after DM = %v, want retrievable", err)
	}
	// Current pointer gone, current file gone.
	if obj, _ := fs.metadata.GetObject(ctx, "b", "k"); obj != nil {
		t.Error("objects current pointer still present after DM")
	}

	// Repeat: latest is now the DM, so CAS against v1 must skip (no second marker).
	_, outcome, err = fs.CreateExpirationDeleteMarker(ctx, "b", "k", v1)
	if err != nil {
		t.Fatalf("CreateExpirationDeleteMarker(repeat): %v", err)
	}
	if outcome != ExpireSkippedStateChanged {
		t.Fatalf("repeat outcome = %v, want SkippedStateChanged", outcome)
	}
	vs, err := fs.GetObjectVersionsForKey(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObjectVersionsForKey: %v", err)
	}
	dmCount := 0
	for _, v := range vs {
		if v.IsDeleteMarker {
			dmCount++
		}
	}
	if dmCount != 1 {
		t.Fatalf("delete-marker count = %d, want exactly 1", dmCount)
	}
}

// TestExpireCurrentObjectGuarded covers the non-versioned path: locked objects
// survive, the last_modified CAS catches concurrent overwrites, and an unlocked
// up-to-date object is physically removed.
func TestExpireCurrentObjectGuarded(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	mkObj := func(t *testing.T) (*FileSystem, time.Time) {
		fs := newTestFileSystem(t)
		if err := fs.CreateBucket(ctx, "b"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		if _, err := fs.PutObject(ctx, "b", "k",
			bytes.NewReader([]byte("body")), 4, "text/plain", nil); err != nil {
			t.Fatalf("PutObject: %v", err)
		}
		// Read the stored last_modified back (matches what ListObjectsV2 returns
		// and what the tx re-reads — avoids in-memory vs DB precision skew).
		obj, err := fs.metadata.GetObject(ctx, "b", "k")
		if err != nil || obj == nil {
			t.Fatalf("GetObject: %v", err)
		}
		return fs, obj.LastModified
	}

	t.Run("locked", func(t *testing.T) {
		fs, lm := mkObj(t)
		insertRetention(t, fs, "b", "k", "", "COMPLIANCE", now.Add(time.Hour))
		outcome, err := fs.ExpireCurrentObjectGuarded(ctx, "b", "k", lm, now)
		if err != nil {
			t.Fatalf("ExpireCurrentObjectGuarded: %v", err)
		}
		if outcome != ExpireSkippedLocked {
			t.Fatalf("outcome = %v, want SkippedLocked", outcome)
		}
		if obj, _ := fs.metadata.GetObject(ctx, "b", "k"); obj == nil {
			t.Error("locked current object was deleted (I1 violation)")
		}
	})

	t.Run("stale-last-modified", func(t *testing.T) {
		fs, _ := mkObj(t)
		stale := now.Add(-48 * time.Hour)
		outcome, err := fs.ExpireCurrentObjectGuarded(ctx, "b", "k", stale, now)
		if err != nil {
			t.Fatalf("ExpireCurrentObjectGuarded: %v", err)
		}
		if outcome != ExpireSkippedStateChanged {
			t.Fatalf("outcome = %v, want SkippedStateChanged", outcome)
		}
		if obj, _ := fs.metadata.GetObject(ctx, "b", "k"); obj == nil {
			t.Error("object deleted despite CAS mismatch")
		}
	})

	t.Run("expired", func(t *testing.T) {
		fs, lm := mkObj(t)
		outcome, err := fs.ExpireCurrentObjectGuarded(ctx, "b", "k", lm, now)
		if err != nil {
			t.Fatalf("ExpireCurrentObjectGuarded: %v", err)
		}
		if outcome != ExpireExpired {
			t.Fatalf("outcome = %v, want Expired", outcome)
		}
		if obj, _ := fs.metadata.GetObject(ctx, "b", "k"); obj != nil {
			t.Error("object row still present after expiry")
		}
		if _, err := os.Stat(fs.dataDir + "/b/k"); !os.IsNotExist(err) {
			t.Errorf("current file still present after expiry: %v", err)
		}
	})
}

// TestExpireGuarded_ConcurrentRetentionVsExpire runs a retention write and a
// guarded expire concurrently many times (run with -race). The observable
// safety invariant must always hold: a SkippedLocked outcome means the version
// survives intact, and an Expired/NotFound outcome means it is gone — there is
// never a partial state where the outcome and the row/file disagree.
func TestExpireGuarded_ConcurrentRetentionVsExpire(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	future := now.Add(time.Hour)

	for i := 0; i < 40; i++ {
		fs := newTestFileSystem(t)
		lifecycleTestBucket(t, fs, "b")
		v1 := putVersion(t, fs, "b", "k", "v1")
		_ = putVersion(t, fs, "b", "k", "v2")

		var wg sync.WaitGroup
		wg.Add(2)
		var outcome ExpireOutcome
		var expErr error
		go func() {
			defer wg.Done()
			// Best-effort retention write racing the expire; busy_timeout
			// absorbs lock contention.
			_, _ = fs.metadata.db.Exec(
				`INSERT INTO object_retention (bucket, key, version_id, mode, retain_until_date) VALUES ('b','k',?,'COMPLIANCE',?)`,
				v1, future)
		}()
		go func() {
			defer wg.Done()
			outcome, expErr = fs.ExpireObjectVersionGuarded(ctx, "b", "k", v1,
				ExpireGuards{RequireNoncurrent: true}, now)
		}()
		wg.Wait()

		if expErr != nil {
			t.Fatalf("iter %d: ExpireObjectVersionGuarded err = %v", i, expErr)
		}
		rowExists := versionRowExists(t, fs, "b", "k", v1)
		fileExists := versionFileExists(t, fs, "b", "k", v1)
		switch outcome {
		case ExpireExpired:
			if rowExists || fileExists {
				t.Fatalf("iter %d: Expired but row=%v file=%v still present", i, rowExists, fileExists)
			}
		case ExpireSkippedLocked, ExpireSkippedBusy, ExpireSkippedStateChanged:
			if !rowExists || !fileExists {
				t.Fatalf("iter %d: %v but row=%v file=%v missing", i, outcome, rowExists, fileExists)
			}
		default:
			t.Fatalf("iter %d: unexpected outcome %v", i, outcome)
		}
	}
}

// TestExpireCurrent_NoRaceWithConcurrentPut is the regression for the codex P1
// data-loss race: a guarded current-object expiration must not unlink the file a
// concurrent successful PUT to the same key just published. The per-key lock
// serializes the two, so the end state is always consistent — never an objects
// row whose current file is missing (which on a non-versioned bucket would be
// silent data loss). Run with -race to also catch shared-state races.
func TestExpireCurrent_NoRaceWithConcurrentPut(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 100; i++ {
		fs := newTestFileSystem(t)
		if err := fs.CreateBucket(ctx, "b"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		if _, err := fs.PutObject(ctx, "b", "k", bytes.NewReader([]byte("old")), 3, "text/plain", nil); err != nil {
			t.Fatalf("seed PutObject: %v", err)
		}
		obj, err := fs.metadata.GetObject(ctx, "b", "k")
		if err != nil || obj == nil {
			t.Fatalf("seed GetObject: %v", err)
		}
		expLM := obj.LastModified

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = fs.PutObject(ctx, "b", "k", bytes.NewReader([]byte("brand-new")), 9, "text/plain", nil)
		}()
		go func() {
			defer wg.Done()
			_, _ = fs.ExpireCurrentObjectGuarded(ctx, "b", "k", expLM, now)
		}()
		wg.Wait()

		// Invariant: if the objects row exists, GetObject can read the file
		// (no "row present, file missing"). A clean expiry yields NotFound.
		data, err := fs.GetObject(ctx, "b", "k")
		if err == nil {
			data.Body.Close()
		} else if !errors.Is(err, ErrObjectNotFound) {
			t.Fatalf("iter %d: GetObject = %v; objects row without its current file (data-loss race)", i, err)
		}
	}
}

// TestLifecycleOrphanGC verifies the orphan GC removes only stale, unreferenced
// files under .versions/.uploads, never live version files, recent files
// (within grace), or current object files (design §1-E, I5).
func TestLifecycleOrphanGC(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	lifecycleTestBucket(t, fs, "b")
	v1 := putVersion(t, fs, "b", "k", "v1")
	v2 := putVersion(t, fs, "b", "k", "v2") // v2 current; both have files under .versions

	now := time.Now()
	old := now.Add(-2 * time.Hour)
	cutoff := now.Add(-1 * time.Hour) // remove files older than 1h

	versionsDir := filepath.Join(fs.dataDir, "b", ".versions", "k")

	// Orphan version file (no row), stale → should be removed.
	orphanStale := filepath.Join(versionsDir, "orphan-stale")
	if err := os.WriteFile(orphanStale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(orphanStale, old, old)

	// Orphan version file (no row), recent → should be kept (within grace).
	orphanRecent := filepath.Join(versionsDir, "orphan-recent")
	if err := os.WriteFile(orphanRecent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(orphanRecent, now, now)

	// Stale .tmp scratch file → should be removed.
	staleTmp := filepath.Join(versionsDir, ".tmp-leftover")
	if err := os.WriteFile(staleTmp, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(staleTmp, old, old)

	// Make the live version files stale too — they must survive because they
	// are referenced by rows.
	os.Chtimes(fs.versionFilePath("b", "k", v1), old, old)
	os.Chtimes(fs.versionFilePath("b", "k", v2), old, old)

	// Orphan upload dir (no row), stale → removed. Referenced upload dir → kept.
	uploadsRoot := filepath.Join(fs.dataDir, ".uploads")
	orphanUpload := filepath.Join(uploadsRoot, "orphan-upload")
	if err := os.MkdirAll(orphanUpload, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(orphanUpload, old, old)

	up, err := fs.CreateMultipartUpload(ctx, "b", "k2", "text/plain", nil, "", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	refUpload := filepath.Join(uploadsRoot, up.UploadID)
	if err := os.MkdirAll(refUpload, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(refUpload, old, old)

	if _, _, err := fs.LifecycleOrphanGC(ctx, cutoff); err != nil {
		t.Fatalf("LifecycleOrphanGC: %v", err)
	}

	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	if exists(orphanStale) {
		t.Error("stale orphan version file not removed")
	}
	if !exists(orphanRecent) {
		t.Error("recent orphan file wrongly removed (grace window violated)")
	}
	if exists(staleTmp) {
		t.Error("stale .tmp file not removed")
	}
	if !versionFileExists(t, fs, "b", "k", v1) || !versionFileExists(t, fs, "b", "k", v2) {
		t.Error("live (referenced) version file wrongly removed")
	}
	if exists(orphanUpload) {
		t.Error("stale orphan upload dir not removed")
	}
	if !exists(refUpload) {
		t.Error("referenced upload dir wrongly removed")
	}
	// Current object file must never be touched.
	if !exists(filepath.Join(fs.dataDir, "b", "k")) {
		t.Error("current object file wrongly removed by GC")
	}
}

// TestListLifecycleObjectKeys verifies the objects ∪ object_versions union and
// keyset pagination (pre-versioning objects must not be missed).
func TestListLifecycleObjectKeys(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// Pre-versioning object (objects row only).
	if _, err := fs.PutObject(ctx, "b", "aaa", bytes.NewReader([]byte("x")), 1, "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	// Versioned objects.
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	putVersion(t, fs, "b", "mmm", "1")
	putVersion(t, fs, "b", "zzz", "1")

	keys, err := fs.ListLifecycleObjectKeys(ctx, "b", "", 100)
	if err != nil {
		t.Fatalf("ListLifecycleObjectKeys: %v", err)
	}
	want := []string{"aaa", "mmm", "zzz"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}

	// Keyset pagination: after "aaa" we expect mmm, zzz.
	page, err := fs.ListLifecycleObjectKeys(ctx, "b", "aaa", 1)
	if err != nil {
		t.Fatalf("ListLifecycleObjectKeys(page): %v", err)
	}
	if len(page) != 1 || page[0] != "mmm" {
		t.Fatalf("page = %v, want [mmm]", page)
	}
}

// TestListLifecycleObjectKeys_VersionOnlyPagination guards the per-branch
// LIMIT + DISTINCT optimization: keys that exist only in object_versions
// (current is a delete marker, so no objects row) and carry several versions
// must still be enumerated exactly once and in order, even with a tiny page
// size. Without DISTINCT, a multi-version key would fill a page with duplicate
// rows and earlier keys could starve later ones.
func TestListLifecycleObjectKeys_VersionOnlyPagination(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	lifecycleTestBucket(t, fs, "b")

	want := []string{"a", "b", "c", "d"}
	for _, k := range want {
		putVersion(t, fs, "b", k, "1")
		putVersion(t, fs, "b", k, "2")
		// Unspecified delete creates a delete marker and removes the objects
		// row, leaving the key present only in object_versions with 3 rows.
		if _, _, err := fs.DeleteObjectVersioned(ctx, "b", k, "", false); err != nil {
			t.Fatalf("delete marker %q: %v", k, err)
		}
	}

	var got []string
	after := ""
	for {
		page, err := fs.ListLifecycleObjectKeys(ctx, "b", after, 1) // page size 1: stress pagination
		if err != nil {
			t.Fatalf("ListLifecycleObjectKeys: %v", err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		after = page[len(page)-1]
	}

	if len(got) != len(want) {
		t.Fatalf("enumerated %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("enumerated %v, want %v", got, want)
		}
	}
}
