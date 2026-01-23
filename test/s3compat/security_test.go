package s3compat

import (
	"context"
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

// TestPathTraversalPutObject tests that path traversal attacks are prevented on PutObject.
func TestPathTraversalPutObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	testCases := []struct {
		name string
		key  string
	}{
		{"parent directory traversal", "../../../etc/passwd"},
		{"parent directory in middle", "foo/../../../etc/passwd"},
		{"double dot only", ".."},
		{"hidden double dot", "foo/.."},
		{"mixed traversal", "normal/../../../secret"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(tc.key),
				Body:   strings.NewReader("malicious content"),
			})
			require.Error(t, err, "expected error for key: %s", tc.key)

			// Should return an error indicating invalid key
			var apiErr smithy.APIError
			if assert.ErrorAs(t, err, &apiErr) {
				// S3 returns InvalidArgument or similar for invalid keys
				assert.Contains(t, []string{"InvalidArgument", "InvalidKey", "AccessDenied"}, apiErr.ErrorCode(),
					"unexpected error code: %s", apiErr.ErrorCode())
			}
		})
	}
}

// TestPathTraversalGetObject tests that path traversal attacks are prevented on GetObject.
func TestPathTraversalGetObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	testCases := []struct {
		name string
		key  string
	}{
		{"parent directory traversal", "../../../etc/passwd"},
		{"parent directory in middle", "foo/../../../etc/passwd"},
		{"double dot only", ".."},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(tc.key),
			})
			require.Error(t, err, "expected error for key: %s", tc.key)
		})
	}
}

// TestPathTraversalDeleteObject tests that path traversal attacks are prevented on DeleteObject.
func TestPathTraversalDeleteObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	testCases := []struct {
		name string
		key  string
	}{
		{"parent directory traversal", "../../../etc/passwd"},
		{"parent directory in middle", "foo/../../../etc/passwd"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(tc.key),
			})
			require.Error(t, err, "expected error for key: %s", tc.key)
		})
	}
}

// TestPathTraversalCopyObject tests that path traversal attacks are prevented on CopyObject.
func TestPathTraversalCopyObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Create a valid source object
	srcKey := testutil.RandomObjectKey()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(srcKey),
		Body:   strings.NewReader("source content"),
	})
	require.NoError(t, err)

	testCases := []struct {
		name       string
		srcKey     string
		dstKey     string
		expectFail bool
	}{
		{"traversal in destination key", srcKey, "../../../etc/passwd", true},
		{"traversal in source key", "../../../etc/passwd", "valid-dst", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			copySource := bucketName + "/" + tc.srcKey
			_, err := client.CopyObject(ctx, &s3.CopyObjectInput{
				Bucket:     aws.String(bucketName),
				Key:        aws.String(tc.dstKey),
				CopySource: aws.String(copySource),
			})
			if tc.expectFail {
				require.Error(t, err, "expected error for copy operation")
			}
		})
	}
}

// TestPathTraversalHeadObject tests that path traversal attacks are prevented on HeadObject.
func TestPathTraversalHeadObject(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	_, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("../../../etc/passwd"),
	})
	require.Error(t, err, "expected error for path traversal in HeadObject")
}

// TestPathTraversalMultipartUpload tests that path traversal attacks are prevented on multipart uploads.
func TestPathTraversalMultipartUpload(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Test CreateMultipartUpload with traversal key
	_, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("../../../etc/passwd"),
	})
	require.Error(t, err, "expected error for path traversal in CreateMultipartUpload")
}

// TestValidObjectKeysWithDots tests that valid object keys containing dots are allowed.
func TestValidObjectKeysWithDots(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// These keys contain dots but should be valid
	validKeys := []string{
		"file.txt",
		"folder/file.txt",
		".hidden",
		"folder/.hidden",
		"a.b.c.d",
		"...not-traversal",
	}

	for _, key := range validKeys {
		t.Run(key, func(t *testing.T) {
			content := "valid content"
			_, err := client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(key),
				Body:   strings.NewReader(content),
			})
			require.NoError(t, err, "expected success for valid key: %s", key)

			// Verify the object can be retrieved
			getResult, err := client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(key),
			})
			require.NoError(t, err)
			getResult.Body.Close()
		})
	}
}

// TestPathTraversalDeleteObjects tests that path traversal attacks are prevented on DeleteObjects.
func TestPathTraversalDeleteObjects(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Try to delete with path traversal keys
	// DeleteObjects returns partial success - traversal keys should be in Errors list
	result, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucketName),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String("../../../etc/passwd")},
				{Key: aws.String("normal-key")},
			},
		},
	})
	require.NoError(t, err, "DeleteObjects should return partial success, not error")

	// Check that traversal key is in Errors
	assert.Len(t, result.Errors, 1, "expected 1 error for traversal key")
	if len(result.Errors) > 0 {
		assert.Equal(t, "../../../etc/passwd", *result.Errors[0].Key)
		assert.Equal(t, "InvalidArgument", *result.Errors[0].Code)
	}

	// Check that normal-key is in Deleted
	assert.Len(t, result.Deleted, 1, "expected 1 deleted key")
	if len(result.Deleted) > 0 {
		assert.Equal(t, "normal-key", *result.Deleted[0].Key)
	}
}
