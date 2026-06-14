package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/config"
	"github.com/kumasuke/jog/internal/notification"
	"github.com/kumasuke/jog/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subscribedConfig builds a notification config subscribing arn to all
// lifecycle-expiration events, so a dispatched expiration matches.
func subscribedConfig(arn string) *storage.NotificationConfiguration {
	return &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "lc", TopicArn: arn, Events: []string{"s3:LifecycleExpiration:*"}},
		},
	}
}

// testConfig returns a Config pointed at an isolated temp data dir so New can
// open real storage without touching the repository.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.MetadataDB = dir + "/metadata.db"
	return cfg
}

// TestNew_NoTargets_NotifierDisabled: with no notification targets, the server
// leaves the notifier unset so Dispatch/Wait are no-ops.
func TestNew_NoTargets_NotifierDisabled(t *testing.T) {
	s, err := New(testConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.storage.Close() })

	assert.Nil(t, s.notifier, "notifier must be nil when no webhook targets are configured")
}

// TestNew_WithTargets_NotifierEnabled: configuring a target wires a dispatcher
// onto the server (and, transitively, the lifecycle engine).
func TestNew_WithTargets_NotifierEnabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Notification.Targets = map[string]string{
		"arn:aws:sns:us-east-1:000000000000:hook": "https://example.test/webhook",
	}

	s, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.storage.Close() })

	assert.NotNil(t, s.notifier, "notifier must be set when a webhook target is configured")
}

// TestShutdown_WaitsForInFlightNotifications pins the graceful-shutdown drain:
// Shutdown must block on the notifier until in-flight async deliveries finish,
// so events are not dropped on exit. We install a dispatcher whose webhook hangs
// until released and assert Shutdown does not return early.
func TestShutdown_WaitsForInFlightNotifications(t *testing.T) {
	s, err := New(testConfig(t))
	require.NoError(t, err)

	release := make(chan struct{})
	var delivered atomic.Bool
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // hold the delivery goroutine until the test releases it
		delivered.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()

	const arn = "arn:aws:sns:us-east-1:000000000000:hook"
	disp := notification.FromTargets(map[string]string{arn: webhook.URL}, "us-east-1", 10*time.Second, false)
	require.NotNil(t, disp)
	s.notifier = disp

	// Kick off one matching delivery; it parks in the hanging handler.
	disp.Dispatch(notification.DispatchInput{
		Config:    subscribedConfig(arn),
		EventName: notification.EventLifecycleExpirationDelete,
		Bucket:    "b", Key: "k",
		EventTime: time.Now(),
	})

	done := make(chan error, 1)
	go func() { done <- s.Shutdown() }()

	// Shutdown must still be blocked on the in-flight delivery.
	select {
	case <-done:
		t.Fatal("Shutdown returned before the in-flight notification finished")
	case <-time.After(150 * time.Millisecond):
	}

	close(release) // let the delivery complete

	select {
	case err := <-done:
		require.NoError(t, err)
		assert.True(t, delivered.Load(), "delivery should have completed before Shutdown returned")
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return after the delivery finished")
	}
}
