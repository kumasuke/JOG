package api

import (
	"context"
	"errors"
	"fmt"
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
// A nil *objectLockWriteIntent (or one with all lock fields nil) means "apply
// nothing": the new version is written without retention or legal hold.
type objectLockWriteIntent struct {
	// retention is non-nil when a retention mode + RetainUntilDate must be
	// applied to the new version.
	retention *storage.ObjectRetention
	// legalHold is non-nil when a legal hold status must be applied. Note that
	// only ON is meaningful on a fresh version (OFF is the implicit default),
	// but S3 accepts and round-trips an explicit OFF, so we honour both.
	legalHold *storage.ObjectLegalHold
	// pendingDefaultRetention holds the bucket DefaultRetention rule when the
	// RetainUntilDate must be computed at CompleteMultipartUpload (not at
	// CreateMultipartUpload). Nil for Put/Copy where the date is fixed at write.
	pendingDefaultRetention *storage.DefaultRetention
}

// isEmpty reports whether the intent has nothing to apply.
func (i *objectLockWriteIntent) isEmpty() bool {
	return i == nil || (i.retention == nil && i.legalHold == nil && i.pendingDefaultRetention == nil)
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
// deferDefaultRetention, when true, stores the bucket DefaultRetention rule on
// the intent without computing RetainUntilDate (multipart create path). The
// date is materialized at CompleteMultipartUpload from time.Now().
//
// It returns (intent, nil) on success — intent may be empty — or (nil, s3Err)
// when validation fails.
func (h *Handler) resolveObjectLockOnWrite(ctx context.Context, bucket string, r *http.Request, deferDefaultRetention bool) (*objectLockWriteIntent, *S3Error) {
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
		// An unexpected backend failure must not silently allow an unprotected
		// write: DefaultRetention cannot be read, so fail-closed always.
		log.Error().Err(cfgErr).Str("bucket", bucket).Msg("Failed to read object lock configuration during write")
		return nil, ErrInternalError
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
		dr := cfg.Rule.DefaultRetention
		if s3Err := validateDefaultRetention(dr); s3Err != nil {
			return nil, s3Err
		}
		if deferDefaultRetention {
			intent.pendingDefaultRetention = dr
		} else {
			retention, s3Err := defaultRetentionToObjectRetention(dr, time.Now())
			if s3Err != nil {
				return nil, s3Err
			}
			intent.retention = retention
		}
	}

	return intent, nil
}

// validateObjectLockIntentVersioning rejects a non-empty lock intent when the
// bucket does not have versioning Enabled. Object Lock on write applies only to
// versioned objects (#39/#40).
func (h *Handler) validateObjectLockIntentVersioning(ctx context.Context, bucket string, intent *objectLockWriteIntent) *S3Error {
	if intent.isEmpty() {
		return nil
	}
	status, err := h.storage.GetBucketVersioning(ctx, bucket)
	if err != nil {
		log.Error().Err(err).Str("bucket", bucket).Msg("Failed to read bucket versioning during object lock write")
		return ErrInternalError
	}
	if status != storage.VersioningStatusEnabled {
		return ErrInvalidBucketState
	}
	return nil
}

// applyObjectLockOnWrite atomically persists retention and legal hold against
// the exact version_id this write produced. versionID must be non-empty
// (versioning Enabled is enforced before write). On failure the new version is
// rolled back so an unprotected version cannot remain.
func (h *Handler) applyObjectLockOnWrite(ctx context.Context, bucket, key, versionID string, intent *objectLockWriteIntent) error {
	if intent.isEmpty() {
		return nil
	}
	err := h.storage.ApplyObjectLockOnVersion(ctx, bucket, key, versionID, intent.retention, intent.legalHold)
	if err != nil {
		if versionID != "" {
			if rbErr := h.storage.RollbackNewObjectVersion(ctx, bucket, key, versionID); rbErr != nil {
				log.Error().Err(rbErr).Str("bucket", bucket).Str("key", key).Str("versionId", versionID).
					Msg("Failed to rollback object version after object lock apply failure")
			}
		}
		return err
	}
	return nil
}

// multipartLockIntent reconstructs the Object Lock intent captured on the upload
// record at CreateMultipartUpload (issue #40). DefaultRetention-derived rules
// are materialized at complete time from time.Now(); explicit retain-until-date
// headers keep their absolute timestamp. A read error fails closed — the
// caller must not proceed to CompleteMultipartUpload.
func (h *Handler) multipartLockIntent(ctx context.Context, uploadID string) (*objectLockWriteIntent, error) {
	upload, err := h.storage.GetMultipartUpload(ctx, uploadID)
	if err != nil {
		log.Error().Err(err).Str("uploadId", uploadID).Msg("Failed to read multipart upload for object lock intent")
		return nil, err
	}
	if upload == nil {
		log.Error().Str("uploadId", uploadID).Msg("Multipart upload missing during object lock intent read")
		return nil, storage.ErrUploadNotFound
	}

	intent := &objectLockWriteIntent{}
	switch {
	case upload.ObjectLockMode != "" && upload.ObjectLockRetainUntilDate != nil:
		intent.retention = &storage.ObjectRetention{
			Mode:            upload.ObjectLockMode,
			RetainUntilDate: upload.ObjectLockRetainUntilDate,
		}
	case upload.ObjectLockMode != "" && (upload.ObjectLockDefaultDays != nil || upload.ObjectLockDefaultYears != nil):
		dr := &storage.DefaultRetention{Mode: upload.ObjectLockMode}
		if upload.ObjectLockDefaultDays != nil {
			dr.Days = upload.ObjectLockDefaultDays
		}
		if upload.ObjectLockDefaultYears != nil {
			dr.Years = upload.ObjectLockDefaultYears
		}
		retention, s3Err := defaultRetentionToObjectRetention(dr, time.Now())
		if s3Err != nil {
			return nil, fmt.Errorf("materialize default retention: %s", s3Err.Code)
		}
		intent.retention = retention
	}
	if upload.ObjectLockLegalHold != "" {
		intent.legalHold = &storage.ObjectLegalHold{Status: upload.ObjectLockLegalHold}
	}
	return intent, nil
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

// validateDefaultRetention checks a stored DefaultRetention rule. Malformed
// mode/duration must not be silently ignored.
func validateDefaultRetention(dr *storage.DefaultRetention) *S3Error {
	if _, ok := normalizeRetentionMode(string(dr.Mode)); !ok {
		return ErrInternalError
	}
	daysSet := dr.Days != nil
	yearsSet := dr.Years != nil
	if daysSet == yearsSet {
		return ErrInternalError
	}
	if daysSet {
		if *dr.Days <= 0 {
			return ErrInternalError
		}
	} else if *dr.Years <= 0 {
		return ErrInternalError
	}
	return nil
}

// defaultRetentionToObjectRetention computes a concrete RetainUntilDate from a
// bucket DefaultRetention rule, anchored at `now`. The date is fixed at write
// time so a later configuration change does not retroactively alter existing
// versions.
func defaultRetentionToObjectRetention(dr *storage.DefaultRetention, now time.Time) (*storage.ObjectRetention, *S3Error) {
	if s3Err := validateDefaultRetention(dr); s3Err != nil {
		return nil, s3Err
	}
	mode, _ := normalizeRetentionMode(string(dr.Mode))

	var retainUntil time.Time
	if dr.Days != nil && *dr.Days > 0 {
		retainUntil = now.AddDate(0, 0, int(*dr.Days))
	} else {
		retainUntil = now.AddDate(int(*dr.Years), 0, 0)
	}

	retainUntil = retainUntil.UTC()
	return &storage.ObjectRetention{
		Mode:            mode,
		RetainUntilDate: &retainUntil,
	}, nil
}
