package api

import (
	"bytes"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// InitiateMultipartUploadResult is the response for CreateMultipartUpload.
type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadId string   `xml:"UploadId"`
}

// CompleteMultipartUploadResult is the response for CompleteMultipartUpload.
type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

// CompleteMultipartUploadRequest is the request body for CompleteMultipartUpload.
type CompleteMultipartUploadRequest struct {
	XMLName xml.Name       `xml:"CompleteMultipartUpload"`
	Parts   []CompletePart `xml:"Part"`
}

// CompletePart represents a part in CompleteMultipartUpload request.
type CompletePart struct {
	PartNumber int32  `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

// ListPartsResult is the response for ListParts.
type ListPartsResult struct {
	XMLName              xml.Name   `xml:"ListPartsResult"`
	Xmlns                string     `xml:"xmlns,attr"`
	Bucket               string     `xml:"Bucket"`
	Key                  string     `xml:"Key"`
	UploadId             string     `xml:"UploadId"`
	PartNumberMarker     int32      `xml:"PartNumberMarker"`
	NextPartNumberMarker int32      `xml:"NextPartNumberMarker,omitempty"`
	MaxParts             int32      `xml:"MaxParts"`
	IsTruncated          bool       `xml:"IsTruncated"`
	Parts                []PartInfo `xml:"Part"`
}

// PartInfo represents a part in ListParts response.
type PartInfo struct {
	PartNumber   int32  `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}

// CopyPartResult is the response for UploadPartCopy.
type CopyPartResult struct {
	XMLName      xml.Name `xml:"CopyPartResult"`
	Xmlns        string   `xml:"xmlns,attr"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

// ListMultipartUploadsResult is the response for ListMultipartUploads.
type ListMultipartUploadsResult struct {
	XMLName            xml.Name     `xml:"ListMultipartUploadsResult"`
	Xmlns              string       `xml:"xmlns,attr"`
	Bucket             string       `xml:"Bucket"`
	KeyMarker          string       `xml:"KeyMarker"`
	UploadIdMarker     string       `xml:"UploadIdMarker"`
	NextKeyMarker      string       `xml:"NextKeyMarker,omitempty"`
	NextUploadIdMarker string       `xml:"NextUploadIdMarker,omitempty"`
	MaxUploads         int32        `xml:"MaxUploads"`
	IsTruncated        bool         `xml:"IsTruncated"`
	Uploads            []UploadInfo `xml:"Upload"`
}

// UploadInfo represents an upload in ListMultipartUploads response.
type UploadInfo struct {
	Key       string `xml:"Key"`
	UploadId  string `xml:"UploadId"`
	Initiated string `xml:"Initiated"`
}

// CreateMultipartUpload handles POST /{bucket}/{key}?uploads - CreateMultipartUpload.
func (h *Handler) CreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Parse custom metadata
	metadata := make(map[string]string)
	for k, values := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-amz-meta-") {
			metaKey := strings.TrimPrefix(strings.ToLower(k), "x-amz-meta-")
			metadata[metaKey] = values[0]
		}
	}

	upload, err := h.storage.CreateMultipartUpload(r.Context(), bucket, key, contentType, metadata)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidKey) {
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Msg("Failed to create multipart upload")
		WriteError(w, ErrInternalError)
		return
	}

	result := InitiateMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:   bucket,
		Key:      key,
		UploadId: upload.UploadID,
	}

	var buf bytes.Buffer
	if err := xml.NewEncoder(&buf).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode CreateMultipartUpload response")
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// UploadPart handles PUT /{bucket}/{key}?partNumber={partNumber}&uploadId={uploadId} - UploadPart.
func (h *Handler) UploadPart(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	query := r.URL.Query()
	uploadID := query.Get("uploadId")
	partNumberStr := query.Get("partNumber")

	partNumber, err := strconv.ParseInt(partNumberStr, 10, 32)
	if err != nil || partNumber < 1 || partNumber > 10000 {
		WriteError(w, ErrInvalidPart)
		return
	}

	contentLength := r.ContentLength
	if contentLength < 0 {
		WriteError(w, ErrMissingContentLength)
		return
	}

	part, err := h.storage.UploadPart(r.Context(), bucket, key, uploadID, int32(partNumber), r.Body, contentLength)
	if err != nil {
		if errors.Is(err, storage.ErrUploadNotFound) {
			WriteError(w, ErrNoSuchUpload)
			return
		}
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Msg("Failed to upload part")
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("ETag", "\""+part.ETag+"\"")
	w.WriteHeader(http.StatusOK)
}

// UploadPartCopy handles PUT /{bucket}/{key}?partNumber={partNumber}&uploadId={uploadId} with x-amz-copy-source header - UploadPartCopy.
func (h *Handler) UploadPartCopy(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	query := r.URL.Query()
	uploadID := query.Get("uploadId")
	partNumberStr := query.Get("partNumber")

	partNumber, err := strconv.ParseInt(partNumberStr, 10, 32)
	if err != nil || partNumber < 1 || partNumber > 10000 {
		WriteError(w, ErrInvalidPart)
		return
	}

	// Parse x-amz-copy-source header
	copySource := r.Header.Get("x-amz-copy-source")
	if copySource == "" {
		WriteError(w, ErrInvalidRequest)
		return
	}

	// URL decode the copy source (may contain URL-encoded characters)
	copySource, err = url.QueryUnescape(copySource)
	if err != nil {
		WriteError(w, ErrInvalidRequest)
		return
	}

	// Remove leading slash if present
	copySource = strings.TrimPrefix(copySource, "/")

	// Parse source bucket and key
	parts := strings.SplitN(copySource, "/", 2)
	if len(parts) != 2 {
		WriteError(w, ErrInvalidRequest)
		return
	}
	srcBucket := parts[0]
	srcKey := parts[1]

	// Parse x-amz-copy-source-range header (optional)
	var startByte, endByte *int64
	copySourceRange := r.Header.Get("x-amz-copy-source-range")
	if copySourceRange != "" {
		// Format: bytes=start-end
		if !strings.HasPrefix(copySourceRange, "bytes=") {
			WriteError(w, ErrInvalidRequest)
			return
		}
		rangeStr := strings.TrimPrefix(copySourceRange, "bytes=")
		rangeParts := strings.Split(rangeStr, "-")
		if len(rangeParts) != 2 {
			WriteError(w, ErrInvalidRequest)
			return
		}

		start, err := strconv.ParseInt(rangeParts[0], 10, 64)
		if err != nil {
			WriteError(w, ErrInvalidRequest)
			return
		}
		end, err := strconv.ParseInt(rangeParts[1], 10, 64)
		if err != nil {
			WriteError(w, ErrInvalidRequest)
			return
		}
		startByte = &start
		endByte = &end
	}

	part, err := h.storage.UploadPartCopy(r.Context(), bucket, key, uploadID, int32(partNumber), srcBucket, srcKey, startByte, endByte)
	if err != nil {
		if errors.Is(err, storage.ErrUploadNotFound) {
			WriteError(w, ErrNoSuchUpload)
			return
		}
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+srcBucket)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+srcBucket+"/"+srcKey)
			return
		}
		if errors.Is(err, storage.ErrInvalidRange) {
			WriteError(w, ErrInvalidRange)
			return
		}
		log.Error().Err(err).Msg("Failed to upload part copy")
		WriteError(w, ErrInternalError)
		return
	}

	result := CopyPartResult{
		Xmlns:        "http://s3.amazonaws.com/doc/2006-03-01/",
		LastModified: part.LastModified.Format(time.RFC3339),
		ETag:         "\"" + part.ETag + "\"",
	}

	var buf bytes.Buffer
	if err := xml.NewEncoder(&buf).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode UploadPartCopy response")
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// CompleteMultipartUpload handles POST /{bucket}/{key}?uploadId={uploadId} - CompleteMultipartUpload.
func (h *Handler) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	query := r.URL.Query()
	uploadID := query.Get("uploadId")

	// Parse request body
	var req CompleteMultipartUploadRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, ErrInvalidRequest)
		return
	}

	// Validate parts list is not empty
	if len(req.Parts) == 0 {
		WriteError(w, ErrMalformedXML)
		return
	}

	// Validate parts are in order
	for i := 1; i < len(req.Parts); i++ {
		if req.Parts[i].PartNumber <= req.Parts[i-1].PartNumber {
			WriteError(w, ErrInvalidPartOrder)
			return
		}
	}

	// Convert to storage parts
	parts := make([]storage.Part, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = storage.Part{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		}
	}

	// Sort parts by part number (should already be sorted, but just to be safe)
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNumber < parts[j].PartNumber
	})

	obj, err := h.storage.CompleteMultipartUpload(r.Context(), bucket, key, uploadID, parts)
	if err != nil {
		if errors.Is(err, storage.ErrUploadNotFound) {
			WriteError(w, ErrNoSuchUpload)
			return
		}
		if errors.Is(err, storage.ErrInvalidPart) {
			WriteError(w, ErrInvalidPart)
			return
		}
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Msg("Failed to complete multipart upload")
		WriteError(w, ErrInternalError)
		return
	}

	result := CompleteMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Location: "/" + bucket + "/" + key,
		Bucket:   bucket,
		Key:      key,
		ETag:     "\"" + obj.ETag + "\"",
	}

	var buf bytes.Buffer
	if err := xml.NewEncoder(&buf).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode CompleteMultipartUpload response")
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// AbortMultipartUpload handles DELETE /{bucket}/{key}?uploadId={uploadId} - AbortMultipartUpload.
func (h *Handler) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	query := r.URL.Query()
	uploadID := query.Get("uploadId")

	err := h.storage.AbortMultipartUpload(r.Context(), bucket, key, uploadID)
	if err != nil {
		if errors.Is(err, storage.ErrUploadNotFound) {
			WriteError(w, ErrNoSuchUpload)
			return
		}
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Msg("Failed to abort multipart upload")
		WriteError(w, ErrInternalError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListParts handles GET /{bucket}/{key}?uploadId={uploadId} - ListParts.
func (h *Handler) ListParts(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	query := r.URL.Query()
	uploadID := query.Get("uploadId")

	maxPartsStr := query.Get("max-parts")
	maxParts := int32(1000)
	if maxPartsStr != "" {
		if mp, err := strconv.ParseInt(maxPartsStr, 10, 32); err == nil && mp > 0 {
			maxParts = int32(mp)
		}
	}

	partNumberMarkerStr := query.Get("part-number-marker")
	var partNumberMarker int32
	if partNumberMarkerStr != "" {
		if pnm, err := strconv.ParseInt(partNumberMarkerStr, 10, 32); err == nil {
			partNumberMarker = int32(pnm)
		}
	}

	input := &storage.ListPartsInput{
		Bucket:           bucket,
		Key:              key,
		UploadID:         uploadID,
		MaxParts:         maxParts,
		PartNumberMarker: partNumberMarker,
	}

	output, err := h.storage.ListParts(r.Context(), input)
	if err != nil {
		if errors.Is(err, storage.ErrUploadNotFound) {
			WriteError(w, ErrNoSuchUpload)
			return
		}
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Msg("Failed to list parts")
		WriteError(w, ErrInternalError)
		return
	}

	result := ListPartsResult{
		Xmlns:            "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:           bucket,
		Key:              key,
		UploadId:         uploadID,
		PartNumberMarker: partNumberMarker,
		MaxParts:         maxParts,
		IsTruncated:      output.IsTruncated,
		Parts:            make([]PartInfo, len(output.Parts)),
	}

	if output.IsTruncated {
		result.NextPartNumberMarker = output.NextPartNumberMarker
	}

	for i, part := range output.Parts {
		result.Parts[i] = PartInfo{
			PartNumber:   part.PartNumber,
			LastModified: part.LastModified.Format(time.RFC3339),
			ETag:         "\"" + part.ETag + "\"",
			Size:         part.Size,
		}
	}

	var buf bytes.Buffer
	if err := xml.NewEncoder(&buf).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode ListParts response")
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// ListMultipartUploads handles GET /{bucket}?uploads - ListMultipartUploads.
func (h *Handler) ListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	query := r.URL.Query()

	prefix := query.Get("prefix")

	maxUploadsStr := query.Get("max-uploads")
	maxUploads := int32(1000)
	if maxUploadsStr != "" {
		if mu, err := strconv.ParseInt(maxUploadsStr, 10, 32); err == nil && mu > 0 {
			maxUploads = int32(mu)
		}
	}

	keyMarker := query.Get("key-marker")
	uploadIdMarker := query.Get("upload-id-marker")

	input := &storage.ListMultipartUploadsInput{
		Bucket:         bucket,
		Prefix:         prefix,
		MaxUploads:     maxUploads,
		KeyMarker:      keyMarker,
		UploadIdMarker: uploadIdMarker,
	}

	output, err := h.storage.ListMultipartUploads(r.Context(), input)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Msg("Failed to list multipart uploads")
		WriteError(w, ErrInternalError)
		return
	}

	result := ListMultipartUploadsResult{
		Xmlns:          "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:         bucket,
		KeyMarker:      keyMarker,
		UploadIdMarker: uploadIdMarker,
		MaxUploads:     maxUploads,
		IsTruncated:    output.IsTruncated,
		Uploads:        make([]UploadInfo, len(output.Uploads)),
	}

	if output.IsTruncated {
		result.NextKeyMarker = output.NextKeyMarker
		result.NextUploadIdMarker = output.NextUploadIdMarker
	}

	for i, upload := range output.Uploads {
		result.Uploads[i] = UploadInfo{
			Key:       upload.Key,
			UploadId:  upload.UploadID,
			Initiated: upload.Initiated.Format(time.RFC3339),
		}
	}

	var buf bytes.Buffer
	if err := xml.NewEncoder(&buf).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode ListMultipartUploads response")
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
