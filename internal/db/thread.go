package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/screwys/igloo/internal/model"
)

// GetFeedItemByTweetID fetches a single feed_items row by tweet_id, including
// ghost rows. Returns (nil, nil) if not found. Used by the reply resolver to
// detect whether a parent already exists in DB before fetching from fxtwitter,
// and by the thread API.
func (db *DB) GetFeedItemByTweetID(tweetID string) (*model.FeedItem, error) {
	f, err := scanFeedItem(db.conn.QueryRow(`
		SELECT `+feedItemSelectSQL("feed_items")+`
		FROM feed_items_resolved AS feed_items
		WHERE tweet_id = ?
	`, tweetID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetFeedItemByTweetID: %w", err)
	}
	return &f, nil
}

// UpsertGhostFeedItem stores a single feed_items row with is_ghost=1. The row
// represents a parent tweet fetched from fxtwitter to maintain thread continuity
// — the user does not follow this account, so we don't want it polluting feed
// listings, but we need it joinable via reply_to_status.
//
// If a row with the same tweet_id already exists and is NOT a ghost (i.e., we
// follow this account and ingested it normally), the ON CONFLICT clause keeps
// is_ghost=0 — the real row wins.
func (db *DB) UpsertGhostFeedItem(item model.FeedItem) error {
	item.IsGhost = true
	_, err := db.UpsertFeedItems([]model.FeedItem{item})
	return err
}

// UpdateReplyToStatus sets reply_to_status on an existing feed_items row.
// Idempotent — calling multiple times with the same value is a no-op.
func (db *DB) UpdateReplyToStatus(tweetID, parentTweetID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`UPDATE feed_items SET reply_to_status = ? WHERE tweet_id = ?`,
			parentTweetID, tweetID,
		)
		return err
	})
}

// GetThreadChain returns the conversation chain rooted at tweetID's earliest
// known ancestor and ending at tweetID, ordered root → leaf. If a parent in
// the chain is missing from the DB, the chain stops at the first orphan
// (the leaf row is always returned, even with no ancestors).
//
// Implementation uses a recursive CTE walking up via reply_to_status, then
// reverses to root → leaf order.
func (db *DB) GetThreadChain(tweetID string) ([]model.FeedItem, error) {
	const q = `
		WITH RECURSIVE chain(tweet_id, depth) AS (
			SELECT tweet_id, 0 FROM feed_items WHERE tweet_id = ?
			UNION ALL
			SELECT fi.reply_to_status, c.depth + 1
			FROM chain c
			JOIN feed_items fi ON fi.tweet_id = c.tweet_id
			WHERE fi.reply_to_status IS NOT NULL
			  AND fi.reply_to_status != ''
			  AND c.depth < 50
		)
		SELECT tweet_id FROM chain
		WHERE tweet_id IS NOT NULL AND tweet_id != ''
		ORDER BY depth DESC`

	rows, err := db.conn.Query(q, tweetID)
	if err != nil {
		return nil, fmt.Errorf("GetThreadChain query: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]model.FeedItem, 0, len(ids))
	for _, id := range ids {
		fi, err := db.GetFeedItemByTweetID(id)
		if err != nil {
			return nil, err
		}
		if fi != nil {
			out = append(out, *fi)
		}
	}
	return out, nil
}

// GetThreadTree returns the earliest known ancestor for tweetID followed by
// every stored descendant reply. Items are ordered as a pre-order reply tree:
// root, first direct reply and its descendants, then the next direct reply.
func (db *DB) GetThreadTree(tweetID string) ([]model.FeedItem, error) {
	chain, err := db.GetThreadChain(tweetID)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return nil, nil
	}
	rootID := chain[0].TweetID

	const q = `
		WITH RECURSIVE subtree(tweet_id, parent_id, depth, published_at) AS (
			SELECT tweet_id, '', 0, COALESCE(published_at, 0)
			FROM feed_items
			WHERE tweet_id = ?
			UNION ALL
			SELECT child.tweet_id, child.reply_to_status, subtree.depth + 1, COALESCE(child.published_at, 0)
			FROM feed_items child
			JOIN subtree ON child.reply_to_status = subtree.tweet_id
			WHERE subtree.depth < 50
		)
		SELECT tweet_id, COALESCE(parent_id, ''), depth, published_at
		FROM subtree
		WHERE tweet_id IS NOT NULL AND tweet_id != ''`

	rows, err := db.conn.Query(q, rootID)
	if err != nil {
		return nil, fmt.Errorf("GetThreadTree query: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	type node struct {
		tweetID     string
		parentID    string
		depth       int
		publishedAt int64
	}
	nodes := make(map[string]node)
	children := make(map[string][]node)
	var ids []string
	for rows.Next() {
		var n node
		if err := rows.Scan(&n.tweetID, &n.parentID, &n.depth, &n.publishedAt); err != nil {
			return nil, err
		}
		if _, exists := nodes[n.tweetID]; exists {
			continue
		}
		nodes[n.tweetID] = n
		ids = append(ids, n.tweetID)
		if n.parentID != "" {
			children[n.parentID] = append(children[n.parentID], n)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	itemsByID, err := db.GetFeedItemsForTweetIDs(ids)
	if err != nil {
		return nil, err
	}
	for parentID := range children {
		sort.Slice(children[parentID], func(i, j int) bool {
			left := children[parentID][i]
			right := children[parentID][j]
			if left.publishedAt != right.publishedAt {
				return left.publishedAt < right.publishedAt
			}
			return left.tweetID < right.tweetID
		})
	}

	out := make([]model.FeedItem, 0, len(ids))
	visited := make(map[string]bool, len(ids))
	var walk func(string)
	walk = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		n, ok := nodes[id]
		if !ok {
			return
		}
		item, ok := itemsByID[id]
		if !ok {
			return
		}
		item.ThreadDepth = n.depth
		out = append(out, item)
		for _, child := range children[id] {
			walk(child.tweetID)
		}
	}
	walk(rootID)
	return out, nil
}

// FindUnresolvedReplies returns leaf reply rows that either still have empty
// reply_to_status or point at a parent row missing from feed_items. Limit caps
// the sweep size so a long-running outage doesn't produce a huge batch.
func (db *DB) FindUnresolvedReplies(limit int) ([]model.FeedItem, error) {
	const q = `
		SELECT f.tweet_id, COALESCE(f.author_handle, ''), COALESCE(f.reply_to_handle, ''),
		       COALESCE(f.reply_to_status, '')
		FROM feed_items_resolved f
		LEFT JOIN feed_items parent
		  ON parent.tweet_id = f.reply_to_status
		 AND COALESCE(f.reply_to_status, '') != ''
		LEFT JOIN feed_rank_snapshot fr
		  ON fr.tweet_id = f.tweet_id
		WHERE COALESCE(f.is_reply, 0) = 1
		  AND COALESCE(f.is_ghost, 0) = 0
		  AND (
		    COALESCE(f.reply_to_status, '') = ''
		    OR parent.tweet_id IS NULL
		  )
		GROUP BY f.tweet_id
		ORDER BY
		  CASE WHEN MIN(fr.rank_position) IS NULL THEN 1 ELSE 0 END,
		  MIN(fr.rank_position),
		  f.published_at DESC
		LIMIT ?`

	rows, err := db.conn.Query(q, limit)
	if err != nil {
		return nil, fmt.Errorf("FindUnresolvedReplies: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []model.FeedItem
	for rows.Next() {
		var f model.FeedItem
		if err := rows.Scan(&f.TweetID, &f.AuthorHandle, &f.ReplyToHandle, &f.ReplyToStatus); err != nil {
			return nil, err
		}
		f.IsReply = true
		out = append(out, f)
	}
	return out, rows.Err()
}
