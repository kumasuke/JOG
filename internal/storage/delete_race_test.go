package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// assertCurrentRowImpliesFile checks the design invariant I2 ("row ⇒ file") for
// the current object: if the objects row exists, its current file on disk must
// also exist. A bare GetObject cannot see this state — a present row whose file
// is missing collapses to os.IsNotExist → ErrObjectNotFound and reads as a clean
// delete — so the check is made directly here.
func assertCurrentRowImpliesFile(t *testing.T, fs *FileSystem, iter int, bucket, key string) {
	t.Helper()
	obj, err := fs.metadata.GetObject(context.Background(), bucket, key)
	if err != nil {
		t.Fatalf("iter %d: metadata.GetObject: %v", iter, err)
	}
	if obj == nil {
		return
	}
	if _, err := os.Stat(filepath.Join(fs.dataDir, bucket, key)); err != nil {
		t.Fatalf("iter %d: objects row present but current file missing (I2 violated, data-loss race): %v", iter, err)
	}
}

// TestDeleteObject_NoRaceWithConcurrentPut is the regression for issue #54 on
// the non-versioned user delete path. A user DeleteObject must not unlink the
// current file that a concurrent successful PutObject to the same key just
// published. On a non-versioned bucket that file is the only copy, so a stale
// unlink slipping between PUT's rename and PUT's row-write is silent data loss
// ("objects row present, current file missing"). The per-key lock + "delete row
// → commit → unlink" ordering serializes the two, so the end state never
// violates I2. Run with -race too, though this is a filesystem-level race.
func TestDeleteObject_NoRaceWithConcurrentPut(t *testing.T) {
	ctx := context.Background()

	for i := 0; i < 200; i++ {
		fs := newTestFileSystem(t)
		if err := fs.CreateBucket(ctx, "b"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		if _, err := fs.PutObject(ctx, "b", "k", bytes.NewReader([]byte("old")), 3, "text/plain", nil); err != nil {
			t.Fatalf("seed PutObject: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = fs.PutObject(ctx, "b", "k", bytes.NewReader([]byte("brand-new")), 9, "text/plain", nil)
		}()
		go func() {
			defer wg.Done()
			_ = fs.DeleteObject(ctx, "b", "k")
		}()
		wg.Wait()

		assertCurrentRowImpliesFile(t, fs, i, "b", "k")
	}
}

// TestDeleteObjectVersioned_NoRaceWithConcurrentPut is the regression for issue
// #54 on the versioned user delete path. An unspecified delete on a versioning
// bucket drops the current pointer and unlinks the current file while a
// concurrent PutObjectVersioned publishes a new current. The per-key lock +
// "delete rows → commit → unlink" ordering must keep the end state consistent:
// if the objects row exists, its current file must be present. Version data
// under .versions/ is never lost regardless. Run with -race.
func TestDeleteObjectVersioned_NoRaceWithConcurrentPut(t *testing.T) {
	ctx := context.Background()

	for i := 0; i < 200; i++ {
		fs := newTestFileSystem(t)
		lifecycleTestBucket(t, fs, "b") // versioning-enabled
		_ = putVersion(t, fs, "b", "k", "old")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _, _ = fs.PutObjectVersioned(ctx, "b", "k",
				bytes.NewReader([]byte("brand-new")), 9, "text/plain", nil)
		}()
		go func() {
			defer wg.Done()
			// Unspecified delete → creates a delete marker, drops current.
			_, _, _ = fs.DeleteObjectVersioned(ctx, "b", "k", "", false)
		}()
		wg.Wait()

		assertCurrentRowImpliesFile(t, fs, i, "b", "k")
	}
}

// assertVersionRowImpliesFile checks the design invariant I2 for a specific
// version row: if a row exists in object_versions (including the null version,
// version_id=""), the corresponding version file on disk must also exist.
// A null-version row with a missing file is the exact corruption described in
// issue #67 — it is invisible to GetObjectVersioned (which returns
// ErrObjectNotFound) but leaves a dangling metadata row.
func assertVersionRowImpliesFile(t *testing.T, fs *FileSystem, iter int, bucket, key string) {
	t.Helper()
	ctx := context.Background()

	// Check null-version row ⇒ null-version file.
	nullRow, err := fs.metadata.GetObjectVersion(ctx, bucket, key, "")
	if err != nil {
		t.Fatalf("iter %d: metadata.GetObjectVersion(null): %v", iter, err)
	}
	if nullRow != nil {
		nullPath := fs.versionFilePath(bucket, key, "")
		if _, err := os.Stat(nullPath); err != nil {
			t.Fatalf("iter %d: null-version row present but version file missing (I2 violated, issue #67): %v", iter, err)
		}
	}
}

// TestDeleteObjectVersioned_TargetedNull_KeepsCurrentRowFileConsistent checks
// that after a targeted null-version permanent delete races against a
// concurrent PutObjectVersioned, the I2 invariant ("objects row ⇒ current
// file") is preserved for the current version.
//
// Verified range: if the objects row exists after both goroutines complete,
// the current file on disk must also exist (assertCurrentRowImpliesFile).
func TestDeleteObjectVersioned_TargetedNull_KeepsCurrentRowFileConsistent(t *testing.T) {
	ctx := context.Background()

	for i := 0; i < 200; i++ {
		fs := newTestFileSystem(t)
		if err := fs.CreateBucket(ctx, "b"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		// Pre-versioning current object (objects row, no version rows).
		if _, err := fs.PutObject(ctx, "b", "k", bytes.NewReader([]byte("old")), 3, "text/plain", nil); err != nil {
			t.Fatalf("seed PutObject: %v", err)
		}
		if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
			t.Fatalf("PutBucketVersioning: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _, _ = fs.PutObjectVersioned(ctx, "b", "k",
				bytes.NewReader([]byte("brand-new")), 9, "text/plain", nil)
		}()
		go func() {
			defer wg.Done()
			// Explicit null-version permanent delete.
			_, _, _ = fs.DeleteObjectVersioned(ctx, "b", "k", "", true)
		}()
		wg.Wait()

		assertCurrentRowImpliesFile(t, fs, i, "b", "k")
	}
}

// TestDeleteObjectVersioned_TargetedNull_KeepsNullVersionRowFileConsistent is
// the regression test for issue #67. It verifies the I2 invariant for the
// null-version row: if object_versions has a row with version_id="" after a
// race between PutObjectVersioned (which triggers snapshotNullVersionIfNeeded)
// and a targeted null-version permanent delete, the null-version file must
// also exist on disk.
//
// Race window (pre-fix): snapshotNullVersionIfNeeded calls copyFile THEN
// PutObjectVersion outside the per-key lock. DeleteObjectVersioned holds the
// key lock throughout, so it can remove the null-version file between PUT's
// copyFile and PUT's row INSERT, leaving a dangling null-version row.
func TestDeleteObjectVersioned_TargetedNull_KeepsNullVersionRowFileConsistent(t *testing.T) {
	ctx := context.Background()

	for i := 0; i < 400; i++ {
		fs := newTestFileSystem(t)
		if err := fs.CreateBucket(ctx, "b"); err != nil {
			t.Fatalf("iter %d: CreateBucket: %v", i, err)
		}
		// Pre-versioning current object (objects row, no version rows) — this
		// causes PutObjectVersioned to call snapshotNullVersionIfNeeded.
		if _, err := fs.PutObject(ctx, "b", "k", bytes.NewReader([]byte("old")), 3, "text/plain", nil); err != nil {
			t.Fatalf("iter %d: seed PutObject: %v", i, err)
		}
		if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
			t.Fatalf("iter %d: PutBucketVersioning: %v", i, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Triggers snapshotNullVersionIfNeeded (pre-versioning object present).
			_, _, _ = fs.PutObjectVersioned(ctx, "b", "k",
				bytes.NewReader([]byte("brand-new")), 9, "text/plain", nil)
		}()
		go func() {
			defer wg.Done()
			// Targeted null-version permanent delete: removes the null-version
			// file (and row if it exists). Pre-fix, it can race the snapshot.
			_, _, _ = fs.DeleteObjectVersioned(ctx, "b", "k", "", true)
		}()
		wg.Wait()

		// Both invariants must hold:
		// 1. current row ⇒ current file (I2, existing check).
		assertCurrentRowImpliesFile(t, fs, i, "b", "k")
		// 2. null-version row ⇒ null-version file (I2 for versions, issue #67).
		assertVersionRowImpliesFile(t, fs, i, "b", "k")
	}
}
