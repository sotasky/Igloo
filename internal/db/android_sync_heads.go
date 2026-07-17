package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

const androidSyncHeadPageCap = 2000

type AndroidSyncClock struct {
	Epoch    string
	Revision int64
}

func (db *DB) GetAndroidSyncClock() (AndroidSyncClock, error) {
	var clock AndroidSyncClock
	err := db.reader().QueryRow(`
		SELECT epoch, revision
		FROM android_sync_clock
		WHERE id = 1
	`).Scan(&clock.Epoch, &clock.Revision)
	return clock, err
}

func (db *DB) ListAndroidSyncHeads(afterRevision int64, limit int) ([]model.AndroidSyncHead, error) {
	return db.ListAndroidSyncHeadsThrough(afterRevision, -1, limit)
}

func (db *DB) ListAndroidSyncHeadsThrough(afterRevision, throughRevision int64, limit int) ([]model.AndroidSyncHead, error) {
	if afterRevision < 0 {
		afterRevision = 0
	}
	if limit <= 0 || limit > androidSyncHeadPageCap {
		limit = 500
	}
	query := `
		SELECT owner_kind, owner_id, revision
		FROM android_sync_heads
		WHERE revision > ?
	`
	args := []any{afterRevision}
	if throughRevision >= 0 {
		query += ` AND revision <= ?`
		args = append(args, throughRevision)
	}
	query += ` ORDER BY revision LIMIT ?`
	args = append(args, limit)
	rows, err := db.reader().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]model.AndroidSyncHead, 0, limit)
	for rows.Next() {
		var head model.AndroidSyncHead
		if err := rows.Scan(&head.OwnerKind, &head.OwnerID, &head.Revision); err != nil {
			return nil, err
		}
		out = append(out, head)
	}
	return out, rows.Err()
}

// touchAndroidSyncHeadTx records a change that is intentionally not covered by
// the schema-maintained payload triggers. Callers must use it only after the
// owning row changed in the same write transaction.
func touchAndroidSyncHeadTx(tx *sql.Tx, ownerKind, ownerID string) error {
	ownerKind = strings.TrimSpace(ownerKind)
	ownerID = strings.TrimSpace(ownerID)
	if ownerKind == "" || ownerID == "" {
		return fmt.Errorf("android sync head needs owner kind and id")
	}
	if _, err := tx.Exec(`
		UPDATE android_sync_clock
		SET revision = revision + 1
		WHERE id = 1
	`); err != nil {
		return fmt.Errorf("advance android sync clock: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO android_sync_heads (owner_kind, owner_id, revision)
		VALUES (?, ?, (SELECT revision FROM android_sync_clock WHERE id = 1))
		ON CONFLICT(owner_kind, owner_id) DO UPDATE SET
			revision = excluded.revision
	`, ownerKind, ownerID); err != nil {
		return fmt.Errorf("touch android sync head %s/%s: %w", ownerKind, ownerID, err)
	}
	return nil
}

// ListAndroidSyncVideoIDsForAssetIDs finds video owners whose current primary
// asset changed. It lets readiness transitions re-evaluate the video library
// without adding another persistent trigger to the server schema.
func (db *DB) ListAndroidSyncVideoIDsForAssetIDs(assetIDs []string) ([]string, error) {
	assetIDs = uniqueStrings(assetIDs)
	if len(assetIDs) == 0 {
		return nil, nil
	}
	selected := make(map[string]struct{})
	for _, chunk := range stringChunks(assetIDs, androidSyncProjectionChunkSize) {
		if err := db.collectStrings(`
			SELECT v.video_id
			FROM assets a
			JOIN videos v
			  ON v.owner_kind = a.owner_kind
			 AND v.video_id = a.owner_id
			WHERE a.asset_id IN (`+placeholders(len(chunk))+`)
			  AND a.asset_kind IN ('video_stream', 'post_media', 'post_audio')
		`, stringsToAny(chunk), selected); err != nil {
			return nil, err
		}
	}
	return sortedKeys(selected), nil
}
