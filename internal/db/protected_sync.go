package db

import (
	"database/sql"
	"strings"
	"time"
)

func (db *DB) bumpFeedItemSyncSeqTx(tx *sql.Tx, tweetID string) error {
	if strings.TrimSpace(tweetID) == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE feed_items SET sync_seq = ? WHERE tweet_id = ?`, db.NextSyncSeq(), tweetID)
	return err
}

func (db *DB) bumpFeedItemAndSiblingsSyncSeqTx(tx *sql.Tx, tweetID string) error {
	if strings.TrimSpace(tweetID) == "" {
		return nil
	}
	rows, err := tx.Query(`
		WITH target_hash AS (
			SELECT NULLIF(TRIM(COALESCE(content_hash, '')), '') AS content_hash
			  FROM feed_items
			 WHERE tweet_id = ?
		)
		SELECT tweet_id
		  FROM feed_items
		 WHERE tweet_id = ?
		    OR (
		        NULLIF(TRIM(COALESCE(content_hash, '')), '') IS NOT NULL
		        AND content_hash IN (SELECT content_hash FROM target_hash WHERE content_hash IS NOT NULL)
		    )
		 ORDER BY tweet_id
	`, tweetID, tweetID)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE feed_items SET sync_seq = ? WHERE tweet_id = ?`, db.NextSyncSeq(), id); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) bumpVideoSyncSeqTx(tx *sql.Tx, videoID string) error {
	if strings.TrimSpace(videoID) == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE videos SET sync_seq = ? WHERE video_id = ?`, db.NextSyncSeq(), videoID)
	return err
}

func (db *DB) bumpBookmarkTargetSyncSeqTx(tx *sql.Tx, videoID string) error {
	if err := db.bumpFeedItemAndSiblingsSyncSeqTx(tx, videoID); err != nil {
		return err
	}
	return db.bumpVideoSyncSeqTx(tx, videoID)
}

func twitterChannelID(handle string) string {
	handle = strings.TrimSpace(strings.ToLower(handle))
	if handle == "" {
		return "twitter_"
	}
	return "twitter_" + handle
}

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
	publishedAtMs := parseTimestampString(fields["published_at"])
	if publishedAtMs == 0 {
		publishedAtMs = twitterSnowflakeMillis(tweetID)
	}
	if publishedAtMs == 0 {
		publishedAtMs = time.Now().UnixMilli()
	}
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO feed_items (
			tweet_id, source_handle, author_handle, author_display_name, author_avatar_url,
			body_text, media_json, canonical_url, published_at, fetched_at, sync_seq
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		tweetID,
		nilIfEmpty(fields["source_handle"]),
		authorHandle,
		nilIfEmpty(fields["author_display_name"]),
		nilIfEmpty(fields["avatar_url"]),
		nilIfEmpty(fields["body_text"]),
		nilIfEmpty(fields["media_json"]),
		nilIfEmpty(nonEmpty(fields["canonical_x_link"], fields["link"])),
		publishedAtMs,
		time.Now().UnixMilli(),
		db.NextSyncSeq(),
	)
	return err
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

	var authorHandle, authorName, avatarURL, bodyText, mediaJSON string
	var publishedAt int64
	err := tx.QueryRow(`
		SELECT
			COALESCE(NULLIF(fi.quote_author_handle, ''), ''),
			COALESCE(NULLIF(fi.quote_author_display_name, ''), ''),
			COALESCE(NULLIF(fi.quote_author_avatar_url, ''), ''),
			COALESCE(NULLIF(fi.quote_body_text, ''), ''),
			COALESCE(NULLIF(fi.quote_media_json, ''), ''),
			COALESCE(fi.quote_published_at, fi.published_at, 0)
		FROM feed_items fi
		WHERE fi.quote_tweet_id = ?
		ORDER BY COALESCE(fi.sync_seq, 0) DESC
		LIMIT 1
	`, videoID).Scan(&authorHandle, &authorName, &avatarURL, &bodyText, &mediaJSON, &publishedAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if authorHandle == "" {
		authorHandle = "unknown"
	}
	if publishedAt == 0 {
		publishedAt = twitterSnowflakeMillis(videoID)
	}
	if publishedAt == 0 {
		publishedAt = time.Now().UnixMilli()
	}
	_, err = tx.Exec(`
		INSERT OR IGNORE INTO feed_items (
			tweet_id, author_handle, author_display_name, author_avatar_url,
			body_text, media_json, published_at, fetched_at, sync_seq
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		videoID,
		authorHandle,
		nilIfEmpty(authorName),
		nilIfEmpty(avatarURL),
		nilIfEmpty(bodyText),
		nilIfEmpty(mediaJSON),
		publishedAt,
		time.Now().UnixMilli(),
		db.NextSyncSeq(),
	)
	return err
}

func (db *DB) ensureBookmarkTargetStubsTx(tx *sql.Tx, videoID string) error {
	if strings.TrimSpace(videoID) == "" {
		return nil
	}
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO videos (video_id, channel_id, title, duration, file_path)
		SELECT
			?,
			COALESCE(
				'twitter_' || LOWER(NULLIF(
					COALESCE(direct.author_handle, quoted.quote_author_handle),
				'')),
				'twitter_'
			),
			'X post ' || ?,
			0,
			''
		FROM (SELECT 1) _
		LEFT JOIN feed_items direct ON direct.tweet_id = ? AND direct.author_handle != ''
		LEFT JOIN feed_items quoted ON quoted.quote_tweet_id = ? AND quoted.quote_author_handle != ''
	`, videoID, videoID, videoID, videoID)
	if err != nil {
		return err
	}
	return db.ensureFeedItemStubFromBookmarkTx(tx, videoID)
}

// CountProtectedFeedItemStubCandidates returns how many distinct protected
// Twitter rows would be materialized by EnsureProtectedFeedItemStubs.
func (db *DB) CountProtectedFeedItemStubCandidates() (int, error) {
	var count int
	err := db.conn.QueryRow(`
		WITH candidates AS (
			SELECT l.tweet_id AS tweet_id
			FROM feed_likes l
			LEFT JOIN feed_items fi ON fi.tweet_id = l.tweet_id
			WHERE fi.tweet_id IS NULL

			UNION

			SELECT b.video_id AS tweet_id
			FROM bookmarks b
			JOIN feed_items fi ON fi.quote_tweet_id = b.video_id
			LEFT JOIN feed_items direct ON direct.tweet_id = b.video_id
			WHERE direct.tweet_id IS NULL
		)
		SELECT COUNT(*) FROM candidates
	`).Scan(&count)
	return count, err
}

// EnsureProtectedFeedItemStubs creates direct feed_items rows for protected Twitter
// content that only exists indirectly today: likes imported without a feed_items row,
// and bookmarks targeting quote tweets. These stubs let Android receive the rows through
// the normal feed delta instead of inventing a second protected-content channel.
func (db *DB) EnsureProtectedFeedItemStubs() (int, error) {
	var created int
	err := db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()

		likeRows, err := tx.Query(`
			SELECT
				l.tweet_id,
				COALESCE(NULLIF(l.source_handle, ''), ''),
				COALESCE(NULLIF(l.author_handle, ''), ''),
				COALESCE(NULLIF(l.author_display_name, ''), ''),
				COALESCE(NULLIF(l.avatar_url, ''), ''),
				COALESCE(NULLIF(l.body_text, ''), ''),
				COALESCE(NULLIF(l.media_json, ''), ''),
				COALESCE(NULLIF(l.canonical_x_link, ''), NULLIF(l.link, ''), ''),
				COALESCE(l.published_at, l.liked_at, 0)
			FROM feed_likes l
			LEFT JOIN feed_items fi ON fi.tweet_id = l.tweet_id
			WHERE fi.tweet_id IS NULL
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = likeRows.Close()
		}()
		for likeRows.Next() {
			var tweetID, sourceHandle, authorHandle, authorName, avatarURL, bodyText, mediaJSON, canonicalURL string
			var publishedAt int64
			if err := likeRows.Scan(&tweetID, &sourceHandle, &authorHandle, &authorName, &avatarURL, &bodyText, &mediaJSON, &canonicalURL, &publishedAt); err != nil {
				return err
			}
			if authorHandle == "" {
				authorHandle = nonEmpty(sourceHandle, "unknown")
			}
			if publishedAt == 0 {
				publishedAt = twitterSnowflakeMillis(tweetID)
			}
			if publishedAt == 0 {
				publishedAt = nowMs
			}
			res, err := tx.Exec(`
				INSERT OR IGNORE INTO feed_items (
					tweet_id, source_handle, author_handle, author_display_name, author_avatar_url,
					body_text, media_json, canonical_url, published_at, fetched_at, sync_seq
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`,
				tweetID,
				nilIfEmpty(sourceHandle),
				authorHandle,
				nilIfEmpty(authorName),
				nilIfEmpty(avatarURL),
				nilIfEmpty(bodyText),
				nilIfEmpty(mediaJSON),
				nilIfEmpty(canonicalURL),
				publishedAt,
				nowMs,
				db.NextSyncSeq(),
			)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				created++
			}
		}
		if err := likeRows.Err(); err != nil {
			return err
		}

		bookmarkRows, err := tx.Query(`
			SELECT DISTINCT
				b.video_id,
				COALESCE(NULLIF(fi.quote_author_handle, ''), ''),
				COALESCE(NULLIF(fi.quote_author_display_name, ''), ''),
				COALESCE(NULLIF(fi.quote_author_avatar_url, ''), ''),
				COALESCE(NULLIF(fi.quote_body_text, ''), ''),
				COALESCE(NULLIF(fi.quote_media_json, ''), ''),
				COALESCE(fi.quote_published_at, fi.published_at, 0)
			FROM bookmarks b
			JOIN feed_items fi ON fi.quote_tweet_id = b.video_id
			LEFT JOIN feed_items direct ON direct.tweet_id = b.video_id
			WHERE direct.tweet_id IS NULL
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = bookmarkRows.Close()
		}()
		for bookmarkRows.Next() {
			var videoID, authorHandle, authorName, avatarURL, bodyText, mediaJSON string
			var publishedAt int64
			if err := bookmarkRows.Scan(&videoID, &authorHandle, &authorName, &avatarURL, &bodyText, &mediaJSON, &publishedAt); err != nil {
				return err
			}
			if authorHandle == "" {
				authorHandle = "unknown"
			}
			if publishedAt == 0 {
				publishedAt = twitterSnowflakeMillis(videoID)
			}
			if publishedAt == 0 {
				publishedAt = nowMs
			}
			res, err := tx.Exec(`
				INSERT OR IGNORE INTO feed_items (
					tweet_id, author_handle, author_display_name, author_avatar_url,
					body_text, media_json, published_at, fetched_at, sync_seq
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`,
				videoID,
				authorHandle,
				nilIfEmpty(authorName),
				nilIfEmpty(avatarURL),
				nilIfEmpty(bodyText),
				nilIfEmpty(mediaJSON),
				publishedAt,
				nowMs,
				db.NextSyncSeq(),
			)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				created++
			}
		}
		return bookmarkRows.Err()
	})
	return created, err
}
