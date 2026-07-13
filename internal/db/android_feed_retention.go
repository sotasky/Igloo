package db

import (
	"database/sql"
	"fmt"
)

type AndroidFeedRetention struct {
	FeedDays       int
	ReconciledAtMs int64
}

func (db *DB) GetAndroidFeedRetention() (*AndroidFeedRetention, error) {
	var state AndroidFeedRetention
	err := db.reader().QueryRow(`
		SELECT feed_days, reconciled_at_ms
		FROM android_feed_retention
		WHERE id = 1
	`).Scan(&state.FeedDays, &state.ReconciledAtMs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !IsValidRetentionDays(state.FeedDays) {
		return nil, fmt.Errorf("invalid stored Android feed retention: %d", state.FeedDays)
	}
	return &state, nil
}

func (db *DB) RecordAndroidFeedRetention(feedDays int, reconciledAtMs int64) error {
	if !IsValidRetentionDays(feedDays) {
		return fmt.Errorf("invalid Android feed retention: %d", feedDays)
	}
	if reconciledAtMs <= 0 {
		return fmt.Errorf("invalid Android feed reconciliation time: %d", reconciledAtMs)
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO android_feed_retention (id, feed_days, reconciled_at_ms)
			VALUES (1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				feed_days = excluded.feed_days,
				reconciled_at_ms = excluded.reconciled_at_ms
		`, feedDays, reconciledAtMs)
		return err
	})
}
