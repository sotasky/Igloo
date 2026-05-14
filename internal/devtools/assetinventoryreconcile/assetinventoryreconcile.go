package assetinventoryreconcile

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
	Limit   int
	Apply   bool
	JSON    bool
	DBPath  string
	DataDir string
}

type report struct {
	Mode       string                           `json:"mode"`
	Database   string                           `json:"database"`
	DataDir    string                           `json:"data_dir"`
	DurationMs int64                            `json:"duration_ms"`
	Result     db.AssetInventoryReconcileResult `json:"result"`
}

func parseOptions(args []string) (options, error) {
	opts := options{
		Limit: 1000,
	}

	fs := flag.NewFlagSet("asset-inventory-reconcile", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&opts.Limit, "limit", opts.Limit, "maximum missing assets rows to reconcile")
	fs.BoolVar(&opts.Apply, "apply", false, "write changes; without this flag the command only reports")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON output")
	fs.StringVar(&opts.DBPath, "db", "", "database path; defaults to configured Igloo database")
	fs.StringVar(&opts.DataDir, "data-dir", "", "data directory; defaults to configured Igloo data dir")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if opts.Limit <= 0 {
		return options{}, fmt.Errorf("limit must be positive")
	}
	return opts, nil
}

func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "asset inventory reconcile: %v\n", err)
		return 2
	}

	cfg := config.Load()
	if cfg.ConfigError != nil {
		_, _ = fmt.Fprintf(stderr, "asset inventory reconcile: invalid configuration: %v\n", cfg.ConfigError)
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

	started := time.Now()
	var store *db.DB
	if opts.Apply {
		store, err = db.Open(dbPath, dataDir)
	} else {
		store, err = db.OpenReadOnly(dbPath, dataDir)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "asset inventory reconcile: open db: %v\n", err)
		return 1
	}
	defer func() {
		_ = store.Close()
	}()

	result, err := store.ReconcileAssetInventoryFromExistingPaths(db.AssetInventoryReconcileOptions{
		NowMs:  time.Now().UnixMilli(),
		Limit:  opts.Limit,
		DryRun: !opts.Apply,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "asset inventory reconcile: run: %v\n", err)
		return 1
	}
	mode := "dry_run"
	if opts.Apply {
		mode = "applied"
	}
	out := report{
		Mode:       mode,
		Database:   dbPath,
		DataDir:    dataDir,
		DurationMs: time.Since(started).Milliseconds(),
		Result:     result,
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			_, _ = fmt.Fprintf(stderr, "asset inventory reconcile: encode JSON: %v\n", err)
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
	fmt.Fprintf(&b, "database=%s\n", r.Database)
	fmt.Fprintf(&b, "data_dir=%s\n", r.DataDir)
	fmt.Fprintf(&b, "limit=%d limit_reached=%t\n", r.Result.Limit, r.Result.LimitReached)
	fmt.Fprintf(
		&b,
		"candidates=%d written=%d skipped_existing=%d\n",
		r.Result.Candidates,
		r.Result.Written,
		r.Result.SkippedExisting,
	)
	if len(r.Result.ByKind) > 0 {
		b.WriteString("by_kind:\n")
		kinds := make([]string, 0, len(r.Result.ByKind))
		for kind := range r.Result.ByKind {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			kindResult := r.Result.ByKind[kind]
			fmt.Fprintf(
				&b,
				"  %s: candidates=%d written=%d ready=%d queued=%d server_missing=%d\n",
				kind,
				kindResult.Candidates,
				kindResult.Written,
				kindResult.Ready,
				kindResult.Queued,
				kindResult.ServerMissing,
			)
		}
	}
	fmt.Fprintf(&b, "duration_ms=%d\n", r.DurationMs)
	return b.String()
}
