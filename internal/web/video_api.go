package web

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/sponsorblock"
	"github.com/screwys/igloo/internal/subtitlemeta"
)

// registerVideoAPIRoutes registers video-related API routes.
func (s *Server) registerVideoAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/videos", s.handleVideosList)
	mux.HandleFunc("GET /api/videos/sync", s.handleVideosSync)
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

// batchCheckSubtitles returns a set of video IDs that have subtitle files.
func batchCheckSubtitles(dataDir string, videos []model.Video) map[string]bool {
	result := make(map[string]bool)
	// Group by directory to avoid re-reading the same dir for every video
	type entry struct {
		videoID string
		stem    string
	}
	byDir := make(map[string][]entry)
	for _, v := range videos {
		if v.FilePath == "" {
			continue
		}
		abs := resolveDataPath(dataDir, v.FilePath)
		dir := filepath.Dir(abs)
		stem := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
		byDir[dir] = append(byDir[dir], entry{v.VideoID, stem})
	}
	for dir, entries := range byDir {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		vttFiles := make([]string, 0)
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".vtt") {
				vttFiles = append(vttFiles, f.Name())
			}
		}
		if len(vttFiles) == 0 {
			continue
		}
		for _, e := range entries {
			for _, vtt := range vttFiles {
				if strings.HasPrefix(vtt, e.stem) {
					result[e.videoID] = true
					break
				}
			}
		}
	}
	return result
}

// videoToJSON converts a Video to a JSON map matching Android's VideoDto.
func videoToJSON(v model.Video) map[string]any {
	m := map[string]any{
		"video_id":          v.VideoID,
		"title":             v.Title,
		"channel_id":        v.ChannelID,
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
	m["dearrow_thumb_path"] = ptrOrNil(v.DearrowThumbPath)
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

	user := userFromContext(r.Context())
	if user != nil {
		opts.UserID = user.Username
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
	positions, _ := s.db.GetPlaybackPositions(videoIDs, opts.UserID)
	subtitleSet := batchCheckSubtitles(s.cfg.DataDir, videos)

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

// DEPRECATED: replaced by /api/videos/delta + /api/shorts/delta (#6).
func (s *Server) handleVideosSync(w http.ResponseWriter, r *http.Request) {
	slog.Info("deprecated endpoint hit", "path", "/api/videos/sync", "ua", r.UserAgent())
	opts := db.GetVideosOpts{
		Limit: 100000,
	}

	user := userFromContext(r.Context())
	if user != nil {
		opts.UserID = user.Username
	}

	videos, err := s.db.GetVideos(opts)
	if err != nil {
		slog.Error("handleVideosSync", "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}

	var videoIDs []string
	for _, v := range videos {
		videoIDs = append(videoIDs, v.VideoID)
	}
	bookmarkInfo, _ := s.db.GetBookmarksForVideoIDsRich(videoIDs)
	positions, _ := s.db.GetPlaybackPositions(videoIDs, opts.UserID)
	subtitleSet := batchCheckSubtitles(s.cfg.DataDir, videos)

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
		"total":  len(jsonVideos),
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
	user := userFromContext(r.Context())
	username := ""
	if user != nil {
		username = user.Username
	}
	videoID, _ := s.db.GetSetting("shorts_cursor_video_id_"+username+"_"+scope, "")
	if videoID == "" && scope == "all" {
		videoID, _ = s.db.GetSetting("shorts_cursor_video_id_"+username, "")
		if videoID == "" {
			videoID, _ = s.db.GetSetting("shorts_cursor_video_id", "")
		}
	}
	updatedAtStr, _ := s.db.GetSetting("shorts_cursor_updated_at_ms_"+username+"_"+scope, "0")
	if updatedAtStr == "0" && scope == "all" {
		updatedAtStr, _ = s.db.GetSetting("shorts_cursor_updated_at_ms", "0")
	}
	updatedAtMs, _ := strconv.ParseInt(updatedAtStr, 10, 64)

	body := map[string]any{
		"video_id":      videoID,
		"updated_at_ms": updatedAtMs,
		"scope":         scope,
	}
	if videoID != "" {
		if ordinal, ok, err := s.db.GetShortsOrdinal(videoID, scope); err != nil {
			slog.Error("GetShortsOrdinal", "video", videoID, "err", err)
		} else if ok {
			body["page"] = ((ordinal - 1) / shortsPageSize) + 1
			body["index"] = (ordinal - 1) % shortsPageSize
			body["page_size"] = shortsPageSize
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
	json.NewDecoder(r.Body).Decode(&body)

	watched := true
	if body.Watched != nil {
		watched = *body.Watched
	}

	if err := s.db.MarkWatched(videoID, watched); err != nil {
		slog.Error("MarkWatched", "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	nowMs := time.Now().UnixMilli()
	if watched {
		if err := s.db.UpsertWatchHistoryFullyWatched(user.Username, videoID, nowMs); err != nil {
			slog.Error("UpsertWatchHistoryFullyWatched", "user", user.Username, "video", videoID, "err", err)
			writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
			return
		}
	} else {
		if err := s.db.DeleteWatchHistory(user.Username, videoID); err != nil {
			slog.Error("DeleteWatchHistory", "user", user.Username, "video", videoID, "err", err)
			writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
			return
		}
	}

	valueJSON := `{"watched":true}`
	if !watched {
		valueJSON = `{"watched":false}`
	}
	if err := s.db.RecordSyncChange("video_watched", videoID, valueJSON); err != nil {
		slog.Error("RecordSyncChange video_watched", "video", videoID, "err", err)
	}
	syncVersion, _ := s.db.GetCurrentSyncVersion()

	writeJSON(w, 200, map[string]any{
		"success":      true,
		"sync_version": syncVersion,
	})
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
		components.PlayerPinButton(p, videoID, pinned, isTemp).Render(r.Context(), w)
		return
	}

	var body struct {
		Pinned *bool `json:"pinned"`
	}
	json.NewDecoder(r.Body).Decode(&body)

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

	// Fetch the record first so we have file_path before deleting
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

	// Delete video file and sibling thumbnails
	if video.FilePath != "" {
		absPath := resolveDataPath(s.cfg.DataDir, video.FilePath)
		_ = os.Remove(absPath)
		stem := strings.TrimSuffix(absPath, filepath.Ext(absPath))
		for _, ext := range []string{".webp", ".jpg", ".png", ".image"} {
			_ = os.Remove(stem + ext)
		}
	}

	// Delete from DB (also cleans media_files in same transaction)
	if err := s.db.DeleteVideo(videoID); err != nil {
		slog.Error("DeleteVideo", "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db delete failed"})
		return
	}

	writeJSON(w, 200, map[string]any{"success": true, "video_id": videoID})
}

func (s *Server) handleVideoProgressGet(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	user := userFromContext(r.Context())
	userID := ""
	if user != nil {
		userID = user.Username
	}

	pos, err := s.db.GetPlaybackPosition(videoID, userID)
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
		ClientType  string  `json:"client_type"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	result, err := s.db.SaveProgress(user.Username, videoID, body.Position, body.Duration, body.UpdatedAtMs, body.ClientType)
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
		"sync_version":           result.SyncVersion,
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
			components.PlayerComments(s.pageProps(w, r), nil, "").Render(r.Context(), w)
			return
		}
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	if comments == nil {
		comments = []model.Comment{}
	}

	if r.URL.Query().Get("fmt") == "html" {
		creatorAuthorID := ""
		if video, _ := s.db.GetVideo(videoID); video != nil {
			creatorAuthorID = components.CommentCreatorAuthorID(video.ChannelID)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		components.PlayerComments(s.pageProps(w, r), comments, creatorAuthorID).Render(r.Context(), w)
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

	// Download English subtitles (separate call — --dump-json suppresses file writes).
	if video.Platform == "youtube" && video.FilePath != "" {
		absPath := resolveDataPath(s.cfg.DataDir, video.FilePath)
		videoDir := filepath.Dir(absPath)
		subTemplate := filepath.Join(videoDir, videoID+".%(ext)s")
		subCtx, subCancel := context.WithTimeout(r.Context(), 30*time.Second)
		subCmd := exec.CommandContext(subCtx, "yt-dlp",
			"--no-download", "--no-warnings", "--no-config",
			"--cookies-from-browser", "firefox",
			"--write-subs", "--write-auto-subs",
			"--sub-langs", "en", "--sub-format", "vtt",
			"-o", subTemplate,
			sourceURL,
		)
		if err := subCmd.Run(); err != nil {
			slog.Warn("subtitle download failed", "video", videoID, "err", err)
		}
		subCancel()
	}

	// Fetch comments via --dump-json.
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	args := []string{
		"--get-comments",
		"--no-download",
		"--no-warnings",
		"--no-config",
		"--quiet",
		"--dump-json",
		"--cookies-from-browser", "firefox",
		"--extractor-args", "youtube:max_comments=50",
	}
	args = append(args, sourceURL)
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.Output()
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

	// Parse yt-dlp JSON output
	var parsed []db.CommentInput
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		var info map[string]any
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			continue
		}
		commentEntries, _ := info["comments"].([]any)
		for _, entry := range commentEntries {
			ce, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			ci := db.CommentInput{
				CommentID:       fmt.Sprint(ce["id"]),
				Author:          stringFromAny(ce["author"]),
				AuthorID:        stringFromAny(ce["author_id"]),
				AuthorThumbnail: stringFromAny(ce["author_thumbnail"]),
				Text:            stringFromAny(ce["text"]),
			}
			if parent, ok := ce["parent"].(string); ok {
				ci.ParentID = parent
			}
			if lc, ok := ce["like_count"].(float64); ok {
				ci.LikeCount = int(lc)
			}
			if ts, ok := ce["timestamp"].(float64); ok {
				ci.Timestamp = int64(ts)
			}
			if ci.CommentID != "" && ci.CommentID != "<nil>" {
				parsed = append(parsed, ci)
			}
		}
	}

	// Save: delete old, insert new
	oldComments, _ := s.db.GetComments(videoID, 100000)
	s.db.DeleteComments(videoID)
	saved, err := s.db.AddComments(videoID, parsed, video.Platform)
	if err != nil {
		slog.Error("AddComments", "video", videoID, "err", err)
	}
	s.queueYouTubeCommentAuthorAvatars(parsed)

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

	if r.URL.Query().Get("fmt") == "html" {
		creatorAuthorID := components.CommentCreatorAuthorID(video.ChannelID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		components.PlayerComments(s.pageProps(w, r), comments, creatorAuthorID).Render(r.Context(), w)
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

func (s *Server) queueYouTubeCommentAuthorAvatars(comments []db.CommentInput) {
	if len(comments) == 0 {
		return
	}
	if n, err := s.db.SeedYouTubeCommentAuthorProfiles(); err != nil {
		slog.Warn("SeedYouTubeCommentAuthorProfiles", "err", err)
	} else if n > 0 {
		slog.Info("seeded_youtube_comment_author_profiles", "count", n)
	}
	if s.requestAvatar == nil {
		return
	}
	seen := map[string]struct{}{}
	for _, comment := range comments {
		channelID := model.YouTubeCommentAuthorChannelID(comment.AuthorID)
		if channelID == "" {
			continue
		}
		if _, ok := seen[channelID]; ok {
			continue
		}
		seen[channelID] = struct{}{}
		s.requestAvatar(channelID)
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
			s.db.MarkSponsorBlockChecked(videoID, "old")
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
	video, err := s.db.GetVideo(videoID)
	if err != nil || video == nil || video.FilePath == "" {
		writeJSON(w, 200, map[string]any{"video_id": videoID, "tracks": []any{}, "count": 0})
		return
	}

	absFilePath := resolveDataPath(s.cfg.DataDir, video.FilePath)
	dir := filepath.Dir(absFilePath)
	stem := strings.TrimSuffix(filepath.Base(absFilePath), filepath.Ext(absFilePath))
	entries, _ := os.ReadDir(dir)

	infoPath := filepath.Join(dir, stem+".info.json")
	manualLangs := subtitlemeta.ManualLangs(infoPath)
	audioLanguage := subtitlemeta.Language(infoPath)

	var tracks []map[string]any
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, stem) || !strings.HasSuffix(name, ".vtt") {
			continue
		}
		lang := subtitlemeta.TrackLang(stem, name)
		isAuto := !manualLangs[lang]
		label := strings.ToUpper(lang[:1]) + lang[1:]
		if isAuto {
			label += " (auto)"
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

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	video, err := s.db.GetVideo(videoID)
	if err != nil {
		slog.Error("GetVideo", "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error", "video_id": videoID})
		return
	}

	var filePath string
	if video != nil {
		filePath = resolveDataPath(s.cfg.DataDir, video.FilePath)
	}

	// Fall back to media_files for feed media videos (GIFs, tweet videos)
	// which are not in the videos table. Check both feed_media and quote_media
	// owner types — quote media is stored under the quote tweet ID. Prefer a
	// video-typed file: mixed tweets (photo + video GIF) put the photo at
	// index 0, so GetMediaFilePath(..., 0) would serve a JPG.
	if filePath == "" {
		filePath = s.findFeedMediaVideoFile(videoID)
	}
	if filePath == "" {
		writeJSON(w, 404, map[string]any{
			"success":  false,
			"error":    "video not found",
			"code":     "VIDEO_NOT_FOUND",
			"video_id": videoID,
		})
		return
	}
	if filePath == "" {
		writeJSON(w, 404, map[string]any{
			"success":  false,
			"error":    "video file not found",
			"code":     "VIDEO_FILE_NOT_FOUND",
			"video_id": videoID,
		})
		return
	}

	// Mixed media: file_path may point to a photo (e.g. slideshow with
	// photo + video). Look for a video file in metadata slides instead.
	fpLower := strings.ToLower(filePath)
	if video != nil && (strings.HasSuffix(fpLower, ".jpg") || strings.HasSuffix(fpLower, ".jpeg") ||
		strings.HasSuffix(fpLower, ".png") || strings.HasSuffix(fpLower, ".webp")) {
		if meta := video.ParseMetadata(); meta != nil {
			for i := range meta.Slides {
				s := meta.SlidePath(i)
				sl := strings.ToLower(s)
				if strings.HasSuffix(sl, ".mp4") || strings.HasSuffix(sl, ".mkv") ||
					strings.HasSuffix(sl, ".webm") || strings.HasSuffix(sl, ".mov") ||
					strings.HasSuffix(sl, ".m4v") {
					if _, err := os.Stat(s); err == nil {
						filePath = s
						break
					}
				}
			}
		}
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		writeJSON(w, 404, map[string]any{
			"success":  false,
			"error":    "video file not found",
			"code":     "VIDEO_FILE_NOT_FOUND",
			"video_id": videoID,
		})
		return
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	contentType := ""
	switch ext {
	case ".mp4":
		contentType = "video/mp4"
	case ".webm":
		contentType = "video/webm"
	case ".mkv":
		contentType = "video/x-matroska"
	case ".jpg", ".jpeg", ".image", ".png", ".webp", ".gif":
		contentType = detectImageContentType(filePath)
	case ".mp3":
		contentType = "audio/mpeg"
	case ".m4a", ".aac", ".ogg":
		contentType = "audio/mp4"
	default:
		contentType = mime.TypeByExtension(ext)
	}

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "private, no-transform")

	f, err := os.Open(filePath)
	if err != nil {
		writeJSON(w, 404, map[string]any{"success": false, "error": "cannot open file", "video_id": videoID})
		return
	}
	defer f.Close()

	fi, _ := f.Stat()
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")

	video, _ := s.db.GetVideo(videoID)
	audioExts := []string{".mp3", ".m4a", ".ogg", ".aac"}

	if video != nil && video.FilePath != "" {
		dir := filepath.Dir(resolveDataPath(s.cfg.DataDir, video.FilePath))
		for _, ext := range audioExts {
			for _, stem := range []string{videoID, videoID + "_0"} {
				candidate := filepath.Join(dir, stem+ext)
				if _, err := os.Stat(candidate); err == nil {
					w.Header().Set("Cache-Control", "public, max-age=3600")
					http.ServeFile(w, r, candidate)
					return
				}
			}
		}
	}

	if path := s.findFeedMediaAudioFile(videoID); path != "" {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeFile(w, r, path)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleSlide(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("videoID")
	indexStr := r.PathValue("index")

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		http.NotFound(w, r)
		return
	}

	// Try video record first (for downloaded videos with slides)
	video, _ := s.db.GetVideo(videoID)
	if video != nil {
		meta := video.ParseMetadata()
		if meta != nil && index < len(meta.Slides) {
			if slide := meta.SlideAsMap(index); slide != nil {
				if pathVal, ok := slide["path"].(string); ok && pathVal != "" {
					absSlide := resolveDataPath(s.cfg.DataDir, pathVal)
					if _, err := os.Stat(absSlide); err == nil {
						s.serveSlideFile(w, r, absSlide)
						return
					}
				}
				if urlVal, ok := slide["url"].(string); ok && urlVal != "" && video.FilePath != "" {
					absVideo := resolveDataPath(s.cfg.DataDir, video.FilePath)
					candidate := filepath.Join(filepath.Dir(absVideo), urlVal)
					if _, err := os.Stat(candidate); err == nil {
						s.serveSlideFile(w, r, candidate)
						return
					}
				}
			}
		}
	}

	// Slideshow fallback: slides are {videoID}_{1-based}.jpg in the video directory
	if video != nil && video.FilePath != "" {
		dir := filepath.Dir(resolveDataPath(s.cfg.DataDir, video.FilePath))
		fileIndex := index + 1
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", videoID, fileIndex, ext))
			if _, err := os.Stat(candidate); err == nil {
				s.serveSlideFile(w, r, candidate)
				return
			}
		}
	}

	// Feed media fallback: look up feed_items and find downloaded media files
	if path := s.findFeedMediaFile(videoID, index); path != "" {
		if fi, err := os.Stat(path); err == nil && fi.Size() >= 100 {
			s.serveSlideFile(w, r, path)
			return
		}
	}

	// CDN proxy fallback: fetch from media_json URL and proxy to client
	if cdnURL := s.findCDNSlideURL(videoID, index); cdnURL != "" {
		s.proxyCDNMedia(w, r, cdnURL)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) serveSlideFile(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if strings.HasPrefix(detectImageContentType(path), "image/") {
		w.Header().Set("Content-Type", detectImageContentType(path))
	}
	http.ServeFile(w, r, path)
}

type feedMediaOwnerRef struct {
	ownerType string
	ownerID   string
	handle    string
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (s *Server) resolveFeedMediaRefs(tweetID string) []feedMediaOwnerRef {
	items, err := s.db.GetFeedItemsForTweetIDs([]string{tweetID})
	if err != nil {
		return nil
	}
	fi, ok := items[tweetID]
	if !ok {
		return nil
	}

	refs := []feedMediaOwnerRef{{
		ownerType: "feed_media",
		ownerID:   tweetID,
		handle:    firstNonEmpty(fi.SourceHandle, fi.AuthorHandle),
	}}
	if fi.QuoteTweetID != "" {
		refs = append(refs, feedMediaOwnerRef{
			ownerType: "quote_media",
			ownerID:   fi.QuoteTweetID,
			handle:    firstNonEmpty(fi.QuoteAuthorHandle, fi.AuthorHandle, fi.SourceHandle),
		})
	}
	return refs
}

func (s *Server) findFeedMediaVideoFile(tweetID string) string {
	for _, ref := range s.resolveFeedMediaRefs(tweetID) {
		if relPath, err := s.db.GetMediaFileVideoPath(ref.ownerType, ref.ownerID); err == nil {
			absPath := resolveDataPath(s.cfg.DataDir, relPath)
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
		if path := s.probeMediaVideoFile(ref.handle, ref.ownerID); path != "" {
			return path
		}
	}
	if path := s.findDirectFeedMediaVideoFile(tweetID); path != "" {
		return path
	}
	return ""
}

func (s *Server) findFeedMediaAudioFile(tweetID string) string {
	for _, ref := range s.resolveFeedMediaRefs(tweetID) {
		if relPath, err := s.db.GetMediaFileAudioPath(ref.ownerType, ref.ownerID); err == nil {
			absPath := resolveDataPath(s.cfg.DataDir, relPath)
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	return ""
}

// findFeedMediaFile locates a downloaded feed media file for a tweet.
// If the tweet is a quote wrapper, it follows quote_tweet_id to the local
// quote_media rows/files instead of assuming the parent tweet owns the file.
func (s *Server) findFeedMediaFile(tweetID string, index int) string {
	refs := s.resolveFeedMediaRefs(tweetID)

	// Primary: resolve from media_files DB (authoritative for Go-era downloads)
	for _, ref := range refs {
		if relPath, err := s.db.GetMediaFilePath(ref.ownerType, ref.ownerID, index); err == nil {
			absPath := resolveDataPath(s.cfg.DataDir, relPath)
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	if path := s.findDirectFeedMediaFile(tweetID, index); path != "" {
		return path
	}

	// Fallback: filesystem probe for legacy data not in media_files.
	if len(refs) == 0 {
		// Try as quote tweet
		return s.findFeedMediaByQuoteTweetID(tweetID, index)
	}
	for _, ref := range refs {
		if path := s.probeMediaFile(ref.handle, ref.ownerID, index); path != "" {
			return path
		}
	}
	return ""
}

func (s *Server) findDirectFeedMediaVideoFile(tweetID string) string {
	for _, ownerType := range []string{"feed_media", "quote_media"} {
		if relPath, err := s.db.GetMediaFileVideoPath(ownerType, tweetID); err == nil {
			absPath := resolveDataPath(s.cfg.DataDir, relPath)
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	return ""
}

func (s *Server) findDirectFeedMediaFile(tweetID string, index int) string {
	for _, ownerType := range []string{"feed_media", "quote_media"} {
		if relPath, err := s.db.GetMediaFilePath(ownerType, tweetID, index); err == nil {
			absPath := resolveDataPath(s.cfg.DataDir, relPath)
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	return ""
}

// findFeedMediaByQuoteTweetID searches for media from quote tweets.
func (s *Server) findFeedMediaByQuoteTweetID(quoteTweetID string, index int) string {
	var sourceHandle string
	s.db.WithRead(func(conn *sql.DB) error {
		conn.QueryRow(
			"SELECT COALESCE(quote_author_handle, author_handle) FROM feed_items WHERE quote_tweet_id = ? LIMIT 1",
			quoteTweetID,
		).Scan(&sourceHandle)
		return nil
	})
	if sourceHandle == "" {
		return ""
	}
	return s.probeMediaFile(sourceHandle, quoteTweetID, index)
}

// probeMediaFile checks common extensions for a feed media file on disk.
func (s *Server) probeMediaFile(handle, tweetID string, index int) string {
	// Try both directories: media/ (Go-era) and videos/ (Python-era)
	dirs := []string{
		filepath.Join(s.cfg.DataDir, "media", "twitter", handle),
		filepath.Join(s.cfg.DataDir, "videos", "twitter", handle),
	}
	// Try both 0-based (Go) and 1-based (legacy Python) file naming
	fileIndices := []int{index, index + 1}

	for _, baseDir := range dirs {
		for _, fi := range fileIndices {
			for _, ext := range []string{".jpg", ".png", ".webp", ".mp4"} {
				path := filepath.Join(baseDir, fmt.Sprintf("%s_%d%s", tweetID, fi, ext))
				if _, err := os.Stat(path); err == nil {
					return path
				}
			}
		}
	}

	return ""
}

func (s *Server) probeMediaVideoFile(handle, tweetID string) string {
	if handle == "" || tweetID == "" {
		return ""
	}

	dirs := []string{
		filepath.Join(s.cfg.DataDir, "media", "twitter", handle),
		filepath.Join(s.cfg.DataDir, "videos", "twitter", handle),
	}
	fileIndices := []int{0, 1}
	videoExts := []string{".mp4", ".webm", ".mkv", ".mov", ".m4v", ".gif"}

	for _, baseDir := range dirs {
		for _, ext := range videoExts {
			path := filepath.Join(baseDir, tweetID+ext)
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		for _, fi := range fileIndices {
			for _, ext := range videoExts {
				path := filepath.Join(baseDir, fmt.Sprintf("%s_%d%s", tweetID, fi, ext))
				if _, err := os.Stat(path); err == nil {
					return path
				}
			}
		}
	}

	return ""
}

// findCDNSlideURL looks up the CDN URL for a feed item's media from media_json or quote_media_json.
func (s *Server) findCDNSlideURL(tweetID string, index int) string {
	// Try media_json from feed_items
	items, _ := s.db.GetFeedItemsForTweetIDs([]string{tweetID})
	if fi, ok := items[tweetID]; ok && fi.MediaJSON != "" {
		if url := pickCDNURL(fi.MediaJSON, index); url != "" {
			return url
		}
	}

	// Try quote_media_json (tweet is a quoted post)
	var quoteMediaJSON string
	s.db.WithRead(func(conn *sql.DB) error {
		conn.QueryRow(
			"SELECT COALESCE(quote_media_json,'') FROM feed_items WHERE quote_tweet_id = ? LIMIT 1",
			tweetID,
		).Scan(&quoteMediaJSON)
		return nil
	})
	if quoteMediaJSON != "" {
		if url := pickCDNURL(quoteMediaJSON, index); url != "" {
			return url
		}
	}

	return ""
}

// pickCDNURL extracts a media URL from a JSON media array at the given index.
// For photos: returns the url field. For video/gif: returns thumbnail_url.
func pickCDNURL(mediaJSON string, index int) string {
	var mediaList []map[string]any
	if err := json.Unmarshal([]byte(mediaJSON), &mediaList); err != nil {
		return ""
	}
	if index < 0 || index >= len(mediaList) {
		return ""
	}
	entry := mediaList[index]
	mtype, _ := entry["type"].(string)
	if mtype == "photo" {
		if url, _ := entry["url"].(string); url != "" {
			return url
		}
	}
	if mtype == "video" || mtype == "gif" {
		if url, _ := entry["thumbnail_url"].(string); url != "" {
			return url
		}
		if url, _ := entry["thumbnail"].(string); url != "" {
			return url
		}
	}
	// Fallback
	if url, _ := entry["url"].(string); url != "" {
		return url
	}
	return ""
}

func normalizeMediaContentType(contentType string) string {
	if contentType == "" {
		return ""
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.Index(contentType, ";"); i >= 0 {
		contentType = strings.TrimSpace(contentType[:i])
	}
	return contentType
}

func isProxyableMediaContentType(contentType string) bool {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return true
	case strings.HasPrefix(contentType, "video/"):
		return true
	case strings.HasPrefix(contentType, "audio/"):
		return true
	default:
		return false
	}
}

const maxCDNProxyBytes int64 = 25 << 20 // 25 MiB

func isSafeCDNProxyURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	hostname := u.Hostname()
	if hostname == "" {
		return false
	}
	if parsed, err := netip.ParseAddr(hostname); err == nil {
		return isPublicAddr(parsed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, addr := range addrs {
		parsed, ok := netip.AddrFromSlice(addr.IP)
		if !ok || !isPublicAddr(parsed) {
			return false
		}
	}
	return true
}

func isPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicAddrPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var nonPublicAddrPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// proxyCDNMedia fetches media from a CDN URL and proxies it to the client.
func (s *Server) proxyCDNMedia(w http.ResponseWriter, r *http.Request, cdnURL string) {
	if !isSafeCDNProxyURL(cdnURL) {
		http.NotFound(w, r)
		return
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest("GET", cdnURL, nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.NotFound(w, r)
		return
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	sniff, _ := reader.Peek(512)
	if resp.ContentLength > maxCDNProxyBytes {
		http.NotFound(w, r)
		return
	}

	contentType := normalizeMediaContentType(resp.Header.Get("Content-Type"))
	if !isProxyableMediaContentType(contentType) {
		if len(sniff) == 0 {
			http.NotFound(w, r)
			return
		}
		contentType = normalizeMediaContentType(http.DetectContentType(sniff))
	}
	if !isProxyableMediaContentType(contentType) {
		http.NotFound(w, r)
		return
	}
	body, oversized, err := stageCDNProxyBody(reader, maxCDNProxyBytes)
	if err != nil {
		slog.Warn("proxyCDNMedia staging failed", "url", cdnURL, "err", err)
		http.NotFound(w, r)
		return
	}
	if oversized {
		http.NotFound(w, r)
		return
	}
	defer func() {
		name := body.Name()
		_ = body.Close()
		_ = os.Remove(name)
	}()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if _, err := io.Copy(w, body); err != nil {
		slog.Warn("proxyCDNMedia copy failed", "url", cdnURL, "err", err)
	}
}

func stageCDNProxyBody(r io.Reader, limit int64) (*os.File, bool, error) {
	body, err := os.CreateTemp("", "igloo-cdn-proxy-*")
	if err != nil {
		return nil, false, err
	}
	n, copyErr := io.CopyN(body, r, limit+1)
	if copyErr != nil && copyErr != io.EOF {
		name := body.Name()
		_ = body.Close()
		_ = os.Remove(name)
		return nil, false, copyErr
	}
	if n > limit {
		name := body.Name()
		_ = body.Close()
		_ = os.Remove(name)
		return nil, true, nil
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		name := body.Name()
		_ = body.Close()
		_ = os.Remove(name)
		return nil, false, err
	}
	return body, false, nil
}
