package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/notification"
	"github.com/kumasuke/jog/internal/storage"
)

// captureWebhook is a fake HTTP endpoint counting the deliveries it receives.
type captureWebhook struct {
	url string
	n   atomic.Int64
}

func newCaptureWebhook(t *testing.T) *captureWebhook {
	t.Helper()
	c := &captureWebhook{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.n.Add(1)
	}))
	t.Cleanup(srv.Close)
	c.url = srv.URL
	return c
}

func (c *captureWebhook) count() int { return int(c.n.Load()) }

// fakeNotifier records every Dispatch the engine makes, so tests can assert
// which lifecycle actions emit which events.
type fakeNotifier struct {
	events []notification.DispatchInput
}

func (f *fakeNotifier) Dispatch(in notification.DispatchInput) {
	f.events = append(f.events, in)
}

// subscribeAllLifecycle gives the bucket a notification config subscribing to
// s3:LifecycleExpiration:* on one topic (so emit's per-bucket config load is
// non-nil; the dispatcher's own match/resolve logic is unit-tested separately).
func subscribeAllLifecycle(t *testing.T, st storage.Storage, bucket string) {
	t.Helper()
	cfg := &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "lc", TopicArn: "arn:topic:lc", Events: []string{"s3:LifecycleExpiration:*"}},
		},
	}
	if err := st.PutBucketNotification(context.Background(), bucket, cfg); err != nil {
		t.Fatalf("PutBucketNotification: %v", err)
	}
}

func TestEngine_Notification_NoncurrentDeleteEmitsDelete(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	fn := &fakeNotifier{}
	eng.SetNotifier(fn)
	enabledBucket(t, st, "b")
	subscribeAllLifecycle(t, st, "b")
	v1 := putV(t, st, "b", "k", "1") // oldest noncurrent → expired
	putV(t, st, "b", "k", "2")       // noncurrent (protected by keep=1)
	putV(t, st, "b", "k", "3")       // current
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "ncve", Status: "Enabled",
		NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{
			NoncurrentDays: i32(1), NewerNoncurrentVersions: i32(1),
		},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(fn.events) != 1 {
		t.Fatalf("got %d events, want 1", len(fn.events))
	}
	ev := fn.events[0]
	if ev.EventName != notification.EventLifecycleExpirationDelete {
		t.Errorf("eventName = %q, want Delete", ev.EventName)
	}
	if ev.Key != "k" || ev.VersionID != v1 {
		t.Errorf("event key/version = %q/%q, want k/%s", ev.Key, ev.VersionID, v1)
	}
	if ev.Bucket != "b" {
		t.Errorf("bucket = %q", ev.Bucket)
	}
}

func TestEngine_Notification_CurrentExpiryEmitsDeleteMarkerCreated(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	fn := &fakeNotifier{}
	eng.SetNotifier(fn)
	enabledBucket(t, st, "b")
	subscribeAllLifecycle(t, st, "b")
	putV(t, st, "b", "k", "data")
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "exp", Status: "Enabled",
		Expiration: &storage.LifecycleExpiration{Days: i32(1)},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(fn.events) != 1 {
		t.Fatalf("got %d events, want 1", len(fn.events))
	}
	ev := fn.events[0]
	if ev.EventName != notification.EventLifecycleExpirationDeleteMarkerCreated {
		t.Errorf("eventName = %q, want DeleteMarkerCreated", ev.EventName)
	}
	if ev.Key != "k" || ev.VersionID == "" {
		t.Errorf("event key/version = %q/%q, want k/<markerID>", ev.Key, ev.VersionID)
	}
}

func TestEngine_Notification_UnversionedDeleteEmitsDeleteNoVersionID(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	fn := &fakeNotifier{}
	eng.SetNotifier(fn)
	// Unversioned bucket.
	if err := st.CreateBucket(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	subscribeAllLifecycle(t, st, "b")
	if _, err := st.PutObject(ctx, "b", "k", bytes.NewReader([]byte("data")), 4, "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "exp", Status: "Enabled",
		Expiration: &storage.LifecycleExpiration{Days: i32(1)},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(fn.events) != 1 {
		t.Fatalf("got %d events, want 1", len(fn.events))
	}
	ev := fn.events[0]
	if ev.EventName != notification.EventLifecycleExpirationDelete {
		t.Errorf("eventName = %q, want Delete", ev.EventName)
	}
	if ev.VersionID != "" {
		t.Errorf("unversioned delete should have empty versionId, got %q", ev.VersionID)
	}
}

func TestEngine_Notification_DryRunEmitsNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, true, now) // dry-run
	fn := &fakeNotifier{}
	eng.SetNotifier(fn)
	enabledBucket(t, st, "b")
	subscribeAllLifecycle(t, st, "b")
	putV(t, st, "b", "k", "1")
	putV(t, st, "b", "k", "2")
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "ncve", Status: "Enabled",
		NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{NoncurrentDays: i32(1)},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(fn.events) != 0 {
		t.Fatalf("dry-run must emit no events, got %d", len(fn.events))
	}
}

// TestEngine_Notification_NoSubscriptionDeliversNothing wires a REAL dispatcher
// (with a capturing webhook) and a bucket whose notification config does not
// subscribe to lifecycle events. The engine still calls Dispatch on the delete,
// but the dispatcher's match step drops it, so nothing is delivered. This is the
// gating contract: a deletion does not produce a webhook unless the bucket
// subscribes. (The "which action emits which event" assertions use the
// fakeNotifier above; this one exercises the end-to-end delivery decision.)
func TestEngine_Notification_NoSubscriptionDeliversNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)

	rcv := newCaptureWebhook(t)
	disp := notification.NewDispatcher(
		notification.NewWebhookSink(nil),
		notification.NewResolver(map[string]string{"arn:topic:other": rcv.url}),
		"us-east-1", time.Second,
	)
	eng.SetNotifier(disp)

	enabledBucket(t, st, "b")
	// Config subscribes to ObjectCreated, NOT lifecycle expiration.
	if err := st.PutBucketNotification(ctx, "b", &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "created", TopicArn: "arn:topic:other", Events: []string{"s3:ObjectCreated:*"}},
		},
	}); err != nil {
		t.Fatalf("PutBucketNotification: %v", err)
	}
	putV(t, st, "b", "k", "1")
	putV(t, st, "b", "k", "2")
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "ncve", Status: "Enabled",
		NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{NoncurrentDays: i32(1)},
	})

	r, err := eng.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if r.Buckets["b"].Actions == 0 {
		t.Fatal("expected a deletion to happen")
	}
	disp.Wait()
	if n := rcv.count(); n != 0 {
		t.Fatalf("bucket not subscribed to lifecycle events must receive nothing, got %d", n)
	}
}

// TestEngine_Notification_SubscribedDeliversWebhook is the positive end-to-end:
// a subscribed bucket's lifecycle delete reaches the webhook.
func TestEngine_Notification_SubscribedDeliversWebhook(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)

	rcv := newCaptureWebhook(t)
	disp := notification.NewDispatcher(
		notification.NewWebhookSink(nil),
		notification.NewResolver(map[string]string{"arn:topic:lc": rcv.url}),
		"us-east-1", time.Second,
	)
	eng.SetNotifier(disp)

	enabledBucket(t, st, "b")
	subscribeAllLifecycle(t, st, "b") // arn:topic:lc → s3:LifecycleExpiration:*
	putV(t, st, "b", "k", "1")
	putV(t, st, "b", "k", "2")
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "ncve", Status: "Enabled",
		NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{NoncurrentDays: i32(1)},
	})

	if _, err := eng.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	disp.Wait()
	if n := rcv.count(); n != 1 {
		t.Fatalf("subscribed bucket should receive 1 webhook, got %d", n)
	}
}

// notifConfigErrStore wraps a real Storage but fails GetBucketNotification, to
// exercise the engine's best-effort branch: a config read error disables events
// for the cycle yet must NOT block the deletions themselves.
type notifConfigErrStore struct {
	storage.Storage
}

func (notifConfigErrStore) GetBucketNotification(context.Context, string) (*storage.NotificationConfiguration, error) {
	return nil, errors.New("boom: notification config unreadable")
}

func TestEngine_Notification_ConfigReadErrorDisablesEventsButDeletes(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(100 * 24 * time.Hour)

	dir := t.TempDir()
	real, err := storage.NewFileSystem(dir, dir+"/metadata.db")
	if err != nil {
		t.Fatalf("NewFileSystem: %v", err)
	}
	t.Cleanup(func() { _ = real.Close() })
	st := notifConfigErrStore{real}

	eng := NewEngine(st, Config{ThrottleEvery: 1 << 30, GCGracePeriod: time.Hour}, func() time.Time { return now })
	fn := &fakeNotifier{}
	eng.SetNotifier(fn)

	enabledBucket(t, st, "b")
	subscribeAllLifecycle(t, st, "b") // stored fine; only the READ back fails
	putV(t, st, "b", "k", "1")        // oldest noncurrent → expired
	putV(t, st, "b", "k", "2")
	putV(t, st, "b", "k", "3")
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "ncve", Status: "Enabled",
		NoncurrentVersionExpiration: &storage.NoncurrentVersionExpiration{
			NoncurrentDays: i32(1), NewerNoncurrentVersions: i32(1),
		},
	})

	r, err := eng.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if r.Buckets["b"].Actions == 0 {
		t.Fatal("a config read error must not block deletions; expected at least one action")
	}
	if len(fn.events) != 0 {
		t.Fatalf("events must be disabled when the notification config read fails, got %d", len(fn.events))
	}
}

func TestEngine_Notification_AbortMPUEmitsNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Add(5 * 24 * time.Hour)
	st, eng := newEngineFixture(t, false, now)
	fn := &fakeNotifier{}
	eng.SetNotifier(fn)
	if err := st.CreateBucket(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	subscribeAllLifecycle(t, st, "b")
	if _, err := st.CreateMultipartUpload(ctx, "b", "k", "text/plain", nil, "", nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	setLifecycle(t, st, "b", storage.LifecycleRule{
		ID: "aimu", Status: "Enabled",
		AbortIncompleteMultipartUpload: &storage.AbortIncompleteMultipartUpload{DaysAfterInitiation: i32(1)},
	})

	r, err := eng.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if r.Buckets["b"].AbortedUploads != 1 {
		t.Fatalf("expected the upload to be aborted, got %d", r.Buckets["b"].AbortedUploads)
	}
	// S3 has no notification event type for AbortIncompleteMultipartUpload.
	if len(fn.events) != 0 {
		t.Fatalf("AIMU must emit no notification, got %d", len(fn.events))
	}
}
