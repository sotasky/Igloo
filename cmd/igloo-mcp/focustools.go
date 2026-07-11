package main

import (
	"database/sql"
	"fmt"
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
	writeAndroidSyncClockAndHeads(&sb, conn)
	writeAndroidSyncHealthReports(&sb, conn)
	writeAndroidSyncInventory(&sb, conn)
	writeAndroidSyncMissingSamples(&sb, conn)
	writeAndroidSyncClientFailureSummary(&sb, minutes)
	return strings.TrimRight(sb.String(), "\n"), nil
}

func writeAndroidSyncClockAndHeads(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Convergence stream:\n")
	var epoch string
	var revision, heads int64
	err := conn.QueryRow(`
		SELECT c.epoch, c.revision, (SELECT COUNT(*) FROM android_sync_heads)
		FROM android_sync_clock c
		WHERE c.id = 1
	`).Scan(&epoch, &revision, &heads)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	fmt.Fprintf(sb, "  epoch=%s revision=%d compact_heads=%d\n", epoch, revision, heads)
	rows, err := conn.Query(`
		SELECT owner_kind, COUNT(*), COALESCE(MAX(revision), 0)
		FROM android_sync_heads
		GROUP BY owner_kind
		ORDER BY owner_kind
	`)
	if err != nil {
		fmt.Fprintf(sb, "  head summary unavailable: %v\n\n", err)
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind string
		var count, latest int64
		if err := rows.Scan(&kind, &count, &latest); err == nil {
			fmt.Fprintf(sb, "  %-20s count=%d latest_revision=%d\n", kind, count, latest)
		}
	}
	sb.WriteString("\n")
}

func writeAndroidSyncHealthReports(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Recent cursor health reports:\n")
	rows, err := conn.Query(`
		SELECT cursor, reported_at_ms, verified_assets, pending_assets,
		       missing_assets, total_assets, verified_bytes
		FROM android_sync_health_reports
		ORDER BY reported_at_ms DESC, id DESC
		LIMIT 5
	`)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	defer func() { _ = rows.Close() }()
	wrote := false
	for rows.Next() {
		var cursor string
		var reportedAtMs, verified, pending, missing, total, verifiedBytes int64
		if err := rows.Scan(&cursor, &reportedAtMs, &verified, &pending, &missing, &total, &verifiedBytes); err != nil {
			continue
		}
		wrote = true
		fmt.Fprintf(sb, "  cursor=%s reported=%s verified=%d pending=%d missing=%d total=%d bytes=%s\n",
			compactLong(cursor, 80), formatMillis(reportedAtMs), verified, pending, missing, total, formatSize(verifiedBytes))
	}
	if !wrote {
		sb.WriteString("  none\n")
	}
	sb.WriteString("\n")
}

func writeAndroidSyncMissingSamples(sb *strings.Builder, conn *sql.DB) {
	sb.WriteString("Canonical missing assets:\n")
	rows, err := conn.Query(`
		SELECT asset_kind, owner_kind, COUNT(*)
		FROM assets
		WHERE state IN ('server_missing', 'permanent_missing')
		GROUP BY asset_kind, owner_kind
		ORDER BY COUNT(*) DESC, asset_kind, owner_kind
		LIMIT 10
	`)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n\n", err)
		return
	}
	defer func() { _ = rows.Close() }()
	wrote := false
	for rows.Next() {
		var kind, ownerKind string
		var count int
		if err := rows.Scan(&kind, &ownerKind, &count); err == nil {
			wrote = true
			fmt.Fprintf(sb, "  %s/%s=%d\n", kind, ownerKind, count)
		}
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
	metadataRetries, err := doctorAndroidSyncMetadataRetryCounts(minutes)
	if err != nil {
		fmt.Fprintf(sb, "  unavailable: %v\n", err)
		return
	}
	if len(metadataRetries) == 0 {
		sb.WriteString("  none\n")
		return
	}
	fmt.Fprintf(sb, "  metadata_retry %s\n", strings.Join(metadataRetries, ", "))
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

	for _, candidate := range candidates {
		writeIdentityCandidateStatus(&sb, conn, candidate, limit)
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
		FROM feed_items_resolved
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

func writeIdentityCandidateStatus(sb *strings.Builder, conn *sql.DB, candidate identityCandidate, limit int) {
	if candidate.channelID == "" && candidate.handle == "" {
		return
	}
	fmt.Fprintf(sb, "Identity [%s]:\n", candidate.label)
	fmt.Fprintf(sb, "  selector: platform=%s handle=%s channel_id=%s\n", candidate.platform, candidate.handle, candidate.channelID)

	profile := loadProfileStatus(conn, candidate)
	if profile.found {
		fmt.Fprintf(sb, "  profile: channel_id=%s platform=%s handle=%s tombstone=%d fetched=%s\n",
			profile.channelID,
			profile.platform,
			profile.handle,
			profile.tombstone,
			formatMillis(profile.fetchedAt),
		)
		if profile.displayName != "" {
			fmt.Fprintf(sb, "  display_name: %s\n", profile.displayName)
		}
		if profile.jobFound {
			fmt.Fprintf(sb, "  profile_job: requested=%d completed=%d attempts=%d next_attempt=%s leased=%t lease_until=%s error=%s\n",
				profile.requestedRevision, profile.completedRevision, profile.attempts,
				formatMillis(profile.nextAttemptAtMs), profile.leaseOwner != "",
				formatMillis(profile.leaseUntilMs), emptyDash(profile.lastError))
		} else {
			sb.WriteString("  profile_job: missing\n")
		}
		fmt.Fprintf(sb, "  profile assets: avatar=%s banner=%s\n",
			emptyDash(profile.avatar.state), emptyDash(profile.banner.state))
		writeProfileAssetStatus(sb, "avatar", profile.avatar)
		writeProfileAssetStatus(sb, "banner", profile.banner)
		candidate.channelID = profile.channelID
		if candidate.handle == "" {
			candidate.handle = profile.handle
		}
	} else {
		sb.WriteString("  profile: missing\n")
	}

	writeIdentityFeedTimeline(sb, conn, candidate, limit)
	sb.WriteString("\n")
}

type profileStatus struct {
	found             bool
	channelID         string
	platform          string
	handle            string
	displayName       string
	fetchedAt         int64
	tombstone         int
	jobFound          bool
	requestedRevision int64
	completedRevision int64
	attempts          int
	nextAttemptAtMs   int64
	leaseOwner        string
	leaseUntilMs      int64
	lastError         string
	avatar            profileAssetStatus
	banner            profileAssetStatus
}

type profileAssetStatus struct {
	state     string
	filePath  string
	sourceURL string
	errorKind string
	updatedAt int64
}

func loadProfileStatus(conn *sql.DB, candidate identityCandidate) profileStatus {
	var profile profileStatus
	err := conn.QueryRow(`
		SELECT cp.channel_id, cp.platform, COALESCE(cp.handle, ''), COALESCE(cp.display_name, ''),
		       cp.fetched_at, COALESCE(cp.tombstone, 0),
		       CASE WHEN pj.channel_id IS NULL THEN 0 ELSE 1 END,
		       COALESCE(pj.requested_revision, 0), COALESCE(pj.completed_revision, 0),
		       COALESCE(pj.attempts, 0), COALESCE(pj.next_attempt_at_ms, 0),
		       COALESCE(pj.lease_owner, ''), COALESCE(pj.lease_until_ms, 0), COALESCE(pj.last_error, ''),
		       COALESCE(avatar.state, ''), COALESCE(avatar.file_path, ''),
		       COALESCE(avatar.source_url, ''), COALESCE(avatar.last_error_kind, ''), COALESCE(avatar.updated_at_ms, 0),
		       COALESCE(banner.state, ''), COALESCE(banner.file_path, ''),
		       COALESCE(banner.source_url, ''), COALESCE(banner.last_error_kind, ''), COALESCE(banner.updated_at_ms, 0)
		FROM channel_profiles cp
		LEFT JOIN profile_jobs pj ON pj.channel_id = cp.channel_id
		LEFT JOIN assets avatar
		  ON avatar.owner_kind = 'channel' AND avatar.owner_id = cp.channel_id
		 AND avatar.asset_kind = 'avatar' AND avatar.media_index = 0
		LEFT JOIN assets banner
		  ON banner.owner_kind = 'channel' AND banner.owner_id = cp.channel_id
		 AND banner.asset_kind = 'banner' AND banner.media_index = 0
		WHERE cp.channel_id = ?
		   OR (cp.platform = ? AND LOWER(COALESCE(cp.handle, '')) = LOWER(?))
		ORDER BY CASE WHEN cp.channel_id = ? THEN 0 ELSE 1 END
		LIMIT 1
	`, candidate.channelID, candidate.platform, candidate.handle, candidate.channelID).Scan(
		&profile.channelID,
		&profile.platform,
		&profile.handle,
		&profile.displayName,
		&profile.fetchedAt,
		&profile.tombstone,
		&profile.jobFound,
		&profile.requestedRevision,
		&profile.completedRevision,
		&profile.attempts,
		&profile.nextAttemptAtMs,
		&profile.leaseOwner,
		&profile.leaseUntilMs,
		&profile.lastError,
		&profile.avatar.state,
		&profile.avatar.filePath,
		&profile.avatar.sourceURL,
		&profile.avatar.errorKind,
		&profile.avatar.updatedAt,
		&profile.banner.state,
		&profile.banner.filePath,
		&profile.banner.sourceURL,
		&profile.banner.errorKind,
		&profile.banner.updatedAt,
	)
	if err == nil {
		profile.found = true
	}
	return profile
}

func writeProfileAssetStatus(sb *strings.Builder, kind string, asset profileAssetStatus) {
	if asset.state == "" {
		return
	}
	fmt.Fprintf(sb, "    %s: file=%s source=%t updated=%s error_kind=%s\n",
		kind, emptyDash(asset.filePath), asset.sourceURL != "",
		formatMillis(asset.updatedAt), emptyDash(asset.errorKind))
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
		FROM feed_items_resolved
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
		FROM feed_items_resolved
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
