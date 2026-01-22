package s3compat

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutBucketCors(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put CORS configuration
	_, err := client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(bucketName),
		CORSConfiguration: &types.CORSConfiguration{
			CORSRules: []types.CORSRule{
				{
					AllowedOrigins: []string{"http://example.com", "http://example.org"},
					AllowedMethods: []string{"GET", "PUT", "POST"},
					AllowedHeaders: []string{"*"},
					ExposeHeaders:  []string{"ETag", "x-amz-meta-custom"},
					MaxAgeSeconds:  aws.Int32(3600),
				},
			},
		},
	})
	require.NoError(t, err)

	// Get CORS configuration to verify
	result, err := client.GetBucketCors(ctx, &s3.GetBucketCorsInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	require.Len(t, result.CORSRules, 1)
	rule := result.CORSRules[0]
	assert.ElementsMatch(t, []string{"http://example.com", "http://example.org"}, rule.AllowedOrigins)
	assert.ElementsMatch(t, []string{"GET", "PUT", "POST"}, rule.AllowedMethods)
	assert.ElementsMatch(t, []string{"*"}, rule.AllowedHeaders)
	assert.ElementsMatch(t, []string{"ETag", "x-amz-meta-custom"}, rule.ExposeHeaders)
	assert.Equal(t, int32(3600), *rule.MaxAgeSeconds)
}

func TestPutBucketCorsMultipleRules(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put CORS configuration with multiple rules
	_, err := client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(bucketName),
		CORSConfiguration: &types.CORSConfiguration{
			CORSRules: []types.CORSRule{
				{
					AllowedOrigins: []string{"http://example.com"},
					AllowedMethods: []string{"GET"},
				},
				{
					AllowedOrigins: []string{"http://example.org"},
					AllowedMethods: []string{"PUT", "POST"},
					AllowedHeaders: []string{"Content-Type"},
				},
			},
		},
	})
	require.NoError(t, err)

	// Get CORS configuration to verify
	result, err := client.GetBucketCors(ctx, &s3.GetBucketCorsInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	assert.Len(t, result.CORSRules, 2)
}

func TestGetBucketCors(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get CORS configuration before any CORS is set
	// S3 returns NoSuchCORSConfiguration error when no CORS is set
	_, err := client.GetBucketCors(ctx, &s3.GetBucketCorsInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchCORSConfiguration", apiErr.ErrorCode())
	}
}

func TestGetBucketCorsNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Get CORS configuration for non-existent bucket
	_, err := client.GetBucketCors(ctx, &s3.GetBucketCorsInput{
		Bucket: aws.String("non-existent-bucket"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}

func TestDeleteBucketCors(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put CORS configuration
	_, err := client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(bucketName),
		CORSConfiguration: &types.CORSConfiguration{
			CORSRules: []types.CORSRule{
				{
					AllowedOrigins: []string{"http://example.com"},
					AllowedMethods: []string{"GET"},
				},
			},
		},
	})
	require.NoError(t, err)

	// Delete CORS configuration
	_, err = client.DeleteBucketCors(ctx, &s3.DeleteBucketCorsInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Verify CORS is deleted (should return NoSuchCORSConfiguration)
	_, err = client.GetBucketCors(ctx, &s3.GetBucketCorsInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchCORSConfiguration", apiErr.ErrorCode())
	}
}

func TestCorsPreflightRequest(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put CORS configuration
	_, err := client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(bucketName),
		CORSConfiguration: &types.CORSConfiguration{
			CORSRules: []types.CORSRule{
				{
					AllowedOrigins: []string{"http://example.com"},
					AllowedMethods: []string{"GET", "PUT"},
					AllowedHeaders: []string{"Content-Type", "Authorization"},
					MaxAgeSeconds:  aws.Int32(3600),
				},
			},
		},
	})
	require.NoError(t, err)

	// Send OPTIONS preflight request
	httpClient := &http.Client{}
	req, err := http.NewRequest("OPTIONS", ts.URL()+"/"+bucketName, nil)
	require.NoError(t, err)

	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "http://example.com", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), "PUT")
	assert.Equal(t, "3600", resp.Header.Get("Access-Control-Max-Age"))
}

func TestCorsPreflightRequestNoMatch(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put CORS configuration
	_, err := client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(bucketName),
		CORSConfiguration: &types.CORSConfiguration{
			CORSRules: []types.CORSRule{
				{
					AllowedOrigins: []string{"http://example.com"},
					AllowedMethods: []string{"GET"},
				},
			},
		},
	})
	require.NoError(t, err)

	// Send OPTIONS preflight request with non-matching origin
	httpClient := &http.Client{}
	req, err := http.NewRequest("OPTIONS", ts.URL()+"/"+bucketName, nil)
	require.NoError(t, err)

	req.Header.Set("Origin", "http://other-domain.com")
	req.Header.Set("Access-Control-Request-Method", "GET")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should not include CORS headers for non-matching origin
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}
