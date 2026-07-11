package translate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/settings"
)

func googleBatchTexts(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["q"].([]any)
	if !ok {
		t.Fatalf("q = %#v, want array", body["q"])
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("q item = %#v, want string", item)
		}
		out = append(out, text)
	}
	return out
}

func TestShouldAutoTranslateCandidate(t *testing.T) {
	skipSet := map[string]bool{"ja": true}
	cases := []struct {
		name       string
		sourceLang string
		text       string
		want       bool
	}{
		{
			name:       "known foreign language latin text still translates",
			sourceLang: "tr",
			text:       "Merhaba dunya",
			want:       true,
		},
		{
			name:       "target language",
			sourceLang: "en",
			text:       "Already English",
			want:       false,
		},
		{
			name:       "target language region tag",
			sourceLang: "en-US",
			text:       "Already English",
			want:       false,
		},
		{
			name:       "skipped language",
			sourceLang: "ja",
			text:       "サンプル本文",
			want:       false,
		},
		{
			name:       "skipped language region tag",
			sourceLang: "ja-JP",
			text:       "サンプル本文",
			want:       false,
		},
		{
			name:       "skipped japanese script despite different detected language",
			sourceLang: "sk",
			text:       "ブラン Blanc",
			want:       false,
		},
		{
			name:       "skipped script in protected hashtag does not block body",
			sourceLang: "zh",
			text:       "今天想要翻译 #アズールレーン",
			want:       true,
		},
		{
			name:       "empty language with latin text",
			sourceLang: "",
			text:       "plain latin text",
			want:       false,
		},
		{
			name:       "empty language with skipped japanese text",
			sourceLang: "",
			text:       "サンプル本文",
			want:       false,
		},
		{
			name:       "empty language with non skipped non latin text",
			sourceLang: "",
			text:       "안녕하세요",
			want:       true,
		},
		{
			name:       "known non skipped language with latin text",
			sourceLang: "sk",
			text:       "plain latin text",
			want:       true,
		},
		{
			name:       "known foreign language caption remains translatable",
			sourceLang: "sk",
			text:       "sample caption #tag 💜\n@sample_user",
			want:       true,
		},
		{
			name:       "tokens only",
			sourceLang: "tr",
			text:       "@alice #tag https://example.com",
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldAutoTranslateCandidate(tc.sourceLang, tc.text, "en", skipSet)
			if got != tc.want {
				t.Fatalf("shouldAutoTranslateCandidate(%q, %q) = %v, want %v", tc.sourceLang, tc.text, got, tc.want)
			}
		})
	}
}

func TestTranslateBackgroundSkipsProviderDetectedSkipLanguage(t *testing.T) {
	requests := 0
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"Blanc","detectedSourceLanguage":"ja"}]}}`))
	}))
	defer srv.Close()

	d := openTranslateTestDB(t)
	if _, err := d.UpsertFeedItems([]model.FeedItem{{
		TweetID:      "tweet-ja-misdetected",
		AuthorHandle: "author",
		BodyText:     "ブラン Blanc",
		Lang:         "sk",
	}}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendGoogle,
		target:  "en",
		skip:    []string{"ja"},
		skipSet: map[string]bool{"ja": true},
	}
	skipped := map[string]translateBackgroundSkip{}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, skipped)
	if err != nil {
		t.Fatalf("runTranslateBackgroundBatch: %v", err)
	}
	if translated != 0 {
		t.Fatalf("translated = %d, want 0", translated)
	}
	if requests != 0 {
		t.Fatalf("provider requests = %d, want 0; body=%#v", requests, gotBody)
	}
	if _, _, err := d.GetTranslation("tweet-ja-misdetected", "body", "en"); err != sql.ErrNoRows {
		t.Fatalf("GetTranslation err = %v, want sql.ErrNoRows", err)
	}
}

func TestTranslateBackgroundBatchesSameLanguageThreadBodies(t *testing.T) {
	requests := 0
	var gotTexts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var gotBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotTexts = googleBatchTexts(t, gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"root translated","detectedSourceLanguage":"ko"},{"translatedText":"reply translated","detectedSourceLanguage":"ko"}]}}`))
	}))
	defer srv.Close()

	d := openTranslateTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:      "thread_root_translate",
			AuthorHandle: "sample_author_root",
			BodyText:     "루트 본문",
			Lang:         "ko",
			PublishedAt:  &base,
		},
		{
			TweetID:       "thread_reply_translate",
			AuthorHandle:  "sample_author_reply",
			BodyText:      "답글 본문",
			Lang:          "ko",
			IsReply:       true,
			ReplyToStatus: "thread_root_translate",
			PublishedAt:   ptrTime(base.Add(time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendGoogle,
		target:  "en",
	}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, map[string]translateBackgroundSkip{})
	if err != nil {
		t.Fatalf("runTranslateBackgroundBatch: %v", err)
	}
	if translated != 2 {
		t.Fatalf("translated = %d, want 2", translated)
	}
	if requests != 1 {
		t.Fatalf("provider requests = %d, want 1", requests)
	}
	if len(gotTexts) != 2 || gotTexts[0] != "루트 본문" || gotTexts[1] != "답글 본문" {
		t.Fatalf("q texts = %#v", gotTexts)
	}
	for tweetID, want := range map[string]string{
		"thread_root_translate":  "root translated",
		"thread_reply_translate": "reply translated",
	} {
		got, _, err := d.GetTranslation(tweetID, "body", "en")
		if err != nil {
			t.Fatalf("GetTranslation %s: %v", tweetID, err)
		}
		if got != want {
			t.Fatalf("%s translation = %q, want %q", tweetID, got, want)
		}
	}
}

func TestTranslateBackgroundRetriesOnlyRejectedBatchItem(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var gotBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		texts := googleBatchTexts(t, gotBody)
		if len(texts) != 2 {
			t.Fatalf("q texts = %#v, want two-item thread batch", texts)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"root translated","detectedSourceLanguage":"ko"},{"translatedText":"reply translated","detectedSourceLanguage":"ko"}]}}`))
	}))
	defer srv.Close()

	d := openTranslateTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:      "sample_root",
			AuthorHandle: "sample_author_root",
			BodyText:     "루트 본문",
			Lang:         "ko",
			PublishedAt:  &base,
		},
		{
			TweetID:       "sample_reply",
			AuthorHandle:  "sample_author_reply",
			BodyText:      "답글 #태그",
			Lang:          "ko",
			IsReply:       true,
			ReplyToStatus: "sample_root",
			PublishedAt:   ptrTime(base.Add(time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendGoogle,
		target:  "en",
	}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, map[string]translateBackgroundSkip{})
	if err != nil {
		t.Fatalf("runTranslateBackgroundBatch: %v", err)
	}
	if translated != 1 {
		t.Fatalf("translated = %d, want only valid item", translated)
	}
	if requests != 1 {
		t.Fatalf("provider requests = %d, want 1", requests)
	}
	if got, _, err := d.GetTranslation("sample_root", "body", "en"); err != nil || got != "root translated" {
		t.Fatalf("root translation = (%q, %v), want root translated", got, err)
	}
	if _, _, err := d.GetTranslation("sample_reply", "body", "en"); err != sql.ErrNoRows {
		t.Fatalf("reply translation err = %v, want sql.ErrNoRows", err)
	}
	var status, kind string
	var attempts int
	if err := d.QueryRow(`SELECT status, attempts, last_error_kind FROM translation_jobs WHERE tweet_id = ? AND field = 'body' AND target_lang = 'en'`, "sample_reply").Scan(&status, &attempts, &kind); err != nil {
		t.Fatalf("read retry job: %v", err)
	}
	if status != "queued" || attempts != 1 || kind != "provider_error" {
		t.Fatalf("reply job = status %q attempts %d kind %q, want queued/1/provider_error", status, attempts, kind)
	}
}

func TestTranslateBackgroundSplitsMixedLanguageThreadBodies(t *testing.T) {
	requests := 0
	var gotBatches [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var gotBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		texts := googleBatchTexts(t, gotBody)
		gotBatches = append(gotBatches, texts)
		w.Header().Set("Content-Type", "application/json")
		switch texts[0] {
		case "루트 본문":
			_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"root translated","detectedSourceLanguage":"ko"}]}}`))
		case "回复正文":
			_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"reply translated","detectedSourceLanguage":"zh"}]}}`))
		default:
			t.Fatalf("unexpected q texts: %#v", texts)
		}
	}))
	defer srv.Close()

	d := openTranslateTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:      "mixed_thread_root",
			AuthorHandle: "sample_author_root",
			BodyText:     "루트 본문",
			Lang:         "ko",
			PublishedAt:  &base,
		},
		{
			TweetID:       "mixed_thread_reply",
			AuthorHandle:  "sample_author_reply",
			BodyText:      "回复正文",
			Lang:          "zh",
			IsReply:       true,
			ReplyToStatus: "mixed_thread_root",
			PublishedAt:   ptrTime(base.Add(time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendGoogle,
		target:  "en",
	}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, map[string]translateBackgroundSkip{})
	if err != nil {
		t.Fatalf("runTranslateBackgroundBatch: %v", err)
	}
	if translated != 2 {
		t.Fatalf("translated = %d, want 2", translated)
	}
	if requests != 2 {
		t.Fatalf("provider requests = %d, want 2; batches=%#v", requests, gotBatches)
	}
	for _, batch := range gotBatches {
		if len(batch) != 1 {
			t.Fatalf("mixed-language batch should contain one item, got %#v", batch)
		}
	}
}

func TestTranslateBackgroundReusesRetweetBodyTranslation(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "provider should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	d := openTranslateTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:          "sample_repost_source",
			AuthorHandle:     "sample_author_original",
			BodyText:         "안녕하세요",
			Lang:             "ko",
			ContentHash:      "sample_same_repost_hash",
			CanonicalTweetID: "sample_repost_source",
			PublishedAt:      &now,
		},
		{
			TweetID:          "sample_repost_dup",
			AuthorHandle:     "sample_author_repost",
			BodyText:         "안녕하세요",
			Lang:             "ko",
			IsRetweet:        true,
			ContentHash:      "sample_same_repost_hash",
			CanonicalTweetID: "sample_repost_source",
			PublishedAt:      ptrTime(now.Add(time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetTranslation("sample_repost_source", "body", "Korean", "en", "Hello"); err != nil {
		t.Fatalf("SetTranslation: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendGoogle,
		target:  "en",
	}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, map[string]translateBackgroundSkip{})
	if err != nil {
		t.Fatalf("runTranslateBackgroundBatch: %v", err)
	}
	if translated != 1 {
		t.Fatalf("translated = %d, want 1 reused translation", translated)
	}
	if requests != 0 {
		t.Fatalf("provider requests = %d, want 0", requests)
	}
	got, src, err := d.GetTranslation("sample_repost_dup", "body", "en")
	if err != nil {
		t.Fatalf("GetTranslation repost: %v", err)
	}
	if got != "Hello" || src != "Korean" {
		t.Fatalf("repost translation = (%q, %q), want (Hello, Korean)", got, src)
	}
}

func TestTranslateBackgroundReusesMergedRetweetSiblingAfterFirstProviderCall(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var gotBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		texts := googleBatchTexts(t, gotBody)
		if len(texts) != 1 || texts[0] != "안녕하세요" {
			t.Fatalf("q texts = %#v", texts)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"Hello","detectedSourceLanguage":"ko"}]}}`))
	}))
	defer srv.Close()

	d := openTranslateTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:          "sample_duplicate_repost_a",
			AuthorHandle:     "sample_author_a",
			BodyText:         "안녕하세요",
			Lang:             "ko",
			IsRetweet:        true,
			ContentHash:      "sample_same_merged_hash",
			CanonicalTweetID: "sample_source",
			PublishedAt:      &now,
		},
		{
			TweetID:          "sample_duplicate_repost_b",
			AuthorHandle:     "sample_author_b",
			BodyText:         "안녕하세요",
			Lang:             "ko",
			IsRetweet:        true,
			ContentHash:      "sample_same_merged_hash",
			CanonicalTweetID: "sample_source",
			PublishedAt:      ptrTime(now.Add(time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendGoogle,
		target:  "en",
	}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, map[string]translateBackgroundSkip{})
	if err != nil {
		t.Fatalf("runTranslateBackgroundBatch: %v", err)
	}
	if translated != 2 {
		t.Fatalf("translated = %d, want 2", translated)
	}
	if requests != 1 {
		t.Fatalf("provider requests = %d, want 1", requests)
	}
	for _, tweetID := range []string{"sample_duplicate_repost_a", "sample_duplicate_repost_b"} {
		got, _, err := d.GetTranslation(tweetID, "body", "en")
		if err != nil {
			t.Fatalf("GetTranslation %s: %v", tweetID, err)
		}
		if got != "Hello" {
			t.Fatalf("%s translation = %q, want Hello", tweetID, got)
		}
	}
}

func TestTranslateBackgroundReusesQuoteWrapperTranslation(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "provider should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	d := openTranslateTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:      "quoted_translation_source",
			AuthorHandle: "sample_author_quoted",
			BodyText:     "고마워요",
			Lang:         "ko",
			PublishedAt:  &now,
		},
		{
			TweetID:       "quote_translation_wrapper",
			AuthorHandle:  "sample_author_wrapper",
			BodyText:      "wrapper",
			Lang:          "en",
			QuoteTweetID:  "quoted_translation_source",
			QuoteBodyText: "고마워요",
			QuoteLang:     "ko",
			PublishedAt:   ptrTime(now.Add(time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetTranslation("quoted_translation_source", "body", "Korean", "en", "Thank you"); err != nil {
		t.Fatalf("SetTranslation: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendGoogle,
		target:  "en",
	}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, map[string]translateBackgroundSkip{})
	if err != nil {
		t.Fatalf("runTranslateBackgroundBatch: %v", err)
	}
	if translated != 1 {
		t.Fatalf("translated = %d, want 1 reused translation", translated)
	}
	if requests != 0 {
		t.Fatalf("provider requests = %d, want 0", requests)
	}
	got, src, err := d.GetTranslation("quote_translation_wrapper", "quote", "en")
	if err != nil {
		t.Fatalf("GetTranslation quote wrapper: %v", err)
	}
	if got != "Thank you" || src != "Korean" {
		t.Fatalf("quote translation = (%q, %q), want (Thank you, Korean)", got, src)
	}
}

func TestTranslateBackgroundContinuesAfterCandidateProviderError(t *testing.T) {
	requests := 0
	errorCandidateRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var gotBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		texts := googleBatchTexts(t, gotBody)
		if len(texts) == 1 && texts[0] == "坏的文本" {
			errorCandidateRequests++
			if errorCandidateRequests == 1 {
				http.Error(w, "provider rejected candidate", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"recovered text","detectedSourceLanguage":"zh"}]}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"good text","detectedSourceLanguage":"zh"}]}}`))
	}))
	defer srv.Close()

	d := openTranslateTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	newer := base.Add(time.Minute)
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:      "tweet-provider-error",
			AuthorHandle: "author_error",
			BodyText:     "坏的文本",
			Lang:         "zh",
			PublishedAt:  &newer,
		},
		{
			TweetID:      "tweet-provider-success",
			AuthorHandle: "author_success",
			BodyText:     "好的文本",
			Lang:         "zh",
			PublishedAt:  &base,
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendGoogle,
		target:  "en",
	}
	skipped := map[string]translateBackgroundSkip{}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, skipped)
	if err != nil {
		t.Fatalf("runTranslateBackgroundBatch: %v", err)
	}
	if translated != 1 {
		t.Fatalf("translated = %d, want 1", translated)
	}
	if requests != 2 {
		t.Fatalf("provider requests = %d, want 2", requests)
	}
	got, src, err := d.GetTranslation("tweet-provider-success", "body", "en")
	if err != nil {
		t.Fatalf("GetTranslation success: %v", err)
	}
	if got != "good text" || src != "Chinese" {
		t.Fatalf("success translation = (%q, %q), want (good text, Chinese)", got, src)
	}
	if _, _, err := d.GetTranslation("tweet-provider-error", "body", "en"); err != sql.ErrNoRows {
		t.Fatalf("GetTranslation error candidate err = %v, want sql.ErrNoRows", err)
	}

	secondTranslated, secondErr := runTranslateBackgroundBatch(context.Background(), d, cfg, skipped)
	if secondErr != nil {
		t.Fatalf("second runTranslateBackgroundBatch: %v", secondErr)
	}
	if secondTranslated != 0 {
		t.Fatalf("second translated = %d, want 0", secondTranslated)
	}
	if requests != 2 {
		t.Fatalf("provider requests after second batch = %d, want 2", requests)
	}

	thirdTranslated, thirdErr := runTranslateBackgroundBatch(context.Background(), d, cfg, skipped)
	if thirdErr != nil {
		t.Fatalf("third runTranslateBackgroundBatch: %v", thirdErr)
	}
	if thirdTranslated != 0 {
		t.Fatalf("third translated = %d, want 0 while retry is delayed", thirdTranslated)
	}
	if requests != 2 {
		t.Fatalf("provider requests after third batch = %d, want 2 while retry is delayed", requests)
	}
	if _, _, err := d.GetTranslation("tweet-provider-error", "body", "en"); err != sql.ErrNoRows {
		t.Fatalf("GetTranslation delayed retry candidate err = %v, want sql.ErrNoRows", err)
	}
}

func TestTranslateBackgroundStopsBatchOnKagiRateLimit(t *testing.T) {
	kagiProviderClearCooldownForTest()
	t.Cleanup(kagiProviderClearCooldownForTest)

	dir := t.TempDir()
	countPath := dir + "/count"
	kagiPath := dir + "/kagi"
	script := "#!/bin/sh\nprintf x >> \"$KAGI_COUNT_FILE\"\necho 'configuration error: Kagi Translate language detection request rejected: HTTP 429 Too Many Requests' >&2\nexit 1\n"
	if err := os.WriteFile(kagiPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write kagi stub: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("KAGI_COUNT_FILE", countPath)

	d := openTranslateTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	if _, err := d.UpsertFeedItems([]model.FeedItem{
		{
			TweetID:      "tweet-kagi-rate-limited-newer",
			AuthorHandle: "author_newer",
			BodyText:     "안녕하세요",
			Lang:         "ko",
			PublishedAt:  &base,
		},
		{
			TweetID:      "tweet-kagi-rate-limited-older",
			AuthorHandle: "author_older",
			BodyText:     "你好",
			Lang:         "zh",
			PublishedAt:  ptrTime(base.Add(-time.Minute)),
		},
	}); err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendKagiCLI); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}

	cfg := translateBackgroundConfig{
		mode:    settings.TranslateAutoBackground,
		backend: settings.TranslateBackendKagiCLI,
		target:  "en",
	}
	translated, err := runTranslateBackgroundBatch(context.Background(), d, cfg, map[string]translateBackgroundSkip{})
	if !errors.Is(err, ErrProviderRateLimited) {
		t.Fatalf("runTranslateBackgroundBatch err = %v, want ErrProviderRateLimited", err)
	}
	if translated != 0 {
		t.Fatalf("translated = %d, want 0", translated)
	}
	count, readErr := os.ReadFile(countPath)
	if readErr != nil {
		t.Fatalf("read kagi count: %v", readErr)
	}
	if len(count) != 1 {
		t.Fatalf("kagi requests = %d, want 1", len(count))
	}
}

func TestLoadTranslateBackgroundConfigDisablesAPIBackendWithoutKey(t *testing.T) {
	d := openTranslateTestDB(t)
	if err := d.SetSetting("translate_auto_mode", settings.TranslateAutoBackground); err != nil {
		t.Fatalf("SetSetting translate_auto_mode: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendDeepL); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}

	cfg := loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendNone {
		t.Fatalf("backend = %q, want %q without API key", cfg.backend, settings.TranslateBackendNone)
	}

	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}
	cfg = loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendDeepL {
		t.Fatalf("backend = %q, want %q with API key", cfg.backend, settings.TranslateBackendDeepL)
	}
}

func TestLoadTranslateBackgroundConfigUsesDisabledBackendWhenUnset(t *testing.T) {
	d := openTranslateTestDB(t)
	if err := d.SetSetting("translate_auto_mode", settings.TranslateAutoBackground); err != nil {
		t.Fatalf("SetSetting translate_auto_mode: %v", err)
	}
	if err := d.SetSetting("translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendNone {
		t.Fatalf("backend = %q, want %q when translate_backend is unset", cfg.backend, settings.TranslateBackendNone)
	}
}

func TestLoadTranslateBackgroundConfigAllowsOpenAICompatWithoutKeyWhenModelSet(t *testing.T) {
	d := openTranslateTestDB(t)
	if err := d.SetSetting("translate_auto_mode", settings.TranslateAutoBackground); err != nil {
		t.Fatalf("SetSetting translate_auto_mode: %v", err)
	}
	if err := d.SetSetting("translate_backend", settings.TranslateBackendOpenAICompat); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}

	cfg := loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendNone {
		t.Fatalf("backend = %q, want %q without model", cfg.backend, settings.TranslateBackendNone)
	}

	if err := d.SetSetting("translate_model", "qwen2.5:7b"); err != nil {
		t.Fatalf("SetSetting translate_model: %v", err)
	}
	cfg = loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendOpenAICompat {
		t.Fatalf("backend = %q, want %q with model", cfg.backend, settings.TranslateBackendOpenAICompat)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func openTranslateTestDB(t *testing.T) *db.DB {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "igloo-translate-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("close temp db: %v", err)
	}
	d, err := db.OpenPath(tmpPath, t.TempDir())
	if err != nil {
		_ = os.Remove(tmpPath)
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Close()
		_ = os.Remove(tmpPath)
	})
	return d
}
