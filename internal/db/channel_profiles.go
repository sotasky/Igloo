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

type twitterProfileSeedRow struct {
	handle      string
	displayName string
	avatarURL   string
	seenAtMs    int64
}

// UpsertChannelProfile inserts or updates a profile row keyed by channel_id.
func (db *DB) UpsertChannelProfile(p model.ChannelProfile) error {
	channelID := strings.TrimSpace(p.ChannelID)
	if channelID == "" {
		return fmt.Errorf("UpsertChannelProfile: empty channel_id")
	}
	if p.Platform == "" {
		return fmt.Errorf("UpsertChannelProfile: empty platform for %s", channelID)
	}
	if err := db.WithWrite(func(tx *sql.Tx) error {
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
				fail_count    = CASE
					WHEN excluded.fail_count = 0
					     AND excluded.next_retry_at = 0
					     AND channel_profiles.next_retry_at != 0
					     AND (excluded.fetched_at = 0 OR excluded.fetched_at <= channel_profiles.fetched_at)
					THEN channel_profiles.fail_count
					ELSE excluded.fail_count
				END,
				next_retry_at = CASE
					WHEN excluded.fail_count = 0
					     AND excluded.next_retry_at = 0
					     AND channel_profiles.next_retry_at != 0
					     AND (excluded.fetched_at = 0 OR excluded.fetched_at <= channel_profiles.fetched_at)
					THEN channel_profiles.next_retry_at
					ELSE excluded.next_retry_at
				END,
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
	}); err != nil {
		return err
	}
	return db.MaintainChannelProfileAssets(channelID, time.Now().UnixMilli())
}

// ClearChannelProfileAvatar removes a stored avatar source URL when a trusted
// profile refresh proves that the previous media-derived URL should not survive.
func (db *DB) ClearChannelProfileAvatar(channelID string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE channel_profiles SET avatar_url = NULL WHERE channel_id = ?`, channelID)
		return err
	})
}

// MarkChannelProfileRefreshDue clears retry/freshness state so the profile
// worker will revisit an existing row even if the last metadata fetch is recent.
func (db *DB) MarkChannelProfileRefreshDue(channelID string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE channel_profiles
			SET fetched_at = 0,
			    next_retry_at = 0,
			    fail_count = 0,
			    tombstone = 0
			WHERE channel_id = ?
		`, channelID)
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
	return scanChannelProfile(row)
}

// GetYouTubeChannelProfileByHandle returns the newest canonical YouTube
// profile row for a known @handle, or (nil, nil).
func (db *DB) GetYouTubeChannelProfileByHandle(handle string) (*model.ChannelProfile, error) {
	handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if handle == "" {
		return nil, nil
	}
	row := db.conn.QueryRow(`
		SELECT channel_id, platform, COALESCE(handle,''),
		       COALESCE(display_name,''), COALESCE(bio,''), COALESCE(website,''),
		       followers, following, verified, COALESCE(verified_type,''), protected,
		       COALESCE(avatar_url,''), COALESCE(banner_url,''),
		       fetched_at, fail_count, next_retry_at, tombstone
		FROM channel_profiles
		WHERE LOWER(platform) = 'youtube'
		  AND COALESCE(tombstone, 0) = 0
		  AND channel_id LIKE 'youtube_UC%'
		  AND LOWER(LTRIM(COALESCE(handle, ''), '@')) = ?
		ORDER BY COALESCE(fetched_at, 0) DESC
		LIMIT 1
	`, handle)
	return scanChannelProfile(row)
}

type channelProfileScanner interface {
	Scan(dest ...any) error
}

func scanChannelProfile(row channelProfileScanner) (*model.ChannelProfile, error) {
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
	defer func() {
		_ = rows.Close()
	}()

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
	defer func() {
		_ = rows.Close()
	}()

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
	return db.nextChannelProfileRefreshCandidate(ttl, "")
}

func (db *DB) NextChannelProfileRefreshCandidateForPlatform(ttl time.Duration, platform string) (string, error) {
	return db.nextChannelProfileRefreshCandidate(ttl, strings.ToLower(strings.TrimSpace(platform)))
}

func (db *DB) nextChannelProfileRefreshCandidate(ttl time.Duration, platform string) (string, error) {
	now := time.Now()
	cutoffMs := now.Add(-ttl).UnixMilli()
	nowMs := now.UnixMilli()
	var channelID string
	err := db.conn.QueryRow(`
			SELECT channel_id FROM channel_profiles
			WHERE tombstone = 0
			  AND (? = '' OR platform = ?)
			  AND (
			       platform != 'youtube'
			       OR EXISTS (SELECT 1 FROM channels c WHERE c.channel_id = channel_profiles.channel_id)
			       OR EXISTS (SELECT 1 FROM videos v WHERE v.channel_id = channel_profiles.channel_id)
			       OR EXISTS (SELECT 1 FROM channel_follows cf WHERE cf.channel_id = channel_profiles.channel_id)
			       OR EXISTS (SELECT 1 FROM channel_stars cs WHERE cs.channel_id = channel_profiles.channel_id)
			  )
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
					WHEN fetched_at = 0
					     AND EXISTS (
					         SELECT 1
					         FROM videos v
					         INNER JOIN video_repost_sources vrs ON vrs.video_id = v.video_id
					         WHERE v.channel_id = channel_profiles.channel_id
					     ) THEN 0
					WHEN platform IN ('tiktok', 'instagram')
					     AND COALESCE(banner_url, '') = ''
					     AND EXISTS (
					         SELECT 1
					         FROM videos v
					         WHERE v.channel_id = channel_profiles.channel_id
					           AND COALESCE(v.file_path, '') != ''
					           AND COALESCE(v.is_temp, 0) = 0
					     ) THEN 1
					WHEN fetched_at = 0 AND EXISTS (
						SELECT 1 FROM channels c WHERE c.channel_id = channel_profiles.channel_id
					) THEN 2
					WHEN fetched_at = 0 THEN 3
					ELSE 4
				END,
				COALESCE((
					SELECT MAX(
						CASE
							WHEN COALESCE(vrs.reposted_at_ms, 0) > 0 THEN vrs.reposted_at_ms
							WHEN COALESCE(vrs.first_seen_at_ms, 0) > 0 THEN vrs.first_seen_at_ms
							WHEN COALESCE(v.published_at, 0) > 0 THEN v.published_at
							ELSE COALESCE(v.downloaded_at, 0)
						END
					)
					FROM videos v
					INNER JOIN video_repost_sources vrs ON vrs.video_id = v.video_id
					WHERE v.channel_id = channel_profiles.channel_id
				), 0) DESC,
				fetched_at ASC
			LIMIT 1
		`, platform, platform, nowMs, cutoffMs, nowMs).Scan(&channelID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return channelID, err
}

// ListQuoteAvatarProfileIDs returns every profile row that owns a quote avatar
// the server should keep available on disk. This includes normal quote authors
// with handles plus synthetic rows created for quote avatars where ingest did
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
	defer func() {
		_ = rows.Close()
	}()

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

			SELECT v.channel_id,
			       MAX(COALESCE(v.published_at, 0), COALESCE(v.downloaded_at, 0)) AS seen_at
			FROM videos v
			WHERE (v.channel_id LIKE 'twitter_%' OR v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
			  AND COALESCE(v.channel_id, '') != ''

			UNION

			SELECT v.channel_id,
			       MAX(COALESCE(vrs.reposted_at_ms, 0), COALESCE(vrs.first_seen_at_ms, 0),
			           COALESCE(v.published_at, 0), COALESCE(v.downloaded_at, 0)) AS seen_at
			FROM video_repost_sources vrs
			INNER JOIN videos v ON v.video_id = vrs.video_id
			WHERE (v.channel_id LIKE 'twitter_%' OR v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
			  AND COALESCE(v.channel_id, '') != ''

			UNION

			SELECT dq.channel_id,
			       MAX(COALESCE(dq.published_at_ms, 0), COALESCE(dq.added_at, 0)) AS seen_at
			FROM download_queue dq
			WHERE (dq.channel_id LIKE 'twitter_%' OR dq.channel_id LIKE 'tiktok_%' OR dq.channel_id LIKE 'instagram_%')
			  AND COALESCE(dq.status, 'pending') IN ('pending', 'processing')

			UNION

			-- YouTube comment authors intentionally stay out of the profile
			-- worker. Comments carry author_thumbnail directly, and Igloo has
			-- no clickable commenter profile surface to keep ready.
			SELECT channel_id,
			       COALESCE(fetched_at, 0) AS seen_at
			FROM channel_profiles
			WHERE platform IN ('twitter', 'youtube', 'tiktok', 'instagram')
			  AND COALESCE(tombstone, 0) = 0
			  AND (
			       platform != 'youtube'
			       OR EXISTS (SELECT 1 FROM channels c WHERE c.channel_id = channel_profiles.channel_id)
			       OR EXISTS (SELECT 1 FROM videos v WHERE v.channel_id = channel_profiles.channel_id)
			       OR EXISTS (SELECT 1 FROM channel_follows cf WHERE cf.channel_id = channel_profiles.channel_id)
			       OR EXISTS (SELECT 1 FROM channel_stars cs WHERE cs.channel_id = channel_profiles.channel_id)
			  )
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
			ORDER BY f.last_seen_at DESC, cp.channel_id ASC
		`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

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

// SeedChannelProfileRowsForFeedItems inserts lightweight Twitter profile rows
// for a just-ingested feed batch. This gives hover/profile rendering a real
// identity row immediately and lets the profile worker prioritize the same
// channels without waiting for the periodic whole-DB seed.
func (db *DB) SeedChannelProfileRowsForFeedItems(items []model.FeedItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	rowsByChannel := make(map[string]twitterProfileSeedRow)
	add := func(handle, displayName, avatarURL string, seenAtMs int64) {
		handle = model.NormalizeTwitterHandle(handle)
		if handle == "" {
			return
		}
		channelID := "twitter_" + handle
		row := twitterProfileSeedRow{
			handle:      handle,
			displayName: strings.TrimSpace(displayName),
			seenAtMs:    seenAtMs,
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
			if existing.seenAtMs > row.seenAtMs {
				row.seenAtMs = existing.seenAtMs
			}
		}
		rowsByChannel[channelID] = row
	}
	for _, item := range items {
		seenAtMs := feedItemIdentitySeenAtMs(item)
		add(item.AuthorHandle, item.AuthorDisplayName, item.AuthorAvatarURL, seenAtMs)
		add(item.QuoteAuthorHandle, item.QuoteAuthorDisplayName, item.QuoteAuthorAvatarURL, seenAtMs)
		add(item.ReplyToHandle, "", "", seenAtMs)
		add(item.RetweetedByHandle, item.RetweetedByDisplayName, "", seenAtMs)
		if item.IsRetweet {
			add(item.SourceHandle, "", "", seenAtMs)
		}
	}
	if len(rowsByChannel) == 0 {
		return 0, nil
	}

	var changed int
	err := db.WithWrite(func(tx *sql.Tx) error {
		if err := suppressConflictingTwitterSeedRowsTx(tx, rowsByChannel); err != nil {
			return err
		}
		driftRows, err := markTwitterProfileDriftDueTx(tx, rowsByChannel)
		if err != nil {
			return err
		}
		changed += driftRows
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
					     AND (
					          LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
					          OR COALESCE(excluded.avatar_url, '') != ''
					     )
						THEN excluded.avatar_url
					ELSE COALESCE(NULLIF(channel_profiles.avatar_url, ''), excluded.avatar_url)
				END,
				fetched_at = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
					     AND (
					          LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
					          OR COALESCE(excluded.avatar_url, '') != ''
					     )
						THEN 0
					ELSE channel_profiles.fetched_at
				END,
				fail_count = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
					     AND (
					          LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
					          OR COALESCE(excluded.avatar_url, '') != ''
					     )
						THEN 0
					ELSE channel_profiles.fail_count
				END,
				next_retry_at = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
					     AND (
					          LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
					          OR COALESCE(excluded.avatar_url, '') != ''
					     )
						THEN 0
					ELSE channel_profiles.next_retry_at
				END,
				tombstone = 0
			WHERE channel_profiles.platform != excluded.platform
			   OR (COALESCE(channel_profiles.handle, '') = '' AND COALESCE(excluded.handle, '') != '')
			   OR (COALESCE(channel_profiles.display_name, '') = '' AND COALESCE(excluded.display_name, '') != '')
			   OR (COALESCE(channel_profiles.avatar_url, '') = '' AND COALESCE(excluded.avatar_url, '') != '')
			   OR COALESCE(channel_profiles.tombstone, 0) != 0
			   OR (channel_profiles.platform = 'twitter'
			       AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
			       AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
			       AND (
			            LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
			            OR COALESCE(excluded.avatar_url, '') != ''
			       ))
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = stmt.Close()
		}()
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

func feedItemIdentitySeenAtMs(item model.FeedItem) int64 {
	var seenAtMs int64
	if item.PublishedAt != nil {
		seenAtMs = item.PublishedAt.UnixMilli()
	}
	if !item.FetchedAt.IsZero() && item.FetchedAt.UnixMilli() > seenAtMs {
		seenAtMs = item.FetchedAt.UnixMilli()
	}
	return seenAtMs
}

func suppressConflictingTwitterSeedRowsTx(tx *sql.Tx, rowsByChannel map[string]twitterProfileSeedRow) error {
	if len(rowsByChannel) == 0 {
		return nil
	}
	avatarToChannels := make(map[string][]string)
	for channelID, row := range rowsByChannel {
		if row.avatarURL == "" {
			continue
		}
		key := model.NormalizeTwitterAvatarURL(row.avatarURL)
		if key == "" {
			continue
		}
		avatarToChannels[key] = append(avatarToChannels[key], channelID)
	}
	if len(avatarToChannels) == 0 {
		return nil
	}
	keys := make([]string, 0, len(avatarToChannels))
	for key := range avatarToChannels {
		keys = append(keys, key)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		args = append(args, key)
	}
	dbRows, err := tx.Query(`
		SELECT LOWER(avatar_url), channel_id
		FROM channel_profiles
		WHERE COALESCE(tombstone, 0) = 0
		  AND COALESCE(avatar_url, '') != ''
		  AND LOWER(avatar_url) IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return err
	}
	defer func() {
		_ = dbRows.Close()
	}()
	for dbRows.Next() {
		var avatarURL, ownerChannelID string
		if err := dbRows.Scan(&avatarURL, &ownerChannelID); err != nil {
			return err
		}
		for _, channelID := range avatarToChannels[avatarURL] {
			if strings.EqualFold(channelID, ownerChannelID) {
				continue
			}
			row := rowsByChannel[channelID]
			row.displayName = ""
			row.avatarURL = ""
			rowsByChannel[channelID] = row
		}
	}
	return dbRows.Err()
}

func markTwitterProfileDriftDueTx(tx *sql.Tx, rowsByChannel map[string]twitterProfileSeedRow) (int, error) {
	if len(rowsByChannel) == 0 {
		return 0, nil
	}
	stmt, err := tx.Prepare(`
		SELECT COALESCE(display_name, ''), COALESCE(avatar_url, ''), fetched_at
		FROM channel_profiles
		WHERE channel_id = ?
		  AND platform = 'twitter'
		  AND COALESCE(tombstone, 0) = 0
	`)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = stmt.Close()
	}()
	updateStmt, err := tx.Prepare(`
		UPDATE channel_profiles
		SET display_name = CASE WHEN ? THEN ? ELSE display_name END,
		    avatar_url = CASE WHEN ? THEN ? ELSE avatar_url END,
		    fetched_at = 0,
		    fail_count = 0,
		    next_retry_at = 0,
		    tombstone = 0
		WHERE channel_id = ?
		  AND COALESCE(tombstone, 0) = 0
	`)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = updateStmt.Close()
	}()
	var changed int
	for channelID, row := range rowsByChannel {
		if row.seenAtMs <= 0 || (row.displayName == "" && row.avatarURL == "") {
			continue
		}
		var displayName, avatarURL string
		var fetchedAt int64
		err := stmt.QueryRow(channelID).Scan(&displayName, &avatarURL, &fetchedAt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return changed, err
		}
		if row.seenAtMs <= fetchedAt {
			continue
		}
		displayDrift := row.displayName != "" && displayName != "" && !strings.EqualFold(strings.TrimSpace(row.displayName), strings.TrimSpace(displayName))
		avatarDrift := row.avatarURL != "" && avatarURL != "" && model.NormalizeTwitterAvatarURL(row.avatarURL) != model.NormalizeTwitterAvatarURL(avatarURL)
		if !displayDrift && !avatarDrift {
			continue
		}
		res, err := updateStmt.Exec(displayDrift, row.displayName, avatarDrift, row.avatarURL, channelID)
		if err != nil {
			return changed, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed += int(n)
		}
	}
	return changed, nil
}

// MarkTwitterProfileDriftDueFromFeedRows clears freshness for Twitter profiles
// whose newest stored feed identity is newer than the profile fetch and
// disagrees on display name or avatar. The newer feed identity replaces only
// the contradicted visible fields while the profile worker owns the follow-up
// canonical refresh.
func (db *DB) MarkTwitterProfileDriftDueFromFeedRows(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	scanLimit := limit * 100
	if scanLimit < limit {
		scanLimit = limit
	}
	rows, err := db.conn.Query(`
		SELECT
			LOWER(author_handle) AS handle,
			COALESCE(author_display_name, '') AS display_name,
			CASE
				WHEN LOWER(COALESCE(author_avatar_url, '')) LIKE '%pbs.twimg.com/profile_images/%'
				THEN author_avatar_url
				ELSE ''
			END AS avatar_url,
			MAX(fetched_at, COALESCE(published_at, 0)) AS seen_at
		FROM feed_items INDEXED BY idx_feed_items_author_fetched
		WHERE author_handle IS NOT NULL
		  AND author_handle != ''
		ORDER BY fetched_at DESC, published_at DESC, tweet_id DESC
		LIMIT ?
	`, scanLimit)
	if err != nil {
		return 0, err
	}
	rowsByChannel := make(map[string]twitterProfileSeedRow)
	for rows.Next() {
		var row twitterProfileSeedRow
		if err := rows.Scan(&row.handle, &row.displayName, &row.avatarURL, &row.seenAtMs); err != nil {
			_ = rows.Close()
			return 0, err
		}
		row.handle = model.NormalizeTwitterHandle(row.handle)
		if row.handle == "" {
			continue
		}
		channelID := "twitter_" + row.handle
		if _, ok := rowsByChannel[channelID]; ok {
			continue
		}
		rowsByChannel[channelID] = row
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var changed int
	err = db.WithWrite(func(tx *sql.Tx) error {
		n, err := markTwitterProfileDriftDueTx(tx, rowsByChannel)
		if err != nil {
			return err
		}
		changed = n
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
					     AND (
					          LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
					          OR COALESCE(excluded.avatar_url, '') != ''
					     )
						THEN excluded.avatar_url
					ELSE COALESCE(NULLIF(channel_profiles.avatar_url, ''), excluded.avatar_url)
				END,
				fetched_at = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
					     AND (
					          LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
					          OR COALESCE(excluded.avatar_url, '') != ''
					     )
						THEN 0
					ELSE channel_profiles.fetched_at
				END,
				fail_count = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
					     AND (
					          LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
					          OR COALESCE(excluded.avatar_url, '') != ''
					     )
						THEN 0
					ELSE channel_profiles.fail_count
				END,
				next_retry_at = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					WHEN channel_profiles.platform = 'twitter'
					     AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
					     AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
					     AND (
					          LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
					          OR COALESCE(excluded.avatar_url, '') != ''
					     )
						THEN 0
					ELSE channel_profiles.next_retry_at
				END,
				tombstone = 0
			WHERE channel_profiles.platform != excluded.platform
			   OR (COALESCE(channel_profiles.handle, '') = '' AND COALESCE(excluded.handle, '') != '')
			   OR (COALESCE(channel_profiles.avatar_url, '') = '' AND COALESCE(excluded.avatar_url, '') != '')
			   OR COALESCE(channel_profiles.tombstone, 0) != 0
			   OR (channel_profiles.platform = 'twitter'
			       AND COALESCE(channel_profiles.avatar_url, '') LIKE 'http%'
			       AND LOWER(channel_profiles.avatar_url) NOT LIKE '%pbs.twimg.com/profile_images/%'
			       AND (
			            LOWER(channel_profiles.avatar_url) NOT LIKE '%abs.twimg.com/sticky/default_profile_images/%'
			            OR COALESCE(excluded.avatar_url, '') != ''
			       ))
		`)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		inserted = int(n)
		shortOwnerRows, err := seedShortVideoOwnerProfileRows(tx)
		if err != nil {
			return err
		}
		inserted += shortOwnerRows
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
		profileBioMentionRows, err := seedProfileBioMentionProfileRows(tx)
		if err != nil {
			return err
		}
		inserted += profileBioMentionRows
		return nil
	})
	return inserted, err
}

type mentionSeedRow struct {
	platform string
	handle   string
}

func seedShortVideoOwnerProfileRows(tx *sql.Tx) (int, error) {
	rows, err := tx.Query(`
		SELECT channel_id
		FROM videos
		WHERE channel_id LIKE 'twitter_%' OR channel_id LIKE 'tiktok_%' OR channel_id LIKE 'instagram_%'

		UNION

		SELECT channel_id
		FROM download_queue
		WHERE channel_id LIKE 'twitter_%' OR channel_id LIKE 'tiktok_%' OR channel_id LIKE 'instagram_%'
	`)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = rows.Close()
	}()

	byChannelID := map[string]mentionSeedRow{}
	for rows.Next() {
		var rawChannelID string
		if err := rows.Scan(&rawChannelID); err != nil {
			return 0, err
		}
		channelID, platform, handle := shortOwnerProfileSeedFromChannelID(rawChannelID)
		if channelID == "" {
			continue
		}
		byChannelID[channelID] = mentionSeedRow{platform: platform, handle: handle}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(byChannelID) == 0 {
		return 0, nil
	}
	return upsertVisibleIdentitySeedRows(tx, byChannelID)
}

func shortOwnerProfileSeedFromChannelID(raw string) (channelID, platform, handle string) {
	if handle, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(raw)), "twitter_"); ok {
		handle = model.NormalizeTwitterHandle(handle)
		if handle != "" {
			return "twitter_" + handle, "twitter", handle
		}
	}
	if handle := model.TikTokHandleFromChannelID(raw); handle != "" {
		channelID := model.TikTokChannelIDFromHandle(handle)
		if channelID != "" {
			return channelID, "tiktok", handle
		}
	}
	if handle := model.InstagramHandleFromChannelID(raw); handle != "" {
		channelID := model.InstagramChannelIDFromHandle(handle)
		if channelID != "" {
			return channelID, "instagram", handle
		}
	}
	return "", "", ""
}

func seedShortDescriptionMentionProfileRows(tx *sql.Tx) (int, error) {
	rows, err := tx.Query(`
		SELECT channel_id, COALESCE(title, ''), COALESCE(description, '')
		FROM videos
		WHERE (channel_id LIKE 'twitter_%' OR channel_id LIKE 'tiktok_%' OR channel_id LIKE 'instagram_%')
		  AND (title LIKE '%@%' OR description LIKE '%@%')

		UNION ALL

		SELECT channel_id, COALESCE(title, ''), ''
		FROM download_queue
		WHERE (channel_id LIKE 'twitter_%' OR channel_id LIKE 'tiktok_%' OR channel_id LIKE 'instagram_%')
		  AND title LIKE '%@%'
	`)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = rows.Close()
	}()

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
		addShortFormMentionSeedRows(byChannelID, platform, title, description)
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

func (db *DB) SeedShortFormMentionProfileRowsForTexts(platform string, texts []string) (int, error) {
	_, inserted, err := db.SeedShortFormMentionProfileRowsForTextsWithIDs(platform, texts)
	return inserted, err
}

func (db *DB) SeedShortFormMentionProfileRowsForTextsWithIDs(platform string, texts []string) ([]string, int, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform != "twitter" && platform != "tiktok" && platform != "instagram" {
		return nil, 0, nil
	}
	byChannelID := map[string]mentionSeedRow{}
	addShortFormMentionSeedRows(byChannelID, platform, texts...)
	if len(byChannelID) == 0 {
		return nil, 0, nil
	}
	ids := make([]string, 0, len(byChannelID))
	for channelID := range byChannelID {
		ids = append(ids, channelID)
	}
	inserted, err := db.seedMentionProfileRows(byChannelID)
	return ids, inserted, err
}

// SeedProfileBioMentionProfileRows inserts lightweight profile rows for
// @mentions discovered inside an already-fetched profile bio.
func (db *DB) SeedProfileBioMentionProfileRows(p model.ChannelProfile) ([]string, error) {
	byChannelID := profileBioMentionSeedRows(p)
	if len(byChannelID) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(byChannelID))
	for channelID := range byChannelID {
		ids = append(ids, channelID)
	}
	_, err := db.seedMentionProfileRows(byChannelID)
	return ids, err
}

func seedProfileBioMentionProfileRows(tx *sql.Tx) (int, error) {
	rows, err := tx.Query(`
		SELECT channel_id, platform, COALESCE(bio, '')
		FROM channel_profiles
		WHERE platform IN ('twitter', 'tiktok', 'instagram')
		  AND bio LIKE '%@%'
		  AND COALESCE(tombstone, 0) = 0
	`)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = rows.Close()
	}()

	byChannelID := map[string]mentionSeedRow{}
	for rows.Next() {
		var p model.ChannelProfile
		if err := rows.Scan(&p.ChannelID, &p.Platform, &p.Bio); err != nil {
			return 0, err
		}
		for channelID, seed := range profileBioMentionSeedRows(p) {
			byChannelID[channelID] = seed
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(byChannelID) == 0 {
		return 0, nil
	}
	return upsertMentionSeedRows(tx, byChannelID)
}

func profileBioMentionSeedRows(p model.ChannelProfile) map[string]mentionSeedRow {
	platform := profileBioMentionPlatform(p.Platform)
	if platform == "" || strings.TrimSpace(p.Bio) == "" {
		return nil
	}
	byChannelID := map[string]mentionSeedRow{}
	addShortFormMentionSeedRows(byChannelID, platform, p.Bio)
	return byChannelID
}

func profileBioMentionPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "twitter", "x":
		return "twitter"
	case "tiktok":
		return "tiktok"
	case "instagram":
		return "instagram"
	default:
		return ""
	}
}

func addShortFormMentionSeedRows(byChannelID map[string]mentionSeedRow, platform string, texts ...string) {
	for _, text := range texts {
		for _, handle := range shortDescriptionMentionHandles(text, platform) {
			channelID := shortMentionChannelID(platform, handle)
			if channelID == "" {
				continue
			}
			byChannelID[channelID] = mentionSeedRow{platform: platform, handle: handle}
		}
	}
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
	defer func() {
		_ = rows.Close()
	}()

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

func (db *DB) seedMentionProfileRows(byChannelID map[string]mentionSeedRow) (int, error) {
	inserted := 0
	err := db.WithWrite(func(tx *sql.Tx) error {
		n, err := upsertMentionSeedRows(tx, byChannelID)
		inserted = n
		return err
	})
	return inserted, err
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

func upsertVisibleIdentitySeedRows(tx *sql.Tx, byChannelID map[string]mentionSeedRow) (int, error) {
	inserted := 0
	for channelID, row := range byChannelID {
		res, err := tx.Exec(`
			INSERT INTO channel_profiles (channel_id, platform, handle)
			VALUES (?, ?, ?)
			ON CONFLICT(channel_id) DO UPDATE SET
				platform = excluded.platform,
				handle = COALESCE(NULLIF(channel_profiles.handle, ''), excluded.handle),
				fetched_at = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					ELSE channel_profiles.fetched_at
				END,
				fail_count = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					ELSE channel_profiles.fail_count
				END,
				next_retry_at = CASE
					WHEN COALESCE(channel_profiles.tombstone, 0) != 0
						THEN 0
					ELSE channel_profiles.next_retry_at
				END,
				tombstone = 0
			WHERE channel_profiles.platform != excluded.platform
			   OR (COALESCE(channel_profiles.handle, '') = '' AND COALESCE(excluded.handle, '') != '')
			   OR COALESCE(channel_profiles.tombstone, 0) != 0
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
	case strings.HasPrefix(channelID, "twitter_"):
		return "twitter"
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
	defer func() {
		_ = rows.Close()
	}()

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
