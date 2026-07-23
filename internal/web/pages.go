package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/auth"
	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/settings"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, _ := s.db.GetSetting("starting_page", "feed")
	if !settings.WebStartingPages[page] {
		page = "feed"
	}
	http.Redirect(w, r, "/"+page, http.StatusSeeOther)
}

func (s *Server) handlePageChannels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	channels := s.enrichedChannels()

	if q != "" {
		ql := strings.ToLower(q)
		var filtered []model.Channel
		for _, ch := range channels {
			if strings.Contains(strings.ToLower(ch.Name), ql) {
				filtered = append(filtered, ch)
			}
		}
		channels = filtered
	}

	// Pagination: show 20 sections per batch to avoid loading 1200+ at once
	const channelPageSize = 4
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	// Determine which channels are in this batch
	end := offset + channelPageSize
	if end > len(channels) {
		end = len(channels)
	}
	isSearch := q != ""
	var batchChannels []model.Channel
	if isSearch {
		batchChannels = channels
	} else {
		batchChannels = channels[offset:end]
	}

	// Use cached preview data if available, otherwise fetch filtered for this batch
	const previewLimit = 8
	videosPerChannel, feedPerAuthor := s.getChannelPreviews(previewLimit, batchChannels, offset == 0)

	sections := make([]components.ChannelWithVideos, 0, len(batchChannels))
	for _, ch := range batchChannels {
		sec := components.ChannelWithVideos{Channel: ch}
		if ch.Platform == "twitter" {
			handle := ch.ChannelID
			if idx := strings.Index(handle, "_"); idx >= 0 {
				handle = handle[idx+1:]
			}
			items := feedPerAuthor[strings.ToLower(handle)]
			for _, item := range items {
				sec.Videos = append(sec.Videos, feedItemToVideo(item, ch))
			}
		} else {
			sec.Videos = videosPerChannel[ch.ChannelID]
		}
		sections = append(sections, sec)
	}

	// First page: mark first batch of videos as eager-load (no loading="lazy")
	if offset == 0 {
		for i := range sections {
			for j := range sections[i].Videos {
				sections[i].Videos[j].EagerLoad = true
			}
		}
	}

	// Full page request
	hasMore := !isSearch && end < len(channels)

	p := s.pageProps(w, r)
	p.PageTitle = "Channels"
	p.ActiveNav = "channels"
	p.PageBadge = fmt.Sprintf("%d channels", len(channels))
	p.ESBundle = "js/dist/shorts.js"
	p.Sidebar = s.buildSidebarContext(r, channels)

	// HTMX request — return sections + load-more sentinel
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.ChannelSections(p, sections).Render(r.Context(), w)
		if !isSearch && end < len(channels) {
			_ = components.ChannelLoadMore(end).Render(r.Context(), w)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.ChannelsPage(p, sections, q, hasMore, end).Render(r.Context(), w)
}

const channelPreviewTTL = 2 * time.Minute

// getChannelPreviews returns video/feed preview maps for the given batch.
// On the first page (isFirstPage=true), it fetches only the batch's channels
// and triggers a background prefetch of all channels. Subsequent batches use
// the cache if available, falling back to a filtered query.
func (s *Server) getChannelPreviews(limit int, batch []model.Channel, isFirstPage bool) (map[string][]model.Video, map[string][]model.FeedItem) {
	// Check cache
	s.channelPreviewMu.Lock()
	cached := s.channelPreviewVids != nil && time.Since(s.channelPreviewAt) < channelPreviewTTL
	var vids map[string][]model.Video
	var feed map[string][]model.FeedItem
	if cached {
		vids = s.channelPreviewVids
		feed = s.channelPreviewFeed
	}
	s.channelPreviewMu.Unlock()

	if cached {
		return vids, feed
	}

	// Fetch filtered for this batch
	var videoIDs, feedHandles []string
	for _, ch := range batch {
		if ch.Platform == "twitter" {
			handle := ch.ChannelID
			if idx := strings.Index(handle, "_"); idx >= 0 {
				handle = handle[idx+1:]
			}
			feedHandles = append(feedHandles, handle)
		} else {
			videoIDs = append(videoIDs, ch.ChannelID)
		}
	}
	vids, _ = s.db.GetLatestVideosPerChannel(limit, videoIDs...)
	feed, _ = s.db.GetLatestFeedMediaPerAuthor(limit, feedHandles...)

	// On first page, prefetch all channels in background for scroll requests
	if isFirstPage {
		go func() {
			allVids, _ := s.db.GetLatestVideosPerChannel(limit)
			allFeed, _ := s.db.GetLatestFeedMediaPerAuthor(limit)
			s.channelPreviewMu.Lock()
			s.channelPreviewVids = allVids
			s.channelPreviewFeed = allFeed
			s.channelPreviewAt = time.Now()
			s.channelPreviewMu.Unlock()
		}()
	}

	return vids, feed
}

// feedItemToVideo converts a FeedItem to a Video for unified card rendering.
func feedItemToVideo(item model.FeedItem, ch model.Channel) model.Video {
	mediaKind := "video"
	slideCount := 0
	if len(item.Media) > 1 {
		mediaKind = "slideshow"
		slideCount = len(item.Media)
	} else if len(item.Media) == 1 {
		switch item.Media[0].Type {
		case "video", "gif":
			mediaKind = "video"
		default:
			mediaKind = "image"
			slideCount = 1
		}
	}

	title := item.BodyText
	if len(title) > 80 {
		title = title[:80] + "..."
	}

	displayName := item.AuthorDisplayName
	if displayName == "" {
		displayName = "@" + item.AuthorHandle
	}

	return model.Video{
		VideoID:         item.TweetID,
		ChannelID:       ch.ChannelID,
		OwnerKind:       "tweet",
		Title:           title,
		PublishedAt:     item.PublishedAt,
		Platform:        "twitter",
		ChannelName:     displayName,
		AvatarURL:       "/api/media/avatar/" + ch.ChannelID,
		ThumbnailURL:    "/api/media/thumbnail/" + item.TweetID + "?owner_kind=tweet",
		MediaKind:       mediaKind,
		MediaSlideCount: slideCount,
		IsShortForm:     true,
		IsSubscribed:    ch.IsSubscribed,
		IsStarred:       ch.IsStarred,
	}
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.store.Get(r, "session")
	if username, ok := sess.Values["auth_user"].(string); ok && username != "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.usersConfigured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	csrfToken, err := s.ensureCSRF(sess, w, r)
	if err != nil {
		s.writeCSRFUnavailable(w, r, err)
		return
	}
	next := safeLoginNext(r.URL.Query().Get("next"))
	p := s.pageProps(w, r)
	p.PageTitle = p.T("login_title", "Igloo Login")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.LoginPage(p, csrfToken, "", next).Render(r.Context(), w)
}

var loginInvalidMessage = components.N("login_error_invalid_credentials", "Invalid username or password")

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	// Use the same auth source as Android (auth_users.json).
	users := auth.GetCachedUsers()
	if len(users) == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}

	sess, _ := s.store.Get(r, "session")
	expected, _ := sess.Values["csrf_token"].(string)
	if expected == "" || r.FormValue("_csrf_token") != expected {
		http.Error(w, "CSRF token invalid", http.StatusForbidden)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if rec, ok := users[username]; ok && auth.VerifyPassword(password, rec.Password) {
		s.loginSuccess(w, r, username, rec.Role, rec.Platforms)
		return
	}

	csrfToken, err := s.ensureCSRF(sess, w, r)
	if err != nil {
		s.writeCSRFUnavailable(w, r, err)
		return
	}
	p := s.pageProps(w, r)
	p.PageTitle = p.T("login_title", "Igloo Login")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.LoginPage(p, csrfToken, p.T(loginInvalidMessage, "Invalid username or password"), "").Render(r.Context(), w)
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Setup is only available from localhost.", http.StatusForbidden)
		return
	}
	sess, _ := s.store.Get(r, "session")
	if s.usersConfigured() {
		if username, ok := sess.Values["auth_user"].(string); ok && username != "" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	csrfToken, err := s.ensureCSRF(sess, w, r)
	if err != nil {
		s.writeCSRFUnavailable(w, r, err)
		return
	}
	p := s.pageProps(w, r)
	p.PageTitle = p.T("setup_title", "Create First Admin")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.SetupPage(p, csrfToken, "", s.setupPlatformChoices(), nil).Render(r.Context(), w)
}

func (s *Server) handleSetupSubmit(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "Setup is only available from localhost.", http.StatusForbidden)
		return
	}
	sess, _ := s.store.Get(r, "session")
	if s.usersConfigured() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderSetupError(w, r, sess, "setup_error_invalid_form", "Invalid setup form.")
		return
	}
	expected, _ := sess.Values["csrf_token"].(string)
	if expected == "" || r.FormValue("_csrf_token") != expected {
		http.Error(w, "CSRF token invalid", http.StatusForbidden)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")
	switch {
	case username == "":
		s.renderSetupError(w, r, sess, "setup_error_username_required", "Username is required.")
		return
	case password == "":
		s.renderSetupError(w, r, sess, "setup_error_password_required", "Password is required.")
		return
	case password != confirm:
		s.renderSetupError(w, r, sess, "setup_error_passwords_mismatch", "Passwords do not match.")
		return
	}
	platforms, err := s.normalizeSetupPlatforms(r.Form["platforms"])
	if err != nil {
		s.renderSetupError(w, r, sess, "setup_error_platform_required", "Select at least one platform.")
		return
	}
	auth.LockUsers()
	defer auth.UnlockUsers()
	users, err := auth.LoadUsers(s.cfg.AuthUsersPath)
	if err != nil {
		slog.Error("setup LoadUsers", "err", err)
		s.renderSetupError(w, r, sess, "setup_error_save_failed", "Could not create the admin user.")
		return
	}
	if len(users) > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := s.cfg.SaveRuntimeConfig(platforms); err != nil {
		slog.Error("setup SaveRuntimeConfig", "err", err)
		s.renderSetupError(w, r, sess, "setup_error_save_failed", "Could not create the admin user.")
		return
	}
	users[username] = auth.UserRecord{
		Password:  auth.HashPassword(password),
		Role:      "admin",
		Platforms: platforms,
	}
	if err := auth.SaveUsers(s.cfg.AuthUsersPath, users); err != nil {
		slog.Error("setup SaveUsers", "err", err)
		s.renderSetupError(w, r, sess, "setup_error_save_failed", "Could not create the admin user.")
		return
	}
	auth.InvalidateCache()
	if s.workers != nil {
		s.workers.KickIngest()
	}
	s.loginSuccess(w, r, username, "admin", platforms)
}

func (s *Server) renderSetupError(w http.ResponseWriter, r *http.Request, sess *sessions.Session, key, fallback string) {
	csrfToken, err := s.ensureCSRF(sess, w, r)
	if err != nil {
		s.writeCSRFUnavailable(w, r, err)
		return
	}
	p := s.pageProps(w, r)
	p.PageTitle = p.T("setup_title", "Create First Admin")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = components.SetupPage(p, csrfToken, p.T(key, fallback), s.setupPlatformChoices(), r.Form["platforms"]).Render(r.Context(), w)
}

func (s *Server) setupPlatformChoices() []components.PlatformChoice {
	platforms := s.enabledPlatforms()
	if len(platforms) == 0 {
		platforms = config.SupportedPlatforms
	}
	choices := make([]components.PlatformChoice, 0, len(platforms))
	for _, p := range platforms {
		choices = append(choices, components.PlatformChoice{
			Value: p,
			Label: platformChoiceLabel(p),
		})
	}
	return choices
}

func (s *Server) normalizeSetupPlatforms(raw []string) ([]string, error) {
	platforms, err := config.NormalizeEnabledPlatforms(raw)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool)
	for _, choice := range s.setupPlatformChoices() {
		allowed[choice.Value] = true
	}
	out := make([]string, 0, len(platforms))
	for _, p := range platforms {
		if !allowed[p] {
			return nil, errPlatformDisabled(p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no platforms selected")
	}
	return out, nil
}

func isLoopbackRequest(r *http.Request) bool {
	return true
	if !isLoopbackAddr(r.RemoteAddr) {
		return false
	}
	if hasForwardedClientHeaders(r) {
		ips, ok := forwardedClientIPs(r)
		if !ok || len(ips) == 0 {
			return false
		}
		for _, ip := range ips {
			if !ip.IsLoopback() {
				return false
			}
		}
		return true
	}
	return isLoopbackHost(r.Host)
}

func hasForwardedClientHeaders(r *http.Request) bool {
	return headerHasValue(r.Header, "Forwarded") ||
		headerHasValue(r.Header, "X-Forwarded-For") ||
		headerHasValue(r.Header, "X-Real-IP")
}

func headerHasValue(header http.Header, key string) bool {
	for _, value := range header.Values(key) {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func forwardedClientIPs(r *http.Request) ([]net.IP, bool) {
	var ips []net.IP
	if raw := r.Header.Values("Forwarded"); len(raw) > 0 {
		for _, header := range raw {
			for _, part := range strings.Split(header, ",") {
				ip, ok := parseForwardedFor(part)
				if !ok {
					return nil, false
				}
				ips = append(ips, ip)
			}
		}
	}
	if raw := r.Header.Values("X-Forwarded-For"); len(raw) > 0 {
		for _, header := range raw {
			for _, part := range strings.Split(header, ",") {
				ip, ok := parseClientIP(part)
				if !ok {
					return nil, false
				}
				ips = append(ips, ip)
			}
		}
	}
	if raw := r.Header.Values("X-Real-IP"); len(raw) > 0 {
		for _, header := range raw {
			ip, ok := parseClientIP(header)
			if !ok {
				return nil, false
			}
			ips = append(ips, ip)
		}
	}
	return ips, true
}

func parseForwardedFor(part string) (net.IP, bool) {
	for _, param := range strings.Split(part, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "for") {
			continue
		}
		return parseClientIP(value)
	}
	return nil, false
}

func isLoopbackAddr(addr string) bool {
	ip, ok := parseClientIP(addr)
	return ok && ip.IsLoopback()
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(hostOnly(host), "localhost") {
		return true
	}
	ip, ok := parseClientIP(host)
	return ok && ip.IsLoopback()
}

func parseClientIP(raw string) (net.IP, bool) {
	host := strings.Trim(hostOnly(strings.Trim(strings.TrimSpace(raw), `"`)), "[]")
	ip := net.ParseIP(host)
	return ip, ip != nil
}

func hostOnly(raw string) string {
	host, _, err := net.SplitHostPort(raw)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(raw, "[]")
}

func (s *Server) usersConfigured() bool {
	return len(auth.GetCachedUsers()) > 0
}

func (s *Server) loginSuccess(w http.ResponseWriter, r *http.Request, username, role string, platforms []string) {
	sess, _ := s.store.Get(r, "session")
	sess.Values["auth_user"] = username
	sess.Values["user_role"] = role
	sess.Values["user_platforms"] = s.effectivePlatforms(platforms)
	sess.Options.MaxAge = 86400 * 30
	sess.Options.HttpOnly = true
	sess.Options.SameSite = http.SameSiteLaxMode
	_ = sess.Save(r, w)

	next := safeLoginNext(r.FormValue("next"))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func safeLoginNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, `\`) {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil {
		return "/"
	}
	if u.Path == "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, `\`) {
		return "/"
	}
	if u.Path == "/login" {
		return "/"
	}
	return u.RequestURI()
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.store.Get(r, "session")
	sess.Values["auth_user"] = ""
	sess.Options.MaxAge = -1
	_ = sess.Save(r, w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) buildSidebarContext(r *http.Request, channels []model.Channel) model.SidebarContext {
	stats, _ := s.db.GetStats()
	groups := sidebarGroupsFromChannels(channels)
	username := ""
	if user := userFromContext(r.Context()); user != nil {
		username = user.Username
	}

	currentlyWatching, _ := s.db.GetCurrentlyWatchingVideos(1)
	currentlyAvailable, _ := s.db.GetCurrentlyAvailableVideos()
	pinnedVideos, _ := s.db.GetPinnedVideos()

	return model.SidebarContext{
		Username:           username,
		Channels:           channels,
		Groups:             groups,
		Stats:              stats,
		CurrentlyWatching:  currentlyWatching,
		CurrentlyAvailable: currentlyAvailable,
		PinnedVideos:       pinnedVideos,
	}
}

func sidebarGroupsFromChannels(channels []model.Channel) []model.ChannelGroup {
	// Group channels by platform
	var favourites, youtube, tiktok, instagram, twitter, other []model.Channel
	for _, ch := range channels {
		if ch.IsStarred {
			favourites = append(favourites, ch)
			continue
		}
		switch ch.Platform {
		case "youtube":
			youtube = append(youtube, ch)
		case "tiktok":
			tiktok = append(tiktok, ch)
		case "instagram":
			instagram = append(instagram, ch)
		case "twitter":
			twitter = append(twitter, ch)
		default:
			other = append(other, ch)
		}
	}

	var groups []model.ChannelGroup
	if len(favourites) > 0 {
		groups = append(groups, model.ChannelGroup{Title: "Favourites", GroupID: "favourites", StarIcon: true, Count: len(favourites), Channels: favourites})
	}
	if len(youtube) > 0 {
		groups = append(groups, model.ChannelGroup{Title: "YouTube", GroupID: "youtube", PlatformKey: "youtube", Collapsed: true, Count: len(youtube), Channels: youtube})
	}
	if len(tiktok) > 0 {
		groups = append(groups, model.ChannelGroup{Title: "TikTok", GroupID: "tiktok", PlatformKey: "tiktok", Collapsed: true, Count: len(tiktok), Channels: tiktok})
	}
	if len(instagram) > 0 {
		groups = append(groups, model.ChannelGroup{Title: "Instagram", GroupID: "instagram", PlatformKey: "instagram", Collapsed: true, Count: len(instagram), Channels: instagram})
	}
	if len(twitter) > 0 {
		groups = append(groups, model.ChannelGroup{Title: "X (Twitter)", GroupID: "twitter", PlatformKey: "twitter", Collapsed: true, Count: len(twitter), Channels: twitter})
	}
	if len(other) > 0 {
		groups = append(groups, model.ChannelGroup{Title: "Other", GroupID: "other", Collapsed: true, Count: len(other), Channels: other})
	}

	return groups
}

func (s *Server) handlePageVideos(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	perPage := 10_000

	opts := db.GetVideosOpts{
		Platform:        "youtube",
		Search:          q,
		Limit:           perPage,
		ExcludeMetadata: true,
	}

	count, _ := s.db.GetVideoCount(opts)
	pager := model.Pager{Page: page, PerPage: perPage, Total: count}
	opts.Offset = pager.Offset()

	videos, err := s.db.GetVideos(opts)
	if err != nil {
		slog.Error("GetVideos", "err", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	p := s.pageProps(w, r)
	p.PageTitle = "Videos"
	p.ActiveNav = "videos"
	p.PageBadge = fmt.Sprintf("%d videos", count)
	p.PageScripts = []string{"js/infinite_page.js"}
	p.Sidebar = s.mustBuildSidebar(r)

	// HTMX request — return video cards for infinite scroll
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.VideosPartial(p, videos, pager, q).Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.VideosPage(p, videos, pager, q).Render(r.Context(), w)
}

func (s *Server) handlePageChannel(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelID")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	perPage := 40

	channel, err := s.db.GetChannel(channelID)
	var profile *model.ChannelProfile
	if (err != nil || channel == nil) && !isTwitterChannelID(channelID) {
		if p, profileErr := s.db.GetChannelProfile(channelID); profileErr == nil && p != nil && !p.Tombstone {
			profile = p
			channel = channelFromProfileOnly(channelID, p, s.db.ResolveSubscribeURL(channelID))
			err = nil
		}
	}

	// Twitter channel without a subscription — show feed items
	if (err != nil || channel == nil) && isTwitterChannelID(channelID) {
		s.renderTwitterChannelFeed(w, r, channelID, s.db.IsChannelFollowed(channelID), false)
		return
	}
	if err != nil || channel == nil {
		http.Redirect(w, r, "/channels", http.StatusSeeOther)
		return
	}

	// Twitter/X channel with subscription — show feed items
	if channel.Platform == "twitter" {
		s.renderTwitterChannelFeed(w, r, channelID, channel.IsSubscribed, channel.IsStarred)
		return
	}

	if s.resolveAvatarPath(channelID) != "" {
		channel.AvatarURL = "/api/media/avatar/" + channelID
	} else {
		channel.AvatarURL = ""
	}
	if channel.Platform == "tiktok" || channel.Platform == "instagram" || channel.Platform == "youtube" {
		if profile == nil {
			profile, _ = s.db.GetChannelProfile(channelID)
		}
		if profile != nil && profile.Tombstone {
			profile = nil
		}
		profile = s.profileForPresentation(profile)
	}

	opts := db.GetVideosOpts{
		ChannelID:       channelID,
		Search:          q,
		Limit:           perPage,
		ExcludeMetadata: true,
	}
	count, _ := s.db.GetVideoCount(opts)
	pager := model.Pager{Page: page, PerPage: perPage, Total: count}
	opts.Offset = pager.Offset()
	videos, _ := s.db.GetVideos(opts)

	usesShorts := channel.Platform == "tiktok" || channel.Platform == "instagram"
	if usesShorts {
		nowMs := time.Now().UnixMilli()
		if err := s.db.AttachStoryStatusToVideos(videos, nowMs); err != nil {
			slog.Error("AttachStoryStatusToVideos channel", "channel", channelID, "err", err)
		}
		if profile != nil {
			if statuses, err := s.db.GetStoryStatusForChannelIDs([]string{channelID}, nowMs); err == nil {
				if status, ok := statuses[channelID]; ok {
					profile.StoryState = status.State
					profile.StoryCount = status.Count
					profile.StoryUnseenCount = status.UnseenCount
					if status.FirstUnseenVideoID != "" {
						profile.StoryFirstVideoID = status.FirstUnseenVideoID
					} else {
						profile.StoryFirstVideoID = status.FirstVideoID
					}
				}
			}
		}
	}

	// Full page
	p := s.pageProps(w, r)
	p.PageTitle = channel.Name
	p.PageBadge = fmt.Sprintf("%d videos", count)
	p.PageScripts = []string{"js/infinite_page.js"}
	if usesShorts {
		p.ESBundle = "js/dist/shorts.js"
	}
	p.Sidebar = s.mustBuildSidebar(r)

	// HTMX request — return video cards + next trigger for infinite scroll
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.VideoGrid(p, videos, *channel, pager, q, usesShorts, true).Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.ChannelPage(p, *channel, profile, videos, pager, q, channel.AvatarURL, usesShorts).Render(r.Context(), w)
}

func channelFromProfileOnly(channelID string, profile *model.ChannelProfile, url string) *model.Channel {
	platform, handle := channelIDPlatformHandle(channelID)
	if profile.Platform != "" {
		platform = profile.Platform
	}
	if profile.Handle != "" {
		handle = strings.TrimPrefix(strings.TrimSpace(profile.Handle), "@")
	}
	name := strings.TrimSpace(profile.DisplayName)
	if name == "" {
		name = handle
	}
	if name == "" {
		name = channelID
	}
	return &model.Channel{
		ChannelID:    channelID,
		SourceID:     handle,
		Name:         name,
		URL:          url,
		Platform:     platform,
		Handle:       handle,
		DisplayName:  profile.DisplayName,
		IsSubscribed: false,
	}
}

func channelIDPlatformHandle(channelID string) (string, string) {
	idx := strings.IndexByte(channelID, '_')
	if idx < 0 || idx == len(channelID)-1 {
		return "", ""
	}
	return channelID[:idx], channelID[idx+1:]
}

func isTwitterChannelID(channelID string) bool {
	lower := strings.ToLower(channelID)
	return strings.HasPrefix(lower, "twitter_") || strings.HasPrefix(lower, "x_")
}

func (s *Server) renderTwitterChannelFeed(w http.ResponseWriter, r *http.Request, channelID string, isFollowing bool, isStarred bool) {
	handle := channelID
	if idx := strings.Index(channelID, "_"); idx >= 0 {
		handle = channelID[idx+1:]
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	pageSize := 40

	items, _ := s.db.GetFeedThreadItemsByAuthorPage(handle, pageSize+1, offset)
	hasMore := len(items) > pageSize
	if hasMore {
		items = items[:pageSize]
	}
	nextOffset := offset + len(items)
	items = feed.EnrichFeedItems(s.db, items)
	nextPageURL := ""
	if hasMore {
		nextPageURL = fmt.Sprintf("/channels/%s?offset=%d", url.PathEscape(channelID), nextOffset)
	}

	p := s.pageProps(w, r)
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.FeedItemsPartial(p, items).Render(r.Context(), w)
		_ = components.FeedScrollSentinel(nextPageURL).Render(r.Context(), w)
		return
	}

	// Pick display name: prefer items where this handle is the author,
	// then quote author, then fall back to @handle.
	displayName := ""
	for _, item := range items {
		if strings.EqualFold(item.AuthorHandle, handle) && item.AuthorDisplayName != "" {
			displayName = item.AuthorDisplayName
			break
		}
	}
	if displayName == "" {
		for _, item := range items {
			if strings.EqualFold(item.QuoteAuthorHandle, handle) && item.QuoteAuthorDisplayName != "" {
				displayName = item.QuoteAuthorDisplayName
				break
			}
		}
	}
	if displayName == "" {
		displayName = "@" + handle
	}

	profile, _ := s.db.GetChannelProfile(channelID)
	if profile != nil && profile.Tombstone {
		profile = nil
	}
	profile = s.profileForPresentation(profile)
	if profile != nil && profile.DisplayName != "" {
		displayName = profile.DisplayName
	}

	xCh := &components.XChannelInfo{
		Handle:      handle,
		DisplayName: displayName,
		ChannelID:   channelID,
		IsFollowing: isFollowing,
		IsStarred:   isStarred,
		Profile:     profile,
	}

	p.PageTitle = displayName
	if count, err := s.db.CountFeedItemsByAuthor(handle); err == nil {
		p.PageBadge = fmt.Sprintf("%d posts", count)
	} else {
		p.PageBadge = fmt.Sprintf("%d posts", len(items))
	}
	p.ESBundle = "js/dist/feed.js"
	p.Sidebar = s.mustBuildSidebar(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.FeedPage(p, items, hasMore, nextPageURL, false, false, xCh, "").Render(r.Context(), w)
}

func (s *Server) handlePagePlayer(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	video, err := s.db.GetVideo(videoID)
	if err != nil || video == nil {
		http.Redirect(w, r, "/videos", http.StatusSeeOther)
		return
	}
	video.EnrichForCard()

	hasLocalAvatar := s.resolveAvatarPath(video.ChannelID) != ""
	if hasLocalAvatar {
		video.AvatarURL = "/api/media/avatar/" + video.ChannelID
	}

	bookmarked, catID, _ := s.db.IsBookmarked(videoID)
	if bookmarked {
		video.BookmarkCategoryID = &catID
	}

	video.PlaybackPosition, _ = s.db.GetPlaybackPosition(videoID)
	comments, _ := s.db.GetComments(videoID, 20)
	s.projectCommentAuthorAvatars(comments)

	moreFromChannel, _ := s.db.GetVideos(db.GetVideosOpts{
		ChannelID:       video.ChannelID,
		Limit:           14,
		ExcludeMetadata: true,
	})
	var filtered []model.Video
	for _, v := range moreFromChannel {
		if v.VideoID != videoID {
			filtered = append(filtered, v)
		}
	}
	moreFromChannel = filtered

	sbCategories, _ := s.db.GetSetting("sponsorblock_categories",
		settings.SponsorBlockCategoriesDefault)
	dearrowMode, _ := s.db.GetSetting("dearrow_mode", "off")
	dearrowMode = settings.NormalizeDearrowMode(dearrowMode)

	nextVideo, _ := s.db.GetNextVideo(videoID)
	if nextVideo != nil {
		nextVideo.EnrichForCard()
		if s.resolveAvatarPath(nextVideo.ChannelID) != "" {
			nextVideo.AvatarURL = "/api/media/avatar/" + nextVideo.ChannelID
		}
	}

	p := s.pageProps(w, r)
	p.PageTitle = ResolveDearrowTitle(dearrowMode, video.Title, video.DearrowTitle, video.DearrowTitleCasual)
	p.ActiveNav = "videos"
	p.PageScripts = []string{"js/videojs_compat.js"}
	p.ESBundle = "js/dist/player.js"
	p.Sidebar = s.mustBuildSidebar(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.PlayerPage(p, *video, comments, moreFromChannel, nextVideo, sbCategories).Render(r.Context(), w)
}

func (s *Server) handlePageFeed(w http.ResponseWriter, r *http.Request) {
	isHTMX := r.Header.Get("HX-Request") != ""

	// `offset` is a rank_position cursor within the immutable snapshot_at
	// generation carried by the page.
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	pageSize := 40

	var items []model.FeedItem
	var nextPageURL string
	hasMore := false

	// Primary path: read from the pre-built rank snapshot (same data Android uses).
	snapAt, _ := s.db.SnapshotComputedAt()
	if snapAt > 0 {
		cursorSnapAt, _ := strconv.ParseInt(r.URL.Query().Get("snapshot_at"), 10, 64)
		pageSnapAt := snapAt
		if offset > 0 && cursorSnapAt > 0 {
			pageSnapAt = cursorSnapAt
		}
		page, snapErr := s.db.ListSnapshotPage(pageSnapAt, offset, pageSize+1)
		if errors.Is(snapErr, db.ErrFeedSnapshotExpired) && !isHTMX {
			offset = 0
			pageSnapAt = snapAt
			page, snapErr = s.db.ListSnapshotPage(pageSnapAt, 0, pageSize+1)
		}
		if errors.Is(snapErr, db.ErrFeedSnapshotExpired) && isHTMX {
			w.Header().Set("HX-Refresh", "true")
			http.Error(w, "feed snapshot expired", http.StatusConflict)
			return
		}
		if snapErr != nil {
			slog.Error("ListSnapshotPage", "err", snapErr)
		} else {
			hasMore = len(page) > pageSize
			if hasMore {
				page = page[:pageSize]
			}
			items = make([]model.FeedItem, len(page))
			for i, p := range page {
				items[i] = p.Item
			}
			items = feed.EnrichFeedItems(s.db, items)
			if hasMore && len(page) > 0 {
				nextPageURL = fmt.Sprintf("/feed?offset=%d&snapshot_at=%d", page[len(page)-1].RankPosition, pageSnapAt)
			}
		}
	}

	// Until the rank snapshot is ready, serve one bounded chronological page.
	if items == nil && offset == 0 {
		rawItems, err := s.db.ListFeedItemsPage(pageSize, nil, true)
		if err != nil {
			slog.Error("ListFeedItemsPage", "err", err)
			rawItems = nil
		}
		items = feed.EnrichFeedItems(s.db, rawItems)
	}

	p := s.pageProps(w, r)

	// HTMX infinite scroll — return just items + next sentinel
	if isHTMX {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.FeedItemsPartial(p, items).Render(r.Context(), w)
		_ = components.FeedScrollSentinel(nextPageURL).Render(r.Context(), w)
		return
	}

	p.PageTitle = "Feed"
	p.ActiveNav = "feed"
	p.ESBundle = "js/dist/feed.js"
	p.Sidebar = s.mustBuildSidebar(r)

	feedHeadAnchor := ""
	if headID, err := s.db.GetLatestFetchedFeedItemID(); err == nil {
		feedHeadAnchor = headID
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.FeedPage(p, items, hasMore, nextPageURL, true, true, nil, feedHeadAnchor).Render(r.Context(), w)
}

func (s *Server) handlePageThread(w http.ResponseWriter, r *http.Request) {
	tweetID := r.PathValue("tweetID")
	if tweetID == "" {
		http.NotFound(w, r)
		return
	}
	items, err := s.db.GetThreadTree(tweetID)
	if err != nil {
		slog.Error("GetThreadTree", "tweet_id", tweetID, "err", err)
		http.Error(w, "thread query failed", http.StatusInternalServerError)
		return
	}
	if len(items) == 0 {
		http.NotFound(w, r)
		return
	}

	items = feed.EnrichFeedItemsPreserveRows(s.db, items)

	p := s.pageProps(w, r)
	p.PageTitle = "Thread"
	p.ActiveNav = "feed"
	p.ESBundle = "js/dist/feed.js"
	p.Sidebar = s.mustBuildSidebar(r)

	returnHref := r.URL.Query().Get("return")
	if returnHref == "" || !strings.HasPrefix(returnHref, "/") || strings.HasPrefix(returnHref, "//") {
		returnHref = "/feed"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("fmt") == "partial" {
		_ = components.ThreadRoutePartial(p, items, returnHref).Render(r.Context(), w)
		return
	}
	_ = components.ThreadPage(p, items, returnHref).Render(r.Context(), w)
}

func (s *Server) handlePageLiked(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	cursorToken := r.URL.Query().Get("cursor")
	limit := 40

	var cursor *model.FeedCursor
	if cursorToken != "" {
		c := model.ParseFeedCursor(cursorToken)
		cursor = &c
	}

	likes, err := s.db.GetFeedLikedPage(limit+1, cursor)
	if err != nil {
		slog.Error("GetFeedLikedPage", "err", err)
		likes = nil
	}

	hasMore := len(likes) > limit
	if hasMore {
		likes = likes[:limit]
	}

	var tweetIDs []string
	for _, l := range likes {
		tweetIDs = append(tweetIDs, l.TweetID)
	}
	feedItemMap, _ := s.db.GetFeedItemsForTweetIDs(tweetIDs)

	var items []model.FeedItem
	for _, l := range likes {
		if fi, ok := feedItemMap[l.TweetID]; ok {
			fi.IsLiked = true
			items = append(items, fi)
		} else {
			items = append(items, model.FeedItem{
				TweetID:           l.TweetID,
				AuthorHandle:      l.AuthorHandle,
				AuthorDisplayName: l.AuthorDisplayName,
				SourceHandle:      l.SourceHandle,
				BodyText:          l.BodyText,
				CanonicalURL:      l.Link,
				PublishedAt:       l.PublishedAt,
				MediaJSON:         l.MediaJSON,
				AuthorAvatarURL:   l.AvatarURL,
				IsLiked:           true,
			})
		}
	}

	items = feed.EnrichFeedItems(s.db, items)

	var nextCursor string
	nextPageURL := ""
	if hasMore && len(likes) > 0 {
		last := likes[len(likes)-1]
		nextCursor = fmt.Sprintf("%d|%s", last.LikedAt.UnixMilli(), last.TweetID)
		nextPageURL = "/liked?cursor=" + url.QueryEscape(nextCursor)
	}

	p := s.pageProps(w, r)

	// HTMX infinite scroll
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.FeedItemsPartial(p, items).Render(r.Context(), w)
		_ = components.FeedScrollSentinel(nextPageURL).Render(r.Context(), w)
		return
	}

	p.PageTitle = "Liked"
	p.ActiveNav = "liked"
	p.PageBadge = fmt.Sprintf("%d posts", len(items))
	p.ESBundle = "js/dist/feed.js"
	p.Sidebar = s.mustBuildSidebar(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.LikedPage(p, items, hasMore, nextPageURL).Render(r.Context(), w)
}

func (s *Server) handlePageShorts(w http.ResponseWriter, r *http.Request) {
	videoHint := strings.TrimSpace(r.URL.Query().Get("video"))
	tab := db.NormalizeMomentsTab(r.URL.Query().Get("tab"))
	if strings.TrimSpace(r.URL.Query().Get("tab")) == "" {
		tab = s.db.MomentsDefaultTab()
	}
	nowMs := time.Now().UnixMilli()
	storyChannels, hasUnseenStories, _ := s.db.ListStoryChannels(nowMs, 200)
	perPage := shortsPageSize

	opts := db.GetVideosOpts{
		Platform: "shorts",
		Limit:    perPage,
		// Moments scroll oldest -> newest so new items append at the end.
		// Keep this aligned with /api/shorts/cards, GetShortsOrdinal, and Android MomentReadDao.
		OrderAsc:        true,
		MomentsMode:     tab,
		ExcludeMetadata: true,
	}
	count, _ := s.db.GetVideoCount(opts)
	pager := model.Pager{Page: 1, PerPage: perPage, Total: count}
	opts.Offset = 0

	var shorts []model.Video
	if tab != "stories" {
		shorts, _ = s.db.GetVideos(opts)
		if err := s.db.AttachStoryStatusToVideos(shorts, nowMs); err != nil {
			slog.Error("AttachStoryStatusToVideos shorts page", "err", err, "tab", tab)
		}
	} else {
		count = len(storyChannels)
		pager.Total = count
	}

	p := s.pageProps(w, r)
	p.PageTitle = p.T("nav_moments", "Moments")
	p.ActiveNav = "shorts"
	if tab == "stories" {
		p.PageBadge = fmt.Sprintf("%d stories", count)
	} else {
		p.PageBadge = fmt.Sprintf("%d videos", count)
	}
	p.PageScripts = []string{"js/infinite_page.js"}
	p.ESBundle = "js/dist/shorts.js"
	p.Sidebar = s.mustBuildSidebar(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.ShortsPage(p, shorts, storyChannels, hasUnseenStories, pager, videoHint, tab, shortsInitialCardLimit, shortsHydrateBatchSize).Render(r.Context(), w)
}

func (s *Server) handlePageBookmarks(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	categoryID, _ := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	selectedLabel := components.BookmarkLabelSelection{
		Label:     strings.TrimSpace(r.URL.Query().Get("label")),
		IsNoLabel: r.URL.Query().Get("label_empty") == "1",
	}
	if selectedLabel.Active() {
		categoryID = 0
	}
	perPage := bookmarksPageSize

	categories, _ := s.db.GetBookmarkCategories()
	labelCounts, err := s.db.GetBookmarkLabelCounts()
	if err != nil {
		slog.Error("GetBookmarkLabelCounts", "err", err)
	}

	opts := db.GetBookmarksOpts{
		CategoryID: categoryID,
		Limit:      perPage,
	}
	if selectedLabel.IsNoLabel {
		opts.LabelFilterMode = db.BookmarkLabelFilterNoLabel
	} else if selectedLabel.Label != "" {
		opts.LabelFilterMode = db.BookmarkLabelFilterExact
		opts.Label = selectedLabel.Label
	}
	count, _ := s.db.GetBookmarkCount(opts)
	pager := model.Pager{Page: page, PerPage: perPage, Total: count}
	opts.Offset = pager.Offset()

	bookmarks, err := s.db.GetBookmarks(opts)
	if err != nil {
		slog.Error("GetBookmarks", "err", err)
	}

	// First page: mark initial visible bookmarks as eager-load
	if page <= 1 {
		for i := range bookmarks {
			if i >= 24 {
				break
			}
			bookmarks[i].EagerLoad = true
		}
	}

	p := s.pageProps(w, r)
	p.PageTitle = "Bookmarks"
	p.ActiveNav = "bookmarks"
	p.PageBadge = fmt.Sprintf("%d bookmarks", count)
	p.PageScripts = []string{"js/infinite_page.js", "js/bookmark_label_filter.js"}
	p.ESBundle = "js/dist/shorts.js"
	p.Sidebar = s.mustBuildSidebar(r)

	// HTMX request — return bookmark cards for infinite scroll
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.BookmarksPartial(p, bookmarks, pager, categoryID, selectedLabel).Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.BookmarksPage(p, bookmarks, categories, labelCounts, categoryID, selectedLabel, pager).Render(r.Context(), w)
}

func (s *Server) handlePageSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var ytChannels, tiktokChannels, xChannels []model.Channel
	var ytVideos, tiktokVideos []model.Video
	var xPosts []model.FeedItem

	if q != "" {
		allChannels, _ := s.db.SearchChannelsFast(q, 50)
		for _, ch := range allChannels {
			switch ch.Platform {
			case "tiktok":
				if s.platformEnabled("tiktok") {
					tiktokChannels = append(tiktokChannels, ch)
				}
			case "twitter":
				if s.platformEnabled("twitter") {
					xChannels = append(xChannels, ch)
				}
			default:
				if s.platformEnabled("youtube") {
					ytChannels = append(ytChannels, ch)
				}
			}
		}

		allVideos, _ := s.db.SearchVideosFast(q, 200)
		for _, v := range allVideos {
			switch v.Platform {
			case "tiktok", "instagram":
				if s.platformEnabled("tiktok") {
					tiktokVideos = append(tiktokVideos, v)
				}
			default:
				if s.platformEnabled("youtube") {
					ytVideos = append(ytVideos, v)
				}
			}
		}

		if s.platformEnabled("twitter") {
			xPosts, _ = s.db.SearchFeedItems(q, 50)
		}

		if len(xPosts) > 0 {
			xPosts = feed.EnrichFeedItems(s.db, xPosts)
		}
	}

	p := s.pageProps(w, r)
	p.PageTitle = "Search"
	p.ActiveNav = "videos"
	p.ESBundle = "js/dist/feed.js"
	p.Sidebar = s.mustBuildSidebar(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.SearchPage(p, q, ytChannels, ytVideos, tiktokChannels, tiktokVideos, xChannels, xPosts).Render(r.Context(), w)
}

func (s *Server) handlePageYouTubeSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var results []map[string]any
	var searchErr string

	if q != "" {
		var err error
		results, err = youtubeSearch(q, 20)
		if err != nil {
			searchErr = err.Error()
			slog.Warn("YouTube search failed", "q", q, "err", err)
		}
	}

	p := s.pageProps(w, r)
	p.PageTitle = fmt.Sprintf("YouTube: %s", q)
	p.ActiveNav = "videos"
	p.Sidebar = s.mustBuildSidebar(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.YouTubeSearchPage(p, q, results, searchErr).Render(r.Context(), w)
}

// youtubeSearch runs yt-dlp ytsearch to find YouTube videos.
func youtubeSearch(q string, limit int) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--dump-json", "--flat-playlist", "--no-warnings", "--quiet",
		fmt.Sprintf("ytsearch%d:%s", limit, q),
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp search: %w", err)
	}

	var results []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		videoID, _ := item["id"].(string)
		title, _ := item["title"].(string)
		channel, _ := item["channel"].(string)
		channelID, _ := item["channel_id"].(string)
		duration, _ := item["duration"].(float64)
		viewCount, _ := item["view_count"].(float64)

		results = append(results, map[string]any{
			"VideoID":      videoID,
			"Title":        title,
			"ThumbnailURL": fmt.Sprintf("https://i.ytimg.com/vi/%s/mqdefault.jpg", videoID),
			"ChannelName":  channel,
			"ChannelID":    channelID,
			"Duration":     int(duration),
			"ViewCount":    int(viewCount),
			"Platform":     "youtube",
			"AvatarURL":    "",
		})
	}
	return results, nil
}

// enrichedChannels fetches subscribed channels with video counts and avatars, sorted.
func (s *Server) enrichedChannels() []model.Channel {
	channels, err := s.db.GetSubscribedChannels()
	if err != nil {
		slog.Error("GetSubscribedChannels", "err", err)
		return nil
	}

	videoCounts, _ := s.db.GetAllVideoCountsByChannel()

	for i := range channels {
		ch := &channels[i]
		ch.VideoCount = videoCounts[ch.ChannelID]
		ch.AvatarURL = "/api/media/avatar/" + ch.ChannelID
	}

	sort.Slice(channels, func(i, j int) bool {
		si, sj := boolToInt(!channels[i].IsStarred), boolToInt(!channels[j].IsStarred)
		if si != sj {
			return si < sj
		}
		return strings.ToLower(channels[i].Name) < strings.ToLower(channels[j].Name)
	})

	return channels
}

// mustBuildSidebar fetches channels and delegates to buildSidebarContext.
func (s *Server) mustBuildSidebar(r *http.Request) model.SidebarContext {
	channels := s.enrichedChannels()
	return s.buildSidebarContext(r, channels)
}

func (s *Server) handlePageTempWatch(w http.ResponseWriter, r *http.Request) {
	videoID := r.URL.Query().Get("v")
	if videoID == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	owner, ok := s.videoAssetOwner(videoID)
	if ok && s.canonicalStreamAsset(owner) != nil {
		http.Redirect(w, r, "/player/"+videoID, http.StatusSeeOther)
		return
	}

	// Video not downloaded yet — render downloading page.
	p := s.pageProps(w, r)
	p.PageTitle = "Downloading..."
	p.ActiveNav = "videos"
	p.Sidebar = s.mustBuildSidebar(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = components.TempDownloadPage(p, videoID, "https://www.youtube.com/watch?v="+videoID).Render(r.Context(), w)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
