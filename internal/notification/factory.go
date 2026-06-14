package notification

import (
	"net/http"
	"time"
)

// FromTargets builds a webhook-backed Dispatcher from an ARN→URL map and a
// region. It returns nil when no targets are configured, so callers can treat a
// nil *Dispatcher as "notifications disabled" (Dispatch is a no-op on nil).
// deliveryTimeout caps both the HTTP client and the per-delivery context; a
// non-positive value defaults to 10s.
func FromTargets(arnToURL map[string]string, region string, deliveryTimeout time.Duration) *Dispatcher {
	if len(arnToURL) == 0 {
		return nil
	}
	if deliveryTimeout <= 0 {
		deliveryTimeout = 10 * time.Second
	}
	sink := NewWebhookSink(&http.Client{Timeout: deliveryTimeout})
	resolver := NewResolver(arnToURL)
	return NewDispatcher(sink, resolver, region, deliveryTimeout)
}
