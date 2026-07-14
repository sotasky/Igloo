package db

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestSetAndGetTranslation(t *testing.T) {
	d := openWritableTestDB(t)

	tweetID := "test_tweet_translate_001"
	field := "body"
	sourceLang := "en"
	targetLang := "fr"
	text := "Bonjour le monde"

	if err := d.SetTranslation(tweetID, field, sourceLang, targetLang, text); err != nil {
		t.Fatalf("SetTranslation: %v", err)
	}

	got, gotLang, err := d.GetTranslation(tweetID, field, targetLang)
	if err != nil {
		t.Fatalf("GetTranslation: %v", err)
	}
	if got != text {
		t.Errorf("text: got %q, want %q", got, text)
	}
	if gotLang != sourceLang {
		t.Errorf("source_lang: got %q, want %q", gotLang, sourceLang)
	}
}

func TestGetTranslationMissing(t *testing.T) {
	d := openWritableTestDB(t)

	_, _, err := d.GetTranslation("nonexistent_tweet", "body", "de")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSetTranslationOverwrite(t *testing.T) {
	d := openWritableTestDB(t)

	tweetID := "test_tweet_translate_002"
	field := "body"
	sourceLang := "en"
	targetLang := "es"

	if err := d.SetTranslation(tweetID, field, sourceLang, targetLang, "Hola mundo"); err != nil {
		t.Fatalf("first SetTranslation: %v", err)
	}

	newText := "Hola mundo (updated)"
	if err := d.SetTranslation(tweetID, field, sourceLang, targetLang, newText); err != nil {
		t.Fatalf("second SetTranslation: %v", err)
	}

	got, _, err := d.GetTranslation(tweetID, field, targetLang)
	if err != nil {
		t.Fatalf("GetTranslation after overwrite: %v", err)
	}
	if got != newText {
		t.Errorf("got %q, want %q", got, newText)
	}
}

func TestSetTranslationQuoteField(t *testing.T) {
	d := openWritableTestDB(t)

	tweetID := "test_tweet_translate_003"
	if err := d.SetTranslation(tweetID, "quote", "ja", "en", "Hello from quote"); err != nil {
		t.Fatalf("SetTranslation quote: %v", err)
	}

	got, _, err := d.GetTranslation(tweetID, "quote", "en")
	if err != nil {
		t.Fatalf("GetTranslation quote: %v", err)
	}
	if got != "Hello from quote" {
		t.Errorf("got %q", got)
	}
}

func TestGetReusableTranslationUsesCanonicalBody(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now()
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:          "original_body",
			AuthorHandle:     "sample_author_original",
			BodyText:         "안녕하세요",
			Lang:             "ko",
			ContentHash:      "same_body_hash",
			CanonicalTweetID: "original_body",
			PublishedAt:      &now,
		},
		{
			TweetID:          "sample_repost_body",
			AuthorHandle:     "sample_author_repost",
			BodyText:         "안녕하세요",
			Lang:             "ko",
			IsRetweet:        true,
			ContentHash:      "same_body_hash",
			CanonicalTweetID: "original_body",
			PublishedAt:      timePtr(now.Add(time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetTranslation("original_body", "body", "Korean", "en", "Hello"); err != nil {
		t.Fatalf("SetTranslation: %v", err)
	}

	got, err := d.GetReusableTranslation("sample_repost_body", "body", "en")
	if err != nil {
		t.Fatalf("GetReusableTranslation: %v", err)
	}
	if got.TranslatedText != "Hello" || got.SourceLang != "Korean" {
		t.Fatalf("reusable translation = %#v", got)
	}
}

func TestGetReusableTranslationUsesQuotedTweetBody(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now()
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:      "quoted_body",
			AuthorHandle: "sample_author_quoted",
			BodyText:     "고마워요",
			Lang:         "ko",
			PublishedAt:  &now,
		},
		{
			TweetID:       "quote_wrapper",
			AuthorHandle:  "sample_author_wrapper",
			BodyText:      "wrapper text",
			Lang:          "en",
			QuoteTweetID:  "quoted_body",
			QuoteBodyText: "고마워요",
			QuoteLang:     "ko",
			PublishedAt:   timePtr(now.Add(time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetTranslation("quoted_body", "body", "Korean", "en", "Thank you"); err != nil {
		t.Fatalf("SetTranslation: %v", err)
	}

	got, err := d.GetReusableTranslation("quote_wrapper", "quote", "en")
	if err != nil {
		t.Fatalf("GetReusableTranslation: %v", err)
	}
	if got.TranslatedText != "Thank you" || got.SourceLang != "Korean" {
		t.Fatalf("reusable translation = %#v", got)
	}
}

func TestReusableTranslationQueriesUseIdentityIndexes(t *testing.T) {
	d := openWritableTestDB(t)
	tests := []struct {
		name    string
		query   string
		args    []any
		indexes []string
	}{
		{
			name:  "body",
			query: reusableBodyTranslationSQL,
			args:  []any{"sample_body", "en"},
			indexes: []string{
				"idx_feed_items_canonical_tweet",
				"idx_feed_items_content_hash",
			},
		},
		{
			name:    "quote",
			query:   reusableQuoteTranslationSQL,
			args:    []any{"sample_quote", "en"},
			indexes: []string{"idx_feed_items_quote"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := d.conn.Query("EXPLAIN QUERY PLAN "+tt.query, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()

			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(details, "\n")
			for _, index := range tt.indexes {
				if !strings.Contains(plan, "USING INDEX "+index) {
					t.Fatalf("reuse plan does not use %s:\n%s", index, plan)
				}
			}
			if strings.Contains(plan, "SCAN tr") {
				t.Fatalf("reuse plan scans the translation cache:\n%s", plan)
			}
		})
	}
}

func TestUpsertFeedItemsQueuesTranslationJobs(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.SetSetting("translate_target_lang", "tr"); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:       "sample_queued",
		AuthorHandle:  "sample_author",
		BodyText:      "body text",
		QuoteBodyText: "quote text",
		PublishedAt:   &base,
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	rows, err := d.conn.Query(`
		SELECT field, target_lang, source_hash, status, priority
		FROM translation_jobs
		WHERE tweet_id = 'sample_queued'
		ORDER BY field
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	wantHashes := map[string]string{
		"body":  translationSourceHash("body text"),
		"quote": translationSourceHash("quote text"),
	}
	seen := make(map[string]bool, len(wantHashes))
	for rows.Next() {
		var field, targetLang, sourceHash, status string
		var priority int
		if err := rows.Scan(&field, &targetLang, &sourceHash, &status, &priority); err != nil {
			t.Fatal(err)
		}
		if targetLang != "tr" || sourceHash != wantHashes[field] || status != "queued" || priority != 1 {
			t.Fatalf("job %s = target %q hash %q status %q priority %d", field, targetLang, sourceHash, status, priority)
		}
		seen[field] = true
	}
	if len(seen) != len(wantHashes) {
		t.Fatalf("queued fields = %#v", seen)
	}
}

func TestTranslationJobsClaimAndComplete(t *testing.T) {
	d := openWritableTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "job_candidate",
		AuthorHandle: "sample_author_job",
		BodyText:     "안녕하세요",
		Lang:         "ko",
		PublishedAt:  &base,
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	job, err := d.ClaimTranslationJob("en", time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ClaimTranslationJob: %v", err)
	}
	if job == nil || job.TweetID != "job_candidate" || job.Field != "body" || job.SourceLang != "ko" {
		t.Fatalf("job = %#v", job)
	}
	if err := d.SetTranslation(job.TweetID, job.Field, job.SourceLang, job.TargetLang, "hello"); err != nil {
		t.Fatalf("SetTranslation: %v", err)
	}
	if err := d.CompleteTranslationJob(job.TweetID, job.Field, job.TargetLang); err != nil {
		t.Fatalf("CompleteTranslationJob: %v", err)
	}
	next, err := d.ClaimTranslationJob("en", time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ClaimTranslationJob after complete: %v", err)
	}
	if next != nil {
		t.Fatalf("next job = %#v, want nil", next)
	}
}

func TestTranslationJobClaimUsesReadyOrderIndex(t *testing.T) {
	d := openWritableTestDB(t)
	rows, err := d.conn.Query(`EXPLAIN QUERY PLAN
		SELECT tweet_id, field
		FROM translation_jobs INDEXED BY idx_translation_jobs_ready
		WHERE target_lang = ?
		  AND status = 'queued'
		  AND next_attempt_at <= ?
		ORDER BY priority DESC, updated_at ASC, tweet_id ASC, field ASC
		LIMIT ?`, "en", int64(1000), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "USING COVERING INDEX idx_translation_jobs_ready") {
		t.Fatalf("translation claim plan = %s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("translation claim sorts outside its queue index = %s", plan)
	}
}

func TestUpsertFeedItemsUnchangedTranslationSourceKeepsCompletedJob(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	item := model.FeedItem{
		TweetID:      "sample_unchanged",
		AuthorHandle: "sample_author",
		BodyText:     "same source text",
		Lang:         "ko",
		Views:        1,
		PublishedAt:  &now,
	}
	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTranslation(item.TweetID, "body", "ko", "en", "cached text"); err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteTranslationJob(item.TweetID, "body", "en"); err != nil {
		t.Fatal(err)
	}

	item.Views = 2
	if _, err := d.UpsertFeedItems([]model.FeedItem{item}); err != nil {
		t.Fatal(err)
	}
	var status, sourceHash string
	var priority, views int
	if err := d.QueryRow(`
		SELECT tj.status, tj.source_hash, tj.priority, f.views
		FROM translation_jobs tj
		JOIN feed_items f ON f.tweet_id = tj.tweet_id
		WHERE tj.tweet_id = ? AND tj.field = 'body' AND tj.target_lang = 'en'
	`, item.TweetID).Scan(&status, &sourceHash, &priority, &views); err != nil {
		t.Fatal(err)
	}
	if status != "done" || sourceHash != translationSourceHash(item.BodyText) || priority != 1 || views != 2 {
		t.Fatalf("job = status %q hash %q priority %d views %d", status, sourceHash, priority, views)
	}
	if text, _, err := d.GetTranslation(item.TweetID, "body", "en"); err != nil || text != "cached text" {
		t.Fatalf("translation = %q, %v", text, err)
	}
}

func TestUpsertFeedItemsChangedTranslationSourceRequeuesJob(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	items := []model.FeedItem{
		{TweetID: "sample_done", AuthorHandle: "sample_author_done", BodyText: "old done text", Lang: "ko", PublishedAt: &now},
		{TweetID: "sample_skipped", AuthorHandle: "sample_author_skipped", BodyText: "old skipped text", Lang: "ko", PublishedAt: &now},
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if err := d.SetTranslation(item.TweetID, "body", "ko", "en", "stale text"); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ExecRaw(`
		UPDATE translation_jobs
		SET status = CASE tweet_id WHEN 'sample_done' THEN 'done' ELSE 'skipped' END,
		    priority = 0, attempts = 4, next_attempt_at = 999,
		    last_error_kind = 'old', last_error = 'old error'
		WHERE tweet_id IN ('sample_done', 'sample_skipped')
	`); err != nil {
		t.Fatal(err)
	}

	items[0].BodyText = "new done text"
	items[1].BodyText = "new skipped text"
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		var status, sourceHash, errorKind, errorText string
		var priority, attempts int
		var nextAttempt int64
		if err := d.QueryRow(`
			SELECT status, source_hash, priority, attempts, next_attempt_at,
			       last_error_kind, last_error
			FROM translation_jobs
			WHERE tweet_id = ? AND field = 'body' AND target_lang = 'en'
		`, item.TweetID).Scan(
			&status, &sourceHash, &priority, &attempts, &nextAttempt, &errorKind, &errorText,
		); err != nil {
			t.Fatal(err)
		}
		if status != "queued" || sourceHash != translationSourceHash(item.BodyText) ||
			priority != 1 || attempts != 0 || nextAttempt != 0 || errorKind != "" || errorText != "" {
			t.Fatalf("job %s = %q %q %d %d %d %q %q", item.TweetID, status, sourceHash, priority, attempts, nextAttempt, errorKind, errorText)
		}
		if _, _, err := d.GetTranslation(item.TweetID, "body", "en"); err != sql.ErrNoRows {
			t.Fatalf("translation %s err = %v", item.TweetID, err)
		}
	}
}

func TestSetTranslationAdvancesAndroidFeedOwner(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now()
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "sample_tweet",
		AuthorHandle: "sample_author",
		BodyText:     "안녕하세요",
		Lang:         "ko",
		PublishedAt:  &now,
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	var before int64
	if err := d.QueryRow(`
		SELECT revision FROM android_sync_heads
		WHERE owner_kind = 'feed' AND owner_id = 'sample_tweet'
	`).Scan(&before); err != nil {
		t.Fatalf("read initial feed revision: %v", err)
	}
	if err := d.SetTranslation("sample_tweet", "body", "ko", "en", "hello"); err != nil {
		t.Fatalf("SetTranslation: %v", err)
	}
	var after int64
	if err := d.QueryRow(`
		SELECT revision FROM android_sync_heads
		WHERE owner_kind = 'feed' AND owner_id = 'sample_tweet'
	`).Scan(&after); err != nil {
		t.Fatalf("read updated feed revision: %v", err)
	}
	if after <= before {
		t.Fatalf("feed revision after translation = %d, want > %d", after, before)
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}
