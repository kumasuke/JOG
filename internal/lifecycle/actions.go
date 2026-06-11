package lifecycle

import "github.com/kumasuke/jog/internal/storage"

// Report summarizes one lifecycle cycle. In dry-run mode Actions counts planned
// (not executed) actions and Plan holds human-readable lines.
type Report struct {
	DryRun  bool
	Buckets map[string]*BucketReport
}

func newReport(dryRun bool) Report {
	return Report{DryRun: dryRun, Buckets: map[string]*BucketReport{}}
}

// bucket returns (creating if needed) the per-bucket report.
func (r Report) bucket(name string) *BucketReport {
	br, ok := r.Buckets[name]
	if !ok {
		br = &BucketReport{}
		r.Buckets[name] = br
	}
	return br
}

// TotalActions sums executed/planned actions across all buckets.
func (r Report) TotalActions() int {
	n := 0
	for _, br := range r.Buckets {
		n += br.Actions
	}
	return n
}

// BucketReport accumulates per-bucket counters for a cycle.
type BucketReport struct {
	Evaluated           int
	Actions             int // executed (or planned in dry-run)
	SkippedLocked       int
	SkippedBusy         int
	SkippedStateChanged int
	NotFound            int
	Errors              int
	AbortedUploads      int
	// LockedCurrentDMs counts current versions still under retention/legal hold
	// that received an expiration delete marker (S3-compliant, data preserved;
	// surfaced for operator visibility per design §1-D / §7-9).
	LockedCurrentDMs int
	// Plan holds dry-run action descriptions; empty when not a dry-run.
	Plan []string
}

// record maps a guarded-deletion outcome onto the counters. Expired counts as a
// completed action.
func (br *BucketReport) record(o storage.ExpireOutcome) {
	switch o {
	case storage.ExpireExpired:
		br.Actions++
	case storage.ExpireSkippedLocked:
		br.SkippedLocked++
	case storage.ExpireSkippedBusy:
		br.SkippedBusy++
	case storage.ExpireSkippedStateChanged:
		br.SkippedStateChanged++
	case storage.ExpireNotFound:
		br.NotFound++
	}
}
