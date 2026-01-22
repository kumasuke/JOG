package api

import (
	"encoding/xml"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// ListAllMyBucketsResult is the response for ListBuckets.
type ListAllMyBucketsResult struct {
	XMLName xml.Name `xml:"ListAllMyBucketsResult"`
	Xmlns   string   `xml:"xmlns,attr"`
	Owner   Owner    `xml:"Owner"`
	Buckets Buckets  `xml:"Buckets"`
}

// Owner represents bucket owner information.
type Owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName,omitempty"`
}

// Buckets is a container for bucket list.
type Buckets struct {
	Bucket []BucketInfo `xml:"Bucket"`
}

// BucketInfo represents a single bucket.
type BucketInfo struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

// Bucket name validation regex
var bucketNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// ValidateBucketName validates a bucket name according to S3 rules.
func ValidateBucketName(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	if !bucketNameRegex.MatchString(name) {
		return false
	}
	// Must not look like IP address
	ipRegex := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
	if ipRegex.MatchString(name) {
		return false
	}
	return true
}

// CreateBucket handles PUT /{bucket} - CreateBucket.
func (h *Handler) CreateBucket(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Validate bucket name
	if !ValidateBucketName(bucket) {
		WriteErrorWithResource(w, ErrInvalidBucketName, "/"+bucket)
		return
	}

	err := h.storage.CreateBucket(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketAlreadyExists) {
			WriteErrorWithResource(w, ErrBucketAlreadyOwnedByYou, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	// Check if object lock should be enabled
	objectLockEnabled := r.Header.Get("x-amz-bucket-object-lock-enabled")
	if objectLockEnabled == "true" {
		err = h.storage.SetBucketObjectLockEnabled(r.Context(), bucket, true)
		if err != nil {
			log.Error().Err(err).Msg("Failed to enable object lock for bucket")
			// Delete the bucket since we couldn't enable object lock
			if delErr := h.storage.DeleteBucket(r.Context(), bucket); delErr != nil {
				log.Error().Err(delErr).Str("bucket", bucket).Msg("Failed to rollback bucket creation")
			}
			WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
			return
		}
	}

	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

// DeleteBucket handles DELETE /{bucket} - DeleteBucket.
func (h *Handler) DeleteBucket(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	err := h.storage.DeleteBucket(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrBucketNotEmpty) {
			WriteErrorWithResource(w, ErrBucketNotEmpty, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HeadBucket handles HEAD /{bucket} - HeadBucket.
func (h *Handler) HeadBucket(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	_, err := h.storage.HeadBucket(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ListBuckets handles GET / - ListBuckets.
func (h *Handler) ListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.storage.ListBuckets(r.Context())
	if err != nil {
		WriteError(w, ErrInternalError)
		return
	}

	result := ListAllMyBucketsResult{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		Owner: Owner{
			ID:          "owner-id",
			DisplayName: "owner",
		},
		Buckets: Buckets{
			Bucket: make([]BucketInfo, len(buckets)),
		},
	}

	for i, b := range buckets {
		result.Buckets.Bucket[i] = BucketInfo{
			Name:         b.Name,
			CreationDate: b.CreationDate.Format(time.RFC3339),
		}
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode ListBuckets response")
	}
}

// LocationConstraint is the response for GetBucketLocation.
type LocationConstraint struct {
	XMLName  xml.Name `xml:"LocationConstraint"`
	Xmlns    string   `xml:"xmlns,attr"`
	Location string   `xml:",chardata"`
}

// GetBucketLocation handles GET /{bucket}?location - GetBucketLocation.
func (h *Handler) GetBucketLocation(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Check if bucket exists
	_, err := h.storage.HeadBucket(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	// S3 returns empty LocationConstraint for us-east-1
	// For other regions, it returns the region name
	// JOG always uses us-east-1 as default
	result := LocationConstraint{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Location: "", // Empty for us-east-1
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketLocation response")
	}
}
