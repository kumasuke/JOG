package cli

import (
	"context"
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/kumasuke/jog/internal/config"
	"github.com/kumasuke/jog/internal/lifecycle"
	"github.com/kumasuke/jog/internal/notification"
	"github.com/kumasuke/jog/internal/storage"
	"github.com/spf13/cobra"
)

var (
	lcConfigFile string
	lcDataDir    string
	lcBucket     string
	lcDryRun     bool
)

// NewLifecycleCmd creates the `lifecycle` command group.
func NewLifecycleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lifecycle",
		Short: "Lifecycle engine operations",
		Long:  "Manually drive the JOG object lifecycle engine.",
	}
	cmd.AddCommand(newLifecycleRunCmd())
	return cmd
}

func newLifecycleRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run one lifecycle cycle",
		Long: "Run a single lifecycle cycle against the configured storage and print a report.\n\n" +
			"This is safe to run against a live server's data directory: every deletion is\n" +
			"guarded transactionally and is idempotent, so concurrent execution with the\n" +
			"server's built-in ticker cannot corrupt state. Normally the built-in ticker is\n" +
			"sufficient; use this for manual runs and --dry-run verification.",
		RunE: runLifecycle,
	}
	cmd.Flags().StringVarP(&lcConfigFile, "config", "c", "", "config file path")
	cmd.Flags().StringVarP(&lcDataDir, "data-dir", "d", "", "data directory (overrides config)")
	cmd.Flags().StringVar(&lcBucket, "bucket", "", "only process this bucket")
	cmd.Flags().BoolVar(&lcDryRun, "dry-run", false, "report what would happen without making any changes")
	return cmd
}

func runLifecycle(cmd *cobra.Command, args []string) error {
	var cfg *config.Config
	var err error
	if lcConfigFile != "" {
		cfg, err = config.LoadFromFile(lcConfigFile)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if lcDataDir != "" {
		cfg.Storage.DataDir = lcDataDir
		cfg.Storage.MetadataDB = lcDataDir + "/metadata.db"
	}

	setupLogging(cfg.Logging)

	store, err := storage.NewFileSystem(cfg.Storage.DataDir, cfg.Storage.MetadataDB)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer store.Close()

	eng := lifecycle.NewEngine(store, lifecycle.Config{
		Interval:           cfg.Lifecycle.Interval.Std(),
		MaxActionsPerCycle: cfg.Lifecycle.MaxActionsPerCycle,
		DryRun:             lcDryRun,
		OnlyBucket:         lcBucket,
	}, nil)

	// Wire lifecycle-expiration notifications for this manual run, matching the
	// server's behavior. FromTargets returns nil (notifications disabled) when no
	// targets are configured; a dry-run never emits regardless of wiring.
	disp := notification.FromTargets(
		cfg.Notification.Targets,
		cfg.Notification.Region,
		cfg.Notification.DeliveryTimeout.Std(),
		cfg.Notification.BlockPrivateTargets,
	)
	if disp != nil {
		eng.SetNotifier(disp)
	}
	// Block until async webhook deliveries finish so a short-lived CLI run does
	// not exit before they are sent, even on the error path where some events
	// may already have been dispatched. Wait is nil-safe.
	defer disp.Wait()

	report, err := eng.RunOnce(context.Background())
	if err != nil {
		return fmt.Errorf("lifecycle run failed: %w", err)
	}
	printReport(cmd, report)
	return nil
}

func printReport(cmd *cobra.Command, report lifecycle.Report) {
	out := cmd.OutOrStdout()
	if report.DryRun {
		fmt.Fprintln(out, "DRY RUN — no changes were made")
	}

	names := make([]string, 0, len(report.Buckets))
	for name := range report.Buckets {
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) == 0 {
		fmt.Fprintln(out, "No buckets with a lifecycle configuration were processed.")
		return
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "BUCKET\tEVALUATED\tACTIONS\tSKIPPED_LOCKED\tSKIPPED_BUSY\tSKIPPED_CHANGED\tABORTED_MPU\tLOCKED_DMS\tERRORS")
	for _, name := range names {
		br := report.Buckets[name]
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
			name, br.Evaluated, br.Actions, br.SkippedLocked, br.SkippedBusy,
			br.SkippedStateChanged, br.AbortedUploads, br.LockedCurrentDMs, br.Errors)
	}
	tw.Flush()

	if report.DryRun {
		for _, name := range names {
			br := report.Buckets[name]
			if len(br.Plan) == 0 {
				continue
			}
			fmt.Fprintf(out, "\nPlanned actions for %s:\n", name)
			for _, line := range br.Plan {
				fmt.Fprintf(out, "  - %s\n", line)
			}
		}
	}
}
