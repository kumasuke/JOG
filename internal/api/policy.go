package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// PutBucketPolicy handles PUT /{bucket}?policy - PutBucketPolicy.
func (h *Handler) PutBucketPolicy(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Read policy from request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, ErrInternalError)
		return
	}

	policy := string(body)

	// Validate JSON format
	if !json.Valid(body) {
		WriteError(w, ErrMalformedPolicy)
		return
	}

	err = h.storage.PutBucketPolicy(r.Context(), bucket, policy)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Str("bucket", bucket).Msg("Failed to put bucket policy")
		WriteError(w, ErrInternalError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetBucketPolicy handles GET /{bucket}?policy - GetBucketPolicy.
func (h *Handler) GetBucketPolicy(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	policy, err := h.storage.GetBucketPolicy(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrNoSuchBucketPolicy) {
			WriteErrorWithResource(w, ErrNoSuchBucketPolicy, "/"+bucket)
			return
		}
		log.Error().Err(err).Str("bucket", bucket).Msg("Failed to get bucket policy")
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(policy)); err != nil {
		log.Error().Err(err).Msg("Failed to write GetBucketPolicy response")
	}
}

// DeleteBucketPolicy handles DELETE /{bucket}?policy - DeleteBucketPolicy.
func (h *Handler) DeleteBucketPolicy(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	err := h.storage.DeleteBucketPolicy(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Str("bucket", bucket).Msg("Failed to delete bucket policy")
		WriteError(w, ErrInternalError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
