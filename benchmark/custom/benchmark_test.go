package custom

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// getEnv returns environment variable value or default.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getS3Client creates an S3 client from environment variables.
func getS3Client() *s3.Client {
	endpoint := getEnv("BENCHMARK_ENDPOINT", "http://localhost:9000")
	accessKey := getEnv("BENCHMARK_ACCESS_KEY", "benchadmin")
	secretKey := getEnv("BENCHMARK_SECRET_KEY", "benchadmin")

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			accessKey,
			secretKey,
			"",
		)),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	return client
}

// randomBytes generates random data of size n.
func randomBytes(n int) []byte {
	data := make([]byte, n)
	if _, err := rand.Read(data); err != nil {
		panic(fmt.Sprintf("failed to generate random data: %v", err))
	}
	return data
}

// setupBucket creates a test bucket and registers cleanup.
func setupBucket(b *testing.B, client *s3.Client, bucketName string) {
	b.Helper()
	ctx := context.Background()

	// Create bucket
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		b.Fatalf("failed to create bucket: %v", err)
	}

	// Register cleanup
	b.Cleanup(func() {
		// Delete all objects first
		listOutput, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(bucketName),
		})
		if err == nil {
			for _, obj := range listOutput.Contents {
				client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(bucketName),
					Key:    obj.Key,
				})
			}
		}

		// Delete bucket
		client.DeleteBucket(ctx, &s3.DeleteBucketInput{
			Bucket: aws.String(bucketName),
		})
	})
}

// BenchmarkPutObject_1KB benchmarks uploading 1KB objects.
func BenchmarkPutObject_1KB(b *testing.B) {
	client := getS3Client()
	bucketName := getEnv("BENCHMARK_BUCKET", "benchmark-bucket")
	setupBucket(b, client, bucketName)

	data := randomBytes(1024) // 1KB
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("object-1kb-%d", i)
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   bytes.NewReader(data),
		})
		if err != nil {
			b.Fatalf("failed to put object: %v", err)
		}
	}
}

// BenchmarkPutObject_1MB benchmarks uploading 1MB objects.
func BenchmarkPutObject_1MB(b *testing.B) {
	client := getS3Client()
	bucketName := getEnv("BENCHMARK_BUCKET", "benchmark-bucket")
	setupBucket(b, client, bucketName)

	data := randomBytes(1024 * 1024) // 1MB
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("object-1mb-%d", i)
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   bytes.NewReader(data),
		})
		if err != nil {
			b.Fatalf("failed to put object: %v", err)
		}
	}
}

// BenchmarkGetObject_1KB benchmarks downloading 1KB objects.
func BenchmarkGetObject_1KB(b *testing.B) {
	client := getS3Client()
	bucketName := getEnv("BENCHMARK_BUCKET", "benchmark-bucket")
	setupBucket(b, client, bucketName)

	data := randomBytes(1024) // 1KB
	ctx := context.Background()

	// Pre-upload object
	objectKey := "benchmark-object-1kb"
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		b.Fatalf("failed to setup object: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(objectKey),
		})
		if err != nil {
			b.Fatalf("failed to get object: %v", err)
		}
		result.Body.Close()
	}
}

// BenchmarkGetObject_1MB benchmarks downloading 1MB objects.
func BenchmarkGetObject_1MB(b *testing.B) {
	client := getS3Client()
	bucketName := getEnv("BENCHMARK_BUCKET", "benchmark-bucket")
	setupBucket(b, client, bucketName)

	data := randomBytes(1024 * 1024) // 1MB
	ctx := context.Background()

	// Pre-upload object
	objectKey := "benchmark-object-1mb"
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		b.Fatalf("failed to setup object: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(objectKey),
		})
		if err != nil {
			b.Fatalf("failed to get object: %v", err)
		}
		result.Body.Close()
	}
}

// BenchmarkListObjectsV2 benchmarks listing objects.
func BenchmarkListObjectsV2(b *testing.B) {
	client := getS3Client()
	bucketName := getEnv("BENCHMARK_BUCKET", "benchmark-bucket")
	setupBucket(b, client, bucketName)

	ctx := context.Background()
	data := randomBytes(1024) // 1KB

	// Pre-create 100 objects
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("list-object-%03d", i)
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   bytes.NewReader(data),
		})
		if err != nil {
			b.Fatalf("failed to setup object: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(bucketName),
		})
		if err != nil {
			b.Fatalf("failed to list objects: %v", err)
		}
	}
}

// BenchmarkMultipartUpload benchmarks 16MB multipart upload with 5MB parts.
func BenchmarkMultipartUpload(b *testing.B) {
	client := getS3Client()
	bucketName := getEnv("BENCHMARK_BUCKET", "benchmark-bucket")
	setupBucket(b, client, bucketName)

	ctx := context.Background()
	objectSize := 16 * 1024 * 1024    // 16MB
	partSize := 5 * 1024 * 1024       // 5MB
	data := randomBytes(objectSize)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("multipart-object-%d", i)

		// Create multipart upload
		createResp, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
		})
		if err != nil {
			b.Fatalf("failed to create multipart upload: %v", err)
		}

		// Upload parts
		var completedParts []types.CompletedPart
		partNumber := int32(1)
		offset := 0

		for offset < objectSize {
			end := offset + partSize
			if end > objectSize {
				end = objectSize
			}

			uploadResp, err := client.UploadPart(ctx, &s3.UploadPartInput{
				Bucket:     aws.String(bucketName),
				Key:        aws.String(key),
				PartNumber: aws.Int32(partNumber),
				UploadId:   createResp.UploadId,
				Body:       bytes.NewReader(data[offset:end]),
			})
			if err != nil {
				b.Fatalf("failed to upload part: %v", err)
			}

			completedParts = append(completedParts, types.CompletedPart{
				ETag:       uploadResp.ETag,
				PartNumber: aws.Int32(partNumber),
			})

			offset = end
			partNumber++
		}

		// Complete multipart upload
		_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucketName),
			Key:      aws.String(key),
			UploadId: createResp.UploadId,
			MultipartUpload: &types.CompletedMultipartUpload{
				Parts: completedParts,
			},
		})
		if err != nil {
			b.Fatalf("failed to complete multipart upload: %v", err)
		}
	}
}
