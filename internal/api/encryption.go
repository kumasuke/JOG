package api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// ServerSideEncryptionConfiguration represents the XML structure for SSE configuration.
type ServerSideEncryptionConfiguration struct {
	XMLName xml.Name                      `xml:"ServerSideEncryptionConfiguration"`
	Xmlns   string                        `xml:"xmlns,attr,omitempty"`
	Rules   []ServerSideEncryptionRule    `xml:"Rule"`
}

// ServerSideEncryptionRule represents a single SSE rule.
type ServerSideEncryptionRule struct {
	ApplyServerSideEncryptionByDefault *ServerSideEncryptionByDefault `xml:"ApplyServerSideEncryptionByDefault,omitempty"`
	BucketKeyEnabled                   *bool                          `xml:"BucketKeyEnabled,omitempty"`
}

// ServerSideEncryptionByDefault represents the default SSE configuration.
type ServerSideEncryptionByDefault struct {
	SSEAlgorithm   string  `xml:"SSEAlgorithm"`
	KMSMasterKeyID *string `xml:"KMSMasterKeyID,omitempty"`
}

// PutBucketEncryption handles PUT /{bucket}?encryption - PutBucketEncryption.
func (h *Handler) PutBucketEncryption(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket)
		return
	}

	var encConfig ServerSideEncryptionConfiguration
	if err := xml.Unmarshal(body, &encConfig); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
		return
	}

	// Convert to storage encryption configuration
	storageConfig := &storage.ServerSideEncryptionConfiguration{
		Rules: make([]storage.ServerSideEncryptionRule, len(encConfig.Rules)),
	}
	for i, rule := range encConfig.Rules {
		storageRule := storage.ServerSideEncryptionRule{}
		if rule.ApplyServerSideEncryptionByDefault != nil {
			storageRule.ApplyServerSideEncryptionByDefault = &storage.ServerSideEncryptionByDefault{
				SSEAlgorithm: storage.SSEAlgorithm(rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm),
			}
			if rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID != nil {
				storageRule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID = *rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID
			}
		}
		if rule.BucketKeyEnabled != nil {
			storageRule.BucketKeyEnabled = *rule.BucketKeyEnabled
		}
		storageConfig.Rules[i] = storageRule
	}

	// Store encryption configuration
	err = h.storage.PutBucketEncryption(r.Context(), bucket, storageConfig)
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

// GetBucketEncryption handles GET /{bucket}?encryption - GetBucketEncryption.
func (h *Handler) GetBucketEncryption(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	config, err := h.storage.GetBucketEncryption(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrNoSuchEncryptionConfiguration) {
			WriteErrorWithResource(w, ErrServerSideEncryptionConfigurationNotFoundError, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	// Build response
	response := ServerSideEncryptionConfiguration{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		Rules: make([]ServerSideEncryptionRule, len(config.Rules)),
	}
	for i, rule := range config.Rules {
		responseRule := ServerSideEncryptionRule{}
		if rule.ApplyServerSideEncryptionByDefault != nil {
			responseRule.ApplyServerSideEncryptionByDefault = &ServerSideEncryptionByDefault{
				SSEAlgorithm: string(rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm),
			}
			if rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID != "" {
				kmsKeyID := rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID
				responseRule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID = &kmsKeyID
			}
		}
		if rule.BucketKeyEnabled {
			bucketKeyEnabled := rule.BucketKeyEnabled
			responseRule.BucketKeyEnabled = &bucketKeyEnabled
		}
		response.Rules[i] = responseRule
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketEncryption response")
	}
}

// DeleteBucketEncryption handles DELETE /{bucket}?encryption - DeleteBucketEncryption.
func (h *Handler) DeleteBucketEncryption(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	err := h.storage.DeleteBucketEncryption(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
