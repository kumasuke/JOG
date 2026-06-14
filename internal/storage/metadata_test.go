package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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

	// user_version must be stamped at the latest generation. Startup runs the
	// #39 lock migration followed by the #41 acl/tags migration, so a DB that
	// began at the legacy generation ends at aclTagsSchemaVersion (the highest
	// stepwise generation), not merely objectLockSchemaVersion.
	var uv int
	if err := m.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if uv != aclTagsSchemaVersion {
		t.Errorf("user_version = %d, want %d", uv, aclTagsSchemaVersion)
	}

	// A physical backup must have been created.
	if _, err := os.Stat(dbPath + ".pre-lockv2.bak"); err != nil {
		t.Errorf("expected pre-migration backup file: %v", err)
	}
}

// TestObjectLockMigration_IdempotentReopen verifies that re-opening an
// already-migrated DB is a no-op: the user_version gate short-circuits the
// migration, the renamed v2 tables are not migrated a second time, and the
// migrated rows survive untouched. This guards the withImmediateTx-based
// migration (issue #57) against accidentally re-running on a current-gen DB.
func TestObjectLockMigration_IdempotentReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/metadata.db"
	openLegacyDB(t, dbPath)

	// First open performs the legacy -> v2 migration.
	m1, err := NewMetadata(dbPath)
	if err != nil {
		t.Fatalf("NewMetadata (first open): %v", err)
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("close first metadata: %v", err)
	}

	// Second open must be idempotent: the dispatcher sees a current-generation
	// user_version and runs no migration.
	m2, err := NewMetadata(dbPath)
	if err != nil {
		t.Fatalf("NewMetadata (reopen): %v", err)
	}
	defer m2.Close()

	var uv int
	if err := m2.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatalf("read user_version after reopen: %v", err)
	}
	if uv != aclTagsSchemaVersion {
		t.Errorf("user_version after reopen = %d, want %d", uv, aclTagsSchemaVersion)
	}

	// Migrated rows must still be readable and unchanged after the no-op reopen.
	mode, until, err := m2.GetObjectRetention(ctx, "b", "gov-key", "ver-1")
	if err != nil {
		t.Fatalf("GetObjectRetention after reopen: %v", err)
	}
	if mode != "GOVERNANCE" || until == nil {
		t.Errorf("gov-key retention lost after idempotent reopen: mode=%q until=%v", mode, until)
	}
	status, err := m2.GetObjectLegalHold(ctx, "b", "hold-key", "")
	if err != nil {
		t.Fatalf("GetObjectLegalHold after reopen: %v", err)
	}
	if status != "ON" {
		t.Errorf("hold-key legal hold lost after idempotent reopen: status=%q", status)
	}

	// The reopen must NOT create a second-generation legacy table (e.g.
	// object_retention_legacy_v1_legacy_v1), which would prove the migration
	// re-ran against the already-promoted tables.
	var doubleMigrated int
	if err := m2.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE '%legacy_v1_legacy_v1'`).
		Scan(&doubleMigrated); err != nil {
		t.Fatalf("inspect for double-migrated tables: %v", err)
	}
	if doubleMigrated != 0 {
		t.Errorf("idempotent reopen re-ran the migration: %d double-migrated tables present", doubleMigrated)
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

// --- issue #41: per-version object_acls / object_tags ---

// sampleACL builds a minimal ACL whose marshalled form differs per call so the
// migration value-loss check has distinguishable rows.
func sampleACL(ownerID string) *ACL {
	return &ACL{
		OwnerID:      ownerID,
		OwnerDisplay: ownerID,
		Grants: []ACLGrant{{
			Permission:  ACLPermissionFullControl,
			GranteeType: ACLGranteeTypeCanonicalUser,
			GranteeID:   ownerID,
		}},
	}
}

// TestObjectTags_PerVersion verifies that tags set on one version are isolated
// from another version of the same key (issue #41).
func TestObjectTags_PerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// Tag the null version and an explicit version separately.
	if err := m.PutObjectTags(ctx, "b", "k", "", []Tag{{Key: "env", Value: "null"}}); err != nil {
		t.Fatalf("PutObjectTags(null): %v", err)
	}
	if err := m.PutObjectTags(ctx, "b", "k", "v-1", []Tag{{Key: "env", Value: "v1"}, {Key: "team", Value: "core"}}); err != nil {
		t.Fatalf("PutObjectTags(v-1): %v", err)
	}

	nullTags, err := m.GetObjectTags(ctx, "b", "k", "")
	if err != nil {
		t.Fatalf("GetObjectTags(null): %v", err)
	}
	if len(nullTags) != 1 || nullTags[0].Value != "null" {
		t.Errorf("null-version tags = %v, want single env=null", nullTags)
	}

	v1Tags, err := m.GetObjectTags(ctx, "b", "k", "v-1")
	if err != nil {
		t.Fatalf("GetObjectTags(v-1): %v", err)
	}
	if len(v1Tags) != 2 {
		t.Errorf("v-1 tags = %v, want 2", v1Tags)
	}

	// Overwriting one version's tags must not touch the other.
	if err := m.PutObjectTags(ctx, "b", "k", "v-1", []Tag{{Key: "env", Value: "v1b"}}); err != nil {
		t.Fatalf("PutObjectTags(v-1 overwrite): %v", err)
	}
	nullTags, _ = m.GetObjectTags(ctx, "b", "k", "")
	if len(nullTags) != 1 || nullTags[0].Value != "null" {
		t.Errorf("null-version tags changed after v-1 overwrite: %v", nullTags)
	}

	// Deleting one version's tags must not touch the other.
	if err := m.DeleteObjectTags(ctx, "b", "k", "v-1"); err != nil {
		t.Fatalf("DeleteObjectTags(v-1): %v", err)
	}
	nullTags, _ = m.GetObjectTags(ctx, "b", "k", "")
	if len(nullTags) != 1 {
		t.Errorf("null-version tags deleted by v-1 delete: %v", nullTags)
	}
}

// TestObjectACL_PerVersion verifies per-version ACL isolation (issue #41).
func TestObjectACL_PerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if err := m.PutObjectACL(ctx, "b", "k", "", sampleACL("owner-null")); err != nil {
		t.Fatalf("PutObjectACL(null): %v", err)
	}
	if err := m.PutObjectACL(ctx, "b", "k", "v-1", sampleACL("owner-v1")); err != nil {
		t.Fatalf("PutObjectACL(v-1): %v", err)
	}

	nullACL, err := m.GetObjectACL(ctx, "b", "k", "")
	if err != nil || nullACL == nil {
		t.Fatalf("GetObjectACL(null): acl=%v err=%v", nullACL, err)
	}
	if nullACL.OwnerID != "owner-null" {
		t.Errorf("null-version ACL owner = %q, want owner-null", nullACL.OwnerID)
	}
	v1ACL, err := m.GetObjectACL(ctx, "b", "k", "v-1")
	if err != nil || v1ACL == nil {
		t.Fatalf("GetObjectACL(v-1): acl=%v err=%v", v1ACL, err)
	}
	if v1ACL.OwnerID != "owner-v1" {
		t.Errorf("v-1 ACL owner = %q, want owner-v1", v1ACL.OwnerID)
	}

	// Updating v-1 ACL must not change the null version's ACL.
	if err := m.PutObjectACL(ctx, "b", "k", "v-1", sampleACL("owner-v1b")); err != nil {
		t.Fatalf("PutObjectACL(v-1 update): %v", err)
	}
	nullACL, _ = m.GetObjectACL(ctx, "b", "k", "")
	if nullACL.OwnerID != "owner-null" {
		t.Errorf("null-version ACL changed after v-1 update: owner=%q", nullACL.OwnerID)
	}
}

// countObjectACLTagRows returns row counts in object_acls / object_tags for a
// specific (bucket, key, version_id) triple.
func countObjectACLTagRows(t *testing.T, m *Metadata, ctx context.Context, bucket, key, versionID string) (aclCount, tagCount int) {
	t.Helper()
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM object_acls WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID).Scan(&aclCount); err != nil {
		t.Fatalf("COUNT object_acls: %v", err)
	}
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM object_tags WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID).Scan(&tagCount); err != nil {
		t.Fatalf("COUNT object_tags: %v", err)
	}
	return aclCount, tagCount
}

// TestDeleteObjectACLTagRows_RemovesRows verifies issue #47: ACL and tag rows
// for a specific version are removed without touching other versions.
func TestDeleteObjectACLTagRows_RemovesRows(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := m.PutObjectTags(ctx, "b", "k", "", []Tag{{Key: "env", Value: "null"}}); err != nil {
		t.Fatalf("PutObjectTags(null): %v", err)
	}
	if err := m.PutObjectACL(ctx, "b", "k", "", sampleACL("owner-null")); err != nil {
		t.Fatalf("PutObjectACL(null): %v", err)
	}
	if err := m.PutObjectTags(ctx, "b", "k", "v-1", []Tag{{Key: "env", Value: "v1"}}); err != nil {
		t.Fatalf("PutObjectTags(v-1): %v", err)
	}
	if err := m.PutObjectACL(ctx, "b", "k", "v-1", sampleACL("owner-v1")); err != nil {
		t.Fatalf("PutObjectACL(v-1): %v", err)
	}

	if err := m.DeleteObjectACLTagRows(ctx, "b", "k", "v-1"); err != nil {
		t.Fatalf("DeleteObjectACLTagRows(v-1): %v", err)
	}
	v1ACL, v1Tags := countObjectACLTagRows(t, m, ctx, "b", "k", "v-1")
	if v1ACL != 0 || v1Tags != 0 {
		t.Errorf("v-1 acl/tag rows after delete = (%d, %d), want (0, 0)", v1ACL, v1Tags)
	}
	nullACL, nullTags := countObjectACLTagRows(t, m, ctx, "b", "k", "")
	if nullACL != 1 || nullTags != 1 {
		t.Errorf("null-version acl/tag rows = (%d, %d), want (1, 1)", nullACL, nullTags)
	}

	if err := m.DeleteObjectACLTagRows(ctx, "b", "k", ""); err != nil {
		t.Fatalf("DeleteObjectACLTagRows(null): %v", err)
	}
	nullACL, nullTags = countObjectACLTagRows(t, m, ctx, "b", "k", "")
	if nullACL != 0 || nullTags != 0 {
		t.Errorf("null-version acl/tag rows after delete = (%d, %d), want (0, 0)", nullACL, nullTags)
	}
}

// TestPutObject_DoesNotCascadeDeleteACLTagRows verifies the #41 FK fix: an
// overwrite PUT on the null version no longer cascade-deletes the acl/tag rows
// of a different (real) version of the same key.
func TestPutObject_DoesNotCascadeDeleteACLTagRows(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	now := time.Now()

	if err := m.CreateBucket(ctx, "b", now); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// Seed a real version row plus its tags and ACL.
	ver := &ObjectVersion{Key: "k", VersionID: "v-1", Size: 1, LastModified: now, ETag: "e", ContentType: "text/plain"}
	if err := m.PutObjectVersion(ctx, "b", ver); err != nil {
		t.Fatalf("PutObjectVersion: %v", err)
	}
	if err := m.PutObjectTags(ctx, "b", "k", "v-1", []Tag{{Key: "keep", Value: "me"}}); err != nil {
		t.Fatalf("PutObjectTags(v-1): %v", err)
	}
	if err := m.PutObjectACL(ctx, "b", "k", "v-1", sampleACL("owner-v1")); err != nil {
		t.Fatalf("PutObjectACL(v-1): %v", err)
	}

	// Overwrite the null version of the same key (non-versioning path). The
	// legacy FK-to-objects cascade would wipe the v-1 acl/tag rows.
	obj := &Object{Key: "k", Size: 2, LastModified: now, ETag: "e2", ContentType: "text/plain"}
	if err := m.PutObject(ctx, "b", obj); err != nil {
		t.Fatalf("PutObject overwrite: %v", err)
	}

	tags, err := m.GetObjectTags(ctx, "b", "k", "v-1")
	if err != nil {
		t.Fatalf("GetObjectTags(v-1) after overwrite: %v", err)
	}
	if len(tags) != 1 || tags[0].Key != "keep" {
		t.Errorf("v-1 tags lost on overwrite (FK cascade regression): %v", tags)
	}
	acl, err := m.GetObjectACL(ctx, "b", "k", "v-1")
	if err != nil {
		t.Fatalf("GetObjectACL(v-1) after overwrite: %v", err)
	}
	if acl == nil || acl.OwnerID != "owner-v1" {
		t.Errorf("v-1 ACL lost on overwrite (FK cascade regression): %v", acl)
	}
}

// openLegacyACLTagsDB creates a SQLite DB with BOTH the pre-#39 lock schema and
// the pre-#41 (bucket, key)[, tag_key]-keyed acl/tag schema, so NewMetadata's
// startup migrations are exercised end to end. It seeds a bucket, objects (one
// with a real version), legacy acl rows and legacy tag rows.
func openLegacyACLTagsDB(t *testing.T, dbPath string) {
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
		// Legacy lock tables so migrateObjectLockSchema runs its legacy path.
		`CREATE TABLE object_retention (
			bucket TEXT NOT NULL, key TEXT NOT NULL, mode TEXT NOT NULL,
			retain_until_date DATETIME NOT NULL, PRIMARY KEY (bucket, key),
			FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE)`,
		`CREATE TABLE object_legal_hold (
			bucket TEXT NOT NULL, key TEXT NOT NULL, status TEXT NOT NULL,
			PRIMARY KEY (bucket, key),
			FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE)`,
		// Legacy acl/tag tables with the dangerous FK to objects (issue #41).
		`CREATE TABLE object_acls (
			bucket TEXT NOT NULL, key TEXT NOT NULL, acl_config TEXT NOT NULL,
			PRIMARY KEY (bucket, key),
			FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE)`,
		`CREATE TABLE object_tags (
			bucket TEXT NOT NULL, key TEXT NOT NULL, tag_key TEXT NOT NULL, tag_value TEXT NOT NULL,
			PRIMARY KEY (bucket, key, tag_key),
			FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("legacy DDL %q: %v", s, err)
		}
	}

	now := time.Now()
	if _, err := db.Exec(`INSERT INTO buckets (name, creation_date) VALUES (?, ?)`, "b", now); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	for _, k := range []string{"ver-key", "null-key"} {
		if _, err := db.Exec(`INSERT INTO objects (bucket, key, size, last_modified, etag, content_type, metadata)
			VALUES (?, ?, 1, ?, 'e', 'text/plain', '')`, "b", k, now); err != nil {
			t.Fatalf("seed object %q: %v", k, err)
		}
	}
	// ver-key has a real version so backfill binds its acl/tag rows to it;
	// null-key has no version rows so they backfill to '' (null version).
	if _, err := db.Exec(`INSERT INTO object_versions (bucket, key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker)
		VALUES (?, ?, ?, 1, ?, 'e', 'text/plain', '', 0)`, "b", "ver-key", "ver-1", now); err != nil {
		t.Fatalf("seed version: %v", err)
	}

	// Legacy acl rows.
	if _, err := db.Exec(`INSERT INTO object_acls (bucket, key, acl_config) VALUES (?, ?, ?)`, "b", "ver-key", `{"OwnerID":"acl-ver"}`); err != nil {
		t.Fatalf("seed acl ver-key: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO object_acls (bucket, key, acl_config) VALUES (?, ?, ?)`, "b", "null-key", `{"OwnerID":"acl-null"}`); err != nil {
		t.Fatalf("seed acl null-key: %v", err)
	}
	// Legacy tag rows (two for ver-key, one for null-key).
	if _, err := db.Exec(`INSERT INTO object_tags (bucket, key, tag_key, tag_value) VALUES (?, ?, ?, ?)`, "b", "ver-key", "env", "prod"); err != nil {
		t.Fatalf("seed tag ver-key/env: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO object_tags (bucket, key, tag_key, tag_value) VALUES (?, ?, ?, ?)`, "b", "ver-key", "team", "core"); err != nil {
		t.Fatalf("seed tag ver-key/team: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO object_tags (bucket, key, tag_key, tag_value) VALUES (?, ?, ?, ?)`, "b", "null-key", "env", "dev"); err != nil {
		t.Fatalf("seed tag null-key/env: %v", err)
	}
}

// TestACLTagsMigration_LegacyToV2 verifies the #41 startup migration: legacy
// (bucket, key)[, tag_key] acl/tag rows are migrated to the per-version schema,
// version_id is backfilled (current real version, or ” for the null version),
// values are preserved, and the legacy tables are renamed to *_legacy_v1.
func TestACLTagsMigration_LegacyToV2(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/metadata.db"
	openLegacyACLTagsDB(t, dbPath)

	m, err := NewMetadata(dbPath)
	if err != nil {
		t.Fatalf("NewMetadata (migration) error = %v", err)
	}
	defer m.Close()

	// ver-key acl/tags are bound to the real version ver-1.
	acl, err := m.GetObjectACL(ctx, "b", "ver-key", "ver-1")
	if err != nil || acl == nil {
		t.Fatalf("GetObjectACL(ver-key, ver-1): acl=%v err=%v", acl, err)
	}
	if acl.OwnerID != "acl-ver" {
		t.Errorf("ver-key acl not migrated to ver-1: owner=%q", acl.OwnerID)
	}
	verTags, err := m.GetObjectTags(ctx, "b", "ver-key", "ver-1")
	if err != nil {
		t.Fatalf("GetObjectTags(ver-key, ver-1): %v", err)
	}
	if len(verTags) != 2 {
		t.Errorf("ver-key tags not migrated to ver-1: %v", verTags)
	}

	// null-key acl/tags are bound to the null version ''.
	acl, err = m.GetObjectACL(ctx, "b", "null-key", "")
	if err != nil || acl == nil {
		t.Fatalf("GetObjectACL(null-key, ''): acl=%v err=%v", acl, err)
	}
	if acl.OwnerID != "acl-null" {
		t.Errorf("null-key acl not migrated to null version: owner=%q", acl.OwnerID)
	}
	nullTags, err := m.GetObjectTags(ctx, "b", "null-key", "")
	if err != nil {
		t.Fatalf("GetObjectTags(null-key, ''): %v", err)
	}
	if len(nullTags) != 1 || nullTags[0].Value != "dev" {
		t.Errorf("null-key tags not migrated to null version: %v", nullTags)
	}

	// Legacy tables must be retained (renamed, not dropped).
	for _, tbl := range []string{"object_acls_legacy_v1", "object_tags_legacy_v1"} {
		var name string
		if err := m.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			t.Errorf("legacy table %q missing after migration: %v", tbl, err)
		}
	}

	// user_version must be stamped at the acl/tags generation.
	var uv int
	if err := m.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if uv != aclTagsSchemaVersion {
		t.Errorf("user_version = %d, want %d", uv, aclTagsSchemaVersion)
	}

	// A physical backup must have been created.
	if _, err := os.Stat(dbPath + ".pre-acltagsv2.bak"); err != nil {
		t.Errorf("expected pre-migration backup file: %v", err)
	}

	// Re-opening the already-migrated DB must be a no-op (idempotent).
	if err := m.Close(); err != nil {
		t.Fatalf("close before reopen: %v", err)
	}
	m2, err := NewMetadata(dbPath)
	if err != nil {
		t.Fatalf("NewMetadata reopen error = %v", err)
	}
	defer m2.Close()
	acl, err = m2.GetObjectACL(ctx, "b", "ver-key", "ver-1")
	if err != nil || acl == nil || acl.OwnerID != "acl-ver" {
		t.Errorf("idempotent reopen lost ver-key acl: acl=%v err=%v", acl, err)
	}
}

// TestFreshDB_ACLTagsSchemaIsPerVersion verifies a brand-new DB starts directly
// on the per-version acl/tags schema (version_id column present, FK to buckets).
func TestFreshDB_ACLTagsSchemaIsPerVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)

	for _, tbl := range []string{"object_acls", "object_tags"} {
		var hasVersionID bool
		rows, err := m.db.QueryContext(ctx, `PRAGMA table_info('`+tbl+`')`)
		if err != nil {
			t.Fatalf("table_info(%s): %v", tbl, err)
		}
		for rows.Next() {
			var cid, notNull, pk int
			var name, ctype string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				t.Fatalf("scan column info: %v", err)
			}
			if name == "version_id" {
				hasVersionID = true
			}
		}
		rows.Close()
		if !hasVersionID {
			t.Errorf("fresh %s table missing version_id column", tbl)
		}
	}

	var uv int
	if err := m.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if uv != aclTagsSchemaVersion {
		t.Errorf("fresh DB user_version = %d, want %d", uv, aclTagsSchemaVersion)
	}
}

const (
	legacyObjectACLsDDL = `CREATE TABLE object_acls (
		bucket TEXT NOT NULL, key TEXT NOT NULL, acl_config TEXT NOT NULL,
		PRIMARY KEY (bucket, key),
		FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE)`
	legacyObjectTagsDDL = `CREATE TABLE object_tags (
		bucket TEXT NOT NULL, key TEXT NOT NULL, tag_key TEXT NOT NULL, tag_value TEXT NOT NULL,
		PRIMARY KEY (bucket, key, tag_key),
		FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE)`
)

type aclTagsSchemaSnapshot struct {
	userVersion      int
	aclsExists       bool
	tagsExists       bool
	aclsHasVersionID bool
	tagsHasVersionID bool
}

func tableExistsInDB(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var tbl string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&tbl)
	return err == nil
}

func tableHasVersionIDColumn(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info('` + table + `')`)
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var colName, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &colName, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if colName == "version_id" {
			return true
		}
	}
	return false
}

func snapshotACLTagsSchema(t *testing.T, dbPath string) aclTagsSchemaSnapshot {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db for snapshot: %v", err)
	}
	defer db.Close()

	var snap aclTagsSchemaSnapshot
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&snap.userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	snap.aclsExists = tableExistsInDB(t, db, "object_acls")
	snap.tagsExists = tableExistsInDB(t, db, "object_tags")
	if snap.aclsExists {
		snap.aclsHasVersionID = tableHasVersionIDColumn(t, db, "object_acls")
	}
	if snap.tagsExists {
		snap.tagsHasVersionID = tableHasVersionIDColumn(t, db, "object_tags")
	}
	return snap
}

// openPreACLTagsMigrationDB creates a DB with the lock migration already
// satisfied (v2 lock tables, user_version == objectLockSchemaVersion). The
// caller supplies acl/tag DDL; an empty string leaves that table absent.
func openPreACLTagsMigrationDB(t *testing.T, dbPath string, aclsDDL, tagsDDL string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open pre-acl/tags db: %v", err)
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
		createRetentionV2DDL,
		createLegalHoldV2DDL,
	}
	if aclsDDL != "" {
		stmts = append(stmts, aclsDDL)
	}
	if tagsDDL != "" {
		stmts = append(stmts, tagsDDL)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("pre-acl/tags DDL %q: %v", s, err)
		}
	}

	now := time.Now()
	if _, err := db.Exec(`INSERT INTO buckets (name, creation_date) VALUES (?, ?)`, "b", now); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO objects (bucket, key, size, last_modified, etag, content_type, metadata)
		VALUES (?, ?, 1, ?, 'e', 'text/plain', '')`, "b", "k", now); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	if aclsDDL == legacyObjectACLsDDL {
		if _, err := db.Exec(`INSERT INTO object_acls (bucket, key, acl_config) VALUES (?, ?, ?)`, "b", "k", `{"OwnerID":"keep"}`); err != nil {
			t.Fatalf("seed legacy acl: %v", err)
		}
	}
	if tagsDDL == legacyObjectTagsDDL {
		if _, err := db.Exec(`INSERT INTO object_tags (bucket, key, tag_key, tag_value) VALUES (?, ?, ?, ?)`, "b", "k", "env", "keep"); err != nil {
			t.Fatalf("seed legacy tag: %v", err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, objectLockSchemaVersion)); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
}

// TestACLTagsMigration_FailsOnMixedGeneration verifies migrateACLTagsSchema is
// fail-closed when object_acls and object_tags are not the same generation.
func TestACLTagsMigration_FailsOnMixedGeneration(t *testing.T) {
	cases := []struct {
		name          string
		aclsDDL       string
		tagsDDL       string
		wantErrSubstr string
	}{
		{
			name:          "legacy_acls_v2_tags",
			aclsDDL:       legacyObjectACLsDDL,
			tagsDDL:       createObjectTagsV2DDL,
			wantErrSubstr: "object_acls=legacy object_tags=v2",
		},
		{
			name:          "absent_acls_legacy_tags",
			aclsDDL:       "",
			tagsDDL:       legacyObjectTagsDDL,
			wantErrSubstr: "object_acls=absent object_tags=legacy",
		},
		{
			name:          "absent_acls_v2_tags",
			aclsDDL:       "",
			tagsDDL:       createObjectTagsV2DDL,
			wantErrSubstr: "object_acls=absent object_tags=v2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := t.TempDir() + "/metadata.db"
			openPreACLTagsMigrationDB(t, dbPath, tc.aclsDDL, tc.tagsDDL)
			before := snapshotACLTagsSchema(t, dbPath)

			_, err := NewMetadata(dbPath)
			if err == nil {
				t.Fatal("expected NewMetadata to fail on mixed acl/tags generation")
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErrSubstr)
			}

			after := snapshotACLTagsSchema(t, dbPath)
			if after != before {
				t.Errorf("schema changed on failure:\nbefore=%+v\nafter=%+v", before, after)
			}
		})
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

func TestObjectLockSchema_MismatchFailsStartup(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, db *sql.DB)
	}{
		{
			name: "retention_v2_legal_hold_legacy",
			seed: func(t *testing.T, db *sql.DB) {
				execDDL(t, db, createRetentionV2DDL)
				execDDL(t, db, `CREATE TABLE object_legal_hold (
					bucket TEXT NOT NULL, key TEXT NOT NULL, status TEXT NOT NULL,
					PRIMARY KEY (bucket, key),
					FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE)`)
			},
		},
		{
			name: "retention_legacy_legal_hold_v2",
			seed: func(t *testing.T, db *sql.DB) {
				execDDL(t, db, `CREATE TABLE object_retention (
					bucket TEXT NOT NULL, key TEXT NOT NULL, mode TEXT NOT NULL,
					retain_until_date DATETIME NOT NULL, PRIMARY KEY (bucket, key),
					FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE)`)
				execDDL(t, db, createLegalHoldV2DDL)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := t.TempDir() + "/metadata.db"
			db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			if _, err := db.Exec(`CREATE TABLE buckets (name TEXT PRIMARY KEY, creation_date DATETIME NOT NULL)`); err != nil {
				t.Fatalf("create buckets: %v", err)
			}
			tt.seed(t, db)
			db.Close()

			m, err := NewMetadata(dbPath)
			if err == nil {
				m.Close()
				t.Fatal("expected NewMetadata to fail on schema mismatch, got nil error")
			}
			if !strings.Contains(err.Error(), "schema mismatch") {
				t.Fatalf("error = %v, want schema mismatch", err)
			}
		})
	}
}

func execDDL(t *testing.T, db *sql.DB, ddl string) {
	t.Helper()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("exec DDL: %v", err)
	}
}

func TestValidateLockMigration_LegalHoldOrphanFails(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/metadata.db"
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	execDDL(t, db, `CREATE TABLE buckets (name TEXT PRIMARY KEY, creation_date DATETIME NOT NULL)`)
	execDDL(t, db, `CREATE TABLE object_versions (
		bucket TEXT NOT NULL, key TEXT NOT NULL, version_id TEXT NOT NULL,
		size INTEGER NOT NULL, last_modified DATETIME NOT NULL, etag TEXT NOT NULL,
		content_type TEXT NOT NULL, metadata TEXT, is_delete_marker INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (bucket, key, version_id),
		FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE)`)
	execDDL(t, db, `CREATE TABLE object_retention (
		bucket TEXT NOT NULL, key TEXT NOT NULL, mode TEXT NOT NULL,
		retain_until_date DATETIME NOT NULL, PRIMARY KEY (bucket, key))`)
	execDDL(t, db, `CREATE TABLE object_legal_hold (
		bucket TEXT NOT NULL, key TEXT NOT NULL, status TEXT NOT NULL,
		PRIMARY KEY (bucket, key))`)
	execDDL(t, db, `CREATE TABLE object_retention_v2 (
		bucket TEXT NOT NULL, key TEXT NOT NULL, version_id TEXT NOT NULL DEFAULT '',
		mode TEXT NOT NULL, retain_until_date DATETIME NOT NULL,
		PRIMARY KEY (bucket, key, version_id))`)
	execDDL(t, db, `CREATE TABLE object_legal_hold_v2 (
		bucket TEXT NOT NULL, key TEXT NOT NULL, version_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL, PRIMARY KEY (bucket, key, version_id))`)

	now := time.Now()
	if _, err := db.Exec(`INSERT INTO buckets (name, creation_date) VALUES (?, ?)`, "b", now); err != nil {
		t.Fatalf("insert bucket: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO object_legal_hold (bucket, key, status) VALUES (?, ?, 'ON')`, "b", "k"); err != nil {
		t.Fatalf("insert legacy legal hold: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO object_legal_hold_v2 (bucket, key, version_id, status)
		VALUES (?, ?, ?, 'ON')`, "b", "k", "missing-version"); err != nil {
		t.Fatalf("insert orphan legal hold: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	err = validateLockMigration(ctx, tx)
	if err == nil {
		t.Fatal("expected validateLockMigration to fail on legal hold orphan, got nil")
	}
	if !strings.Contains(err.Error(), "legal hold rows reference a non-existent version") {
		t.Fatalf("error = %v, want legal hold orphan message", err)
	}
}

// TestWithImmediateTx_BusyTimeoutOption verifies that withBusyTimeout(ms) controls
// how long withImmediateTx waits for a contended write lock, and that omitting the
// option uses the engineBusyTimeoutMS (100 ms) default (fail-closed behaviour).
//
// The test pins a lock-holding connection that releases after ~300 ms. A call
// with withBusyTimeout(defaultBusyTimeoutMS) (5000 ms) must succeed; the 100 ms
// default would have timed out and returned ErrBusy. A separate subtest
// confirms this by passing an artificially short timeout explicitly.
func TestWithImmediateTx_BusyTimeoutOption(t *testing.T) {
	ctx := context.Background()

	// holdLock pins a write lock on m for holdDuration, signals ready on the
	// returned channel when the lock is acquired, then releases it.
	holdLock := func(t *testing.T, m *Metadata, holdDuration time.Duration) <-chan struct{} {
		t.Helper()
		ready := make(chan struct{})
		go func() {
			conn, err := m.db.Conn(ctx)
			if err != nil {
				t.Errorf("holdLock Conn: %v", err)
				close(ready)
				return
			}
			defer conn.Close()
			if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
				t.Errorf("holdLock BEGIN IMMEDIATE: %v", err)
				close(ready)
				return
			}
			close(ready) // signal: lock is now held
			time.Sleep(holdDuration)
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}()
		return ready
	}

	t.Run("succeeds_with_long_timeout", func(t *testing.T) {
		m := newTestMetadata(t)
		if err := m.CreateBucket(ctx, "b", time.Now()); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}

		// Hold the write lock for 300 ms — longer than engineBusyTimeoutMS (100 ms)
		// but shorter than defaultBusyTimeoutMS (5000 ms).
		ready := holdLock(t, m, 300*time.Millisecond)
		<-ready // wait until the lock is actually held

		// withBusyTimeout(defaultBusyTimeoutMS) must wait past the 300 ms hold and succeed.
		err := m.withImmediateTx(ctx, func(conn *sql.Conn) error {
			return nil
		}, withBusyTimeout(defaultBusyTimeoutMS))
		if err != nil {
			t.Fatalf("withImmediateTx with 5000 ms timeout: want nil, got %v", err)
		}
	})

	t.Run("fails_fast_with_short_timeout", func(t *testing.T) {
		m := newTestMetadata(t)
		if err := m.CreateBucket(ctx, "b", time.Now()); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}

		// Hold the write lock for 300 ms.
		ready := holdLock(t, m, 300*time.Millisecond)
		<-ready

		// With a 50 ms timeout the BEGIN IMMEDIATE should fail with ErrBusy.
		var wg sync.WaitGroup
		wg.Add(1)
		var gotErr error
		go func() {
			defer wg.Done()
			gotErr = m.withImmediateTx(ctx, func(conn *sql.Conn) error {
				return nil
			}, withBusyTimeout(50))
		}()
		wg.Wait()
		if !errors.Is(gotErr, ErrBusy) {
			t.Fatalf("withImmediateTx with 50 ms timeout: want ErrBusy, got %v", gotErr)
		}
	})
}

// TestWithImmediateTx_RestoresBusyTimeout verifies that after withImmediateTx
// completes (regardless of which timeout was requested), the connection returned
// to the pool has busy_timeout reset to defaultBusyTimeoutMS (5000 ms) so that
// subsequent request-path writes get the full wait.
func TestWithImmediateTx_RestoresBusyTimeout(t *testing.T) {
	ctx := context.Background()

	// Use a Metadata with MaxOpenConns=1 so the same physical connection is
	// reused by both the withImmediateTx call and our follow-up PRAGMA read.
	m := newTestMetadata(t)
	m.db.SetMaxOpenConns(1)

	// Run withImmediateTx with a non-default (short) timeout.
	if err := m.withImmediateTx(ctx, func(conn *sql.Conn) error {
		return nil
	}, withBusyTimeout(42)); err != nil {
		t.Fatalf("withImmediateTx: %v", err)
	}

	// The single connection should now be back in the pool with its timeout
	// restored. Read busy_timeout from that same connection.
	conn, err := m.db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn after withImmediateTx: %v", err)
	}
	defer conn.Close()

	var gotTimeout int
	row := conn.QueryRowContext(ctx, "PRAGMA busy_timeout")
	if err := row.Scan(&gotTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout scan: %v", err)
	}

	if gotTimeout != defaultBusyTimeoutMS {
		t.Errorf("busy_timeout after withImmediateTx = %d, want %d (defaultBusyTimeoutMS)",
			gotTimeout, defaultBusyTimeoutMS)
	}
}
