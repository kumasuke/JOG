package storage

import (
	"context"
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
