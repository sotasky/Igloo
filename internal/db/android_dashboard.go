package db

import (
	"database/sql"
	"encoding/json"
)

// AndroidRetentionSettings mirrors the Android cache-retention preferences that
// define the local content boundary. Zero disables the corresponding bucket.
type AndroidRetentionSettings struct {
	FeedDays    int
	YoutubeDays int
	MomentsDays int
	StoryHours  int
}

type AndroidSyncHealthReport struct {
	ID             int64
	Cursor         string
	ReportedAtMs   int64
	PayloadJSON    string
	VerifiedAssets int
	PendingAssets  int
	MissingAssets  int
	TotalAssets    int
	VerifiedBytes  int64
	Retention      AndroidRetentionSettings
	HasRetention   bool
}

func (db *DB) GetLatestAndroidSyncHealthReport() (*AndroidSyncHealthReport, error) {
	var row AndroidSyncHealthReport
	err := db.conn.QueryRow(`
		SELECT id, cursor, reported_at_ms, payload_json,
		       verified_assets, pending_assets, missing_assets,
		       total_assets, verified_bytes
		FROM android_sync_health_reports
		ORDER BY reported_at_ms DESC, id DESC
		LIMIT 1
	`).Scan(
		&row.ID,
		&row.Cursor,
		&row.ReportedAtMs,
		&row.PayloadJSON,
		&row.VerifiedAssets,
		&row.PendingAssets,
		&row.MissingAssets,
		&row.TotalAssets,
		&row.VerifiedBytes,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	row.Retention, row.HasRetention = androidSyncHealthRetention(row.PayloadJSON)
	return &row, nil
}

func (db *DB) RecordAndroidSyncHealth(cursor string, reportedAtMs int64, payload []byte, verifiedAssets, pendingAssets, missingAssets, totalAssets int, verifiedBytes int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO android_sync_health_reports (
				cursor, reported_at_ms, payload_json, verified_assets,
				pending_assets, missing_assets, total_assets, verified_bytes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, cursor, reportedAtMs, string(payload), verifiedAssets, pendingAssets, missingAssets, totalAssets, verifiedBytes)
		return err
	})
}

func androidSyncHealthRetention(payload string) (AndroidRetentionSettings, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &raw) != nil {
		return AndroidRetentionSettings{}, false
	}
	retentionRaw, ok := raw["retention"]
	if !ok {
		return AndroidRetentionSettings{}, false
	}
	var ret map[string]int
	if json.Unmarshal(retentionRaw, &ret) != nil {
		return AndroidRetentionSettings{}, false
	}
	if !IsValidRetentionDays(ret["feed_days"]) ||
		!IsValidRetentionDays(ret["youtube_days"]) ||
		!IsValidRetentionDays(ret["moments_days"]) {
		return AndroidRetentionSettings{}, false
	}
	storyHours := 48
	if v, ok := ret["story_hours"]; ok {
		storyHours = v
		if v > 0 {
			storyHours = NormalizeStoriesWindowHours(v)
		}
	}
	return AndroidRetentionSettings{
		FeedDays:    max(0, ret["feed_days"]),
		YoutubeDays: max(0, ret["youtube_days"]),
		MomentsDays: max(0, ret["moments_days"]),
		StoryHours:  storyHours,
	}, true
}
