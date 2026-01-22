package api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// ObjectLockConfiguration represents the XML structure for object lock configuration.
type ObjectLockConfiguration struct {
	XMLName           xml.Name             `xml:"ObjectLockConfiguration"`
	Xmlns             string               `xml:"xmlns,attr,omitempty"`
	ObjectLockEnabled string               `xml:"ObjectLockEnabled,omitempty"`
	Rule              *ObjectLockRule      `xml:"Rule,omitempty"`
}

// ObjectLockRule represents the object lock rule.
type ObjectLockRule struct {
	DefaultRetention *DefaultRetention `xml:"DefaultRetention,omitempty"`
}

// DefaultRetention represents the default retention settings.
type DefaultRetention struct {
	Mode  string `xml:"Mode,omitempty"`
	Days  *int32 `xml:"Days,omitempty"`
	Years *int32 `xml:"Years,omitempty"`
}

// ObjectLockRetention represents the retention settings for an object.
type ObjectLockRetention struct {
	XMLName         xml.Name   `xml:"Retention"`
	Xmlns           string     `xml:"xmlns,attr,omitempty"`
	Mode            string     `xml:"Mode,omitempty"`
	RetainUntilDate *time.Time `xml:"RetainUntilDate,omitempty"`
}

// ObjectLockLegalHold represents the legal hold settings for an object.
type ObjectLockLegalHold struct {
	XMLName xml.Name `xml:"LegalHold"`
	Xmlns   string   `xml:"xmlns,attr,omitempty"`
	Status  string   `xml:"Status,omitempty"`
}

// PutObjectLockConfiguration handles PUT /{bucket}?object-lock - PutObjectLockConfiguration.
func (h *Handler) PutObjectLockConfiguration(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket)
		return
	}

	var config ObjectLockConfiguration
	if err := xml.Unmarshal(body, &config); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
		return
	}

	// Convert to storage object lock configuration
	storageConfig := &storage.ObjectLockConfiguration{
		ObjectLockEnabled: config.ObjectLockEnabled == "Enabled",
	}

	if config.Rule != nil && config.Rule.DefaultRetention != nil {
		storageConfig.Rule = &storage.ObjectLockRule{
			DefaultRetention: &storage.DefaultRetention{
				Mode:  storage.ObjectLockRetentionMode(config.Rule.DefaultRetention.Mode),
				Days:  config.Rule.DefaultRetention.Days,
				Years: config.Rule.DefaultRetention.Years,
			},
		}
	}

	// Store object lock configuration
	err = h.storage.PutObjectLockConfiguration(r.Context(), bucket, storageConfig)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectLockConfigurationNotFound) {
			WriteErrorWithResource(w, ErrObjectLockConfigurationNotFoundError, "/"+bucket)
			return
		}
		log.Error().Err(err).Msg("Failed to put object lock configuration")
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetObjectLockConfiguration handles GET /{bucket}?object-lock - GetObjectLockConfiguration.
func (h *Handler) GetObjectLockConfiguration(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	config, err := h.storage.GetObjectLockConfiguration(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectLockConfigurationNotFound) {
			WriteErrorWithResource(w, ErrObjectLockConfigurationNotFoundError, "/"+bucket)
			return
		}
		log.Error().Err(err).Msg("Failed to get object lock configuration")
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	// Build response
	response := ObjectLockConfiguration{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
	}

	if config.ObjectLockEnabled {
		response.ObjectLockEnabled = "Enabled"
	}

	if config.Rule != nil && config.Rule.DefaultRetention != nil {
		response.Rule = &ObjectLockRule{
			DefaultRetention: &DefaultRetention{
				Mode:  string(config.Rule.DefaultRetention.Mode),
				Days:  config.Rule.DefaultRetention.Days,
				Years: config.Rule.DefaultRetention.Years,
			},
		}
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetObjectLockConfiguration response")
	}
}

// PutObjectRetention handles PUT /{bucket}/{key}?retention - PutObjectRetention.
func (h *Handler) PutObjectRetention(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket+"/"+key)
		return
	}

	var retention ObjectLockRetention
	if err := xml.Unmarshal(body, &retention); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket+"/"+key)
		return
	}

	// Convert to storage object retention
	storageRetention := &storage.ObjectRetention{
		Mode:            storage.ObjectLockRetentionMode(retention.Mode),
		RetainUntilDate: retention.RetainUntilDate,
	}

	// Store object retention
	err = h.storage.PutObjectRetention(r.Context(), bucket, key, storageRetention)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrInvalidRequestObjectLock) {
			WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket+"/"+key)
			return
		}
		log.Error().Err(err).Msg("Failed to put object retention")
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetObjectRetention handles GET /{bucket}/{key}?retention - GetObjectRetention.
func (h *Handler) GetObjectRetention(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	retention, err := h.storage.GetObjectRetention(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) {
			WriteErrorWithResource(w, ErrNoSuchObjectLockConfiguration, "/"+bucket+"/"+key)
			return
		}
		log.Error().Err(err).Msg("Failed to get object retention")
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
		return
	}

	// Build response
	response := ObjectLockRetention{
		Xmlns:           "http://s3.amazonaws.com/doc/2006-03-01/",
		Mode:            string(retention.Mode),
		RetainUntilDate: retention.RetainUntilDate,
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetObjectRetention response")
	}
}

// PutObjectLegalHold handles PUT /{bucket}/{key}?legal-hold - PutObjectLegalHold.
func (h *Handler) PutObjectLegalHold(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket+"/"+key)
		return
	}

	var legalHold ObjectLockLegalHold
	if err := xml.Unmarshal(body, &legalHold); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket+"/"+key)
		return
	}

	// Convert to storage object legal hold
	storageLegalHold := &storage.ObjectLegalHold{
		Status: storage.ObjectLegalHoldStatus(legalHold.Status),
	}

	// Store object legal hold
	err = h.storage.PutObjectLegalHold(r.Context(), bucket, key, storageLegalHold)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrInvalidRequestObjectLock) {
			WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket+"/"+key)
			return
		}
		log.Error().Err(err).Msg("Failed to put object legal hold")
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetObjectLegalHold handles GET /{bucket}/{key}?legal-hold - GetObjectLegalHold.
func (h *Handler) GetObjectLegalHold(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	legalHold, err := h.storage.GetObjectLegalHold(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		if errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) {
			WriteErrorWithResource(w, ErrNoSuchObjectLockConfiguration, "/"+bucket+"/"+key)
			return
		}
		log.Error().Err(err).Msg("Failed to get object legal hold")
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
		return
	}

	// Build response
	response := ObjectLockLegalHold{
		Xmlns:  "http://s3.amazonaws.com/doc/2006-03-01/",
		Status: string(legalHold.Status),
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetObjectLegalHold response")
	}
}
