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

	// sem bounds the number of in-flight delivery goroutines. It is a buffered
	// channel of capacity maxConcurrentDeliveries: deliver() does a non-blocking
	// try-acquire and drops the event when the channel is full. This caps both
	// goroutines and (transitively) open sockets without ever blocking the
	// single-goroutine lifecycle engine — preserving the "never blocks the
	// caller" invariant for best-effort delivery.
	sem chan struct{}

	log zerolog.Logger
	wg  sync.WaitGroup
}

// maxConcurrentDeliveries caps in-flight webhook deliveries. With
// max_actions_per_cycle defaulting to 10000, an unbounded fan-out could spawn
// up to 10k concurrent POSTs per cycle and exhaust fds/ports or DoS the
// receiver. This bound keeps the fan-out predictable; excess events are dropped
// and logged (best-effort, not retried in v1). Not configurable in v1 to keep
// the wiring small; promote to config if a deployment needs to tune it.
const maxConcurrentDeliveries = 64

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
		sem:            make(chan struct{}, maxConcurrentDeliveries),
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
	// Non-blocking try-acquire: if the concurrency limit is reached, drop the
	// event rather than block the lifecycle engine. This matches the best-effort
	// contract — a backlogged or slow set of endpoints must not stall deletes.
	select {
	case d.sem <- struct{}{}:
	default:
		d.log.Warn().
			Str("bucket", bucket).Str("key", key).Str("url", url).
			Int("limit", cap(d.sem)).
			Msg("notification dropped: delivery concurrency limit reached (best-effort, not retried)")
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() { <-d.sem }()
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
