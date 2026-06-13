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

// TestDeleteObjectVersioned_TargetedNull_NoRaceWithConcurrentPut exercises the
// version-targeted null-version delete branch on a pre-versioning current
// object racing a concurrent versioned PUT. Same I2 invariant on the current
// file.
func TestDeleteObjectVersioned_TargetedNull_NoRaceWithConcurrentPut(t *testing.T) {
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
