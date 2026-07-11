package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
)

const (
	feedMediaBatchSize     = 25
	feedMediaLeaseDuration = 5 * time.Minute
	maxRetries404          = 10
)

func (m *Manager) runFeedMediaWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	log.Printf("[feedmedia] worker started")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !m.IsStopRequested() {
				m.processFeedMediaBatch(ctx)
			}
		case <-m.feedMediaKick:
			if !m.IsStopRequested() {
				m.processFeedMediaBatch(ctx)
			}
		}
	}
}

func (m *Manager) processFeedMediaBatch(ctx context.Context) {
	if m == nil || m.db == nil || m.cfg == nil || m.downloader == nil || ctx.Err() != nil {
		return
	}
	owner := feedMediaLeaseOwner()
	for {
		assets, err := m.db.ClaimContentAssetDownloadBatch(db.LeaseOptions{
			Owner: owner, LeaseMs: feedMediaLeaseDuration.Milliseconds(), Limit: feedMediaBatchSize,
		})
		if err != nil {
			log.Printf("[feedmedia] ClaimContentAssetDownloadBatch: %v", err)
			return
		}
		if len(assets) == 0 {
			return
		}
		for _, asset := range assets {
			if ctx.Err() != nil {
				m.releaseContentAsset(asset)
				continue
			}
			m.processContentAsset(ctx, asset)
		}
		if len(assets) < feedMediaBatchSize {
			return
		}
	}
}

func (m *Manager) processContentAsset(ctx context.Context, asset db.Asset) {
	stopRenew := m.startContentAssetLeaseRenewal(ctx, asset)
	if stopRenew != nil {
		defer stopRenew()
	}
	if asset.OwnerKind == "tweet" && m.cfg != nil && !m.cfg.PlatformEnabled("twitter") {
		m.failContentAsset(asset, fmt.Errorf("platform disabled: twitter"))
		return
	}
	oldPath := asset.FilePath
	finalPath, contentType, err := m.downloadContentAsset(ctx, asset)
	if err != nil {
		m.failContentAsset(asset, err)
		return
	}
	key, err := m.cfg.Storage.Key(finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		m.failContentAsset(asset, err)
		return
	}
	asset.FilePath = key
	asset.ContentType = contentType
	if err := m.db.CompleteAssetDownload(asset, asset.LeaseOwner, time.Now().UnixMilli()); err != nil {
		_ = os.Remove(finalPath)
		if !errors.Is(err, db.ErrQueueLeaseNotHeld) {
			log.Printf("[feedmedia] CompleteAssetDownload %s: %v", asset.AssetID, err)
		}
		return
	}
	if oldPath != "" && oldPath != key {
		if _, err := m.db.RemoveAssetFileIfUnreferenced(oldPath); err != nil {
			log.Printf("[feedmedia] remove replaced file %s: %v", oldPath, err)
		}
	}
	m.EmitFeed("feed_media", fmt.Sprintf("Downloaded %s for %s", asset.AssetKind, asset.OwnerID), "done")
}

func (m *Manager) downloadContentAsset(ctx context.Context, asset db.Asset) (string, string, error) {
	ownerKey, err := safeProfileMediaKey(asset.OwnerID)
	if err != nil {
		return "", "", err
	}
	unique := fmt.Sprintf("%s_%s_%d_%d", ownerKey, asset.AssetKind, asset.MediaIndex, time.Now().UnixNano())
	switch asset.AssetKind {
	case "avatar", "post_thumbnail":
		dirName := "generated"
		if asset.AssetKind == "avatar" {
			dirName = "avatars"
		}
		dir, err := m.cfg.Storage.WritePath(filepath.Join("thumbnails", dirName))
		if err != nil {
			return "", "", err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		sourceURL := asset.SourceURL
		if asset.AssetKind == "avatar" && asset.OwnerKind == "tweet" {
			sourceURL = upgradeTwimgURL(sourceURL)
		}
		tmpPath, err := m.downloader.HTTP.DownloadFile(ctx, sourceURL, dir, unique+".download")
		if err != nil && sourceURL != asset.SourceURL {
			tmpPath, err = m.downloader.HTTP.DownloadFile(ctx, asset.SourceURL, dir, unique+".download")
		}
		if err != nil {
			return "", "", err
		}
		path, err := normalizeDownloadedImage(tmpPath, dir, unique)
		if err != nil {
			return "", "", err
		}
		contentType, err := sniffImageContentType(path)
		return path, contentType, err
	case "post_audio", "post_media":
		dir, err := m.cfg.Storage.WritePath(filepath.Join("media", "twitter", ownerKey))
		if err != nil {
			return "", "", err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
		mediaType := "photo"
		if asset.AssetKind == "post_audio" || strings.HasPrefix(asset.ContentType, "audio/") {
			mediaType = "audio"
		} else if strings.HasPrefix(asset.ContentType, "video/") {
			mediaType = "video"
		}
		opts := download.Opts{OutputDir: dir, ID: unique}
		paths, err := m.downloader.Download(ctx, asset.SourceURL, mediaType, opts)
		if err != nil {
			return "", "", err
		}
		if len(paths) != 1 {
			for _, path := range paths {
				_ = os.Remove(path)
			}
			return "", "", fmt.Errorf("source produced %d files for one canonical asset", len(paths))
		}
		contentType := strings.TrimSpace(asset.ContentType)
		if detected := mime.TypeByExtension(strings.ToLower(filepath.Ext(paths[0]))); detected != "" {
			contentType = detected
		}
		return paths[0], contentType, nil
	default:
		return "", "", fmt.Errorf("unsupported content asset kind: %s", asset.AssetKind)
	}
}

func (m *Manager) failContentAsset(asset db.Asset, cause error) {
	if errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		m.releaseContentAsset(asset)
		return
	}
	attempt := asset.Attempts + 1
	classification := download.ClassifyFailure(cause, nil, attempt)
	nowMs := time.Now().UnixMilli()
	if (classification.Kind == download.ErrorKindNotFound && attempt > maxRetries404) || classification.Permanent {
		if err := m.db.MarkContentAssetPermanentMissing(
			asset.AssetID, asset.AssetKind, asset.LeaseOwner, classification.Kind, cause.Error(), nowMs,
		); err != nil {
			log.Printf("[feedmedia] mark permanent %s: %v", asset.AssetID, err)
		}
		return
	}
	if err := m.db.RetryAssetDownload(
		asset.AssetID, asset.AssetKind, asset.LeaseOwner,
		classification.Kind, cause.Error(), classification.RetryDelay, nowMs,
	); err != nil {
		log.Printf("[feedmedia] retry %s: %v", asset.AssetID, err)
	}
}

func (m *Manager) releaseContentAsset(asset db.Asset) {
	if err := m.db.ReleaseAssetDownload(asset.AssetID, asset.AssetKind, asset.LeaseOwner, time.Now().UnixMilli()); err != nil &&
		!errors.Is(err, db.ErrQueueLeaseNotHeld) {
		log.Printf("[feedmedia] release %s: %v", asset.AssetID, err)
	}
}

func (m *Manager) startContentAssetLeaseRenewal(ctx context.Context, asset db.Asset) func() {
	if asset.AssetID == "" || asset.AssetKind == "" || asset.LeaseOwner == "" {
		return nil
	}
	renewCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(feedMediaLeaseDuration / 2)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				if err := m.db.RenewAssetDownloadLease(
					asset.AssetID, asset.AssetKind, asset.LeaseOwner,
					time.Now().UnixMilli(), feedMediaLeaseDuration,
				); err != nil {
					log.Printf("[feedmedia] renew %s: %v", asset.AssetID, err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func feedMediaLeaseOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("feedmedia:%s:%d", host, os.Getpid())
}
