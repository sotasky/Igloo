package worker

import (
	"context"
	"log"
	"path/filepath"
	"time"
)

// triggerDearrowFetch runs a DeArrow check for videoID in the background.
// videoRelPath is the video's file_path (relative to DataDir); it is used
// for ffmpeg frame extraction when DeArrow returns a thumbnail timestamp.
//
// Swallowed errors are logged. On complete failure (network error, all nil)
// this still marks the video as "checked now" so the background worker
// doesn't immediately re-fetch it.
//
// Does nothing if the manager has no dearrowFetcher (test-mode) or if
// platform != "youtube".
func (m *Manager) triggerDearrowFetch(ctx context.Context, videoID, videoRelPath, platform string) {
	if m.dearrowFetcher == nil || platform != "youtube" {
		return
	}

	absPath := videoRelPath
	if absPath != "" && !filepath.IsAbs(absPath) {
		absPath = filepath.Join(m.cfg.DataDir, videoRelPath)
	}

	res, err := m.dearrowFetcher.FetchAndProcess(ctx, videoID, absPath)
	nowMs := time.Now().UnixMilli()
	if err != nil {
		log.Printf("[dearrow] fetch %s: %v", videoID, err)
		// Partial: extractor may have failed but titles may still be set.
		if res.Title == nil && res.CasualTitle == nil && res.ThumbPath == nil {
			if mErr := m.db.MarkDearrowChecked(videoID, nowMs); mErr != nil {
				log.Printf("[dearrow] mark-checked %s: %v", videoID, mErr)
			}
			return
		}
		// Fall through: persist whatever we got.
	}

	var thumbRel *string
	if res.ThumbPath != nil {
		rel, rErr := filepath.Rel(m.cfg.DataDir, *res.ThumbPath)
		if rErr == nil {
			thumbRel = &rel
		} else {
			// Path outside DataDir — store the absolute path.
			abs := *res.ThumbPath
			thumbRel = &abs
		}
	}
	if sErr := m.db.SetDearrowData(videoID, res.Title, res.CasualTitle, thumbRel, nowMs); sErr != nil {
		log.Printf("[dearrow] save %s: %v", videoID, sErr)
	}
}

// triggerYoutubeEnrichFetch runs both a DeArrow and SponsorBlock fetch for
// videoID. Called from the download-complete hook so a freshly downloaded
// YouTube video gets both kinds of data populated under a single rate limit.
// Silently no-ops for non-YouTube platforms.
func (m *Manager) triggerYoutubeEnrichFetch(ctx context.Context, videoID, videoRelPath, platform string) {
	if platform != "youtube" {
		return
	}
	m.triggerDearrowFetch(ctx, videoID, videoRelPath, platform)

	// Only fetch SB if we don't already have a record for this video. Skips
	// redundant work when the youtube-enrichment worker beat us to it.
	if existing, _ := m.db.GetSponsorBlockChecked(videoID); existing == nil {
		var publishedAtMs int64
		if v, err := m.db.GetVideo(videoID); err == nil && v != nil && v.PublishedAt != nil {
			publishedAtMs = v.PublishedAt.UnixMilli()
		}
		m.fetchSponsorBlockFor(ctx, videoID, publishedAtMs)
	}
}
