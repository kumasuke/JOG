package s3compat

import (
	"context"
	"io"
	"strings"
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

// withBypassGovernanceHeader returns an SDK functional option that injects
// the x-amz-bypass-governance-retention=true header into the outgoing
// request. Used by H-1 multipart tests because
// CompleteMultipartUploadInput does not expose BypassGovernanceRetention as
// a typed field in the AWS SDK Go v2.
func withBypassGovernanceHeader() func(*s3.Options) {
	return func(o *s3.Options) {
		o.APIOptions = append(o.APIOptions,
			smithyhttp.SetHeaderValue("x-amz-bypass-governance-retention", "true"),
		)
	}
}

// TestPutObjectLockConfiguration tests setting object lock configuration on a bucket.
func TestPutObjectLockConfiguration(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Put object lock configuration with governance mode
	_, err = client.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{
		Bucket: aws.String(bucketName),
		ObjectLockConfiguration: &types.ObjectLockConfiguration{
			ObjectLockEnabled: types.ObjectLockEnabledEnabled,
			Rule: &types.ObjectLockRule{
				DefaultRetention: &types.DefaultRetention{
					Mode: types.ObjectLockRetentionModeGovernance,
					Days: aws.Int32(30),
				},
			},
		},
	})
	require.NoError(t, err)

	// Get object lock configuration to verify
	result, err := client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	assert.Equal(t, types.ObjectLockEnabledEnabled, result.ObjectLockConfiguration.ObjectLockEnabled)
	require.NotNil(t, result.ObjectLockConfiguration.Rule)
	require.NotNil(t, result.ObjectLockConfiguration.Rule.DefaultRetention)
	assert.Equal(t, types.ObjectLockRetentionModeGovernance, result.ObjectLockConfiguration.Rule.DefaultRetention.Mode)
	assert.Equal(t, int32(30), *result.ObjectLockConfiguration.Rule.DefaultRetention.Days)
}

// TestPutObjectLockConfigurationWithComplianceMode tests object lock with compliance mode.
func TestPutObjectLockConfigurationWithComplianceMode(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Put object lock configuration with compliance mode
	_, err = client.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{
		Bucket: aws.String(bucketName),
		ObjectLockConfiguration: &types.ObjectLockConfiguration{
			ObjectLockEnabled: types.ObjectLockEnabledEnabled,
			Rule: &types.ObjectLockRule{
				DefaultRetention: &types.DefaultRetention{
					Mode:  types.ObjectLockRetentionModeCompliance,
					Years: aws.Int32(1),
				},
			},
		},
	})
	require.NoError(t, err)

	// Get object lock configuration to verify
	result, err := client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	assert.Equal(t, types.ObjectLockRetentionModeCompliance, result.ObjectLockConfiguration.Rule.DefaultRetention.Mode)
	assert.Equal(t, int32(1), *result.ObjectLockConfiguration.Rule.DefaultRetention.Years)
}

// TestGetObjectLockConfigurationNotConfigured tests getting object lock config when not set.
func TestGetObjectLockConfigurationNotConfigured(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get object lock configuration for bucket without object lock enabled
	_, err := client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "ObjectLockConfigurationNotFoundError", apiErr.ErrorCode())
	}
}

// TestGetObjectLockConfigurationBucketNotFound tests getting object lock config for non-existent bucket.
func TestGetObjectLockConfigurationBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Get object lock configuration for non-existent bucket
	_, err := client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{
		Bucket: aws.String("non-existent-bucket"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}

// TestPutObjectRetention tests setting object retention.
func TestPutObjectRetention(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		// Cleanup objects and bucket
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       obj.Key,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Put an object
	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Set object retention
	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Get object retention to verify
	result, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	assert.Equal(t, types.ObjectLockRetentionModeGovernance, result.Retention.Mode)
	assert.WithinDuration(t, retainUntil, *result.Retention.RetainUntilDate, time.Second)
}

// TestPutObjectRetentionComplianceMode tests setting object retention with compliance mode.
func TestPutObjectRetentionComplianceMode(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Put an object
	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Set object retention with compliance mode
	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Get object retention to verify
	result, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	assert.Equal(t, types.ObjectLockRetentionModeCompliance, result.Retention.Mode)
}

// TestGetObjectRetentionNotSet tests getting retention for object without retention.
func TestGetObjectRetentionNotSet(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: obj.Key})
			}
		}
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Put an object
	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Get object retention for object without retention
	_, err = client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchObjectLockConfiguration", apiErr.ErrorCode())
	}
}

// TestGetObjectRetentionObjectNotFound tests getting retention for non-existent object.
func TestGetObjectRetentionObjectNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Get object retention for non-existent object
	_, err = client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("non-existent-key"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchKey", apiErr.ErrorCode())
	}
}

// TestPutObjectLegalHold tests setting legal hold on an object.
func TestPutObjectLegalHold(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		// Cleanup objects and bucket
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				// Remove legal hold first
				client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
					Bucket:    aws.String(bucketName),
					Key:       obj.Key,
					LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff},
				})
				client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: obj.Key})
			}
		}
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Put an object
	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Set legal hold ON
	_, err = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		LegalHold: &types.ObjectLockLegalHold{
			Status: types.ObjectLockLegalHoldStatusOn,
		},
	})
	require.NoError(t, err)

	// Get legal hold to verify
	result, err := client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	assert.Equal(t, types.ObjectLockLegalHoldStatusOn, result.LegalHold.Status)
}

// TestPutObjectLegalHoldOff tests removing legal hold from an object.
func TestPutObjectLegalHoldOff(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: obj.Key})
			}
		}
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Put an object
	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Set legal hold ON first
	_, err = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		LegalHold: &types.ObjectLockLegalHold{
			Status: types.ObjectLockLegalHoldStatusOn,
		},
	})
	require.NoError(t, err)

	// Set legal hold OFF
	_, err = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		LegalHold: &types.ObjectLockLegalHold{
			Status: types.ObjectLockLegalHoldStatusOff,
		},
	})
	require.NoError(t, err)

	// Get legal hold to verify it's off
	result, err := client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	assert.Equal(t, types.ObjectLockLegalHoldStatusOff, result.LegalHold.Status)
}

// TestGetObjectLegalHoldNotSet tests getting legal hold for object without legal hold.
func TestGetObjectLegalHoldNotSet(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: obj.Key})
			}
		}
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Put an object
	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Get legal hold for object without legal hold set
	// S3 returns NoSuchObjectLockConfiguration for objects without legal hold
	_, err = client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchObjectLockConfiguration", apiErr.ErrorCode())
	}
}

// TestGetObjectLegalHoldObjectNotFound tests getting legal hold for non-existent object.
func TestGetObjectLegalHoldObjectNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket with object lock enabled
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Get legal hold for non-existent object
	_, err = client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("non-existent-key"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchKey", apiErr.ErrorCode())
	}
}

// TestPutObjectLegalHoldOnBucketWithoutObjectLock tests legal hold on bucket without object lock.
func TestPutObjectLegalHoldOnBucketWithoutObjectLock(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put an object
	objectKey := testutil.RandomObjectKey()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Try to set legal hold on bucket without object lock enabled
	_, err = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		LegalHold: &types.ObjectLockLegalHold{
			Status: types.ObjectLockLegalHoldStatusOn,
		},
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "InvalidRequest", apiErr.ErrorCode())
	}
}

// TestPutObjectRetentionOnBucketWithoutObjectLock tests retention on bucket without object lock.
func TestPutObjectRetentionOnBucketWithoutObjectLock(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put an object
	objectKey := testutil.RandomObjectKey()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Try to set retention on bucket without object lock enabled
	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "InvalidRequest", apiErr.ErrorCode())
	}
}

// TestDeleteObject_GovernanceRetentionBlocksDelete verifies that DeleteObject
// fails with AccessDenied while a GOVERNANCE retention is still active, and
// succeeds when the client supplies BypassGovernanceRetention=true (CR-5).
// Without this evaluation a client could simply DELETE a "locked" object,
// defeating the entire Object Lock feature.
func TestDeleteObject_GovernanceRetentionBlocksDelete(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("locked"),
	})
	require.NoError(t, err)

	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Plain delete should be rejected because the retention is still active.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.Error(t, err, "delete must be rejected while object is under GOVERNANCE retention")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}

	// With BypassGovernanceRetention the delete should succeed.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(objectKey),
		BypassGovernanceRetention: aws.Bool(true),
	})
	require.NoError(t, err)
}

// TestDeleteObject_LegalHoldOnBlocksDelete verifies that DeleteObject is
// rejected while legal hold is ON even though no retention is set (CR-5).
// Legal hold has no expiry, so bypass-governance-retention does not lift it.
func TestDeleteObject_LegalHoldOnBlocksDelete(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		// Remove legal hold before bucket cleanup so DeleteBucket succeeds.
		_, _ = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
			Bucket:    aws.String(bucketName),
			Key:       aws.String("hold-key"),
			LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff},
		})
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(bucketName),
					Key:    obj.Key,
				})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	objectKey := "hold-key"
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("legal-hold"),
	})
	require.NoError(t, err)

	_, err = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn},
	})
	require.NoError(t, err)

	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.Error(t, err, "delete must be rejected while legal hold is ON")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}
}

// TestPutObject_OverwriteLockedObjectRejected verifies that overwriting a
// non-versioned bucket's object via PUT is blocked when the existing object
// is protected by an active retention (CR-5). On a non-versioned bucket the
// overwrite is effectively a delete + create of the same key, which would
// silently bypass Object Lock without this check.
func TestPutObject_OverwriteLockedObjectRejected(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       obj.Key,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v1"),
	})
	require.NoError(t, err)

	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Overwrite attempt: must fail because the existing object is under
	// COMPLIANCE retention and the bucket is not versioned.
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v2-overwrite"),
	})
	require.Error(t, err, "overwrite of locked object must be rejected")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}
}

// TestDeleteObjects_GovernanceRetentionBlocksBatchDelete verifies that the
// per-key Object Lock evaluation is enforced on the bulk DeleteObjects path
// (CR-5). Without it, a client could trivially bypass single-object retention
// by enqueueing the protected key in a multi-delete request — defeating the
// whole point of the feature.
func TestDeleteObjects_GovernanceRetentionBlocksBatchDelete(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       obj.Key,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	lockedKey := "locked-" + testutil.RandomObjectKey()
	freeKey := "free-" + testutil.RandomObjectKey()

	for _, k := range []string{lockedKey, freeKey} {
		_, err = client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(k),
			Body:   strings.NewReader("payload"),
		})
		require.NoError(t, err)
	}

	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(lockedKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Bulk delete without bypass: the locked key must surface as a per-key
	// AccessDenied while the free key is removed.
	out, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucketName),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String(lockedKey)},
				{Key: aws.String(freeKey)},
			},
		},
	})
	require.NoError(t, err, "DeleteObjects request itself must succeed (errors are reported per-key)")

	var sawLockedDenied bool
	for _, e := range out.Errors {
		if aws.ToString(e.Key) == lockedKey {
			sawLockedDenied = true
			assert.Equal(t, "AccessDenied", aws.ToString(e.Code))
		}
	}
	assert.True(t, sawLockedDenied, "locked key must appear in DeleteObjects errors with AccessDenied")

	// The unprotected key must have been deleted.
	deletedFree := false
	for _, d := range out.Deleted {
		if aws.ToString(d.Key) == freeKey {
			deletedFree = true
		}
	}
	assert.True(t, deletedFree, "unprotected key must be reported as deleted")

	// The locked key must still exist.
	_, err = client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(lockedKey),
	})
	assert.NoError(t, err, "locked key must still be retrievable after blocked delete")

	// With BypassGovernanceRetention the same call must remove it.
	out, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket:                    aws.String(bucketName),
		BypassGovernanceRetention: aws.Bool(true),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String(lockedKey)},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, out.Errors, "bypass must clear the per-key error")
}

// TestCopyObject_OverwriteLockedDestinationRejected verifies that CopyObject
// honours Object Lock on the destination key when the destination bucket is
// not versioned (CR-5). Without this guard, a client that cannot directly
// PUT/DELETE the protected key could simply COPY any source over it,
// silently defeating retention.
func TestCopyObject_OverwriteLockedDestinationRejected(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       obj.Key,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	dstKey := "dst-" + testutil.RandomObjectKey()
	srcKey := "src-" + testutil.RandomObjectKey()

	// Seed source and destination with distinct payloads.
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(srcKey),
		Body:   strings.NewReader("source-payload"),
	})
	require.NoError(t, err)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(dstKey),
		Body:   strings.NewReader("destination-original"),
	})
	require.NoError(t, err)

	// Lock the destination under GOVERNANCE retention.
	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(dstKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Copy without bypass must be rejected with AccessDenied.
	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(dstKey),
		CopySource: aws.String(bucketName + "/" + srcKey),
	})
	require.Error(t, err, "copy onto a locked destination must be rejected")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}

	// Verify the destination payload is untouched (original bytes still there).
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(dstKey),
	})
	require.NoError(t, err)
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, "destination-original", string(body), "destination must keep its original payload after blocked copy")
}

// objectLockRetentionTestSetup creates a bucket with Object Lock enabled and
// stores an object with the given retention configuration. Returns the bucket
// name, object key, and a cleanup func that removes the object (with bypass)
// and the bucket. Used by C-1 retention modification tests.
func objectLockRetentionTestSetup(t *testing.T, ts *testutil.TestServer, mode types.ObjectLockRetentionMode, retainUntil time.Time) (string, string, func()) {
	t.Helper()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)

	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("payload"),
	})
	require.NoError(t, err)

	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            mode,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	cleanup := func() {
		// Try to clear retention with COMPLIANCE-aware retry path: bypass
		// works only on GOVERNANCE; for COMPLIANCE the underlying object
		// can simply be left to the temp data dir tear-down. The DeleteBucket
		// is best-effort.
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       obj.Key,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}
	return bucketName, objectKey, cleanup
}

// TestPutObjectRetention_ComplianceCannotDowngradeToGovernance verifies that a
// COMPLIANCE retention cannot be replaced with a GOVERNANCE retention via
// PutObjectRetention, even when bypass is supplied (C-1). COMPLIANCE is
// immutable until the date passes — any mode change to GOVERNANCE while the
// retention is active must be rejected with AccessDenied.
func TestPutObjectRetention_ComplianceCannotDowngradeToGovernance(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	bucketName, objectKey, cleanup := objectLockRetentionTestSetup(t, ts, types.ObjectLockRetentionModeCompliance, retainUntil)
	defer cleanup()

	// Attempt downgrade to GOVERNANCE; even with bypass header this must fail.
	_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(objectKey),
		BypassGovernanceRetention: aws.Bool(true),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil.Add(24 * time.Hour)),
		},
	})
	require.Error(t, err, "downgrading COMPLIANCE to GOVERNANCE must be rejected")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}
}

// TestPutObjectRetention_ComplianceCannotShortenRetainUntil verifies that a
// COMPLIANCE retain-until-date cannot be shortened (C-1). Even with bypass,
// the new RetainUntilDate must be at or after the existing one for
// COMPLIANCE — otherwise an attacker could rewind the lock and delete early.
func TestPutObjectRetention_ComplianceCannotShortenRetainUntil(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	bucketName, objectKey, cleanup := objectLockRetentionTestSetup(t, ts, types.ObjectLockRetentionModeCompliance, retainUntil)
	defer cleanup()

	shorter := retainUntil.Add(-12 * time.Hour)
	_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(objectKey),
		BypassGovernanceRetention: aws.Bool(true),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(shorter),
		},
	})
	require.Error(t, err, "shortening COMPLIANCE retain-until-date must be rejected")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}
}

// TestPutObjectRetention_ComplianceExtensionAllowed verifies that pushing a
// COMPLIANCE retain-until-date further into the future is allowed without
// bypass (C-1). Only shortening / mode-change is forbidden.
func TestPutObjectRetention_ComplianceExtensionAllowed(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	bucketName, objectKey, cleanup := objectLockRetentionTestSetup(t, ts, types.ObjectLockRetentionModeCompliance, retainUntil)
	defer cleanup()

	longer := retainUntil.Add(24 * time.Hour)
	_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(longer),
		},
	})
	require.NoError(t, err, "extending COMPLIANCE retain-until-date must be allowed")
}

// TestPutObjectRetention_GovernanceShortenWithoutBypassRejected verifies that
// shortening a GOVERNANCE retain-until-date requires the
// x-amz-bypass-governance-retention header (C-1). Without it, the modification
// must be rejected with AccessDenied.
func TestPutObjectRetention_GovernanceShortenWithoutBypassRejected(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	bucketName, objectKey, cleanup := objectLockRetentionTestSetup(t, ts, types.ObjectLockRetentionModeGovernance, retainUntil)
	defer cleanup()

	shorter := retainUntil.Add(-12 * time.Hour)
	_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(shorter),
		},
	})
	require.Error(t, err, "shortening GOVERNANCE retain-until-date without bypass must be rejected")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}
}

// TestPutObjectRetention_GovernanceShortenWithBypassAllowed verifies that
// shortening a GOVERNANCE retain-until-date is allowed when bypass is set
// (C-1). This is the standard escape hatch for governance mode.
func TestPutObjectRetention_GovernanceShortenWithBypassAllowed(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	bucketName, objectKey, cleanup := objectLockRetentionTestSetup(t, ts, types.ObjectLockRetentionModeGovernance, retainUntil)
	defer cleanup()

	shorter := retainUntil.Add(-12 * time.Hour)
	_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(objectKey),
		BypassGovernanceRetention: aws.Bool(true),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(shorter),
		},
	})
	require.NoError(t, err, "shortening GOVERNANCE retain-until-date with bypass must succeed")
}

// TestPutObjectRetention_GovernanceShortenViaComplianceModeChangeRejected
// closes a bypass uncovered in Codex review: previously the GOVERNANCE
// branch in evaluateRetentionChange returned early on next.Mode ==
// COMPLIANCE before the shortening check, so a client could rewrite an
// active "GOVERNANCE until +N days" retention as "COMPLIANCE until past"
// without bypass and immediately delete the object — a complete CR-5
// bypass disguised as a mode "upgrade".
func TestPutObjectRetention_GovernanceShortenViaComplianceModeChangeRejected(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	bucketName, objectKey, cleanup := objectLockRetentionTestSetup(t, ts, types.ObjectLockRetentionModeGovernance, retainUntil)
	defer cleanup()

	// Attempt to mask a shortening as an "upgrade" to COMPLIANCE without
	// bypass. Must be rejected: the date check has to run before the
	// mode check, so a shorter RetainUntilDate is denied irrespective
	// of the requested mode.
	pastDate := time.Now().Add(-1 * time.Hour).UTC()
	_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(pastDate),
		},
	})
	require.Error(t, err, "shortening via mode 'upgrade' must be rejected without bypass")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}

	// Sanity: the original GOVERNANCE retention must still be intact.
	got, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)
	assert.Equal(t, types.ObjectLockRetentionModeGovernance, got.Retention.Mode, "original GOVERNANCE mode must be preserved")
	assert.WithinDuration(t, retainUntil, *got.Retention.RetainUntilDate, time.Second, "original RetainUntilDate must be preserved")

	// Same call with bypass must succeed (governance shortening + mode
	// upgrade is still legal when the caller opts in).
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(objectKey),
		BypassGovernanceRetention: aws.Bool(true),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(pastDate),
		},
	})
	require.NoError(t, err, "with bypass, the shortening + upgrade combination must be accepted")
}

// TestPutObjectRetention_GovernanceUpgradeWithExtensionAllowed verifies that
// a GOVERNANCE→COMPLIANCE upgrade combined with an extended RetainUntilDate
// is allowed without bypass. This pins the legitimate "strict upgrade" case
// that was at risk of regressing once the date check moved before the mode
// check (P1 fix).
func TestPutObjectRetention_GovernanceUpgradeWithExtensionAllowed(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	bucketName, objectKey, cleanup := objectLockRetentionTestSetup(t, ts, types.ObjectLockRetentionModeGovernance, retainUntil)
	defer cleanup()

	extended := retainUntil.Add(24 * time.Hour)
	_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(extended),
		},
	})
	require.NoError(t, err, "GOVERNANCE→COMPLIANCE upgrade with extension must be allowed without bypass")

	got, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)
	assert.Equal(t, types.ObjectLockRetentionModeCompliance, got.Retention.Mode, "mode must be upgraded to COMPLIANCE")
	assert.WithinDuration(t, extended, *got.Retention.RetainUntilDate, time.Second, "RetainUntilDate must be the extended value")
}

// TestCompleteMultipartUpload_GovernanceRetentionBlocksOverwrite verifies
// that finishing a multipart upload onto a key already protected by an
// active GOVERNANCE retention is rejected with AccessDenied (H-1). Without
// this guard, multipart provides a full Object Lock bypass: the storage
// CompleteMultipartUpload call would silently overwrite the locked object.
func TestCompleteMultipartUpload_GovernanceRetentionBlocksOverwrite(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       obj.Key,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v1-locked"),
	})
	require.NoError(t, err)

	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Now try to overwrite via multipart.
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	partContent := strings.Repeat("x", 5*1024*1024) // 5MB single part
	partResult, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(objectKey),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       strings.NewReader(partContent),
	})
	require.NoError(t, err)

	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(objectKey),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{PartNumber: aws.Int32(1), ETag: partResult.ETag},
			},
		},
	})
	require.Error(t, err, "complete-multipart onto a locked object must be rejected without bypass")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}

	// Verify the original payload is still intact.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, "v1-locked", string(body), "locked object must keep its original payload after blocked complete-multipart")

	// Best-effort abort so the in-progress upload does not block bucket cleanup.
	_, _ = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(objectKey),
		UploadId: createResult.UploadId,
	})
}

// TestCompleteMultipartUpload_BypassGovernanceAllowsOverwrite verifies that
// supplying x-amz-bypass-governance-retention=true on the
// CompleteMultipartUpload request lifts a GOVERNANCE retention and the
// overwrite succeeds (H-1). This mirrors single-PUT semantics. The header is
// injected via a smithy middleware because the AWS SDK v2
// CompleteMultipartUploadInput does not expose BypassGovernanceRetention as
// a typed field.
func TestCompleteMultipartUpload_BypassGovernanceAllowsOverwrite(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)
	defer func() {
		listOutput, _ := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, obj := range listOutput.Contents {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       obj.Key,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	objectKey := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v1-locked"),
	})
	require.NoError(t, err)

	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	partContent := strings.Repeat("y", 5*1024*1024)
	partResult, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(objectKey),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       strings.NewReader(partContent),
	})
	require.NoError(t, err)

	// CompleteMultipartUpload with bypass header injected via a smithy
	// finalize middleware. The SDK does not expose BypassGovernanceRetention
	// for this op, so we set the header on the underlying HTTP request.
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(objectKey),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{PartNumber: aws.Int32(1), ETag: partResult.ETag},
			},
		},
	}, withBypassGovernanceHeader())
	require.NoError(t, err, "complete-multipart with bypass must succeed against a GOVERNANCE-locked key")

	// Verify the new payload replaced the original.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, partContent, string(body), "bypass complete-multipart must overwrite the locked object")
}
// upgrading GOVERNANCE to COMPLIANCE is allowed without bypass (C-1). Going
// from a relaxed mode to a stricter mode never reduces protection.
func TestPutObjectRetention_GovernanceUpgradeToComplianceAllowed(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	retainUntil := time.Now().Add(48 * time.Hour).UTC()
	bucketName, objectKey, cleanup := objectLockRetentionTestSetup(t, ts, types.ObjectLockRetentionModeGovernance, retainUntil)
	defer cleanup()

	_, err := client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err, "upgrade from GOVERNANCE to COMPLIANCE must be allowed")
}

// TestCopyObject_DestinationBucketNotFound is a P2 regression: HeadObject on a
// missing destination bucket returns ErrBucketNotFound, which the Object Lock
// pre-check must treat as a known client-facing condition (skip the lock
// evaluation) so the downstream storage call can surface the canonical
// NoSuchBucket. A naive "anything other than ErrObjectNotFound is fail-closed"
// implementation would have collapsed this 404 to a 500 InternalError.
func TestCopyObject_DestinationBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	srcBucket := testutil.RandomBucketName()
	srcCleanup := ts.CreateTestBucket(t, srcBucket)
	defer srcCleanup()

	srcKey := testutil.RandomObjectKey()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(srcBucket),
		Key:    aws.String(srcKey),
		Body:   strings.NewReader("payload"),
	})
	require.NoError(t, err)

	missingDstBucket := "missing-" + testutil.RandomBucketName()
	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(missingDstBucket),
		Key:        aws.String("dst-key"),
		CopySource: aws.String(srcBucket + "/" + srcKey),
	})
	require.Error(t, err)
	// Must surface NoSuchBucket (404), not InternalError (500).
	assert.Contains(t, err.Error(), "NoSuchBucket")
	assert.NotContains(t, err.Error(), "InternalError")
}

// TestCompleteMultipartUpload_NonExistentBucket is the corresponding P2
// regression for the multipart finish path. With the old fail-closed default,
// HeadObject's ErrBucketNotFound on a missing bucket would have been remapped
// to InternalError before the storage layer could return NoSuchBucket /
// NoSuchUpload. We pre-create+abort an upload to obtain a syntactically
// realistic upload-id, then point the Complete call at a bucket that does not
// exist.
func TestCompleteMultipartUpload_NonExistentBucket(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	missingBucket := "missing-" + testutil.RandomBucketName()
	_, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(missingBucket),
		Key:      aws.String("any-key"),
		UploadId: aws.String("fabricated-upload-id"),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{PartNumber: aws.Int32(1), ETag: aws.String("\"deadbeef\"")},
			},
		},
	})
	require.Error(t, err)
	// Must surface a canonical S3 client error (NoSuchBucket or NoSuchUpload),
	// not InternalError.
	errStr := err.Error()
	assert.True(t,
		strings.Contains(errStr, "NoSuchBucket") || strings.Contains(errStr, "NoSuchUpload"),
		"expected NoSuchBucket or NoSuchUpload, got: %s", errStr,
	)
	assert.NotContains(t, errStr, "InternalError")
}
