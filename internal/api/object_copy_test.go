package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kumasuke/jog/internal/storage"
)

// copyHeadFailingStorage embeds the limit-test mockStorage and overrides
// HeadObject to return a non-sentinel error, modelling a transient backend
// outage. The CopyObject lock-check must fail closed (InternalError) rather
// than skip the lock evaluation as it did before the M-2 follow-up fix.
type copyHeadFailingStorage struct {
	mockStorage
}

var errHeadBackend = errors.New("simulated head backend failure")

func (s *copyHeadFailingStorage) HeadObject(ctx context.Context, bucket, key string) (*storage.Object, error) {
	return nil, errHeadBackend
}

// TestCopyObject_InvalidMetadataDirective verifies that an
// x-amz-metadata-directive value other than COPY or REPLACE is rejected
// with InvalidArgument before the storage layer is touched (H-10).
// Without validation the handler treated any non-COPY value (e.g. an empty
// REPLACE typo, or attacker-supplied junk) as REPLACE, silently overwriting
// metadata.
func TestCopyObject_InvalidMetadataDirective(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/dst/key", strings.NewReader(""))
	req.Header.Set("x-amz-copy-source", "/src/src-key")
	req.Header.Set("x-amz-metadata-directive", "DELETE")
	req = setContext(req, "dst", "key")
	rr := httptest.NewRecorder()

	h.CopyObject(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "InvalidArgument" {
		t.Fatalf("error code = %q, want %q", code, "InvalidArgument")
	}
}

// TestCopyObject_HeadObjectErrorFailClosed verifies that a non-sentinel
// HeadObject error during the destination lock check (CR-5 path) returns
// InternalError instead of silently skipping the check. Before the fix,
// any error other than nil suppressed the lock evaluation, so a transient
// metadata DB outage could let a copy overwrite a locked destination.
func TestCopyObject_HeadObjectErrorFailClosed(t *testing.T) {
	h := &Handler{storage: &copyHeadFailingStorage{}}

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
}

// TestCopyObject_SelfCopyWithCopyDirective verifies that a request to copy
// an object to itself with metadata-directive=COPY (i.e. nothing to change)
// is rejected with InvalidRequest, matching AWS S3 behaviour (H-10). The
// directive is the default, so without this guard a malformed client could
// trigger a no-op write that still counts against storage.
func TestCopyObject_SelfCopyWithCopyDirective(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/same/key", strings.NewReader(""))
	req.Header.Set("x-amz-copy-source", "/same/key")
	// No directive => defaults to COPY.
	req = setContext(req, "same", "key")
	rr := httptest.NewRecorder()

	h.CopyObject(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	code := parseS3ErrorCode(t, rr.Body.String())
	if code != "InvalidRequest" {
		t.Fatalf("error code = %q, want %q", code, "InvalidRequest")
	}
}

// TestCopyObject_SelfCopyWithReplaceDirectiveAllowed confirms that the
// self-copy guard only fires when metadata-directive=COPY. With REPLACE the
// client is intentionally rewriting metadata, which is a documented S3 use
// case and must continue to work.
func TestCopyObject_SelfCopyWithReplaceDirectiveAllowed(t *testing.T) {
	h := newHandlerWithMock()

	req := httptest.NewRequest(http.MethodPut, "/same/key", strings.NewReader(""))
	req.Header.Set("x-amz-copy-source", "/same/key")
	req.Header.Set("x-amz-metadata-directive", "REPLACE")
	req = setContext(req, "same", "key")
	rr := httptest.NewRecorder()

	h.CopyObject(rr, req)

	// The mockStorage's CopyObject is unimplemented and will panic or return
	// an error; we only assert that we got past the validation guard, i.e.
	// the response is NOT InvalidRequest with "self-copy" semantics.
	if rr.Code == http.StatusBadRequest {
		code := parseS3ErrorCode(t, rr.Body.String())
		if code == "InvalidRequest" {
			t.Fatalf("self-copy with REPLACE directive was rejected; want allowed (body=%s)", rr.Body.String())
		}
	}
}
