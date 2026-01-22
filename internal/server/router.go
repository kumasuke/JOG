package server

import (
	"net/http"
	"strings"

	"github.com/kumasuke/jog/internal/api"
	"github.com/kumasuke/jog/internal/auth"
)

// Router handles S3 API routing.
type Router struct {
	handler    *api.Handler
	authMiddle auth.Authenticator
}

// NewRouter creates a new Router.
func NewRouter(handler *api.Handler, authMiddle auth.Authenticator) *Router {
	return &Router{
		handler:    handler,
		authMiddle: authMiddle,
	}
}

// ServeHTTP handles HTTP requests.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Apply middleware
	var handler http.Handler = r.routeRequest()
	handler = r.authMiddle.Wrap(handler)
	handler = LoggingMiddleware(handler)
	handler = RecoveryMiddleware(handler)

	handler.ServeHTTP(w, req)
}

// routeRequest returns a handler that routes requests based on S3 API patterns.
func (r *Router) routeRequest() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path

		// Parse bucket and key from path
		// S3 path-style: /{bucket} or /{bucket}/{key}
		parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)

		bucket := ""
		key := ""
		if len(parts) > 0 {
			bucket = parts[0]
		}
		if len(parts) > 1 {
			key = parts[1]
		}

		// Store in context for handlers
		req = api.WithBucket(req, bucket)
		req = api.WithKey(req, key)

		switch req.Method {
		case http.MethodGet:
			if bucket == "" {
				// GET / - ListBuckets
				r.handler.ListBuckets(w, req)
			} else if key == "" {
				// GET /{bucket} - ListObjectsV2
				r.handler.ListObjectsV2(w, req)
			} else {
				// GET /{bucket}/{key} - GetObject
				r.handler.GetObject(w, req)
			}

		case http.MethodPut:
			if bucket != "" && key == "" {
				// PUT /{bucket} - CreateBucket
				r.handler.CreateBucket(w, req)
			} else if bucket != "" && key != "" {
				// PUT /{bucket}/{key} - PutObject
				r.handler.PutObject(w, req)
			} else {
				api.WriteError(w, api.ErrInvalidRequest)
			}

		case http.MethodDelete:
			if bucket != "" && key == "" {
				// DELETE /{bucket} - DeleteBucket
				r.handler.DeleteBucket(w, req)
			} else if bucket != "" && key != "" {
				// DELETE /{bucket}/{key} - DeleteObject
				r.handler.DeleteObject(w, req)
			} else {
				api.WriteError(w, api.ErrInvalidRequest)
			}

		case http.MethodHead:
			if bucket != "" && key == "" {
				// HEAD /{bucket} - HeadBucket
				r.handler.HeadBucket(w, req)
			} else if bucket != "" && key != "" {
				// HEAD /{bucket}/{key} - HeadObject
				r.handler.HeadObject(w, req)
			} else {
				api.WriteError(w, api.ErrInvalidRequest)
			}

		default:
			api.WriteError(w, api.ErrMethodNotAllowed)
		}
	}
}
