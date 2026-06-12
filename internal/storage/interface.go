// Package storage provides storage backend abstraction for JOG server.
package storage

import (
	"context"
	"io"
	"time"
)

// Bucket represents a storage bucket.
type Bucket struct {
	Name         string
	CreationDate time.Time
}

// Object represents a stored object.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	ContentType  string
	Metadata     map[string]string
}

// ObjectData represents object data for reading.
type ObjectData struct {
	Object
	Body io.ReadCloser
}

// ListObjectsInput holds parameters for listing objects.
type ListObjectsInput struct {
	Bucket            string
	Prefix            string
	Delimiter         string
	MaxKeys           int32
	ContinuationToken string
	StartAfter        string
}

// ListObjectsOutput holds the result of listing objects.
type ListObjectsOutput struct {
	Objects               []Object
	CommonPrefixes        []string
	IsTruncated           bool
	NextContinuationToken string
	KeyCount              int32
}

// MultipartUpload represents a multipart upload in progress.
type MultipartUpload struct {
	UploadID    string
	Bucket      string
	Key         string
	ContentType string
	Metadata    map[string]string
	Initiated   time.Time

	// Object Lock intent captured from the x-amz-object-lock-* headers at
	// CreateMultipartUpload (issue #40). These are applied to the version
	// finalized at CompleteMultipartUpload, not to the in-progress upload.
	// Empty / nil fields mean "no lock from headers".
	ObjectLockMode            ObjectLockRetentionMode
	ObjectLockRetainUntilDate *time.Time // explicit x-amz-object-lock-retain-until-date (absolute)
	ObjectLockDefaultDays     *int32     // bucket DefaultRetention: materialized at complete
	ObjectLockDefaultYears    *int32     // bucket DefaultRetention: materialized at complete
	ObjectLockLegalHold       ObjectLegalHoldStatus
}

// Part represents an uploaded part.
type Part struct {
	PartNumber   int32
	Size         int64
	ETag         string
	LastModified time.Time
}

// ListPartsInput holds parameters for listing parts.
type ListPartsInput struct {
	Bucket           string
	Key              string
	UploadID         string
	MaxParts         int32
	PartNumberMarker int32
}

// ListPartsOutput holds the result of listing parts.
type ListPartsOutput struct {
	Parts                []Part
	IsTruncated          bool
	NextPartNumberMarker int32
}

// ListMultipartUploadsInput holds parameters for listing multipart uploads.
type ListMultipartUploadsInput struct {
	Bucket         string
	Prefix         string
	MaxUploads     int32
	KeyMarker      string
	UploadIdMarker string
}

// ListMultipartUploadsOutput holds the result of listing multipart uploads.
type ListMultipartUploadsOutput struct {
	Uploads            []MultipartUpload
	IsTruncated        bool
	NextKeyMarker      string
	NextUploadIdMarker string
}

// DeletedObject represents a successfully deleted object.
type DeletedObject struct {
	Key string
}

// DeleteError represents an error deleting an object.
type DeleteError struct {
	Key     string
	Code    string
	Message string
}

// Tag represents a key-value tag.
type Tag struct {
	Key   string
	Value string
}

// CORSRule represents a CORS rule.
type CORSRule struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	ExposeHeaders  []string
	MaxAgeSeconds  int32
}

// CORSConfiguration holds CORS rules for a bucket.
type CORSConfiguration struct {
	Rules []CORSRule
}

// VersioningStatus represents the versioning status of a bucket.
type VersioningStatus string

const (
	VersioningStatusDisabled  VersioningStatus = ""
	VersioningStatusEnabled   VersioningStatus = "Enabled"
	VersioningStatusSuspended VersioningStatus = "Suspended"
)

// ObjectVersion represents a version of an object.
type ObjectVersion struct {
	Key            string
	VersionID      string
	IsLatest       bool
	LastModified   time.Time
	ETag           string
	Size           int64
	ContentType    string
	Metadata       map[string]string
	IsDeleteMarker bool
}

// ListObjectVersionsInput holds parameters for listing object versions.
type ListObjectVersionsInput struct {
	Bucket          string
	Prefix          string
	Delimiter       string
	MaxKeys         int32
	KeyMarker       string
	VersionIdMarker string
}

// ListObjectVersionsOutput holds the result of listing object versions.
type ListObjectVersionsOutput struct {
	Versions            []ObjectVersion
	DeleteMarkers       []ObjectVersion
	CommonPrefixes      []string
	IsTruncated         bool
	NextKeyMarker       string
	NextVersionIdMarker string
}

// ACLPermission represents an ACL permission.
type ACLPermission string

const (
	ACLPermissionFullControl ACLPermission = "FULL_CONTROL"
	ACLPermissionWrite       ACLPermission = "WRITE"
	ACLPermissionWriteACP    ACLPermission = "WRITE_ACP"
	ACLPermissionRead        ACLPermission = "READ"
	ACLPermissionReadACP     ACLPermission = "READ_ACP"
)

// ACLGranteeType represents the type of grantee.
type ACLGranteeType string

const (
	ACLGranteeTypeCanonicalUser  ACLGranteeType = "CanonicalUser"
	ACLGranteeTypeAmazonCustomer ACLGranteeType = "AmazonCustomerByEmail"
	ACLGranteeTypeGroup          ACLGranteeType = "Group"
)

// ACLGrant represents a single grant in an ACL.
type ACLGrant struct {
	Permission  ACLPermission
	GranteeType ACLGranteeType
	GranteeID   string // Canonical user ID
	GranteeURI  string // Group URI (e.g., http://acs.amazonaws.com/groups/global/AllUsers)
}

// ACL represents an access control list.
type ACL struct {
	OwnerID      string
	OwnerDisplay string
	Grants       []ACLGrant
}

// CannedACL represents a predefined ACL.
type CannedACL string

const (
	CannedACLPrivate           CannedACL = "private"
	CannedACLPublicRead        CannedACL = "public-read"
	CannedACLPublicReadWrite   CannedACL = "public-read-write"
	CannedACLAuthenticatedRead CannedACL = "authenticated-read"
	CannedACLBucketOwnerRead   CannedACL = "bucket-owner-read"
	CannedACLBucketOwnerFC     CannedACL = "bucket-owner-full-control"
)

// AllUsersGroupURI is the URI for the AllUsers group.
const AllUsersGroupURI = "http://acs.amazonaws.com/groups/global/AllUsers"

// AuthenticatedUsersGroupURI is the URI for the AuthenticatedUsers group.
const AuthenticatedUsersGroupURI = "http://acs.amazonaws.com/groups/global/AuthenticatedUsers"

// SSEAlgorithm represents the server-side encryption algorithm.
type SSEAlgorithm string

const (
	SSEAlgorithmAES256  SSEAlgorithm = "AES256"
	SSEAlgorithmKMS     SSEAlgorithm = "aws:kms"
	SSEAlgorithmKMSDSSE SSEAlgorithm = "aws:kms:dsse"
)

// ServerSideEncryptionByDefault represents the default server-side encryption configuration.
type ServerSideEncryptionByDefault struct {
	SSEAlgorithm   SSEAlgorithm
	KMSMasterKeyID string
}

// ServerSideEncryptionRule represents a server-side encryption rule.
type ServerSideEncryptionRule struct {
	ApplyServerSideEncryptionByDefault *ServerSideEncryptionByDefault
	BucketKeyEnabled                   bool
}

// ServerSideEncryptionConfiguration represents the server-side encryption configuration.
type ServerSideEncryptionConfiguration struct {
	Rules []ServerSideEncryptionRule
}

// LifecycleRuleFilter represents a lifecycle rule filter.
type LifecycleRuleFilter struct {
	Prefix                string
	Tag                   *Tag
	ObjectSizeGreaterThan *int64
	ObjectSizeLessThan    *int64
}

// LifecycleExpiration represents expiration settings.
type LifecycleExpiration struct {
	Days                      *int32
	Date                      *string
	ExpiredObjectDeleteMarker *bool
}

// LifecycleTransition represents a transition to a different storage class.
type LifecycleTransition struct {
	Days         *int32
	Date         *string
	StorageClass string
}

// NoncurrentVersionExpiration represents expiration for noncurrent versions.
type NoncurrentVersionExpiration struct {
	NoncurrentDays          *int32
	NewerNoncurrentVersions *int32
}

// NoncurrentVersionTransition represents transition for noncurrent versions.
type NoncurrentVersionTransition struct {
	NoncurrentDays          *int32
	StorageClass            string
	NewerNoncurrentVersions *int32
}

// AbortIncompleteMultipartUpload represents settings for aborting incomplete multipart uploads.
type AbortIncompleteMultipartUpload struct {
	DaysAfterInitiation *int32
}

// LifecycleRule represents a lifecycle rule.
type LifecycleRule struct {
	ID                             string
	Status                         string
	Filter                         *LifecycleRuleFilter
	Expiration                     *LifecycleExpiration
	Transitions                    []LifecycleTransition
	NoncurrentVersionExpiration    *NoncurrentVersionExpiration
	NoncurrentVersionTransitions   []NoncurrentVersionTransition
	AbortIncompleteMultipartUpload *AbortIncompleteMultipartUpload
}

// LifecycleConfiguration represents a bucket lifecycle configuration.
type LifecycleConfiguration struct {
	Rules []LifecycleRule
}

// ObjectLockRetentionMode represents the retention mode for object lock.
type ObjectLockRetentionMode string

const (
	ObjectLockRetentionModeGovernance ObjectLockRetentionMode = "GOVERNANCE"
	ObjectLockRetentionModeCompliance ObjectLockRetentionMode = "COMPLIANCE"
)

// DefaultRetention represents the default retention settings for object lock.
type DefaultRetention struct {
	Mode  ObjectLockRetentionMode
	Days  *int32
	Years *int32
}

// ObjectLockRule represents the rule for object lock configuration.
type ObjectLockRule struct {
	DefaultRetention *DefaultRetention
}

// ObjectLockConfiguration represents the object lock configuration for a bucket.
type ObjectLockConfiguration struct {
	ObjectLockEnabled bool
	Rule              *ObjectLockRule
}

// ObjectRetention represents the retention settings for an object.
type ObjectRetention struct {
	Mode            ObjectLockRetentionMode
	RetainUntilDate *time.Time
}

// ObjectLegalHoldStatus represents the legal hold status.
type ObjectLegalHoldStatus string

const (
	ObjectLegalHoldStatusOn  ObjectLegalHoldStatus = "ON"
	ObjectLegalHoldStatusOff ObjectLegalHoldStatus = "OFF"
)

// ObjectLegalHold represents the legal hold settings for an object.
type ObjectLegalHold struct {
	Status ObjectLegalHoldStatus
}

// WebsiteConfiguration represents a bucket website configuration.
type WebsiteConfiguration struct {
	IndexDocument         *IndexDocument
	ErrorDocument         *ErrorDocument
	RedirectAllRequestsTo *RedirectAllRequestsTo
	RoutingRules          []RoutingRule
}

// IndexDocument represents the index document configuration.
type IndexDocument struct {
	Suffix string
}

// ErrorDocument represents the error document configuration.
type ErrorDocument struct {
	Key string
}

// RedirectAllRequestsTo represents redirect all requests configuration.
type RedirectAllRequestsTo struct {
	HostName string
	Protocol string
}

// RoutingRule represents a routing rule for website hosting.
type RoutingRule struct {
	Condition *Condition
	Redirect  *Redirect
}

// Condition represents a routing rule condition.
type Condition struct {
	KeyPrefixEquals             string
	HttpErrorCodeReturnedEquals string
}

// Redirect represents redirect configuration.
type Redirect struct {
	HostName             string
	HttpRedirectCode     string
	Protocol             string
	ReplaceKeyPrefixWith string
	ReplaceKeyWith       string
}

// NotificationConfiguration holds bucket event notification settings.
type NotificationConfiguration struct {
	TopicConfigurations          []TopicNotificationConfiguration
	QueueConfigurations          []QueueNotificationConfiguration
	LambdaFunctionConfigurations []LambdaFunctionNotificationConfiguration
	EventBridgeConfiguration     *EventBridgeNotificationConfiguration
}

// TopicNotificationConfiguration describes SNS topic notification settings.
type TopicNotificationConfiguration struct {
	ID       string
	TopicArn string
	Events   []string
	Filter   *NotificationFilter
}

// QueueNotificationConfiguration describes SQS queue notification settings.
type QueueNotificationConfiguration struct {
	ID       string
	QueueArn string
	Events   []string
	Filter   *NotificationFilter
}

// LambdaFunctionNotificationConfiguration describes Lambda notification settings.
type LambdaFunctionNotificationConfiguration struct {
	ID                string
	LambdaFunctionArn string
	Events            []string
	Filter            *NotificationFilter
}

// EventBridgeNotificationConfiguration enables EventBridge delivery.
type EventBridgeNotificationConfiguration struct{}

// NotificationFilter stores object key filter rules.
type NotificationFilter struct {
	Key *S3KeyFilter
}

// S3KeyFilter stores S3 key filter rules.
type S3KeyFilter struct {
	FilterRules []FilterRule
}

// FilterRule stores a single prefix or suffix filter rule.
type FilterRule struct {
	Name  string
	Value string
}

// Storage defines the interface for storage backends.
type Storage interface {
	// Bucket operations
	CreateBucket(ctx context.Context, name string) error
	DeleteBucket(ctx context.Context, name string) error
	HeadBucket(ctx context.Context, name string) (*Bucket, error)
	ListBuckets(ctx context.Context) ([]Bucket, error)

	// Object operations
	PutObject(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string, metadata map[string]string) (*Object, error)
	GetObject(ctx context.Context, bucket, key string) (*ObjectData, error)
	GetObjectRange(ctx context.Context, bucket, key string, start, end int64) (*ObjectData, error)
	HeadObject(ctx context.Context, bucket, key string) (*Object, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	DeleteObjects(ctx context.Context, bucket string, keys []string) ([]DeletedObject, []DeleteError, error)
	CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*Object, error)
	CopyObjectVersioned(ctx context.Context, srcBucket, srcKey, srcVersionID, dstBucket, dstKey string, metadata map[string]string) (*Object, string, error)
	ListObjectsV2(ctx context.Context, input *ListObjectsInput) (*ListObjectsOutput, error)

	// Multipart upload operations.
	//
	// CreateMultipartUpload captures the optional Object Lock intent
	// (lockMode/lockRetainUntilDate/lockDefaultDays/lockDefaultYears/lockLegalHold,
	// issue #40) parsed from the x-amz-object-lock-* headers or bucket
	// DefaultRetention so it can be applied to the version finalized at
	// CompleteMultipartUpload. Pass zero values for "no lock".
	CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string, lockMode ObjectLockRetentionMode, lockRetainUntilDate *time.Time, lockDefaultDays, lockDefaultYears *int32, lockLegalHold ObjectLegalHoldStatus) (*MultipartUpload, error)
	UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader, size int64) (*Part, error)
	UploadPartCopy(ctx context.Context, bucket, key, uploadID string, partNumber int32, srcBucket, srcKey string, startByte, endByte *int64) (*Part, error)
	// GetMultipartUpload returns the in-progress upload record (including the
	// Object Lock intent captured at creation, issue #40), or nil if no such
	// upload exists.
	GetMultipartUpload(ctx context.Context, uploadID string) (*MultipartUpload, error)
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []Part) (*Object, error)
	CompleteMultipartUploadVersioned(ctx context.Context, bucket, key, uploadID string, parts []Part) (*Object, string, error)
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
	ListParts(ctx context.Context, input *ListPartsInput) (*ListPartsOutput, error)
	ListMultipartUploads(ctx context.Context, input *ListMultipartUploadsInput) (*ListMultipartUploadsOutput, error)

	// Tagging operations.
	// Object tagging is per-version (issue #41): versionID "" targets the null
	// version, a non-empty versionID targets that exact version.
	PutObjectTagging(ctx context.Context, bucket, key, versionID string, tags []Tag) error
	GetObjectTagging(ctx context.Context, bucket, key, versionID string) ([]Tag, error)
	DeleteObjectTagging(ctx context.Context, bucket, key, versionID string) error
	PutBucketTagging(ctx context.Context, bucket string, tags []Tag) error
	GetBucketTagging(ctx context.Context, bucket string) ([]Tag, error)
	DeleteBucketTagging(ctx context.Context, bucket string) error

	// CORS operations
	PutBucketCors(ctx context.Context, bucket string, cors *CORSConfiguration) error
	GetBucketCors(ctx context.Context, bucket string) (*CORSConfiguration, error)
	DeleteBucketCors(ctx context.Context, bucket string) error

	// Versioning operations
	PutBucketVersioning(ctx context.Context, bucket string, status VersioningStatus) error
	GetBucketVersioning(ctx context.Context, bucket string) (VersioningStatus, error)
	PutObjectVersioned(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string, metadata map[string]string) (*Object, string, error)
	GetObjectVersioned(ctx context.Context, bucket, key, versionID string) (*ObjectData, error)
	DeleteObjectVersioned(ctx context.Context, bucket, key, versionID string, versionTargeted bool) (string, bool, error)
	ListObjectVersions(ctx context.Context, input *ListObjectVersionsInput) (*ListObjectVersionsOutput, error)

	// ACL operations.
	// Object ACLs are per-version (issue #41): versionID "" targets the null
	// version, a non-empty versionID targets that exact version.
	PutBucketACL(ctx context.Context, bucket string, acl *ACL) error
	GetBucketACL(ctx context.Context, bucket string) (*ACL, error)
	PutObjectACL(ctx context.Context, bucket, key, versionID string, acl *ACL) error
	GetObjectACL(ctx context.Context, bucket, key, versionID string) (*ACL, error)

	// Encryption operations
	PutBucketEncryption(ctx context.Context, bucket string, config *ServerSideEncryptionConfiguration) error
	GetBucketEncryption(ctx context.Context, bucket string) (*ServerSideEncryptionConfiguration, error)
	DeleteBucketEncryption(ctx context.Context, bucket string) error

	// Lifecycle operations
	PutBucketLifecycleConfiguration(ctx context.Context, bucket string, config *LifecycleConfiguration) error
	GetBucketLifecycleConfiguration(ctx context.Context, bucket string) (*LifecycleConfiguration, error)
	DeleteBucketLifecycle(ctx context.Context, bucket string) error

	// Object Lock operations
	SetBucketObjectLockEnabled(ctx context.Context, bucket string, enabled bool) error
	GetBucketObjectLockEnabled(ctx context.Context, bucket string) (bool, error)
	PutObjectLockConfiguration(ctx context.Context, bucket string, config *ObjectLockConfiguration) error
	GetObjectLockConfiguration(ctx context.Context, bucket string) (*ObjectLockConfiguration, error)
	PutObjectRetention(ctx context.Context, bucket, key, versionID string, retention *ObjectRetention) error
	GetObjectRetention(ctx context.Context, bucket, key, versionID string) (*ObjectRetention, error)
	PutObjectLegalHold(ctx context.Context, bucket, key, versionID string, legalHold *ObjectLegalHold) error
	GetObjectLegalHold(ctx context.Context, bucket, key, versionID string) (*ObjectLegalHold, error)
	// ApplyObjectLockOnVersion atomically persists retention and legal hold for
	// a single object version in one metadata transaction.
	ApplyObjectLockOnVersion(ctx context.Context, bucket, key, versionID string, retention *ObjectRetention, legalHold *ObjectLegalHold) error
	// RollbackNewObjectVersion removes a newly created version and restores the
	// prior current pointer when lock application fails after a versioned write.
	RollbackNewObjectVersion(ctx context.Context, bucket, key, versionID string) error
	// ResolveObjectVersion maps a client-supplied versionId selector to a
	// concrete version. requestedVersionID "" means "current version". The
	// returned resolvedVersionID is "" for the null version. isDeleteMarker
	// reports whether the resolved version is a delete marker. ErrObjectNotFound
	// is returned when no matching version exists.
	ResolveObjectVersion(ctx context.Context, bucket, key, requestedVersionID string) (resolvedVersionID string, isDeleteMarker bool, err error)

	// Bucket Policy operations
	PutBucketPolicy(ctx context.Context, bucket string, policy string) error
	GetBucketPolicy(ctx context.Context, bucket string) (string, error)
	DeleteBucketPolicy(ctx context.Context, bucket string) error

	// Website Hosting operations
	PutBucketWebsite(ctx context.Context, bucket string, config *WebsiteConfiguration) error
	GetBucketWebsite(ctx context.Context, bucket string) (*WebsiteConfiguration, error)
	DeleteBucketWebsite(ctx context.Context, bucket string) error

	// Bucket Notification operations
	PutBucketNotification(ctx context.Context, bucket string, config *NotificationConfiguration) error
	GetBucketNotification(ctx context.Context, bucket string) (*NotificationConfiguration, error)

	// --- Lifecycle engine support ---

	// ListLifecycleObjectKeys returns object keys in a bucket using keyset
	// pagination. Keys come from the DISTINCT union of the objects and
	// object_versions tables so pre-versioning objects (which have no version
	// rows) are not missed. Results are sorted ascending and only include keys
	// strictly greater than afterKey.
	ListLifecycleObjectKeys(ctx context.Context, bucket, afterKey string, limit int) ([]string, error)

	// GetObjectVersionsForKey returns every version of a single key ordered by
	// (last_modified DESC, version_id DESC) so element 0 is the deterministic
	// latest (current) version.
	GetObjectVersionsForKey(ctx context.Context, bucket, key string) ([]ObjectVersion, error)

	// ExpireObjectVersionGuarded physically deletes a single noncurrent object
	// version inside a real BEGIN IMMEDIATE transaction. It re-checks legal
	// hold / retention (fail-closed, GOVERNANCE is never bypassed) and the
	// requested state guards before deleting the version + lock/acl/tag rows;
	// the version file is unlinked only after commit. Guard failures and busy
	// locks are reported via ExpireOutcome, not as errors.
	ExpireObjectVersionGuarded(ctx context.Context, bucket, key, versionID string, guards ExpireGuards, now time.Time) (ExpireOutcome, error)

	// CreateExpirationDeleteMarker creates an S3-style expiration delete marker
	// over the current version of a key (Enabled buckets). Inside a BEGIN
	// IMMEDIATE transaction it CAS-verifies that the latest version is still
	// expectedCurrentVersionID and not already a delete marker, inserts the
	// marker row, removes the current pointer, and unlinks the current file
	// after commit. Pre-versioning objects are snapshotted to the null version
	// first (pass expectedCurrentVersionID == ""). No data is destroyed, so
	// retention / legal hold do not block marker creation (S3-compliant).
	CreateExpirationDeleteMarker(ctx context.Context, bucket, key, expectedCurrentVersionID string) (markerVersionID string, outcome ExpireOutcome, err error)

	// ExpireCurrentObjectGuarded physically deletes the current object of a
	// non-versioned bucket inside a BEGIN IMMEDIATE transaction. It re-checks
	// the null-version lock rows (fail-closed) and verifies objects.last_modified
	// still equals expectedLastModified before deleting; the file is unlinked
	// only after commit.
	ExpireCurrentObjectGuarded(ctx context.Context, bucket, key string, expectedLastModified time.Time, now time.Time) (ExpireOutcome, error)

	// RecordLifecycleRun persists a per-bucket summary of the last lifecycle
	// cycle for observability (best-effort; failures must not abort a cycle).
	RecordLifecycleRun(ctx context.Context, bucket string, lastRunAt time.Time, actions, skippedLocked, errs int) error

	// LifecycleOrphanGC removes files left behind by interrupted operations:
	// version files under .versions/<key>/ with no object_versions row,
	// .uploads/<uploadID> directories with no multipart_uploads row, and
	// .tmp-* scratch files inside those trees — each only when its mtime is
	// strictly older than cutoff. Current object files are never touched.
	// Returns the number of files scanned and removed.
	LifecycleOrphanGC(ctx context.Context, cutoff time.Time) (scanned int, removed int, err error)

	// Close releases storage resources.
	Close() error
}

// ExpireGuards selects the transactional state guards applied by
// ExpireObjectVersionGuarded in addition to the always-on lock guard.
type ExpireGuards struct {
	// RequireNoncurrent fails the delete (SkippedStateChanged) unless a newer
	// version exists for the key — i.e. the target is still noncurrent and has
	// not been promoted to current by a concurrent latest-version delete.
	RequireNoncurrent bool
	// RequireAllVersionsAreDeleteMarkers fails the delete unless every version
	// of the key is a delete marker (the EODM precondition). Guards against
	// resurrecting a real version by removing the marker that hides it.
	RequireAllVersionsAreDeleteMarkers bool
}

// ExpireOutcome reports the result of a guarded lifecycle deletion.
type ExpireOutcome int

const (
	// ExpireExpired means the version/object was deleted.
	ExpireExpired ExpireOutcome = iota
	// ExpireSkippedLocked means an active retention or legal hold blocked it.
	ExpireSkippedLocked
	// ExpireSkippedStateChanged means a concurrent change invalidated the plan.
	ExpireSkippedStateChanged
	// ExpireSkippedBusy means the write lock could not be acquired (fail-closed).
	ExpireSkippedBusy
	// ExpireNotFound means the target row no longer exists (idempotent no-op).
	ExpireNotFound
)

// String renders an ExpireOutcome for logs and reports.
func (o ExpireOutcome) String() string {
	switch o {
	case ExpireExpired:
		return "Expired"
	case ExpireSkippedLocked:
		return "SkippedLocked"
	case ExpireSkippedStateChanged:
		return "SkippedStateChanged"
	case ExpireSkippedBusy:
		return "SkippedBusy"
	case ExpireNotFound:
		return "NotFound"
	default:
		return "Unknown"
	}
}
