package api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// GetObjectAttributesResponse is the response for GetObjectAttributes.
type GetObjectAttributesResponse struct {
	XMLName      xml.Name `xml:"GetObjectAttributesResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	ETag         string   `xml:"ETag,omitempty"`
	StorageClass string   `xml:"StorageClass,omitempty"`
	ObjectSize   *int64   `xml:"ObjectSize,omitempty"`
}

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

// CopyObjectResult is the response for CopyObject.
type CopyObjectResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	Xmlns        string   `xml:"xmlns,attr"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

// DeleteRequest is the request for DeleteObjects.
type DeleteRequest struct {
	XMLName xml.Name           `xml:"Delete"`
	Objects []ObjectIdentifier `xml:"Object"`
	Quiet   bool               `xml:"Quiet,omitempty"`
}

// ObjectIdentifier identifies an object to delete.
type ObjectIdentifier struct {
	Key string `xml:"Key"`
}

// DeleteResult is the response for DeleteObjects.
type DeleteResult struct {
	XMLName xml.Name             `xml:"DeleteResult"`
	Xmlns   string               `xml:"xmlns,attr"`
	Deleted []DeletedObjectInfo  `xml:"Deleted,omitempty"`
	Errors  []DeleteObjectsError `xml:"Error,omitempty"`
}

// DeletedObjectInfo represents a successfully deleted object.
type DeletedObjectInfo struct {
	Key string `xml:"Key"`
}

// DeleteObjectsError represents an error deleting an object.
type DeleteObjectsError struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
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

	// Check for aws-chunked encoding (streaming payload signature)
	contentEncoding := r.Header.Get("Content-Encoding")
	contentSHA256 := r.Header.Get("X-Amz-Content-Sha256")
	var body io.Reader = r.Body

	if IsAWSChunked(contentEncoding, contentSHA256) {
		// Use decoded content length for aws-chunked
		decodedLengthStr := r.Header.Get("X-Amz-Decoded-Content-Length")
		if decodedLengthStr != "" {
			decodedLength, err := strconv.ParseInt(decodedLengthStr, 10, 64)
			if err == nil {
				contentLength = decodedLength
			}
		}
		// Wrap body with chunked reader to decode aws-chunked format
		body = NewChunkedReader(r.Body)
	}

	// Parse custom metadata
	metadata := make(map[string]string)
	for key, values := range r.Header {
		if strings.HasPrefix(strings.ToLower(key), "x-amz-meta-") {
			metaKey := strings.TrimPrefix(strings.ToLower(key), "x-amz-meta-")
			metadata[metaKey] = values[0]
		}
	}

	// Parse x-amz-tagging header
	taggingHeader := r.Header.Get("x-amz-tagging")
	tags, err := ParseTaggingHeader(taggingHeader)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket+"/"+key)
		return
	}

	// Check if versioning is enabled
	versioningStatus, _ := h.storage.GetBucketVersioning(r.Context(), bucket)

	var obj *storage.Object
	var versionID string

	if versioningStatus == storage.VersioningStatusEnabled {
		// Use versioned put
		obj, versionID, err = h.storage.PutObjectVersioned(r.Context(), bucket, key, body, contentLength, contentType, metadata)
	} else {
		// Use regular put
		obj, err = h.storage.PutObject(r.Context(), bucket, key, body, contentLength, contentType, metadata)
	}

	if err != nil {
		if errors.Is(err, storage.ErrInvalidKey) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteError(w, ErrInternalError)
		return
	}

	// Store tags if provided
	// Note: Tag setting failure is logged but does not fail the request.
	// This matches S3's behavior where the object creation is prioritized,
	// and tag failures are treated as non-critical. The object is still
	// usable without tags, and tags can be set separately via PutObjectTagging.
	if len(tags) > 0 {
		if err := h.storage.PutObjectTagging(r.Context(), bucket, key, tags); err != nil {
			log.Error().Err(err).Str("bucket", bucket).Str("key", key).Msg("Failed to set object tags")
		}
	}

	// Handle canned ACL header
	// Note: ACL setting failure is logged but does not fail the request.
	// Similar to tags, the object creation takes priority. The default ACL
	// (private) is applied when ACL setting fails, and ACL can be set
	// separately via PutObjectAcl.
	cannedACL := r.Header.Get("x-amz-acl")
	if cannedACL != "" {
		if !isValidCannedACL(cannedACL) {
			// Log warning but don't fail - use default private ACL
			log.Warn().Str("bucket", bucket).Str("key", key).Str("acl", cannedACL).Msg("Invalid canned ACL specified, ignoring")
		} else {
			acl := storage.CannedACLToACL(storage.CannedACL(cannedACL), storage.DefaultOwnerID, storage.DefaultOwnerDisplay)
			if err := h.storage.PutObjectACL(r.Context(), bucket, key, acl); err != nil {
				log.Error().Err(err).Str("bucket", bucket).Str("key", key).Msg("Failed to set object ACL")
			}
		}
	}

	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	if versionID != "" {
		w.Header().Set("x-amz-version-id", versionID)
	}
	w.WriteHeader(http.StatusOK)
}

// GetObject handles GET /{bucket}/{key} - GetObject.
func (h *Handler) GetObject(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	// Check for versionId query parameter
	versionID := r.URL.Query().Get("versionId")

	// Check for Range header
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" && versionID == "" {
		h.getObjectRange(w, r, bucket, key, rangeHeader)
		return
	}

	var obj *storage.ObjectData
	var err error

	if versionID != "" {
		// Get specific version
		obj, err = h.storage.GetObjectVersioned(r.Context(), bucket, key, versionID)
	} else {
		obj, err = h.storage.GetObject(r.Context(), bucket, key)
	}

	if err != nil {
		if errors.Is(err, storage.ErrInvalidKey) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
			return
		}
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

	// Set version ID header if versioning was used
	if versionID != "" {
		w.Header().Set("x-amz-version-id", versionID)
	}

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
		if errors.Is(err, storage.ErrInvalidKey) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
			return
		}
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
		if errors.Is(err, storage.ErrInvalidKey) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
			return
		}
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
		if errors.Is(err, storage.ErrInvalidKey) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
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

	// Check for versionId query parameter
	versionID := r.URL.Query().Get("versionId")

	// Check if versioning is enabled
	versioningStatus, _ := h.storage.GetBucketVersioning(r.Context(), bucket)

	if versioningStatus == storage.VersioningStatusEnabled || versionID != "" {
		// Use versioned delete
		returnedVersionID, isDeleteMarker, err := h.storage.DeleteObjectVersioned(r.Context(), bucket, key, versionID)
		if err != nil {
			if errors.Is(err, storage.ErrInvalidKey) {
				WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
				return
			}
			if errors.Is(err, storage.ErrBucketNotFound) {
				WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
				return
			}
			if errors.Is(err, storage.ErrObjectNotFound) {
				// S3 returns 204 even if version doesn't exist
				w.WriteHeader(http.StatusNoContent)
				return
			}
			WriteError(w, ErrInternalError)
			return
		}

		if returnedVersionID != "" {
			w.Header().Set("x-amz-version-id", returnedVersionID)
		}
		if isDeleteMarker {
			w.Header().Set("x-amz-delete-marker", "true")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Regular delete (no versioning)
	err := h.storage.DeleteObject(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidKey) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		// S3 returns 204 even if object doesn't exist
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteObjects handles POST /{bucket}?delete - DeleteObjects.
func (h *Handler) DeleteObjects(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse request body
	var deleteReq DeleteRequest
	if err := xml.NewDecoder(r.Body).Decode(&deleteReq); err != nil {
		WriteError(w, ErrMalformedXML)
		return
	}

	// Extract keys from request
	keys := make([]string, len(deleteReq.Objects))
	for i, obj := range deleteReq.Objects {
		keys[i] = obj.Key
	}

	// Delete objects
	deleted, errs, err := h.storage.DeleteObjects(r.Context(), bucket, keys)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteError(w, ErrInternalError)
		return
	}

	// Build response
	result := DeleteResult{
		Xmlns:  "http://s3.amazonaws.com/doc/2006-03-01/",
		Errors: make([]DeleteObjectsError, len(errs)),
	}

	// In Quiet mode, only return errors (not successfully deleted objects)
	if !deleteReq.Quiet {
		result.Deleted = make([]DeletedObjectInfo, len(deleted))
		for i, d := range deleted {
			result.Deleted[i] = DeletedObjectInfo{
				Key: d.Key,
			}
		}
	}

	for i, e := range errs {
		result.Errors[i] = DeleteObjectsError{
			Key:     e.Key,
			Code:    e.Code,
			Message: e.Message,
		}
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode DeleteObjects response")
	}
}

// CopyObject handles PUT /{bucket}/{key} with x-amz-copy-source header - CopyObject.
func (h *Handler) CopyObject(w http.ResponseWriter, r *http.Request) {
	dstBucket := GetBucket(r)
	dstKey := GetKey(r)

	// Get copy source from header
	copySource := r.Header.Get("x-amz-copy-source")
	if copySource == "" {
		WriteError(w, ErrInvalidRequest)
		return
	}

	// URL decode the copy source (may contain URL-encoded characters)
	copySource, err := url.QueryUnescape(copySource)
	if err != nil {
		WriteError(w, ErrInvalidRequest)
		return
	}

	// Parse copy source: /bucket/key or bucket/key
	copySource = strings.TrimPrefix(copySource, "/")
	parts := strings.SplitN(copySource, "/", 2)
	if len(parts) != 2 {
		WriteError(w, ErrInvalidRequest)
		return
	}
	srcBucket := parts[0]
	srcKey := parts[1]

	// Get metadata directive (default is COPY)
	metadataDirective := r.Header.Get("x-amz-metadata-directive")
	if metadataDirective == "" {
		metadataDirective = "COPY"
	}

	var metadata map[string]string
	if metadataDirective == "REPLACE" {
		// Use new metadata from request headers
		metadata = make(map[string]string)
		for key, values := range r.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-amz-meta-") {
				metaKey := strings.TrimPrefix(strings.ToLower(key), "x-amz-meta-")
				metadata[metaKey] = values[0]
			}
		}
	}
	// If COPY, pass nil to preserve original metadata

	obj, err := h.storage.CopyObject(r.Context(), srcBucket, srcKey, dstBucket, dstKey, metadata)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidKey) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+dstBucket+"/"+dstKey)
			return
		}
		var bucketErr *storage.BucketNotFoundError
		if errors.As(err, &bucketErr) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucketErr.Bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+srcBucket+"/"+srcKey)
			return
		}
		WriteError(w, ErrInternalError)
		return
	}

	result := CopyObjectResult{
		Xmlns:        "http://s3.amazonaws.com/doc/2006-03-01/",
		LastModified: obj.LastModified.Format(time.RFC3339),
		ETag:         "\"" + obj.ETag + "\"",
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode CopyObject response")
	}
}

// GetObjectAttributes handles GET /{bucket}/{key}?attributes - GetObjectAttributes.
func (h *Handler) GetObjectAttributes(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	// Parse requested attributes from x-amz-object-attributes header
	// AWS SDK may send multiple headers with the same name, so use Header.Values()
	attributesHeaders := r.Header.Values("x-amz-object-attributes")
	requestedAttrs := make(map[string]bool)
	for _, header := range attributesHeaders {
		for _, attr := range strings.Split(header, ",") {
			requestedAttrs[strings.TrimSpace(attr)] = true
		}
	}

	// Get object metadata
	obj, err := h.storage.HeadObject(r.Context(), bucket, key)
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

	result := GetObjectAttributesResponse{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
	}

	// Include requested attributes
	if len(requestedAttrs) == 0 || requestedAttrs["ETag"] {
		result.ETag = obj.ETag
	}
	if len(requestedAttrs) == 0 || requestedAttrs["ObjectSize"] {
		result.ObjectSize = &obj.Size
	}
	if len(requestedAttrs) == 0 || requestedAttrs["StorageClass"] {
		result.StorageClass = "STANDARD"
	}

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Last-Modified", obj.LastModified.Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetObjectAttributes response")
	}
}

// ListBucketResultV1 is the response for ListObjects (v1).
type ListBucketResultV1 struct {
	XMLName        xml.Name       `xml:"ListBucketResult"`
	Xmlns          string         `xml:"xmlns,attr"`
	Name           string         `xml:"Name"`
	Prefix         string         `xml:"Prefix"`
	Delimiter      string         `xml:"Delimiter,omitempty"`
	Marker         string         `xml:"Marker,omitempty"`
	MaxKeys        int32          `xml:"MaxKeys"`
	IsTruncated    bool           `xml:"IsTruncated"`
	NextMarker     string         `xml:"NextMarker,omitempty"`
	Contents       []ObjectInfo   `xml:"Contents"`
	CommonPrefixes []CommonPrefix `xml:"CommonPrefixes,omitempty"`
}

// ListObjects handles GET /{bucket} without list-type=2 - ListObjects (v1).
func (h *Handler) ListObjects(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse query parameters
	query := r.URL.Query()
	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	maxKeysStr := query.Get("max-keys")
	marker := query.Get("marker")

	maxKeys := int32(1000)
	if maxKeysStr != "" {
		if mk, err := strconv.ParseInt(maxKeysStr, 10, 32); err == nil {
			maxKeys = int32(mk)
		}
	}

	// Use marker as start-after for the storage layer
	input := &storage.ListObjectsInput{
		Bucket:     bucket,
		Prefix:     prefix,
		Delimiter:  delimiter,
		MaxKeys:    maxKeys,
		StartAfter: marker,
	}

	output, err := h.storage.ListObjectsV2(r.Context(), input)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Str("bucket", bucket).Msg("Failed to list objects")
		WriteError(w, ErrInternalError)
		return
	}

	result := ListBucketResultV1{
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:        bucket,
		Prefix:      prefix,
		Delimiter:   delimiter,
		Marker:      marker,
		MaxKeys:     maxKeys,
		IsTruncated: output.IsTruncated,
		Contents:    make([]ObjectInfo, len(output.Objects)),
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

	// Set NextMarker if truncated (use the last key in the result)
	if output.IsTruncated && len(output.Objects) > 0 {
		result.NextMarker = output.Objects[len(output.Objects)-1].Key
	}

	for _, prefix := range output.CommonPrefixes {
		result.CommonPrefixes = append(result.CommonPrefixes, CommonPrefix{Prefix: prefix})
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode ListObjects response")
	}
}

// ListObjectsV2 handles GET /{bucket}?list-type=2 - ListObjectsV2.
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
