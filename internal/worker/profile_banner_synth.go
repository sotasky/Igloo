package worker

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

// shortsBannerSentinelPrefix marks a ChannelProfile.BannerURL as a
// server-synthesized banner rather than an upstream source URL. The suffix is
// the source video_id so a new video triggers bnChanged and a re-copy.
const shortsBannerSentinelPrefix = "synth:latest-video:"

// synthesizeShortsBanner picks the newest downloaded short-form item for the channel
// and copies its cover image into bnDir as the banner file. Returns the
// sentinel URL to store on the profile row (so downstream change-detection
// keys on the source video_id) and true when a banner is available.
func (m *Manager) synthesizeShortsBanner(channelID, bnDir string, existing *model.ChannelProfile) (string, bool) {
	videoID, filePath, err := m.db.LatestVideoFileForChannel(channelID)
	if err != nil {
		log.Printf("[profile] synth banner %s: latest-video lookup: %v", channelID, err)
		return "", false
	}
	if videoID == "" {
		return "", false
	}
	srcPath := resolveVideoThumbFile(m.cfg.DataDir, filePath)
	if srcPath == "" {
		return "", false
	}
	sentinel := shortsBannerSentinelPrefix + videoID
	if existing != nil && existing.BannerURL == sentinel && hasConventionalMediaFile(bnDir, channelID) {
		return sentinel, true
	}
	if err := copyBannerFromThumb(srcPath, bnDir, channelID); err != nil {
		log.Printf("[profile] synth banner %s: copy: %v", channelID, err)
		return "", false
	}
	return sentinel, true
}

func (m *Manager) refreshStoredShortsBanner(channelID, bnDir string, existing *model.ChannelProfile, current string) string {
	if current != "" && !strings.HasPrefix(current, shortsBannerSentinelPrefix) {
		return current
	}
	if sentinel, ok := m.synthesizeShortsBanner(channelID, bnDir, existing); ok {
		return sentinel
	}
	return current
}

// resolveVideoThumbFile locates the cover image on disk for a stored video
// file_path. TikTok slideshows may be saved directly as an image file, while
// normal videos typically have a sibling cover such as .image/.jpg next to the
// .mp4.
func resolveVideoThumbFile(dataDir, filePath string) string {
	if filePath == "" {
		return ""
	}
	abs := filePath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dataDir, filePath)
	}
	ext := strings.ToLower(filepath.Ext(abs))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".image":
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	base := strings.TrimSuffix(abs, filepath.Ext(abs))
	for _, e := range []string{".image", ".webp", ".jpg", ".jpeg", ".png"} {
		if candidate := base + e; fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

// copyBannerFromThumb writes the source image to bnDir/<channelID>.<ext>
// using normalizeDownloadedImage to detect the true content type and strip
// any stale banner files with mismatched extensions.
func copyBannerFromThumb(srcPath, bnDir, channelID string) error {
	tmpPath := filepath.Join(bnDir, channelID+".download")
	if err := copyFile(srcPath, tmpPath); err != nil {
		return err
	}
	if _, err := normalizeDownloadedImage(tmpPath, bnDir, channelID); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return out.Close()
}
