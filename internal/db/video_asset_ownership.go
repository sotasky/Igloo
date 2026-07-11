package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

var completedVideoAssetKinds = []string{
	"video_stream",
	"post_media",
	"post_audio",
	"post_thumbnail",
}

// CompletedVideo is the content metadata committed with the producer's exact
// completed asset set. Canonical asset rows own file identity, readiness, and size.
type CompletedVideo struct {
	VideoID       string
	ChannelID     string
	OwnerKind     string
	Title         string
	Description   string
	Duration      int
	PublishedAtMs int64
	MetadataJSON  string
	MediaKind     string
	SlideCount    int
	IsTemp        bool
	SourceKind    string
	Assets        []Asset
}

// StoreCompletedVideo fingerprints every producer-supplied file before the DB
// transaction, then commits content metadata and the complete canonical asset
// set together. Assets omitted from the completed set are retired.
func (db *DB) StoreCompletedVideo(video CompletedVideo) error {
	video.VideoID = strings.TrimSpace(video.VideoID)
	video.ChannelID = strings.TrimSpace(video.ChannelID)
	video.OwnerKind = strings.TrimSpace(video.OwnerKind)
	if video.VideoID == "" {
		return fmt.Errorf("completed video id is empty")
	}
	platform, ok := videoPlatformForOwnerKind(video.OwnerKind)
	if !ok || video.OwnerKind == "tweet" {
		return fmt.Errorf("completed video %s has invalid non-X owner kind %q", video.VideoID, video.OwnerKind)
	}
	if len(video.Assets) == 0 {
		return fmt.Errorf("completed video %s has no primary media", video.VideoID)
	}
	var instagramAccounts []model.InstagramAccount
	if platform == "instagram" {
		video.MetadataJSON, instagramAccounts = sanitizeInstagramVideoMetadata(video.MetadataJSON)
	}

	prepared, newKeys, err := db.prepareCompletedVideoAssets(video, platform)
	if err != nil {
		return err
	}
	hasPrimaryMedia := false
	for _, asset := range prepared {
		if asset.MediaIndex == 0 && (asset.AssetKind == "video_stream" || asset.AssetKind == "post_media" || asset.AssetKind == "post_audio") {
			hasPrimaryMedia = true
		}
	}
	if !hasPrimaryMedia {
		return fmt.Errorf("completed video %s has no primary media asset", video.VideoID)
	}
	var retiredKeys []string
	err = db.WithWrite(func(tx *sql.Tx) error {
		if err := upsertVideoMetadataTx(tx, video); err != nil {
			return err
		}
		if err := observeInstagramVideoAccountsTx(tx, video.ChannelID, instagramAccounts, time.Now().UnixMilli()); err != nil {
			return err
		}
		var err error
		retiredKeys, err = replaceVideoAssetsTx(
			tx, prepared[0].OwnerKind, video.VideoID, completedVideoAssetKinds, prepared, "",
		)
		return err
	})
	if err != nil {
		return err
	}
	db.removeRetiredCanonicalFiles(retiredKeys, newKeys)
	return nil
}

func sanitizeInstagramVideoMetadata(raw string) (string, []model.InstagramAccount) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var metadata model.VideoMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return raw, nil
	}
	accounts := append(append([]model.InstagramAccount{}, metadata.Coauthors...), metadata.TaggedUsers...)
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return raw, accounts
	}
	for _, key := range []string{"coauthors", "tagged_users"} {
		entries, _ := document[key].([]any)
		for _, entry := range entries {
			account, _ := entry.(map[string]any)
			for _, field := range []string{"profile_pic_url", "profile_pic_url_hd", "avatar_url", "profile_image_url"} {
				delete(account, field)
			}
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return raw, accounts
	}
	return string(encoded), accounts
}

func observeInstagramVideoAccountsTx(tx *sql.Tx, primaryChannelID string, accounts []model.InstagramAccount, observedAt int64) error {
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		channelID := model.InstagramChannelIDFromHandle(account.Username)
		if channelID == "" || channelID == primaryChannelID {
			continue
		}
		if _, exists := seen[channelID]; exists {
			continue
		}
		seen[channelID] = struct{}{}
		if err := observeProfileTx(tx, profileObservation{
			channelID: channelID, platform: "instagram",
			handle: account.Username, displayName: account.FullName,
			avatarURL: account.ProfilePicURL, observedAt: observedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) prepareCompletedVideoAssets(video CompletedVideo, platform string) ([]Asset, map[string]struct{}, error) {
	nowMs := time.Now().UnixMilli()
	prepared := make([]Asset, 0, len(video.Assets))
	newKeys := make(map[string]struct{}, len(video.Assets))
	seen := make(map[string]struct{}, len(video.Assets))
	for _, input := range video.Assets {
		input.AssetKind = strings.TrimSpace(input.AssetKind)
		if !isCompletedVideoAssetKind(input.AssetKind) {
			return nil, nil, fmt.Errorf("unsupported completed video asset kind %q", input.AssetKind)
		}
		if input.MediaIndex < 0 {
			return nil, nil, fmt.Errorf("negative media index for %s", input.AssetKind)
		}
		identity := fmt.Sprintf("%s:%d", input.AssetKind, input.MediaIndex)
		if _, ok := seen[identity]; ok {
			return nil, nil, fmt.Errorf("duplicate completed video asset %s", identity)
		}
		seen[identity] = struct{}{}

		input.OwnerKind = video.OwnerKind
		input.OwnerID = video.VideoID
		input.AssetID = BuildAssetID(platform, video.OwnerKind, video.VideoID, input.AssetKind, input.MediaIndex)
		input.FilePath = strings.TrimSpace(input.FilePath)
		if input.FilePath == "" {
			return nil, nil, fmt.Errorf("empty completed video asset path for %s", identity)
		}
		if input.RequiredReason == "" {
			input.RequiredReason = "retention"
		}
		input.State = AssetStateReady
		ready, err := db.prepareReadyAssetMetadata(input, nowMs)
		if err != nil {
			return nil, nil, fmt.Errorf("fingerprint %s %s: %w", video.VideoID, identity, err)
		}
		ready = normalizeAsset(ready, nowMs)
		prepared = append(prepared, ready)
		newKeys[ready.FilePath] = struct{}{}
	}
	sort.Slice(prepared, func(i, j int) bool {
		if prepared[i].AssetKind != prepared[j].AssetKind {
			return prepared[i].AssetKind < prepared[j].AssetKind
		}
		return prepared[i].MediaIndex < prepared[j].MediaIndex
	})
	return prepared, newKeys, nil
}

func replaceVideoAssetsTx(
	tx *sql.Tx,
	ownerKind string,
	videoID string,
	kinds []string,
	assets []Asset,
	expectedStreamSHA string,
) ([]string, error) {
	existing, err := readVideoAssetsTx(tx, ownerKind, videoID)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]struct{}, len(kinds)+2)
	for _, kind := range kinds {
		selected[kind] = struct{}{}
	}
	newIdentity := make(map[string]Asset, len(assets))
	for _, asset := range assets {
		if _, ok := selected[asset.AssetKind]; !ok {
			return nil, fmt.Errorf("replacement asset kind %q is not selected", asset.AssetKind)
		}
		newIdentity[canonicalVideoAssetIdentity(asset)] = asset
	}

	oldStreamSHA, hadOldStream := primaryVideoStreamSHA(existing)
	if expectedStreamSHA != "" {
		var readyStreamSHA string
		var ready bool
		for _, asset := range existing {
			if asset.AssetKind == "video_stream" && asset.MediaIndex == 0 && asset.State == AssetStateReady {
				readyStreamSHA, ready = asset.SHA256, true
				break
			}
		}
		if !ready {
			return nil, sql.ErrNoRows
		}
		if readyStreamSHA != expectedStreamSHA {
			return nil, fmt.Errorf("preview input changed for %s", videoID)
		}
	}
	newStreamSHA, hasNewStream := primaryVideoStreamSHA(assets)
	if _, replacesStream := selected["video_stream"]; replacesStream &&
		(hadOldStream != hasNewStream || hadOldStream && oldStreamSHA != newStreamSHA) {
		for _, kind := range []string{"preview_track_json", "preview_sprite"} {
			selected[kind] = struct{}{}
		}
	}

	retired := make(map[string]struct{})
	for _, old := range existing {
		if _, ok := selected[old.AssetKind]; !ok {
			continue
		}
		replacement, keep := newIdentity[canonicalVideoAssetIdentity(old)]
		if keep {
			if old.FilePath != "" && old.FilePath != replacement.FilePath {
				retired[old.FilePath] = struct{}{}
			}
			continue
		}
		if old.FilePath != "" {
			retired[old.FilePath] = struct{}{}
		}
		if _, err := tx.Exec(`DELETE FROM assets WHERE id = ?`, old.ID); err != nil {
			return nil, err
		}
	}
	for _, asset := range assets {
		if err := upsertAssetTx(tx, asset); err != nil {
			return nil, err
		}
	}
	return sortedSet(retired), nil
}

func primaryVideoStreamSHA(assets []Asset) (string, bool) {
	for _, asset := range assets {
		if asset.AssetKind == "video_stream" && asset.MediaIndex == 0 {
			return asset.SHA256, true
		}
	}
	return "", false
}

func readVideoAssetsTx(tx *sql.Tx, ownerKind, videoID string) ([]Asset, error) {
	rows, err := tx.Query(`
		SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
		       source_url, file_path, content_type, size_bytes, sha256, file_mtime_ns, revision,
		       is_auto, audio_language, state,
		       required_reason, last_error_kind, last_error, attempts,
		       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
		FROM assets
		WHERE owner_kind = ? AND owner_id = ?
		ORDER BY id
	`, ownerKind, videoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var assets []Asset
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func canonicalVideoAssetIdentity(asset Asset) string {
	return fmt.Sprintf("%s:%d", asset.AssetKind, asset.MediaIndex)
}

func isCompletedVideoAssetKind(kind string) bool {
	for _, candidate := range completedVideoAssetKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

func upsertVideoMetadataTx(tx *sql.Tx, video CompletedVideo) error {
	if err := requireVideoOwnerKindTx(tx, video.VideoID, video.OwnerKind); err != nil {
		return err
	}
	_, err := tx.Exec(`
		INSERT INTO videos
			(video_id, channel_id, owner_kind, title, description, duration,
			 published_at, metadata_json, media_kind, slide_count, source_kind,
			 is_temp, downloaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000)
		ON CONFLICT(video_id) DO UPDATE SET
			channel_id = CASE WHEN excluded.channel_id != '' THEN excluded.channel_id ELSE videos.channel_id END,
			owner_kind = excluded.owner_kind,
			title = CASE WHEN excluded.title != '' THEN excluded.title ELSE videos.title END,
			description = CASE WHEN excluded.description != '' THEN excluded.description ELSE videos.description END,
			duration = CASE WHEN excluded.duration > 0 THEN excluded.duration ELSE videos.duration END,
			published_at = CASE WHEN excluded.published_at > 0 THEN excluded.published_at ELSE videos.published_at END,
			metadata_json = CASE WHEN excluded.metadata_json != '' THEN excluded.metadata_json ELSE videos.metadata_json END,
			media_kind = CASE WHEN excluded.media_kind != '' THEN excluded.media_kind ELSE videos.media_kind END,
			slide_count = CASE
				WHEN excluded.slide_count > 0 THEN excluded.slide_count
				WHEN excluded.media_kind = 'slideshow' THEN videos.slide_count
				ELSE excluded.slide_count
			END,
			source_kind = CASE WHEN excluded.source_kind != '' THEN excluded.source_kind ELSE COALESCE(videos.source_kind, '') END,
			is_temp = excluded.is_temp,
			downloaded_at = excluded.downloaded_at
	`, video.VideoID, video.ChannelID, video.OwnerKind, video.Title, video.Description, video.Duration,
		video.PublishedAtMs, video.MetadataJSON, video.MediaKind, video.SlideCount,
		video.SourceKind, video.IsTemp)
	return err
}

func requireVideoOwnerKindTx(tx *sql.Tx, videoID, ownerKind string) error {
	var existing string
	err := tx.QueryRow(`SELECT owner_kind FROM videos WHERE video_id = ?`, videoID).Scan(&existing)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if existing != ownerKind {
		return fmt.Errorf("video id %q is owned by %q, not %q", videoID, existing, ownerKind)
	}
	return nil
}

func readyVideoMediaExistsSQL(videoAlias string) string {
	return `EXISTS (
		SELECT 1
		FROM assets ready_video_media
		WHERE ready_video_media.owner_kind = ` + videoAlias + `.owner_kind
		  AND ready_video_media.owner_id = ` + videoAlias + `.video_id
		  AND ready_video_media.asset_kind IN ('video_stream', 'post_media', 'post_audio')
		  AND ready_video_media.state = 'ready'
		  AND ready_video_media.file_path != ''
	)`
}

// GetReadyVideoPrimaryAsset returns the preferred playable asset using the
// video's exact canonical owner identity.
func (db *DB) GetReadyVideoPrimaryAsset(videoID string) (*Asset, error) {
	row := db.conn.QueryRow(`
		SELECT a.id, a.asset_id, a.asset_kind, a.owner_kind, a.owner_id, a.media_index,
		       a.source_url, a.file_path, a.content_type, a.size_bytes, a.sha256, a.file_mtime_ns, a.revision,
		       a.is_auto, a.audio_language, a.state,
		       a.required_reason, a.last_error_kind, a.last_error, a.attempts,
		       a.next_attempt_at_ms, a.lease_owner, a.lease_until_ms, a.created_at_ms, a.updated_at_ms
		FROM videos v
		JOIN assets a
		  ON a.owner_kind = v.owner_kind
		 AND a.owner_id = v.video_id
		WHERE v.video_id = ?
		  AND a.asset_kind IN ('video_stream', 'post_media', 'post_audio')
		  AND a.media_index = 0
		  AND a.state = 'ready'
		  AND a.file_path != ''
		ORDER BY CASE a.asset_kind
		           WHEN 'video_stream' THEN 1
		           WHEN 'post_media' THEN 2
		           ELSE 3
		         END
		LIMIT 1
	`, videoID)
	asset, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

// StoreVideoPreviewAssets replaces the complete preview pair from exact paths.
func (db *DB) StoreVideoPreviewAssets(videoID, inputSHA256, trackPath, spritePath string, nowMs int64) error {
	inputSHA256 = strings.TrimSpace(inputSHA256)
	if inputSHA256 == "" {
		return fmt.Errorf("preview input fingerprint is empty")
	}
	source := "sha256:" + inputSHA256
	assets := []Asset{
		{AssetKind: "preview_track_json", FilePath: trackPath, ContentType: "application/json", SourceURL: source},
		{AssetKind: "preview_sprite", FilePath: spritePath, ContentType: "image/jpeg", SourceURL: source},
	}
	return db.storeVideoAssets(videoID, []string{"preview_track_json", "preview_sprite"}, assets, inputSHA256, nowMs)
}

// StoreVideoSubtitleAssets fingerprints and replaces the complete subtitle set
// supplied by a producer. The caller supplies exact logical storage keys and
// optional language metadata; this method performs no sibling discovery.
func (db *DB) StoreVideoSubtitleAssets(videoID string, assets []Asset, nowMs int64) error {
	assets = append([]Asset(nil), assets...)
	seen := make(map[int]struct{}, len(assets))
	for _, asset := range assets {
		if asset.AssetKind != "" && asset.AssetKind != "subtitle" {
			return fmt.Errorf("unexpected subtitle asset kind %q", asset.AssetKind)
		}
		if asset.MediaIndex < 0 {
			return fmt.Errorf("negative subtitle media index")
		}
		if _, ok := seen[asset.MediaIndex]; ok {
			return fmt.Errorf("duplicate subtitle media index %d", asset.MediaIndex)
		}
		seen[asset.MediaIndex] = struct{}{}
	}
	for i := range assets {
		assets[i].AssetKind = "subtitle"
		if assets[i].ContentType == "" {
			assets[i].ContentType = "text/vtt"
		}
	}
	return db.storeVideoAssets(videoID, []string{"subtitle"}, assets, "", nowMs)
}

func (db *DB) storeVideoAssets(videoID string, kinds []string, assets []Asset, expectedStreamSHA string, nowMs int64) error {
	videoID = strings.TrimSpace(videoID)
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	var ownerKind string
	if err := db.conn.QueryRow(`SELECT owner_kind FROM videos WHERE video_id = ?`, videoID).Scan(&ownerKind); err != nil {
		return err
	}
	platform, ok := videoPlatformForOwnerKind(ownerKind)
	if !ok || ownerKind == "tweet" {
		return fmt.Errorf("video %s has invalid non-X owner kind %q", videoID, ownerKind)
	}
	prepared := make([]Asset, 0, len(assets))
	newKeys := make(map[string]struct{}, len(assets))
	for i, asset := range assets {
		asset.OwnerKind = ownerKind
		asset.OwnerID = videoID
		asset.AssetID = BuildAssetID(platform, ownerKind, videoID, asset.AssetKind, asset.MediaIndex)
		asset.RequiredReason = "retention"
		asset.State = AssetStateReady
		ready, err := db.prepareReadyAssetMetadata(asset, nowMs)
		if err != nil {
			return fmt.Errorf("fingerprint %s asset %d: %w", videoID, i, err)
		}
		ready = normalizeAsset(ready, nowMs)
		prepared = append(prepared, ready)
		newKeys[ready.FilePath] = struct{}{}
	}
	var retired []string
	err := db.WithWrite(func(tx *sql.Tx) error {
		var err error
		retired, err = replaceVideoAssetsTx(tx, ownerKind, videoID, kinds, prepared, expectedStreamSHA)
		return err
	})
	if err != nil {
		return err
	}
	db.removeRetiredCanonicalFiles(retired, newKeys)
	return nil
}

// DeleteVideoAssetsTx deletes content and every canonical non-X asset row in a
// single transaction, returning only the exact logical keys those rows owned.
func (db *DB) DeleteVideoAssetsTx(videoID string) ([]string, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return nil, fmt.Errorf("video id is empty")
	}
	keys := map[string]struct{}{}
	err := db.WithWrite(func(tx *sql.Tx) error {
		var ownerKind string
		if err := tx.QueryRow(`SELECT owner_kind FROM videos WHERE video_id = ?`, videoID).Scan(&ownerKind); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("video not found: %s", videoID)
			}
			return err
		}
		if _, ok := videoPlatformForOwnerKind(ownerKind); !ok || ownerKind == "tweet" {
			return fmt.Errorf("video not found: %s", videoID)
		}
		rows, err := tx.Query(`
			SELECT DISTINCT file_path
			FROM assets
			WHERE owner_kind = ? AND owner_id = ?
			  AND file_path != ''
		`, ownerKind, videoID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				_ = rows.Close()
				return err
			}
			keys[key] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			DELETE FROM assets
			WHERE owner_kind = ? AND owner_id = ?
		`, ownerKind, videoID); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM videos WHERE video_id = ?`, videoID)
		return err
	})
	return sortedSet(keys), err
}

func (db *DB) removeRetiredCanonicalFiles(keys []string, keep map[string]struct{}) {
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := keep[key]; ok {
			continue
		}
		var refs int
		if err := db.conn.QueryRow(`SELECT COUNT(*) FROM assets WHERE file_path = ?`, key).Scan(&refs); err != nil || refs > 0 {
			continue
		}
		path, err := db.storage.WritePath(key)
		if err != nil {
			slog.Warn("resolve retired canonical video asset", "key", key, "err", err)
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("remove retired canonical video asset", "key", key, "err", err)
		}
	}
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
