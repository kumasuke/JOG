package s3compat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/kumasuke/jog/internal/lifecycle"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/require"
)

// runCycle builds a lifecycle engine over the test server's storage with an
// injected clock and executes a single cycle. now is normally set far in the
// future so day-based rules become eligible.
func runCycle(t *testing.T, ts *testutil.TestServer, now time.Time, dryRun bool) lifecycle.Report {
	t.Helper()
	eng := lifecycle.NewEngine(ts.Storage(), lifecycle.Config{DryRun: dryRun}, func() time.Time { return now })
	report, err := eng.RunOnce(context.Background())
	require.NoError(t, err)
	return report
}

func putObject(t *testing.T, client *s3.Client, bucket, key, body string) string {
	t.Helper()
	resp, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(body),
	})
	require.NoError(t, err)
	if resp.VersionId != nil {
		return *resp.VersionId
	}
	return ""
}

func versionRetrievable(t *testing.T, client *s3.Client, bucket, key, versionID string) bool {
	t.Helper()
	_, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket:    aws.String(bucket),
		Key:       aws.String(key),
		VersionId: aws.String(versionID),
	})
	return err == nil
}

func objectExists(t *testing.T, client *s3.Client, bucket, key string) bool {
	t.Helper()
	_, err := client.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	return err == nil
}

func setLifecycleRules(t *testing.T, client *s3.Client, bucket string, rules []types.LifecycleRule) {
	t.Helper()
	_, err := client.PutBucketLifecycleConfiguration(context.Background(), &s3.PutBucketLifecycleConfigurationInput{
		Bucket:                 aws.String(bucket),
		LifecycleConfiguration: &types.BucketLifecycleConfiguration{Rules: rules},
	})
	require.NoError(t, err)
}

func futureNow() time.Time { return time.Now().Add(100 * 24 * time.Hour) }

// TestLifecycleEngine_ExpirationCreatesDeleteMarker: a versioned bucket gets an
// expiration delete marker; the underlying version survives.
func TestLifecycleEngine_ExpirationCreatesDeleteMarker(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucket := testutil.RandomBucketName()
	ts.CreateTestBucket(t, bucket)
	enableVersioning(t, client, bucket)
	v1 := putObject(t, client, bucket, "logs/a", "data")
	setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
		ID:         aws.String("exp"),
		Status:     types.ExpirationStatusEnabled,
		Filter:     &types.LifecycleRuleFilter{Prefix: aws.String("logs/")},
		Expiration: &types.LifecycleExpiration{Days: aws.Int32(1)},
	}})

	runCycle(t, ts, futureNow(), false)

	// HEAD on the key now returns 404 (a delete marker hides it).
	if objectExists(t, client, bucket, "logs/a") {
		t.Error("object should be hidden by the expiration delete marker")
	}
	// A delete marker appears in ListObjectVersions and the real version remains.
	versions, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	if len(versions.DeleteMarkers) != 1 {
		t.Fatalf("delete markers = %d, want 1", len(versions.DeleteMarkers))
	}
	if len(versions.Versions) != 1 {
		t.Fatalf("real versions = %d, want 1 (data preserved)", len(versions.Versions))
	}
	if !versionRetrievable(t, client, bucket, "logs/a", v1) {
		t.Error("underlying version must remain retrievable by versionId")
	}
}

// TestLifecycleEngine_NonVersionedPhysicalDelete: a non-versioned bucket has the
// current object physically removed.
func TestLifecycleEngine_NonVersionedPhysicalDelete(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := testutil.RandomBucketName()
	ts.CreateTestBucket(t, bucket)
	putObject(t, client, bucket, "logs/a", "data")
	setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
		ID:         aws.String("exp"),
		Status:     types.ExpirationStatusEnabled,
		Filter:     &types.LifecycleRuleFilter{Prefix: aws.String("logs/")},
		Expiration: &types.LifecycleExpiration{Days: aws.Int32(1)},
	}})

	runCycle(t, ts, futureNow(), false)

	if objectExists(t, client, bucket, "logs/a") {
		t.Error("non-versioned object should have been physically deleted")
	}
}

// TestLifecycleEngine_NoncurrentVersionExpiration: old noncurrent versions are
// removed; NewerNoncurrentVersions and the current version are preserved.
func TestLifecycleEngine_NoncurrentVersionExpiration(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := testutil.RandomBucketName()
	ts.CreateTestBucket(t, bucket)
	enableVersioning(t, client, bucket)
	v1 := putObject(t, client, bucket, "k", "1")
	v2 := putObject(t, client, bucket, "k", "2")
	v3 := putObject(t, client, bucket, "k", "3") // current
	setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
		ID:     aws.String("ncve"),
		Status: types.ExpirationStatusEnabled,
		Filter: &types.LifecycleRuleFilter{},
		NoncurrentVersionExpiration: &types.NoncurrentVersionExpiration{
			NoncurrentDays:          aws.Int32(1),
			NewerNoncurrentVersions: aws.Int32(1),
		},
	}})

	runCycle(t, ts, futureNow(), false)

	if versionRetrievable(t, client, bucket, "k", v1) {
		t.Error("oldest noncurrent version v1 should have been expired")
	}
	if !versionRetrievable(t, client, bucket, "k", v2) {
		t.Error("v2 (NewerNoncurrentVersions=1) should be preserved")
	}
	if !versionRetrievable(t, client, bucket, "k", v3) {
		t.Error("current version v3 must never be expired by NCVE")
	}
}

// TestLifecycleEngine_ExpiredObjectDeleteMarker: a key left with only a delete
// marker is cleaned up.
func TestLifecycleEngine_ExpiredObjectDeleteMarker(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucket := testutil.RandomBucketName()
	ts.CreateTestBucket(t, bucket)
	enableVersioning(t, client, bucket)
	v1 := putObject(t, client, bucket, "k", "data")
	// Unspecified delete creates a delete marker.
	_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("k")})
	require.NoError(t, err)
	// Permanently delete the real version, leaving only the delete marker.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("k"), VersionId: aws.String(v1)})
	require.NoError(t, err)
	setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
		ID:         aws.String("eodm"),
		Status:     types.ExpirationStatusEnabled,
		Filter:     &types.LifecycleRuleFilter{},
		Expiration: &types.LifecycleExpiration{ExpiredObjectDeleteMarker: aws.Bool(true)},
	}})

	runCycle(t, ts, futureNow(), false)

	versions, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	if len(versions.DeleteMarkers) != 0 || len(versions.Versions) != 0 {
		t.Fatalf("EODM should have removed the orphan delete marker; markers=%d versions=%d",
			len(versions.DeleteMarkers), len(versions.Versions))
	}
}

// TestLifecycleEngine_AbortIncompleteMultipartUpload: old uploads are aborted.
func TestLifecycleEngine_AbortIncompleteMultipartUpload(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucket := testutil.RandomBucketName()
	ts.CreateTestBucket(t, bucket)
	create, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String("big")})
	require.NoError(t, err)
	setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
		ID:                             aws.String("aimu"),
		Status:                         types.ExpirationStatusEnabled,
		Filter:                         &types.LifecycleRuleFilter{},
		AbortIncompleteMultipartUpload: &types.AbortIncompleteMultipartUpload{DaysAfterInitiation: aws.Int32(1)},
	}})

	// 5 days after creation: DaysAfterInitiation=1 is elapsed.
	runCycle(t, ts, time.Now().Add(5*24*time.Hour), false)

	uploads, err := client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	for _, u := range uploads.Uploads {
		if u.UploadId != nil && *u.UploadId == *create.UploadId {
			t.Fatal("incomplete upload past DaysAfterInitiation should have been aborted")
		}
	}
}

// TestLifecycleEngine_ObjectLockRegression is the direct E2E check of the
// overriding constraint: locked versions survive a cycle, and an expiration
// delete marker placed over a locked current version still leaves that version
// retrievable by versionId.
func TestLifecycleEngine_ObjectLockRegression(t *testing.T) {
	ctx := context.Background()
	now := futureNow()
	retainUntil := now.Add(24 * time.Hour) // future relative to the engine clock

	t.Run("compliance-noncurrent-survives", func(t *testing.T) {
		ts := testutil.NewTestServer(t)
		defer ts.Cleanup()
		client := ts.S3Client(t)

		bucket := testutil.RandomBucketName()
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket:                     aws.String(bucket),
			ObjectLockEnabledForBucket: aws.Bool(true),
		})
		require.NoError(t, err)

		v1 := putObject(t, client, bucket, "k", "v1")
		_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
			Bucket:    aws.String(bucket),
			Key:       aws.String("k"),
			VersionId: aws.String(v1),
			Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, RetainUntilDate: aws.Time(retainUntil)},
		})
		require.NoError(t, err)
		putObject(t, client, bucket, "k", "v2") // v1 becomes noncurrent

		setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
			ID:                          aws.String("ncve"),
			Status:                      types.ExpirationStatusEnabled,
			Filter:                      &types.LifecycleRuleFilter{},
			NoncurrentVersionExpiration: &types.NoncurrentVersionExpiration{NoncurrentDays: aws.Int32(1)},
		}})

		runCycle(t, ts, now, false)

		if !versionRetrievable(t, client, bucket, "k", v1) {
			t.Error("COMPLIANCE-locked noncurrent version was deleted (I1 violation)")
		}
	})

	t.Run("legal-hold-noncurrent-survives", func(t *testing.T) {
		ts := testutil.NewTestServer(t)
		defer ts.Cleanup()
		client := ts.S3Client(t)

		bucket := testutil.RandomBucketName()
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket:                     aws.String(bucket),
			ObjectLockEnabledForBucket: aws.Bool(true),
		})
		require.NoError(t, err)

		v1 := putObject(t, client, bucket, "k", "v1")
		_, err = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
			Bucket:    aws.String(bucket),
			Key:       aws.String("k"),
			VersionId: aws.String(v1),
			LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn},
		})
		require.NoError(t, err)
		putObject(t, client, bucket, "k", "v2")

		setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
			ID:                          aws.String("ncve"),
			Status:                      types.ExpirationStatusEnabled,
			Filter:                      &types.LifecycleRuleFilter{},
			NoncurrentVersionExpiration: &types.NoncurrentVersionExpiration{NoncurrentDays: aws.Int32(1)},
		}})

		runCycle(t, ts, now, false)

		if !versionRetrievable(t, client, bucket, "k", v1) {
			t.Error("legal-hold version was deleted (I1 violation)")
		}
	})

	t.Run("locked-current-gets-DM-but-version-retrievable", func(t *testing.T) {
		ts := testutil.NewTestServer(t)
		defer ts.Cleanup()
		client := ts.S3Client(t)

		bucket := testutil.RandomBucketName()
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket:                     aws.String(bucket),
			ObjectLockEnabledForBucket: aws.Bool(true),
		})
		require.NoError(t, err)

		v1 := putObject(t, client, bucket, "k", "v1")
		_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
			Bucket:    aws.String(bucket),
			Key:       aws.String("k"),
			VersionId: aws.String(v1),
			Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, RetainUntilDate: aws.Time(retainUntil)},
		})
		require.NoError(t, err)

		setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
			ID:         aws.String("exp"),
			Status:     types.ExpirationStatusEnabled,
			Filter:     &types.LifecycleRuleFilter{},
			Expiration: &types.LifecycleExpiration{Days: aws.Int32(1)},
		}})

		report := runCycle(t, ts, now, false)

		// Per design §1-D: the DM is created even over a locked current version
		// (data is preserved), and the version remains retrievable by versionId.
		if !versionRetrievable(t, client, bucket, "k", v1) {
			t.Error("locked current version's data must remain retrievable after DM")
		}
		if objectExists(t, client, bucket, "k") {
			t.Error("a delete marker should now hide the current object")
		}
		if report.Buckets[bucket] == nil || report.Buckets[bucket].LockedCurrentDMs != 1 {
			t.Errorf("LockedCurrentDMs = %v, want 1", report.Buckets[bucket])
		}
	})
}

// TestLifecycleEngine_DryRunNoChanges: a dry-run cycle changes nothing.
func TestLifecycleEngine_DryRunNoChanges(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := testutil.RandomBucketName()
	ts.CreateTestBucket(t, bucket)
	enableVersioning(t, client, bucket)
	v1 := putObject(t, client, bucket, "k", "data")
	setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
		ID:         aws.String("exp"),
		Status:     types.ExpirationStatusEnabled,
		Filter:     &types.LifecycleRuleFilter{},
		Expiration: &types.LifecycleExpiration{Days: aws.Int32(1)},
	}})

	report := runCycle(t, ts, futureNow(), true)
	if report.Buckets[bucket] == nil || report.Buckets[bucket].Actions != 1 {
		t.Fatalf("dry-run should plan 1 action, got %v", report.Buckets[bucket])
	}
	if !objectExists(t, client, bucket, "k") {
		t.Error("dry-run must not hide the object")
	}
	if !versionRetrievable(t, client, bucket, "k", v1) {
		t.Error("dry-run must not touch the version")
	}
}

// TestLifecycleEngine_SuspendedSkipsExpiration: a suspended bucket's Expiration
// rule is skipped.
func TestLifecycleEngine_SuspendedSkipsExpiration(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucket := testutil.RandomBucketName()
	ts.CreateTestBucket(t, bucket)
	enableVersioning(t, client, bucket)
	v1 := putObject(t, client, bucket, "k", "data")
	_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusSuspended},
	})
	require.NoError(t, err)
	setLifecycleRules(t, client, bucket, []types.LifecycleRule{{
		ID:         aws.String("exp"),
		Status:     types.ExpirationStatusEnabled,
		Filter:     &types.LifecycleRuleFilter{},
		Expiration: &types.LifecycleExpiration{Days: aws.Int32(1)},
	}})

	runCycle(t, ts, futureNow(), false)

	if !versionRetrievable(t, client, bucket, "k", v1) {
		t.Error("suspended bucket Expiration must be skipped; version was removed")
	}
}
