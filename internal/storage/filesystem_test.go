package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
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
