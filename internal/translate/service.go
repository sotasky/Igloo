package translate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/settings"
)

var (
	// Combined token regex: URL first (most specific), then mention, then hashtag.
	// One pass preserves positional order so indexes match reading order.
	reToken    = regexp.MustCompile(`https?://\S+|@\S+|#\S+`)
	reWS       = regexp.MustCompile(`[ \t]+`)
	reSentinel = regexp.MustCompile(`\{\{\s*(\d+)\s*\}\}`)

	ErrNotConfigured         = errors.New("translation provider not configured")
	ErrFeedItemNotFound      = errors.New("feed item not found")
	ErrUnsupportedField      = errors.New("unsupported translation field")
	ErrNoText                = errors.New("no text to translate")
	ErrTranslationFailed     = errors.New("translation failed")
	ErrAlreadyTargetLanguage = errors.New("already in target language")
)

type Result struct {
	TranslatedText string
	SourceLang     string
	TargetLang     string
	Provider       string
}

type AlreadyTargetLanguageError struct {
	SourceLang string
}

func (e AlreadyTargetLanguageError) Error() string {
	return ErrAlreadyTargetLanguage.Error()
}

func (e AlreadyTargetLanguageError) Is(target error) bool {
	return target == ErrAlreadyTargetLanguage
}

// FeedText translates one stored feed text field and writes the cache entry.
// The cache is checked first, so callers can use it for both manual and
// background translation paths without duplicating provider behavior.
func FeedText(ctx context.Context, database *db.DB, tweetID, field, targetLang string) (*Result, error) {
	tweetID = strings.TrimSpace(tweetID)
	field = strings.TrimSpace(field)
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	if targetLang == "" {
		targetLang = "en"
	}
	if field != "body" && field != "quote" {
		return nil, ErrUnsupportedField
	}

	cached, cachedLang, err := database.GetTranslation(tweetID, field, targetLang)
	if err == nil {
		return &Result{
			TranslatedText: cached,
			SourceLang:     cachedLang,
			TargetLang:     targetLang,
			Provider:       "cache",
		}, nil
	}
	if err != sql.ErrNoRows {
		slog.Error("GetTranslation cache check", "tweet_id", tweetID, "err", err)
	}

	items, err := database.GetFeedItemsForTweetIDs([]string{tweetID})
	if err != nil {
		return nil, err
	}
	fi, ok := items[tweetID]
	if !ok {
		return nil, ErrFeedItemNotFound
	}

	var sourceText, detectedLang string
	switch field {
	case "body":
		sourceText = fi.BodyText
		detectedLang = fi.Lang
	case "quote":
		sourceText = fi.QuoteBodyText
		detectedLang = fi.QuoteLang
	}
	if strings.TrimSpace(sourceText) == "" {
		return nil, ErrNoText
	}

	cleanSource, placeholders := protectForTranslate(sourceText)
	if !hasTranslatableContent(cleanSource) {
		return nil, ErrNoText
	}
	if sourceLanguageMatchesTarget(detectedLang, targetLang) {
		return nil, AlreadyTargetLanguageError{SourceLang: strings.ToLower(strings.TrimSpace(detectedLang))}
	}

	contextHint := stripForTranslateContext(buildContext(fi.BodyText, fi.QuoteBodyText, field))
	translated, provider, err := translateTextWithDB(ctx, database, cleanSource, targetLang, contextHint)
	if err != nil {
		return nil, err
	}
	if translated == nil {
		return nil, ErrTranslationFailed
	}
	if strings.TrimSpace(translated.TranslatedText) == "" {
		return nil, ErrTranslationFailed
	}

	srcLang := strings.ToLower(strings.TrimSpace(translated.SourceLang))
	if srcLang != "" && srcLang == targetLang {
		return nil, AlreadyTargetLanguageError{SourceLang: srcLang}
	}

	text := restoreFromTranslate(translated.TranslatedText, placeholders)
	if !preservedProtectedTokens(translated.TranslatedText, text, placeholders) {
		return nil, ErrTranslationFailed
	}
	cacheLang := srcLang
	if cacheLang == "" {
		cacheLang = strings.ToLower(strings.TrimSpace(detectedLang))
	}
	if cacheLang == "" {
		cacheLang = "und"
	}
	if err := database.SetTranslation(tweetID, field, cacheLang, targetLang, text); err != nil {
		slog.Warn("SetTranslation cache write", "tweet_id", tweetID, "err", err)
	}

	return &Result{
		TranslatedText: text,
		SourceLang:     srcLang,
		TargetLang:     targetLang,
		Provider:       provider,
	}, nil
}

// protectForTranslate replaces URLs, mentions, and hashtags with numbered
// placeholders so the translator preserves them verbatim. Placeholders are
// numbered in order of appearance. Returns the protected text and the
// ordered list of originals for restoration.
func protectForTranslate(text string) (string, []string) {
	var originals []string
	out := reToken.ReplaceAllStringFunc(text, func(original string) string {
		s := fmt.Sprintf("{{%d}}", len(originals))
		originals = append(originals, original)
		return s
	})
	out = reWS.ReplaceAllString(out, " ")
	return strings.TrimSpace(out), originals
}

// restoreFromTranslate swaps placeholders in translator output back to the
// original URL/mention/hashtag strings. Whitespace inside braces is tolerated
// since some translators insert it.
func restoreFromTranslate(text string, originals []string) string {
	return reSentinel.ReplaceAllStringFunc(text, func(match string) string {
		sub := reSentinel.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		idx, err := strconv.Atoi(sub[1])
		if err != nil || idx < 0 || idx >= len(originals) {
			return match
		}
		return originals[idx]
	})
}

func preservedProtectedTokens(translated, restored string, originals []string) bool {
	if len(originals) == 0 {
		return true
	}
	sentinelMatches := reSentinel.FindAllStringSubmatch(translated, -1)
	seen := make(map[int]bool, len(sentinelMatches))
	for _, match := range sentinelMatches {
		if len(match) < 2 {
			continue
		}
		idx, err := strconv.Atoi(match[1])
		if err == nil {
			seen[idx] = true
		}
	}
	for idx, original := range originals {
		if seen[idx] || strings.Contains(restored, original) {
			continue
		}
		return false
	}
	return true
}

// stripForTranslateContext drops URLs, mentions, and hashtags from the
// translator context hint. Context isn't part of the output, so no
// preservation is needed.
func stripForTranslateContext(text string) string {
	out := reToken.ReplaceAllString(text, "")
	out = reWS.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

// hasTranslatableContent reports whether protected text has any non-placeholder,
// non-whitespace letter or number worth translating. Emoji and punctuation
// alone should not cause token-only posts to be sent to a translator.
func hasTranslatableContent(protected string) bool {
	text := strings.TrimSpace(reSentinel.ReplaceAllString(protected, ""))
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

func looksAlreadyReadableInTarget(protected, targetLang string) bool {
	if strings.ToLower(strings.TrimSpace(targetLang)) != "en" {
		return false
	}
	text := strings.TrimSpace(reSentinel.ReplaceAllString(protected, ""))
	hasText := false
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		hasText = true
		if r > unicode.MaxASCII {
			return false
		}
	}
	return hasText
}

func sourceLanguageMatchesTarget(sourceLang, targetLang string) bool {
	src := normalizeLanguageCode(sourceLang)
	dst := normalizeLanguageCode(targetLang)
	return src != "" && dst != "" && src == dst
}

func normalizeLanguageCode(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if idx := strings.IndexAny(lang, "-_"); idx >= 0 {
		lang = lang[:idx]
	}
	return lang
}

// buildContext builds context for quote tweet translations.
func buildContext(bodyText, quoteBodyText, field string) string {
	if field == "body" && quoteBodyText != "" {
		return "You are translating a post that quotes another post.\nQuoted post: \"" + quoteBodyText + "\""
	}
	if field == "quote" && bodyText != "" {
		return "You are translating a quoted post. The post quoting it says:\n\"" + bodyText + "\""
	}
	return "social media post"
}

func translateTextWithDB(ctx context.Context, database *db.DB, text, targetLang, contextHint string) (*Result, string, error) {
	backendRaw, _ := database.GetSetting("translate_backend", settings.TranslateBackendNone)
	backend := settings.NormalizeTranslateBackend(backendRaw)

	switch backend {
	case settings.TranslateBackendKagiCLI:
		result, err := kagiTranslate(ctx, text, targetLang, contextHint)
		return result, backend, err
	case settings.TranslateBackendGoogle:
		apiKey, _ := database.GetSetting("translate_api_key", "")
		apiSite, _ := database.GetSetting("translate_api_site", "")
		if strings.TrimSpace(apiKey) == "" {
			return nil, backend, ErrNotConfigured
		}
		result, err := googleTranslate(ctx, apiSite, apiKey, text, targetLang)
		return result, backend, err
	case settings.TranslateBackendDeepL:
		apiKey, _ := database.GetSetting("translate_api_key", "")
		apiSite, _ := database.GetSetting("translate_api_site", "")
		if strings.TrimSpace(apiKey) == "" {
			return nil, backend, ErrNotConfigured
		}
		result, err := deeplTranslate(ctx, apiSite, apiKey, text, targetLang)
		return result, backend, err
	case settings.TranslateBackendOpenAICompat:
		apiKey, _ := database.GetSetting("translate_api_key", "")
		apiSite, _ := database.GetSetting("translate_api_site", "")
		model, _ := database.GetSetting("translate_model", "")
		if strings.TrimSpace(model) == "" {
			return nil, backend, ErrNotConfigured
		}
		result, err := openAICompatTranslate(ctx, apiSite, apiKey, model, text, targetLang, contextHint)
		return result, backend, err
	default:
		return nil, backend, ErrNotConfigured
	}
}

// kagiTranslate calls the kagi CLI to translate text and returns the result.
func kagiTranslate(ctx context.Context, text, targetLang, contextHint string) (*Result, error) {
	args := []string{"translate", "--to", targetLang, "--no-alternatives", "--no-word-insights"}
	if contextHint != "" {
		args = append(args, "--context", contextHint)
	}
	args = append(args, text)
	cmd := exec.CommandContext(ctx, "kagi", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 512 {
			msg = msg[:512] + "..."
		}
		if msg != "" {
			return nil, fmt.Errorf("kagi translate: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("kagi translate: %w", err)
	}

	var data struct {
		Translation struct {
			Translation    string `json:"translation"`
			SourceLanguage string `json:"source_language"`
		} `json:"translation"`
		DetectedLanguage struct {
			ISO string `json:"iso"`
		} `json:"detected_language"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
		return nil, err
	}
	if data.Translation.Translation == "" {
		return nil, nil
	}

	sourceLang := data.DetectedLanguage.ISO
	if sourceLang == "" {
		sourceLang = data.Translation.SourceLanguage
	}
	sourceLang = strings.ToLower(sourceLang)
	sourceLang = strings.ReplaceAll(sourceLang, "zh_cn", "zh")
	sourceLang = strings.ReplaceAll(sourceLang, "zh_tw", "zh-tw")

	return &Result{
		TranslatedText: data.Translation.Translation,
		SourceLang:     sourceLang,
	}, nil
}

func googleTranslate(ctx context.Context, endpoint, apiKey, text, targetLang string) (*Result, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "https://translation.googleapis.com/language/translate/v2"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("key", strings.TrimSpace(apiKey))
	u.RawQuery = q.Encode()

	body, err := json.Marshal(map[string]any{
		"q":      text,
		"target": strings.ToLower(strings.TrimSpace(targetLang)),
		"format": "text",
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("google translate status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var data struct {
		Data struct {
			Translations []struct {
				TranslatedText         string `json:"translatedText"`
				DetectedSourceLanguage string `json:"detectedSourceLanguage"`
			} `json:"translations"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Data.Translations) == 0 {
		return nil, nil
	}
	tr := data.Data.Translations[0]
	return &Result{
		TranslatedText: html.UnescapeString(tr.TranslatedText),
		SourceLang:     strings.ToLower(tr.DetectedSourceLanguage),
	}, nil
}

func deeplTranslate(ctx context.Context, endpoint, apiKey, text, targetLang string) (*Result, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = defaultDeepLEndpoint(apiKey)
	}
	body, err := json.Marshal(map[string]any{
		"text":        []string{text},
		"target_lang": deeplTargetLang(targetLang),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+strings.TrimSpace(apiKey))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("deepl translate status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var data struct {
		Translations []struct {
			Text                   string `json:"text"`
			DetectedSourceLanguage string `json:"detected_source_language"`
		} `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Translations) == 0 {
		return nil, nil
	}
	tr := data.Translations[0]
	return &Result{
		TranslatedText: tr.Text,
		SourceLang:     strings.ToLower(tr.DetectedSourceLanguage),
	}, nil
}

func defaultDeepLEndpoint(apiKey string) string {
	if strings.HasSuffix(strings.TrimSpace(apiKey), ":fx") {
		return "https://api-free.deepl.com/v2/translate"
	}
	return "https://api.deepl.com/v2/translate"
}

func deeplTargetLang(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return "EN"
	}
	return strings.ToUpper(lang)
}

func openAICompatTranslate(ctx context.Context, endpoint, apiKey, model, text, targetLang, contextHint string) (*Result, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, ErrNotConfigured
	}
	endpointURL, err := openAICompatEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	prompt := "Translate the provided social-media text. Preserve every placeholder like {{0}} exactly. Return only JSON with keys translated_text and source_lang."
	user := "Target language: " + strings.ToLower(strings.TrimSpace(targetLang)) + "\n"
	if strings.TrimSpace(contextHint) != "" {
		user += "Context: " + strings.TrimSpace(contextHint) + "\n"
	}
	user += "Text:\n" + text

	body, err := json.Marshal(map[string]any{
		"model":       model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": prompt},
			{"role": "user", "content": user},
		},
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("openai-compatible translate status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var data struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Choices) == 0 || strings.TrimSpace(data.Choices[0].Message.Content) == "" {
		return nil, nil
	}
	return parseOpenAICompatTranslation(data.Choices[0].Message.Content)
}

func openAICompatEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434"
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	switch strings.TrimRight(u.Path, "/") {
	case "":
		u.Path = "/v1/chat/completions"
	case "/v1":
		u.Path = "/v1/chat/completions"
	}
	return u.String(), nil
}

func parseOpenAICompatTranslation(content string) (*Result, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var data struct {
		TranslatedText string `json:"translated_text"`
		Translation    string `json:"translation"`
		SourceLang     string `json:"source_lang"`
		SourceLanguage string `json:"source_language"`
	}
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(data.TranslatedText)
	if text == "" {
		text = strings.TrimSpace(data.Translation)
	}
	if text == "" {
		return nil, nil
	}
	sourceLang := strings.ToLower(strings.TrimSpace(data.SourceLang))
	if sourceLang == "" {
		sourceLang = strings.ToLower(strings.TrimSpace(data.SourceLanguage))
	}
	sourceLang = strings.ReplaceAll(sourceLang, "zh_cn", "zh")
	sourceLang = strings.ReplaceAll(sourceLang, "zh_tw", "zh-tw")
	return &Result{
		TranslatedText: text,
		SourceLang:     sourceLang,
	}, nil
}
