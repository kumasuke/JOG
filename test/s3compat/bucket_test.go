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

func TestCreateBucket(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Verify bucket exists
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
}

func TestCreateBucketAlreadyExists(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket first time
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Try to create same bucket again
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})

	// Should return BucketAlreadyOwnedByYou error
	require.Error(t, err)
	// S3 returns BucketAlreadyOwnedByYou when you already own the bucket
}

func TestCreateBucketInvalidName(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	testCases := []struct {
		name       string
		bucketName string
	}{
		{"too_short", "ab"},
		{"invalid_chars", "my_bucket!"},
		{"uppercase", "MyBucket"},
		{"starts_with_dot", ".mybucket"},
		{"ends_with_dot", "mybucket."},
		{"starts_with_hyphen", "-mybucket"},
		{"ends_with_hyphen", "mybucket-"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
				Bucket: aws.String(tc.bucketName),
			})
			require.Error(t, err, "expected error for invalid bucket name: %s", tc.bucketName)
		})
	}
}

func TestListBuckets(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Create multiple buckets
	bucketNames := []string{
		"test-bucket-a-" + testutil.RandomBucketName()[12:],
		"test-bucket-b-" + testutil.RandomBucketName()[12:],
		"test-bucket-c-" + testutil.RandomBucketName()[12:],
	}

	for _, name := range bucketNames {
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: aws.String(name),
		})
		require.NoError(t, err)
	}

	// List buckets
	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)

	// Verify all buckets are returned
	foundBuckets := make(map[string]bool)
	for _, bucket := range result.Buckets {
		foundBuckets[*bucket.Name] = true
	}

	for _, name := range bucketNames {
		assert.True(t, foundBuckets[name], "bucket %s should be in list", name)
	}
}

func TestHeadBucket(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Head bucket should succeed
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
}

func TestHeadBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Head non-existent bucket
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String("non-existent-bucket"),
	})
	require.Error(t, err)

	// Check for NotFound error
	var notFound *types.NotFound
	assert.ErrorAs(t, err, &notFound)
}

func TestDeleteBucket(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Delete bucket
	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Verify bucket no longer exists
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err)
}

func TestDeleteBucketNotEmpty(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Put an object
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("test-object"),
		Body:   nil,
	})
	require.NoError(t, err)

	// Try to delete non-empty bucket
	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err, "should not be able to delete non-empty bucket")
}

func TestDeleteBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Delete non-existent bucket
	_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String("non-existent-bucket"),
	})
	require.Error(t, err)
}

func TestGetBucketLocation(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Get bucket location
	result, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// S3 returns empty LocationConstraint for us-east-1
	// or returns the actual region for other regions
	// JOG defaults to us-east-1, so LocationConstraint should be empty or ""
	assert.True(t, result.LocationConstraint == "",
		"expected empty location constraint for us-east-1, got: %s", result.LocationConstraint)
}

func TestGetBucketLocationNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Get location for non-existent bucket
	_, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String("non-existent-bucket"),
	})
	require.Error(t, err)
}

// TestDeleteBucketNotEmptyWithVersions is a regression test for issue #42.
// When versioning is enabled and a delete marker exists (current key removed from
// objects table), CountObjects returns 0 but object_versions still has rows.
// DeleteBucket must return BucketNotEmpty in this case.
func TestDeleteBucketNotEmptyWithVersions(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Enable versioning
	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	// Put an object (creates a version in object_versions)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("test-key"),
		Body:   strings.NewReader("hello"),
	})
	require.NoError(t, err)

	// Delete the object WITHOUT a versionId: creates a delete marker,
	// which removes the current row from objects table but leaves the
	// original version in object_versions.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("test-key"),
	})
	require.NoError(t, err)

	// DeleteBucket MUST fail with BucketNotEmpty because object_versions still
	// has the original version row (and a delete-marker row).
	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.Error(t, err, "DeleteBucket should fail when versioned objects still exist")

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "BucketNotEmpty", apiErr.ErrorCode(),
			"expected BucketNotEmpty error code")
	}
}
