package s3compat

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S3ErrorResponse represents the XML error response from S3.
type S3ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
}

func TestErrorResponseFormat(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	// Make a raw HTTP request to get the XML error response
	resp, err := http.Get(ts.Endpoint + "/non-existent-bucket/non-existent-key")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should be 404
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Content-Type should be XML
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/xml")

	// Parse error response
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var errorResp S3ErrorResponse
	err = xml.Unmarshal(body, &errorResp)
	require.NoError(t, err)

	// Verify error response structure
	assert.NotEmpty(t, errorResp.Code)
	assert.NotEmpty(t, errorResp.Message)
	assert.NotEmpty(t, errorResp.RequestID)
}

func TestNoSuchBucketError(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Get object from non-existent bucket
	_, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("non-existent-bucket"),
		Key:    aws.String("some-key"),
	})
	require.Error(t, err)

	// Check for NoSuchBucket error in error message
	assert.Contains(t, err.Error(), "NoSuchBucket")
}

func TestNoSuchKeyError(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	// Get non-existent object
	_, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String("non-existent-key"),
	})
	require.Error(t, err)

	var noSuchKey *types.NoSuchKey
	assert.ErrorAs(t, err, &noSuchKey)
}

func TestBucketNotEmptyError(t *testing.T) {
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

	// Put object
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
	require.Error(t, err)

	// BucketNotEmpty error should contain "BucketNotEmpty" in error message
	assert.Contains(t, err.Error(), "BucketNotEmpty")
}

func TestInvalidBucketNameError(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	// Try to create bucket with invalid name
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("A"), // Too short and uppercase
	})
	require.Error(t, err)
}

func TestHTTPStatusCodes(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	testCases := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
	}{
		{
			name:           "GET non-existent bucket",
			method:         "GET",
			path:           "/non-existent-bucket/",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "HEAD non-existent bucket",
			method:         "HEAD",
			path:           "/non-existent-bucket",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "DELETE non-existent bucket",
			method:         "DELETE",
			path:           "/non-existent-bucket",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, ts.Endpoint+tc.path, nil)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.expectedStatus, resp.StatusCode)
		})
	}
}

func TestDeleteBucketReturns204(t *testing.T) {
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

	// Delete bucket using raw HTTP to check status code
	req, err := http.NewRequest("DELETE", ts.Endpoint+"/"+bucketName, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestDeleteObjectReturns204(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Put object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   nil,
	})
	require.NoError(t, err)

	// Delete object using raw HTTP to check status code
	req, err := http.NewRequest("DELETE", ts.Endpoint+"/"+bucketName+"/"+key, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestGetObjectReturns200(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	key := testutil.RandomObjectKey()

	// Put object
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   nil,
	})
	require.NoError(t, err)

	// Get object using raw HTTP to check status code
	req, err := http.NewRequest("GET", ts.Endpoint+"/"+bucketName+"/"+key, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
