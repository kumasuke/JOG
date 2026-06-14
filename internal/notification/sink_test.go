package notification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestWebhookSink_Non2xxIsError pins the Sink contract: a non-2xx response is an
// error so the dispatcher can log the failure (best-effort, not retried in v1).
func TestWebhookSink_Non2xxIsError(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusForbidden} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			sink := NewWebhookSink(&http.Client{Timeout: 2 * time.Second})
			err := sink.Deliver(context.Background(), srv.URL, Envelope{Records: []Event{}})
			if err == nil {
				t.Fatalf("Deliver to a %d endpoint returned nil, want error", code)
			}
		})
	}
}

// TestWebhookSink_2xxIsSuccess confirms the happy path returns no error.
func TestWebhookSink_2xxIsSuccess(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			sink := NewWebhookSink(&http.Client{Timeout: 2 * time.Second})
			if err := sink.Deliver(context.Background(), srv.URL, Envelope{Records: []Event{}}); err != nil {
				t.Fatalf("Deliver to a %d endpoint returned %v, want nil", code, err)
			}
		})
	}
}

// TestWebhookSink_TransportErrorIsError confirms a transport failure (no server)
// surfaces as an error rather than being swallowed.
func TestWebhookSink_TransportErrorIsError(t *testing.T) {
	sink := NewWebhookSink(&http.Client{Timeout: 200 * time.Millisecond})
	// Reserved-for-documentation address that should not accept connections.
	err := sink.Deliver(context.Background(), "http://127.0.0.1:0", Envelope{Records: []Event{}})
	if err == nil {
		t.Fatal("Deliver to an unreachable endpoint returned nil, want error")
	}
}

// TestWebhookSink_RejectsNonHTTPScheme pins the scheme allowlist: a non-http(s)
// target (file://, gopher://, a relative URL) is rejected before any dial, so a
// misconfigured target cannot reach the local filesystem or odd protocols.
func TestWebhookSink_RejectsNonHTTPScheme(t *testing.T) {
	for _, url := range []string{
		"file:///etc/passwd",
		"gopher://example.test/_data",
		"ftp://example.test/x",
		"/relative/path",
	} {
		t.Run(url, func(t *testing.T) {
			sink := NewWebhookSink(&http.Client{Timeout: 2 * time.Second})
			if err := sink.Deliver(context.Background(), url, Envelope{Records: []Event{}}); err == nil {
				t.Fatalf("Deliver to %q returned nil, want scheme rejection", url)
			}
		})
	}
}
