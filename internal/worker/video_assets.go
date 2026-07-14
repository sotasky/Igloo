package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/subtitlemeta"
)

type completedVideoFiles struct {
	assets               []db.Asset
	subtitleAssets       []db.Asset
	primaryPath          string
	primaryKey           string
	imageKeys            []string
	thumbnailImageSource string
	thumbnailVideoSource string
	transientPaths       []string
}

func (m *Manager) prepareCompletedVideoFiles(ctx context.Context, lane download.MediaLane, completed download.CompletedDownload) (completedVideoFiles, error) {
	if m == nil || m.downloader == nil {
		return m.prepareCompletedVideoFilesAdmitted(completed)
	}
	var files completedVideoFiles
	err := m.downloader.RunMedia(ctx, lane, func() error {
		var err error
		files, err = m.prepareCompletedVideoFilesAdmitted(completed)
		return err
	})
	return files, err
}

func (m *Manager) prepareCompletedVideoFilesAdmitted(completed download.CompletedDownload) (completedVideoFiles, error) {
	var out completedVideoFiles
	if m == nil || m.cfg == nil {
		return out, fmt.Errorf("storage is not configured")
	}

	indexes := map[string]int{}
	var firstImage, firstVideo string
	primaryPriority := 0
	for _, path := range completed.MediaPaths {
		kind, contentType, priority := completedMediaIdentity(path)
		if kind == "" {
			return out, fmt.Errorf("unsupported completed media output %s", path)
		}
		key, err := m.cfg.Storage.Key(path)
		if err != nil {
			return out, err
		}
		index := indexes[kind]
		indexes[kind]++
		out.assets = append(out.assets, db.Asset{
			AssetKind:      kind,
			MediaIndex:     index,
			FilePath:       key,
			ContentType:    contentType,
			RequiredReason: "retention",
		})
		if kind == "post_media" {
			out.imageKeys = append(out.imageKeys, key)
			if firstImage == "" {
				firstImage = path
			}
		}
		if kind == "video_stream" && firstVideo == "" {
			firstVideo = path
		}
		if priority > primaryPriority {
			primaryPriority = priority
			out.primaryPath = path
			out.primaryKey = key
		}
	}
	if out.primaryPath == "" {
		return out, fmt.Errorf("completed download returned no supported media")
	}
	info, err := os.Stat(out.primaryPath)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return out, fmt.Errorf("stat completed media %s: %w", out.primaryPath, err)
	}
	out.thumbnailImageSource = completed.ThumbnailPath
	if out.thumbnailImageSource == "" {
		out.thumbnailImageSource = firstImage
	}
	out.thumbnailVideoSource = firstVideo

	subtitlePaths := append([]string(nil), completed.SubtitlePaths...)
	sort.Strings(subtitlePaths)
	videoStem := strings.TrimSuffix(filepath.Base(out.primaryPath), filepath.Ext(out.primaryPath))
	audioLanguage := subtitlemeta.Language(completed.Metadata)
	manualLangs := subtitlemeta.ManualLangs(completed.Metadata)
	for index, path := range subtitlePaths {
		key, err := m.cfg.Storage.Key(path)
		if err != nil {
			return out, err
		}
		lang := subtitlemeta.TrackLang(videoStem, filepath.Base(path))
		isAuto := !manualLangs[lang]
		out.subtitleAssets = append(out.subtitleAssets, db.Asset{
			AssetKind:      "subtitle",
			MediaIndex:     index,
			FilePath:       key,
			ContentType:    "text/vtt",
			IsAuto:         &isAuto,
			AudioLanguage:  audioLanguage,
			RequiredReason: "retention",
		})
	}

	kept := make(map[string]struct{}, len(completed.MediaPaths)+len(completed.SubtitlePaths))
	for _, path := range completed.MediaPaths {
		kept[path] = struct{}{}
	}
	for _, path := range completed.SubtitlePaths {
		kept[path] = struct{}{}
	}
	for _, path := range []string{completed.InfoJSONPath, completed.ThumbnailPath} {
		if path == "" {
			continue
		}
		if _, ok := kept[path]; !ok {
			out.transientPaths = append(out.transientPaths, path)
		}
	}
	return out, nil
}

func (m *Manager) publishCompletedVideoThumbnail(ctx context.Context, lane download.MediaLane, videoID, platform, outputID string, files completedVideoFiles) error {
	if files.thumbnailImageSource == "" && files.thumbnailVideoSource == "" {
		return nil
	}
	var thumbnailPath string
	materialize := func() error {
		var err error
		thumbnailPath, err = m.materializeVideoThumbnail(
			ctx, platform, outputID, files.thumbnailImageSource, files.thumbnailVideoSource,
		)
		return err
	}
	if m != nil && m.downloader != nil {
		if err := m.downloader.RunMedia(ctx, lane, materialize); err != nil {
			return err
		}
	} else if err := materialize(); err != nil {
		return err
	}
	if thumbnailPath == "" {
		return nil
	}
	key, err := m.cfg.Storage.Key(thumbnailPath)
	if err == nil {
		err = m.db.StoreVideoThumbnailAsset(videoID, db.Asset{
			FilePath: key, ContentType: completedContentType(thumbnailPath),
		}, 0)
		if err == nil {
			return nil
		}
	}
	m.removeStoredPaths(ctx, lane, []string{thumbnailPath})
	return err
}

func (m *Manager) storeCompletedVideoOutputs(
	ctx context.Context,
	lane download.MediaLane,
	platform string,
	outputID string,
	video db.CompletedVideo,
	files completedVideoFiles,
	completed download.CompletedDownload,
	subtitleAsset *db.Asset,
) error {
	if err := m.db.StoreCompletedVideo(video); err != nil {
		m.removeFailedAttempt(ctx, lane, files, completed)
		return err
	}
	if subtitleAsset != nil {
		if err := m.db.DeclareAsset(*subtitleAsset, 0); err != nil {
			log.Printf("[downloadpool] DeclareSubtitleAsset %s: %v", video.VideoID, err)
		}
	}
	if err := m.publishCompletedVideoThumbnail(ctx, lane, video.VideoID, platform, outputID, files); err != nil {
		log.Printf("[downloadpool] StoreVideoThumbnailAsset %s: %v", video.VideoID, err)
	}
	if err := m.storeCompletedSubtitles(ctx, video.VideoID, files, completed); err != nil {
		log.Printf("[downloadpool] StoreVideoSubtitleAssets %s: %v", video.VideoID, err)
	}
	m.removeTransientFiles(ctx, lane, files)
	return nil
}

func completedMediaIdentity(path string) (kind, contentType string, priority int) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video_stream", "video/mp4", 3
	case ".webm":
		return "video_stream", "video/webm", 3
	case ".mkv":
		return "video_stream", "video/x-matroska", 3
	case ".mov":
		return "video_stream", "video/quicktime", 3
	case ".m4v":
		return "video_stream", "video/x-m4v", 3
	case ".jpg", ".jpeg", ".image":
		return "post_media", "image/jpeg", 2
	case ".png":
		return "post_media", "image/png", 2
	case ".webp":
		return "post_media", "image/webp", 2
	case ".gif":
		return "post_media", "image/gif", 2
	case ".mp3":
		return "post_audio", "audio/mpeg", 1
	case ".m4a":
		return "post_audio", "audio/mp4", 1
	case ".aac":
		return "post_audio", "audio/aac", 1
	case ".ogg":
		return "post_audio", "audio/ogg", 1
	case ".wav":
		return "post_audio", "audio/wav", 1
	default:
		return "", "", 0
	}
}

func completedContentType(path string) string {
	_, contentType, _ := completedMediaIdentity(path)
	return contentType
}

func (m *Manager) materializeVideoThumbnail(ctx context.Context, platform, outputID, imageSource, videoSource string) (string, error) {
	platform, err := safeVideoFileName(platform)
	if err != nil {
		return "", err
	}
	outputID, err = safeVideoFileName(outputID)
	if err != nil {
		return "", err
	}
	outDir, err := m.cfg.Storage.WritePath(filepath.ToSlash(filepath.Join("thumbnails", "videos", platform)))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	if imageSource != "" {
		ext := canonicalImageExtension(imageSource)
		if ext == "" {
			return "", fmt.Errorf("unsupported thumbnail output %s", imageSource)
		}
		dest := filepath.Join(outDir, outputID+ext)
		if err := copyExactFile(imageSource, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	if videoSource == "" {
		return "", nil
	}
	tmp, err := os.CreateTemp(outDir, "."+outputID+"-*.jpg")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	if err := extractRepresentativeFrame(ctx, videoSource, tmpPath); err != nil {
		return "", fmt.Errorf("extract thumbnail for %s: %w", outputID, err)
	}
	dest := filepath.Join(outDir, outputID+".jpg")
	if err := os.Rename(tmpPath, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func safeVideoFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || filepath.Base(name) != name {
		return "", fmt.Errorf("unsafe video file name %q", name)
	}
	return name, nil
}

func newDownloadAttemptID(videoID string) (string, error) {
	videoID, err := safeVideoFileName(videoID)
	if err != nil {
		return "", err
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return videoID + "-attempt-" + hex.EncodeToString(suffix[:]), nil
}

func canonicalImageExtension(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".image":
		return ".jpg"
	case ".png":
		return ".png"
	case ".webp":
		return ".webp"
	case ".gif":
		return ".gif"
	default:
		return ""
	}
}

func copyExactFile(source, destination string) error {
	if filepath.Clean(source) == filepath.Clean(destination) {
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("thumbnail source is not a regular file: %s", source)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".thumbnail-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return err
	}
	ok = true
	return nil
}

func (m *Manager) removeTransientFiles(ctx context.Context, bulkLane download.MediaLane, files completedVideoFiles) {
	m.removeStoredPaths(ctx, bulkLane, files.transientPaths)
}

func (m *Manager) removeMediaPaths(ctx context.Context, lane download.MediaLane, paths ...string) {
	if len(paths) == 0 {
		return
	}
	remove := func() error { removeExactPaths(paths); return nil }
	if m == nil || m.downloader == nil {
		_ = remove()
		return
	}
	_ = m.downloader.RunMedia(ctx, lane, remove)
}

func (m *Manager) removeFailedAttempt(ctx context.Context, bulkLane download.MediaLane, files completedVideoFiles, completed download.CompletedDownload) {
	paths := append([]string(nil), completed.MediaPaths...)
	paths = append(paths, completed.SubtitlePaths...)
	paths = append(paths, completed.ThumbnailPath, completed.InfoJSONPath)
	m.removeStoredPaths(ctx, bulkLane, paths)
}

func (m *Manager) removeStoredPaths(ctx context.Context, bulkLane download.MediaLane, paths []string) {
	var statePaths, bulkPaths []string
	for _, path := range paths {
		key, err := m.cfg.Storage.Key(path)
		if err == nil && strings.HasPrefix(key, "media/") {
			bulkPaths = append(bulkPaths, path)
		} else {
			statePaths = append(statePaths, path)
		}
	}
	m.removeMediaPaths(ctx, bulkLane, bulkPaths...)
	m.removeMediaPaths(ctx, download.MediaLaneState, statePaths...)
}

func (m *Manager) storeCompletedSubtitles(ctx context.Context, videoID string, files completedVideoFiles, completed download.CompletedDownload) error {
	if len(completed.SubtitlePaths) == 0 {
		return nil
	}
	if len(files.subtitleAssets) == 0 {
		m.removeMediaPaths(ctx, download.MediaLaneState, completed.SubtitlePaths...)
		return fmt.Errorf("subtitle producer returned no publishable outputs")
	}
	if err := m.db.StoreVideoSubtitleAssets(videoID, files.subtitleAssets, 0); err != nil {
		m.removeMediaPaths(ctx, download.MediaLaneState, completed.SubtitlePaths...)
		return err
	}
	return nil
}

func removeExactPaths(paths []string) {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		_ = os.Remove(path)
	}
}
