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

func timePtr(t time.Time) *time.Time {
	return &t
}
