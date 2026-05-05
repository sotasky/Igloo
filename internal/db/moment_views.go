package db

import "time"

// MomentView records that `username` viewed shorts `videoID`.
type MomentView struct {
	VideoID  string
	ViewedAt time.Time
}

// UpsertMomentView inserts or refreshes viewed_at for (username, videoID).
// Returns the resulting viewed_at as stored.
func (db *DB) UpsertMomentView(username, videoID string) (time.Time, error) {
	nowMs := time.Now().UnixMilli()
	if _, err := db.conn.Exec(
		`INSERT INTO moment_views (username, video_id, viewed_at) VALUES (?, ?, ?)
		 ON CONFLICT(username, video_id) DO UPDATE SET viewed_at = excluded.viewed_at`,
		username, videoID, nowMs,
	); err != nil {
		return time.Time{}, err
	}
	var storedMs int64
	err := db.conn.QueryRow(
		`SELECT viewed_at FROM moment_views WHERE username = ? AND video_id = ?`,
		username, videoID,
	).Scan(&storedMs)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(storedMs), nil
}

// ListMomentViews returns moment views for a user, newest first,
// filtered to rows with viewed_at > since (zero time = no filter), capped at limit.
func (db *DB) ListMomentViews(username string, since time.Time, limit int) ([]MomentView, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT video_id, viewed_at FROM moment_views WHERE username = ?`
	args := []any{username}
	if !since.IsZero() {
		q += ` AND viewed_at > ?`
		args = append(args, since.UnixMilli())
	}
	q += ` ORDER BY viewed_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MomentView
	for rows.Next() {
		var mv MomentView
		var viewedMs int64
		if err := rows.Scan(&mv.VideoID, &viewedMs); err != nil {
			return nil, err
		}
		if viewedMs > 0 {
			mv.ViewedAt = time.UnixMilli(viewedMs)
		}
		out = append(out, mv)
	}
	return out, rows.Err()
}

// CountMomentViews returns the total number of moments viewed by username.
func (db *DB) CountMomentViews(username string) (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM moment_views WHERE username = ?`, username).Scan(&n)
	return n, err
}
