package db

import "database/sql"

// AnalyticsRollup represents a daily event aggregate.
type AnalyticsRollup struct {
	Day            string
	EventType      string
	Screen         string
	ContentType    string
	Count          int
	TotalElapsedMs int
}

// AnalyticsEvent represents a raw analytics event.
type AnalyticsEvent struct {
	EventID     string
	EventType   string
	TimestampMs int64
	Screen      string
	ContentType string
	ElapsedMs   int
	ExtraJSON   string
}

// GetAnalyticsRollups returns daily rollups, most recent first.
func (db *DB) GetAnalyticsRollups(limit int) ([]AnalyticsRollup, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.conn.Query(`
		SELECT day, event_type, screen, content_type, count, total_elapsed_ms
		FROM analytics_rollups_daily
		ORDER BY day DESC, event_type, screen
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rollups []AnalyticsRollup
	for rows.Next() {
		var r AnalyticsRollup
		if err := rows.Scan(&r.Day, &r.EventType, &r.Screen, &r.ContentType, &r.Count, &r.TotalElapsedMs); err != nil {
			return nil, err
		}
		rollups = append(rollups, r)
	}
	return rollups, rows.Err()
}

// AddAnalyticsEvents inserts events (INSERT OR IGNORE) and returns the count inserted.
func (db *DB) AddAnalyticsEvents(events []AnalyticsEvent) (int, error) {
	added := 0
	err := db.WithWrite(func(tx *sql.Tx) error {
		for _, e := range events {
			res, err := tx.Exec(`
				INSERT OR IGNORE INTO analytics_events
				  (event_id, event_type, timestamp_ms, screen, content_type, elapsed_ms, extra_json)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, e.EventID, e.EventType, e.TimestampMs, e.Screen, e.ContentType, e.ElapsedMs, e.ExtraJSON)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			added += int(n)
		}
		return nil
	})
	return added, err
}

// GetAnalyticsRecentEvents returns recent raw events, newest first.
func (db *DB) GetAnalyticsRecentEvents(limit int) ([]AnalyticsEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.conn.Query(`
		SELECT event_id, event_type, timestamp_ms, COALESCE(screen,''),
		       COALESCE(content_type,''), COALESCE(elapsed_ms,0), COALESCE(extra_json,'')
		FROM analytics_events
		ORDER BY timestamp_ms DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []AnalyticsEvent
	for rows.Next() {
		var e AnalyticsEvent
		if err := rows.Scan(&e.EventID, &e.EventType, &e.TimestampMs, &e.Screen, &e.ContentType, &e.ElapsedMs, &e.ExtraJSON); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
