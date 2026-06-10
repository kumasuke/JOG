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

// TestPutObjectOnLockedKeyCreatesNewVersion is the #39 fix-round-1 E2E
// regression. An Object-Lock-enabled bucket auto-enables versioning, so a PUT
// onto a key whose existing version is under GOVERNANCE retention must create a
// brand-new version and succeed (S3 semantic: new-version creation is always
// allowed regardless of locks on prior versions). Before the fix the storage
// guard rejected this versioned write with ErrObjectLocked, surfacing as 500.
func TestPutObjectOnLockedKeyCreatesNewVersion(t *testing.T) {
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
		listOutput, _ := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucketName)})
		if listOutput != nil {
			for _, v := range listOutput.Versions {
				client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       v.Key,
					VersionId:                 v.VersionId,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	objectKey := testutil.RandomObjectKey()

	// First PUT creates the initial version.
	putOut, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v0"),
	})
	require.NoError(t, err)
	firstVersionID := aws.ToString(putOut.VersionId)
	require.NotEmpty(t, firstVersionID, "Object Lock bucket must auto-enable versioning")

	// Lock that first version under GOVERNANCE retention.
	retainUntil := time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(firstVersionID),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// A second PUT must create a NEW version and succeed (must NOT 500 / be
	// blocked by the null-version guard on the locked prior version).
	put2, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v1-overwrite"),
	})
	require.NoError(t, err, "new-version PUT must not be blocked by lock on prior version")
	secondVersionID := aws.ToString(put2.VersionId)
	require.NotEmpty(t, secondVersionID)
	require.NotEqual(t, firstVersionID, secondVersionID, "PUT must create a new version")

	// The locked first version must remain retrievable with its retention.
	ret, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(firstVersionID),
	})
	require.NoError(t, err)
	assert.Equal(t, types.ObjectLockRetentionModeGovernance, ret.Retention.Mode)
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
	putOut, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("locked"),
	})
	require.NoError(t, err)
	versionID := aws.ToString(putOut.VersionId)
	require.NotEmpty(t, versionID, "object lock bucket must auto-enable versioning (#39)")

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

	// A plain DELETE (no versionId) creates a delete marker on a versioned
	// bucket and is allowed even when the underlying version is protected —
	// this is canonical S3 behaviour (#39).
	delMarkerOut, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err, "plain DELETE creates a delete marker and must be allowed")
	assert.True(t, aws.ToBool(delMarkerOut.DeleteMarker), "plain DELETE must create a delete marker")

	// A version-targeted DELETE of the protected version must be rejected.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(versionID),
	})
	require.Error(t, err, "version-targeted DELETE must be rejected while under GOVERNANCE retention")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}

	// With BypassGovernanceRetention the version-targeted delete should succeed.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(objectKey),
		VersionId:                 aws.String(versionID),
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
	putOut, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("legal-hold"),
	})
	require.NoError(t, err)
	versionID := aws.ToString(putOut.VersionId)
	require.NotEmpty(t, versionID, "object lock bucket must auto-enable versioning (#39)")

	_, err = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn},
	})
	require.NoError(t, err)

	// A version-targeted DELETE of the version under legal hold must be rejected.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(versionID),
	})
	require.Error(t, err, "version-targeted delete must be rejected while legal hold is ON")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}
}

// TestPutObject_OverwriteLockedObjectCreatesNewVersion verifies the canonical
// S3 semantics on an Object Lock bucket (which is always versioned, #39): a PUT
// to a key whose current version is under COMPLIANCE retention creates a NEW
// version and leaves the locked version intact and still protected, rather than
// being rejected. The shorter retention is scoped to the original version.
func TestPutObject_OverwriteLockedObjectCreatesNewVersion(t *testing.T) {
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
	putOut, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v1"),
	})
	require.NoError(t, err)
	v1 := aws.ToString(putOut.VersionId)
	require.NotEmpty(t, v1, "object lock bucket must auto-enable versioning (#39)")

	// Use a short COMPLIANCE retention so the temp data dir is the only cleanup
	// needed; the version stays immutable until the (short) date passes.
	retainUntil := time.Now().Add(3 * time.Second).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Overwrite: creates a new version (must succeed) without disturbing v1.
	put2, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v2-overwrite"),
	})
	require.NoError(t, err, "PUT on a versioned lock bucket creates a new version and must be allowed")
	v2 := aws.ToString(put2.VersionId)
	require.NotEmpty(t, v2)
	assert.NotEqual(t, v1, v2, "overwrite must create a distinct new version")

	// v1 is still retrievable and still carries its COMPLIANCE retention.
	ret, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(v1),
	})
	require.NoError(t, err, "v1 retention must survive the overwrite (#39, defect 2 regression)")
	assert.Equal(t, types.ObjectLockRetentionModeCompliance, ret.Retention.Mode)

	gotV1, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(v1),
	})
	require.NoError(t, err)
	defer gotV1.Body.Close()
	body, err := io.ReadAll(gotV1.Body)
	require.NoError(t, err)
	assert.Equal(t, "v1", string(body), "v1 bytes must be intact after overwrite")
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

	versionIDs := map[string]string{}
	for _, k := range []string{lockedKey, freeKey} {
		putOut, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(k),
			Body:   strings.NewReader("payload"),
		})
		require.NoError(t, err)
		versionIDs[k] = aws.ToString(putOut.VersionId)
		require.NotEmpty(t, versionIDs[k], "object lock bucket must auto-enable versioning (#39)")
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

	// Version-targeted bulk delete without bypass: the locked version must
	// surface as a per-key AccessDenied while the free version is removed
	// (#39: per-entry version-aware lock evaluation).
	out, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucketName),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String(lockedKey), VersionId: aws.String(versionIDs[lockedKey])},
				{Key: aws.String(freeKey), VersionId: aws.String(versionIDs[freeKey])},
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
	assert.True(t, sawLockedDenied, "locked version must appear in DeleteObjects errors with AccessDenied")

	// The unprotected version must have been deleted.
	deletedFree := false
	for _, d := range out.Deleted {
		if aws.ToString(d.Key) == freeKey {
			deletedFree = true
		}
	}
	assert.True(t, deletedFree, "unprotected version must be reported as deleted")

	// The locked version must still exist.
	_, err = client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(lockedKey),
		VersionId: aws.String(versionIDs[lockedKey]),
	})
	assert.NoError(t, err, "locked version must still be retrievable after blocked delete")

	// With BypassGovernanceRetention the same call must remove it.
	out, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket:                    aws.String(bucketName),
		BypassGovernanceRetention: aws.Bool(true),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String(lockedKey), VersionId: aws.String(versionIDs[lockedKey])},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, out.Errors, "bypass must clear the per-key error")
}

// TestCopyObject_OntoLockedDestinationCreatesNewVersion verifies the canonical
// S3 semantics on an Object Lock bucket (always versioned, #39): a CopyObject
// onto a key whose current version is locked creates a NEW destination version
// (allowed) and leaves the locked version's bytes intact, rather than being
// rejected.
func TestCopyObject_OntoLockedDestinationCreatesNewVersion(t *testing.T) {
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

	dstKey := "dst-" + testutil.RandomObjectKey()
	srcKey := "src-" + testutil.RandomObjectKey()

	// Seed source and destination with distinct payloads.
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(srcKey),
		Body:   strings.NewReader("source-payload"),
	})
	require.NoError(t, err)
	dstPut, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(dstKey),
		Body:   strings.NewReader("destination-original"),
	})
	require.NoError(t, err)
	dstV1 := aws.ToString(dstPut.VersionId)
	require.NotEmpty(t, dstV1)

	// Lock the destination's current version under a short COMPLIANCE retention.
	retainUntil := time.Now().Add(3 * time.Second).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(dstKey),
		VersionId: aws.String(dstV1),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Copy must succeed, producing a new destination version.
	copyOut, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(dstKey),
		CopySource: aws.String(bucketName + "/" + srcKey),
	})
	require.NoError(t, err, "copy onto a versioned lock bucket creates a new version and must be allowed")
	dstV2 := aws.ToString(copyOut.VersionId)
	require.NotEmpty(t, dstV2)
	assert.NotEqual(t, dstV1, dstV2, "copy must create a distinct new destination version")

	// The locked version's original bytes must still be retrievable.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(dstKey),
		VersionId: aws.String(dstV1),
	})
	require.NoError(t, err)
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, "destination-original", string(body), "the locked version must keep its original payload after copy")
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

// TestCompleteMultipartUpload_OntoLockedKeyCreatesNewVersion verifies the
// canonical S3 semantics on an Object Lock bucket (always versioned, #39):
// completing a multipart upload onto a key whose current version is locked
// creates a NEW version (allowed) and leaves the locked version's bytes intact,
// rather than being rejected.
func TestCompleteMultipartUpload_OntoLockedKeyCreatesNewVersion(t *testing.T) {
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
	putOut, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v1-locked"),
	})
	require.NoError(t, err)
	v1 := aws.ToString(putOut.VersionId)
	require.NotEmpty(t, v1)

	// Short COMPLIANCE retention on v1 so no bypass-based cleanup is needed.
	retainUntil := time.Now().Add(3 * time.Second).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(v1),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeCompliance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	// Overwrite via multipart: creates a new version, must succeed.
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

	completeOut, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(objectKey),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{PartNumber: aws.Int32(1), ETag: partResult.ETag},
			},
		},
	})
	require.NoError(t, err, "complete-multipart on a versioned lock bucket creates a new version and must be allowed")
	v2 := aws.ToString(completeOut.VersionId)
	require.NotEmpty(t, v2)
	assert.NotEqual(t, v1, v2, "complete-multipart must create a distinct new version")

	// The locked version's original payload must still be intact.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(v1),
	})
	require.NoError(t, err)
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, "v1-locked", string(body), "locked version must keep its original payload after the new version is created")
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

// versioningEnabledLockedBucket creates an Object Lock-enabled bucket with
// versioning Enabled and seeds it with a single object under GOVERNANCE
// retention. The returned cleanup func bypass-deletes any objects and then
// the bucket so versioned buckets do not strand state across tests.
//
// Why this exists: the carve-out removal regression tests all need the same
// "versioning Enabled + locked object" preamble; without a helper each test
// would duplicate ~30 lines of setup that obscures the actual assertion.
func versioningEnabledLockedBucket(t *testing.T, ts *testutil.TestServer, payload string) (bucketName, objectKey string, retainUntil time.Time, cleanup func()) {
	t.Helper()
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName = testutil.RandomBucketName()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                     aws.String(bucketName),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	require.NoError(t, err)

	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	objectKey = testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader(payload),
	})
	require.NoError(t, err)

	retainUntil = time.Now().Add(24 * time.Hour).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.NoError(t, err)

	cleanup = func() {
		// Versioned bucket: list every version + delete marker and remove
		// each one with bypass before dropping the bucket.
		versionsOutput, _ := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucketName)})
		if versionsOutput != nil {
			for _, v := range versionsOutput.Versions {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       v.Key,
					VersionId:                 v.VersionId,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
			for _, m := range versionsOutput.DeleteMarkers {
				_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket:                    aws.String(bucketName),
					Key:                       m.Key,
					VersionId:                 m.VersionId,
					BypassGovernanceRetention: aws.Bool(true),
				})
			}
		}
		// Best-effort fallback for current-version artefacts.
		_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket:                    aws.String(bucketName),
			Key:                       aws.String(objectKey),
			BypassGovernanceRetention: aws.Bool(true),
		})
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}
	return bucketName, objectKey, retainUntil, cleanup
}

// TestPutObject_VersioningEnabled_CreatesNewVersionLeavingLockIntact verifies
// the S3-canonical semantics restored in #39: a PUT to a key whose current
// version is locked creates a NEW version (allowed) and leaves the locked
// version intact. This replaces the PR #38 interim behaviour where versioning
// Enabled blocked the overwrite.
func TestPutObject_VersioningEnabled_CreatesNewVersionLeavingLockIntact(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, objectKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "v1-locked")
	defer cleanup()

	// A new PUT creates a new version and must be allowed.
	put2, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("v2-overwrite"),
	})
	require.NoError(t, err, "PUT creates a new version and must be allowed on a versioned lock bucket")
	require.NotEmpty(t, aws.ToString(put2.VersionId))

	// The current (latest) object now reflects the new payload.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, "v2-overwrite", string(body))
}

// TestDeleteObject_VersioningEnabled_MarkerAllowed_VersionTargetedBlocked
// verifies the S3-canonical semantics restored in #39: an unspecified DELETE
// on a versioned bucket creates a delete marker (allowed even when the current
// version is locked), while a version-targeted DELETE of the locked version is
// rejected with AccessDenied and succeeds only with the bypass header.
func TestDeleteObject_VersioningEnabled_MarkerAllowed_VersionTargetedBlocked(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, objectKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "v1-locked")
	defer cleanup()

	// Resolve the locked version's id.
	versions, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(objectKey),
	})
	require.NoError(t, err)
	require.NotEmpty(t, versions.Versions)
	lockedVersionID := aws.ToString(versions.Versions[0].VersionId)
	require.NotEmpty(t, lockedVersionID)

	// Unspecified DELETE creates a delete marker and must be allowed.
	delOut, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err, "unspecified DELETE creates a delete marker and must be allowed")
	assert.True(t, aws.ToBool(delOut.DeleteMarker))

	// Version-targeted DELETE of the locked version must be rejected.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(lockedVersionID),
	})
	require.Error(t, err, "version-targeted DELETE of a locked version must be rejected")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}

	// Bypass must succeed.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:                    aws.String(bucketName),
		Key:                       aws.String(objectKey),
		VersionId:                 aws.String(lockedVersionID),
		BypassGovernanceRetention: aws.Bool(true),
	})
	require.NoError(t, err, "bypass header must allow version-targeted DELETE of a GOVERNANCE-locked version")
}

// TestDeleteObjects_VersioningEnabled_PerVersionLockEvaluation verifies the
// S3-canonical batch semantics restored in #39: a version-targeted batch
// delete evaluates the lock per entry, so the locked version surfaces as
// AccessDenied while the unlocked version is removed in the same round trip,
// and the bypass header lifts a GOVERNANCE block.
func TestDeleteObjects_VersioningEnabled_PerVersionLockEvaluation(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, objectKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "v1-locked")
	defer cleanup()

	// Resolve the locked version's id.
	versions, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(objectKey),
	})
	require.NoError(t, err)
	require.NotEmpty(t, versions.Versions)
	lockedVersionID := aws.ToString(versions.Versions[0].VersionId)
	require.NotEmpty(t, lockedVersionID)

	// Add a second unlocked object to confirm only the locked version fails.
	unlockedKey := "unlocked-" + testutil.RandomObjectKey()
	unlockedPut, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(unlockedKey),
		Body:   strings.NewReader("free"),
	})
	require.NoError(t, err)
	unlockedVersionID := aws.ToString(unlockedPut.VersionId)
	require.NotEmpty(t, unlockedVersionID)

	out, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucketName),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String(objectKey), VersionId: aws.String(lockedVersionID)},
				{Key: aws.String(unlockedKey), VersionId: aws.String(unlockedVersionID)},
			},
		},
	})
	require.NoError(t, err)

	// Locked version must surface as an error entry; unlocked version must be
	// in the Deleted list.
	var lockedErrCode string
	for _, e := range out.Errors {
		if e.Key != nil && *e.Key == objectKey && e.Code != nil {
			lockedErrCode = *e.Code
		}
	}
	assert.Equal(t, "AccessDenied", lockedErrCode, "locked version must be reported as AccessDenied")

	var unlockedDeleted bool
	for _, d := range out.Deleted {
		if d.Key != nil && *d.Key == unlockedKey {
			unlockedDeleted = true
		}
	}
	assert.True(t, unlockedDeleted, "unlocked version must still be deleted in the same batch")

	// Bypass on the batch must allow the locked version through.
	out, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket:                    aws.String(bucketName),
		BypassGovernanceRetention: aws.Bool(true),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String(objectKey), VersionId: aws.String(lockedVersionID)},
			},
		},
	})
	require.NoError(t, err)
	var bypassDeleted bool
	for _, d := range out.Deleted {
		if d.Key != nil && *d.Key == objectKey {
			bypassDeleted = true
		}
	}
	assert.True(t, bypassDeleted, "bypass must allow batch delete of the locked version")
}

// TestCopyObject_VersioningEnabled_CreatesNewDestinationVersion verifies the
// S3-canonical semantics restored in #39: a copy onto a key whose current
// version is locked creates a NEW destination version (allowed) and leaves the
// locked version's bytes intact.
func TestCopyObject_VersioningEnabled_CreatesNewDestinationVersion(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, dstKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "destination-original")
	defer cleanup()

	// Resolve the locked destination version.
	versions, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(dstKey),
	})
	require.NoError(t, err)
	require.NotEmpty(t, versions.Versions)
	lockedVersionID := aws.ToString(versions.Versions[0].VersionId)

	srcKey := "src-" + testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(srcKey),
		Body:   strings.NewReader("source-payload"),
	})
	require.NoError(t, err)

	copyOut, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(dstKey),
		CopySource: aws.String(bucketName + "/" + srcKey),
	})
	require.NoError(t, err, "copy creates a new destination version and must be allowed")
	require.NotEmpty(t, aws.ToString(copyOut.VersionId))

	// The locked version's bytes must be unchanged.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(dstKey),
		VersionId: aws.String(lockedVersionID),
	})
	require.NoError(t, err)
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, "destination-original", string(body))
}

// TestCompleteMultipartUpload_VersioningEnabled_CreatesNewVersion verifies the
// S3-canonical semantics restored in #39: completing a multipart upload onto a
// key whose current version is locked creates a NEW version (allowed) and
// leaves the locked version's bytes intact.
func TestCompleteMultipartUpload_VersioningEnabled_CreatesNewVersion(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, objectKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "v1-locked")
	defer cleanup()

	// Resolve the locked version.
	versions, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(objectKey),
	})
	require.NoError(t, err)
	require.NotEmpty(t, versions.Versions)
	lockedVersionID := aws.ToString(versions.Versions[0].VersionId)

	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	partContent := strings.Repeat("y", 5*1024*1024) // 5MB single part
	partResult, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(objectKey),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       strings.NewReader(partContent),
	})
	require.NoError(t, err)

	completeOut, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(objectKey),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{PartNumber: aws.Int32(1), ETag: partResult.ETag},
			},
		},
	})
	require.NoError(t, err, "complete-multipart creates a new version and must be allowed")
	require.NotEmpty(t, aws.ToString(completeOut.VersionId))

	// The locked version's original bytes must be unchanged.
	got, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(objectKey),
		VersionId: aws.String(lockedVersionID),
	})
	require.NoError(t, err)
	defer got.Body.Close()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, "v1-locked", string(body))
}

// TestCopyObject_VersioningEnabled_NonExistentSourceReturnsNoSuchKey verifies
// canonical-error ordering: when the destination is locked but the source
// key does not exist, the response must be NoSuchKey (404), not AccessDenied
// (403). SDK error-handling code branches on the error code, so a regression
// to AccessDenied here would silently break clients that retry on NoSuchKey.
func TestCopyObject_VersioningEnabled_NonExistentSourceReturnsNoSuchKey(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, dstKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "destination-original")
	defer cleanup()

	// src key does NOT exist (bucket exists, key does not).
	_, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(dstKey),
		CopySource: aws.String(bucketName + "/non-existent-src-key"),
	})
	require.Error(t, err)
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchKey", apiErr.ErrorCode(),
			"missing source must surface NoSuchKey, not AccessDenied from the dst lock check")
	}
}

// TestCopyObject_VersioningEnabled_NonExistentSourceBucketReturnsNoSuchBucket
// verifies canonical-error ordering when the source bucket itself does not
// exist: the response must be NoSuchBucket, not AccessDenied.
func TestCopyObject_VersioningEnabled_NonExistentSourceBucketReturnsNoSuchBucket(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, dstKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "destination-original")
	defer cleanup()

	missingSrcBucket := "missing-" + testutil.RandomBucketName()
	_, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(dstKey),
		CopySource: aws.String(missingSrcBucket + "/any-key"),
	})
	require.Error(t, err)
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode(),
			"missing source bucket must surface NoSuchBucket, not AccessDenied from the dst lock check")
	}
}

// TestCompleteMultipartUpload_VersioningEnabled_NonExistentUploadIdReturnsNoSuchUpload
// verifies canonical-error ordering for the multipart finish path: when the
// destination key is locked but the supplied uploadId does not exist (or is
// stale/aborted), the response must be NoSuchUpload, not AccessDenied.
func TestCompleteMultipartUpload_VersioningEnabled_NonExistentUploadIdReturnsNoSuchUpload(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, objectKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "v1-locked")
	defer cleanup()

	// Fabricated uploadId — never created on this bucket+key.
	_, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(objectKey),
		UploadId: aws.String("fabricated-upload-id"),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{PartNumber: aws.Int32(1), ETag: aws.String("\"deadbeef\"")},
			},
		},
	})
	require.Error(t, err)
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchUpload", apiErr.ErrorCode(),
			"non-existent uploadId must surface NoSuchUpload, not AccessDenied from the lock check")
	}
}

// TestObjectRetention_PerVersion verifies the #39 per-version retention model:
// retention set on v1 survives a v2 overwrite, is readable via versionId, and
// a version-targeted delete of v1 is blocked while v2 (unlocked) deletes
// freely. v1 uses a short COMPLIANCE retention so no bypass cleanup is needed.
func TestObjectRetention_PerVersion(t *testing.T) {
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
	defer func() { _, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)}) }()

	// Auto-versioning must be on for an Object Lock bucket (#39).
	vc, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucketName)})
	require.NoError(t, err)
	assert.Equal(t, types.BucketVersioningStatusEnabled, vc.Status, "object lock bucket must auto-enable versioning")

	key := testutil.RandomObjectKey()
	put1, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), Body: strings.NewReader("v1")})
	require.NoError(t, err)
	v1 := aws.ToString(put1.VersionId)
	require.NotEmpty(t, v1)

	retainUntil := time.Now().Add(3 * time.Second).UTC()
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(key),
		VersionId: aws.String(v1),
		Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, RetainUntilDate: aws.Time(retainUntil)},
	})
	require.NoError(t, err)

	// Create v2 (new version) — must not disturb v1's retention.
	put2, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), Body: strings.NewReader("v2")})
	require.NoError(t, err)
	v2 := aws.ToString(put2.VersionId)
	require.NotEmpty(t, v2)

	// GetObjectRetention(v1) must still return the COMPLIANCE retention.
	gotV1, err := client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v1)})
	require.NoError(t, err, "v1 retention must survive the v2 overwrite (#39 defect 2 regression)")
	assert.Equal(t, types.ObjectLockRetentionModeCompliance, gotV1.Retention.Mode)

	// GetObjectRetention(v2) must report no retention configured.
	_, err = client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v2)})
	require.Error(t, err, "v2 must have no retention")
	var noRet smithy.APIError
	if assert.ErrorAs(t, err, &noRet) {
		assert.Equal(t, "NoSuchObjectLockConfiguration", noRet.ErrorCode())
	}

	// Deleting v1 must be blocked (COMPLIANCE, no bypass possible).
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v1)})
	require.Error(t, err, "v1 delete must be blocked while under COMPLIANCE retention")
	var delErr smithy.APIError
	if assert.ErrorAs(t, err, &delErr) {
		assert.Equal(t, "AccessDenied", delErr.ErrorCode())
	}

	// Deleting v2 (unlocked) must succeed.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v2)})
	require.NoError(t, err, "v2 delete must succeed (no lock)")
}

// TestObjectLegalHold_PerVersion verifies per-version legal hold: a hold set on
// v1 is readable by versionId, blocks a version-targeted delete of v1, and is
// independent of v2. The hold is turned OFF at the end so cleanup can proceed.
func TestObjectLegalHold_PerVersion(t *testing.T) {
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

	key := testutil.RandomObjectKey()
	put1, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), Body: strings.NewReader("v1")})
	require.NoError(t, err)
	v1 := aws.ToString(put1.VersionId)
	put2, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), Body: strings.NewReader("v2")})
	require.NoError(t, err)
	v2 := aws.ToString(put2.VersionId)

	defer func() {
		_, _ = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
			Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v1),
			LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff},
		})
		_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v1)})
		_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v2)})
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	}()

	// Legal hold ON v1 only.
	_, err = client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
		Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v1),
		LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn},
	})
	require.NoError(t, err)

	holdV1, err := client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v1)})
	require.NoError(t, err)
	assert.Equal(t, types.ObjectLockLegalHoldStatusOn, holdV1.LegalHold.Status)

	// v2 has no legal hold.
	_, err = client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v2)})
	require.Error(t, err, "v2 must have no legal hold")

	// Delete of v1 blocked; v2 not (legal hold is per-version).
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), VersionId: aws.String(v1)})
	require.Error(t, err, "v1 delete must be blocked by legal hold")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}
}

// TestDeleteMarker_CoexistsWithProtectedVersion verifies that creating a delete
// marker over a protected version is allowed, the marker hides the object from
// a plain GET, ListObjectVersions still shows both the marker and the protected
// version, and a version-targeted delete of the protected version stays blocked.
func TestDeleteMarker_CoexistsWithProtectedVersion(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName, objectKey, _, cleanup := versioningEnabledLockedBucket(t, ts, "protected-v1")
	defer cleanup()

	versions, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucketName), Prefix: aws.String(objectKey)})
	require.NoError(t, err)
	require.NotEmpty(t, versions.Versions)
	protectedVersionID := aws.ToString(versions.Versions[0].VersionId)

	// Plain DELETE creates a delete marker (allowed even though v1 is locked).
	dm, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: aws.String(objectKey)})
	require.NoError(t, err)
	assert.True(t, aws.ToBool(dm.DeleteMarker))

	// Plain GET now returns 404 (the marker is the latest version).
	_, err = client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucketName), Key: aws.String(objectKey)})
	require.Error(t, err, "GET must 404 once a delete marker is the latest version")

	// ListObjectVersions still shows the protected version and the marker.
	after, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucketName), Prefix: aws.String(objectKey)})
	require.NoError(t, err)
	foundProtected := false
	for _, v := range after.Versions {
		if aws.ToString(v.VersionId) == protectedVersionID {
			foundProtected = true
		}
	}
	assert.True(t, foundProtected, "protected version must still be listed alongside the delete marker")
	assert.NotEmpty(t, after.DeleteMarkers, "delete marker must be listed")

	// Version-targeted delete of the protected version is still blocked.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucketName), Key: aws.String(objectKey), VersionId: aws.String(protectedVersionID)})
	require.Error(t, err, "protected version must remain undeletable behind the marker")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "AccessDenied", apiErr.ErrorCode())
	}
}

// TestVersioningSuspend_RejectedOnLockBucket verifies an Object Lock bucket
// rejects PutBucketVersioning(Suspended) with InvalidBucketState (#39).
func TestVersioningSuspend_RejectedOnLockBucket(t *testing.T) {
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
	defer func() { _, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)}) }()

	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusSuspended},
	})
	require.Error(t, err, "suspending versioning on an Object Lock bucket must be rejected")
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "InvalidBucketState", apiErr.ErrorCode())
	}
}

// TestGetObjectRetention_NonExistentVersionReturnsNoSuchVersion verifies the
// versionId resolution: a retention GET against a fabricated versionId surfaces
// NoSuchVersion (#39), not NoSuchKey.
func TestGetObjectRetention_NonExistentVersionReturnsNoSuchVersion(t *testing.T) {
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
	defer func() { _, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)}) }()

	key := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key), Body: strings.NewReader("v1")})
	require.NoError(t, err)

	_, err = client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(key),
		VersionId: aws.String("fabricated-version-id"),
	})
	require.Error(t, err)
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchVersion", apiErr.ErrorCode())
	}
}

func TestObjectLockNullVersionOnVersionOnlyKeyReturnsNoSuchKey(t *testing.T) {
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
	defer func() { _, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)}) }()

	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	key := testutil.RandomObjectKey()
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("v1"),
	})
	require.NoError(t, err)

	retainUntil := time.Now().Add(24 * time.Hour)
	_, err = client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(key),
		VersionId: aws.String("null"),
		Retention: &types.ObjectLockRetention{
			Mode:            types.ObjectLockRetentionModeGovernance,
			RetainUntilDate: aws.Time(retainUntil),
		},
	})
	require.Error(t, err)
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchKey", apiErr.ErrorCode())
	}

	_, err = client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(key),
		VersionId: aws.String("null"),
	})
	require.Error(t, err)
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchKey", apiErr.ErrorCode())
	}
}
