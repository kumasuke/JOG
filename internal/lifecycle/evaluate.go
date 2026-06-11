package lifecycle

import (
	"strings"
	"time"

	"github.com/kumasuke/jog/internal/storage"
)

// This file holds the pure rule-evaluation helpers used by the engine. They
// have no I/O so they are exhaustively unit-tested (design §6c). All time
// arithmetic is anchored to UTC and uses the S3 "round up to next midnight"
// convention.

// roundUpMidnightUTC returns 00:00 UTC of the day after t (in UTC).
func roundUpMidnightUTC(t time.Time) time.Time {
	u := t.UTC()
	y, m, d := u.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

// eligibleByDays reports whether `days` have elapsed since `created` under the
// S3 rounding rule: now >= midnight-UTC after (created + days).
func eligibleByDays(now, created time.Time, days int32) bool {
	if days < 0 {
		days = 0
	}
	threshold := roundUpMidnightUTC(created.AddDate(0, 0, int(days)))
	return !now.UTC().Before(threshold)
}

// eligibleByDate reports whether now has reached an RFC3339 (or date-only)
// expiration Date. The second return is false when the date string cannot be
// parsed, so the caller can fail-safe (skip the rule, never delete).
func eligibleByDate(now time.Time, dateStr string) (bool, bool) {
	if dateStr == "" {
		return false, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if d, err := time.Parse(layout, dateStr); err == nil {
			return !now.UTC().Before(d.UTC()), true
		}
	}
	return false, false
}

// enabledRules returns the subset of rules whose Status is "Enabled".
func enabledRules(rules []storage.LifecycleRule) []storage.LifecycleRule {
	var out []storage.LifecycleRule
	for _, r := range rules {
		if r.Status == "Enabled" {
			out = append(out, r)
		}
	}
	return out
}

// filterMatches reports whether an object matches a rule filter. A nil filter
// matches everything. Prefix/Size/Tag are AND-combined (JOG stores the flat
// form; §7-8). Pass tags == nil when per-version tags are not loaded — a rule
// with a Tag filter then does not match, which is the conservative (never
// over-delete) outcome.
func filterMatches(filter *storage.LifecycleRuleFilter, key string, size int64, tags []storage.Tag) bool {
	if filter == nil {
		return true
	}
	if filter.Prefix != "" && !strings.HasPrefix(key, filter.Prefix) {
		return false
	}
	if filter.ObjectSizeGreaterThan != nil && !(size > *filter.ObjectSizeGreaterThan) {
		return false
	}
	if filter.ObjectSizeLessThan != nil && !(size < *filter.ObjectSizeLessThan) {
		return false
	}
	if filter.Tag != nil {
		if !hasTag(tags, filter.Tag.Key, filter.Tag.Value) {
			return false
		}
	}
	return true
}

func hasTag(tags []storage.Tag, k, v string) bool {
	for _, t := range tags {
		if t.Key == k && t.Value == v {
			return true
		}
	}
	return false
}

// currentExpirationRule returns the first enabled rule whose Expiration applies
// to a current object (filter matches and Days/Date has elapsed). The returned
// rule's Expiration is guaranteed non-nil. ok is false when no rule applies.
//
// A rule whose Expiration only carries ExpiredObjectDeleteMarker (no Days/Date)
// is NOT a current-expiration rule — it is handled by the EODM path.
func currentExpirationRule(rules []storage.LifecycleRule, now, created time.Time, key string, size int64, tags []storage.Tag) (storage.LifecycleRule, bool) {
	for _, r := range rules {
		if r.Expiration == nil {
			continue
		}
		if !filterMatches(r.Filter, key, size, tags) {
			continue
		}
		if r.Expiration.Days != nil {
			if eligibleByDays(now, created, *r.Expiration.Days) {
				return r, true
			}
			continue
		}
		if r.Expiration.Date != nil {
			if elig, ok := eligibleByDate(now, *r.Expiration.Date); ok && elig {
				return r, true
			}
			continue
		}
	}
	return storage.LifecycleRule{}, false
}

// dateParseError reports whether a rule carries an Expiration.Date that cannot
// be parsed (so the engine can log it and fail-safe).
func dateParseError(r storage.LifecycleRule) bool {
	if r.Expiration == nil || r.Expiration.Date == nil {
		return false
	}
	_, ok := eligibleByDate(time.Now(), *r.Expiration.Date)
	return !ok
}

// noncurrentRule returns the first enabled rule with a NoncurrentVersionExpiration
// whose prefix matches the key. The caller applies the full filter (size/tag)
// per version. v1 limitation: only the first prefix-matching rule is used; when
// several NCVE rules overlap a key they are not merged (under-deletion, safe).
// ok is false when none applies.
func noncurrentRule(rules []storage.LifecycleRule, key string) (storage.LifecycleRule, bool) {
	for _, r := range rules {
		if r.NoncurrentVersionExpiration == nil {
			continue
		}
		if r.Filter != nil && r.Filter.Prefix != "" && !strings.HasPrefix(key, r.Filter.Prefix) {
			continue
		}
		return r, true
	}
	return storage.LifecycleRule{}, false
}

// noncurrentExpiryCandidates returns the version IDs of noncurrent, non-delete-
// marker versions eligible for NoncurrentVersionExpiration. vs MUST be ordered
// (last_modified DESC, version_id DESC) so vs[0] is the current version.
//
// `match` reports whether a version is in scope of the rule filter (prefix /
// size / tag). Only matching versions are considered: the newest `keep`
// matching versions are protected by NewerNoncurrentVersions, and the rest
// become eligible once NoncurrentDays have elapsed since they became noncurrent
// (the last_modified of the immediately newer version, matching or not). A nil
// match treats every version as in scope.
func noncurrentExpiryCandidates(vs []storage.ObjectVersion, keep int, noncurrentDays int32, now time.Time, match func(storage.ObjectVersion) bool) []string {
	if keep < 0 {
		keep = 0
	}
	var out []string
	matchedSeen := 0
	for i := 1; i < len(vs); i++ {
		v := vs[i]
		if v.IsDeleteMarker {
			continue // delete markers are handled by the EODM path, never NCVE
		}
		if match != nil && !match(v) {
			continue // filtered out — never delete an out-of-scope version
		}
		if matchedSeen < keep {
			matchedSeen++
			continue // protected by NewerNoncurrentVersions
		}
		matchedSeen++
		noncurrentSince := vs[i-1].LastModified // immediately newer version
		if eligibleByDays(now, noncurrentSince, noncurrentDays) {
			out = append(out, v.VersionID)
		}
	}
	return out
}

// allDeleteMarkers reports whether vs is non-empty and every version is a
// delete marker (the ExpiredObjectDeleteMarker precondition).
func allDeleteMarkers(vs []storage.ObjectVersion) bool {
	if len(vs) == 0 {
		return false
	}
	for i := range vs {
		if !vs[i].IsDeleteMarker {
			return false
		}
	}
	return true
}

// eodmRuleMatches reports whether any enabled rule requests
// ExpiredObjectDeleteMarker cleanup for the key (prefix filter only).
func eodmRuleMatches(rules []storage.LifecycleRule, key string) bool {
	for _, r := range rules {
		if r.Expiration == nil || r.Expiration.ExpiredObjectDeleteMarker == nil || !*r.Expiration.ExpiredObjectDeleteMarker {
			continue
		}
		if r.Filter != nil && r.Filter.Prefix != "" && !strings.HasPrefix(key, r.Filter.Prefix) {
			continue
		}
		return true
	}
	return false
}

// abortMPURule returns the first enabled rule with an
// AbortIncompleteMultipartUpload action. S3 forbids combining a tag filter with
// AIMU, so rules carrying a Tag filter are skipped. v1 limitation: only the
// first such rule is applied; multiple AIMU rules with disjoint prefixes are
// not all evaluated (under-action, safe).
func abortMPURule(rules []storage.LifecycleRule) (storage.LifecycleRule, bool) {
	for _, r := range rules {
		if r.AbortIncompleteMultipartUpload == nil || r.AbortIncompleteMultipartUpload.DaysAfterInitiation == nil {
			continue
		}
		if r.Filter != nil && r.Filter.Tag != nil {
			continue // S3: tag filter is incompatible with AIMU
		}
		return r, true
	}
	return storage.LifecycleRule{}, false
}
