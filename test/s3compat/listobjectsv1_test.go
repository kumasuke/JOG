package s3compat

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListObjects(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Create multiple objects
	keys := []string{"object1.txt", "object2.txt", "object3.txt"}
	for _, key := range keys {
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   strings.NewReader("content"),
		})
		require.NoError(t, err)
	}

	// List objects using v1 API
	result, err := client.ListObjects(ctx, &s3.ListObjectsInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	assert.Len(t, result.Contents, 3)
	assert.False(t, *result.IsTruncated)

	foundKeys := make(map[string]bool)
	for _, obj := range result.Contents {
		foundKeys[*obj.Key] = true
	}
	for _, key := range keys {
		assert.True(t, foundKeys[key], "key %s should be in list", key)
	}
}

func TestListObjectsPrefix(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Create objects with different prefixes
	keys := []string{
		"images/photo1.jpg",
		"images/photo2.jpg",
		"docs/file1.txt",
		"docs/file2.txt",
	}
	for _, key := range keys {
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   strings.NewReader("content"),
		})
		require.NoError(t, err)
	}

	// List with prefix
	result, err := client.ListObjects(ctx, &s3.ListObjectsInput{
		Bucket: aws.String(bucketName),
		Prefix: aws.String("images/"),
	})
	require.NoError(t, err)

	assert.Len(t, result.Contents, 2)
	for _, obj := range result.Contents {
		assert.True(t, strings.HasPrefix(*obj.Key, "images/"))
	}
}

func TestListObjectsDelimiter(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Create objects with folder structure
	keys := []string{
		"images/photo1.jpg",
		"images/photo2.jpg",
		"docs/file1.txt",
		"root.txt",
	}
	for _, key := range keys {
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   strings.NewReader("content"),
		})
		require.NoError(t, err)
	}

	// List with delimiter
	result, err := client.ListObjects(ctx, &s3.ListObjectsInput{
		Bucket:    aws.String(bucketName),
		Delimiter: aws.String("/"),
	})
	require.NoError(t, err)

	// Should have 1 object (root.txt) and 2 common prefixes (images/, docs/)
	assert.Len(t, result.Contents, 1)
	assert.Equal(t, "root.txt", *result.Contents[0].Key)
	assert.Len(t, result.CommonPrefixes, 2)
}

func TestListObjectsPagination(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Create objects with sorted names for predictable pagination
	keys := []string{"obj-a", "obj-b", "obj-c", "obj-d", "obj-e"}
	for _, key := range keys {
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   strings.NewReader("content"),
		})
		require.NoError(t, err)
	}

	// List with max keys
	result, err := client.ListObjects(ctx, &s3.ListObjectsInput{
		Bucket:  aws.String(bucketName),
		MaxKeys: aws.Int32(2),
	})
	require.NoError(t, err)

	assert.Len(t, result.Contents, 2)
	require.NotNil(t, result.IsTruncated)
	assert.True(t, *result.IsTruncated)
	require.NotNil(t, result.NextMarker)
	assert.NotEmpty(t, *result.NextMarker)

	// Get next page using Marker
	result2, err := client.ListObjects(ctx, &s3.ListObjectsInput{
		Bucket:  aws.String(bucketName),
		MaxKeys: aws.Int32(2),
		Marker:  result.NextMarker,
	})
	require.NoError(t, err)

	assert.Len(t, result2.Contents, 2)
	// Should continue from where we left off
	assert.NotEqual(t, result.Contents[0].Key, result2.Contents[0].Key)
}

func TestListObjectsBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// List objects from non-existing bucket
	_, err := client.ListObjects(ctx, &s3.ListObjectsInput{
		Bucket: aws.String("non-existing-bucket"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NoSuchBucket")
}

func TestListObjectsEmptyBucket(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// List objects from empty bucket
	result, err := client.ListObjects(ctx, &s3.ListObjectsInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	assert.Len(t, result.Contents, 0)
	assert.False(t, *result.IsTruncated)
}
