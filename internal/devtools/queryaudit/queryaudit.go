package queryaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/config"

	_ "modernc.org/sqlite"
)

type Options struct {
	DBPath  string        `json:"db_path,omitempty"`
	JSON    bool          `json:"-"`
	Limit   int           `json:"limit"`
	Search  string        `json:"search"`
	Probe   string        `json:"probe,omitempty"`
	Timeout time.Duration `json:"timeout"`
	NowMs   int64         `json:"now_ms"`
}

type Report struct {
	DBPath string        `json:"db_path"`
	Limit  int           `json:"limit"`
	Probes []ProbeReport `json:"probes"`
}

type ProbeReport struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Lifecycle   string   `json:"lifecycle"`
	ElapsedMs   int64    `json:"elapsed_ms"`
	Rows        int      `json:"rows"`
	Plan        []string `json:"plan"`
	Error       string   `json:"error,omitempty"`
}

type probeSpec struct {
	name        string
	description string
	lifecycle   string
	build       func(Options) (string, []any)
}

func parseOptions(args []string) (Options, error) {
	opts := defaultOptions()
	fs := flag.NewFlagSet("query-audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.DBPath, "db", "", "database path; defaults to configured Igloo database")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON output")
	fs.IntVar(&opts.Limit, "limit", opts.Limit, "maximum rows to read for each probe")
	fs.StringVar(&opts.Search, "search", opts.Search, "search term used for search probes")
	fs.StringVar(&opts.Probe, "probe", "", "run one named probe")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "per-probe timeout")
	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}
	if fs.NArg() != 0 {
		return Options{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.Limit <= 0 {
		return Options{}, fmt.Errorf("limit must be positive")
	}
	if opts.Timeout <= 0 {
		return Options{}, fmt.Errorf("timeout must be positive")
	}
	opts.Probe = strings.TrimSpace(opts.Probe)
	if opts.Probe != "" && !knownProbe(opts.Probe) {
		return Options{}, fmt.Errorf("unknown probe %q", opts.Probe)
	}
	return opts, nil
}

func defaultOptions() Options {
	return Options{
		Limit:   50,
		Search:  "sample",
		Timeout: 5 * time.Second,
	}
}

func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "query audit: %v\n", err)
		return 2
	}
	dbPath := strings.TrimSpace(opts.DBPath)
	if dbPath == "" {
		cfg := config.Load()
		if cfg.ConfigError != nil {
			_, _ = fmt.Fprintf(stderr, "query audit: invalid configuration: %v\n", cfg.ConfigError)
			return 1
		}
		dbPath = cfg.Storage.DatabasePath()
	}
	opts.DBPath = dbPath

	report, err := ReadReport(dbPath, opts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "query audit: %v\n", err)
		return 1
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			_, _ = fmt.Fprintf(stderr, "query audit: encode JSON: %v\n", err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprint(stdout, formatText(report))
	return 0
}

func ReadReport(dbPath string, opts Options) (Report, error) {
	opts = normalizeOptions(opts)
	dbPath = filepath.Clean(dbPath)
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", dbPath))
	if err != nil {
		return Report{}, fmt.Errorf("open readonly db: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	if err := conn.Ping(); err != nil {
		return Report{}, fmt.Errorf("ping readonly db: %w", err)
	}

	report := Report{DBPath: dbPath, Limit: opts.Limit}
	for _, spec := range selectedProbes(opts.Probe) {
		report.Probes = append(report.Probes, runProbe(conn, opts, spec))
	}
	return report, nil
}

func normalizeOptions(opts Options) Options {
	def := defaultOptions()
	if opts.Limit <= 0 {
		opts.Limit = def.Limit
	}
	if opts.Search == "" {
		opts.Search = def.Search
	}
	if opts.Timeout <= 0 {
		opts.Timeout = def.Timeout
	}
	if opts.NowMs <= 0 {
		opts.NowMs = time.Now().UnixMilli()
	}
	return opts
}

func runProbe(conn *sql.DB, opts Options, spec probeSpec) ProbeReport {
	sqlText, args := spec.build(opts)
	out := ProbeReport{
		Name:        spec.name,
		Description: spec.description,
		Lifecycle:   spec.lifecycle,
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	plan, err := explainPlan(ctx, conn, sqlText, args)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Plan = plan

	started := time.Now()
	rows, err := readRows(ctx, conn, sqlText, args)
	out.ElapsedMs = time.Since(started).Milliseconds()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Rows = rows
	return out
}

func explainPlan(ctx context.Context, conn *sql.DB, sqlText string, args []any) ([]string, error) {
	rows, err := conn.QueryContext(ctx, "EXPLAIN QUERY PLAN "+sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return nil, fmt.Errorf("scan explain: %w", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate explain: %w", err)
	}
	return plan, nil
}

func readRows(ctx context.Context, conn *sql.DB, sqlText string, args []any) (int, error) {
	rows, err := conn.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return 0, fmt.Errorf("query: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	cols, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("columns: %w", err)
	}
	values := make([]any, len(cols))
	scan := make([]any, len(cols))
	for i := range values {
		scan[i] = &values[i]
	}
	count := 0
	for rows.Next() {
		if err := rows.Scan(scan...); err != nil {
			return count, fmt.Errorf("scan row: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("iterate rows: %w", err)
	}
	return count, nil
}

func formatText(report Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "db: %s\n", report.DBPath)
	fmt.Fprintf(&b, "limit: %d\n", report.Limit)
	fmt.Fprintln(&b, "query_audit:")
	for _, probe := range report.Probes {
		fmt.Fprintf(&b, "  %s:\n", probe.Name)
		fmt.Fprintf(&b, "    lifecycle: %s\n", probe.Lifecycle)
		fmt.Fprintf(&b, "    elapsed_ms: %d\n", probe.ElapsedMs)
		fmt.Fprintf(&b, "    rows: %d\n", probe.Rows)
		if probe.Error != "" {
			fmt.Fprintf(&b, "    error: %s\n", probe.Error)
		}
		fmt.Fprintln(&b, "    plan:")
		for _, detail := range probe.Plan {
			fmt.Fprintf(&b, "      - %s\n", detail)
		}
	}
	return b.String()
}

func knownProbe(name string) bool {
	for _, spec := range probeSpecs() {
		if spec.name == name {
			return true
		}
	}
	return false
}

func selectedProbes(name string) []probeSpec {
	specs := probeSpecs()
	if name == "" {
		return specs
	}
	for _, spec := range specs {
		if spec.name == name {
			return []probeSpec{spec}
		}
	}
	return nil
}

func probeSpecs() []probeSpec {
	return []probeSpec{
		{
			name:        "feed_snapshot_page",
			description: "ranked feed snapshot page read",
			lifecycle:   "derived_cache/archive/user_state",
			build: func(opts Options) (string, []any) {
				return `
					SELECT s.tweet_id, s.rank_position
					FROM feed_rank_snapshot s
					JOIN feed_items fi ON fi.tweet_id = s.tweet_id
					WHERE s.rank_position > 0
					  AND NOT EXISTS (
					    SELECT 1
					    FROM feed_seen fs
					    WHERE fs.tweet_id = fi.tweet_id
					  )
					ORDER BY s.rank_position ASC
					LIMIT ?
				`, []any{opts.Limit}
			},
		},
		{
			name:        "videos_shorts_page",
			description: "short-form video page read for followed channels",
			lifecycle:   "archive/user_state",
			build: func(opts Options) (string, []any) {
				return `
					SELECT v.video_id, v.published_at
					FROM videos v
					JOIN channel_follows cf ON cf.channel_id = v.channel_id
					WHERE (v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
					  AND COALESCE(v.source_kind, '') != 'story'
					ORDER BY v.published_at DESC, v.video_id DESC
					LIMIT ?
				`, []any{opts.Limit}
			},
		},
		{
			name:        "android_sync_media_videos",
			description: "Android sync retained media video set",
			lifecycle:   "archive/user_state",
			build: func(opts Options) (string, []any) {
				cutoff := opts.NowMs - int64(90*24*time.Hour/time.Millisecond)
				return `
					SELECT v.video_id
					FROM channel_follows cf
					JOIN videos v ON v.channel_id = cf.channel_id
					WHERE (
					    v.channel_id LIKE 'youtube_%'
					    OR v.channel_id LIKE 'tiktok_%'
					    OR v.channel_id LIKE 'instagram_%'
					  )
					  AND COALESCE(v.source_kind, '') != 'story'
					  AND (? = 0 OR COALESCE(v.published_at, 0) >= ?)

					UNION
					SELECT v.video_id
					FROM bookmarks b
					JOIN videos v ON v.video_id = b.video_id
					WHERE (
					    v.channel_id LIKE 'youtube_%'
					    OR v.channel_id LIKE 'tiktok_%'
					    OR v.channel_id LIKE 'instagram_%'
					  )

					UNION
					SELECT v.video_id
					FROM feed_likes fl
					JOIN videos v ON v.video_id = fl.tweet_id
					WHERE (
					    v.channel_id LIKE 'youtube_%'
					    OR v.channel_id LIKE 'tiktok_%'
					    OR v.channel_id LIKE 'instagram_%'
					  )
					LIMIT ?
				`, []any{cutoff, cutoff, opts.Limit}
			},
		},
		{
			name:        "asset_download_claim_candidates",
			description: "asset download lease candidate read",
			lifecycle:   "maintained_state",
			build: func(opts Options) (string, []any) {
				return `
					SELECT asset_id
					FROM assets
					WHERE (
					    state = 'queued'
					    AND (next_attempt_at_ms = 0 OR next_attempt_at_ms <= ?)
					  )
					   OR (
					    state = 'downloading'
					    AND lease_until_ms > 0
					    AND lease_until_ms <= ?
					  )
					ORDER BY attempts ASC, updated_at_ms ASC, id ASC
					LIMIT ?
				`, []any{opts.NowMs, opts.NowMs, opts.Limit}
			},
		},
		{
			name:        "channel_search",
			description: "channel FTS search read",
			lifecycle:   "archive/user_state",
			build: func(opts Options) (string, []any) {
				return `
					SELECT f.channel_id_pk
					FROM search_channels_fts f
					LEFT JOIN channels c ON c.channel_id = f.channel_id_pk
					LEFT JOIN channel_stars cs ON cs.channel_id = f.channel_id_pk
					LEFT JOIN channel_profiles cp ON cp.channel_id = f.channel_id_pk AND cp.tombstone = 0
					WHERE search_channels_fts MATCH ?
					ORDER BY rank
					LIMIT ?
				`, []any{compileSearchFTSQuery(opts.Search), opts.Limit}
			},
		},
		{
			name:        "video_search",
			description: "video FTS search read",
			lifecycle:   "archive",
			build: func(opts Options) (string, []any) {
				return `
					SELECT f.video_id_pk
					FROM search_videos_fts f
					LEFT JOIN videos v ON v.video_id = f.video_id_pk
					LEFT JOIN channels c ON c.channel_id = v.channel_id
					WHERE search_videos_fts MATCH ?
					ORDER BY rank
					LIMIT ?
				`, []any{compileSearchFTSQuery(opts.Search), opts.Limit}
			},
		},
	}
}

func compileSearchFTSQuery(q string) string {
	terms := strings.Fields(strings.TrimSpace(q))
	if len(terms) == 0 {
		return `""`
	}
	var parts []string
	for _, term := range terms {
		term = strings.ReplaceAll(term, `"`, `""`)
		parts = append(parts, `"`+term+`"*`)
	}
	return strings.Join(parts, " AND ")
}
