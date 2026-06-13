package api

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kumasuke/jog/internal/storage"
)

// deleteBusyStorage models the synchronous delete paths hitting the storage
// engine's fail-fast transaction (BEGIN IMMEDIATE with the short engine
// busy_timeout) under write contention: withImmediateTx returns
// storage.ErrBusy. Pre-#54 the autocommit delete waited the full 5s default
// busy_timeout and effectively never surfaced a transient busy error, so the
// handler must now translate ErrBusy into a retryable 503 SlowDown rather than
// reporting a phantom success (204) or a permanent failure (500).
type deleteBusyStorage struct {
	mockStorage
	versioning storage.VersioningStatus
}

func (s *deleteBusyStorage) GetBucketVersioning(ctx context.Context, bucket string) (storage.VersioningStatus, error) {
	return s.versioning, nil
}

func (s *deleteBusyStorage) DeleteObject(ctx context.Context, bucket, key string) error {
	return storage.ErrBusy
}

func (s *deleteBusyStorage) DeleteObjectVersioned(ctx context.Context, bucket, key, versionID string, versionTargeted bool) (string, bool, error) {
	return "", false, storage.ErrBusy
}

// HeadBucket lets the batch DeleteObjects up-front bucket-existence check pass
// so the per-entry delete loop is the path under test. The handler discards
// the returned *storage.Bucket (it only inspects the error), so nil is fine.
func (s *deleteBusyStorage) HeadBucket(ctx context.Context, bucket string) (*storage.Bucket, error) {
	return nil, nil
}

// parseDeleteResult unmarshals a DeleteObjects (POST ?delete) response body.
// Unlike single-object deletes, the batch endpoint replies HTTP 200 with a
// <DeleteResult> envelope whose per-key failures are nested <Error> elements,
// so the top-level parseS3ErrorCode helper does not apply here.
func parseDeleteResult(t *testing.T, body string) DeleteResult {
	t.Helper()
	var result DeleteResult
	if err := xml.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("failed to parse DeleteResult XML: %v\nbody: %s", err, body)
	}
	return result
}

// TestDeleteObject_BusyMapsToSlowDown covers finding (1): the non-versioned
// DeleteObject path must not swallow storage.ErrBusy as a phantom 204 success.
func TestDeleteObject_BusyMapsToSlowDown(t *testing.T) {
	store := &deleteBusyStorage{versioning: storage.VersioningStatusDisabled}
	h := &Handler{storage: store}

	req := httptest.NewRequest(http.MethodDelete, "/test-bucket/test-key", nil)
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.DeleteObject(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "SlowDown" {
		t.Fatalf("error code = %q, want %q", code, "SlowDown")
	}
}

// newDeleteObjectsRequest builds a POST /{bucket}?delete request for a single
// key (no VersionId) with the bucket/key routing context the handler reads.
func newDeleteObjectsRequest(bucket, key string) *http.Request {
	body := "<Delete><Object><Key>" + key + "</Key></Object></Delete>"
	req := httptest.NewRequest(http.MethodPost, "/"+bucket+"?delete", strings.NewReader(body))
	return setContext(req, bucket, "")
}

// TestDeleteObjectsBatch_NonVersioned_BusyMapsToSlowDown covers finding (2) for
// the batch DeleteObjects non-versioned branch (object.go:709). DeleteObject
// now routes through the engine's fail-fast transaction, so storage.ErrBusy is
// a routine contention outcome and must surface as the retryable per-key
// SlowDown code, not the non-canonical InternalError.
func TestDeleteObjectsBatch_NonVersioned_BusyMapsToSlowDown(t *testing.T) {
	store := &deleteBusyStorage{versioning: storage.VersioningStatusDisabled}
	h := &Handler{storage: store}

	rr := httptest.NewRecorder()
	h.DeleteObjects(rr, newDeleteObjectsRequest("test-bucket", "test-key"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	result := parseDeleteResult(t, rr.Body.String())
	if len(result.Deleted) != 0 {
		t.Fatalf("Deleted = %v, want empty (busy key must not report phantom success)", result.Deleted)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors length = %d, want 1; body: %s", len(result.Errors), rr.Body.String())
	}
	if result.Errors[0].Code != ErrSlowDown.Code {
		t.Fatalf("error code = %q, want %q", result.Errors[0].Code, ErrSlowDown.Code)
	}
}

// TestDeleteObjectsBatch_Versioned_BusyMapsToSlowDown covers finding (1) for
// the batch DeleteObjects versioned branch (object.go:685). An unspecified
// versionId on a versioning-Enabled bucket is the delete-marker-creation path,
// which skips lock evaluation and reaches DeleteObjectVersioned directly; its
// storage.ErrBusy must map to the retryable per-key SlowDown code.
func TestDeleteObjectsBatch_Versioned_BusyMapsToSlowDown(t *testing.T) {
	store := &deleteBusyStorage{versioning: storage.VersioningStatusEnabled}
	h := &Handler{storage: store}

	rr := httptest.NewRecorder()
	h.DeleteObjects(rr, newDeleteObjectsRequest("test-bucket", "test-key"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	result := parseDeleteResult(t, rr.Body.String())
	if len(result.Deleted) != 0 {
		t.Fatalf("Deleted = %v, want empty (busy key must not report phantom success)", result.Deleted)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors length = %d, want 1; body: %s", len(result.Errors), rr.Body.String())
	}
	if result.Errors[0].Code != ErrSlowDown.Code {
		t.Fatalf("error code = %q, want %q", result.Errors[0].Code, ErrSlowDown.Code)
	}
}

// TestDeleteObjectVersioned_BusyMapsToSlowDown covers finding (2): the
// versioned DeleteObject path must map storage.ErrBusy to a retryable 503
// SlowDown rather than a permanent 500 InternalError. A DELETE with no
// versionId on a versioning-Enabled bucket is the delete-marker-creation path,
// which skips lock evaluation and reaches DeleteObjectVersioned directly.
func TestDeleteObjectVersioned_BusyMapsToSlowDown(t *testing.T) {
	store := &deleteBusyStorage{versioning: storage.VersioningStatusEnabled}
	h := &Handler{storage: store}

	req := httptest.NewRequest(http.MethodDelete, "/test-bucket/test-key", nil)
	req = setContext(req, "test-bucket", "test-key")
	rr := httptest.NewRecorder()

	h.DeleteObject(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "SlowDown" {
		t.Fatalf("error code = %q, want %q", code, "SlowDown")
	}
}
