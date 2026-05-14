package androidsyncmaintenance

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	igloodb "github.com/screwys/igloo/internal/db"
)

func TestParseOptionsDefaults(t *testing.T) {
	opts, err := parseOptions([]string{})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.Passes <= 0 {
		t.Fatalf("passes = %d, want positive default", opts.Passes)
	}
	if opts.Policy.KeepReadyGenerations != 2 {
		t.Fatalf("keep ready = %d, want 2", opts.Policy.KeepReadyGenerations)
	}
	if opts.Policy.KeepMinAge != 6*time.Hour {
		t.Fatalf("keep min age = %s, want 6h", opts.Policy.KeepMinAge)
	}
}

func TestParseOptionsCustomBudget(t *testing.T) {
	opts, err := parseOptions([]string{
		"-passes", "7",
		"-item-batch", "11",
		"-asset-batch", "13",
		"-generation-batch", "3",
		"-health-batch", "5",
		"-keep-ready", "4",
		"-keep-min-age", "2h",
		"-protect-generation", "android-sync-current",
		"-dry-run",
		"-json",
	})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.Passes != 7 || opts.Policy.MaxItemDeletes != 11 || opts.Policy.MaxAssetDeletes != 13 || opts.Policy.MaxGenerationDeletes != 3 || opts.Policy.MaxHealthDeletes != 5 {
		t.Fatalf("budget parse = %+v", opts)
	}
	if opts.Policy.KeepReadyGenerations != 4 || opts.Policy.KeepMinAge != 2*time.Hour || opts.Policy.ProtectGenerationID != "android-sync-current" {
		t.Fatalf("policy parse = %+v", opts.Policy)
	}
	if !opts.DryRun || !opts.JSON {
		t.Fatalf("flags dry_run/json = %t/%t, want true/true", opts.DryRun, opts.JSON)
	}
}

func TestFormatTextIncludesBeforeAfter(t *testing.T) {
	out := formatText(report{
		Before:  debtReport{EligibleGenerations: 3, EligibleItems: 30, EligibleAssets: 40, EligibleHealthReports: 1},
		Deleted: deleteReport{Generations: 2, Items: 20, Assets: 25, HealthReports: 1},
		After:   debtReport{EligibleGenerations: 1, EligibleItems: 10, EligibleAssets: 15, EligibleHealthReports: 0},
		Passes:  2,
	})
	for _, want := range []string{
		"before: generations=3 items=30 assets=40 health_reports=1",
		"deleted: generations=2 items=20 assets=25 health_reports=1 passes=2",
		"after: generations=1 items=10 assets=15 health_reports=0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunDryRunReportsDebtWithoutDeletingRows(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("IGLOO_DATA_DIR", tmp)
	store, err := igloodb.Open(filepath.Join(tmp, "igloo.db"), tmp)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	nowMs := time.Now().UnixMilli()
	oldBaseMs := nowMs - int64(48*time.Hour/time.Millisecond)
	for i := 0; i < 3; i++ {
		generationID := "android-sync-cli-dry-run-" + string(rune('a'+i))
		if err := store.ExecRaw(`
			INSERT INTO android_sync_generations (
				generation_id, created_at_ms, status, source_version, retention_json,
				item_count, asset_count, ready_asset_count, server_missing_asset_count,
				total_bytes, content_counts_json, asset_counts_json
			) VALUES (?, ?, 'ready', ?, '{}', 1, 1, 1, 0, 1, '{}', '{}')
		`, generationID, oldBaseMs+int64(i), generationID+"-source"); err != nil {
			t.Fatalf("insert generation %s: %v", generationID, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close writable db: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	code := Run([]string{"-dry-run", "-keep-ready", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "dry_run: true") {
		t.Fatalf("dry-run output missing marker:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "before: generations=2") {
		t.Fatalf("dry-run output missing debt:\n%s", stdout.String())
	}

	readonly, err := igloodb.OpenReadOnly(filepath.Join(tmp, "igloo.db"), tmp)
	if err != nil {
		t.Fatalf("open readonly db: %v", err)
	}
	defer func() {
		_ = readonly.Close()
	}()
	var count int
	if err := readonly.QueryRow(`SELECT COUNT(*) FROM android_sync_generations`).Scan(&count); err != nil {
		t.Fatalf("count generations: %v", err)
	}
	if count != 3 {
		t.Fatalf("generation count after dry-run = %d, want 3", count)
	}
}
