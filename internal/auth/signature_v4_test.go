package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/api"
)

func TestCanonicalQueryStringSortsAndEncodes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	req.URL.RawQuery = url.Values{
		"multi": []string{"b", "a"},
		"slash": []string{"a/b"},
		"space": []string{"a b"},
		"z":     []string{"last"},
	}.Encode()
	m := NewMiddleware("access", "secret")

	got := m.canonicalQueryString(req)
	// SigV4 canonicalization requires spaces as %20, not '+', and '/' in query
	// values must be encoded as %2F.
	want := "multi=a&multi=b&slash=a%2Fb&space=a%20b&z=last"
	if got != want {
		t.Fatalf("canonicalQueryString() = %q, want %q", got, want)
	}
}

func TestCreateCanonicalRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/bucket/a%20key?partNumber=1&uploadId=xyz", strings.NewReader("body"))
	req.Host = "example.com"
	req.Header.Set("X-Amz-Date", "20260102T030405Z")
	req.Header.Set("X-Amz-Content-SHA256", "payload-hash")
	req.Header.Set("Content-Type", "text/plain")

	m := NewMiddleware("access", "secret")
	got := m.createCanonicalRequest(req, "host;content-type;x-amz-date")

	want := strings.Join([]string{
		"PUT",
		"/bucket/a%20key",
		"partNumber=1&uploadId=xyz",
		"content-type:text/plain",
		"host:example.com",
		"x-amz-date:20260102T030405Z",
		"",
		"host;content-type;x-amz-date",
		"payload-hash",
	}, "\n")
	if got != want {
		t.Fatalf("canonical request mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestVerifySignatureV4RejectsInvalidInputs(t *testing.T) {
	m := NewMiddleware("access", "secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Amz-Date", time.Now().UTC().Format("20060102T150405Z"))

	tests := []struct {
		name string
		auth string
	}{
		{
			name: "wrong scheme",
			auth: "Basic abc",
		},
		{
			name: "missing signature",
			auth: "AWS4-HMAC-SHA256 Credential=access/20260102/us-east-1/s3/aws4_request,SignedHeaders=host",
		},
		{
			name: "malformed credential",
			auth: "AWS4-HMAC-SHA256 Credential=access/20260102,SignedHeaders=host,Signature=abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.verifySignatureV4(req, tt.auth)
			if err == nil {
				t.Fatal("verifySignatureV4() returned nil error")
			}
			if err.Code != api.ErrAccessDenied.Code {
				t.Fatalf("error code = %s, want %s", err.Code, api.ErrAccessDenied.Code)
			}
		})
	}
}

func TestVerifyPresignedURLRejectsExpiredRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	query := req.URL.Query()
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", "access/20260102/us-east-1/s3/aws4_request")
	query.Set("X-Amz-SignedHeaders", "host")
	query.Set("X-Amz-Signature", "abc")
	query.Set("X-Amz-Date", time.Now().UTC().Add(-2*time.Hour).Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", "60")
	req.URL.RawQuery = query.Encode()

	m := NewMiddleware("access", "secret")
	err := m.verifyPresignedURL(req)
	if err == nil {
		t.Fatal("verifyPresignedURL() returned nil error")
	}
	if err.Code != api.ErrRequestTimeTooSkewed.Code {
		t.Fatalf("error code = %s, want %s", err.Code, api.ErrRequestTimeTooSkewed.Code)
	}
}

// TestWrapPayload_UnsignedPayloadSkipsVerification asserts that a request
// declaring UNSIGNED-PAYLOAD passes through wrapPayload untouched.
func TestWrapPayload_UnsignedPayloadSkipsVerification(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/b/o", strings.NewReader("anything-goes"))
	req.Header.Set("X-Amz-Content-SHA256", "UNSIGNED-PAYLOAD")
	original := req.Body

	if err := wrapPayload(req, &sigCtx{payloadHashHeader: "UNSIGNED-PAYLOAD"}); err != nil {
		t.Fatalf("wrapPayload(UNSIGNED-PAYLOAD) = %v, want nil", err)
	}
	if req.Body != original {
		t.Fatal("wrapPayload(UNSIGNED-PAYLOAD) replaced r.Body; want passthrough")
	}
}

// TestWrapPayload_HexHashMismatchFails asserts that when the request body's
// SHA-256 does not match the value declared in X-Amz-Content-SHA256, the
// wrapped body returns an error on EOF (CR-3).
func TestWrapPayload_HexHashMismatchFails(t *testing.T) {
	body := "the wire bytes"
	declared := strings.Repeat("0", 64) // sha256("") prefix - definitely not body's hash

	req := httptest.NewRequest(http.MethodPut, "/b/o", strings.NewReader(body))
	req.Header.Set("X-Amz-Content-SHA256", declared)

	if err := wrapPayload(req, &sigCtx{payloadHashHeader: declared}); err != nil {
		t.Fatalf("wrapPayload returned S3Error %v", err)
	}
	_, err := io.ReadAll(req.Body)
	if err == nil {
		t.Fatal("io.ReadAll on mismatched-hash body returned nil error; want CR-3 failure")
	}
	if !IsPayloadHashMismatch(err) {
		t.Fatalf("err = %v, want IsPayloadHashMismatch", err)
	}
}

// TestWrapPayload_HexHashMatchSucceeds asserts that when the body matches
// the declared hex digest, the wrapped body returns the bytes verbatim and
// io.EOF without error.
func TestWrapPayload_HexHashMatchSucceeds(t *testing.T) {
	body := "the wire bytes"
	sum := sha256.Sum256([]byte(body))
	declared := hex.EncodeToString(sum[:])

	req := httptest.NewRequest(http.MethodPut, "/b/o", strings.NewReader(body))
	req.Header.Set("X-Amz-Content-SHA256", declared)

	if err := wrapPayload(req, &sigCtx{payloadHashHeader: declared}); err != nil {
		t.Fatalf("wrapPayload returned S3Error %v", err)
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("io.ReadAll on matching-hash body returned %v", err)
	}
	if string(got) != body {
		t.Fatalf("got %q, want %q", got, body)
	}
}

// TestWrapPayload_UnknownSentinelIsRejected asserts that an unrecognised
// payload-hash sentinel (i.e. not UNSIGNED-PAYLOAD, not the streaming
// constant, and not a 64-char hex) is treated as a SignatureDoesNotMatch.
func TestWrapPayload_UnknownSentinelIsRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/b/o", strings.NewReader("x"))
	req.Header.Set("X-Amz-Content-SHA256", "NOT-A-REAL-SENTINEL")

	err := wrapPayload(req, &sigCtx{payloadHashHeader: "NOT-A-REAL-SENTINEL"})
	if err == nil {
		t.Fatal("wrapPayload(unknown sentinel) = nil; want SignatureDoesNotMatch")
	}
	if err.Code != api.ErrSignatureDoesNotMatch.Code {
		t.Fatalf("err.Code = %s, want %s", err.Code, api.ErrSignatureDoesNotMatch.Code)
	}
}

func TestDisabledMiddlewareReturnsOriginalHandler(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	wrapped := NewDisabledMiddleware().Wrap(next)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
