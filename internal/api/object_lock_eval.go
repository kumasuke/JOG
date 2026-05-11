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
func (h *Handler) evaluateObjectLock(ctx context.Context, bucket, key string, bypassGovernance bool) *S3Error {
	cfg, err := h.storage.GetObjectLockConfiguration(ctx, bucket)
	if err != nil || cfg == nil || !cfg.ObjectLockEnabled {
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
