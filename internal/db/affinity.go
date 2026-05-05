package db

import "database/sql"

// AffinityRow holds a single affinity score with timing info.
type AffinityRow struct {
	Score       float64
	LastEventMs int64
	EventCount  int
}

// GetAccountAffinityScores reads from feed_account_affinity + feed_share_account_affinity.
// Returns combined scores per handle (state scores from likes/bookmarks + decay scores from shares).
func (db *DB) GetAccountAffinityScores(username string, handles []string) (map[string]AffinityRow, error) {
	if len(handles) == 0 {
		return nil, nil
	}
	result := make(map[string]AffinityRow)
	db.queryAffinityTable("feed_account_affinity", "handle", username, handles, result)
	db.queryAffinityTable("feed_share_account_affinity", "handle", username, handles, result)
	return result, nil
}

// GetTokenAffinityScores reads from feed_token_affinity + feed_share_token_affinity.
func (db *DB) GetTokenAffinityScores(username string, tokens []string) (map[string]AffinityRow, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	result := make(map[string]AffinityRow)
	db.queryAffinityTable("feed_token_affinity", "token", username, tokens, result)
	db.queryAffinityTable("feed_share_token_affinity", "token", username, tokens, result)
	return result, nil
}

// queryAffinityTable is a helper that queries one of the 4 affinity tables
// and accumulates scores into the result map.
func (db *DB) queryAffinityTable(table, keyCol, username string, keys []string, result map[string]AffinityRow) {
	placeholders := make([]byte, 0, len(keys)*2)
	args := make([]any, 0, len(keys)+1)
	args = append(args, username)
	for i, k := range keys {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, k)
	}

	query := "SELECT " + keyCol + ", COALESCE(score,0), COALESCE(last_event_at_ms,0), COALESCE(event_count,0) " +
		"FROM " + table + " WHERE username = ? AND " + keyCol + " IN (" + string(placeholders) + ")"

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return // Table may not exist — graceful fallback
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var row AffinityRow
		if err := rows.Scan(&key, &row.Score, &row.LastEventMs, &row.EventCount); err != nil {
			continue
		}
		existing := result[key]
		existing.Score += row.Score
		if row.LastEventMs > existing.LastEventMs {
			existing.LastEventMs = row.LastEventMs
		}
		existing.EventCount += row.EventCount
		result[key] = existing
	}
}

// UpsertShareAccountAffinity updates the share-based account affinity score.
func (db *DB) UpsertShareAccountAffinity(username, handle string, scoreDelta float64, eventAtMs int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO feed_share_account_affinity (username, handle, score, last_event_at_ms, event_count)
			VALUES (?, ?, ?, ?, 1)
			ON CONFLICT(username, handle) DO UPDATE SET
				score = feed_share_account_affinity.score + excluded.score,
				last_event_at_ms = MAX(feed_share_account_affinity.last_event_at_ms, excluded.last_event_at_ms),
				event_count = feed_share_account_affinity.event_count + 1
		`, username, handle, scoreDelta, eventAtMs)
		return err
	})
}

// UpsertShareTokenAffinity updates the share-based token affinity score.
func (db *DB) UpsertShareTokenAffinity(username, token string, scoreDelta float64, eventAtMs int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO feed_share_token_affinity (username, token, score, last_event_at_ms, event_count)
			VALUES (?, ?, ?, ?, 1)
			ON CONFLICT(username, token) DO UPDATE SET
				score = feed_share_token_affinity.score + excluded.score,
				last_event_at_ms = MAX(feed_share_token_affinity.last_event_at_ms, excluded.last_event_at_ms),
				event_count = feed_share_token_affinity.event_count + 1
		`, username, token, scoreDelta, eventAtMs)
		return err
	})
}

// PruneShareTokenAffinity keeps only the top N token affinities for a user.
func (db *DB) PruneShareTokenAffinity(username string, keepTop int) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			DELETE FROM feed_share_token_affinity
			WHERE username = ? AND token NOT IN (
				SELECT token FROM feed_share_token_affinity
				WHERE username = ?
				ORDER BY score DESC
				LIMIT ?
			)
		`, username, username, keepTop)
		return err
	})
}

// BuildStateAccountScores builds retroactive account interest scores from current likes + bookmarks.
func (db *DB) BuildStateAccountScores(username string) (map[string]float64, error) {
	accountScores := make(map[string]float64)

	rows, err := db.conn.Query(`
		SELECT LOWER(COALESCE(fl.author_handle, fl.source_handle)), COUNT(*)
		FROM feed_likes fl
		WHERE fl.username = ?
		  AND COALESCE(fl.author_handle, fl.source_handle) IS NOT NULL
		  AND COALESCE(fl.author_handle, fl.source_handle) <> ''
		GROUP BY LOWER(COALESCE(fl.author_handle, fl.source_handle))
	`, username)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var handle string
			var count float64
			rows.Scan(&handle, &count)
			accountScores[handle] += count
		}
	}

	bRows, err := db.conn.Query(`
		SELECT LOWER(COALESCE(fi.source_handle, fi.author_handle)), COUNT(*)
		FROM bookmarks b
		JOIN feed_items fi ON b.video_id = fi.tweet_id
		WHERE b.user_id = ?
		  AND COALESCE(fi.source_handle, fi.author_handle) IS NOT NULL
		  AND COALESCE(fi.source_handle, fi.author_handle) <> ''
		GROUP BY LOWER(COALESCE(fi.source_handle, fi.author_handle))
	`, username)
	if err == nil {
		defer bRows.Close()
		for bRows.Next() {
			var handle string
			var count float64
			bRows.Scan(&handle, &count)
			accountScores[handle] += count * 2
		}
	}

	return accountScores, nil
}

// FindSiblingTweetIDsForLikes finds sibling tweet IDs (same content hash)
// for retweet->original like propagation.
func (db *DB) FindSiblingTweetIDsForLikes(tweetIDs []string) (map[string][]string, error) {
	if len(tweetIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]byte, 0, len(tweetIDs)*2)
	args := make([]any, 0, len(tweetIDs))
	for i, id := range tweetIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, id)
	}

	hashRows, err := db.conn.Query(
		"SELECT tweet_id, content_hash FROM feed_items WHERE tweet_id IN ("+string(placeholders)+") AND content_hash IS NOT NULL AND content_hash <> ''",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer hashRows.Close()

	tweetToHash := make(map[string]string)
	hashSet := make(map[string]bool)
	for hashRows.Next() {
		var tid, hash string
		hashRows.Scan(&tid, &hash)
		tweetToHash[tid] = hash
		hashSet[hash] = true
	}

	if len(hashSet) == 0 {
		return nil, nil
	}

	hashArgs := make([]any, 0, len(hashSet))
	hashPH := make([]byte, 0, len(hashSet)*2)
	first := true
	for h := range hashSet {
		if !first {
			hashPH = append(hashPH, ',')
		}
		hashPH = append(hashPH, '?')
		hashArgs = append(hashArgs, h)
		first = false
	}

	sibRows, err := db.conn.Query(
		"SELECT tweet_id, content_hash FROM feed_items WHERE content_hash IN ("+string(hashPH)+")",
		hashArgs...,
	)
	if err != nil {
		return nil, err
	}
	defer sibRows.Close()

	hashToTweets := make(map[string][]string)
	for sibRows.Next() {
		var tid, hash string
		sibRows.Scan(&tid, &hash)
		hashToTweets[hash] = append(hashToTweets[hash], tid)
	}

	result := make(map[string][]string)
	for tid, hash := range tweetToHash {
		siblings := hashToTweets[hash]
		if len(siblings) > 1 {
			var others []string
			for _, s := range siblings {
				if s != tid {
					others = append(others, s)
				}
			}
			if len(others) > 0 {
				result[tid] = others
			}
		}
	}
	return result, nil
}
