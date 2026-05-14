package db

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/subtitlemeta"
)

// Legacy media manifest inventory helpers. The public HTTP route is retired,
// but the asset identity format remains shared with Android sync materialization.
//
// Cursor-paged, per-scope asset manifest. Each entry tells Android
// where to download one cached binary (post thumbnail, post media,
// avatar, video stream, …). Authoritative — nothing else is cached;
// no client-side URL synthesis.
//
// asset_id format (locked in track-a intent-pass):
//   {platform}_{owner_kind}_{owner_id}_{asset_kind}[_{index}]
// e.g.
//   twitter_tweet_12345_thumb
//   twitter_tweet_12345_media_0
//   twitter_channel_alice_avatar
//   youtube_video_abcd_stream
//
// Stable across requests; changes only when the source URL changes
// (avatar swap → new hash → new asset_id → client re-downloads).
//
// Cursor is a synthetic monotonic sequence spanning media_files rows,
// conventional avatar-cache rows, and video-derived assets.

const manifestDefaultCap = 200

type manifestRawRow struct {
	Branch     string
	RawSeq     int64
	Seq        int64
	OwnerID    string
	OwnerType  string
	MediaType  string
	MediaIndex int
	FileSize   int64
	FilePath   string
	SourceURL  string
}

type manifestCursor struct {
	FeedMedia       int64
	QuoteMedia      int64
	Avatar          int64
	Banner          int64
	VideoStream     int64
	VideoThumb      int64
	Subtitle        int64
	FeedMediaThumb  int64
	QuoteMediaThumb int64
}

func parseManifestCursor(raw string) manifestCursor {
	var cursor manifestCursor
	if !strings.HasPrefix(raw, "v2:") {
		// Legacy numeric cursors used one synthetic sequence across disjoint
		// ranges. If the cursor is still in the low media_files range, preserve
		// that progress; replaying subscriptions from zero is expensive and does
		// not recover any high-range video/avatar assets. Higher legacy cursors
		// still replay into v2 so branches below the old synthetic range get a
		// chance to repair skipped assets.
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 && n < manifestSeqAvatar {
			cursor.FeedMedia = n
			cursor.QuoteMedia = n
		}
		return cursor
	}
	for _, part := range strings.Split(strings.TrimPrefix(raw, "v2:"), ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch key {
		case "feed":
			cursor.FeedMedia = n
		case "quote":
			cursor.QuoteMedia = n
		case "avatar":
			cursor.Avatar = n
		case "banner":
			cursor.Banner = n
		case "stream":
			cursor.VideoStream = n
		case "vthumb":
			cursor.VideoThumb = n
		case "sub":
			cursor.Subtitle = n
		case "fthumb":
			cursor.FeedMediaThumb = n
		case "qthumb":
			cursor.QuoteMediaThumb = n
		}
	}
	return cursor
}

func (c manifestCursor) String() string {
	return fmt.Sprintf(
		"v2:feed=%d,quote=%d,avatar=%d,banner=%d,stream=%d,vthumb=%d,sub=%d,fthumb=%d,qthumb=%d",
		c.FeedMedia,
		c.QuoteMedia,
		c.Avatar,
		c.Banner,
		c.VideoStream,
		c.VideoThumb,
		c.Subtitle,
		c.FeedMediaThumb,
		c.QuoteMediaThumb,
	)
}

func (c *manifestCursor) advance(branch string, rawSeq int64) {
	if rawSeq <= 0 {
		return
	}
	switch branch {
	case "feed":
		c.FeedMedia = max(c.FeedMedia, rawSeq)
	case "quote":
		c.QuoteMedia = max(c.QuoteMedia, rawSeq)
	case "avatar":
		c.Avatar = max(c.Avatar, rawSeq)
	case "banner":
		c.Banner = max(c.Banner, rawSeq)
	case "stream":
		c.VideoStream = max(c.VideoStream, rawSeq)
	case "vthumb":
		c.VideoThumb = max(c.VideoThumb, rawSeq)
	case "sub":
		c.Subtitle = max(c.Subtitle, rawSeq)
	case "fthumb":
		c.FeedMediaThumb = max(c.FeedMediaThumb, rawSeq)
	case "qthumb":
		c.QuoteMediaThumb = max(c.QuoteMediaThumb, rawSeq)
	}
}

func manifestQueryArgs(base []any, extra ...any) []any {
	args := make([]any, 0, len(base)+len(extra))
	args = append(args, base...)
	args = append(args, extra...)
	return args
}

// BuildManifestAssetID returns the canonical asset_id for an entry.
// Exported so tests and future per-kind builders can share the format.
func BuildManifestAssetID(platform, ownerKind, ownerID, assetKind string, index int) string {
	id := platform + "_" + ownerKind + "_" + ownerID + "_" + assetKind
	if index > 0 {
		id = fmt.Sprintf("%s_%d", id, index)
	}
	return id
}

// manifestScopeSubqueryPaged returns the tweet-ID subquery for a scope —
// filters Twitter-asset UNION branches (feed_media, quote_media, Twitter
// avatars from feed-item authors).
func manifestScopeSubqueryPaged(scope, username string) (string, []any, error) {
	switch scope {
	case "subscriptions":
		return `SELECT fi.tweet_id FROM feed_items fi
		        WHERE LOWER(fi.author_handle) IN (
		          SELECT LOWER(SUBSTR(cf.channel_id, 9))
		          FROM channel_follows cf
		          WHERE cf.user_id = ''
		            AND SUBSTR(cf.channel_id, 1, 8) = 'twitter_'
		        )`,
			nil, nil
	case "liked":
		return `SELECT l.tweet_id FROM feed_likes l WHERE l.username = ?`,
			[]any{username}, nil
	case "bookmarked":
		return `SELECT b.video_id FROM bookmarks b`, nil, nil
	default:
		return "", nil, fmt.Errorf("unknown manifest scope: %s", scope)
	}
}

// manifestChannelsSubquery returns the channel-ID subquery that drives the
// non-Twitter UNION branches: all-platform avatars, YouTube/Shorts video
// streams, and video thumbnails. Scope-specific semantics:
//   - subscriptions → every followed channel (any platform)
//   - liked/bookmarked → channels whose videos appear in the scope table
func manifestChannelsSubquery(scope, _ string) (string, []any, error) {
	switch scope {
	case "subscriptions":
		return `SELECT cf.channel_id FROM channel_follows cf WHERE cf.user_id = ''`,
			nil, nil
	case "liked":
		// feed_likes is Twitter-only; no channel set to expand video branches.
		return `SELECT NULL WHERE 0`, nil, nil
	case "bookmarked":
		return `SELECT v.channel_id FROM videos v
		        JOIN bookmarks b ON b.video_id = v.video_id`, nil, nil
	default:
		return "", nil, fmt.Errorf("unknown manifest scope: %s", scope)
	}
}

// Synthetic ordering offsets keep the UNION results in a stable display-friendly
// order while the opaque cursor stores each branch's real row sequence separately.
// media_files.id climbs from 1; video_stream and video_thumbnail get large offsets
// so they sort after every plausible media_files row. The offsets are arbitrary as
// long as they exceed any realistic media_files.id AND differ between branches.
//
// FeedMediaThumb is the per-tweet post_thumbnail companion for video/gif
// feed_media — clients (Coil3) cannot decode MP4, so without an explicit
// post_thumbnail asset they fall back to post_media (the MP4 itself) and render
// a broken-image icon. The fix emits a separate post_thumbnail asset that
// resolves through the server's `/api/media/thumbnail/<tweetID>` cascade
// (which extracts the first frame on demand and caches it).
const (
	manifestSeqAvatar          = int64(500_000_000_000)   // 5e11
	manifestSeqBanner          = int64(600_000_000_000)   // 6e11
	manifestSeqVideoStream     = int64(1_000_000_000_000) // 1e12
	manifestSeqVideoThumb      = int64(2_000_000_000_000) // 2e12
	manifestSeqSubtitle        = int64(3_000_000_000_000) // 3e12
	manifestSeqFeedMediaThumb  = int64(4_000_000_000_000) // 4e12
	manifestSeqQuoteMediaThumb = int64(4_500_000_000_000) // 4.5e12
)

// GetMediaManifestV2 builds one legacy asset-manifest batch at a time, ordered by
// synthetic branch priority. Callers re-call with the opaque next_marker to
// drain; the marker records per-branch row positions so a new low-range asset
// cannot be skipped after an older pass reached a high-range branch.
func (db *DB) GetMediaManifestV2(scope, username string, since string, limit int) ([]model.ManifestEntry, string, bool, error) {
	if limit <= 0 || limit > 200 {
		limit = manifestDefaultCap
	}
	cursor := parseManifestCursor(since)
	nextCursor := cursor
	scopeSQL, scopeArgs, err := manifestScopeSubqueryPaged(scope, username)
	if err != nil {
		return nil, "", true, err
	}
	channelsSQL, channelsArgs, err := manifestChannelsSubquery(scope, username)
	if err != nil {
		return nil, "", true, err
	}

	var manifestRows []manifestRawRow
	appendRows := func(query string, args ...any) error {
		if len(manifestRows) >= limit {
			return nil
		}
		rows, err := db.queryManifestRawRows(query, args...)
		if err != nil {
			return err
		}
		remaining := limit - len(manifestRows)
		if len(rows) > remaining {
			rows = rows[:remaining]
		}
		manifestRows = append(manifestRows, rows...)
		return nil
	}

	// Prioritize actual video playback assets before the Twitter/feed media
	// backlog. Android downloads by recency once rows exist locally, but it
	// cannot prioritize video rows that the manifest has not surfaced yet. Keep
	// stream/thumb/subtitle cursors independent while interleaving rows by video
	// id so one asset kind cannot starve the other.
	videoPlaybackQuery := fmt.Sprintf(`
		WITH scope_channels(channel_id) AS (%s)
		SELECT branch, raw_seq, seq, owner_id, owner_type, media_type, media_index, file_size, file_path, source_url
		FROM (
			SELECT 'vthumb' AS branch, v.id AS raw_seq, (v.id + %d) AS seq, v.video_id AS owner_id, 'video_thumbnail' AS owner_type,
			       'thumbnail' AS media_type, 0 AS media_index,
			       0 AS file_size,
			       COALESCE(NULLIF(v.thumbnail_path,''), v.file_path, '') AS file_path,
			       COALESCE(v.channel_id,'') AS source_url,
			       0 AS branch_order
			FROM videos v
			WHERE v.id > ?
			  AND v.channel_id IN (SELECT channel_id FROM scope_channels)
			  AND (COALESCE(v.thumbnail_path,'') != '' OR COALESCE(v.file_path,'') != '')

			UNION ALL

			SELECT 'stream' AS branch, v.id AS raw_seq, (v.id + %d) AS seq, v.video_id AS owner_id, 'video_stream' AS owner_type,
			       COALESCE(v.media_kind,'') AS media_type, 0 AS media_index,
			       COALESCE(v.file_size,0) AS file_size, COALESCE(v.file_path,'') AS file_path, COALESCE(v.channel_id,'') AS source_url,
			       1 AS branch_order
			FROM videos v
			WHERE v.id > ?
			  AND v.channel_id IN (SELECT channel_id FROM scope_channels)
			  AND COALESCE(v.file_path,'') != ''

			UNION ALL

			SELECT 'sub' AS branch, v.id AS raw_seq, (v.id + %d) AS seq, v.video_id AS owner_id, 'subtitle' AS owner_type,
			       'subtitle' AS media_type, 0 AS media_index,
			       0 AS file_size, COALESCE(v.file_path,'') AS file_path, COALESCE(v.channel_id,'') AS source_url,
			       2 AS branch_order
			FROM videos v
			WHERE v.id > ?
			  AND v.channel_id IN (SELECT channel_id FROM scope_channels)
			  AND v.channel_id LIKE 'youtube_%%'
			  AND COALESCE(v.file_path,'') != ''
		)
		ORDER BY raw_seq ASC, branch_order ASC
		LIMIT ?
	`, channelsSQL, manifestSeqVideoThumb, manifestSeqVideoStream, manifestSeqSubtitle)
	if err := appendRows(
		videoPlaybackQuery,
		manifestQueryArgs(channelsArgs, cursor.VideoThumb, cursor.VideoStream, cursor.Subtitle, limit)...,
	); err != nil {
		return nil, "", true, fmt.Errorf("GetMediaManifestV2: %w", err)
	}

	var avatarQuery string
	var avatarArgs []any
	if scope == "subscriptions" {
		// Subscriptions is the broad feed scope. Profile seeding keeps
		// channel_profiles populated for followed channels plus visible Twitter
		// authors/quotes/retweeters, including feed-provided avatar URLs. Paging
		// directly over that server-owned identity table avoids rebuilding the
		// large feed/retweet/quote CTE for every 200-row avatar page.
		avatarQuery = fmt.Sprintf(`
			SELECT 'avatar' AS branch, cp.rowid AS raw_seq, (cp.rowid + %d) AS seq, cp.channel_id AS owner_id, 'avatar' AS owner_type,
			       'avatar' AS media_type, 0 AS media_index, 0 AS file_size,
			       '' AS file_path, COALESCE(cp.avatar_url,'') AS source_url
			FROM channel_profiles cp
			WHERE cp.rowid > ?
			  AND cp.tombstone = 0
			ORDER BY cp.rowid ASC
			LIMIT ?
		`, manifestSeqAvatar)
		avatarArgs = manifestQueryArgs(nil, cursor.Avatar, limit-len(manifestRows))
	} else {
		avatarQuery = fmt.Sprintf(`
			WITH
			scope_tweets(tweet_id) AS (%s),
			scope_channels(channel_id) AS (%s),
			scope_avatar_channels(channel_id) AS (
				SELECT channel_id FROM scope_channels
				UNION
				SELECT DISTINCT 'twitter_' || LOWER(fi.author_handle)
				FROM feed_items fi
				JOIN scope_tweets st ON st.tweet_id = fi.tweet_id
				WHERE fi.author_handle != ''
				UNION
				SELECT DISTINCT 'twitter_' || LOWER(fi.quote_author_handle)
				FROM feed_items fi
				JOIN scope_tweets st ON st.tweet_id = fi.tweet_id
				WHERE fi.quote_author_handle != ''
				UNION
				SELECT DISTINCT cp2.channel_id
				FROM feed_items fi
				JOIN scope_tweets st ON st.tweet_id = fi.tweet_id
				JOIN channel_profiles cp2 ON LOWER(cp2.avatar_url) = LOWER(fi.quote_author_avatar_url)
				WHERE fi.quote_author_avatar_url != ''
				  AND cp2.tombstone = 0
				UNION
				SELECT DISTINCT 'twitter_' || LOWER(rs.retweeter_handle)
				FROM retweet_sources rs
				JOIN feed_items fi ON fi.content_hash = rs.content_hash
				JOIN scope_tweets st ON st.tweet_id = fi.tweet_id
				WHERE rs.retweeter_handle != ''
			)

			-- Avatars: every followed channel (any platform) AND every tweet author,
			-- quote author, or retweeter whose tweet is in-scope. The file itself lives in the
			-- conventional thumbnails cache, not media_files.
			SELECT 'avatar' AS branch, cp.rowid AS raw_seq, (cp.rowid + %d) AS seq, cp.channel_id AS owner_id, 'avatar' AS owner_type,
			       'avatar' AS media_type, 0 AS media_index, 0 AS file_size,
			       '' AS file_path, COALESCE(cp.avatar_url,'') AS source_url
			FROM channel_profiles cp
			WHERE cp.rowid > ?
			  AND cp.tombstone = 0
			  AND cp.channel_id IN (SELECT channel_id FROM scope_avatar_channels)
			ORDER BY cp.rowid ASC
			LIMIT ?
		`, scopeSQL, channelsSQL, manifestSeqAvatar)
		avatarArgs = manifestQueryArgs(manifestQueryArgs(scopeArgs, channelsArgs...), cursor.Avatar, limit-len(manifestRows))
	}
	if err := appendRows(
		avatarQuery,
		avatarArgs...,
	); err != nil {
		return nil, "", true, fmt.Errorf("GetMediaManifestV2: %w", err)
	}

	bannerQuery := fmt.Sprintf(`
		WITH scope_channels(channel_id) AS (%s)
		SELECT 'banner' AS branch, cp.rowid AS raw_seq, (cp.rowid + %d) AS seq, cp.channel_id AS owner_id, 'banner' AS owner_type,
		       'banner' AS media_type, 0 AS media_index, 0 AS file_size,
		       '' AS file_path, COALESCE(cp.banner_url,'') AS source_url
		FROM channel_profiles cp
		WHERE cp.rowid > ?
		  AND cp.tombstone = 0
		  AND COALESCE(cp.banner_url, '') != ''
		  AND cp.channel_id IN (SELECT channel_id FROM scope_channels)
		ORDER BY cp.rowid ASC
		LIMIT ?
	`, channelsSQL, manifestSeqBanner)
	if err := appendRows(
		bannerQuery,
		manifestQueryArgs(channelsArgs, cursor.Banner, limit-len(manifestRows))...,
	); err != nil {
		return nil, "", true, fmt.Errorf("GetMediaManifestV2: %w", err)
	}

	// Feed/quote media can be a very large backlog. It sits behind playback
	// assets and avatars so UI-critical rows surface before long post-media
	// replays. The opaque cursor remains per branch.
	lowRangeQuery := fmt.Sprintf(`
		WITH
		scope_tweets(tweet_id) AS (%s),
		scope_quote_media(quote_tweet_id) AS (
			SELECT fi.quote_tweet_id
			FROM feed_items fi
			JOIN feed_media_jobs fmj ON fmj.tweet_id = fi.tweet_id
			JOIN scope_tweets st ON st.tweet_id = fi.tweet_id
			WHERE fmj.status = 'completed'
			  AND fi.quote_tweet_id IS NOT NULL
			  AND fi.quote_tweet_id != ''
		)
		SELECT * FROM (
			SELECT 'feed' AS branch, mf.id AS raw_seq, mf.id AS seq, mf.owner_id, mf.owner_type, COALESCE(mf.media_type,'') AS media_type,
			       mf.media_index, COALESCE(mf.file_size,0) AS file_size,
			       COALESCE(mf.file_path,'') AS file_path,
			       COALESCE(mf.source_url,'') AS source_url
			FROM media_files mf
			WHERE mf.id > ?
			  AND mf.owner_type = 'feed_media'
			  AND mf.owner_id IN (SELECT tweet_id FROM scope_tweets)
			  AND EXISTS (SELECT 1 FROM feed_media_jobs fmj WHERE fmj.tweet_id = mf.owner_id AND fmj.status = 'completed')
			ORDER BY mf.id ASC
			LIMIT ?
		)

		UNION ALL

		SELECT * FROM (
			SELECT 'quote' AS branch, mf.id AS raw_seq, mf.id AS seq, mf.owner_id, mf.owner_type, COALESCE(mf.media_type,'') AS media_type,
			       mf.media_index, COALESCE(mf.file_size,0) AS file_size,
			       COALESCE(mf.file_path,'') AS file_path,
			       COALESCE(mf.source_url,'') AS source_url
			FROM media_files mf
			WHERE mf.id > ?
			  AND mf.owner_type = 'quote_media'
			  AND mf.owner_id IN (SELECT quote_tweet_id FROM scope_quote_media)
			ORDER BY mf.id ASC
			LIMIT ?
		)

		ORDER BY seq ASC
		LIMIT ?
	`, scopeSQL)
	if err := appendRows(
		lowRangeQuery,
		manifestQueryArgs(scopeArgs, cursor.FeedMedia, limit, cursor.QuoteMedia, limit, limit)...,
	); err != nil {
		return nil, "", true, fmt.Errorf("GetMediaManifestV2: %w", err)
	}

	feedThumbQuery := fmt.Sprintf(`
		WITH scope_tweets(tweet_id) AS (%s)
		-- Per-tweet post_thumbnail companion for video/gif feed_media. Clients
		-- (Coil3) can't decode MP4, so without this they render the post_media
		-- MP4 as a broken image. ServerURL routes through the server's
		-- /api/media/thumbnail/<tweetID> cascade — first frame extracted from
		-- the cached video, then cached as JPEG under thumbnails/generated/.
		-- One row per tweet (media_index = 0) — the cover frame is enough.
		SELECT 'fthumb' AS branch, mf.id AS raw_seq, (mf.id + %d) AS seq, mf.owner_id, 'feed_media_thumb' AS owner_type,
		       'image/jpeg' AS media_type, 0 AS media_index, 0 AS file_size,
		       '' AS file_path, '' AS source_url
		FROM media_files mf
		WHERE mf.id > ?
		  AND mf.owner_type = 'feed_media'
		  AND (mf.media_type = 'video' OR mf.media_type = 'gif')
		  AND mf.media_index = 0
		  AND mf.owner_id IN (SELECT tweet_id FROM scope_tweets)
		  AND EXISTS (SELECT 1 FROM feed_media_jobs fmj WHERE fmj.tweet_id = mf.owner_id AND fmj.status = 'completed')
		ORDER BY mf.id ASC
		LIMIT ?
	`, scopeSQL, manifestSeqFeedMediaThumb)
	if err := appendRows(
		feedThumbQuery,
		manifestQueryArgs(scopeArgs, cursor.FeedMediaThumb, limit-len(manifestRows))...,
	); err != nil {
		return nil, "", true, fmt.Errorf("GetMediaManifestV2: %w", err)
	}

	quoteThumbQuery := fmt.Sprintf(`
		WITH
		scope_tweets(tweet_id) AS (%s),
		scope_quote_media(quote_tweet_id) AS (
			SELECT fi.quote_tweet_id
			FROM feed_items fi
			JOIN feed_media_jobs fmj ON fmj.tweet_id = fi.tweet_id
			JOIN scope_tweets st ON st.tweet_id = fi.tweet_id
			WHERE fmj.status = 'completed'
			  AND fi.quote_tweet_id IS NOT NULL
			  AND fi.quote_tweet_id != ''
		)
		-- Same companion for quote_media video/gif rows. Owner is the
		-- quote_tweet_id (matching the quote_media post_media branch) so
		-- bookmarks of quote-wrapper tweets fall through to the parent
		-- thumbnail endpoint via initialThumbnailUri; bookmarks of the quoted
		-- tweet directly find this entry.
		SELECT 'qthumb' AS branch, mf.id AS raw_seq, (mf.id + %d) AS seq, mf.owner_id, 'quote_media_thumb' AS owner_type,
		       'image/jpeg' AS media_type, 0 AS media_index, 0 AS file_size,
		       '' AS file_path, '' AS source_url
		FROM media_files mf
		WHERE mf.id > ?
		  AND mf.owner_type = 'quote_media'
		  AND (mf.media_type = 'video' OR mf.media_type = 'gif')
		  AND mf.media_index = 0
		  AND mf.owner_id IN (SELECT quote_tweet_id FROM scope_quote_media)
		ORDER BY mf.id ASC
		LIMIT ?
	`, scopeSQL, manifestSeqQuoteMediaThumb)
	if err := appendRows(
		quoteThumbQuery,
		manifestQueryArgs(scopeArgs, cursor.QuoteMediaThumb, limit-len(manifestRows))...,
	); err != nil {
		return nil, "", true, fmt.Errorf("GetMediaManifestV2: %w", err)
	}

	var entries []model.ManifestEntry
	// Collect tweet_ids / video_ids for recency lookup in one pass.
	tweetIDs := map[string]struct{}{}
	videoIDs := map[string]struct{}{}
	for _, row := range manifestRows {
		nextCursor.advance(row.Branch, row.RawSeq)
		if row.OwnerType == "subtitle" {
			platform := videoPlatformFromChannelID(row.SourceURL)
			if platform == "" {
				platform = videoPlatform(row.OwnerID)
			}
			ownerKind := videoOwnerKindForPlatform(platform)
			subtitleRelPath := db.findSubtitleRelativePath(row.FilePath)
			if subtitleRelPath == "" {
				continue
			}
			subtitleSize := fileInfoSize(resolveManifestDataPath(db.dataDir, subtitleRelPath))
			if sanitizedSize, ok := sanitizedVTTSize(resolveManifestDataPath(db.dataDir, subtitleRelPath)); ok {
				subtitleSize = sanitizedSize
			}
			videoAbsPath := resolveManifestDataPath(db.dataDir, row.FilePath)
			videoStem := strings.TrimSuffix(filepath.Base(videoAbsPath), filepath.Ext(videoAbsPath))
			lang := subtitlemeta.TrackLang(videoStem, filepath.Base(subtitleRelPath))
			infoPath := filepath.Join(filepath.Dir(videoAbsPath), videoStem+".info.json")
			isAuto := subtitlemeta.IsAuto(infoPath, lang)
			audioLang := subtitlemeta.Language(infoPath)
			entries = append(entries, model.ManifestEntry{
				AssetID:            BuildManifestAssetID(platform, ownerKind, row.OwnerID, "subtitle", 0),
				AssetKind:          "subtitle",
				OwnerID:            row.OwnerID,
				OwnerKind:          ownerKind,
				Scope:              scope,
				ServerURL:          "/api/media/subtitle/" + row.OwnerID,
				Bucket:             videoBucket(platform),
				SizeHint:           subtitleSize,
				ContentType:        "text/vtt",
				IsAuto:             &isAuto,
				AudioLanguage:      audioLang,
				MediaType:          "subtitle",
				ManifestSeq:        row.Seq,
				EffectiveRecencyMs: 0,
			})
			continue
		}
		if row.OwnerType == "avatar" {
			row.FilePath = db.findAvatarRelativePath(row.OwnerID)
			if row.FilePath == "" {
				if strings.TrimSpace(row.SourceURL) == "" {
					continue
				}
			} else {
				row.FileSize = fileInfoSize(resolveManifestDataPath(db.dataDir, row.FilePath))
			}
		}
		if row.OwnerType == "banner" {
			row.FilePath = db.findBannerRelativePath(row.OwnerID)
			if row.FilePath == "" {
				continue
			}
			row.FileSize = fileInfoSize(resolveManifestDataPath(db.dataDir, row.FilePath))
		}
		if row.OwnerType == "feed_media_thumb" || row.OwnerType == "quote_media_thumb" {
			// Cached generated thumbnail. May not exist yet — first
			// /api/media/thumbnail/<tweetID> hit will create it via ffmpeg.
			// Size 0 is acceptable initial value; the asset still resolves.
			thumbRel := filepath.Join("thumbnails", "generated", row.OwnerID+".jpg")
			row.FileSize = fileInfoSize(resolveManifestDataPath(db.dataDir, thumbRel))
		}
		if row.OwnerType == "video_thumbnail" {
			thumbRel := db.findVideoThumbnailRelativePath(row.OwnerID, row.FilePath)
			if thumbRel == "" {
				continue
			}
			row.FilePath = thumbRel
			row.FileSize = fileInfoSize(resolveManifestDataPath(db.dataDir, thumbRel))
		}
		if (row.OwnerType == "feed_media" || row.OwnerType == "quote_media") && manifestSkipsFile(row.FilePath) {
			continue
		}
		e := buildManifestEntry(row.OwnerID, row.OwnerType, row.MediaType, row.MediaIndex, row.FileSize, row.FilePath, row.SourceURL, scope)
		if row.OwnerType == "avatar" || row.OwnerType == "banner" || manifestUsesImageTransport(row.FilePath, row.MediaType) {
			e.ContentType = detectManifestContentType(resolveManifestDataPath(db.dataDir, row.FilePath), e.ContentType)
		}
		e.ManifestSeq = row.Seq
		entries = append(entries, e)
		if row.OwnerType == "video_stream" {
			videoIDs[row.OwnerID] = struct{}{}
		}
		if row.OwnerType == "feed_media" || row.OwnerType == "feed_media_thumb" {
			tweetIDs[row.OwnerID] = struct{}{}
		} else if row.OwnerType == "quote_media" || row.OwnerType == "quote_media_thumb" {
			// quote_media entries inherit recency from the parent tweet's cluster.
			// Look up the parent tweet_id from feed_items (any row whose
			// quote_tweet_id matches this ownerID works — they share content).
			tweetIDs[row.OwnerID] = struct{}{}
		}
	}

	if err := db.populateEffectiveRecency(entries, tweetIDs); err != nil {
		// Non-fatal — entries still serve without cluster recency, client
		// falls back to its local now() heuristic.
		_ = err
	}
	if err := db.populateVideoRecency(entries, videoIDs); err != nil {
		_ = err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ManifestSeq < entries[j].ManifestSeq
	})
	return entries, nextCursor.String(), len(manifestRows) < limit, nil
}

func (db *DB) queryManifestRawRows(query string, args ...any) ([]manifestRawRow, error) {
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []manifestRawRow
	for rows.Next() {
		var row manifestRawRow
		if err := rows.Scan(
			&row.Branch,
			&row.RawSeq,
			&row.Seq,
			&row.OwnerID,
			&row.OwnerType,
			&row.MediaType,
			&row.MediaIndex,
			&row.FileSize,
			&row.FilePath,
			&row.SourceURL,
		); err != nil {
			return nil, fmt.Errorf("GetMediaManifestV2 scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

var manifestVttCueSettingRe = regexp.MustCompile(`\s+(?:align|position|line|size|vertical):\S+`)

func sanitizedVTTSize(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return int64(len(sanitizeManifestVTT(data))), true
}

func sanitizeManifestVTT(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.Contains(line, "-->") {
			lines[i] = manifestVttCueSettingRe.ReplaceAllString(line, "")
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// buildManifestEntry assembles one asset identity from a media_files row.
// Platform + owner_kind are derived from owner_type; asset_kind maps from
// owner_type + media_type.
func buildManifestEntry(ownerID, ownerType, mediaType string, mediaIndex int, fileSize int64, filePath, sourceURL, scope string) model.ManifestEntry {
	platform := "twitter"
	ownerKind := "tweet"
	assetKind := "post_media"
	bucket := "twitter_media"
	var serverURL string
	var contentType string

	switch ownerType {
	case "feed_media":
		if manifestUsesImageTransport(filePath, mediaType) {
			serverURL = fmt.Sprintf("/api/media/slide/%s/%d", ownerID, mediaIndex)
		} else if mediaType == "video" || mediaType == "gif" {
			serverURL = "/api/media/stream/" + ownerID
		} else {
			serverURL = fmt.Sprintf("/api/media/slide/%s/%d", ownerID, mediaIndex)
		}
		contentType = contentTypeForMediaPath(filePath, mediaType, "image/jpeg")
	case "quote_media":
		ownerKind = "tweet"
		assetKind = "post_media"
		if manifestUsesImageTransport(filePath, mediaType) {
			serverURL = fmt.Sprintf("/api/media/slide/%s/%d", ownerID, mediaIndex)
		} else if mediaType == "video" || mediaType == "gif" {
			serverURL = "/api/media/stream/" + ownerID
		} else {
			serverURL = fmt.Sprintf("/api/media/slide/%s/%d", ownerID, mediaIndex)
		}
		contentType = contentTypeForMediaPath(filePath, mediaType, "image/jpeg")
	case "avatar":
		ownerKind = "channel"
		assetKind = "avatar"
		bucket = "avatars"
		serverURL = "/api/media/avatar/" + ownerID
		contentType = contentTypeForPath(filePath, "image/jpeg")
		// ownerID is "twitter_<handle>" / "youtube_<id>" / "tiktok_<handle>" —
		// derive the platform prefix.
		if idx := strings.Index(ownerID, "_"); idx > 0 {
			platform = ownerID[:idx]
		}
	case "banner":
		ownerKind = "channel"
		assetKind = "banner"
		bucket = "banners"
		serverURL = "/api/media/banner/" + ownerID
		contentType = contentTypeForPath(filePath, "image/jpeg")
		if idx := strings.Index(ownerID, "_"); idx > 0 {
			platform = ownerID[:idx]
		}
	case "video_stream":
		// ownerID is the video_id. For bookmark-created Twitter stubs the ID is a
		// bare tweet ID, so prefer the channel_id hint (plumbed through sourceURL)
		// over the legacy ownerID-prefix heuristic.
		assetKind = "video_stream"
		platform = videoPlatformFromChannelID(sourceURL)
		if platform == "" {
			platform = videoPlatform(ownerID)
		}
		ownerKind = videoOwnerKindForPlatform(platform)
		bucket = videoBucket(platform)
		serverURL = "/api/media/stream/" + ownerID
		contentType = contentTypeForMediaPath(filePath, mediaType, "video/mp4")
	case "video_thumbnail":
		assetKind = "post_thumbnail"
		platform = videoPlatformFromChannelID(sourceURL)
		if platform == "" {
			platform = videoPlatform(ownerID)
		}
		ownerKind = videoOwnerKindForPlatform(platform)
		bucket = videoBucket(platform)
		serverURL = "/api/media/thumbnail/" + ownerID
		contentType = contentTypeForPath(filePath, "image/jpeg")
	case "feed_media_thumb", "quote_media_thumb":
		// Per-tweet post_thumbnail for video/gif feed_media. Coil3 cannot
		// decode MP4, so we emit a separate thumbnail asset routed through
		// `/api/media/thumbnail/<tweetID>` (server extracts first frame).
		ownerKind = "tweet"
		assetKind = "post_thumbnail"
		bucket = "twitter_media"
		serverURL = "/api/media/thumbnail/" + ownerID
		contentType = "image/jpeg"
	}
	return model.ManifestEntry{
		AssetID:     BuildManifestAssetID(platform, ownerKind, ownerID, assetKind, mediaIndex),
		AssetKind:   assetKind,
		OwnerID:     ownerID,
		OwnerKind:   ownerKind,
		Scope:       scope,
		ServerURL:   serverURL,
		Bucket:      bucket,
		SizeHint:    fileSize,
		ContentType: contentType,
		MediaType:   mediaType,
		MediaIndex:  mediaIndex,
	}
}

func (db *DB) findSubtitleRelativePath(videoFilePath string) string {
	if videoFilePath == "" {
		return ""
	}
	absFilePath := resolveManifestDataPath(db.dataDir, videoFilePath)
	videoBase := strings.TrimSuffix(absFilePath, filepath.Ext(absFilePath))
	for _, suffix := range []string{".en.vtt", ".vtt"} {
		candidate := videoBase + suffix
		if _, err := os.Stat(candidate); err == nil {
			rel, relErr := filepath.Rel(db.dataDir, candidate)
			if relErr == nil {
				return rel
			}
		}
	}
	dir := filepath.Dir(absFilePath)
	stem := filepath.Base(videoBase)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, stem) && strings.HasSuffix(name, ".vtt") {
			candidate := filepath.Join(dir, name)
			rel, relErr := filepath.Rel(db.dataDir, candidate)
			if relErr == nil {
				return rel
			}
		}
	}
	return ""
}

func (db *DB) findAvatarRelativePath(channelID string) string {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
		rel := filepath.Join("thumbnails", "avatars", channelID+ext)
		if _, err := os.Stat(resolveManifestDataPath(db.dataDir, rel)); err == nil {
			return rel
		}
	}
	return ""
}

func (db *DB) findBannerRelativePath(channelID string) string {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
		rel := filepath.Join("thumbnails", "banners", channelID+ext)
		if _, err := os.Stat(resolveManifestDataPath(db.dataDir, rel)); err == nil {
			return rel
		}
	}
	return ""
}

func (db *DB) findVideoThumbnailRelativePath(videoID, pathHint string) string {
	if pathHint == "" {
		return ""
	}
	absPath := resolveManifestDataPath(db.dataDir, pathHint)
	if isManifestImagePath(absPath) {
		if _, err := os.Stat(absPath); err == nil {
			return normalizeManifestRelPath(db.dataDir, absPath, pathHint)
		}
	}

	base := strings.TrimSuffix(absPath, filepath.Ext(absPath))
	for _, ext := range []string{".webp", ".jpg", ".jpeg", ".png", ".image"} {
		candidate := base + ext
		if _, err := os.Stat(candidate); err == nil {
			return normalizeManifestRelPath(db.dataDir, candidate, candidate)
		}
	}

	dir := filepath.Dir(absPath)
	if strings.HasSuffix(base, "_0") {
		slideBase := base[:len(base)-2] + "_1"
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			candidate := slideBase + ext
			if _, err := os.Stat(candidate); err == nil {
				return normalizeManifestRelPath(db.dataDir, candidate, candidate)
			}
		}
	}
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		candidate := filepath.Join(dir, videoID+"_1"+ext)
		if _, err := os.Stat(candidate); err == nil {
			return normalizeManifestRelPath(db.dataDir, candidate, candidate)
		}
	}

	return ""
}

func normalizeManifestRelPath(dataDir, absPath, fallback string) string {
	if rel, err := filepath.Rel(dataDir, absPath); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return fallback
}

func fileInfoSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func resolveManifestDataPath(dataDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dataDir, path)
}

func contentTypeForMediaPath(path, mediaType, fallback string) string {
	if ct := contentTypeForPath(path, ""); ct != "" {
		return ct
	}
	if mediaType == "video" || mediaType == "gif" {
		return "video/mp4"
	}
	return fallback
}

func manifestUsesImageTransport(path, mediaType string) bool {
	if isManifestImagePath(path) {
		return true
	}
	return mediaType != "video" && mediaType != "gif"
}

func isManifestImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".image":
		return true
	default:
		return false
	}
}

func manifestSkipsFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".m4a", ".aac", ".ogg":
		return true
	default:
		return false
	}
}

func contentTypeForPath(path, fallback string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".image":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".mov":
		return "video/quicktime"
	case ".m4v":
		return "video/x-m4v"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".aac", ".ogg":
		return "audio/mp4"
	case ".vtt":
		return "text/vtt"
	default:
		return fallback
	}
}

func detectManifestContentType(path, fallback string) string {
	f, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer func() {
		_ = f.Close()
	}()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return fallback
	}
	if detected := http.DetectContentType(buf[:n]); strings.HasPrefix(detected, "image/") {
		return detected
	}
	return fallback
}

// videoPlatform extracts the platform prefix from a video_id. Android-side
// platform prefixes match server video ID conventions: "youtube_abc" →
// "youtube", "tiktok_xyz" → "tiktok", "instagram_123" → "instagram". For
// opaque IDs without an underscore, fall back to youtube (the legacy default).
func videoPlatform(videoID string) string {
	if idx := strings.Index(videoID, "_"); idx > 0 {
		return videoID[:idx]
	}
	return "youtube"
}

func videoPlatformFromChannelID(channelID string) string {
	switch {
	case strings.HasPrefix(channelID, "twitter_"):
		return "twitter"
	case strings.HasPrefix(channelID, "youtube_"):
		return "youtube"
	case strings.HasPrefix(channelID, "tiktok_"):
		return "tiktok"
	case strings.HasPrefix(channelID, "instagram_"):
		return "instagram"
	default:
		return ""
	}
}

func videoOwnerKindForPlatform(platform string) string {
	switch platform {
	case "twitter":
		return "tweet"
	case "tiktok":
		return "tiktok_video"
	case "instagram":
		return "instagram_reel"
	default:
		return "youtube_video"
	}
}

func videoOwnerKind(videoID string) string {
	return videoOwnerKindForPlatform(videoPlatform(videoID))
}

func videoBucket(platform string) string {
	switch platform {
	case "youtube":
		return "youtube_videos"
	case "twitter":
		return "twitter_media"
	default:
		return "shorts_videos"
	}
}

// populateEffectiveRecency fills EffectiveRecencyMs for Twitter-origin
// entries using the cluster-aware max(published_at) SQL from the
// track-a intent-pass (#9). Entries are mutated in place.
func (db *DB) populateEffectiveRecency(entries []model.ManifestEntry, tweetIDs map[string]struct{}) error {
	if len(tweetIDs) == 0 {
		return nil
	}

	targets := make([]string, 0, len(tweetIDs))
	args := make([]any, 0, len(tweetIDs))
	for tid := range tweetIDs {
		targets = append(targets, "(?)")
		args = append(args, tid)
	}

	query := fmt.Sprintf(`
		WITH targets(tid) AS (VALUES %s),
		pubs AS (
			SELECT t.tid, fi.published_at AS pub
			FROM targets t
			JOIN feed_items root ON root.tweet_id = t.tid
			JOIN feed_items fi ON fi.content_hash = root.content_hash
			WHERE fi.content_hash IS NOT NULL AND fi.content_hash != ''

			UNION ALL

			SELECT t.tid, rs.published_at AS pub
			FROM targets t
			JOIN feed_items root ON root.tweet_id = t.tid
			JOIN retweet_sources rs ON rs.content_hash = root.content_hash
			WHERE root.content_hash IS NOT NULL AND root.content_hash != ''

			UNION ALL

			SELECT t.tid, fi.published_at AS pub
			FROM targets t
			JOIN feed_items fi ON fi.quote_tweet_id = t.tid
			WHERE fi.quote_tweet_id IS NOT NULL AND fi.quote_tweet_id != ''

			UNION ALL

			SELECT t.tid, fi.published_at AS pub
			FROM targets t
			JOIN feed_items fi ON fi.tweet_id = t.tid
		)
		SELECT tid, MAX(pub)
		FROM pubs
		GROUP BY tid
	`, strings.Join(targets, ","))

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	recencyBy := make(map[string]int64, len(tweetIDs))
	for rows.Next() {
		var tid string
		var pub sql.NullInt64
		if err := rows.Scan(&tid, &pub); err != nil {
			return err
		}
		if pub.Valid {
			recencyBy[tid] = pub.Int64
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range entries {
		// Avatar owner_id is "twitter_<handle>" — fall back to 0 until we
		// add per-channel cluster recency (channel updates are rare).
		if r, ok := recencyBy[entries[i].OwnerID]; ok {
			entries[i].EffectiveRecencyMs = r
		}
	}
	return nil
}

func (db *DB) populateVideoRecency(entries []model.ManifestEntry, videoIDs map[string]struct{}) error {
	if len(videoIDs) == 0 {
		return nil
	}

	targets := make([]string, 0, len(videoIDs))
	args := make([]any, 0, len(videoIDs))
	for videoID := range videoIDs {
		targets = append(targets, "?")
		args = append(args, videoID)
	}

	query := fmt.Sprintf(
		`SELECT video_id, published_at FROM videos WHERE video_id IN (%s)`,
		strings.Join(targets, ","),
	)
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	recencyBy := make(map[string]int64, len(videoIDs))
	for rows.Next() {
		var videoID string
		var publishedAt sql.NullInt64
		if err := rows.Scan(&videoID, &publishedAt); err != nil {
			return err
		}
		if publishedAt.Valid {
			recencyBy[videoID] = publishedAt.Int64
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range entries {
		if r, ok := recencyBy[entries[i].OwnerID]; ok {
			entries[i].EffectiveRecencyMs = r
		}
	}
	return nil
}
