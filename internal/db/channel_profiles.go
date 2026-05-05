package db

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

var shortDescriptionMentionRe = regexp.MustCompile(`(^|[^A-Za-z0-9_@.])@([A-Za-z0-9_](?:[A-Za-z0-9_.]{0,30}[A-Za-z0-9_])?)`)

// UpsertChannelProfile inserts or updates a profile row keyed by channel_id.
func (db *DB) UpsertChannelProfile(p model.ChannelProfile) error {
	channelID := strings.TrimSpace(p.ChannelID)
	if channelID == "" {
		return fmt.Errorf("UpsertChannelProfile: empty channel_id")
	}
	if p.Platform == "" {
		return fmt.Errorf("UpsertChannelProfile: empty platform for %s", channelID)
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO channel_profiles (
				channel_id, platform, handle, display_name, bio, website,
				followers, following, verified, verified_type, protected,
				avatar_url, banner_url, fetched_at, fail_count, next_retry_at, tombstone
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(channel_id) DO UPDATE SET
				platform      = excluded.platform,
				handle        = COALESCE(excluded.handle, channel_profiles.handle),
				display_name  = excluded.display_name,
				bio           = excluded.bio,
				website       = excluded.website,
				followers     = excluded.followers,
				following     = excluded.following,
				verified      = excluded.verified,
				verified_type = excluded.verified_type,
				protected     = excluded.protected,
				avatar_url    = COALESCE(excluded.avatar_url, channel_profiles.avatar_url),
				banner_url    = COALESCE(excluded.banner_url, channel_profiles.banner_url),
				fetched_at    = COALESCE(excluded.fetched_at, channel_profiles.fetched_at),
				fail_count    = excluded.fail_count,
				next_retry_at = excluded.next_retry_at,
				tombstone     = excluded.tombstone
		`,
			channelID, p.Platform, nilIfEmpty(p.Handle), p.DisplayName, p.Bio,
			nilIfEmpty(p.Website),
			p.Followers, p.Following,
			boolToInt(p.Verified), nilIfEmpty(p.VerifiedType), boolToInt(p.Protected),
			nilIfEmpty(p.AvatarURL), nilIfEmpty(p.BannerURL),
			nilIfTimeZero(p.FetchedAt), p.FailCount, nilIfTimeZero(p.NextRetryAt),
			boolToInt(p.Tombstone),
		)
		return err
	})
}

// GetChannelProfile returns the row for the given channel_id, or (nil, nil).
func (db *DB) GetChannelProfile(channelID string) (*model.ChannelProfile, error) {
	id := strings.TrimSpace(channelID)
	if id == "" {
		return nil, nil
	}
	row := db.conn.QueryRow(`
		SELECT channel_id, platform, COALESCE(handle,''),
		       COALESCE(display_name,''), COALESCE(bio,''), COALESCE(website,''),
		       followers, following, verified, COALESCE(verified_type,''), protected,
		       COALESCE(avatar_url,''), COALESCE(banner_url,''),
		       fetched_at, fail_count, next_retry_at, tombstone
		FROM channel_profiles WHERE channel_id = ?
	`, id)
	var p model.ChannelProfile
	var verified, protected, tombstone int
	var fetchedAt, nextRetryAt sql.NullInt64
	err := row.Scan(
		&p.ChannelID, &p.Platform, &p.Handle,
		&p.DisplayName, &p.Bio, &p.Website,
		&p.Followers, &p.Following, &verified, &p.VerifiedType, &protected,
		&p.AvatarURL, &p.BannerURL,
		&fetchedAt, &p.FailCount, &nextRetryAt, &tombstone,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Verified = verified != 0
	p.Protected = protected != 0
	p.Tombstone = tombstone != 0
	p.FetchedAt = millisToTimePtr(fetchedAt)
	p.NextRetryAt = millisToTimePtr(nextRetryAt)
	return &p, nil
}

// GetChannelIDsByAvatarURLs resolves known avatar source URLs back to channel IDs.
// Used when a feed row has a raw avatar URL but the parsed handle is missing.
func (db *DB) GetChannelIDsByAvatarURLs(urls []string) (map[string]string, error) {
	if len(urls) == 0 {
		return map[string]string{}, nil
	}

	keys := make([]string, 0, len(urls))
	seen := make(map[string]struct{}, len(urls))
	for _, raw := range urls {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return map[string]string{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		args = append(args, key)
	}

	rows, err := db.conn.Query(`
		SELECT LOWER(avatar_url), channel_id
		FROM channel_profiles
		WHERE tombstone = 0
		  AND avatar_url IS NOT NULL
		  AND avatar_url != ''
		  AND LOWER(avatar_url) IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string, len(keys))
	for rows.Next() {
		var avatarURL, channelID string
		if err := rows.Scan(&avatarURL, &channelID); err != nil {
			return nil, err
		}
		if avatarURL == "" || channelID == "" {
			continue
		}
		out[avatarURL] = channelID
	}
	return out, rows.Err()
}

// GetTwitterChannelProfilesByHandles returns the freshest known Twitter profile
// rows keyed by normalized handle. This is the profile-worker-backed identity
// source for feed/card display-name repair when feed_items still carries blanks.
func (db *DB) GetTwitterChannelProfilesByHandles(handles []string) (map[string]model.ChannelProfile, error) {
	if len(handles) == 0 {
		return map[string]model.ChannelProfile{}, nil
	}

	keys := make([]string, 0, len(handles))
	seen := make(map[string]struct{}, len(handles))
	for _, raw := range handles {
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "@")))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return map[string]model.ChannelProfile{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		args = append(args, key)
	}

	rows, err := db.conn.Query(`
		SELECT channel_id, COALESCE(handle,''), COALESCE(display_name,''), COALESCE(avatar_url,'')
		FROM channel_profiles
		WHERE platform = 'twitter'
		  AND tombstone = 0
		  AND LOWER(COALESCE(handle, '')) IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]model.ChannelProfile, len(keys))
	for rows.Next() {
		var p model.ChannelProfile
		if err := rows.Scan(&p.ChannelID, &p.Handle, &p.DisplayName, &p.AvatarURL); err != nil {
			return nil, err
		}
		p.Platform = "twitter"
		key := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(p.Handle, "@")))
		if key == "" && strings.HasPrefix(p.ChannelID, "twitter_") {
			key = strings.TrimPrefix(strings.ToLower(p.ChannelID), "twitter_")
			p.Handle = key
		}
		if key == "" {
			continue
		}
		out[key] = p
	}
	return out, rows.Err()
}

// NextChannelProfileRefreshCandidate returns the next channel_id due for a
// background refresh. In addition to stale rows, short-form rows with no banner
// are treated as immediately due so synthesized banners are backfilled without
// waiting for a hover/profile-page request.
func (db *DB) NextChannelProfileRefreshCandidate(ttl time.Duration) (string, error) {
	cutoffMs := time.Now().Add(-ttl).UnixMilli()
	nowMs := time.Now().UnixMilli()
	var channelID string
	err := db.conn.QueryRow(`
			SELECT channel_id FROM channel_profiles
			WHERE tombstone = 0
			  AND (
			       (
			           (next_retry_at = 0 OR next_retry_at < ?)
			           AND (fetched_at = 0 OR fetched_at < ?)
			       )
			    OR (
			         platform IN ('tiktok', 'instagram')
			         AND COALESCE(banner_url, '') = ''
			         AND (next_retry_at = 0 OR next_retry_at < ?)
			         AND EXISTS (
			             SELECT 1
			             FROM videos v
			             WHERE v.channel_id = channel_profiles.channel_id
			               AND COALESCE(v.file_path, '') != ''
			               AND COALESCE(v.is_temp, 0) = 0
			         )
			       )
			  )
			ORDER BY
				CASE
					WHEN platform IN ('tiktok', 'instagram')
					     AND COALESCE(banner_url, '') = ''
					     AND EXISTS (
					         SELECT 1
					         FROM videos v
					         WHERE v.channel_id = channel_profiles.channel_id
					           AND COALESCE(v.file_path, '') != ''
					           AND COALESCE(v.is_temp, 0) = 0
					     ) THEN 0
					WHEN fetched_at = 0 AND EXISTS (
						SELECT 1 FROM channels c WHERE c.channel_id = channel_profiles.channel_id
					) THEN 1
					WHEN fetched_at = 0 THEN 2
					ELSE 3
				END,
				fetched_at ASC
			LIMIT 1
		`, nowMs, cutoffMs, nowMs).Scan(&channelID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return channelID, err
}

// ListQuoteAvatarProfileIDs returns every profile row that owns a quote avatar
// the server should keep available on disk. This includes normal quote authors
// with handles plus synthetic rows created for quote avatars where RSSHub did
// not parse a handle.
func (db *DB) ListQuoteAvatarProfileIDs() ([]string, error) {
	rows, err := db.conn.Query(`
		WITH quote_profile_ids AS (
			SELECT DISTINCT 'twitter_' || LOWER(quote_author_handle) AS channel_id
			FROM feed_items
			WHERE COALESCE(quote_author_handle, '') != ''

			UNION

			SELECT channel_id
			FROM channel_profiles
			WHERE channel_id LIKE 'twitter_avatarhash_%'
		)
		SELECT cp.channel_id
		FROM channel_profiles cp
		INNER JOIN quote_profile_ids q ON q.channel_id = cp.channel_id
		WHERE cp.tombstone = 0
		ORDER BY
			CASE WHEN COALESCE(cp.avatar_url, '') = '' THEN 0 ELSE 1 END,
			cp.fetched_at ASC,
			cp.channel_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		if channelID != "" {
			ids = append(ids, channelID)
		}
	}
	return ids, rows.Err()
}

// ListFeedAvatarProfileIDs returns every profile row whose identity should be
// available for feed/thread/player rendering. Unlike ListQuoteAvatarProfileIDs,
// this also covers primary authors, reply parents, retweeters, and YouTube
// commenters so visible identity chrome and hover cards do not depend on
// render-time repair.
func (db *DB) ListFeedAvatarProfileIDs() ([]string, error) {
	rows, err := db.conn.Query(`
		WITH feed_profile_ids AS (
			SELECT 'twitter_' || LOWER(author_handle) AS channel_id,
			       MAX(COALESCE(published_at, 0), COALESCE(fetched_at, 0)) AS seen_at
			FROM feed_items
			WHERE COALESCE(author_handle, '') != ''

			UNION

			SELECT 'twitter_' || LOWER(quote_author_handle) AS channel_id,
			       MAX(COALESCE(quote_published_at, 0), COALESCE(published_at, 0), COALESCE(fetched_at, 0)) AS seen_at
			FROM feed_items
			WHERE COALESCE(quote_author_handle, '') != ''

			UNION

			SELECT 'twitter_' || LOWER(reply_to_handle) AS channel_id,
			       MAX(COALESCE(published_at, 0), COALESCE(fetched_at, 0)) AS seen_at
			FROM feed_items
			WHERE COALESCE(reply_to_handle, '') != ''

			UNION

			SELECT 'twitter_' || LOWER(retweeted_by_handle) AS channel_id,
			       MAX(COALESCE(published_at, 0), COALESCE(fetched_at, 0)) AS seen_at
			FROM feed_items
			WHERE COALESCE(retweeted_by_handle, '') != ''

			UNION

			SELECT 'twitter_' || LOWER(source_handle) AS channel_id,
			       MAX(COALESCE(published_at, 0), COALESCE(fetched_at, 0)) AS seen_at
			FROM feed_items
			WHERE COALESCE(is_retweet, 0) = 1
			  AND COALESCE(source_handle, '') != ''

			UNION

			SELECT 'twitter_' || LOWER(retweeter_handle) AS channel_id,
			       COALESCE(published_at, 0) AS seen_at
			FROM retweet_sources
			WHERE COALESCE(retweeter_handle, '') != ''

			UNION

			SELECT channel_id, 0 AS seen_at
			FROM channel_profiles
			WHERE channel_id LIKE 'twitter_avatarhash_%'

			UNION

			SELECT CASE
				WHEN author_id GLOB 'youtube_UC*' THEN author_id
				WHEN author_id GLOB 'UC*' THEN 'youtube_' || author_id
				ELSE ''
			END AS channel_id,
			MAX(COALESCE(published_at, 0), COALESCE(fetched_at, 0)) AS seen_at
			FROM video_comments
			WHERE COALESCE(platform, 'youtube') = 'youtube'
			  AND COALESCE(author_id, '') != ''
		),
		feed_profiles AS (
			SELECT channel_id, MAX(seen_at) AS last_seen_at
			FROM feed_profile_ids
			WHERE channel_id != ''
			GROUP BY channel_id
		)
		SELECT cp.channel_id
		FROM channel_profiles cp
		INNER JOIN feed_profiles f ON f.channel_id = cp.channel_id
		WHERE cp.tombstone = 0
		ORDER BY
			f.last_seen_at DESC,
			CASE WHEN COALESCE(cp.fetched_at, 0) = 0 THEN 0 ELSE 1 END,
			CASE WHEN COALESCE(cp.avatar_url, '') = '' THEN 0 ELSE 1 END,
			cp.fetched_at ASC,
			cp.channel_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		if channelID != "" {
			ids = append(ids, channelID)
		}
	}
	return ids, rows.Err()
}

// SeedYouTubeCommentAuthorProfiles creates lightweight profile rows for
// commenter channels discovered by yt-dlp. The thumbnail URL is enough for the
// avatar worker to cache the image without making Android hit YouTube directly.
func (db *DB) SeedYouTubeCommentAuthorProfiles() (int, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT COALESCE(author_id, ''), COALESCE(author_name, ''), COALESCE(author_thumbnail, '')
		FROM video_comments
		WHERE COALESCE(platform, 'youtube') = 'youtube'
		  AND COALESCE(author_id, '') != ''
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type seedRow struct {
		channelID string
		handle    string
		name      string
		avatarURL string
	}
	byChannel := map[string]seedRow{}
	for rows.Next() {
		var authorID, authorName, avatarURL string
		if err := rows.Scan(&authorID, &authorName, &avatarURL); err != nil {
			return 0, err
		}
		channelID := youtubeCommentAuthorChannelID(authorID)
		if channelID == "" {
			continue
		}
		row := seedRow{
			channelID: channelID,
			handle:    strings.TrimPrefix(channelID, "youtube_"),
			name:      strings.TrimSpace(authorName),
			avatarURL: httpURLOrEmpty(avatarURL),
		}
		if existing, ok := byChannel[channelID]; ok {
			if existing.avatarURL != "" {
				row.avatarURL = existing.avatarURL
			}
			if existing.name != "" {
				row.name = existing.name
			}
		}
		byChannel[channelID] = row
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(byChannel) == 0 {
		return 0, nil
	}

	var inserted int
	err = db.WithWrite(func(tx *sql.Tx) error {
		for _, row := range byChannel {
			res, err := tx.Exec(`
				INSERT INTO channel_profiles (channel_id, platform, handle, display_name, avatar_url)
				VALUES (?, 'youtube', ?, ?, ?)
				ON CONFLICT(channel_id) DO UPDATE SET
					platform = excluded.platform,
					handle = COALESCE(NULLIF(channel_profiles.handle, ''), excluded.handle),
					display_name = COALESCE(NULLIF(channel_profiles.display_name, ''), excluded.display_name),
					avatar_url = COALESCE(NULLIF(channel_profiles.avatar_url, ''), excluded.avatar_url)
				WHERE channel_profiles.platform != excluded.platform
				   OR (COALESCE(channel_profiles.handle, '') = '' AND COALESCE(excluded.handle, '') != '')
				   OR (COALESCE(channel_profiles.display_name, '') = '' AND COALESCE(excluded.display_name, '') != '')
				   OR (COALESCE(channel_profiles.avatar_url, '') = '' AND COALESCE(excluded.avatar_url, '') != '')
			`, row.channelID, nilIfEmpty(row.handle), nilIfEmpty(row.name), nilIfEmpty(row.avatarURL))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted += int(n)
			}
		}
		return nil
	})
	return inserted, err
}

func youtubeCommentAuthorChannelID(authorID string) string {
	return model.YouTubeCommentAuthorChannelID(authorID)
}

func httpURLOrEmpty(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return raw
	}
	return ""
}

// SeedChannelProfileRowsForFeedItems inserts lightweight Twitter profile rows
// for a just-ingested feed batch. This gives hover/profile rendering a real
// identity row immediately and lets the profile worker prioritize the same
// channels without waiting for the periodic whole-DB seed.
func (db *DB) SeedChannelProfileRowsForFeedItems(items []model.FeedItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	type seedRow struct {
		handle      string
		displayName string
		avatarURL   string
	}
	rowsByChannel := make(map[string]seedRow)
	add := func(handle, displayName, avatarURL string) {
		handle = model.NormalizeTwitterHandle(handle)
		if handle == "" {
			return
		}
		channelID := "twitter_" + handle
		row := seedRow{
			handle:      handle,
			displayName: strings.TrimSpace(displayName),
		}
		if model.IsRawTwitterProfileAvatar(avatarURL) {
			row.avatarURL = strings.TrimSpace(avatarURL)
		}
		if existing, ok := rowsByChannel[channelID]; ok {
			if existing.displayName != "" {
				row.displayName = existing.displayName
			}
			if existing.avatarURL != "" {
				row.avatarURL = existing.avatarURL
			}
		}
		rowsByChannel[channelID] = row
	}
	for _, item := range items {
		add(item.AuthorHandle, item.AuthorDisplayName, item.AuthorAvatarURL)
		add(item.QuoteAuthorHandle, item.QuoteAuthorDisplayName, item.QuoteAuthorAvatarURL)
		add(item.ReplyToHandle, "", "")
		add(item.RetweetedByHandle, item.RetweetedByDisplayName, "")
		if item.IsRetweet {
			add(item.SourceHandle, "", "")
		}
	}
	if len(rowsByChannel) == 0 {
		return 0, nil
	}

	var changed int
	err := db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			INSERT INTO channel_profiles (channel_id, platform, handle, display_name, avatar_url)
			VALUES (?, 'twitter', ?, ?, ?)
			ON CONFLICT(channel_id) DO UPDATE SET
				platform = excluded.platform,
				handle = COALESCE(NULLIF(channel_profiles.handle, ''), excluded.handle),
				display_name = COALESCE(NULLIF(channel_profiles.display_name, ''), excluded.display_name),
				avatar_url = CASE
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
						THEN excluded.avatar_url
					ELSE COALESCE(NULLIF(channel_profiles.avatar_url, ''), excluded.avatar_url)
				END,
				fetched_at = CASE
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
						THEN 0
					ELSE channel_profiles.fetched_at
				END,
				fail_count = CASE
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
						THEN 0
					ELSE channel_profiles.fail_count
				END,
				next_retry_at = CASE
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
						THEN 0
					ELSE channel_profiles.next_retry_at
				END
			WHERE channel_profiles.platform != excluded.platform
			   OR (COALESCE(channel_profiles.handle, '') = '' AND COALESCE(excluded.handle, '') != '')
			   OR (COALESCE(channel_profiles.display_name, '') = '' AND COALESCE(excluded.display_name, '') != '')
			   OR (COALESCE(channel_profiles.avatar_url, '') = '' AND COALESCE(excluded.avatar_url, '') != '')
			   OR (channel_profiles.platform = 'twitter'
			       AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
			       AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%')
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for channelID, row := range rowsByChannel {
			res, err := stmt.Exec(channelID, row.handle, nilIfEmpty(row.displayName), nilIfEmpty(row.avatarURL))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				changed += int(n)
			}
		}
		return nil
	})
	return changed, err
}

// SeedChannelProfileRows inserts lightweight profile rows for every identity the
// server should be able to render/sync without waiting for a page request:
// followed channels, Twitter feed authors, quote authors, reply parents,
// retweeters, short-form account mentions found in stored descriptions, and
// Twitter mentions found in stored feed text.
// Feed-provided avatar URLs are copied into empty profile rows so downstream
// media manifests can expose the avatar before the profile refresher catches up.
// Returns the number of rows inserted or updated.
func (db *DB) SeedChannelProfileRows() (int, error) {
	var inserted int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			WITH seed_rows AS (
				SELECT c.channel_id, c.platform, NULL AS handle, NULL AS avatar_url
				FROM channels c
				INNER JOIN channel_follows cf ON cf.channel_id = c.channel_id
				WHERE c.platform IN ('twitter', 'youtube', 'tiktok', 'instagram')

				UNION

				SELECT DISTINCT 'twitter_' || LOWER(fi.author_handle) AS channel_id,
				       'twitter' AS platform,
				       LOWER(fi.author_handle) AS handle,
				       CASE
				           WHEN LOWER(COALESCE(fi.author_avatar_url, '')) LIKE '%pbs.twimg.com/profile_images/%' THEN fi.author_avatar_url
				           ELSE NULL
				       END AS avatar_url
				FROM feed_items fi
				WHERE COALESCE(fi.author_handle, '') != ''

				UNION

				SELECT DISTINCT 'twitter_' || LOWER(fi.quote_author_handle) AS channel_id,
				       'twitter' AS platform,
				       LOWER(fi.quote_author_handle) AS handle,
				       CASE
				           WHEN LOWER(COALESCE(fi.quote_author_avatar_url, '')) LIKE '%pbs.twimg.com/profile_images/%' THEN fi.quote_author_avatar_url
				           ELSE NULL
				       END AS avatar_url
				FROM feed_items fi
				WHERE COALESCE(fi.quote_author_handle, '') != ''

				UNION

				SELECT DISTINCT 'twitter_' || LOWER(fi.reply_to_handle) AS channel_id,
				       'twitter' AS platform,
				       LOWER(fi.reply_to_handle) AS handle,
				       NULL AS avatar_url
				FROM feed_items fi
				WHERE COALESCE(fi.reply_to_handle, '') != ''

				UNION

				SELECT DISTINCT 'twitter_' || LOWER(fi.retweeted_by_handle) AS channel_id,
				       'twitter' AS platform,
				       LOWER(fi.retweeted_by_handle) AS handle,
				       NULL AS avatar_url
				FROM feed_items fi
				WHERE COALESCE(fi.retweeted_by_handle, '') != ''

				UNION

				SELECT DISTINCT 'twitter_' || LOWER(fi.source_handle) AS channel_id,
				       'twitter' AS platform,
				       LOWER(fi.source_handle) AS handle,
				       NULL AS avatar_url
				FROM feed_items fi
				WHERE COALESCE(fi.is_retweet, 0) = 1
				  AND COALESCE(fi.source_handle, '') != ''

				UNION

				SELECT DISTINCT 'twitter_' || LOWER(rs.retweeter_handle) AS channel_id,
				       'twitter' AS platform,
				       LOWER(rs.retweeter_handle) AS handle,
				       NULL AS avatar_url
				FROM retweet_sources rs
				INNER JOIN feed_items fi ON fi.content_hash = rs.content_hash
				WHERE COALESCE(rs.retweeter_handle, '') != ''
			)
			INSERT INTO channel_profiles (channel_id, platform, handle, avatar_url)
			SELECT channel_id, platform, handle, NULLIF(avatar_url, '')
			FROM seed_rows
			WHERE 1 = 1
			ON CONFLICT(channel_id) DO UPDATE SET
				platform = excluded.platform,
				handle = COALESCE(NULLIF(channel_profiles.handle, ''), excluded.handle),
				avatar_url = CASE
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
						THEN excluded.avatar_url
					ELSE COALESCE(NULLIF(channel_profiles.avatar_url, ''), excluded.avatar_url)
				END,
				fetched_at = CASE
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
						THEN 0
					ELSE channel_profiles.fetched_at
				END,
				fail_count = CASE
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
						THEN 0
					ELSE channel_profiles.fail_count
				END,
				next_retry_at = CASE
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
						THEN 0
					ELSE channel_profiles.next_retry_at
				END
			WHERE channel_profiles.platform != excluded.platform
			   OR (COALESCE(channel_profiles.handle, '') = '' AND COALESCE(excluded.handle, '') != '')
			   OR (COALESCE(channel_profiles.avatar_url, '') = '' AND COALESCE(excluded.avatar_url, '') != '')
			   OR (channel_profiles.platform = 'twitter'
			       AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
			       AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%')
		`)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		inserted = int(n)
		mentionRows, err := seedShortDescriptionMentionProfileRows(tx)
		if err != nil {
			return err
		}
		inserted += mentionRows
		twitterMentionRows, err := seedTwitterTextMentionProfileRows(tx)
		if err != nil {
			return err
		}
		inserted += twitterMentionRows
		return nil
	})
	return inserted, err
}

type mentionSeedRow struct {
	platform string
	handle   string
}

func seedShortDescriptionMentionProfileRows(tx *sql.Tx) (int, error) {
	rows, err := tx.Query(`
		SELECT channel_id, COALESCE(title, ''), COALESCE(description, '')
		FROM videos
		WHERE (channel_id LIKE 'tiktok_%' OR channel_id LIKE 'instagram_%')
		  AND (title LIKE '%@%' OR description LIKE '%@%')
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	byChannelID := map[string]mentionSeedRow{}
	for rows.Next() {
		var sourceChannelID, title, description string
		if err := rows.Scan(&sourceChannelID, &title, &description); err != nil {
			return 0, err
		}
		platform := shortMentionPlatformFromChannelID(sourceChannelID)
		if platform == "" {
			continue
		}
		for _, handle := range shortDescriptionMentionHandles(title+"\n"+description, platform) {
			channelID := shortMentionChannelID(platform, handle)
			if channelID == "" {
				continue
			}
			byChannelID[channelID] = mentionSeedRow{platform: platform, handle: handle}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(byChannelID) == 0 {
		return 0, nil
	}

	return upsertMentionSeedRows(tx, byChannelID)
}

func seedTwitterTextMentionProfileRows(tx *sql.Tx) (int, error) {
	rows, err := tx.Query(`
		SELECT COALESCE(body_text, ''), COALESCE(quote_body_text, '')
		FROM feed_items
		WHERE body_text LIKE '%@%' OR quote_body_text LIKE '%@%'
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	byChannelID := map[string]mentionSeedRow{}
	for rows.Next() {
		var bodyText, quoteBodyText string
		if err := rows.Scan(&bodyText, &quoteBodyText); err != nil {
			return 0, err
		}
		for _, handle := range shortDescriptionMentionHandles(bodyText+"\n"+quoteBodyText, "twitter") {
			channelID := shortMentionChannelID("twitter", handle)
			if channelID == "" {
				continue
			}
			byChannelID[channelID] = mentionSeedRow{platform: "twitter", handle: handle}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(byChannelID) == 0 {
		return 0, nil
	}

	return upsertMentionSeedRows(tx, byChannelID)
}

func upsertMentionSeedRows(tx *sql.Tx, byChannelID map[string]mentionSeedRow) (int, error) {
	inserted := 0
	for channelID, row := range byChannelID {
		res, err := tx.Exec(`
			INSERT INTO channel_profiles (channel_id, platform, handle)
			VALUES (?, ?, ?)
			ON CONFLICT(channel_id) DO UPDATE SET
				platform = excluded.platform,
				handle = COALESCE(NULLIF(channel_profiles.handle, ''), excluded.handle)
			WHERE channel_profiles.platform != excluded.platform
			   OR (COALESCE(channel_profiles.handle, '') = '' AND COALESCE(excluded.handle, '') != '')
		`, channelID, row.platform, row.handle)
		if err != nil {
			return inserted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted += int(n)
		}
	}
	return inserted, nil
}

func shortMentionPlatformFromChannelID(channelID string) string {
	switch {
	case strings.HasPrefix(channelID, "tiktok_"):
		return "tiktok"
	case strings.HasPrefix(channelID, "instagram_"):
		return "instagram"
	default:
		return ""
	}
}

func shortDescriptionMentionHandles(text, platform string) []string {
	matches := shortDescriptionMentionRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var handles []string
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		handle := strings.TrimSpace(match[2])
		switch platform {
		case "twitter":
			handle = model.NormalizeTwitterHandle(handle)
			if handle == "" || strings.Contains(handle, ".") {
				continue
			}
		case "tiktok":
			handle = model.NormalizeTikTokHandle(handle)
			if handle == "" || model.IsTikTokInternalID(handle) {
				continue
			}
		case "instagram":
			handle = model.NormalizeInstagramHandle(handle)
			if handle == "" {
				continue
			}
		default:
			continue
		}
		if _, ok := seen[handle]; ok {
			continue
		}
		seen[handle] = struct{}{}
		handles = append(handles, handle)
	}
	return handles
}

func shortMentionChannelID(platform, handle string) string {
	switch platform {
	case "twitter":
		handle = model.NormalizeTwitterHandle(handle)
		if handle == "" || strings.Contains(handle, ".") {
			return ""
		}
		return "twitter_" + handle
	case "tiktok":
		return model.TikTokChannelIDFromHandle(handle)
	case "instagram":
		return model.InstagramChannelIDFromHandle(handle)
	default:
		return ""
	}
}

// SeedSyntheticTwitterAvatarProfiles backfills profile rows for quote-author avatar URLs
// that have no parsed handle but do have a stable twimg profile image URL.
func (db *DB) SeedSyntheticTwitterAvatarProfiles() (int, error) {
	rows, err := db.conn.Query(`
		SELECT DISTINCT quote_author_avatar_url
		FROM feed_items
		WHERE COALESCE(quote_author_handle, '') = ''
		  AND COALESCE(quote_author_avatar_url, '') != ''
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var inserted int
	err = db.WithWrite(func(tx *sql.Tx) error {
		for rows.Next() {
			var avatarURL string
			if err := rows.Scan(&avatarURL); err != nil {
				return err
			}
			channelID := model.SyntheticTwitterAvatarChannelID(avatarURL)
			if channelID == "" {
				continue
			}
			res, err := tx.Exec(`
				INSERT OR IGNORE INTO channel_profiles (channel_id, platform, avatar_url)
				VALUES (?, 'twitter', ?)
			`, channelID, avatarURL)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted += int(n)
			}
		}
		return rows.Err()
	})
	return inserted, err
}

// nilIfTimeZero returns 0 for a nil/zero *time.Time, else unix millis.
// The column is INTEGER NOT NULL DEFAULT 0, so a bare 0 means "unset".
func nilIfTimeZero(t *time.Time) any {
	if t == nil || t.IsZero() {
		return int64(0)
	}
	return t.UnixMilli()
}
