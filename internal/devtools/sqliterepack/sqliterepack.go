package sqliterepack

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/screwys/igloo/internal/config"

	_ "modernc.org/sqlite"
)

const minVacuumReserveBytes int64 = 512 << 20

type options struct {
	DBPath     string
	VacuumInto string
	JSON       bool
}

type report struct {
	DBPath               string `json:"db_path"`
	WALPath              string `json:"wal_path"`
	DBBytes              int64  `json:"db_bytes"`
	WALBytes             int64  `json:"wal_bytes"`
	PageSize             int64  `json:"page_size"`
	PageCount            int64  `json:"page_count"`
	UsedPages            int64  `json:"used_pages"`
	FreelistCount        int64  `json:"freelist_count"`
	ReclaimableBytes     int64  `json:"reclaimable_bytes"`
	CompactEstimateBytes int64  `json:"compact_estimate_bytes"`
	VacuumInto           string `json:"vacuum_into,omitempty"`
	OutputBytes          int64  `json:"output_bytes,omitempty"`
	DurationMs           int64  `json:"duration_ms,omitempty"`
}

func parseOptions(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("sqlite-repack", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.DBPath, "db", "", "database path; defaults to Igloo config")
	fs.StringVar(&opts.VacuumInto, "vacuum-into", "", "absolute output path for a compact SQLite copy")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON output")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sqlite repack: %v\n", err)
		return 2
	}

	dbPath := opts.DBPath
	if dbPath == "" {
		cfg := config.Load()
		if cfg.ConfigError != nil {
			_, _ = fmt.Fprintf(stderr, "sqlite repack: invalid configuration: %v\n", cfg.ConfigError)
			return 1
		}
		dbPath = cfg.DatabasePath
	}

	rep, err := readReport(dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sqlite repack: %v\n", err)
		return 1
	}

	if opts.VacuumInto != "" {
		started := time.Now()
		if err := vacuumInto(dbPath, opts.VacuumInto, rep.CompactEstimateBytes); err != nil {
			_, _ = fmt.Fprintf(stderr, "sqlite repack: %v\n", err)
			return 1
		}
		rep.VacuumInto = opts.VacuumInto
		rep.DurationMs = time.Since(started).Milliseconds()
		if info, err := os.Stat(opts.VacuumInto); err == nil {
			rep.OutputBytes = info.Size()
		}
	}

	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			_, _ = fmt.Fprintf(stderr, "sqlite repack: encode JSON: %v\n", err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprint(stdout, formatText(rep))
	return 0
}

func readReport(dbPath string) (report, error) {
	dbPath = filepath.Clean(dbPath)
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", dbPath))
	if err != nil {
		return report{}, fmt.Errorf("open readonly db: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	if err := conn.Ping(); err != nil {
		return report{}, fmt.Errorf("ping readonly db: %w", err)
	}

	var rep report
	rep.DBPath = dbPath
	rep.WALPath = dbPath + "-wal"
	if info, err := os.Stat(dbPath); err == nil {
		rep.DBBytes = info.Size()
	}
	if info, err := os.Stat(rep.WALPath); err == nil {
		rep.WALBytes = info.Size()
	}
	if err := conn.QueryRow(`PRAGMA page_size`).Scan(&rep.PageSize); err != nil {
		return report{}, fmt.Errorf("read page_size: %w", err)
	}
	if err := conn.QueryRow(`PRAGMA page_count`).Scan(&rep.PageCount); err != nil {
		return report{}, fmt.Errorf("read page_count: %w", err)
	}
	if err := conn.QueryRow(`PRAGMA freelist_count`).Scan(&rep.FreelistCount); err != nil {
		return report{}, fmt.Errorf("read freelist_count: %w", err)
	}
	rep.UsedPages = rep.PageCount - rep.FreelistCount
	if rep.UsedPages < 0 {
		rep.UsedPages = 0
	}
	rep.ReclaimableBytes = rep.FreelistCount * rep.PageSize
	rep.CompactEstimateBytes = rep.UsedPages * rep.PageSize
	return rep, nil
}

func vacuumInto(dbPath, outputPath string, compactEstimateBytes int64) error {
	outputPath = filepath.Clean(outputPath)
	if !filepath.IsAbs(outputPath) {
		return fmt.Errorf("vacuum-into path must be absolute: %s", outputPath)
	}
	if filepath.Clean(dbPath) == outputPath {
		return fmt.Errorf("vacuum-into path must not be the live database path")
	}
	if _, err := os.Stat(outputPath); err == nil {
		return fmt.Errorf("vacuum-into output already exists: %s", outputPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat vacuum-into output: %w", err)
	}
	parent := filepath.Dir(outputPath)
	if info, err := os.Stat(parent); err != nil {
		return fmt.Errorf("stat vacuum-into directory: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("vacuum-into parent is not a directory: %s", parent)
	}
	if available, err := availableBytes(parent); err == nil && available < compactEstimateBytes+minVacuumReserveBytes {
		return fmt.Errorf(
			"not enough free space in %s: available=%s required=%s",
			parent,
			formatSize(available),
			formatSize(compactEstimateBytes+minVacuumReserveBytes),
		)
	}

	conn, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=journal_mode(wal)&_pragma=busy_timeout(30000)", filepath.Clean(dbPath)))
	if err != nil {
		return fmt.Errorf("open writable db: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	if err := conn.Ping(); err != nil {
		return fmt.Errorf("ping writable db: %w", err)
	}
	if _, err := conn.Exec(`VACUUM INTO ?`, outputPath); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}

func availableBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func formatText(rep report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "db: %s\n", rep.DBPath)
	fmt.Fprintf(&b, "db_size: %s\n", formatSize(rep.DBBytes))
	fmt.Fprintf(&b, "wal_size: %s\n", formatSize(rep.WALBytes))
	fmt.Fprintf(&b, "page_size: %s\n", formatSize(rep.PageSize))
	fmt.Fprintf(&b, "pages: total=%d used=%d freelist=%d\n", rep.PageCount, rep.UsedPages, rep.FreelistCount)
	fmt.Fprintf(&b, "reclaimable_freelist: %s\n", formatSize(rep.ReclaimableBytes))
	fmt.Fprintf(&b, "compact_estimate: %s\n", formatSize(rep.CompactEstimateBytes))
	if rep.VacuumInto == "" {
		b.WriteString("dry_run: true\n")
		return b.String()
	}
	fmt.Fprintf(&b, "vacuum_into: %s\n", rep.VacuumInto)
	fmt.Fprintf(&b, "output_size: %s\n", formatSize(rep.OutputBytes))
	fmt.Fprintf(&b, "duration_ms: %d\n", rep.DurationMs)
	return b.String()
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
