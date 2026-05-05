package worker

import (
	"context"
	"log"
	"time"

	"github.com/screwys/igloo/internal/db"
)

// fetchSponsorBlockFor pulls SponsorBlock segments for videoID and persists
// them along with a sponsorblock_checked marker. Used by the youtube-enrichment
// worker and by the download-completion hook. publishedAtMs is unix-millis
// from the videos row (0 when unknown); it picks the "young"/"old" age label
// that sbShouldFetch uses to decide whether to re-check in the future.
//
// No-op when m.sponsorblockClient is nil (tests or SB disabled). Errors are
// logged and swallowed. On failure the video is still marked as checked so
// the worker doesn't immediately re-fetch it next tick.
func (m *Manager) fetchSponsorBlockFor(ctx context.Context, videoID string, publishedAtMs int64) {
	if m.sponsorblockClient == nil {
		return
	}
	ageLabel := "old"
	if publishedAtMs > 0 && time.Since(time.UnixMilli(publishedAtMs)).Hours() < 48 {
		ageLabel = "young"
	}

	raw, err := m.sponsorblockClient.Fetch(ctx, videoID)
	if err != nil {
		log.Printf("[youtube-enrich] sponsorblock fetch %s: %v", videoID, err)
		if mErr := m.db.MarkSponsorBlockChecked(videoID, ageLabel); mErr != nil {
			log.Printf("[youtube-enrich] sponsorblock mark-checked %s: %v", videoID, mErr)
		}
		return
	}

	segments := make([]db.SponsorBlockSegment, 0, len(raw))
	for _, s := range raw {
		segments = append(segments, db.SponsorBlockSegment{Start: s.Start, End: s.End, Category: s.Category})
	}
	if err := m.db.SaveSponsorBlockSegments(videoID, segments); err != nil {
		log.Printf("[youtube-enrich] sponsorblock save %s: %v", videoID, err)
	}
	if err := m.db.MarkSponsorBlockChecked(videoID, ageLabel); err != nil {
		log.Printf("[youtube-enrich] sponsorblock mark-checked %s: %v", videoID, err)
	}
}
