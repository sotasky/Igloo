package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DataFileRemovalResult reports reference-safe file removals.
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

// XMediaDedupeOptions controls exact-source X media dedupe.
type XMediaDedupeOptions struct {
	NowMs  int64 `json:"now_ms"`
	Limit  int   `json:"limit"`
	DryRun bool  `json:"dry_run"`
}

// XMediaDedupeResult reports exact-source X media dedupe work.
type XMediaDedupeResult struct {
	DryRun                bool                  `json:"dry_run"`
	Limit                 int                   `json:"limit"`
	LimitReached          bool                  `json:"limit_reached"`
	Groups                int                   `json:"groups"`
	Rows                  int                   `json:"rows"`
	RowsRewritten         int                   `json:"rows_rewritten"`
	AssetRowsUpdated      int                   `json:"asset_rows_updated"`
	GroupsWithoutLiveFile int                   `json:"groups_without_live_file"`
	DuplicatePaths        int                   `json:"duplicate_paths"`
	DuplicateBytes        int64                 `json:"duplicate_bytes"`
	FileRemoval           DataFileRemovalResult `json:"file_removal"`
}

// XMediaRetentionOptions controls followed-X feed media pruning.
type XMediaRetentionOptions struct {
	NowMs          int64 `json:"now_ms"`
	Limit          int   `json:"limit"`
	RetentionLimit int   `json:"retention_limit"`
	DryRun         bool  `json:"dry_run"`
}

// XMediaRetentionResult reports followed-X feed media retention work.
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
	MediaRowsDeleted   int                   `json:"media_rows_deleted"`
	AssetRowsDeleted   int                   `json:"asset_rows_deleted"`
	JobsMarkedPruned   int                   `json:"jobs_marked_pruned"`
	CandidateFileBytes int64                 `json:"candidate_file_bytes"`
	FileRemoval        DataFileRemovalResult `json:"file_removal"`
}

// AssetFileStateMaintenanceOptions controls ready-asset file-state refresh.
type AssetFileStateMaintenanceOptions struct {
	NowMs  int64 `json:"now_ms"`
	Limit  int   `json:"limit"`
	DryRun bool  `json:"dry_run"`
}

type AssetFileStateKindResult struct {
	Checked       int   `json:"checked"`
	Missing       int   `json:"missing"`
	SizeChanged   int   `json:"size_changed"`
	Updated       int   `json:"updated"`
	MissingBytes  int64 `json:"missing_bytes"`
	PreviousBytes int64 `json:"previous_bytes"`
	ActualBytes   int64 `json:"actual_bytes"`
}

// AssetFileStateMaintenanceResult reports ready-asset state drift.
type AssetFileStateMaintenanceResult struct {
	DryRun       bool                                `json:"dry_run"`
	Limit        int                                 `json:"limit"`
	LimitReached bool                                `json:"limit_reached"`
	Checked      int                                 `json:"checked"`
	Missing      int                                 `json:"missing"`
	SizeChanged  int                                 `json:"size_changed"`
	Updated      int                                 `json:"updated"`
	ByKind       map[string]AssetFileStateKindResult `json:"by_kind"`
}

type xMediaFileRow struct {
	id         int64
	ownerType  string
	ownerID    string
	mediaIndex int
	filePath   string
	mediaType  string
	sourceURL  string
	fileSize   int64
	actualSize int64
	exists     bool
}

// DataFileReferenceCount counts authoritative DB references to a relative or
// absolute data path. Derived asset rows are intentionally excluded so stale
// inventory does not keep pruned media files alive.
func (db *DB) DataFileReferenceCount(path string) (int, error) {
	candidates := db.dataPathCandidates(path)
	if len(candidates) == 0 {
		return 0, nil
	}
	args := stringsToAny(candidates)
	allArgs := make([]any, 0, len(args)*4)
	for i := 0; i < 4; i++ {
		allArgs = append(allArgs, args...)
	}
	inClause := placeholders(len(candidates))
	var count int
	err := db.conn.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM media_files WHERE file_path IN (`+inClause+`)) +
			(SELECT COUNT(*) FROM videos
			 WHERE file_path IN (`+inClause+`)
			    OR thumbnail_path IN (`+inClause+`)
			    OR dearrow_thumb_path IN (`+inClause+`))
	`, allArgs...).Scan(&count)
	return count, err
}

// RemoveUnreferencedDataFiles removes files only when no remaining DB row
// references the same data path.
func (db *DB) RemoveUnreferencedDataFiles(paths []string) (DataFileRemovalResult, error) {
	var result DataFileRemovalResult
	seen := map[string]struct{}{}
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			result.InvalidOrEmpty++
			continue
		}
		absPath := resolveManifestDataPath(db.dataDir, path)
		if absPath == "" || absPath == "." || absPath == string(filepath.Separator) {
			result.InvalidOrEmpty++
			continue
		}
		clean := filepath.Clean(absPath)
		if _, ok := seen[clean]; ok {
			result.DuplicateRequests++
			continue
		}
		seen[clean] = struct{}{}
		result.Considered++

		refs, err := db.DataFileReferenceCount(path)
		if err != nil {
			return result, err
		}
		if refs > 0 {
			result.StillReferenced++
			continue
		}
		info, err := os.Stat(clean)
		if os.IsNotExist(err) {
			result.Missing++
			continue
		}
		if err != nil {
			return result, err
		}
		if info.IsDir() {
			result.InvalidOrEmpty++
			continue
		}
		size := info.Size()
		if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
			result.RemoveErrors++
			continue
		}
		result.Removed++
		result.RemovedBytes += size
	}
	return result, nil
}

func (db *DB) dataPathCandidates(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	out := []string{filepath.Clean(path)}
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(db.dataDir, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			out = append(out, filepath.Clean(rel))
		}
	} else if db.dataDir != "" {
		out = append(out, filepath.Clean(filepath.Join(db.dataDir, path)))
	}
	return uniqueStrings(out)
}

// DedupeXMediaBySourceURL rewrites duplicate X feed/quote media rows sharing
// the exact same source_url to a single live local file.
func (db *DB) DedupeXMediaBySourceURL(opts XMediaDedupeOptions) (XMediaDedupeResult, error) {
	result := XMediaDedupeResult{
		DryRun: opts.DryRun,
		Limit:  opts.Limit,
	}
	nowMs := opts.NowMs
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	sourceURLs, err := db.xDuplicateSourceURLs(opts.Limit)
	if err != nil {
		return result, err
	}
	result.LimitReached = opts.Limit > 0 && len(sourceURLs) >= opts.Limit

	pathsToRemove := make([]string, 0)
	for _, sourceURL := range sourceURLs {
		rows, err := db.xMediaRowsBySourceURL(sourceURL)
		if err != nil {
			return result, err
		}
		result.Groups++
		result.Rows += len(rows)
		canonical, ok := chooseXMediaCanonical(rows)
		if !ok {
			result.GroupsWithoutLiveFile++
			continue
		}
		rewrites := make([]xMediaFileRow, 0)
		seenDuplicatePaths := map[string]int64{}
		for _, row := range rows {
			if row.filePath == canonical.filePath {
				continue
			}
			rewrites = append(rewrites, row)
			if row.exists {
				if _, ok := seenDuplicatePaths[row.filePath]; !ok {
					seenDuplicatePaths[row.filePath] = row.actualSize
				}
			}
		}
		if len(rewrites) == 0 {
			continue
		}
		for path, size := range seenDuplicatePaths {
			pathsToRemove = append(pathsToRemove, path)
			result.DuplicatePaths++
			result.DuplicateBytes += size
		}
		result.RowsRewritten += len(rewrites)
		if opts.DryRun {
			continue
		}
		updated, err := db.rewriteXMediaRowsToCanonical(rewrites, canonical, nowMs)
		if err != nil {
			return result, err
		}
		result.AssetRowsUpdated += updated
	}
	if !opts.DryRun {
		removal, err := db.RemoveUnreferencedDataFiles(pathsToRemove)
		if err != nil {
			return result, err
		}
		result.FileRemoval = removal
	}
	return result, nil
}

func (db *DB) xDuplicateSourceURLs(limit int) ([]string, error) {
	query := `
		SELECT source_url
		FROM media_files
		WHERE owner_type IN ('feed_media', 'quote_media')
		  AND file_path LIKE 'media/twitter/%'
		  AND COALESCE(source_url, '') != ''
		GROUP BY source_url
		HAVING COUNT(DISTINCT file_path) > 1
		ORDER BY COUNT(*) DESC, source_url ASC
	`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = db.conn.Query(query+` LIMIT ?`, limit)
	} else {
		rows, err = db.conn.Query(query)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []string
	for rows.Next() {
		var sourceURL string
		if err := rows.Scan(&sourceURL); err != nil {
			return nil, err
		}
		out = append(out, sourceURL)
	}
	return out, rows.Err()
}

func (db *DB) xMediaRowsBySourceURL(sourceURL string) ([]xMediaFileRow, error) {
	rows, err := db.conn.Query(`
		SELECT id, owner_type, owner_id, media_index, COALESCE(file_path, ''),
		       COALESCE(media_type, ''), COALESCE(source_url, ''), COALESCE(file_size, 0)
		FROM media_files
		WHERE owner_type IN ('feed_media', 'quote_media')
		  AND source_url = ?
		  AND COALESCE(file_path, '') != ''
		ORDER BY id ASC
	`, sourceURL)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []xMediaFileRow
	for rows.Next() {
		var row xMediaFileRow
		if err := rows.Scan(
			&row.id,
			&row.ownerType,
			&row.ownerID,
			&row.mediaIndex,
			&row.filePath,
			&row.mediaType,
			&row.sourceURL,
			&row.fileSize,
		); err != nil {
			return nil, err
		}
		info, err := os.Stat(resolveManifestDataPath(db.dataDir, row.filePath))
		if err == nil && !info.IsDir() {
			row.exists = true
			row.actualSize = info.Size()
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func chooseXMediaCanonical(rows []xMediaFileRow) (xMediaFileRow, bool) {
	var live []xMediaFileRow
	for _, row := range rows {
		if row.exists {
			live = append(live, row)
		}
	}
	if len(live) == 0 {
		return xMediaFileRow{}, false
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].actualSize != live[j].actualSize {
			return live[i].actualSize > live[j].actualSize
		}
		return live[i].id < live[j].id
	})
	return live[0], true
}

func (db *DB) rewriteXMediaRowsToCanonical(rows []xMediaFileRow, canonical xMediaFileRow, nowMs int64) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	assetRowsUpdated := 0
	err := db.WithWrite(func(tx *sql.Tx) error {
		for _, row := range rows {
			if _, err := tx.Exec(`
				UPDATE media_files
				SET file_path = ?, file_size = ?
				WHERE id = ?
			`, canonical.filePath, canonical.actualSize, row.id); err != nil {
				return err
			}
			res, err := tx.Exec(`
				UPDATE assets
				SET file_path = ?,
				    size_bytes = ?,
				    state = ?,
				    content_type = CASE WHEN content_type = '' THEN ? ELSE content_type END,
				    updated_at_ms = ?
				WHERE asset_kind = 'post_media'
				  AND owner_kind = 'tweet'
				  AND owner_id = ?
				  AND media_index = ?
			`, canonical.filePath, canonical.actualSize, AssetStateReady,
				contentTypeForMediaPath(canonical.filePath, row.mediaType, "image/jpeg"),
				nowMs, row.ownerID, row.mediaIndex)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil {
				assetRowsUpdated += int(n)
			}
		}
		return nil
	})
	return assetRowsUpdated, err
}

// PruneXMediaRetentionForChannel enforces the X media retention window for one
// followed Twitter channel.
func (db *DB) PruneXMediaRetentionForChannel(channelID string, opts XMediaRetentionOptions) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{
		DryRun:         opts.DryRun,
		Limit:          1,
		RetentionLimit: opts.RetentionLimit,
	}
	source, ok := xRetentionSourceFromChannelID(channelID)
	if !ok {
		return result, nil
	}
	limit := opts.RetentionLimit
	if limit <= 0 {
		settings, err := db.GetChannelSettings(source.channelID)
		if err != nil {
			return result, err
		}
		if settings == nil || settings.MediaDownloadLimit <= 0 {
			return result, nil
		}
		limit = settings.MediaDownloadLimit
	}
	result.RetentionLimit = limit
	return db.pruneXMediaRetentionSource(source, limit, opts.NowMs, result)
}

// PruneXMediaRetention prunes feed_media rows for followed X sources beyond
// the per-source media_download_limit. Bookmarks and likes are retained.
func (db *DB) PruneXMediaRetention(opts XMediaRetentionOptions) (XMediaRetentionResult, error) {
	result := XMediaRetentionResult{
		DryRun:         opts.DryRun,
		Limit:          opts.Limit,
		RetentionLimit: opts.RetentionLimit,
	}
	sources, err := db.followedXMediaRetentionSources()
	if err != nil {
		return result, err
	}
	if opts.Limit > 0 && len(sources) > opts.Limit {
		sources = sources[:opts.Limit]
		result.LimitReached = true
	}

	for _, source := range sources {
		limit := opts.RetentionLimit
		if limit <= 0 {
			settings, err := db.GetChannelSettings(source.channelID)
			if err != nil {
				return result, err
			}
			if settings == nil || settings.MediaDownloadLimit <= 0 {
				continue
			}
			limit = settings.MediaDownloadLimit
		}
		result, err = db.pruneXMediaRetentionSource(source, limit, opts.NowMs, result)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (db *DB) pruneXMediaRetentionSource(source xRetentionSource, limit int, nowMs int64, result XMediaRetentionResult) (XMediaRetentionResult, error) {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	result.SourcesScanned++
	items, err := db.xMediaRetentionItems(source.handle)
	if err != nil {
		return result, err
	}
	if len(items) == 0 {
		return result, nil
	}
	keptUnprotected := 0
	var pruneIDs []string
	for _, item := range items {
		if item.protected {
			result.ProtectedItems++
			continue
		}
		if keptUnprotected < limit {
			keptUnprotected++
			result.KeptItems++
			continue
		}
		pruneIDs = append(pruneIDs, item.tweetID)
		result.PrunedItems++
		result.CandidateFileBytes += item.fileBytes
	}
	if len(pruneIDs) == 0 {
		return result, nil
	}
	result.SourcesOverLimit++
	quoteIDs, err := db.quoteMediaIDsPrunedWithParents(pruneIDs)
	if err != nil {
		return result, err
	}
	quoteBytes, err := db.mediaBytesByOwnerType("quote_media", quoteIDs)
	if err != nil {
		return result, err
	}
	result.CandidateFileBytes += quoteBytes
	if result.DryRun {
		return result, nil
	}
	pruned, err := db.pruneXMediaRows(pruneIDs, quoteIDs, nowMs)
	if err != nil {
		return result, err
	}
	result.MediaRowsDeleted += pruned.mediaRowsDeleted
	result.AssetRowsDeleted += pruned.assetRowsDeleted
	result.JobsMarkedPruned += pruned.jobsMarkedPruned
	removal, err := db.RemoveUnreferencedDataFiles(pruned.filePaths)
	if err != nil {
		return result, err
	}
	result.FileRemoval.Considered += removal.Considered
	result.FileRemoval.Removed += removal.Removed
	result.FileRemoval.RemovedBytes += removal.RemovedBytes
	result.FileRemoval.StillReferenced += removal.StillReferenced
	result.FileRemoval.Missing += removal.Missing
	result.FileRemoval.RemoveErrors += removal.RemoveErrors
	result.FileRemoval.InvalidOrEmpty += removal.InvalidOrEmpty
	result.FileRemoval.DuplicateRequests += removal.DuplicateRequests
	return result, nil
}

type xRetentionSource struct {
	channelID string
	handle    string
}

func (db *DB) followedXMediaRetentionSources() ([]xRetentionSource, error) {
	rows, err := db.conn.Query(`
		SELECT c.channel_id
		FROM channels c
		INNER JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		WHERE c.platform = 'twitter'
		  AND c.channel_id LIKE 'twitter_%'
		ORDER BY c.channel_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []xRetentionSource
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		source, ok := xRetentionSourceFromChannelID(channelID)
		if !ok {
			continue
		}
		out = append(out, source)
	}
	return out, rows.Err()
}

func xRetentionSourceFromChannelID(channelID string) (xRetentionSource, bool) {
	channelID = strings.TrimSpace(channelID)
	lower := strings.ToLower(channelID)
	if !strings.HasPrefix(lower, "twitter_") {
		return xRetentionSource{}, false
	}
	handle := strings.TrimPrefix(lower, "twitter_")
	if handle == "" {
		return xRetentionSource{}, false
	}
	return xRetentionSource{channelID: channelID, handle: handle}, true
}

type xRetentionItem struct {
	tweetID   string
	published int64
	protected bool
	fileBytes int64
}

func (db *DB) xMediaRetentionItems(handle string) ([]xRetentionItem, error) {
	rows, err := db.conn.Query(`
		SELECT fi.tweet_id,
		       COALESCE(fi.published_at, 0),
		       CASE WHEN EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = fi.tweet_id)
		              OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = fi.tweet_id)
		            THEN 1 ELSE 0 END,
		       COALESCE(mf.file_path, ''),
		       COALESCE(mf.file_size, 0)
		FROM feed_items fi
		LEFT JOIN media_files mf ON mf.owner_type = 'feed_media' AND mf.owner_id = fi.tweet_id
		WHERE LOWER(COALESCE(NULLIF(fi.source_handle, ''), NULLIF(fi.retweeted_by_handle, ''), fi.author_handle)) = ?
		  AND (
		    COALESCE(fi.media_json, '') NOT IN ('', '[]')
		    OR COALESCE(fi.quote_media_json, '') NOT IN ('', '[]')
		    OR EXISTS (SELECT 1 FROM feed_media_jobs fmj WHERE fmj.tweet_id = fi.tweet_id)
		  )
		ORDER BY COALESCE(fi.published_at, 0) DESC, fi.tweet_id DESC, mf.media_index ASC, mf.id ASC
	`, handle)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var out []xRetentionItem
	byTweet := map[string]int{}
	seenPathsByTweet := map[string]map[string]struct{}{}
	for rows.Next() {
		var item xRetentionItem
		var protected int
		var filePath string
		var fileSize int64
		if err := rows.Scan(&item.tweetID, &item.published, &protected, &filePath, &fileSize); err != nil {
			return nil, err
		}
		item.protected = protected != 0
		idx, ok := byTweet[item.tweetID]
		if !ok {
			byTweet[item.tweetID] = len(out)
			seenPathsByTweet[item.tweetID] = map[string]struct{}{}
			out = append(out, item)
			idx = len(out) - 1
		}
		if filePath == "" {
			out[idx].fileBytes += fileSize
			continue
		}
		if _, ok := seenPathsByTweet[item.tweetID][filePath]; ok {
			continue
		}
		seenPathsByTweet[item.tweetID][filePath] = struct{}{}
		out[idx].fileBytes += db.fileSizeForPathOrRecorded(filePath, fileSize)
	}
	return out, rows.Err()
}

func (db *DB) fileSizeForPathOrRecorded(path string, recorded int64) int64 {
	info, err := os.Stat(resolveManifestDataPath(db.dataDir, path))
	if err == nil && !info.IsDir() {
		return info.Size()
	}
	return recorded
}

type xMediaPruneResult struct {
	mediaRowsDeleted int
	assetRowsDeleted int
	jobsMarkedPruned int
	filePaths        []string
}

func (db *DB) quoteMediaIDsPrunedWithParents(tweetIDs []string) ([]string, error) {
	tweetIDs = uniqueStrings(tweetIDs)
	if len(tweetIDs) == 0 {
		return nil, nil
	}
	pruneSet := make(map[string]struct{}, len(tweetIDs))
	for _, tweetID := range tweetIDs {
		pruneSet[tweetID] = struct{}{}
	}
	candidates := map[string]struct{}{}
	for _, chunk := range stringChunks(tweetIDs, 400) {
		args := stringsToAny(chunk)
		rows, err := db.conn.Query(`
			SELECT DISTINCT COALESCE(quote_tweet_id, '')
			FROM feed_items
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
			  AND COALESCE(quote_tweet_id, '') != ''
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var quoteID string
			if err := rows.Scan(&quoteID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			candidates[quoteID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	quoteIDs := make([]string, 0, len(candidates))
	for quoteID := range candidates {
		quoteIDs = append(quoteIDs, quoteID)
	}
	sort.Strings(quoteIDs)
	retainedRefs := map[string]struct{}{}
	for _, chunk := range stringChunks(quoteIDs, 400) {
		args := stringsToAny(chunk)
		rows, err := db.conn.Query(`
			SELECT COALESCE(quote_tweet_id, ''), tweet_id
			FROM feed_items
			WHERE quote_tweet_id IN (`+placeholders(len(chunk))+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var quoteID, parentTweetID string
			if err := rows.Scan(&quoteID, &parentTweetID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, pruningParent := pruneSet[parentTweetID]; !pruningParent {
				retainedRefs[quoteID] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}

	out := make([]string, 0, len(quoteIDs))
	for _, quoteID := range quoteIDs {
		if _, retained := retainedRefs[quoteID]; retained {
			continue
		}
		out = append(out, quoteID)
	}
	return out, nil
}

func (db *DB) mediaBytesByOwnerType(ownerType string, ownerIDs []string) (int64, error) {
	ownerIDs = uniqueStrings(ownerIDs)
	if len(ownerIDs) == 0 {
		return 0, nil
	}
	var total int64
	seenPaths := map[string]struct{}{}
	for _, chunk := range stringChunks(ownerIDs, 400) {
		args := append([]any{ownerType}, stringsToAny(chunk)...)
		rows, err := db.conn.Query(`
			SELECT COALESCE(file_path, ''), COALESCE(file_size, 0)
			FROM media_files
			WHERE owner_type = ?
			  AND owner_id IN (`+placeholders(len(chunk))+`)
		`, args...)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var filePath string
			var fileSize int64
			if err := rows.Scan(&filePath, &fileSize); err != nil {
				_ = rows.Close()
				return 0, err
			}
			if filePath == "" {
				total += fileSize
				continue
			}
			if _, ok := seenPaths[filePath]; ok {
				continue
			}
			seenPaths[filePath] = struct{}{}
			total += db.fileSizeForPathOrRecorded(filePath, fileSize)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return 0, err
		}
		_ = rows.Close()
	}
	return total, nil
}

func (db *DB) pruneXMediaRows(tweetIDs []string, quoteIDs []string, nowMs int64) (xMediaPruneResult, error) {
	var result xMediaPruneResult
	tweetIDs = uniqueStrings(tweetIDs)
	quoteIDs = uniqueStrings(quoteIDs)
	for _, chunk := range stringChunks(tweetIDs, 400) {
		if len(chunk) == 0 {
			continue
		}
		args := stringsToAny(chunk)
		inClause := placeholders(len(chunk))
		err := db.WithWrite(func(tx *sql.Tx) error {
			rows, err := tx.Query(`
				SELECT COALESCE(file_path, '')
				FROM media_files
				WHERE owner_type = 'feed_media'
				  AND owner_id IN (`+inClause+`)
				  AND COALESCE(file_path, '') != ''
			`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var path string
				if err := rows.Scan(&path); err != nil {
					_ = rows.Close()
					return err
				}
				result.filePaths = append(result.filePaths, path)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			_ = rows.Close()

			res, err := tx.Exec(`
				DELETE FROM media_files
				WHERE owner_type = 'feed_media'
				  AND owner_id IN (`+inClause+`)
			`, args...)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil {
				result.mediaRowsDeleted += int(n)
			}

			res, err = tx.Exec(`
				UPDATE feed_media_jobs
				SET status = 'pruned',
				    last_error_kind = 'retention',
				    last_error = 'x media retention',
				    next_attempt_at_ms = 0,
				    lease_owner = '',
				    lease_until_ms = 0,
				    completed_at_ms = CASE WHEN completed_at_ms = 0 THEN ? ELSE completed_at_ms END,
				    updated_at = ?
				WHERE tweet_id IN (`+inClause+`)
				  AND status IN ('completed', 'queued', 'failed')
			`, append([]any{nowMs, nowMs}, args...)...)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil {
				result.jobsMarkedPruned += int(n)
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	for _, chunk := range stringChunks(quoteIDs, 400) {
		if len(chunk) == 0 {
			continue
		}
		args := stringsToAny(chunk)
		inClause := placeholders(len(chunk))
		err := db.WithWrite(func(tx *sql.Tx) error {
			rows, err := tx.Query(`
				SELECT COALESCE(file_path, '')
				FROM media_files
				WHERE owner_type = 'quote_media'
				  AND owner_id IN (`+inClause+`)
				  AND COALESCE(file_path, '') != ''
			`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var path string
				if err := rows.Scan(&path); err != nil {
					_ = rows.Close()
					return err
				}
				result.filePaths = append(result.filePaths, path)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			_ = rows.Close()

			res, err := tx.Exec(`
				DELETE FROM media_files
				WHERE owner_type = 'quote_media'
				  AND owner_id IN (`+inClause+`)
			`, args...)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil {
				result.mediaRowsDeleted += int(n)
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	assetOwnerIDs := append(append([]string{}, tweetIDs...), quoteIDs...)
	for _, chunk := range stringChunks(uniqueStrings(assetOwnerIDs), 400) {
		if len(chunk) == 0 {
			continue
		}
		args := stringsToAny(chunk)
		inClause := placeholders(len(chunk))
		err := db.WithWrite(func(tx *sql.Tx) error {
			res, err := tx.Exec(`
				DELETE FROM assets
				WHERE asset_kind = 'post_media'
				  AND owner_kind = 'tweet'
				  AND owner_id IN (`+inClause+`)
				  AND NOT EXISTS (
				    SELECT 1
				    FROM media_files mf
				    WHERE mf.owner_type IN ('feed_media', 'quote_media')
				      AND mf.owner_id = assets.owner_id
				      AND mf.media_index = assets.media_index
				  )
			`, args...)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil {
				result.assetRowsDeleted += int(n)
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// MaintainReadyAssetFileStates refreshes ready assets whose recorded file path
// is missing or whose recorded size no longer matches the file on disk.
func (db *DB) MaintainReadyAssetFileStates(opts AssetFileStateMaintenanceOptions) (AssetFileStateMaintenanceResult, error) {
	result := AssetFileStateMaintenanceResult{
		DryRun: opts.DryRun,
		Limit:  opts.Limit,
		ByKind: map[string]AssetFileStateKindResult{},
	}
	nowMs := opts.NowMs
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	query := `
		SELECT asset_id, asset_kind, COALESCE(file_path, ''), COALESCE(size_bytes, 0)
		FROM assets
		WHERE state = ?
		  AND COALESCE(file_path, '') != ''
		ORDER BY updated_at_ms ASC, id ASC
	`
	var rows *sql.Rows
	var err error
	if opts.Limit > 0 {
		rows, err = db.conn.Query(query+` LIMIT ?`, AssetStateReady, opts.Limit)
	} else {
		rows, err = db.conn.Query(query, AssetStateReady)
	}
	if err != nil {
		return result, err
	}
	defer func() {
		_ = rows.Close()
	}()
	type update struct {
		assetID   string
		assetKind string
		state     string
		sizeBytes int64
	}
	var updates []update
	for rows.Next() {
		var assetID, assetKind, filePath string
		var recordedSize int64
		if err := rows.Scan(&assetID, &assetKind, &filePath, &recordedSize); err != nil {
			return result, err
		}
		result.Checked++
		kindResult := result.ByKind[assetKind]
		kindResult.Checked++
		kindResult.PreviousBytes += recordedSize
		info, statErr := os.Stat(resolveManifestDataPath(db.dataDir, filePath))
		if statErr != nil || info.IsDir() {
			result.Missing++
			kindResult.Missing++
			kindResult.MissingBytes += recordedSize
			updates = append(updates, update{assetID: assetID, assetKind: assetKind, state: AssetStateServerMissing})
		} else {
			actualSize := info.Size()
			kindResult.ActualBytes += actualSize
			if actualSize != recordedSize {
				result.SizeChanged++
				kindResult.SizeChanged++
				updates = append(updates, update{assetID: assetID, assetKind: assetKind, state: AssetStateReady, sizeBytes: actualSize})
			}
		}
		result.ByKind[assetKind] = kindResult
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.LimitReached = opts.Limit > 0 && result.Checked >= opts.Limit
	if opts.DryRun || len(updates) == 0 {
		return result, nil
	}
	err = db.WithWrite(func(tx *sql.Tx) error {
		for _, u := range updates {
			res, err := tx.Exec(`
				UPDATE assets
				SET state = ?,
				    size_bytes = ?,
				    updated_at_ms = ?
				WHERE asset_id = ?
				  AND asset_kind = ?
			`, u.state, u.sizeBytes, nowMs, u.assetID, u.assetKind)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err == nil && n > 0 {
				result.Updated += int(n)
				kindResult := result.ByKind[u.assetKind]
				kindResult.Updated += int(n)
				result.ByKind[u.assetKind] = kindResult
			}
		}
		return nil
	})
	return result, err
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
