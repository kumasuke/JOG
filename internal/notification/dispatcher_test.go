package notification

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/storage"
)

// captureReceiver is a fake webhook endpoint that records the envelopes it gets.
type captureReceiver struct {
	mu   sync.Mutex
	got  []Envelope
	srv  *httptest.Server
	code int // status to return (0 → 200)
}

func newCaptureReceiver(t *testing.T) *captureReceiver {
	t.Helper()
	r := &captureReceiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var env Envelope
		_ = json.Unmarshal(body, &env)
		r.mu.Lock()
		r.got = append(r.got, env)
		r.mu.Unlock()
		if r.code != 0 {
			w.WriteHeader(r.code)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *captureReceiver) envelopes() []Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Envelope(nil), r.got...)
}

func newTestDispatcher(t *testing.T, arn, url string) *Dispatcher {
	t.Helper()
	resolver := NewResolver(map[string]string{arn: url})
	sink := NewWebhookSink(&http.Client{Timeout: 2 * time.Second})
	return NewDispatcher(sink, resolver, "us-east-1", 2*time.Second)
}

func TestDispatch_DeliversMatchingEvent(t *testing.T) {
	rcv := newCaptureReceiver(t)
	d := newTestDispatcher(t, "arn:topic:1", rcv.srv.URL)

	cfg := &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "cfg-1", TopicArn: "arn:topic:1", Events: []string{"s3:LifecycleExpiration:*"}},
		},
	}
	d.Dispatch(DispatchInput{
		Config:    cfg,
		EventName: EventLifecycleExpirationDelete,
		Bucket:    "b", BucketOwner: "owner-1",
		Key: "logs/a", VersionID: "v1", Size: 42,
		EventTime: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	})
	d.Wait()

	envs := rcv.envelopes()
	if len(envs) != 1 || len(envs[0].Records) != 1 {
		t.Fatalf("got %d envelopes, want 1 with 1 record", len(envs))
	}
	rec := envs[0].Records[0]
	if rec.EventName != "LifecycleExpiration:Delete" {
		t.Errorf("eventName = %q", rec.EventName)
	}
	if rec.EventVersion != "2.3" {
		t.Errorf("eventVersion = %q, want 2.3", rec.EventVersion)
	}
	if rec.S3.Bucket.Name != "b" || rec.S3.Object.Key != "logs/a" || rec.S3.Object.VersionID != "v1" {
		t.Errorf("s3 entity = %+v", rec.S3)
	}
	if rec.S3.Object.Size != 42 {
		t.Errorf("size = %d, want 42", rec.S3.Object.Size)
	}
	if rec.S3.ConfigurationID != "cfg-1" {
		t.Errorf("configurationId = %q, want cfg-1", rec.S3.ConfigurationID)
	}
	if rec.AWSRegion != "us-east-1" {
		t.Errorf("awsRegion = %q", rec.AWSRegion)
	}
	if rec.S3.Bucket.ARN != "arn:aws:s3:::b" {
		t.Errorf("bucket arn = %q", rec.S3.Bucket.ARN)
	}
}

func TestDispatch_NoMatchNoDelivery(t *testing.T) {
	rcv := newCaptureReceiver(t)
	d := newTestDispatcher(t, "arn:topic:1", rcv.srv.URL)

	cfg := &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			// Subscribes to a different event family.
			{ID: "cfg-1", TopicArn: "arn:topic:1", Events: []string{"s3:ObjectCreated:*"}},
		},
	}
	d.Dispatch(DispatchInput{
		Config:    cfg,
		EventName: EventLifecycleExpirationDelete,
		Bucket:    "b", Key: "k",
		EventTime: time.Now(),
	})
	d.Wait()

	if envs := rcv.envelopes(); len(envs) != 0 {
		t.Fatalf("got %d envelopes, want 0 (no subscription match)", len(envs))
	}
}

func TestDispatch_KeyFilterFiltersOut(t *testing.T) {
	rcv := newCaptureReceiver(t)
	d := newTestDispatcher(t, "arn:topic:1", rcv.srv.URL)

	cfg := &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "cfg-1", TopicArn: "arn:topic:1", Events: []string{"s3:LifecycleExpiration:*"},
				Filter: &storage.NotificationFilter{Key: &storage.S3KeyFilter{
					FilterRules: []storage.FilterRule{{Name: "prefix", Value: "logs/"}}}}},
		},
	}
	d.Dispatch(DispatchInput{
		Config:    cfg,
		EventName: EventLifecycleExpirationDelete,
		Bucket:    "b", Key: "data/a", // prefix miss
		EventTime: time.Now(),
	})
	d.Wait()

	if envs := rcv.envelopes(); len(envs) != 0 {
		t.Fatalf("got %d envelopes, want 0 (key filter excludes)", len(envs))
	}
}

func TestDispatch_UnmappedARNDropped(t *testing.T) {
	rcv := newCaptureReceiver(t)
	// Resolver knows arn:topic:1 only; config references arn:topic:2.
	d := newTestDispatcher(t, "arn:topic:1", rcv.srv.URL)

	cfg := &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "cfg-1", TopicArn: "arn:topic:2", Events: []string{"s3:LifecycleExpiration:*"}},
		},
	}
	d.Dispatch(DispatchInput{
		Config:    cfg,
		EventName: EventLifecycleExpirationDelete,
		Bucket:    "b", Key: "k",
		EventTime: time.Now(),
	})
	d.Wait()

	if envs := rcv.envelopes(); len(envs) != 0 {
		t.Fatalf("got %d envelopes, want 0 (ARN has no URL mapping)", len(envs))
	}
}

func TestDispatch_NilDispatcherAndNilConfigAreNoops(t *testing.T) {
	var d *Dispatcher
	// Must not panic.
	d.Dispatch(DispatchInput{EventName: EventLifecycleExpirationDelete, Key: "k"})
	d.Wait()

	rcv := newCaptureReceiver(t)
	d2 := newTestDispatcher(t, "arn:topic:1", rcv.srv.URL)
	d2.Dispatch(DispatchInput{Config: nil, EventName: EventLifecycleExpirationDelete, Key: "k"})
	d2.Wait()
	if envs := rcv.envelopes(); len(envs) != 0 {
		t.Fatalf("nil config should deliver nothing, got %d", len(envs))
	}
}

func TestDispatch_VersionIDOmittedWhenEmpty(t *testing.T) {
	rcv := newCaptureReceiver(t)
	d := newTestDispatcher(t, "arn:topic:1", rcv.srv.URL)
	cfg := &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "cfg-1", TopicArn: "arn:topic:1", Events: []string{"s3:LifecycleExpiration:*"}},
		},
	}
	d.Dispatch(DispatchInput{
		Config:    cfg,
		EventName: EventLifecycleExpirationDelete,
		Bucket:    "b", Key: "k", // unversioned: no VersionID
		EventTime: time.Now(),
	})
	d.Wait()

	envs := rcv.envelopes()
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1", len(envs))
	}
	// Re-marshal and confirm versionId key is absent (omitempty).
	raw, _ := json.Marshal(envs[0].Records[0].S3.Object)
	if got := string(raw); contains(got, "versionId") {
		t.Errorf("versionId should be omitted for unversioned object, got %s", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
