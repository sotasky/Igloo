package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const contentAssetWorkerOwnerSQL = `(
	(? AND a.owner_kind = 'tweet' AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail'))
	OR (a.owner_kind = 'comment_author' AND a.asset_kind = 'avatar')
	OR (a.owner_kind IN ('youtube_video', 'tiktok_video', 'instagram_reel') AND a.asset_kind = 'subtitle')
)`

const AssetStatePruned = "pruned"

func declareXContentAssetsTx(tx *sql.Tx, item model.FeedItem, nowMs int64) (bool, error) {
	item.ParseMedia()
	changed, err := declareXMediaRefsTx(tx, item.TweetID, item.Media, nowMs)
	if err != nil {
		return false, err
	}
	if item.QuoteTweetID != "" {
		quoteChanged, err := declareXMediaRefsTx(tx, item.QuoteTweetID, item.QuoteMedia, nowMs)
		return changed || quoteChanged, err
	}
	return changed, nil
}

func declareXMediaRefsTx(tx *sql.Tx, ownerID string, refs []model.MediaRef, nowMs int64) (bool, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return false, nil
	}
	changed := false
	thumbnailDeclared := false
	for index, ref := range refs {
		sourceURL := strings.TrimSpace(ref.URL)
		if !downloadableAssetSource(sourceURL) {
			continue
		}
		kind := "post_media"
		contentType := contentTypeForMediaPath(sourceURL, strings.ToLower(strings.TrimSpace(ref.Type)), "image/jpeg")
		if strings.EqualFold(strings.TrimSpace(ref.Type), "audio") {
			kind = "post_audio"
			contentType = contentTypeForMediaPath(sourceURL, "audio", "audio/mpeg")
		}
		mediaAsset := Asset{
			AssetID:        BuildAssetID("twitter", "tweet", ownerID, kind, index),
			AssetKind:      kind,
			OwnerKind:      "tweet",
			OwnerID:        ownerID,
			MediaIndex:     index,
			SourceURL:      sourceURL,
			ContentType:    contentType,
			State:          AssetStateQueued,
			DownloadLane:   DownloadLaneCurrent,
			RequiredReason: "retention",
		}
		assetChanged, err := declareSourceAssetChangeTx(tx, mediaAsset, nowMs)
		if err != nil {
			return false, err
		}
		changed = changed || assetChanged
		if thumbnailDeclared || !strings.HasPrefix(contentType, "video/") {
			continue
		}
		thumbnailDeclared = true
		mediaAsset = prepareAssetIdentity(normalizeAsset(mediaAsset, nowMs))
		assetChanged, err = declareSourceAssetChangeTx(tx, Asset{
			AssetID:        BuildAssetID("twitter", "tweet", ownerID, "post_thumbnail", 0),
			AssetKind:      "post_thumbnail",
			OwnerKind:      "tweet",
			OwnerID:        ownerID,
			ObjectKey:      VideoThumbnailObjectKey(mediaAsset.ObjectID),
			SourceURL:      sourceURL,
			ContentType:    "image/jpeg",
			State:          AssetStateQueued,
			DownloadLane:   DownloadLaneCurrent,
			RequiredReason: "retention",
		}, nowMs)
		if err != nil {
			return false, err
		}
		changed = changed || assetChanged
	}
	return changed, nil
}

func downloadableAssetSource(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// declareSourceAssetTx records desired content without demoting immutable
// ready bytes. A changed URL can supersede work that has not completed, but a
// successful capture stays authoritative instead of disappearing before a
// network call succeeds.
func declareSourceAssetTx(tx *sql.Tx, asset Asset, nowMs int64) error {
	_, err := declareSourceAssetChangeTx(tx, asset, nowMs)
	return err
}

func declareSourceAssetChangeTx(tx *sql.Tx, asset Asset, nowMs int64) (bool, error) {
	asset = normalizeAsset(asset, nowMs)
	if asset.SourceURL == "" {
		return false, nil
	}
	var existingSource, existingState string
	err := tx.QueryRow(`
		SELECT desired.source_url, desired.job_state
		FROM assets a
		JOIN media_objects desired ON desired.object_id = a.desired_object_id
		WHERE a.asset_kind = ? AND a.owner_kind = ? AND a.owner_id = ? AND a.media_index = ?
	`, asset.AssetKind, asset.OwnerKind, asset.OwnerID, asset.MediaIndex).Scan(&existingSource, &existingState)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	changed := err == sql.ErrNoRows || existingSource != asset.SourceURL
	if existingSource == asset.SourceURL && (existingState == AssetStateFailed || existingState == AssetStatePermanentMissing) {
		asset.State = existingState
	}
	return changed, upsertAssetTx(tx, asset)
}

// ClaimContentAssetDownloadBatch claims one scheduling class of content media
// and non-profile comment avatars. Channel identity assets remain owned by the
// profile pipeline.
func (db *DB) ClaimContentAssetDownloadBatch(opts LeaseOptions, includeTweets bool, lane DownloadLane) ([]Asset, error) {
	opts.StatusFrom = AssetStateQueued
	opts.StatusTo = AssetStateDownloading
	opts = normalizeLeaseOptions(opts, AssetStateQueued, AssetStateDownloading)
	if lane != DownloadLaneCurrent && lane != DownloadLaneBackfill {
		return nil, fmt.Errorf("invalid content asset lane %q", lane)
	}
	var claimed []Asset
	err := db.WithWrite(func(tx *sql.Tx) error {
		query, args := contentAssetClaimQuery(opts, includeTweets, lane)
		ids, err := claimLeasedIDsWithStateColumn(
			tx, "media_objects", "object_id", "job_state", query, args, opts,
		)
		if err != nil {
			return err
		}
		for _, id := range ids {
			asset, err := scanAsset(tx.QueryRow(`SELECT `+assetProjectionSQL+assetJoinsSQL+`
				WHERE a.desired_object_id = ?
				ORDER BY CASE WHEN a.required_reason IN ('bookmark', 'like', 'manual') THEN 0 ELSE 1 END, a.id
				LIMIT 1`, id))
			if err != nil {
				return err
			}
			claimed = append(claimed, asset)
		}
		return nil
	})
	return claimed, err
}

func contentAssetClaimQuery(opts LeaseOptions, includeTweets bool, lane DownloadLane) (string, []any) {
	query := `
		SELECT desired.object_id
		FROM media_objects desired INDEXED BY idx_media_objects_claim
		WHERE desired.download_lane = ?
		  AND desired.job_state IN ('queued', 'downloading')
		  AND desired.source_url != ''
		  AND (
		    desired.object_key NOT LIKE ?
		    OR EXISTS (
		      SELECT 1 FROM media_objects source
		      WHERE source.object_id = substr(desired.object_key, ?)
		        AND source.published_revision > 0 AND source.file_path != ''
		    )
		  )
		  AND desired.next_attempt_at_ms <= ?
		  AND (
		    (desired.job_state = 'queued' AND (desired.lease_until_ms = 0 OR desired.lease_until_ms <= ?))
		    OR (desired.job_state = 'downloading' AND desired.lease_until_ms <= ?)
		  )
		  AND EXISTS (
		    SELECT 1
		    FROM assets a INDEXED BY idx_assets_desired_object
		    WHERE a.desired_object_id = desired.object_id
		      AND a.lifecycle_state = 'active'
		      AND ` + contentAssetWorkerOwnerSQL + `
		  )
		ORDER BY desired.next_attempt_at_ms ASC, desired.attempts ASC,
		         desired.updated_at_ms DESC, desired.id DESC
		LIMIT ?`
	args := []any{
		lane, videoThumbnailObjectKeyPrefix + "%", len(videoThumbnailObjectKeyPrefix) + 1,
		opts.NowMs, opts.NowMs, opts.NowMs, includeTweets, opts.Limit,
	}
	return query, args
}

func (db *DB) RequeueXContentAssets(ownerIDs []string, includePruned bool, reason string, nowMs int64) (int, error) {
	ownerIDs = uniqueStrings(ownerIDs)
	if len(ownerIDs) == 0 {
		return 0, nil
	}
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	states := "'failed', 'permanent_missing', 'server_missing'"
	if includePruned {
		states += ", 'pruned'"
	}
	changed := 0
	for _, chunk := range stringChunks(ownerIDs, 400) {
		err := db.WithWrite(func(tx *sql.Tx) error {
			reason = strings.TrimSpace(reason)
			if _, err := tx.Exec(`
				UPDATE assets
				SET required_reason = CASE WHEN ? != '' THEN ? ELSE required_reason END,
				    lifecycle_state = 'active', revision = revision + 1, updated_at_ms = ?
				WHERE owner_kind = 'tweet' AND owner_id IN (`+placeholders(len(chunk))+`)
				  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			`, append([]any{reason, reason, nowMs}, stringsToAny(chunk)...)...); err != nil {
				return err
			}
			res, err := tx.Exec(`
				UPDATE media_objects
				SET job_state = 'queued',
				    download_lane = CASE WHEN ? IN ('bookmark', 'like', 'manual') THEN 'current' ELSE download_lane END,
				    attempts = 0, next_attempt_at_ms = 0,
				    last_error_kind = '', last_error = '',
				    lease_owner = '', lease_until_ms = 0,
				    updated_at_ms = ?
				WHERE source_url != '' AND job_state IN (`+states+`)
				  AND object_id IN (
				    SELECT desired_object_id FROM assets
				    WHERE owner_kind = 'tweet' AND owner_id IN (`+placeholders(len(chunk))+`)
				      AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
				  )
			`, append([]any{reason, nowMs}, stringsToAny(chunk)...)...)
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

func (db *DB) WakeContentAssetAuthRetriesForPlatform(platform string) (int, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	ownerKind, ok := VideoOwnerKindForPlatform(platform)
	if !ok {
		return 0, nil
	}
	assetKinds := []string{"subtitle"}
	if ownerKind == "tweet" {
		assetKinds = []string{"post_audio", "post_media", "post_thumbnail"}
	}
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE media_objects AS mo
			SET job_state = 'queued', attempts = 0, next_attempt_at_ms = 0,
			    last_error_kind = '', last_error = '', lease_owner = '', lease_until_ms = 0,
			    updated_at_ms = ?
			WHERE mo.last_error_kind = 'auth'
			  AND EXISTS (
			    SELECT 1 FROM assets a
			    WHERE a.desired_object_id = mo.object_id
			      AND a.lifecycle_state = 'active'
			      AND a.owner_kind = ?
			      AND a.asset_kind IN (`+placeholders(len(assetKinds))+`)
			  )
		`, append([]any{time.Now().UnixMilli(), ownerKind}, stringsToAny(assetKinds)...)...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		affected = int(n)
		return err
	})
	return affected, err
}

func retireUndesiredXContentObjectsTx(tx *sql.Tx, ownerIDs []string, nowMs int64) error {
	if len(ownerIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(`
		UPDATE media_objects AS mo
		SET job_state = 'pruned', attempts = 0, next_attempt_at_ms = 0,
		    last_error_kind = '', last_error = '', lease_owner = '', lease_until_ms = 0,
		    updated_at_ms = ?
		WHERE mo.object_id IN (
		  SELECT desired_object_id FROM assets
		  WHERE owner_kind = 'tweet'
		    AND owner_id IN (`+placeholders(len(ownerIDs))+`)
		    AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		)
		  AND NOT EXISTS (
		    SELECT 1 FROM assets active
		    WHERE active.desired_object_id = mo.object_id
		      AND active.lifecycle_state = 'active'
		  )
		  AND mo.job_state != 'pruned'
	`, append([]any{nowMs}, stringsToAny(ownerIDs)...)...)
	return err
}

// requireXContentAssetsForUserStateTx makes direct and quoted X content
// durable for a successful user action in the same transaction as that action.
func requireXContentAssetsForUserStateTx(tx *sql.Tx, tweetIDs []string, reason string, nowMs int64) error {
	ownerIDs, err := xContentOwnerIDsForUserStateTx(tx, tweetIDs)
	if err != nil {
		return err
	}
	for _, chunk := range stringChunks(ownerIDs, 400) {
		args := stringsToAny(chunk)
		_, err := tx.Exec(`
			UPDATE assets SET required_reason = ?, lifecycle_state = 'active', revision = revision + 1, updated_at_ms = ?
			WHERE owner_kind = 'tweet' AND owner_id IN (`+placeholders(len(chunk))+`)
			  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		`, append([]any{strings.TrimSpace(reason), nowMs}, args...)...)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`
				UPDATE media_objects
				SET job_state = 'queued', download_lane = 'current', attempts = 0, next_attempt_at_ms = 0,
			    last_error_kind = '', last_error = '', lease_owner = '', lease_until_ms = 0,
			    updated_at_ms = ?
			WHERE source_url != ''
			  AND job_state IN ('failed', 'permanent_missing', 'server_missing', 'stale', 'pruned')
			  AND object_id IN (
			    SELECT desired_object_id FROM assets
			    WHERE owner_kind = 'tweet' AND owner_id IN (`+placeholders(len(chunk))+`)
			      AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			  )
		`, append([]any{nowMs}, args...)...)
		if err != nil {
			return err
		}
	}
	return nil
}

func xContentOwnerIDsForUserStateTx(tx *sql.Tx, tweetIDs []string) ([]string, error) {
	tweetIDs = uniqueStrings(tweetIDs)
	owners := make(map[string]struct{}, len(tweetIDs))
	for _, id := range tweetIDs {
		owners[id] = struct{}{}
	}
	for _, chunk := range stringChunks(tweetIDs, 400) {
		rows, err := tx.Query(`
			SELECT COALESCE(quote_tweet_id, '')
			FROM feed_items
			WHERE tweet_id IN (`+placeholders(len(chunk))+`)
		`, stringsToAny(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var quoteID string
			if err := rows.Scan(&quoteID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if quoteID = strings.TrimSpace(quoteID); quoteID != "" {
				owners[quoteID] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return sortedKeys(owners), nil
}

// refreshXContentUserStateRequirementTx derives the diagnostic/priority reason
// from the actual bookmark and like side tables after one of those states is
// removed. The asset row does not try to model two simultaneous user reasons.
func refreshXContentUserStateRequirementTx(tx *sql.Tx, tweetIDs []string, nowMs int64) error {
	ownerIDs, err := xContentOwnerIDsForUserStateTx(tx, tweetIDs)
	if err != nil {
		return err
	}
	for _, chunk := range stringChunks(ownerIDs, 400) {
		_, err := tx.Exec(`
			UPDATE assets AS a
			SET required_reason = CASE
					WHEN EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = a.owner_id)
					  OR EXISTS (
						SELECT 1 FROM feed_items fi
						JOIN bookmarks b ON b.video_id = fi.tweet_id
						WHERE fi.quote_tweet_id = a.owner_id
					  ) THEN 'bookmark'
					WHEN EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = a.owner_id)
					  OR EXISTS (
						SELECT 1 FROM feed_items fi
						JOIN feed_likes fl ON fl.tweet_id = fi.tweet_id
						WHERE fi.quote_tweet_id = a.owner_id
					  ) THEN 'like'
					ELSE 'retention'
				END,
				updated_at_ms = ?
			WHERE a.owner_kind = 'tweet'
			  AND a.owner_id IN (`+placeholders(len(chunk))+`)
			  AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			  AND a.required_reason IN ('bookmark', 'like')
		`, append([]any{nowMs}, stringsToAny(chunk)...)...)
		if err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) MarkContentAssetPermanentMissing(assetID, assetKind, owner, kind, message string, nowMs int64) error {
	return db.markContentAssetFailure(assetID, assetKind, owner, AssetStatePermanentMissing, kind, message, nowMs)
}

func (db *DB) MarkContentAssetFailed(assetID, assetKind, owner, kind, message string, nowMs int64) error {
	return db.markContentAssetFailure(assetID, assetKind, owner, AssetStateFailed, kind, message, nowMs)
}

func (db *DB) markContentAssetFailure(assetID, assetKind, owner, state, kind, message string, nowMs int64) error {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE media_objects
			SET job_state = ?, attempts = attempts + 1,
			    next_attempt_at_ms = 0, last_error_kind = ?, last_error = ?,
			    lease_owner = '', lease_until_ms = 0, updated_at_ms = ?
			WHERE object_id = (SELECT desired_object_id FROM assets WHERE asset_id = ? AND asset_kind = ?)
			  AND job_state = 'downloading' AND lease_owner = ?
		`, state, trimJobError(kind), trimJobError(message), nowMs,
			strings.TrimSpace(assetID), strings.TrimSpace(assetKind), strings.TrimSpace(owner))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "media_objects", assetID+"/"+assetKind, owner)
	})
}
