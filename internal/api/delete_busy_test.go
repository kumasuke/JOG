package api

import (
	"context"
	"net/http"
	"net/http/httptest"
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
