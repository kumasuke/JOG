package api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// BucketLifecycleConfiguration represents the XML structure for lifecycle configuration.
type BucketLifecycleConfiguration struct {
	XMLName xml.Name        `xml:"LifecycleConfiguration"`
	Xmlns   string          `xml:"xmlns,attr,omitempty"`
	Rules   []LifecycleRule `xml:"Rule"`
}

// LifecycleRule represents a single lifecycle rule.
type LifecycleRule struct {
	ID                             *string                         `xml:"ID,omitempty"`
	Status                         string                          `xml:"Status"`
	Filter                         *LifecycleRuleFilter            `xml:"Filter,omitempty"`
	Expiration                     *LifecycleExpiration            `xml:"Expiration,omitempty"`
	Transitions                    []LifecycleTransition           `xml:"Transition,omitempty"`
	NoncurrentVersionExpiration    *NoncurrentVersionExpiration    `xml:"NoncurrentVersionExpiration,omitempty"`
	NoncurrentVersionTransitions   []NoncurrentVersionTransition   `xml:"NoncurrentVersionTransition,omitempty"`
	AbortIncompleteMultipartUpload *AbortIncompleteMultipartUpload `xml:"AbortIncompleteMultipartUpload,omitempty"`
}

// LifecycleRuleFilter represents the filter for a lifecycle rule.
type LifecycleRuleFilter struct {
	Prefix                *string       `xml:"Prefix,omitempty"`
	Tag                   *LifecycleTag `xml:"Tag,omitempty"`
	ObjectSizeGreaterThan *int64        `xml:"ObjectSizeGreaterThan,omitempty"`
	ObjectSizeLessThan    *int64        `xml:"ObjectSizeLessThan,omitempty"`
}

// LifecycleTag represents a tag in lifecycle filter.
type LifecycleTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// LifecycleExpiration represents expiration settings.
type LifecycleExpiration struct {
	Days                      *int32  `xml:"Days,omitempty"`
	Date                      *string `xml:"Date,omitempty"`
	ExpiredObjectDeleteMarker *bool   `xml:"ExpiredObjectDeleteMarker,omitempty"`
}

// LifecycleTransition represents a transition to a different storage class.
type LifecycleTransition struct {
	Days         *int32  `xml:"Days,omitempty"`
	Date         *string `xml:"Date,omitempty"`
	StorageClass string  `xml:"StorageClass"`
}

// NoncurrentVersionExpiration represents expiration for noncurrent versions.
type NoncurrentVersionExpiration struct {
	NoncurrentDays          *int32 `xml:"NoncurrentDays,omitempty"`
	NewerNoncurrentVersions *int32 `xml:"NewerNoncurrentVersions,omitempty"`
}

// NoncurrentVersionTransition represents transition for noncurrent versions.
type NoncurrentVersionTransition struct {
	NoncurrentDays          *int32 `xml:"NoncurrentDays,omitempty"`
	StorageClass            string `xml:"StorageClass"`
	NewerNoncurrentVersions *int32 `xml:"NewerNoncurrentVersions,omitempty"`
}

// AbortIncompleteMultipartUpload represents settings for aborting incomplete multipart uploads.
type AbortIncompleteMultipartUpload struct {
	DaysAfterInitiation *int32 `xml:"DaysAfterInitiation,omitempty"`
}

// PutBucketLifecycleConfiguration handles PUT /{bucket}?lifecycle - PutBucketLifecycleConfiguration.
func (h *Handler) PutBucketLifecycleConfiguration(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket)
		return
	}

	var lifecycleConfig BucketLifecycleConfiguration
	if err := xml.Unmarshal(body, &lifecycleConfig); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
		return
	}

	// Convert to storage lifecycle configuration
	storageConfig := &storage.LifecycleConfiguration{
		Rules: make([]storage.LifecycleRule, len(lifecycleConfig.Rules)),
	}
	for i, rule := range lifecycleConfig.Rules {
		storageRule := storage.LifecycleRule{
			Status: rule.Status,
		}
		if rule.ID != nil {
			storageRule.ID = *rule.ID
		}
		if rule.Filter != nil {
			storageRule.Filter = &storage.LifecycleRuleFilter{}
			if rule.Filter.Prefix != nil {
				storageRule.Filter.Prefix = *rule.Filter.Prefix
			}
			if rule.Filter.Tag != nil {
				storageRule.Filter.Tag = &storage.Tag{
					Key:   rule.Filter.Tag.Key,
					Value: rule.Filter.Tag.Value,
				}
			}
			storageRule.Filter.ObjectSizeGreaterThan = rule.Filter.ObjectSizeGreaterThan
			storageRule.Filter.ObjectSizeLessThan = rule.Filter.ObjectSizeLessThan
		}
		if rule.Expiration != nil {
			storageRule.Expiration = &storage.LifecycleExpiration{
				Days:                      rule.Expiration.Days,
				Date:                      rule.Expiration.Date,
				ExpiredObjectDeleteMarker: rule.Expiration.ExpiredObjectDeleteMarker,
			}
		}
		for _, transition := range rule.Transitions {
			storageRule.Transitions = append(storageRule.Transitions, storage.LifecycleTransition{
				Days:         transition.Days,
				Date:         transition.Date,
				StorageClass: transition.StorageClass,
			})
		}
		if rule.NoncurrentVersionExpiration != nil {
			storageRule.NoncurrentVersionExpiration = &storage.NoncurrentVersionExpiration{
				NoncurrentDays:          rule.NoncurrentVersionExpiration.NoncurrentDays,
				NewerNoncurrentVersions: rule.NoncurrentVersionExpiration.NewerNoncurrentVersions,
			}
		}
		for _, nvt := range rule.NoncurrentVersionTransitions {
			storageRule.NoncurrentVersionTransitions = append(storageRule.NoncurrentVersionTransitions, storage.NoncurrentVersionTransition{
				NoncurrentDays:          nvt.NoncurrentDays,
				StorageClass:            nvt.StorageClass,
				NewerNoncurrentVersions: nvt.NewerNoncurrentVersions,
			})
		}
		if rule.AbortIncompleteMultipartUpload != nil {
			storageRule.AbortIncompleteMultipartUpload = &storage.AbortIncompleteMultipartUpload{
				DaysAfterInitiation: rule.AbortIncompleteMultipartUpload.DaysAfterInitiation,
			}
		}
		storageConfig.Rules[i] = storageRule
	}

	// Store lifecycle configuration
	err = h.storage.PutBucketLifecycleConfiguration(r.Context(), bucket, storageConfig)
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

// GetBucketLifecycleConfiguration handles GET /{bucket}?lifecycle - GetBucketLifecycleConfiguration.
func (h *Handler) GetBucketLifecycleConfiguration(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	config, err := h.storage.GetBucketLifecycleConfiguration(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrNoSuchLifecycleConfiguration) {
			WriteErrorWithResource(w, ErrNoSuchLifecycleConfiguration, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	// Build response
	response := BucketLifecycleConfiguration{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		Rules: make([]LifecycleRule, len(config.Rules)),
	}
	for i, rule := range config.Rules {
		responseRule := LifecycleRule{
			Status: rule.Status,
		}
		if rule.ID != "" {
			id := rule.ID
			responseRule.ID = &id
		}
		if rule.Filter != nil {
			responseRule.Filter = &LifecycleRuleFilter{}
			if rule.Filter.Prefix != "" {
				prefix := rule.Filter.Prefix
				responseRule.Filter.Prefix = &prefix
			}
			if rule.Filter.Tag != nil {
				responseRule.Filter.Tag = &LifecycleTag{
					Key:   rule.Filter.Tag.Key,
					Value: rule.Filter.Tag.Value,
				}
			}
			responseRule.Filter.ObjectSizeGreaterThan = rule.Filter.ObjectSizeGreaterThan
			responseRule.Filter.ObjectSizeLessThan = rule.Filter.ObjectSizeLessThan
		}
		if rule.Expiration != nil {
			responseRule.Expiration = &LifecycleExpiration{
				Days:                      rule.Expiration.Days,
				Date:                      rule.Expiration.Date,
				ExpiredObjectDeleteMarker: rule.Expiration.ExpiredObjectDeleteMarker,
			}
		}
		for _, transition := range rule.Transitions {
			responseRule.Transitions = append(responseRule.Transitions, LifecycleTransition{
				Days:         transition.Days,
				Date:         transition.Date,
				StorageClass: transition.StorageClass,
			})
		}
		if rule.NoncurrentVersionExpiration != nil {
			responseRule.NoncurrentVersionExpiration = &NoncurrentVersionExpiration{
				NoncurrentDays:          rule.NoncurrentVersionExpiration.NoncurrentDays,
				NewerNoncurrentVersions: rule.NoncurrentVersionExpiration.NewerNoncurrentVersions,
			}
		}
		for _, nvt := range rule.NoncurrentVersionTransitions {
			responseRule.NoncurrentVersionTransitions = append(responseRule.NoncurrentVersionTransitions, NoncurrentVersionTransition{
				NoncurrentDays:          nvt.NoncurrentDays,
				StorageClass:            nvt.StorageClass,
				NewerNoncurrentVersions: nvt.NewerNoncurrentVersions,
			})
		}
		if rule.AbortIncompleteMultipartUpload != nil {
			responseRule.AbortIncompleteMultipartUpload = &AbortIncompleteMultipartUpload{
				DaysAfterInitiation: rule.AbortIncompleteMultipartUpload.DaysAfterInitiation,
			}
		}
		response.Rules[i] = responseRule
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketLifecycleConfiguration response")
	}
}

// DeleteBucketLifecycle handles DELETE /{bucket}?lifecycle - DeleteBucketLifecycle.
func (h *Handler) DeleteBucketLifecycle(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	err := h.storage.DeleteBucketLifecycle(r.Context(), bucket)
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
