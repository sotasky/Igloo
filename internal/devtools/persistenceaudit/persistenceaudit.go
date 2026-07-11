package persistenceaudit

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/screwys/igloo/internal/config"
	igloodb "github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/persistencebudget"

	_ "modernc.org/sqlite"
)

type options struct {
	DBPath string
	JSON   bool
	Top    int
}

type Report struct {
	DBPath       string                      `json:"db_path"`
	DBBytes      int64                       `json:"db_bytes"`
	WALBytes     int64                       `json:"wal_bytes"`
	PageSize     int64                       `json:"page_size"`
	PageCount    int64                       `json:"page_count"`
	UsedPages    int64                       `json:"used_pages"`
	Freelist     int64                       `json:"freelist_count"`
	Groups       []LifecycleGroup            `json:"groups"`
	Warnings     []persistencebudget.Warning `json:"warnings,omitempty"`
	Unclassified []TableReport               `json:"unclassified,omitempty"`
}

type LifecycleGroup struct {
	Lifecycle string        `json:"lifecycle"`
	Tables    int           `json:"tables"`
	Rows      int64         `json:"rows"`
	Bytes     int64         `json:"bytes"`
	TopTables []TableReport `json:"top_tables"`
}

type TableReport struct {
	Name      string `json:"name"`
	Lifecycle string `json:"lifecycle"`
	Rows      int64  `json:"rows"`
	Bytes     int64  `json:"bytes"`
}

func parseOptions(args []string) (options, error) {
	opts := options{Top: 5}
	fs := flag.NewFlagSet("persistence-audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.DBPath, "db", "", "database path; defaults to configured Igloo database")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON output")
	fs.IntVar(&opts.Top, "top", opts.Top, "number of top tables to print per lifecycle")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.Top < 0 {
		return options{}, fmt.Errorf("top must be non-negative")
	}
	return opts, nil
}

func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "persistence audit: %v\n", err)
		return 2
	}

	dbPath := strings.TrimSpace(opts.DBPath)
	if dbPath == "" {
		cfg := config.Load()
		if cfg.ConfigError != nil {
			_, _ = fmt.Fprintf(stderr, "persistence audit: invalid configuration: %v\n", cfg.ConfigError)
			return 1
		}
		dbPath = cfg.Storage.DatabasePath()
	}

	report, err := ReadReport(dbPath, opts.Top)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "persistence audit: %v\n", err)
		return 1
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			_, _ = fmt.Fprintf(stderr, "persistence audit: encode JSON: %v\n", err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprint(stdout, formatText(report))
	return 0
}

func ReadReport(dbPath string, top int) (Report, error) {
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

	report := Report{DBPath: dbPath}
	if info, err := os.Stat(dbPath); err == nil {
		report.DBBytes = info.Size()
	}
	if info, err := os.Stat(dbPath + "-wal"); err == nil {
		report.WALBytes = info.Size()
	}
	if err := conn.QueryRow(`PRAGMA page_size`).Scan(&report.PageSize); err != nil {
		return Report{}, fmt.Errorf("read page_size: %w", err)
	}
	if err := conn.QueryRow(`PRAGMA page_count`).Scan(&report.PageCount); err != nil {
		return Report{}, fmt.Errorf("read page_count: %w", err)
	}
	if err := conn.QueryRow(`PRAGMA freelist_count`).Scan(&report.Freelist); err != nil {
		return Report{}, fmt.Errorf("read freelist_count: %w", err)
	}
	report.UsedPages = report.PageCount - report.Freelist
	if report.UsedPages < 0 {
		report.UsedPages = 0
	}

	tables, err := userTables(conn)
	if err != nil {
		return Report{}, err
	}
	bytesByTable, err := tableStorageBytes(conn)
	if err != nil {
		return Report{}, err
	}
	report.Groups, report.Unclassified = lifecycleGroups(conn, tables, bytesByTable, top)
	report.Warnings = persistencebudget.Evaluate(budgetGroups(report.Groups))
	return report, nil
}

func budgetGroups(groups []LifecycleGroup) []persistencebudget.LifecycleGroup {
	out := make([]persistencebudget.LifecycleGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, persistencebudget.LifecycleGroup{
			Lifecycle: group.Lifecycle,
			Tables:    group.Tables,
			Rows:      group.Rows,
			Bytes:     group.Bytes,
		})
	}
	return out
}

func lifecycleGroups(conn *sql.DB, tables []string, bytesByTable map[string]int64, top int) ([]LifecycleGroup, []TableReport) {
	order := []string{
		"archive",
		"maintained_state",
		"user_state",
		"queue",
		"derived_cache",
		"diagnostic",
		"security_state",
		"legacy_migration",
		"unclassified",
	}
	byLifecycle := make(map[string][]TableReport, len(order))
	for _, table := range tables {
		lifecycle, ok := igloodb.SchemaTableLifecycle(table)
		if !ok {
			lifecycle = "unclassified"
		}
		byLifecycle[lifecycle] = append(byLifecycle[lifecycle], TableReport{
			Name:      table,
			Lifecycle: lifecycle,
			Rows:      tableRowCount(conn, table),
			Bytes:     bytesByTable[table],
		})
	}

	var groups []LifecycleGroup
	var unclassified []TableReport
	for _, lifecycle := range order {
		reports := byLifecycle[lifecycle]
		if len(reports) == 0 {
			continue
		}
		sortTables(reports)
		group := LifecycleGroup{
			Lifecycle: lifecycle,
			Tables:    len(reports),
			TopTables: topTables(reports, top),
		}
		for _, table := range reports {
			group.Rows += table.Rows
			group.Bytes += table.Bytes
		}
		groups = append(groups, group)
		if lifecycle == "unclassified" {
			unclassified = append(unclassified, reports...)
		}
	}
	return groups, unclassified
}

func userTables(conn *sql.DB) ([]string, error) {
	rows, err := conn.Query(`
		SELECT name
		FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("query user tables: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf("scan user table: %w", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user tables: %w", err)
	}
	return tables, nil
}

func tableStorageBytes(conn *sql.DB) (map[string]int64, error) {
	rows, err := conn.Query(`
		SELECT m.tbl_name, COALESCE(SUM(s.pgsize), 0) AS bytes
		FROM sqlite_master m
		LEFT JOIN dbstat s ON s.name = m.name
		WHERE m.type IN ('table', 'index')
		  AND m.tbl_name NOT LIKE 'sqlite_%'
		GROUP BY m.tbl_name
	`)
	if err != nil {
		return nil, fmt.Errorf("query table storage bytes: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	out := make(map[string]int64)
	for rows.Next() {
		var table string
		var bytes int64
		if err := rows.Scan(&table, &bytes); err != nil {
			return nil, fmt.Errorf("scan table storage bytes: %w", err)
		}
		out[table] = bytes
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate table storage bytes: %w", err)
	}
	return out, nil
}

func tableRowCount(conn *sql.DB, table string) int64 {
	var count int64
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s`, quoteSQLiteIdent(table))
	if err := conn.QueryRow(query).Scan(&count); err != nil {
		return 0
	}
	return count
}

func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func topTables(tables []TableReport, limit int) []TableReport {
	if limit > len(tables) {
		limit = len(tables)
	}
	if limit <= 0 {
		return nil
	}
	out := make([]TableReport, limit)
	copy(out, tables[:limit])
	return out
}

func sortTables(tables []TableReport) {
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Bytes != tables[j].Bytes {
			return tables[i].Bytes > tables[j].Bytes
		}
		if tables[i].Rows != tables[j].Rows {
			return tables[i].Rows > tables[j].Rows
		}
		return tables[i].Name < tables[j].Name
	})
}

func formatText(report Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "db: %s\n", report.DBPath)
	fmt.Fprintf(&b, "db_size: %s\n", formatSize(report.DBBytes))
	fmt.Fprintf(&b, "wal_size: %s\n", formatSize(report.WALBytes))
	fmt.Fprintf(&b, "page_size: %s\n", formatSize(report.PageSize))
	fmt.Fprintf(&b, "pages: total=%d used=%d freelist=%d\n", report.PageCount, report.UsedPages, report.Freelist)
	b.WriteString("lifecycles:\n")
	for _, group := range report.Groups {
		fmt.Fprintf(&b, "  %-18s tables=%d rows=%d size=%s\n", group.Lifecycle+":", group.Tables, group.Rows, formatSize(group.Bytes))
		for _, table := range group.TopTables {
			fmt.Fprintf(&b, "    %-30s rows=%d size=%s\n", table.Name, table.Rows, formatSize(table.Bytes))
		}
	}
	if len(report.Warnings) > 0 {
		b.WriteString("warnings:\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "  - %s %s/%s: %s\n", warning.Severity, warning.Lifecycle, warning.Code, warning.Message)
		}
	}
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
