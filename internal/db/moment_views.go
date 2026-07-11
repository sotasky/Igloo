package db

import "time"

type MomentView struct {
	VideoID  string
	ViewedAt time.Time
}

// Returns the resulting viewed_at as stored.
func (db *DB) UpsertMomentView(videoID string) (time.Time, error) {
	nowMs := time.Now().UnixMilli()
	if _, err := db.MutateMomentView(videoID, nowMs); err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(nowMs), nil
}

// ListMomentViews returns moment views newest first.
func (db *DB) ListMomentViews(since time.Time, limit int) ([]MomentView, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT video_id, viewed_at FROM moment_views`
	var args []any
	if !since.IsZero() {
		q += ` WHERE viewed_at > ?`
		args = append(args, since.UnixMilli())
	}
	q += ` ORDER BY viewed_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

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

func (db *DB) CountMomentViews() (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM moment_views`).Scan(&n)
	return n, err
}
