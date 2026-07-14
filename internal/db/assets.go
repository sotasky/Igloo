package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	ID                 int64
	AssetID            string
	AssetKind          string
	OwnerKind          string
	OwnerID            string
	MediaIndex         int
	ObjectID           string
	DesiredObjectID    string
	ObjectKey          string
	StorageClass       string
	SourceURL          string
	PublishedSourceURL string
	FilePath           string
	ContentType        string
	SizeBytes          int64
	FileMtimeNs        int64
	Revision           int64
	IsAuto             *bool
	AudioLanguage      string
	State              string
	RequiredReason     string
	LastErrorKind      string
	LastError          string
	Attempts           int
	NextAttemptAtMs    int64
	LeaseOwner         string
	LeaseUntilMs       int64
	CreatedAtMs        int64
	UpdatedAtMs        int64
}

const assetProjectionSQL = `
	a.id, a.asset_id, a.asset_kind, a.owner_kind, a.owner_id, a.media_index,
	a.object_id, a.desired_object_id, desired.object_key, desired.storage_class,
	desired.source_url, current.published_source_url,
	CASE WHEN current.published_revision > 0 THEN current.file_path ELSE '' END,
	CASE WHEN current.published_revision > 0 THEN current.content_type ELSE desired.content_type END,
	CASE WHEN current.published_revision > 0 THEN current.size_bytes ELSE 0 END,
	CASE WHEN current.published_revision > 0 THEN current.file_mtime_ns ELSE 0 END,
	a.revision, a.is_auto, a.audio_language,
	CASE
		WHEN a.lifecycle_state = 'pruned' THEN 'pruned'
		WHEN current.published_revision > 0 AND current.file_path != '' THEN 'ready'
		ELSE desired.job_state
	END,
	a.required_reason, desired.last_error_kind, desired.last_error, desired.attempts,
	desired.next_attempt_at_ms, desired.lease_owner, desired.lease_until_ms,
	a.created_at_ms, MAX(a.updated_at_ms, desired.updated_at_ms)`

const assetJoinsSQL = `
	FROM assets a
	JOIN media_objects current ON current.object_id = a.object_id
	JOIN media_objects desired ON desired.object_id = a.desired_object_id`

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
			args := []any{ownerKind}
			args = append(args, stringsToAny(ownerIDs)...)
			kindClause := ""
			if len(assetKinds) > 0 {
				kindClause = " AND a.asset_kind IN (" + placeholders(len(assetKinds)) + ")"
				args = append(args, stringsToAny(assetKinds)...)
			}
			rows, err := db.reader().Query(`SELECT `+assetProjectionSQL+assetJoinsSQL+`
			WHERE current.published_revision > 0
			  AND a.lifecycle_state = 'active'
			  AND a.owner_kind = ?
			  AND a.owner_id IN (`+placeholders(len(ownerIDs))+`)`+kindClause+`
			ORDER BY a.owner_kind, a.owner_id, a.asset_kind, a.media_index, a.asset_id
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
	if asset == nil || asset.State != AssetStateReady {
		return false
	}
	publishedSource := asset.PublishedSourceURL
	if publishedSource == "" {
		publishedSource = asset.SourceURL
	}
	return publishedSource == sourceURL &&
		asset.FilePath != "" && asset.ContentType != "" && asset.SizeBytes > 0 &&
		asset.FileMtimeNs > 0
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
			SELECT a.owner_id, a.asset_kind,
			       CASE WHEN current.published_revision > 0 THEN current.content_type ELSE desired.content_type END,
			       CASE WHEN a.lifecycle_state = 'pruned' THEN 'pruned' WHEN current.published_revision > 0 AND current.file_path != '' THEN 'ready' ELSE desired.job_state END
			FROM assets a
			JOIN media_objects current ON current.object_id = a.object_id
			JOIN media_objects desired ON desired.object_id = a.desired_object_id
			WHERE a.owner_kind = 'tweet'
			  AND a.owner_id IN (`+placeholders(len(chunk))+`)
			  AND a.asset_kind IN ('post_media', 'post_audio', 'video_stream', 'post_thumbnail')
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

// StoreReadyAsset validates a completed file and publishes its canonical
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

// DeclareAsset records desired bytes without changing a currently published
// object. The media executor owns the resulting job state.
func (db *DB) DeclareAsset(asset Asset, nowMs int64) error {
	asset.State = AssetStateQueued
	asset = normalizeAsset(asset, nowMs)
	if asset.SourceURL == "" {
		return fmt.Errorf("declare asset %s: source URL is empty", asset.AssetID)
	}
	return db.WithWrite(func(tx *sql.Tx) error { return upsertAssetTx(tx, asset) })
}

func upsertAssetTx(tx *sql.Tx, asset Asset) error {
	asset = prepareAssetIdentity(asset)
	publishedRevision := int64(0)
	publicationChanged := false
	if asset.State == AssetStateReady {
		publishedRevision = 1
		var sourceURL, filePath, contentType string
		var size, mtime int64
		err := tx.QueryRow(`
			SELECT published_source_url, file_path, content_type, size_bytes, file_mtime_ns
			FROM media_objects WHERE object_key = ? AND published_revision > 0
		`, asset.ObjectKey).Scan(&sourceURL, &filePath, &contentType, &size, &mtime)
		publicationChanged = err == nil && (sourceURL != asset.SourceURL || filePath != asset.FilePath ||
			contentType != asset.ContentType || size != asset.SizeBytes || mtime != asset.FileMtimeNs)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	_, err := tx.Exec(`
		INSERT INTO media_objects (
			object_id, object_key, source_url, published_source_url, storage_class,
			desired_revision, published_revision,
			file_path, content_type, size_bytes, file_mtime_ns,
			job_state, last_error_kind, last_error, attempts,
			next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(object_key) DO UPDATE SET
			source_url = excluded.source_url,
			published_source_url = CASE WHEN excluded.published_revision > 0 THEN excluded.source_url ELSE media_objects.published_source_url END,
			storage_class = excluded.storage_class,
			desired_revision = CASE
				WHEN media_objects.source_url != excluded.source_url THEN media_objects.desired_revision + 1
				ELSE media_objects.desired_revision
			END,
			published_revision = CASE
				WHEN excluded.published_revision > 0 THEN
					CASE WHEN media_objects.source_url != excluded.source_url THEN media_objects.desired_revision + 1 ELSE media_objects.desired_revision END
				ELSE media_objects.published_revision
			END,
			file_path = CASE WHEN excluded.published_revision > 0 THEN excluded.file_path ELSE media_objects.file_path END,
			content_type = CASE WHEN excluded.published_revision > 0 OR media_objects.content_type = '' THEN excluded.content_type ELSE media_objects.content_type END,
			size_bytes = CASE WHEN excluded.published_revision > 0 THEN excluded.size_bytes ELSE media_objects.size_bytes END,
			file_mtime_ns = CASE WHEN excluded.published_revision > 0 THEN excluded.file_mtime_ns ELSE media_objects.file_mtime_ns END,
			job_state = CASE
				WHEN excluded.published_revision > 0 THEN 'ready'
				WHEN media_objects.source_url != excluded.source_url THEN 'queued'
				WHEN excluded.job_state = 'queued' AND media_objects.job_state IN ('failed', 'server_missing', 'permanent_missing', 'stale', 'pruned') THEN 'queued'
				ELSE media_objects.job_state
			END,
			last_error_kind = CASE WHEN excluded.published_revision > 0 OR media_objects.source_url != excluded.source_url OR (excluded.job_state = 'queued' AND media_objects.job_state IN ('failed', 'server_missing', 'permanent_missing', 'stale', 'pruned')) THEN '' ELSE media_objects.last_error_kind END,
			last_error = CASE WHEN excluded.published_revision > 0 OR media_objects.source_url != excluded.source_url OR (excluded.job_state = 'queued' AND media_objects.job_state IN ('failed', 'server_missing', 'permanent_missing', 'stale', 'pruned')) THEN '' ELSE media_objects.last_error END,
			attempts = CASE WHEN excluded.published_revision > 0 OR media_objects.source_url != excluded.source_url OR (excluded.job_state = 'queued' AND media_objects.job_state IN ('failed', 'server_missing', 'permanent_missing', 'stale', 'pruned')) THEN 0 ELSE media_objects.attempts END,
			next_attempt_at_ms = CASE WHEN excluded.published_revision > 0 OR media_objects.source_url != excluded.source_url OR (excluded.job_state = 'queued' AND media_objects.job_state IN ('failed', 'server_missing', 'permanent_missing', 'stale', 'pruned')) THEN 0 ELSE media_objects.next_attempt_at_ms END,
			lease_owner = CASE WHEN excluded.published_revision > 0 OR media_objects.source_url != excluded.source_url OR (excluded.job_state = 'queued' AND media_objects.job_state IN ('failed', 'server_missing', 'permanent_missing', 'stale', 'pruned')) THEN '' ELSE media_objects.lease_owner END,
			lease_until_ms = CASE WHEN excluded.published_revision > 0 OR media_objects.source_url != excluded.source_url OR (excluded.job_state = 'queued' AND media_objects.job_state IN ('failed', 'server_missing', 'permanent_missing', 'stale', 'pruned')) THEN 0 ELSE media_objects.lease_until_ms END,
			updated_at_ms = excluded.updated_at_ms
	`, asset.ObjectID, asset.ObjectKey, asset.SourceURL, asset.SourceURL, asset.StorageClass,
		publishedRevision, asset.FilePath, asset.ContentType, asset.SizeBytes, asset.FileMtimeNs,
		asset.State, asset.LastErrorKind, asset.LastError, asset.Attempts,
		asset.NextAttemptAtMs, asset.LeaseOwner, asset.LeaseUntilMs, asset.CreatedAtMs, asset.UpdatedAtMs)
	if err != nil {
		return err
	}
	var objectID string
	var objectPublishedRevision int64
	if err := tx.QueryRow(`
		SELECT object_id, published_revision FROM media_objects WHERE object_key = ?
	`, asset.ObjectKey).Scan(&objectID, &objectPublishedRevision); err != nil {
		return err
	}
	currentObjectID := objectID
	if objectPublishedRevision == 0 {
		_ = tx.QueryRow(`
			SELECT object_id FROM assets
			WHERE asset_kind = ? AND owner_kind = ? AND owner_id = ? AND media_index = ?
		`, asset.AssetKind, asset.OwnerKind, asset.OwnerID, asset.MediaIndex).Scan(&currentObjectID)
	}
	_, err = tx.Exec(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			object_id, desired_object_id, lifecycle_state, is_auto, audio_language, required_reason,
			created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?)
		ON CONFLICT(asset_kind, owner_kind, owner_id, media_index) DO UPDATE SET
			asset_id = excluded.asset_id,
			object_id = CASE WHEN ? > 0 THEN excluded.object_id ELSE assets.object_id END,
			desired_object_id = excluded.desired_object_id,
			lifecycle_state = 'active',
			is_auto = excluded.is_auto,
			audio_language = excluded.audio_language,
			required_reason = CASE
				WHEN assets.required_reason IN ('bookmark', 'like') THEN assets.required_reason
				ELSE excluded.required_reason
			END,
			revision = CASE
				WHEN assets.object_id != excluded.object_id OR assets.desired_object_id != excluded.desired_object_id
				  OR assets.is_auto IS NOT excluded.is_auto OR assets.audio_language != excluded.audio_language
				  OR assets.required_reason != excluded.required_reason
				  OR assets.lifecycle_state != 'active'
				  OR ?
				THEN assets.revision + 1 ELSE assets.revision END,
			updated_at_ms = excluded.updated_at_ms
	`, asset.AssetID, asset.AssetKind, asset.OwnerKind, asset.OwnerID, asset.MediaIndex,
		currentObjectID, objectID, asset.IsAuto, asset.AudioLanguage, asset.RequiredReason,
		asset.CreatedAtMs, asset.UpdatedAtMs, objectPublishedRevision, publicationChanged)
	return err
}

func prepareAssetIdentity(asset Asset) Asset {
	if asset.ObjectKey == "" {
		if asset.OwnerKind == "tweet" && (asset.AssetKind == "post_media" || asset.AssetKind == "post_audio" || asset.AssetKind == "post_thumbnail") && asset.SourceURL != "" {
			asset.ObjectKey = "source:" + asset.SourceURL
		} else {
			asset.ObjectKey = fmt.Sprintf("owner:%s:%s:%s:%d", asset.OwnerKind, asset.OwnerID, asset.AssetKind, asset.MediaIndex)
		}
	}
	if asset.ObjectID == "" {
		sum := sha256.Sum256([]byte(asset.ObjectKey))
		asset.ObjectID = hex.EncodeToString(sum[:])
	}
	if asset.StorageClass == "" {
		switch asset.AssetKind {
		case "avatar", "banner", "post_thumbnail", "dearrow_thumbnail", "subtitle", "preview_track_json", "preview_sprite":
			asset.StorageClass = "state_ssd"
		default:
			asset.StorageClass = "bulk_hdd"
		}
	}
	asset.DesiredObjectID = asset.ObjectID
	return asset
}

func normalizeAsset(asset Asset, nowMs int64) Asset {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	asset.AssetID = strings.TrimSpace(asset.AssetID)
	asset.AssetKind = strings.TrimSpace(asset.AssetKind)
	asset.OwnerKind = strings.TrimSpace(asset.OwnerKind)
	asset.OwnerID = strings.TrimSpace(asset.OwnerID)
	asset.ObjectID = strings.TrimSpace(asset.ObjectID)
	asset.DesiredObjectID = strings.TrimSpace(asset.DesiredObjectID)
	asset.ObjectKey = strings.TrimSpace(asset.ObjectKey)
	asset.StorageClass = strings.TrimSpace(asset.StorageClass)
	asset.SourceURL = strings.TrimSpace(asset.SourceURL)
	asset.PublishedSourceURL = strings.TrimSpace(asset.PublishedSourceURL)
	asset.FilePath = strings.TrimSpace(asset.FilePath)
	asset.ContentType = strings.TrimSpace(asset.ContentType)
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
	row := db.reader().QueryRow(`SELECT `+assetProjectionSQL+assetJoinsSQL+`
		WHERE a.asset_id = ? AND a.asset_kind = ?
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
			UPDATE media_objects
			SET published_revision = 0, job_state = ?, updated_at_ms = ?
			WHERE object_id = (SELECT object_id FROM assets WHERE asset_id = ? AND revision = ?)
			  AND published_revision > 0
			  AND file_path = ? AND size_bytes = ? AND file_mtime_ns = ?
		`, AssetStateServerMissing, nowMs, expected.AssetID, expected.Revision,
			expected.FilePath, expected.SizeBytes, expected.FileMtimeNs)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		changed = rows > 0
		if changed {
			_, err = tx.Exec(`UPDATE assets SET revision = revision + 1, updated_at_ms = ? WHERE object_id = ?`, nowMs, expected.ObjectID)
		}
		return err
	})
	return changed, err
}

func readAssetTx(tx *sql.Tx, assetID string) (Asset, error) {
	return scanAsset(tx.QueryRow(`SELECT `+assetProjectionSQL+assetJoinsSQL+`
		WHERE a.asset_id = ?
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
			UPDATE media_objects
			   SET published_revision=desired_revision,
			       published_source_url=source_url,
			       file_path=?, content_type=?, size_bytes=?, file_mtime_ns=?,
			       job_state=?, last_error_kind='', last_error='', attempts=0, next_attempt_at_ms=0,
			       lease_owner='', lease_until_ms=0, updated_at_ms=?
			 WHERE object_id=(SELECT desired_object_id FROM assets WHERE asset_id=? AND asset_kind=?)
			   AND job_state=? AND lease_owner=?
		`, strings.TrimSpace(asset.FilePath), strings.TrimSpace(asset.ContentType),
			asset.SizeBytes, asset.FileMtimeNs, AssetStateReady,
			nowMs, asset.AssetID, asset.AssetKind, AssetStateDownloading, owner)
		if err != nil {
			return err
		}
		if err := requireQueueLeaseUpdate(res, "media_objects", asset.AssetID+"/"+asset.AssetKind, owner); err != nil {
			return err
		}
		_, err = tx.Exec(`
			UPDATE assets
			SET object_id = desired_object_id, revision = revision + 1, updated_at_ms = ?
			WHERE desired_object_id = (SELECT desired_object_id FROM assets WHERE asset_id = ? AND asset_kind = ?)
		`, nowMs, asset.AssetID, asset.AssetKind)
		return err
	})
}

func (db *DB) ReleaseAssetDownload(assetID, assetKind, owner string, nowMs int64) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE media_objects
			SET job_state = ?, lease_owner = '', lease_until_ms = 0, updated_at_ms = ?
			WHERE object_id = (SELECT desired_object_id FROM assets WHERE asset_id = ? AND asset_kind = ?)
			  AND job_state = ? AND lease_owner = ?
		`, AssetStateQueued, nowMs, strings.TrimSpace(assetID), strings.TrimSpace(assetKind), AssetStateDownloading, strings.TrimSpace(owner))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "media_objects", assetID+"/"+assetKind, owner)
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
			UPDATE media_objects
			SET job_state=?, attempts=attempts+1, next_attempt_at_ms=?,
			    last_error_kind=?, last_error=?, lease_owner='', lease_until_ms=0, updated_at_ms=?
			WHERE object_id=(SELECT desired_object_id FROM assets WHERE asset_id=? AND asset_kind=?)
			  AND job_state=? AND lease_owner=?
		`, AssetStateQueued, nextMs, trimJobError(kind), trimJobError(message), nowMs,
			strings.TrimSpace(assetID), strings.TrimSpace(assetKind), AssetStateDownloading, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "media_objects", strings.TrimSpace(assetID)+"/"+strings.TrimSpace(assetKind), owner)
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
			UPDATE media_objects SET lease_until_ms=?, updated_at_ms=?
			WHERE object_id=(SELECT desired_object_id FROM assets WHERE asset_id=? AND asset_kind=?)
			  AND job_state=? AND lease_owner=?
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
	lane := storage.MediaLaneState
	if strings.HasPrefix(key, "media/") {
		lane = storage.MediaLaneBulkBackground
	}
	var removed bool
	err := db.storage.MediaExecutor().Run(context.Background(), lane, func() error {
		var err error
		removed, err = db.removeAssetFileIfUnreferenced(key)
		return err
	})
	return removed, err
}

func (db *DB) removeAssetFileIfUnreferenced(key string) (bool, error) {
	withdrawn := false
	nowMs := time.Now().UnixMilli()
	if err := db.WithWrite(func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRow(`
			SELECT COUNT(*) FROM assets a
			JOIN media_objects live ON live.object_id = a.object_id
			WHERE a.lifecycle_state = 'active' AND live.published_revision > 0 AND live.file_path = ?
		`, key).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		_, err := tx.Exec(`
			UPDATE media_objects
			SET published_revision = 0, job_state = 'pruned', updated_at_ms = ?
			WHERE file_path = ? AND published_revision > 0
		`, nowMs, key)
		if err != nil {
			return err
		}
		withdrawn = true
		return nil
	}); err != nil || !withdrawn {
		return false, err
	}
	path, err := db.storage.WritePath(key)
	if err != nil {
		return false, fmt.Errorf("resolve unreferenced asset %q: %w", key, err)
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		_ = db.WithWrite(func(tx *sql.Tx) error {
			_, restoreErr := tx.Exec(`
				UPDATE media_objects
				SET published_revision = desired_revision, job_state = 'ready', updated_at_ms = ?
				WHERE file_path = ? AND published_revision = 0 AND job_state = 'pruned'
			`, time.Now().UnixMilli(), key)
			return restoreErr
		})
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
			rows, err := db.reader().Query(`SELECT `+assetProjectionSQL+assetJoinsSQL+`
					WHERE a.owner_kind = ?
					  AND a.owner_id IN (`+placeholders(len(chunk))+`)
					  AND a.lifecycle_state != 'pruned'
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
		&asset.ObjectID,
		&asset.DesiredObjectID,
		&asset.ObjectKey,
		&asset.StorageClass,
		&asset.SourceURL,
		&asset.PublishedSourceURL,
		&asset.FilePath,
		&asset.ContentType,
		&asset.SizeBytes,
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

// prepareReadyAssetMetadata validates the completed producer output and
// captures the small metadata needed to publish it immediately.
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
	asset.ContentType, err = CanonicalAssetContentType(path, asset.FilePath, asset.AssetKind, asset.ContentType)
	if err != nil {
		return asset, err
	}
	return asset, nil
}

func (db *DB) prepareReadyAssetSetMetadata(assets []Asset, nowMs int64) ([]Asset, error) {
	prepared := make([]Asset, len(assets))
	for i := range assets {
		ready, err := db.prepareReadyAssetMetadata(assets[i], nowMs)
		if err != nil {
			return nil, err
		}
		prepared[i] = ready
	}
	return prepared, nil
}

// CanonicalAssetContentType returns the stored media type for completed bytes.
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
	row := db.reader().QueryRow(`SELECT `+assetProjectionSQL+assetJoinsSQL+`
		WHERE a.asset_kind = ? AND a.owner_kind = ? AND a.owner_id = ? AND a.media_index = ?
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
