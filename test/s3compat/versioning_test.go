package s3compat

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutBucketVersioning(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Enable versioning
	_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	// Verify versioning is enabled
	result, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
	assert.Equal(t, types.BucketVersioningStatusEnabled, result.Status)
}

func TestPutBucketVersioningSuspend(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Enable versioning
	_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	// Suspend versioning
	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusSuspended,
		},
	})
	require.NoError(t, err)

	// Verify versioning is suspended
	result, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
	assert.Equal(t, types.BucketVersioningStatusSuspended, result.Status)
}

func TestGetBucketVersioning(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get versioning status for a bucket that has never had versioning enabled
	result, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Status should be empty (never enabled)
	assert.Empty(t, result.Status)
}

func TestPutObjectVersioned(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Enable versioning
	_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	key := testutil.RandomObjectKey()

	// Put first version
	result1, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("version 1"),
	})
	require.NoError(t, err)
	assert.NotNil(t, result1.VersionId)
	assert.NotEmpty(t, *result1.VersionId)
	version1 := *result1.VersionId

	// Put second version
	result2, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("version 2"),
	})
	require.NoError(t, err)
	assert.NotNil(t, result2.VersionId)
	assert.NotEmpty(t, *result2.VersionId)
	version2 := *result2.VersionId

	// Versions should be different
	assert.NotEqual(t, version1, version2)
}

func TestGetObjectVersioned(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Enable versioning
	_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	key := testutil.RandomObjectKey()

	// Put first version
	result1, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("version 1"),
	})
	require.NoError(t, err)
	version1 := *result1.VersionId

	// Put second version
	result2, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("version 2"),
	})
	require.NoError(t, err)
	version2 := *result2.VersionId

	// Get specific version (first)
	getResult1, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(key),
		VersionId: aws.String(version1),
	})
	require.NoError(t, err)
	defer getResult1.Body.Close()
	body1, _ := io.ReadAll(getResult1.Body)
	assert.Equal(t, "version 1", string(body1))

	// Get specific version (second)
	getResult2, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(key),
		VersionId: aws.String(version2),
	})
	require.NoError(t, err)
	defer getResult2.Body.Close()
	body2, _ := io.ReadAll(getResult2.Body)
	assert.Equal(t, "version 2", string(body2))

	// Get without version (should return latest)
	getResultLatest, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer getResultLatest.Body.Close()
	bodyLatest, _ := io.ReadAll(getResultLatest.Body)
	assert.Equal(t, "version 2", string(bodyLatest))
}

func TestDeleteObjectVersioned(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Enable versioning
	_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	key := testutil.RandomObjectKey()

	// Put object
	putResult, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("content"),
	})
	require.NoError(t, err)
	version := *putResult.VersionId

	// Delete without version ID (should create delete marker)
	deleteResult, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	assert.NotNil(t, deleteResult.VersionId)
	assert.True(t, *deleteResult.DeleteMarker)

	// Get object without version should fail
	_, err = client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.Error(t, err)

	// Get specific version should still work
	getResult, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(bucketName),
		Key:       aws.String(key),
		VersionId: aws.String(version),
	})
	require.NoError(t, err)
	defer getResult.Body.Close()
	body, _ := io.ReadAll(getResult.Body)
	assert.Equal(t, "content", string(body))
}

func TestListObjectVersions(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Enable versioning
	_, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	key := testutil.RandomObjectKey()

	// Put multiple versions
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("version 1"),
	})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("version 2"),
	})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("version 3"),
	})
	require.NoError(t, err)

	// List versions
	result, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Should have 3 versions
	assert.Len(t, result.Versions, 3)

	// Verify all versions have the same key
	for _, v := range result.Versions {
		assert.Equal(t, key, *v.Key)
		assert.NotEmpty(t, *v.VersionId)
	}
}
