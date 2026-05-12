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
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
