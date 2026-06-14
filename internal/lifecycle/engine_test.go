package lifecycle

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/storage"
)

// newEngineFixture builds a real FileSystem-backed engine with an injected
// clock. The engine's "now" is set far in the future so day-based rules fire.
func newEngineFixture(t *testing.T, dryRun bool, now time.Time) (storage.Storage, *Engine) {
	t.Helper()
	dir := t.TempDir()
	st, err := storage.NewFileSystem(dir, dir+"/metadata.db")
	if err != nil {
		t.Fatalf("NewFileSystem: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := NewEngine(st, Config{
		DryRun:        dryRun,
		ThrottleEvery: 1 << 30, // effectively no throttle in tests
		GCGracePeriod: time.Hour,
	}, func() time.Time { return now })
	return st, eng
}

func enabledBucket(t *testing.T, st storage.Storage, bucket string) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := st.PutBucketVersioning(ctx, bucket, storage.VersioningStatusEnabled); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
}

func putV(t *testing.T, st storage.Storage, bucket, key, content string) string {
	t.Helper()
	_, vid, err := st.PutObjectVersioned(context.Background(), bucket, key,
		bytes.NewReader([]byte(content)), int64(len(content)), "text/plain", nil)
	if err != nil {
		t.Fatalf("PutObjectVersioned: %v", err)
	}
	return vid
}

func setLifecycle(t *testing.T, st storage.Storage, bucket string, rules ...storage.LifecycleRule) {
	t.Helper()
	if err := st.PutBucketLifecycleConfiguration(context.Background(), bucket,
		&storage.LifecycleConfiguration{Rules: rules}); err != nil {
		t.Fatalf("PutBucketLifecycleConfiguration: %v", err)
	}
}

func TestEngine_ExpirationCreatesDeleteMarker(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	enabledBucket(t, st, "b")
	v1 := putV(t, st, "b", "k", "data")
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "exp", Status: "Enabled",
		Expiration: &storage.LifecycleExpiration{Days: i32(1)},
	})

	report, err := eng.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := report.Buckets["b"].Actions; got != 1 {
		t.Fatalf("actions = %d, want 1 (DM created)", got)
	}
	// Current pointer is gone (a DM now hides it).
	if obj, _ := st.HeadObject(ctx, "b", "k"); obj != nil {
		t.Error("current object still visible after expiration DM")
	}
	// The underlying version is still retrievable by versionId (data preserved).
	if _, err := st.GetObjectVersioned(ctx, "b", "k", v1); err != nil {
		t.Errorf("GetObjectVersioned(v1) = %v, want retrievable", err)
	}
	// Exactly one delete marker exists.
	vs, _ := st.GetObjectVersionsForKey(ctx, "b", "k")
	dm := 0
	for _, v := range vs {
		if v.IsDeleteMarker {
			dm++
		}
	}
	if dm != 1 {
		t.Fatalf("delete markers = %d, want 1", dm)
	}
}

func TestEngine_NoncurrentVersionExpiration(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	enabledBucket(t, st, "b")
	v1 := putV(t, st, "b", "k", "1")
	v2 := putV(t, st, "b", "k", "2")
	v3 := putV(t, st, "b", "k", "3") // current
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "ncve", Status: "Enabled",
		NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{
			NoncurrentDays:          i32(1),
			NewerNoncurrentVersions: i32(1), // keep newest 1 noncurrent (v2), delete v1
		},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	present := func(vid string) bool {
		_, err := st.GetObjectVersioned(ctx, "b", "k", vid)
		return err == nil
	}
	if present(v1) {
		t.Error("v1 (oldest noncurrent) should have been expired")
	}
	if !present(v2) {
		t.Error("v2 (protected by NewerNoncurrentVersions) should survive")
	}
	if !present(v3) {
		t.Error("v3 (current) must never be expired by NCVE")
	}
}

// TestEngine_NoncurrentExpirationRespectsTagFilter verifies the fix for the
// codex P1: a tag-filtered NCVE rule must not expire noncurrent versions that
// do not carry the tag (no over-deletion of out-of-scope versions).
func TestEngine_NoncurrentExpirationRespectsTagFilter(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	enabledBucket(t, st, "b")
	v1 := putV(t, st, "b", "k", "1") // will be noncurrent, NOT tagged
	putV(t, st, "b", "k", "2")       // current
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID:     "ncve-tagged",
		Status: "Enabled",
		Filter: &storage.LifecycleRuleFilter{Tag: &storage.Tag{Key: "expire", Value: "yes"}},
		NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{
			NoncurrentDays: i32(1),
		},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetObjectVersioned(ctx, "b", "k", v1); err != nil {
		t.Fatal("untagged noncurrent version must survive a tag-filtered NCVE rule (over-deletion)")
	}

	// Now tag v1 with the matching tag; it should become eligible.
	if err := st.PutObjectTagging(ctx, "b", "k", v1, []storage.Tag{{Key: "expire", Value: "yes"}}); err != nil {
		t.Fatalf("PutObjectTagging: %v", err)
	}
	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetObjectVersioned(ctx, "b", "k", v1); err == nil {
		t.Error("tagged noncurrent version should be expired by the matching NCVE rule")
	}
}

// TestEngine_ExpirationRespectsTagFilter exercises the needTags=true scan path:
// an Expiration rule with a tag filter must only act on objects carrying the
// tag (the tag-fetch optimization must not break correctness).
func TestEngine_ExpirationRespectsTagFilter(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	enabledBucket(t, st, "b")

	tagged := putV(t, st, "b", "tagged", "x")
	if err := st.PutObjectTagging(ctx, "b", "tagged", tagged, []storage.Tag{{Key: "expire", Value: "yes"}}); err != nil {
		t.Fatalf("PutObjectTagging: %v", err)
	}
	putV(t, st, "b", "untagged", "y")

	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID:         "exp-tagged",
		Status:     "Enabled",
		Filter:     &storage.LifecycleRuleFilter{Tag: &storage.Tag{Key: "expire", Value: "yes"}},
		Expiration: &storage.LifecycleExpiration{Days: i32(1)},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// Tagged object: hidden by a delete marker.
	if obj, _ := st.HeadObject(ctx, "b", "tagged"); obj != nil {
		t.Error("tagged object should have received an expiration delete marker")
	}
	// Untagged object: untouched.
	if obj, _ := st.HeadObject(ctx, "b", "untagged"); obj == nil {
		t.Error("untagged object must not be expired by a tag-filtered rule")
	}
}

func TestEngine_DryRunHasNoSideEffects(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, true, now)
	enabledBucket(t, st, "b")
	v1 := putV(t, st, "b", "k", "data")
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "exp", Status: "Enabled",
		Expiration: &storage.LifecycleExpiration{Days: i32(1)},
	})

	report, err := eng.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(report.Buckets["b"].Plan) == 0 {
		t.Error("dry-run plan should list planned actions")
	}
	if report.Buckets["b"].Actions != 1 {
		t.Errorf("planned actions = %d, want 1", report.Buckets["b"].Actions)
	}
	// Nothing actually changed.
	if obj, _ := st.HeadObject(ctx, "b", "k"); obj == nil {
		t.Error("dry-run must not remove the current object")
	}
	if _, err := st.GetObjectVersioned(ctx, "b", "k", v1); err != nil {
		t.Error("dry-run must not touch the version")
	}
	vs, _ := st.GetObjectVersionsForKey(ctx, "b", "k")
	if len(vs) != 1 {
		t.Errorf("dry-run created versions: %d, want 1", len(vs))
	}
}

func TestEngine_SuspendedSkipsExpiration(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	if err := st.CreateBucket(ctx, "b"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := st.PutBucketVersioning(ctx, "b", storage.VersioningStatusEnabled); err != nil {
		t.Fatalf("enable versioning: %v", err)
	}
	v1 := putV(t, st, "b", "k", "data")
	if err := st.PutBucketVersioning(ctx, "b", storage.VersioningStatusSuspended); err != nil {
		t.Fatalf("suspend versioning: %v", err)
	}
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "exp", Status: "Enabled",
		Expiration: &storage.LifecycleExpiration{Days: i32(1)},
	})

	report, err := eng.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Buckets["b"].Actions != 0 {
		t.Errorf("suspended Expiration should be skipped, actions = %d", report.Buckets["b"].Actions)
	}
	if _, err := st.GetObjectVersioned(ctx, "b", "k", v1); err != nil {
		t.Error("version wrongly removed under suspended bucket")
	}
}

func TestEngine_ExpiredObjectDeleteMarker(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	enabledBucket(t, st, "b")
	v1 := putV(t, st, "b", "k", "data")
	// Create a delete marker (unspecified delete), then permanently delete v1
	// so the key is left with only the delete marker.
	if _, _, err := st.DeleteObjectVersioned(ctx, "b", "k", "", false); err != nil {
		t.Fatalf("create DM: %v", err)
	}
	if _, _, err := st.DeleteObjectVersioned(ctx, "b", "k", v1, true); err != nil {
		t.Fatalf("delete v1: %v", err)
	}
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "eodm", Status: "Enabled",
		Expiration: &storage.LifecycleExpiration{ExpiredObjectDeleteMarker: bp(true)},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	vs, _ := st.GetObjectVersionsForKey(ctx, "b", "k")
	if len(vs) != 0 {
		t.Fatalf("EODM should have removed all delete markers, remaining = %d", len(vs))
	}
}

func TestEngine_AbortIncompleteMultipartUpload(t *testing.T) {
	ctx := context.Background()
	// 5 days after creation so DaysAfterInitiation=1 fires but =10 does not.
	now := time.Now().Add(5 * 24 * time.Hour)

	t.Run("aborts-old-upload", func(t *testing.T) {
		st, eng := newEngineFixture(t, false, now)
		if err := st.CreateBucket(ctx, "b"); err != nil {
			t.Fatal(err)
		}
		up, err := st.CreateMultipartUpload(ctx, "b", "k", "text/plain", nil, "", nil, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		setLifecycle(t, st, "b", storage.LifecycleRule{
			ID: "aimu", Status: "Enabled",
			AbortIncompleteMultipartUpload: &storage.AbortIncompleteMultipartUpload{DaysAfterInitiation: i32(1)},
		})
		if _, err := eng.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		got, _ := st.GetMultipartUpload(ctx, up.UploadID)
		if got != nil {
			t.Error("old upload should have been aborted")
		}
	})

	t.Run("keeps-recent-upload", func(t *testing.T) {
		st, eng := newEngineFixture(t, false, now)
		if err := st.CreateBucket(ctx, "b"); err != nil {
			t.Fatal(err)
		}
		up, err := st.CreateMultipartUpload(ctx, "b", "k", "text/plain", nil, "", nil, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		setLifecycle(t, st, "b", storage.LifecycleRule{
			ID: "aimu", Status: "Enabled",
			AbortIncompleteMultipartUpload: &storage.AbortIncompleteMultipartUpload{DaysAfterInitiation: i32(10)},
		})
		if _, err := eng.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		got, _ := st.GetMultipartUpload(ctx, up.UploadID)
		if got == nil {
			t.Error("recent upload (within DaysAfterInitiation) should be kept")
		}
	})
}

// TestEngine_NoncurrentVersionExpiration_OverlappingRules verifies the #53 fix:
// when two NCVE rules overlap a key the engine unions their per-rule verdicts (a
// version is expired when ANY rule expires it), matching S3's independent-rule /
// shortest-expiration semantics.
//
// This case is constructed to FAIL under a naive field-merge (taking the minimum
// keep and minimum days across rules as one synthetic rule), which is the
// over-delete trap the implementation must avoid:
//
//	noncurrent versions: v1 (oldest), v2, v3   (v4 = current)
//	Rule A (broad, prefix=""):    keep=2, days=1   → protects v3,v2; expires {v1}
//	Rule B (narrow, prefix=logs/): keep=0, days=365 → 365d not elapsed → expires {}
//	correct union:        {v1}
//	field-merge(min keep=0, min days=1): would expire {v1,v2,v3}  ← WRONG
//
// So the assertions (v1 gone, v2/v3/v4 kept) are green only for the union and red
// for both the old first-match logic AND a field-merge.
func TestEngine_NoncurrentVersionExpiration_OverlappingRules(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	enabledBucket(t, st, "b")
	v1 := putV(t, st, "b", "logs/k", "1") // oldest noncurrent
	v2 := putV(t, st, "b", "logs/k", "2") // noncurrent
	v3 := putV(t, st, "b", "logs/k", "3") // noncurrent
	v4 := putV(t, st, "b", "logs/k", "4") // current
	setLifecycle(t, st, "b",
		// Broad rule: keep newest 2 noncurrent (v3,v2), expire the rest (v1).
		storage.LifecycleRule{
			ID: "keep-2-all", Status: "Enabled",
			NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{
				NoncurrentDays: i32(1), NewerNoncurrentVersions: i32(2),
			},
		},
		// Narrow rule: keep none but require 365 days — not elapsed, so it expires
		// nothing here. A field-merge would borrow its keep=0 and the broad rule's
		// days=1 and wrongly expire v2 and v3.
		storage.LifecycleRule{
			ID: "keep-0-logs-365d", Status: "Enabled",
			Filter:                      &storage.LifecycleRuleFilter{Prefix: "logs/"},
			NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{NoncurrentDays: i32(365)},
		},
	)

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	present := func(vid string) bool {
		_, err := st.GetObjectVersioned(ctx, "b", "logs/k", vid)
		return err == nil
	}
	if present(v1) {
		t.Error("v1 (beyond keep=2 of the broad rule) should be expired")
	}
	if !present(v2) {
		t.Error("v2 should survive: the broad rule protects it (keep=2) and the narrow rule's 365d has not elapsed; a field-merge would wrongly delete it")
	}
	if !present(v3) {
		t.Error("v3 should survive: same as v2; a field-merge would wrongly delete it")
	}
	if !present(v4) {
		t.Error("v4 (current) must never be expired by NCVE")
	}
}

// TestEngine_AbortIncompleteMultipartUpload_OverlappingRules verifies the #53
// fix for AIMU: each upload is evaluated against every AIMU rule and aborted
// when any rule's prefix matches and its DaysAfterInitiation has elapsed. The
// old first-match logic listed uploads under one rule's prefix only, so a
// second rule with a different prefix was silently ignored.
func TestEngine_AbortIncompleteMultipartUpload_OverlappingRules(t *testing.T) {
	ctx := context.Background()
	// 5 days after creation: a 3-day rule fires, a 30-day rule does not.
	now := time.Now().Add(5 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	if err := st.CreateBucket(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	logsUp, err := st.CreateMultipartUpload(ctx, "b", "logs/a", "text/plain", nil, "", nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	dataUp, err := st.CreateMultipartUpload(ctx, "b", "data/a", "text/plain", nil, "", nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	setLifecycle(t, st, "b",
		// Broad rule (all keys) at 30 days: does not fire at +5d.
		storage.LifecycleRule{
			ID: "all-30", Status: "Enabled",
			AbortIncompleteMultipartUpload: &storage.AbortIncompleteMultipartUpload{DaysAfterInitiation: i32(30)},
		},
		// Narrow rule (logs/) at 3 days: fires at +5d for the logs/ upload only.
		storage.LifecycleRule{
			ID: "logs-3", Status: "Enabled",
			Filter:                         &storage.LifecycleRuleFilter{Prefix: "logs/"},
			AbortIncompleteMultipartUpload: &storage.AbortIncompleteMultipartUpload{DaysAfterInitiation: i32(3)},
		},
	)

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMultipartUpload(ctx, logsUp.UploadID); got != nil {
		t.Error("logs/ upload should be aborted by the 3-day rule (overlapping evaluation)")
	}
	if got, _ := st.GetMultipartUpload(ctx, dataUp.UploadID); got == nil {
		t.Error("data/ upload should survive: only the 30-day rule applies and it has not elapsed")
	}
}

func TestEngine_Idempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	enabledBucket(t, st, "b")
	putV(t, st, "b", "k", "1")
	putV(t, st, "b", "k", "2")
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "ncve", Status: "Enabled",
		NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{NoncurrentDays: i32(1)},
	})

	r1, err := eng.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Buckets["b"].Actions == 0 {
		t.Fatal("first run should expire the noncurrent version")
	}
	r2, err := eng.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Buckets["b"].Actions != 0 {
		t.Errorf("second run should be a no-op, actions = %d", r2.Buckets["b"].Actions)
	}
}
