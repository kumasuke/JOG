package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// objectLockWriteIntent captures the Object Lock retention / legal hold that a
// write request (PutObject / CopyObject / multipart) wants to apply to the new
// version. It is the resolved result of the x-amz-object-lock-* headers and/or
// the bucket's DefaultRetention, ready to be written to the per-version lock
// tables (issue #40, building on #39's per-version schema).
//
// A nil *objectLockWriteIntent (or one with both fields nil) means "apply
// nothing": the new version is written without retention or legal hold.
type objectLockWriteIntent struct {
	// retention is non-nil when a retention mode + RetainUntilDate must be
	// applied to the new version.
	retention *storage.ObjectRetention
	// legalHold is non-nil when a legal hold status must be applied. Note that
	// only ON is meaningful on a fresh version (OFF is the implicit default),
	// but S3 accepts and round-trips an explicit OFF, so we honour both.
	legalHold *storage.ObjectLegalHold
}

// isEmpty reports whether the intent has nothing to apply.
func (i *objectLockWriteIntent) isEmpty() bool {
	return i == nil || (i.retention == nil && i.legalHold == nil)
}

// resolveObjectLockOnWrite parses the x-amz-object-lock-* request headers and,
// falling back to the bucket's DefaultRetention, produces the lock intent to
// apply to the version this write creates. It enforces the S3 contract:
//
//   - x-amz-object-lock-mode and x-amz-object-lock-retain-until-date MUST be
//     supplied together; only one => InvalidArgument.
//   - mode must be GOVERNANCE or COMPLIANCE; date must be RFC3339 in the
//     future; legal-hold must be ON or OFF — otherwise InvalidArgument.
//   - any x-amz-object-lock-* header on a bucket that is NOT Object-Lock-
//     enabled => InvalidRequest.
//   - header values take precedence over the bucket's DefaultRetention.
//   - when no retention header is given and the bucket has a DefaultRetention
//     rule, the RetainUntilDate is computed at write time from Days/Years and
//     applied to the new version. A later config change does NOT retroactively
//     affect this version because the date is fixed here.
//
// It returns (intent, nil) on success — intent may be empty — or (nil, s3Err)
// when validation fails.
func (h *Handler) resolveObjectLockOnWrite(ctx context.Context, bucket string, r *http.Request) (*objectLockWriteIntent, *S3Error) {
	modeHeader := r.Header.Get("x-amz-object-lock-mode")
	dateHeader := r.Header.Get("x-amz-object-lock-retain-until-date")
	legalHoldHeader := r.Header.Get("x-amz-object-lock-legal-hold")

	hasAnyHeader := modeHeader != "" || dateHeader != "" || legalHoldHeader != ""

	// Determine whether the bucket is Object-Lock-enabled. We fetch the lock
	// configuration once and reuse it for both the header gate and the default
	// retention fallback.
	cfg, cfgErr := h.storage.GetObjectLockConfiguration(ctx, bucket)
	lockEnabled := false
	if cfgErr == nil && cfg != nil && cfg.ObjectLockEnabled {
		lockEnabled = true
	} else if cfgErr != nil &&
		!errors.Is(cfgErr, storage.ErrBucketNotFound) &&
		!errors.Is(cfgErr, storage.ErrNoSuchObjectLockConfiguration) &&
		!errors.Is(cfgErr, storage.ErrObjectLockConfigurationNotFound) {
		// An unexpected backend failure must not silently drop a requested
		// lock. Fail-closed only when the caller actually asked for a lock;
		// otherwise let the (unlocked) write proceed.
		if hasAnyHeader {
			log.Error().Err(cfgErr).Str("bucket", bucket).Msg("Failed to read object lock configuration during write")
			return nil, ErrInternalError
		}
		return &objectLockWriteIntent{}, nil
	}

	// Headers on a non Object-Lock-enabled bucket are an InvalidRequest.
	if hasAnyHeader && !lockEnabled {
		return nil, ErrInvalidRequest
	}

	intent := &objectLockWriteIntent{}

	// --- Retention from headers (takes precedence over DefaultRetention) ---
	if modeHeader != "" || dateHeader != "" {
		// Both must be present together.
		if modeHeader == "" || dateHeader == "" {
			return nil, ErrInvalidArgument
		}
		mode, ok := normalizeRetentionMode(modeHeader)
		if !ok {
			return nil, ErrInvalidArgument
		}
		retainUntil, err := parseRetainUntilDate(dateHeader)
		if err != nil {
			return nil, ErrInvalidArgument
		}
		// The retain-until-date must be in the future.
		if !retainUntil.After(time.Now()) {
			return nil, ErrInvalidArgument
		}
		intent.retention = &storage.ObjectRetention{
			Mode:            mode,
			RetainUntilDate: &retainUntil,
		}
	}

	// --- Legal hold from header ---
	if legalHoldHeader != "" {
		status, ok := normalizeLegalHoldStatus(legalHoldHeader)
		if !ok {
			return nil, ErrInvalidArgument
		}
		intent.legalHold = &storage.ObjectLegalHold{Status: status}
	}

	// --- Bucket DefaultRetention fallback (only when no retention header) ---
	if intent.retention == nil && lockEnabled && cfg != nil &&
		cfg.Rule != nil && cfg.Rule.DefaultRetention != nil {
		retention, s3Err := defaultRetentionToObjectRetention(cfg.Rule.DefaultRetention, time.Now())
		if s3Err != nil {
			return nil, s3Err
		}
		if retention != nil {
			intent.retention = retention
		}
	}

	return intent, nil
}

// applyObjectLockOnWrite persists the resolved lock intent against the exact
// version_id this write produced, reusing the #39 per-version lock methods.
// versionID is "" for the null version on a non-versioning bucket. It is a
// no-op when the intent is empty.
//
// By this point the headers / default have already been validated and the
// bucket is known to be Object-Lock-enabled, so a failure here is unexpected
// and surfaced to the caller (the version is already written; the caller
// decides how to report it).
func (h *Handler) applyObjectLockOnWrite(ctx context.Context, bucket, key, versionID string, intent *objectLockWriteIntent) error {
	if intent.isEmpty() {
		return nil
	}
	if intent.retention != nil {
		if err := h.storage.PutObjectRetention(ctx, bucket, key, versionID, intent.retention); err != nil {
			return err
		}
	}
	if intent.legalHold != nil {
		if err := h.storage.PutObjectLegalHold(ctx, bucket, key, versionID, intent.legalHold); err != nil {
			return err
		}
	}
	return nil
}

// multipartLockIntent reconstructs the Object Lock intent that was captured on
// the upload record at CreateMultipartUpload (issue #40). A missing upload or a
// read error yields an empty intent — completion has already validated the
// upload's existence, so a transient read failure here must not block the
// completion of an otherwise-valid multipart upload.
func (h *Handler) multipartLockIntent(ctx context.Context, uploadID string) *objectLockWriteIntent {
	intent := &objectLockWriteIntent{}
	upload, err := h.storage.GetMultipartUpload(ctx, uploadID)
	if err != nil || upload == nil {
		if err != nil {
			log.Error().Err(err).Str("uploadId", uploadID).Msg("Failed to read multipart upload for object lock intent")
		}
		return intent
	}
	if upload.ObjectLockMode != "" && upload.ObjectLockRetainUntilDate != nil {
		intent.retention = &storage.ObjectRetention{
			Mode:            upload.ObjectLockMode,
			RetainUntilDate: upload.ObjectLockRetainUntilDate,
		}
	}
	if upload.ObjectLockLegalHold != "" {
		intent.legalHold = &storage.ObjectLegalHold{Status: upload.ObjectLockLegalHold}
	}
	return intent
}

// normalizeRetentionMode validates and canonicalises an Object Lock retention
// mode supplied via header. AWS sends the canonical upper-case GOVERNANCE /
// COMPLIANCE; we accept those exactly.
func normalizeRetentionMode(v string) (storage.ObjectLockRetentionMode, bool) {
	switch v {
	case string(storage.ObjectLockRetentionModeGovernance):
		return storage.ObjectLockRetentionModeGovernance, true
	case string(storage.ObjectLockRetentionModeCompliance):
		return storage.ObjectLockRetentionModeCompliance, true
	}
	return "", false
}

// normalizeLegalHoldStatus validates an Object Lock legal hold status supplied
// via header. AWS sends ON / OFF.
func normalizeLegalHoldStatus(v string) (storage.ObjectLegalHoldStatus, bool) {
	switch v {
	case string(storage.ObjectLegalHoldStatusOn):
		return storage.ObjectLegalHoldStatusOn, true
	case string(storage.ObjectLegalHoldStatusOff):
		return storage.ObjectLegalHoldStatusOff, true
	}
	return "", false
}

// parseRetainUntilDate parses the x-amz-object-lock-retain-until-date header.
// S3 sends an RFC3339 / ISO-8601 timestamp; the AWS SDK encodes it as RFC3339
// with nanoseconds. Accept both RFC3339 and RFC3339Nano.
func parseRetainUntilDate(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// defaultRetentionToObjectRetention computes a concrete RetainUntilDate from a
// bucket DefaultRetention rule, anchored at `now`. The date is fixed at write
// time so a later configuration change does not retroactively alter existing
// versions. Returns (nil, nil) when the rule has no Days/Years to apply.
func defaultRetentionToObjectRetention(dr *storage.DefaultRetention, now time.Time) (*storage.ObjectRetention, *S3Error) {
	mode, ok := normalizeRetentionMode(string(dr.Mode))
	if !ok {
		// A malformed stored rule (should never happen — PutObjectLockConfiguration
		// validates) must not silently write garbage; ignore the default.
		return nil, nil
	}

	var retainUntil time.Time
	switch {
	case dr.Days != nil && *dr.Days > 0:
		retainUntil = now.AddDate(0, 0, int(*dr.Days))
	case dr.Years != nil && *dr.Years > 0:
		retainUntil = now.AddDate(int(*dr.Years), 0, 0)
	default:
		// No usable duration on the rule.
		return nil, nil
	}

	retainUntil = retainUntil.UTC()
	return &storage.ObjectRetention{
		Mode:            mode,
		RetainUntilDate: &retainUntil,
	}, nil
}
