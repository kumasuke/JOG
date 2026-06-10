package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kumasuke/jog/internal/storage"
)

var errVersioningBackend = errors.New("simulated versioning backend failure")

// versioningReadFailingStorage models a transient metadata DB outage when
// reading bucket versioning state. Write/delete paths must fail closed.
type versioningReadFailingStorage struct {
	mockStorage
	writeCalled  bool
	deleteCalled bool
}

func (s *versioningReadFailingStorage) GetBucketVersioning(ctx context.Context, bucket string) (storage.VersioningStatus, error) {
	return "", errVersioningBackend
}

func (s *versioningReadFailingStorage) PutObject(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string, metadata map[string]string) (*storage.Object, error) {
	s.writeCalled = true
	return &storage.Object{}, nil
}

func (s *versioningReadFailingStorage) PutObjectVersioned(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string, metadata map[string]string) (*storage.Object, string, error) {
	s.writeCalled = true
	return &storage.Object{}, "v1", nil
}

func (s *versioningReadFailingStorage) DeleteObject(ctx context.Context, bucket, key string) error {
	s.deleteCalled = true
	return nil
}

func (s *versioningReadFailingStorage) DeleteObjectVersioned(ctx context.Context, bucket, key, versionID string, versionTargeted bool) (string, bool, error) {
	s.deleteCalled = true
	return "", false, nil
}

func (s *versioningReadFailingStorage) HeadBucket(ctx context.Context, bucket string) (*storage.Bucket, error) {
	return &storage.Bucket{Name: bucket}, nil
}

func (s *versioningReadFailingStorage) HeadObject(ctx context.Context, bucket, key string) (*storage.Object, error) {
	return &storage.Object{Key: key}, nil
}

func (s *versioningReadFailingStorage) ListParts(ctx context.Context, input *storage.ListPartsInput) (*storage.ListPartsOutput, error) {
	return &storage.ListPartsOutput{}, nil
}

func (s *versioningReadFailingStorage) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*storage.Object, error) {
	s.writeCalled = true
	return &storage.Object{}, nil
}

func (s *versioningReadFailingStorage) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []storage.Part) (*storage.Object, error) {
	s.writeCalled = true
	return &storage.Object{}, nil
}

func (s *versioningReadFailingStorage) CompleteMultipartUploadVersioned(ctx context.Context, bucket, key, uploadID string, parts []storage.Part) (*storage.Object, string, error) {
	s.writeCalled = true
	return &storage.Object{}, "v1", nil
}

func TestPutObject_GetBucketVersioningErrorFailClosed(t *testing.T) {
	store := &versioningReadFailingStorage{}
	h := &Handler{storage: store}

	req := httptest.NewRequest(http.MethodPut, "/test-bucket/test-key", strings.NewReader("data"))
	req.ContentLength = 4
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.PutObject(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "InternalError" {
		t.Fatalf("error code = %q, want %q", code, "InternalError")
	}
	if store.writeCalled {
		t.Fatal("PutObject storage write must not run when GetBucketVersioning fails")
	}
}

func TestDeleteObject_GetBucketVersioningErrorFailClosed(t *testing.T) {
	store := &versioningReadFailingStorage{}
	h := &Handler{storage: store}

	req := httptest.NewRequest(http.MethodDelete, "/test-bucket/test-key", nil)
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.DeleteObject(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "InternalError" {
		t.Fatalf("error code = %q, want %q", code, "InternalError")
	}
	if store.deleteCalled {
		t.Fatal("DeleteObject storage delete must not run when GetBucketVersioning fails")
	}
}

func TestDeleteObjects_GetBucketVersioningErrorFailClosed(t *testing.T) {
	store := &versioningReadFailingStorage{}
	h := &Handler{storage: store}

	body := `<Delete><Object><Key>obj1</Key></Object></Delete>`
	req := httptest.NewRequest(http.MethodPost, "/test-bucket?delete", strings.NewReader(body))
	req = setContext(req, "test-bucket", "")
	rr := httptest.NewRecorder()

	h.DeleteObjects(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "InternalError" {
		t.Fatalf("error code = %q, want %q", code, "InternalError")
	}
	if store.deleteCalled {
		t.Fatal("DeleteObjects storage delete must not run when GetBucketVersioning fails")
	}
}

func TestCopyObject_GetBucketVersioningErrorFailClosed(t *testing.T) {
	store := &versioningReadFailingStorage{}
	h := &Handler{storage: store}

	req := httptest.NewRequest(http.MethodPut, "/dst/key", strings.NewReader(""))
	req.Header.Set("x-amz-copy-source", "/src/src-key")
	req = setContext(req, "dst", "key")
	rr := httptest.NewRecorder()

	h.CopyObject(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "InternalError" {
		t.Fatalf("error code = %q, want %q", code, "InternalError")
	}
	if store.writeCalled {
		t.Fatal("CopyObject storage copy must not run when GetBucketVersioning fails")
	}
}

func TestCompleteMultipartUpload_GetBucketVersioningErrorFailClosed(t *testing.T) {
	store := &versioningReadFailingStorage{}
	h := &Handler{storage: store}

	body := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"etag"</ETag></Part></CompleteMultipartUpload>`
	req := httptest.NewRequest(http.MethodPost, "/test-bucket/test-key?uploadId=abc123", strings.NewReader(body))
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.CompleteMultipartUpload(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "InternalError" {
		t.Fatalf("error code = %q, want %q", code, "InternalError")
	}
	if store.writeCalled {
		t.Fatal("CompleteMultipartUpload storage complete must not run when GetBucketVersioning fails")
	}
}
