package lifecycle

import (
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/storage"
)

func i32(v int32) *int32 { return &v }
func i64(v int64) *int64 { return &v }
func bp(v bool) *bool    { return &v }

func TestRoundUpMidnightUTC(t *testing.T) {
	cases := []struct {
		in   time.Time
		want time.Time
	}{
		{time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		// Non-UTC input is normalized first.
		{time.Date(2026, 1, 1, 23, 0, 0, 0, time.FixedZone("X", -2*3600)), time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := roundUpMidnightUTC(c.in); !got.Equal(c.want) {
			t.Errorf("roundUpMidnightUTC(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestEligibleByDays(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// threshold = midnight after (created + 1 day) = 2026-01-03 00:00 UTC.
	threshold := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		now  time.Time
		days int32
		want bool
	}{
		{"one-second-before", threshold.Add(-time.Second), 1, false},
		{"exactly-at", threshold, 1, true},
		{"one-second-after", threshold.Add(time.Second), 1, true},
		{"long-after", threshold.Add(72 * time.Hour), 1, true},
		{"zero-days-next-midnight", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), 0, true},
		{"zero-days-before-midnight", time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := eligibleByDays(c.now, created, c.days); got != c.want {
				t.Errorf("eligibleByDays(%v, created, %d) = %v, want %v", c.now, c.days, got, c.want)
			}
		})
	}
}

func TestEligibleByDate(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		date     string
		wantElig bool
		wantOK   bool
	}{
		{"past-rfc3339", "2026-01-01T00:00:00Z", true, true},
		{"future-rfc3339", "2030-01-01T00:00:00Z", false, true},
		{"date-only-past", "2026-01-01", true, true},
		{"unparseable", "not-a-date", false, false},
		{"empty", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			elig, ok := eligibleByDate(now, c.date)
			if elig != c.wantElig || ok != c.wantOK {
				t.Errorf("eligibleByDate(%q) = (%v,%v), want (%v,%v)", c.date, elig, ok, c.wantElig, c.wantOK)
			}
		})
	}
}

func TestFilterMatches(t *testing.T) {
	tags := []storage.Tag{{Key: "env", Value: "prod"}}
	cases := []struct {
		name   string
		filter *storage.LifecycleRuleFilter
		key    string
		size   int64
		tags   []storage.Tag
		want   bool
	}{
		{"nil-filter", nil, "any", 5, nil, true},
		{"prefix-match", &storage.LifecycleRuleFilter{Prefix: "logs/"}, "logs/a", 5, nil, true},
		{"prefix-miss", &storage.LifecycleRuleFilter{Prefix: "logs/"}, "data/a", 5, nil, false},
		{"size-gt-match", &storage.LifecycleRuleFilter{ObjectSizeGreaterThan: i64(10)}, "k", 11, nil, true},
		{"size-gt-miss", &storage.LifecycleRuleFilter{ObjectSizeGreaterThan: i64(10)}, "k", 10, nil, false},
		{"size-lt-match", &storage.LifecycleRuleFilter{ObjectSizeLessThan: i64(10)}, "k", 9, nil, true},
		{"size-lt-miss", &storage.LifecycleRuleFilter{ObjectSizeLessThan: i64(10)}, "k", 10, nil, false},
		{"tag-match", &storage.LifecycleRuleFilter{Tag: &storage.Tag{Key: "env", Value: "prod"}}, "k", 1, tags, true},
		{"tag-miss-value", &storage.LifecycleRuleFilter{Tag: &storage.Tag{Key: "env", Value: "dev"}}, "k", 1, tags, false},
		{"tag-miss-no-tags", &storage.LifecycleRuleFilter{Tag: &storage.Tag{Key: "env", Value: "prod"}}, "k", 1, nil, false},
		{"and-all", &storage.LifecycleRuleFilter{Prefix: "logs/", ObjectSizeGreaterThan: i64(2), Tag: &storage.Tag{Key: "env", Value: "prod"}}, "logs/x", 5, tags, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := filterMatches(c.filter, c.key, c.size, c.tags); got != c.want {
				t.Errorf("filterMatches = %v, want %v", got, c.want)
			}
		})
	}
}

func TestEnabledRules(t *testing.T) {
	rules := []storage.LifecycleRule{
		{ID: "a", Status: "Enabled"},
		{ID: "b", Status: "Disabled"},
		{ID: "c", Status: "Enabled"},
	}
	got := enabledRules(rules)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Fatalf("enabledRules = %+v, want a,c", got)
	}
}

func TestNoncurrentExpiryCandidates(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Versions newest-first. last_modified spaced 1 day apart.
	mk := func(id string, dayOffset int, dm bool) storage.ObjectVersion {
		return storage.ObjectVersion{VersionID: id, LastModified: base.AddDate(0, 0, dayOffset), IsDeleteMarker: dm}
	}
	// current=v5(day5), noncurrent reals v4..v1, all became noncurrent when the
	// next newer was written.
	vs := []storage.ObjectVersion{
		mk("v5", 5, false),
		mk("v4", 4, false),
		mk("v3", 3, false),
		mk("v2", 2, false),
		mk("v1", 1, false),
	}
	now := base.AddDate(0, 0, 100) // far future, everything past NoncurrentDays

	t.Run("keep-2-deletes-rest", func(t *testing.T) {
		got := noncurrentExpiryCandidates(vs, 2, 1, now, nil)
		// Protect v4, v3 (newest 2 noncurrent). Delete v2, v1.
		want := map[string]bool{"v2": true, "v1": true}
		if len(got) != 2 || !want[got[0]] || !want[got[1]] {
			t.Fatalf("got %v, want v2,v1", got)
		}
	})

	t.Run("keep-0-noncurrentdays-not-elapsed", func(t *testing.T) {
		// now just after v5 written: v4 became noncurrent at day5; NoncurrentDays=30 not elapsed.
		nowEarly := base.AddDate(0, 0, 5).Add(time.Hour)
		got := noncurrentExpiryCandidates(vs, 0, 30, nowEarly, nil)
		if len(got) != 0 {
			t.Fatalf("got %v, want none (NoncurrentDays not elapsed)", got)
		}
	})

	t.Run("delete-markers-excluded", func(t *testing.T) {
		withDM := []storage.ObjectVersion{
			mk("dm", 6, true), // current is a delete marker
			mk("v2", 2, false),
			mk("v1", 1, false),
		}
		got := noncurrentExpiryCandidates(withDM, 0, 1, now, nil)
		// Both v2, v1 are real noncurrent and eligible; dm is current (index 0), excluded.
		if len(got) != 2 {
			t.Fatalf("got %v, want v2,v1", got)
		}
	})

	t.Run("filter-selects-candidates-keep-counts-all", func(t *testing.T) {
		// keep=1 protects the newest real noncurrent version (v4) regardless of
		// filter. Beyond the keep window, only filter-matching versions are
		// deletion candidates: v3 and v1 match and are eligible; v2 is out of
		// scope and is never deleted. (S3: NewerNoncurrentVersions counts ALL
		// newer noncurrent versions, not just matching ones.)
		match := func(v storage.ObjectVersion) bool {
			return v.VersionID == "v1" || v.VersionID == "v3"
		}
		got := noncurrentExpiryCandidates(vs, 1, 1, now, match)
		if len(got) != 2 || got[0] != "v3" || got[1] != "v1" {
			t.Fatalf("got %v, want [v3 v1] (v4 protected by keep, v2 out of scope)", got)
		}
	})

	t.Run("keep-counts-nonmatching-versions", func(t *testing.T) {
		// keep=1, but the newest noncurrent (v4) does NOT match the filter; it
		// still occupies the single keep slot, so the next matching version (v3)
		// is eligible — it already has a newer noncurrent version (v4).
		match := func(v storage.ObjectVersion) bool { return v.VersionID != "v4" }
		got := noncurrentExpiryCandidates(vs, 1, 1, now, match)
		// v4 protected (keep), v3/v2/v1 match and are eligible.
		want := map[string]bool{"v3": true, "v2": true, "v1": true}
		if len(got) != 3 || !want[got[0]] || !want[got[1]] || !want[got[2]] {
			t.Fatalf("got %v, want v3,v2,v1 (v4 fills the keep slot despite not matching)", got)
		}
	})
}

func TestAllDeleteMarkers(t *testing.T) {
	dm := storage.ObjectVersion{IsDeleteMarker: true}
	real := storage.ObjectVersion{IsDeleteMarker: false}
	if allDeleteMarkers(nil) {
		t.Error("empty should be false")
	}
	if !allDeleteMarkers([]storage.ObjectVersion{dm, dm}) {
		t.Error("all-DM should be true")
	}
	if allDeleteMarkers([]storage.ObjectVersion{dm, real}) {
		t.Error("mixed should be false")
	}
}

func TestEODMRuleMatches(t *testing.T) {
	rules := []storage.LifecycleRule{
		{Status: "Enabled", Filter: &storage.LifecycleRuleFilter{Prefix: "logs/"}, Expiration: &storage.LifecycleExpiration{ExpiredObjectDeleteMarker: bp(true)}},
	}
	if !eodmRuleMatches(rules, "logs/x") {
		t.Error("should match logs/x")
	}
	if eodmRuleMatches(rules, "data/x") {
		t.Error("should not match data/x")
	}
	if eodmRuleMatches([]storage.LifecycleRule{{Status: "Enabled", Expiration: &storage.LifecycleExpiration{ExpiredObjectDeleteMarker: bp(false)}}}, "x") {
		t.Error("EODM=false should not match")
	}
}

func TestAbortMPURule(t *testing.T) {
	withTag := []storage.LifecycleRule{{
		Status:                         "Enabled",
		Filter:                         &storage.LifecycleRuleFilter{Tag: &storage.Tag{Key: "k", Value: "v"}},
		AbortIncompleteMultipartUpload: &storage.AbortIncompleteMultipartUpload{DaysAfterInitiation: i32(7)},
	}}
	if _, ok := abortMPURule(withTag); ok {
		t.Error("AIMU with tag filter must be skipped (S3 rule)")
	}
	withPrefix := []storage.LifecycleRule{{
		Status:                         "Enabled",
		Filter:                         &storage.LifecycleRuleFilter{Prefix: "p/"},
		AbortIncompleteMultipartUpload: &storage.AbortIncompleteMultipartUpload{DaysAfterInitiation: i32(7)},
	}}
	if _, ok := abortMPURule(withPrefix); !ok {
		t.Error("AIMU with prefix filter should apply")
	}
}

func TestCurrentExpirationRule(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rules := []storage.LifecycleRule{
		{ID: "disabled", Status: "Disabled", Expiration: &storage.LifecycleExpiration{Days: i32(1)}},
		{ID: "days", Status: "Enabled", Filter: &storage.LifecycleRuleFilter{Prefix: "logs/"}, Expiration: &storage.LifecycleExpiration{Days: i32(30)}},
	}
	enabled := enabledRules(rules)
	r, ok := currentExpirationRule(enabled, now, created, "logs/a", 10, nil)
	if !ok || r.ID != "days" {
		t.Fatalf("currentExpirationRule = %v,%v want days,true", r.ID, ok)
	}
	// Prefix miss.
	if _, ok := currentExpirationRule(enabled, now, created, "data/a", 10, nil); ok {
		t.Error("prefix miss should not match")
	}
	// EODM-only rule is not a current-expiration rule.
	eodmOnly := enabledRules([]storage.LifecycleRule{{ID: "eodm", Status: "Enabled", Expiration: &storage.LifecycleExpiration{ExpiredObjectDeleteMarker: bp(true)}}})
	if _, ok := currentExpirationRule(eodmOnly, now, created, "x", 1, nil); ok {
		t.Error("EODM-only rule should not be a current-expiration rule")
	}
}
