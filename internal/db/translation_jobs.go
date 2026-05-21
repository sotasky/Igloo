package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"
)

type TranslationJob struct {
	TweetID       string
	Field         string
	TargetLang    string
	SourceText    string
	SourceLang    string
	BodyText      string
	QuoteBodyText string
	Attempts      int
}

func (db *DB) EnqueueTranslationCandidates(targetLang string, skipLangs []string, limit int) (int, error) {
	candidates, err := db.ListTranslationCandidates(targetLang, skipLangs, limit)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	nowMs := time.Now().UnixMilli()
	enqueued := 0
	err = db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			INSERT INTO translation_jobs (
				tweet_id, field, target_lang, source_hash, status, priority,
				attempts, next_attempt_at, last_error_kind, last_error,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, 'queued', 0, 0, 0, '', '', ?, ?)
			ON CONFLICT(tweet_id, field, target_lang) DO UPDATE SET
				source_hash = excluded.source_hash,
				status = CASE
					WHEN translation_jobs.source_hash != excluded.source_hash THEN 'queued'
					WHEN translation_jobs.status = 'running' THEN 'queued'
					ELSE translation_jobs.status
				END,
				attempts = CASE
					WHEN translation_jobs.source_hash != excluded.source_hash THEN 0
					ELSE translation_jobs.attempts
				END,
				next_attempt_at = CASE
					WHEN translation_jobs.source_hash != excluded.source_hash THEN 0
					WHEN translation_jobs.status = 'running' THEN 0
					ELSE translation_jobs.next_attempt_at
				END,
				last_error_kind = CASE
					WHEN translation_jobs.source_hash != excluded.source_hash THEN ''
					ELSE translation_jobs.last_error_kind
				END,
				last_error = CASE
					WHEN translation_jobs.source_hash != excluded.source_hash THEN ''
					ELSE translation_jobs.last_error
				END,
				updated_at = excluded.updated_at
			WHERE translation_jobs.status != 'done'
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = stmt.Close()
		}()
		for _, c := range candidates {
			if strings.TrimSpace(c.TweetID) == "" || strings.TrimSpace(c.Field) == "" || strings.TrimSpace(c.SourceText) == "" {
				continue
			}
			res, err := stmt.Exec(c.TweetID, c.Field, targetLang, translationSourceHash(c.SourceText), nowMs, nowMs)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				enqueued++
			}
		}
		return nil
	})
	return enqueued, err
}

func (db *DB) ClaimTranslationJob(targetLang string, nowMs int64) (*TranslationJob, error) {
	jobs, err := db.ClaimTranslationJobs(targetLang, nowMs, 1)
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return jobs[0], nil
}

func (db *DB) ClaimTranslationJobs(targetLang string, nowMs int64, limit int) ([]*TranslationJob, error) {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	if targetLang == "" {
		targetLang = "en"
	}
	if limit < 1 {
		limit = 1
	}
	var jobs []*TranslationJob
	err := db.WithWrite(func(tx *sql.Tx) error {
		rows, err := tx.Query(`
			SELECT tweet_id, field
			FROM translation_jobs
			WHERE target_lang = ?
			  AND status = 'queued'
			  AND next_attempt_at <= ?
			ORDER BY priority DESC, updated_at ASC, tweet_id ASC, field ASC
			LIMIT ?
		`, targetLang, nowMs, limit)
		if err != nil {
			return err
		}
		var selected []struct {
			tweetID string
			field   string
		}
		for rows.Next() {
			var row struct {
				tweetID string
				field   string
			}
			if err := rows.Scan(&row.tweetID, &row.field); err != nil {
				_ = rows.Close()
				return err
			}
			selected = append(selected, row)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, row := range selected {
			res, err := tx.Exec(`
				UPDATE translation_jobs
				SET status = 'running', updated_at = ?
				WHERE tweet_id = ? AND field = ? AND target_lang = ? AND status = 'queued'
			`, nowMs, row.tweetID, row.field, targetLang)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue
			}
			claimed, err := readTranslationJobTx(tx, row.tweetID, row.field, targetLang)
			if err != nil {
				return err
			}
			jobs = append(jobs, claimed)
		}
		return nil
	})
	return jobs, err
}

func readTranslationJobTx(tx *sql.Tx, tweetID, field, targetLang string) (*TranslationJob, error) {
	var job TranslationJob
	job.TweetID = tweetID
	job.Field = field
	job.TargetLang = targetLang
	err := tx.QueryRow(`
		SELECT
			CASE WHEN tj.field = 'quote' THEN COALESCE(f.quote_body_text, '') ELSE COALESCE(f.body_text, '') END,
			LOWER(TRIM(CASE WHEN tj.field = 'quote' THEN COALESCE(f.quote_lang, '') ELSE COALESCE(f.lang, '') END)),
			COALESCE(f.body_text, ''),
			COALESCE(f.quote_body_text, ''),
			tj.attempts
		FROM translation_jobs tj
		JOIN feed_items f ON f.tweet_id = tj.tweet_id
		WHERE tj.tweet_id = ? AND tj.field = ? AND tj.target_lang = ?
	`, tweetID, field, targetLang).Scan(&job.SourceText, &job.SourceLang, &job.BodyText, &job.QuoteBodyText, &job.Attempts)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (db *DB) CompleteTranslationJob(tweetID, field, targetLang string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE translation_jobs
			SET status = 'done', last_error_kind = '', last_error = '', updated_at = ?
			WHERE tweet_id = ? AND field = ? AND target_lang = ?
		`, time.Now().UnixMilli(), tweetID, field, targetLang)
		return err
	})
}

func (db *DB) SkipTranslationJob(tweetID, field, targetLang, reason string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE translation_jobs
			SET status = 'skipped', last_error_kind = 'skipped', last_error = ?, updated_at = ?
			WHERE tweet_id = ? AND field = ? AND target_lang = ?
		`, trimJobError(reason), time.Now().UnixMilli(), tweetID, field, targetLang)
		return err
	})
}

func (db *DB) RetryTranslationJob(tweetID, field, targetLang, kind, message string, delay time.Duration) error {
	nowMs := time.Now().UnixMilli()
	nextMs := nowMs + delay.Milliseconds()
	if delay < 0 {
		nextMs = nowMs
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE translation_jobs
			SET status = 'queued',
			    attempts = attempts + 1,
			    next_attempt_at = ?,
			    last_error_kind = ?,
			    last_error = ?,
			    updated_at = ?
			WHERE tweet_id = ? AND field = ? AND target_lang = ?
		`, nextMs, trimJobError(kind), trimJobError(message), nowMs, tweetID, field, targetLang)
		return err
	})
}

func translationSourceHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func trimJobError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		return value[:512]
	}
	return value
}
