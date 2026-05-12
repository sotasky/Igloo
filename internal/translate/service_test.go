package translate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/settings"
)

func TestProtectForTranslateReplacesTokens(t *testing.T) {
	text := "cos. @example_handle_99\n\n#쿠키런 #성수 https://example.com/x"
	protected, originals := protectForTranslate(text)

	if strings.Contains(protected, "@example_handle_99") {
		t.Errorf("mention leaked into protected text: %q", protected)
	}
	if strings.Contains(protected, "#쿠키런") || strings.Contains(protected, "#성수") {
		t.Errorf("hashtag leaked into protected text: %q", protected)
	}
	if strings.Contains(protected, "https://") {
		t.Errorf("URL leaked into protected text: %q", protected)
	}
	if len(originals) != 4 {
		t.Fatalf("expected 4 originals, got %d: %v", len(originals), originals)
	}
}

func TestProtectRestoreRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"mention", "hello @alice bye"},
		{"hashtag_cjk", "260411 #쿠키런 #성수"},
		{"url", "see https://example.com/path?q=1"},
		{"mixed", "cos. @example_handle_99 #쿠키런 https://x.com/y"},
		{"no_tokens", "just plain text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			protected, originals := protectForTranslate(c.in)
			restored := restoreFromTranslate(protected, originals)
			normalized := strings.TrimSpace(reWS.ReplaceAllString(c.in, " "))
			if restored != normalized {
				t.Errorf("round-trip failed:\n input: %q\n protected: %q\n restored: %q\n want: %q",
					c.in, protected, restored, normalized)
			}
		})
	}
}

func TestRestoreToleratesBraceWhitespace(t *testing.T) {
	_, originals := protectForTranslate("@alice #tag https://x.com")
	translated := "saw {{ 0 }} talking about {{1}} at {{  2  }}"
	restored := restoreFromTranslate(translated, originals)
	want := "saw @alice talking about #tag at https://x.com"
	if restored != want {
		t.Errorf("tolerant restore failed:\n got: %q\n want: %q", restored, want)
	}
}

func TestRestoreLeavesUnknownIndexAlone(t *testing.T) {
	_, originals := protectForTranslate("@alice")
	translated := "hi {{0}} and {{7}}"
	restored := restoreFromTranslate(translated, originals)
	want := "hi @alice and {{7}}"
	if restored != want {
		t.Errorf("unknown index handling:\n got: %q\n want: %q", restored, want)
	}
}

func TestHasTranslatableContent(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"{{0}}", false},
		{"{{0}} {{1}}", false},
		{"🪼 {{0}}", false},
		{"✨ !!!", false},
		{"{{0}} hi", true},
		{"just text", true},
		{"今日は {{0}}", true},
	}
	for _, c := range cases {
		if got := hasTranslatableContent(c.in); got != c.want {
			t.Errorf("hasTranslatableContent(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPreservedProtectedTokensRequiresPlaceholderOrOriginal(t *testing.T) {
	translated := "hello {{0}}"
	restored := restoreFromTranslate(translated, []string{"#tag"})
	if !preservedProtectedTokens(translated, restored, []string{"#tag"}) {
		t.Fatalf("expected placeholder-preserved token to pass")
	}

	translated = "hello #tag"
	if !preservedProtectedTokens(translated, translated, []string{"#tag"}) {
		t.Fatalf("expected original-preserved token to pass")
	}

	translated = "hello tag"
	if preservedProtectedTokens(translated, translated, []string{"#tag"}) {
		t.Fatalf("expected dropped protected token to fail")
	}
}

func TestLooksAlreadyReadableInTarget(t *testing.T) {
	cases := []struct {
		name       string
		protected  string
		targetLang string
		want       bool
	}{
		{
			name:       "english_ascii_with_protected_tokens_and_emoji",
			protected:  "sample caption {{0}} 💜 {{1}}",
			targetLang: "en",
			want:       true,
		},
		{
			name:       "non_latin_text",
			protected:  "今日は {{0}}",
			targetLang: "en",
			want:       false,
		},
		{
			name:       "tokens_and_emoji_only",
			protected:  "🪼 {{0}}",
			targetLang: "en",
			want:       false,
		},
		{
			name:       "non_english_target",
			protected:  "sample caption {{0}}",
			targetLang: "tr",
			want:       false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksAlreadyReadableInTarget(tc.protected, tc.targetLang); got != tc.want {
				t.Fatalf("looksAlreadyReadableInTarget(%q, %q) = %v, want %v", tc.protected, tc.targetLang, got, tc.want)
			}
		})
	}
}

func TestSourceLanguageMatchesTargetNormalizesLanguageTags(t *testing.T) {
	cases := []struct {
		source string
		target string
		want   bool
	}{
		{source: "en-US", target: "en", want: true},
		{source: "pt_BR", target: "pt", want: true},
		{source: "Korean", target: "ko", want: true},
		{source: "kr", target: "ko", want: false},
		{source: "es", target: "en", want: false},
		{source: "", target: "en", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.source+"_"+tc.target, func(t *testing.T) {
			if got := sourceLanguageMatchesTarget(tc.source, tc.target); got != tc.want {
				t.Fatalf("sourceLanguageMatchesTarget(%q, %q) = %v, want %v", tc.source, tc.target, got, tc.want)
			}
		})
	}
}

func TestKagiTranslateArgsUseKnownSourceLanguage(t *testing.T) {
	got := strings.Join(kagiTranslateArgs("안녕하세요", "en", "social media post", "ko"), "\x00")
	if !strings.Contains(got, "\x00--from\x00ko\x00") {
		t.Fatalf("kagi args missing source language: %q", got)
	}
	if !strings.Contains(got, "\x00--predicted-language\x00ko\x00") {
		t.Fatalf("kagi args missing predicted source language: %q", got)
	}

	got = strings.Join(kagiTranslateArgs("Mimpi basah WNI", "en", "", "in"), "\x00")
	if !strings.Contains(got, "\x00--from\x00id\x00") {
		t.Fatalf("kagi args did not normalize Indonesian source language: %q", got)
	}

	got = strings.Join(kagiTranslateArgs("hello", "en", "", "qam"), "\x00")
	if strings.Contains(got, "\x00--from\x00") {
		t.Fatalf("kagi args should not pass private-use source language: %q", got)
	}

	longContext := strings.Repeat("x", kagiContextMaxRunes+20)
	gotArgs := kagiTranslateArgs("bonjour", "en", longContext, "fr")
	for i, arg := range gotArgs {
		if arg != "--context" || i+1 >= len(gotArgs) {
			continue
		}
		if len([]rune(gotArgs[i+1])) != kagiContextMaxRunes {
			t.Fatalf("context length = %d, want %d", len([]rune(gotArgs[i+1])), kagiContextMaxRunes)
		}
		return
	}
	t.Fatalf("missing --context in kagi args: %#v", gotArgs)
}

func TestKagiMessageNeedsCooldown(t *testing.T) {
	msg := `configuration error: Kagi Translate language detection request rejected: HTTP 429 Too Many Requests`
	if !kagiMessageNeedsCooldown(msg) {
		t.Fatalf("expected Kagi 429 message to be rate limited")
	}
	msg = `authentication error: translate bootstrap did not mint a translate_session cookie`
	if !kagiMessageNeedsCooldown(msg) {
		t.Fatalf("expected Kagi bootstrap failure to trigger cooldown")
	}
	if kagiMessageNeedsCooldown("HTTP 500 Internal Server Error") {
		t.Fatalf("expected generic provider failure not to trigger cooldown")
	}
}

func TestKagiCooldownShortCircuitsAfterRateLimit(t *testing.T) {
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
	if err := d.SetSetting("", "translate_backend", settings.TranslateBackendKagiCLI); err != nil {
		t.Fatalf("SetSetting translate_backend: %v", err)
	}

	if _, _, err := translateTextWithDB(context.Background(), d, "안녕하세요", "en", "", "ko"); !errors.Is(err, ErrProviderRateLimited) {
		t.Fatalf("first translateTextWithDB err = %v, want ErrProviderRateLimited", err)
	}
	if _, _, err := translateTextWithDB(context.Background(), d, "你好", "en", "", "zh"); !errors.Is(err, ErrProviderRateLimited) {
		t.Fatalf("second translateTextWithDB err = %v, want ErrProviderRateLimited", err)
	}

	count, readErr := os.ReadFile(countPath)
	if readErr != nil {
		t.Fatalf("read kagi count: %v", readErr)
	}
	if len(count) != 1 {
		t.Fatalf("kagi requests = %d, want 1", len(count))
	}
}

func TestProtectOnlyTokensProducesPlaceholdersOnly(t *testing.T) {
	protected, originals := protectForTranslate("@alice #tag")
	if hasTranslatableContent(protected) {
		t.Errorf("expected no translatable content, got %q", protected)
	}
	if len(originals) != 2 {
		t.Fatalf("expected 2 originals, got %d", len(originals))
	}
}

func TestGoogleTranslateUsesConfiguredEndpointAndKey(t *testing.T) {
	var gotKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"translations":[{"translatedText":"Hello &amp; welcome","detectedSourceLanguage":"ja"}]}}`))
	}))
	defer srv.Close()

	result, err := googleTranslate(context.Background(), srv.URL, "test-key", "サンプル本文", "en")
	if err != nil {
		t.Fatalf("googleTranslate: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("key query = %q, want test-key", gotKey)
	}
	if gotBody["target"] != "en" || gotBody["format"] != "text" {
		t.Fatalf("request body = %#v", gotBody)
	}
	if result.TranslatedText != "Hello & welcome" || result.SourceLang != "Japanese" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDeepLTranslateUsesAuthorizationHeader(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"translations":[{"text":"Hello","detected_source_language":"JA"}]}`))
	}))
	defer srv.Close()

	result, err := deeplTranslate(context.Background(), srv.URL, "deepl-key", "サンプル本文", "en")
	if err != nil {
		t.Fatalf("deeplTranslate: %v", err)
	}
	if gotAuth != "DeepL-Auth-Key deepl-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBody["target_lang"] != "EN" {
		t.Fatalf("target_lang = %v, want EN", gotBody["target_lang"])
	}
	if result.TranslatedText != "Hello" || result.SourceLang != "Japanese" {
		t.Fatalf("result = %#v", result)
	}
}

func TestOpenAICompatTranslateUsesBareEndpointAndModelWithoutKey(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"translated_text\":\"Hello {{0}}\",\"source_language\":\"Korean\"}"}}]}`))
	}))
	defer srv.Close()

	result, err := openAICompatTranslate(context.Background(), srv.URL, "", "qwen2.5:7b", "サンプル本文 {{0}}", "en", "social media post")
	if err != nil {
		t.Fatalf("openAICompatTranslate: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want empty when API key is blank", gotAuth)
	}
	if gotBody["model"] != "qwen2.5:7b" {
		t.Fatalf("model = %v, want qwen2.5:7b", gotBody["model"])
	}
	messages, ok := gotBody["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages missing from request: %#v", gotBody)
	}
	if result.TranslatedText != "Hello {{0}}" || result.SourceLang != "Korean" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDefaultDeepLEndpointUsesFreeForFXKeys(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"free", "abc:fx", "https://api-free.deepl.com/v2/translate"},
		{"free_trimmed", " abc:fx ", "https://api-free.deepl.com/v2/translate"},
		{"pro", "abc", "https://api.deepl.com/v2/translate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultDeepLEndpoint(tc.key); got != tc.want {
				t.Fatalf("defaultDeepLEndpoint(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}
