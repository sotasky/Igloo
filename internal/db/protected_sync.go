package db

import (
	"database/sql"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func nonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (db *DB) ensureFeedItemStubFromLikeTx(tx *sql.Tx, tweetID string, fields map[string]string) error {
	if strings.TrimSpace(tweetID) == "" {
		return nil
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM feed_items WHERE tweet_id = ?`, tweetID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	authorHandle := nonEmpty(fields["author_handle"], fields["source_handle"])
	sourceChannelID := model.TwitterChannelIDFromHandle(fields["source_handle"])
	channelID := model.TwitterChannelIDFromHandle(authorHandle)
	publishedAtMs := parseTimestampString(fields["published_at"])
	if publishedAtMs == 0 {
		publishedAtMs = twitterSnowflakeMillis(tweetID)
	}
	if publishedAtMs == 0 {
		publishedAtMs = time.Now().UnixMilli()
	}
	nowMs := time.Now().UnixMilli()
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO feed_items (
			tweet_id, source_channel_id, channel_id,
			body_text, media_json, canonical_url, published_at, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		tweetID,
		nilIfEmpty(sourceChannelID),
		nilIfEmpty(channelID),
		nilIfEmpty(fields["body_text"]),
		nilIfEmpty(fields["media_json"]),
		nilIfEmpty(nonEmpty(fields["canonical_x_link"], fields["link"])),
		publishedAtMs,
		nowMs,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if channelID != "" {
		if err := observeProfileTx(tx, profileObservation{
			channelID: channelID, platform: "twitter", handle: authorHandle,
			displayName: fields["author_display_name"], avatarURL: fields["avatar_url"], observedAt: nowMs,
		}); err != nil {
			return err
		}
	}
	if sourceChannelID != "" && sourceChannelID != channelID {
		return observeProfileTx(tx, profileObservation{
			channelID: sourceChannelID, platform: "twitter", handle: fields["source_handle"], observedAt: nowMs,
		})
	}
	return nil
}

func (db *DB) ensureFeedItemStubFromBookmarkTx(tx *sql.Tx, videoID string) error {
	if strings.TrimSpace(videoID) == "" {
		return nil
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM feed_items WHERE tweet_id = ?`, videoID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	var channelID, authorHandle, authorName, avatarURL, bodyText, mediaJSON string
	var publishedAt, observedAt int64
	err := tx.QueryRow(`
		SELECT
			COALESCE(fi.quote_channel_id, ''),
			COALESCE(NULLIF(fi.quote_author_handle, ''), ''),
			COALESCE(NULLIF(fi.quote_author_display_name, ''), ''),
			COALESCE(NULLIF(fi.quote_author_avatar_url, ''), ''),
			COALESCE(NULLIF(fi.quote_body_text, ''), ''),
			COALESCE(NULLIF(fi.quote_media_json, ''), ''),
			COALESCE(fi.quote_published_at, fi.published_at, 0),
			MAX(COALESCE(fi.fetched_at, 0), COALESCE(fi.quote_published_at, 0), COALESCE(fi.published_at, 0))
		FROM feed_items_resolved fi
		WHERE fi.quote_tweet_id = ?
		ORDER BY COALESCE(fi.fetched_at, 0) DESC, fi.tweet_id DESC
		LIMIT 1
	`, videoID).Scan(&channelID, &authorHandle, &authorName, &avatarURL, &bodyText, &mediaJSON, &publishedAt, &observedAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if publishedAt == 0 {
		publishedAt = twitterSnowflakeMillis(videoID)
	}
	if publishedAt == 0 {
		publishedAt = time.Now().UnixMilli()
	}
	nowMs := time.Now().UnixMilli()
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO feed_items (
			tweet_id, channel_id,
			body_text, media_json, published_at, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`,
		videoID,
		nilIfEmpty(channelID),
		nilIfEmpty(bodyText),
		nilIfEmpty(mediaJSON),
		publishedAt,
		nowMs,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if channelID != "" {
		return observeProfileTx(tx, profileObservation{
			channelID: channelID, platform: "twitter", handle: authorHandle,
			displayName: authorName, avatarURL: avatarURL, observedAt: observedAt,
		})
	}
	return nil
}

func (db *DB) ensureBookmarkTargetStubsTx(tx *sql.Tx, videoID string) error {
	if strings.TrimSpace(videoID) == "" {
		return nil
	}
	var feedRows int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM feed_items WHERE tweet_id = ? OR quote_tweet_id = ?`, videoID, videoID).Scan(&feedRows); err != nil {
		return err
	}
	if feedRows > 0 {
		if err := requireVideoOwnerKindTx(tx, videoID, "tweet"); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO videos (video_id, channel_id, owner_kind, title, duration)
		SELECT
			?,
			COALESCE(NULLIF(direct.channel_id, ''), NULLIF(quoted.quote_channel_id, ''), ''),
			'tweet',
			'X post ' || ?,
			0
		FROM (SELECT 1) _
		LEFT JOIN feed_items direct ON direct.tweet_id = ? AND COALESCE(direct.channel_id, '') != ''
		LEFT JOIN feed_items quoted ON quoted.quote_tweet_id = ? AND COALESCE(quoted.quote_channel_id, '') != ''
	`, videoID, videoID, videoID, videoID)
	if err != nil {
		return err
	}
	return db.ensureFeedItemStubFromBookmarkTx(tx, videoID)
}
