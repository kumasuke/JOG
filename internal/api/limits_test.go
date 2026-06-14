package api

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
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

// GetBucketVersioning lets the CR-5 evaluation paths in PutObject /
// DeleteObject / DeleteObjects / CopyObject reach the storage call instead
// of nil-deref'ing the embedded interface. Defaulting to "Disabled" means
// the lock-evaluation branch is the one being exercised in tests below.
func (s *mockStorage) GetBucketVersioning(ctx context.Context, bucket string) (storage.VersioningStatus, error) {
	return storage.VersioningStatusDisabled, nil
}

// HeadObject is needed by the CR-5 destination check in CopyObject.
// Returning ErrObjectNotFound mimics "destination key does not yet exist",
// so the lock evaluation is correctly skipped (nothing to protect).
func (s *mockStorage) HeadObject(ctx context.Context, bucket, key string) (*storage.Object, error) {
	return nil, storage.ErrObjectNotFound
}

// GetObjectLockConfiguration is invoked from evaluateObjectLock as the
// short-circuit "is the bucket Object-Lock-enabled at all?" check.
// Returning nil means the helper exits early as a no-op, which matches
// the standard test-bucket setup used by the limit/copy tests.
func (s *mockStorage) GetObjectLockConfiguration(ctx context.Context, bucket string) (*storage.ObjectLockConfiguration, error) {
	return nil, nil
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

// TestReadXMLBody_WithinLimit verifies that readXMLBody returns the full body
// and a nil S3Error when the body is at or below the configured limit (the
// backward-compatible happy path used by every XML subresource handler).
func TestReadXMLBody_WithinLimit(t *testing.T) {
	payload := strings.Repeat("y", 1024)
	req := httptest.NewRequest(http.MethodPut, "/test-bucket?cors", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	body, s3err := readXMLBody(rr, req, MaxCORSBodySize)
	require.Nil(t, s3err, "in-limit body must not produce an error")
	assert.Equal(t, payload, string(body))
}

// TestReadXMLBody_TooLarge verifies that readXMLBody returns ErrEntityTooLarge
// (and no body) when the request body exceeds the limit. The caller is then
// responsible for writing the error with its own resource path.
func TestReadXMLBody_TooLarge(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/test-bucket?cors", oversizedBody(MaxCORSBodySize))
	rr := httptest.NewRecorder()

	body, s3err := readXMLBody(rr, req, MaxCORSBodySize)
	require.NotNil(t, s3err, "oversized body must produce an error")
	assert.Equal(t, ErrEntityTooLarge, s3err)
	assert.Nil(t, body)
}

// TestReadXMLBody_ReadError verifies that a non-MaxBytes read failure maps to
// ErrInvalidRequest rather than ErrEntityTooLarge.
func TestReadXMLBody_ReadError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/test-bucket?cors", io.NopCloser(failingReader{}))
	rr := httptest.NewRecorder()

	body, s3err := readXMLBody(rr, req, MaxCORSBodySize)
	require.NotNil(t, s3err, "read failure must produce an error")
	assert.Equal(t, ErrInvalidRequest, s3err)
	assert.Nil(t, body)
}

// failingReader always fails, modelling a transport-level read error that is
// not a MaxBytesError.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("simulated read failure")
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

// failClosedLockStorage embeds the limit-test mockStorage and overrides
// GetObjectLockConfiguration to return an arbitrary non-sentinel error,
// modelling a transient DB outage. evaluateObjectLock must treat this as
// deny (M-2 fail-closed) rather than the legacy fail-open.
type failClosedLockStorage struct {
	mockStorage
}

var errLockBackend = errors.New("simulated lock-config backend failure")

func (s *failClosedLockStorage) GetObjectLockConfiguration(ctx context.Context, bucket string) (*storage.ObjectLockConfiguration, error) {
	return nil, errLockBackend
}

// TestEvaluateObjectLock_GetObjectLockConfigurationErrorFailClosed verifies
// that an unexpected error from GetObjectLockConfiguration deny-lists the
// destructive op (M-2). Sentinel "no configuration" errors must continue
// to allow.
func TestEvaluateObjectLock_GetObjectLockConfigurationErrorFailClosed(t *testing.T) {
	h := &Handler{storage: &failClosedLockStorage{}}
	got := h.evaluateObjectLock(context.Background(), "any-bucket", "any-key", "", false)
	require.NotNil(t, got, "non-sentinel storage error must deny via AccessDenied")
	assert.Equal(t, ErrAccessDenied, got)
}
