package s3compat

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBucketAcl(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Get ACL
	result, err := client.GetBucketAcl(context.Background(), &s3.GetBucketAclInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Should have owner
	assert.NotNil(t, result.Owner)
	assert.NotEmpty(t, result.Owner.ID)

	// Should have at least one grant (FULL_CONTROL for owner)
	assert.NotEmpty(t, result.Grants)
}

func TestPutBucketAclCannedPrivate(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Set ACL to private
	_, err = client.PutBucketAcl(context.Background(), &s3.PutBucketAclInput{
		Bucket: aws.String(bucketName),
		ACL:    types.BucketCannedACLPrivate,
	})
	require.NoError(t, err)

	// Get ACL and verify
	result, err := client.GetBucketAcl(context.Background(), &s3.GetBucketAclInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Private ACL should only have owner with FULL_CONTROL
	assert.Len(t, result.Grants, 1)
	assert.Equal(t, types.PermissionFullControl, result.Grants[0].Permission)
}

func TestPutBucketAclCannedPublicRead(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Set ACL to public-read
	_, err = client.PutBucketAcl(context.Background(), &s3.PutBucketAclInput{
		Bucket: aws.String(bucketName),
		ACL:    types.BucketCannedACLPublicRead,
	})
	require.NoError(t, err)

	// Get ACL and verify
	result, err := client.GetBucketAcl(context.Background(), &s3.GetBucketAclInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Public-read ACL should have owner FULL_CONTROL and AllUsers READ
	assert.GreaterOrEqual(t, len(result.Grants), 2)

	// Verify there's a READ grant for AllUsers
	hasPublicRead := false
	for _, grant := range result.Grants {
		if grant.Grantee != nil && grant.Grantee.URI != nil {
			if strings.Contains(*grant.Grantee.URI, "AllUsers") && grant.Permission == types.PermissionRead {
				hasPublicRead = true
				break
			}
		}
	}
	assert.True(t, hasPublicRead, "expected public READ grant")
}

func TestGetObjectAcl(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	bucketName := testutil.RandomBucketName()
	objectKey := testutil.RandomObjectKey()

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Put object
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Get ACL
	result, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	// Should have owner
	assert.NotNil(t, result.Owner)
	assert.NotEmpty(t, result.Owner.ID)

	// Should have at least one grant (FULL_CONTROL for owner)
	assert.NotEmpty(t, result.Grants)
}

func TestPutObjectAclCannedPrivate(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	bucketName := testutil.RandomBucketName()
	objectKey := testutil.RandomObjectKey()

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Put object
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Set ACL to private
	_, err = client.PutObjectAcl(context.Background(), &s3.PutObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		ACL:    types.ObjectCannedACLPrivate,
	})
	require.NoError(t, err)

	// Get ACL and verify
	result, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	// Private ACL should only have owner with FULL_CONTROL
	assert.Len(t, result.Grants, 1)
	assert.Equal(t, types.PermissionFullControl, result.Grants[0].Permission)
}

func TestPutObjectAclCannedPublicRead(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	bucketName := testutil.RandomBucketName()
	objectKey := testutil.RandomObjectKey()

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Put object
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
	})
	require.NoError(t, err)

	// Set ACL to public-read
	_, err = client.PutObjectAcl(context.Background(), &s3.PutObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		ACL:    types.ObjectCannedACLPublicRead,
	})
	require.NoError(t, err)

	// Get ACL and verify
	result, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	// Public-read ACL should have owner FULL_CONTROL and AllUsers READ
	assert.GreaterOrEqual(t, len(result.Grants), 2)

	// Verify there's a READ grant for AllUsers
	hasPublicRead := false
	for _, grant := range result.Grants {
		if grant.Grantee != nil && grant.Grantee.URI != nil {
			if strings.Contains(*grant.Grantee.URI, "AllUsers") && grant.Permission == types.PermissionRead {
				hasPublicRead = true
				break
			}
		}
	}
	assert.True(t, hasPublicRead, "expected public READ grant")
}

func TestPutObjectWithCannedAcl(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	bucketName := testutil.RandomBucketName()
	objectKey := testutil.RandomObjectKey()

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Put object with public-read ACL
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   strings.NewReader("test content"),
		ACL:    types.ObjectCannedACLPublicRead,
	})
	require.NoError(t, err)

	// Get ACL and verify
	result, err := client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	require.NoError(t, err)

	// Should have public READ grant
	hasPublicRead := false
	for _, grant := range result.Grants {
		if grant.Grantee != nil && grant.Grantee.URI != nil {
			if strings.Contains(*grant.Grantee.URI, "AllUsers") && grant.Permission == types.PermissionRead {
				hasPublicRead = true
				break
			}
		}
	}
	assert.True(t, hasPublicRead, "expected public READ grant from canned ACL on PutObject")
}

func TestGetBucketAclNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)

	// Try to get ACL for non-existent bucket
	_, err := client.GetBucketAcl(context.Background(), &s3.GetBucketAclInput{
		Bucket: aws.String("nonexistent-bucket"),
	})
	require.Error(t, err)
}

func TestGetObjectAclNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	bucketName := testutil.RandomBucketName()

	// Create bucket
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	// Try to get ACL for non-existent object
	_, err = client.GetObjectAcl(context.Background(), &s3.GetObjectAclInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("nonexistent-key"),
	})
	require.Error(t, err)
}
