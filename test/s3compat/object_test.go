package s3compat

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	content := "Hello, World!"

	result, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        strings.NewReader(content),
		ContentType: aws.String("text/plain"),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.ETag)
}

func TestPutObjectWithMetadata(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	content := "Hello, World!"
	metadata := map[string]string{
		"custom-key": "custom-value",
		"another":    "metadata",
	}

	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        strings.NewReader(content),
		ContentType: aws.String("text/plain"),
		Metadata:    metadata,
	})
	require.NoError(t, err)

	// Verify metadata
	headResult, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	assert.Equal(t, "custom-value", headResult.Metadata["custom-key"])
	assert.Equal(t, "metadata", headResult.Metadata["another"])
}

func TestGetObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	content := "Hello, World! This is test content."

	// Put object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        strings.NewReader(content),
		ContentType: aws.String("text/plain"),
	})
	require.NoError(t, err)

	// Get object
	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	// Read content
	body, err := io.ReadAll(result.Body)
	require.NoError(t, err)

	assert.Equal(t, content, string(body))
	assert.Equal(t, "text/plain", *result.ContentType)
	assert.Equal(t, int64(len(content)), *result.ContentLength)
	assert.NotEmpty(t, result.ETag)
}

func TestGetObjectNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get non-existent object
	_, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("non-existent-key"),
	})
	require.Error(t, err)

	var noSuchKey *types.NoSuchKey
	assert.ErrorAs(t, err, &noSuchKey)
}

func TestGetObjectRange(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	content := "0123456789ABCDEF"

	// Put object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader(content),
	})
	require.NoError(t, err)

	// Get range
	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Range:  aws.String("bytes=0-4"),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	body, err := io.ReadAll(result.Body)
	require.NoError(t, err)

	assert.Equal(t, "01234", string(body))
}

func TestHeadObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	content := "Hello, World!"

	// Put object
	putResult, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        strings.NewReader(content),
		ContentType: aws.String("text/plain"),
	})
	require.NoError(t, err)

	// Head object
	headResult, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	assert.Equal(t, int64(len(content)), *headResult.ContentLength)
	assert.Equal(t, "text/plain", *headResult.ContentType)
	assert.Equal(t, *putResult.ETag, *headResult.ETag)
}

func TestHeadObjectNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Head non-existent object
	_, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("non-existent-key"),
	})
	require.Error(t, err)

	var notFound *types.NotFound
	assert.ErrorAs(t, err, &notFound)
}

func TestDeleteObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Put object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("content"),
	})
	require.NoError(t, err)

	// Delete object
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Verify object no longer exists
	_, err = client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.Error(t, err)
}

func TestDeleteObjectNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Delete non-existent object (S3 returns success)
	_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("non-existent-key"),
	})
	require.NoError(t, err)
}

func TestListObjectsV2(t *testing.T) {
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

	// List objects
	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	assert.Equal(t, int32(3), *result.KeyCount)
	assert.Len(t, result.Contents, 3)

	foundKeys := make(map[string]bool)
	for _, obj := range result.Contents {
		foundKeys[*obj.Key] = true
	}
	for _, key := range keys {
		assert.True(t, foundKeys[key], "key %s should be in list", key)
	}
}

func TestListObjectsV2Prefix(t *testing.T) {
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
	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
		Prefix: aws.String("images/"),
	})
	require.NoError(t, err)

	assert.Equal(t, int32(2), *result.KeyCount)
	for _, obj := range result.Contents {
		assert.True(t, strings.HasPrefix(*obj.Key, "images/"))
	}
}

func TestListObjectsV2Delimiter(t *testing.T) {
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
	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucketName),
		Delimiter: aws.String("/"),
	})
	require.NoError(t, err)

	// Should have 1 object (root.txt) and 2 common prefixes (images/, docs/)
	assert.Len(t, result.Contents, 1)
	assert.Equal(t, "root.txt", *result.Contents[0].Key)
	assert.Len(t, result.CommonPrefixes, 2)
}

func TestListObjectsV2Pagination(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Create multiple objects
	for i := 0; i < 5; i++ {
		key := testutil.RandomObjectKey()
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   strings.NewReader("content"),
		})
		require.NoError(t, err)
	}

	// List with max keys
	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucketName),
		MaxKeys: aws.Int32(2),
	})
	require.NoError(t, err)

	assert.Equal(t, int32(2), *result.MaxKeys)
	assert.Len(t, result.Contents, 2)
	require.NotNil(t, result.IsTruncated)
	assert.True(t, *result.IsTruncated)
	require.NotNil(t, result.NextContinuationToken)
	assert.NotEmpty(t, *result.NextContinuationToken)

	// Get next page
	result2, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:            aws.String(bucketName),
		MaxKeys:           aws.Int32(2),
		ContinuationToken: result.NextContinuationToken,
	})
	require.NoError(t, err)

	assert.Len(t, result2.Contents, 2)
}

func TestPutGetLargeObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create 1MB content
	content := bytes.Repeat([]byte("x"), 1024*1024)

	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   bytes.NewReader(content),
	})
	require.NoError(t, err)

	// Get object
	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	body, err := io.ReadAll(result.Body)
	require.NoError(t, err)

	assert.Equal(t, len(content), len(body))
	assert.Equal(t, content, body)
}

func TestCopyObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	srcBucket := testutil.RandomBucketName()
	dstBucket := testutil.RandomBucketName()
	cleanupSrc := ts.CreateTestBucket(t, srcBucket)
	defer cleanupSrc()
	cleanupDst := ts.CreateTestBucket(t, dstBucket)
	defer cleanupDst()

	srcKey := testutil.RandomObjectKey()
	dstKey := testutil.RandomObjectKey()
	content := "Hello, CopyObject!"

	// Put source object
	putResult, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(srcBucket),
		Key:         aws.String(srcKey),
		Body:        strings.NewReader(content),
		ContentType: aws.String("text/plain"),
	})
	require.NoError(t, err)

	// Copy object
	copyResult, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(srcBucket + "/" + srcKey),
	})
	require.NoError(t, err)
	assert.NotNil(t, copyResult.CopyObjectResult)
	assert.NotNil(t, copyResult.CopyObjectResult.ETag)
	assert.NotNil(t, copyResult.CopyObjectResult.LastModified)

	// Verify copied object
	getResult, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(dstBucket),
		Key:    aws.String(dstKey),
	})
	require.NoError(t, err)
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	require.NoError(t, err)

	assert.Equal(t, content, string(body))
	assert.Equal(t, "text/plain", *getResult.ContentType)
	assert.Equal(t, *putResult.ETag, *getResult.ETag)
}

func TestCopyObjectSameBucket(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	srcKey := testutil.RandomObjectKey()
	dstKey := testutil.RandomObjectKey()
	content := "Hello, Same Bucket Copy!"

	// Put source object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(srcKey),
		Body:   strings.NewReader(content),
	})
	require.NoError(t, err)

	// Copy object within same bucket
	copyResult, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(dstKey),
		CopySource: aws.String(bucketName + "/" + srcKey),
	})
	require.NoError(t, err)
	assert.NotNil(t, copyResult.CopyObjectResult)

	// Verify copied object
	getResult, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(dstKey),
	})
	require.NoError(t, err)
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	require.NoError(t, err)

	assert.Equal(t, content, string(body))
}

func TestCopyObjectWithMetadata(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	srcKey := testutil.RandomObjectKey()
	dstKey := testutil.RandomObjectKey()
	content := "Hello, Metadata!"

	// Put source object with metadata
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(srcKey),
		Body:   strings.NewReader(content),
		Metadata: map[string]string{
			"original": "metadata",
		},
	})
	require.NoError(t, err)

	// Copy object with new metadata (REPLACE directive)
	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(bucketName),
		Key:               aws.String(dstKey),
		CopySource:        aws.String(bucketName + "/" + srcKey),
		MetadataDirective: types.MetadataDirectiveReplace,
		Metadata: map[string]string{
			"new": "metadata",
		},
	})
	require.NoError(t, err)

	// Verify new metadata
	headResult, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(dstKey),
	})
	require.NoError(t, err)

	assert.Equal(t, "metadata", headResult.Metadata["new"])
	assert.NotContains(t, headResult.Metadata, "original")
}

func TestCopyObjectSourceNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	dstKey := testutil.RandomObjectKey()

	// Try to copy non-existent object
	_, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(dstKey),
		CopySource: aws.String(bucketName + "/non-existent-key"),
	})
	require.Error(t, err)

	// Check for NoSuchKey error - use the same pattern as multipart tests
	// AWS SDK v2 wraps errors differently for CopyObject
	assert.Contains(t, err.Error(), "NoSuchKey")
}

func TestDeleteObjects(t *testing.T) {
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

	// Delete multiple objects
	result, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucketName),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String("object1.txt")},
				{Key: aws.String("object2.txt")},
				{Key: aws.String("object3.txt")},
			},
		},
	})
	require.NoError(t, err)

	// Verify all objects were deleted
	assert.Len(t, result.Deleted, 3)
	assert.Len(t, result.Errors, 0)

	// Verify objects no longer exist
	for _, key := range keys {
		_, err := client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
		})
		require.Error(t, err)
	}
}

func TestDeleteObjectsPartialError(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Create only some objects
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("object1.txt"),
		Body:   strings.NewReader("content"),
	})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("object3.txt"),
		Body:   strings.NewReader("content"),
	})
	require.NoError(t, err)

	// Try to delete both existing and non-existing objects
	// S3 behavior: non-existing objects are treated as successfully deleted
	result, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucketName),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String("object1.txt")},
				{Key: aws.String("object2.txt")}, // does not exist
				{Key: aws.String("object3.txt")},
			},
		},
	})
	require.NoError(t, err)

	// All should be reported as deleted (S3 behavior)
	assert.Len(t, result.Deleted, 3)
	assert.Len(t, result.Errors, 0)
}

func TestDeleteObjectsBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Try to delete objects from non-existing bucket
	_, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String("non-existing-bucket"),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String("object1.txt")},
			},
		},
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}

func TestGetObjectAttributes(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	content := "Hello, GetObjectAttributes!"

	// Put object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        strings.NewReader(content),
		ContentType: aws.String("text/plain"),
	})
	require.NoError(t, err)

	// Get object attributes
	result, err := client.GetObjectAttributes(ctx, &s3.GetObjectAttributesInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		ObjectAttributes: []types.ObjectAttributes{
			types.ObjectAttributesEtag,
			types.ObjectAttributesObjectSize,
			types.ObjectAttributesStorageClass,
		},
	})
	require.NoError(t, err)

	// Verify attributes
	require.NotNil(t, result.ETag)
	require.NotNil(t, result.ObjectSize)
	assert.Equal(t, int64(len(content)), *result.ObjectSize)
	assert.Equal(t, types.StorageClassStandard, result.StorageClass)
}

func TestGetObjectAttributesNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get attributes for non-existent object
	_, err := client.GetObjectAttributes(ctx, &s3.GetObjectAttributesInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("non-existent-key"),
		ObjectAttributes: []types.ObjectAttributes{
			types.ObjectAttributesEtag,
		},
	})
	require.Error(t, err)

	var noSuchKey *types.NoSuchKey
	assert.ErrorAs(t, err, &noSuchKey)
}

func TestGetObjectAttributesBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Get attributes from non-existent bucket
	_, err := client.GetObjectAttributes(ctx, &s3.GetObjectAttributesInput{
		Bucket: aws.String("non-existent-bucket"),
		Key:    aws.String("some-key"),
		ObjectAttributes: []types.ObjectAttributes{
			types.ObjectAttributesEtag,
		},
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}
