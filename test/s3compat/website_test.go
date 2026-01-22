package s3compat

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutGetBucketWebsite(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put website configuration
	_, err := client.PutBucketWebsite(ctx, &s3.PutBucketWebsiteInput{
		Bucket: aws.String(bucketName),
		WebsiteConfiguration: &types.WebsiteConfiguration{
			IndexDocument: &types.IndexDocument{
				Suffix: aws.String("index.html"),
			},
			ErrorDocument: &types.ErrorDocument{
				Key: aws.String("error.html"),
			},
		},
	})
	require.NoError(t, err)

	// Get website configuration
	result, err := client.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	require.NotNil(t, result.IndexDocument)
	assert.Equal(t, "index.html", *result.IndexDocument.Suffix)
	require.NotNil(t, result.ErrorDocument)
	assert.Equal(t, "error.html", *result.ErrorDocument.Key)
}

func TestDeleteBucketWebsite(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put website configuration
	_, err := client.PutBucketWebsite(ctx, &s3.PutBucketWebsiteInput{
		Bucket: aws.String(bucketName),
		WebsiteConfiguration: &types.WebsiteConfiguration{
			IndexDocument: &types.IndexDocument{
				Suffix: aws.String("index.html"),
			},
		},
	})
	require.NoError(t, err)

	// Delete website configuration
	_, err = client.DeleteBucketWebsite(ctx, &s3.DeleteBucketWebsiteInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Get should return error
	_, err = client.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchWebsiteConfiguration", apiErr.ErrorCode())
	}
}

func TestGetBucketWebsiteNotExists(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get website configuration without setting one
	_, err := client.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchWebsiteConfiguration", apiErr.ErrorCode())
	}
}

func TestBucketWebsiteBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Get website for non-existent bucket
	_, err := client.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{
		Bucket: aws.String("non-existent-bucket"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}

func TestPutBucketWebsiteWithRedirectRules(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put website configuration with redirect rules
	_, err := client.PutBucketWebsite(ctx, &s3.PutBucketWebsiteInput{
		Bucket: aws.String(bucketName),
		WebsiteConfiguration: &types.WebsiteConfiguration{
			IndexDocument: &types.IndexDocument{
				Suffix: aws.String("index.html"),
			},
			RoutingRules: []types.RoutingRule{
				{
					Condition: &types.Condition{
						KeyPrefixEquals: aws.String("docs/"),
					},
					Redirect: &types.Redirect{
						ReplaceKeyPrefixWith: aws.String("documents/"),
					},
				},
				{
					Condition: &types.Condition{
						HttpErrorCodeReturnedEquals: aws.String("404"),
					},
					Redirect: &types.Redirect{
						ReplaceKeyWith: aws.String("not-found.html"),
					},
				},
			},
		},
	})
	require.NoError(t, err)

	// Get and verify
	result, err := client.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	assert.Equal(t, "index.html", *result.IndexDocument.Suffix)
	assert.Len(t, result.RoutingRules, 2)
}

func TestPutBucketWebsiteRedirectAllRequests(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Put website configuration with redirect all requests
	_, err := client.PutBucketWebsite(ctx, &s3.PutBucketWebsiteInput{
		Bucket: aws.String(bucketName),
		WebsiteConfiguration: &types.WebsiteConfiguration{
			RedirectAllRequestsTo: &types.RedirectAllRequestsTo{
				HostName: aws.String("example.com"),
				Protocol: types.ProtocolHttps,
			},
		},
	})
	require.NoError(t, err)

	// Get and verify
	result, err := client.GetBucketWebsite(ctx, &s3.GetBucketWebsiteInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	require.NotNil(t, result.RedirectAllRequestsTo)
	assert.Equal(t, "example.com", *result.RedirectAllRequestsTo.HostName)
	assert.Equal(t, types.ProtocolHttps, result.RedirectAllRequestsTo.Protocol)
}

func TestDeleteBucketWebsiteNonExistent(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Delete website that doesn't exist (should succeed like S3)
	_, err := client.DeleteBucketWebsite(ctx, &s3.DeleteBucketWebsiteInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
}
