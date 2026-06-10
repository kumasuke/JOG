package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestFileSystem(t *testing.T) *FileSystem {
	t.Helper()

	dir := t.TempDir()
	fs, err := NewFileSystem(dir, dir+"/metadata.db")
	if err != nil {
		t.Fatalf("NewFileSystem() error = %v", err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return fs
}

func TestFileSystemBucketLifecycle(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)

	if err := fs.CreateBucket(ctx, "bucket"); err != nil {
		t.Fatalf("CreateBucket() error = %v", err)
	}
	if err := fs.CreateBucket(ctx, "bucket"); !errors.Is(err, ErrBucketAlreadyExists) {
		t.Fatalf("CreateBucket() duplicate error = %v, want %v", err, ErrBucketAlreadyExists)
	}

	bucket, err := fs.HeadBucket(ctx, "bucket")
	if err != nil {
		t.Fatalf("HeadBucket() error = %v", err)
	}
	if bucket.Name != "bucket" {
		t.Fatalf("bucket name = %q, want bucket", bucket.Name)
	}

	buckets, err := fs.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("ListBuckets() error = %v", err)
	}
	if len(buckets) != 1 || buckets[0].Name != "bucket" {
		t.Fatalf("ListBuckets() = %#v", buckets)
	}

	if err := fs.DeleteBucket(ctx, "bucket"); err != nil {
		t.Fatalf("DeleteBucket() error = %v", err)
	}
	if _, err := fs.HeadBucket(ctx, "bucket"); !errors.Is(err, ErrBucketNotFound) {
		t.Fatalf("HeadBucket() after delete error = %v, want %v", err, ErrBucketNotFound)
	}
}

func TestFileSystemObjectCRUDAndRange(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	if err := fs.CreateBucket(ctx, "bucket"); err != nil {
		t.Fatalf("CreateBucket() error = %v", err)
	}

	obj, err := fs.PutObject(ctx, "bucket", "dir/object.txt", strings.NewReader("hello world"), 11, "text/plain", map[string]string{"author": "test"})
	if err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}
	if obj.Size != 11 || obj.ContentType != "text/plain" || obj.Metadata["author"] != "test" {
		t.Fatalf("PutObject() object = %#v", obj)
	}

	got, err := fs.GetObject(ctx, "bucket", "dir/object.txt")
	if err != nil {
		t.Fatalf("GetObject() error = %v", err)
	}
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != "hello world" {
		t.Fatalf("body = %q, want hello world", body)
	}

	ranged, err := fs.GetObjectRange(ctx, "bucket", "dir/object.txt", 6, 10)
	if err != nil {
		t.Fatalf("GetObjectRange() error = %v", err)
	}
	defer ranged.Body.Close()
	rangeBody, err := io.ReadAll(ranged.Body)
	if err != nil {
		t.Fatalf("ReadAll(range) error = %v", err)
	}
	if string(rangeBody) != "world" || ranged.Size != 5 {
		t.Fatalf("range body=%q size=%d, want world size 5", rangeBody, ranged.Size)
	}

	head, err := fs.HeadObject(ctx, "bucket", "dir/object.txt")
	if err != nil {
		t.Fatalf("HeadObject() error = %v", err)
	}
	if head.ETag == "" {
		t.Fatal("HeadObject() returned empty ETag")
	}

	if err := fs.DeleteObject(ctx, "bucket", "dir/object.txt"); err != nil {
		t.Fatalf("DeleteObject() error = %v", err)
	}
	if _, err := fs.GetObject(ctx, "bucket", "dir/object.txt"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("GetObject() after delete error = %v, want %v", err, ErrObjectNotFound)
	}
}

func TestFileSystemRejectsInvalidObjectKeys(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	if err := fs.CreateBucket(ctx, "bucket"); err != nil {
		t.Fatalf("CreateBucket() error = %v", err)
	}

	tests := []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "dot_dot", key: ".."},
		{name: "leading_parent", key: "../outside"},
		{name: "middle_parent", key: "dir/../outside"},
		{name: "trailing_parent", key: "dir/.."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := fs.validateObjectKey("bucket", tt.key); !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("validateObjectKey(%q) error = %v, want %v", tt.key, err, ErrInvalidKey)
			}
		})
	}

	t.Run("put_object_public_api", func(t *testing.T) {
		_, err := fs.PutObject(ctx, "bucket", "../outside", strings.NewReader(""), 0, "", nil)
		if !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("PutObject() error = %v, want %v", err, ErrInvalidKey)
		}
	})
}

// TestVersionedWrites_NotBlockedByNullVersionLock reproduces the #39 fix-round-1
// regression: when a key's null version (”) carries an active retention/legal
// hold (reachable via the migration backfill that binds legacy locks to ”, or
// via a retention/legal-hold set on a pre-versioning object), a subsequent
// versioned write MUST create a new version and always succeed, leaving the
// locked prior version intact. Exercises all three versioned write paths:
// PutObjectVersioned, CopyObjectVersioned and CompleteMultipartUploadVersioned.
func TestVersionedWrites_NotBlockedByNullVersionLock(t *testing.T) {
	now := time.Now()

	// seed creates a non-versioning object on key "k", binds an active
	// GOVERNANCE retention to its null version (''), then flips the bucket to
	// versioning Enabled — mirroring the migration/null-version-resolution path.
	seed := func(t *testing.T) *FileSystem {
		t.Helper()
		ctx := context.Background()
		fs := newTestFileSystem(t)
		if err := fs.CreateBucket(ctx, "b"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		if _, err := fs.PutObject(ctx, "b", "k", strings.NewReader("v0"), 2, "text/plain", nil); err != nil {
			t.Fatalf("PutObject seed: %v", err)
		}
		// Bind an active retention to the null version directly via metadata
		// (the migration backfill / pre-versioning PutObjectRetention result).
		if err := fs.metadata.PutObjectRetention(ctx, "b", "k", "", "GOVERNANCE", now.Add(time.Hour)); err != nil {
			t.Fatalf("seed null-version retention: %v", err)
		}
		if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
			t.Fatalf("PutBucketVersioning: %v", err)
		}
		return fs
	}

	// assertNullLockIntact confirms the locked null-version row survived the
	// versioned write (the prior version must remain protected).
	assertNullLockIntact := func(t *testing.T, fs *FileSystem) {
		t.Helper()
		mode, until, err := fs.metadata.GetObjectRetention(context.Background(), "b", "k", "")
		if err != nil {
			t.Fatalf("GetObjectRetention(null): %v", err)
		}
		if mode != "GOVERNANCE" || until == nil {
			t.Errorf("null-version retention must survive versioned write: mode=%q until=%v", mode, until)
		}
	}

	t.Run("PutObjectVersioned", func(t *testing.T) {
		ctx := context.Background()
		fs := seed(t)
		_, versionID, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v1"), 2, "text/plain", nil)
		if err != nil {
			t.Fatalf("PutObjectVersioned blocked by null-version guard: %v", err)
		}
		if versionID == "" {
			t.Fatalf("PutObjectVersioned must return a new version id")
		}
		assertNullLockIntact(t, fs)
	})

	t.Run("CopyObjectVersioned", func(t *testing.T) {
		ctx := context.Background()
		fs := seed(t)
		// Source object on a different key.
		if _, err := fs.PutObject(ctx, "b", "src", strings.NewReader("source"), 6, "text/plain", nil); err != nil {
			t.Fatalf("PutObject src: %v", err)
		}
		_, versionID, err := fs.CopyObjectVersioned(ctx, "b", "src", "", "b", "k", nil)
		if err != nil {
			t.Fatalf("CopyObjectVersioned blocked by null-version guard: %v", err)
		}
		if versionID == "" {
			t.Fatalf("CopyObjectVersioned must return a new version id")
		}
		assertNullLockIntact(t, fs)
	})

	t.Run("CompleteMultipartUploadVersioned", func(t *testing.T) {
		ctx := context.Background()
		fs := seed(t)
		upload, err := fs.CreateMultipartUpload(ctx, "b", "k", "text/plain", nil, "", nil, nil, nil, "")
		if err != nil {
			t.Fatalf("CreateMultipartUpload: %v", err)
		}
		part, err := fs.UploadPart(ctx, "b", "k", upload.UploadID, 1, strings.NewReader("multipartdata"), 13)
		if err != nil {
			t.Fatalf("UploadPart: %v", err)
		}
		_, versionID, err := fs.CompleteMultipartUploadVersioned(ctx, "b", "k", upload.UploadID, []Part{{PartNumber: 1, ETag: part.ETag, Size: 13}})
		if err != nil {
			t.Fatalf("CompleteMultipartUploadVersioned blocked by null-version guard: %v", err)
		}
		if versionID == "" {
			t.Fatalf("CompleteMultipartUploadVersioned must return a new version id")
		}
		assertNullLockIntact(t, fs)
	})
}

func TestPutObjectLockConfiguration_InvalidDefaultRetention(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.SetBucketObjectLockEnabled(ctx, "b", true); err != nil {
		t.Fatalf("SetBucketObjectLockEnabled: %v", err)
	}

	tests := []struct {
		name string
		dr   *DefaultRetention
	}{
		{
			name: "both days and years",
			dr: &DefaultRetention{
				Mode:  ObjectLockRetentionModeGovernance,
				Days:  testInt32Ptr(7),
				Years: testInt32Ptr(1),
			},
		},
		{
			name: "neither days nor years",
			dr: &DefaultRetention{
				Mode: ObjectLockRetentionModeGovernance,
			},
		},
		{
			name: "zero days",
			dr: &DefaultRetention{
				Mode: ObjectLockRetentionModeGovernance,
				Days: testInt32Ptr(0),
			},
		},
		{
			name: "zero years",
			dr: &DefaultRetention{
				Mode:  ObjectLockRetentionModeGovernance,
				Years: testInt32Ptr(0),
			},
		},
		{
			name: "negative days",
			dr: &DefaultRetention{
				Mode: ObjectLockRetentionModeGovernance,
				Days: testInt32Ptr(-1),
			},
		},
		{
			name: "negative years",
			dr: &DefaultRetention{
				Mode:  ObjectLockRetentionModeGovernance,
				Years: testInt32Ptr(-1),
			},
		},
		{
			name: "days with zero years",
			dr: &DefaultRetention{
				Mode:  ObjectLockRetentionModeGovernance,
				Days:  testInt32Ptr(7),
				Years: testInt32Ptr(0),
			},
		},
		{
			name: "days with negative years",
			dr: &DefaultRetention{
				Mode:  ObjectLockRetentionModeGovernance,
				Days:  testInt32Ptr(7),
				Years: testInt32Ptr(-1),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fs.PutObjectLockConfiguration(ctx, "b", &ObjectLockConfiguration{
				ObjectLockEnabled: true,
				Rule: &ObjectLockRule{
					DefaultRetention: tt.dr,
				},
			})
			if !errors.Is(err, ErrMalformedXML) {
				t.Fatalf("PutObjectLockConfiguration() error = %v, want %v", err, ErrMalformedXML)
			}
		})
	}
}

func TestRollbackNewObjectVersion_PriorFileMissingPreservesState(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	_, v1, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v1"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v1: %v", err)
	}
	v2Obj, v2, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v2"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v2: %v", err)
	}
	currentBefore, err := fs.metadata.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject before failed rollback: %v", err)
	}
	v2Ver, err := fs.metadata.GetObjectVersion(ctx, "b", "k", v2)
	if err != nil {
		t.Fatalf("GetObjectVersion v2 before failed rollback: %v", err)
	}
	if v2Ver == nil {
		t.Fatal("v2 version row missing before failed rollback")
	}

	priorPath := fs.versionFilePath("b", "k", v1)
	if err := os.Remove(priorPath); err != nil {
		t.Fatalf("Remove prior version file: %v", err)
	}

	err = fs.RollbackNewObjectVersion(ctx, "b", "k", v2)
	if err == nil {
		t.Fatal("RollbackNewObjectVersion() expected error when prior file is missing")
	}

	current, err := fs.metadata.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject after failed rollback: %v", err)
	}
	if current == nil {
		t.Fatal("current pointer must remain after failed rollback")
	}
	if current.ETag != v2Obj.ETag || current.Size != v2Obj.Size || current.ContentType != v2Obj.ContentType {
		t.Fatalf("current pointer changed after failed rollback: got ETag=%q Size=%d ContentType=%q, want ETag=%q Size=%d ContentType=%q",
			current.ETag, current.Size, current.ContentType, v2Obj.ETag, v2Obj.Size, v2Obj.ContentType)
	}
	if currentBefore == nil ||
		current.ETag != currentBefore.ETag ||
		current.Size != currentBefore.Size ||
		current.ContentType != currentBefore.ContentType {
		t.Fatalf("current pointer differs from pre-rollback state: before=%+v after=%+v", currentBefore, current)
	}
	if v2Ver.ETag != v2Obj.ETag || v2Ver.Size != v2Obj.Size || v2Ver.ContentType != v2Obj.ContentType {
		t.Fatalf("v2 version metadata mismatch: got ETag=%q Size=%d ContentType=%q, want ETag=%q Size=%d ContentType=%q",
			v2Ver.ETag, v2Ver.Size, v2Ver.ContentType, v2Obj.ETag, v2Obj.Size, v2Obj.ContentType)
	}
	if ver, _ := fs.metadata.GetObjectVersion(ctx, "b", "k", v2); ver == nil {
		t.Fatal("new version row must remain after failed rollback")
	}
	if _, statErr := os.Stat(fs.versionFilePath("b", "k", v2)); statErr != nil {
		t.Fatalf("new version file must remain after failed rollback: %v", statErr)
	}
}

func TestObjectVersionExists_NullVersionOnVersionOnlyKey(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	now := time.Now()

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	if err := fs.SetBucketObjectLockEnabled(ctx, "b", true); err != nil {
		t.Fatalf("SetBucketObjectLockEnabled: %v", err)
	}
	_, versionID, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v1"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned: %v", err)
	}
	if versionID == "" {
		t.Fatal("expected non-empty version id")
	}

	exists, err := fs.objectVersionExists(ctx, "b", "k", "")
	if err != nil {
		t.Fatalf("objectVersionExists: %v", err)
	}
	if exists {
		t.Fatal("null version must not exist when only versioned rows are present")
	}

	retention := &ObjectRetention{
		Mode:            ObjectLockRetentionModeGovernance,
		RetainUntilDate: ptrTime(now.Add(time.Hour)),
	}
	if err := fs.PutObjectRetention(ctx, "b", "k", "", retention); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("PutObjectRetention(null) error = %v, want %v", err, ErrObjectNotFound)
	}

	if err := fs.PutObjectLegalHold(ctx, "b", "k", "", &ObjectLegalHold{Status: ObjectLegalHoldStatusOn}); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("PutObjectLegalHold(null) error = %v, want %v", err, ErrObjectNotFound)
	}
}

func TestDeleteObjectVersioned_NullVersionAbsentNoOpWhenEmpty(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	returnedID, isDeleteMarker, err := fs.DeleteObjectVersioned(ctx, "b", "missing-key", "", true)
	if err != nil {
		t.Fatalf("DeleteObjectVersioned(null): %v", err)
	}
	if returnedID != "" || isDeleteMarker {
		t.Fatalf("DeleteObjectVersioned(null) = (%q, %v), want (\"\", false)", returnedID, isDeleteMarker)
	}
}

// TestDeleteObjectVersioned_NullVersionAbsentDeletesACLTagRows verifies issue
// #47: deleting the pre-versioning current object (null version) removes its
// object_acls / object_tags rows.
func TestDeleteObjectVersioned_NullVersionAbsentDeletesACLTagRows(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	if _, err := fs.PutObject(ctx, "b", "k", strings.NewReader("pre"), 3, "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if err := fs.PutObjectTagging(ctx, "b", "k", "", []Tag{{Key: "env", Value: "null"}}); err != nil {
		t.Fatalf("PutObjectTagging: %v", err)
	}
	if err := fs.PutObjectACL(ctx, "b", "k", "", sampleACL("owner-null")); err != nil {
		t.Fatalf("PutObjectACL: %v", err)
	}

	returnedID, isDeleteMarker, err := fs.DeleteObjectVersioned(ctx, "b", "k", "", true)
	if err != nil {
		t.Fatalf("DeleteObjectVersioned(null): %v", err)
	}
	if returnedID != "" || isDeleteMarker {
		t.Fatalf("DeleteObjectVersioned(null) = (%q, %v), want (\"\", false)", returnedID, isDeleteMarker)
	}

	aclCount, tagCount := countObjectACLTagRows(t, fs.metadata, ctx, "b", "k", "")
	if aclCount != 0 || tagCount != 0 {
		t.Errorf("orphan acl/tag rows after null delete = (%d, %d), want (0, 0)", aclCount, tagCount)
	}
}

// TestDeleteObjectVersioned_SpecificVersionDeletesACLTagRows verifies issue #47:
// deleting one version removes only that version's acl/tag rows.
func TestDeleteObjectVersioned_SpecificVersionDeletesACLTagRows(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	_, v1, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v1"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v1: %v", err)
	}
	_, v2, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v2"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v2: %v", err)
	}
	if err := fs.PutObjectTagging(ctx, "b", "k", v1, []Tag{{Key: "ver", Value: "v1"}}); err != nil {
		t.Fatalf("PutObjectTagging(v1): %v", err)
	}
	if err := fs.PutObjectACL(ctx, "b", "k", v1, sampleACL("owner-v1")); err != nil {
		t.Fatalf("PutObjectACL(v1): %v", err)
	}
	if err := fs.PutObjectTagging(ctx, "b", "k", v2, []Tag{{Key: "ver", Value: "v2"}}); err != nil {
		t.Fatalf("PutObjectTagging(v2): %v", err)
	}
	if err := fs.PutObjectACL(ctx, "b", "k", v2, sampleACL("owner-v2")); err != nil {
		t.Fatalf("PutObjectACL(v2): %v", err)
	}

	if _, _, err := fs.DeleteObjectVersioned(ctx, "b", "k", v2, true); err != nil {
		t.Fatalf("DeleteObjectVersioned(v2): %v", err)
	}

	v2ACL, v2Tags := countObjectACLTagRows(t, fs.metadata, ctx, "b", "k", v2)
	if v2ACL != 0 || v2Tags != 0 {
		t.Errorf("v2 orphan acl/tag rows = (%d, %d), want (0, 0)", v2ACL, v2Tags)
	}
	v1ACL, v1Tags := countObjectACLTagRows(t, fs.metadata, ctx, "b", "k", v1)
	if v1ACL != 1 || v1Tags != 1 {
		t.Errorf("v1 acl/tag rows after v2 delete = (%d, %d), want (1, 1)", v1ACL, v1Tags)
	}
}

func TestDeleteObjectVersioned_NullVersionAbsentDeletesPreVersioningCurrent(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	now := time.Now()

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	if _, err := fs.PutObject(ctx, "b", "k", strings.NewReader("pre"), 3, "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if err := fs.metadata.PutObjectRetention(ctx, "b", "k", "", "GOVERNANCE", now.Add(time.Hour)); err != nil {
		t.Fatalf("PutObjectRetention: %v", err)
	}

	returnedID, isDeleteMarker, err := fs.DeleteObjectVersioned(ctx, "b", "k", "", true)
	if err != nil {
		t.Fatalf("DeleteObjectVersioned(null): %v", err)
	}
	if returnedID != "" || isDeleteMarker {
		t.Fatalf("DeleteObjectVersioned(null) = (%q, %v), want (\"\", false)", returnedID, isDeleteMarker)
	}

	if _, err := fs.GetObject(ctx, "b", "k"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("GetObject after null delete = %v, want %v", err, ErrObjectNotFound)
	}
	currentPath := fs.dataDir + "/b/k"
	if _, err := os.Stat(currentPath); !os.IsNotExist(err) {
		t.Fatalf("current file should be removed: stat err=%v", err)
	}
	mode, until, err := fs.metadata.GetObjectRetention(ctx, "b", "k", "")
	if err != nil || mode != "" || until != nil {
		t.Fatalf("null-version retention should be deleted: mode=%q until=%v err=%v", mode, until, err)
	}
}

func TestDeleteObjectVersioned_NullVersionAbsentPreservesCurrent(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	_, versionID, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("latest"), 6, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned: %v", err)
	}

	returnedID, isDeleteMarker, err := fs.DeleteObjectVersioned(ctx, "b", "k", "", true)
	if err != nil {
		t.Fatalf("DeleteObjectVersioned(null): %v", err)
	}
	if returnedID != "" || isDeleteMarker {
		t.Fatalf("DeleteObjectVersioned(null) = (%q, %v), want (\"\", false)", returnedID, isDeleteMarker)
	}

	got, err := fs.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject after null delete: %v", err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	if string(body) != "latest" {
		t.Fatalf("current body = %q, want latest", body)
	}

	ver, err := fs.metadata.GetObjectVersion(ctx, "b", "k", versionID)
	if err != nil || ver == nil {
		t.Fatalf("version row must remain: err=%v ver=%v", err, ver)
	}
}

func TestDeleteObjectVersioned_LatestRestoresPrevious(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	_, v1, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v1"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v1: %v", err)
	}
	_, v2, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v2"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v2: %v", err)
	}

	if _, _, err := fs.DeleteObjectVersioned(ctx, "b", "k", v2, true); err != nil {
		t.Fatalf("DeleteObjectVersioned latest: %v", err)
	}

	got, err := fs.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject after latest delete: %v", err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	if string(body) != "v1" {
		t.Fatalf("current body = %q, want v1", body)
	}

	if _, err := fs.metadata.GetObjectVersion(ctx, "b", "k", v2); err != nil {
		t.Fatalf("GetObjectVersion(v2): %v", err)
	}
	if ver, err := fs.metadata.GetObjectVersion(ctx, "b", "k", v1); err != nil || ver == nil {
		t.Fatalf("v1 must remain: err=%v ver=%v", err, ver)
	}
}

func TestDeleteObjectVersioned_NonLatestNullPreservesCurrent(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)
	now := time.Now()

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	if _, err := fs.PutObject(ctx, "b", "k", strings.NewReader("null"), 4, "text/plain", nil); err != nil {
		t.Fatalf("PutObject seed: %v", err)
	}
	nullVersion := &ObjectVersion{
		Key: "k", VersionID: "", Size: 4, LastModified: now,
		ETag: "e0", ContentType: "text/plain",
	}
	if err := fs.metadata.PutObjectVersion(ctx, "b", nullVersion); err != nil {
		t.Fatalf("PutObjectVersion null: %v", err)
	}
	nullPath := fs.versionFilePath("b", "k", "")
	if err := os.MkdirAll(filepath.Dir(nullPath), 0755); err != nil {
		t.Fatalf("mkdir null version dir: %v", err)
	}
	if err := os.WriteFile(nullPath, []byte("null"), 0644); err != nil {
		t.Fatalf("write null version file: %v", err)
	}

	_, v2, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v2"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v2: %v", err)
	}

	if _, _, err := fs.DeleteObjectVersioned(ctx, "b", "k", "", true); err != nil {
		t.Fatalf("DeleteObjectVersioned(null): %v", err)
	}

	got, err := fs.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject after non-latest null delete: %v", err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	if string(body) != "v2" {
		t.Fatalf("current body = %q, want v2", body)
	}

	if ver, err := fs.metadata.GetObjectVersion(ctx, "b", "k", v2); err != nil || ver == nil {
		t.Fatalf("latest version must remain: err=%v ver=%v", err, ver)
	}
	if ver, err := fs.metadata.GetObjectVersion(ctx, "b", "k", ""); err != nil || ver != nil {
		t.Fatalf("null version row must be deleted: err=%v ver=%v", err, ver)
	}
}

func TestRebuildCurrentAfterVersionDelete_MissingVersionFileFails(t *testing.T) {
	ctx := context.Background()
	fs := newTestFileSystem(t)

	if err := fs.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
	_, v1, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v1"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v1: %v", err)
	}
	_, v2, err := fs.PutObjectVersioned(ctx, "b", "k", strings.NewReader("v2"), 2, "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned v2: %v", err)
	}

	v1Path := fs.versionFilePath("b", "k", v1)
	if err := os.Remove(v1Path); err != nil {
		t.Fatalf("remove v1 file: %v", err)
	}

	if _, _, err := fs.DeleteObjectVersioned(ctx, "b", "k", v2, true); err == nil {
		t.Fatal("DeleteObjectVersioned latest should fail when remaining version file is missing")
	}

	got, err := fs.GetObject(ctx, "b", "k")
	if err != nil {
		t.Fatalf("GetObject after failed rebuild: %v", err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	if string(body) != "v2" {
		t.Fatalf("current body = %q, want v2 (unchanged)", body)
	}
	if ver, err := fs.metadata.GetObjectVersion(ctx, "b", "k", v2); err != nil || ver == nil {
		t.Fatalf("v2 version row must remain after failed rebuild: err=%v ver=%v", err, ver)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func testInt32Ptr(v int32) *int32 { return &v }
