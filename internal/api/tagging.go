package api

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

const (
	maxTagsPerResource = 10
	maxTagKeyLength    = 128
	maxTagValueLength  = 256
)

// validateTags validates that tags meet S3 requirements.
func validateTags(tags []storage.Tag) error {
	if len(tags) > maxTagsPerResource {
		return fmt.Errorf("number of tags exceeds the limit of %d", maxTagsPerResource)
	}
	for _, tag := range tags {
		if len(tag.Key) > maxTagKeyLength {
			return fmt.Errorf("tag key exceeds the limit of %d characters", maxTagKeyLength)
		}
		if len(tag.Value) > maxTagValueLength {
			return fmt.Errorf("tag value exceeds the limit of %d characters", maxTagValueLength)
		}
	}
	return nil
}

// Tagging represents the XML structure for tag operations.
type Tagging struct {
	XMLName xml.Name `xml:"Tagging"`
	Xmlns   string   `xml:"xmlns,attr,omitempty"`
	TagSet  TagSet   `xml:"TagSet"`
}

// TagSet contains a list of tags.
type TagSet struct {
	Tags []TagXML `xml:"Tag"`
}

// TagXML represents a single tag in XML format.
type TagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// PutObjectTagging handles PUT /{bucket}/{key}?tagging - PutObjectTagging.
func (h *Handler) PutObjectTagging(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket+"/"+key)
		return
	}

	var tagging Tagging
	if err := xml.Unmarshal(body, &tagging); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket+"/"+key)
		return
	}

	// Convert to storage tags
	tags := make([]storage.Tag, len(tagging.TagSet.Tags))
	for i, t := range tagging.TagSet.Tags {
		tags[i] = storage.Tag{Key: t.Key, Value: t.Value}
	}

	// Validate tags
	if err := validateTags(tags); err != nil {
		WriteErrorWithResource(w, ErrInvalidTag, "/"+bucket+"/"+key)
		return
	}

	// Store tags
	err = h.storage.PutObjectTagging(r.Context(), bucket, key, tags)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetObjectTagging handles GET /{bucket}/{key}?tagging - GetObjectTagging.
func (h *Handler) GetObjectTagging(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	tags, err := h.storage.GetObjectTagging(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
		return
	}

	// Build response
	response := Tagging{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		TagSet: TagSet{
			Tags: make([]TagXML, len(tags)),
		},
	}
	for i, t := range tags {
		response.TagSet.Tags[i] = TagXML{Key: t.Key, Value: t.Value}
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetObjectTagging response")
	}
}

// DeleteObjectTagging handles DELETE /{bucket}/{key}?tagging - DeleteObjectTagging.
func (h *Handler) DeleteObjectTagging(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	err := h.storage.DeleteObjectTagging(r.Context(), bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrObjectNotFound) {
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket+"/"+key)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// PutBucketTagging handles PUT /{bucket}?tagging - PutBucketTagging.
func (h *Handler) PutBucketTagging(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket)
		return
	}

	var tagging Tagging
	if err := xml.Unmarshal(body, &tagging); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
		return
	}

	// Convert to storage tags
	tags := make([]storage.Tag, len(tagging.TagSet.Tags))
	for i, t := range tagging.TagSet.Tags {
		tags[i] = storage.Tag{Key: t.Key, Value: t.Value}
	}

	// Validate tags
	if err := validateTags(tags); err != nil {
		WriteErrorWithResource(w, ErrInvalidTag, "/"+bucket)
		return
	}

	// Store tags
	err = h.storage.PutBucketTagging(r.Context(), bucket, tags)
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

// GetBucketTagging handles GET /{bucket}?tagging - GetBucketTagging.
func (h *Handler) GetBucketTagging(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	tags, err := h.storage.GetBucketTagging(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrNoSuchTagSet) {
			WriteErrorWithResource(w, ErrNoSuchTagSet, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	// Build response
	response := Tagging{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		TagSet: TagSet{
			Tags: make([]TagXML, len(tags)),
		},
	}
	for i, t := range tags {
		response.TagSet.Tags[i] = TagXML{Key: t.Key, Value: t.Value}
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketTagging response")
	}
}

// DeleteBucketTagging handles DELETE /{bucket}?tagging - DeleteBucketTagging.
func (h *Handler) DeleteBucketTagging(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	err := h.storage.DeleteBucketTagging(r.Context(), bucket)
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

// ParseTaggingHeader parses the x-amz-tagging header into tags.
func ParseTaggingHeader(header string) ([]storage.Tag, error) {
	if header == "" {
		return nil, nil
	}

	var tags []storage.Tag
	pairs := strings.Split(header, "&")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, err := url.QueryUnescape(parts[0])
		if err != nil {
			return nil, err
		}
		value, err := url.QueryUnescape(parts[1])
		if err != nil {
			return nil, err
		}
		tags = append(tags, storage.Tag{Key: key, Value: value})
	}

	// Validate tags
	if err := validateTags(tags); err != nil {
		return nil, err
	}

	return tags, nil
}
