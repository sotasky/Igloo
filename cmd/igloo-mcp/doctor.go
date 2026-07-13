package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	igloodb "github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/persistencebudget"
	"github.com/screwys/igloo/internal/storage"
)

func doctorStatus() (string, error) {
	conn, err := getServerDB()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("=== Igloo Doctor ===\n\n")
	writeDoctorStorageLayout(&sb)
	writeDoctorDBFiles(&sb)
	writeDoctorSQLiteStorage(&sb, conn)
	writeDoctorDBStat(&sb, conn)
	writeDoctorPersistenceLifecycle(&sb, conn)
	writeDoctorAndroidSync(&sb, conn)
	writeDoctorQueues(&sb, conn)
	writeDoctorProfileReadiness(&sb, conn)
	writeDoctorAssetInventory(&sb, conn)
	writeDoctorDownloaderFailures(&sb, conn)
	writeDoctorAndroidSyncClientFailures(&sb)
	writeDoctorRecentErrors(&sb)
	return strings.TrimRight(sb.String(), "\n"), nil
}

func writeDoctorStorageLayout(sb *strings.Builder) {
	stateRoot := filepath.Dir(getDBPath())
	configuredMediaRoot := strings.TrimSpace(os.Getenv("IGLOO_MEDIA_DIR"))
	layout, err := storage.New(stateRoot, configuredMediaRoot)
	sb.WriteString("Storage layout:\n")
	if err != nil {
		fmt.Fprintf(sb, "  invalid: %v\n\n", err)
		return
	}
	mode := "co-located"
	if filepath.Clean(layout.MediaRoot()) != filepath.Join(filepath.Clean(layout.StateRoot()), "media") {
		mode = "external"
	}
	fmt.Fprintf(sb, "  state_root: %s\n", layout.StateRoot())
	fmt.Fprintf(sb, "  media_root: %s (%s)\n", layout.MediaRoot(), mode)
	if _, err := layout.Path("media/.doctor-readiness"); err != nil {
		fmt.Fprintf(sb, "  media_ready: false (%v)\n\n", err)
		return
	}
	sb.WriteString("  media_ready: true\n\n")
}

func writeDoctorDBFiles(sb *strings.Builder) {
	dbPath := getDBPath()
	fmt.Fprintf(sb, "Database files:\n")
	for _, path := range []string{dbPath, dbPath + "-wal"} {
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(sb, "  %-12s %s\n", filepath.Base(path)+":", formatSize(info.Size()))
		} else {
			fmt.Fprintf(sb, "  %-12s missing\n", filepath.Base(path)+":")
		}
	}
	sb.WriteString("\n")
}

func writeDoctorSQLiteStorage(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("SQLite storage:\n")
	var pageSize, pageCount, freelistCount int64
	if err := conn.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	if err := conn.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	if err := conn.QueryRow(`PRAGMA freelist_count`).Scan(&freelistCount); err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	usedPages := pageCount - freelistCount
	if usedPages < 0 {
		usedPages = 0
	}
	reclaimableBytes := freelistCount * pageSize
	fmt.Fprintf(sb, "  page_size: %s\n", formatSize(pageSize))
	fmt.Fprintf(sb, "  pages: total=%d used=%d freelist=%d\n", pageCount, usedPages, freelistCount)
	fmt.Fprintf(sb, "  reclaimable freelist: %s", formatSize(reclaimableBytes))
	if info, err := os.Stat(getDBPath()); err == nil && info.Size() > 0 {
		fmt.Fprintf(sb, " (%.1f%% of database file)", float64(reclaimableBytes)*100/float64(info.Size()))
	}
	sb.WriteString("\n\n")
}

func writeDoctorDBStat(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Top dbstat tables/indexes:\n")
	rows, err := conn.Query(`
		SELECT name, SUM(pgsize) AS bytes
		FROM dbstat
		GROUP BY name
		ORDER BY bytes DESC
		LIMIT 10
	`)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	wrote := false
	for rows.Next() {
		var name string
		var bytes int64
		if err := rows.Scan(&name, &bytes); err != nil {
			continue
		}
		wrote = true
		fmt.Fprintf(sb, "  %-32s %s\n", name, formatSize(bytes))
	}
	if !wrote {
		sb.WriteString("  none\n")
	}
	sb.WriteString("\n")
}

type doctorPersistenceTable struct {
	name  string
	rows  int64
	bytes int64
}

type doctorPersistenceLifecycle struct {
	name   string
	tables []doctorPersistenceTable
	rows   int64
	bytes  int64
}

func writeDoctorPersistenceLifecycle(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Persistence lifecycle:\n")
	groups, err := doctorPersistenceLifecycles(conn)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	for _, group := range groups {
		if len(group.tables) == 0 {
			continue
		}
		fmt.Fprintf(sb, "  %-18s tables=%d rows=%d size=%s\n", group.name+":", len(group.tables), group.rows, formatSize(group.bytes))
		sort.Slice(group.tables, func(i, j int) bool {
			if group.tables[i].bytes != group.tables[j].bytes {
				return group.tables[i].bytes > group.tables[j].bytes
			}
			if group.tables[i].rows != group.tables[j].rows {
				return group.tables[i].rows > group.tables[j].rows
			}
			return group.tables[i].name < group.tables[j].name
		})
		limit := len(group.tables)
		if limit > 3 {
			limit = 3
		}
		for _, table := range group.tables[:limit] {
			fmt.Fprintf(sb, "    %-30s rows=%d size=%s\n", table.name, table.rows, formatSize(table.bytes))
		}
	}
	warnings := persistencebudget.Evaluate(doctorBudgetGroups(groups))
	if len(warnings) > 0 {
		sb.WriteString("  warnings:\n")
		for _, warning := range warnings {
			fmt.Fprintf(sb, "    - %s %s/%s: %s\n", warning.Severity, warning.Lifecycle, warning.Code, warning.Message)
		}
	}
	sb.WriteString("\n")
}

func doctorBudgetGroups(groups []doctorPersistenceLifecycle) []persistencebudget.LifecycleGroup {
	out := make([]persistencebudget.LifecycleGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, persistencebudget.LifecycleGroup{
			Lifecycle: group.name,
			Tables:    len(group.tables),
			Rows:      group.rows,
			Bytes:     group.bytes,
		})
	}
	return out
}

func doctorPersistenceLifecycles(conn *sql.DB) ([]doctorPersistenceLifecycle, error) {
	tables, err := doctorUserTables(conn)
	if err != nil {
		return nil, err
	}
	bytesByTable, err := doctorTableStorageBytes(conn)
	if err != nil {
		return nil, err
	}

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
	byLifecycle := make(map[string]*doctorPersistenceLifecycle, len(order))
	for _, name := range order {
		byLifecycle[name] = &doctorPersistenceLifecycle{name: name}
	}
	for _, table := range tables {
		lifecycle, ok := igloodb.SchemaTableLifecycle(table)
		if !ok {
			lifecycle = "unclassified"
		}
		group := byLifecycle[lifecycle]
		if group == nil {
			group = &doctorPersistenceLifecycle{name: lifecycle}
			byLifecycle[lifecycle] = group
			order = append(order, lifecycle)
		}
		rowCount := doctorTableRowCount(conn, table)
		bytes := bytesByTable[table]
		group.tables = append(group.tables, doctorPersistenceTable{
			name:  table,
			rows:  rowCount,
			bytes: bytes,
		})
		group.rows += rowCount
		group.bytes += bytes
	}

	groups := make([]doctorPersistenceLifecycle, 0, len(order))
	for _, name := range order {
		if group := byLifecycle[name]; group != nil {
			groups = append(groups, *group)
		}
	}
	return groups, nil
}

func doctorUserTables(conn *sql.DB) ([]string, error) {
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

func doctorTableStorageBytes(conn *sql.DB) (map[string]int64, error) {
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

func doctorTableRowCount(conn *sql.DB, table string) int64 {
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

func writeDoctorAndroidSync(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Android sync:\n")
	var epoch string
	var revision, heads int64
	if err := conn.QueryRow(`
		SELECT epoch, revision, (SELECT COUNT(*) FROM android_sync_heads)
		FROM android_sync_clock WHERE id = 1
	`).Scan(&epoch, &revision, &heads); err != nil {
		fmt.Fprintf(sb, "  stream unavailable: %v\n\n", err)
		return
	}
	fmt.Fprintf(sb, "  stream: epoch=%s revision=%d compact_heads=%d\n", epoch, revision, heads)
	var total, ready, missing int64
	if err := conn.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(current.published_revision > 0 AND current.file_path != ''), 0),
		       COALESCE(SUM(current.published_revision = 0 AND desired.job_state IN ('server_missing', 'permanent_missing')), 0)
		FROM assets a
		JOIN media_objects current ON current.object_id = a.object_id
		JOIN media_objects desired ON desired.object_id = a.desired_object_id
		WHERE a.lifecycle_state != 'pruned'
	`).Scan(&total, &ready, &missing); err == nil {
		fmt.Fprintf(sb, "  canonical assets: total=%d ready=%d missing=%d\n", total, ready, missing)
	}
	var cursor sql.NullString
	var reportedAt, verified, pending, deviceMissing sql.NullInt64
	if err := conn.QueryRow(`
		SELECT cursor, reported_at_ms, verified_assets, pending_assets, missing_assets
		FROM android_sync_health_reports
		ORDER BY reported_at_ms DESC, id DESC
		LIMIT 1
	`).Scan(&cursor, &reportedAt, &verified, &pending, &deviceMissing); err == nil && cursor.Valid {
		fmt.Fprintf(sb, "  device: cursor=%s reported=%s verified=%d pending=%d missing=%d\n",
			compactLong(cursor.String, 80), formatMillis(reportedAt.Int64), verified.Int64, pending.Int64, deviceMissing.Int64)
	}
	sb.WriteString("\n")
}

func writeDoctorQueues(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Queue counts:\n")
	for _, table := range []string{"download_queue", "translation_jobs"} {
		parts := doctorStatusCounts(conn, table, "status", "")
		if len(parts) == 0 {
			parts = []string{"empty=0"}
		}
		fmt.Fprintf(sb, "  %-18s %s\n", table+":", strings.Join(parts, ", "))
	}
	parts := doctorAssetStatusCounts(conn, "")
	if len(parts) == 0 {
		parts = []string{"empty=0"}
	}
	fmt.Fprintf(sb, "  %-18s %s\n", "assets:", strings.Join(parts, ", "))
	sb.WriteString("\n")
}

func writeDoctorProfileReadiness(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Profile/media readiness:\n")
	var profiles, tombstones int
	_ = conn.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN COALESCE(tombstone, 0) != 0 THEN 1 ELSE 0 END), 0)
		FROM channel_profiles
	`).Scan(&profiles, &tombstones)
	fmt.Fprintf(sb, "  channel_profiles: total=%d tombstones=%d\n", profiles, tombstones)
	var pendingJobs, leasedJobs, failedJobs int
	_ = conn.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN completed_revision < requested_revision THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN lease_owner != '' THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN attempts > 0 AND last_error != '' THEN 1 ELSE 0 END), 0)
		FROM profile_jobs
	`).Scan(&pendingJobs, &leasedJobs, &failedJobs)
	fmt.Fprintf(sb, "  profile_jobs: pending=%d leased=%d failed=%d\n", pendingJobs, leasedJobs, failedJobs)

	avatarStates := doctorAssetStatusCounts(conn, "a.owner_kind = 'channel' AND a.asset_kind = 'avatar'")
	if len(avatarStates) == 0 {
		avatarStates = []string{"empty=0"}
	}
	bannerStates := doctorAssetStatusCounts(conn, "a.owner_kind = 'channel' AND a.asset_kind = 'banner'")
	if len(bannerStates) == 0 {
		bannerStates = []string{"empty=0"}
	}
	fmt.Fprintf(sb, "  avatar assets: %s\n", strings.Join(avatarStates, ", "))
	fmt.Fprintf(sb, "  banner assets: %s\n\n", strings.Join(bannerStates, ", "))
}

func writeDoctorAssetInventory(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Asset inventory:\n")
	parts := doctorAssetStatusCounts(conn, "")
	if len(parts) == 0 {
		parts = []string{"empty=0"}
	}
	fmt.Fprintf(sb, "  inventory states: %s\n", strings.Join(parts, ", "))
	activeLeases, expiredLeases := doctorAssetLeaseCounts(conn, time.Now().UnixMilli())
	fmt.Fprintf(sb, "  asset leases: active_downloading=%d expired_downloading=%d\n", activeLeases, expiredLeases)
	for _, kind := range []string{
		"post_media", "post_audio", "video_stream", "post_thumbnail",
		"dearrow_thumbnail", "subtitle", "avatar", "banner",
		"preview_track_json", "preview_sprite",
	} {
		states := doctorAssetStatusCounts(conn, fmt.Sprintf("a.asset_kind = '%s'", kind))
		if len(states) == 0 {
			states = []string{"empty=0"}
		}
		fmt.Fprintf(sb, "  %-20s %s\n", kind+":", strings.Join(states, ", "))
	}
	sb.WriteString("\n")
}

func doctorAssetLeaseCounts(conn *sql.DB, nowMs int64) (active int, expired int) {
	_ = conn.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN COALESCE(lease_until_ms, 0) > ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(lease_until_ms, 0) > 0 AND lease_until_ms <= ? THEN 1 ELSE 0 END), 0)
		FROM media_objects
		WHERE job_state = 'downloading'
	`, nowMs, nowMs).Scan(&active, &expired)
	return active, expired
}

func writeDoctorDownloaderFailures(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Downloader failures:\n")
	parts := doctorStatusCounts(conn, "downloader_operations", "error_kind", "status IN ('failed', 'error')")
	if len(parts) == 0 {
		sb.WriteString("  none\n\n")
		return
	}
	fmt.Fprintf(sb, "  %s\n\n", strings.Join(parts, ", "))
}

func writeDoctorAndroidSyncClientFailures(sb *strings.Builder) {
	sb.WriteString("Android sync client failures:\n")
	metadataRetries, err := doctorAndroidSyncMetadataRetryCounts(60)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	if len(metadataRetries) == 0 {
		sb.WriteString("  none\n\n")
		return
	}
	fmt.Fprintf(sb, "  metadata_retry %s\n", strings.Join(metadataRetries, ", "))
	sb.WriteString("\n")
}

func doctorAndroidSyncMetadataRetryCounts(minutes int) ([]string, error) {
	logsDir := getLogsDir()
	cutoff := time.Duration(minutes*2) * time.Minute
	metadataCounts := map[string]int{}
	type androidLogLine struct {
		Event  string         `json:"event"`
		Fields map[string]any `json:"fields"`
	}
	err := filepath.WalkDir(logsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(logsDir, path)
		if !strings.Contains(rel, "android") {
			return nil
		}
		info, err := d.Info()
		if err != nil || time.Since(info.ModTime()) > cutoff {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var entry androidLogLine
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				continue
			}
			if entry.Event == "android_sync_metadata_retry" {
				label := doctorLogField(entry.Fields, "label")
				if label == "" {
					label = "unknown"
				}
				metadataCounts[maskSensitive(label)]++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return doctorSortedCountParts(metadataCounts), nil
}

func doctorLogField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func doctorSortedCountParts(counts map[string]int) []string {
	parts := make([]string, 0, len(counts))
	for key, count := range counts {
		parts = append(parts, fmt.Sprintf("%s=%d", key, count))
	}
	sort.Strings(parts)
	return parts
}

func writeDoctorRecentErrors(sb *strings.Builder) {
	sb.WriteString("Recent high-signal log errors:\n")
	errors, err := recentErrors(60, "")
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n", err)
		return
	}
	for _, line := range strings.Split(maskSensitive(errors), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Fprintf(sb, "  %s\n", line)
	}
}

func doctorStatusCounts(conn *sql.DB, table, groupColumn, where string) []string {
	query := fmt.Sprintf("SELECT COALESCE(NULLIF(%s, ''), 'unknown'), COUNT(*) FROM %s", groupColumn, table)
	if where != "" {
		query += " WHERE " + where
	}
	query += " GROUP BY 1"
	rows, err := conn.Query(query)
	if err != nil {
		return nil
	}
	defer func() {
		_ = rows.Close()
	}()
	var parts []string
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%d", key, count))
	}
	sort.Strings(parts)
	return parts
}

func doctorAssetStatusCounts(conn *sql.DB, where string) []string {
	query := `
		SELECT CASE WHEN a.lifecycle_state = 'pruned' THEN 'pruned'
		            WHEN current.published_revision > 0 AND current.file_path != '' THEN 'ready'
		            ELSE desired.job_state END AS state,
		       COUNT(*)
		FROM assets a
		JOIN media_objects current ON current.object_id = a.object_id
		JOIN media_objects desired ON desired.object_id = a.desired_object_id`
	if where != "" {
		query += " WHERE " + where
	}
	query += " GROUP BY state"
	rows, err := conn.Query(query)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var parts []string
	for rows.Next() {
		var state string
		var count int
		if rows.Scan(&state, &count) == nil {
			parts = append(parts, fmt.Sprintf("%s=%d", state, count))
		}
	}
	sort.Strings(parts)
	return parts
}

func doctorCount(conn *sql.DB, query string, args ...any) int {
	var count int
	if err := conn.QueryRow(query, args...).Scan(&count); err != nil {
		return 0
	}
	return count
}

var sensitiveMaskers = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)\b(cookie|token|secret|password|passphrase|authorization|api[_-]?key)=([^ \t\r\n,;]+)`), "$1=***"},
	{regexp.MustCompile(`(?im)\b(authorization)\s*:\s*[^\r\n]+`), "$1: ***"},
	{regexp.MustCompile(`(?im)\b(set-cookie|cookie)\s*:\s*[^\r\n]+`), "$1: ***"},
	{regexp.MustCompile(`(?im)\b([A-Za-z0-9_-]*(?:token|secret|password|passphrase|api[_-]?key)[A-Za-z0-9_-]*)\s*:\s*([^ \t\r\n,;]+)`), "$1: ***"},
}

func maskSensitive(s string) string {
	for _, masker := range sensitiveMaskers {
		s = masker.re.ReplaceAllString(s, masker.repl)
	}
	return s
}
