package db

import (
	"database/sql"
	"strings"
)

func (db *DB) PruneXFeedSourceRetention(sourceID string, limit int, nowMs int64) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{Limit: 1, RetentionLimit: limit}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || limit <= 0 {
		return result, nil
	}
	nowMs = normalizeNowMs(nowMs)
	items, err := db.xFeedSourceRetentionItems(sourceID)
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
	retained, err := db.xRetainedMediaOwnerSet(nowMs, 0, candidates)
	if err != nil {
		return result, err
	}
	if err := db.deleteXFeedSourceAttribution(sourceID, pruneIDs); err != nil {
		return result, err
	}
	return db.reconcileXMediaOwnerSet(result, retained, candidates, false, nowMs)
}

func (db *DB) xFeedSourceRetentionItems(sourceID string) ([]xRetentionItem, error) {
	rows, err := db.conn.Query(`
		SELECT fis.tweet_id,
		       CASE WHEN EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = fis.tweet_id)
		              OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = fis.tweet_id)
		            THEN 1 ELSE 0 END
		FROM feed_item_sources fis
		LEFT JOIN feed_items fi ON fi.tweet_id = fis.tweet_id
		WHERE fis.source_id = ?
		ORDER BY COALESCE(NULLIF(fi.published_at, 0), fis.last_seen_at * 1000) DESC,
		         fis.tweet_id DESC
	`, sourceID)
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

func (db *DB) deleteXFeedSourceAttribution(sourceID string, tweetIDs []string) error {
	for _, chunk := range stringChunks(uniqueStrings(tweetIDs), 400) {
		if err := db.WithWrite(func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				DELETE FROM feed_item_sources
				WHERE source_id = ? AND tweet_id IN (`+placeholders(len(chunk))+`)
			`, append([]any{sourceID}, stringsToAny(chunk)...)...)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func retainedXMediaTweetIDs(items []xRetentionItem, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, min(len(items), limit))
	kept := 0
	for _, item := range items {
		if item.protected {
			out = append(out, item.tweetID)
			continue
		}
		if kept < limit {
			kept++
			out = append(out, item.tweetID)
		}
	}
	return out
}

func (db *DB) xAssetOwnerIDsForTweets(tweetIDs []string) ([]string, error) {
	owners := make(map[string]struct{})
	for _, tweetID := range uniqueStrings(tweetIDs) {
		owners[tweetID] = struct{}{}
	}
	for _, chunk := range stringChunks(sortedKeys(owners), 400) {
		rows, err := db.conn.Query(`
			SELECT COALESCE(quote_tweet_id, '')
			FROM feed_items
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var quoteID string
			if err := rows.Scan(&quoteID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if quoteID = strings.TrimSpace(quoteID); quoteID != "" {
				owners[quoteID] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return sortedKeys(owners), nil
}

func (db *DB) xPrunedAssetOwnerIDs(ownerIDs []string) ([]string, error) {
	var out []string
	for _, chunk := range stringChunks(uniqueStrings(ownerIDs), 400) {
		rows, err := db.conn.Query(`
			SELECT DISTINCT owner_id
			FROM assets
			WHERE owner_kind = 'tweet' AND lifecycle_state = 'pruned'
			  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			  AND owner_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ownerID string
			if err := rows.Scan(&ownerID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, ownerID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return uniqueStrings(out), nil
}

func (db *DB) restorePrunedXMediaOwners(ownerIDs []string, nowMs int64) (int, error) {
	ownerIDs, err := db.xPrunedAssetOwnerIDs(ownerIDs)
	if err != nil || len(ownerIDs) == 0 {
		return 0, err
	}
	restored := 0
	for _, chunk := range stringChunks(ownerIDs, 400) {
		err := db.WithWrite(func(tx *sql.Tx) error {
			res, err := tx.Exec(`
				UPDATE assets
				SET lifecycle_state = 'active', object_id = desired_object_id,
				    revision = revision + 1, updated_at_ms = ?
				WHERE owner_kind = 'tweet'
				  AND owner_id IN (`+placeholders(len(chunk))+`)
				  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
				  AND lifecycle_state = 'pruned'
			`, append([]any{nowMs}, stringsToAny(chunk)...)...)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			restored += int(n)
			if err != nil {
				return err
			}
			_, err = tx.Exec(`
				UPDATE media_objects AS mo
				SET job_state = 'queued', attempts = 0, next_attempt_at_ms = 0,
				    last_error_kind = '', last_error = '', lease_owner = '', lease_until_ms = 0,
				    updated_at_ms = ?
				WHERE mo.job_state = 'pruned'
				  AND EXISTS (
				    SELECT 1 FROM assets a
				    WHERE a.desired_object_id = mo.object_id
				      AND a.lifecycle_state = 'active'
				      AND a.owner_kind = 'tweet'
				      AND a.owner_id IN (`+placeholders(len(chunk))+`)
				      AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
				  )
			`, append([]any{nowMs}, stringsToAny(chunk)...)...)
			return err
		})
		if err != nil {
			return restored, err
		}
	}
	return restored, nil
}
