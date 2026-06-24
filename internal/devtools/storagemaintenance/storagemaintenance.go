package storagemaintenance

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
)

type options struct {
	Action         string
	Limit          int
	RetentionLimit int
	Apply          bool
	JSON           bool
	DBPath         string
	DataDir        string
}

type report struct {
	Mode       string `json:"mode"`
	Action     string `json:"action"`
	Database   string `json:"database"`
	DataDir    string `json:"data_dir"`
	DurationMs int64  `json:"duration_ms"`
	Result     any    `json:"result"`
}

func parseOptions(args []string) (options, error) {
	opts := options{
		Limit: 1000,
	}
	fs := flag.NewFlagSet("storage-maintenance", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.Action, "action", "", "maintenance action: x-dedupe, x-retention, or asset-file-state")
	fs.IntVar(&opts.Limit, "limit", opts.Limit, "maximum duplicate groups, X sources, or assets to inspect; 0 means unlimited")
	fs.IntVar(&opts.RetentionLimit, "retention-limit", 0, "override X media rows kept per followed source; 0 uses each channel setting")
	fs.BoolVar(&opts.Apply, "apply", false, "write DB changes and remove unreferenced files; without this flag the command only reports")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON output")
	fs.StringVar(&opts.DBPath, "db", "", "database path; defaults to configured Igloo database")
	fs.StringVar(&opts.DataDir, "data-dir", "", "data directory; defaults to configured Igloo data dir")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	opts.Action = strings.TrimSpace(opts.Action)
	switch opts.Action {
	case "x-dedupe", "x-retention", "asset-file-state", "asset-state":
	case "":
		return options{}, fmt.Errorf("action is required")
	default:
		return options{}, fmt.Errorf("unknown action %q", opts.Action)
	}
	if opts.Action == "asset-state" {
		opts.Action = "asset-file-state"
	}
	if opts.Limit < 0 {
		return options{}, fmt.Errorf("limit must be >= 0")
	}
	if opts.RetentionLimit < 0 {
		return options{}, fmt.Errorf("retention-limit must be >= 0")
	}
	return opts, nil
}

func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "storage maintenance: %v\n", err)
		return 2
	}

	cfg := config.Load()
	if cfg.ConfigError != nil {
		_, _ = fmt.Fprintf(stderr, "storage maintenance: invalid configuration: %v\n", cfg.ConfigError)
		return 1
	}
	dbPath := strings.TrimSpace(opts.DBPath)
	if dbPath == "" {
		dbPath = cfg.DatabasePath
	}
	dataDir := strings.TrimSpace(opts.DataDir)
	if dataDir == "" {
		dataDir = cfg.DataDir
	}

	var store *db.DB
	if opts.Apply {
		store, err = db.Open(dbPath, dataDir)
	} else {
		store, err = db.OpenReadOnly(dbPath, dataDir)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "storage maintenance: open db: %v\n", err)
		return 1
	}
	defer func() {
		_ = store.Close()
	}()

	started := time.Now()
	nowMs := time.Now().UnixMilli()
	var result any
	switch opts.Action {
	case "x-dedupe":
		result, err = store.DedupeXMediaBySourceURL(db.XMediaDedupeOptions{
			NowMs:  nowMs,
			Limit:  opts.Limit,
			DryRun: !opts.Apply,
		})
	case "x-retention":
		result, err = store.PruneXMediaRetention(db.XMediaRetentionOptions{
			NowMs:          nowMs,
			Limit:          opts.Limit,
			RetentionLimit: opts.RetentionLimit,
			DryRun:         !opts.Apply,
		})
	case "asset-file-state":
		result, err = store.MaintainReadyAssetFileStates(db.AssetFileStateMaintenanceOptions{
			NowMs:  nowMs,
			Limit:  opts.Limit,
			DryRun: !opts.Apply,
		})
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "storage maintenance: run %s: %v\n", opts.Action, err)
		return 1
	}

	mode := "dry_run"
	if opts.Apply {
		mode = "applied"
	}
	out := report{
		Mode:       mode,
		Action:     opts.Action,
		Database:   dbPath,
		DataDir:    dataDir,
		DurationMs: time.Since(started).Milliseconds(),
		Result:     result,
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			_, _ = fmt.Fprintf(stderr, "storage maintenance: encode JSON: %v\n", err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprint(stdout, formatText(out))
	return 0
}

func formatText(r report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mode=%s\n", r.Mode)
	fmt.Fprintf(&b, "action=%s\n", r.Action)
	fmt.Fprintf(&b, "database=%s\n", r.Database)
	fmt.Fprintf(&b, "data_dir=%s\n", r.DataDir)
	switch result := r.Result.(type) {
	case db.XMediaDedupeResult:
		formatXMediaDedupe(&b, result)
	case db.XMediaRetentionResult:
		formatXMediaRetention(&b, result)
	case db.AssetFileStateMaintenanceResult:
		formatAssetFileState(&b, result)
	default:
		fmt.Fprintf(&b, "result=%v\n", result)
	}
	fmt.Fprintf(&b, "duration_ms=%d\n", r.DurationMs)
	return b.String()
}

func formatXMediaDedupe(b *strings.Builder, result db.XMediaDedupeResult) {
	fmt.Fprintf(b, "limit=%d limit_reached=%t\n", result.Limit, result.LimitReached)
	fmt.Fprintf(b, "groups=%d rows=%d rows_rewritten=%d asset_rows_updated=%d groups_without_live_file=%d\n",
		result.Groups, result.Rows, result.RowsRewritten, result.AssetRowsUpdated, result.GroupsWithoutLiveFile)
	fmt.Fprintf(b, "duplicate_paths=%d duplicate_bytes=%d\n", result.DuplicatePaths, result.DuplicateBytes)
	formatRemoval(b, result.FileRemoval)
}

func formatXMediaRetention(b *strings.Builder, result db.XMediaRetentionResult) {
	limit := fmt.Sprintf("%d", result.RetentionLimit)
	if result.RetentionLimit <= 0 {
		limit = "channel-settings"
	}
	fmt.Fprintf(b, "limit=%d limit_reached=%t retention_limit=%s\n", result.Limit, result.LimitReached, limit)
	fmt.Fprintf(b, "sources_scanned=%d sources_over_limit=%d protected_items=%d kept_items=%d pruned_items=%d\n",
		result.SourcesScanned, result.SourcesOverLimit, result.ProtectedItems, result.KeptItems, result.PrunedItems)
	fmt.Fprintf(b, "media_rows_deleted=%d asset_rows_deleted=%d jobs_marked_pruned=%d candidate_file_bytes=%d\n",
		result.MediaRowsDeleted, result.AssetRowsDeleted, result.JobsMarkedPruned, result.CandidateFileBytes)
	formatRemoval(b, result.FileRemoval)
}

func formatAssetFileState(b *strings.Builder, result db.AssetFileStateMaintenanceResult) {
	fmt.Fprintf(b, "limit=%d limit_reached=%t\n", result.Limit, result.LimitReached)
	fmt.Fprintf(b, "checked=%d missing=%d size_changed=%d updated=%d\n",
		result.Checked, result.Missing, result.SizeChanged, result.Updated)
	if len(result.ByKind) == 0 {
		return
	}
	b.WriteString("by_kind:\n")
	kinds := make([]string, 0, len(result.ByKind))
	for kind := range result.ByKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		kindResult := result.ByKind[kind]
		fmt.Fprintf(b, "  %s: checked=%d missing=%d size_changed=%d updated=%d missing_bytes=%d previous_bytes=%d actual_bytes=%d\n",
			kind,
			kindResult.Checked,
			kindResult.Missing,
			kindResult.SizeChanged,
			kindResult.Updated,
			kindResult.MissingBytes,
			kindResult.PreviousBytes,
			kindResult.ActualBytes)
	}
}

func formatRemoval(b *strings.Builder, result db.DataFileRemovalResult) {
	fmt.Fprintf(b, "file_removal: considered=%d removed=%d removed_bytes=%d still_referenced=%d missing=%d remove_errors=%d invalid_or_empty=%d duplicate_requests=%d\n",
		result.Considered,
		result.Removed,
		result.RemovedBytes,
		result.StillReferenced,
		result.Missing,
		result.RemoveErrors,
		result.InvalidOrEmpty,
		result.DuplicateRequests)
}
