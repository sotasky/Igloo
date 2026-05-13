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
	writeDoctorAssetParity(&sb, conn)
	writeDoctorDownloaderFailures(&sb, conn)
	writeDoctorAndroidSyncClientFailures(&sb)
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
	if debt, err := doctorAndroidSyncPruneDebt(conn, time.Now().UnixMilli(), igloodb.DefaultAndroidSyncPrunePolicy()); err == nil {
		fmt.Fprintf(
			sb,
			"  prune eligible: generations=%d items=%d assets=%d health_reports=%d\n",
			debt.generations,
			debt.items,
			debt.assets,
			debt.healthReports,
		)
	} else {
		fmt.Fprintf(sb, "  prune eligible: unavailable: %v\n", err)
	}
	sb.WriteString("\n")
}

type doctorPruneDebt struct {
	generations   int
	items         int
	assets        int
	healthReports int
}

func doctorAndroidSyncPruneDebt(conn *sql.DB, nowMs int64, policy igloodb.AndroidSyncPrunePolicy) (doctorPruneDebt, error) {
	cutoffMs := nowMs - int64(policy.KeepMinAge/time.Millisecond)
	var debt doctorPruneDebt
	err := conn.QueryRow(`
		WITH retained_ready(generation_id) AS (
			SELECT generation_id
			FROM android_sync_generations
			WHERE status = 'ready'
			ORDER BY created_at_ms DESC, generation_id DESC
			LIMIT ?
		),
		eligible_generations(generation_id) AS (
			SELECT g.generation_id
			FROM android_sync_generations g
			WHERE g.status = 'ready'
			  AND g.created_at_ms < ?
			  AND g.generation_id != ?
			  AND NOT EXISTS (
				SELECT 1
				FROM retained_ready rr
				WHERE rr.generation_id = g.generation_id
			  )
		),
		overflow_health(id) AS (
			SELECT h.id
			FROM android_sync_health_reports h
			WHERE h.id NOT IN (
				SELECT kept.id
				FROM android_sync_health_reports kept
				ORDER BY kept.reported_at_ms DESC, kept.id DESC
				LIMIT ?
			)
		),
		health_debt(id) AS (
			SELECT h.id
			FROM android_sync_health_reports h
			INNER JOIN eligible_generations eg ON eg.generation_id = h.generation_id
			UNION
			SELECT id FROM overflow_health
		)
		SELECT
			(SELECT COUNT(*) FROM eligible_generations),
			(SELECT COUNT(*)
			 FROM android_sync_items i
			 INNER JOIN eligible_generations eg ON eg.generation_id = i.generation_id),
			(SELECT COUNT(*)
			 FROM android_sync_assets a
			 INNER JOIN eligible_generations eg ON eg.generation_id = a.generation_id),
			(SELECT COUNT(*) FROM health_debt)
	`, policy.KeepReadyGenerations, cutoffMs, policy.ProtectGenerationID, policy.KeepHealthReports).Scan(
		&debt.generations,
		&debt.items,
		&debt.assets,
		&debt.healthReports,
	)
	return debt, err
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

func writeDoctorAssetParity(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Asset inventory parity:\n")
	parts := doctorStatusCounts(conn, "assets", "state", "")
	if len(parts) == 0 {
		parts = []string{"empty=0"}
	}
	fmt.Fprintf(sb, "  inventory states: %s\n", strings.Join(parts, ", "))

	dataDir := filepath.Dir(getDBPath())
	rows := []struct {
		kind   string
		legacy int
	}{
		{"post_media", doctorCount(conn, `SELECT COUNT(*) FROM media_files WHERE COALESCE(file_path, '') != '' AND owner_type IN ('feed_media', 'quote_media')`)},
		{"video_stream", doctorCount(conn, `SELECT COUNT(*) FROM videos WHERE COALESCE(file_path, '') != ''`)},
		{"post_thumbnail", doctorCount(conn, `SELECT COUNT(*) FROM videos WHERE COALESCE(thumbnail_path, '') != ''`) + countFiles(filepath.Join(dataDir, "thumbnails", "generated"))},
		{"dearrow_thumbnail", doctorCount(conn, `SELECT COUNT(*) FROM videos WHERE COALESCE(dearrow_thumb_path, '') != ''`) + countFiles(filepath.Join(dataDir, "thumbnails", "dearrow"))},
		{"avatar", countFiles(filepath.Join(dataDir, "thumbnails", "avatars"))},
		{"banner", countFiles(filepath.Join(dataDir, "thumbnails", "banners"))},
		{"preview_track_json", countFilesNamed(filepath.Join(dataDir, "thumbnails", "previews"), "track.json")},
		{"preview_sprite", countFilesNamed(filepath.Join(dataDir, "thumbnails", "previews"), "sprite.jpg")},
	}
	for _, row := range rows {
		assets := doctorAssetKindCount(conn, row.kind)
		gap := row.legacy - assets
		if gap < 0 {
			gap = 0
		}
		fmt.Fprintf(sb, "  %-20s assets=%d legacy=%d gap=%d\n", row.kind+":", assets, row.legacy, gap)
	}
	sb.WriteString("\n")
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
	assetFailures, assetStale, metadataRetries, err := doctorAndroidSyncClientFailureCounts(60)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	if len(assetFailures) == 0 && len(assetStale) == 0 && len(metadataRetries) == 0 {
		sb.WriteString("  none\n\n")
		return
	}
	if len(assetFailures) > 0 {
		fmt.Fprintf(sb, "  asset_failed %s\n", strings.Join(assetFailures, ", "))
	}
	if len(assetStale) > 0 {
		fmt.Fprintf(sb, "  asset_stale %s\n", strings.Join(assetStale, ", "))
	}
	if len(metadataRetries) > 0 {
		fmt.Fprintf(sb, "  metadata_retry %s\n", strings.Join(metadataRetries, ", "))
	}
	sb.WriteString("\n")
}

func doctorAndroidSyncClientFailureCounts(minutes int) ([]string, []string, []string, error) {
	logsDir := getLogsDir()
	cutoff := time.Duration(minutes*2) * time.Minute
	assetCounts := map[string]int{}
	staleCounts := map[string]int{}
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
			switch entry.Event {
			case "android_sync_asset_failed":
				errorKind := doctorLogField(entry.Fields, "error")
				assetKind := doctorLogField(entry.Fields, "asset_kind")
				if errorKind == "" {
					errorKind = "unknown"
				}
				if assetKind == "" {
					assetKind = "unknown"
				}
				assetCounts[maskSensitive(errorKind)+"/"+maskSensitive(assetKind)]++
			case "android_sync_asset_stale":
				reason := doctorLogField(entry.Fields, "reason")
				assetKind := doctorLogField(entry.Fields, "asset_kind")
				if reason == "" {
					reason = "unknown"
				}
				if assetKind == "" {
					assetKind = "unknown"
				}
				staleCounts[maskSensitive(reason)+"/"+maskSensitive(assetKind)]++
			case "android_sync_metadata_retry":
				label := doctorLogField(entry.Fields, "label")
				message := doctorLogField(entry.Fields, "error")
				if label == "" {
					label = "unknown"
				}
				if message == "" {
					message = "unknown"
				}
				metadataCounts[maskSensitive(label)+"/"+doctorAndroidSyncMetadataErrorKind(message)]++
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return doctorSortedCountParts(assetCounts), doctorSortedCountParts(staleCounts), doctorSortedCountParts(metadataCounts), nil
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

func doctorAndroidSyncMetadataErrorKind(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "sync http 502") {
		return "http_502"
	}
	if strings.Contains(lower, "sync http 503") {
		return "http_503"
	}
	if strings.Contains(lower, "sync http 504") {
		return "http_504"
	}
	if strings.Contains(lower, "sync decode failed") {
		return "decode_failed"
	}
	if strings.Contains(lower, "connect timeout") {
		return "connect_timeout"
	}
	if strings.Contains(lower, "request timeout") {
		return "request_timeout"
	}
	if strings.Contains(lower, "failed to connect") {
		return "failed_connect"
	}
	if strings.Contains(lower, "certpathvalidator") || strings.Contains(lower, "trust anchor") {
		return "certificate"
	}
	if strings.Contains(lower, "connection abort") || strings.Contains(lower, "unexpected end of stream") {
		return "connection_closed"
	}
	fallback := maskSensitive(strings.TrimSpace(message))
	if len(fallback) > 80 {
		fallback = fallback[:80]
	}
	if fallback == "" {
		return "unknown"
	}
	return fallback
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

func doctorCount(conn *sql.DB, query string, args ...any) int {
	var count int
	if err := conn.QueryRow(query, args...).Scan(&count); err != nil {
		return 0
	}
	return count
}

func doctorAssetKindCount(conn *sql.DB, kind string) int {
	return doctorCount(conn, `SELECT COUNT(*) FROM assets WHERE asset_kind = ?`, kind)
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

func countFilesNamed(dir, name string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if d.Name() == name {
			count++
		}
		return nil
	})
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
