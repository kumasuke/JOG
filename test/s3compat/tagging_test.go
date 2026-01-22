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

// Object Tagging Tests

func TestPutObjectTagging(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("content"),
	})
	require.NoError(t, err)

	// Put tags
	_, err = client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Tagging: &types.Tagging{
			TagSet: []types.Tag{
				{Key: aws.String("Environment"), Value: aws.String("Production")},
				{Key: aws.String("Project"), Value: aws.String("JOG")},
			},
		},
	})
	require.NoError(t, err)

	// Get tags to verify
	result, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	assert.Len(t, result.TagSet, 2)
	tags := make(map[string]string)
	for _, tag := range result.TagSet {
		tags[*tag.Key] = *tag.Value
	}
	assert.Equal(t, "Production", tags["Environment"])
	assert.Equal(t, "JOG", tags["Project"])
}

func TestGetObjectTagging(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("content"),
	})
	require.NoError(t, err)

	// Get tags before any tags are set (should return empty)
	result, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	assert.Empty(t, result.TagSet)
}

func TestGetObjectTaggingNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get tags for non-existent object
	_, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("non-existent-key"),
	})
	require.Error(t, err)

	// Check for NoSuchKey error
	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchKey", apiErr.ErrorCode())
	}
}

func TestDeleteObjectTagging(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader("content"),
	})
	require.NoError(t, err)

	// Put tags
	_, err = client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Tagging: &types.Tagging{
			TagSet: []types.Tag{
				{Key: aws.String("Environment"), Value: aws.String("Production")},
			},
		},
	})
	require.NoError(t, err)

	// Delete tags
	_, err = client.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Verify tags are deleted
	result, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	assert.Empty(t, result.TagSet)
}

func TestPutObjectWithTagging(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Create object with tags using x-amz-tagging header
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:  aws.String(bucketName),
		Key:     aws.String(key),
		Body:    strings.NewReader("content"),
		Tagging: aws.String("Environment=Production&Project=JOG"),
	})
	require.NoError(t, err)

	// Get tags to verify
	result, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	assert.Len(t, result.TagSet, 2)
	tags := make(map[string]string)
	for _, tag := range result.TagSet {
		tags[*tag.Key] = *tag.Value
	}
	assert.Equal(t, "Production", tags["Environment"])
	assert.Equal(t, "JOG", tags["Project"])
}

// Bucket Tagging Tests

func TestPutBucketTagging(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put bucket tags
	_, err := client.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket: aws.String(bucketName),
		Tagging: &types.Tagging{
			TagSet: []types.Tag{
				{Key: aws.String("Environment"), Value: aws.String("Production")},
				{Key: aws.String("Team"), Value: aws.String("Backend")},
			},
		},
	})
	require.NoError(t, err)

	// Get bucket tags to verify
	result, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	assert.Len(t, result.TagSet, 2)
	tags := make(map[string]string)
	for _, tag := range result.TagSet {
		tags[*tag.Key] = *tag.Value
	}
	assert.Equal(t, "Production", tags["Environment"])
	assert.Equal(t, "Backend", tags["Team"])
}

func TestGetBucketTagging(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get bucket tags before any tags are set
	// S3 returns NoSuchTagSet error when no tags are set
	_, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchTagSet", apiErr.ErrorCode())
	}
}

func TestGetBucketTaggingNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Get bucket tags for non-existent bucket
	_, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket: aws.String("non-existent-bucket"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}

func TestDeleteBucketTagging(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put bucket tags
	_, err := client.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket: aws.String(bucketName),
		Tagging: &types.Tagging{
			TagSet: []types.Tag{
				{Key: aws.String("Environment"), Value: aws.String("Production")},
			},
		},
	})
	require.NoError(t, err)

	// Delete bucket tags
	_, err = client.DeleteBucketTagging(ctx, &s3.DeleteBucketTaggingInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Verify tags are deleted (should return NoSuchTagSet)
	_, err = client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchTagSet", apiErr.ErrorCode())
	}
}
