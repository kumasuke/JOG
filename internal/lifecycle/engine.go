// Package lifecycle implements the S3 object lifecycle execution engine.
//
// The engine periodically scans buckets that have a lifecycle configuration and
// applies expiration / abort actions. Its overriding constraint (design §0) is
// to never delete a version protected by COMPLIANCE/GOVERNANCE retention or a
// legal hold — every deletion goes through a guarded, transactional storage
// primitive that re-checks the lock state under a write lock and fails closed.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kumasuke/jog/internal/objectlock"
	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// errMaxActions is an internal sentinel used to unwind the scan when the
// per-cycle action cap is reached. Remaining work is deferred to the next cycle.
var errMaxActions = errors.New("lifecycle: max actions per cycle reached")

// Config tunes the engine. Zero values are replaced with sane defaults.
type Config struct {
	Interval           time.Duration
	MaxActionsPerCycle int
	ThrottleEvery      int           // actions between throttle sleeps
	ThrottleSleep      time.Duration // sleep duration per throttle point
	KeyPageSize        int           // keyset pagination page size
	GCGracePeriod      time.Duration // orphan-file GC grace window
	InitialDelay       time.Duration // delay before the first cycle in Run
	DryRun             bool          // plan only; no side effects whatsoever
	OnlyBucket         string        // when set, only this bucket is processed (CLI --bucket)
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = time.Hour
	}
	if c.MaxActionsPerCycle <= 0 {
		c.MaxActionsPerCycle = 10000
	}
	if c.ThrottleEvery <= 0 {
		c.ThrottleEvery = 100
	}
	if c.ThrottleSleep <= 0 {
		c.ThrottleSleep = 10 * time.Millisecond
	}
	if c.KeyPageSize <= 0 {
		c.KeyPageSize = 1000
	}
	if c.GCGracePeriod <= 0 {
		c.GCGracePeriod = time.Hour
	}
	if c.InitialDelay <= 0 {
		c.InitialDelay = time.Minute
	}
	return c
}

// Engine runs lifecycle cycles. It is single-goroutine by design: RunOnce must
// not be called concurrently on the same instance.
type Engine struct {
	storage storage.Storage
	cfg     Config
	now     func() time.Time
	log     zerolog.Logger

	actions int // per-cycle action counter (reset at the start of RunOnce)
}

// NewEngine constructs an engine. now may be nil (defaults to time.Now); tests
// inject a fixed/advanced clock.
func NewEngine(st storage.Storage, cfg Config, now func() time.Time) *Engine {
	if now == nil {
		now = time.Now
	}
	return &Engine{
		storage: st,
		cfg:     cfg.withDefaults(),
		now:     now,
		log:     log.With().Str("component", "lifecycle").Logger(),
	}
}

// Config returns the effective (defaulted) configuration.
func (e *Engine) EffectiveConfig() Config { return e.cfg }

// Run drives the ticker loop until ctx is cancelled. It logs the buckets that
// carry a lifecycle configuration at startup, runs an initial cycle after
// InitialDelay, then one cycle per Interval.
func (e *Engine) Run(ctx context.Context) {
	e.logConfiguredBuckets(ctx)

	select {
	case <-ctx.Done():
		return
	case <-time.After(e.cfg.InitialDelay):
	}
	e.runOnceLogged(ctx)

	ticker := time.NewTicker(e.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runOnceLogged(ctx)
		}
	}
}

func (e *Engine) runOnceLogged(ctx context.Context) {
	report, err := e.RunOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		e.log.Error().Err(err).Msg("lifecycle cycle failed")
		return
	}
	for bucket, br := range report.Buckets {
		e.log.Info().
			Str("bucket", bucket).
			Int("evaluated", br.Evaluated).
			Int("actions", br.Actions).
			Int("skipped_locked", br.SkippedLocked).
			Int("skipped_busy", br.SkippedBusy).
			Int("skipped_state_changed", br.SkippedStateChanged).
			Int("aborted_uploads", br.AbortedUploads).
			Int("locked_current_dms", br.LockedCurrentDMs).
			Int("errors", br.Errors).
			Msg("lifecycle bucket cycle complete")
	}
}

// logConfiguredBuckets emits a WARN listing every bucket with a lifecycle
// configuration, so an operator enabling the engine sees what is now active.
func (e *Engine) logConfiguredBuckets(ctx context.Context) {
	buckets, err := e.storage.ListBuckets(ctx)
	if err != nil {
		return
	}
	var configured []string
	for _, b := range buckets {
		if _, err := e.storage.GetBucketLifecycleConfiguration(ctx, b.Name); err == nil {
			configured = append(configured, b.Name)
		}
	}
	if len(configured) > 0 {
		e.log.Warn().
			Strs("buckets", configured).
			Bool("dry_run", e.cfg.DryRun).
			Msg("lifecycle engine active for buckets with a lifecycle configuration")
	}
}

// RunOnce executes a single lifecycle cycle and returns its report.
func (e *Engine) RunOnce(ctx context.Context) (Report, error) {
	report := newReport(e.cfg.DryRun)
	e.actions = 0
	now := e.now()

	buckets, err := e.storage.ListBuckets(ctx)
	if err != nil {
		return report, err
	}

	for _, b := range buckets {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if e.cfg.OnlyBucket != "" && b.Name != e.cfg.OnlyBucket {
			continue
		}

		cfg, err := e.storage.GetBucketLifecycleConfiguration(ctx, b.Name)
		if err != nil {
			if errors.Is(err, storage.ErrNoSuchLifecycleConfiguration) {
				continue // no lifecycle config — nothing to do
			}
			// Unknown failure reading the config: fail closed, skip the bucket.
			e.log.Error().Err(err).Str("bucket", b.Name).Msg("read lifecycle config; skipping bucket (fail-closed)")
			report.bucket(b.Name).Errors++
			continue
		}

		versioning, err := e.storage.GetBucketVersioning(ctx, b.Name)
		if err != nil {
			// fail closed: never act on a bucket whose versioning state is unknown (#48).
			e.log.Error().Err(err).Str("bucket", b.Name).Msg("read versioning; skipping bucket (fail-closed)")
			report.bucket(b.Name).Errors++
			continue
		}

		rules := enabledRules(cfg.Rules)
		if len(rules) == 0 {
			continue
		}
		e.warnDateParseErrors(rules, b.Name)

		br := report.bucket(b.Name)
		err = e.processBucket(ctx, b.Name, versioning, rules, now, br)
		if !e.cfg.DryRun {
			e.recordRun(ctx, b.Name, now, br)
		}
		if err != nil {
			if errors.Is(err, errMaxActions) {
				e.log.Warn().Str("bucket", b.Name).Int("max", e.cfg.MaxActionsPerCycle).
					Msg("max actions per cycle reached; remaining work deferred to next cycle")
				return report, nil
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return report, err
			}
			e.log.Error().Err(err).Str("bucket", b.Name).Msg("bucket processing error")
			br.Errors++
		}
	}

	// Orphan GC is global (it scans every bucket's .versions tree), so it is
	// skipped for dry-run and for single-bucket CLI runs.
	if !e.cfg.DryRun && e.cfg.OnlyBucket == "" {
		e.runOrphanGC(ctx, now, report)
	}
	return report, nil
}

func (e *Engine) processBucket(ctx context.Context, bucket string, versioning storage.VersioningStatus, rules []storage.LifecycleRule, now time.Time, br *BucketReport) error {
	if err := e.runAIMU(ctx, bucket, rules, now, br); err != nil {
		return err
	}
	if versioning == storage.VersioningStatusDisabled {
		return e.scanNonVersioned(ctx, bucket, rules, now, br)
	}
	return e.scanVersioned(ctx, bucket, versioning, rules, now, br)
}

// --- AbortIncompleteMultipartUpload ---------------------------------------

func (e *Engine) runAIMU(ctx context.Context, bucket string, rules []storage.LifecycleRule, now time.Time, br *BucketReport) error {
	rule, ok := abortMPURule(rules)
	if !ok {
		return nil
	}
	days := *rule.AbortIncompleteMultipartUpload.DaysAfterInitiation
	prefix := ""
	if rule.Filter != nil {
		prefix = rule.Filter.Prefix
	}

	uploads, err := e.listAllUploads(ctx, bucket, prefix)
	if err != nil {
		return err
	}
	for _, u := range uploads {
		if err := ctx.Err(); err != nil {
			return err
		}
		if prefix != "" && !strings.HasPrefix(u.Key, prefix) {
			continue
		}
		if !eligibleByDays(now, u.Initiated, days) {
			continue
		}
		br.Evaluated++
		if e.cfg.DryRun {
			br.Actions++
			br.AbortedUploads++
			br.Plan = append(br.Plan, fmt.Sprintf("AbortMPU key=%s upload=%s", u.Key, u.UploadID))
		} else {
			if err := e.storage.AbortMultipartUpload(ctx, bucket, u.Key, u.UploadID); err != nil {
				e.log.Error().Err(err).Str("bucket", bucket).Str("key", u.Key).Msg("abort multipart upload failed")
				br.Errors++
				continue
			}
			br.Actions++
			br.AbortedUploads++
		}
		if err := e.afterAction(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) listAllUploads(ctx context.Context, bucket, prefix string) ([]storage.MultipartUpload, error) {
	var all []storage.MultipartUpload
	var keyMarker, uploadMarker string
	for {
		out, err := e.storage.ListMultipartUploads(ctx, &storage.ListMultipartUploadsInput{
			Bucket:         bucket,
			Prefix:         prefix,
			MaxUploads:     1000,
			KeyMarker:      keyMarker,
			UploadIdMarker: uploadMarker,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, out.Uploads...)
		if !out.IsTruncated {
			break
		}
		keyMarker, uploadMarker = out.NextKeyMarker, out.NextUploadIdMarker
		if keyMarker == "" && uploadMarker == "" {
			break // defensive: avoid an infinite loop on a misbehaving backend
		}
	}
	return all, nil
}

// --- non-versioned scan ----------------------------------------------------

func (e *Engine) scanNonVersioned(ctx context.Context, bucket string, rules []storage.LifecycleRule, now time.Time, br *BucketReport) error {
	after := ""
	for {
		keys, err := e.storage.ListLifecycleObjectKeys(ctx, bucket, after, e.cfg.KeyPageSize)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			obj, err := e.storage.HeadObject(ctx, bucket, key)
			if err != nil {
				if errors.Is(err, storage.ErrObjectNotFound) {
					continue
				}
				e.log.Error().Err(err).Str("bucket", bucket).Str("key", key).Msg("head object failed")
				br.Errors++
				continue
			}
			br.Evaluated++
			tags := e.objectTags(ctx, bucket, key, "")
			if _, ok := currentExpirationRule(rules, now, obj.LastModified, key, obj.Size, tags); !ok {
				continue
			}
			// Pre-filter: skip locked objects (the tx guard is the authority).
			if !e.lockVerdict(ctx, bucket, key, "", now) {
				br.SkippedLocked++
				continue
			}
			if e.cfg.DryRun {
				br.Actions++
				br.Plan = append(br.Plan, fmt.Sprintf("ExpireCurrentPhysical key=%s", key))
			} else {
				outcome, err := e.storage.ExpireCurrentObjectGuarded(ctx, bucket, key, obj.LastModified, now)
				if err != nil {
					e.log.Error().Err(err).Str("bucket", bucket).Str("key", key).Msg("expire current object failed")
					br.Errors++
					continue
				}
				br.record(outcome)
			}
			if err := e.afterAction(ctx); err != nil {
				return err
			}
		}
		if len(keys) < e.cfg.KeyPageSize {
			break
		}
		after = keys[len(keys)-1]
	}
	return nil
}

// --- versioned scan --------------------------------------------------------

func (e *Engine) scanVersioned(ctx context.Context, bucket string, versioning storage.VersioningStatus, rules []storage.LifecycleRule, now time.Time, br *BucketReport) error {
	after := ""
	for {
		keys, err := e.storage.ListLifecycleObjectKeys(ctx, bucket, after, e.cfg.KeyPageSize)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := e.processVersionedKey(ctx, bucket, versioning, rules, key, now, br); err != nil {
				return err
			}
		}
		if len(keys) < e.cfg.KeyPageSize {
			break
		}
		after = keys[len(keys)-1]
	}
	return nil
}

func (e *Engine) processVersionedKey(ctx context.Context, bucket string, versioning storage.VersioningStatus, rules []storage.LifecycleRule, key string, now time.Time, br *BucketReport) error {
	vs, err := e.storage.GetObjectVersionsForKey(ctx, bucket, key)
	if err != nil {
		return err
	}
	br.Evaluated++

	// (1) Expiration → delete marker (Enabled only; Suspended is skipped + WARN).
	if versioning == storage.VersioningStatusEnabled {
		if err := e.maybeExpireCurrentDM(ctx, bucket, rules, key, vs, now, br); err != nil {
			return err
		}
	} else {
		if created, size, _, tags, ok := e.currentExpiryContext(ctx, bucket, key, vs); ok {
			if _, match := currentExpirationRule(rules, now, created, key, size, tags); match {
				e.log.Warn().Str("bucket", bucket).Str("key", key).
					Msg("Suspended bucket: Expiration skipped (v1 does not implement suspended delete semantics)")
			}
		}
	}

	// (2) NoncurrentVersionExpiration → physical delete (delete markers excluded).
	if err := e.expireNoncurrent(ctx, bucket, rules, key, vs, now, br); err != nil {
		return err
	}

	// (3) ExpiredObjectDeleteMarker → clean keys whose every version is a DM.
	if err := e.cleanupEODM(ctx, bucket, rules, key, vs, now, br); err != nil {
		return err
	}
	return nil
}

// currentExpiryContext returns the data needed to match an Expiration rule
// against the current object. ok is false when there is no current real version
// (e.g. the current version is already a delete marker or the object is gone).
func (e *Engine) currentExpiryContext(ctx context.Context, bucket, key string, vs []storage.ObjectVersion) (created time.Time, size int64, expectedVID string, tags []storage.Tag, ok bool) {
	if len(vs) > 0 {
		if vs[0].IsDeleteMarker {
			return time.Time{}, 0, "", nil, false
		}
		return vs[0].LastModified, vs[0].Size, vs[0].VersionID, e.objectTags(ctx, bucket, key, vs[0].VersionID), true
	}
	// Pre-versioning object: only an objects row exists.
	obj, err := e.storage.HeadObject(ctx, bucket, key)
	if err != nil || obj == nil {
		return time.Time{}, 0, "", nil, false
	}
	return obj.LastModified, obj.Size, "", e.objectTags(ctx, bucket, key, ""), true
}

func (e *Engine) maybeExpireCurrentDM(ctx context.Context, bucket string, rules []storage.LifecycleRule, key string, vs []storage.ObjectVersion, now time.Time, br *BucketReport) error {
	created, size, expectedVID, tags, ok := e.currentExpiryContext(ctx, bucket, key, vs)
	if !ok {
		return nil
	}
	if _, match := currentExpirationRule(rules, now, created, key, size, tags); !match {
		return nil
	}

	// Per design §1-D the marker is created even when the current version is
	// under retention/legal hold (data is preserved; the version stays
	// retrievable by versionId). We only record/log that fact for visibility.
	locked := !e.lockVerdict(ctx, bucket, key, expectedVID, now)

	if e.cfg.DryRun {
		br.Actions++
		if locked {
			br.LockedCurrentDMs++
		}
		br.Plan = append(br.Plan, fmt.Sprintf("ExpireCurrentDM key=%s expected=%q", key, expectedVID))
		return e.afterAction(ctx)
	}

	markerID, outcome, err := e.storage.CreateExpirationDeleteMarker(ctx, bucket, key, expectedVID)
	if err != nil {
		e.log.Error().Err(err).Str("bucket", bucket).Str("key", key).Msg("create expiration delete marker failed")
		br.Errors++
		return nil
	}
	br.record(outcome)
	if outcome == storage.ExpireExpired && locked {
		br.LockedCurrentDMs++
		e.log.Info().Str("bucket", bucket).Str("key", key).Str("marker", markerID).
			Msg("expiration delete marker created over a locked current version (S3-compliant; version remains retrievable by versionId)")
	}
	return e.afterAction(ctx)
}

func (e *Engine) expireNoncurrent(ctx context.Context, bucket string, rules []storage.LifecycleRule, key string, vs []storage.ObjectVersion, now time.Time, br *BucketReport) error {
	rule, ok := noncurrentRule(rules, key)
	if !ok {
		return nil
	}
	ncve := rule.NoncurrentVersionExpiration
	if ncve.NoncurrentDays == nil && ncve.NewerNoncurrentVersions == nil {
		return nil // invalid rule, nothing to do
	}
	keep := 0
	if ncve.NewerNoncurrentVersions != nil {
		keep = int(*ncve.NewerNoncurrentVersions)
	}
	days := int32(0)
	if ncve.NoncurrentDays != nil {
		days = *ncve.NoncurrentDays
	}

	for _, vid := range noncurrentExpiryCandidates(vs, keep, days, now) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.lockVerdict(ctx, bucket, key, vid, now) {
			br.SkippedLocked++
			continue
		}
		if e.cfg.DryRun {
			br.Actions++
			br.Plan = append(br.Plan, fmt.Sprintf("ExpireNoncurrent key=%s version=%s", key, vid))
		} else {
			outcome, err := e.storage.ExpireObjectVersionGuarded(ctx, bucket, key, vid,
				storage.ExpireGuards{RequireNoncurrent: true}, now)
			if err != nil {
				e.log.Error().Err(err).Str("bucket", bucket).Str("key", key).Str("version", vid).Msg("expire noncurrent version failed")
				br.Errors++
				continue
			}
			br.record(outcome)
		}
		if err := e.afterAction(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) cleanupEODM(ctx context.Context, bucket string, rules []storage.LifecycleRule, key string, vs []storage.ObjectVersion, now time.Time, br *BucketReport) error {
	if !eodmRuleMatches(rules, key) || !allDeleteMarkers(vs) {
		return nil
	}
	for _, v := range vs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.cfg.DryRun {
			br.Actions++
			br.Plan = append(br.Plan, fmt.Sprintf("CleanupEODM key=%s version=%s", key, v.VersionID))
		} else {
			outcome, err := e.storage.ExpireObjectVersionGuarded(ctx, bucket, key, v.VersionID,
				storage.ExpireGuards{RequireAllVersionsAreDeleteMarkers: true}, now)
			if err != nil {
				e.log.Error().Err(err).Str("bucket", bucket).Str("key", key).Str("version", v.VersionID).Msg("cleanup EODM failed")
				br.Errors++
				continue
			}
			br.record(outcome)
		}
		if err := e.afterAction(ctx); err != nil {
			return err
		}
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

// afterAction increments the per-cycle counter, throttles every ThrottleEvery
// actions, and returns errMaxActions once the cap is reached.
func (e *Engine) afterAction(ctx context.Context) error {
	e.actions++
	if e.actions%e.cfg.ThrottleEvery == 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.cfg.ThrottleSleep):
		}
	}
	if e.actions >= e.cfg.MaxActionsPerCycle {
		return errMaxActions
	}
	return nil
}

// lockVerdict reports whether (bucket,key,versionID) may be deleted under Object
// Lock. Unexpected errors fail closed (treated as "not deletable").
func (e *Engine) lockVerdict(ctx context.Context, bucket, key, versionID string, now time.Time) bool {
	v, err := objectlock.EvaluateDeletable(ctx, e.storage, bucket, key, versionID, false, now)
	if err != nil {
		return false
	}
	return v.Deletable
}

func (e *Engine) objectTags(ctx context.Context, bucket, key, versionID string) []storage.Tag {
	tags, err := e.storage.GetObjectTagging(ctx, bucket, key, versionID)
	if err != nil {
		return nil
	}
	return tags
}

func (e *Engine) recordRun(ctx context.Context, bucket string, now time.Time, br *BucketReport) {
	if err := e.storage.RecordLifecycleRun(ctx, bucket, now, br.Actions, br.SkippedLocked, br.Errors); err != nil {
		e.log.Debug().Err(err).Str("bucket", bucket).Msg("record lifecycle run failed (non-fatal)")
	}
}

func (e *Engine) warnDateParseErrors(rules []storage.LifecycleRule, bucket string) {
	for _, r := range rules {
		if dateParseError(r) {
			e.log.Error().Str("bucket", bucket).Str("rule", r.ID).Str("date", *r.Expiration.Date).
				Msg("unparseable Expiration.Date; rule skipped (fail-safe: nothing deleted)")
		}
	}
}

func (e *Engine) runOrphanGC(ctx context.Context, now time.Time, report Report) {
	cutoff := now.Add(-e.cfg.GCGracePeriod)
	scanned, removed, err := e.storage.LifecycleOrphanGC(ctx, cutoff)
	if err != nil {
		e.log.Error().Err(err).Msg("orphan file GC failed")
		return
	}
	if removed > 0 {
		e.log.Info().Int("scanned", scanned).Int("removed", removed).Msg("orphan file GC complete")
	}
}
