package translate

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/language"
	"github.com/screwys/igloo/internal/settings"
)

const (
	translateBackgroundStartupDelay   = 5 * time.Second
	translateBackgroundActiveDelay    = 2 * time.Second
	translateBackgroundIdleDelay      = 30 * time.Second
	translateBackgroundErrorDelay     = time.Minute
	translateBackgroundRateLimitDelay = kagiProviderCooldownDuration
	translateBackgroundScanLimit      = 500
	translateBackgroundBatchSize      = 10
	translateBackgroundMaxErrors      = translateBackgroundBatchSize
	translateBackgroundProviderDelay  = 1500 * time.Millisecond
)

var translateSkipScriptPatterns = map[string]*regexp.Regexp{
	"ja": regexp.MustCompile(`[\x{3040}-\x{30FF}\x{FF66}-\x{FF9F}]`),
}

type translateBackgroundConfig struct {
	mode     string
	backend  string
	target   string
	skip     []string
	skipSet  map[string]bool
	delay    time.Duration
	stateKey string
}

type translateBackgroundSkip struct {
	sourceText string
	retryable  bool
}

// RunBackground continuously fills the translation cache when the
// auto-translate mode is set to background.
func RunBackground(ctx context.Context, database *db.DB) {
	if database == nil {
		return
	}

	timer := time.NewTimer(translateBackgroundStartupDelay)
	defer timer.Stop()

	var cfg translateBackgroundConfig
	skipped := make(map[string]translateBackgroundSkip)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		nextCfg := loadTranslateBackgroundConfig(database)
		if nextCfg.stateKey != cfg.stateKey {
			cfg = nextCfg
			skipped = make(map[string]translateBackgroundSkip)
		}

		translated, err := runTranslateBackgroundBatch(ctx, database, cfg, skipped)
		delay := translateBackgroundIdleDelay
		if translated > 0 {
			delay = translateBackgroundActiveDelay
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil) {
				return
			}
			slog.Warn("translate background batch failed", "err", err)
			if errors.Is(err, ErrProviderRateLimited) {
				delay = translateBackgroundRateLimitDelay
			} else {
				delay = translateBackgroundErrorDelay
			}
		}
		timer.Reset(delay)
	}
}

func loadTranslateBackgroundConfig(database *db.DB) translateBackgroundConfig {
	modeRaw, _ := database.GetSetting("translate_auto_mode", settings.TranslateAutoLazy)
	backendRaw, _ := database.GetSetting("translate_backend", settings.TranslateBackendNone)
	targetRaw, _ := database.GetSetting("translate_target_lang", "en")
	skipRaw, _ := database.GetSetting("translate_skip_langs", "")

	target := strings.ToLower(strings.TrimSpace(targetRaw))
	if target == "" {
		target = "en"
	}
	skipText := normalizeSkipLangs(skipRaw)
	skip := splitTranslateLangs(skipText)
	skipSet := make(map[string]bool, len(skip))
	for _, lang := range skip {
		skipSet[lang] = true
	}

	mode := settings.NormalizeTranslateAutoMode(modeRaw)
	backend := settings.NormalizeTranslateBackend(backendRaw)
	if !translateBackendConfigured(database, backend) {
		backend = settings.TranslateBackendNone
	}
	return translateBackgroundConfig{
		mode:     mode,
		backend:  backend,
		target:   target,
		skip:     skip,
		skipSet:  skipSet,
		delay:    translateBackgroundProviderDelay,
		stateKey: strings.Join([]string{mode, backend, target, skipText}, "\x00"),
	}
}

func translateBackendConfigured(database *db.DB, backend string) bool {
	switch backend {
	case settings.TranslateBackendGoogle, settings.TranslateBackendDeepL:
		apiKey, _ := database.GetSetting("translate_api_key", "")
		return strings.TrimSpace(apiKey) != ""
	case settings.TranslateBackendOpenAICompat:
		model, _ := database.GetSetting("translate_model", "")
		return strings.TrimSpace(model) != ""
	case settings.TranslateBackendKagiCLI:
		return true
	default:
		return false
	}
}

func splitTranslateLangs(raw string) []string {
	var out []string
	for _, lang := range strings.Split(raw, ",") {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang != "" {
			out = append(out, lang)
		}
	}
	return out
}

func normalizeSkipLangs(raw string) string {
	seen := make(map[string]bool)
	var out []string
	for _, lang := range strings.Split(raw, ",") {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "" || seen[lang] {
			continue
		}
		seen[lang] = true
		out = append(out, lang)
	}
	return strings.Join(out, ",")
}

func runTranslateBackgroundBatch(ctx context.Context, database *db.DB, cfg translateBackgroundConfig, skipped map[string]translateBackgroundSkip) (int, error) {
	if cfg.mode != settings.TranslateAutoBackground || cfg.backend == settings.TranslateBackendNone {
		return 0, nil
	}

	if _, err := database.EnqueueTranslationCandidates(cfg.target, cfg.skip, translateBackgroundScanLimit); err != nil {
		return 0, err
	}

	translated := 0
	failures := 0
	attempted := 0
	var lastErr error
	for translated < translateBackgroundBatchSize {
		if translated >= translateBackgroundBatchSize {
			break
		}
		if err := ctx.Err(); err != nil {
			return translated, err
		}

		job, err := database.ClaimTranslationJob(cfg.target, time.Now().UnixMilli())
		if err != nil {
			return translated, err
		}
		if job == nil {
			break
		}

		if attempted > 0 && cfg.delay > 0 {
			timer := time.NewTimer(cfg.delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return translated, ctx.Err()
			case <-timer.C:
			}
		}
		attempted++
		candidate := db.TranslationCandidate{
			TweetID:       job.TweetID,
			Field:         job.Field,
			SourceText:    job.SourceText,
			SourceLang:    job.SourceLang,
			BodyText:      job.BodyText,
			QuoteBodyText: job.QuoteBodyText,
		}
		key := translateBackgroundCandidateKey(candidate, cfg.target)
		if shouldSkipTranslateBackgroundCandidate(skipped, key, candidate.SourceText) {
			_ = database.SkipTranslationJob(job.TweetID, job.Field, job.TargetLang, "locally skipped")
			continue
		}
		if !shouldAutoTranslateCandidate(candidate.SourceLang, candidate.SourceText, cfg.target, cfg.skipSet) {
			skipped[key] = translateBackgroundSkip{sourceText: candidate.SourceText}
			_ = database.SkipTranslationJob(job.TweetID, job.Field, job.TargetLang, "not eligible")
			continue
		}
		wrote, err := translateAndCacheBackgroundCandidate(ctx, database, cfg, candidate)
		if err != nil {
			if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil) {
				return translated, err
			}
			if errors.Is(err, ErrProviderRateLimited) {
				_ = database.RetryTranslationJob(job.TweetID, job.Field, job.TargetLang, "rate_limited", err.Error(), translateBackgroundRateLimitDelay)
				return translated, err
			}
			failures++
			lastErr = err
			skipped[key] = translateBackgroundSkip{sourceText: candidate.SourceText, retryable: true}
			_ = database.RetryTranslationJob(job.TweetID, job.Field, job.TargetLang, "provider_error", err.Error(), translateBackgroundErrorDelay)
			if failures >= translateBackgroundMaxErrors {
				break
			}
			continue
		}
		if wrote {
			translated++
			delete(skipped, key)
			_ = database.CompleteTranslationJob(job.TweetID, job.Field, job.TargetLang)
			continue
		}
		skipped[key] = translateBackgroundSkip{sourceText: candidate.SourceText}
		_ = database.SkipTranslationJob(job.TweetID, job.Field, job.TargetLang, "no translation written")
	}
	if translated == 0 && lastErr != nil {
		return translated, lastErr
	}
	return translated, nil
}

func shouldSkipTranslateBackgroundCandidate(skipped map[string]translateBackgroundSkip, key, sourceText string) bool {
	state, ok := skipped[key]
	if !ok {
		return false
	}
	if state.sourceText != sourceText {
		delete(skipped, key)
		return false
	}
	if state.retryable {
		delete(skipped, key)
	}
	return true
}

func translateAndCacheBackgroundCandidate(ctx context.Context, database *db.DB, cfg translateBackgroundConfig, candidate db.TranslationCandidate) (bool, error) {
	cleanSource, placeholders := protectForTranslate(candidate.SourceText)
	if !hasTranslatableContent(cleanSource) {
		return false, nil
	}

	contextHint := stripForTranslateContext(buildContext(candidate.BodyText, candidate.QuoteBodyText, candidate.Field))
	translateCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	result, provider, err := translateTextWithDB(translateCtx, database, cleanSource, cfg.target, contextHint, candidate.SourceLang)
	if err != nil || result == nil {
		slog.Warn("translate background candidate failed", "provider", provider, "tweet_id", candidate.TweetID, "field", candidate.Field, "err", err)
		return false, err
	}

	translatedText := strings.TrimSpace(result.TranslatedText)
	if translatedText == "" {
		return false, nil
	}
	restoredText := restoreFromTranslate(translatedText, placeholders)
	if !preservedProtectedTokens(translatedText, restoredText, placeholders) {
		slog.Warn("translate background candidate dropped protected token", "provider", provider, "tweet_id", candidate.TweetID, "field", candidate.Field)
		return false, nil
	}

	srcLang := language.DisplayName(result.SourceLang)
	if sourceLanguageMatchesTarget(srcLang, cfg.target) {
		return false, nil
	}
	if language.InSet(srcLang, cfg.skipSet) {
		return false, nil
	}

	translatedText = restoredText
	cacheLang := srcLang
	if cacheLang == "" {
		cacheLang = language.DisplayName(candidate.SourceLang)
	}
	if cacheLang == "" {
		cacheLang = "und"
	}
	if err := database.SetTranslation(candidate.TweetID, candidate.Field, cacheLang, cfg.target, translatedText); err != nil {
		return false, err
	}
	return true, nil
}

func shouldAutoTranslateCandidate(sourceLang, sourceText, targetLang string, skipSet map[string]bool) bool {
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	if targetLang == "" {
		targetLang = "en"
	}
	if !language.IsUnknown(sourceLang) {
		if sourceLanguageMatchesTarget(sourceLang, targetLang) || language.InSet(sourceLang, skipSet) {
			return false
		}
		cleanSource, _ := protectForTranslate(sourceText)
		if hasSkippedLanguageScript(cleanSource, skipSet) {
			return false
		}
		return hasTranslatableContent(cleanSource)
	}

	cleanSource, _ := protectForTranslate(sourceText)
	if hasSkippedLanguageScript(cleanSource, skipSet) {
		return false
	}
	if looksAlreadyReadableInTarget(cleanSource, targetLang) {
		return false
	}
	return hasTranslatableContent(cleanSource)
}

func hasSkippedLanguageScript(sourceText string, skipSet map[string]bool) bool {
	if len(skipSet) == 0 || sourceText == "" {
		return false
	}
	for lang := range skipSet {
		pattern := translateSkipScriptPatterns[lang]
		if pattern != nil && pattern.MatchString(sourceText) {
			return true
		}
	}
	return false
}

func translateBackgroundCandidateKey(candidate db.TranslationCandidate, targetLang string) string {
	return candidate.TweetID + "\x00" + candidate.Field + "\x00" + targetLang
}
