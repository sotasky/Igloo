package worker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/fetchprofile"
	"github.com/screwys/igloo/internal/model"
)

const (
	profileJobBatchSize      = 4
	profileJobInitialBackoff = time.Minute
	profileJobMaxBackoff     = 6 * time.Hour
	profileJobMaxAttempts    = 8
)

// fetchFn is fetchprofile.Fetch, overridable by focused worker tests.
type fetchFn func(ctx context.Context, channelID string) (*fetchprofile.Profile, error)

type instagramProfileFetchFn func(ctx context.Context, channelID, handle string) (*model.ChannelProfile, error)

// runProfileJobLoop is the sole normal profile-recovery path. The request and
// retry state lives in profile_jobs; the channel is only a coalescing wake-up
// signal. Each claimed revision fetches metadata and replacement identity
// media before publishing the successful parts of that same durable revision.
func (m *Manager) runProfileJobLoop(ctx context.Context) {
	log.Printf("[profile] durable identity worker started")
	for {
		if delay := m.externalRetryDelay(time.Now()); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-m.profileKick:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
			}
			continue
		}
		profileWork := m.processProfileJobBatch(ctx, fetchprofile.Fetch)
		if ctx.Err() != nil {
			return
		}
		if profileWork {
			continue
		}
		delay, err := m.db.NextProfileJobDelay(time.Now().UnixMilli())
		if err != nil {
			log.Printf("[profile] next due: %v", err)
			delay = time.Minute
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.profileKick:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

func (m *Manager) processProfileJobBatch(ctx context.Context, fetch fetchFn) bool {
	if m == nil || m.db == nil || fetch == nil || ctx.Err() != nil {
		return false
	}
	limit := profileJobBatchSize
	if m.externalNetwork.isUnavailable() {
		limit = 1
	}
	jobs, err := m.db.ClaimProfileJobs(db.LeaseOptions{
		Owner: profileJobLeaseOwner(),
		Limit: limit,
	})
	if err != nil {
		log.Printf("[profile] ClaimProfileJobs: %v", err)
		return false
	}
	if len(jobs) == 0 {
		return false
	}
	if !m.externalWorkAllowed(time.Now()) {
		for _, job := range jobs {
			if err := m.db.ReleaseProfileJob(job, time.Now().UnixMilli()); err != nil {
				log.Printf("[profile] release %s while network probe is active: %v", job.ChannelID, err)
			}
		}
		return true
	}

	var wg sync.WaitGroup
	for _, job := range jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.processProfileJob(ctx, fetch, job)
		}()
	}
	wg.Wait()
	return true
}

func (m *Manager) processProfileJob(ctx context.Context, fetch fetchFn, job model.ProfileJob) {
	profile, err := m.fetchProfileJob(ctx, fetch, job.ChannelID)
	now := time.Now().UTC()
	if err != nil {
		if m.ReportExternalResult(err) {
			if releaseErr := m.db.ReleaseProfileJob(job, now.UnixMilli()); releaseErr != nil {
				log.Printf("[profile] release %s after network failure: %v", job.ChannelID, releaseErr)
			}
			return
		}
		if errors.Is(err, fetchprofile.ErrNotFound) && platformForChannelID(job.ChannelID) != "twitter" {
			profile = model.ChannelProfile{
				ChannelID: job.ChannelID,
				Platform:  platformForChannelID(job.ChannelID),
				Handle:    trimChannelIDPrefix(job.ChannelID),
				FetchedAt: &now,
				Tombstone: true,
			}
			if profile.Platform == "" {
				m.retryProfileJob(job, err, now)
				return
			}
		} else {
			m.retryProfileJob(job, err, now)
			return
		}
	}
	m.ReportExternalResult(nil)

	replacements, previous, stagedPaths, stageErr := m.stageProfileJobAssets(ctx, job, profile)
	stored, complete, err := m.db.CompleteProfileJob(job, profile, replacements, now.UnixMilli())
	if err != nil {
		m.removeMediaPaths(ctx, download.MediaLaneState, stagedPaths...)
		log.Printf("[profile] CompleteProfileJob %s: %v", job.ChannelID, err)
		m.retryProfileJob(job, err, now)
		return
	}
	if !stored {
		m.removeMediaPaths(ctx, download.MediaLaneState, stagedPaths...)
		log.Printf("[profile] discarded superseded revision %d for %s", job.RequestedRevision, job.ChannelID)
		m.KickProfileJobs()
		return
	}
	m.removeReplacedProfileFiles(previous, profile, replacements)
	if complete {
		return
	}
	if stageErr == nil {
		stageErr = errors.New("declared profile media is not ready")
	}
	if m.ReportExternalResult(stageErr) {
		if releaseErr := m.db.ReleaseProfileJob(job, now.UnixMilli()); releaseErr != nil {
			log.Printf("[profile] release incomplete %s after network failure: %v", job.ChannelID, releaseErr)
		}
		return
	}
	m.retryProfileJob(job, stageErr, now)
}

func (m *Manager) fetchProfileJob(ctx context.Context, fetch fetchFn, channelID string) (model.ChannelProfile, error) {
	if strings.HasPrefix(channelID, "instagram_") {
		profile, err := m.fetchInstagramProfile(ctx, channelID, strings.TrimPrefix(channelID, "instagram_"))
		if err != nil {
			return model.ChannelProfile{}, err
		}
		if profile == nil || strings.TrimSpace(profile.DisplayName) == "" {
			return model.ChannelProfile{}, fmt.Errorf("%w: %s has no display name", fetchprofile.ErrIncompleteProfile, channelID)
		}
		profile.ChannelID = channelID
		profile.Platform = "instagram"
		if profile.Handle == "" {
			profile.Handle = strings.TrimPrefix(channelID, "instagram_")
		}
		return *profile, nil
	}

	profile, err := fetch(ctx, channelID)
	if err != nil {
		return model.ChannelProfile{}, err
	}
	if err := fetchprofile.ValidateChannelIdentity(channelID, profile); err != nil {
		return model.ChannelProfile{}, err
	}
	return model.ChannelProfile{
		ChannelID:    channelID,
		Platform:     profile.Platform,
		Handle:       profile.Handle,
		DisplayName:  profile.DisplayName,
		Bio:          profile.Bio,
		Website:      profile.Website,
		Followers:    profile.Followers,
		Following:    profile.Following,
		Verified:     profile.Verified,
		VerifiedType: profile.VerifiedType,
		Protected:    profile.Protected,
		AvatarURL:    profile.AvatarURL,
		BannerURL:    profile.BannerURL,
	}, nil
}

func (m *Manager) fetchInstagramProfile(ctx context.Context, channelID, handle string) (*model.ChannelProfile, error) {
	if m != nil && m.instagramProfileFetch != nil {
		return m.instagramProfileFetch(ctx, channelID, handle)
	}
	if m == nil || m.downloader == nil || m.downloader.GalleryDL == nil {
		return nil, errors.New("instagram profile downloader unavailable")
	}
	cookiesFile, cookiesBrowser := m.cookieFileAndBrowserFor("instagram")
	profile, err := m.downloader.GalleryDL.InstagramProfile(ctx, handle, cookiesFile, cookiesBrowser)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, fetchprofile.ErrNotFound
	}
	return &model.ChannelProfile{
		ChannelID:   channelID,
		Platform:    "instagram",
		Handle:      profile.Handle,
		DisplayName: profile.DisplayName,
		Bio:         profile.Bio,
		Website:     profile.Website,
		Followers:   profile.Followers,
		Following:   profile.Following,
		Verified:    profile.Verified,
		AvatarURL:   profile.AvatarURL,
	}, nil
}

func (m *Manager) retryProfileJob(job model.ProfileJob, cause error, now time.Time) {
	if errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		if err := m.db.ReleaseProfileJob(job, now.UnixMilli()); err != nil {
			log.Printf("[profile] ReleaseProfileJob %s: %v", job.ChannelID, err)
		}
		return
	}
	delay := profileJobRetryDelay(job.Attempts + 1)
	message := download.RedactText(cause.Error())
	if job.Attempts+1 >= profileJobMaxAttempts {
		if err := m.db.FailProfileJob(job, message, now.UnixMilli()); err != nil {
			log.Printf("[profile] FailProfileJob %s: %v", job.ChannelID, err)
			return
		}
		log.Printf("[profile] stopped %s after %d attempts: %s", job.ChannelID, job.Attempts+1, message)
		return
	}
	if err := m.db.RetryProfileJob(job, message, delay, now.UnixMilli()); err != nil {
		log.Printf("[profile] RetryProfileJob %s: %v", job.ChannelID, err)
		return
	}
	log.Printf("[profile] retry %s in %s: %s", job.ChannelID, delay, message)
}

func profileJobRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := profileJobInitialBackoff
	for i := 1; i < attempt && delay < profileJobMaxBackoff; i++ {
		delay *= 2
		if delay > profileJobMaxBackoff {
			delay = profileJobMaxBackoff
		}
	}
	return delay
}

func (m *Manager) stageProfileJobAssets(ctx context.Context, job model.ProfileJob, profile model.ChannelProfile) ([]db.Asset, map[string]*db.Asset, []string, error) {
	if m == nil || m.db == nil || m.cfg == nil {
		return nil, nil, nil, errors.New("profile media storage unavailable")
	}
	previous := make(map[string]*db.Asset, 2)
	var replacements []db.Asset
	var stagedPaths []string
	var stageErrors []error
	for _, kind := range []string{"avatar", "banner"} {
		current, err := m.db.GetAssetByOwnerIdentity(kind, "channel", profile.ChannelID, 0)
		if err != nil {
			stageErrors = append(stageErrors, fmt.Errorf("read current %s: %w", kind, err))
			continue
		}
		previous[kind] = current
		sourceURL := profileMediaSource(profile, kind)
		if sourceURL == "" || db.ReadyAssetMatchesSource(current, sourceURL) {
			continue
		}
		replacement, path, err := m.downloadProfileJobAsset(ctx, job, profile, kind, sourceURL)
		if err != nil {
			stageErrors = append(stageErrors, fmt.Errorf("download %s: %w", kind, err))
			continue
		}
		replacements = append(replacements, replacement)
		stagedPaths = append(stagedPaths, path)
	}
	return replacements, previous, stagedPaths, errors.Join(stageErrors...)
}

func profileMediaSource(profile model.ChannelProfile, kind string) string {
	if kind == "banner" {
		return strings.TrimSpace(profile.BannerURL)
	}
	return strings.TrimSpace(profile.AvatarURL)
}

func (m *Manager) downloadProfileJobAsset(ctx context.Context, job model.ProfileJob, profile model.ChannelProfile, kind, sourceURL string) (db.Asset, string, error) {
	if m.downloader == nil || m.downloader.HTTP == nil {
		return db.Asset{}, "", errors.New("profile media downloader unavailable")
	}
	var asset db.Asset
	var path string
	err := m.downloader.RunMedia(ctx, download.MediaLaneState, func() error {
		var err error
		asset, path, err = m.downloadProfileJobAssetAdmitted(ctx, job, profile, kind, sourceURL)
		return err
	})
	return asset, path, err
}

func (m *Manager) downloadProfileJobAssetAdmitted(ctx context.Context, job model.ProfileJob, profile model.ChannelProfile, kind, sourceURL string) (db.Asset, string, error) {
	dirName := "avatars"
	if kind == "banner" {
		dirName = "banners"
	}
	dir, err := m.cfg.Storage.WritePath(filepath.Join("thumbnails", dirName))
	if err != nil {
		return db.Asset{}, "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return db.Asset{}, "", err
	}
	if job.LeaseUntil == nil {
		return db.Asset{}, "", errors.New("profile media job has no lease token")
	}
	base := fmt.Sprintf(
		"%s-r%d-%d", profileMediaOwnerKey(profile.ChannelID), job.RequestedRevision, job.LeaseUntil.UnixMilli(),
	)
	downloadURL := sourceURL
	if kind == "avatar" && profile.Platform == "twitter" {
		downloadURL = upgradeTwimgURL(sourceURL)
	}
	tmpPath, err := m.downloader.HTTP.DownloadFile(ctx, downloadURL, dir, base+".download")
	if err != nil && downloadURL != sourceURL {
		tmpPath, err = m.downloader.HTTP.DownloadFile(ctx, sourceURL, dir, base+".download")
	}
	if err != nil {
		return db.Asset{}, "", err
	}
	finalPath, err := normalizeDownloadedImage(tmpPath, dir, base)
	if err != nil {
		_ = os.Remove(tmpPath)
		return db.Asset{}, "", err
	}
	fileKey, err := m.cfg.Storage.Key(finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return db.Asset{}, "", err
	}
	contentType, err := sniffImageContentType(finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return db.Asset{}, "", err
	}
	return db.Asset{
		AssetKind:      kind,
		SourceURL:      sourceURL,
		FilePath:       fileKey,
		ContentType:    contentType,
		State:          db.AssetStateReady,
		RequiredReason: "identity",
	}, finalPath, nil
}

func profileMediaOwnerKey(channelID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(channelID)))
	return fmt.Sprintf("profile-%x", sum[:12])
}

func (m *Manager) removeReplacedProfileFiles(previous map[string]*db.Asset, profile model.ChannelProfile, replacements []db.Asset) {
	kept := make(map[string]bool, 2)
	replacedKinds := make(map[string]bool, len(replacements))
	for _, replacement := range replacements {
		kept[replacement.FilePath] = true
		replacedKinds[replacement.AssetKind] = true
	}
	for kind, asset := range previous {
		if asset == nil || asset.FilePath == "" {
			continue
		}
		if profileMediaSource(profile, kind) != "" && !replacedKinds[kind] {
			kept[asset.FilePath] = true
		}
	}
	for kind, asset := range previous {
		if asset == nil || asset.FilePath == "" {
			continue
		}
		if kept[asset.FilePath] {
			continue
		}
		if _, err := m.db.RemoveAssetFileIfUnreferenced(asset.FilePath); err != nil {
			log.Printf("[profile] remove replaced %s file %s: %v", kind, asset.FilePath, err)
		}
	}
}

func upgradeTwimgURL(rawURL string) string {
	return strings.Replace(rawURL, "_normal.", "_400x400.", 1)
}

func profileJobLeaseOwner() string {
	return processLeaseOwner("profile")
}

func processLeaseOwner(worker string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s:%s:%d", worker, host, os.Getpid())
}

func platformForChannelID(channelID string) string {
	switch {
	case strings.HasPrefix(channelID, "twitter_"):
		return "twitter"
	case strings.HasPrefix(channelID, "youtube_"):
		return "youtube"
	case strings.HasPrefix(channelID, "tiktok_"):
		return "tiktok"
	case strings.HasPrefix(channelID, "instagram_"):
		return "instagram"
	default:
		return ""
	}
}

func trimChannelIDPrefix(channelID string) string {
	if idx := strings.IndexByte(channelID, '_'); idx >= 0 && idx+1 < len(channelID) {
		return channelID[idx+1:]
	}
	return channelID
}
