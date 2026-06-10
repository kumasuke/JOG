package s3compat

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withHeader returns an SDK functional option that injects a raw request
// header. Used to exercise validation paths where the AWS SDK would otherwise
// enforce its own client-side pairing rules (e.g. sending only one of the
// retention mode / date headers).
func withHeader(name, value string) func(*s3.Options) {
	return func(o *s3.Options) {
		o.APIOptions = append(o.APIOptions, smithyhttp.SetHeaderValue(name, value))
	}
}

// createObjectLockBucket creates an Object-Lock-enabled bucket and returns a
// cleanup func that removes all versions (bypassing governance / clearing legal
// holds) before deleting the bucket. Object Lock buckets auto-enable versioning
// (#39), so listing must use ListObjectVersions.
func createObjectLockBucket(t *testing.T, client *s3.Client, ctx context.Context) (string, func()) {
	t.Helper()
	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)

	cleanup := func() {
		out, _ := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucketName)})
		if out != nil {
			all := append([]types.ObjectVersion{}, out.Versions...)
			for _, v := range all {
				// Clear any legal hold so the version can be removed.
				client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
					Bucket:    aws.String(bucketName),
					Key:       v.Key,
					VersionId: v.VersionId,
					LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff},
				})
				client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       v.Key,
					VersionId:                 v.VersionId,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
			for _, dm := range out.DeleteMarkers {
				client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       dm.Key,
					VersionId:                 dm.VersionId,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}
	return bucketName, cleanup
}

// TestPutObjectWithRetentionHeaders verifies that a PutObject carrying the
// x-amz-object-lock-mode / x-amz-object-lock-retain-until-date headers applies
// the retention to the newly written version (issue #40), readable back via
// GetObjectRetention.
func TestPutObjectWithRetentionHeaders(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	key := testutil.RandomObjectKey()
	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(key),
		Body:                      bytes.NewReader([]byte("locked-on-write")),
		ObjectLockMode:            types.ObjectLockModeGovernance,
		ObjectLockRetainUntilDate: aws.Time(retainUntil),
	})
	require.NoError(t, err)

	got, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	require.NotNil(t, got.Retention)
	assert.Equal(t, types.ObjectLockRetentionModeGovernance, got.Retention.Mode)
	assert.WithinDuration(t, retainUntil, *got.Retention.RetainUntilDate, time.Second)
}

// TestPutObjectWithLegalHoldHeader verifies the x-amz-object-lock-legal-hold
// header is applied to the new version.
func TestPutObjectWithLegalHoldHeader(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	key := testutil.RandomObjectKey()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(key),
		Body:                      bytes.NewReader([]byte("legal-hold-on-write")),
		ObjectLockLegalHoldStatus: types.ObjectLockLegalHoldStatusOn,
	})
	require.NoError(t, err)

	got, err := client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	require.NotNil(t, got.LegalHold)
	assert.Equal(t, types.ObjectLockLegalHoldStatusOn, got.LegalHold.Status)
}

// TestPutObjectDefaultRetentionApplied verifies that when a bucket has a
// DefaultRetention rule and the PUT carries no retention header, the new
// version is automatically protected with a RetainUntilDate computed at write
// time from Days.
func TestPutObjectDefaultRetentionApplied(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	// Configure a 7-day GOVERNANCE default retention.
	_, err := client.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{
		Bucket: aws.String(bucketName),
		ObjectLockConfiguration: &types.ObjectLockConfiguration{
			ObjectLockEnabled: types.ObjectLockEnabledEnabled,
			Rule: &types.ObjectLockRule{
				DefaultRetention: &types.DefaultRetention{
					Mode: types.ObjectLockRetentionModeGovernance,
					Days: aws.Int32(7),
				},
			},
		},
	})
	require.NoError(t, err)

	before := time.Now().UTC()
	key := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte("default-retained")),
	})
	require.NoError(t, err)

	got, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	require.NotNil(t, got.Retention)
	assert.Equal(t, types.ObjectLockRetentionModeGovernance, got.Retention.Mode)
	// RetainUntilDate ~= now + 7 days, fixed at write time.
	expected := before.AddDate(0, 0, 7)
	assert.WithinDuration(t, expected, *got.Retention.RetainUntilDate, 10*time.Second)
}

// TestPutObjectHeaderOverridesDefaultRetention verifies an explicit retention
// header takes precedence over the bucket's DefaultRetention.
func TestPutObjectHeaderOverridesDefaultRetention(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	// Default is 7-day GOVERNANCE.
	_, err := client.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{
		Bucket: aws.String(bucketName),
		ObjectLockConfiguration: &types.ObjectLockConfiguration{
			ObjectLockEnabled: types.ObjectLockEnabledEnabled,
			Rule: &types.ObjectLockRule{
				DefaultRetention: &types.DefaultRetention{
					Mode: types.ObjectLockRetentionModeGovernance,
					Days: aws.Int32(7),
				},
			},
		},
	})
	require.NoError(t, err)

	// Header asks for COMPLIANCE with a far-future date.
	headerUntil := time.Now().Add(72 * time.Hour).UTC()
	key := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(key),
		Body:                      bytes.NewReader([]byte("override")),
		ObjectLockMode:            types.ObjectLockModeCompliance,
		ObjectLockRetainUntilDate: aws.Time(headerUntil),
	})
	require.NoError(t, err)

	got, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	require.NotNil(t, got.Retention)
	assert.Equal(t, types.ObjectLockRetentionModeCompliance, got.Retention.Mode)
	assert.WithinDuration(t, headerUntil, *got.Retention.RetainUntilDate, time.Second)
}

// TestPutObjectRetentionHeaderMissingDate verifies that supplying the mode
// header without the retain-until-date header is rejected with InvalidArgument
// (the two must be supplied together).
func TestPutObjectRetentionHeaderMissingDate(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	key := testutil.RandomObjectKey()
	// Inject only the mode header (no retain-until-date) via raw header so the
	// SDK does not enforce its own pairing validation.
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte("bad")),
	}, withHeader("x-amz-object-lock-mode", "GOVERNANCE"))
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "InvalidArgument", apiErr.ErrorCode())
}

// TestPutObjectRetentionHeaderMissingMode verifies the symmetric case: a
// retain-until-date header without a mode header is InvalidArgument.
func TestPutObjectRetentionHeaderMissingMode(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	key := testutil.RandomObjectKey()
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte("bad")),
	}, withHeader("x-amz-object-lock-retain-until-date", future))
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "InvalidArgument", apiErr.ErrorCode())
}

// TestPutObjectLockHeaderOnNonLockBucket verifies that any x-amz-object-lock-*
// header on a bucket that is NOT Object-Lock-enabled is rejected with
// InvalidRequest.
func TestPutObjectLockHeaderOnNonLockBucket(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(key),
		Body:                      bytes.NewReader([]byte("no-lock-bucket")),
		ObjectLockMode:            types.ObjectLockModeGovernance,
		ObjectLockRetainUntilDate: aws.Time(retainUntil),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "InvalidRequest", apiErr.ErrorCode())
}

// TestCopyObjectDoesNotInheritSourceRetention verifies a CopyObject does NOT
// inherit the source version's retention. The source is locked under
// GOVERNANCE; the copy (no lock headers, no default retention) must have no
// retention.
func TestCopyObjectDoesNotInheritSourceRetention(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	srcKey := testutil.RandomObjectKey()
	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(srcKey),
		Body:                      bytes.NewReader([]byte("source-locked")),
		ObjectLockMode:            types.ObjectLockModeGovernance,
		ObjectLockRetainUntilDate: aws.Time(retainUntil),
	})
	require.NoError(t, err)

	// Sanity: source has retention.
	srcRet, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(srcKey),
	})
	require.NoError(t, err)
	require.NotNil(t, srcRet.Retention)

	dstKey := testutil.RandomObjectKey()
	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(dstKey),
		CopySource: aws.String(bucketName + "/" + srcKey),
	})
	require.NoError(t, err)

	// Destination must NOT have inherited the source retention.
	_, err = client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(dstKey),
	})
	require.Error(t, err)
	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "NoSuchObjectLockConfiguration", apiErr.ErrorCode())
}

// TestCopyObjectWithRetentionHeaders verifies retention headers on a CopyObject
// apply to the copied (destination) version.
func TestCopyObjectWithRetentionHeaders(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	srcKey := testutil.RandomObjectKey()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(srcKey),
		Body:   bytes.NewReader([]byte("source")),
	})
	require.NoError(t, err)

	dstKey := testutil.RandomObjectKey()
	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(dstKey),
		CopySource:                aws.String(bucketName + "/" + srcKey),
		ObjectLockMode:            types.ObjectLockModeCompliance,
		ObjectLockRetainUntilDate: aws.Time(retainUntil),
	})
	require.NoError(t, err)

	got, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(dstKey),
	})
	require.NoError(t, err)
	require.NotNil(t, got.Retention)
	assert.Equal(t, types.ObjectLockRetentionModeCompliance, got.Retention.Mode)
	assert.WithinDuration(t, retainUntil, *got.Retention.RetainUntilDate, time.Second)
}

// TestMultipartUploadWithRetentionHeaders verifies that retention headers
// supplied at CreateMultipartUpload are applied to the version finalized at
// CompleteMultipartUpload (issue #40).
func TestMultipartUploadWithRetentionHeaders(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, cleanup := createObjectLockBucket(t, client, ctx)
	defer cleanup()

	key := testutil.RandomObjectKey()
	retainUntil := time.Now().Add(48 * time.Hour).UTC()

	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(key),
		ObjectLockMode:            types.ObjectLockModeGovernance,
		ObjectLockRetainUntilDate: aws.Time(retainUntil),
		ObjectLockLegalHoldStatus: types.ObjectLockLegalHoldStatusOn,
	})
	require.NoError(t, err)

	partContent := bytes.Repeat([]byte("m"), 5*1024*1024)
	partResult, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(partContent),
	})
	require.NoError(t, err)

	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{PartNumber: aws.Int32(1), ETag: partResult.ETag},
			},
		},
	})
	require.NoError(t, err)

	// The finalized version must carry the retention captured at create time.
	gotRet, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	require.NotNil(t, gotRet.Retention)
	assert.Equal(t, types.ObjectLockRetentionModeGovernance, gotRet.Retention.Mode)
	assert.WithinDuration(t, retainUntil, *gotRet.Retention.RetainUntilDate, time.Second)

	// And the legal hold captured at create time.
	gotHold, err := client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	require.NotNil(t, gotHold.LegalHold)
	assert.Equal(t, types.ObjectLockLegalHoldStatusOn, gotHold.LegalHold.Status)
}
