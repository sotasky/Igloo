package web

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

// Profile media is read-only at request time. A miss is a pipeline fact, not
// a signal to fetch, scan directories, or mutate inventory.
func (s *Server) handleChannelAvatar(w http.ResponseWriter, r *http.Request) {
	s.serveProfileAsset(w, r, canonicalProfileChannelID(r.PathValue("channelID")), "avatar")
}

func (s *Server) handleChannelBanner(w http.ResponseWriter, r *http.Request) {
	s.serveProfileAsset(w, r, canonicalProfileChannelID(r.PathValue("channelID")), "banner")
}

func (s *Server) handleCommentAuthorAvatar(w http.ResponseWriter, r *http.Request) {
	ownerID := strings.TrimSpace(r.PathValue("ownerID"))
	file := s.canonicalAsset(db.AssetOwnerRef{OwnerKind: "comment_author", OwnerID: ownerID}, "avatar", 0)
	if file == nil || !strings.HasPrefix(file.asset.ContentType, "image/") {
		http.NotFound(w, r)
		return
	}
	cacheControl := "public, no-cache"
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", file.asset.ContentType)
	if s.serveDataFileViaXAccel(w, r, file.path, file.asset.ContentType, cacheControl) {
		return
	}
	http.ServeFile(w, r, file.path)
}

func (s *Server) projectCommentAuthorAvatars(comments []model.Comment) {
	owners := make([]db.AssetOwnerRef, 0, len(comments))
	for i := range comments {
		comments[i].AuthorThumbnail = ""
		ownerID := model.YouTubeCommentAuthorChannelID(comments[i].AuthorID)
		if ownerID != "" {
			owners = append(owners, db.AssetOwnerRef{OwnerKind: "comment_author", OwnerID: ownerID})
		}
	}
	assets, err := s.db.ListReadyAssetsForOwners(owners, []string{"avatar"})
	if err != nil {
		return
	}
	ready := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		if asset.OwnerKind == "comment_author" && strings.HasPrefix(asset.ContentType, "image/") {
			ready[asset.OwnerID] = struct{}{}
		}
	}
	for i := range comments {
		ownerID := model.YouTubeCommentAuthorChannelID(comments[i].AuthorID)
		if _, ok := ready[ownerID]; ok {
			comments[i].AuthorThumbnail = "/api/media/comment-avatar/" + url.PathEscape(ownerID)
		}
	}
}

func (s *Server) serveProfileAsset(w http.ResponseWriter, r *http.Request, channelID, kind string) {
	path, contentType := s.resolveProfileAsset(channelID, kind)
	if path == "" {
		http.NotFound(w, r)
		return
	}
	cacheControl := "public, no-cache"
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", contentType)
	if s.serveDataFileViaXAccel(w, r, path, contentType, cacheControl) {
		return
	}
	http.ServeFile(w, r, path)
}

func (s *Server) resolveAvatarPath(channelID string) string {
	path, _ := s.resolveProfileAsset(canonicalProfileChannelID(channelID), "avatar")
	return path
}

func (s *Server) resolveBannerPath(channelID string) string {
	path, _ := s.resolveProfileAsset(canonicalProfileChannelID(channelID), "banner")
	return path
}

func (s *Server) resolveProfileAsset(channelID, kind string) (string, string) {
	if channelID == "" || (kind != "avatar" && kind != "banner") {
		return "", ""
	}
	file := s.canonicalAsset(db.AssetOwnerRef{OwnerKind: "channel", OwnerID: channelID}, kind, 0)
	if file == nil {
		return "", ""
	}
	contentType := strings.TrimSpace(file.asset.ContentType)
	if !strings.HasPrefix(contentType, "image/") {
		return "", ""
	}
	return file.path, contentType
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	videoID := strings.TrimSpace(r.PathValue("videoID"))
	if videoID == "" {
		http.NotFound(w, r)
		return
	}
	owner, ok := s.requestMediaAssetOwner(r, videoID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var file *canonicalAssetFile
	if r.URL.Query().Get("da") == "1" {
		file = s.canonicalAsset(owner, "dearrow_thumbnail", 0)
	}
	if file == nil {
		file = s.canonicalAsset(owner, "post_thumbnail", 0)
	}
	if file == nil {
		candidate := s.canonicalAsset(owner, "post_media", 0)
		if candidate != nil && strings.HasPrefix(candidate.asset.ContentType, "image/") {
			file = candidate
		}
	}
	if file == nil {
		http.NotFound(w, r)
		return
	}
	s.serveThumbFile(w, r, *file)
}

func (s *Server) serveThumbFile(w http.ResponseWriter, r *http.Request, file canonicalAssetFile) {
	cacheControl := "public, max-age=86400"
	contentType := strings.TrimSpace(file.asset.ContentType)
	if !strings.HasPrefix(contentType, "image/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", contentType)
	if s.serveDataFileViaXAccel(w, r, file.path, contentType, cacheControl) {
		return
	}
	http.ServeFile(w, r, file.path)
}

func (s *Server) handleDownloadVideo(w http.ResponseWriter, r *http.Request) {
	videoID := strings.TrimSpace(r.PathValue("videoID"))
	owner, ok := s.videoAssetOwner(videoID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	file := s.canonicalAsset(owner, "video_stream", 0)
	if file == nil {
		http.NotFound(w, r)
		return
	}
	title := "video_" + videoID
	if video, err := s.db.GetVideo(videoID); err == nil && video != nil && strings.TrimSpace(video.Title) != "" {
		title = video.Title
	}
	filename := sanitizeFilename(title) + filepath.Ext(file.path)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if s.serveDataFileViaXAccel(w, r, file.path, file.asset.ContentType, "") {
		return
	}
	http.ServeFile(w, r, file.path)
}

func sanitizeFilename(name string) string {
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
