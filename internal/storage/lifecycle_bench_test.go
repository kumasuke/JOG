package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newBenchFileSystem(b *testing.B) *FileSystem {
	b.Helper()
	dir := b.TempDir()
	fs, err := NewFileSystem(dir, dir+"/metadata.db")
	if err != nil {
		b.Fatalf("NewFileSystem: %v", err)
	}
	b.Cleanup(func() { _ = fs.Close() })
	return fs
}

// seedNoncurrentVersions creates n keys, each with a noncurrent version (v1)
// and a current version (v2). Returns the keys and the noncurrent version IDs.
func seedNoncurrentVersions(b *testing.B, fs *FileSystem, n int) (keys, vids []string) {
	b.Helper()
	ctx := context.Background()
	keys = make([]string, n)
	vids = make([]string, n)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k-%08d", i)
		_, v1, err := fs.PutObjectVersioned(ctx, "b", k, strings.NewReader("x"), 1, "text/plain", nil)
		if err != nil {
			b.Fatalf("seed v1: %v", err)
		}
		if _, _, err := fs.PutObjectVersioned(ctx, "b", k, strings.NewReader("y"), 1, "text/plain", nil); err != nil {
			b.Fatalf("seed v2: %v", err)
		}
		keys[i] = k
		vids[i] = v1
	}
	return keys, vids
}

// BenchmarkExpireObjectVersionGuarded measures the throughput of the lifecycle
// engine's guarded, transactional noncurrent delete (lock guard + state guard +
// BEGIN IMMEDIATE tx + unlink).
func BenchmarkExpireObjectVersionGuarded(b *testing.B) {
	fs := newBenchFileSystem(b)
	ctx := context.Background()
	mustBucket(b, fs)
	keys, vids := seedNoncurrentVersions(b, fs, b.N)
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fs.ExpireObjectVersionGuarded(ctx, "b", keys[i], vids[i],
			ExpireGuards{RequireNoncurrent: true}, now); err != nil {
			b.Fatalf("ExpireObjectVersionGuarded: %v", err)
		}
	}
}

// BenchmarkDeleteObjectVersioned measures the existing (non-guarded,
// file-first) version delete used by the user request path, as a baseline for
// the guarded variant above.
func BenchmarkDeleteObjectVersioned(b *testing.B) {
	fs := newBenchFileSystem(b)
	ctx := context.Background()
	mustBucket(b, fs)
	keys, vids := seedNoncurrentVersions(b, fs, b.N)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := fs.DeleteObjectVersioned(ctx, "b", keys[i], vids[i], true); err != nil {
			b.Fatalf("DeleteObjectVersioned: %v", err)
		}
	}
}

// BenchmarkWithImmediateTxNoop isolates the fixed per-operation overhead of the
// guard primitive: pin a pooled connection, BEGIN IMMEDIATE, COMMIT.
func BenchmarkWithImmediateTxNoop(b *testing.B) {
	fs := newBenchFileSystem(b)
	ctx := context.Background()
	mustBucket(b, fs)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := fs.metadata.withImmediateTx(ctx, func(conn *sql.Conn) error { return nil }); err != nil {
			b.Fatalf("withImmediateTx: %v", err)
		}
	}
}

// BenchmarkPutObjectVersioned_NoContention is the request-path write baseline.
func BenchmarkPutObjectVersioned_NoContention(b *testing.B) {
	fs := newBenchFileSystem(b)
	ctx := context.Background()
	mustBucket(b, fs)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := fs.PutObjectVersioned(ctx, "b", fmt.Sprintf("p-%08d", i),
			strings.NewReader("payload"), 7, "text/plain", nil); err != nil {
			b.Fatalf("PutObjectVersioned: %v", err)
		}
	}
}

// BenchmarkPutObjectVersioned_EngineContention measures request-path write
// throughput while a background goroutine periodically takes a short BEGIN
// IMMEDIATE write lock, approximating the lifecycle engine's bounded lock
// occupancy (the real engine runs hourly and throttles every 100 actions).
//
// SQLite is a single writer: when the engine holds the write lock, a concurrent
// autocommit write returns SQLITE_BUSY *fast* (the driver does not wait out
// busy_timeout for a write-write conflict — see ErrBusy in withImmediateTx).
// A robust caller therefore retries; this benchmark does the same and reports
// `busy-retries/op` so the contention impact is visible as a number rather than
// hidden as a fatal error.
func BenchmarkPutObjectVersioned_EngineContention(b *testing.B) {
	fs := newBenchFileSystem(b)
	ctx := context.Background()
	mustBucket(b, fs)

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_ = fs.metadata.withImmediateTx(ctx, func(conn *sql.Conn) error {
				_, _ = conn.ExecContext(ctx,
					`INSERT OR REPLACE INTO lifecycle_runs (bucket, last_run_at, actions, skipped_locked, errors) VALUES ('b', ?, 0, 0, 0)`,
					time.Now().UTC())
				return nil
			})
			time.Sleep(500 * time.Microsecond) // bounded occupancy, like the engine throttle
		}
	}()

	var busyRetries int64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("q-%08d", i)
		for attempt := 0; ; attempt++ {
			_, _, err := fs.PutObjectVersioned(ctx, "b", key, strings.NewReader("payload"), 7, "text/plain", nil)
			if err == nil {
				break
			}
			if benchIsBusy(err) && attempt < 200 {
				busyRetries++
				time.Sleep(time.Millisecond)
				continue
			}
			b.Fatalf("PutObjectVersioned: %v", err)
		}
	}
	b.StopTimer()
	stop.Store(true)
	wg.Wait()
	b.ReportMetric(float64(busyRetries)/float64(b.N), "busy-retries/op")
}

// benchIsBusy detects an SQLITE_BUSY error robustly: the typed check plus a
// string fallback for the autocommit-write error shape, which the driver
// renders as "database is locked (5) (SQLITE_BUSY)".
func benchIsBusy(err error) bool {
	if err == nil {
		return false
	}
	if isSQLiteBusy(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

func mustBucket(b *testing.B, fs *FileSystem) {
	b.Helper()
	ctx := context.Background()
	if err := fs.CreateBucket(ctx, "b"); err != nil {
		b.Fatalf("CreateBucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, "b", VersioningStatusEnabled); err != nil {
		b.Fatalf("PutBucketVersioning: %v", err)
	}
}
