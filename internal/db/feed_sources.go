package db

import (
	"database/sql"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func (db *DB) UpsertFeedSource(source model.FeedSource) error {
	now := time.Now().Unix()
	createdAt := unixOr(source.CreatedAt, now)
	updatedAt := unixOr(source.UpdatedAt, now)
	enabled := 0
	if source.Enabled {
		enabled = 1
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO feed_sources (
				source_id, platform, source_type, external_id, label, url, enabled,
				last_checked, last_ok, last_error, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_id) DO UPDATE SET
				platform = excluded.platform,
				source_type = excluded.source_type,
				external_id = excluded.external_id,
				label = COALESCE(NULLIF(excluded.label,''), feed_sources.label),
				url = excluded.url,
				enabled = excluded.enabled,
				updated_at = excluded.updated_at
		`, source.SourceID, source.Platform, source.SourceType, source.ExternalID, source.Label, source.URL, enabled,
			unixPtr(source.LastChecked), unixPtr(source.LastOK), source.LastError, createdAt, updatedAt)
		return err
	})
}

func (db *DB) ListFeedSources(platform string) ([]model.FeedSource, error) {
	args := []any{}
	where := ""
	if platform != "" {
		where = "WHERE fs.platform = ?"
		args = append(args, platform)
	}
	rows, err := db.conn.Query(`
		SELECT fs.source_id, fs.platform, fs.source_type, fs.external_id, fs.label, fs.url,
		       fs.enabled, fs.last_checked, fs.last_ok, fs.last_error, fs.created_at, fs.updated_at,
		       COUNT(fis.tweet_id) AS item_count
		FROM feed_sources fs
		LEFT JOIN feed_item_sources fis ON fis.source_id = fs.source_id
		`+where+`
		GROUP BY fs.source_id
		ORDER BY fs.updated_at DESC, fs.label COLLATE NOCASE
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var sources []model.FeedSource
	for rows.Next() {
		var src model.FeedSource
		var enabled int
		var lastChecked, lastOK, createdAt, updatedAt sql.NullInt64
		if err := rows.Scan(&src.SourceID, &src.Platform, &src.SourceType, &src.ExternalID, &src.Label, &src.URL,
			&enabled, &lastChecked, &lastOK, &src.LastError, &createdAt, &updatedAt, &src.ItemCount); err != nil {
			return nil, err
		}
		src.Enabled = enabled != 0
		src.LastChecked = feedSourceTimePtr(lastChecked)
		src.LastOK = feedSourceTimePtr(lastOK)
		if createdAt.Valid {
			src.CreatedAt = time.Unix(createdAt.Int64, 0)
		}
		if updatedAt.Valid {
			src.UpdatedAt = time.Unix(updatedAt.Int64, 0)
		}
		sources = append(sources, src)
	}
	return sources, rows.Err()
}

func (db *DB) GetFeedSource(sourceID string) (model.FeedSource, error) {
	sources, err := db.ListFeedSources("")
	if err != nil {
		return model.FeedSource{}, err
	}
	for _, src := range sources {
		if src.SourceID == sourceID {
			return src, nil
		}
	}
	return model.FeedSource{}, sql.ErrNoRows
}

func (db *DB) DeleteFeedSource(sourceID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM feed_item_sources WHERE source_id = ?`, sourceID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM feed_sources WHERE source_id = ?`, sourceID)
		return err
	})
}

func (db *DB) RecordFeedItemSources(tweetID string, sourceIDs []string) error {
	if tweetID == "" || len(sourceIDs) == 0 {
		return nil
	}
	now := time.Now().Unix()
	return db.WithWrite(func(tx *sql.Tx) error {
		for _, sourceID := range sourceIDs {
			if sourceID == "" {
				continue
			}
			if _, err := tx.Exec(`
				INSERT INTO feed_item_sources (tweet_id, source_id, first_seen_at, last_seen_at)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(tweet_id, source_id) DO UPDATE SET last_seen_at = excluded.last_seen_at
			`, tweetID, sourceID, now, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) RecordFeedSourceSuccess(sourceID string) error {
	now := time.Now().Unix()
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE feed_sources SET last_checked = ?, last_ok = ?, last_error = '', updated_at = ? WHERE source_id = ?`, now, now, now, sourceID)
		return err
	})
}

func (db *DB) RecordFeedSourceFailure(sourceID, message string) error {
	now := time.Now().Unix()
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE feed_sources SET last_checked = ?, last_error = ?, updated_at = ? WHERE source_id = ?`, now, message, now, sourceID)
		return err
	})
}

func unixOr(t time.Time, fallback int64) int64 {
	if t.IsZero() {
		return fallback
	}
	return t.Unix()
}

func unixPtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Unix()
}

func feedSourceTimePtr(v sql.NullInt64) *time.Time {
	if !v.Valid || v.Int64 <= 0 {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}
