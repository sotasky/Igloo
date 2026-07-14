package db

import (
	"math"
	"strings"
)

func (db *DB) listAndroidSyncDesiredFeedIDsAmong(feedDays int, nowMs int64, candidates []string) (map[string]struct{}, error) {
	candidates = uniqueStrings(candidates)
	selected := make(map[string]struct{}, len(candidates))
	if len(candidates) == 0 {
		return selected, nil
	}
	nodes, err := db.listAndroidSyncFeedRecencyNodes(candidates)
	if err != nil {
		return nil, err
	}

	contentHashes := make([]string, 0, len(nodes))
	nodeIDs := make([]string, 0, len(nodes))
	for id, node := range nodes {
		nodeIDs = append(nodeIDs, id)
		if node.contentHash != "" {
			contentHashes = append(contentHashes, node.contentHash)
		}
	}
	contentHashes = uniqueStrings(contentHashes)
	nodeIDs = uniqueStrings(nodeIDs)

	hashRecency := make(map[string]int64, len(contentHashes))
	protectedHashes := make(map[string]struct{})
	for _, chunk := range stringChunks(contentHashes, androidSyncProjectionChunkSize) {
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
			`SELECT q.content_hash, COALESCE(MAX(parent.published_at), 0)
			 FROM feed_items q INDEXED BY idx_feed_items_content_hash
			 JOIN feed_items parent INDEXED BY idx_feed_items_quote
			   ON parent.quote_tweet_id = q.tweet_id
			 WHERE q.content_hash IS NOT NULL AND q.content_hash != ''
			   AND q.content_hash IN (` + placeholders(len(chunk)) + `)
			   AND parent.quote_tweet_id IS NOT NULL AND parent.quote_tweet_id != ''
			 GROUP BY q.content_hash`,
		} {
			rows, err := db.reader().Query(query, args...)
			if err != nil {
				return nil, err
			}
			if err := collectAndroidSyncRecency(rows, hashRecency); err != nil {
				return nil, err
			}
		}
		if err := db.collectStrings(`
			SELECT DISTINCT fi.content_hash
			FROM feed_items fi INDEXED BY idx_feed_items_content_hash
			WHERE fi.content_hash IS NOT NULL AND fi.content_hash != ''
			  AND fi.content_hash IN (`+placeholders(len(chunk))+`)
			  AND (
			    EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = fi.tweet_id)
			    OR EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = fi.tweet_id)
			  )
		`, args, protectedHashes); err != nil {
			return nil, err
		}
	}

	quotedRecency := make(map[string]int64, len(nodeIDs))
	directProtected := make(map[string]struct{})
	for _, chunk := range stringChunks(nodeIDs, androidSyncProjectionChunkSize) {
		args := stringsToAny(chunk)
		rows, err := db.reader().Query(`
			SELECT quote_tweet_id, COALESCE(MAX(published_at), 0)
			FROM feed_items INDEXED BY idx_feed_items_quote
			WHERE quote_tweet_id IS NOT NULL AND quote_tweet_id != ''
			  AND quote_tweet_id IN (`+placeholders(len(chunk))+`)
			GROUP BY quote_tweet_id
		`, args...)
		if err != nil {
			return nil, err
		}
		if err := collectAndroidSyncRecency(rows, quotedRecency); err != nil {
			return nil, err
		}
		if err := db.collectStrings(`
			SELECT tweet_id FROM feed_likes
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
			UNION
			SELECT video_id FROM bookmarks
			WHERE video_id IN (`+placeholders(len(chunk))+`)
		`, append(args, args...), directProtected); err != nil {
			return nil, err
		}
	}

	cutoffMs := retentionCutoffMs(nowMs, feedDays)
	baseEligible := make(map[string]struct{}, len(nodes))
	for id, node := range nodes {
		if !node.exists {
			continue
		}
		_, direct := directProtected[id]
		_, hashProtected := protectedHashes[node.contentHash]
		if direct || hashProtected || max(node.publishedAt, hashRecency[node.contentHash], quotedRecency[id]) >= cutoffMs {
			baseEligible[id] = struct{}{}
		}
	}

	ancestorEligible := make(map[string]struct{})
	for id := range baseEligible {
		current := id
		seen := map[string]struct{}{id: {}}
		for {
			parentID := nodes[current].parentID
			if parentID == "" {
				break
			}
			parent, included := nodes[parentID]
			if !included || !parent.exists {
				break
			}
			if _, loop := seen[parentID]; loop {
				break
			}
			seen[parentID] = struct{}{}
			ancestorEligible[parentID] = struct{}{}
			current = parentID
		}
	}

	visible := make(map[string]struct{}, len(candidates))
	for _, chunk := range stringChunks(candidates, androidSyncProjectionChunkSize) {
		if err := db.collectStrings(`
			SELECT fi.tweet_id
			FROM feed_items fi
			WHERE fi.tweet_id IN (`+placeholders(len(chunk))+`)
			  AND (`+retweetFilterClause("fi")+`)
		`, stringsToAny(chunk), visible); err != nil {
			return nil, err
		}
	}
	for _, id := range candidates {
		node := nodes[id]
		if !node.exists {
			continue
		}
		if _, ok := ancestorEligible[id]; ok {
			selected[id] = struct{}{}
			continue
		}
		if _, ok := baseEligible[id]; !ok {
			continue
		}
		if _, ok := directProtected[id]; ok {
			selected[id] = struct{}{}
			continue
		}
		if _, ok := visible[id]; ok {
			selected[id] = struct{}{}
		}
	}
	return selected, nil
}

func (db *DB) ListAndroidSyncDesiredFeedAssetOwnersAmong(feedDays int, nowMs int64, candidates []string) ([]string, error) {
	candidates = uniqueStrings(candidates)
	if len(candidates) == 0 {
		return nil, nil
	}
	candidateSet := make(map[string]struct{}, len(candidates))
	for _, id := range candidates {
		candidateSet[id] = struct{}{}
	}
	parents := make(map[string]string)
	roots := append([]string(nil), candidates...)
	for _, chunk := range stringChunks(candidates, androidSyncProjectionChunkSize) {
		rows, err := db.reader().Query(`
			SELECT tweet_id, quote_tweet_id
			FROM feed_items INDEXED BY idx_feed_items_quote
			WHERE quote_tweet_id IS NOT NULL AND quote_tweet_id != ''
			  AND quote_tweet_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var parentID, quoteID string
			if err := rows.Scan(&parentID, &quoteID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			parents[parentID] = quoteID
			roots = append(roots, parentID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	selected, err := db.listAndroidSyncDesiredFeedIDsAmong(feedDays, nowMs, roots)
	if err != nil {
		return nil, err
	}
	owners := make(map[string]struct{}, len(candidates))
	for id := range selected {
		if _, ok := candidateSet[id]; ok {
			owners[id] = struct{}{}
		}
		if quoteID := strings.TrimSpace(parents[id]); quoteID != "" {
			owners[quoteID] = struct{}{}
		}
	}
	return sortedKeys(owners), nil
}

// ListAndroidSyncDesiredContentAmong applies the canonical retention rules only
// to content invalidated since the client's cursor.
func (db *DB) ListAndroidSyncDesiredContentAmong(
	settings AndroidRetentionSettings,
	nowMs int64,
	feedCandidates []string,
	videoCandidates []string,
) (AndroidSyncDesiredSets, error) {
	out := AndroidSyncDesiredSets{
		Tweets:           map[string]struct{}{},
		TweetAssetOwners: map[string]struct{}{},
		FeedRanks:        map[string]struct{}{},
		Videos:           map[string]struct{}{},
		MediaVideos:      map[string]struct{}{},
		Channels:         map[string]struct{}{},
	}
	selectedFeed, err := db.listAndroidSyncDesiredFeedIDsAmong(settings.FeedDays, nowMs, feedCandidates)
	if err != nil {
		return out, err
	}
	out.Tweets = selectedFeed
	for id := range selectedFeed {
		out.TweetAssetOwners[id] = struct{}{}
	}

	selectedVideos, err := db.listAndroidSyncDesiredVideoIDsAmong(settings, nowMs, videoCandidates)
	if err != nil {
		return out, err
	}
	out.Videos = selectedVideos
	for id := range selectedVideos {
		out.MediaVideos[id] = struct{}{}
	}
	return out, nil
}

func (db *DB) listAndroidSyncDesiredVideoIDsAmong(
	settings AndroidRetentionSettings,
	nowMs int64,
	candidates []string,
) (map[string]struct{}, error) {
	candidates = uniqueStrings(candidates)
	selected := make(map[string]struct{}, len(candidates))
	if len(candidates) == 0 {
		return selected, nil
	}
	youtubeCutoff := retentionCutoffMs(nowMs, settings.YoutubeDays)
	momentsCutoff := retentionCutoffMs(nowMs, settings.MomentsDays)
	storyCutoff := int64(math.MaxInt64)
	if settings.StoryHours > 0 {
		storyCutoff = nowMs - int64(settings.StoryHours)*3_600_000
	}
	includeMomentReposts := db.MomentsIncludeRepostsEnabled()
	includeInstagramTagged := db.InstagramIncludeTaggedEnabled()

	for _, chunk := range stringChunks(candidates, androidSyncProjectionChunkSize) {
		args := stringsToAny(chunk)
		args = append(args, youtubeCutoff, momentsCutoff, momentsCutoff, storyCutoff)
		if err := db.collectStrings(`
			SELECT v.video_id
			FROM videos v
			LEFT JOIN channels c ON c.channel_id = v.channel_id
			WHERE v.video_id IN (`+placeholders(len(chunk))+`)
			  AND (
			    (v.channel_id LIKE 'youtube_%'
			      AND COALESCE(v.published_at, 0) >= ?
			      AND EXISTS (SELECT 1 FROM channel_follows cf WHERE cf.channel_id = v.channel_id))
			    OR
			    ((v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
			      AND COALESCE(v.source_kind, '') != 'story'
			      AND COALESCE(v.published_at, 0) >= ?
			      AND EXISTS (SELECT 1 FROM channel_follows cf WHERE cf.channel_id = v.channel_id))
			    OR
			    ((v.channel_id LIKE 'youtube_%' OR v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
			      AND (EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = v.video_id)
			        OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = v.video_id)))
			    OR
			    (`+sourceWindowPlatformEnabledClause("v", includeMomentReposts, includeInstagramTagged)+`
			      AND COALESCE(v.source_kind, '') != 'story'
			      AND EXISTS (
			        SELECT 1
			        FROM video_repost_sources vrs
			        JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
			        LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
			        WHERE vrs.video_id = v.video_id
			          AND COALESCE(rcs.include_reposts, 1) != 0
			          AND COALESCE(NULLIF(vrs.reposted_at_ms, 0), NULLIF(vrs.first_seen_at_ms, 0), v.published_at, 0) >= ?
			      ))
			    OR
			    (COALESCE(c.platform, '') IN ('tiktok','instagram')
			      AND COALESCE(v.source_kind, '') = 'story'
			      AND COALESCE(v.published_at, 0) >= ?
			      AND EXISTS (SELECT 1 FROM channel_follows cf WHERE cf.channel_id = v.channel_id)
			      AND `+validStoryVideoSQL("v", "c")+`)
			  )
		`, args, selected); err != nil {
			return nil, err
		}
	}
	return selected, nil
}

func (db *DB) ListAndroidSyncDesiredFeedRanksAmong(
	feedDays int,
	nowMs int64,
	candidates []string,
	limit int,
) (map[string]struct{}, error) {
	candidates = uniqueStrings(candidates)
	selected := make(map[string]struct{}, len(candidates))
	if len(candidates) == 0 || limit <= 0 {
		return selected, nil
	}
	desiredFeed, err := db.listAndroidSyncDesiredFeedIDsAmong(feedDays, nowMs, candidates)
	if err != nil {
		return nil, err
	}
	for _, chunk := range stringChunks(sortedKeys(desiredFeed), androidSyncProjectionChunkSize) {
		args := stringsToAny(chunk)
		args = append(args, limit)
		if err := db.collectStrings(`
			SELECT s.tweet_id
			FROM feed_rank_snapshot s
			JOIN feed_items fi ON fi.tweet_id = s.tweet_id
			WHERE s.tweet_id IN (`+placeholders(len(chunk))+`)
			  AND s.rank_position <= ?
			  AND `+feedPrimaryItemPredicate("fi")+`
			  AND `+feedActiveOwnerPredicate("fi")+`
			  AND `+feedUnseenPredicate("fi")+`
		`, args, selected); err != nil {
			return nil, err
		}
	}
	return selected, nil
}
