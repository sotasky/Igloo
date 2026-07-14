package translate

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"sort"
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

type translateBackgroundWorkItem struct {
	candidate    db.TranslationCandidate
	cleanSource  string
	placeholders []string
	contextHint  string
	threadOrder  int
}

type translateBackgroundWorkGroup struct {
	key   string
	items []translateBackgroundWorkItem
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

	translated := 0
	failures := 0
	attempted := 0
	var lastErr error
	for translated < translateBackgroundBatchSize {
		if err := ctx.Err(); err != nil {
			return translated, err
		}

		jobs, err := database.ClaimTranslationJobs(cfg.target, time.Now().UnixMilli(), translateBackgroundBatchSize-translated)
		if err != nil {
			return translated, err
		}
		if len(jobs) == 0 {
			break
		}

		groupsByKey := make(map[string]*translateBackgroundWorkGroup)
		var groupOrder []string
		for _, job := range jobs {
			if err := ctx.Err(); err != nil {
				return translated, err
			}
			if _, _, err := database.GetTranslation(job.TweetID, job.Field, job.TargetLang); err == nil {
				_ = database.CompleteTranslationJob(job.TweetID, job.Field, job.TargetLang)
				continue
			} else if err != sql.ErrNoRows {
				_ = database.RetryTranslationJob(job.TweetID, job.Field, job.TargetLang, "cache_read", err.Error(), translateBackgroundErrorDelay)
				return translated, err
			}

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
			if _, ok, err := reusableTranslation(database, job.TweetID, job.Field, candidate.SourceText, cfg.target, cfg.skipSet); err != nil {
				failures++
				lastErr = err
				skipped[key] = translateBackgroundSkip{sourceText: candidate.SourceText, retryable: true}
				_ = database.RetryTranslationJob(job.TweetID, job.Field, job.TargetLang, "reuse_error", err.Error(), translateBackgroundErrorDelay)
				continue
			} else if ok {
				translated++
				delete(skipped, key)
				_ = database.CompleteTranslationJob(job.TweetID, job.Field, job.TargetLang)
				continue
			}

			cleanSource, placeholders := protectForTranslate(candidate.SourceText)
			if !hasTranslatableContent(cleanSource) {
				skipped[key] = translateBackgroundSkip{sourceText: candidate.SourceText}
				_ = database.SkipTranslationJob(job.TweetID, job.Field, job.TargetLang, "no translatable content")
				continue
			}

			groupKey, threadOrder := translateBackgroundGroupKey(database, cfg.backend, candidate, cfg.target)
			group := groupsByKey[groupKey]
			if group == nil {
				group = &translateBackgroundWorkGroup{key: groupKey}
				groupsByKey[groupKey] = group
				groupOrder = append(groupOrder, groupKey)
			}
			group.items = append(group.items, translateBackgroundWorkItem{
				candidate:    candidate,
				cleanSource:  cleanSource,
				placeholders: placeholders,
				contextHint:  stripForTranslateContext(buildContext(candidate.BodyText, candidate.QuoteBodyText, candidate.Field)),
				threadOrder:  threadOrder,
			})
		}

		groups := make([]translateBackgroundWorkGroup, 0, len(groupOrder))
		for _, groupKey := range groupOrder {
			group := groupsByKey[groupKey]
			if group == nil || len(group.items) == 0 {
				continue
			}
			sort.SliceStable(group.items, func(i, j int) bool {
				if group.items[i].threadOrder != group.items[j].threadOrder {
					return group.items[i].threadOrder < group.items[j].threadOrder
				}
				if group.items[i].candidate.TweetID != group.items[j].candidate.TweetID {
					return group.items[i].candidate.TweetID < group.items[j].candidate.TweetID
				}
				return group.items[i].candidate.Field < group.items[j].candidate.Field
			})
			groups = append(groups, *group)
		}

		for i, group := range groups {
			if err := ctx.Err(); err != nil {
				return translated, err
			}
			remaining, reused, err := reuseBackgroundWorkItems(database, cfg, group.items, skipped)
			translated += reused
			if err != nil {
				failures++
				lastErr = err
				if failures >= translateBackgroundMaxErrors {
					if i+1 < len(groups) {
						retryBackgroundGroups(database, cfg, groups[i+1:], "provider_error", err.Error(), translateBackgroundErrorDelay, skipped)
					}
					break
				}
			}
			group.items = remaining
			groups[i].items = remaining
			if len(group.items) == 0 {
				continue
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

			wrote, err := translateAndCacheBackgroundCandidates(ctx, database, cfg, group.items, skipped)
			if err != nil {
				if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil) {
					return translated, err
				}
				if errors.Is(err, ErrProviderRateLimited) {
					retryBackgroundGroups(database, cfg, groups[i:], "rate_limited", err.Error(), translateBackgroundRateLimitDelay, skipped)
					return translated, err
				}
				failures += len(group.items)
				lastErr = err
				retryBackgroundGroups(database, cfg, groups[i:i+1], "provider_error", err.Error(), translateBackgroundErrorDelay, skipped)
				if failures >= translateBackgroundMaxErrors {
					if i+1 < len(groups) {
						retryBackgroundGroups(database, cfg, groups[i+1:], "provider_error", err.Error(), translateBackgroundErrorDelay, skipped)
					}
					break
				}
				continue
			}
			translated += wrote
		}

		if failures >= translateBackgroundMaxErrors {
			break
		}
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

func translateAndCacheBackgroundCandidates(ctx context.Context, database *db.DB, cfg translateBackgroundConfig, items []translateBackgroundWorkItem, skipped map[string]translateBackgroundSkip) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	requests := make([]translationTextRequest, 0, len(items))
	for _, item := range items {
		requests = append(requests, translationTextRequest{
			Text:           item.cleanSource,
			ContextHint:    item.contextHint,
			SourceLangHint: item.candidate.SourceLang,
		})
	}

	translateCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	results, provider, err := translateTextBatchWithDB(translateCtx, database, requests, cfg.target)
	if err != nil {
		slog.Warn("translate background group failed", "provider", provider, "items", len(items), "err", err)
		return 0, err
	}
	if len(results) != len(items) {
		return 0, ErrTranslationFailed
	}

	wrote := 0
	for i, result := range results {
		item := items[i]
		candidate := item.candidate
		key := translateBackgroundCandidateKey(candidate, cfg.target)
		if result == nil {
			retryBackgroundWorkItem(database, cfg, item, "provider_error", "no translation written", translateBackgroundErrorDelay, skipped)
			continue
		}

		translatedText := strings.TrimSpace(result.TranslatedText)
		if translatedText == "" {
			retryBackgroundWorkItem(database, cfg, item, "provider_error", "no translation written", translateBackgroundErrorDelay, skipped)
			continue
		}
		restoredText := restoreFromTranslate(translatedText, item.placeholders)
		if !preservedProtectedTokens(translatedText, restoredText, item.placeholders) {
			slog.Warn("translate background candidate dropped protected token", "provider", provider, "tweet_id", candidate.TweetID, "field", candidate.Field)
			retryBackgroundWorkItem(database, cfg, item, "provider_error", "protected token dropped", translateBackgroundErrorDelay, skipped)
			continue
		}

		srcLang := language.DisplayName(result.SourceLang)
		if sourceLanguageMatchesTarget(srcLang, cfg.target) || language.InSet(srcLang, cfg.skipSet) {
			skipped[key] = translateBackgroundSkip{sourceText: candidate.SourceText}
			_ = database.SkipTranslationJob(candidate.TweetID, candidate.Field, cfg.target, "not eligible")
			continue
		}

		cacheLang := srcLang
		if cacheLang == "" {
			cacheLang = language.DisplayName(candidate.SourceLang)
		}
		if cacheLang == "" {
			cacheLang = "und"
		}
		if err := database.SetTranslation(candidate.TweetID, candidate.Field, cacheLang, cfg.target, restoredText); err != nil {
			return wrote, err
		}
		if err := database.CompleteTranslationJob(candidate.TweetID, candidate.Field, cfg.target); err != nil {
			return wrote, err
		}
		wrote++
		delete(skipped, key)
	}
	return wrote, nil
}

func retryBackgroundGroups(database *db.DB, cfg translateBackgroundConfig, groups []translateBackgroundWorkGroup, kind, message string, delay time.Duration, skipped map[string]translateBackgroundSkip) {
	for _, group := range groups {
		for _, item := range group.items {
			retryBackgroundWorkItem(database, cfg, item, kind, message, delay, skipped)
		}
	}
}

func retryBackgroundWorkItem(database *db.DB, cfg translateBackgroundConfig, item translateBackgroundWorkItem, kind, message string, delay time.Duration, skipped map[string]translateBackgroundSkip) {
	candidate := item.candidate
	key := translateBackgroundCandidateKey(candidate, cfg.target)
	skipped[key] = translateBackgroundSkip{sourceText: candidate.SourceText, retryable: true}
	_ = database.RetryTranslationJob(candidate.TweetID, candidate.Field, cfg.target, kind, message, delay)
}

func reuseBackgroundWorkItems(database *db.DB, cfg translateBackgroundConfig, items []translateBackgroundWorkItem, skipped map[string]translateBackgroundSkip) ([]translateBackgroundWorkItem, int, error) {
	remaining := make([]translateBackgroundWorkItem, 0, len(items))
	reused := 0
	var firstErr error
	for _, item := range items {
		candidate := item.candidate
		key := translateBackgroundCandidateKey(candidate, cfg.target)
		if _, ok, err := reusableTranslation(database, candidate.TweetID, candidate.Field, candidate.SourceText, cfg.target, cfg.skipSet); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			skipped[key] = translateBackgroundSkip{sourceText: candidate.SourceText, retryable: true}
			_ = database.RetryTranslationJob(candidate.TweetID, candidate.Field, cfg.target, "reuse_error", err.Error(), translateBackgroundErrorDelay)
			continue
		} else if ok {
			reused++
			delete(skipped, key)
			_ = database.CompleteTranslationJob(candidate.TweetID, candidate.Field, cfg.target)
			continue
		}
		remaining = append(remaining, item)
	}
	return remaining, reused, firstErr
}

func translateBackgroundGroupKey(database *db.DB, backend string, candidate db.TranslationCandidate, targetLang string) (string, int) {
	unique := translateBackgroundCandidateKey(candidate, targetLang)
	if !providerSupportsBatch(backend) || candidate.Field != "body" || language.IsUnknown(candidate.SourceLang) {
		return unique, 0
	}
	chain, err := database.GetThreadChain(candidate.TweetID)
	if err != nil || len(chain) == 0 {
		return unique, 0
	}
	rootID := strings.TrimSpace(chain[0].TweetID)
	if rootID == "" {
		return unique, 0
	}
	order := len(chain) - 1
	for i, item := range chain {
		if item.TweetID == candidate.TweetID {
			order = i
			break
		}
	}
	sourceLang := strings.ToLower(strings.TrimSpace(candidate.SourceLang))
	return "thread\x00" + targetLang + "\x00" + sourceLang + "\x00" + rootID, order
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
