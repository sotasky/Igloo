package db

import (
	"sort"

	"github.com/screwys/igloo/internal/model"
)

// AndroidSyncCommentAuthorAsset is a canonical commenter avatar selected for
// comments attached to a retained YouTube video.
type AndroidSyncCommentAuthorAsset struct {
	Asset     Asset
	RecencyMs int64
}

// ListAndroidSyncCommentAuthorAssets returns canonical comment-author avatars
// for the same top comments included in Android video payloads. Commenters stay
// out of channel/profile identity; callers may omit a row when the same owner
// already has a selected channel avatar.
func (db *DB) ListAndroidSyncCommentAuthorAssets(videoIDs []string, limitPerVideo int) ([]AndroidSyncCommentAuthorAsset, error) {
	if limitPerVideo <= 0 || len(videoIDs) == 0 {
		return nil, nil
	}

	recencyByOwner := make(map[string]int64)
	for _, chunk := range stringChunks(videoIDs, 400) {
		args := make([]any, 0, len(chunk)+1)
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, limitPerVideo)
		rows, err := db.reader().Query(`
			WITH ranked AS (
				SELECT
					COALESCE(vc.author_id, '') AS author_id,
					COALESCE(NULLIF(vc.published_at, 0), NULLIF(v.published_at, 0), 0) AS recency_ms,
					ROW_NUMBER() OVER (
						PARTITION BY vc.video_id
						ORDER BY COALESCE(vc.like_count, 0) DESC, vc.comment_id ASC
					) AS video_rank
				FROM video_comments vc
				JOIN videos v ON v.video_id = vc.video_id
				WHERE vc.video_id IN (`+placeholders(len(chunk))+`)
				  AND v.channel_id LIKE 'youtube_%'
			)
			SELECT author_id, recency_ms
			FROM ranked
			WHERE video_rank <= ?
		`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var authorID string
			var recencyMs int64
			if err := rows.Scan(&authorID, &recencyMs); err != nil {
				_ = rows.Close()
				return nil, err
			}
			ownerID := model.YouTubeCommentAuthorChannelID(authorID)
			if ownerID == "" {
				continue
			}
			if current, ok := recencyByOwner[ownerID]; !ok || recencyMs > current {
				recencyByOwner[ownerID] = recencyMs
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	if len(recencyByOwner) == 0 {
		return nil, nil
	}

	owners := make([]string, 0, len(recencyByOwner))
	for ownerID := range recencyByOwner {
		owners = append(owners, ownerID)
	}
	sort.Strings(owners)

	var out []AndroidSyncCommentAuthorAsset
	for _, chunk := range stringChunks(owners, 400) {
		args := make([]any, 0, len(chunk)+2)
		args = append(args, "avatar", "comment_author")
		for _, ownerID := range chunk {
			args = append(args, ownerID)
		}
		rows, err := db.reader().Query(`
			SELECT id, asset_id, asset_kind, owner_kind, owner_id, media_index,
			       source_url, file_path, content_type, size_bytes, sha256, file_mtime_ns, revision,
			       is_auto, audio_language, state,
			       required_reason, last_error_kind, last_error, attempts,
			       next_attempt_at_ms, lease_owner, lease_until_ms, created_at_ms, updated_at_ms
				FROM assets
				WHERE asset_kind = ?
				  AND owner_kind = ?
				  AND state != 'pruned'
				  AND owner_id IN (`+placeholders(len(chunk))+`)
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
			out = append(out, AndroidSyncCommentAuthorAsset{
				Asset:     asset,
				RecencyMs: recencyByOwner[asset.OwnerID],
			})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].RecencyMs != out[j].RecencyMs {
			return out[i].RecencyMs > out[j].RecencyMs
		}
		return out[i].Asset.AssetID < out[j].Asset.AssetID
	})
	return out, nil
}
