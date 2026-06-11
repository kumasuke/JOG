package storage

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestGetLatestObjectVersion_TiebreakByVersionID is the §0 prerequisite for the
// lifecycle engine: when several versions share the same last_modified, the
// latest must be decided deterministically by version_id DESC. Without the
// tiebreak the latest pointer is non-deterministic and the engine could
// misclassify the real current version as noncurrent and over-delete it.
func TestGetLatestObjectVersion_TiebreakByVersionID(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)

	if err := m.CreateBucket(ctx, "b", time.Now()); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// Insert in an order that does NOT match version_id sorting, all sharing
	// the same last_modified.
	for _, vid := range []string{"v-bbb", "v-zzz", "v-aaa", "v-mmm"} {
		v := &ObjectVersion{
			Key:          "k",
			VersionID:    vid,
			Size:         1,
			LastModified: ts,
			ETag:         "e",
			ContentType:  "text/plain",
		}
		if err := m.PutObjectVersion(ctx, "b", v); err != nil {
			t.Fatalf("PutObjectVersion(%q): %v", vid, err)
		}
	}

	// Run repeatedly: the result must be stable and equal to the max version_id.
	for i := 0; i < 20; i++ {
		latest, err := m.GetLatestObjectVersion(ctx, "b", "k")
		if err != nil {
			t.Fatalf("GetLatestObjectVersion: %v", err)
		}
		if latest == nil {
			t.Fatal("GetLatestObjectVersion returned nil")
		}
		if latest.VersionID != "v-zzz" {
			t.Fatalf("iteration %d: latest = %q, want %q (version_id DESC tiebreak)", i, latest.VersionID, "v-zzz")
		}
	}
}

// TestWithImmediateTx_CommitAndRollback verifies the new primitive runs fn in a
// real transaction: a committed write persists, a returned error rolls back.
func TestWithImmediateTx_CommitAndRollback(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	if err := m.CreateBucket(ctx, "b", time.Now()); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// Commit path: insert a legal hold row inside the tx.
	err := m.withImmediateTx(ctx, func(conn *sql.Conn) error {
		_, e := conn.ExecContext(ctx,
			`INSERT INTO object_legal_hold (bucket, key, version_id, status) VALUES (?, ?, ?, 'ON')`,
			"b", "k", "v1")
		return e
	})
	if err != nil {
		t.Fatalf("withImmediateTx commit: %v", err)
	}
	status, err := m.GetObjectLegalHold(ctx, "b", "k", "v1")
	if err != nil {
		t.Fatalf("GetObjectLegalHold: %v", err)
	}
	if status != "ON" {
		t.Fatalf("after commit status = %q, want ON", status)
	}

	// Rollback path: fn returns an error after an insert; the insert must not persist.
	sentinel := errors.New("boom")
	err = m.withImmediateTx(ctx, func(conn *sql.Conn) error {
		if _, e := conn.ExecContext(ctx,
			`INSERT INTO object_legal_hold (bucket, key, version_id, status) VALUES (?, ?, ?, 'ON')`,
			"b", "k", "v2"); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("withImmediateTx rollback err = %v, want sentinel", err)
	}
	status, err = m.GetObjectLegalHold(ctx, "b", "k", "v2")
	if err != nil {
		t.Fatalf("GetObjectLegalHold v2: %v", err)
	}
	if status != "" {
		t.Fatalf("after rollback status = %q, want empty (no row)", status)
	}
}

// TestWithImmediateTx_BusyReturnsErrBusy verifies that when another connection
// already holds the write lock, BEGIN IMMEDIATE fails with ErrBusy (after the
// busy_timeout) rather than silently proceeding without the guard. The engine
// treats ErrBusy as a fail-closed skip.
func TestWithImmediateTx_BusyReturnsErrBusy(t *testing.T) {
	ctx := context.Background()
	m := newTestMetadata(t)
	if err := m.CreateBucket(ctx, "b", time.Now()); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// Hold a write lock on a dedicated connection for longer than busy_timeout.
	hold, err := m.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer hold.Close()
	if _, err := hold.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold BEGIN IMMEDIATE: %v", err)
	}
	// Make it a genuine write lock holder.
	if _, err := hold.ExecContext(ctx,
		`INSERT INTO object_legal_hold (bucket, key, version_id, status) VALUES ('b','k','held','ON')`); err != nil {
		t.Fatalf("hold insert: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var gotErr error
	go func() {
		defer wg.Done()
		gotErr = m.withImmediateTx(ctx, func(conn *sql.Conn) error {
			_, e := conn.ExecContext(ctx,
				`INSERT INTO object_legal_hold (bucket, key, version_id, status) VALUES ('b','k','blocked','ON')`)
			return e
		})
	}()
	wg.Wait()

	// Release the holder.
	_, _ = hold.ExecContext(ctx, "ROLLBACK")

	if !errors.Is(gotErr, ErrBusy) {
		t.Fatalf("withImmediateTx under contention err = %v, want ErrBusy", gotErr)
	}
}
