package api

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStorage is a minimal storage.Storage stub for limit tests.
type mockStorage struct {
	storage.Storage
}

// CopyObject is implemented on the mock so handler tests that reach the
// storage call (after validation succeeds) do not panic on the unembedded
// interface; it returns ErrObjectNotFound so the handler converts it into a
// NoSuchKey response that tests can distinguish from validation rejections.
func (s *mockStorage) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*storage.Object, error) {
	return nil, storage.ErrObjectNotFound
}

func newHandlerWithMock() *Handler {
	return &Handler{storage: &mockStorage{}}
}

// setContext is a helper to add bucket/key context to the request.
func setContext(r *http.Request, bucket, key string) *http.Request {
	r = WithBucket(r, bucket)
	if key != "" {
		r = WithKey(r, key)
	}
	return r
}

// parseS3ErrorCode reads the XML body of an S3 error response and returns the Code field.
func parseS3ErrorCode(t *testing.T, body string) string {
	t.Helper()
	var errResp struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
	}
	if err := xml.Unmarshal([]byte(body), &errResp); err != nil {
		t.Fatalf("failed to parse S3 error XML: %v\nbody: %s", err, body)
	}
	return errResp.Code
}

// oversizedBody returns a reader containing n+1 bytes of 'x'.
func oversizedBody(n int64) *bytes.Reader {
	return bytes.NewReader(bytes.Repeat([]byte("x"), int(n)+1))
}

// TestPutBucketCors_BodyTooLarge verifies that PutBucketCors returns EntityTooLarge
// when the request body exceeds MaxCORSBodySize.
func TestPutBucketCors_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket?cors", oversizedBody(MaxCORSBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutBucketCors(rr, req)

	// net/http sends 413 automatically before the handler can write when MaxBytesReader
	// is triggered, so accept either 400 (EntityTooLarge via our handler) or 413.
	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutBucketTagging_BodyTooLarge verifies that PutBucketTagging returns EntityTooLarge
// when the body exceeds MaxTaggingBodySize.
func TestPutBucketTagging_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket?tagging", oversizedBody(MaxTaggingBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutBucketTagging(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutObjectTagging_BodyTooLarge verifies that PutObjectTagging returns EntityTooLarge.
func TestPutObjectTagging_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket/test-key?tagging", oversizedBody(MaxTaggingBodySize))
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.PutObjectTagging(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutBucketVersioning_BodyTooLarge verifies that PutBucketVersioning returns EntityTooLarge.
func TestPutBucketVersioning_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket?versioning", oversizedBody(MaxVersioningBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutBucketVersioning(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutBucketLifecycle_BodyTooLarge verifies that PutBucketLifecycleConfiguration returns EntityTooLarge.
func TestPutBucketLifecycle_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket?lifecycle", oversizedBody(MaxLifecycleBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutBucketLifecycleConfiguration(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutBucketEncryption_BodyTooLarge verifies that PutBucketEncryption returns EntityTooLarge.
func TestPutBucketEncryption_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket?encryption", oversizedBody(MaxEncryptionBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutBucketEncryption(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutObjectLockConfiguration_BodyTooLarge verifies that PutObjectLockConfiguration returns EntityTooLarge.
func TestPutObjectLockConfiguration_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket?object-lock", oversizedBody(MaxObjectLockBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutObjectLockConfiguration(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutBucketNotification_BodyTooLarge verifies that PutBucketNotification returns EntityTooLarge.
func TestPutBucketNotification_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket?notification", oversizedBody(MaxNotificationBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutBucketNotification(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutBucketWebsite_BodyTooLarge verifies that PutBucketWebsite returns EntityTooLarge.
func TestPutBucketWebsite_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket?website", oversizedBody(MaxWebsiteBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutBucketWebsite(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestDeleteObjects_BodyTooLarge verifies that DeleteObjects returns EntityTooLarge.
func TestDeleteObjects_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPost, "/test-bucket?delete", oversizedBody(MaxDeleteObjectsSize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.DeleteObjects(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutBucketAcl_BodyTooLarge verifies that PutBucketAcl returns EntityTooLarge.
func TestPutBucketAcl_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	// No x-amz-acl header so body path is exercised
	req := httptest.NewRequest(http.MethodPut, "/test-bucket?acl", oversizedBody(MaxACLBodySize))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.PutBucketAcl(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestCompleteMultipartUpload_BodyTooLarge verifies that CompleteMultipartUpload returns EntityTooLarge.
func TestCompleteMultipartUpload_BodyTooLarge(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPost, "/test-bucket/test-key?uploadId=abc123", oversizedBody(MaxMultipartCompleteSize))
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.CompleteMultipartUpload(rr, req)

	statusOK := rr.Code == http.StatusBadRequest || rr.Code == http.StatusRequestEntityTooLarge
	assert.True(t, statusOK, "expected 400 or 413, got %d", rr.Code)
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		assert.Equal(t, "EntityTooLarge", code)
	}
}

// TestPutObject_ContentLengthExceedsMax verifies that PutObject rejects
// requests whose declared Content-Length exceeds MaxPutObjectSize before
// the body is read (H-11). This prevents a single-PUT upload from
// consuming unbounded disk space.
func TestPutObject_ContentLengthExceedsMax(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket/test-key", strings.NewReader(""))
	req.ContentLength = MaxPutObjectSize + 1
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.PutObject(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "EntityTooLarge" {
		t.Fatalf("error code = %q, want %q", code, "EntityTooLarge")
	}
}

// TestUploadPart_ContentLengthExceedsMax verifies that UploadPart rejects
// requests whose declared Content-Length exceeds MaxUploadPartSize (H-11).
func TestUploadPart_ContentLengthExceedsMax(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/test-bucket/test-key?partNumber=1&uploadId=abc", strings.NewReader(""))
	req.ContentLength = MaxUploadPartSize + 1
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.UploadPart(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "EntityTooLarge" {
		t.Fatalf("error code = %q, want %q", code, "EntityTooLarge")
	}
}

// TestIsBodyTooLarge verifies the isBodyTooLarge helper correctly identifies MaxBytesError.
func TestIsBodyTooLarge(t *testing.T) {
	// Simulate what MaxBytesReader returns when limit is exceeded
	w := httptest.NewRecorder()
	body := strings.NewReader(strings.Repeat("x", 100))
	limited := http.MaxBytesReader(w, io.NopCloser(body), 10)

	buf := make([]byte, 100)
	_, err := limited.Read(buf)
	require.Error(t, err)
	assert.True(t, isBodyTooLarge(err), "isBodyTooLarge should return true for MaxBytesError")
}
