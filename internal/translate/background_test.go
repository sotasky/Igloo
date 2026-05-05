package translate

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/settings"
)

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
	if err := d.SetSetting("", "translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("", "translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("", "translate_api_key", "test-key"); err != nil {
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

func TestTranslateBackgroundContinuesAfterCandidateProviderError(t *testing.T) {
	requests := 0
	errorCandidateRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var gotBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if gotBody["q"] == "坏的文本" {
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
	if err := d.SetSetting("", "translate_backend", settings.TranslateBackendGoogle); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}
	if err := d.SetSetting("", "translate_api_site", srv.URL); err != nil {
		t.Fatalf("SetSetting translate_api_site: %v", err)
	}
	if err := d.SetSetting("", "translate_api_key", "test-key"); err != nil {
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
	if got != "good text" || src != "zh" {
		t.Fatalf("success translation = (%q, %q), want (good text, zh)", got, src)
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
	if thirdTranslated != 1 {
		t.Fatalf("third translated = %d, want 1", thirdTranslated)
	}
	if requests != 3 {
		t.Fatalf("provider requests after third batch = %d, want 3", requests)
	}
	got, src, err = d.GetTranslation("tweet-provider-error", "body", "en")
	if err != nil {
		t.Fatalf("GetTranslation recovered candidate: %v", err)
	}
	if got != "recovered text" || src != "zh" {
		t.Fatalf("recovered translation = (%q, %q), want (recovered text, zh)", got, src)
	}
}

func TestLoadTranslateBackgroundConfigDisablesAPIBackendWithoutKey(t *testing.T) {
	d := openTranslateTestDB(t)
	if err := d.SetSetting("", "translate_auto_mode", settings.TranslateAutoBackground); err != nil {
		t.Fatalf("SetSetting translate_auto_mode: %v", err)
	}
	if err := d.SetSetting("", "translate_backend", settings.TranslateBackendDeepL); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}

	cfg := loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendNone {
		t.Fatalf("backend = %q, want %q without API key", cfg.backend, settings.TranslateBackendNone)
	}

	if err := d.SetSetting("", "translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}
	cfg = loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendDeepL {
		t.Fatalf("backend = %q, want %q with API key", cfg.backend, settings.TranslateBackendDeepL)
	}
}

func TestLoadTranslateBackgroundConfigUsesDisabledBackendWhenUnset(t *testing.T) {
	d := openTranslateTestDB(t)
	if err := d.SetSetting("", "translate_auto_mode", settings.TranslateAutoBackground); err != nil {
		t.Fatalf("SetSetting translate_auto_mode: %v", err)
	}
	if err := d.SetSetting("", "translate_api_key", "test-key"); err != nil {
		t.Fatalf("SetSetting translate_api_key: %v", err)
	}

	cfg := loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendNone {
		t.Fatalf("backend = %q, want %q when translate_backend is unset", cfg.backend, settings.TranslateBackendNone)
	}
}

func TestLoadTranslateBackgroundConfigAllowsOpenAICompatWithoutKeyWhenModelSet(t *testing.T) {
	d := openTranslateTestDB(t)
	if err := d.SetSetting("", "translate_auto_mode", settings.TranslateAutoBackground); err != nil {
		t.Fatalf("SetSetting translate_auto_mode: %v", err)
	}
	if err := d.SetSetting("", "translate_backend", settings.TranslateBackendOpenAICompat); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}

	cfg := loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendNone {
		t.Fatalf("backend = %q, want %q without model", cfg.backend, settings.TranslateBackendNone)
	}

	if err := d.SetSetting("", "translate_model", "qwen2.5:7b"); err != nil {
		t.Fatalf("SetSetting translate_model: %v", err)
	}
	cfg = loadTranslateBackgroundConfig(d)
	if cfg.backend != settings.TranslateBackendOpenAICompat {
		t.Fatalf("backend = %q, want %q with model", cfg.backend, settings.TranslateBackendOpenAICompat)
	}
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
	d, err := db.Open(tmpPath, t.TempDir())
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
