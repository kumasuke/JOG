package lifecycle

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/storage"
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
