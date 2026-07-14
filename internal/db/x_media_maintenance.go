package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type DataFileRemovalResult struct {
	Considered        int   `json:"considered"`
	Removed           int   `json:"removed"`
	RemovedBytes      int64 `json:"removed_bytes"`
	StillReferenced   int   `json:"still_referenced"`
	Missing           int   `json:"missing"`
	RemoveErrors      int   `json:"remove_errors"`
	InvalidOrEmpty    int   `json:"invalid_or_empty"`
	DuplicateRequests int   `json:"duplicate_requests"`
}

type XMediaRetentionOptions struct {
	NowMs          int64 `json:"now_ms"`
	Limit          int   `json:"limit"`
	RetentionLimit int   `json:"retention_limit"`
	DryRun         bool  `json:"dry_run"`
}

type XMediaRetentionResult struct {
	DryRun             bool                  `json:"dry_run"`
	Limit              int                   `json:"limit"`
	RetentionLimit     int                   `json:"retention_limit"`
	LimitReached       bool                  `json:"limit_reached"`
	SourcesScanned     int                   `json:"sources_scanned"`
	SourcesOverLimit   int                   `json:"sources_over_limit"`
	ProtectedItems     int                   `json:"protected_items"`
	KeptItems          int                   `json:"kept_items"`
	PrunedItems        int                   `json:"pruned_items"`
	AssetsPruned       int                   `json:"assets_pruned"`
	AssetsRestored     int                   `json:"assets_restored"`
	CandidateFileBytes int64                 `json:"candidate_file_bytes"`
	FileRemoval        DataFileRemovalResult `json:"file_removal"`
}

type xRetentionSource struct {
	channelID string
	handle    string
}

type xRetentionItem struct {
	tweetID   string
	protected bool
}

func (db *DB) PruneXMediaRetentionForChannel(channelID string, opts XMediaRetentionOptions) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{DryRun: opts.DryRun, Limit: 1, RetentionLimit: opts.RetentionLimit}
	source, ok := xRetentionSourceFromChannelID(channelID)
	if !ok {
		return result, nil
	}
	limit, err := db.xRetentionLimit(source, opts.RetentionLimit)
	if err != nil || limit <= 0 {
		return result, err
	}
	result.RetentionLimit = limit
	items, err := db.xMediaRetentionItems(channelID)
	if err != nil {
		return result, err
	}
	pruneIDs := addXRetentionStats(&result, items, limit)
	if len(pruneIDs) == 0 {
		return result, nil
	}
	candidates, err := db.xAssetOwnerIDsForTweets(pruneIDs)
	if err != nil {
		return result, err
	}
	retained, err := db.xRetainedMediaOwnerSet(normalizeNowMs(opts.NowMs), opts.RetentionLimit, candidates)
	if err != nil {
		return result, err
	}
	return db.reconcileXMediaOwnerSet(result, retained, candidates, false, DownloadLaneBackfill, normalizeNowMs(opts.NowMs))
}

func (db *DB) ReconcileXMediaRetentionChanges(channelID string, changedTweetIDs []string, opts XMediaRetentionOptions) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{DryRun: opts.DryRun, Limit: 1, RetentionLimit: opts.RetentionLimit}
	source, ok := xRetentionSourceFromChannelID(channelID)
	if !ok {
		return result, nil
	}
	changedTweetIDs = uniqueStrings(changedTweetIDs)
	if len(changedTweetIDs) == 0 {
		return result, nil
	}
	limit, err := db.xRetentionLimit(source, opts.RetentionLimit)
	if err != nil || limit <= 0 {
		return result, err
	}
	result.RetentionLimit = limit
	result.SourcesScanned = 1
	retainedTweets, displaced, err := db.xMediaRetentionWindowAndBoundary(channelID, limit, len(changedTweetIDs))
	if err != nil {
		return result, err
	}
	result.KeptItems = len(retainedTweets)
	result.PrunedItems = len(displaced)
	if len(displaced) > 0 {
		result.SourcesOverLimit = 1
	}
	candidates, err := db.xAssetOwnerIDsForTweets(append(changedTweetIDs, displaced...))
	if err != nil {
		return result, err
	}
	nowMs := normalizeNowMs(opts.NowMs)
	retained, err := db.xRetainedMediaOwnerSet(nowMs, opts.RetentionLimit, candidates)
	if err != nil {
		return result, err
	}
	return db.reconcileXMediaOwnerSet(result, retained, candidates, false, DownloadLaneCurrent, nowMs)
}

func (db *DB) PruneXMediaRetention(opts XMediaRetentionOptions) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{DryRun: opts.DryRun, Limit: opts.Limit, RetentionLimit: opts.RetentionLimit}
	nowMs := normalizeNowMs(opts.NowMs)
	var pruneIDs []string
	if opts.Limit > 0 {
		sources, err := db.followedXMediaRetentionSources()
		if err != nil {
			return result, err
		}
		if len(sources) > opts.Limit {
			sources = sources[:opts.Limit]
			result.LimitReached = true
		}
		for _, source := range sources {
			limit, err := db.xRetentionLimit(source, opts.RetentionLimit)
			if err != nil {
				return result, err
			}
			if limit <= 0 {
				continue
			}
			items, err := db.xMediaRetentionItems(source.channelID)
			if err != nil {
				return result, err
			}
			pruneIDs = append(pruneIDs, addXRetentionStats(&result, items, limit)...)
		}
	}
	retained, err := db.xRetainedMediaOwnerSet(nowMs, opts.RetentionLimit, nil)
	if err != nil {
		return result, err
	}
	if !opts.DryRun {
		feedLimit := db.IntSetting("media_download_limit_default")
		if feedLimit < 1 {
			feedLimit = 1
		}
		feedSources, err := db.ListFeedSources("twitter")
		if err != nil {
			return result, err
		}
		for _, source := range feedSources {
			if !source.Enabled {
				continue
			}
			items, err := db.xFeedSourceRetentionItems(source.SourceID)
			if err != nil {
				return result, err
			}
			removeIDs := addXRetentionStats(&result, items, feedLimit)
			if err := db.deleteXFeedSourceAttribution(source.SourceID, removeIDs); err != nil {
				return result, err
			}
		}
	}
	if opts.Limit > 0 {
		candidates, err := db.xAssetOwnerIDsForTweets(pruneIDs)
		if err != nil {
			return result, err
		}
		return db.reconcileXMediaOwnerSet(result, retained, candidates, false, DownloadLaneBackfill, nowMs)
	}
	return db.reconcileXMediaOwnerSet(result, retained, nil, true, DownloadLaneBackfill, nowMs)
}

func (db *DB) RestoreXMediaRetentionForChannel(channelID string, nowMs int64) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{Limit: 1}
	source, ok := xRetentionSourceFromChannelID(channelID)
	if !ok {
		return result, nil
	}
	limit, err := db.xRetentionLimit(source, 0)
	if err != nil || limit <= 0 {
		return result, err
	}
	result.RetentionLimit = limit
	items, err := db.xMediaRetentionItems(channelID)
	if err != nil {
		return result, err
	}
	result.SourcesScanned = 1
	candidates, err := db.xAssetOwnerIDsForTweets(retainedXMediaTweetIDs(items, limit))
	if err != nil || len(candidates) == 0 {
		return result, err
	}
	nowMs = normalizeNowMs(nowMs)
	retained, err := db.xRetainedMediaOwnerSet(nowMs, 0, candidates)
	if err != nil {
		return result, err
	}
	result.AssetsRestored, err = db.restorePrunedXMediaOwners(sortedKeys(retained), DownloadLaneBackfill, nowMs)
	return result, err
}

func (db *DB) RestoreXMediaForAndroidFeed(feedDays int, nowMs int64) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{}
	if !IsValidRetentionDays(feedDays) {
		return result, fmt.Errorf("invalid Android feed retention: %d", feedDays)
	}
	nowMs = normalizeNowMs(nowMs)
	retained, err := db.xRetainedMediaOwnerSetForFeedDays(nowMs, 0, nil, feedDays)
	if err != nil {
		return result, err
	}
	restored, err := db.restorePrunedXMediaOwners(sortedKeys(retained), DownloadLaneBackfill, nowMs)
	result.AssetsRestored = restored
	return result, err
}

func normalizeNowMs(nowMs int64) int64 {
	if nowMs > 0 {
		return nowMs
	}
	return time.Now().UnixMilli()
}

func addXRetentionStats(result *XMediaRetentionResult, items []xRetentionItem, limit int) []string {
	result.SourcesScanned++
	kept := 0
	var pruneIDs []string
	for _, item := range items {
		if item.protected {
			result.ProtectedItems++
			continue
		}
		if kept < limit {
			kept++
			result.KeptItems++
			continue
		}
		pruneIDs = append(pruneIDs, item.tweetID)
		result.PrunedItems++
	}
	if len(pruneIDs) > 0 {
		result.SourcesOverLimit++
	}
	return pruneIDs
}

func (db *DB) xRetainedMediaOwnerSet(nowMs int64, followedOverride int, candidates []string) (map[string]struct{}, error) {
	state, err := db.GetAndroidFeedRetention()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("Android feed retention is not initialized")
	}
	return db.xRetainedMediaOwnerSetForFeedDays(nowMs, followedOverride, candidates, state.FeedDays)
}

func (db *DB) xRetainedMediaOwnerSetForFeedDays(nowMs int64, followedOverride int, candidates []string, feedDays int) (map[string]struct{}, error) {
	var androidOwners []string
	var err error
	if len(candidates) == 0 {
		androidOwners, err = db.ListAndroidSyncDesiredFeedAssetOwners(feedDays, nowMs)
	} else {
		androidOwners, err = db.ListAndroidSyncDesiredFeedAssetOwnersAmong(feedDays, nowMs, candidates)
	}
	if err != nil {
		return nil, err
	}
	candidateSet := make(map[string]struct{}, len(candidates))
	for _, ownerID := range uniqueStrings(candidates) {
		candidateSet[ownerID] = struct{}{}
	}
	retained := make(map[string]struct{}, len(androidOwners))
	for _, ownerID := range androidOwners {
		if len(candidateSet) == 0 {
			retained[ownerID] = struct{}{}
		} else if _, ok := candidateSet[ownerID]; ok {
			retained[ownerID] = struct{}{}
		}
	}
	if len(candidateSet) > 0 {
		if err := db.addCandidateServerXMediaOwners(retained, candidateSet, followedOverride); err != nil {
			return nil, err
		}
		return retained, nil
	}

	feedLimit := db.IntSetting("media_download_limit_default")
	if feedLimit < 1 {
		feedLimit = 1
	}
	if err := db.collectStrings(`
		WITH asset_owners AS MATERIALIZED (
			SELECT DISTINCT owner_id
			FROM assets
			WHERE owner_kind = 'tweet'
			  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		), candidate_items AS MATERIALIZED (
			SELECT fi.tweet_id, COALESCE(fi.quote_tweet_id, '') AS quote_tweet_id,
			       fi.reposter_channel_id, fi.source_channel_id, fi.channel_id, fi.published_at
			FROM asset_owners ao
			JOIN feed_items fi ON fi.tweet_id = ao.owner_id

			UNION

			SELECT fi.tweet_id, COALESCE(fi.quote_tweet_id, '') AS quote_tweet_id,
			       fi.reposter_channel_id, fi.source_channel_id, fi.channel_id, fi.published_at
			FROM asset_owners ao
			JOIN feed_items fi INDEXED BY idx_feed_items_quote ON fi.quote_tweet_id = ao.owner_id
			WHERE fi.quote_tweet_id IS NOT NULL AND fi.quote_tweet_id != ''
		), followed_base AS MATERIALIZED (
			SELECT ci.tweet_id, ci.quote_tweet_id,
			       COALESCE(NULLIF(ci.reposter_channel_id, ''), NULLIF(ci.source_channel_id, ''), ci.channel_id) AS retention_source,
			       ci.published_at,
			       CASE WHEN EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = ci.tweet_id)
			              OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = ci.tweet_id)
			            THEN 1 ELSE 0 END AS protected,
			       CASE WHEN ? > 0 THEN ?
			            WHEN COALESCE(cs.media_download_limit, 0) > 0 THEN cs.media_download_limit
			            ELSE ? END AS retention_limit
			FROM candidate_items ci
			JOIN channel_follows cf
			  ON cf.channel_id = COALESCE(NULLIF(ci.reposter_channel_id, ''), NULLIF(ci.source_channel_id, ''), ci.channel_id)
			LEFT JOIN channel_settings cs ON cs.channel_id = cf.channel_id
		), followed_ranked AS (
			SELECT followed_base.*,
			       SUM(CASE WHEN protected = 0 THEN 1 ELSE 0 END) OVER (
			         PARTITION BY retention_source
			         ORDER BY published_at DESC, tweet_id DESC
			         ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
			       ) AS retention_position
			FROM followed_base
		), retained_tweets AS MATERIALIZED (
			SELECT tweet_id, quote_tweet_id
			FROM followed_ranked
			WHERE protected = 1 OR retention_position <= retention_limit
		)
		SELECT tweet_id FROM retained_tweets
		UNION
		SELECT quote_tweet_id FROM retained_tweets WHERE quote_tweet_id != ''
	`, []any{followedOverride, followedOverride, feedLimit}, retained); err != nil {
		return nil, err
	}

	var retainedTweets []string
	feedSources, err := db.ListFeedSources("twitter")
	if err != nil {
		return nil, err
	}
	for _, source := range feedSources {
		if !source.Enabled {
			continue
		}
		items, err := db.xFeedSourceRetentionItems(source.SourceID)
		if err != nil {
			return nil, err
		}
		retainedTweets = append(retainedTweets, retainedXMediaTweetIDs(items, feedLimit)...)
	}
	serverOwners, err := db.xAssetOwnerIDsForTweets(retainedTweets)
	if err != nil {
		return nil, err
	}
	for _, ownerID := range serverOwners {
		retained[ownerID] = struct{}{}
	}
	rows, err := db.conn.Query(`
		SELECT video_id AS owner_id FROM bookmarks
		UNION
		SELECT tweet_id AS owner_id FROM feed_likes
		UNION
		SELECT DISTINCT owner_id
		FROM assets
		WHERE owner_kind = 'tweet'
		  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		  AND required_reason IN ('bookmark', 'like', 'manual')
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ownerID string
		if err := rows.Scan(&ownerID); err != nil {
			return nil, err
		}
		retained[ownerID] = struct{}{}
	}
	return retained, rows.Err()
}

func (db *DB) addCandidateServerXMediaOwners(retained, candidates map[string]struct{}, followedOverride int) error {
	parentIDs := map[string]struct{}{}
	sourceIDs := map[string]struct{}{}
	for _, chunk := range stringChunks(sortedKeys(candidates), 300) {
		query, args := candidateServerXMediaParentsQuery(chunk)
		rows, err := db.conn.Query(query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var parentID, sourceID string
			if err := rows.Scan(&parentID, &sourceID); err != nil {
				_ = rows.Close()
				return err
			}
			parentIDs[parentID] = struct{}{}
			if sourceID != "" {
				sourceIDs[sourceID] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}

	followed := map[string]struct{}{}
	for _, chunk := range stringChunks(sortedKeys(sourceIDs), 400) {
		rows, err := db.conn.Query(`
			SELECT channel_id FROM channel_follows
			WHERE channel_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sourceID string
			if err := rows.Scan(&sourceID); err != nil {
				_ = rows.Close()
				return err
			}
			followed[sourceID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}
	var retainedTweets []string
	for _, sourceID := range sortedKeys(followed) {
		source, ok := xRetentionSourceFromChannelID(sourceID)
		if !ok {
			continue
		}
		limit, err := db.xRetentionLimit(source, followedOverride)
		if err != nil {
			return err
		}
		items, _, err := db.xMediaRetentionWindowAndBoundary(sourceID, limit, 0)
		if err != nil {
			return err
		}
		retainedTweets = append(retainedTweets, items...)
	}

	feedSourceIDs := map[string]struct{}{}
	for _, chunk := range stringChunks(sortedKeys(parentIDs), 400) {
		rows, err := db.conn.Query(`
			SELECT DISTINCT fis.source_id
			FROM feed_item_sources fis
			JOIN feed_sources fs ON fs.source_id = fis.source_id AND fs.enabled = 1
			WHERE fis.tweet_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sourceID string
			if err := rows.Scan(&sourceID); err != nil {
				_ = rows.Close()
				return err
			}
			feedSourceIDs[sourceID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}
	feedLimit := db.IntSetting("media_download_limit_default")
	if feedLimit < 1 {
		feedLimit = 1
	}
	for _, sourceID := range sortedKeys(feedSourceIDs) {
		items, err := db.xFeedSourceRetainedTweetIDs(sourceID, feedLimit)
		if err != nil {
			return err
		}
		retainedTweets = append(retainedTweets, items...)
	}
	for _, chunk := range stringChunks(sortedKeys(parentIDs), 400) {
		args := stringsToAny(chunk)
		rows, err := db.conn.Query(`
			SELECT video_id FROM bookmarks
			WHERE video_id IN (`+placeholders(len(chunk))+`)
			UNION
			SELECT tweet_id FROM feed_likes
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
		`, append(append([]any{}, args...), args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var tweetID string
			if err := rows.Scan(&tweetID); err != nil {
				_ = rows.Close()
				return err
			}
			retainedTweets = append(retainedTweets, tweetID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}
	ownerIDs, err := db.xAssetOwnerIDsForTweets(retainedTweets)
	if err != nil {
		return err
	}
	for _, ownerID := range ownerIDs {
		if _, ok := candidates[ownerID]; ok {
			retained[ownerID] = struct{}{}
		}
	}
	for _, chunk := range stringChunks(sortedKeys(candidates), 400) {
		args := stringsToAny(chunk)
		rows, err := db.conn.Query(`
			SELECT video_id AS owner_id
			FROM bookmarks
			WHERE video_id IN (`+placeholders(len(chunk))+`)
			UNION
			SELECT tweet_id AS owner_id
			FROM feed_likes
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
			UNION
			SELECT DISTINCT owner_id
			FROM assets
			WHERE owner_kind = 'tweet'
			  AND owner_id IN (`+placeholders(len(chunk))+`)
			  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			  AND required_reason IN ('bookmark', 'like', 'manual')
		`, append(append(append([]any{}, args...), args...), args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var ownerID string
			if err := rows.Scan(&ownerID); err != nil {
				_ = rows.Close()
				return err
			}
			retained[ownerID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}
	return nil
}

func candidateServerXMediaParentsQuery(ownerIDs []string) (string, []any) {
	ids := placeholders(len(ownerIDs))
	query := `
		SELECT tweet_id,
		       COALESCE(NULLIF(reposter_channel_id, ''), NULLIF(source_channel_id, ''), channel_id)
		FROM feed_items
		WHERE tweet_id IN (` + ids + `)

		UNION ALL

		SELECT tweet_id,
		       COALESCE(NULLIF(reposter_channel_id, ''), NULLIF(source_channel_id, ''), channel_id)
		FROM feed_items INDEXED BY idx_feed_items_quote
		WHERE quote_tweet_id IS NOT NULL AND quote_tweet_id != ''
		  AND quote_tweet_id IN (` + ids + `)`
	return query, append(stringsToAny(ownerIDs), stringsToAny(ownerIDs)...)
}

func (db *DB) reconcileXMediaOwnerSet(result XMediaRetentionResult, retained map[string]struct{}, candidates []string, pruneAll bool, restoreLane DownloadLane, nowMs int64) (XMediaRetentionResult, error) {
	if !result.DryRun {
		restored, err := db.restorePrunedXMediaOwners(sortedKeys(retained), restoreLane, nowMs)
		if err != nil {
			return result, err
		}
		result.AssetsRestored += restored
	}
	var err error
	if pruneAll {
		candidates, err = db.activeXMediaOwnerIDs()
		if err != nil {
			return result, err
		}
	}
	candidates = ownersOutsideSet(candidates, retained)
	pathSizes, bytes, err := db.xContentAssetPaths(candidates)
	if err != nil {
		return result, err
	}
	result.CandidateFileBytes += bytes
	if result.DryRun || len(candidates) == 0 {
		return result, nil
	}
	pruned, err := db.markXContentAssetsPruned(candidates, nowMs)
	if err != nil {
		return result, err
	}
	result.AssetsPruned += pruned
	db.removeXContentPaths(pathSizes, &result.FileRemoval)
	return result, nil
}

func ownersOutsideSet(ownerIDs []string, retained map[string]struct{}) []string {
	ownerIDs = uniqueStrings(ownerIDs)
	out := ownerIDs[:0]
	for _, ownerID := range ownerIDs {
		if _, ok := retained[ownerID]; !ok {
			out = append(out, ownerID)
		}
	}
	return out
}

func (db *DB) activeXMediaOwnerIDs() ([]string, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT owner_id
		FROM assets
		WHERE owner_kind = 'tweet' AND lifecycle_state = 'active'
		  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var ownerID string
		if err := rows.Scan(&ownerID); err != nil {
			return nil, err
		}
		out = append(out, ownerID)
	}
	return out, rows.Err()
}

func (db *DB) followedXMediaRetentionSources() ([]xRetentionSource, error) {
	rows, err := db.conn.Query(`
		SELECT c.channel_id
		FROM channels c
		JOIN channel_follows cf ON cf.channel_id = c.channel_id
		WHERE c.platform = 'twitter' AND c.channel_id LIKE 'twitter_%'
		ORDER BY c.channel_id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []xRetentionSource
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		if source, ok := xRetentionSourceFromChannelID(channelID); ok {
			out = append(out, source)
		}
	}
	return out, rows.Err()
}

func xRetentionSourceFromChannelID(channelID string) (xRetentionSource, bool) {
	channelID = strings.TrimSpace(channelID)
	handle := strings.TrimPrefix(strings.ToLower(channelID), "twitter_")
	if handle == "" || handle == strings.ToLower(channelID) {
		return xRetentionSource{}, false
	}
	return xRetentionSource{channelID: channelID, handle: handle}, true
}

func (db *DB) xRetentionLimit(source xRetentionSource, override int) (int, error) {
	if override > 0 {
		return override, nil
	}
	settings, err := db.GetChannelSettings(source.channelID)
	if err != nil || settings == nil {
		return 0, err
	}
	return settings.MediaDownloadLimit, nil
}

const xMediaRetentionSourceItemsSQL = `
	SELECT fi.tweet_id, fi.published_at
	FROM feed_items fi INDEXED BY idx_feed_items_reposter_channel
	WHERE fi.reposter_channel_id IS NOT NULL
	  AND fi.reposter_channel_id != ''
	  AND fi.reposter_channel_id = ?
	  AND (
	    COALESCE(fi.media_json, '') NOT IN ('', '[]')
	    OR COALESCE(fi.quote_media_json, '') NOT IN ('', '[]')
	    OR EXISTS (SELECT 1 FROM assets a WHERE a.owner_kind = 'tweet' AND a.owner_id = fi.tweet_id)
	  )

	UNION ALL

	SELECT fi.tweet_id, fi.published_at
	FROM feed_items fi INDEXED BY idx_feed_items_source_channel
	WHERE COALESCE(fi.reposter_channel_id, '') = ''
	  AND fi.source_channel_id = ?
	  AND (
	    COALESCE(fi.media_json, '') NOT IN ('', '[]')
	    OR COALESCE(fi.quote_media_json, '') NOT IN ('', '[]')
	    OR EXISTS (SELECT 1 FROM assets a WHERE a.owner_kind = 'tweet' AND a.owner_id = fi.tweet_id)
	  )

	UNION ALL

	SELECT fi.tweet_id, fi.published_at
	FROM feed_items fi INDEXED BY idx_feed_items_channel
	WHERE COALESCE(fi.reposter_channel_id, '') = ''
	  AND COALESCE(fi.source_channel_id, '') = ''
	  AND fi.channel_id = ?
	  AND (
	    COALESCE(fi.media_json, '') NOT IN ('', '[]')
	    OR COALESCE(fi.quote_media_json, '') NOT IN ('', '[]')
	    OR EXISTS (SELECT 1 FROM assets a WHERE a.owner_kind = 'tweet' AND a.owner_id = fi.tweet_id)
	  )`

func (db *DB) xMediaRetentionItems(channelID string) ([]xRetentionItem, error) {
	rows, err := db.conn.Query(`
		WITH source_items AS (
			`+xMediaRetentionSourceItemsSQL+`
		)
		SELECT si.tweet_id,
		       CASE WHEN EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = si.tweet_id)
		              OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = si.tweet_id)
		            THEN 1 ELSE 0 END
		FROM source_items si
		ORDER BY COALESCE(si.published_at, 0) DESC, si.tweet_id DESC
	`, channelID, channelID, channelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []xRetentionItem
	for rows.Next() {
		var item xRetentionItem
		var protected int
		if err := rows.Scan(&item.tweetID, &protected); err != nil {
			return nil, err
		}
		item.protected = protected != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (db *DB) xMediaRetentionWindowAndBoundary(channelID string, limit, boundary int) ([]string, []string, error) {
	if limit <= 0 {
		return nil, nil, nil
	}
	if boundary < 0 {
		boundary = 0
	}
	rows, err := db.conn.Query(`
		WITH source_items AS (
			`+xMediaRetentionSourceItemsSQL+`
		)
		SELECT si.tweet_id
		FROM source_items si
		WHERE NOT EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = si.tweet_id)
		  AND NOT EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = si.tweet_id)
		ORDER BY COALESCE(si.published_at, 0) DESC, si.tweet_id DESC
		LIMIT ?
	`, channelID, channelID, channelID, limit+boundary)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var retained, displaced []string
	for rows.Next() {
		var tweetID string
		if err := rows.Scan(&tweetID); err != nil {
			return nil, nil, err
		}
		if len(retained) < limit {
			retained = append(retained, tweetID)
		} else {
			displaced = append(displaced, tweetID)
		}
	}
	return retained, displaced, rows.Err()
}

func (db *DB) xContentAssetPaths(ownerIDs []string) (map[string]int64, int64, error) {
	seen := map[string]int64{}
	for _, chunk := range stringChunks(uniqueStrings(ownerIDs), 400) {
		rows, err := db.conn.Query(`
			SELECT mo.file_path, MAX(mo.size_bytes)
			FROM assets a JOIN media_objects mo ON mo.object_id = a.object_id
			WHERE a.owner_kind = 'tweet'
			  AND a.owner_id IN (`+placeholders(len(chunk))+`)
			  AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			  AND a.lifecycle_state = 'active' AND mo.published_revision > 0 AND mo.file_path != ''
			GROUP BY mo.file_path
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var path string
			var size int64
			if err := rows.Scan(&path, &size); err != nil {
				_ = rows.Close()
				return nil, 0, err
			}
			if size > seen[path] {
				seen[path] = size
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		_ = rows.Close()
	}
	var total int64
	for _, path := range sortedInt64Keys(seen) {
		total += seen[path]
	}
	return seen, total, nil
}

func (db *DB) removeXContentPaths(pathSizes map[string]int64, result *DataFileRemovalResult) {
	for _, path := range sortedInt64Keys(pathSizes) {
		result.Considered++
		removed, err := db.RemoveAssetFileIfUnreferenced(path)
		if err != nil {
			result.RemoveErrors++
			continue
		}
		if removed {
			result.Removed++
			result.RemovedBytes += pathSizes[path]
			continue
		}
		var refs int
		if err := db.conn.QueryRow(`
			SELECT COUNT(*) FROM assets a JOIN media_objects mo ON mo.object_id = a.object_id
			WHERE a.lifecycle_state = 'active' AND mo.file_path = ? AND mo.published_revision > 0
		`, path).Scan(&refs); err == nil && refs > 0 {
			result.StillReferenced++
		} else {
			result.Missing++
		}
	}
}

func (db *DB) markXContentAssetsPruned(ownerIDs []string, nowMs int64) (int, error) {
	changed := 0
	for _, chunk := range stringChunks(uniqueStrings(ownerIDs), 400) {
		err := db.WithWrite(func(tx *sql.Tx) error {
			n, err := markXContentAssetsPrunedTx(tx, chunk, nowMs)
			changed += n
			return err
		})
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

func markXContentAssetsPrunedTx(tx *sql.Tx, ownerIDs []string, nowMs int64) (int, error) {
	res, err := tx.Exec(`
		UPDATE assets
		SET lifecycle_state = 'pruned', revision = revision + 1, updated_at_ms = ?
		WHERE owner_kind = 'tweet'
		  AND owner_id IN (`+placeholders(len(ownerIDs))+`)
		  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		  AND lifecycle_state != 'pruned'
	`, append([]any{nowMs}, stringsToAny(ownerIDs)...)...)
	if err != nil {
		return 0, err
	}
	if err := retireUndesiredXContentObjectsTx(tx, ownerIDs, nowMs); err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sortedInt64Keys(values map[string]int64) []string {
	set := make(map[string]struct{}, len(values))
	for key := range values {
		set[key] = struct{}{}
	}
	return sortedKeys(set)
}
