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

const reusableBodyTranslationSQL = `
	WITH target AS MATERIALIZED (
		SELECT tweet_id,
		       TRIM(COALESCE(body_text, '')) AS body_text,
		       NULLIF(TRIM(COALESCE(content_hash, '')), '') AS content_hash,
		       NULLIF(TRIM(COALESCE(canonical_tweet_id, '')), '') AS canonical_tweet_id
		FROM feed_items
		WHERE tweet_id = ?
	),
	candidates(tweet_id, priority) AS MATERIALIZED (
		SELECT f.tweet_id, 0
		FROM target t
		CROSS JOIN feed_items f
		WHERE t.body_text != ''
		  AND t.canonical_tweet_id IS NOT NULL
		  AND f.tweet_id = t.canonical_tweet_id
		  AND f.tweet_id != t.tweet_id
		  AND TRIM(COALESCE(f.body_text, '')) = t.body_text

		UNION ALL

		SELECT f.tweet_id,
		       CASE WHEN f.canonical_tweet_id = f.tweet_id THEN 1 ELSE 2 END
		FROM target t
		CROSS JOIN feed_items f INDEXED BY idx_feed_items_canonical_tweet
		WHERE t.body_text != ''
		  AND t.canonical_tweet_id IS NOT NULL
		  AND f.canonical_tweet_id IS NOT NULL
		  AND f.canonical_tweet_id != ''
		  AND f.canonical_tweet_id = t.canonical_tweet_id
		  AND f.tweet_id != t.tweet_id
		  AND TRIM(COALESCE(f.body_text, '')) = t.body_text

		UNION ALL

		SELECT f.tweet_id,
		       CASE WHEN f.canonical_tweet_id = f.tweet_id THEN 1 ELSE 2 END
		FROM target t
		CROSS JOIN feed_items f INDEXED BY idx_feed_items_content_hash
		WHERE t.body_text != ''
		  AND t.content_hash IS NOT NULL
		  AND f.content_hash IS NOT NULL
		  AND f.content_hash != ''
		  AND f.content_hash = t.content_hash
		  AND f.tweet_id != t.tweet_id
		  AND TRIM(COALESCE(f.body_text, '')) = t.body_text
	)
	SELECT tr.translated_text, tr.source_lang
	FROM candidates c
	CROSS JOIN translations tr
	WHERE tr.tweet_id = c.tweet_id
	  AND tr.field = 'body'
	  AND tr.target_lang = ?
	  AND TRIM(COALESCE(tr.translated_text, '')) != ''
	ORDER BY c.priority, tr.translated_at DESC
	LIMIT 1`

func (db *DB) getReusableBodyTranslation(tweetID, targetLang string) (TranslationEntry, error) {
	var entry TranslationEntry
	err := db.conn.QueryRow(reusableBodyTranslationSQL, tweetID, targetLang).Scan(&entry.TranslatedText, &entry.SourceLang)
	if err == sql.ErrNoRows {
		return TranslationEntry{}, sql.ErrNoRows
	}
	return entry, err
}

const reusableQuoteTranslationSQL = `
	WITH wrapper AS MATERIALIZED (
		SELECT tweet_id,
		       NULLIF(TRIM(COALESCE(quote_tweet_id, '')), '') AS quote_tweet_id,
		       TRIM(COALESCE(quote_body_text, '')) AS quote_body_text
		FROM feed_items
		WHERE tweet_id = ?
	),
	candidates(tweet_id, field, priority) AS MATERIALIZED (
		SELECT quoted.tweet_id, 'body', 0
		FROM wrapper w
		CROSS JOIN feed_items quoted
		WHERE w.quote_tweet_id IS NOT NULL
		  AND w.quote_body_text != ''
		  AND quoted.tweet_id = w.quote_tweet_id
		  AND TRIM(COALESCE(quoted.body_text, '')) = w.quote_body_text

		UNION ALL

		SELECT sibling.tweet_id, 'quote', 1
		FROM wrapper w
		CROSS JOIN feed_items sibling INDEXED BY idx_feed_items_quote
		WHERE w.quote_tweet_id IS NOT NULL
		  AND w.quote_body_text != ''
		  AND sibling.quote_tweet_id IS NOT NULL
		  AND sibling.quote_tweet_id != ''
		  AND sibling.quote_tweet_id = w.quote_tweet_id
		  AND sibling.tweet_id != w.tweet_id
		  AND TRIM(COALESCE(sibling.quote_body_text, '')) = w.quote_body_text
	)
	SELECT tr.translated_text, tr.source_lang
	FROM candidates c
	CROSS JOIN translations tr
	WHERE tr.tweet_id = c.tweet_id
	  AND tr.field = c.field
	  AND tr.target_lang = ?
	  AND TRIM(COALESCE(tr.translated_text, '')) != ''
	ORDER BY c.priority, tr.translated_at DESC
	LIMIT 1`

func (db *DB) getReusableQuoteTranslation(tweetID, targetLang string) (TranslationEntry, error) {
	var entry TranslationEntry
	err := db.conn.QueryRow(reusableQuoteTranslationSQL, tweetID, targetLang).Scan(&entry.TranslatedText, &entry.SourceLang)
	if err == sql.ErrNoRows {
		return TranslationEntry{}, sql.ErrNoRows
	}
	return entry, err
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
		return nil
	})
}
