package api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// ListBucketResult is the response for ListObjectsV2.
type ListBucketResult struct {
	XMLName               xml.Name       `xml:"ListBucketResult"`
	Xmlns                 string         `xml:"xmlns,attr"`
	Name                  string         `xml:"Name"`
	Prefix                string         `xml:"Prefix"`
	Delimiter             string         `xml:"Delimiter,omitempty"`
	MaxKeys               int32          `xml:"MaxKeys"`
	IsTruncated           bool           `xml:"IsTruncated"`
	KeyCount              int32          `xml:"KeyCount"`
	ContinuationToken     string         `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
	StartAfter            string         `xml:"StartAfter,omitempty"`
	Contents              []ObjectInfo   `xml:"Contents"`
	CommonPrefixes        []CommonPrefix `xml:"CommonPrefixes,omitempty"`
}

// ObjectInfo represents a single object in listing.
type ObjectInfo struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

// CommonPrefix represents a common prefix.
type CommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

// PutObject handles PUT /{bucket}/{key} - PutObject.
func (h *Handler) PutObject(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Get content length
	contentLength := r.ContentLength
	if contentLength < 0 {
		WriteError(w, ErrMissingContentLength)
		return
	}

	// Parse custom metadata
	metadata := make(map[string]string)
	for key, values := range r.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-amz-meta-") {
			metaKey := strings.TrimPrefix(strings.ToLower(key), "x-amz-meta-")
			metadata[metaKey] = values[0]
		}
	}

	obj, err := h.storage.PutObject(r.Context(), bucket, key, r.Body, contentLength, contentType, metadata)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	w.WriteHeader(http.StatusOK)
}

// GetObject handles GET /{bucket}/{key} - GetObject.
func (h *Handler) GetObject(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	// Check for Range header
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		h.getObjectRange(w, r, bucket, key, rangeHeader)
		return
	}

	obj, err := h.storage.GetObject(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		WriteError(w, ErrInternalError)
		return
	}
	defer obj.Body.Close()

	// Set response headers
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	w.Header().Set("Last-Modified", obj.LastModified.Format(http.TimeFormat))

	// Set custom metadata headers
	for k, v := range obj.Metadata {
		w.Header().Set("x-amz-meta-"+k, v)
	}

	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, obj.Body); err != nil {
		log.Error().Err(err).Str("bucket", bucket).Str("key", key).Msg("Failed to write object body")
	}
}

// getObjectRange handles GET with Range header.
func (h *Handler) getObjectRange(w http.ResponseWriter, r *http.Request, bucket, key, rangeHeader string) {
	// Parse range header: bytes=start-end
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		WriteError(w, ErrInvalidRange)
		return
	}

	// Get object metadata first
	objMeta, err := h.storage.HeadObject(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		WriteError(w, ErrInternalError)
		return
	}

	rangeSpec := strings.TrimPrefix(rangeHeader, "bytes=")
	parts := strings.Split(rangeSpec, "-")
	if len(parts) != 2 {
		WriteError(w, ErrInvalidRange)
		return
	}

	var start, end int64

	if parts[0] == "" {
		// Suffix range: -500 means last 500 bytes
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			WriteError(w, ErrInvalidRange)
			return
		}
		start = objMeta.Size - suffix
		end = objMeta.Size - 1
	} else if parts[1] == "" {
		// From start to end: 500-
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			WriteError(w, ErrInvalidRange)
			return
		}
		end = objMeta.Size - 1
	} else {
		// Explicit range: 0-499
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			WriteError(w, ErrInvalidRange)
			return
		}
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			WriteError(w, ErrInvalidRange)
			return
		}
	}

	// Validate range
	if start < 0 || end >= objMeta.Size || start > end {
		WriteError(w, ErrInvalidRange)
		return
	}

	obj, err := h.storage.GetObjectRange(r.Context(), bucket, key, start, end)
	if err != nil {
		WriteError(w, ErrInternalError)
		return
	}
	defer obj.Body.Close()

	// Set response headers
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(objMeta.Size, 10))
	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	w.Header().Set("Last-Modified", obj.LastModified.Format(http.TimeFormat))

	w.WriteHeader(http.StatusPartialContent)
	if _, err := io.Copy(w, obj.Body); err != nil {
		log.Error().Err(err).Str("bucket", bucket).Str("key", key).Msg("Failed to write object body range")
	}
}

// HeadObject handles HEAD /{bucket}/{key} - HeadObject.
func (h *Handler) HeadObject(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	obj, err := h.storage.HeadObject(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Set response headers
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	w.Header().Set("Last-Modified", obj.LastModified.Format(http.TimeFormat))

	// Set custom metadata headers
	for k, v := range obj.Metadata {
		w.Header().Set("x-amz-meta-"+k, v)
	}

	w.WriteHeader(http.StatusOK)
}

// DeleteObject handles DELETE /{bucket}/{key} - DeleteObject.
func (h *Handler) DeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	err := h.storage.DeleteObject(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		// S3 returns 204 even if object doesn't exist
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListObjectsV2 handles GET /{bucket} - ListObjectsV2.
func (h *Handler) ListObjectsV2(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse query parameters
	query := r.URL.Query()
	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	maxKeysStr := query.Get("max-keys")
	continuationToken := query.Get("continuation-token")
	startAfter := query.Get("start-after")

	maxKeys := int32(1000)
	if maxKeysStr != "" {
		if mk, err := strconv.ParseInt(maxKeysStr, 10, 32); err == nil {
			maxKeys = int32(mk)
		}
	}

	input := &storage.ListObjectsInput{
		Bucket:            bucket,
		Prefix:            prefix,
		Delimiter:         delimiter,
		MaxKeys:           maxKeys,
		ContinuationToken: continuationToken,
		StartAfter:        startAfter,
	}

	output, err := h.storage.ListObjectsV2(r.Context(), input)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteError(w, ErrInternalError)
		return
	}

	result := ListBucketResult{
		Xmlns:                 "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:                  bucket,
		Prefix:                prefix,
		Delimiter:             delimiter,
		MaxKeys:               maxKeys,
		IsTruncated:           output.IsTruncated,
		KeyCount:              output.KeyCount,
		ContinuationToken:     continuationToken,
		NextContinuationToken: output.NextContinuationToken,
		StartAfter:            startAfter,
		Contents:              make([]ObjectInfo, len(output.Objects)),
	}

	for i, obj := range output.Objects {
		result.Contents[i] = ObjectInfo{
			Key:          obj.Key,
			LastModified: obj.LastModified.Format(time.RFC3339),
			ETag:         "\"" + obj.ETag + "\"",
			Size:         obj.Size,
			StorageClass: "STANDARD",
		}
	}

	for _, prefix := range output.CommonPrefixes {
		result.CommonPrefixes = append(result.CommonPrefixes, CommonPrefix{Prefix: prefix})
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode ListObjectsV2 response")
	}
}
