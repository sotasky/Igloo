package db

import (
	"database/sql"
	"net/url"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const AssetStatePruned = "pruned"

func declareXContentAssetsTx(tx *sql.Tx, item model.FeedItem, nowMs int64) error {
	item.ParseMedia()
	if err := declareXMediaRefsTx(tx, item.TweetID, item.Media, nowMs); err != nil {
		return err
	}
	if item.QuoteTweetID != "" {
		return declareXMediaRefsTx(tx, item.QuoteTweetID, item.QuoteMedia, nowMs)
	}
	return nil
}

func declareXMediaRefsTx(tx *sql.Tx, ownerID string, refs []model.MediaRef, nowMs int64) error {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return nil
	}
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
		if err := declareSourceAssetTx(tx, Asset{
			AssetID:        BuildAssetID("twitter", "tweet", ownerID, kind, index),
			AssetKind:      kind,
			OwnerKind:      "tweet",
			OwnerID:        ownerID,
			MediaIndex:     index,
			SourceURL:      sourceURL,
			ContentType:    contentType,
			State:          AssetStateQueued,
			RequiredReason: "retention",
		}, nowMs); err != nil {
			return err
		}
		if thumbnailDeclared || (ref.Type != "video" && ref.Type != "gif") || !downloadableAssetSource(ref.ThumbnailURL) {
			continue
		}
		thumbnailDeclared = true
		if err := declareSourceAssetTx(tx, Asset{
			AssetID:        BuildAssetID("twitter", "tweet", ownerID, "post_thumbnail", 0),
			AssetKind:      "post_thumbnail",
			OwnerKind:      "tweet",
			OwnerID:        ownerID,
			SourceURL:      strings.TrimSpace(ref.ThumbnailURL),
			ContentType:    "image/jpeg",
			State:          AssetStateQueued,
			RequiredReason: "retention",
		}, nowMs); err != nil {
			return err
		}
	}
	return nil
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
	asset = normalizeAsset(asset, nowMs)
	if asset.SourceURL == "" {
		return nil
	}
	_, err := tx.Exec(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			source_url, content_type, state, required_reason,
			created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_kind, owner_kind, owner_id, media_index) DO UPDATE SET
			asset_id = excluded.asset_id,
			source_url = CASE
				WHEN assets.state = 'ready' THEN assets.source_url
				ELSE excluded.source_url
			END,
			content_type = CASE
				WHEN assets.content_type = '' THEN excluded.content_type
				ELSE assets.content_type
			END,
			required_reason = CASE
				WHEN assets.required_reason IN ('bookmark', 'like') THEN assets.required_reason
				ELSE excluded.required_reason
			END,
			state = CASE
				WHEN assets.state != 'ready' AND assets.source_url != excluded.source_url THEN 'queued'
				ELSE assets.state
			END,
			last_error_kind = CASE WHEN assets.state != 'ready' AND assets.source_url != excluded.source_url THEN '' ELSE assets.last_error_kind END,
			last_error = CASE WHEN assets.state != 'ready' AND assets.source_url != excluded.source_url THEN '' ELSE assets.last_error END,
			attempts = CASE WHEN assets.state != 'ready' AND assets.source_url != excluded.source_url THEN 0 ELSE assets.attempts END,
			next_attempt_at_ms = CASE WHEN assets.state != 'ready' AND assets.source_url != excluded.source_url THEN 0 ELSE assets.next_attempt_at_ms END,
			lease_owner = CASE WHEN assets.state != 'ready' AND assets.source_url != excluded.source_url THEN '' ELSE assets.lease_owner END,
			lease_until_ms = CASE WHEN assets.state != 'ready' AND assets.source_url != excluded.source_url THEN 0 ELSE assets.lease_until_ms END,
			updated_at_ms = CASE
				WHEN (assets.state != 'ready' AND assets.source_url != excluded.source_url)
				  OR assets.required_reason != excluded.required_reason
				THEN excluded.updated_at_ms
				ELSE assets.updated_at_ms
			END
	`, asset.AssetID, asset.AssetKind, asset.OwnerKind, asset.OwnerID, asset.MediaIndex,
		asset.SourceURL, asset.ContentType, asset.State, asset.RequiredReason,
		asset.CreatedAtMs, asset.UpdatedAtMs)
	return err
}

// ClaimContentAssetDownloadBatch claims content media and non-profile comment
// avatars. Channel identity assets remain owned by the profile pipeline.
func (db *DB) ClaimContentAssetDownloadBatch(opts LeaseOptions) ([]Asset, error) {
	opts = normalizeLeaseOptions(opts, AssetStateQueued, AssetStateDownloading)
	var claimed []Asset
	err := db.WithWrite(func(tx *sql.Tx) error {
		query := `
			SELECT asset_id
			FROM assets
			WHERE ` + leaseEligibleSQLFor("state", "next_attempt_at_ms", "lease_until_ms") + `
			  AND source_url != ''
			  AND (
			    (owner_kind = 'tweet' AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail'))
			    OR (owner_kind = 'comment_author' AND asset_kind = 'avatar')
			  )
			ORDER BY CASE WHEN required_reason IN ('bookmark', 'like') THEN 0 ELSE 1 END,
			         attempts ASC, updated_at_ms DESC, id DESC
			LIMIT ?`
		ids, err := claimLeasedIDsWithStateColumn(tx, "assets", "asset_id", "state", query, []any{
			opts.NowMs, opts.StatusFrom, opts.NowMs, opts.StatusTo, opts.NowMs, opts.Limit,
		}, opts)
		if err != nil {
			return err
		}
		for _, id := range ids {
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
			res, err := tx.Exec(`
				UPDATE assets
				SET state = 'queued',
				    required_reason = CASE WHEN ? != '' THEN ? ELSE required_reason END,
				    attempts = 0, next_attempt_at_ms = 0,
				    last_error_kind = '', last_error = '',
				    lease_owner = '', lease_until_ms = 0,
				    updated_at_ms = ?
				WHERE owner_kind = 'tweet'
				  AND owner_id IN (`+placeholders(len(chunk))+`)
				  AND source_url != ''
				  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
				  AND state IN (`+states+`)
			`, append([]any{strings.TrimSpace(reason), strings.TrimSpace(reason), nowMs}, stringsToAny(chunk)...)...)
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

// requireXContentAssetsForUserStateTx makes direct and quoted X content
// durable for a successful user action in the same transaction as that action.
func requireXContentAssetsForUserStateTx(tx *sql.Tx, tweetIDs []string, reason string, nowMs int64) error {
	ownerIDs, err := xContentOwnerIDsForUserStateTx(tx, tweetIDs)
	if err != nil {
		return err
	}
	for _, chunk := range stringChunks(ownerIDs, 400) {
		_, err := tx.Exec(`
			UPDATE assets
			SET required_reason = ?,
			    state = CASE
			      WHEN state IN ('failed', 'permanent_missing', 'server_missing', 'stale', 'pruned') THEN 'queued'
			      ELSE state
			    END,
			    attempts = CASE
			      WHEN state IN ('failed', 'permanent_missing', 'server_missing', 'stale', 'pruned') THEN 0
			      ELSE attempts
			    END,
			    next_attempt_at_ms = CASE
			      WHEN state IN ('failed', 'permanent_missing', 'server_missing', 'stale', 'pruned') THEN 0
			      ELSE next_attempt_at_ms
			    END,
			    last_error_kind = CASE
			      WHEN state IN ('failed', 'permanent_missing', 'server_missing', 'stale', 'pruned') THEN ''
			      ELSE last_error_kind
			    END,
			    last_error = CASE
			      WHEN state IN ('failed', 'permanent_missing', 'server_missing', 'stale', 'pruned') THEN ''
			      ELSE last_error
			    END,
			    lease_owner = CASE
			      WHEN state IN ('failed', 'permanent_missing', 'server_missing', 'stale', 'pruned') THEN ''
			      ELSE lease_owner
			    END,
			    lease_until_ms = CASE
			      WHEN state IN ('failed', 'permanent_missing', 'server_missing', 'stale', 'pruned') THEN 0
			      ELSE lease_until_ms
			    END,
			    updated_at_ms = ?
			WHERE owner_kind = 'tweet'
			  AND owner_id IN (`+placeholders(len(chunk))+`)
			  AND source_url != ''
			  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		`, append([]any{strings.TrimSpace(reason), nowMs}, stringsToAny(chunk)...)...)
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
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE assets
			SET state = 'permanent_missing', attempts = attempts + 1,
			    next_attempt_at_ms = 0, last_error_kind = ?, last_error = ?,
			    lease_owner = '', lease_until_ms = 0, updated_at_ms = ?
			WHERE asset_id = ? AND asset_kind = ?
			  AND state = 'downloading' AND lease_owner = ?
		`, trimJobError(kind), trimJobError(message), nowMs,
			strings.TrimSpace(assetID), strings.TrimSpace(assetKind), strings.TrimSpace(owner))
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "assets", assetID+"/"+assetKind, owner)
	})
}
