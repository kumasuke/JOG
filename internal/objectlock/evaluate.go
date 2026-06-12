package objectlock

import (
	"context"
	"errors"
	"time"

	"github.com/kumasuke/jog/internal/storage"
)

// Verdict reports whether a version may be deleted under the bucket's Object
// Lock policy. Deletable=false means an active lock blocks deletion.
type Verdict struct {
	Deletable bool
	Reason    string // "", "legal-hold", "compliance", "governance"
}

// EvaluateDeletable decides whether a destructive operation against
// (bucket, key, versionID) is allowed under the bucket's Object Lock policy.
//
// Return value contract:
//   - Allow           → (Verdict{Deletable: true}, nil)
//   - Blocked by lock → (Verdict{Deletable: false, Reason: "legal-hold"|"compliance"|"governance"}, nil)
//   - Unexpected error → (Verdict{}, err) — caller should fail-closed
func EvaluateDeletable(ctx context.Context, st storage.Storage, bucket, key, versionID string, bypassGovernance bool, now time.Time) (Verdict, error) {
	// Step 1: Object Lock configuration check
	cfg, err := st.GetObjectLockConfiguration(ctx, bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) ||
			errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) ||
			errors.Is(err, storage.ErrObjectLockConfigurationNotFound) {
			return Verdict{Deletable: true}, nil
		}
		return Verdict{}, err
	}
	if cfg == nil || !cfg.ObjectLockEnabled {
		return Verdict{Deletable: true}, nil
	}

	// Step 2: Legal hold check
	legalHold, err := st.GetObjectLegalHold(ctx, bucket, key, versionID)
	if err != nil {
		if !errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) && !errors.Is(err, storage.ErrObjectNotFound) {
			return Verdict{}, err
		}
	} else if legalHold != nil && legalHold.Status == storage.ObjectLegalHoldStatusOn {
		return Verdict{Deletable: false, Reason: "legal-hold"}, nil
	}

	// Step 3: Retention check
	retention, err := st.GetObjectRetention(ctx, bucket, key, versionID)
	if err != nil {
		if errors.Is(err, storage.ErrNoSuchObjectLockConfiguration) ||
			errors.Is(err, storage.ErrObjectNotFound) {
			return Verdict{Deletable: true}, nil
		}
		return Verdict{}, err
	}
	if retention == nil || retention.RetainUntilDate == nil {
		return Verdict{Deletable: true}, nil
	}
	if !retention.RetainUntilDate.After(now) {
		return Verdict{Deletable: true}, nil
	}
	switch retention.Mode {
	case storage.ObjectLockRetentionModeCompliance:
		return Verdict{Deletable: false, Reason: "compliance"}, nil
	case storage.ObjectLockRetentionModeGovernance:
		if !bypassGovernance {
			return Verdict{Deletable: false, Reason: "governance"}, nil
		}
	}
	return Verdict{Deletable: true}, nil
}
