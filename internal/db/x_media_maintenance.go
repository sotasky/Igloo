package db

import (
	"database/sql"
	"strings"
	"time"
)

type DataFileRemovalResult struct {
	Considered        int   `json:"considered"`
	Removed           int   `json:"removed"`
	RemovedBytes      int64 `json:"removed_bytes"`
	StillReferenced   int   `json:"still_referenced"`
	Missing           int   `json:"missing"`
	RemoveErrors      int   `json:"remove_errors"`
	InvalidOrEmpty    int   `json:"invalid_or_empty"`
	DuplicateRequests int   `json:"duplicate_requests"`
}

type XMediaRetentionOptions struct {
	NowMs          int64 `json:"now_ms"`
	Limit          int   `json:"limit"`
	RetentionLimit int   `json:"retention_limit"`
	DryRun         bool  `json:"dry_run"`
}

type XMediaRetentionResult struct {
	DryRun             bool                  `json:"dry_run"`
	Limit              int                   `json:"limit"`
	RetentionLimit     int                   `json:"retention_limit"`
	LimitReached       bool                  `json:"limit_reached"`
	SourcesScanned     int                   `json:"sources_scanned"`
	SourcesOverLimit   int                   `json:"sources_over_limit"`
	ProtectedItems     int                   `json:"protected_items"`
	KeptItems          int                   `json:"kept_items"`
	PrunedItems        int                   `json:"pruned_items"`
	AssetsPruned       int                   `json:"assets_pruned"`
	CandidateFileBytes int64                 `json:"candidate_file_bytes"`
	FileRemoval        DataFileRemovalResult `json:"file_removal"`
}

type xRetentionSource struct {
	channelID string
	handle    string
}

type xRetentionItem struct {
	tweetID   string
	protected bool
}

func (db *DB) PruneXMediaRetentionForChannel(channelID string, opts XMediaRetentionOptions) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{DryRun: opts.DryRun, Limit: 1, RetentionLimit: opts.RetentionLimit}
	source, ok := xRetentionSourceFromChannelID(channelID)
	if !ok {
		return result, nil
	}
	limit, err := db.xRetentionLimit(source, opts.RetentionLimit)
	if err != nil || limit <= 0 {
		return result, err
	}
	result.RetentionLimit = limit
	return db.pruneXMediaRetentionSource(source, limit, opts.NowMs, result)
}

func (db *DB) PruneXMediaRetention(opts XMediaRetentionOptions) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{DryRun: opts.DryRun, Limit: opts.Limit, RetentionLimit: opts.RetentionLimit}
	sources, err := db.followedXMediaRetentionSources()
	if err != nil {
		return result, err
	}
	if opts.Limit > 0 && len(sources) > opts.Limit {
		sources = sources[:opts.Limit]
		result.LimitReached = true
	}
	for _, source := range sources {
		limit, err := db.xRetentionLimit(source, opts.RetentionLimit)
		if err != nil {
			return result, err
		}
		if limit <= 0 {
			continue
		}
		result, err = db.pruneXMediaRetentionSource(source, limit, opts.NowMs, result)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (db *DB) xRetentionLimit(source xRetentionSource, override int) (int, error) {
	if override > 0 {
		return override, nil
	}
	settings, err := db.GetChannelSettings(source.channelID)
	if err != nil || settings == nil {
		return 0, err
	}
	return settings.MediaDownloadLimit, nil
}

func (db *DB) pruneXMediaRetentionSource(source xRetentionSource, limit int, nowMs int64, result XMediaRetentionResult) (XMediaRetentionResult, error) {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	result.SourcesScanned++
	items, err := db.xMediaRetentionItems(source.channelID)
	if err != nil {
		return result, err
	}
	kept := 0
	var pruneIDs []string
	for _, item := range items {
		if item.protected {
			result.ProtectedItems++
			continue
		}
		if kept < limit {
			kept++
			result.KeptItems++
			continue
		}
		pruneIDs = append(pruneIDs, item.tweetID)
		result.PrunedItems++
	}
	if len(pruneIDs) == 0 {
		return result, nil
	}
	result.SourcesOverLimit++
	assetOwnerIDs, err := db.xPrunableAssetOwners(pruneIDs)
	if err != nil {
		return result, err
	}
	pathSizes, bytes, err := db.xContentAssetPaths(assetOwnerIDs)
	if err != nil {
		return result, err
	}
	result.CandidateFileBytes += bytes
	if result.DryRun {
		return result, nil
	}
	pruned, err := db.markXContentAssetsPruned(assetOwnerIDs, nowMs)
	if err != nil {
		return result, err
	}
	result.AssetsPruned += pruned
	for _, path := range sortedInt64Keys(pathSizes) {
		result.FileRemoval.Considered++
		removed, err := db.RemoveAssetFileIfUnreferenced(path)
		if err != nil {
			result.FileRemoval.RemoveErrors++
			continue
		}
		if removed {
			result.FileRemoval.Removed++
			result.FileRemoval.RemovedBytes += pathSizes[path]
			continue
		}
		var refs int
		if err := db.conn.QueryRow(`
			SELECT COUNT(*) FROM assets a JOIN media_objects mo ON mo.object_id = a.object_id
			WHERE a.lifecycle_state = 'active' AND mo.file_path = ? AND mo.published_revision > 0
		`, path).Scan(&refs); err == nil && refs > 0 {
			result.FileRemoval.StillReferenced++
		} else {
			result.FileRemoval.Missing++
		}
	}
	return result, nil
}

func (db *DB) followedXMediaRetentionSources() ([]xRetentionSource, error) {
	rows, err := db.conn.Query(`
		SELECT c.channel_id
		FROM channels c
		JOIN channel_follows cf ON cf.channel_id = c.channel_id
		WHERE c.platform = 'twitter' AND c.channel_id LIKE 'twitter_%'
		ORDER BY c.channel_id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []xRetentionSource
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		if source, ok := xRetentionSourceFromChannelID(channelID); ok {
			out = append(out, source)
		}
	}
	return out, rows.Err()
}

func xRetentionSourceFromChannelID(channelID string) (xRetentionSource, bool) {
	channelID = strings.TrimSpace(channelID)
	handle := strings.TrimPrefix(strings.ToLower(channelID), "twitter_")
	if handle == "" || handle == strings.ToLower(channelID) {
		return xRetentionSource{}, false
	}
	return xRetentionSource{channelID: channelID, handle: handle}, true
}

func (db *DB) xMediaRetentionItems(channelID string) ([]xRetentionItem, error) {
	rows, err := db.conn.Query(`
		SELECT fi.tweet_id,
		       CASE WHEN EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = fi.tweet_id)
		              OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = fi.tweet_id)
		            THEN 1 ELSE 0 END
		FROM feed_items fi
		WHERE COALESCE(NULLIF(fi.reposter_channel_id, ''), NULLIF(fi.source_channel_id, ''), fi.channel_id) = ?
		  AND (
		    COALESCE(fi.media_json, '') NOT IN ('', '[]')
		    OR COALESCE(fi.quote_media_json, '') NOT IN ('', '[]')
		    OR EXISTS (SELECT 1 FROM assets a WHERE a.owner_kind = 'tweet' AND a.owner_id = fi.tweet_id)
		  )
		ORDER BY COALESCE(fi.published_at, 0) DESC, fi.tweet_id DESC
	`, channelID)
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

// xPrunableAssetOwners includes quote owners only when every parent is being
// pruned, and excludes direct owners still referenced by a retained quote.
func (db *DB) xPrunableAssetOwners(tweetIDs []string) ([]string, error) {
	tweetIDs = uniqueStrings(tweetIDs)
	if len(tweetIDs) == 0 {
		return nil, nil
	}
	pruneSet := make(map[string]struct{}, len(tweetIDs))
	for _, id := range tweetIDs {
		pruneSet[id] = struct{}{}
	}
	owners := make(map[string]struct{}, len(tweetIDs))
	for _, id := range tweetIDs {
		owners[id] = struct{}{}
	}
	for _, chunk := range stringChunks(tweetIDs, 400) {
		rows, err := db.conn.Query(`
			SELECT tweet_id, COALESCE(quote_tweet_id, '')
			FROM feed_items
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
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
			if quoteID != "" {
				owners[quoteID] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	ownerIDs := sortedKeys(owners)
	for _, chunk := range stringChunks(ownerIDs, 400) {
		rows, err := db.conn.Query(`
			SELECT quote_tweet_id, tweet_id
			FROM feed_items
			WHERE quote_tweet_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ownerID, parentID string
			if err := rows.Scan(&ownerID, &parentID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, pruning := pruneSet[parentID]; !pruning {
				delete(owners, ownerID)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	for _, chunk := range stringChunks(sortedKeys(owners), 400) {
		args := stringsToAny(chunk)
		rows, err := db.conn.Query(`
			SELECT video_id
			FROM bookmarks
			WHERE video_id IN (`+placeholders(len(chunk))+`)
			UNION
			SELECT tweet_id
			FROM feed_likes
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
			UNION
			SELECT owner_id
			FROM assets
			WHERE owner_id IN (`+placeholders(len(chunk))+`)
			  AND owner_kind = 'tweet' AND required_reason = 'like'
		`, append(append(append([]any{}, args...), args...), args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var protectedOwnerID string
			if err := rows.Scan(&protectedOwnerID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			delete(owners, protectedOwnerID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return sortedKeys(owners), nil
}

func (db *DB) xContentAssetPaths(ownerIDs []string) (map[string]int64, int64, error) {
	seen := map[string]int64{}
	for _, chunk := range stringChunks(uniqueStrings(ownerIDs), 400) {
		rows, err := db.conn.Query(`
			SELECT mo.file_path, MAX(mo.size_bytes)
			FROM assets a JOIN media_objects mo ON mo.object_id = a.object_id
			WHERE a.owner_kind = 'tweet'
			  AND a.owner_id IN (`+placeholders(len(chunk))+`)
			  AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			  AND a.lifecycle_state = 'active' AND mo.published_revision > 0 AND mo.file_path != ''
			GROUP BY mo.file_path
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var path string
			var size int64
			if err := rows.Scan(&path, &size); err != nil {
				_ = rows.Close()
				return nil, 0, err
			}
			if size > seen[path] {
				seen[path] = size
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		_ = rows.Close()
	}
	var total int64
	for _, path := range sortedInt64Keys(seen) {
		total += seen[path]
	}
	return seen, total, nil
}

func (db *DB) markXContentAssetsPruned(ownerIDs []string, nowMs int64) (int, error) {
	changed := 0
	for _, chunk := range stringChunks(uniqueStrings(ownerIDs), 400) {
		err := db.WithWrite(func(tx *sql.Tx) error {
			res, err := tx.Exec(`
				UPDATE assets
				SET lifecycle_state = 'pruned', revision = revision + 1, updated_at_ms = ?
				WHERE owner_kind = 'tweet'
				  AND owner_id IN (`+placeholders(len(chunk))+`)
				  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
				  AND lifecycle_state != 'pruned'
			`, append([]any{nowMs}, stringsToAny(chunk)...)...)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			changed += int(n)
			return err
		})
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sortedInt64Keys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	keys = sortedKeys(set)
	return keys
}
