package notification

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/storage"
)

// TestFromTargets_DoesNotFollowRedirects pins the SSRF hardening on the webhook
// delivery client. A webhook endpoint that answers with a 3xx redirect to
// another host (the cloud metadata endpoint at 169.254.169.254, a loopback
// admin service, an RFC1918 address) must NOT be followed: the default
// net/http client follows up to 10 redirects, which would turn an
// attacker-controlled external receiver into an SSRF pivot into internal
// infrastructure. The delivery client must follow none.
func TestFromTargets_DoesNotFollowRedirects(t *testing.T) {
	var internalHit atomic.Bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		internalHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()

	// An external webhook target that 302-redirects delivery at the internal
	// server — the classic SSRF-via-redirect bypass.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	const arn = "arn:aws:sns:us-east-1:000000000000:hook"
	d := FromTargets(map[string]string{arn: redirector.URL}, "us-east-1", 2*time.Second, false)
	if d == nil {
		t.Fatal("FromTargets returned nil for a non-empty target map")
	}

	dispatchOne(d, arn, "k")
	d.Wait()

	if internalHit.Load() {
		t.Fatal("webhook delivery followed a redirect to an internal host (SSRF); redirects must be refused")
	}
}

// TestFromTargets_BlockPrivateTargets_RefusesLoopback pins the opt-in SSRF
// guard: with blockPrivate=true, delivery to a loopback/private address is
// refused at dial time. httptest servers listen on 127.0.0.1, so a successful
// block means the receiver records nothing.
func TestFromTargets_BlockPrivateTargets_RefusesLoopback(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const arn = "arn:aws:sns:us-east-1:000000000000:hook"
	d := FromTargets(map[string]string{arn: srv.URL}, "us-east-1", 2*time.Second, true)
	if d == nil {
		t.Fatal("FromTargets returned nil for a non-empty target map")
	}

	dispatchOne(d, arn, "k")
	d.Wait()

	if hit.Load() {
		t.Fatal("delivery to a loopback address was not blocked despite block_private_targets=true")
	}
}

// TestFromTargets_BlockPrivateTargets_Disabled_AllowsLoopback is the default:
// without the opt-in, an internal (loopback) target is delivered normally, so
// the common "webhook to a same-host/cluster service" deployment keeps working.
func TestFromTargets_BlockPrivateTargets_Disabled_AllowsLoopback(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const arn = "arn:aws:sns:us-east-1:000000000000:hook"
	d := FromTargets(map[string]string{arn: srv.URL}, "us-east-1", 2*time.Second, false)
	dispatchOne(d, arn, "k")
	d.Wait()

	if !hit.Load() {
		t.Fatal("loopback delivery should succeed when block_private_targets is off")
	}
}

// dispatchOne fires a single lifecycle-expiration event subscribed to arn.
func dispatchOne(d *Dispatcher, arn, key string) {
	cfg := &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "cfg-1", TopicArn: arn, Events: []string{"s3:LifecycleExpiration:*"}},
		},
	}
	d.Dispatch(DispatchInput{
		Config:    cfg,
		EventName: EventLifecycleExpirationDelete,
		Bucket:    "b", Key: key,
		EventTime: time.Now(),
	})
}
