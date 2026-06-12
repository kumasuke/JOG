package lifecycle

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	_ "modernc.org/sqlite"
)

func newBenchEngine(b *testing.B, now time.Time) (storage.Storage, *Engine) {
	b.Helper()
	dir := b.TempDir()
	st, err := storage.NewFileSystem(dir, dir+"/metadata.db")
	if err != nil {
		b.Fatalf("NewFileSystem: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	eng := NewEngine(st, Config{ThrottleEvery: 1 << 30}, func() time.Time { return now })
	return st, eng
}

// BenchmarkRunOnce_ScanNoEligible measures the steady-state cost of a lifecycle
// cycle over a populated versioned bucket where nothing is eligible for
// expiry — i.e. the per-cycle scan + rule-evaluation overhead that runs even
// when there is no work to do (the common case once a bucket has been groomed).
//
// The bucket is seeded with `keys` keys (2 versions each). Reported ns/op is the
// cost of one full cycle over that bucket; divide by `keys` for per-key cost.
func BenchmarkRunOnce_ScanNoEligible(b *testing.B) {
	const keys = 2000
	now := time.Now()
	st, eng := newBenchEngine(b, now)
	ctx := context.Background()
	if err := st.CreateBucket(ctx, "b"); err != nil {
		b.Fatal(err)
	}
	if err := st.PutBucketVersioning(ctx, "b", storage.VersioningStatusEnabled); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < keys; i++ {
		k := fmt.Sprintf("k-%08d", i)
		mustPut(b, st, k)
		mustPut(b, st, k)
	}
	// Rule matches the prefix but Days is far in the future, so no object is ever
	// eligible — the engine scans every key and version but deletes nothing.
	days := int32(36500)
	if err := st.PutBucketLifecycleConfiguration(ctx, "b", &storage.LifecycleConfiguration{
		Rules: []storage.LifecycleRule{{
			ID: "noop", Status: "Enabled",
			Expiration: &storage.LifecycleExpiration{Days: &days},
		}},
	}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		report, err := eng.RunOnce(ctx)
		if err != nil {
			b.Fatalf("RunOnce: %v", err)
		}
		if report.Buckets["b"].Actions != 0 {
			b.Fatalf("expected no actions, got %d", report.Buckets["b"].Actions)
		}
	}
	b.ReportMetric(float64(keys), "keys/cycle")
}

func mustPut(b *testing.B, st storage.Storage, key string) {
	b.Helper()
	if _, _, err := st.PutObjectVersioned(context.Background(), "b", key,
		bytes.NewReader([]byte("x")), 1, "text/plain", nil); err != nil {
		b.Fatalf("PutObjectVersioned: %v", err)
	}
}

// BenchmarkRunOnce_ScanScale measures how the per-cycle scan cost scales with
// the number of objects in a groomed bucket (nothing eligible). Rows are
// bulk-inserted directly into object_versions (2 versions/key, no data files)
// so seeding a million keys is fast; the no-eligible scan path never touches
// files. Run with e.g. `-benchtime=1x` to do a single cycle per size:
//
//	go test ./internal/lifecycle/ -run '^$' -bench RunOnce_ScanScale -benchtime=1x
func BenchmarkRunOnce_ScanScale(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{100_000, 1_000_000} {
		b.Run(fmt.Sprintf("keys_%d", n), func(b *testing.B) {
			now := time.Now()
			dir := b.TempDir()
			dbPath := dir + "/metadata.db"
			st, err := storage.NewFileSystem(dir, dbPath)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = st.Close() })
			if err := st.CreateBucket(ctx, "b"); err != nil {
				b.Fatal(err)
			}
			if err := st.PutBucketVersioning(ctx, "b", storage.VersioningStatusEnabled); err != nil {
				b.Fatal(err)
			}
			seedVersionRows(b, dbPath, n)
			days := int32(36500) // never eligible: pure scan
			if err := st.PutBucketLifecycleConfiguration(ctx, "b", &storage.LifecycleConfiguration{
				Rules: []storage.LifecycleRule{{ID: "noop", Status: "Enabled", Expiration: &storage.LifecycleExpiration{Days: &days}}},
			}); err != nil {
				b.Fatal(err)
			}
			eng := NewEngine(st, Config{ThrottleEvery: 1 << 30}, func() time.Time { return now })

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rep, err := eng.RunOnce(ctx)
				if err != nil {
					b.Fatalf("RunOnce: %v", err)
				}
				if rep.Buckets["b"].Actions != 0 {
					b.Fatalf("expected no actions, got %d", rep.Buckets["b"].Actions)
				}
			}
			b.ReportMetric(float64(n), "keys")
		})
	}
}

// seedVersionRows bulk-inserts n keys with 2 versions each straight into
// object_versions over a dedicated connection (WAL allows it while the
// FileSystem connection is idle).
func seedVersionRows(b *testing.B, dbPath string, n int) {
	b.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	base := time.Now().Add(-1000 * time.Hour).UTC()
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO object_versions
		(bucket, key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker)
		VALUES ('b', ?, ?, 1, ?, 'e', 'text/plain', '', 0)`)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k-%08d", i)
		if _, err := stmt.Exec(key, fmt.Sprintf("%s-a", key), base); err != nil {
			b.Fatal(err)
		}
		if _, err := stmt.Exec(key, fmt.Sprintf("%s-b", key), base.Add(time.Second)); err != nil {
			b.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}
