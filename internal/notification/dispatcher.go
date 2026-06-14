package notification

import (
	"context"
	"sync"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Dispatcher decides whether an event should be delivered (per the bucket's
// stored notification configuration) and, if so, delivers it best-effort and
// asynchronously. It never blocks the caller on the network round-trip — this is
// what lets the single-goroutine lifecycle engine emit events without stalling.
//
// A nil *Dispatcher is a valid no-op: Dispatch returns immediately. This keeps
// the engine wiring simple when notifications are not configured.
type Dispatcher struct {
	sink     Sink
	resolver *Resolver
	region   string

	// deliverTimeout caps a single delivery's context so a slow endpoint cannot
	// pin a goroutine forever.
	deliverTimeout time.Duration

	log zerolog.Logger
	wg  sync.WaitGroup
}

// NewDispatcher builds a dispatcher. region is stamped into every event's
// awsRegion. A zero deliverTimeout defaults to 10s.
func NewDispatcher(sink Sink, resolver *Resolver, region string, deliverTimeout time.Duration) *Dispatcher {
	if deliverTimeout <= 0 {
		deliverTimeout = 10 * time.Second
	}
	return &Dispatcher{
		sink:           sink,
		resolver:       resolver,
		region:         region,
		deliverTimeout: deliverTimeout,
		log:            log.With().Str("component", "notification").Logger(),
	}
}

// Region returns the configured AWS region marker (used by callers that build
// events). Safe on a nil dispatcher.
func (d *Dispatcher) Region() string {
	if d == nil {
		return ""
	}
	return d.region
}

// DispatchInput carries everything needed to decide and deliver one event.
type DispatchInput struct {
	// Config is the bucket's stored notification configuration. A nil config
	// means the bucket subscribes to nothing, so nothing is delivered.
	Config *storage.NotificationConfiguration
	// EventName is the on-the-wire eventName (no "s3:" prefix), used both for
	// subscription matching and stamped into the message.
	EventName string
	Bucket    string
	// BucketOwner is the principal ID stamped into s3.bucket.ownerIdentity.
	BucketOwner string
	Key         string
	ETag        string
	VersionID   string
	Size        int64
	// EventTime is the action's completion time (engine clock).
	EventTime time.Time
}

// Dispatch matches the event against the bucket configuration and, for each
// matched target that resolves to a webhook URL, delivers the message in a
// background goroutine. It returns immediately. A nil dispatcher is a no-op.
func (d *Dispatcher) Dispatch(in DispatchInput) {
	if d == nil || in.Config == nil {
		return
	}
	targets := matchTargets(in.Config, in.EventName, in.Key)
	if len(targets) == 0 {
		return
	}
	for _, t := range targets {
		url, ok := d.resolver.Resolve(t.arn)
		if !ok {
			d.log.Warn().
				Str("bucket", in.Bucket).Str("arn", t.arn).Str("event", in.EventName).
				Msg("notification target ARN has no webhook URL mapping; event dropped")
			continue
		}
		event := NewLifecycleEvent(
			in.EventName, d.region, in.Bucket, in.BucketOwner,
			in.Key, in.ETag, in.VersionID, in.Size, in.EventTime, t.configurationID,
		)
		env := Envelope{Records: []Event{event}}
		d.deliver(url, env, in.Bucket, in.Key)
	}
}

func (d *Dispatcher) deliver(url string, env Envelope, bucket, key string) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), d.deliverTimeout)
		defer cancel()
		if err := d.sink.Deliver(ctx, url, env); err != nil {
			d.log.Warn().Err(err).
				Str("bucket", bucket).Str("key", key).Str("url", url).
				Msg("notification delivery failed (best-effort, not retried)")
		}
	}()
}

// Wait blocks until all in-flight deliveries complete. Intended for graceful
// shutdown and tests; not required on the hot path. Safe on a nil dispatcher.
func (d *Dispatcher) Wait() {
	if d == nil {
		return
	}
	d.wg.Wait()
}
