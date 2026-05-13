package web

import (
	"encoding/gob"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/i18n"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/settings"
	"github.com/screwys/igloo/internal/worker"
)

const (
	shortsPageSize         = 10_000
	shortsInitialCardLimit = 96
	shortsHydrateBatchSize = 96
	bookmarksPageSize      = 200
)

func init() {
	// gorilla/sessions uses encoding/gob — register types stored in sessions.
	gob.Register([]string{})
}

type Server struct {
	db            *db.DB
	cfg           *config.Config
	store         sessions.Store
	workers       *worker.Manager
	requestAvatar func(string)
	staticV       func(string) string
	i18n          *i18n.Catalog

	// Channel preview cache — populated in background on first page load
	channelPreviewMu   sync.Mutex
	channelPreviewVids map[string][]model.Video
	channelPreviewFeed map[string][]model.FeedItem
	channelPreviewAt   time.Time

	// Profile card endpoint
	profileFetch  fetchProfileFn
	profileFlight *profileFlight

	downloaderReportMu     sync.Mutex
	downloaderReportLatest *downloaderReport

	androidSyncGenerationMu         sync.Mutex
	androidSyncGenerationRefreshMu  sync.Mutex
	androidSyncGenerationRefreshing bool
	androidSyncAssetServeSemOnce    sync.Once
	androidSyncAssetServeSemaphore  chan struct{}
}

func NewServer(database *db.DB, cfg *config.Config, workers *worker.Manager, staticV func(string) string) http.Handler {
	catalog, err := i18n.LoadDir(cfg.LocaleDir)
	if err != nil {
		catalog = i18n.NewCatalog()
	}
	s := &Server{
		db:            database,
		cfg:           cfg,
		store:         sessions.NewCookieStore([]byte(cfg.SecretKey)),
		workers:       workers,
		requestAvatar: workers.RequestAvatar,
		staticV:       staticV,
		i18n:          catalog,
		profileFlight: newProfileFlight(),
	}

	mux := http.NewServeMux()

	// Static files
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		http.FileServer(http.Dir(cfg.StaticDir))))

	// Page routes
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /channels", s.handlePageChannels)
	mux.HandleFunc("GET /feed", s.handlePageFeed)
	mux.HandleFunc("GET /thread/{tweetID}", s.handlePageThread)
	mux.HandleFunc("GET /liked", s.handlePageLiked)
	mux.HandleFunc("GET /shorts", s.handlePageShorts)
	mux.HandleFunc("GET /channels/{channelID}", s.handlePageChannel)
	mux.HandleFunc("GET /videos", s.handlePageVideos)
	mux.HandleFunc("GET /creators", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/channels", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /player/{videoID}", s.handlePagePlayer)
	mux.HandleFunc("GET /bookmarks", s.handlePageBookmarks)
	mux.HandleFunc("GET /temp/watch", s.handlePageTempWatch)
	mux.HandleFunc("GET /temp/x-demo", s.handlePageXDemo)
	mux.HandleFunc("GET /temp/downloader-report", s.handlePageDownloaderReport)
	mux.HandleFunc("GET /search", s.handlePageSearch)
	mux.HandleFunc("GET /search/youtube", s.handlePageYouTubeSearch)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("GET /setup", s.handleSetupPage)
	mux.HandleFunc("POST /setup", s.handleSetupSubmit)
	mux.HandleFunc("GET /logout", s.handleLogout)

	// Media routes (#15 G — consolidated under /api/media/)
	mux.HandleFunc("GET /api/media/avatar/{channelID}", s.handleChannelAvatar)
	mux.HandleFunc("GET /api/media/banner/{channelID}", s.handleChannelBanner)
	mux.HandleFunc("GET /api/media/thumbnail/{videoID}", s.handleThumbnail)
	mux.HandleFunc("GET /api/download/video/{videoID}", s.handleDownloadVideo)

	// API routes
	s.registerAuthAPIRoutes(mux)
	s.registerFeedAPIRoutes(mux)
	s.registerFeedSourceAPIRoutes(mux)
	s.registerBookmarkAPIRoutes(mux)
	s.registerSyncAPIRoutes(mux)
	s.registerShortsAPIRoutes(mux)
	s.registerChannelAPIRoutes(mux)
	s.registerVideoAPIRoutes(mux)
	s.registerSearchAPIRoutes(mux)
	s.registerStatusAPIRoutes(mux)
	s.registerSubtitleRoutes(mux)
	s.registerAdminAPIRoutes(mux)
	s.registerTranslateAPIRoutes(mux)
	s.registerI18NAPIRoutes(mux)
	s.registerLogsAPIRoutes(mux)
	s.registerXAPIRoutes(mux)
	s.registerPreviewAPIRoutes(mux)
	s.registerDownloadAPIRoutes(mux)
	s.registerDownloaderReportRoutes(mux)
	s.registerTweetMediaAPIRoutes(mux)
	s.registerAndroidSyncAPIRoutes(mux)
	s.registerProfileAPIRoutes(mux)
	s.registerHealthAPIRoutes(mux)
	s.registerClientLogsAPIRoutes(mux)
	s.registerDeltaAPIRoutes(mux)
	s.registerMutationAPIRoutes(mux)
	s.registerThreadAPIRoutes(mux)

	return chain(mux,
		requestLogger,
		recoverPanic,
		s.enforceAuth,
		s.csrfProtect,
	)
}

// chain wraps an http.Handler with middleware (applied in order, outermost first).
func chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

func activeNavForPath(path string) string {
	path = "/" + strings.Trim(strings.TrimSpace(path), "/")
	switch {
	case path == "/channels" || strings.HasPrefix(path, "/channels/"):
		return "channels"
	case path == "/videos" || strings.HasPrefix(path, "/videos/"):
		return "videos"
	case path == "/player" || strings.HasPrefix(path, "/player/") || path == "/watch":
		return "videos"
	case path == "/youtube/search":
		return "videos"
	case path == "/feed":
		return "feed"
	case path == "/liked":
		return "liked"
	case path == "/shorts" || strings.HasPrefix(path, "/shorts/"):
		return "shorts"
	case path == "/bookmarks" || strings.HasPrefix(path, "/bookmarks/"):
		return "bookmarks"
	default:
		return ""
	}
}

// pageProps builds the common PageProps for templ-rendered pages.
func (s *Server) pageProps(w http.ResponseWriter, r *http.Request) components.PageProps {
	sess, _ := s.store.Get(r, "session")
	translateTarget := s.setting("translate_target_lang", "en")
	translateSkip := s.setting("translate_skip_langs", "")
	translateBackend := s.setting("translate_backend", settings.TranslateBackendNone)
	translateAutoMode := s.setting("translate_auto_mode", settings.TranslateAutoLazy)
	translateLookaheadRaw := s.setting("translate_auto_lookahead", "20")
	translateLookahead, _ := strconv.Atoi(translateLookaheadRaw)
	if translateLookahead == 0 {
		translateLookahead = settings.IntDefault("translate_auto_lookahead")
	}
	dearrowMode := s.setting("dearrow_mode", "off")
	lang := s.requestLanguage(r)
	if w != nil {
		w.Header().Set("Content-Language", lang)
	}
	activeNav := ""
	if r != nil && r.URL != nil {
		activeNav = activeNavForPath(r.URL.Path)
	}
	langs := s.supportedLanguageChoices(lang)
	return components.PageProps{
		CSRFToken:               s.ensureCSRF(sess, w, r),
		UserRole:                sessionStr(sess, "user_role", "user"),
		Username:                sessionStr(sess, "auth_user", ""),
		UserPlatforms:           s.effectivePlatforms(sessionStrSlice(sess, "user_platforms")),
		ActiveNav:               activeNav,
		ShortcutConfig:          defaultShortcutConfig(),
		TranslateTargetLang:     translateTarget,
		TranslateSkipLangs:      translateSkip,
		TranslateBackend:        settings.NormalizeTranslateBackend(translateBackend),
		TranslateAutoMode:       settings.NormalizeTranslateAutoMode(translateAutoMode),
		TranslateLookahead:      strconv.Itoa(settings.ClampTranslateLookahead(translateLookahead)),
		Language:                lang,
		Text:                    s.catalog().Messages(lang),
		SupportedLanguages:      langs,
		ShareEmbedFriendlyLinks: s.boolSetting("share_embed_friendly_links"),
		StaticV:                 s.staticV,
		Prefs: components.PrefsData{Settings: map[string]any{
			"dearrow_mode": dearrowMode,
		}},
	}
}

func (s *Server) setting(key, fallback string) string {
	if s == nil || s.db == nil {
		return fallback
	}
	v, err := s.db.GetSetting(key, fallback)
	if err != nil {
		return fallback
	}
	return v
}

func (s *Server) boolSetting(key string) bool {
	if s == nil || s.db == nil {
		if def, ok := settings.Defaults[key].(bool); ok {
			return def
		}
		return false
	}
	return s.db.BoolSetting(key)
}

func (s *Server) catalog() *i18n.Catalog {
	if s == nil || s.i18n == nil {
		return i18n.NewCatalog()
	}
	return s.i18n
}

func (s *Server) requestLanguage(r *http.Request) string {
	if r != nil {
		if lang := r.URL.Query().Get("lang"); lang != "" {
			if lang == "auto" {
				return s.catalog().MatchAcceptLanguage(r.Header.Get("Accept-Language"))
			}
			return s.catalog().ResolveLanguage(lang)
		}
	}
	setting := s.setting("ui_language", settings.Defaults["ui_language"].(string))
	if setting == "auto" && r != nil {
		return s.catalog().MatchAcceptLanguage(r.Header.Get("Accept-Language"))
	}
	return s.catalog().ResolveLanguage(setting)
}

func (s *Server) supportedLanguageChoices(lang string) []components.LanguageChoice {
	langs := s.catalog().Languages()
	out := make([]components.LanguageChoice, 0, len(langs)+1)
	out = append(out, components.LanguageChoice{
		Code: "auto",
		Name: s.catalog().T(lang, components.N("language_auto", "Automatic (browser)"), "Automatic (browser)"),
	})
	for _, lang := range langs {
		out = append(out, components.LanguageChoice{Code: lang.Code, Name: lang.Name})
	}
	return out
}

// resolveDataPath makes a relative path absolute by joining it with dataDir.
// Already-absolute paths are returned unchanged.
func resolveDataPath(dataDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dataDir, path)
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	writeJSONEnvelope(w, status, body)
}

func sessionStr(sess *sessions.Session, key, fallback string) string {
	if v, ok := sess.Values[key].(string); ok {
		return v
	}
	return fallback
}

func sessionStrSlice(sess *sessions.Session, key string) []string {
	if v, ok := sess.Values[key].([]string); ok {
		return v
	}
	return nil
}

func defaultShortcutConfig() map[string]string {
	return map[string]string{
		"feed.like": "l", "feed.bookmark": "b", "feed.share": "s", "feed.translate": "t", "feed.media": "f",
		"shorts.autoplay": "a", "shorts.bookmark": "b", "shorts.share": "s", "shorts.grid": "c",
		"player.fullscreen": "f", "player.bookmark": "b", "player.share": "s", "player.autoplay": "a",
	}
}
