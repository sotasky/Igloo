package db

import (
	"database/sql"
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

func TestListTranslationCandidates(t *testing.T) {
	d := openWritableTestDB(t)

	base := time.Unix(1_700_000_000, 0)
	items := []model.FeedItem{
		{
			TweetID:      "body-tr",
			AuthorHandle: "author_tr",
			BodyText:     "Merhaba dunya",
			Lang:         "tr",
			PublishedAt:  &base,
		},
		{
			TweetID:       "quote-es",
			AuthorHandle:  "author_es",
			BodyText:      "wrapper",
			Lang:          "en",
			QuoteBodyText: "Hola cita",
			QuoteLang:     "es",
			PublishedAt:   timePtr(base.Add(-time.Hour)),
		},
		{
			TweetID:      "cached",
			AuthorHandle: "author_cached",
			BodyText:     "Zaten cevrildi",
			Lang:         "tr",
			PublishedAt:  timePtr(base.Add(-2 * time.Hour)),
		},
		{
			TweetID:      "target-en",
			AuthorHandle: "author_en",
			BodyText:     "Already English",
			Lang:         "en",
			PublishedAt:  timePtr(base.Add(-3 * time.Hour)),
		},
		{
			TweetID:      "skip-ja",
			AuthorHandle: "author_ja",
			BodyText:     "サンプル本文",
			Lang:         "ja",
			PublishedAt:  timePtr(base.Add(-4 * time.Hour)),
		},
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetTranslation("cached", "body", "tr", "en", "Already translated"); err != nil {
		t.Fatalf("SetTranslation cached: %v", err)
	}

	got, err := d.ListTranslationCandidates("en", []string{"ja"}, 10)
	if err != nil {
		t.Fatalf("ListTranslationCandidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %#v", len(got), got)
	}
	want := []struct {
		tweetID string
		field   string
		lang    string
		text    string
	}{
		{"body-tr", "body", "tr", "Merhaba dunya"},
		{"quote-es", "quote", "es", "Hola cita"},
	}
	for i, w := range want {
		if got[i].TweetID != w.tweetID || got[i].Field != w.field || got[i].SourceLang != w.lang || got[i].SourceText != w.text {
			t.Fatalf("candidate %d = %#v, want tweet=%s field=%s lang=%s text=%q", i, got[i], w.tweetID, w.field, w.lang, w.text)
		}
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
	n, err := d.EnqueueTranslationCandidates("en", nil, 10)
	if err != nil {
		t.Fatalf("EnqueueTranslationCandidates: %v", err)
	}
	if n == 0 {
		t.Fatalf("EnqueueTranslationCandidates enqueued 0 rows")
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
