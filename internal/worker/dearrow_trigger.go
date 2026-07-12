package worker

import (
	"context"
	"log"
	"time"

	"github.com/screwys/igloo/internal/download"
)

// triggerDearrowFetch runs a DeArrow check for videoID in the background.
// videoRelPath is the video's logical storage key; it is used
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

	absPath := ""
	if videoRelPath != "" {
		var pathErr error
		absPath, pathErr = m.cfg.Storage.Path(videoRelPath)
		if pathErr != nil {
			log.Printf("[dearrow] storage path %s: %v", videoID, pathErr)
			return
		}
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
		if saveErr := m.db.SetDearrowTitles(videoID, res.Title, res.CasualTitle, nowMs); saveErr != nil {
			log.Printf("[dearrow] save partial %s: %v", videoID, saveErr)
		}
		return
	}

	var thumbRel *string
	if res.ThumbPath != nil {
		rel, rErr := m.cfg.Storage.Key(*res.ThumbPath)
		if rErr == nil {
			thumbRel = &rel
		} else {
			log.Printf("[dearrow] reject thumbnail path %s: %v", videoID, rErr)
			m.removeMediaPaths(ctx, download.MediaLaneBulk, *res.ThumbPath)
			if saveErr := m.db.SetDearrowTitles(videoID, res.Title, res.CasualTitle, nowMs); saveErr != nil {
				log.Printf("[dearrow] save partial %s: %v", videoID, saveErr)
			}
			return
		}
	}
	if sErr := m.db.SetDearrowData(videoID, res.Title, res.CasualTitle, thumbRel, nowMs); sErr != nil {
		log.Printf("[dearrow] save %s: %v", videoID, sErr)
		if res.ThumbPath != nil {
			m.removeMediaPaths(ctx, download.MediaLaneBulk, *res.ThumbPath)
		}
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

	// Only fetch SB if we don't already have a record for this video.
	if existing, _ := m.db.GetSponsorBlockChecked(videoID); existing == nil {
		var publishedAtMs int64
		if v, err := m.db.GetVideo(videoID); err == nil && v != nil && v.PublishedAt != nil {
			publishedAtMs = v.PublishedAt.UnixMilli()
		}
		m.fetchSponsorBlockFor(ctx, videoID, publishedAtMs)
	}
}
