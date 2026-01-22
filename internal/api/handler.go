package api

import (
	"context"
	"net/http"

	"github.com/kumasuke/jog/internal/storage"
)

// Handler handles S3 API requests.
type Handler struct {
	storage storage.Storage
}

// NewHandler creates a new Handler.
func NewHandler(storage storage.Storage) *Handler {
	return &Handler{
		storage: storage,
	}
}

// Context keys
type contextKey string

const (
	bucketKey contextKey = "bucket"
	keyKey    contextKey = "key"
)

// WithBucket adds bucket name to request context.
func WithBucket(r *http.Request, bucket string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), bucketKey, bucket))
}

// WithKey adds object key to request context.
func WithKey(r *http.Request, key string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), keyKey, key))
}

// GetBucket returns bucket name from request context.
func GetBucket(r *http.Request) string {
	if bucket, ok := r.Context().Value(bucketKey).(string); ok {
		return bucket
	}
	return ""
}

// GetKey returns object key from request context.
func GetKey(r *http.Request) string {
	if key, ok := r.Context().Value(keyKey).(string); ok {
		return key
	}
	return ""
}
