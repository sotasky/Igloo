package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func androidSyncStatus(minutes int) (string, error) {
	if minutes <= 0 {
		minutes = 60
	}
	conn, err := getServerDB()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("=== Android Sync Status ===\n\n")
	writeAndroidSyncGenerations(&sb, conn)
	latestID := writeAndroidSyncLatestGeneration(&sb, conn)
	if latestID != "" {
		writeAndroidSyncLatestAssets(&sb, conn, latestID)
	}
	writeAndroidSyncHealthReports(&sb, conn)
	writeAndroidSyncInventory(&sb, conn)
	writeAndroidSyncClientFailureSummary(&sb, minutes)
	return strings.TrimRight(sb.String(), "\n"), nil
}

func writeAndroidSyncGenerations(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Generations:\n")
	rows, err := conn.Query(`
		SELECT status, COUNT(*), COALESCE(MAX(created_at_ms), 0)
		FROM android_sync_generations
		GROUP BY status
		ORDER BY COUNT(*) DESC, status
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
		var status string
		var count int
		var newestMs int64
		if err := rows.Scan(&status, &count, &newestMs); err != nil {
			continue
		}
		wrote = true
		fmt.Fprintf(sb, "  %-10s count=%d newest=%s\n", status, count, formatMillis(newestMs))
	}
	if !wrote {
		sb.WriteString("  none\n")
	}
	sb.WriteString("\n")
}

func writeAndroidSyncLatestGeneration(sb *strings.Builder, conn *sql.DB) string {
	sb.WriteString("Latest generation:\n")
	var (
		id            string
		status        string
		createdAtMs   int64
		itemCount     int64
		assetCount    int64
		readyCount    int64
		missingCount  int64
		totalBytes    int64
		contentCounts string
		assetCounts   string
	)
	err := conn.QueryRow(`
		SELECT generation_id, status, created_at_ms, item_count, asset_count,
		       ready_asset_count, server_missing_asset_count, total_bytes,
		       content_counts_json, asset_counts_json
		FROM android_sync_generations
		ORDER BY created_at_ms DESC, generation_id DESC
		LIMIT 1
	`).Scan(&id, &status, &createdAtMs, &itemCount, &assetCount, &readyCount, &missingCount, &totalBytes, &contentCounts, &assetCounts)
	if err != nil {
		if err == sql.ErrNoRows {
			sb.WriteString("  none\n\n")
			return ""
		}
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return ""
	}
	fmt.Fprintf(sb, "  id: %s\n", id)
	fmt.Fprintf(sb, "  status: %s\n", status)
	fmt.Fprintf(sb, "  created: %s\n", formatMillis(createdAtMs))
	fmt.Fprintf(sb, "  counts: items=%d assets=%d ready=%d server_missing=%d bytes=%s\n", itemCount, assetCount, readyCount, missingCount, formatSize(totalBytes))
	if contentCounts != "" && contentCounts != "{}" {
		fmt.Fprintf(sb, "  content_counts_json: %s\n", compactLong(contentCounts, 300))
	}
	if assetCounts != "" && assetCounts != "{}" {
		fmt.Fprintf(sb, "  asset_counts_json: %s\n", compactLong(assetCounts, 300))
	}
	sb.WriteString("\n")
	return id
}

func writeAndroidSyncLatestAssets(sb *strings.Builder, conn *sql.DB, generationID string) {
	sb.WriteString("Latest generation assets:\n")
	rows, err := conn.Query(`
		SELECT state, asset_kind, COUNT(*)
		FROM android_sync_assets
		WHERE generation_id = ?
		GROUP BY state, asset_kind
		ORDER BY state, COUNT(*) DESC, asset_kind
	`, generationID)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	wrote := false
	for rows.Next() {
		var state, kind string
		var count int
		if err := rows.Scan(&state, &kind, &count); err != nil {
			continue
		}
		wrote = true
		fmt.Fprintf(sb, "  %-15s %-20s %d\n", state, kind, count)
	}
	if !wrote {
		sb.WriteString("  none\n")
	}
	writeAndroidSyncServerMissingSamples(sb, conn, generationID)
	sb.WriteString("\n")
}

func writeAndroidSyncServerMissingSamples(sb *strings.Builder, conn *sql.DB, generationID string) {
	rows, err := conn.Query(`
		SELECT asset_kind, owner_kind, COUNT(*)
		FROM android_sync_assets
		WHERE generation_id = ? AND state = 'server_missing'
		GROUP BY asset_kind, owner_kind
		ORDER BY COUNT(*) DESC, asset_kind, owner_kind
		LIMIT 10
	`, generationID)
	if err != nil {
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	var groups []string
	for rows.Next() {
		var kind, ownerKind string
		var count int
		if err := rows.Scan(&kind, &ownerKind, &count); err != nil {
			continue
		}
		groups = append(groups, fmt.Sprintf("%s/%s=%d", kind, ownerKind, count))
	}
	if len(groups) > 0 {
		fmt.Fprintf(sb, "  server_missing groups: %s\n", strings.Join(groups, ", "))
	}

	samples, err := conn.Query(`
		SELECT asset_kind, owner_kind, owner_id, media_index, required_reason
		FROM android_sync_assets
		WHERE generation_id = ? AND state = 'server_missing'
		ORDER BY asset_kind, owner_kind, seq
		LIMIT 8
	`, generationID)
	if err != nil {
		return
	}
	defer func() {
		_ = samples.Close()
	}()
	wroteHeader := false
	for samples.Next() {
		var kind, ownerKind, ownerID, reason string
		var mediaIndex int
		if err := samples.Scan(&kind, &ownerKind, &ownerID, &mediaIndex, &reason); err != nil {
			continue
		}
		if !wroteHeader {
			sb.WriteString("  server_missing samples:\n")
			wroteHeader = true
		}
		fmt.Fprintf(sb, "    %s/%s owner=%s media_index=%d reason=%s\n", kind, ownerKind, ownerID, mediaIndex, reason)
	}
}

func writeAndroidSyncHealthReports(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Recent health reports:\n")
	rows, err := conn.Query(`
		SELECT generation_id, reported_at_ms, verified_assets, pending_assets,
		       failed_assets, missing_assets, total_assets, verified_bytes
		FROM android_sync_health_reports
		ORDER BY reported_at_ms DESC, id DESC
		LIMIT 5
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
		var generationID string
		var reportedAtMs int64
		var verified, pending, failed, missing, total int64
		var verifiedBytes int64
		if err := rows.Scan(&generationID, &reportedAtMs, &verified, &pending, &failed, &missing, &total, &verifiedBytes); err != nil {
			continue
		}
		wrote = true
		fmt.Fprintf(
			sb,
			"  %s reported=%s verified=%d pending=%d failed=%d missing=%d total=%d bytes=%s\n",
			generationID,
			formatMillis(reportedAtMs),
			verified,
			pending,
			failed,
			missing,
			total,
			formatSize(verifiedBytes),
		)
	}
	if !wrote {
		sb.WriteString("  none\n")
	}
	sb.WriteString("\n")
}

func writeAndroidSyncInventory(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Current asset inventory:\n")
	rows, err := conn.Query(`
		SELECT state, asset_kind, COUNT(*)
		FROM assets
		GROUP BY state, asset_kind
		ORDER BY state, COUNT(*) DESC, asset_kind
		LIMIT 24
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
		var state, kind string
		var count int
		if err := rows.Scan(&state, &kind, &count); err != nil {
			continue
		}
		wrote = true
		fmt.Fprintf(sb, "  %-15s %-20s %d\n", state, kind, count)
	}
	if !wrote {
		sb.WriteString("  none\n")
	}
	activeLeases, expiredLeases := doctorAssetLeaseCounts(conn, time.Now().UnixMilli())
	fmt.Fprintf(sb, "  leases: active_downloading=%d expired_downloading=%d\n\n", activeLeases, expiredLeases)
}

func writeAndroidSyncClientFailureSummary(sb *strings.Builder, minutes int) {
	sb.WriteString("Recent Android client sync failures:\n")
	assetFailures, assetStale, metadataRetries, err := doctorAndroidSyncClientFailureCounts(minutes)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n", err)
		return
	}
	if len(assetFailures) == 0 && len(assetStale) == 0 && len(metadataRetries) == 0 {
		sb.WriteString("  none\n")
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
}

func identityMediaStatus(channelID, platform, handle, tweetID string, limit int) (string, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	conn, err := getServerDB()
	if err != nil {
		return "", err
	}

	candidates := identityCandidatesFromInputs(channelID, platform, handle)
	var sb strings.Builder
	sb.WriteString("=== Identity Media Status ===\n\n")
	if strings.TrimSpace(tweetID) != "" {
		tweetCandidates, err := writeTweetIdentityContext(&sb, conn, strings.TrimSpace(tweetID))
		if err != nil {
			return "", err
		}
		candidates = append(candidates, tweetCandidates...)
	}
	candidates = dedupeIdentityCandidates(candidates)
	if len(candidates) == 0 {
		sb.WriteString("No identity selector provided. Pass channel_id, platform+handle, or tweet_id.\n")
		return sb.String(), nil
	}

	dataDir := filepath.Dir(getDBPath())
	for _, candidate := range candidates {
		writeIdentityCandidateStatus(&sb, conn, dataDir, candidate, limit)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

type identityCandidate struct {
	label     string
	platform  string
	handle    string
	channelID string
}

func identityCandidatesFromInputs(channelID, platform, handle string) []identityCandidate {
	channelID = strings.TrimSpace(channelID)
	platform = strings.TrimSpace(platform)
	handle = strings.TrimSpace(handle)
	if platform == "" {
		platform = platformFromChannelID(channelID)
	}
	if platform == "" {
		platform = "twitter"
	}
	var candidates []identityCandidate
	if channelID != "" || handle != "" {
		candidates = append(candidates, identityCandidate{
			label:     "input",
			platform:  platform,
			handle:    normalizeHandle(handle),
			channelID: normalizeChannelID(channelID, platform, handle),
		})
	}
	return candidates
}

func writeTweetIdentityContext(sb *strings.Builder, conn *sql.DB, tweetID string) ([]identityCandidate, error) {
	var row struct {
		sourceHandle         sql.NullString
		authorHandle         sql.NullString
		retweetedByHandle    sql.NullString
		quoteTweetID         sql.NullString
		quoteAuthorHandle    sql.NullString
		replyToHandle        sql.NullString
		publishedAt          sql.NullInt64
		fetchedAt            sql.NullInt64
		authorDisplayName    sql.NullString
		quoteAuthorDisplay   sql.NullString
		authorAvatarURL      sql.NullString
		quoteAuthorAvatarURL sql.NullString
	}
	err := conn.QueryRow(`
		SELECT source_handle, author_handle, retweeted_by_handle, quote_tweet_id,
		       quote_author_handle, reply_to_handle, published_at, fetched_at,
		       author_display_name, quote_author_display_name,
		       author_avatar_url, quote_author_avatar_url
		FROM feed_items
		WHERE tweet_id = ?
	`, tweetID).Scan(
		&row.sourceHandle,
		&row.authorHandle,
		&row.retweetedByHandle,
		&row.quoteTweetID,
		&row.quoteAuthorHandle,
		&row.replyToHandle,
		&row.publishedAt,
		&row.fetchedAt,
		&row.authorDisplayName,
		&row.quoteAuthorDisplay,
		&row.authorAvatarURL,
		&row.quoteAuthorAvatarURL,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Fprintf(sb, "Feed item %s: not found\n\n", tweetID)
			return nil, nil
		}
		return nil, fmt.Errorf("load feed item %s: %w", tweetID, err)
	}
	fmt.Fprintf(sb, "Feed item %s:\n", tweetID)
	fmt.Fprintf(sb, "  published=%s fetched=%s quote_tweet_id=%s\n", formatNullMillis(row.publishedAt), formatNullMillis(row.fetchedAt), nullString(row.quoteTweetID))
	if row.authorDisplayName.Valid || row.quoteAuthorDisplay.Valid {
		fmt.Fprintf(sb, "  names: author=%s quote_author=%s\n", nullString(row.authorDisplayName), nullString(row.quoteAuthorDisplay))
	}
	if row.authorAvatarURL.Valid || row.quoteAuthorAvatarURL.Valid {
		fmt.Fprintf(sb, "  avatar urls: author=%s quote_author=%s\n", compactLong(nullString(row.authorAvatarURL), 120), compactLong(nullString(row.quoteAuthorAvatarURL), 120))
	}
	sb.WriteString("\n")

	add := func(label string, value sql.NullString) identityCandidate {
		handle := normalizeHandle(nullString(value))
		return identityCandidate{
			label:     label,
			platform:  "twitter",
			handle:    handle,
			channelID: normalizeChannelID("", "twitter", handle),
		}
	}
	var candidates []identityCandidate
	for _, entry := range []struct {
		label string
		value sql.NullString
	}{
		{"source", row.sourceHandle},
		{"author", row.authorHandle},
		{"retweeter", row.retweetedByHandle},
		{"quote_author", row.quoteAuthorHandle},
		{"reply_parent", row.replyToHandle},
	} {
		if normalizeHandle(nullString(entry.value)) != "" {
			candidates = append(candidates, add(entry.label, entry.value))
		}
	}
	return candidates, nil
}

func writeIdentityCandidateStatus(sb *strings.Builder, conn *sql.DB, dataDir string, candidate identityCandidate, limit int) {
	if candidate.channelID == "" && candidate.handle == "" {
		return
	}
	fmt.Fprintf(sb, "Identity [%s]:\n", candidate.label)
	fmt.Fprintf(sb, "  selector: platform=%s handle=%s channel_id=%s\n", candidate.platform, candidate.handle, candidate.channelID)

	profile := loadProfileStatus(conn, candidate)
	if profile.found {
		fmt.Fprintf(sb, "  profile: channel_id=%s platform=%s handle=%s tombstone=%d fail_count=%d fetched=%s next_retry=%s\n",
			profile.channelID,
			profile.platform,
			profile.handle,
			profile.tombstone,
			profile.failCount,
			formatMillis(profile.fetchedAt),
			formatMillis(profile.nextRetryAt),
		)
		if profile.displayName != "" {
			fmt.Fprintf(sb, "  display_name: %s\n", profile.displayName)
		}
		fmt.Fprintf(sb, "  profile urls: avatar=%t banner=%t\n", profile.avatarURL != "", profile.bannerURL != "")
		candidate.channelID = profile.channelID
		if candidate.handle == "" {
			candidate.handle = profile.handle
		}
	} else {
		sb.WriteString("  profile: missing\n")
	}

	avatarPath, hasAvatarFile := firstProfileMediaPath(dataDir, "avatars", candidate.channelID)
	bannerPath, hasBannerFile := firstProfileMediaPath(dataDir, "banners", candidate.channelID)
	fmt.Fprintf(sb, "  cached files: avatar=%t", hasAvatarFile)
	if hasAvatarFile {
		fmt.Fprintf(sb, " (%s)", avatarPath)
	}
	fmt.Fprintf(sb, " banner=%t", hasBannerFile)
	if hasBannerFile {
		fmt.Fprintf(sb, " (%s)", bannerPath)
	}
	sb.WriteString("\n")

	writeIdentityAssetRows(sb, conn, candidate.channelID)
	writeIdentityChannelQueue(sb, conn, candidate.channelID)
	writeIdentityFeedTimeline(sb, conn, candidate, limit)
	sb.WriteString("\n")
}

type profileStatus struct {
	found       bool
	channelID   string
	platform    string
	handle      string
	displayName string
	avatarURL   string
	bannerURL   string
	fetchedAt   int64
	nextRetryAt int64
	failCount   int
	tombstone   int
}

func loadProfileStatus(conn *sql.DB, candidate identityCandidate) profileStatus {
	var profile profileStatus
	err := conn.QueryRow(`
		SELECT channel_id, platform, COALESCE(handle, ''), COALESCE(display_name, ''),
		       COALESCE(avatar_url, ''), COALESCE(banner_url, ''),
		       fetched_at, next_retry_at, COALESCE(fail_count, 0), COALESCE(tombstone, 0)
		FROM channel_profiles
		WHERE channel_id = ?
		   OR (platform = ? AND LOWER(COALESCE(handle, '')) = LOWER(?))
		ORDER BY CASE WHEN channel_id = ? THEN 0 ELSE 1 END
		LIMIT 1
	`, candidate.channelID, candidate.platform, candidate.handle, candidate.channelID).Scan(
		&profile.channelID,
		&profile.platform,
		&profile.handle,
		&profile.displayName,
		&profile.avatarURL,
		&profile.bannerURL,
		&profile.fetchedAt,
		&profile.nextRetryAt,
		&profile.failCount,
		&profile.tombstone,
	)
	if err == nil {
		profile.found = true
	}
	return profile
}

func writeIdentityAssetRows(sb *strings.Builder, conn *sql.DB, channelID string) {
	if channelID == "" {
		return
	}
	rows, err := conn.Query(`
		SELECT asset_kind, state, owner_kind, owner_id, media_index,
		       COALESCE(file_path, ''), COALESCE(last_error_kind, ''), updated_at_ms
		FROM assets
		WHERE owner_id = ? AND asset_kind IN ('avatar', 'banner')
		ORDER BY asset_kind, media_index
	`, channelID)
	if err != nil {
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	wrote := false
	for rows.Next() {
		var kind, state, ownerKind, ownerID, filePath, errorKind string
		var mediaIndex int
		var updatedAtMs int64
		if err := rows.Scan(&kind, &state, &ownerKind, &ownerID, &mediaIndex, &filePath, &errorKind, &updatedAtMs); err != nil {
			continue
		}
		if !wrote {
			sb.WriteString("  asset rows:\n")
			wrote = true
		}
		fmt.Fprintf(sb, "    %s state=%s owner=%s/%s media_index=%d updated=%s file=%s error_kind=%s\n",
			kind, state, ownerKind, ownerID, mediaIndex, formatMillis(updatedAtMs), emptyDash(filePath), emptyDash(errorKind))
	}
	if !wrote {
		sb.WriteString("  asset rows: none\n")
	}
}

func writeIdentityChannelQueue(sb *strings.Builder, conn *sql.DB, channelID string) {
	if channelID == "" {
		return
	}
	var status string
	var priority int
	var addedAt, startedAt, completedAt int64
	err := conn.QueryRow(`
		SELECT COALESCE(status, ''), COALESCE(priority, 0), added_at, started_at, completed_at
		FROM channel_queue
		WHERE channel_id = ?
	`, channelID).Scan(&status, &priority, &addedAt, &startedAt, &completedAt)
	if err == nil {
		fmt.Fprintf(sb, "  channel_queue: status=%s priority=%d added=%s started=%s completed=%s\n",
			status, priority, formatMillis(addedAt), formatMillis(startedAt), formatMillis(completedAt))
	}
}

func writeIdentityFeedTimeline(sb *strings.Builder, conn *sql.DB, candidate identityCandidate, limit int) {
	handle := normalizeHandle(candidate.handle)
	if handle == "" && candidate.channelID != "" {
		handle = handleFromChannelID(candidate.channelID)
	}
	if handle == "" {
		return
	}
	var count int
	var maxFetched, maxPublished sql.NullInt64
	_ = conn.QueryRow(`
		SELECT COUNT(*), MAX(fetched_at), MAX(published_at)
		FROM feed_items
		WHERE LOWER(COALESCE(author_handle, '')) = LOWER(?)
		   OR LOWER(COALESCE(source_handle, '')) = LOWER(?)
		   OR LOWER(COALESCE(retweeted_by_handle, '')) = LOWER(?)
		   OR LOWER(COALESCE(quote_author_handle, '')) = LOWER(?)
		   OR LOWER(COALESCE(reply_to_handle, '')) = LOWER(?)
	`, handle, handle, handle, handle, handle).Scan(&count, &maxFetched, &maxPublished)
	fmt.Fprintf(sb, "  feed presence: rows=%d latest_published=%s latest_fetched=%s\n", count, formatNullMillis(maxPublished), formatNullMillis(maxFetched))

	if count == 0 {
		return
	}
	rows, err := conn.Query(`
		SELECT tweet_id, published_at, fetched_at,
		       CASE
		         WHEN LOWER(COALESCE(author_handle, '')) = LOWER(?) THEN 'author'
		         WHEN LOWER(COALESCE(source_handle, '')) = LOWER(?) THEN 'source'
		         WHEN LOWER(COALESCE(retweeted_by_handle, '')) = LOWER(?) THEN 'retweeter'
		         WHEN LOWER(COALESCE(quote_author_handle, '')) = LOWER(?) THEN 'quote_author'
		         WHEN LOWER(COALESCE(reply_to_handle, '')) = LOWER(?) THEN 'reply_parent'
		         ELSE 'unknown'
		       END AS role
		FROM feed_items
		WHERE LOWER(COALESCE(author_handle, '')) = LOWER(?)
		   OR LOWER(COALESCE(source_handle, '')) = LOWER(?)
		   OR LOWER(COALESCE(retweeted_by_handle, '')) = LOWER(?)
		   OR LOWER(COALESCE(quote_author_handle, '')) = LOWER(?)
		   OR LOWER(COALESCE(reply_to_handle, '')) = LOWER(?)
		ORDER BY fetched_at DESC, published_at DESC
		LIMIT ?
	`, handle, handle, handle, handle, handle, handle, handle, handle, handle, handle, limit)
	if err != nil {
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	sb.WriteString("  recent feed rows:\n")
	for rows.Next() {
		var tweetID, role string
		var publishedAt, fetchedAt int64
		if err := rows.Scan(&tweetID, &publishedAt, &fetchedAt, &role); err != nil {
			continue
		}
		fmt.Fprintf(sb, "    %s role=%s published=%s fetched=%s\n", tweetID, role, formatMillis(publishedAt), formatMillis(fetchedAt))
	}
}

func dedupeIdentityCandidates(candidates []identityCandidate) []identityCandidate {
	seen := map[string]bool{}
	var out []identityCandidate
	for _, candidate := range candidates {
		if candidate.channelID == "" && candidate.handle != "" {
			candidate.channelID = normalizeChannelID("", candidate.platform, candidate.handle)
		}
		key := candidate.channelID
		if key == "" {
			key = candidate.platform + ":" + candidate.handle
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, candidate)
	}
	return out
}

func firstProfileMediaPath(dataDir, dirName, channelID string) (string, bool) {
	if channelID == "" {
		return "", false
	}
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
		rel := filepath.Join("thumbnails", dirName, channelID+ext)
		if _, err := os.Stat(filepath.Join(dataDir, rel)); err == nil {
			return rel, true
		}
	}
	return "", false
}

func normalizeChannelID(channelID, platform, handle string) string {
	channelID = strings.TrimSpace(channelID)
	if channelID != "" {
		return channelID
	}
	handle = normalizeHandle(handle)
	if handle == "" {
		return ""
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		platform = "twitter"
	}
	return platform + "_" + strings.ToLower(handle)
}

func normalizeHandle(handle string) string {
	return strings.TrimPrefix(strings.TrimSpace(handle), "@")
}

func platformFromChannelID(channelID string) string {
	if i := strings.Index(channelID, "_"); i > 0 {
		return channelID[:i]
	}
	return ""
}

func handleFromChannelID(channelID string) string {
	if i := strings.Index(channelID, "_"); i >= 0 && i+1 < len(channelID) {
		return channelID[i+1:]
	}
	return ""
}

func formatMillis(ms int64) string {
	if ms <= 0 {
		return "none"
	}
	t := time.UnixMilli(ms)
	return fmt.Sprintf("%s (%s ago)", t.Format(time.RFC3339), time.Since(t).Round(time.Second))
}

func formatNullMillis(v sql.NullInt64) string {
	if !v.Valid || v.Int64 <= 0 {
		return "none"
	}
	return formatMillis(v.Int64)
}

func nullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func compactLong(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
