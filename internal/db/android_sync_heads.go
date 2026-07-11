package db

import (
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
	if afterRevision < 0 {
		afterRevision = 0
	}
	if limit <= 0 || limit > androidSyncHeadPageCap {
		limit = 500
	}
	rows, err := db.reader().Query(`
		SELECT owner_kind, owner_id, revision
		FROM android_sync_heads
		WHERE revision > ?
		ORDER BY revision
		LIMIT ?
	`, afterRevision, limit)
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
