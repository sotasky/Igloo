package db

import (
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// GetIngestState returns the ingest state for a handle.
// Returns a zero-value IngestState (with Handle set) if no record exists.
func (db *DB) GetIngestState(handle string) (model.IngestState, error) {
	var s model.IngestState
	s.Handle = handle

	var updatedAt sql.NullInt64
	err := db.conn.QueryRow(`
		SELECT fail_count, COALESCE(next_retry_at,0), COALESCE(last_success_at,0),
		       COALESCE(last_attempt_at,0), COALESCE(last_error,''),
		       COALESCE(last_http_status,0), COALESCE(avg_latency_ms,0),
		       updated_at
		FROM ingest_state
		WHERE handle = ?
	`, handle).Scan(
		&s.FailCount, &s.NextRetryAt, &s.LastSuccessAt,
		&s.LastAttemptAt, &s.LastError, &s.LastHTTPStatus,
		&s.AvgLatencyMs, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if t := millisToTimePtr(updatedAt); t != nil {
		s.UpdatedAt = *t
	}
	return s, nil
}

// RecordIngestSuccess upserts an ingest success: resets fail_count, records timing.
func (db *DB) RecordIngestSuccess(handle string, successAt float64, latencyMs float64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		_, err := tx.Exec(`
			INSERT INTO ingest_state
				(handle, fail_count, next_retry_at, last_success_at,
				 last_attempt_at, avg_latency_ms, updated_at)
			VALUES (?, 0, 0, ?, ?, ?, ?)
			ON CONFLICT(handle) DO UPDATE SET
				fail_count      = 0,
				next_retry_at   = 0,
				last_success_at = excluded.last_success_at,
				last_attempt_at = excluded.last_attempt_at,
				avg_latency_ms  = CASE
					WHEN ingest_state.avg_latency_ms = 0 THEN excluded.avg_latency_ms
					ELSE (ingest_state.avg_latency_ms * 0.8 + excluded.avg_latency_ms * 0.2)
				END,
				updated_at      = excluded.updated_at
		`, handle, successAt, successAt, latencyMs, now)
		return err
	})
}

// RecordIngestFailure upserts an ingest failure with backoff:
//   - All errors: increment fail_count
//   - HTTP 429 only: exponential backoff min(120 * 2^max(0, fail_count-1), 1800)
//   - All other errors (5xx, timeouts, parse failures): flat 60s retry
func (db *DB) RecordIngestFailure(handle string, lastError string, httpStatus int) error {
	return db.recordIngestFailureClassified(handle, lastError, httpStatus)
}

func (db *DB) recordIngestFailureClassified(handle string, lastError string, httpStatus int) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		nowUnix := float64(time.Now().Unix())

		// Read current fail_count
		var failCount int
		err := tx.QueryRow(
			"SELECT COALESCE(fail_count,0) FROM ingest_state WHERE handle=?", handle,
		).Scan(&failCount)
		if err != nil && err != sql.ErrNoRows {
			return err
		}

		// All errors increment fail_count.
		// 429 only → exponential backoff; everything else → flat 60s.
		failCount++
		var nextRetry float64
		if httpStatus == 429 {
			exp := math.Max(0, float64(failCount-1))
			backoffSecs := math.Min(120*math.Pow(2, exp), 1800)
			nextRetry = nowUnix + backoffSecs
		} else {
			nextRetry = nowUnix + 60
		}

		_, err = tx.Exec(`
			INSERT INTO ingest_state
				(handle, fail_count, next_retry_at, last_attempt_at,
				 last_error, last_http_status, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(handle) DO UPDATE SET
				fail_count       = excluded.fail_count,
				next_retry_at    = excluded.next_retry_at,
				last_attempt_at  = excluded.last_attempt_at,
				last_error       = excluded.last_error,
				last_http_status = excluded.last_http_status,
				updated_at       = excluded.updated_at
		`, handle, failCount, nextRetry, nowUnix,
			nilIfEmpty(lastError), nilIfZero(int64(httpStatus)), now)
		return err
	})
}

// ResetIngestHandle clears a single channel's ingest state so it is fetched immediately.
func (db *DB) ResetIngestHandle(handle string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE ingest_state SET fail_count = 0, next_retry_at = 0, last_success_at = 0 WHERE handle = ?", handle)
		return err
	})
}

// ResetIngestBackoff clears all backoff state so every channel is retried immediately.
// Called at server startup to recover from stale backoff after restarts.
func (db *DB) ResetIngestBackoff() error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE ingest_state SET fail_count = 0, next_retry_at = 0")
		return err
	})
}

// FilterReadyHandles returns handles that are not in backoff and not fetched
// recently. intervalSec is the minimum seconds since last_success_at before a
// handle is considered due again; pass 0 to skip the recency check.
//
// Returns (ready, notDue, cooling):
//   - ready:   handles to fetch this cycle
//   - notDue:  skipped because fetched within intervalSec
//   - cooling: skipped because next_retry_at is in the future (backoff)
func (db *DB) FilterReadyHandles(handles []string, intervalSec float64) (ready []string, notDue int, cooling int) {
	if len(handles) == 0 {
		return nil, 0, 0
	}

	now := float64(time.Now().Unix())

	// Query blocked handles with the reason so callers can report both counts.
	// reason='cooling'  → next_retry_at > now (backoff)
	// reason='not_due'  → fetched too recently (within intervalSec)
	// A cooling handle takes priority over not_due.
	var query string
	var args []any
	if intervalSec > 0 {
		query = `SELECT handle,
			CASE WHEN next_retry_at > ? THEN 'cooling' ELSE 'not_due' END
		FROM ingest_state
		WHERE handle IN (` + placeholders(len(handles)) + `)
		AND (
			next_retry_at > ?
			OR (last_success_at > 0 AND ? - last_success_at < ?)
		)`
		args = append(stringsToAny(handles), now)
		args = append([]any{now}, args...)
		args = append(args, now, intervalSec)
	} else {
		query = `SELECT handle, 'cooling'
		FROM ingest_state
		WHERE handle IN (` + placeholders(len(handles)) + `) AND next_retry_at > ?`
		args = append(stringsToAny(handles), now)
	}

	blocked := make(map[string]string) // handle → reason
	rows, err := db.conn.Query(query, args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var h, reason string
			if rows.Scan(&h, &reason) == nil {
				blocked[h] = reason
			}
		}
	}

	ready = make([]string, 0, len(handles))
	for _, h := range handles {
		switch blocked[h] {
		case "cooling":
			cooling++
		case "not_due":
			notDue++
		default:
			ready = append(ready, h)
		}
	}

	// Sort ready handles by staleness (oldest last_success_at first, never-fetched first).
	// This ensures the cycle resumes from where it left off after a restart
	// instead of always starting from A alphabetically.
	if len(ready) > 1 {
		staleness := make(map[string]float64, len(ready))
		staleRows, staleErr := db.conn.Query(
			`SELECT handle, COALESCE(last_success_at, 0) FROM ingest_state
			 WHERE handle IN (`+placeholders(len(ready))+`)`,
			stringsToAny(ready)...)
		if staleErr == nil {
			defer staleRows.Close()
			for staleRows.Next() {
				var h string
				var ts float64
				if staleRows.Scan(&h, &ts) == nil {
					staleness[h] = ts
				}
			}
		}
		sort.Slice(ready, func(i, j int) bool {
			return staleness[ready[i]] < staleness[ready[j]]
		})
	}

	return ready, notDue, cooling
}

// placeholders returns a comma-separated list of ? for SQLite IN clauses.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}

// stringsToAny converts []string to []any for variadic SQL args.
func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// GetAllIngestStates returns all rows from the ingest_state table.
func (db *DB) GetAllIngestStates() ([]model.IngestState, error) {
	rows, err := db.conn.Query(`
		SELECT handle, fail_count, COALESCE(next_retry_at,0), COALESCE(last_success_at,0),
		       COALESCE(last_attempt_at,0), COALESCE(last_error,''),
		       COALESCE(last_http_status,0), COALESCE(avg_latency_ms,0), updated_at
		FROM ingest_state
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []model.IngestState
	for rows.Next() {
		var s model.IngestState
		var updatedAt sql.NullInt64
		if err := rows.Scan(
			&s.Handle, &s.FailCount, &s.NextRetryAt, &s.LastSuccessAt,
			&s.LastAttemptAt, &s.LastError, &s.LastHTTPStatus,
			&s.AvgLatencyMs, &updatedAt,
		); err != nil {
			return nil, err
		}
		if t := millisToTimePtr(updatedAt); t != nil {
			s.UpdatedAt = *t
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// CountFeedItemsBySource returns source_handle → count for all feed items.
func (db *DB) CountFeedItemsBySource() (map[string]int, error) {
	rows, err := db.conn.Query("SELECT source_handle, COUNT(*) FROM feed_items GROUP BY source_handle")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var handle string
		var count int
		if err := rows.Scan(&handle, &count); err != nil {
			return nil, err
		}
		counts[handle] = count
	}
	return counts, rows.Err()
}
