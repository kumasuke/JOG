package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kumasuke/jog/internal/lifecycle"
	"github.com/spf13/cobra"
)

func TestNewLifecycleCmd_HasRunSubcommand(t *testing.T) {
	cmd := NewLifecycleCmd()
	var run *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "run" {
			run = c
		}
	}
	if run == nil {
		t.Fatal("lifecycle command missing 'run' subcommand")
	}
	for _, flag := range []string{"config", "data-dir", "bucket", "dry-run"} {
		if run.Flags().Lookup(flag) == nil {
			t.Errorf("run subcommand missing --%s flag", flag)
		}
	}
}

func TestPrintReport_DryRunPlan(t *testing.T) {
	report := lifecycle.Report{
		DryRun: true,
		Buckets: map[string]*lifecycle.BucketReport{
			"b": {
				Evaluated:     3,
				Actions:       2,
				SkippedLocked: 1,
				Plan:          []string{"ExpireCurrentDM key=a expected=\"v1\"", "ExpireNoncurrent key=b version=v2"},
			},
		},
	}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	printReport(cmd, report)

	out := buf.String()
	for _, want := range []string{"DRY RUN", "BUCKET", "Planned actions for b", "ExpireCurrentDM", "ExpireNoncurrent"} {
		if !strings.Contains(out, want) {
			t.Errorf("report output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestPrintReport_NoBuckets(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	printReport(cmd, lifecycle.Report{Buckets: map[string]*lifecycle.BucketReport{}})
	if !strings.Contains(buf.String(), "No buckets") {
		t.Errorf("expected empty-report message, got %q", buf.String())
	}
}
