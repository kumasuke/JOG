package api

import (
	"context"
	"errors"
	"time"

	"github.com/kumasuke/jog/internal/storage"
)

// evaluateObjectLock decides whether a destructive operation against
// (bucket, key) is allowed under the bucket's Object Lock policy (CR-5).
//
// The S3 contract:
//   - If the bucket has no Object Lock configuration, every operation is
//     allowed (most buckets).
//   - If the object has legal hold ON, the operation is rejected
//     regardless of mode or bypass header. Legal hold has no expiry.
//   - If the object is under COMPLIANCE retention with RetainUntilDate
//     in the future, the operation is rejected (bypass is ignored —
//     COMPLIANCE is immutable until the date passes).
//   - If the object is under GOVERNANCE retention with RetainUntilDate
//     in the future, the operation is rejected unless the caller set
//     x-amz-bypass-governance-retention=true.
//   - If RetainUntilDate has already passed, retention no longer
//     applies — the operation is allowed.
//
// Storage-layer "not configured" / "not found" errors are treated as
// "no protection" rather than propagated, so a delete on an unlocked
// object continues to succeed.
//
// M-2: any other (unexpected) error from GetObjectLockConfiguration is
// fail-closed: returning AccessDenied. The previous `err != nil → allow`
// behaviour was a fail-open silent bypass — a transient DB outage would
// have let destructive operations through against locked objects.
func (h *Handler) evaluateObjectLock(ctx context.Context, bucket, key string, bypassGovernance bool) *S3Error {
	cfg, err := h.storage.GetObjectLockConfiguration(ctx, bucket)
	if err != nil {
		// Treat "no configuration on this bucket" as allow; everything
		// else (DB I/O failure, schema corruption, …) as deny.
		if errors.Is(err, storage.ErrBucketNotFound) ||
			errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) ||
			errors.Is(err, storage.ErrObjectLockConfigurationNotFound) {
			return nil
		}
		return ErrAccessDenied
	}
	if cfg == nil || !cfg.ObjectLockEnabled {
		return nil
	}

	legalHold, err := h.storage.GetObjectLegalHold(ctx, bucket, key)
	if err == nil && legalHold != nil && legalHold.Status == storage.ObjectLegalHoldStatusOn {
		return ErrAccessDenied
	} else if err != nil && !errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) && !errors.Is(err, storage.ErrObjectNotFound) {
		return ErrAccessDenied
	}

	retention, err := h.storage.GetObjectRetention(ctx, bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) || errors.Is(err, storage.ErrObjectNotFound) {
			return nil
		}
		return ErrAccessDenied
	}
	if retention == nil || retention.RetainUntilDate == nil {
		return nil
	}
	if !retention.RetainUntilDate.After(time.Now()) {
		return nil
	}
	switch retention.Mode {
	case storage.ObjectLockRetentionModeCompliance:
		return ErrAccessDenied
	case storage.ObjectLockRetentionModeGovernance:
		if !bypassGovernance {
			return ErrAccessDenied
		}
	}
	return nil
}

// parseBypassGovernanceHeader extracts the x-amz-bypass-governance-retention
// header. AWS treats only the literal "true" (case-insensitive) as opt-in.
func parseBypassGovernanceHeader(v string) bool {
	switch v {
	case "true", "True", "TRUE":
		return true
	}
	return false
}

// evaluateRetentionChange decides whether replacing the existing Object
// Retention with `next` is permitted by the AWS Object Lock contract (C-1).
//
// The rules, summarised:
//
//   - No active prior retention (none configured, expired, or no
//     RetainUntilDate set): any new retention is accepted.
//   - Prior retention is COMPLIANCE and still active: refuse any mode
//     change (incl. COMPLIANCE→GOVERNANCE downgrade) and any shortening of
//     RetainUntilDate, regardless of the bypass header. Extensions are OK.
//   - Prior retention is GOVERNANCE and still active: refuse mode-change
//     (GOVERNANCE→GOVERNANCE shortening / mode flip) without bypass header.
//     Extensions are OK without bypass. GOVERNANCE→COMPLIANCE upgrade is
//     allowed without bypass — the protection is strictly increasing.
//
// Storage "not configured" / "object not found" errors short-circuit to
// "no prior retention", matching evaluateObjectLock's allow-list.
func (h *Handler) evaluateRetentionChange(ctx context.Context, bucket, key string, next *storage.ObjectRetention, bypassGovernance bool) *S3Error {
	if next == nil {
		// PutObjectRetention with no body — let the storage layer reject
		// it via ErrMalformedXML rather than synthesising AccessDenied.
		return nil
	}

	prior, err := h.storage.GetObjectRetention(ctx, bucket, key)
	if err != nil {
		if errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) ||
			errors.Is(err, storage.ErrObjectNotFound) ||
			errors.Is(err, storage.ErrBucketNotFound) {
			return nil
		}
		// Treat unexpected DB errors as fail-closed: better to surface a
		// 403 than to silently let a downgrade through during an outage.
		return ErrAccessDenied
	}
	if prior == nil || prior.RetainUntilDate == nil {
		return nil
	}
	// Once the previous RetainUntilDate is in the past, the lock is no
	// longer active and the object may be re-locked freely.
	if !prior.RetainUntilDate.After(time.Now()) {
		return nil
	}

	// Active prior retention. The new retention must not weaken protection.
	switch prior.Mode {
	case storage.ObjectLockRetentionModeCompliance:
		// COMPLIANCE is immutable until expiry: no mode change, no
		// shortening — bypass header is irrelevant (it only affects
		// GOVERNANCE).
		if next.Mode != storage.ObjectLockRetentionModeCompliance {
			return ErrAccessDenied
		}
		if next.RetainUntilDate == nil || next.RetainUntilDate.Before(*prior.RetainUntilDate) {
			return ErrAccessDenied
		}
	case storage.ObjectLockRetentionModeGovernance:
		// IMPORTANT: evaluate the date change BEFORE the mode change,
		// otherwise a GOVERNANCE→COMPLIANCE "upgrade" with a shorter
		// RetainUntilDate would early-return as allowed and silently
		// shorten the lock. The order is:
		//
		//   1. If the new RetainUntilDate is earlier than the current
		//      one, treat this as a shortening — require the bypass
		//      header regardless of the new mode.
		//   2. Only after the date check passes, accept any mode change
		//      (GOVERNANCE→GOVERNANCE keeping/extending the date, or
		//      GOVERNANCE→COMPLIANCE upgrade — both strictly preserve
		//      or increase protection).
		if next.RetainUntilDate == nil || next.RetainUntilDate.Before(*prior.RetainUntilDate) {
			if !bypassGovernance {
				return ErrAccessDenied
			}
		}
	}
	return nil
}
