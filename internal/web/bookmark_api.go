package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
)

// registerBookmarkAPIRoutes registers bookmark CRUD API routes.
func (s *Server) registerBookmarkAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/bookmark/{videoID}", s.handleBookmarkAdd)
	mux.HandleFunc("DELETE /api/bookmark/{videoID}", s.handleBookmarkRemove)
	mux.HandleFunc("GET /api/bookmark/{videoID}", s.handleBookmarkGet)
	mux.HandleFunc("GET /api/bookmark-account-options", s.handleBookmarkAccountOptions)
	mux.HandleFunc("GET /api/bookmark-categories", s.handleBookmarkCategoriesList)
	mux.HandleFunc("POST /api/bookmark-categories", s.handleBookmarkCategoryCreate)
	mux.HandleFunc("POST /api/bookmark-categories/batch", s.handleBookmarkCategoryBatch)
	mux.HandleFunc("PUT /api/bookmark-categories/{categoryID}", s.handleBookmarkCategoryUpdate)
	mux.HandleFunc("DELETE /api/bookmark-categories/{categoryID}", s.handleBookmarkCategoryDelete)
	mux.HandleFunc("GET /api/bookmark-labels", s.handleBookmarkLabels)
	mux.HandleFunc("DELETE /api/bookmark-labels/{label}", s.handleBookmarkLabelDelete)
}

type bookmarkAccountOption struct {
	Handle    string `json:"handle"`
	Label     string `json:"label"`
	Platform  string `json:"platform,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
}

func (s *Server) handleBookmarkAccountOptions(w http.ResponseWriter, r *http.Request) {
	channels, err := s.db.GetSubscribedChannels()
	if err != nil {
		slog.Error("GetSubscribedChannels", "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}

	options := make([]bookmarkAccountOption, 0, len(channels))
	seen := make(map[string]struct{}, len(channels))
	for _, ch := range channels {
		handle := strings.TrimSpace(components.ChannelDisplayHandle(ch))
		if handle == "" {
			continue
		}
		key := strings.ToLower(handle)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		label := strings.TrimSpace(components.ChannelDisplayName(ch))
		if label == "" {
			label = handle
		}
		options = append(options, bookmarkAccountOption{
			Handle:    handle,
			Label:     label,
			Platform:  ch.Platform,
			ChannelID: ch.ChannelID,
		})
	}

	writeJSON(w, 200, map[string]any{"accounts": options})
}

func (s *Server) handleBookmarkAdd(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	rawVideoID := r.PathValue("videoID")

	var body struct {
		CategoryID     int64    `json:"category_id"`
		CustomTitle    string   `json:"custom_title"`
		AccountHandles []string `json:"account_handles"`
		MediaIndices   []int    `json:"media_indices"`
	}
	if err := decodeJSON(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
		if requestBodyTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"success": false, "error": requestBodyTooLargeMessage})
			return
		}
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid json"})
		return
	}

	accountHandlesJSON := ""
	if len(body.AccountHandles) > 0 {
		b, _ := json.Marshal(body.AccountHandles)
		accountHandlesJSON = string(b)
	}
	mediaIndicesJSON := ""
	if len(body.MediaIndices) > 0 {
		b, _ := json.Marshal(body.MediaIndices)
		mediaIndicesJSON = string(b)
	}

	category, ok, err := s.resolveBookmarkCategory(body.CategoryID)
	if err != nil {
		slog.Error("GetBookmarkCategories", "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	if !ok {
		writeJSON(w, 404, map[string]any{"success": false, "error": "bookmark category not found"})
		return
	}
	body.CategoryID = category.ID

	mutation := db.BookmarkMutation{VideoID: rawVideoID, Action: "set", CategoryID: &body.CategoryID}
	if body.CustomTitle != "" {
		mutation.CustomTitle = &body.CustomTitle
	}
	if accountHandlesJSON != "" {
		mutation.AccountHandles = &accountHandlesJSON
	}
	if mediaIndicesJSON != "" {
		mutation.MediaIndices = &mediaIndicesJSON
	}
	result, err := s.db.MutateBookmark(mutation)
	if err != nil {
		slog.Error("MutateBookmark", "video", rawVideoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	if result.Applied {
		s.requestXStatusRecovery(result.CanonicalID, true)
	}

	categoryName := category.Name
	archivePath := ""
	if bookmarkArchivePathsAllowed(user) {
		archivePath = category.ArchivePath
	}

	writeJSON(w, 200, map[string]any{
		"success":       true,
		"bookmarked":    true,
		"category_id":   body.CategoryID,
		"category_name": categoryName,
	})

	// Side effects past the response: feed_seen + algo_invalidate + archive.
	// None affect the response payload, and each previously added a write-mutex
	// acquisition to the hot path; keeping them off the critical path lets the
	// menu close immediately even while the feed-scoring worker holds the lock
	// for a snapshot rebuild.
	if result.Applied {
		go func() {
			_, _ = s.db.MutateSeen([]string{result.CanonicalID}, 0)
			_ = s.db.InvalidateAlgoScore(result.CanonicalID)
			s.workers.KickFeedScoring()
			if result.Affected > 0 {
				s.workers.KickFeedMedia()
				s.startBookmarkArchive(result.CanonicalID, archivePath, body.CustomTitle, accountHandlesJSON, body.MediaIndices)
			}
		}()
	}
}

func nullableString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func (s *Server) startBookmarkArchive(videoID, archivePath, customTitle, accountHandles string, mediaIndices []int) {
	if archivePath == "" {
		return
	}
	go s.archiveBookmarkCombined(videoID, archivePath, customTitle, accountHandles, mediaIndices)
}

func (s *Server) handleBookmarkRemove(w http.ResponseWriter, r *http.Request) {
	rawVideoID := r.PathValue("videoID")
	result, err := s.db.MutateBookmark(db.BookmarkMutation{VideoID: rawVideoID, Action: "clear"})
	if err != nil {
		slog.Error("MutateBookmark", "video", rawVideoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}
	if result.Applied {
		_ = s.db.InvalidateAlgoScore(result.CanonicalID)
		s.workers.KickFeedScoring()
	}

	writeJSON(w, 200, map[string]any{
		"success":    true,
		"bookmarked": false,
	})
}

func (s *Server) handleBookmarkGet(w http.ResponseWriter, r *http.Request) {
	videoID, ok := s.resolveFeedStateIDForJSON(w, r.PathValue("videoID"))
	if !ok {
		return
	}

	bookmarked, catID, err := s.db.IsBookmarked(videoID)
	if err != nil {
		slog.Error("IsBookmarked", "video", videoID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	categoryName := ""
	if bookmarked && catID > 0 {
		cats, _ := s.db.GetBookmarkCategories()
		for _, c := range cats {
			if c.ID == catID {
				categoryName = c.Name
				break
			}
		}
	}

	writeJSON(w, 200, map[string]any{
		"bookmarked":    bookmarked,
		"category_id":   catID,
		"category_name": categoryName,
		"account_handles": func() []string {
			if !bookmarked {
				return nil
			}
			var raw sql.NullString
			if err := s.db.QueryRow(`
				SELECT account_handles
				  FROM bookmarks
				 WHERE video_id = ?
			`, videoID).Scan(&raw); err != nil {
				return nil
			}
			return parseBookmarkAccountHandles(nullableString(raw))
		}(),
	})
}

func parseBookmarkAccountHandles(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var parsed []string
	if strings.HasPrefix(raw, "[") {
		_ = json.Unmarshal([]byte(raw), &parsed)
	} else {
		parsed = strings.Split(raw, ",")
	}
	seen := make(map[string]struct{}, len(parsed))
	out := make([]string, 0, len(parsed))
	for _, handle := range parsed {
		clean := strings.TrimSpace(strings.TrimPrefix(handle, "@"))
		if clean == "" {
			continue
		}
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, clean)
	}
	return out
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

type bookmarkCategorySelection struct {
	ID          int64
	Name        string
	ArchivePath string
}

func bookmarkArchivePathsAllowed(user *userInfo) bool {
	// Archive paths point at the server filesystem, so treat them as admin-only.
	return user != nil && user.Role == "admin"
}

func (s *Server) resolveBookmarkCategory(requestedID int64) (bookmarkCategorySelection, bool, error) {
	cats, err := s.db.GetBookmarkCategories()
	if err != nil {
		return bookmarkCategorySelection{}, false, err
	}
	if requestedID > 0 {
		for _, c := range cats {
			if c.ID == requestedID {
				return bookmarkCategorySelection{ID: c.ID, Name: c.Name, ArchivePath: c.ArchivePath}, true, nil
			}
		}
		return bookmarkCategorySelection{}, false, nil
	}
	if len(cats) > 0 {
		c := cats[0]
		return bookmarkCategorySelection{ID: c.ID, Name: c.Name, ArchivePath: c.ArchivePath}, true, nil
	}
	return bookmarkCategorySelection{}, true, nil
}

func (s *Server) handleBookmarkCategoriesList(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())

	cats, err := s.db.GetBookmarkCategories()
	if err != nil {
		slog.Error("GetBookmarkCategories", "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	if r.Header.Get("HX-Request") != "" {
		s.renderBookmarkCategoriesHTML(w, r)
		return
	}

	result := make([]map[string]any, 0, len(cats))
	includeArchivePath := bookmarkArchivePathsAllowed(user)
	for _, c := range cats {
		archivePath := ""
		if includeArchivePath {
			archivePath = c.ArchivePath
		}
		result = append(result, map[string]any{
			"id":             c.ID,
			"name":           c.Name,
			"archive_path":   archivePath,
			"created_at":     c.CreatedAtMs,
			"bookmark_count": c.BookmarkCount,
		})
	}

	writeJSON(w, 200, map[string]any{"categories": result})
}

func (s *Server) handleBookmarkCategoryBatch(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	ids := r.Form["id"]
	names := r.Form["name"]
	paths := r.Form["archive_path"]
	canEditArchivePath := bookmarkArchivePathsAllowed(user)

	for i := range ids {
		if i >= len(names) {
			break
		}
		id, _ := strconv.ParseInt(ids[i], 10, 64)
		name := strings.TrimSpace(names[i])
		archivePath := ""
		// Archive paths are server filesystem destinations, so only admins can configure them.
		if canEditArchivePath && i < len(paths) {
			var pathErr error
			archivePath, pathErr = normalizeArchivePath(paths[i])
			if pathErr != nil {
				continue
			}
			if err := ensureArchivePath(archivePath); err != nil {
				continue
			}
		}
		if name == "" {
			continue
		}
		if id > 0 {
			if err := s.db.UpdateBookmarkCategory(id, name, archivePath); err != nil {
				slog.Error("UpdateBookmarkCategory", "id", id, "err", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
		} else {
			if _, err := s.db.CreateBookmarkCategory(name, archivePath); err != nil {
				slog.Error("CreateBookmarkCategory", "err", err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
		}
	}

	s.renderBookmarkCategoriesHTML(w, r)
}

func (s *Server) renderBookmarkCategoriesHTML(w http.ResponseWriter, r *http.Request) {
	cats, _ := s.db.GetBookmarkCategories()
	display := make([]components.BookmarkCategoryDisplay, 0, len(cats))
	for _, c := range cats {
		display = append(display, components.BookmarkCategoryDisplay{
			ID: c.ID, Name: c.Name, ArchivePath: c.ArchivePath,
			Slug: slugRe.ReplaceAllString(strings.ToLower(c.Name), "-"),
		})
	}
	_ = components.BookmarkCategoryPathsPanel(s.pageProps(w, r), display).Render(r.Context(), w)
}

func (s *Server) handleBookmarkCategoryCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())

	var body struct {
		Name        string `json:"name"`
		ArchivePath string `json:"archive_path"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		if requestBodyTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"success": false, "error": requestBodyTooLargeMessage})
			return
		}
		writeJSON(w, 400, map[string]any{"success": false, "error": "name required"})
		return
	}
	if body.Name == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "name required"})
		return
	}
	archivePath := ""
	if bookmarkArchivePathsAllowed(user) {
		var err error
		archivePath, err = normalizeArchivePath(body.ArchivePath)
		if err != nil {
			writeJSON(w, 422, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := ensureArchivePath(archivePath); err != nil {
			writeJSON(w, 422, map[string]any{"success": false, "error": err.Error()})
			return
		}
	}

	catID, err := s.db.CreateBookmarkCategory(body.Name, archivePath)
	if err != nil {
		slog.Error("CreateBookmarkCategory", "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	writeJSON(w, 200, map[string]any{
		"success": true,
		"category": map[string]any{
			"id":           catID,
			"category_id":  catID,
			"name":         body.Name,
			"archive_path": archivePath,
		},
	})
}

func (s *Server) handleBookmarkCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())

	categoryID, err := strconv.ParseInt(r.PathValue("categoryID"), 10, 64)
	if err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid category_id"})
		return
	}

	var body struct {
		Name        string `json:"name"`
		ArchivePath string `json:"archive_path"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		if requestBodyTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"success": false, "error": requestBodyTooLargeMessage})
			return
		}
		writeJSON(w, 400, map[string]any{"success": false, "error": "name required"})
		return
	}
	if body.Name == "" {
		writeJSON(w, 400, map[string]any{"success": false, "error": "name required"})
		return
	}
	archivePath := ""
	if bookmarkArchivePathsAllowed(user) {
		var err error
		archivePath, err = normalizeArchivePath(body.ArchivePath)
		if err != nil {
			writeJSON(w, 422, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := ensureArchivePath(archivePath); err != nil {
			writeJSON(w, 422, map[string]any{"success": false, "error": err.Error()})
			return
		}
	}

	if err := s.db.UpdateBookmarkCategory(categoryID, body.Name, archivePath); err != nil {
		slog.Error("UpdateBookmarkCategory", "id", categoryID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	writeJSON(w, 200, map[string]any{"success": true})
}

func normalizeArchivePath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", nil
	}
	if strings.ContainsRune(path, 0) {
		return "", errors.New("archive_path contains an invalid character")
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", errors.New("archive_path must be an absolute path")
	}
	return clean, nil
}

func ensureArchivePath(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create archive_path: %w", err)
	}
	return nil
}

func (s *Server) handleBookmarkCategoryDelete(w http.ResponseWriter, r *http.Request) {
	categoryID, err := strconv.ParseInt(r.PathValue("categoryID"), 10, 64)
	if err != nil {
		writeJSON(w, 400, map[string]any{"success": false, "error": "invalid category_id"})
		return
	}

	err = s.db.DeleteBookmarkCategory(categoryID)
	if err != nil {
		slog.Error("DeleteBookmarkCategory", "id", categoryID, "err", err)
		writeJSON(w, 500, map[string]any{"success": false, "error": "db error"})
		return
	}

	if r.Header.Get("HX-Request") != "" {
		s.renderBookmarkCategoriesHTML(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

// archiveBookmarkCombined archives media for a bookmarked tweet, combining parent
// and quote media into a single list that matches the JS modal's index order:
// parent slides (indices 0..M-1) + quote slides (indices M..M+Q-1).
// If mediaIndices is non-empty, only the selected indices are archived.
func (s *Server) archiveBookmarkCombined(tweetID, archivePath, customTitle, accountHandlesJSON string, mediaIndices []int) {
	if archivePath == "" {
		return
	}
	if err := ensureArchivePath(archivePath); err != nil {
		slog.Warn("[Bookmark] archive path is not writable", "path", archivePath, "err", err)
		return
	}

	// Build the combined slide list: parent first, then quote.
	// This matches the JS download-order indexing.
	var allSlides []string

	// Parent slides
	parentSlides := s.collectSlides(tweetID)
	allSlides = append(allSlides, parentSlides...)

	// Quote slides
	items, _ := s.db.GetFeedItemsForTweetIDs([]string{tweetID})
	if fi, ok := items[tweetID]; ok && fi.QuoteTweetID != "" {
		quoteSlides := s.collectSlides(fi.QuoteTweetID)
		allSlides = append(allSlides, quoteSlides...)
	}

	if len(allSlides) == 0 {
		allSlides = s.waitForBookmarkArchiveSlides(tweetID, 15*time.Second)
	}

	if len(allSlides) == 0 {
		slog.Warn("[Bookmark] no slides found", "tweet", tweetID)
		return
	}

	slog.Info("[Bookmark] combined slides", "tweet", tweetID, "total", len(allSlides), "slides", allSlides, "mediaIndices", mediaIndices)

	// Filter by media_indices if specified
	if len(mediaIndices) > 0 {
		allowed := make(map[int]bool, len(mediaIndices))
		for _, idx := range mediaIndices {
			allowed[idx] = true
		}
		var filtered []string
		for i, slide := range allSlides {
			if allowed[i] {
				filtered = append(filtered, slide)
			}
		}
		allSlides = filtered
	}

	if len(allSlides) == 0 {
		slog.Warn("[Bookmark] no slides after filtering", "tweet", tweetID, "indices", mediaIndices)
		return
	}

	// Build filename prefix
	account := buildArchiveAccount(accountHandlesJSON, "", tweetID)
	label := customTitle
	if label == "" {
		label = tweetID
	}
	safeName := sanitizeArchiveName(account + " " + label)

	// Find the highest existing numbered file for this name prefix so we
	// don't overwrite previous bookmarks saved under the same label.
	startNum := 0
	entries, _ := os.ReadDir(archivePath)
	namePrefix := safeName + " "
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, namePrefix) {
			// Extract number from "prefix NNN.ext"
			numPart := strings.TrimPrefix(n, namePrefix)
			numPart = strings.TrimSuffix(numPart, filepath.Ext(numPart))
			if num, err := strconv.Atoi(strings.TrimSpace(numPart)); err == nil && num > startNum {
				startNum = num
			}
		}
	}

	// Copy files
	for i, slidePath := range allSlides {
		ext := filepath.Ext(slidePath)
		if ext == "" {
			ext = ".jpg"
		}
		fileNum := startNum + i + 1
		destFile := filepath.Join(archivePath, fmt.Sprintf("%s %03d%s", safeName, fileNum, ext))
		src, err := os.Open(slidePath)
		if err != nil {
			slog.Warn("[Bookmark] cannot open slide", "path", slidePath, "err", err)
			continue
		}
		dst, err := os.Create(destFile)
		if err != nil {
			_ = src.Close()
			continue
		}
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
		_ = src.Close()
	}
	slog.Info("[Bookmark] archived", "tweet", tweetID, "slides", len(allSlides), "dest", archivePath)
}

func (s *Server) waitForBookmarkArchiveSlides(tweetID string, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		var slides []string
		slides = append(slides, s.collectSlides(tweetID)...)
		items, _ := s.db.GetFeedItemsForTweetIDs([]string{tweetID})
		if fi, ok := items[tweetID]; ok && fi.QuoteTweetID != "" {
			slides = append(slides, s.collectSlides(fi.QuoteTweetID)...)
		}
		if len(slides) > 0 {
			return slides
		}
	}
	return nil
}

// collectSlides gathers canonical local media paths for a tweet ID.
func (s *Server) collectSlides(tweetID string) []string {
	files := s.canonicalAssets(db.AssetOwnerRef{OwnerKind: "tweet", OwnerID: tweetID}, "post_media", "video_stream")
	slides := make([]string, 0, len(files))
	for _, file := range files {
		slides = append(slides, file.path)
	}
	return slides
}

func buildArchiveAccount(accountHandlesJSON, channelName, videoID string) string {
	if accountHandlesJSON != "" {
		handles := parseBookmarkHandleList(accountHandlesJSON)
		if len(handles) > 0 {
			return strings.Join(handles, " ")
		}
	}
	if channelName != "" {
		return channelName
	}
	return "Unknown"
}

func parseBookmarkHandleList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var handles []string
	if strings.HasPrefix(raw, "[") {
		_ = json.Unmarshal([]byte(raw), &handles)
	} else {
		handles = strings.Split(raw, ",")
	}
	out := make([]string, 0, len(handles))
	seen := map[string]bool{}
	for _, h := range handles {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

func parseBookmarkMediaIndices(raw string) []int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var values []int
	if strings.HasPrefix(raw, "[") {
		_ = json.Unmarshal([]byte(raw), &values)
	} else {
		for _, part := range strings.Split(raw, ",") {
			idx, err := strconv.Atoi(strings.TrimSpace(part))
			if err == nil {
				values = append(values, idx)
			}
		}
	}
	out := make([]int, 0, len(values))
	seen := map[int]bool{}
	for _, idx := range values {
		if idx < 0 || seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, idx)
	}
	return out
}

func sanitizeArchiveName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	s := replacer.Replace(strings.TrimSpace(name))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ── Labels (distinct custom_title values for autocomplete) ──────────────────

func (s *Server) handleBookmarkLabels(w http.ResponseWriter, r *http.Request) {
	labels, _ := s.db.GetBookmarkLabels(r.URL.Query().Get("category_id"))
	writeJSON(w, 200, map[string]any{"labels": labels})
}

func (s *Server) handleBookmarkLabelDelete(w http.ResponseWriter, r *http.Request) {
	label := r.PathValue("label")
	if label == "" {
		writeJSON(w, 400, map[string]any{"error": "label required"})
		return
	}
	if err := s.db.ClearBookmarkLabel(label); err != nil {
		slog.Error("ClearBookmarkLabel", "label", label, "err", err)
		writeJSON(w, 500, map[string]any{"error": "db error"})
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}
