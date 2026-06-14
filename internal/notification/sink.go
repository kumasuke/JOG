package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Sink delivers a notification envelope to one resolved destination. The webhook
// sink posts JSON over HTTP; future sinks (SNS/SQS/Lambda/EventBridge) implement
// the same interface so the dispatcher does not change when they are added.
type Sink interface {
	// Deliver sends env to target. It returns an error on a non-2xx response or a
	// transport failure; the dispatcher logs (does not retry) in v1.
	Deliver(ctx context.Context, target string, env Envelope) error
}

// WebhookSink posts the envelope as JSON to an HTTP(S) endpoint.
type WebhookSink struct {
	client *http.Client
}

// NewWebhookSink builds a webhook sink using the given HTTP client. A nil client
// uses http.DefaultClient; callers should pass a client with a timeout so a slow
// endpoint cannot pin a delivery goroutine indefinitely.
func NewWebhookSink(client *http.Client) *WebhookSink {
	if client == nil {
		client = http.DefaultClient
	}
	return &WebhookSink{client: client}
}

// Deliver POSTs env to url as application/json. A non-2xx status is an error.
func (s *WebhookSink) Deliver(ctx context.Context, url string, env Envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("notification: marshal envelope: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notification: build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("notification: webhook POST %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification: webhook %s returned status %d", url, resp.StatusCode)
	}
	return nil
}
