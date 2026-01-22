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
	ListObjectsV2(ctx context.Context, input *ListObjectsInput) (*ListObjectsOutput, error)

	// Close releases storage resources.
	Close() error
}
