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
		query := req.URL.Query()

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
				if query.Has("uploads") {
					// GET /{bucket}?uploads - ListMultipartUploads
					r.handler.ListMultipartUploads(w, req)
				} else if query.Has("location") {
					// GET /{bucket}?location - GetBucketLocation
					r.handler.GetBucketLocation(w, req)
				} else if query.Has("tagging") {
					// GET /{bucket}?tagging - GetBucketTagging
					r.handler.GetBucketTagging(w, req)
				} else if query.Has("cors") {
					// GET /{bucket}?cors - GetBucketCors
					r.handler.GetBucketCors(w, req)
				} else if query.Has("versioning") {
					// GET /{bucket}?versioning - GetBucketVersioning
					r.handler.GetBucketVersioning(w, req)
				} else if query.Has("versions") {
					// GET /{bucket}?versions - ListObjectVersions
					r.handler.ListObjectVersions(w, req)
				} else if query.Has("acl") {
					// GET /{bucket}?acl - GetBucketAcl
					r.handler.GetBucketAcl(w, req)
				} else if query.Has("encryption") {
					// GET /{bucket}?encryption - GetBucketEncryption
					r.handler.GetBucketEncryption(w, req)
				} else if query.Has("lifecycle") {
					// GET /{bucket}?lifecycle - GetBucketLifecycleConfiguration
					r.handler.GetBucketLifecycleConfiguration(w, req)
				} else if query.Has("object-lock") {
					// GET /{bucket}?object-lock - GetObjectLockConfiguration
					r.handler.GetObjectLockConfiguration(w, req)
				} else {
					// GET /{bucket} - ListObjectsV2
					r.handler.ListObjectsV2(w, req)
				}
			} else if query.Has("uploadId") {
				// GET /{bucket}/{key}?uploadId={uploadId} - ListParts
				r.handler.ListParts(w, req)
			} else if query.Has("attributes") {
				// GET /{bucket}/{key}?attributes - GetObjectAttributes
				r.handler.GetObjectAttributes(w, req)
			} else if query.Has("tagging") {
				// GET /{bucket}/{key}?tagging - GetObjectTagging
				r.handler.GetObjectTagging(w, req)
			} else if query.Has("acl") {
				// GET /{bucket}/{key}?acl - GetObjectAcl
				r.handler.GetObjectAcl(w, req)
			} else if query.Has("retention") {
				// GET /{bucket}/{key}?retention - GetObjectRetention
				r.handler.GetObjectRetention(w, req)
			} else if query.Has("legal-hold") {
				// GET /{bucket}/{key}?legal-hold - GetObjectLegalHold
				r.handler.GetObjectLegalHold(w, req)
			} else {
				// GET /{bucket}/{key} - GetObject
				r.handler.GetObject(w, req)
			}

		case http.MethodPut:
			if bucket != "" && key == "" {
				if query.Has("tagging") {
					// PUT /{bucket}?tagging - PutBucketTagging
					r.handler.PutBucketTagging(w, req)
				} else if query.Has("cors") {
					// PUT /{bucket}?cors - PutBucketCors
					r.handler.PutBucketCors(w, req)
				} else if query.Has("versioning") {
					// PUT /{bucket}?versioning - PutBucketVersioning
					r.handler.PutBucketVersioning(w, req)
				} else if query.Has("acl") {
					// PUT /{bucket}?acl - PutBucketAcl
					r.handler.PutBucketAcl(w, req)
				} else if query.Has("encryption") {
					// PUT /{bucket}?encryption - PutBucketEncryption
					r.handler.PutBucketEncryption(w, req)
				} else if query.Has("lifecycle") {
					// PUT /{bucket}?lifecycle - PutBucketLifecycleConfiguration
					r.handler.PutBucketLifecycleConfiguration(w, req)
				} else if query.Has("object-lock") {
					// PUT /{bucket}?object-lock - PutObjectLockConfiguration
					r.handler.PutObjectLockConfiguration(w, req)
				} else {
					// PUT /{bucket} - CreateBucket
					r.handler.CreateBucket(w, req)
				}
			} else if bucket != "" && key != "" {
				if query.Has("partNumber") && query.Has("uploadId") {
					// Check if this is UploadPartCopy (has x-amz-copy-source header)
					if req.Header.Get("x-amz-copy-source") != "" {
						// PUT /{bucket}/{key}?partNumber={partNumber}&uploadId={uploadId} with x-amz-copy-source - UploadPartCopy
						r.handler.UploadPartCopy(w, req)
					} else {
						// PUT /{bucket}/{key}?partNumber={partNumber}&uploadId={uploadId} - UploadPart
						r.handler.UploadPart(w, req)
					}
				} else if query.Has("tagging") {
					// PUT /{bucket}/{key}?tagging - PutObjectTagging
					r.handler.PutObjectTagging(w, req)
				} else if query.Has("acl") {
					// PUT /{bucket}/{key}?acl - PutObjectAcl
					r.handler.PutObjectAcl(w, req)
				} else if query.Has("retention") {
					// PUT /{bucket}/{key}?retention - PutObjectRetention
					r.handler.PutObjectRetention(w, req)
				} else if query.Has("legal-hold") {
					// PUT /{bucket}/{key}?legal-hold - PutObjectLegalHold
					r.handler.PutObjectLegalHold(w, req)
				} else if req.Header.Get("x-amz-copy-source") != "" {
					// PUT /{bucket}/{key} with x-amz-copy-source - CopyObject
					r.handler.CopyObject(w, req)
				} else {
					// PUT /{bucket}/{key} - PutObject
					r.handler.PutObject(w, req)
				}
			} else {
				api.WriteError(w, api.ErrInvalidRequest)
			}

		case http.MethodPost:
			if bucket != "" && key != "" {
				if query.Has("uploads") {
					// POST /{bucket}/{key}?uploads - CreateMultipartUpload
					r.handler.CreateMultipartUpload(w, req)
				} else if query.Has("uploadId") {
					// POST /{bucket}/{key}?uploadId={uploadId} - CompleteMultipartUpload
					r.handler.CompleteMultipartUpload(w, req)
				} else {
					api.WriteError(w, api.ErrInvalidRequest)
				}
			} else if bucket != "" && key == "" {
				if query.Has("delete") {
					// POST /{bucket}?delete - DeleteObjects
					r.handler.DeleteObjects(w, req)
				} else {
					api.WriteError(w, api.ErrInvalidRequest)
				}
			} else {
				api.WriteError(w, api.ErrInvalidRequest)
			}

		case http.MethodDelete:
			if bucket != "" && key == "" {
				if query.Has("tagging") {
					// DELETE /{bucket}?tagging - DeleteBucketTagging
					r.handler.DeleteBucketTagging(w, req)
				} else if query.Has("cors") {
					// DELETE /{bucket}?cors - DeleteBucketCors
					r.handler.DeleteBucketCors(w, req)
				} else if query.Has("encryption") {
					// DELETE /{bucket}?encryption - DeleteBucketEncryption
					r.handler.DeleteBucketEncryption(w, req)
				} else if query.Has("lifecycle") {
					// DELETE /{bucket}?lifecycle - DeleteBucketLifecycle
					r.handler.DeleteBucketLifecycle(w, req)
				} else {
					// DELETE /{bucket} - DeleteBucket
					r.handler.DeleteBucket(w, req)
				}
			} else if bucket != "" && key != "" {
				if query.Has("uploadId") {
					// DELETE /{bucket}/{key}?uploadId={uploadId} - AbortMultipartUpload
					r.handler.AbortMultipartUpload(w, req)
				} else if query.Has("tagging") {
					// DELETE /{bucket}/{key}?tagging - DeleteObjectTagging
					r.handler.DeleteObjectTagging(w, req)
				} else {
					// DELETE /{bucket}/{key} - DeleteObject
					r.handler.DeleteObject(w, req)
				}
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

		case http.MethodOptions:
			// OPTIONS /{bucket} or /{bucket}/{key} - CORS preflight
			if bucket != "" {
				r.handler.HandleCorsPreflightRequest(w, req)
			} else {
				w.WriteHeader(http.StatusOK)
			}

		default:
			api.WriteError(w, api.ErrMethodNotAllowed)
		}
	}
}
