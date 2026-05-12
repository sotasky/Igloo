package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func doctorStatus() (string, error) {
	conn, err := getServerDB()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("=== Igloo Doctor ===\n\n")
	writeDoctorDBFiles(&sb)
	writeDoctorDBStat(&sb, conn)
	writeDoctorAndroidSync(&sb, conn)
	writeDoctorQueues(&sb, conn)
	writeDoctorProfileReadiness(&sb, conn)
	writeDoctorDownloaderFailures(&sb, conn)
	writeDoctorRecentErrors(&sb)
	return strings.TrimRight(sb.String(), "\n"), nil
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
	defer rows.Close()
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

func writeDoctorAndroidSync(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Android sync:\n")
	var total int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM android_sync_generations`).Scan(&total)
	fmt.Fprintf(sb, "  generations: %d\n", total)

	var (
		id          sql.NullString
		createdAtMs sql.NullInt64
		itemCount   sql.NullInt64
		assetCount  sql.NullInt64
		readyCount  sql.NullInt64
		missing     sql.NullInt64
	)
	err := conn.QueryRow(`
		SELECT generation_id, created_at_ms, item_count, asset_count,
		       ready_asset_count, server_missing_asset_count
		FROM android_sync_generations
		ORDER BY created_at_ms DESC
		LIMIT 1
	`).Scan(&id, &createdAtMs, &itemCount, &assetCount, &readyCount, &missing)
	if err == nil && id.Valid {
		age := "unknown"
		if createdAtMs.Valid && createdAtMs.Int64 > 0 {
			age = time.Since(time.UnixMilli(createdAtMs.Int64)).Round(time.Second).String()
		}
		fmt.Fprintf(
			sb,
			"  latest: %s age=%s items=%d assets=%d ready=%d missing=%d\n",
			id.String,
			age,
			itemCount.Int64,
			assetCount.Int64,
			readyCount.Int64,
			missing.Int64,
		)
	}
	sb.WriteString("\n")
}

func writeDoctorQueues(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Queue counts:\n")
	for _, table := range []string{"download_queue", "feed_media_jobs", "channel_queue", "translation_jobs"} {
		parts := doctorStatusCounts(conn, table, "status", "")
		if len(parts) == 0 {
			parts = []string{"empty=0"}
		}
		fmt.Fprintf(sb, "  %-18s %s\n", table+":", strings.Join(parts, ", "))
	}
	sb.WriteString("\n")
}

func writeDoctorProfileReadiness(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Profile/media readiness:\n")
	var profiles, avatarURLs, bannerURLs int
	_ = conn.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN COALESCE(avatar_url, '') != '' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN COALESCE(banner_url, '') != '' THEN 1 ELSE 0 END), 0)
		FROM channel_profiles
		WHERE COALESCE(tombstone, 0) = 0
	`).Scan(&profiles, &avatarURLs, &bannerURLs)
	fmt.Fprintf(sb, "  channel_profiles: total=%d avatar_url=%d banner_url=%d\n", profiles, avatarURLs, bannerURLs)

	dataDir := filepath.Dir(getDBPath())
	fmt.Fprintf(sb, "  cached avatars: %d\n", countFiles(filepath.Join(dataDir, "thumbnails", "avatars")))
	fmt.Fprintf(sb, "  cached banners: %d\n\n", countFiles(filepath.Join(dataDir, "thumbnails", "banners")))
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
	defer rows.Close()
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

func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			count++
		}
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
