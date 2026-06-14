package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// TestSigV4RoundTrip_MultiValueSort verifies that a request signed by the
// aws-sdk-go-v2 signer with a multi-value query parameter whose raw-value
// sort order and percent-encoded sort order differ is accepted (200) by JOG.
//
// Concretely, Param1 has values "@" and ".":
//
//	raw-value sort:    '.'(0x2E) < '@'(0x40)  => ".", "@"
//	encoded sort:      "%40"     < "."(0x2E)  => "@", "."
//
// aws-sdk-go-v2 v1.32.8 (aws/signer/v4/v4.go:153) sorts multi-values with
// sort.Strings(query[key]) — i.e. on the raw (decoded) values — and then
// calls query.Encode() which percent-encodes each value. So the SDK puts the
// "." entry first in the canonical query string ("Param1=.&Param1=%40"), and
// the signature it sends covers that ordering.
//
// If JOG's canonicalQueryString sorts by the encoded representation instead
// (producing "Param1=%40&Param1=."), the canonical strings diverge and JOG
// will return 403 SignatureDoesNotMatch for a valid request.
//
// This test is intentionally a Red test against the PR-as-committed code:
// run it before the fix to confirm it returns 403, run it after to confirm 200.
func TestSigV4RoundTrip_MultiValueSort(t *testing.T) {
	const (
		accessKey = "AKIDEXAMPLE"
		secretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		service   = "s3"
		region    = "us-east-1"
		// SHA-256 of the empty payload.
		emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	)

	// Build the request with the two values in wire order "@", "."
	// (differs from both sort orders to ensure the test is not vacuous).
	req := httptest.NewRequest(http.MethodGet, "/?Param1=%40&Param1=.", nil)
	req.Host = "example.amazonaws.com"
	req.Header.Set("X-Amz-Content-SHA256", emptyPayloadHash)

	// Sign the request with the real aws-sdk-go-v2 signer.
	signer := v4.NewSigner()
	creds := aws.Credentials{
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
	}
	signingTime := time.Now().UTC()

	if err := signer.SignHTTP(
		context.Background(),
		creds,
		req,
		emptyPayloadHash,
		service,
		region,
		signingTime,
	); err != nil {
		t.Fatalf("SignHTTP failed: %v", err)
	}

	// Pass the signed request through JOG's auth middleware.
	m := NewMiddleware(accessKey, secretKey)
	called := false
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SDK-signed request with multi-value Param1=[@,.] was rejected: got HTTP %d, want 200 OK\n"+
			"This means JOG's canonical query sort order diverges from the SDK's raw-value sort.\n"+
			"Authorization header: %s",
			rec.Code,
			req.Header.Get("Authorization"),
		)
	}
	if !called {
		t.Fatal("handler was not called; auth middleware blocked the request")
	}
}
