package db

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"
)

type AndroidSyncDesiredSets struct {
	Tweets           map[string]struct{}
	TweetAssetOwners map[string]struct{}
	FeedRanks        map[string]struct{}
	Videos           map[string]struct{}
	MediaVideos      map[string]struct{}
	Channels         map[string]struct{}
}

func (s AndroidSyncDesiredSets) SortedTweets() []string { return sortedKeys(s.Tweets) }
func (s AndroidSyncDesiredSets) SortedTweetAssetOwners() []string {
	owners := make(map[string]struct{}, len(s.Tweets)+len(s.TweetAssetOwners))
	for id := range s.Tweets {
		owners[id] = struct{}{}
	}
	for id := range s.TweetAssetOwners {
		owners[id] = struct{}{}
	}
	return sortedKeys(owners)
}
func (s AndroidSyncDesiredSets) HasTweetAssetOwner(id string) bool {
	if _, ok := s.Tweets[id]; ok {
		return true
	}
	_, ok := s.TweetAssetOwners[id]
	return ok
}
func (s AndroidSyncDesiredSets) SortedVideos() []string      { return sortedKeys(s.Videos) }
func (s AndroidSyncDesiredSets) SortedMediaVideos() []string { return sortedKeys(s.MediaVideos) }
func (s AndroidSyncDesiredSets) SortedChannels() []string    { return sortedKeys(s.Channels) }

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if strings.TrimSpace(k) != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func retentionCutoffMs(nowMs int64, days int) int64 {
	if days <= 0 {
		return math.MaxInt64
	}
	return nowMs - int64(days)*86_400_000
}

func androidEligibleFeedCTE(cutoffMs int64) (string, []any) {
	return `
		WITH RECURSIVE
		recent_hashes AS (
			SELECT DISTINCT content_hash
			FROM feed_items
			WHERE content_hash IS NOT NULL AND content_hash != ''
			  AND published_at >= ?

			UNION

			SELECT DISTINCT content_hash
			FROM retweet_sources
			WHERE content_hash IS NOT NULL AND content_hash != ''
			  AND published_at >= ?

			UNION

			SELECT DISTINCT q.content_hash
			FROM feed_items parent
			JOIN feed_items q ON q.tweet_id = parent.quote_tweet_id
			WHERE parent.published_at >= ?
			  AND q.content_hash IS NOT NULL
			  AND q.content_hash != ''
		),
		protected_hashes AS (
			SELECT DISTINCT fi.content_hash
			FROM feed_items fi
			JOIN feed_likes fl ON fl.tweet_id = fi.tweet_id
			WHERE fi.content_hash IS NOT NULL
			  AND fi.content_hash != ''

			UNION

			SELECT DISTINCT fi.content_hash
			FROM feed_items fi
			JOIN bookmarks b ON b.video_id = fi.tweet_id
			WHERE fi.content_hash IS NOT NULL
			  AND fi.content_hash != ''
		),
		eligible_tweet_ids AS (
			SELECT tweet_id
			FROM feed_items
			WHERE published_at >= ?

			UNION

			SELECT fi.tweet_id
			FROM recent_hashes rh
			CROSS JOIN feed_items fi INDEXED BY idx_feed_items_content_hash
			WHERE fi.content_hash = rh.content_hash
			  AND fi.content_hash IS NOT NULL
			  AND fi.content_hash != ''

			UNION

			SELECT quote_tweet_id AS tweet_id
			FROM feed_items
			WHERE quote_tweet_id IS NOT NULL
			  AND quote_tweet_id != ''
			  AND published_at >= ?

			UNION

			SELECT fl.tweet_id
			FROM feed_likes fl

			UNION

			SELECT b.video_id
			FROM bookmarks b

			UNION

			SELECT fi.tweet_id
			FROM protected_hashes ph
			CROSS JOIN feed_items fi INDEXED BY idx_feed_items_content_hash
			WHERE fi.content_hash = ph.content_hash
			  AND fi.content_hash IS NOT NULL
			  AND fi.content_hash != ''
		)
	`, []any{
			cutoffMs, cutoffMs, cutoffMs,
			cutoffMs, cutoffMs,
		}
}

func (db *DB) listAndroidSyncDesiredFeed(feedDays int, nowMs int64) (map[string]struct{}, map[string]struct{}, error) {
	tweets := map[string]struct{}{}
	assetOwners := map[string]struct{}{}
	feedCutoff := retentionCutoffMs(nowMs, feedDays)
	cte, args := androidEligibleFeedCTE(feedCutoff)
	start := time.Now()
	err := db.collectStrings(cte+`,
		reply_chain(tweet_id, is_ancestor) AS (
			SELECT tweet_id, 0 FROM eligible_tweet_ids

			UNION

			SELECT fi.reply_to_status, 1
			FROM reply_chain rc
			JOIN feed_items fi ON fi.tweet_id = rc.tweet_id
			WHERE COALESCE(fi.reply_to_status, '') != ''
		)
		SELECT DISTINCT fi.tweet_id
		FROM reply_chain rc
		JOIN feed_items fi ON fi.tweet_id = rc.tweet_id
		WHERE rc.is_ancestor = 1
		   OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = fi.tweet_id)
		   OR EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = fi.tweet_id)
		   OR (`+retweetFilterClause("fi")+`)
	`, args, tweets)
	androidSyncLogDesiredSetQuery("feed", len(tweets), len(tweets), start, err)
	if err != nil {
		return nil, nil, fmt.Errorf("android sync desired tweets: %w", err)
	}
	for id := range tweets {
		assetOwners[id] = struct{}{}
	}
	tweetIDsJSON, err := json.Marshal(sortedKeys(tweets))
	if err != nil {
		return nil, nil, err
	}
	start = time.Now()
	before := len(assetOwners)
	err = db.collectStrings(`
		SELECT DISTINCT quote_tweet_id
		FROM feed_items
		WHERE tweet_id IN (SELECT value FROM json_each(?))
		  AND COALESCE(quote_tweet_id, '') != ''
	`, []any{string(tweetIDsJSON)}, assetOwners)
	androidSyncLogDesiredSetQuery("quote_asset_owners", len(assetOwners)-before, len(assetOwners), start, err)
	if err != nil {
		return nil, nil, fmt.Errorf("android sync desired quote asset owners: %w", err)
	}
	return tweets, assetOwners, nil
}

// ListAndroidSyncDesiredFeedAssetOwners is the shared feed-media ownership
// boundary used by Android sync and server-side X retention.
func (db *DB) ListAndroidSyncDesiredFeedAssetOwners(feedDays int, nowMs int64) ([]string, error) {
	_, owners, err := db.listAndroidSyncDesiredFeed(feedDays, nowMs)
	if err != nil {
		return nil, err
	}
	return sortedKeys(owners), nil
}

// ListAndroidSyncDesiredContent returns the canonical retained/protected
// content selection without expanding its identity dependencies.
func (db *DB) ListAndroidSyncDesiredContent(settings AndroidRetentionSettings, nowMs int64) (AndroidSyncDesiredSets, error) {
	out := AndroidSyncDesiredSets{
		Tweets:           map[string]struct{}{},
		TweetAssetOwners: map[string]struct{}{},
		Videos:           map[string]struct{}{},
		MediaVideos:      map[string]struct{}{},
		Channels:         map[string]struct{}{},
	}
	collect := func(name string, query string, args []any, dest map[string]struct{}) error {
		start := time.Now()
		before := len(dest)
		err := db.collectStrings(query, args, dest)
		androidSyncLogDesiredSetQuery(name, len(dest)-before, len(dest), start, err)
		return err
	}
	collectStories := func(name string, cutoffMs int64, dest map[string]struct{}) error {
		start := time.Now()
		before := len(dest)
		err := db.collectStoryVideoIDs(cutoffMs, dest)
		androidSyncLogDesiredSetQuery(name, len(dest)-before, len(dest), start, err)
		return err
	}

	youtubeCutoff := retentionCutoffMs(nowMs, settings.YoutubeDays)
	momentsCutoff := retentionCutoffMs(nowMs, settings.MomentsDays)
	storyCutoff := int64(math.MaxInt64)
	if settings.StoryHours > 0 {
		storyCutoff = nowMs - int64(settings.StoryHours)*3_600_000
	}
	includeMomentReposts := db.MomentsIncludeRepostsEnabled()
	includeInstagramTagged := db.InstagramIncludeTaggedEnabled()
	includeSourceWindows := includeMomentReposts || includeInstagramTagged

	var err error
	out.Tweets, out.TweetAssetOwners, err = db.listAndroidSyncDesiredFeed(settings.FeedDays, nowMs)
	if err != nil {
		return out, err
	}

	if err := collect(
		"content_videos",
		androidSyncDesiredVideoRowsSQL("v.video_id"),
		androidSyncDesiredVideoRowsArgs(youtubeCutoff, momentsCutoff),
		out.Videos,
	); err != nil {
		return out, fmt.Errorf("android sync desired content videos: %w", err)
	}
	if includeSourceWindows {
		if err := collect("repost_content_videos", `
			SELECT DISTINCT v.video_id
			FROM videos v
			INNER JOIN video_repost_sources vrs ON vrs.video_id = v.video_id
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
			LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
			WHERE `+sourceWindowPlatformEnabledClause("v", includeMomentReposts, includeInstagramTagged)+`
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND COALESCE(rcs.include_reposts, 1) != 0
			  AND COALESCE(NULLIF(vrs.reposted_at_ms, 0), NULLIF(vrs.first_seen_at_ms, 0), v.published_at, 0) >= ?
		`, []any{momentsCutoff}, out.Videos); err != nil {
			return out, fmt.Errorf("android sync desired repost videos: %w", err)
		}
	}
	if err := collectStories("story_content_videos", storyCutoff, out.Videos); err != nil {
		return out, fmt.Errorf("android sync desired story videos: %w", err)
	}
	for id := range out.Videos {
		out.MediaVideos[id] = struct{}{}
	}
	if len(out.MediaVideos) > 0 {
		videoIDsJSON, err := json.Marshal(out.SortedMediaVideos())
		if err != nil {
			return out, err
		}
		excluded := map[string]struct{}{}
		if err := collect("media_video_cutoff", `
			SELECT v.video_id
			FROM videos v
			WHERE v.video_id IN (SELECT value FROM json_each(?))
			  AND v.channel_id LIKE 'youtube_%'
			  AND COALESCE(v.published_at, 0) < ?
			  AND NOT EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = v.video_id)
			  AND NOT EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = v.video_id)
		`, []any{string(videoIDsJSON), youtubeCutoff}, excluded); err != nil {
			return out, fmt.Errorf("android sync media video cutoff: %w", err)
		}
		for id := range excluded {
			delete(out.MediaVideos, id)
		}
	}
	return out, nil
}

// ListAndroidSyncDesiredSets adds the identity dependencies needed to
// materialize the canonical content selection.
func (db *DB) ListAndroidSyncDesiredSets(settings AndroidRetentionSettings, nowMs int64) (AndroidSyncDesiredSets, error) {
	out, err := db.ListAndroidSyncDesiredContent(settings, nowMs)
	if err != nil {
		return out, err
	}
	collect := func(name string, query string, args []any, dest map[string]struct{}) error {
		start := time.Now()
		before := len(dest)
		err := db.collectStrings(query, args, dest)
		androidSyncLogDesiredSetQuery(name, len(dest)-before, len(dest), start, err)
		return err
	}
	tweetIDsJSON, err := json.Marshal(out.SortedTweets())
	if err != nil {
		return out, err
	}

	if err := collect("state_channels", `
		WITH desired(channel_id) AS (
			SELECT channel_id FROM channel_follows
			UNION SELECT channel_id FROM channel_stars
			UNION SELECT channel_id FROM muted_channels
			UNION
			SELECT channel_id FROM channel_settings
			WHERE media_only IS NOT NULL OR include_reposts IS NOT NULL
			   OR media_download_limit IS NOT NULL OR max_videos IS NOT NULL
			   OR download_subtitles IS NOT NULL
		)
		SELECT desired.channel_id
		FROM desired
		LEFT JOIN channel_profiles cp ON cp.channel_id = desired.channel_id
		WHERE COALESCE(cp.tombstone, 0) = 0
	`, nil, out.Channels); err != nil {
		return out, fmt.Errorf("android sync desired state channels: %w", err)
	}
	if err := collect("feed_channels", `
		WITH desired(tweet_id) AS (SELECT value FROM json_each(?)),
		candidates(channel_id) AS (
			SELECT source_channel_id FROM feed_items fi JOIN desired d ON d.tweet_id = fi.tweet_id
			UNION SELECT channel_id FROM feed_items fi JOIN desired d ON d.tweet_id = fi.tweet_id
			UNION SELECT quote_channel_id FROM feed_items fi JOIN desired d ON d.tweet_id = fi.tweet_id
			UNION SELECT reply_channel_id FROM feed_items fi JOIN desired d ON d.tweet_id = fi.tweet_id
			UNION SELECT reposter_channel_id FROM feed_items fi JOIN desired d ON d.tweet_id = fi.tweet_id
			UNION
			SELECT rs.retweeter_channel_id
			FROM retweet_sources rs
			JOIN feed_items fi ON fi.content_hash = rs.content_hash
			JOIN desired d ON d.tweet_id = fi.tweet_id
		)
		SELECT candidates.channel_id
		FROM candidates
		LEFT JOIN channel_profiles cp ON cp.channel_id = candidates.channel_id
		WHERE COALESCE(candidates.channel_id, '') != ''
		  AND COALESCE(cp.tombstone, 0) = 0
	`, []any{string(tweetIDsJSON)}, out.Channels); err != nil {
		return out, fmt.Errorf("android sync desired feed channels: %w", err)
	}
	videoIDsJSON, err := json.Marshal(out.SortedVideos())
	if err != nil {
		return out, err
	}
	if err := collect("video_channels", `
		WITH desired(video_id) AS (SELECT value FROM json_each(?)),
		candidates(channel_id) AS (
			SELECT channel_id FROM videos v JOIN desired d ON d.video_id = v.video_id
			UNION
			SELECT reposter_channel_id FROM video_repost_sources vrs
			JOIN desired d ON d.video_id = vrs.video_id
		)
		SELECT candidates.channel_id
		FROM candidates
		LEFT JOIN channel_profiles cp ON cp.channel_id = candidates.channel_id
		WHERE COALESCE(candidates.channel_id, '') != ''
		  AND COALESCE(cp.tombstone, 0) = 0
	`, []any{string(videoIDsJSON)}, out.Channels); err != nil {
		return out, fmt.Errorf("android sync desired video channels: %w", err)
	}

	return out, nil
}

func androidSyncLogDesiredSetQuery(name string, added int, total int, start time.Time, err error) {
	fields := []any{
		"query", name,
		"added", added,
		"total", total,
		"duration_ms", time.Since(start).Milliseconds(),
	}
	if err != nil {
		fields = append(fields, "err", err)
		slog.Warn("android_sync_desired_set_query", fields...)
		return
	}
	slog.Info("android_sync_desired_set_query", fields...)
}

func androidSyncDesiredVideoRowsSQL(selectExpr string) string {
	return fmt.Sprintf(`
		SELECT %s
		FROM channel_follows cf
		JOIN videos v ON v.channel_id = cf.channel_id
		WHERE v.channel_id LIKE 'youtube_%%'
		  AND COALESCE(v.published_at, 0) >= ?

		UNION
		SELECT %s
		FROM channel_follows cf
		JOIN videos v ON v.channel_id = cf.channel_id
		WHERE (v.channel_id LIKE 'tiktok_%%' OR v.channel_id LIKE 'instagram_%%')
		  AND COALESCE(v.source_kind, '') != 'story'
		  AND COALESCE(v.published_at, 0) >= ?

		UNION
		SELECT %s
		FROM bookmarks b
		JOIN videos v ON v.video_id = b.video_id
		WHERE (
		    v.channel_id LIKE 'youtube_%%'
		    OR v.channel_id LIKE 'tiktok_%%'
		    OR v.channel_id LIKE 'instagram_%%'
		  )

		UNION
		SELECT %s
		FROM feed_likes fl
		JOIN videos v ON v.video_id = fl.tweet_id
		WHERE (
		    v.channel_id LIKE 'youtube_%%'
		    OR v.channel_id LIKE 'tiktok_%%'
		    OR v.channel_id LIKE 'instagram_%%'
		  )
	`, selectExpr, selectExpr, selectExpr, selectExpr)
}

func androidSyncDesiredVideoRowsArgs(youtubeCutoff, momentsCutoff int64) []any {
	return []any{youtubeCutoff, momentsCutoff}
}

func (db *DB) collectStoryVideoIDs(storyCutoff int64, into map[string]struct{}) error {
	return db.collectStrings(`
		SELECT DISTINCT sv.video_id
		FROM videos sv
		INNER JOIN channels c ON c.channel_id = sv.channel_id
		INNER JOIN channel_follows cf ON cf.channel_id = sv.channel_id
		WHERE COALESCE(c.platform, '') IN ('tiktok','instagram')
		  AND COALESCE(sv.source_kind, '') = 'story'
		  AND COALESCE(sv.published_at, 0) >= ?
		  AND `+validStoryVideoSQL("sv", "c")+`
	`, []any{storyCutoff}, into)
}

func (db *DB) collectStrings(query string, args []any, into map[string]struct{}) error {
	rows, err := db.reader().Query(query, args...)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return err
		}
		if strings.TrimSpace(value) != "" {
			into[value] = struct{}{}
		}
	}
	return rows.Err()
}

func stringChunks(values []string, size int) [][]string {
	if size <= 0 || len(values) == 0 {
		return nil
	}
	var chunks [][]string
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, values[start:end])
	}
	return chunks
}
