package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	AssetStateQueued           = "queued"
	AssetStateDownloading      = "downloading"
	AssetStateReady            = "ready"
	AssetStateFailed           = "failed"
	AssetStateServerMissing    = "server_missing"
	AssetStatePermanentMissing = "permanent_missing"
	AssetStateStale            = "stale"
)

// Asset is the server-owned inventory row for a binary or derived media asset.
type Asset struct {
	ID              int64
	AssetID         string
	AssetKind       string
	OwnerKind       string
	OwnerID         string
	MediaIndex      int
	SourceURL       string
	FilePath        string
	ContentType     string
	SizeBytes       int64
	SHA256          string
	State           string
	RequiredReason  string
	LastErrorKind   string
	LastError       string
	Attempts        int
	NextAttemptAtMs int64
	LeaseOwner      string
	LeaseUntilMs    int64
	CreatedAtMs     int64
	UpdatedAtMs     int64
}

// UpsertAsset inserts or updates an inventory row. The asset identity follows
// the Android/manifest asset_id contract while this table remains additive.
func (db *DB) UpsertAsset(asset Asset, nowMs int64) error {
	asset = normalizeAsset(asset, nowMs)
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO assets (
				asset_id, asset_kind, owner_kind, owner_id, media_index,
				source_url, file_path, content_type, size_bytes, sha256, state,
				required_reason, last_error_kind, last_error, attempts,
				next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(asset_kind, owner_kind, owner_id, media_index) DO UPDATE SET
				asset_id = excluded.asset_id,
				source_url = excluded.source_url,
				file_path = excluded.file_path,
				content_type = excluded.content_type,
				size_bytes = excluded.size_bytes,
				sha256 = excluded.sha256,
				state = excluded.state,
				required_reason = excluded.required_reason,
				last_error_kind = excluded.last_error_kind,
				last_error = excluded.last_error,
				attempts = excluded.attempts,
				next_attempt_at_ms = excluded.next_attempt_at_ms,
				lease_owner = excluded.lease_owner,
				lease_until_ms = excluded.lease_until_ms,
				updated_at_ms = excluded.updated_at_ms
		`, asset.AssetID, asset.AssetKind, asset.OwnerKind, asset.OwnerID, asset.MediaIndex,
			asset.SourceURL, asset.FilePath, asset.ContentType, asset.SizeBytes, asset.SHA256, asset.State,
			asset.RequiredReason, asset.LastErrorKind, asset.LastError, asset.Attempts,
			asset.NextAttemptAtMs, asset.LeaseOwner, asset.LeaseUntilMs, asset.CreatedAtMs, asset.UpdatedAtMs)
		return err
	})
}

func normalizeAsset(asset Asset, nowMs int64) Asset {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	asset.AssetID = strings.TrimSpace(asset.AssetID)
	asset.AssetKind = strings.TrimSpace(asset.AssetKind)
	asset.OwnerKind = strings.TrimSpace(asset.OwnerKind)
	asset.OwnerID = strings.TrimSpace(asset.OwnerID)
	asset.SourceURL = strings.TrimSpace(asset.SourceURL)
	asset.FilePath = strings.TrimSpace(asset.FilePath)
	asset.ContentType = strings.TrimSpace(asset.ContentType)
	asset.SHA256 = strings.TrimSpace(asset.SHA256)
	asset.State = strings.TrimSpace(asset.State)
	asset.RequiredReason = strings.TrimSpace(asset.RequiredReason)
	asset.LastErrorKind = strings.TrimSpace(asset.LastErrorKind)
	asset.LastError = strings.TrimSpace(asset.LastError)
	asset.LeaseOwner = strings.TrimSpace(asset.LeaseOwner)
	if asset.State == "" {
		asset.State = AssetStateQueued
	}
	if asset.CreatedAtMs <= 0 {
		asset.CreatedAtMs = nowMs
	}
	if asset.UpdatedAtMs <= 0 {
		asset.UpdatedAtMs = nowMs
	}
	return asset
}

// GetAsset returns one inventory row by public asset identity.
func (db *DB) GetAsset(assetID, assetKind string) (*Asset, error) {
	row := db.conn.QueryRow(`
		SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
		       source_url, file_path, content_type, size_bytes, sha256, state,
		       required_reason, last_error_kind, last_error, attempts,
		       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
		FROM assets
		WHERE asset_id = ? AND asset_kind = ?
	`, strings.TrimSpace(assetID), strings.TrimSpace(assetKind))
	asset, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

// ClaimAssetRepairBatch claims queued or expired downloading assets for repair.
func (db *DB) ClaimAssetRepairBatch(limit int) ([]Asset, error) {
	return db.ClaimAssetRepairBatchWithLease(LeaseOptions{
		Owner: "assets:legacy",
		Limit: limit,
	})
}

// ClaimAssetRepairBatchWithLease claims assets with a durable lease.
func (db *DB) ClaimAssetRepairBatchWithLease(opts LeaseOptions) ([]Asset, error) {
	opts = normalizeLeaseOptions(opts, AssetStateQueued, AssetStateDownloading)
	var claimed []Asset
	err := db.WithWrite(func(tx *sql.Tx) error {
		query := `
			SELECT asset_id
			FROM assets
			WHERE ` + leaseEligibleSQLFor("state", "next_attempt_at_ms", "lease_until_ms") + `
			ORDER BY attempts ASC, updated_at_ms ASC, id ASC
			LIMIT ?`
		ids, err := claimLeasedIDsWithStateColumn(tx, "assets", "asset_id", "state", query, []any{
			opts.NowMs, opts.StatusFrom, opts.NowMs, opts.StatusTo, opts.NowMs, opts.Limit,
		}, opts)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE assets SET updated_at_ms = ? WHERE asset_id = ?`, opts.NowMs, id); err != nil {
				return err
			}
			asset, err := readAssetTx(tx, id)
			if err != nil {
				return err
			}
			claimed = append(claimed, asset)
		}
		return nil
	})
	return claimed, err
}

func readAssetTx(tx *sql.Tx, assetID string) (Asset, error) {
	return scanAsset(tx.QueryRow(`
		SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
		       source_url, file_path, content_type, size_bytes, sha256, state,
		       required_reason, last_error_kind, last_error, attempts,
		       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
		FROM assets
		WHERE asset_id = ?
	`, assetID))
}

func (db *DB) CompleteAssetRepair(asset Asset, owner string, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	asset.AssetID = strings.TrimSpace(asset.AssetID)
	asset.AssetKind = strings.TrimSpace(asset.AssetKind)
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE assets
			   SET state=?,
			       file_path=?,
			       content_type=?,
			       size_bytes=?,
			       sha256=?,
			       last_error_kind='',
			       last_error='',
			       attempts=0,
			       next_attempt_at_ms=0,
			       lease_owner='',
			       lease_until_ms=0,
			       updated_at_ms=?
			 WHERE asset_id=?
			   AND asset_kind=?
			   AND state=?
			   AND lease_owner=?
		`, AssetStateReady, strings.TrimSpace(asset.FilePath), strings.TrimSpace(asset.ContentType),
			asset.SizeBytes, strings.TrimSpace(asset.SHA256), nowMs, asset.AssetID, asset.AssetKind,
			AssetStateDownloading, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "assets", asset.AssetID+"/"+asset.AssetKind, owner)
	})
}

func (db *DB) RetryAssetRepair(assetID, assetKind, owner, kind, message string, delay time.Duration, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	nextMs := nowMs + delay.Milliseconds()
	if delay < 0 {
		nextMs = nowMs
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE assets
			   SET state=?,
			       attempts=attempts+1,
			       next_attempt_at_ms=?,
			       last_error_kind=?,
			       last_error=?,
			       lease_owner='',
			       lease_until_ms=0,
			       updated_at_ms=?
			 WHERE asset_id=?
			   AND asset_kind=?
			   AND state=?
			   AND lease_owner=?
		`, AssetStateQueued, nextMs, trimJobError(kind), trimJobError(message), nowMs,
			strings.TrimSpace(assetID), strings.TrimSpace(assetKind), AssetStateDownloading, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "assets", strings.TrimSpace(assetID)+"/"+strings.TrimSpace(assetKind), owner)
	})
}

func (db *DB) FailAssetRepair(assetID, assetKind, owner, kind, message string, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE assets
			   SET state=?,
			       attempts=attempts+1,
			       next_attempt_at_ms=0,
			       last_error_kind=?,
			       last_error=?,
			       lease_owner='',
			       lease_until_ms=0,
			       updated_at_ms=?
			 WHERE asset_id=?
			   AND asset_kind=?
			   AND state=?
			   AND lease_owner=?
		`, AssetStateFailed, trimJobError(kind), trimJobError(message), nowMs,
			strings.TrimSpace(assetID), strings.TrimSpace(assetKind), AssetStateDownloading, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "assets", strings.TrimSpace(assetID)+"/"+strings.TrimSpace(assetKind), owner)
	})
}

func (db *DB) RenewAssetRepairLease(assetID, assetKind, owner string, nowMs int64, lease time.Duration) error {
	if assetID == "" || assetKind == "" || owner == "" {
		return nil
	}
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	if lease <= 0 {
		lease = defaultQueueLease
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE assets
			   SET lease_until_ms=?, updated_at_ms=?
			 WHERE asset_id=?
			   AND asset_kind=?
			   AND state=?
			   AND lease_owner=?
		`, nowMs+lease.Milliseconds(), nowMs, strings.TrimSpace(assetID), strings.TrimSpace(assetKind), AssetStateDownloading, owner)
		return err
	})
}

// ListAndroidSyncAssetInventoryRows returns inventory rows that can contribute
// directly to Android sync generation for the desired owner sets.
func (db *DB) ListAndroidSyncAssetInventoryRows(sets AndroidSyncDesiredSets) ([]Asset, error) {
	ownerIDsByKind := map[string]map[string]struct{}{}
	addOwner := func(ownerKind, ownerID string) {
		ownerKind = strings.TrimSpace(ownerKind)
		ownerID = strings.TrimSpace(ownerID)
		if ownerKind == "" || ownerID == "" {
			return
		}
		if ownerIDsByKind[ownerKind] == nil {
			ownerIDsByKind[ownerKind] = map[string]struct{}{}
		}
		ownerIDsByKind[ownerKind][ownerID] = struct{}{}
	}
	for _, id := range sets.SortedTweets() {
		addOwner("tweet", id)
	}
	videoIDs := map[string]struct{}{}
	for _, id := range sets.SortedVideos() {
		videoIDs[id] = struct{}{}
	}
	for _, id := range sets.SortedMediaVideos() {
		videoIDs[id] = struct{}{}
	}
	videoOwnerKinds, err := db.androidSyncInventoryVideoOwnerKinds(sortedKeys(videoIDs))
	if err != nil {
		return nil, err
	}
	for _, id := range sortedKeys(videoIDs) {
		ownerKind := videoOwnerKinds[id]
		if ownerKind == "" {
			ownerKind = videoOwnerKind(id)
		}
		addOwner(ownerKind, id)
	}
	for _, id := range sets.SortedChannels() {
		addOwner("channel", id)
	}
	var out []Asset
	ownerKinds := make([]string, 0, len(ownerIDsByKind))
	for ownerKind := range ownerIDsByKind {
		ownerKinds = append(ownerKinds, ownerKind)
	}
	sort.Strings(ownerKinds)
	for _, ownerKind := range ownerKinds {
		for _, chunk := range stringChunks(sortedKeys(ownerIDsByKind[ownerKind]), 400) {
			if len(chunk) == 0 {
				continue
			}
			args := make([]any, 0, len(chunk)+1)
			args = append(args, ownerKind)
			for _, id := range chunk {
				args = append(args, id)
			}
			rows, err := db.conn.Query(`
				SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
				       source_url, file_path, content_type, size_bytes, sha256, state,
				       required_reason, last_error_kind, last_error, attempts,
				       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
				FROM assets
				WHERE owner_kind = ?
				  AND owner_id IN (`+placeholders(len(chunk))+`)
				  AND state IN ('ready', 'server_missing')
				ORDER BY id ASC
			`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				asset, err := scanAsset(rows)
				if err != nil {
					rows.Close()
					return nil, err
				}
				out = append(out, asset)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (db *DB) androidSyncInventoryVideoOwnerKinds(videoIDs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, chunk := range stringChunks(videoIDs, 400) {
		if len(chunk) == 0 {
			continue
		}
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := db.conn.Query(`
			SELECT video_id, COALESCE(channel_id, '')
			FROM videos
			WHERE video_id IN (`+placeholders(len(chunk))+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var videoID, channelID string
			if err := rows.Scan(&videoID, &channelID); err != nil {
				rows.Close()
				return nil, err
			}
			platform := videoPlatformFromChannelID(channelID)
			if platform == "" {
				platform = videoPlatform(videoID)
			}
			out[videoID] = videoOwnerKindForPlatform(platform)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

type assetScanner interface {
	Scan(dest ...any) error
}

func scanAsset(row assetScanner) (Asset, error) {
	var asset Asset
	err := row.Scan(
		&asset.ID,
		&asset.AssetID,
		&asset.AssetKind,
		&asset.OwnerKind,
		&asset.OwnerID,
		&asset.MediaIndex,
		&asset.SourceURL,
		&asset.FilePath,
		&asset.ContentType,
		&asset.SizeBytes,
		&asset.SHA256,
		&asset.State,
		&asset.RequiredReason,
		&asset.LastErrorKind,
		&asset.LastError,
		&asset.Attempts,
		&asset.NextAttemptAtMs,
		&asset.LeaseOwner,
		&asset.LeaseUntilMs,
		&asset.CreatedAtMs,
		&asset.UpdatedAtMs,
	)
	return asset, err
}

// RefreshAssetFileState reconciles one asset's ready/server_missing state from
// its recorded file path. It does not compute checksums.
func (db *DB) RefreshAssetFileState(assetID, assetKind string, nowMs int64) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	asset, err := db.GetAsset(assetID, assetKind)
	if err != nil || asset == nil {
		return err
	}
	absPath := resolveManifestDataPath(db.dataDir, asset.FilePath)
	state := AssetStateServerMissing
	sizeBytes := int64(0)
	contentType := asset.ContentType
	if asset.FilePath == "" {
		state = AssetStateQueued
	} else if info, statErr := os.Stat(absPath); statErr == nil {
		state = AssetStateReady
		sizeBytes = info.Size()
		if contentType == "" {
			contentType = contentTypeForPath(asset.FilePath, "")
		}
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE assets
			SET state = ?, size_bytes = ?, content_type = ?, updated_at_ms = ?
			WHERE asset_id = ? AND asset_kind = ?
		`, state, sizeBytes, contentType, nowMs, asset.AssetID, asset.AssetKind)
		return err
	})
}

// AssetInventoryReconcileOptions controls the explicit legacy-path to assets
// reconciliation pass. The default behavior reconciles missing inventory rows
// only; it is intended for one-off maintenance commands, not startup work.
type AssetInventoryReconcileOptions struct {
	NowMs  int64 `json:"now_ms"`
	Limit  int   `json:"limit"`
	DryRun bool  `json:"dry_run"`
}

// AssetInventoryReconcileKindResult reports reconciliation work for one asset
// kind.
type AssetInventoryReconcileKindResult struct {
	Candidates    int `json:"candidates"`
	Written       int `json:"written"`
	Ready         int `json:"ready"`
	Queued        int `json:"queued"`
	ServerMissing int `json:"server_missing"`
}

// AssetInventoryReconcileResult reports bounded legacy-path reconciliation.
type AssetInventoryReconcileResult struct {
	DryRun          bool                                         `json:"dry_run"`
	Limit           int                                          `json:"limit"`
	LimitReached    bool                                         `json:"limit_reached"`
	Candidates      int                                          `json:"candidates"`
	Written         int                                          `json:"written"`
	SkippedExisting int                                          `json:"skipped_existing"`
	ByKind          map[string]AssetInventoryReconcileKindResult `json:"by_kind"`
}

// BackfillAssetsFromExistingPaths creates or refreshes inventory rows from the
// legacy media path columns and conventional cache directories. It is safe to
// run repeatedly, but intentionally remains an explicit call instead of startup
// behavior.
func (db *DB) BackfillAssetsFromExistingPaths(nowMs int64) (int, error) {
	result, err := db.backfillAssetsFromExistingPaths(AssetInventoryReconcileOptions{
		NowMs: nowMs,
	}, true)
	if err != nil {
		return result.Written, err
	}
	return result.Written, nil
}

// ReconcileAssetInventoryFromExistingPaths populates missing assets rows from
// legacy media path columns and conventional cache directories. Existing rows
// are skipped so a small limit makes progress on inventory parity.
func (db *DB) ReconcileAssetInventoryFromExistingPaths(opts AssetInventoryReconcileOptions) (AssetInventoryReconcileResult, error) {
	return db.backfillAssetsFromExistingPaths(opts, false)
}

// MaintainVideoAssets refreshes inventory rows for the media assets implied by
// one videos row and conventional sibling files such as subtitles/previews.
func (db *DB) MaintainVideoAssets(videoID string, nowMs int64) error {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return nil
	}
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	src, ok, err := db.getVideoAssetSource(videoID)
	if err != nil || !ok {
		return err
	}
	for _, asset := range db.videoAssetsFromSource(src) {
		if err := db.upsertAssetFromLegacyPath(asset, nowMs); err != nil {
			return err
		}
	}
	return nil
}

// MaintainChannelProfileAssets refreshes avatar/banner inventory rows for one
// non-tombstoned channel profile.
func (db *DB) MaintainChannelProfileAssets(channelID string, nowMs int64) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil
	}
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	src, ok, err := db.getChannelProfileAssetSource(channelID)
	if err != nil || !ok {
		return err
	}
	for _, asset := range db.profileAssetsFromSource(src) {
		if err := db.upsertAssetFromLegacyPath(asset, nowMs); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) backfillAssetsFromExistingPaths(opts AssetInventoryReconcileOptions, includeExisting bool) (AssetInventoryReconcileResult, error) {
	run := newAssetInventoryReconcileRun(db, opts, includeExisting)
	for _, fn := range []func(*assetInventoryReconcileRun) error{
		db.backfillMediaFileAssets,
		db.backfillVideoAssets,
		db.backfillProfileAssets,
	} {
		if run.exhausted() {
			break
		}
		if err := fn(run); err != nil {
			return run.result, err
		}
	}
	return run.result, nil
}

type assetInventoryReconcileRun struct {
	db              *DB
	nowMs           int64
	limit           int
	dryRun          bool
	includeExisting bool
	result          AssetInventoryReconcileResult
}

func newAssetInventoryReconcileRun(db *DB, opts AssetInventoryReconcileOptions, includeExisting bool) *assetInventoryReconcileRun {
	nowMs := opts.NowMs
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	limit := opts.Limit
	if limit < 0 {
		limit = 0
	}
	return &assetInventoryReconcileRun{
		db:              db,
		nowMs:           nowMs,
		limit:           limit,
		dryRun:          opts.DryRun,
		includeExisting: includeExisting,
		result: AssetInventoryReconcileResult{
			DryRun: opts.DryRun,
			Limit:  limit,
			ByKind: map[string]AssetInventoryReconcileKindResult{},
		},
	}
}

func (run *assetInventoryReconcileRun) exhausted() bool {
	return run.limit > 0 && run.result.Candidates >= run.limit
}

func (run *assetInventoryReconcileRun) handle(asset Asset) error {
	if run.exhausted() {
		return nil
	}
	asset = run.db.assetFromLegacyPath(asset)
	asset = normalizeAsset(asset, run.nowMs)
	existing, err := run.db.getAssetByOwnerIdentity(asset.AssetKind, asset.OwnerKind, asset.OwnerID, asset.MediaIndex)
	if err != nil {
		return err
	}
	if existing != nil && !run.includeExisting {
		run.result.SkippedExisting++
		return nil
	}
	run.recordCandidate(asset)
	if run.dryRun {
		return nil
	}
	if err := run.db.upsertBackfilledAsset(asset, run.nowMs); err != nil {
		return err
	}
	run.recordWritten(asset)
	return nil
}

func (run *assetInventoryReconcileRun) recordCandidate(asset Asset) {
	run.result.Candidates++
	byKind := run.result.ByKind[asset.AssetKind]
	byKind.Candidates++
	switch asset.State {
	case AssetStateReady:
		byKind.Ready++
	case AssetStateServerMissing:
		byKind.ServerMissing++
	case AssetStateQueued:
		byKind.Queued++
	}
	run.result.ByKind[asset.AssetKind] = byKind
	if run.exhausted() {
		run.result.LimitReached = true
	}
}

func (run *assetInventoryReconcileRun) recordWritten(asset Asset) {
	run.result.Written++
	byKind := run.result.ByKind[asset.AssetKind]
	byKind.Written++
	run.result.ByKind[asset.AssetKind] = byKind
}

func (db *DB) backfillMediaFileAssets(run *assetInventoryReconcileRun) error {
	rows, err := db.conn.Query(`
		SELECT owner_id, COALESCE(media_type, ''), media_index,
		       COALESCE(file_size, 0), COALESCE(file_path, ''), COALESCE(source_url, '')
		FROM media_files
		WHERE owner_type IN ('feed_media', 'quote_media')
		ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		if run.exhausted() {
			return nil
		}
		var ownerID, mediaType, filePath, sourceURL string
		var mediaIndex int
		var fileSize int64
		if err := rows.Scan(&ownerID, &mediaType, &mediaIndex, &fileSize, &filePath, &sourceURL); err != nil {
			return err
		}
		if manifestSkipsFile(filePath) {
			continue
		}
		asset := Asset{
			AssetID:        BuildManifestAssetID("twitter", "tweet", ownerID, "post_media", mediaIndex),
			AssetKind:      "post_media",
			OwnerKind:      "tweet",
			OwnerID:        ownerID,
			MediaIndex:     mediaIndex,
			SourceURL:      sourceURL,
			FilePath:       filePath,
			ContentType:    contentTypeForMediaPath(filePath, mediaType, "image/jpeg"),
			SizeBytes:      fileSize,
			State:          AssetStateQueued,
			RequiredReason: "backfill",
		}
		if err := run.handle(asset); err != nil {
			return err
		}

		if mediaIndex == 0 && (mediaType == "video" || mediaType == "gif" || !manifestUsesImageTransport(filePath, mediaType)) {
			if run.exhausted() {
				return nil
			}
			thumbRel := filepath.Join("thumbnails", "generated", ownerID+".jpg")
			thumb := Asset{
				AssetID:        BuildManifestAssetID("twitter", "tweet", ownerID, "post_thumbnail", 0),
				AssetKind:      "post_thumbnail",
				OwnerKind:      "tweet",
				OwnerID:        ownerID,
				FilePath:       thumbRel,
				ContentType:    "image/jpeg",
				State:          AssetStateQueued,
				RequiredReason: "backfill",
			}
			if err := run.handle(thumb); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

type videoAssetSource struct {
	videoID       string
	channelID     string
	thumbnailPath string
	filePath      string
	fileSize      int64
	dearrowPath   string
}

func (db *DB) getVideoAssetSource(videoID string) (videoAssetSource, bool, error) {
	var src videoAssetSource
	err := db.conn.QueryRow(`
		SELECT video_id, COALESCE(channel_id, ''), COALESCE(thumbnail_path, ''),
		       COALESCE(file_path, ''), COALESCE(file_size, 0), COALESCE(dearrow_thumb_path, '')
		FROM videos
		WHERE video_id = ?
	`, videoID).Scan(
		&src.videoID,
		&src.channelID,
		&src.thumbnailPath,
		&src.filePath,
		&src.fileSize,
		&src.dearrowPath,
	)
	if err == sql.ErrNoRows {
		return src, false, nil
	}
	if err != nil {
		return src, false, err
	}
	return src, true, nil
}

func (db *DB) videoAssetsFromSource(src videoAssetSource) []Asset {
	platform := videoPlatformFromChannelID(src.channelID)
	if platform == "" {
		platform = videoPlatform(src.videoID)
	}
	ownerKind := videoOwnerKindForPlatform(platform)
	assets := make([]Asset, 0, 8)
	for _, asset := range []Asset{
		{
			AssetID:        BuildManifestAssetID(platform, ownerKind, src.videoID, "video_stream", 0),
			AssetKind:      "video_stream",
			OwnerKind:      ownerKind,
			OwnerID:        src.videoID,
			FilePath:       src.filePath,
			ContentType:    contentTypeForMediaPath(src.filePath, "", "video/mp4"),
			SizeBytes:      src.fileSize,
			State:          AssetStateQueued,
			RequiredReason: "retention",
		},
		{
			AssetID:        BuildManifestAssetID(platform, ownerKind, src.videoID, "post_thumbnail", 0),
			AssetKind:      "post_thumbnail",
			OwnerKind:      ownerKind,
			OwnerID:        src.videoID,
			FilePath:       src.thumbnailPath,
			ContentType:    contentTypeForPath(src.thumbnailPath, "image/jpeg"),
			State:          AssetStateQueued,
			RequiredReason: "retention",
		},
		{
			AssetID:        BuildManifestAssetID(platform, ownerKind, src.videoID, "dearrow_thumbnail", 0),
			AssetKind:      "dearrow_thumbnail",
			OwnerKind:      ownerKind,
			OwnerID:        src.videoID,
			FilePath:       src.dearrowPath,
			ContentType:    contentTypeForPath(src.dearrowPath, "image/jpeg"),
			State:          AssetStateQueued,
			RequiredReason: "retention",
		},
	} {
		if strings.TrimSpace(asset.FilePath) != "" {
			assets = append(assets, asset)
		}
	}
	if subtitleRel := db.findSubtitleRelativePath(src.filePath); subtitleRel != "" {
		assets = append(assets, Asset{
			AssetID:        BuildManifestAssetID(platform, ownerKind, src.videoID, "subtitle", 0),
			AssetKind:      "subtitle",
			OwnerKind:      ownerKind,
			OwnerID:        src.videoID,
			FilePath:       subtitleRel,
			ContentType:    "text/vtt",
			State:          AssetStateQueued,
			RequiredReason: "retention",
		})
	}
	for _, preview := range []struct {
		name        string
		assetKind   string
		contentType string
	}{
		{name: "track.json", assetKind: "preview_track_json", contentType: "application/json"},
		{name: "sprite.jpg", assetKind: "preview_sprite", contentType: "image/jpeg"},
	} {
		rel := filepath.Join("thumbnails", "previews", src.videoID, preview.name)
		if _, err := os.Stat(resolveManifestDataPath(db.dataDir, rel)); err != nil {
			continue
		}
		assets = append(assets, Asset{
			AssetID:        BuildManifestAssetID(platform, ownerKind, src.videoID, preview.assetKind, 0),
			AssetKind:      preview.assetKind,
			OwnerKind:      ownerKind,
			OwnerID:        src.videoID,
			FilePath:       rel,
			ContentType:    preview.contentType,
			State:          AssetStateQueued,
			RequiredReason: "retention",
		})
	}
	return assets
}

func (db *DB) backfillVideoAssets(run *assetInventoryReconcileRun) error {
	rows, err := db.conn.Query(`
		SELECT video_id, COALESCE(channel_id, ''), COALESCE(thumbnail_path, ''),
		       COALESCE(file_path, ''), COALESCE(file_size, 0), COALESCE(dearrow_thumb_path, '')
		FROM videos
		ORDER BY id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		if run.exhausted() {
			return nil
		}
		var videoID, channelID, thumbnailPath, filePath, dearrowPath string
		var fileSize int64
		if err := rows.Scan(&videoID, &channelID, &thumbnailPath, &filePath, &fileSize, &dearrowPath); err != nil {
			return err
		}
		for _, asset := range db.videoAssetsFromSource(videoAssetSource{
			videoID:       videoID,
			channelID:     channelID,
			thumbnailPath: thumbnailPath,
			filePath:      filePath,
			fileSize:      fileSize,
			dearrowPath:   dearrowPath,
		}) {
			asset.RequiredReason = "backfill"
			if err := run.handle(asset); err != nil {
				return err
			}
			if run.exhausted() {
				return nil
			}
		}
	}
	return rows.Err()
}

type channelProfileAssetSource struct {
	channelID string
	platform  string
	avatarURL string
	bannerURL string
}

func (db *DB) getChannelProfileAssetSource(channelID string) (channelProfileAssetSource, bool, error) {
	var src channelProfileAssetSource
	err := db.conn.QueryRow(`
		SELECT channel_id, COALESCE(platform, ''), COALESCE(avatar_url, ''), COALESCE(banner_url, '')
		FROM channel_profiles
		WHERE channel_id = ?
		  AND COALESCE(tombstone, 0) = 0
	`, channelID).Scan(&src.channelID, &src.platform, &src.avatarURL, &src.bannerURL)
	if err == sql.ErrNoRows {
		return src, false, nil
	}
	if err != nil {
		return src, false, err
	}
	return src, true, nil
}

func (db *DB) profileAssetsFromSource(src channelProfileAssetSource) []Asset {
	platform := src.platform
	if platform == "" {
		platform = videoPlatformFromChannelID(src.channelID)
	}
	if platform == "" {
		platform = strings.SplitN(src.channelID, "_", 2)[0]
	}
	assets := make([]Asset, 0, 2)
	for _, asset := range []Asset{
		{
			AssetID:        BuildManifestAssetID(platform, "channel", src.channelID, "avatar", 0),
			AssetKind:      "avatar",
			OwnerKind:      "channel",
			OwnerID:        src.channelID,
			SourceURL:      src.avatarURL,
			FilePath:       db.findAvatarRelativePath(src.channelID),
			ContentType:    "image/jpeg",
			State:          AssetStateQueued,
			RequiredReason: "retention",
		},
		{
			AssetID:        BuildManifestAssetID(platform, "channel", src.channelID, "banner", 0),
			AssetKind:      "banner",
			OwnerKind:      "channel",
			OwnerID:        src.channelID,
			SourceURL:      src.bannerURL,
			FilePath:       db.findBannerRelativePath(src.channelID),
			ContentType:    "image/jpeg",
			State:          AssetStateQueued,
			RequiredReason: "retention",
		},
	} {
		if asset.FilePath == "" && asset.SourceURL == "" {
			continue
		}
		assets = append(assets, asset)
	}
	return assets
}

func (db *DB) backfillProfileAssets(run *assetInventoryReconcileRun) error {
	rows, err := db.conn.Query(`
		SELECT channel_id, COALESCE(platform, ''), COALESCE(avatar_url, ''), COALESCE(banner_url, '')
		FROM channel_profiles
		WHERE COALESCE(tombstone, 0) = 0
		ORDER BY rowid ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		if run.exhausted() {
			return nil
		}
		var channelID, platform, avatarURL, bannerURL string
		if err := rows.Scan(&channelID, &platform, &avatarURL, &bannerURL); err != nil {
			return err
		}
		for _, asset := range db.profileAssetsFromSource(channelProfileAssetSource{
			channelID: channelID,
			platform:  platform,
			avatarURL: avatarURL,
			bannerURL: bannerURL,
		}) {
			asset.RequiredReason = "backfill"
			if err := run.handle(asset); err != nil {
				return err
			}
			if run.exhausted() {
				return nil
			}
		}
	}
	return rows.Err()
}

func (db *DB) upsertAssetFromLegacyPath(asset Asset, nowMs int64) error {
	return db.upsertBackfilledAsset(db.assetFromLegacyPath(asset), nowMs)
}

func (db *DB) upsertBackfilledAsset(asset Asset, nowMs int64) error {
	asset = normalizeAsset(asset, nowMs)
	if asset.State != AssetStateReady {
		existing, err := db.getAssetByOwnerIdentity(asset.AssetKind, asset.OwnerKind, asset.OwnerID, asset.MediaIndex)
		if err != nil {
			return err
		}
		if existing != nil && preservesAssetRetryState(*existing) {
			asset.State = existing.State
			asset.LastErrorKind = existing.LastErrorKind
			asset.LastError = existing.LastError
			asset.Attempts = existing.Attempts
			asset.NextAttemptAtMs = existing.NextAttemptAtMs
			asset.LeaseOwner = existing.LeaseOwner
			asset.LeaseUntilMs = existing.LeaseUntilMs
		}
	}
	return db.UpsertAsset(asset, nowMs)
}

func (db *DB) getAssetByOwnerIdentity(assetKind, ownerKind, ownerID string, mediaIndex int) (*Asset, error) {
	row := db.conn.QueryRow(`
		SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
		       source_url, file_path, content_type, size_bytes, sha256, state,
		       required_reason, last_error_kind, last_error, attempts,
		       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
		FROM assets
		WHERE asset_kind = ? AND owner_kind = ? AND owner_id = ? AND media_index = ?
	`, strings.TrimSpace(assetKind), strings.TrimSpace(ownerKind), strings.TrimSpace(ownerID), mediaIndex)
	asset, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func preservesAssetRetryState(asset Asset) bool {
	switch asset.State {
	case AssetStateDownloading, AssetStateFailed, AssetStatePermanentMissing:
		return true
	}
	return asset.Attempts > 0 ||
		asset.NextAttemptAtMs > 0 ||
		strings.TrimSpace(asset.LastErrorKind) != "" ||
		strings.TrimSpace(asset.LastError) != ""
}

func (db *DB) assetFromLegacyPath(asset Asset) Asset {
	path := strings.TrimSpace(asset.FilePath)
	if path == "" {
		asset.State = AssetStateQueued
		asset.SizeBytes = 0
		return asset
	}
	if info, err := os.Stat(resolveManifestDataPath(db.dataDir, path)); err == nil {
		asset.State = AssetStateReady
		asset.SizeBytes = info.Size()
		if asset.ContentType == "" {
			asset.ContentType = contentTypeForPath(path, "")
		}
		return asset
	}
	asset.State = AssetStateServerMissing
	asset.SizeBytes = 0
	return asset
}

// AssetServerURL maps an inventory row to the existing server media endpoint
// contract used by legacy manifest and Android sync asset rows.
func AssetServerURL(asset Asset) string {
	switch asset.AssetKind {
	case "avatar":
		return "/api/media/avatar/" + asset.OwnerID
	case "banner":
		return "/api/media/banner/" + asset.OwnerID
	case "post_thumbnail":
		return "/api/media/thumbnail/" + asset.OwnerID
	case "dearrow_thumbnail":
		return "/api/media/thumbnail/" + asset.OwnerID + "?da=1"
	case "video_stream":
		return "/api/media/stream/" + asset.OwnerID
	case "subtitle":
		return "/api/media/subtitle/" + asset.OwnerID
	case "post_audio":
		return "/api/media/audio/" + asset.OwnerID
	case "preview_track_json":
		return "/api/media/preview-track-json/" + asset.OwnerID
	case "preview_sprite":
		return "/api/media/preview-sprite/" + asset.OwnerID
	case "post_media":
		return fmt.Sprintf("/api/media/slide/%s/%d", asset.OwnerID, asset.MediaIndex)
	default:
		return ""
	}
}
