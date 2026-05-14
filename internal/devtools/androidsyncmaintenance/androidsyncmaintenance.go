package androidsyncmaintenance

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
)

type options struct {
	Passes int
	DryRun bool
	JSON   bool
	Policy db.AndroidSyncPrunePolicy
}

type debtReport struct {
	EligibleGenerations   int `json:"eligible_generations"`
	EligibleItems         int `json:"eligible_items"`
	EligibleAssets        int `json:"eligible_assets"`
	EligibleHealthReports int `json:"eligible_health_reports"`
}

type deleteReport struct {
	Generations   int `json:"generations"`
	Items         int `json:"items"`
	Assets        int `json:"assets"`
	HealthReports int `json:"health_reports"`
}

type report struct {
	DryRun     bool         `json:"dry_run"`
	Passes     int          `json:"passes"`
	DurationMs int64        `json:"duration_ms"`
	Before     debtReport   `json:"before"`
	Deleted    deleteReport `json:"deleted"`
	After      debtReport   `json:"after"`
}

func parseOptions(args []string) (options, error) {
	policy := db.DefaultAndroidSyncPrunePolicy()
	opts := options{
		Passes: db.DefaultAndroidSyncPruneDrainPasses,
		Policy: policy,
	}

	fs := flag.NewFlagSet("android-sync-maintenance", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&opts.Passes, "passes", opts.Passes, "maximum bounded prune passes to run")
	fs.IntVar(&opts.Policy.MaxItemDeletes, "item-batch", policy.MaxItemDeletes, "maximum android_sync_items rows to delete per pass")
	fs.IntVar(&opts.Policy.MaxAssetDeletes, "asset-batch", policy.MaxAssetDeletes, "maximum android_sync_assets rows to delete per pass")
	fs.IntVar(&opts.Policy.MaxGenerationDeletes, "generation-batch", policy.MaxGenerationDeletes, "maximum android_sync_generations rows to delete per pass")
	fs.IntVar(&opts.Policy.MaxHealthDeletes, "health-batch", policy.MaxHealthDeletes, "maximum android_sync_health_reports rows to delete per pass")
	fs.IntVar(&opts.Policy.KeepReadyGenerations, "keep-ready", policy.KeepReadyGenerations, "number of newest ready generations to retain")
	fs.DurationVar(&opts.Policy.KeepMinAge, "keep-min-age", policy.KeepMinAge, "minimum age before a non-retained generation is prune-eligible")
	fs.StringVar(&opts.Policy.ProtectGenerationID, "protect-generation", policy.ProtectGenerationID, "generation id that must not be pruned")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "report prune debt without deleting rows")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON output")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if opts.Passes <= 0 {
		return options{}, fmt.Errorf("passes must be positive")
	}
	return opts, nil
}

func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "android sync maintenance: %v\n", err)
		return 2
	}

	cfg := config.Load()
	if cfg.ConfigError != nil {
		_, _ = fmt.Fprintf(stderr, "android sync maintenance: invalid configuration: %v\n", cfg.ConfigError)
		return 1
	}

	var out report
	if opts.DryRun {
		store, err := db.OpenReadOnly(cfg.DatabasePath, cfg.DataDir)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "android sync maintenance: open readonly db: %v\n", err)
			return 1
		}
		defer func() {
			_ = store.Close()
		}()
		debt, err := store.AndroidSyncPruneDebt(time.Now().UnixMilli(), opts.Policy)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "android sync maintenance: read prune debt: %v\n", err)
			return 1
		}
		out = report{
			DryRun: true,
			Before: debtToReport(debt),
			After:  debtToReport(debt),
		}
	} else {
		// Writable maintenance uses the normal DB open path, so it may run the
		// same schema and startup repairs as the server before pruning.
		store, err := db.Open(cfg.DatabasePath, cfg.DataDir)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "android sync maintenance: open writable db: %v\n", err)
			return 1
		}
		defer func() {
			_ = store.Close()
		}()
		result, err := store.RunAndroidSyncMaintenance(db.AndroidSyncMaintenanceOptions{
			Policy:    opts.Policy,
			MaxPasses: opts.Passes,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "android sync maintenance: run: %v\n", err)
			return 1
		}
		out = report{
			Passes:     result.Drain.Passes,
			DurationMs: result.DurationMs,
			Before:     debtToReport(result.Before),
			Deleted: deleteReport{
				Generations:   result.Drain.GenerationsDeleted,
				Items:         result.Drain.ItemsDeleted,
				Assets:        result.Drain.AssetsDeleted,
				HealthReports: result.Drain.HealthReportsDeleted,
			},
			After: debtToReport(result.After),
		}
	}

	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			_, _ = fmt.Fprintf(stderr, "android sync maintenance: encode JSON: %v\n", err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprint(stdout, formatText(out))
	return 0
}

func debtToReport(debt db.AndroidSyncPruneDebt) debtReport {
	return debtReport{
		EligibleGenerations:   debt.EligibleGenerations,
		EligibleItems:         debt.EligibleItems,
		EligibleAssets:        debt.EligibleAssets,
		EligibleHealthReports: debt.EligibleHealthReports,
	}
}

func formatText(r report) string {
	var b strings.Builder
	if r.DryRun {
		b.WriteString("dry_run: true\n")
	}
	fmt.Fprintf(
		&b,
		"before: generations=%d items=%d assets=%d health_reports=%d\n",
		r.Before.EligibleGenerations,
		r.Before.EligibleItems,
		r.Before.EligibleAssets,
		r.Before.EligibleHealthReports,
	)
	fmt.Fprintf(
		&b,
		"deleted: generations=%d items=%d assets=%d health_reports=%d passes=%d\n",
		r.Deleted.Generations,
		r.Deleted.Items,
		r.Deleted.Assets,
		r.Deleted.HealthReports,
		r.Passes,
	)
	fmt.Fprintf(
		&b,
		"after: generations=%d items=%d assets=%d health_reports=%d\n",
		r.After.EligibleGenerations,
		r.After.EligibleItems,
		r.After.EligibleAssets,
		r.After.EligibleHealthReports,
	)
	if r.DurationMs > 0 {
		fmt.Fprintf(&b, "duration_ms: %d\n", r.DurationMs)
	}
	return b.String()
}
