package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/storage"
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
	FileMtimeNs     int64
	Revision        int64
	IsAuto          *bool
	AudioLanguage   string
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

type AssetOwnerRef struct {
	OwnerKind string
	OwnerID   string
}

// ListReadyAssetsForOwners selects canonical rows by exact owner identity.
// An empty assetKinds filter selects every ready asset for those owners.
func (db *DB) ListReadyAssetsForOwners(owners []AssetOwnerRef, assetKinds []string) ([]Asset, error) {
	owners = normalizeAssetOwners(owners)
	assetKinds = uniqueStrings(assetKinds)
	if len(owners) == 0 {
		return nil, nil
	}
	ownerIDsByKind := make(map[string][]string)
	for _, owner := range owners {
		ownerIDsByKind[owner.OwnerKind] = append(ownerIDsByKind[owner.OwnerKind], owner.OwnerID)
	}
	ownerKinds := make([]string, 0, len(ownerIDsByKind))
	for ownerKind := range ownerIDsByKind {
		ownerKinds = append(ownerKinds, ownerKind)
	}
	sort.Strings(ownerKinds)
	var assets []Asset
	for _, ownerKind := range ownerKinds {
		for _, ownerIDs := range stringChunks(ownerIDsByKind[ownerKind], 400) {
			args := []any{AssetStateReady, ownerKind}
			args = append(args, stringsToAny(ownerIDs)...)
			kindClause := ""
			if len(assetKinds) > 0 {
				kindClause = " AND asset_kind IN (" + placeholders(len(assetKinds)) + ")"
				args = append(args, stringsToAny(assetKinds)...)
			}
			rows, err := db.reader().Query(`
			SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
			       source_url, file_path, content_type, size_bytes, sha256, file_mtime_ns, revision,
			       is_auto, audio_language, state,
			       required_reason, last_error_kind, last_error, attempts,
			       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
			FROM assets
			WHERE state = ?
			  AND owner_kind = ?
			  AND owner_id IN (`+placeholders(len(ownerIDs))+`)`+kindClause+`
			ORDER BY owner_kind, owner_id, asset_kind, media_index, asset_id
		`, args...)
			if err != nil {
				return nil, fmt.Errorf("list ready owner assets: %w", err)
			}
			for rows.Next() {
				asset, err := scanAsset(rows)
				if err != nil {
					_ = rows.Close()
					return nil, err
				}
				assets = append(assets, asset)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if err := rows.Close(); err != nil {
				return nil, err
			}
		}
	}
	return assets, nil
}

func normalizeAssetOwners(owners []AssetOwnerRef) []AssetOwnerRef {
	seen := make(map[string]struct{}, len(owners))
	out := make([]AssetOwnerRef, 0, len(owners))
	for _, owner := range owners {
		owner.OwnerKind = strings.TrimSpace(owner.OwnerKind)
		owner.OwnerID = strings.TrimSpace(owner.OwnerID)
		key := owner.OwnerKind + "\x00" + owner.OwnerID
		if owner.OwnerKind == "" || owner.OwnerID == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, owner)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OwnerKind != out[j].OwnerKind {
			return out[i].OwnerKind < out[j].OwnerKind
		}
		return out[i].OwnerID < out[j].OwnerID
	})
	return out
}

func ReadyAssetMatchesSource(asset *Asset, sourceURL string) bool {
	return asset != nil && asset.State == AssetStateReady && asset.SourceURL == sourceURL &&
		asset.FilePath != "" && asset.ContentType != "" && asset.SizeBytes > 0 &&
		asset.SHA256 != "" && asset.FileMtimeNs > 0
}

// MediaAssetAvailability is the presentation-ready projection of canonical
// asset state. Operational downloader jobs are deliberately excluded.
type MediaAssetAvailability struct {
	Declared     bool
	ReadyMedia   bool
	ReadyVideo   bool
	ReadyPreview bool
	Pending      bool
	Failed       bool
}

// GetTweetMediaAssetAvailability projects presentation state for X content
// without allowing a colliding video owner to satisfy the row.
func (db *DB) GetTweetMediaAssetAvailability(ownerIDs []string) (map[string]MediaAssetAvailability, error) {
	out := make(map[string]MediaAssetAvailability)
	for _, chunk := range stringChunks(uniqueStrings(ownerIDs), 400) {
		if len(chunk) == 0 {
			continue
		}
		args := stringsToAny(chunk)
		rows, err := db.reader().Query(`
			SELECT owner_id, asset_kind, COALESCE(content_type, ''), state
			FROM assets
			WHERE owner_kind = 'tweet'
			  AND owner_id IN (`+placeholders(len(chunk))+`)
			  AND asset_kind IN ('post_media', 'post_audio', 'video_stream', 'post_thumbnail')
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ownerID, kind, contentType, state string
			if err := rows.Scan(&ownerID, &kind, &contentType, &state); err != nil {
				_ = rows.Close()
				return nil, err
			}
			availability := out[ownerID]
			availability.Declared = true
			switch state {
			case AssetStateReady:
				if kind == "post_thumbnail" {
					availability.ReadyPreview = true
				} else {
					availability.ReadyMedia = true
				}
				if kind == "video_stream" || strings.HasPrefix(contentType, "video/") || contentType == "image/gif" {
					availability.ReadyVideo = true
				}
			case AssetStateQueued, AssetStateDownloading, AssetStateStale:
				availability.Pending = true
			case AssetStateFailed, AssetStatePermanentMissing:
				availability.Failed = true
			}
			out[ownerID] = availability
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// StoreReadyAsset fingerprints a completed file and publishes its canonical
// inventory row. Producers call this after the final same-filesystem rename.
func (db *DB) StoreReadyAsset(asset Asset, nowMs int64) error {
	asset.State = AssetStateReady
	prepared, err := db.prepareReadyAssetMetadata(asset, nowMs)
	if err != nil {
		return err
	}
	prepared = normalizeAsset(prepared, nowMs)
	return db.WithWrite(func(tx *sql.Tx) error {
		return upsertAssetTx(tx, prepared)
	})
}

func upsertAssetTx(tx *sql.Tx, asset Asset) error {
	_, err := tx.Exec(`
			INSERT INTO assets (
				asset_id, asset_kind, owner_kind, owner_id, media_index,
				source_url, file_path, content_type, size_bytes, sha256, file_mtime_ns,
				is_auto, audio_language, state,
				required_reason, last_error_kind, last_error, attempts,
				next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(asset_kind, owner_kind, owner_id, media_index) DO UPDATE SET
				asset_id = excluded.asset_id,
				source_url = excluded.source_url,
				file_path = excluded.file_path,
				content_type = excluded.content_type,
				size_bytes = excluded.size_bytes,
				sha256 = excluded.sha256,
				file_mtime_ns = excluded.file_mtime_ns,
				is_auto = excluded.is_auto,
				audio_language = excluded.audio_language,
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
		asset.SourceURL, asset.FilePath, asset.ContentType, asset.SizeBytes, asset.SHA256, asset.FileMtimeNs,
		asset.IsAuto, asset.AudioLanguage, asset.State,
		asset.RequiredReason, asset.LastErrorKind, asset.LastError, asset.Attempts,
		asset.NextAttemptAtMs, asset.LeaseOwner, asset.LeaseUntilMs, asset.CreatedAtMs, asset.UpdatedAtMs)
	return err
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
	asset.AudioLanguage = strings.TrimSpace(asset.AudioLanguage)
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
	row := db.reader().QueryRow(`
		SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
		       source_url, file_path, content_type, size_bytes, sha256, file_mtime_ns, revision,
		       is_auto, audio_language, state,
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

// MarkReadyAssetUnavailable withdraws an exact published file identity after
// its recorded bytes are found missing or changed. The conditional update
// cannot overwrite a concurrently published or revalidated replacement.
func (db *DB) MarkReadyAssetUnavailable(expected Asset, nowMs int64) (bool, error) {
	expected.AssetID = strings.TrimSpace(expected.AssetID)
	if expected.AssetID == "" || expected.Revision <= 0 {
		return false, nil
	}
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	changed := false
	err := db.WithWrite(func(tx *sql.Tx) error {
		result, err := tx.Exec(`
			UPDATE assets
			SET state = ?, updated_at_ms = ?
			WHERE asset_id = ? AND revision = ? AND state = ?
			  AND file_path = ? AND size_bytes = ? AND file_mtime_ns = ? AND sha256 = ?
		`, AssetStateServerMissing, nowMs, expected.AssetID, expected.Revision, AssetStateReady,
			expected.FilePath, expected.SizeBytes, expected.FileMtimeNs, expected.SHA256)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		changed = rows > 0
		return nil
	})
	return changed, err
}

func readAssetTx(tx *sql.Tx, assetID string) (Asset, error) {
	return scanAsset(tx.QueryRow(`
		SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
		       source_url, file_path, content_type, size_bytes, sha256, file_mtime_ns, revision,
		       is_auto, audio_language, state,
		       required_reason, last_error_kind, last_error, attempts,
		       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
		FROM assets
		WHERE asset_id = ?
	`, assetID))
}

// CompleteAssetDownload publishes a file for a content asset claimed by its
// producer-owned queue.
func (db *DB) CompleteAssetDownload(asset Asset, owner string, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	asset.AssetID = strings.TrimSpace(asset.AssetID)
	asset.AssetKind = strings.TrimSpace(asset.AssetKind)
	var err error
	asset, err = db.prepareReadyAssetMetadata(asset, nowMs)
	if err != nil {
		return err
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE assets
			   SET state=?, file_path=?, content_type=?, size_bytes=?, sha256=?, file_mtime_ns=?,
			       last_error_kind='', last_error='', attempts=0, next_attempt_at_ms=0,
			       lease_owner='', lease_until_ms=0, updated_at_ms=?
			 WHERE asset_id=? AND asset_kind=? AND state=? AND lease_owner=?
		`, AssetStateReady, strings.TrimSpace(asset.FilePath), strings.TrimSpace(asset.ContentType),
			asset.SizeBytes, strings.TrimSpace(asset.SHA256), asset.FileMtimeNs, nowMs,
			asset.AssetID, asset.AssetKind, AssetStateDownloading, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "assets", asset.AssetID+"/"+asset.AssetKind, owner)
	})
}

func (db *DB) ReleaseAssetDownload(assetID, assetKind, owner string, nowMs int64) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE assets
			SET state = ?, lease_owner = '', lease_until_ms = 0, updated_at_ms = ?
			WHERE asset_id = ? AND asset_kind = ? AND state = ? AND lease_owner = ?
		`, AssetStateQueued, nowMs, strings.TrimSpace(assetID), strings.TrimSpace(assetKind), AssetStateDownloading, strings.TrimSpace(owner))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "assets", assetID+"/"+assetKind, owner)
	})
}

func (db *DB) RetryAssetDownload(assetID, assetKind, owner, kind, message string, delay time.Duration, nowMs int64) error {
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
			SET state=?, attempts=attempts+1, next_attempt_at_ms=?,
			    last_error_kind=?, last_error=?, lease_owner='', lease_until_ms=0, updated_at_ms=?
			WHERE asset_id=? AND asset_kind=? AND state=? AND lease_owner=?
		`, AssetStateQueued, nextMs, trimJobError(kind), trimJobError(message), nowMs,
			strings.TrimSpace(assetID), strings.TrimSpace(assetKind), AssetStateDownloading, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "assets", strings.TrimSpace(assetID)+"/"+strings.TrimSpace(assetKind), owner)
	})
}

func (db *DB) RenewAssetDownloadLease(assetID, assetKind, owner string, nowMs int64, lease time.Duration) error {
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
			UPDATE assets SET lease_until_ms=?, updated_at_ms=?
			WHERE asset_id=? AND asset_kind=? AND state=? AND lease_owner=?
		`, nowMs+lease.Milliseconds(), nowMs, strings.TrimSpace(assetID), strings.TrimSpace(assetKind), AssetStateDownloading, owner)
		return err
	})
}

// RemoveAssetFileIfUnreferenced deletes a canonical file only after every
// ready inventory row has stopped referencing its key.
func (db *DB) RemoveAssetFileIfUnreferenced(key string) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return false, nil
	}
	var references int
	if err := db.reader().QueryRow(`SELECT COUNT(*) FROM assets WHERE file_path = ? AND state = 'ready'`, key).Scan(&references); err != nil {
		return false, err
	}
	if references > 0 {
		return false, nil
	}
	path, err := db.storage.WritePath(key)
	if err != nil {
		return false, fmt.Errorf("resolve unreferenced asset %q: %w", key, err)
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListAndroidSyncAssetInventoryRows returns inventory rows for the desired owners.
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
	for _, id := range sets.SortedTweetAssetOwners() {
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
		addOwner(videoOwnerKinds[id], id)
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
			rows, err := db.reader().Query(`
				SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
				       source_url, file_path, content_type, size_bytes, sha256, file_mtime_ns, revision,
				       is_auto, audio_language, state,
				       required_reason, last_error_kind, last_error, attempts,
				       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
					FROM assets
					WHERE owner_kind = ?
					  AND owner_id IN (`+placeholders(len(chunk))+`)
					  AND state != 'pruned'
			`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				asset, err := scanAsset(rows)
				if err != nil {
					_ = rows.Close()
					return nil, err
				}
				out = append(out, asset)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
			_ = rows.Close()
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
		rows, err := db.reader().Query(`
			SELECT v.video_id, v.owner_kind
			FROM videos v
			WHERE v.video_id IN (`+placeholders(len(chunk))+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var videoID, ownerKind string
			if err := rows.Scan(&videoID, &ownerKind); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, ok := videoPlatformForOwnerKind(ownerKind); ok {
				out[videoID] = ownerKind
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
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
		&asset.FileMtimeNs,
		&asset.Revision,
		&asset.IsAuto,
		&asset.AudioLanguage,
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

// prepareReadyAssetMetadata fingerprints a completed file once. Repeated
// declarations reuse the stored checksum while path, size, and mtime agree.
func (db *DB) prepareReadyAssetMetadata(asset Asset, nowMs int64) (Asset, error) {
	asset = normalizeAsset(asset, nowMs)
	path, err := db.storage.WritePath(asset.FilePath)
	if err != nil {
		return asset, fmt.Errorf("resolve ready asset path %q: %w", asset.FilePath, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return asset, err
	}
	if !info.Mode().IsRegular() {
		return asset, fmt.Errorf("asset path is not a regular file: %s", asset.FilePath)
	}
	asset.State = AssetStateReady
	asset.SizeBytes = info.Size()
	asset.FileMtimeNs = info.ModTime().UnixNano()
	if asset.SizeBytes <= 0 {
		return asset, fmt.Errorf("ready asset is empty: %s", asset.FilePath)
	}
	// The canonical file API owns the checksum. Never trust a checksum supplied
	// by a caller for bytes that this process can fingerprint itself.
	asset.SHA256 = ""

	var existing *Asset
	if asset.AssetID != "" && asset.AssetKind != "" {
		existing, err = db.GetAsset(asset.AssetID, asset.AssetKind)
	} else {
		existing, err = db.GetAssetByOwnerIdentity(asset.AssetKind, asset.OwnerKind, asset.OwnerID, asset.MediaIndex)
	}
	if err != nil {
		return asset, err
	}
	if existing != nil && existing.State == AssetStateReady && existing.SHA256 != "" && existing.ContentType != "" &&
		existing.FilePath == asset.FilePath &&
		existing.SizeBytes == asset.SizeBytes &&
		existing.FileMtimeNs == asset.FileMtimeNs {
		if err := requireSameAssetFile(path, info); err != nil {
			return asset, err
		}
		asset.ContentType = existing.ContentType
		asset.SHA256 = existing.SHA256
		return asset, nil
	}
	durability := db.readyAssetDurability
	if durability == nil {
		durability = makeReadyAssetDurable
	}
	if err := durability(path); err != nil {
		return asset, fmt.Errorf("make ready asset durable %q: %w", asset.FilePath, err)
	}
	info, err = os.Stat(path)
	if err != nil {
		return asset, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return asset, fmt.Errorf("ready asset is not a non-empty regular file: %s", asset.FilePath)
	}
	asset.SizeBytes = info.Size()
	asset.FileMtimeNs = info.ModTime().UnixNano()
	asset.ContentType, err = CanonicalAssetContentType(path, asset.FilePath, asset.AssetKind, asset.ContentType)
	if err != nil {
		return asset, err
	}

	size, mtimeNs, sum, err := fingerprintAssetFile(path)
	if err != nil {
		return asset, err
	}
	asset.SizeBytes = size
	asset.FileMtimeNs = mtimeNs
	asset.SHA256 = sum
	return asset, nil
}

// CanonicalAssetContentType returns the stored media type for verified bytes.
// Strong byte signatures override declarations and filename extensions.
func CanonicalAssetContentType(filePath, storageKey, assetKind, declared string) (string, error) {
	detectedType, err := sniffAssetContentType(filePath, assetKind)
	if err != nil {
		return "", err
	}
	contentType := detectedType
	if contentType == "" {
		contentType = strings.TrimSpace(declared)
	}
	if contentType == "" {
		contentType = contentTypeForPath(storageKey, "")
	}
	contentType, _, err = mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("ready asset %s has invalid content type %q", storageKey, declared)
	}
	if contentType == "audio/x-m4a" {
		contentType = "audio/mp4"
	}
	if contentType == "" || contentType == "application/octet-stream" || contentType == "text/html" {
		return "", fmt.Errorf("ready asset %s has invalid content type %q", storageKey, contentType)
	}
	return contentType, nil
}

func sniffAssetContentType(path, assetKind string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	buf = buf[:n]
	if len(buf) >= 12 && string(buf[:4]) == "RIFF" && string(buf[8:12]) == "WEBP" {
		return "image/webp", nil
	}
	if len(buf) >= 8 && string(buf[4:8]) == "ftyp" {
		if assetKind == "post_audio" {
			return "audio/mp4", nil
		}
		return "video/mp4", nil
	}
	if len(buf) >= 4 && buf[0] == 0x1a && buf[1] == 0x45 && buf[2] == 0xdf && buf[3] == 0xa3 {
		if assetKind == "post_audio" {
			return "audio/webm", nil
		}
		return "video/webm", nil
	}
	if len(buf) >= 4 && string(buf[:4]) == "OggS" {
		return "audio/ogg", nil
	}
	if len(buf) >= 3 && string(buf[:3]) == "ID3" {
		return "audio/mpeg", nil
	}
	if len(buf) >= 6 && string(buf[:6]) == "WEBVTT" {
		return "text/vtt", nil
	}
	detected := http.DetectContentType(buf)
	switch detected {
	case "image/jpeg", "image/png", "image/gif":
		return detected, nil
	case "text/html; charset=utf-8":
		return "text/html", nil
	default:
		return "", nil
	}
}

func fingerprintAssetFile(path string) (int64, int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, "", err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil {
		return 0, 0, "", err
	}
	if !before.Mode().IsRegular() {
		return 0, 0, "", fmt.Errorf("asset path is not a regular file: %s", path)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, 0, "", err
	}
	after, err := f.Stat()
	if err != nil {
		return 0, 0, "", err
	}
	if before.Size() != after.Size() || before.ModTime() != after.ModTime() || n != after.Size() {
		return 0, 0, "", fmt.Errorf("asset changed while fingerprinting: %s", path)
	}
	if err := requireSameAssetFile(path, after); err != nil {
		return 0, 0, "", err
	}
	return n, after.ModTime().UnixNano(), hex.EncodeToString(h.Sum(nil)), nil
}

func requireSameAssetFile(path string, expected os.FileInfo) error {
	current, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, current) || expected.Size() != current.Size() || expected.ModTime() != current.ModTime() {
		return fmt.Errorf("asset changed while preparing metadata: %s", path)
	}
	return nil
}

func makeReadyAssetDurable(path string) error {
	return makeReadyAssetDurableWith(path, func(file *os.File) error {
		return file.Sync()
	}, storage.SyncDirectory)
}

func makeReadyAssetDurableWith(path string, syncFile func(*os.File) error, syncDirectory func(string) error) error {
	if syncFile == nil || syncDirectory == nil {
		return fmt.Errorf("asset durability operations are required")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	closeWith := func(err error) error { return errors.Join(err, file.Close()) }
	before, err := file.Stat()
	if err != nil {
		return closeWith(err)
	}
	if !before.Mode().IsRegular() {
		return closeWith(fmt.Errorf("asset path is not a regular file: %s", path))
	}
	if err := syncFile(file); err != nil {
		return closeWith(err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return closeWith(err)
	}
	after, err := file.Stat()
	if err != nil {
		return closeWith(err)
	}
	if before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return closeWith(fmt.Errorf("asset changed while making durable: %s", path))
	}
	if err := requireSameAssetFile(path, after); err != nil {
		return closeWith(err)
	}
	return file.Close()
}

// BuildAssetID returns the stable public identity for one canonical asset.
func BuildAssetID(platform, ownerKind, ownerID, assetKind string, index int) string {
	id := platform + "_" + ownerKind + "_" + ownerID + "_" + assetKind
	if index > 0 {
		id = fmt.Sprintf("%s_%d", id, index)
	}
	return id
}

func contentTypeForMediaPath(path, mediaType, fallback string) string {
	if contentType := contentTypeForPath(path, ""); contentType != "" {
		return contentType
	}
	if mediaType == "video" || mediaType == "gif" {
		return "video/mp4"
	}
	return fallback
}

func contentTypeForPath(path, fallback string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".image":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".mov":
		return "video/quicktime"
	case ".m4v":
		return "video/x-m4v"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".aac", ".ogg":
		return "audio/mp4"
	case ".vtt":
		return "text/vtt"
	case ".json":
		return "application/json"
	default:
		return fallback
	}
}

// VideoOwnerKindForPlatform translates an exact source platform into the
// canonical owner kind stored by video producers. Unknown platforms fail
// closed instead of being treated as YouTube.
func VideoOwnerKindForPlatform(platform string) (string, bool) {
	switch platform {
	case "twitter":
		return "tweet", true
	case "youtube":
		return "youtube_video", true
	case "tiktok":
		return "tiktok_video", true
	case "instagram":
		return "instagram_reel", true
	default:
		return "", false
	}
}

func videoPlatformForOwnerKind(ownerKind string) (string, bool) {
	switch ownerKind {
	case "tweet":
		return "twitter", true
	case "youtube_video":
		return "youtube", true
	case "tiktok_video":
		return "tiktok", true
	case "instagram_reel":
		return "instagram", true
	default:
		return "", false
	}
}

func IsCanonicalVideoOwnerKind(ownerKind string) bool {
	_, ok := videoPlatformForOwnerKind(ownerKind)
	return ok
}

func (db *DB) GetAssetByOwnerIdentity(assetKind, ownerKind, ownerID string, mediaIndex int) (*Asset, error) {
	row := db.reader().QueryRow(`
		SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
		       source_url, file_path, content_type, size_bytes, sha256, file_mtime_ns, revision,
		       is_auto, audio_language, state,
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
