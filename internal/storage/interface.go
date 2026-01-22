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

	// Close releases storage resources.
	Close() error
}
