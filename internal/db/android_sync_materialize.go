package db

import (
	"database/sql"
	"sort"
)

type AndroidSyncFeedRankProjection struct {
	TweetID      string `json:"tweet_id"`
	RankPosition int    `json:"rank_position"`
	SnapshotAt   int64  `json:"snapshot_at"`
}

type androidSyncFeedRecencyNode struct {
	parentID    string
	contentHash string
	publishedAt int64
	exists      bool
}

func (db *DB) ListAndroidSyncFeedEffectiveRecency(tweetIDs []string) (map[string]int64, error) {
	roots := uniqueStrings(tweetIDs)
	out := make(map[string]int64, len(roots))
	if len(roots) == 0 {
		return out, nil
	}
	nodes, err := db.listAndroidSyncFeedRecencyNodes(roots)
	if err != nil {
		return nil, err
	}

	contentHashes := make([]string, 0, len(nodes))
	for _, row := range nodes {
		if row.contentHash != "" {
			contentHashes = append(contentHashes, row.contentHash)
		}
	}
	hashRecency := make(map[string]int64)
	for _, chunk := range stringChunks(uniqueStrings(contentHashes), androidSyncProjectionChunkSize) {
		args := stringsToAny(chunk)
		for _, query := range []string{
			`SELECT content_hash, COALESCE(MAX(published_at), 0)
			 FROM feed_items INDEXED BY idx_feed_items_content_hash
			 WHERE content_hash IS NOT NULL AND content_hash != ''
			   AND content_hash IN (` + placeholders(len(chunk)) + `)
			 GROUP BY content_hash`,
			`SELECT content_hash, COALESCE(MAX(published_at), 0)
			 FROM retweet_sources
			 WHERE content_hash IN (` + placeholders(len(chunk)) + `)
			 GROUP BY content_hash`,
		} {
			rows, err := db.reader().Query(query, args...)
			if err != nil {
				return nil, err
			}
			if err := collectAndroidSyncRecency(rows, hashRecency); err != nil {
				return nil, err
			}
		}
	}

	quotedRecency := make(map[string]int64)
	nodeIDs := make([]string, 0, len(nodes))
	for id := range nodes {
		nodeIDs = append(nodeIDs, id)
	}
	for _, chunk := range stringChunks(uniqueStrings(nodeIDs), androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`
			SELECT quote_tweet_id, COALESCE(MAX(published_at), 0)
			FROM feed_items INDEXED BY idx_feed_items_quote
			WHERE quote_tweet_id IS NOT NULL AND quote_tweet_id != ''
			  AND quote_tweet_id IN (`+placeholders(len(chunk))+`)
			GROUP BY quote_tweet_id
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		if err := collectAndroidSyncRecency(rows, quotedRecency); err != nil {
			return nil, err
		}
	}

	baseRecency := make(map[string]int64, len(nodes))
	effectiveRecency := make(map[string]int64, len(nodes))
	for id, row := range nodes {
		value := max(row.publishedAt, hashRecency[row.contentHash], quotedRecency[id])
		baseRecency[id] = value
		effectiveRecency[id] = value
	}
	for id, value := range baseRecency {
		current := id
		for steps := 0; steps < 50; steps++ {
			parentID := nodes[current].parentID
			if parentID == "" {
				break
			}
			if _, included := nodes[parentID]; !included {
				break
			}
			if effectiveRecency[parentID] < value {
				effectiveRecency[parentID] = value
			}
			current = parentID
		}
	}
	for _, id := range roots {
		out[id] = effectiveRecency[id]
	}
	return out, nil
}

func (db *DB) listAndroidSyncFeedRecencyNodes(roots []string) (map[string]androidSyncFeedRecencyNode, error) {
	nodes := make(map[string]androidSyncFeedRecencyNode, len(roots))
	queued := make(map[string]struct{}, len(roots))
	frontier := append([]string(nil), roots...)
	for _, id := range roots {
		queued[id] = struct{}{}
		nodes[id] = androidSyncFeedRecencyNode{}
	}
	for len(frontier) > 0 {
		var childIDs []string
		for _, chunk := range stringChunks(frontier, androidSyncProjectionChunkSize) {
			rows, err := db.reader().Query(`
				SELECT tweet_id, COALESCE(reply_to_status, ''),
				       COALESCE(content_hash, ''), COALESCE(published_at, 0)
				FROM feed_items
				WHERE tweet_id IN (`+placeholders(len(chunk))+`)
			`, stringsToAny(chunk)...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				var row androidSyncFeedRecencyNode
				if err := rows.Scan(&id, &row.parentID, &row.contentHash, &row.publishedAt); err != nil {
					_ = rows.Close()
					return nil, err
				}
				row.exists = true
				nodes[id] = row
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
			rows, err = db.reader().Query(`
				SELECT tweet_id
				FROM feed_items
				WHERE reply_to_status IS NOT NULL
				  AND reply_to_status != ''
				  AND reply_to_status IN (`+placeholders(len(chunk))+`)
			`, stringsToAny(chunk)...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					_ = rows.Close()
					return nil, err
				}
				childIDs = append(childIDs, id)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
		}
		frontier = frontier[:0]
		for _, id := range uniqueStrings(childIDs) {
			if _, exists := queued[id]; exists {
				continue
			}
			queued[id] = struct{}{}
			nodes[id] = androidSyncFeedRecencyNode{}
			frontier = append(frontier, id)
		}
	}
	return nodes, nil
}

func collectAndroidSyncRecency(rows *sql.Rows, into map[string]int64) error {
	for rows.Next() {
		var key string
		var recency int64
		if err := rows.Scan(&key, &recency); err != nil {
			_ = rows.Close()
			return err
		}
		if recency > into[key] {
			into[key] = recency
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

func (db *DB) ListAndroidSyncFeedClosureIDs(leafIDs []string) ([]string, error) {
	leafIDs = uniqueStrings(leafIDs)
	if len(leafIDs) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(leafIDs))
	for _, chunk := range stringChunks(leafIDs, androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`
			WITH RECURSIVE closure(tweet_id, depth) AS (
				SELECT tweet_id, 0
				FROM feed_items
				WHERE tweet_id IN (`+placeholders(len(chunk))+`)

				UNION

				SELECT parent.tweet_id, closure.depth + 1
				FROM closure
				JOIN feed_items child ON child.tweet_id = closure.tweet_id
				JOIN feed_items parent ON parent.tweet_id = child.reply_to_status
				WHERE closure.depth < 50
			)
			SELECT DISTINCT tweet_id FROM closure
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			seen[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// ListAndroidSyncFeedHydrationIDs returns the canonical owner plus every row
// whose admission depends on it: same-hash peers, quoted targets, and reply
// ancestors for each selected row.
func (db *DB) ListAndroidSyncFeedHydrationIDs(tweetIDs []string) ([]string, error) {
	tweetIDs = uniqueStrings(tweetIDs)
	if len(tweetIDs) == 0 {
		return nil, nil
	}
	queued := make(map[string]struct{}, len(tweetIDs))
	for _, id := range tweetIDs {
		queued[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(tweetIDs))
	expandedHashes := make(map[string]struct{})
	frontier := tweetIDs
	for len(frontier) > 0 {
		var linkedIDs []string
		var contentHashes []string
		for _, chunk := range stringChunks(frontier, androidSyncProjectionChunkSize) {
			rows, err := db.reader().Query(`
				SELECT tweet_id, COALESCE(content_hash, ''),
				       COALESCE(quote_tweet_id, ''), COALESCE(reply_to_status, '')
				FROM feed_items
				WHERE tweet_id IN (`+placeholders(len(chunk))+`)
			`, stringsToAny(chunk)...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id, contentHash, quoteID, parentID string
				if err := rows.Scan(&id, &contentHash, &quoteID, &parentID); err != nil {
					_ = rows.Close()
					return nil, err
				}
				seen[id] = struct{}{}
				linkedIDs = append(linkedIDs, quoteID, parentID)
				if contentHash != "" {
					if _, expanded := expandedHashes[contentHash]; !expanded {
						expandedHashes[contentHash] = struct{}{}
						contentHashes = append(contentHashes, contentHash)
					}
				}
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
		}

		peers, err := db.ListAndroidSyncFeedIDsByContentHashes(contentHashes)
		if err != nil {
			return nil, err
		}
		linkedIDs = append(linkedIDs, peers...)
		frontier = frontier[:0]
		for _, id := range uniqueStrings(linkedIDs) {
			if _, exists := queued[id]; exists {
				continue
			}
			queued[id] = struct{}{}
			frontier = append(frontier, id)
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (db *DB) ListAndroidSyncFeedIDsByContentHashes(contentHashes []string) ([]string, error) {
	var out []string
	for _, chunk := range stringChunks(uniqueStrings(contentHashes), androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`
			SELECT tweet_id
			FROM feed_items INDEXED BY idx_feed_items_content_hash
			WHERE content_hash IS NOT NULL
			  AND content_hash != ''
			  AND content_hash IN (`+placeholders(len(chunk))+`)
			ORDER BY tweet_id
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return uniqueStrings(out), nil
}

func (db *DB) ListAndroidSyncFeedRankProjections(tweetIDs []string) (map[string]AndroidSyncFeedRankProjection, error) {
	out := make(map[string]AndroidSyncFeedRankProjection, len(tweetIDs))
	for _, chunk := range stringChunks(uniqueStrings(tweetIDs), androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`
			SELECT tweet_id, rank_position, computed_at
			FROM feed_rank_snapshot
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var row AndroidSyncFeedRankProjection
			if err := rows.Scan(&row.TweetID, &row.RankPosition, &row.SnapshotAt); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[row.TweetID] = row
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (db *DB) ListAndroidSyncAssetsByIDs(assetIDs []string) (map[string]Asset, error) {
	out := make(map[string]Asset, len(assetIDs))
	for _, chunk := range stringChunks(uniqueStrings(assetIDs), androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`SELECT `+assetProjectionSQL+assetJoinsSQL+`
			WHERE a.asset_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			asset, err := scanAsset(rows)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[asset.AssetID] = asset
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (db *DB) GetAndroidSyncAssetByID(assetID string) (*Asset, error) {
	rows, err := db.ListAndroidSyncAssetsByIDs([]string{assetID})
	if err != nil {
		return nil, err
	}
	asset, ok := rows[assetID]
	if !ok {
		return nil, nil
	}
	return &asset, nil
}

func (db *DB) GetAndroidSyncSetting(key string) (*struct {
	Key   string  `json:"key"`
	Value *string `json:"value"`
}, error) {
	var value sql.NullString
	row := struct {
		Key   string  `json:"key"`
		Value *string `json:"value"`
	}{Key: key}
	if err := db.reader().QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if value.Valid {
		row.Value = &value.String
	}
	return &row, nil
}
