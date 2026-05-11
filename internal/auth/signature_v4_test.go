package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
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
			if err := m.verifySignatureV4(req, tt.auth); err == nil {
				t.Fatal("verifySignatureV4() returned nil error")
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
	if err := m.verifyPresignedURL(req); err == nil {
		t.Fatal("verifyPresignedURL() returned nil error")
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
