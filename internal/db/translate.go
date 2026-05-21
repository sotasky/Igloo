package db

import (
	"database/sql"
	"strings"
	"time"
)

// GetTranslation returns a cached translation for (tweetID, field, targetLang).
// Returns sql.ErrNoRows if not found.
func (db *DB) GetTranslation(tweetID, field, targetLang string) (text string, sourceLang string, err error) {
	err = db.conn.QueryRow(
		"SELECT translated_text, source_lang FROM translations WHERE tweet_id=? AND field=? AND target_lang=?",
		tweetID, field, targetLang,
	).Scan(&text, &sourceLang)
	if err == sql.ErrNoRows {
		return "", "", sql.ErrNoRows
	}
	return text, sourceLang, err
}

// TranslationEntry holds a cached translation for one field of a tweet.
type TranslationEntry struct {
	TranslatedText string
	SourceLang     string
}

// TranslationCandidate is a feed text field that does not yet have a cached
// translation for the requested target language.
type TranslationCandidate struct {
	TweetID       string
	Field         string
	SourceText    string
	SourceLang    string
	BodyText      string
	QuoteBodyText string
}

// GetTranslationsForTweetIDs batch-fetches translations for multiple tweet IDs.
// Returns {tweetID: {"body": entry, "quote": entry}}.
func (db *DB) GetTranslationsForTweetIDs(tweetIDs []string, targetLang string) (map[string]map[string]TranslationEntry, error) {
	result := make(map[string]map[string]TranslationEntry)
	if len(tweetIDs) == 0 {
		return result, nil
	}
	query := "SELECT tweet_id, field, translated_text, source_lang FROM translations WHERE tweet_id IN (" +
		placeholders(len(tweetIDs)) + ") AND target_lang = ?"
	args := make([]any, len(tweetIDs)+1)
	for i, id := range tweetIDs {
		args[i] = id
	}
	args[len(tweetIDs)] = targetLang
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return result, err
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var tid, field, text, srcLang string
		if err := rows.Scan(&tid, &field, &text, &srcLang); err != nil {
			continue
		}
		if result[tid] == nil {
			result[tid] = make(map[string]TranslationEntry)
		}
		result[tid][field] = TranslationEntry{TranslatedText: text, SourceLang: srcLang}
	}
	return result, rows.Err()
}

// GetReusableTranslation returns a cached translation from an equivalent stored
// feed text field. It is intentionally conservative: body reuse is limited to
// rows sharing canonical/content identity and matching body text, while quote
// reuse requires the wrapper quote text to match the locally stored quoted tweet.
func (db *DB) GetReusableTranslation(tweetID, field, targetLang string) (TranslationEntry, error) {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	if targetLang == "" {
		targetLang = "en"
	}
	switch strings.TrimSpace(field) {
	case "body":
		return db.getReusableBodyTranslation(tweetID, targetLang)
	case "quote":
		return db.getReusableQuoteTranslation(tweetID, targetLang)
	default:
		return TranslationEntry{}, sql.ErrNoRows
	}
}

func (db *DB) getReusableBodyTranslation(tweetID, targetLang string) (TranslationEntry, error) {
	var entry TranslationEntry
	err := db.conn.QueryRow(`
		WITH target AS (
			SELECT tweet_id,
			       TRIM(COALESCE(body_text, '')) AS body_text,
			       TRIM(COALESCE(content_hash, '')) AS content_hash,
			       TRIM(COALESCE(canonical_tweet_id, '')) AS canonical_tweet_id
			FROM feed_items
			WHERE tweet_id = ?
		)
		SELECT tr.translated_text, tr.source_lang
		FROM target t
		JOIN feed_items f
		  ON f.tweet_id != t.tweet_id
		 AND TRIM(COALESCE(f.body_text, '')) = t.body_text
		 AND t.body_text != ''
		 AND (
			(t.canonical_tweet_id != '' AND (
				f.tweet_id = t.canonical_tweet_id
				OR TRIM(COALESCE(f.canonical_tweet_id, '')) = t.canonical_tweet_id
			))
			OR (t.content_hash != '' AND TRIM(COALESCE(f.content_hash, '')) = t.content_hash)
		 )
		JOIN translations tr
		  ON tr.tweet_id = f.tweet_id
		 AND tr.field = 'body'
		 AND tr.target_lang = ?
		WHERE TRIM(COALESCE(tr.translated_text, '')) != ''
		ORDER BY
			CASE
				WHEN t.canonical_tweet_id != '' AND f.tweet_id = t.canonical_tweet_id THEN 0
				WHEN TRIM(COALESCE(f.canonical_tweet_id, '')) = f.tweet_id THEN 1
				ELSE 2
			END,
			tr.translated_at DESC
		LIMIT 1
	`, tweetID, targetLang).Scan(&entry.TranslatedText, &entry.SourceLang)
	if err == sql.ErrNoRows {
		return TranslationEntry{}, sql.ErrNoRows
	}
	return entry, err
}

func (db *DB) getReusableQuoteTranslation(tweetID, targetLang string) (TranslationEntry, error) {
	var entry TranslationEntry
	err := db.conn.QueryRow(`
		WITH wrapper AS (
			SELECT tweet_id,
			       TRIM(COALESCE(quote_tweet_id, '')) AS quote_tweet_id,
			       TRIM(COALESCE(quote_body_text, '')) AS quote_body_text
			FROM feed_items
			WHERE tweet_id = ?
		),
		candidates AS (
			SELECT tr.translated_text, tr.source_lang, tr.translated_at, 0 AS priority
			FROM wrapper w
			JOIN feed_items quoted
			  ON quoted.tweet_id = w.quote_tweet_id
			 AND TRIM(COALESCE(quoted.body_text, '')) = w.quote_body_text
			 AND w.quote_body_text != ''
			JOIN translations tr
			  ON tr.tweet_id = quoted.tweet_id
			 AND tr.field = 'body'
			 AND tr.target_lang = ?

			UNION ALL

			SELECT tr.translated_text, tr.source_lang, tr.translated_at, 1 AS priority
			FROM wrapper w
			JOIN feed_items sibling
			  ON sibling.tweet_id != w.tweet_id
			 AND TRIM(COALESCE(sibling.quote_tweet_id, '')) = w.quote_tweet_id
			 AND TRIM(COALESCE(sibling.quote_body_text, '')) = w.quote_body_text
			 AND w.quote_tweet_id != ''
			 AND w.quote_body_text != ''
			JOIN translations tr
			  ON tr.tweet_id = sibling.tweet_id
			 AND tr.field = 'quote'
			 AND tr.target_lang = ?
		)
		SELECT translated_text, source_lang
		FROM candidates
		WHERE TRIM(COALESCE(translated_text, '')) != ''
		ORDER BY priority ASC, translated_at DESC
		LIMIT 1
	`, tweetID, targetLang, targetLang).Scan(&entry.TranslatedText, &entry.SourceLang)
	if err == sql.ErrNoRows {
		return TranslationEntry{}, sql.ErrNoRows
	}
	return entry, err
}

// ListTranslationCandidates returns recent body and quote text fields missing a
// cached translation. Known target/skip languages are filtered in SQL; unknown
// language rows are returned after known foreign-language rows so callers can
// apply text-based eligibility checks without blocking obvious candidates.
func (db *DB) ListTranslationCandidates(targetLang string, skipLangs []string, limit int) ([]TranslationCandidate, error) {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	if targetLang == "" {
		targetLang = "en"
	}
	if limit < 1 {
		limit = 100
	}

	excluded := []string{targetLang}
	seen := map[string]bool{targetLang: true}
	for _, lang := range skipLangs {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "" || seen[lang] {
			continue
		}
		seen[lang] = true
		excluded = append(excluded, lang)
	}
	excludePlaceholders := placeholders(len(excluded))

	query := `
		SELECT tweet_id, field, source_text, source_lang, body_text, quote_body_text
		FROM (
			SELECT
				f.tweet_id,
				'body' AS field,
				COALESCE(f.body_text, '') AS source_text,
				LOWER(TRIM(COALESCE(f.lang, ''))) AS source_lang,
				COALESCE(f.body_text, '') AS body_text,
				COALESCE(f.quote_body_text, '') AS quote_body_text,
				f.published_at AS published_at,
				f.fetched_at AS fetched_at
			FROM feed_items f
			LEFT JOIN translations tr
				ON tr.tweet_id = f.tweet_id
				AND tr.field = 'body'
				AND tr.target_lang = ?
			WHERE tr.tweet_id IS NULL
				AND TRIM(COALESCE(f.body_text, '')) != ''
				AND (
					LOWER(TRIM(COALESCE(f.lang, ''))) = ''
					OR LOWER(TRIM(COALESCE(f.lang, ''))) NOT IN (` + excludePlaceholders + `)
				)

			UNION ALL

			SELECT
				f.tweet_id,
				'quote' AS field,
				COALESCE(f.quote_body_text, '') AS source_text,
				LOWER(TRIM(COALESCE(f.quote_lang, ''))) AS source_lang,
				COALESCE(f.body_text, '') AS body_text,
				COALESCE(f.quote_body_text, '') AS quote_body_text,
				f.published_at AS published_at,
				f.fetched_at AS fetched_at
			FROM feed_items f
			LEFT JOIN translations tr
				ON tr.tweet_id = f.tweet_id
				AND tr.field = 'quote'
				AND tr.target_lang = ?
			WHERE tr.tweet_id IS NULL
				AND TRIM(COALESCE(f.quote_body_text, '')) != ''
				AND (
					LOWER(TRIM(COALESCE(f.quote_lang, ''))) = ''
					OR LOWER(TRIM(COALESCE(f.quote_lang, ''))) NOT IN (` + excludePlaceholders + `)
				)
		) candidates
		ORDER BY
			CASE WHEN source_lang = '' THEN 1 ELSE 0 END,
			published_at DESC,
			fetched_at DESC,
			tweet_id DESC,
			field ASC
		LIMIT ?`

	args := make([]any, 0, 2+len(excluded)*2+1)
	args = append(args, targetLang)
	for _, lang := range excluded {
		args = append(args, lang)
	}
	args = append(args, targetLang)
	for _, lang := range excluded {
		args = append(args, lang)
	}
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var candidates []TranslationCandidate
	for rows.Next() {
		var c TranslationCandidate
		if err := rows.Scan(&c.TweetID, &c.Field, &c.SourceText, &c.SourceLang, &c.BodyText, &c.QuoteBodyText); err != nil {
			return nil, err
		}
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

// SetTranslation inserts or replaces a translation cache entry.
func (db *DB) SetTranslation(tweetID, field, sourceLang, targetLang, text string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT OR REPLACE INTO translations
				(tweet_id, field, source_lang, target_lang, translated_text, translated_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, tweetID, field, sourceLang, targetLang, text, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE feed_items SET sync_seq = ? WHERE tweet_id = ?`, db.NextSyncSeq(), tweetID)
		return err
	})
}
