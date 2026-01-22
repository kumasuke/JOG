package s3compat

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/require"
)

func TestValidSignatureV4(t *testing.T) {
	ts := testutil.NewTestServerWithAuth(t)
	defer ts.Cleanup()

	// Create client with correct credentials
	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Should succeed with valid credentials
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Verify bucket was created
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
}

func TestInvalidSignatureV4(t *testing.T) {
	ts := testutil.NewTestServerWithAuth(t)
	defer ts.Cleanup()

	ctx := context.Background()

	// Create client with wrong credentials
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"wrong-access-key",
			"wrong-secret-key",
			"",
		)),
	)
	require.NoError(t, err)

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.Endpoint)
		o.UsePathStyle = true
	})

	// Should fail with invalid credentials
	_, err = client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.Error(t, err)
}

func TestInvalidAccessKey(t *testing.T) {
	ts := testutil.NewTestServerWithAuth(t)
	defer ts.Cleanup()

	ctx := context.Background()

	// Create client with wrong access key but correct secret format
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"invalid-access-key",
			ts.SecretKey, // Correct secret key
			"",
		)),
	)
	require.NoError(t, err)

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.Endpoint)
		o.UsePathStyle = true
	})

	// Should fail with invalid access key
	_, err = client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.Error(t, err)
}

func TestAuthenticatedBucketOperations(t *testing.T) {
	ts := testutil.NewTestServerWithAuth(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// List buckets
	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)

	found := false
	for _, bucket := range result.Buckets {
		if *bucket.Name == bucketName {
			found = true
			break
		}
	}
	require.True(t, found, "created bucket should be in list")

	// Delete bucket
	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
}

func TestAuthenticatedObjectOperations(t *testing.T) {
	ts := testutil.NewTestServerWithAuth(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	key := testutil.RandomObjectKey()

	// Put object
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   nil,
	})
	require.NoError(t, err)

	// Get object
	_, err = client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)

	// Delete object
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
}
