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

// enableVersioning turns on versioning for a bucket.
func enableVersioning(t *testing.T, client *s3.Client, bucket string) {
	t.Helper()
	_, err := client.PutBucketVersioning(context.Background(), &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)
}

// TestObjectTagging_PerVersion verifies that tagging is addressable per version
// and that tags set on an older version survive a subsequent PUT of a new
// version (issue #41: no FK-cascade wipe across versions).
func TestObjectTagging_PerVersion(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucket := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucket)
	defer cleanup()
	enableVersioning(t, client, bucket)

	key := testutil.RandomObjectKey()

	// Put version 1 and tag it.
	put1, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader("v1"),
	})
	require.NoError(t, err)
	v1 := aws.ToString(put1.VersionId)
	require.NotEmpty(t, v1)

	_, err = client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("v"), Value: aws.String("one")}}},
	})
	require.NoError(t, err)

	// Put version 2 (creates a new version, must not touch v1's tags).
	put2, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader("v2"),
	})
	require.NoError(t, err)
	v2 := aws.ToString(put2.VersionId)
	require.NotEmpty(t, v2)
	require.NotEqual(t, v1, v2)

	// Tag version 2 differently.
	_, err = client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(v2),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("v"), Value: aws.String("two")}}},
	})
	require.NoError(t, err)

	// v1 tags must still be "one".
	got1, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(v1),
	})
	require.NoError(t, err)
	require.Len(t, got1.TagSet, 1)
	assert.Equal(t, "one", aws.ToString(got1.TagSet[0].Value))

	// Current version (v2) tags must be "two".
	gotCurrent, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)
	require.Len(t, gotCurrent.TagSet, 1)
	assert.Equal(t, "two", aws.ToString(gotCurrent.TagSet[0].Value))

	// Deleting v2's tags must not affect v1.
	_, err = client.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(v2),
	})
	require.NoError(t, err)
	got1, err = client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(v1),
	})
	require.NoError(t, err)
	require.Len(t, got1.TagSet, 1)
	assert.Equal(t, "one", aws.ToString(got1.TagSet[0].Value))
}

// TestObjectAcl_PerVersion verifies an ACL set on one version is isolated from
// another version of the same key (issue #41).
func TestObjectAcl_PerVersion(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucket := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucket)
	defer cleanup()
	enableVersioning(t, client, bucket)

	key := testutil.RandomObjectKey()

	put1, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader("v1"),
	})
	require.NoError(t, err)
	v1 := aws.ToString(put1.VersionId)

	// Make v1 public-read.
	_, err = client.PutObjectAcl(ctx, &s3.PutObjectAclInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(v1),
		ACL: types.ObjectCannedACLPublicRead,
	})
	require.NoError(t, err)

	// Put a new version; it must get a fresh (default) ACL, not v1's.
	put2, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader("v2"),
	})
	require.NoError(t, err)
	v2 := aws.ToString(put2.VersionId)
	require.NotEqual(t, v1, v2)

	// v1 ACL must still carry the public-read (AllUsers READ) grant.
	acl1, err := client.GetObjectAcl(ctx, &s3.GetObjectAclInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(v1),
	})
	require.NoError(t, err)
	if !hasPublicReadGrant(acl1.Grants) {
		t.Errorf("v1 ACL lost its public-read AllUsers grant: %+v", acl1.Grants)
	}

	// v2 ACL must be the default (owner FULL_CONTROL only, no AllUsers grant).
	acl2, err := client.GetObjectAcl(ctx, &s3.GetObjectAclInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(v2),
	})
	require.NoError(t, err)
	if hasPublicReadGrant(acl2.Grants) {
		t.Errorf("v2 ACL unexpectedly inherited a public-read grant from v1: %+v", acl2.Grants)
	}
}

// hasPublicReadGrant reports whether any grant is an AllUsers READ grant, as
// produced by the public-read canned ACL. Matches the URI-based assertion used
// by the existing canned-ACL s3compat tests.
func hasPublicReadGrant(grants []types.Grant) bool {
	for _, g := range grants {
		if g.Grantee != nil && g.Grantee.URI != nil &&
			strings.Contains(*g.Grantee.URI, "AllUsers") && g.Permission == types.PermissionRead {
			return true
		}
	}
	return false
}

// TestObjectTagging_NullVersion verifies the "null" versionId selector targets
// the null version on a non-versioning bucket (issue #41).
func TestObjectTagging_NullVersion(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucket := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucket)
	defer cleanup()

	key := testutil.RandomObjectKey()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader("content"),
	})
	require.NoError(t, err)

	// Tag the null version explicitly via versionId="null".
	_, err = client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String("null"),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("k"), Value: aws.String("nullv")}}},
	})
	require.NoError(t, err)

	// Reading with no versionId (current version == null version) sees them.
	got, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)
	require.Len(t, got.TagSet, 1)
	assert.Equal(t, "nullv", aws.ToString(got.TagSet[0].Value))

	// Reading via versionId="null" sees the same tags.
	gotNull, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String("null"),
	})
	require.NoError(t, err)
	require.Len(t, gotNull.TagSet, 1)
	assert.Equal(t, "nullv", aws.ToString(gotNull.TagSet[0].Value))
}
