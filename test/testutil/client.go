package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Client returns an S3 client configured for the test server.
func (ts *TestServer) S3Client(t *testing.T) *s3.Client {
	t.Helper()

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			ts.AccessKey,
			ts.SecretKey,
			"",
		)),
	)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.Endpoint)
		o.UsePathStyle = true
	})

	return client
}

// CreateTestBucket creates a bucket for testing and returns a cleanup function.
func (ts *TestServer) CreateTestBucket(t *testing.T, name string) func() {
	t.Helper()

	client := ts.S3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(name),
	})
	if err != nil {
		t.Fatalf("failed to create test bucket: %v", err)
	}

	return func() {
		// Delete all objects first
		listOutput, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(name),
		})
		if err == nil {
			for _, obj := range listOutput.Contents {
				client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(name),
					Key:    obj.Key,
				})
			}
		}

		// Delete bucket
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{
			Bucket: aws.String(name),
		})
	}
}

// RandomBucketName generates a random bucket name for testing.
func RandomBucketName() string {
	return "test-bucket-" + randomString(8)
}

// RandomObjectKey generates a random object key for testing.
func RandomObjectKey() string {
	return "test-object-" + randomString(8)
}

func randomString(n int) string {
	b := make([]byte, n/2+1)
	rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
