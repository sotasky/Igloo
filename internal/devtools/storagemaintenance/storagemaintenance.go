package storagemaintenance

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/storage"
)

type options struct {
	Action         string
	Limit          int
	RetentionLimit int
	Apply          bool
	JSON           bool
	DBPath         string
	DataDir        string
	MediaDir       string
}

type report struct {
	Mode       string `json:"mode"`
	Action     string `json:"action"`
	Database   string `json:"database"`
	DataDir    string `json:"data_dir"`
	MediaDir   string `json:"media_dir"`
	DurationMs int64  `json:"duration_ms"`
	Result     any    `json:"result"`
}

func parseOptions(args []string) (options, error) {
	opts := options{
		Limit: 1000,
	}
	fs := flag.NewFlagSet("storage-maintenance", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.Action, "action", "", "maintenance action: x-retention")
	fs.IntVar(&opts.Limit, "limit", opts.Limit, "maximum X sources to inspect; 0 means unlimited")
	fs.IntVar(&opts.RetentionLimit, "retention-limit", 0, "override X media rows kept per followed source; 0 uses each channel setting")
	fs.BoolVar(&opts.Apply, "apply", false, "write DB changes and remove unreferenced files; without this flag the command only reports")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON output")
	fs.StringVar(&opts.DBPath, "db", "", "database path; defaults to configured Igloo database")
	fs.StringVar(&opts.DataDir, "data-dir", "", "data directory; defaults to configured Igloo data dir")
	fs.StringVar(&opts.MediaDir, "media-dir", "", "media directory; defaults to configured Igloo media dir")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	opts.Action = strings.TrimSpace(opts.Action)
	switch opts.Action {
	case "x-retention":
	case "":
		return options{}, fmt.Errorf("action is required")
	default:
		return options{}, fmt.Errorf("unknown action %q", opts.Action)
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
	layout := cfg.Storage
	if strings.TrimSpace(opts.DataDir) != "" || strings.TrimSpace(opts.MediaDir) != "" {
		stateRoot := strings.TrimSpace(opts.DataDir)
		if stateRoot == "" {
			stateRoot = cfg.Storage.StateRoot()
		}
		mediaRoot := strings.TrimSpace(opts.MediaDir)
		var layoutErr error
		layout, layoutErr = storage.New(stateRoot, mediaRoot)
		if layoutErr != nil {
			_, _ = fmt.Fprintf(stderr, "storage maintenance: storage layout: %v\n", layoutErr)
			return 1
		}
	}
	dbPath := strings.TrimSpace(opts.DBPath)
	if dbPath == "" {
		dbPath = layout.DatabasePath()
	}

	var store *db.DB
	if opts.Apply {
		store, err = db.OpenLayoutPath(dbPath, layout)
	} else {
		store, err = db.OpenReadOnlyLayout(dbPath, layout)
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
	case "x-retention":
		result, err = store.PruneXMediaRetention(db.XMediaRetentionOptions{
			NowMs:          nowMs,
			Limit:          opts.Limit,
			RetentionLimit: opts.RetentionLimit,
			DryRun:         !opts.Apply,
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
		DataDir:    layout.StateRoot(),
		MediaDir:   layout.MediaRoot(),
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
	fmt.Fprintf(&b, "media_dir=%s\n", r.MediaDir)
	switch result := r.Result.(type) {
	case db.XMediaRetentionResult:
		formatXMediaRetention(&b, result)
	default:
		fmt.Fprintf(&b, "result=%v\n", result)
	}
	fmt.Fprintf(&b, "duration_ms=%d\n", r.DurationMs)
	return b.String()
}

func formatXMediaRetention(b *strings.Builder, result db.XMediaRetentionResult) {
	limit := fmt.Sprintf("%d", result.RetentionLimit)
	if result.RetentionLimit <= 0 {
		limit = "channel-settings"
	}
	fmt.Fprintf(b, "limit=%d limit_reached=%t retention_limit=%s\n", result.Limit, result.LimitReached, limit)
	fmt.Fprintf(b, "sources_scanned=%d sources_over_limit=%d protected_items=%d kept_items=%d pruned_items=%d\n",
		result.SourcesScanned, result.SourcesOverLimit, result.ProtectedItems, result.KeptItems, result.PrunedItems)
	fmt.Fprintf(b, "assets_pruned=%d candidate_file_bytes=%d\n", result.AssetsPruned, result.CandidateFileBytes)
	formatRemoval(b, result.FileRemoval)
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
