package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// AndroidSyncMaterializerVersion is part of the generation source hash. Bump
// it when server-side asset materialization semantics change and Android needs
// a fresh immutable generation even if the source rows did not change.
const AndroidSyncMaterializerVersion = 36

const (
	defaultAndroidSyncKeepReadyGenerations = 2
	defaultAndroidSyncKeepMinAge           = 6 * time.Hour
	defaultAndroidSyncKeepHealthReports    = 200
	defaultAndroidSyncPruneGenerationBatch = 50
	defaultAndroidSyncPruneRowBatch        = 2000
	defaultAndroidSyncPruneHealthBatch     = 2000
	// DefaultAndroidSyncPruneDrainPasses caps how many bounded prune batches a
	// request path may run before reporting remaining debt.
	DefaultAndroidSyncPruneDrainPasses = 16
)

type AndroidSyncPrunePolicy struct {
	KeepReadyGenerations int
	KeepMinAge           time.Duration
	KeepHealthReports    int
	ProtectGenerationID  string
	MaxGenerationDeletes int
	MaxItemDeletes       int
	MaxAssetDeletes      int
	MaxHealthDeletes     int
}

type AndroidSyncPruneResult struct {
	GenerationsDeleted   int
	ItemsDeleted         int
	AssetsDeleted        int
	HealthReportsDeleted int
}

type AndroidSyncPruneDebt struct {
	EligibleGenerations   int
	EligibleItems         int
	EligibleAssets        int
	EligibleHealthReports int
}

type AndroidSyncPruneDrainResult struct {
	AndroidSyncPruneResult
	Passes int
	Debt   AndroidSyncPruneDebt
}

type AndroidSyncMaintenanceOptions struct {
	NowMs     int64
	Policy    AndroidSyncPrunePolicy
	MaxPasses int
}

type AndroidSyncMaintenanceResult struct {
	StartedAtMs int64
	EndedAtMs   int64
	DurationMs  int64
	Before      AndroidSyncPruneDebt
	Drain       AndroidSyncPruneDrainResult
	After       AndroidSyncPruneDebt
}

func DefaultAndroidSyncPrunePolicy() AndroidSyncPrunePolicy {
	return AndroidSyncPrunePolicy{
		KeepReadyGenerations: defaultAndroidSyncKeepReadyGenerations,
		KeepMinAge:           defaultAndroidSyncKeepMinAge,
		KeepHealthReports:    defaultAndroidSyncKeepHealthReports,
		MaxGenerationDeletes: defaultAndroidSyncPruneGenerationBatch,
		MaxItemDeletes:       defaultAndroidSyncPruneRowBatch,
		MaxAssetDeletes:      defaultAndroidSyncPruneRowBatch,
		MaxHealthDeletes:     defaultAndroidSyncPruneHealthBatch,
	}
}

// AndroidSyncDesiredSets is the server-owned content boundary for one
// Android generation. The web materializer uses it to filter assets and build
// content item payloads.
type AndroidSyncDesiredSets struct {
	Tweets      map[string]struct{}
	Videos      map[string]struct{}
	MediaVideos map[string]struct{}
	Channels    map[string]struct{}
}

func (s AndroidSyncDesiredSets) SortedTweets() []string      { return sortedKeys(s.Tweets) }
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

// ListAndroidSyncDesiredSets returns all retained/protected content owners.
// It mirrors the Android dashboard denominator logic, but exposes identities
// instead of only counts so generation materialization can be exact.
func (db *DB) ListAndroidSyncDesiredSets(username string, settings AndroidRetentionSettings, nowMs int64) (AndroidSyncDesiredSets, error) {
	out := AndroidSyncDesiredSets{
		Tweets:      map[string]struct{}{},
		Videos:      map[string]struct{}{},
		MediaVideos: map[string]struct{}{},
		Channels:    map[string]struct{}{},
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

	feedCutoff := retentionCutoffMs(nowMs, settings.FeedDays)
	youtubeCutoff := retentionCutoffMs(nowMs, settings.YoutubeDays)
	momentsCutoff := retentionCutoffMs(nowMs, settings.MomentsDays)
	storyHours := settings.StoryHours
	if storyHours <= 0 {
		storyHours = 48
	}
	storyCutoff := storyCutoffMs(nowMs, storyHours)
	includeMomentReposts := db.MomentsIncludeRepostsEnabled()
	includeInstagramTagged := db.InstagramIncludeTaggedEnabled()
	includeSourceWindows := includeMomentReposts || includeInstagramTagged

	cte, args := androidEligibleFeedCTE(username, feedCutoff)
	if err := collect("tweets", cte+`
		SELECT DISTINCT fi.tweet_id
		FROM eligible_tweet_ids e
		CROSS JOIN feed_items fi
		WHERE fi.tweet_id = e.tweet_id
		  AND `+retweetFilterClause("fi"), args, out.Tweets); err != nil {
		return out, fmt.Errorf("android sync desired tweets: %w", err)
	}
	if err := collect("thread_ancestors", cte+`,
		reply_chain(tweet_id, depth) AS (
			SELECT tweet_id, 0 FROM eligible_tweet_ids

			UNION

			SELECT fi.reply_to_status, rc.depth + 1
			FROM reply_chain rc
			JOIN feed_items fi ON fi.tweet_id = rc.tweet_id
			WHERE fi.reply_to_status IS NOT NULL
			  AND fi.reply_to_status != ''
			  AND rc.depth < 50
		)
		SELECT DISTINCT fi.tweet_id
		FROM reply_chain rc
		JOIN feed_items fi ON fi.tweet_id = rc.tweet_id
		WHERE rc.tweet_id IS NOT NULL
		  AND rc.tweet_id != ''
	`, args, out.Tweets); err != nil {
		return out, fmt.Errorf("android sync desired thread ancestors: %w", err)
	}

	if err := collect(
		"media_videos",
		androidSyncDesiredVideoRowsSQL("v.video_id", true),
		androidSyncDesiredVideoRowsArgs(username, youtubeCutoff, true, momentsCutoff),
		out.MediaVideos,
	); err != nil {
		return out, fmt.Errorf("android sync desired media videos: %w", err)
	}
	if includeSourceWindows {
		if err := collect("repost_media_videos", `
			SELECT DISTINCT v.video_id
			FROM videos v
			INNER JOIN video_repost_sources vrs ON vrs.video_id = v.video_id
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id AND rcf.user_id = ''
			LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
			WHERE `+sourceWindowPlatformEnabledClause("v", includeMomentReposts, includeInstagramTagged)+`
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND COALESCE(rcs.include_reposts, 1) != 0
			  AND (? = 0 OR COALESCE(NULLIF(vrs.reposted_at_ms, 0), NULLIF(vrs.first_seen_at_ms, 0), v.published_at, 0) >= ?)
		`, []any{momentsCutoff, momentsCutoff}, out.MediaVideos); err != nil {
			return out, fmt.Errorf("android sync desired repost media videos: %w", err)
		}
	}
	if err := collectStories("story_media_videos", storyCutoff, out.MediaVideos); err != nil {
		return out, fmt.Errorf("android sync desired story media videos: %w", err)
	}

	if err := collect(
		"content_videos",
		androidSyncDesiredVideoRowsSQL("v.video_id", false),
		androidSyncDesiredVideoRowsArgs(username, 0, false, momentsCutoff),
		out.Videos,
	); err != nil {
		return out, fmt.Errorf("android sync desired content videos: %w", err)
	}
	if includeSourceWindows {
		if err := collect("repost_content_videos", `
			SELECT DISTINCT v.video_id
			FROM videos v
			INNER JOIN video_repost_sources vrs ON vrs.video_id = v.video_id
			INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id AND rcf.user_id = ''
			LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
			WHERE `+sourceWindowPlatformEnabledClause("v", includeMomentReposts, includeInstagramTagged)+`
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND COALESCE(rcs.include_reposts, 1) != 0
			  AND (? = 0 OR COALESCE(NULLIF(vrs.reposted_at_ms, 0), NULLIF(vrs.first_seen_at_ms, 0), v.published_at, 0) >= ?)
		`, []any{momentsCutoff, momentsCutoff}, out.Videos); err != nil {
			return out, fmt.Errorf("android sync desired repost videos: %w", err)
		}
	}
	if err := collectStories("story_content_videos", storyCutoff, out.Videos); err != nil {
		return out, fmt.Errorf("android sync desired story videos: %w", err)
	}

	contentVideoSQL := androidSyncDesiredVideoRowsSQL("v.video_id, v.channel_id", false)
	contentVideoArgs := androidSyncDesiredVideoRowsArgs(username, 0, false, momentsCutoff)
	channelArgs := append([]any{}, args...)
	channelArgs = append(channelArgs, contentVideoArgs...)
	if err := collect("channels", cte+`
		, reply_chain(tweet_id, depth) AS (
			SELECT tweet_id, 0 FROM eligible_tweet_ids

			UNION

			SELECT fi.reply_to_status, rc.depth + 1
			FROM reply_chain rc
			JOIN feed_items fi ON fi.tweet_id = rc.tweet_id
			WHERE fi.reply_to_status IS NOT NULL
			  AND fi.reply_to_status != ''
			  AND rc.depth < 50
		),
		desired_tweet_ids(tweet_id) AS (
			SELECT tweet_id FROM eligible_tweet_ids

			UNION

			SELECT tweet_id
			FROM reply_chain
			WHERE tweet_id IS NOT NULL
			  AND tweet_id != ''
		),
		content_videos(video_id, channel_id) AS (`+contentVideoSQL+`),
		avatar_channels(channel_id) AS (
			SELECT cf.channel_id
			FROM channel_follows cf
			WHERE cf.user_id = ''

			UNION
			SELECT DISTINCT 'twitter_' || LOWER(fi.author_handle)
			FROM feed_items fi
			JOIN desired_tweet_ids e ON e.tweet_id = fi.tweet_id
			WHERE fi.author_handle != ''

			UNION
			SELECT DISTINCT 'twitter_' || LOWER(fi.quote_author_handle)
			FROM feed_items fi
			JOIN desired_tweet_ids e ON e.tweet_id = fi.tweet_id
			WHERE fi.quote_author_handle != ''

			UNION
			SELECT DISTINCT 'twitter_' || LOWER(rs.retweeter_handle)
			FROM retweet_sources rs
			JOIN feed_items fi ON fi.content_hash = rs.content_hash
			JOIN desired_tweet_ids e ON e.tweet_id = fi.tweet_id
			WHERE rs.retweeter_handle != ''

			UNION
			SELECT channel_id FROM content_videos
			-- YouTube comment author thumbnails remain on video_comments
			-- attachments; commenter authors are not synced as channels.
		)
			SELECT DISTINCT ac.channel_id
			FROM avatar_channels ac
			LEFT JOIN channel_profiles cp ON cp.channel_id = ac.channel_id
			WHERE ac.channel_id != ''
			  AND COALESCE(cp.tombstone, 0) = 0
	`, channelArgs, out.Channels); err != nil {
		return out, fmt.Errorf("android sync desired channels: %w", err)
	}
	for _, chunk := range stringChunks(out.SortedVideos(), 400) {
		if len(chunk) == 0 {
			continue
		}
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		if err := collect("video_channels", `
			SELECT DISTINCT channel_id
			FROM videos
			WHERE video_id IN (`+placeholders(len(chunk))+`)
			  AND COALESCE(channel_id, '') != ''
		`, args, out.Channels); err != nil {
			return out, fmt.Errorf("android sync desired video channels: %w", err)
		}
		if err := collect("video_reposter_channels", `
			SELECT DISTINCT reposter_channel_id
			FROM video_repost_sources
			WHERE video_id IN (`+placeholders(len(chunk))+`)
			  AND COALESCE(reposter_channel_id, '') != ''
		`, args, out.Channels); err != nil {
			return out, fmt.Errorf("android sync desired reposter channels: %w", err)
		}
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

func androidSyncDesiredVideoRowsSQL(selectExpr string, includeYouTubeCutoff bool) string {
	youtubeCutoffSQL := ""
	if includeYouTubeCutoff {
		youtubeCutoffSQL = "AND (? = 0 OR COALESCE(v.published_at, 0) >= ?)"
	}
	return fmt.Sprintf(`
		SELECT %s
		FROM channel_follows cf
		JOIN videos v ON v.channel_id = cf.channel_id
		WHERE cf.user_id = ''
		  AND v.channel_id LIKE 'youtube_%%'
		  %s

		UNION
		SELECT %s
		FROM channel_follows cf
		JOIN videos v ON v.channel_id = cf.channel_id
		WHERE cf.user_id = ''
		  AND (v.channel_id LIKE 'tiktok_%%' OR v.channel_id LIKE 'instagram_%%')
		  AND COALESCE(v.source_kind, '') != 'story'
		  AND (? = 0 OR COALESCE(v.published_at, 0) >= ?)

		UNION
		SELECT %s
		FROM bookmarks b
		JOIN videos v ON v.video_id = b.video_id
		WHERE (b.user_id = '' OR b.user_id = ?)
		  AND (
		    v.channel_id LIKE 'youtube_%%'
		    OR v.channel_id LIKE 'tiktok_%%'
		    OR v.channel_id LIKE 'instagram_%%'
		  )

		UNION
		SELECT %s
		FROM feed_likes fl
		JOIN videos v ON v.video_id = fl.tweet_id
		WHERE fl.username = ?
		  AND (
		    v.channel_id LIKE 'youtube_%%'
		    OR v.channel_id LIKE 'tiktok_%%'
		    OR v.channel_id LIKE 'instagram_%%'
		  )
	`, selectExpr, youtubeCutoffSQL, selectExpr, selectExpr, selectExpr)
}

func androidSyncDesiredVideoRowsArgs(username string, youtubeCutoff int64, includeYouTubeCutoff bool, momentsCutoff int64) []any {
	var args []any
	if includeYouTubeCutoff {
		args = append(args, youtubeCutoff, youtubeCutoff)
	}
	return append(args, momentsCutoff, momentsCutoff, username, username)
}

func (db *DB) collectStoryVideoIDs(storyCutoff int64, into map[string]struct{}) error {
	return db.collectStrings(`
		SELECT DISTINCT sv.video_id
		FROM videos sv
		INNER JOIN channels c ON c.channel_id = sv.channel_id
		INNER JOIN channel_follows cf ON cf.channel_id = sv.channel_id AND cf.user_id = ''
		WHERE COALESCE(c.platform, '') IN ('tiktok','instagram')
		  AND COALESCE(sv.source_kind, '') = 'story'
		  AND (? = 0 OR COALESCE(sv.published_at, 0) >= ?)
		  AND `+validStoryVideoSQL("sv", "c")+`
	`, []any{storyCutoff, storyCutoff}, into)
}

func (db *DB) ListAndroidSyncAlwaysThumbnailVideoIDs() ([]string, error) {
	out := map[string]struct{}{}
	if err := db.collectStrings(`
		SELECT DISTINCT video_id
		FROM videos
		WHERE channel_id LIKE 'youtube_%'
		   OR channel_id LIKE 'tiktok_%'
		   OR channel_id LIKE 'instagram_%'
	`, nil, out); err != nil {
		return nil, err
	}
	return sortedKeys(out), nil
}

func (db *DB) ListAndroidSyncProfileChannelIDs() ([]string, error) {
	out := map[string]struct{}{}
	if err := db.collectStrings(`
		SELECT DISTINCT channel_id
		FROM channel_profiles
		WHERE COALESCE(tombstone, 0) = 0
	`, nil, out); err != nil {
		return nil, err
	}
	return sortedKeys(out), nil
}

func (db *DB) collectStrings(query string, args []any, into map[string]struct{}) error {
	rows, err := db.conn.Query(query, args...)
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

type AndroidSyncMediaAssetRow struct {
	OwnerID    string
	OwnerType  string
	MediaType  string
	MediaIndex int
	FileSize   int64
	FilePath   string
	SourceURL  string
	RecencyMs  int64
}

type AndroidSyncCommentAvatarRow struct {
	ChannelID string
	SourceURL string
	RecencyMs int64
}

func (db *DB) ListAndroidSyncYouTubeCommentAvatarRows(videoIDs []string, limitPerVideo int) ([]AndroidSyncCommentAvatarRow, error) {
	if limitPerVideo <= 0 || len(videoIDs) == 0 {
		return nil, nil
	}
	byChannelID := map[string]AndroidSyncCommentAvatarRow{}
	for _, chunk := range stringChunks(videoIDs, 400) {
		if len(chunk) == 0 {
			continue
		}
		args := make([]any, 0, len(chunk)+1)
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, limitPerVideo)
		rows, err := db.conn.Query(`
			WITH ranked AS (
				SELECT
					COALESCE(vc.author_id, '') AS author_id,
					COALESCE(vc.author_thumbnail, '') AS author_thumbnail,
					COALESCE(NULLIF(vc.published_at, 0), NULLIF(v.published_at, 0), vc.fetched_at, 0) AS recency_ms,
					ROW_NUMBER() OVER (
						PARTITION BY vc.video_id
						ORDER BY COALESCE(vc.like_count, 0) DESC, vc.id ASC
					) AS video_rank
				FROM video_comments vc
				JOIN videos v ON v.video_id = vc.video_id
				WHERE vc.video_id IN (`+placeholders(len(chunk))+`)
				  AND v.channel_id LIKE 'youtube_%'
				  AND COALESCE(vc.author_id, '') != ''
				  AND COALESCE(vc.author_thumbnail, '') != ''
			)
			SELECT author_id, author_thumbnail, recency_ms
			FROM ranked
			WHERE video_rank <= ?
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var authorID, sourceURL string
			var recencyMs int64
			if err := rows.Scan(&authorID, &sourceURL, &recencyMs); err != nil {
				_ = rows.Close()
				return nil, err
			}
			channelID := model.YouTubeCommentAuthorChannelID(authorID)
			if channelID == "" || !androidSyncCommentAvatarURL(sourceURL) {
				continue
			}
			row := AndroidSyncCommentAvatarRow{
				ChannelID: channelID,
				SourceURL: strings.TrimSpace(sourceURL),
				RecencyMs: recencyMs,
			}
			if existing, ok := byChannelID[channelID]; !ok || row.RecencyMs > existing.RecencyMs {
				byChannelID[channelID] = row
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	out := make([]AndroidSyncCommentAvatarRow, 0, len(byChannelID))
	for _, row := range byChannelID {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RecencyMs != out[j].RecencyMs {
			return out[i].RecencyMs > out[j].RecencyMs
		}
		return out[i].ChannelID < out[j].ChannelID
	})
	return out, nil
}

func androidSyncCommentAvatarURL(raw string) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://")
}

func (db *DB) ListAndroidSyncQuoteTweetIDs(tweetIDs []string) ([]string, error) {
	out := map[string]struct{}{}
	for _, chunk := range stringChunks(tweetIDs, 400) {
		if len(chunk) == 0 {
			continue
		}
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := db.conn.Query(`
			SELECT DISTINCT quote_tweet_id
			FROM feed_items
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
			  AND COALESCE(quote_tweet_id, '') != ''
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if strings.TrimSpace(id) != "" {
				out[id] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return sortedKeys(out), nil
}

func (db *DB) ListAndroidSyncMediaAssetRows(ownerType string, ownerIDs []string) ([]AndroidSyncMediaAssetRow, error) {
	var out []AndroidSyncMediaAssetRow
	if ownerType != "feed_media" && ownerType != "quote_media" {
		return out, fmt.Errorf("unsupported android sync media owner type: %s", ownerType)
	}
	for _, chunk := range stringChunks(ownerIDs, 400) {
		if len(chunk) == 0 {
			continue
		}
		args := make([]any, 0, len(chunk)+1)
		args = append(args, ownerType)
		for _, id := range chunk {
			args = append(args, id)
		}
		recencyExpr := "COALESCE(fi.published_at, 0)"
		join := "LEFT JOIN feed_items fi ON fi.tweet_id = mf.owner_id"
		groupBy := ""
		if ownerType == "quote_media" {
			recencyExpr = "0"
			join = ""
		}
		query := `
			SELECT mf.owner_id, mf.owner_type, COALESCE(mf.media_type,''), mf.media_index,
			       COALESCE(mf.file_size,0), COALESCE(mf.file_path,''), COALESCE(mf.source_url,''), ` + recencyExpr + `
			FROM media_files mf
			` + join + `
			WHERE mf.owner_type = ?
			  AND mf.owner_id IN (` + placeholders(len(chunk)) + `)
			  AND COALESCE(mf.file_path, '') != ''
		`
		if ownerType == "feed_media" {
			query += ` AND EXISTS (
				SELECT 1 FROM feed_media_jobs fmj
				WHERE fmj.tweet_id = mf.owner_id AND fmj.status = 'completed'
			)`
		}
		query += groupBy + ` ORDER BY mf.id ASC`
		rows, err := db.conn.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var row AndroidSyncMediaAssetRow
			if err := rows.Scan(
				&row.OwnerID,
				&row.OwnerType,
				&row.MediaType,
				&row.MediaIndex,
				&row.FileSize,
				&row.FilePath,
				&row.SourceURL,
				&row.RecencyMs,
			); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

func (db *DB) ListAndroidSyncQuoteMediaAssetRows(parentTweetIDs []string) ([]AndroidSyncMediaAssetRow, error) {
	quoteRecency := map[string]int64{}
	quoteSet := map[string]struct{}{}
	for _, chunk := range stringChunks(parentTweetIDs, 400) {
		if len(chunk) == 0 {
			continue
		}
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := db.conn.Query(`
			SELECT quote_tweet_id, COALESCE(MAX(published_at), 0)
			FROM feed_items
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
			  AND COALESCE(quote_tweet_id, '') != ''
			GROUP BY quote_tweet_id
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var quoteID string
			var recency int64
			if err := rows.Scan(&quoteID, &recency); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if strings.TrimSpace(quoteID) == "" {
				continue
			}
			quoteSet[quoteID] = struct{}{}
			if recency > quoteRecency[quoteID] {
				quoteRecency[quoteID] = recency
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}

	out, err := db.ListAndroidSyncMediaAssetRows("quote_media", sortedKeys(quoteSet))
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].RecencyMs = quoteRecency[out[i].OwnerID]
	}
	return out, nil
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

// AndroidSyncSourceVersion returns a stable fingerprint of the rows/settings
// that can change one user's generation membership or asset bytes.
func (db *DB) AndroidSyncSourceVersion(username string, settings AndroidRetentionSettings) (string, error) {
	overallStart := time.Now()
	username = strings.TrimSpace(username)
	type stat struct {
		Name  string
		Count int64
		Max   int64
	}
	stats := []stat{}
	phaseStart := time.Now()
	for _, q := range []struct {
		name  string
		query string
	}{
		{"feed_items", `SELECT COUNT(*), COALESCE(MAX(sync_seq),0) FROM feed_items`},
		{"videos", `SELECT COUNT(*), COALESCE(MAX(sync_seq),0) FROM videos`},
		{"video_comments", `SELECT COUNT(*), COALESCE(MAX(fetched_at),0) FROM video_comments`},
		{"video_repost_sources", `SELECT COUNT(*), COALESCE(MAX(updated_at_ms),0) FROM video_repost_sources`},
		{"channels", `SELECT COUNT(*), COALESCE(MAX(sync_seq),0) FROM channels`},
		{"channel_settings", `SELECT COUNT(*), COALESCE(MAX(updated_at),0) FROM channel_settings`},
		{"media_files", `SELECT COUNT(*), COALESCE(MAX(id),0) FROM media_files`},
		{"assets", `SELECT COUNT(*), COALESCE(MAX(updated_at_ms),0) FROM assets`},
		{"channel_profiles", `SELECT COUNT(*), COALESCE(MAX(rowid),0) FROM channel_profiles`},
		{"channel_follows", `SELECT COUNT(*), COALESCE(MAX(followed_at),0) FROM channel_follows`},
		{"channel_stars", `SELECT COUNT(*), COALESCE(MAX(starred_at),0) FROM channel_stars`},
		{"bookmarks", `SELECT COUNT(*), COALESCE(MAX(bookmarked_at),0) FROM bookmarks`},
		{"feed_likes", `SELECT COUNT(*), COALESCE(MAX(liked_at),0) FROM feed_likes`},
		{"feed_seen", `SELECT COUNT(*), COALESCE(MAX(seen_at),0) FROM feed_seen`},
		{"translations", `SELECT COUNT(*), COALESCE(MAX(translated_at),0) FROM translations`},
		{"feed_rank_snapshot", `SELECT COUNT(*), COALESCE(MAX(computed_at),0) FROM feed_rank_snapshot`},
		{"muted_accounts", `SELECT COUNT(*), COALESCE(MAX(muted_at),0) FROM muted_accounts`},
		{"moment_views", `SELECT COUNT(*), COALESCE(MAX(viewed_at),0) FROM moment_views`},
		{"watch_history", `SELECT COUNT(*), COALESCE(MAX(progress_updated_at_ms),0) FROM watch_history`},
	} {
		var s stat
		s.Name = q.name
		if err := db.conn.QueryRow(q.query).Scan(&s.Count, &s.Max); err != nil {
			return "", err
		}
		stats = append(stats, s)
	}
	slog.Info(
		"android_sync_source_version_phase",
		"phase", "table_stats",
		"queries", len(stats),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)
	phaseStart = time.Now()
	fileStats, err := db.androidSyncSourceFileStats()
	if err != nil {
		return "", err
	}
	slog.Info(
		"android_sync_source_version_phase",
		"phase", "file_stats",
		"files", len(fileStats),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)
	phaseStart = time.Now()
	profileHash, err := db.androidSyncProfileHash()
	if err != nil {
		return "", err
	}
	slog.Info(
		"android_sync_source_version_phase",
		"phase", "profile_hash",
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)
	phaseStart = time.Now()
	channelFollowHash, err := db.androidSyncChannelFollowHash()
	if err != nil {
		return "", err
	}
	slog.Info(
		"android_sync_source_version_phase",
		"phase", "channel_follow_hash",
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)
	phaseStart = time.Now()
	bookmarkMetadataHash, err := db.androidSyncBookmarkMetadataHash()
	if err != nil {
		return "", err
	}
	slog.Info(
		"android_sync_source_version_phase",
		"phase", "bookmark_metadata_hash",
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)
	payload := map[string]any{
		"materializer_version":   AndroidSyncMaterializerVersion,
		"username":               username,
		"settings":               settings,
		"moments_reposts":        db.MomentsIncludeRepostsEnabled(),
		"instagram_tagged":       db.InstagramIncludeTaggedEnabled(),
		"stats":                  stats,
		"file_stats":             fileStats,
		"profile_hash":           profileHash,
		"channel_follow_hash":    channelFollowHash,
		"bookmark_metadata_hash": bookmarkMetadataHash,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	sourceVersion := hex.EncodeToString(sum[:])
	slog.Info(
		"android_sync_source_version_done",
		"source_version", shortAndroidSyncSourceVersion(sourceVersion),
		"duration_ms", time.Since(overallStart).Milliseconds(),
	)
	return sourceVersion, nil
}

func shortAndroidSyncSourceVersion(sourceVersion string) string {
	if len(sourceVersion) <= 16 {
		return sourceVersion
	}
	return sourceVersion[:16]
}

func (db *DB) androidSyncProfileHash() (string, error) {
	rows, err := db.conn.Query(`
		SELECT channel_id, COALESCE(platform, ''), COALESCE(handle, ''), COALESCE(display_name, ''),
		       COALESCE(bio, ''), COALESCE(website, ''), COALESCE(followers, 0), COALESCE(following, 0),
		       COALESCE(verified, 0), COALESCE(verified_type, ''), COALESCE(protected, 0),
		       COALESCE(avatar_url, ''), COALESCE(banner_url, ''), COALESCE(tombstone, 0)
		FROM channel_profiles
		ORDER BY channel_id
	`)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = rows.Close()
	}()
	h := sha256.New()
	for rows.Next() {
		var channelID, platform, handle, displayName, bio, website, verifiedType, avatarURL, bannerURL string
		var followers, following, verified, protected, tombstone int
		if err := rows.Scan(
			&channelID, &platform, &handle, &displayName,
			&bio, &website, &followers, &following,
			&verified, &verifiedType, &protected,
			&avatarURL, &bannerURL, &tombstone,
		); err != nil {
			return "", err
		}
		for _, value := range []string{
			channelID,
			platform,
			handle,
			displayName,
			bio,
			website,
			fmt.Sprintf("%d", followers),
			fmt.Sprintf("%d", following),
			fmt.Sprintf("%d", verified),
			verifiedType,
			fmt.Sprintf("%d", protected),
			avatarURL,
			bannerURL,
			fmt.Sprintf("%d", tombstone),
		} {
			h.Write([]byte(value))
			h.Write([]byte{0})
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (db *DB) androidSyncChannelFollowHash() (string, error) {
	rows, err := db.conn.Query(`
		SELECT COALESCE(user_id, ''), channel_id, COALESCE(followed_at, 0)
		FROM channel_follows
		ORDER BY COALESCE(user_id, ''), channel_id
	`)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = rows.Close()
	}()
	h := sha256.New()
	for rows.Next() {
		var userID, channelID string
		var followedAt int64
		if err := rows.Scan(&userID, &channelID, &followedAt); err != nil {
			return "", err
		}
		h.Write([]byte(userID))
		h.Write([]byte{0})
		h.Write([]byte(channelID))
		h.Write([]byte{0})
		h.Write([]byte(fmt.Sprintf("%d", followedAt)))
		h.Write([]byte{0})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (db *DB) androidSyncBookmarkMetadataHash() (string, error) {
	h := sha256.New()
	categoryRows, err := db.conn.Query(`
		SELECT COALESCE(user_id, ''), id, COALESCE(name, ''), COALESCE(archive_path, ''), COALESCE(created_at, 0)
		FROM bookmark_categories
		ORDER BY COALESCE(user_id, ''), id
	`)
	if err != nil {
		return "", err
	}
	for categoryRows.Next() {
		var userID, name, archivePath string
		var id, createdAt int64
		if err := categoryRows.Scan(&userID, &id, &name, &archivePath, &createdAt); err != nil {
			_ = categoryRows.Close()
			return "", err
		}
		h.Write([]byte("category"))
		h.Write([]byte{0})
		h.Write([]byte(userID))
		h.Write([]byte{0})
		h.Write([]byte(fmt.Sprintf("%d", id)))
		h.Write([]byte{0})
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(archivePath))
		h.Write([]byte{0})
		h.Write([]byte(fmt.Sprintf("%d", createdAt)))
		h.Write([]byte{0})
	}
	if err := categoryRows.Err(); err != nil {
		_ = categoryRows.Close()
		return "", err
	}
	_ = categoryRows.Close()

	bookmarkRows, err := db.conn.Query(`
		SELECT COALESCE(user_id, ''), video_id, COALESCE(category_id, 0),
		       COALESCE(custom_title, ''), COALESCE(account_handles, ''),
		       COALESCE(media_indices, ''), COALESCE(bookmarked_at, 0)
		FROM bookmarks
		ORDER BY COALESCE(user_id, ''), video_id
	`)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = bookmarkRows.Close()
	}()
	for bookmarkRows.Next() {
		var userID, videoID, customTitle, accountHandles, mediaIndices string
		var categoryID, bookmarkedAt int64
		if err := bookmarkRows.Scan(&userID, &videoID, &categoryID, &customTitle, &accountHandles, &mediaIndices, &bookmarkedAt); err != nil {
			return "", err
		}
		h.Write([]byte("bookmark"))
		h.Write([]byte{0})
		h.Write([]byte(userID))
		h.Write([]byte{0})
		h.Write([]byte(videoID))
		h.Write([]byte{0})
		h.Write([]byte(fmt.Sprintf("%d", categoryID)))
		h.Write([]byte{0})
		h.Write([]byte(customTitle))
		h.Write([]byte{0})
		h.Write([]byte(accountHandles))
		h.Write([]byte{0})
		h.Write([]byte(mediaIndices))
		h.Write([]byte{0})
		h.Write([]byte(fmt.Sprintf("%d", bookmarkedAt)))
		h.Write([]byte{0})
	}
	if err := bookmarkRows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type androidSyncSourceFileStat struct {
	Name        string `json:"name"`
	Exists      bool   `json:"exists"`
	Size        int64  `json:"size"`
	ModUnixNano int64  `json:"mod_unix_nano"`
}

func (db *DB) androidSyncSourceFileStats() ([]androidSyncSourceFileStat, error) {
	rows, err := db.conn.Query(`
		SELECT source_key, source_path
		FROM (
			SELECT 'media_files:' || owner_type || ':' || owner_id || ':' || media_index AS source_key,
			       file_path AS source_path
			FROM media_files
			WHERE COALESCE(file_path, '') != ''

			UNION ALL
			SELECT 'videos:file:' || video_id AS source_key,
			       file_path AS source_path
			FROM videos
			WHERE COALESCE(file_path, '') != ''

			UNION ALL
			SELECT 'videos:thumbnail:' || video_id AS source_key,
			       thumbnail_path AS source_path
			FROM videos
			WHERE COALESCE(thumbnail_path, '') != ''
		)
		ORDER BY source_key
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []androidSyncSourceFileStat
	for rows.Next() {
		var name, path string
		if err := rows.Scan(&name, &path); err != nil {
			return nil, err
		}
		item := androidSyncSourceFileStat{Name: name}
		if info, err := os.Stat(resolveManifestDataPath(db.dataDir, path)); err == nil {
			item.Exists = true
			item.Size = info.Size()
			item.ModUnixNano = info.ModTime().UnixNano()
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, dir := range []string{
		filepath.Join(db.dataDir, "thumbnails", "avatars"),
		filepath.Join(db.dataDir, "thumbnails", "banners"),
	} {
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(db.dataDir, path)
			if relErr != nil {
				rel = path
			}
			item := androidSyncSourceFileStat{Name: "profile_media:" + rel}
			if info, statErr := d.Info(); statErr == nil {
				item.Exists = true
				item.Size = info.Size()
				item.ModUnixNano = info.ModTime().UnixNano()
			}
			out = append(out, item)
			return nil
		}); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (db *DB) GetAndroidSyncGenerationBySource(sourceVersion string) (*model.AndroidSyncGeneration, error) {
	return db.getAndroidSyncGeneration(`
		SELECT generation_id, created_at_ms, status, source_version, retention_json,
		       item_count, asset_count, ready_asset_count, server_missing_asset_count,
		       total_bytes, content_counts_json, asset_counts_json
		FROM android_sync_generations
		WHERE source_version = ?
	`, sourceVersion)
}

func (db *DB) GetAndroidSyncGeneration(generationID string) (*model.AndroidSyncGeneration, error) {
	return db.getAndroidSyncGeneration(`
		SELECT generation_id, created_at_ms, status, source_version, retention_json,
		       item_count, asset_count, ready_asset_count, server_missing_asset_count,
		       total_bytes, content_counts_json, asset_counts_json
		FROM android_sync_generations
		WHERE generation_id = ?
	`, generationID)
}

func (db *DB) GetLatestAndroidSyncGeneration() (*model.AndroidSyncGeneration, error) {
	return db.getAndroidSyncGeneration(`
		SELECT generation_id, created_at_ms, status, source_version, retention_json,
		       item_count, asset_count, ready_asset_count, server_missing_asset_count,
		       total_bytes, content_counts_json, asset_counts_json
		FROM android_sync_generations
		WHERE status = 'ready'
		ORDER BY created_at_ms DESC, generation_id DESC
		LIMIT 1
	`)
}

func (db *DB) getAndroidSyncGeneration(query string, args ...any) (*model.AndroidSyncGeneration, error) {
	row := db.conn.QueryRow(query, args...)
	var gen model.AndroidSyncGeneration
	var retentionJSON, contentCountsJSON, assetCountsJSON string
	if err := row.Scan(
		&gen.GenerationID,
		&gen.CreatedAtMs,
		&gen.Status,
		&gen.SourceVersion,
		&retentionJSON,
		&gen.ItemCount,
		&gen.AssetCount,
		&gen.ReadyAssetCount,
		&gen.ServerMissingAssetCount,
		&gen.TotalBytes,
		&contentCountsJSON,
		&assetCountsJSON,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(retentionJSON), &gen.Retention)
	_ = json.Unmarshal([]byte(contentCountsJSON), &gen.ContentCounts)
	_ = json.Unmarshal([]byte(assetCountsJSON), &gen.AssetCounts)
	if gen.Retention == nil {
		gen.Retention = map[string]int{}
	}
	if gen.ContentCounts == nil {
		gen.ContentCounts = map[string]int{}
	}
	if gen.AssetCounts == nil {
		gen.AssetCounts = map[string]int{}
	}
	return &gen, nil
}

func (db *DB) StoreAndroidSyncGeneration(gen model.AndroidSyncGeneration, items []model.AndroidSyncItem, assets []model.AndroidSyncAsset) error {
	retentionJSON, _ := json.Marshal(gen.Retention)
	contentCountsJSON, _ := json.Marshal(gen.ContentCounts)
	assetCountsJSON, _ := json.Marshal(gen.AssetCounts)
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO android_sync_generations (
				generation_id, created_at_ms, status, source_version, retention_json,
				item_count, asset_count, ready_asset_count, server_missing_asset_count,
				total_bytes, content_counts_json, asset_counts_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(generation_id) DO NOTHING
		`,
			gen.GenerationID,
			gen.CreatedAtMs,
			gen.Status,
			gen.SourceVersion,
			string(retentionJSON),
			gen.ItemCount,
			gen.AssetCount,
			gen.ReadyAssetCount,
			gen.ServerMissingAssetCount,
			gen.TotalBytes,
			string(contentCountsJSON),
			string(assetCountsJSON),
		); err != nil {
			return err
		}

		itemStmt, err := tx.Prepare(`
			INSERT OR IGNORE INTO android_sync_items (
				generation_id, seq, item_kind, item_id, payload_json
			) VALUES (?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = itemStmt.Close()
		}()
		for _, item := range items {
			if _, err := itemStmt.Exec(gen.GenerationID, item.Seq, item.ItemKind, item.ItemID, string(item.PayloadJSON)); err != nil {
				return err
			}
		}

		assetStmt, err := tx.Prepare(`
			INSERT OR IGNORE INTO android_sync_assets (
				generation_id, seq, asset_id, asset_kind, media_index, owner_id, owner_kind,
				bucket, server_url, content_type, size_bytes, sha256, state,
				required_reason, is_auto, audio_language, effective_recency_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = assetStmt.Close()
		}()
		for _, asset := range assets {
			if _, err := assetStmt.Exec(
				gen.GenerationID,
				asset.Seq,
				asset.AssetID,
				asset.AssetKind,
				asset.MediaIndex,
				asset.OwnerID,
				asset.OwnerKind,
				asset.Bucket,
				asset.ServerURL,
				asset.ContentType,
				asset.SizeBytes,
				asset.SHA256,
				asset.State,
				asset.RequiredReason,
				asset.IsAuto,
				asset.AudioLanguage,
				asset.EffectiveRecencyMs,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) PruneAndroidSyncState(nowMs int64, policy AndroidSyncPrunePolicy) (AndroidSyncPruneResult, error) {
	policy = normalizeAndroidSyncPrunePolicy(policy)
	cutoffMs := nowMs - int64(policy.KeepMinAge/time.Millisecond)
	var result AndroidSyncPruneResult
	err := db.WithWrite(func(tx *sql.Tx) error {
		itemsDeleted, err := execRowsAffected(tx, `
			DELETE FROM android_sync_items
			WHERE rowid IN (
				SELECT i.rowid
				FROM android_sync_items i
				INNER JOIN android_sync_generations g ON g.generation_id = i.generation_id
				WHERE g.status = 'ready'
				  AND g.created_at_ms < ?
				  AND g.generation_id != ?
				  AND g.generation_id NOT IN (
					SELECT generation_id
					FROM android_sync_generations
					WHERE status = 'ready'
					ORDER BY created_at_ms DESC, generation_id DESC
					LIMIT ?
				  )
				ORDER BY g.created_at_ms ASC, g.generation_id ASC, i.seq ASC
				LIMIT ?
			)
		`, cutoffMs, policy.ProtectGenerationID, policy.KeepReadyGenerations, policy.MaxItemDeletes)
		if err != nil {
			return err
		}
		result.ItemsDeleted = itemsDeleted

		assetsDeleted, err := execRowsAffected(tx, `
			DELETE FROM android_sync_assets
			WHERE rowid IN (
				SELECT a.rowid
				FROM android_sync_assets a
				INNER JOIN android_sync_generations g ON g.generation_id = a.generation_id
				WHERE g.status = 'ready'
				  AND g.created_at_ms < ?
				  AND g.generation_id != ?
				  AND g.generation_id NOT IN (
					SELECT generation_id
					FROM android_sync_generations
					WHERE status = 'ready'
					ORDER BY created_at_ms DESC, generation_id DESC
					LIMIT ?
				  )
				ORDER BY g.created_at_ms ASC, g.generation_id ASC, a.seq ASC
				LIMIT ?
			)
		`, cutoffMs, policy.ProtectGenerationID, policy.KeepReadyGenerations, policy.MaxAssetDeletes)
		if err != nil {
			return err
		}
		result.AssetsDeleted = assetsDeleted

		healthForGenerationsDeleted, err := execRowsAffected(tx, `
			DELETE FROM android_sync_health_reports
			WHERE id IN (
				SELECT h.id
				FROM android_sync_health_reports h
				INNER JOIN android_sync_generations g ON g.generation_id = h.generation_id
				WHERE g.status = 'ready'
				  AND g.created_at_ms < ?
				  AND g.generation_id != ?
				  AND g.generation_id NOT IN (
					SELECT generation_id
					FROM android_sync_generations
					WHERE status = 'ready'
					ORDER BY created_at_ms DESC, generation_id DESC
					LIMIT ?
				  )
				ORDER BY g.created_at_ms ASC, g.generation_id ASC, h.reported_at_ms ASC, h.id ASC
				LIMIT ?
			)
		`, cutoffMs, policy.ProtectGenerationID, policy.KeepReadyGenerations, policy.MaxHealthDeletes)
		if err != nil {
			return err
		}
		result.HealthReportsDeleted = healthForGenerationsDeleted

		generationsDeleted, err := execRowsAffected(tx, `
			DELETE FROM android_sync_generations
			WHERE generation_id IN (
				SELECT g.generation_id
				FROM android_sync_generations g
				WHERE g.status = 'ready'
				  AND g.created_at_ms < ?
				  AND g.generation_id != ?
				  AND g.generation_id NOT IN (
					SELECT generation_id
					FROM android_sync_generations
					WHERE status = 'ready'
					ORDER BY created_at_ms DESC, generation_id DESC
					LIMIT ?
				  )
				  AND NOT EXISTS (SELECT 1 FROM android_sync_items i WHERE i.generation_id = g.generation_id)
				  AND NOT EXISTS (SELECT 1 FROM android_sync_assets a WHERE a.generation_id = g.generation_id)
				  AND NOT EXISTS (SELECT 1 FROM android_sync_health_reports h WHERE h.generation_id = g.generation_id)
				ORDER BY g.created_at_ms ASC, g.generation_id ASC
				LIMIT ?
			)
		`, cutoffMs, policy.ProtectGenerationID, policy.KeepReadyGenerations, policy.MaxGenerationDeletes)
		if err != nil {
			return err
		}
		result.GenerationsDeleted = generationsDeleted

		healthDeleteLimit := policy.MaxHealthDeletes - result.HealthReportsDeleted
		if healthDeleteLimit > 0 {
			healthOverflowDeleted, err := execRowsAffected(tx, `
				DELETE FROM android_sync_health_reports
				WHERE id IN (
					SELECT id
					FROM android_sync_health_reports
					WHERE id NOT IN (
						SELECT id
						FROM android_sync_health_reports
						ORDER BY reported_at_ms DESC, id DESC
						LIMIT ?
					)
					ORDER BY reported_at_ms ASC, id ASC
					LIMIT ?
				)
			`, policy.KeepHealthReports, healthDeleteLimit)
			if err != nil {
				return err
			}
			result.HealthReportsDeleted += healthOverflowDeleted
		}
		return nil
	})
	return result, err
}

func (db *DB) DrainAndroidSyncState(nowMs int64, policy AndroidSyncPrunePolicy, maxPasses int) (AndroidSyncPruneDrainResult, error) {
	policy = normalizeAndroidSyncPrunePolicy(policy)
	if maxPasses <= 0 {
		maxPasses = DefaultAndroidSyncPruneDrainPasses
	}

	var out AndroidSyncPruneDrainResult
	for pass := 0; pass < maxPasses; pass++ {
		result, err := db.PruneAndroidSyncState(nowMs, policy)
		if err != nil {
			return out, err
		}
		if result.isZero() {
			break
		}
		out.Passes++
		out.GenerationsDeleted += result.GenerationsDeleted
		out.ItemsDeleted += result.ItemsDeleted
		out.AssetsDeleted += result.AssetsDeleted
		out.HealthReportsDeleted += result.HealthReportsDeleted
	}

	debt, err := db.AndroidSyncPruneDebt(nowMs, policy)
	if err != nil {
		return out, err
	}
	out.Debt = debt
	return out, nil
}

func (db *DB) RunAndroidSyncMaintenance(opts AndroidSyncMaintenanceOptions) (AndroidSyncMaintenanceResult, error) {
	started := time.Now()
	nowMs := opts.NowMs
	if nowMs <= 0 {
		nowMs = started.UnixMilli()
	}
	policy := normalizeAndroidSyncPrunePolicy(opts.Policy)

	before, err := db.AndroidSyncPruneDebt(nowMs, policy)
	if err != nil {
		return AndroidSyncMaintenanceResult{}, err
	}
	drain, err := db.DrainAndroidSyncState(nowMs, policy, opts.MaxPasses)
	if err != nil {
		return AndroidSyncMaintenanceResult{}, err
	}
	ended := time.Now()
	return AndroidSyncMaintenanceResult{
		StartedAtMs: started.UnixMilli(),
		EndedAtMs:   ended.UnixMilli(),
		DurationMs:  ended.Sub(started).Milliseconds(),
		Before:      before,
		Drain:       drain,
		After:       drain.Debt,
	}, nil
}

func (db *DB) AndroidSyncPruneDebt(nowMs int64, policy AndroidSyncPrunePolicy) (AndroidSyncPruneDebt, error) {
	policy = normalizeAndroidSyncPrunePolicy(policy)
	cutoffMs := nowMs - int64(policy.KeepMinAge/time.Millisecond)
	var debt AndroidSyncPruneDebt
	err := db.WithRead(func(conn *sql.DB) error {
		return conn.QueryRow(`
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
			&debt.EligibleGenerations,
			&debt.EligibleItems,
			&debt.EligibleAssets,
			&debt.EligibleHealthReports,
		)
	})
	return debt, err
}

func (r AndroidSyncPruneResult) isZero() bool {
	return r.GenerationsDeleted == 0 &&
		r.ItemsDeleted == 0 &&
		r.AssetsDeleted == 0 &&
		r.HealthReportsDeleted == 0
}

func normalizeAndroidSyncPrunePolicy(policy AndroidSyncPrunePolicy) AndroidSyncPrunePolicy {
	defaults := DefaultAndroidSyncPrunePolicy()
	if policy.KeepReadyGenerations <= 0 {
		policy.KeepReadyGenerations = defaults.KeepReadyGenerations
	}
	if policy.KeepMinAge <= 0 {
		policy.KeepMinAge = defaults.KeepMinAge
	}
	if policy.KeepHealthReports <= 0 {
		policy.KeepHealthReports = defaults.KeepHealthReports
	}
	policy.ProtectGenerationID = strings.TrimSpace(policy.ProtectGenerationID)
	if policy.MaxGenerationDeletes <= 0 {
		policy.MaxGenerationDeletes = defaults.MaxGenerationDeletes
	}
	if policy.MaxItemDeletes <= 0 {
		policy.MaxItemDeletes = defaults.MaxItemDeletes
	}
	if policy.MaxAssetDeletes <= 0 {
		policy.MaxAssetDeletes = defaults.MaxAssetDeletes
	}
	if policy.MaxHealthDeletes <= 0 {
		policy.MaxHealthDeletes = defaults.MaxHealthDeletes
	}
	return policy
}

func execRowsAffected(tx *sql.Tx, query string, args ...any) (int, error) {
	res, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func (db *DB) ListAndroidSyncItems(generationID string, afterSeq int64, limit int) ([]model.AndroidSyncItem, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := db.conn.Query(`
		SELECT generation_id, seq, item_kind, item_id, payload_json
		FROM android_sync_items
		WHERE generation_id = ? AND seq > ?
		ORDER BY seq ASC
		LIMIT ?
	`, generationID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []model.AndroidSyncItem
	for rows.Next() {
		var item model.AndroidSyncItem
		var payloadJSON string
		if err := rows.Scan(&item.GenerationID, &item.Seq, &item.ItemKind, &item.ItemID, &payloadJSON); err != nil {
			return nil, err
		}
		item.PayloadJSON = json.RawMessage(payloadJSON)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (db *DB) ListAndroidSyncAssets(generationID string, afterSeq int64, limit int) ([]model.AndroidSyncAsset, error) {
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	rows, err := db.conn.Query(`
		SELECT generation_id, seq, asset_id, asset_kind, media_index, owner_id, owner_kind,
		       bucket, server_url, content_type, size_bytes, sha256, state,
		       required_reason, is_auto, audio_language, effective_recency_ms
		FROM android_sync_assets
		WHERE generation_id = ? AND seq > ?
		ORDER BY seq ASC
		LIMIT ?
	`, generationID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []model.AndroidSyncAsset
	for rows.Next() {
		asset, err := scanAndroidSyncAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	return out, rows.Err()
}

func (db *DB) GetAndroidSyncAsset(assetID string) (*model.AndroidSyncAsset, error) {
	rows, err := db.conn.Query(`
		SELECT a.generation_id, a.seq, a.asset_id, a.asset_kind, a.media_index, a.owner_id, a.owner_kind,
		       a.bucket, a.server_url, a.content_type, a.size_bytes, a.sha256, a.state,
		       a.required_reason, a.is_auto, a.audio_language, a.effective_recency_ms
		FROM android_sync_assets a
		JOIN android_sync_generations g ON g.generation_id = a.generation_id
		WHERE a.asset_id = ? AND a.state = 'ready' AND g.status = 'ready'
		ORDER BY g.created_at_ms DESC, a.seq ASC
		LIMIT 1
	`, assetID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		return nil, nil
	}
	asset, err := scanAndroidSyncAsset(rows)
	if err != nil {
		return nil, err
	}
	return &asset, rows.Err()
}

func (db *DB) GetAndroidSyncGenerationAsset(generationID, assetID string) (*model.AndroidSyncAsset, error) {
	rows, err := db.conn.Query(`
		SELECT generation_id, seq, asset_id, asset_kind, media_index, owner_id, owner_kind,
		       bucket, server_url, content_type, size_bytes, sha256, state,
		       required_reason, is_auto, audio_language, effective_recency_ms
		FROM android_sync_assets
		WHERE generation_id = ? AND asset_id = ?
		LIMIT 1
	`, generationID, assetID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		return nil, nil
	}
	asset, err := scanAndroidSyncAsset(rows)
	if err != nil {
		return nil, err
	}
	return &asset, rows.Err()
}

type androidSyncAssetScanner interface {
	Scan(dest ...any) error
}

func scanAndroidSyncAsset(rows androidSyncAssetScanner) (model.AndroidSyncAsset, error) {
	var asset model.AndroidSyncAsset
	err := rows.Scan(
		&asset.GenerationID,
		&asset.Seq,
		&asset.AssetID,
		&asset.AssetKind,
		&asset.MediaIndex,
		&asset.OwnerID,
		&asset.OwnerKind,
		&asset.Bucket,
		&asset.ServerURL,
		&asset.ContentType,
		&asset.SizeBytes,
		&asset.SHA256,
		&asset.State,
		&asset.RequiredReason,
		&asset.IsAuto,
		&asset.AudioLanguage,
		&asset.EffectiveRecencyMs,
	)
	return asset, err
}

func (db *DB) RecordAndroidSyncHealth(generationID string, reportedAtMs int64, payload []byte, verifiedAssets, pendingAssets, failedAssets, missingAssets, totalAssets int, verifiedBytes int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO android_sync_health_reports (
				generation_id, reported_at_ms, payload_json, verified_assets,
				pending_assets, failed_assets, missing_assets, total_assets, verified_bytes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, generationID, reportedAtMs, string(payload), verifiedAssets, pendingAssets, failedAssets, missingAssets, totalAssets, verifiedBytes)
		return err
	})
}
