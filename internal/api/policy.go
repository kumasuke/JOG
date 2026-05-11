package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// SECURITY (CR-7): Bucket policies are stored and returned verbatim, but
// JOG DOES NOT EVALUATE THEM for any access-control decision. The only
// authentication/authorisation in effect is AWS Signature V4 against the
// single root credential pair configured via JOG_AUTH_ACCESS_KEY /
// JOG_AUTH_SECRET_KEY. In particular:
//
//   - "Allow" statements grant nothing — a policy cannot expose an object
//     to anonymous callers, nor can it grant cross-account access (JOG
//     has no concept of additional principals).
//   - "Deny" statements block nothing — a request authenticated as the
//     root credential is permitted regardless of what the bucket policy
//     says (e.g. NotPrincipal, IpAddress, aws:SecureTransport conditions
//     are ignored).
//   - Public-read / public-write policies do NOT make objects public.
//   - Conditions on aws:SecureTransport, s3:x-amz-server-side-encryption,
//     aws:MultiFactorAuthAge, etc. are NOT enforced.
//
// Treat these handlers as a *compatibility shim* so AWS SDK calls that
// expect PutBucketPolicy / GetBucketPolicy to succeed do not crash. Do
// not rely on them for access control. See docs/S3_API_CHECKLIST.md for
// the formal list of compatibility limitations.

// Maximum bucket policy size (20KB, same as AWS S3)
const maxPolicySize = 20 * 1024

// PutBucketPolicy handles PUT /{bucket}?policy - PutBucketPolicy.
//
// NOTE: the stored policy is *not* enforced. See the package-level
// CR-7 comment above for details.
func (h *Handler) PutBucketPolicy(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Read policy from request body with size limit
	limitedReader := io.LimitReader(r.Body, maxPolicySize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		WriteError(w, ErrInternalError)
		return
	}

	// Check if policy exceeds size limit
	if len(body) > maxPolicySize {
		WriteError(w, ErrMalformedPolicy)
		return
	}

	policy := string(body)

	// Validate JSON format
	if !json.Valid(body) {
		WriteError(w, ErrMalformedPolicy)
		return
	}

	// Validate policy structure (must have at least Version and Statement)
	var policyDoc map[string]interface{}
	if err := json.Unmarshal(body, &policyDoc); err != nil {
		WriteError(w, ErrMalformedPolicy)
		return
	}

	// Check for required fields
	if _, hasStatement := policyDoc["Statement"]; !hasStatement {
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
