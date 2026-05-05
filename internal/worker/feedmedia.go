package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

const (
	feedMediaBatchSize = 10
	// maxRetries404 is the retry cap for jobs where every media URL returns 404.
	// After quality fallback is exhausted, a 404 means the content is deleted
	// from the CDN and won't come back.
	maxRetries404 = 10
)

// runFeedMediaWorker processes feed media download jobs.
// It wakes on a 30s ticker or when kicked via feedMediaKick.
func (m *Manager) runFeedMediaWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Printf("[feedmedia] worker started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.IsStopRequested() {
				continue
			}
			m.processFeedMediaBatch(ctx)
		case <-m.feedMediaKick:
			if m.IsStopRequested() {
				continue
			}
			m.processFeedMediaBatch(ctx)
		}
	}
}

// processFeedMediaBatch claims up to feedMediaBatchSize queued jobs and
// processes each one sequentially.
func (m *Manager) processFeedMediaBatch(ctx context.Context) {
	jobs, err := m.db.ClaimFeedMediaBatch(feedMediaBatchSize)
	if err != nil {
		log.Printf("[feedmedia] ClaimFeedMediaBatch: %v", err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	log.Printf("[feedmedia] processing %d jobs", len(jobs))

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		platform := "twitter"
		handle := job.SourceHandle
		if strings.HasPrefix(handle, "tiktok_") {
			platform = "tiktok"
			handle = strings.TrimPrefix(handle, "tiktok_")
		} else {
			handle = strings.TrimPrefix(handle, "twitter_")
		}
		if m.cfg != nil && !m.cfg.PlatformEnabled(platform) {
			reason := fmt.Sprintf("platform disabled: %s", platform)
			log.Printf("[feedmedia] skip %s: %s", job.TweetID, reason)
			if err := m.db.UpdateFeedMediaJobStatus(job.TweetID, "failed", reason, job.RetryCount); err != nil {
				log.Printf("[feedmedia] UpdateFeedMediaJobStatus %s: %v", job.TweetID, err)
			}
			continue
		}
		jobDir := filepath.Join(m.cfg.DataDir, "media", platform, handle)
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			log.Printf("[feedmedia] mkdir %s: %v", jobDir, err)
			m.failJob(job, fmt.Sprintf("mkdir: %v", err))
			continue
		}
		m.processOneMediaJob(ctx, job, jobDir)
	}
}

// processOneMediaJob downloads all media for a single job.
func (m *Manager) processOneMediaJob(ctx context.Context, job db.FeedMediaJobRow, feedMediaDir string) {
	start := time.Now()
	m.EmitFeed("feed_media", fmt.Sprintf("Processing media for %s", job.TweetID), "info")

	// Look up the parent feed item for canonical URL and media refs.
	items, err := m.db.GetFeedItemsForTweetIDs([]string{job.TweetID})
	if err != nil {
		log.Printf("[feedmedia] GetFeedItemsForTweetIDs %s: %v", job.TweetID, err)
		m.failJob(job, fmt.Sprintf("db lookup: %v", err))
		return
	}

	feedItem, ok := items[job.TweetID]
	if !ok {
		// Feed item was deleted — this is an orphaned job. Prune immediately.
		log.Printf("[feedmedia] feed item not found, pruning job: %s", job.TweetID)
		if err := m.db.UpdateFeedMediaJobStatus(job.TweetID, "pruned", "feed item not found", job.RetryCount); err != nil {
			log.Printf("[feedmedia] UpdateFeedMediaJobStatus %s: %v", job.TweetID, err)
		}
		return
	}

	feedItem.ParseMedia()

	if len(feedItem.Media) == 0 && len(feedItem.QuoteMedia) == 0 {
		// Nothing to download — mark completed.
		if err := m.db.UpdateFeedMediaJobStatus(job.TweetID, "completed", "", 0); err != nil {
			log.Printf("[feedmedia] UpdateFeedMediaJobStatus %s: %v", job.TweetID, err)
		}
		return
	}

	// Skip items where media_json contains local proxy URLs (/api/slide/) instead
	// of CDN URLs — original URLs were lost, nothing to download.
	if allLocalProxyURLs(feedItem.Media) && allLocalProxyURLs(feedItem.QuoteMedia) {
		log.Printf("[feedmedia] skipping %s: only local proxy URLs", job.TweetID)
		if err := m.db.UpdateFeedMediaJobStatus(job.TweetID, "pruned", "local proxy URLs only", 0); err != nil {
			log.Printf("[feedmedia] UpdateFeedMediaJobStatus %s: %v", job.TweetID, err)
		}
		return
	}

	var mediaFiles []model.MediaFile
	var lastErr error
	allErrorsPermanent := true // tracks whether every failure was a 404

	for idx, ref := range feedItem.Media {
		select {
		case <-ctx.Done():
			return
		default:
		}

		opts := download.Opts{
			OutputDir: feedMediaDir,
			ID:        fmt.Sprintf("%s_%d", job.TweetID, idx),
		}
		if download.IsTikTokURL(ref.URL) {
			opts.Cookies, opts.CookiesFromBrowser = m.cookiesFor("tiktok")
		}
		paths, dlErr := m.downloader.Download(ctx, ref.URL, ref.Type, opts)

		if dlErr != nil {
			log.Printf("[feedmedia] download %s[%d]: %v", job.TweetID, idx, dlErr)
			lastErr = dlErr
			if !isHTTPPermanent(dlErr) {
				allErrorsPermanent = false
			}
			continue
		}

		for i, p := range paths {
			relPath := toRelPath(m.cfg.DataDir, p)
			mediaIdx := idx
			if len(paths) > 1 {
				mediaIdx = i
			}
			mediaFiles = append(mediaFiles, model.MediaFile{
				OwnerType:  "feed_media",
				OwnerID:    job.TweetID,
				MediaIndex: mediaIdx,
				FilePath:   relPath,
				MediaType:  ref.Type,
				SourceURL:  ref.URL,
			})
		}
	}

	// Download quote media if present.
	if len(feedItem.QuoteMedia) > 0 && feedItem.QuoteTweetID != "" {
		quoteHandle := feedItem.QuoteAuthorHandle
		if quoteHandle == "" {
			quoteHandle = strings.TrimPrefix(job.SourceHandle, "twitter_")
		}
		quoteMediaDir := filepath.Join(m.cfg.DataDir, "media", "twitter", quoteHandle)
		if mkErr := os.MkdirAll(quoteMediaDir, 0o755); mkErr != nil {
			log.Printf("[feedmedia] mkdir quote %s: %v", quoteMediaDir, mkErr)
			lastErr = mkErr
			allErrorsPermanent = false
		} else {
			for idx, ref := range feedItem.QuoteMedia {
				select {
				case <-ctx.Done():
					return
				default:
				}

				opts := download.Opts{
					OutputDir: quoteMediaDir,
					ID:        fmt.Sprintf("%s_%d", feedItem.QuoteTweetID, idx),
				}
				if download.IsTikTokURL(ref.URL) {
					opts.Cookies, opts.CookiesFromBrowser = m.cookiesFor("tiktok")
				}
				paths, dlErr := m.downloader.Download(ctx, ref.URL, ref.Type, opts)

				if dlErr != nil {
					log.Printf("[feedmedia] download quote %s[%d]: %v", feedItem.QuoteTweetID, idx, dlErr)
					lastErr = dlErr
					if !isHTTPPermanent(dlErr) {
						allErrorsPermanent = false
					}
					continue
				}

				for i, p := range paths {
					relPath := toRelPath(m.cfg.DataDir, p)
					mediaIdx := idx
					if len(paths) > 1 {
						mediaIdx = i
					}
					mediaFiles = append(mediaFiles, model.MediaFile{
						OwnerType:  "quote_media",
						OwnerID:    feedItem.QuoteTweetID,
						MediaIndex: mediaIdx,
						FilePath:   relPath,
						MediaType:  ref.Type,
						SourceURL:  ref.URL,
					})
				}
			}
		}
	}

	// Insert media file records.
	if len(mediaFiles) > 0 {
		if err := m.db.InsertMediaFileBatch(mediaFiles); err != nil {
			log.Printf("[feedmedia] InsertMediaFileBatch %s: %v", job.TweetID, err)
		}
	}

	elapsed := time.Since(start).Round(time.Millisecond)

	if lastErr != nil {
		newRetry := job.RetryCount + 1

		// If every failed URL returned 404 (after quality fallback) and we've
		// exceeded maxRetries404, the media is permanently deleted from the CDN.
		if allErrorsPermanent && newRetry > maxRetries404 {
			log.Printf("[feedmedia] job %s pruned after %d retries (all 404): %v", job.TweetID, newRetry, lastErr)
			m.EmitFeed("feed_media", fmt.Sprintf("Media pruned for %s (deleted from CDN)", job.TweetID), "warn")
			if err := m.db.UpdateFeedMediaJobStatus(job.TweetID, "pruned", lastErr.Error(), newRetry); err != nil {
				log.Printf("[feedmedia] UpdateFeedMediaJobStatus %s: %v", job.TweetID, err)
			}
			return
		}

		log.Printf("[feedmedia] job %s queued for retry %d: %v", job.TweetID, newRetry, lastErr)
		m.EmitFeed("feed_media", fmt.Sprintf("Media failed for %s: %v", job.TweetID, lastErr), "error")
		if err := m.db.UpdateFeedMediaJobStatus(job.TweetID, "queued", lastErr.Error(), newRetry); err != nil {
			log.Printf("[feedmedia] UpdateFeedMediaJobStatus %s: %v", job.TweetID, err)
		}
		return
	}

	log.Printf("[feedmedia] completed job %s (%d files, %s)", job.TweetID, len(mediaFiles), elapsed)
	m.EmitFeed("feed_media", fmt.Sprintf("Downloaded media for %s (%s)", job.TweetID, job.MediaKind), "done")
	if err := m.db.UpdateFeedMediaJobStatus(job.TweetID, "completed", "", job.RetryCount); err != nil {
		log.Printf("[feedmedia] UpdateFeedMediaJobStatus %s: %v", job.TweetID, err)
	}
	_ = m.db.RecordSyncChange("media_ready", job.TweetID,
		fmt.Sprintf(`{"tweet_id":"%s","kind":"%s"}`, job.TweetID, job.MediaKind))
}

// failJob keeps a job queued with an incremented retry count so the backoff
// filter in ClaimFeedMediaBatch will delay the next attempt. Failures are
// always considered transient.
func (m *Manager) failJob(job db.FeedMediaJobRow, reason string) {
	newRetry := job.RetryCount + 1
	log.Printf("[feedmedia] failJob %s queued for retry %d: %s", job.TweetID, newRetry, reason)
	if err := m.db.UpdateFeedMediaJobStatus(job.TweetID, "queued", reason, newRetry); err != nil {
		log.Printf("[feedmedia] failJob UpdateFeedMediaJobStatus %s: %v", job.TweetID, err)
	}
}

// allLocalProxyURLs returns true if every ref has a relative /api/ URL
// (lost CDN URLs replaced by local proxy during Android sync).
func allLocalProxyURLs(refs []model.MediaRef) bool {
	for _, r := range refs {
		if !strings.HasPrefix(r.URL, "/api/") {
			return false
		}
	}
	return true
}

// isHTTPPermanent checks if an error is an HTTP 403 or 404 status error
// (content deleted or access permanently denied).
func isHTTPPermanent(err error) bool {
	var httpErr *download.HTTPStatusError
	return errors.As(err, &httpErr) && (httpErr.StatusCode == 404 || httpErr.StatusCode == 403)
}

// toRelPath converts an absolute path to a path relative to baseDir.
// Falls back to the original path if it is not under baseDir.
func toRelPath(baseDir, absPath string) string {
	if !strings.HasPrefix(absPath, baseDir) {
		return absPath
	}
	rel := absPath[len(baseDir):]
	return strings.TrimPrefix(rel, string(filepath.Separator))
}
