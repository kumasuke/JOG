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
	ACLGranteeTypeCanonicalUser   ACLGranteeType = "CanonicalUser"
	ACLGranteeTypeAmazonCustomer  ACLGranteeType = "AmazonCustomerByEmail"
	ACLGranteeTypeGroup           ACLGranteeType = "Group"
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
	ListObjectsV2(ctx context.Context, input *ListObjectsInput) (*ListObjectsOutput, error)

	// Multipart upload operations
	CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string) (*MultipartUpload, error)
	UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader, size int64) (*Part, error)
	UploadPartCopy(ctx context.Context, bucket, key, uploadID string, partNumber int32, srcBucket, srcKey string, startByte, endByte *int64) (*Part, error)
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []Part) (*Object, error)
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
	ListParts(ctx context.Context, input *ListPartsInput) (*ListPartsOutput, error)
	ListMultipartUploads(ctx context.Context, input *ListMultipartUploadsInput) (*ListMultipartUploadsOutput, error)

	// Tagging operations
	PutObjectTagging(ctx context.Context, bucket, key string, tags []Tag) error
	GetObjectTagging(ctx context.Context, bucket, key string) ([]Tag, error)
	DeleteObjectTagging(ctx context.Context, bucket, key string) error
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
	DeleteObjectVersioned(ctx context.Context, bucket, key, versionID string) (string, bool, error)
	ListObjectVersions(ctx context.Context, input *ListObjectVersionsInput) (*ListObjectVersionsOutput, error)

	// ACL operations
	PutBucketACL(ctx context.Context, bucket string, acl *ACL) error
	GetBucketACL(ctx context.Context, bucket string) (*ACL, error)
	PutObjectACL(ctx context.Context, bucket, key string, acl *ACL) error
	GetObjectACL(ctx context.Context, bucket, key string) (*ACL, error)

	// Close releases storage resources.
	Close() error
}
