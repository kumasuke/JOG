package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kumasuke/jog/internal/lifecycle"
	"github.com/kumasuke/jog/internal/storage"
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

// TestRunLifecycle_WiresNotificationFromConfig is the CLI glue test: a manual
// `jog lifecycle run` must build the dispatcher from config targets, wire it
// onto the engine, and await async deliveries (defer disp.Wait()) before
// returning. We pre-seed storage with an object due for expiration and a bucket
// subscribed to the webhook, point the CLI at a config.yaml naming that webhook,
// and assert the delivery arrived by the time runLifecycle returns.
func TestRunLifecycle_WiresNotificationFromConfig(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metadata.db")

	const arn = "arn:topic:lc"
	var delivered atomic.Int64
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		delivered.Add(1)
	}))
	defer webhook.Close()

	// Seed storage: an unversioned object already past a date-based expiration,
	// in a bucket subscribed to lifecycle events via the resolved ARN.
	st, err := storage.NewFileSystem(dir, dbPath)
	if err != nil {
		t.Fatalf("NewFileSystem: %v", err)
	}
	if err := st.CreateBucket(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutObject(ctx, "b", "k", strings.NewReader("data"), 4, "text/plain", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.PutBucketLifecycleConfiguration(ctx, "b", &storage.LifecycleConfiguration{
		Rules: []storage.LifecycleRule{{
			ID: "exp", Status: "Enabled",
			Expiration: &storage.LifecycleExpiration{Date: strptr("2000-01-01")},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutBucketNotification(ctx, "b", &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "lc", TopicArn: arn, Events: []string{"s3:LifecycleExpiration:*"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Config file naming the same data dir and mapping the ARN to our webhook.
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgYAML := fmt.Sprintf(`
storage:
  data_dir: %q
  metadata_db: %q
logging:
  level: error
notification:
  region: us-east-1
  targets:
    %q: %q
`, dir, dbPath, arn, webhook.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Drive runLifecycle via its package-level flags; restore them after.
	prevConfig, prevDataDir, prevBucket, prevDryRun := lcConfigFile, lcDataDir, lcBucket, lcDryRun
	t.Cleanup(func() {
		lcConfigFile, lcDataDir, lcBucket, lcDryRun = prevConfig, prevDataDir, prevBucket, prevDryRun
	})
	lcConfigFile, lcDataDir, lcBucket, lcDryRun = cfgPath, "", "", false

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := runLifecycle(cmd, nil); err != nil {
		t.Fatalf("runLifecycle: %v", err)
	}

	// runLifecycle's deferred disp.Wait() has run by now, so the webhook must
	// have been delivered (the CLI wired the dispatcher from config targets).
	if n := delivered.Load(); n != 1 {
		t.Fatalf("expected 1 webhook delivery wired from config, got %d", n)
	}
}

func strptr(s string) *string { return &s }
