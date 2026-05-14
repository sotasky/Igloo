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
)

func doctorStatus() (string, error) {
	conn, err := getServerDB()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("=== Igloo Doctor ===\n\n")
	writeDoctorDBFiles(&sb)
	writeDoctorSQLiteStorage(&sb, conn)
	writeDoctorDBStat(&sb, conn)
	writeDoctorPersistenceLifecycle(&sb, conn)
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
	activeLeases, expiredLeases := doctorAssetLeaseCounts(conn, time.Now().UnixMilli())
	fmt.Fprintf(sb, "  asset leases: active_downloading=%d expired_downloading=%d\n", activeLeases, expiredLeases)

	dataDir := filepath.Dir(getDBPath())
	postMediaLegacy := doctorCount(conn, `
		SELECT COUNT(*)
		FROM (
			SELECT owner_id, media_index
			FROM media_files
			WHERE COALESCE(file_path, '') != ''
			  AND owner_type IN ('feed_media', 'quote_media')
			  AND `+doctorNonAudioPathSQL("file_path")+`
			GROUP BY owner_id, media_index
		)`)
	postThumbnailLegacy := doctorCount(conn, `
		SELECT COUNT(*)
		FROM (
			SELECT
				CASE
					WHEN channel_id LIKE 'twitter_%' THEN 'tweet'
					WHEN channel_id LIKE 'tiktok_%' THEN 'tiktok_video'
					WHEN channel_id LIKE 'instagram_%' THEN 'instagram_reel'
					ELSE 'youtube_video'
				END AS owner_kind,
				video_id AS owner_id
			FROM videos
			WHERE COALESCE(thumbnail_path, '') != ''
			UNION
			SELECT 'tweet' AS owner_kind, owner_id
			FROM media_files
			WHERE COALESCE(file_path, '') != ''
			  AND owner_type IN ('feed_media', 'quote_media')
			  AND media_index = 0
			  AND `+doctorNonAudioPathSQL("file_path")+`
			  AND (
			       COALESCE(media_type, '') IN ('video', 'gif')
			    OR `+doctorNotImageTransportPathSQL("file_path")+`
			  )
			GROUP BY owner_id
		)`)
	rows := []struct {
		kind   string
		legacy int
		raw    int
	}{
		{"post_media", postMediaLegacy, 0},
		{"video_stream", doctorCount(conn, `SELECT COUNT(*) FROM videos WHERE COALESCE(file_path, '') != ''`), 0},
		{
			"post_thumbnail",
			postThumbnailLegacy,
			doctorCount(conn, `SELECT COUNT(*) FROM videos WHERE COALESCE(thumbnail_path, '') != ''`) + countFiles(filepath.Join(dataDir, "thumbnails", "generated")),
		},
		{
			"dearrow_thumbnail",
			doctorCount(conn, `SELECT COUNT(*) FROM videos WHERE COALESCE(dearrow_thumb_path, '') != ''`),
			countFiles(filepath.Join(dataDir, "thumbnails", "dearrow")),
		},
		{"avatar", doctorProfileAssetTargetCount(conn, dataDir, "avatar"), countFiles(filepath.Join(dataDir, "thumbnails", "avatars"))},
		{"banner", doctorProfileAssetTargetCount(conn, dataDir, "banner"), countFiles(filepath.Join(dataDir, "thumbnails", "banners"))},
		{
			"preview_track_json",
			countPreviewFilesForKnownVideos(conn, dataDir, "track.json"),
			countFilesNamed(filepath.Join(dataDir, "thumbnails", "previews"), "track.json"),
		},
		{
			"preview_sprite",
			countPreviewFilesForKnownVideos(conn, dataDir, "sprite.jpg"),
			countFilesNamed(filepath.Join(dataDir, "thumbnails", "previews"), "sprite.jpg"),
		},
	}
	for _, row := range rows {
		assets := doctorAssetKindCount(conn, row.kind)
		gap := row.legacy - assets
		if gap < 0 {
			gap = 0
		}
		if row.raw > 0 && row.raw != row.legacy {
			fmt.Fprintf(sb, "  %-20s assets=%d legacy=%d raw_files=%d gap=%d\n", row.kind+":", assets, row.legacy, row.raw, gap)
		} else {
			fmt.Fprintf(sb, "  %-20s assets=%d legacy=%d gap=%d\n", row.kind+":", assets, row.legacy, gap)
		}
	}
	sb.WriteString("\n")
}

func doctorAssetLeaseCounts(conn *sql.DB, nowMs int64) (active int, expired int) {
	_ = conn.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN COALESCE(lease_until_ms, 0) > ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(lease_until_ms, 0) > 0 AND lease_until_ms <= ? THEN 1 ELSE 0 END), 0)
		FROM assets
		WHERE state = 'downloading'
	`, nowMs, nowMs).Scan(&active, &expired)
	return active, expired
}

func doctorNonAudioPathSQL(column string) string {
	return fmt.Sprintf(`LOWER(COALESCE(%s, '')) NOT LIKE '%%.mp3'
		  AND LOWER(COALESCE(%s, '')) NOT LIKE '%%.m4a'
		  AND LOWER(COALESCE(%s, '')) NOT LIKE '%%.aac'
		  AND LOWER(COALESCE(%s, '')) NOT LIKE '%%.ogg'`, column, column, column, column)
}

func doctorNotImageTransportPathSQL(column string) string {
	return fmt.Sprintf(`LOWER(COALESCE(%s, '')) NOT LIKE '%%.jpg'
		  AND LOWER(COALESCE(%s, '')) NOT LIKE '%%.jpeg'
		  AND LOWER(COALESCE(%s, '')) NOT LIKE '%%.png'
		  AND LOWER(COALESCE(%s, '')) NOT LIKE '%%.webp'
		  AND LOWER(COALESCE(%s, '')) NOT LIKE '%%.gif'
		  AND LOWER(COALESCE(%s, '')) NOT LIKE '%%.image'`, column, column, column, column, column, column)
}

func doctorProfileAssetTargetCount(conn *sql.DB, dataDir, kind string) int {
	sourceColumn := "avatar_url"
	dirName := "avatars"
	if kind == "banner" {
		sourceColumn = "banner_url"
		dirName = "banners"
	}
	rows, err := conn.Query(fmt.Sprintf(`
		SELECT channel_id, COALESCE(%s, '')
		FROM channel_profiles
		WHERE COALESCE(tombstone, 0) = 0
	`, sourceColumn))
	if err != nil {
		return 0
	}
	defer func() {
		_ = rows.Close()
	}()
	count := 0
	for rows.Next() {
		var channelID, sourceURL string
		if err := rows.Scan(&channelID, &sourceURL); err != nil {
			continue
		}
		if sourceURL != "" || doctorProfileMediaFileExists(dataDir, dirName, channelID) {
			count++
		}
	}
	return count
}

func doctorProfileMediaFileExists(dataDir, dirName, channelID string) bool {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
		if _, err := os.Stat(filepath.Join(dataDir, "thumbnails", dirName, channelID+ext)); err == nil {
			return true
		}
	}
	return false
}

func countPreviewFilesForKnownVideos(conn *sql.DB, dataDir, name string) int {
	dir := filepath.Join(dataDir, "thumbnails", "previews")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), name)); err != nil {
			continue
		}
		if doctorVideoExists(conn, entry.Name()) {
			count++
		}
	}
	return count
}

func doctorVideoExists(conn *sql.DB, videoID string) bool {
	var exists int
	err := conn.QueryRow(`SELECT 1 FROM videos WHERE video_id = ? LIMIT 1`, videoID).Scan(&exists)
	return err == nil && exists == 1
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
