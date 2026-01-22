package api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// VersioningConfiguration represents the XML structure for bucket versioning.
type VersioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Xmlns   string   `xml:"xmlns,attr,omitempty"`
	Status  string   `xml:"Status,omitempty"`
}

// ListVersionsResult represents the response for ListObjectVersions.
type ListVersionsResult struct {
	XMLName             xml.Name             `xml:"ListVersionsResult"`
	Xmlns               string               `xml:"xmlns,attr"`
	Name                string               `xml:"Name"`
	Prefix              string               `xml:"Prefix,omitempty"`
	KeyMarker           string               `xml:"KeyMarker,omitempty"`
	VersionIdMarker     string               `xml:"VersionIdMarker,omitempty"`
	NextKeyMarker       string               `xml:"NextKeyMarker,omitempty"`
	NextVersionIdMarker string               `xml:"NextVersionIdMarker,omitempty"`
	MaxKeys             int32                `xml:"MaxKeys"`
	IsTruncated         bool                 `xml:"IsTruncated"`
	Versions            []VersionInfo        `xml:"Version,omitempty"`
	DeleteMarkers       []DeleteMarkerInfo   `xml:"DeleteMarker,omitempty"`
	CommonPrefixes      []CommonPrefix       `xml:"CommonPrefixes,omitempty"`
}

// VersionInfo represents a version in the listing.
type VersionInfo struct {
	Key          string `xml:"Key"`
	VersionId    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

// DeleteMarkerInfo represents a delete marker in the listing.
type DeleteMarkerInfo struct {
	Key          string `xml:"Key"`
	VersionId    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
}

// PutBucketVersioning handles PUT /{bucket}?versioning - PutBucketVersioning.
func (h *Handler) PutBucketVersioning(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket)
		return
	}

	var versioningConfig VersioningConfiguration
	if err := xml.Unmarshal(body, &versioningConfig); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
		return
	}

	// Validate status
	status := storage.VersioningStatus(versioningConfig.Status)
	if status != storage.VersioningStatusEnabled && status != storage.VersioningStatusSuspended {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
		return
	}

	err = h.storage.PutBucketVersioning(r.Context(), bucket, status)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetBucketVersioning handles GET /{bucket}?versioning - GetBucketVersioning.
func (h *Handler) GetBucketVersioning(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	status, err := h.storage.GetBucketVersioning(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	response := VersioningConfiguration{
		Xmlns:  "http://s3.amazonaws.com/doc/2006-03-01/",
		Status: string(status),
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketVersioning response")
	}
}

// ListObjectVersions handles GET /{bucket}?versions - ListObjectVersions.
func (h *Handler) ListObjectVersions(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse query parameters
	query := r.URL.Query()
	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	keyMarker := query.Get("key-marker")
	versionIdMarker := query.Get("version-id-marker")
	maxKeysStr := query.Get("max-keys")

	maxKeys := int32(1000)
	if maxKeysStr != "" {
		if mk, err := strconv.ParseInt(maxKeysStr, 10, 32); err == nil {
			maxKeys = int32(mk)
		}
	}

	input := &storage.ListObjectVersionsInput{
		Bucket:          bucket,
		Prefix:          prefix,
		Delimiter:       delimiter,
		MaxKeys:         maxKeys,
		KeyMarker:       keyMarker,
		VersionIdMarker: versionIdMarker,
	}

	output, err := h.storage.ListObjectVersions(r.Context(), input)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	result := ListVersionsResult{
		Xmlns:               "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:                bucket,
		Prefix:              prefix,
		KeyMarker:           keyMarker,
		VersionIdMarker:     versionIdMarker,
		NextKeyMarker:       output.NextKeyMarker,
		NextVersionIdMarker: output.NextVersionIdMarker,
		MaxKeys:             maxKeys,
		IsTruncated:         output.IsTruncated,
	}

	// Add versions
	for _, v := range output.Versions {
		result.Versions = append(result.Versions, VersionInfo{
			Key:          v.Key,
			VersionId:    v.VersionID,
			IsLatest:     v.IsLatest,
			LastModified: v.LastModified.Format(time.RFC3339),
			ETag:         "\"" + v.ETag + "\"",
			Size:         v.Size,
			StorageClass: "STANDARD",
		})
	}

	// Add delete markers
	for _, dm := range output.DeleteMarkers {
		result.DeleteMarkers = append(result.DeleteMarkers, DeleteMarkerInfo{
			Key:          dm.Key,
			VersionId:    dm.VersionID,
			IsLatest:     dm.IsLatest,
			LastModified: dm.LastModified.Format(time.RFC3339),
		})
	}

	// Add common prefixes
	for _, cp := range output.CommonPrefixes {
		result.CommonPrefixes = append(result.CommonPrefixes, CommonPrefix{Prefix: cp})
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode ListObjectVersions response")
	}
}
