package s3compat

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateMultipartUpload(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	result, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		ContentType: aws.String("application/octet-stream"),
	})
	require.NoError(t, err)
	require.NotNil(t, result.UploadId)
	assert.NotEmpty(t, *result.UploadId)
	assert.Equal(t, bucketName, *result.Bucket)
	assert.Equal(t, key, *result.Key)

	// Cleanup - abort the upload
	_, err = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: result.UploadId,
	})
	require.NoError(t, err)
}

func TestCreateMultipartUploadWithMetadata(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	metadata := map[string]string{
		"custom-key": "custom-value",
	}

	// Create multipart upload with metadata
	result, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		ContentType: aws.String("text/plain"),
		Metadata:    metadata,
	})
	require.NoError(t, err)
	require.NotNil(t, result.UploadId)

	// Cleanup
	_, _ = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: result.UploadId,
	})
}

func TestCreateMultipartUploadBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Create multipart upload on non-existent bucket
	_, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String("non-existent-bucket"),
		Key:    aws.String("test-key"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}

func TestUploadPart(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Upload part
	partContent := bytes.Repeat([]byte("a"), 5*1024*1024) // 5MB part
	partResult, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(partContent),
	})
	require.NoError(t, err)
	require.NotNil(t, partResult.ETag)
	assert.NotEmpty(t, *partResult.ETag)

	// Cleanup
	_, _ = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
	})
}

func TestUploadPartInvalidUploadId(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Upload part with invalid upload ID
	_, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String("test-key"),
		UploadId:   aws.String("invalid-upload-id"),
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader([]byte("content")),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchUpload", apiErr.ErrorCode())
	}
}

func TestCompleteMultipartUpload(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		ContentType: aws.String("application/octet-stream"),
	})
	require.NoError(t, err)

	// Upload two parts (minimum part size is 5MB except for the last part)
	part1Content := bytes.Repeat([]byte("a"), 5*1024*1024) // 5MB
	part1Result, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(part1Content),
	})
	require.NoError(t, err)

	part2Content := bytes.Repeat([]byte("b"), 1024) // 1KB (last part can be smaller)
	part2Result, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(2),
		Body:       bytes.NewReader(part2Content),
	})
	require.NoError(t, err)

	// Complete multipart upload
	completeResult, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{
					PartNumber: aws.Int32(1),
					ETag:       part1Result.ETag,
				},
				{
					PartNumber: aws.Int32(2),
					ETag:       part2Result.ETag,
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, bucketName, *completeResult.Bucket)
	assert.Equal(t, key, *completeResult.Key)
	require.NotNil(t, completeResult.ETag)

	// Verify the object exists and can be retrieved
	getResult, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	require.NoError(t, err)

	// Verify content is concatenated correctly
	expectedContent := append(part1Content, part2Content...)
	assert.Equal(t, len(expectedContent), len(body))
	assert.Equal(t, expectedContent, body)
}

func TestCompleteMultipartUploadSinglePart(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Upload single part
	partContent := []byte("Hello, multipart world!")
	partResult, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(partContent),
	})
	require.NoError(t, err)

	// Complete multipart upload
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{
					PartNumber: aws.Int32(1),
					ETag:       partResult.ETag,
				},
			},
		},
	})
	require.NoError(t, err)

	// Verify content
	getResult, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	require.NoError(t, err)
	assert.Equal(t, partContent, body)
}

func TestAbortMultipartUpload(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Upload a part
	_, err = client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader([]byte("test content")),
	})
	require.NoError(t, err)

	// Abort the upload
	_, err = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
	})
	require.NoError(t, err)

	// Verify upload no longer exists (ListParts should fail)
	_, err = client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchUpload", apiErr.ErrorCode())
	}
}

func TestAbortMultipartUploadInvalidUploadId(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Abort with invalid upload ID
	_, err := client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String("test-key"),
		UploadId: aws.String("invalid-upload-id"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchUpload", apiErr.ErrorCode())
	}
}

func TestListParts(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Upload multiple parts
	part1Content := bytes.Repeat([]byte("a"), 5*1024*1024) // 5MB
	part1Result, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(part1Content),
	})
	require.NoError(t, err)

	part2Content := bytes.Repeat([]byte("b"), 3*1024*1024) // 3MB
	part2Result, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(2),
		Body:       bytes.NewReader(part2Content),
	})
	require.NoError(t, err)

	// List parts
	listResult, err := client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
	})
	require.NoError(t, err)

	require.Len(t, listResult.Parts, 2)

	// Verify part 1
	assert.Equal(t, int32(1), *listResult.Parts[0].PartNumber)
	assert.Equal(t, int64(5*1024*1024), *listResult.Parts[0].Size)
	assert.Equal(t, *part1Result.ETag, *listResult.Parts[0].ETag)

	// Verify part 2
	assert.Equal(t, int32(2), *listResult.Parts[1].PartNumber)
	assert.Equal(t, int64(3*1024*1024), *listResult.Parts[1].Size)
	assert.Equal(t, *part2Result.ETag, *listResult.Parts[1].ETag)

	// Cleanup
	_, _ = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
	})
}

func TestListPartsInvalidUploadId(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// List parts with invalid upload ID
	_, err := client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String("test-key"),
		UploadId: aws.String("invalid-upload-id"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchUpload", apiErr.ErrorCode())
	}
}

func TestListPartsPagination(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Upload multiple parts
	for i := 1; i <= 5; i++ {
		_, err = client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(bucketName),
			Key:        aws.String(key),
			UploadId:   createResult.UploadId,
			PartNumber: aws.Int32(int32(i)),
			Body:       bytes.NewReader([]byte("content")),
		})
		require.NoError(t, err)
	}

	// List parts with max parts
	listResult, err := client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
		MaxParts: aws.Int32(2),
	})
	require.NoError(t, err)

	assert.Len(t, listResult.Parts, 2)
	require.NotNil(t, listResult.IsTruncated)
	assert.True(t, *listResult.IsTruncated)
	require.NotNil(t, listResult.NextPartNumberMarker)

	// Get next page
	listResult2, err := client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:           aws.String(bucketName),
		Key:              aws.String(key),
		UploadId:         createResult.UploadId,
		MaxParts:         aws.Int32(2),
		PartNumberMarker: listResult.NextPartNumberMarker,
	})
	require.NoError(t, err)

	assert.Len(t, listResult2.Parts, 2)

	// Cleanup
	_, _ = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
	})
}

func TestMultipartUploadWithMetadata(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()
	metadata := map[string]string{
		"custom-key": "custom-value",
		"another":    "metadata",
	}

	// Create multipart upload with metadata
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		ContentType: aws.String("text/plain"),
		Metadata:    metadata,
	})
	require.NoError(t, err)

	// Upload a part
	partContent := []byte("Hello, World!")
	partResult, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(partContent),
	})
	require.NoError(t, err)

	// Complete the upload
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{
					PartNumber: aws.Int32(1),
					ETag:       partResult.ETag,
				},
			},
		},
	})
	require.NoError(t, err)

	// Verify metadata is preserved
	headResult, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	assert.Equal(t, "custom-value", headResult.Metadata["custom-key"])
	assert.Equal(t, "metadata", headResult.Metadata["another"])
	assert.Equal(t, "text/plain", *headResult.ContentType)
}

func TestMultipartUploadETagFormat(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Upload parts
	var completedParts []types.CompletedPart
	for i := 1; i <= 3; i++ {
		partContent := bytes.Repeat([]byte{byte('a' + i - 1)}, 1024)
		partResult, err := client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(bucketName),
			Key:        aws.String(key),
			UploadId:   createResult.UploadId,
			PartNumber: aws.Int32(int32(i)),
			Body:       bytes.NewReader(partContent),
		})
		require.NoError(t, err)

		// Verify individual part ETag is proper MD5
		etag := *partResult.ETag
		// Remove quotes if present
		if etag[0] == '"' {
			etag = etag[1 : len(etag)-1]
		}
		_, err = hex.DecodeString(etag)
		require.NoError(t, err, "Part ETag should be valid hex")

		// Verify ETag matches MD5 of content
		hash := md5.Sum(partContent)
		expectedETag := hex.EncodeToString(hash[:])
		assert.Equal(t, expectedETag, etag)

		completedParts = append(completedParts, types.CompletedPart{
			PartNumber: aws.Int32(int32(i)),
			ETag:       partResult.ETag,
		})
	}

	// Complete multipart upload
	completeResult, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	require.NoError(t, err)

	// Multipart upload ETag format is: "md5-of-md5s-numParts"
	etag := *completeResult.ETag
	if etag[0] == '"' {
		etag = etag[1 : len(etag)-1]
	}
	// Should end with -3 (3 parts)
	assert.Contains(t, etag, "-3")
}

func TestOverwritePartNumber(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create multipart upload
	createResult, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Upload part 1
	part1ContentOld := []byte("old content for part 1")
	_, err = client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(part1ContentOld),
	})
	require.NoError(t, err)

	// Upload part 1 again (should overwrite)
	part1ContentNew := []byte("new content for part 1")
	part1Result, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   createResult.UploadId,
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(part1ContentNew),
	})
	require.NoError(t, err)

	// Complete with the new part
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{
					PartNumber: aws.Int32(1),
					ETag:       part1Result.ETag,
				},
			},
		},
	})
	require.NoError(t, err)

	// Verify content is the new content
	getResult, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer getResult.Body.Close()

	body, err := io.ReadAll(getResult.Body)
	require.NoError(t, err)
	assert.Equal(t, part1ContentNew, body)
}
