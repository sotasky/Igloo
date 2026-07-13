package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/sponsorblock"
)

// registerVideoAPIRoutes registers video-related API routes.
func (s *Server) registerVideoAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/videos", s.handleVideosList)
	mux.HandleFunc("GET /api/shorts/history", s.handleShortsHistory)
	mux.HandleFunc("POST /api/videos/{videoID}/watched", s.handleVideoWatched)
	mux.HandleFunc("POST /api/videos/{videoID}/pin", s.handleVideoPin)
	mux.HandleFunc("GET /api/videos/{videoID}/progress", s.handleVideoProgressGet)
	mux.HandleFunc("POST /api/videos/{videoID}/progress", s.handleVideoProgressPost)
	mux.HandleFunc("GET /api/videos/{videoID}/comments", s.handleVideoComments)
	mux.HandleFunc("POST /api/videos/{videoID}/comments/refresh", s.handleVideoCommentsRefresh)
	mux.HandleFunc("GET /api/videos/{videoID}/segments", s.handleVideoSegments)
	mux.HandleFunc("GET /api/videos/{videoID}/subtitles", s.handleVideoSubtitlesList)
	mux.HandleFunc("DELETE /api/videos/{videoID}", s.handleVideoDelete)
	mux.HandleFunc("GET /api/media/stream/{videoID}", s.handleStream)
	mux.HandleFunc("GET /api/media/audio/{videoID}", s.handleAudio)
	mux.HandleFunc("GET /api/media/slide/{videoID}/{index}", s.handleSlide)
}

func (s *Server) batchCheckSubtitles(videos []model.Video) map[string]bool {
	owners := make([]db.AssetOwnerRef, 0, len(videos))
	for _, video := range videos {
		switch video.OwnerKind {
		case "tweet", "youtube_video", "tiktok_video", "instagram_reel":
			owners = append(owners, db.AssetOwnerRef{OwnerKind: video.OwnerKind, OwnerID: video.VideoID})
		}
	}
	assets, err := s.db.ListReadyAssetsForOwners(owners, []string{"subtitle"})
	if err != nil {
		return map[string]bool{}
	}
	result := make(map[string]bool, len(assets))
	for _, asset := range assets {
		result[asset.OwnerID] = true
	}
	return result
}

// videoToJSON converts a Video to a JSON map matching Android's VideoDto.
func videoToJSON(v model.Video) map[string]any {
	m := map[string]any{
		"video_id":          v.VideoID,
		"title":             v.Title,
		"channel_id":        v.ChannelID,
		"owner_kind":        v.OwnerKind,
		"channel_name":      v.ChannelName,
		"platform":          v.Platform,
		"duration":          float64(v.Duration),
		"duration_label":    model.DurationLabel(v.Duration),
		"thumbnail_url":     v.ThumbnailURL,
		"watched":           v.Watched,
		"playback_position": v.PlaybackPosition,
		"bookmarked":        false,
		"description":       v.Description,
		"comment_count":     0,
		"has_subtitles":     false,
	}
	if v.PublishedAt != nil {
		m["published_at"] = v.PublishedAt.UnixMilli()
	} else {
		m["published_at"] = int64(0)
	}
	// Add metadata stats if available
	meta := v.ParseMetadata()
	if meta != nil {
		if meta.ViewCount > 0 {
			m["view_count"] = meta.ViewCount
			m["view_count_label"] = model.CompactCountLabel(meta.ViewCount)
		}
		if meta.LikeCount > 0 {
			m["like_count"] = meta.LikeCount
			m["like_count_label"] = model.CompactCountLabel(meta.LikeCount)
		}
	}
	m["media_kind"] = v.MediaKind
	m["media_mode"] = model.MediaMode(v.MediaKind, v.MediaSlideCount)
	m["media_slide_count"] = v.MediaSlideCount
	m["slide_count"] = v.MediaSlideCount
	m["source_kind"] = v.SourceKind
	if v.MediaKind == "slideshow" && v.Platform == "tiktok" {
		m["audio_url"] = "/api/media/audio/" + v.VideoID
	}
	displayTitle, displayTitleCasual := ResolveDearrowDisplayTitles(v.Title, v.DearrowTitle, v.DearrowTitleCasual)
	m["display_title"] = displayTitle
	m["display_title_casual"] = displayTitleCasual
	m["dearrow_title"] = ptrOrNil(v.DearrowTitle)
	m["dearrow_title_casual"] = ptrOrNil(v.DearrowTitleCasual)
	m["dearrow_checked_at_ms"] = ptrOrNilInt64(v.DearrowCheckedAtMs)
	return m
}

// ptrOrNil returns nil for a nil pointer, else the dereferenced string.
// Used to emit SQL NULLs as JSON nulls rather than "".
func ptrOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func ptrOrNilInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func (s *Server) handleVideosList(w http.ResponseWriter, r *http.Request) {
	limit := 40
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	opts := db.GetVideosOpts{
		ChannelID:     r.URL.Query().Get("channel_id"),
		Platform:      r.URL.Query().Get("platform"),
		Search:        r.URL.Query().Get("search"),
		UnwatchedOnly: r.URL.Query().Get("unwatched") == "1",
		Limit:         limit,
		Offset:        offset,
	}

	total, _ := s.db.GetVideoCount(opts)
	videos, err := s.db.GetVideos(opts)
	if err != nil {
		slog.Error("GetVideos", "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}

	// Collect video IDs for bookmark + position lookup
	var videoIDs []string
	for _, v := range videos {
		videoIDs = append(videoIDs, v.VideoID)
	}
	bookmarkInfo, _ := s.db.GetBookmarksForVideoIDsRich(videoIDs)
	positions, _ := s.db.GetPlaybackPositions(videoIDs)
	subtitleSet := s.batchCheckSubtitles(videos)

	var jsonVideos []map[string]any
	for _, v := range videos {
		v.EnrichForCard()
		jv := videoToJSON(v)
		if bi, ok := bookmarkInfo[v.VideoID]; ok {
			jv["bookmarked"] = true
			if bi.CategoryID != nil {
				jv["bookmark_category_id"] = *bi.CategoryID
			}
		}
		if pos, ok := positions[v.VideoID]; ok {
			jv["playback_position"] = pos
		}
		if subtitleSet[v.VideoID] {
			jv["has_subtitles"] = true
		}
		jsonVideos = append(jsonVideos, jv)
	}
	if jsonVideos == nil {
		jsonVideos = []map[string]any{}
	}

	writeJSON(w, 200, map[string]any{
		"videos": jsonVideos,
		"total":  total,
	})
}

func (s *Server) handleShortsHistory(w http.ResponseWriter, r *http.Request) {
	scope, ok := db.NormalizeMomentsCursorScope(r.URL.Query().Get("tab"))
	if r.URL.Query().Get("tab") == "" {
		scope, ok = db.NormalizeMomentsCursorScope(s.db.MomentsDefaultTab())
		if !ok {
			scope = "all"
		}
	}
	if !ok {
		writeJSON(w, 200, map[string]any{
			"video_id":      "",
			"updated_at_ms": int64(0),
			"scope":         "",
		})
		return
	}
	cursor, _, err := s.db.GetMomentsCursor(scope)
	if err != nil {
		slog.Error("GetMomentsCursor", "scope", scope, "err", err)
	}
	videoID := cursor.VideoID
	updatedAtMs := cursor.UpdatedAtMs
	sortAtMs := cursor.SortAtMs

	body := map[string]any{
		"video_id":      videoID,
		"updated_at_ms": updatedAtMs,
		"scope":         scope,
	}
	if sortAtMs > 0 {
		body["sort_at_ms"] = sortAtMs
	}
	if videoID != "" {
		setPageHint := func(ordinal int) {
			body["page"] = ((ordinal - 1) / shortsPageSize) + 1
			body["index"] = (ordinal - 1) % shortsPageSize
			body["page_size"] = shortsPageSize
		}
		resolved := false
		if sortAtMs > 0 {
			if currentSortAt, visible, err := s.db.GetShortsVisibleSortAt(videoID, scope); err != nil {
				slog.Error("GetShortsVisibleSortAt", "video", videoID, "scope", scope, "err", err)
			} else if !visible || currentSortAt != sortAtMs {
				if fallbackVideoID, fallbackOrdinal, fallbackOK, err := s.db.GetNearestShortsCursorTarget(videoID, scope, sortAtMs); err != nil {
					slog.Error("GetNearestShortsCursorTarget", "video", videoID, "scope", scope, "err", err)
				} else if fallbackOK {
					body["video_id"] = fallbackVideoID
					body["fallback_for_video_id"] = videoID
					setPageHint(fallbackOrdinal)
					resolved = true
				}
			}
		}
		if !resolved {
			if ordinal, ok, err := s.db.GetShortsOrdinal(videoID, scope); err != nil {
				slog.Error("GetShortsOrdinal", "video", videoID, "err", err)
			} else if ok {
				setPageHint(ordinal)
			} else if fallbackVideoID, fallbackOrdinal, fallbackOK, err := s.db.GetNearestShortsCursorTarget(videoID, scope, sortAtMs); err != nil {
				slog.Error("GetNearestShortsCursorTarget", "video", videoID, "scope", scope, "err", err)
			} else if fallbackOK {
				body["video_id"] = fallbackVideoID
				body["fallback_for_video_id"] = videoID
				setPageHint(fallbackOrdinal)
			}
		}
	}

	writeJSON(w, 200, body)
}

func (s *Server) handleVideoWatched(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	videoID := r.PathValue("videoID")
	if videoID == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "videoID required"})
		return
	}

	var body struct {
		Watched *bool `json:"watched"`
	}
	if err := decodeJSON(w, r, &body); err != nil && requestBodyTooLarge(err) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"success": false, "error": requestBodyTooLargeMessage})
		return
	}

	watched := true
	if body.Watched != nil {
		watched = *body.Watched
	}

	nowMs := time.Now().UnixMilli()
	if watched {
		if err := s.db.UpsertWatchHistoryFullyWatched(videoID, nowMs); err != nil {
			slog.Error("UpsertWatchHistoryFullyWatched", "user", user.Username, "video", videoID, "err", err)
			writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
			return
		}
	} else {
		if err := s.db.DeleteWatchHistory(videoID, nowMs); err != nil {
			slog.Error("DeleteWatchHistory", "user", user.Username, "video", videoID, "err", err)
			writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
			return
		}
	}

	writeJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleVideoPin(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	// HTMX player button: toggle and return the re-rendered button.
	if r.URL.Query().Get("ctx") == "player" {
		pinned, err := s.db.TogglePinned(videoID)
		if err != nil {
			slog.Error("TogglePinned", "video", videoID, "err", err)
			http.Error(w, "db error", 500)
			return
		}
		// Fetch the video to know if it's still temp (affects button visibility).
		video, _ := s.db.GetVideo(videoID)
		isTemp := false
		if video != nil {
			isTemp = video.IsTemp
		}
		p := s.pageProps(w, r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.PlayerPinButton(p, videoID, pinned, isTemp).Render(r.Context(), w)
		return
	}

	var body struct {
		Pinned *bool `json:"pinned"`
	}
	if err := decodeJSON(w, r, &body); err != nil && requestBodyTooLarge(err) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"success": false, "error": requestBodyTooLargeMessage})
		return
	}

	pinned := true
	if body.Pinned != nil {
		pinned = *body.Pinned
	}

	if err := s.db.SetPinned(videoID, pinned); err != nil {
		slog.Error("SetPinned", "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"success":   true,
		"video_id":  videoID,
		"is_pinned": pinned,
	})
}

func (s *Server) handleVideoDelete(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	video, err := s.db.GetVideo(videoID)
	if err != nil {
		slog.Error("GetVideo for delete", "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	if video == nil {
		writeJSON(w, 404, map[string]any{"success": false, "error": "video not found"})
		return
	}

	if err := s.db.DeleteVideoWithFile(videoID); err != nil {
		slog.Error("DeleteVideo", "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db delete failed"})
		return
	}

	writeJSON(w, 200, map[string]any{"success": true, "video_id": videoID})
}

func (s *Server) handleVideoProgressGet(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	pos, err := s.db.GetPlaybackPosition(videoID)
	if err != nil {
		slog.Error("GetPlaybackPosition", "video", videoID, "err", err)
		pos = 0
	}

	writeJSON(w, 200, map[string]any{
		"video_id":      videoID,
		"position":      pos,
		"source_policy": "latest_wins_v2",
	})
}

func (s *Server) handleVideoProgressPost(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, 401, map[string]any{"success": false, "error": "unauthorized"})
		return
	}
	videoID := r.PathValue("videoID")

	var body struct {
		Position    float64 `json:"position"`
		Duration    float64 `json:"duration"`
		UpdatedAtMs int64   `json:"updated_at_ms"`
	}
	if err := decodeJSON(w, r, &body); err != nil && requestBodyTooLarge(err) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"success": false, "error": requestBodyTooLargeMessage})
		return
	}

	result, err := s.db.SaveProgress(videoID, body.Position, body.Duration, body.UpdatedAtMs)
	if err != nil {
		slog.Error("SaveProgress", "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	writeJSON(w, 200, map[string]any{
		"success":                true,
		"resolved_position":      result.ResolvedPosition,
		"resolved_updated_at_ms": result.ResolvedUpdatedAtMs,
		"accepted":               result.Accepted,
		"source_policy":          "latest_wins_v2",
	})
}

func (s *Server) handleVideoComments(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	limit := 100
	if r.URL.Query().Get("all") != "" {
		limit = 100000
	} else if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}

	comments, err := s.db.GetComments(videoID, limit)
	if err != nil {
		slog.Error("GetComments", "video", videoID, "err", err)
		if r.URL.Query().Get("fmt") == "html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = components.PlayerComments(s.pageProps(w, r), nil, "").Render(r.Context(), w)
			return
		}
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	if comments == nil {
		comments = []model.Comment{}
	}
	s.projectCommentAuthorAvatars(comments)

	if r.URL.Query().Get("fmt") == "html" {
		creatorAuthorID := ""
		if video, _ := s.db.GetVideo(videoID); video != nil {
			creatorAuthorID = components.CommentCreatorAuthorID(video.ChannelID)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.PlayerComments(s.pageProps(w, r), comments, creatorAuthorID).Render(r.Context(), w)
		return
	}

	writeJSON(w, 200, map[string]any{
		"comments": comments,
		"count":    len(comments),
	})
}

func (s *Server) handleVideoCommentsRefresh(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	video, _ := s.db.GetVideo(videoID)
	if video == nil {
		writeJSON(w, 404, map[string]any{"error": "video not found"})
		return
	}

	if video.Platform != "youtube" && video.Platform != "tiktok" {
		writeJSON(w, 200, map[string]any{
			"success":  true,
			"comments": []any{},
			"count":    0,
			"message":  "comments not supported for " + video.Platform,
		})
		return
	}

	// Build source URL
	var sourceURL string
	switch video.Platform {
	case "youtube":
		sourceURL = "https://www.youtube.com/watch?v=" + videoID
	case "tiktok":
		meta := video.ParseMetadata()
		if meta != nil && meta.WebpageURL != "" {
			sourceURL = meta.WebpageURL
		}
	}
	if sourceURL == "" {
		writeJSON(w, 200, map[string]any{
			"success":  false,
			"error":    "source URL not available",
			"comments": []any{},
			"count":    0,
		})
		return
	}

	// Fetch comments through the same yt-dlp wrapper used by normal and temp downloads.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	commentsDownloader := &download.YtDlpWrapper{}
	var mediaDownloader *download.Downloader
	if s.workers != nil {
		if dl := s.workers.Downloader(); dl != nil && dl.YtDlp != nil {
			mediaDownloader = dl
			commentsDownloader = dl.YtDlp
		}
	}
	if video.Platform == "youtube" {
		s.refreshVideoSubtitles(r.Context(), mediaDownloader, videoID, sourceURL)
	}
	parsed, err := commentsDownloader.FetchComments(ctx, sourceURL, download.DefaultCommentFetchLimit, download.Opts{
		CookiesFromBrowser: "firefox",
	})
	if err != nil {
		slog.Warn("comments refresh yt-dlp failed", "video", videoID, "err", err)
		// Return existing comments on failure
		comments, _ := s.db.GetComments(videoID, 100)
		if comments == nil {
			comments = []model.Comment{}
		}
		writeJSON(w, 200, map[string]any{
			"success":  false,
			"error":    "yt-dlp failed: " + err.Error(),
			"comments": comments,
			"count":    len(comments),
		})
		return
	}

	// Save: delete old, insert new
	oldComments, _ := s.db.GetComments(videoID, 100000)
	_, _ = s.db.DeleteComments(videoID)
	saved, err := s.db.AddComments(videoID, parsed)
	if err != nil {
		slog.Error("AddComments", "video", videoID, "err", err)
	}
	if err == nil && s.workers != nil && len(parsed) > 0 {
		s.workers.KickMediaWork()
	}

	// If fetch returned nothing but had comments before, log warning
	if saved == 0 && len(oldComments) > 0 {
		slog.Warn("comments refresh returned 0, old comments were deleted", "video", videoID, "old_count", len(oldComments))
	}

	// Return refreshed comments
	comments, _ := s.db.GetComments(videoID, 100)
	if comments == nil {
		comments = []model.Comment{}
	}
	for i := range comments {
		comments[i].SetPublishedAtMs()
	}
	s.projectCommentAuthorAvatars(comments)

	if r.URL.Query().Get("fmt") == "html" {
		creatorAuthorID := components.CommentCreatorAuthorID(video.ChannelID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = components.PlayerComments(s.pageProps(w, r), comments, creatorAuthorID).Render(r.Context(), w)
		return
	}

	writeJSON(w, 200, map[string]any{
		"success":  true,
		"deleted":  len(oldComments),
		"saved":    saved,
		"comments": comments,
		"count":    len(comments),
	})
}

func (s *Server) refreshVideoSubtitles(parent context.Context, downloader *download.Downloader, videoID, sourceURL string) {
	owner, ok := s.videoAssetOwner(videoID)
	if !ok {
		return
	}
	stream := s.canonicalStreamAsset(owner)
	if downloader == nil || downloader.YtDlp == nil || stream == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	subtitleDir, err := s.cfg.Storage.WritePath("subtitles/youtube")
	if err != nil {
		slog.Warn("subtitle storage path failed", "video", videoID, "err", err)
		return
	}
	var paths []string
	err = downloader.RunMedia(ctx, download.MediaLaneState, func() error {
		var err error
		paths, err = downloader.YtDlp.DownloadSubtitles(ctx, sourceURL, download.Opts{
			ID:                 fmt.Sprintf("%s-sub-%d", videoID, time.Now().UnixNano()),
			SubtitleDir:        subtitleDir,
			CookiesFromBrowser: "firefox",
		})
		return err
	})
	if err != nil {
		slog.Warn("subtitle download failed", "video", videoID, "err", err)
		return
	}
	assets := make([]db.Asset, 0, len(paths))
	for index, path := range paths {
		key, keyErr := s.cfg.Storage.Key(path)
		if keyErr != nil {
			_ = downloader.RunMedia(ctx, download.MediaLaneState, func() error { removePaths(paths); return nil })
			slog.Warn("subtitle output escaped storage", "video", videoID, "err", keyErr)
			return
		}
		assets = append(assets, db.Asset{
			AssetKind:     "subtitle",
			MediaIndex:    index,
			FilePath:      key,
			ContentType:   "text/vtt",
			AudioLanguage: "en",
		})
	}
	if err := s.db.StoreVideoSubtitleAssets(videoID, assets, time.Now().UnixMilli()); err != nil {
		_ = downloader.RunMedia(ctx, download.MediaLaneState, func() error { removePaths(paths); return nil })
		slog.Warn("publish subtitle assets failed", "video", videoID, "err", err)
	}
}

func removePaths(paths []string) {
	for _, path := range paths {
		if path != "" {
			_ = os.Remove(path)
		}
	}
}

// stringFromAny safely extracts a string from an any value.
func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func (s *Server) handleVideoSegments(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	// Fast path: already checked and has segments.
	checked, _ := s.db.GetSponsorBlockChecked(videoID)
	if checked != nil {
		segments, _ := s.db.GetSponsorBlockSegments(videoID)
		if len(segments) > 0 {
			writeJSON(w, 200, map[string]any{"segments": segments})
			return
		}
	}

	// Only fetch for YouTube videos.
	video, _ := s.db.GetVideo(videoID)
	if video == nil || video.Platform != "youtube" {
		writeJSON(w, 200, map[string]any{"segments": []any{}})
		return
	}

	// Should we fetch from API?
	if !sbShouldFetch(checked) {
		if checked == nil {
			_ = s.db.MarkSponsorBlockChecked(videoID, "old")
		}
		segments, _ := s.db.GetSponsorBlockSegments(videoID)
		if segments == nil {
			segments = []db.SponsorBlockSegment{}
		}
		writeJSON(w, 200, map[string]any{"segments": segments})
		return
	}

	// Fetch from SponsorBlock API.
	raw, err := sponsorblock.Fetch(r.Context(), videoID)
	if err != nil {
		slog.Warn("SponsorBlock fetch failed", "video", videoID, "err", err)
		writeJSON(w, 200, map[string]any{"segments": []any{}})
		return
	}
	segments := make([]db.SponsorBlockSegment, 0, len(raw))
	for _, s := range raw {
		segments = append(segments, db.SponsorBlockSegment{Start: s.Start, End: s.End, Category: s.Category})
	}

	ageLabel := sbAgeLabel(video.PublishedAt)

	if err := s.db.SaveSponsorBlockSegments(videoID, segments); err != nil {
		slog.Error("SaveSponsorBlockSegments", "video", videoID, "err", err)
	}
	if err := s.db.MarkSponsorBlockChecked(videoID, ageLabel); err != nil {
		slog.Error("MarkSponsorBlockChecked", "video", videoID, "err", err)
	}
	writeJSON(w, 200, map[string]any{"segments": segments})
}

func sbShouldFetch(checked *db.SBCheckedRow) bool {
	if checked != nil {
		if checked.VideoAgeAtCheck == "old" {
			return false // Old video, already checked, don't re-fetch
		}
		// Young video — re-check after 24 hours
		if checked.CheckedAtMs > 0 && time.Since(time.UnixMilli(checked.CheckedAtMs)).Hours() < 24 {
			return false
		}
		return true
	}
	// Never checked — always fetch once.
	return true
}

// sbAgeLabel returns "young" if the video was published in the last 48h,
// "old" otherwise. Used by the lazy handler and the background worker so
// sponsorblock_checked carries a consistent label across both paths.
func sbAgeLabel(publishedAt *time.Time) string {
	if publishedAt != nil && time.Since(*publishedAt).Hours() < 48 {
		return "young"
	}
	return "old"
}

func (s *Server) handleVideoSubtitlesList(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	owner, ok := s.videoAssetOwner(videoID)
	var assets []canonicalAssetFile
	if ok {
		assets = s.canonicalAssets(owner, "subtitle")
	}
	var tracks []map[string]any
	audioLanguage := ""
	for _, file := range assets {
		name := filepath.Base(file.asset.FilePath)
		lang := subtitleTrackLanguage(name)
		isAuto := file.asset.IsAuto != nil && *file.asset.IsAuto
		label := strings.ToUpper(lang[:1]) + lang[1:]
		if isAuto {
			label += " (auto)"
		}
		if audioLanguage == "" {
			audioLanguage = file.asset.AudioLanguage
		}
		tracks = append(tracks, map[string]any{
			"track_id":   name,
			"label":      label,
			"srclang":    lang,
			"kind":       "subtitles",
			"is_auto":    isAuto,
			"is_default": lang == "en",
		})
	}
	sort.SliceStable(tracks, func(i, j int) bool {
		ai, _ := tracks[i]["is_auto"].(bool)
		aj, _ := tracks[j]["is_auto"].(bool)
		if ai != aj {
			return !ai
		}
		return tracks[i]["track_id"].(string) < tracks[j]["track_id"].(string)
	})
	if tracks == nil {
		tracks = []map[string]any{}
	}

	writeJSON(w, 200, map[string]any{
		"video_id":       videoID,
		"tracks":         tracks,
		"count":          len(tracks),
		"audio_language": audioLanguage,
	})
}

func subtitleTrackLanguage(name string) string {
	name = strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".vtt")
	parts := strings.Split(name, ".")
	if len(parts) > 1 && strings.TrimSpace(parts[len(parts)-1]) != "" {
		return parts[len(parts)-1]
	}
	return "und"
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	owner, ok := s.requestMediaAssetOwner(r, videoID)
	var file *canonicalAssetFile
	if ok {
		file = s.canonicalStreamAsset(owner)
	}
	if file == nil {
		writeJSON(w, 404, map[string]any{
			"success":  false,
			"error":    "video file not found",
			"code":     "VIDEO_FILE_NOT_FOUND",
			"video_id": videoID,
		})
		return
	}
	contentType := file.asset.ContentType
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "private, no-transform")
	if s.serveDataFileViaXAccel(w, r, file.path, contentType, "private, no-transform") {
		return
	}
	http.ServeFile(w, r, file.path)
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	owner, ok := s.videoAssetOwner(videoID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	file := s.canonicalAsset(owner, "post_audio", 0)
	if file == nil {
		http.NotFound(w, r)
		return
	}
	cacheControl := "public, max-age=3600"
	contentType := file.asset.ContentType
	w.Header().Set("Cache-Control", cacheControl)
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if s.serveDataFileViaXAccel(w, r, file.path, contentType, cacheControl) {
		return
	}
	http.ServeFile(w, r, file.path)
}

func (s *Server) handleSlide(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	indexStr := r.PathValue("index")

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		http.NotFound(w, r)
		return
	}

	owner, ok := s.requestMediaAssetOwner(r, videoID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	file := s.canonicalAsset(owner, "post_media", index)
	if file == nil {
		http.NotFound(w, r)
		return
	}
	s.serveSlideFile(w, r, *file)
}

func (s *Server) serveSlideFile(w http.ResponseWriter, r *http.Request, file canonicalAssetFile) {
	cacheControl := "public, max-age=3600"
	w.Header().Set("Cache-Control", cacheControl)
	contentType := file.asset.ContentType
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if s.serveDataFileViaXAccel(w, r, file.path, contentType, cacheControl) {
		return
	}
	http.ServeFile(w, r, file.path)
}
