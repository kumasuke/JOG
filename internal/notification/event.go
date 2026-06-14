// Package notification builds and delivers S3-compatible event notification
// messages. The current sink is webhook (HTTP POST); SNS/SQS/Lambda/EventBridge
// are intentionally out of scope (they would require escalating aws-sdk-go-v2 to
// a main dependency — see TODO.md / Issue #56). The package is structured so
// those sinks can be slotted in later behind the same Sink interface.
//
// Delivery is best-effort and asynchronous: callers (notably the single-goroutine
// lifecycle engine) hand an event to the Dispatcher and never block on the HTTP
// round-trip. A failed delivery is logged, not retried in v1.
package notification

import "time"

// Event mirrors one element of the S3 event notification "Records" array.
// JSON field names and casing match the AWS S3 event message structure
// (notification-content-structure). Lifecycle events use eventVersion 2.3.
type Event struct {
	EventVersion      string            `json:"eventVersion"`
	EventSource       string            `json:"eventSource"`
	AWSRegion         string            `json:"awsRegion"`
	EventTime         string            `json:"eventTime"`
	EventName         string            `json:"eventName"`
	UserIdentity      UserIdentity      `json:"userIdentity"`
	RequestParameters RequestParameters `json:"requestParameters"`
	ResponseElements  ResponseElements  `json:"responseElements"`
	S3                S3Entity          `json:"s3"`
}

// UserIdentity carries the principal that caused the event. For lifecycle the
// principal is the storage service itself.
type UserIdentity struct {
	PrincipalID string `json:"principalId"`
}

// RequestParameters carries request metadata. Lifecycle actions are internal, so
// the source address is the local host marker S3 uses for service events.
type RequestParameters struct {
	SourceIPAddress string `json:"sourceIPAddress"`
}

// ResponseElements echoes the request/host trace IDs.
type ResponseElements struct {
	XAmzRequestID string `json:"x-amz-request-id"`
	XAmzID2       string `json:"x-amz-id-2"`
}

// S3Entity describes the bucket and object the event concerns.
type S3Entity struct {
	S3SchemaVersion string         `json:"s3SchemaVersion"`
	ConfigurationID string         `json:"configurationId"`
	Bucket          S3BucketEntity `json:"bucket"`
	Object          S3ObjectEntity `json:"object"`
}

// S3BucketEntity describes the bucket.
type S3BucketEntity struct {
	Name          string       `json:"name"`
	OwnerIdentity UserIdentity `json:"ownerIdentity"`
	ARN           string       `json:"arn"`
}

// S3ObjectEntity describes the object. VersionID is omitted when empty
// (unversioned bucket), matching S3's "null or not present" contract. Sequencer
// is a hex string used to order events for the same key.
type S3ObjectEntity struct {
	Key       string `json:"key"`
	Size      int64  `json:"size"`
	ETag      string `json:"eTag,omitempty"`
	VersionID string `json:"versionId,omitempty"`
	Sequencer string `json:"sequencer,omitempty"`
}

// Envelope is the top-level message S3 delivers: a Records array. Each delivery
// in v1 carries exactly one event.
type Envelope struct {
	Records []Event `json:"Records"`
}

// Lifecycle event names. The on-the-wire eventName omits the "s3:" prefix
// (notification-content-structure), but the bucket notification configuration
// subscribes using the "s3:"-prefixed forms (notification-how-to-event-types).
// Only :Delete and :DeleteMarkerCreated exist for LifecycleExpiration — there is
// no S3 event type for AbortIncompleteMultipartUpload or expired-delete-marker
// cleanup, so those engine actions emit nothing.
const (
	// EventLifecycleExpirationDelete fires when lifecycle permanently deletes an
	// object (unversioned bucket) or an object version.
	EventLifecycleExpirationDelete = "LifecycleExpiration:Delete"
	// EventLifecycleExpirationDeleteMarkerCreated fires when lifecycle expires a
	// current version in a versioned bucket by creating a delete marker.
	EventLifecycleExpirationDeleteMarkerCreated = "LifecycleExpiration:DeleteMarkerCreated"

	eventSourceS3            = "aws:s3"
	eventVersionLifecycle    = "2.3"
	s3SchemaVersion          = "1.0"
	lifecyclePrincipalID     = "s3.amazonaws.com"
	lifecycleSourceIPAddress = "s3.amazonaws.com"
)

// NewLifecycleEvent assembles an Event for a lifecycle expiration action. The
// caller passes the on-the-wire eventName (without the "s3:" prefix), the bucket
// and object identity, and a clock-derived timestamp. configurationID is the ID
// of the matched notification configuration (empty when unnamed).
func NewLifecycleEvent(eventName, region, bucket, bucketOwner, key, etag, versionID string, size int64, eventTime time.Time, configurationID string) Event {
	return Event{
		EventVersion:      eventVersionLifecycle,
		EventSource:       eventSourceS3,
		AWSRegion:         region,
		EventTime:         eventTime.UTC().Format("2006-01-02T15:04:05.000Z"),
		EventName:         eventName,
		UserIdentity:      UserIdentity{PrincipalID: lifecyclePrincipalID},
		RequestParameters: RequestParameters{SourceIPAddress: lifecycleSourceIPAddress},
		ResponseElements:  ResponseElements{},
		S3: S3Entity{
			S3SchemaVersion: s3SchemaVersion,
			ConfigurationID: configurationID,
			Bucket: S3BucketEntity{
				Name:          bucket,
				OwnerIdentity: UserIdentity{PrincipalID: bucketOwner},
				ARN:           "arn:aws:s3:::" + bucket,
			},
			Object: S3ObjectEntity{
				Key:       key,
				Size:      size,
				ETag:      etag,
				VersionID: versionID,
			},
		},
	}
}
