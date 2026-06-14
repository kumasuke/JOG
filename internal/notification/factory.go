package notification

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// FromTargets builds a webhook-backed Dispatcher from an ARN→URL map and a
// region. It returns nil when no targets are configured, so callers can treat a
// nil *Dispatcher as "notifications disabled" (Dispatch is a no-op on nil).
// deliveryTimeout caps both the HTTP client and the per-delivery context; a
// non-positive value defaults to 10s. When blockPrivate is true, delivery to
// loopback/link-local/private/unspecified addresses is refused at dial time
// (SSRF defense-in-depth; see dialControlBlockPrivate).
func FromTargets(arnToURL map[string]string, region string, deliveryTimeout time.Duration, blockPrivate bool) *Dispatcher {
	if len(arnToURL) == 0 {
		return nil
	}
	if deliveryTimeout <= 0 {
		deliveryTimeout = 10 * time.Second
	}
	// Bound sockets per webhook host so a burst of deliveries reuses connections
	// instead of opening one socket per POST. The dispatcher's global goroutine
	// semaphore is the primary cap; this is per-host, so multiple webhook URLs
	// multiply it, but it still protects a single endpoint from a connection
	// storm and keeps idle connections from lingering.
	transport := &http.Transport{
		MaxConnsPerHost:     maxConcurrentDeliveries,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	if blockPrivate {
		// Enforce the block at dial time on the resolved IP. Checking here (rather
		// than parsing the configured URL) closes the DNS-rebinding / TOCTOU gap:
		// the address passed to Control is the IP net/http is about to connect to.
		dialer := &net.Dialer{Control: dialControlBlockPrivate}
		transport.DialContext = dialer.DialContext
	}
	client := &http.Client{
		Timeout:   deliveryTimeout,
		Transport: transport,
		// Refuse HTTP redirects. The default client follows up to 10 redirects,
		// so a webhook endpoint that answers with a 3xx to an internal address
		// (169.254.169.254 metadata, loopback, an RFC1918 service) would turn
		// delivery into an SSRF vector. Returning ErrUseLastResponse stops the
		// follow and hands the 3xx back to the sink, which reports it as a
		// non-2xx delivery failure (best-effort, logged, not retried).
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	sink := NewWebhookSink(client)
	resolver := NewResolver(arnToURL)
	return NewDispatcher(sink, resolver, region, deliveryTimeout)
}

// dialControlBlockPrivate is a net.Dialer.Control hook that refuses connections
// to non-public addresses. It runs after DNS resolution with the concrete IP
// net/http is about to dial, so a hostname that resolves to a private/loopback
// address is blocked just like a literal one.
func dialControlBlockPrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("notification: parse dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("notification: unresolved dial host %q", host)
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() || ip.IsUnspecified() {
		return fmt.Errorf("notification: blocked webhook delivery to non-public address %s (block_private_targets)", ip)
	}
	return nil
}
