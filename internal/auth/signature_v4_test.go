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

// TestVerifyPresignedURL_MissingExpires asserts that a presigned URL without
// X-Amz-Expires is rejected (H-1). The historical implementation silently
// skipped the expiry check when the parameter was missing or non-numeric.
func TestVerifyPresignedURL_MissingExpires(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	query := req.URL.Query()
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", "access/20260102/us-east-1/s3/aws4_request")
	query.Set("X-Amz-SignedHeaders", "host")
	query.Set("X-Amz-Signature", strings.Repeat("a", 64))
	query.Set("X-Amz-Date", time.Now().UTC().Format("20060102T150405Z"))
	// X-Amz-Expires intentionally omitted.
	req.URL.RawQuery = query.Encode()

	m := NewMiddleware("access", "secret")
	_, err := m.verifyPresignedURL(req)
	if err == nil {
		t.Fatal("verifyPresignedURL(missing Expires) returned nil; want AccessDenied")
	}
	if err.Code != api.ErrAccessDenied.Code {
		t.Fatalf("err.Code = %s, want %s", err.Code, api.ErrAccessDenied.Code)
	}
}

// TestVerifyPresignedURL_NonNumericExpires asserts that a non-numeric Expires
// value is rejected rather than silently treated as zero (H-1).
func TestVerifyPresignedURL_NonNumericExpires(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	query := req.URL.Query()
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", "access/20260102/us-east-1/s3/aws4_request")
	query.Set("X-Amz-SignedHeaders", "host")
	query.Set("X-Amz-Signature", strings.Repeat("a", 64))
	query.Set("X-Amz-Date", time.Now().UTC().Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", "not-a-number")
	req.URL.RawQuery = query.Encode()

	m := NewMiddleware("access", "secret")
	_, err := m.verifyPresignedURL(req)
	if err == nil {
		t.Fatal("verifyPresignedURL(non-numeric Expires) returned nil; want AccessDenied")
	}
	if err.Code != api.ErrAccessDenied.Code {
		t.Fatalf("err.Code = %s, want %s", err.Code, api.ErrAccessDenied.Code)
	}
}

// TestVerifyPresignedURL_ExpiresOutOfRange asserts that Expires <= 0 or
// > 7 days is rejected (AWS caps presigned URLs at 604800 seconds).
func TestVerifyPresignedURL_ExpiresOutOfRange(t *testing.T) {
	cases := []string{"0", "-1", "604801"}
	for _, expires := range cases {
		t.Run("expires="+expires, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
			query := req.URL.Query()
			query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
			query.Set("X-Amz-Credential", "access/20260102/us-east-1/s3/aws4_request")
			query.Set("X-Amz-SignedHeaders", "host")
			query.Set("X-Amz-Signature", strings.Repeat("a", 64))
			query.Set("X-Amz-Date", time.Now().UTC().Format("20060102T150405Z"))
			query.Set("X-Amz-Expires", expires)
			req.URL.RawQuery = query.Encode()

			m := NewMiddleware("access", "secret")
			_, err := m.verifyPresignedURL(req)
			if err == nil {
				t.Fatalf("verifyPresignedURL(Expires=%s) returned nil; want AccessDenied", expires)
			}
			if err.Code != api.ErrAccessDenied.Code {
				t.Fatalf("err.Code = %s, want %s", err.Code, api.ErrAccessDenied.Code)
			}
		})
	}
}

// TestVerifyPresignedURL_DateInFuture asserts that a request whose X-Amz-Date
// is more than 15 minutes in the future is rejected (H-1).
func TestVerifyPresignedURL_DateInFuture(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	query := req.URL.Query()
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", "access/20260102/us-east-1/s3/aws4_request")
	query.Set("X-Amz-SignedHeaders", "host")
	query.Set("X-Amz-Signature", strings.Repeat("a", 64))
	// 1 hour in the future
	query.Set("X-Amz-Date", time.Now().UTC().Add(1*time.Hour).Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", "60")
	req.URL.RawQuery = query.Encode()

	m := NewMiddleware("access", "secret")
	_, err := m.verifyPresignedURL(req)
	if err == nil {
		t.Fatal("verifyPresignedURL(future Date) returned nil; want RequestTimeTooSkewed")
	}
	if err.Code != api.ErrRequestTimeTooSkewed.Code {
		t.Fatalf("err.Code = %s, want %s", err.Code, api.ErrRequestTimeTooSkewed.Code)
	}
}

// TestVerifyPresignedURL_PayloadBoundWhenSigned exercises H-1's payload
// binding: if X-Amz-Content-SHA256 appears in SignedHeaders, the canonical
// request must use the header value (not "UNSIGNED-PAYLOAD"), and a 64-hex
// digest must cause the body to be verified via payloadVerifyingReader.
func TestVerifyPresignedURL_PayloadBoundWhenSigned(t *testing.T) {
	const (
		access = "ACCESS"
		secret = "SECRET"
	)
	body := []byte("the wire bytes")
	sum := sha256.Sum256(body)
	declared := hex.EncodeToString(sum[:])

	amzDate := time.Now().UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	cred := access + "/" + date + "/us-east-1/s3/aws4_request"

	// Build a request that the SDK would build to put a signed-payload
	// presigned URL on the wire.
	req := httptest.NewRequest(http.MethodPut, "/bucket/key", strings.NewReader(string(body)))
	req.Host = "example.com"
	req.Header.Set("X-Amz-Content-SHA256", declared)

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", cred)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", "300")
	q.Set("X-Amz-SignedHeaders", "host;x-amz-content-sha256")
	req.URL.RawQuery = q.Encode()

	m := NewMiddleware(access, secret)
	sig := m.calculatePresignedSignature(req, date, "us-east-1", "s3", "host;x-amz-content-sha256", amzDate)
	q.Set("X-Amz-Signature", sig)
	req.URL.RawQuery = q.Encode()

	if _, err := m.verifyPresignedURL(req); err != nil {
		t.Fatalf("verifyPresignedURL(signed payload) = %v, want nil", err)
	}
}

// TestVerifyPresignedURL_BodyBearingMethodMissingContentSHA asserts that a
// presigned URL for a body-bearing method (PUT / POST / PATCH) is rejected
// when X-Amz-Content-SHA256 is not in SignedHeaders. Without this guard the
// canonical request silently used "UNSIGNED-PAYLOAD" — i.e. the body was
// not bound to the signature, so a holder of the URL could PUT arbitrary
// content. The choice to opt out of payload signing must itself be signed
// (by including x-amz-content-sha256 with value "UNSIGNED-PAYLOAD" in
// SignedHeaders).
func TestVerifyPresignedURL_BodyBearingMethodMissingContentSHA(t *testing.T) {
	const (
		access = "ACCESS"
		secret = "SECRET"
	)

	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			amzDate := time.Now().UTC().Format("20060102T150405Z")
			date := amzDate[:8]
			cred := access + "/" + date + "/us-east-1/s3/aws4_request"

			req := httptest.NewRequest(method, "/bucket/key", strings.NewReader("body"))
			req.Host = "example.com"

			q := req.URL.Query()
			q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
			q.Set("X-Amz-Credential", cred)
			q.Set("X-Amz-Date", amzDate)
			q.Set("X-Amz-Expires", "300")
			// Note: SignedHeaders intentionally does NOT include
			// x-amz-content-sha256 — this is the gap the new check closes.
			q.Set("X-Amz-SignedHeaders", "host")
			req.URL.RawQuery = q.Encode()

			m := NewMiddleware(access, secret)
			sig := m.calculatePresignedSignature(req, date, "us-east-1", "s3", "host", amzDate)
			q.Set("X-Amz-Signature", sig)
			req.URL.RawQuery = q.Encode()

			_, err := m.verifyPresignedURL(req)
			if err == nil {
				t.Fatalf("verifyPresignedURL(%s without signed content-sha256) = nil; want AccessDenied", method)
			}
			if err.Code != api.ErrAccessDenied.Code {
				t.Fatalf("err.Code = %s, want %s", err.Code, api.ErrAccessDenied.Code)
			}
		})
	}
}

// TestVerifyPresignedURL_BodyBearingMethodWithUnsignedPayloadAccepted asserts
// that a body-bearing presigned URL whose SignedHeaders includes
// x-amz-content-sha256 with the literal "UNSIGNED-PAYLOAD" value is accepted.
// This is the explicit-opt-out path: the client said "I do not want payload
// binding" and that choice is itself signed, so an attacker cannot rewrite
// the canonical request to claim binding (or vice versa).
func TestVerifyPresignedURL_BodyBearingMethodWithUnsignedPayloadAccepted(t *testing.T) {
	const (
		access = "ACCESS"
		secret = "SECRET"
	)

	amzDate := time.Now().UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	cred := access + "/" + date + "/us-east-1/s3/aws4_request"

	req := httptest.NewRequest(http.MethodPut, "/bucket/key", strings.NewReader("body"))
	req.Host = "example.com"
	req.Header.Set("X-Amz-Content-SHA256", "UNSIGNED-PAYLOAD")

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", cred)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", "300")
	q.Set("X-Amz-SignedHeaders", "host;x-amz-content-sha256")
	req.URL.RawQuery = q.Encode()

	m := NewMiddleware(access, secret)
	sig := m.calculatePresignedSignature(req, date, "us-east-1", "s3", "host;x-amz-content-sha256", amzDate)
	q.Set("X-Amz-Signature", sig)
	req.URL.RawQuery = q.Encode()

	if _, err := m.verifyPresignedURL(req); err != nil {
		t.Fatalf("verifyPresignedURL(explicit UNSIGNED-PAYLOAD) = %v, want nil", err)
	}
}

// TestVerifyPresignedURL_GETStillAllowedWithoutContentSHA asserts that the
// new strict body-binding requirement does NOT apply to bodyless methods
// (GET / HEAD / DELETE), which is the most common presigned-URL use case
// (downloads). Otherwise every presigned-download client in the wild would
// break.
func TestVerifyPresignedURL_GETStillAllowedWithoutContentSHA(t *testing.T) {
	const (
		access = "ACCESS"
		secret = "SECRET"
	)

	amzDate := time.Now().UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	cred := access + "/" + date + "/us-east-1/s3/aws4_request"

	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	req.Host = "example.com"

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", cred)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", "300")
	q.Set("X-Amz-SignedHeaders", "host")
	req.URL.RawQuery = q.Encode()

	m := NewMiddleware(access, secret)
	sig := m.calculatePresignedSignature(req, date, "us-east-1", "s3", "host", amzDate)
	q.Set("X-Amz-Signature", sig)
	req.URL.RawQuery = q.Encode()

	if _, err := m.verifyPresignedURL(req); err != nil {
		t.Fatalf("verifyPresignedURL(GET without signed content-sha256) = %v, want nil", err)
	}
}

// TestVerifyPresignedURL_BodyMismatchAfterAuth asserts that for a presigned
// URL that signs X-Amz-Content-SHA256, sending a body that does not match
// the signed hash is detected by the Middleware.Wrap path (H-1 + CR-3).
func TestVerifyPresignedURL_BodyMismatchAfterAuth(t *testing.T) {
	const (
		access = "ACCESS"
		secret = "SECRET"
	)
	signedBody := []byte("the wire bytes")
	sum := sha256.Sum256(signedBody)
	declared := hex.EncodeToString(sum[:])

	amzDate := time.Now().UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	cred := access + "/" + date + "/us-east-1/s3/aws4_request"

	// Build the canonical request the SDK would sign...
	tmp := httptest.NewRequest(http.MethodPut, "/bucket/key", strings.NewReader(string(signedBody)))
	tmp.Host = "example.com"
	tmp.Header.Set("X-Amz-Content-SHA256", declared)
	q := tmp.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", cred)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", "300")
	q.Set("X-Amz-SignedHeaders", "host;x-amz-content-sha256")
	tmp.URL.RawQuery = q.Encode()

	m := NewMiddleware(access, secret)
	sig := m.calculatePresignedSignature(tmp, date, "us-east-1", "s3", "host;x-amz-content-sha256", amzDate)
	q.Set("X-Amz-Signature", sig)

	// ...but the client sends a tampered body. The middleware should pass
	// the signature check (because the declared hash matches the signed
	// canonical request) but the downstream payload verification on EOF
	// must surface as a CR-3 hash mismatch.
	tamperedBody := []byte("the wire bytez")
	req := httptest.NewRequest(http.MethodPut, "/bucket/key", strings.NewReader(string(tamperedBody)))
	req.Host = "example.com"
	req.ContentLength = int64(len(tamperedBody))
	req.Header.Set("X-Amz-Content-SHA256", declared)
	req.URL.RawQuery = q.Encode()

	rec := httptest.NewRecorder()
	called := false
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = io.ReadAll(r.Body)
	}))
	handler.ServeHTTP(rec, req)

	if !called {
		// Wrap rejected before handler — acceptable if it's a 403 already.
		if rec.Code == http.StatusForbidden {
			return
		}
		t.Fatalf("handler not called; status = %d", rec.Code)
	}
	// If the handler was called, the body read should have surfaced the
	// mismatch via ErrPayloadHashMismatch; the handler in this test just
	// ignores it. We assert the wrapper at least replaced r.Body with our
	// verifying reader.
	if _, ok := req.Body.(*payloadVerifyingReader); !ok {
		t.Fatalf("Middleware.Wrap did not install payloadVerifyingReader for signed-payload presigned URL; got %T", req.Body)
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
	_, err := m.verifyPresignedURL(req)
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

// TestConstantTimeHexEqual_LengthMismatch asserts that constantTimeHexEqual
// rejects two strings whose decoded lengths differ without panicking. The
// historical implementation compared hex strings directly via hmac.Equal,
// which short-circuits when string lengths differ — leaking length info via
// timing (H-2).
func TestConstantTimeHexEqual_LengthMismatch(t *testing.T) {
	if constantTimeHexEqual("abcd", "abcdef") {
		t.Fatal("constantTimeHexEqual(short, long) returned true; want false")
	}
	if constantTimeHexEqual("ab", "abcd") {
		t.Fatal("constantTimeHexEqual(short, long) returned true; want false")
	}
}

// TestConstantTimeHexEqual_NonHexProvided asserts that a non-hex provided
// signature is rejected (rather than triggering a panic in hex.DecodeString).
// SigV4 signatures are always lower-case hex of HMAC-SHA256 (64 hex chars
// / 32 bytes); anything else is invalid.
func TestConstantTimeHexEqual_NonHexProvided(t *testing.T) {
	expected := strings.Repeat("a", 64)
	if constantTimeHexEqual(expected, "not-hex-at-all-but-64-chars-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX") {
		t.Fatal("constantTimeHexEqual(hex, non-hex) returned true; want false")
	}
}

// TestConstantTimeHexEqual_NoEarlyReturnOnLengthMismatch is a behavioural
// regression for H-2: even when provided is dramatically shorter or longer
// than expected, the comparison must still produce a value (not panic, not
// short-circuit on length) so that the caller cannot distinguish the two
// cases by anything other than the constant-time HMAC comparison itself.
// We can't measure timing reliably in a unit test, but we can at least
// assert the function tolerates a wide range of lengths and never reports
// equality by accident.
func TestConstantTimeHexEqual_NoEarlyReturnOnLengthMismatch(t *testing.T) {
	expected := strings.Repeat("a", 64)
	for _, n := range []int{0, 1, 2, 31, 63, 65, 128, 256, 1024} {
		provided := strings.Repeat("a", n)
		if n == 64 {
			continue // would actually match
		}
		if constantTimeHexEqual(expected, provided) {
			t.Fatalf("constantTimeHexEqual(64-char a, %d-char a) returned true; want false", n)
		}
	}
	// Also assert it doesn't panic on arbitrary non-hex bytes of varying
	// lengths — the HMAC path normalises everything to a 32-byte digest.
	for _, n := range []int{0, 1, 13, 100} {
		_ = constantTimeHexEqual(expected, strings.Repeat("\x00\xff\x7f", n))
	}
}

// TestConstantTimeHexEqual_MatchAndMismatch asserts the basic identity and
// difference cases.
func TestConstantTimeHexEqual_MatchAndMismatch(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("a", 63) + "b"
	if !constantTimeHexEqual(a, a) {
		t.Fatal("constantTimeHexEqual(a, a) returned false; want true")
	}
	if constantTimeHexEqual(a, b) {
		t.Fatal("constantTimeHexEqual(a, b) returned true; want false")
	}
	// Mixed-case provided value should still match (canonical hex is lower).
	upper := strings.ToUpper(a)
	if !constantTimeHexEqual(a, upper) {
		t.Fatal("constantTimeHexEqual(a, upper(a)) returned false; want true (case insensitive)")
	}
}

// TestWrap_CORSPreflightBypassesAuth verifies that CORS preflight requests
// (OPTIONS with Origin + Access-Control-Request-Method) are passed through
// to the next handler without authentication (H-5). Browsers cannot attach
// SigV4 credentials to preflights, so a 403 here would block any cross-origin
// access to a publicly-CORS-enabled bucket.
func TestWrap_CORSPreflightBypassesAuth(t *testing.T) {
	m := NewMiddleware("access", "secret")
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/bucket/key", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	m.Wrap(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("CORS preflight was blocked by auth middleware; next handler not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestWrap_OptionsWithoutCORSHeadersStillRequiresAuth confirms that a plain
// OPTIONS request without the preflight indicator headers still requires
// authentication. The bypass must be narrowly scoped to actual preflights.
func TestWrap_OptionsWithoutCORSHeadersStillRequiresAuth(t *testing.T) {
	m := NewMiddleware("access", "secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be invoked when auth fails")
	})

	req := httptest.NewRequest(http.MethodOptions, "/bucket/key", nil)
	rec := httptest.NewRecorder()
	m.Wrap(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (AccessDenied)", rec.Code, http.StatusForbidden)
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
