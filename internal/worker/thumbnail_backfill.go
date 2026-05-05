package worker

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// backfillThumbnails generates thumbnails next to downloaded videos that have
// no thumbnail on disk. The sibling file is the canonical path persisted in
// videos.thumbnail_path and synced to Android.
func (m *Manager) backfillThumbnails(ctx context.Context) {
	rows, err := m.db.QueryVideoThumbnails()
	if err != nil {
		log.Printf("[thumbnail_backfill] query: %v", err)
		return
	}

	var generated, skipped int
	for _, r := range rows {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if r.FilePath == "" {
			continue
		}

		if r.ThumbnailPath != "" {
			continue
		}

		// Resolve absolute path.
		absPath := r.FilePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(m.cfg.DataDir, absPath)
		}

		// Only process video files.
		ext := strings.ToLower(filepath.Ext(absPath))
		if ext != ".mp4" && ext != ".webm" && ext != ".mkv" && ext != ".mov" {
			continue
		}

		if !fileExists(absPath) {
			continue
		}

		// If yt-dlp/gallery-dl already wrote a sibling thumbnail, persist it as
		// the first-class thumbnail path so Android can sync it.
		if siblingPath := findSiblingThumbnail(absPath, r.VideoID); siblingPath != "" {
			if err := m.db.UpdateVideoThumbnailPath(r.VideoID, toRelPath(m.cfg.DataDir, siblingPath)); err != nil {
				skipped++
				log.Printf("[thumbnail_backfill] update sibling %s: %v", r.VideoID, err)
				continue
			}
			continue
		}

		thumbPath := filepath.Join(filepath.Dir(absPath), r.VideoID+".jpg")
		if err := extractFirstFrame(ctx, absPath, thumbPath); err != nil {
			skipped++
			if skipped <= 3 {
				log.Printf("[thumbnail_backfill] ffmpeg %s: %v", r.VideoID, err)
			}
			continue
		}
		if err := m.db.UpdateVideoThumbnailPath(r.VideoID, toRelPath(m.cfg.DataDir, thumbPath)); err != nil {
			skipped++
			log.Printf("[thumbnail_backfill] update %s: %v", r.VideoID, err)
			_ = os.Remove(thumbPath)
			continue
		}
		generated++
	}

	if generated > 0 || skipped > 0 {
		log.Printf("[thumbnail_backfill] done: generated %d, skipped %d", generated, skipped)
	}
}

// extractFirstFrame extracts the first video frame as a JPEG thumbnail.
func extractFirstFrame(ctx context.Context, videoPath, outPath string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", videoPath,
		"-frames:v", "1",
		"-vf", "scale='min(480,iw)':-2",
		"-q:v", "5",
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Clean up partial file.
		os.Remove(outPath)
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
