package web

import (
	"net/http"
	"strings"

	"github.com/screwys/igloo/internal/db"
)

type canonicalAssetFile struct {
	asset db.Asset
	path  string
}

func (s *Server) canonicalStreamAsset(owner db.AssetOwnerRef) *canonicalAssetFile {
	files := s.canonicalAssets(owner, "video_stream", "post_media")
	for i := range files {
		if files[i].asset.AssetKind == "video_stream" {
			return &files[i]
		}
	}
	for i := range files {
		contentType := files[i].asset.ContentType
		if strings.HasPrefix(contentType, "video/") || contentType == "image/gif" {
			return &files[i]
		}
	}
	return nil
}

func (s *Server) canonicalAsset(owner db.AssetOwnerRef, kind string, index int) *canonicalAssetFile {
	assets, err := s.db.ListReadyAssetsForOwners([]db.AssetOwnerRef{owner}, []string{kind})
	if err != nil {
		return nil
	}
	for _, asset := range assets {
		if asset.MediaIndex != index {
			continue
		}
		if file := s.canonicalAssetFile(asset); file != nil {
			return file
		}
	}
	return nil
}

func (s *Server) canonicalAssets(owner db.AssetOwnerRef, kinds ...string) []canonicalAssetFile {
	var out []canonicalAssetFile
	assets, err := s.db.ListReadyAssetsForOwners([]db.AssetOwnerRef{owner}, kinds)
	if err != nil {
		return nil
	}
	for _, asset := range assets {
		if file := s.canonicalAssetFile(asset); file != nil {
			out = append(out, *file)
		}
	}
	return out
}

func (s *Server) videoAssetOwner(videoID string) (db.AssetOwnerRef, bool) {
	video, err := s.db.GetVideo(strings.TrimSpace(videoID))
	if err != nil || video == nil {
		return db.AssetOwnerRef{}, false
	}
	switch video.OwnerKind {
	case "tweet", "youtube_video", "tiktok_video", "instagram_reel":
		return db.AssetOwnerRef{OwnerKind: video.OwnerKind, OwnerID: video.VideoID}, true
	default:
		return db.AssetOwnerRef{}, false
	}
}

func (s *Server) requestMediaAssetOwner(r *http.Request, id string) (db.AssetOwnerRef, bool) {
	switch r.URL.Query().Get("owner_kind") {
	case "tweet":
		return db.AssetOwnerRef{OwnerKind: "tweet", OwnerID: strings.TrimSpace(id)}, strings.TrimSpace(id) != ""
	case "":
		return s.videoAssetOwner(id)
	default:
		return db.AssetOwnerRef{}, false
	}
}

func (s *Server) canonicalAssetFile(asset db.Asset) *canonicalAssetFile {
	contentType := strings.TrimSpace(asset.ContentType)
	if asset.State != db.AssetStateReady || strings.TrimSpace(asset.FilePath) == "" ||
		contentType == "" || contentType == "application/octet-stream" {
		return nil
	}
	asset.ContentType = contentType
	path, err := s.cfg.Storage.Path(asset.FilePath)
	if err != nil {
		return nil
	}
	return &canonicalAssetFile{asset: asset, path: path}
}
