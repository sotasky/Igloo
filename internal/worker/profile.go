package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/fetchprofile"
	"github.com/screwys/igloo/internal/model"
)

type storedMediaHostLookup func(host string) ([]netip.Addr, error)

var lookupStoredMediaHost storedMediaHostLookup = func(host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

const (
	profileActiveTick         = 5 * time.Second
	profileIdleTick           = 5 * time.Minute
	profileTTL                = 24 * time.Hour
	instagramProfileBackoff   = 15 * time.Minute
	profileMaxBackoff         = 6 * time.Hour
	profileBatchPerTick       = 5
	feedProfileBatchPerTick   = 10
	feedAvatarSeedMinSpacing  = time.Minute
	profileRequestConcurrency = 4
)

var profileRefreshPlatforms = []string{"twitter", "youtube", "tiktok", "instagram"}

// fetchFn is fetchprofile.Fetch, overridable by tests.
type fetchFn func(ctx context.Context, channelID string) (*fetchprofile.Profile, error)

type instagramProfileFetchFn func(ctx context.Context, channelID, handle string) (*model.ChannelProfile, error)

// runProfileRefreshLoop owns the broad profile + avatar + banner sweeps across
// all platforms. Two-phase cadence: 5 s between refresh batches while work is
// pending, 5 min idle tick when everything is fresh.
func (m *Manager) runProfileRefreshLoop(ctx context.Context) {
	log.Printf("[profile] refresh worker started")
	avDir := filepath.Join(m.cfg.DataDir, "thumbnails", "avatars")
	bnDir := filepath.Join(m.cfg.DataDir, "thumbnails", "banners")
	if err := os.MkdirAll(avDir, 0o755); err != nil {
		log.Printf("[profile] mkdir %s: %v", avDir, err)
	}
	if err := os.MkdirAll(bnDir, 0o755); err != nil {
		log.Printf("[profile] mkdir %s: %v", bnDir, err)
	}
	var requestWG sync.WaitGroup
	requestWG.Add(1)
	go func() {
		defer requestWG.Done()
		m.runOnDemandProfileRequestLoop(ctx, avDir, bnDir)
	}()
	defer requestWG.Wait()

	interval := profileActiveTick
	lastFeedAvatarSeed := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(lastFeedAvatarSeed) >= feedAvatarSeedMinSpacing {
				if n, err := m.db.SeedChannelProfileRows(); err != nil {
					log.Printf("[profile] SeedChannelProfileRows: %v", err)
				} else if n > 0 {
					log.Printf("[profile] seeded/updated %d profile rows", n)
				}
				if n, err := m.db.MarkTwitterProfileDriftDueFromFeedRows(200); err != nil {
					log.Printf("[profile] MarkTwitterProfileDriftDueFromFeedRows: %v", err)
				} else if n > 0 {
					log.Printf("[profile] marked %d twitter profile rows refresh-due after feed identity drift", n)
				}
				if n, err := m.db.SeedSyntheticTwitterAvatarProfiles(); err != nil {
					log.Printf("[profile] SeedSyntheticTwitterAvatarProfiles: %v", err)
				} else if n > 0 {
					log.Printf("[profile] seeded %d synthetic twitter avatar profile rows", n)
				}
				lastFeedAvatarSeed = time.Now()
			}
			feedWorked := m.refreshFeedProfileCompletenessBatch(ctx, fetchprofile.Fetch, avDir, bnDir, feedProfileBatchPerTick)
			staleWorked := m.refreshStaleProfilesBatch(ctx, fetchprofile.Fetch, avDir, bnDir, profileBatchPerTick)
			worked := feedWorked || staleWorked
			if !worked {
				if n, err := m.db.SeedChannelProfileRows(); err == nil && n > 0 {
					log.Printf("[profile] seeded/updated %d profile rows", n)
					if driftN, driftErr := m.db.MarkTwitterProfileDriftDueFromFeedRows(200); driftErr != nil {
						log.Printf("[profile] MarkTwitterProfileDriftDueFromFeedRows: %v", driftErr)
					} else if driftN > 0 {
						log.Printf("[profile] marked %d twitter profile rows refresh-due after feed identity drift", driftN)
					}
					worked = m.refreshFeedProfileCompletenessBatch(ctx, fetchprofile.Fetch, avDir, bnDir, feedProfileBatchPerTick)
					if !worked {
						worked = m.refreshStaleProfilesBatch(ctx, fetchprofile.Fetch, avDir, bnDir, profileBatchPerTick)
					}
					if !worked {
						worked = true
					}
				}
			}
			want := profileIdleTick
			if worked {
				want = profileActiveTick
			}
			if want != interval {
				interval = want
				ticker.Reset(interval)
			}
		}
	}
}

// refreshFeedProfileCompletenessBatch backfills recently visible identities
// before the generic stale sweep: full profile rows first, then missing avatars.
func (m *Manager) refreshFeedProfileCompletenessBatch(ctx context.Context, fetch fetchFn, avDir, bnDir string, limit int) bool {
	if limit <= 0 {
		limit = 1
	}
	channelIDs, err := m.db.ListFeedAvatarProfileIDs()
	if err != nil {
		log.Printf("[profile] ListFeedAvatarProfileIDs: %v", err)
		return false
	}
	byPlatform := make(map[string][]string)
	for _, channelID := range channelIDs {
		platform := platformForChannelID(channelID)
		if platform == "" {
			platform = "other"
		}
		byPlatform[platform] = append(byPlatform[platform], channelID)
	}
	if len(byPlatform) == 0 {
		return false
	}

	var wg sync.WaitGroup
	workedCh := make(chan bool, len(byPlatform))
	for _, ids := range byPlatform {
		ids := ids
		if len(ids) == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			workedCh <- m.refreshFeedProfileCompletenessIDs(ctx, fetch, avDir, bnDir, ids, limit)
		}()
	}
	wg.Wait()
	close(workedCh)
	worked := false
	for platformWorked := range workedCh {
		worked = worked || platformWorked
	}
	return worked
}

func (m *Manager) refreshFeedProfileCompletenessIDs(ctx context.Context, fetch fetchFn, avDir, bnDir string, channelIDs []string, limit int) bool {
	worked := false
	profileAttempts := 0
	storedMediaAttempts := 0
	now := time.Now()
	for _, channelID := range channelIDs {
		if ctx.Err() != nil {
			break
		}
		existing, err := m.db.GetChannelProfile(channelID)
		if err != nil {
			log.Printf("[profile] GetChannelProfile %s: %v", channelID, err)
			continue
		}
		if existing == nil || existing.Tombstone || !profileRetryDue(existing, now) {
			continue
		}
		storedInstagramAvatarAttempt := strings.HasPrefix(channelID, "instagram_") &&
			!hasConventionalMediaFile(avDir, channelID) &&
			canDownloadStoredAvatar(channelID, existing.AvatarURL)
		fullRefreshDue := shouldRefreshFullProfile(channelID, existing, now)
		storedMediaDue := storedProfileMediaDue(channelID, existing, avDir, bnDir)
		downloaded, attempted := false, false
		if storedMediaDue && storedMediaAttempts < limit {
			downloaded, attempted = m.downloadStoredProfileMedia(ctx, channelID, existing, avDir, bnDir)
			if attempted {
				storedMediaAttempts++
			}
		}
		if downloaded {
			worked = true
			if fullRefreshDue && profileAttempts < limit {
				m.refreshProfile(ctx, fetch, channelID, avDir, bnDir)
				profileAttempts++
			}
			continue
		}
		if attempted && storedInstagramAvatarAttempt && !profileFetchDue(existing, now) {
			m.recordInstagramAvatarFallbackError(channelID, existing, errors.New("stored instagram profile media download failed"), now)
			worked = true
			continue
		}
		if fullRefreshDue {
			if profileAttempts >= limit {
				continue
			}
			m.refreshProfile(ctx, fetch, channelID, avDir, bnDir)
			worked = true
			profileAttempts++
			continue
		}

		if strings.HasPrefix(channelID, "instagram_") &&
			existing.AvatarURL == "" &&
			!profileFetchDue(existing, now) &&
			!hasConventionalMediaFile(avDir, channelID) {
			if storedMediaAttempts >= limit {
				continue
			}
			downloaded, err := m.downloadInstagramProfileAvatar(ctx, channelID, avDir)
			if downloaded {
				worked = true
			} else if err != nil {
				m.recordInstagramAvatarFallbackError(channelID, existing, err, now)
			}
			storedMediaAttempts++
			continue
		}
		if attempted || profileFetchDue(existing, now) {
			if profileAttempts >= limit {
				continue
			}
			m.refreshProfile(ctx, fetch, channelID, avDir, bnDir)
		} else {
			continue
		}
		worked = true
		profileAttempts++
	}
	return worked
}

func profileRetryDue(p *model.ChannelProfile, now time.Time) bool {
	return p.NextRetryAt == nil || !p.NextRetryAt.After(now)
}

func profileFetchDue(p *model.ChannelProfile, now time.Time) bool {
	return p.FetchedAt == nil || p.FetchedAt.IsZero() || p.FetchedAt.Before(now.Add(-profileTTL))
}

func shouldRefreshFullProfile(channelID string, p *model.ChannelProfile, now time.Time) bool {
	if p == nil || !profileFetchDue(p, now) || model.IsSyntheticTwitterAvatarChannelID(channelID) {
		return false
	}
	return strings.HasPrefix(channelID, "twitter_") ||
		strings.HasPrefix(channelID, "youtube_") ||
		strings.HasPrefix(channelID, "tiktok_")
}

func (m *Manager) downloadStoredProfileMedia(ctx context.Context, channelID string, p *model.ChannelProfile, avDir, bnDir string) (downloaded bool, attempted bool) {
	if p == nil {
		return false, false
	}
	if !hasConventionalMediaFile(avDir, channelID) && canDownloadStoredAvatar(channelID, p.AvatarURL) {
		attempted = true
		if m.downloadProfileMedia(ctx, channelID, "avatar", p.AvatarURL, avDir) {
			downloaded = true
		}
	}
	if !hasConventionalMediaFile(bnDir, channelID) && canDownloadStoredBanner(p.BannerURL) {
		attempted = true
		if m.downloadProfileMedia(ctx, channelID, "banner", p.BannerURL, bnDir) {
			downloaded = true
		}
	}
	return downloaded, attempted
}

func storedProfileMediaDue(channelID string, p *model.ChannelProfile, avDir, bnDir string) bool {
	if p == nil {
		return false
	}
	if !hasConventionalMediaFile(avDir, channelID) && canDownloadStoredAvatar(channelID, p.AvatarURL) {
		return true
	}
	return !hasConventionalMediaFile(bnDir, channelID) && canDownloadStoredBanner(p.BannerURL)
}

func canDownloadStoredAvatar(channelID, avatarURL string) bool {
	avatarURL = strings.TrimSpace(avatarURL)
	if avatarURL == "" {
		return false
	}
	if strings.HasPrefix(channelID, "twitter_") {
		return model.IsRawTwitterProfileAvatar(avatarURL)
	}
	return isSafeStoredMediaURL(avatarURL)
}

func canDownloadStoredBanner(bannerURL string) bool {
	bannerURL = strings.TrimSpace(bannerURL)
	if bannerURL == "" || strings.HasPrefix(bannerURL, "synth:") {
		return false
	}
	lower := strings.ToLower(bannerURL)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func isSafeStoredMediaURL(rawURL string) bool {
	return isSafeStoredMediaURLWithLookup(rawURL, lookupStoredMediaHost)
}

func isSafeStoredMediaURLWithLookup(rawURL string, lookup storedMediaHostLookup) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	host = strings.TrimRight(host, ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return isSafeStoredMediaAddr(ip)
	}
	if lookup == nil {
		return false
	}
	addrs, err := lookup(host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, addr := range addrs {
		if !isSafeStoredMediaAddr(addr) {
			return false
		}
	}
	return true
}

func isSafeStoredMediaAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsValid() &&
		addr.IsGlobalUnicast() &&
		!addr.IsLoopback() &&
		!addr.IsPrivate() &&
		!addr.IsLinkLocalUnicast() &&
		!addr.IsUnspecified()
}

func (m *Manager) refreshStaleProfilesBatch(ctx context.Context, fetch fetchFn, avDir, bnDir string, limit int) bool {
	if limit <= 0 {
		limit = 1
	}
	var wg sync.WaitGroup
	workedCh := make(chan bool, len(profileRefreshPlatforms))
	for _, platform := range profileRefreshPlatforms {
		platform := platform
		wg.Add(1)
		go func() {
			defer wg.Done()
			platformWorked := false
			for i := 0; i < limit; i++ {
				if ctx.Err() != nil {
					break
				}
				if !m.refreshOneStaleProfileForPlatform(ctx, fetch, avDir, bnDir, platform) {
					break
				}
				platformWorked = true
			}
			workedCh <- platformWorked
		}()
	}
	wg.Wait()
	close(workedCh)
	worked := false
	for platformWorked := range workedCh {
		worked = worked || platformWorked
	}
	return worked
}

func (m *Manager) runOnDemandProfileRequestLoop(ctx context.Context, avDir, bnDir string) {
	sem := make(chan struct{}, profileRequestConcurrency)
	var wg sync.WaitGroup
	var inFlightMu sync.Mutex
	inFlight := make(map[string]bool)
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			return
		case channelID := <-m.avatarRequest:
			channelID = strings.TrimSpace(channelID)
			if channelID == "" {
				continue
			}
			inFlightMu.Lock()
			if inFlight[channelID] {
				inFlightMu.Unlock()
				continue
			}
			inFlight[channelID] = true
			inFlightMu.Unlock()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				inFlightMu.Lock()
				delete(inFlight, channelID)
				inFlightMu.Unlock()
				return
			}

			wg.Add(1)
			go func(channelID string) {
				defer wg.Done()
				defer func() {
					<-sem
					inFlightMu.Lock()
					delete(inFlight, channelID)
					inFlightMu.Unlock()
				}()
				m.refreshRequestedAvatar(ctx, fetchprofile.Fetch, channelID, avDir, bnDir)
			}(channelID)
		}
	}
}

func (m *Manager) refreshRequestedAvatar(ctx context.Context, fetch fetchFn, channelID, avDir, bnDir string) {
	existing, err := m.db.GetChannelProfile(channelID)
	if err != nil {
		log.Printf("[profile] GetChannelProfile %s: %v", channelID, err)
	}
	attemptedFullRefresh := false
	now := time.Now()
	if existing != nil && !existing.Tombstone && profileRetryDue(existing, now) && shouldRefreshFullProfile(channelID, existing, now) {
		m.refreshProfile(ctx, fetch, channelID, avDir, bnDir)
		attemptedFullRefresh = true
		m.downloadStoredProfileMedia(ctx, channelID, existing, avDir, bnDir)
		if hasConventionalMediaFile(avDir, channelID) {
			return
		}
	}
	if existing != nil && !attemptedFullRefresh {
		m.downloadStoredProfileMedia(ctx, channelID, existing, avDir, bnDir)
		if hasConventionalMediaFile(avDir, channelID) {
			return
		}
	}
	if attemptedFullRefresh {
		return
	}
	m.refreshProfile(ctx, fetch, channelID, avDir, bnDir)
}

// refreshOneStaleProfile picks the oldest-stale row and refreshes it.
// Returns true iff it attempted a fetch.
func (m *Manager) refreshOneStaleProfile(ctx context.Context, fetch fetchFn, avDir, bnDir string) bool {
	return m.refreshOneStaleProfileForPlatform(ctx, fetch, avDir, bnDir, "")
}

func (m *Manager) refreshOneStaleProfileForPlatform(ctx context.Context, fetch fetchFn, avDir, bnDir, platform string) bool {
	var (
		channelID string
		err       error
	)
	if platform == "" {
		channelID, err = m.db.NextChannelProfileRefreshCandidate(profileTTL)
	} else {
		channelID, err = m.db.NextChannelProfileRefreshCandidateForPlatform(profileTTL, platform)
	}
	if err != nil {
		log.Printf("[profile] NextChannelProfileRefreshCandidate: %v", err)
		return false
	}
	if channelID == "" {
		return false
	}
	m.refreshProfile(ctx, fetch, channelID, avDir, bnDir)
	return true
}

// refreshProfile fetches a single channel's profile, updates the row, and
// downloads avatar/banner if the source URLs changed or files are missing.
// Errors are recorded as backoff/tombstone; never returned.
func (m *Manager) refreshProfile(ctx context.Context, fetch fetchFn, channelID, avDir, bnDir string) {
	existing, _ := m.db.GetChannelProfile(channelID)
	if existing != nil && model.IsSyntheticTwitterAvatarChannelID(channelID) && existing.AvatarURL != "" {
		now := time.Now().UTC()
		if !hasConventionalMediaFile(avDir, channelID) {
			m.downloadProfileMedia(ctx, channelID, "avatar", existing.AvatarURL, avDir)
		}
		row := *existing
		row.Platform = "twitter"
		row.FetchedAt = &now
		row.FailCount = 0
		row.NextRetryAt = nil
		row.Tombstone = false
		if err := m.db.UpsertChannelProfile(row); err != nil {
			log.Printf("[profile] upsert synthetic %s: %v", channelID, err)
		}
		return
	}
	if strings.HasPrefix(channelID, "instagram_") {
		m.refreshInstagramStoredProfile(ctx, channelID, avDir, bnDir, existing)
		return
	}
	p, err := m.fetchProfile(ctx, fetch, channelID)
	now := time.Now().UTC()
	if err != nil {
		m.recordProfileFetchError(channelID, existing, err, now)
		return
	}
	if err := fetchprofile.ValidateChannelIdentity(channelID, p); err != nil {
		m.recordProfileFetchError(channelID, existing, err, now)
		return
	}

	// TikTok/Instagram have no native profile banner; synthesize one from the newest
	// downloaded video's cover so the channel header matches Twitter/YouTube.
	if (p.Platform == "tiktok" || p.Platform == "instagram") && p.BannerURL == "" {
		p.BannerURL = m.refreshStoredShortsBanner(channelID, bnDir, existing, p.BannerURL)
	}

	row := model.ChannelProfile{
		ChannelID:    p.ChannelID,
		Platform:     p.Platform,
		Handle:       p.Handle,
		DisplayName:  p.DisplayName,
		Bio:          p.Bio,
		Website:      p.Website,
		Followers:    p.Followers,
		Following:    p.Following,
		Verified:     p.Verified,
		VerifiedType: p.VerifiedType,
		Protected:    p.Protected,
		AvatarURL:    p.AvatarURL,
		BannerURL:    p.BannerURL,
		FetchedAt:    &now,
		FailCount:    0,
	}
	if err := m.db.UpsertChannelProfile(row); err != nil {
		log.Printf("[profile] upsert %s: %v", channelID, err)
		return
	}
	m.primeProfileBioMentions(row)

	// Avatar: download if URL changed or file missing.
	avChanged := existing == nil || existing.AvatarURL != p.AvatarURL
	if p.AvatarURL != "" && (avChanged || !hasConventionalMediaFile(avDir, channelID)) {
		m.downloadProfileMedia(ctx, channelID, "avatar", p.AvatarURL, avDir)
	}

	// Banner: same, skipped when platform has no banner (empty URL).
	// synth:* URLs are server-synthesized; the file is already placed on disk
	// by synthesizeShortsBanner, no HTTP fetch needed.
	bnChanged := existing == nil || existing.BannerURL != p.BannerURL
	if p.BannerURL != "" && !strings.HasPrefix(p.BannerURL, "synth:") &&
		(bnChanged || !hasConventionalMediaFile(bnDir, channelID)) {
		m.downloadProfileMedia(ctx, channelID, "banner", p.BannerURL, bnDir)
	}
}

func (m *Manager) fetchProfile(ctx context.Context, fetch fetchFn, channelID string) (*fetchprofile.Profile, error) {
	return fetch(ctx, channelID)
}

func (m *Manager) refreshInstagramStoredProfile(ctx context.Context, channelID, avDir, bnDir string, existing *model.ChannelProfile) {
	now := time.Now().UTC()
	handle := strings.TrimPrefix(channelID, "instagram_")
	row := model.ChannelProfile{
		ChannelID: channelID,
		Platform:  "instagram",
		Handle:    handle,
		FetchedAt: &now,
		FailCount: 0,
	}
	if existing != nil {
		row.Handle = existing.Handle
		if row.Handle == "" {
			row.Handle = handle
		}
		row.DisplayName = existing.DisplayName
		row.Bio = existing.Bio
		row.Website = existing.Website
		row.Followers = existing.Followers
		row.Following = existing.Following
		row.Verified = existing.Verified
		row.VerifiedType = existing.VerifiedType
		row.Protected = existing.Protected
		row.AvatarURL = existing.AvatarURL
		row.BannerURL = existing.BannerURL
	}
	row.BannerURL = m.refreshStoredShortsBanner(channelID, bnDir, existing, row.BannerURL)
	// Avoid competing with the Instagram source/download backlog when we already
	// have an avatar URL to use, but do not starve blank avatar rows forever.
	if m.instagramSourceBacklogExists() && row.AvatarURL != "" && hasConventionalMediaFile(avDir, channelID) {
		next := now.Add(instagramProfileBackoff)
		row.FetchedAt = nil
		if existing != nil {
			row.FetchedAt = existing.FetchedAt
		}
		row.NextRetryAt = &next
		if err := m.db.UpsertChannelProfile(row); err != nil {
			log.Printf("[profile] defer instagram profile %s: %v", channelID, err)
		} else {
			m.primeProfileBioMentions(row)
		}
		if row.AvatarURL != "" && !hasConventionalMediaFile(avDir, channelID) {
			m.downloadProfileMedia(ctx, channelID, "avatar", row.AvatarURL, avDir)
		}
		return
	}
	if existing != nil && row.BannerURL != "" && existing.BannerURL != row.BannerURL &&
		(row.AvatarURL != "" || !profileRetryDue(existing, now) || !profileFetchDue(existing, now)) {
		next := now.Add(instagramProfileBackoff)
		if existing.NextRetryAt != nil && existing.NextRetryAt.After(next) {
			next = *existing.NextRetryAt
		}
		row.FetchedAt = existing.FetchedAt
		row.FailCount = existing.FailCount
		row.NextRetryAt = &next
		row.Tombstone = existing.Tombstone
		if err := m.db.UpsertChannelProfile(row); err != nil {
			log.Printf("[profile] defer instagram profile after banner %s: %v", channelID, err)
		} else {
			m.primeProfileBioMentions(row)
		}
		return
	}
	errorBase := m.persistInstagramBannerSnapshot(row, existing)
	if profile, err := m.fetchInstagramProfile(ctx, channelID, handle); err != nil {
		m.recordProfileFetchError(channelID, errorBase, err, now)
		return
	} else if profile != nil {
		if strings.TrimSpace(profile.DisplayName) == "" {
			m.recordProfileFetchError(channelID, errorBase,
				fmt.Errorf("%w: %s has no display name", fetchprofile.ErrIncompleteProfile, channelID), now)
			return
		}
		row.Handle = profile.Handle
		if row.Handle == "" {
			row.Handle = handle
		}
		row.DisplayName = profile.DisplayName
		row.Bio = profile.Bio
		row.Website = profile.Website
		row.Followers = profile.Followers
		row.Following = profile.Following
		row.Verified = profile.Verified
		row.AvatarURL = profile.AvatarURL
	}
	row.BannerURL = m.refreshStoredShortsBanner(channelID, bnDir, existing, row.BannerURL)
	if err := m.db.UpsertChannelProfile(row); err != nil {
		log.Printf("[profile] upsert instagram stored %s: %v", channelID, err)
		return
	}
	m.primeProfileBioMentions(row)
	if row.AvatarURL != "" && !hasConventionalMediaFile(avDir, channelID) {
		if !m.downloadProfileMedia(ctx, channelID, "avatar", row.AvatarURL, avDir) && m != nil && m.downloader != nil {
			m.recordInstagramAvatarFallbackError(channelID, &row, errors.New("instagram avatar download failed"), now)
		}
	} else if row.AvatarURL == "" && !hasConventionalMediaFile(avDir, channelID) {
		if downloaded, err := m.downloadInstagramProfileAvatar(ctx, channelID, avDir); !downloaded && err != nil {
			m.recordInstagramAvatarFallbackError(channelID, &row, err, now)
		}
	}
}

func (m *Manager) primeProfileBioMentions(p model.ChannelProfile) {
	if m == nil || m.db == nil {
		return
	}
	channelIDs, err := m.db.SeedProfileBioMentionProfileRows(p)
	if err != nil {
		log.Printf("[profile] seed bio mentions %s: %v", p.ChannelID, err)
		return
	}
	for _, channelID := range channelIDs {
		if channelID == "" || channelID == p.ChannelID {
			continue
		}
		m.RequestAvatar(channelID)
	}
}

func (m *Manager) persistInstagramBannerSnapshot(row model.ChannelProfile, existing *model.ChannelProfile) *model.ChannelProfile {
	if row.BannerURL == "" || !strings.HasPrefix(row.BannerURL, shortsBannerSentinelPrefix) {
		return existing
	}
	if existing != nil && existing.BannerURL == row.BannerURL {
		return existing
	}
	snapshot := row
	if existing != nil {
		snapshot.FetchedAt = existing.FetchedAt
		snapshot.FailCount = existing.FailCount
		snapshot.NextRetryAt = existing.NextRetryAt
		snapshot.Tombstone = existing.Tombstone
	} else {
		snapshot.FetchedAt = nil
		snapshot.FailCount = 0
		snapshot.NextRetryAt = nil
		snapshot.Tombstone = false
	}
	if err := m.db.UpsertChannelProfile(snapshot); err != nil {
		log.Printf("[profile] persist instagram banner %s: %v", row.ChannelID, err)
		return existing
	}
	return &snapshot
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

func (m *Manager) instagramSourceBacklogExists() bool {
	if m == nil || m.db == nil {
		return false
	}
	channels, err := m.db.GetSubscribedChannels()
	if err != nil {
		log.Printf("[profile] GetSubscribedChannels for instagram backlog: %v", err)
		return true
	}
	for _, ch := range channels {
		if ch.Platform != "instagram" {
			continue
		}
		if ch.LastChecked == nil || ch.LastChecked.IsZero() {
			return true
		}
		queued, err := m.db.GetQueuedVideoCount(ch.ChannelID)
		if err != nil {
			log.Printf("[profile] GetQueuedVideoCount for instagram backlog %s: %v", ch.ChannelID, err)
			return true
		}
		if queued > 0 {
			return true
		}
	}
	return false
}

func (m *Manager) downloadProfileMedia(ctx context.Context, channelID, kind, url, dir string) bool {
	if m == nil || m.downloader == nil {
		return false
	}
	if kind == "avatar" && strings.HasPrefix(channelID, "instagram_") {
		if downloaded, _ := m.downloadInstagramProfileAvatar(ctx, channelID, dir); downloaded {
			return true
		}
	}
	if m.downloader.HTTP == nil {
		return false
	}
	start := time.Now()
	dlURL := url
	// Twitter avatar URLs from fxtwitter use the _normal 48x48 variant;
	// upgrade to 400x400 so the downloaded file exceeds the placeholder
	// size threshold.
	if kind == "avatar" && strings.HasPrefix(channelID, "twitter_") {
		dlURL = upgradeTwimgURL(url)
	}
	tempName := channelID + ".download"
	tmpPath, err := m.downloader.HTTP.DownloadFile(ctx, dlURL, dir, tempName)
	if err != nil {
		if dlURL != url {
			tmpPath, err = m.downloader.HTTP.DownloadFile(ctx, url, dir, tempName)
		}
		if err != nil {
			m.recordProfileMediaOperation(ctx, channelID, kind, url, start, err, 0, 0)
			log.Printf("[profile] %s download %s: %v", kind, channelID, err)
			return false
		}
	}
	if _, err := normalizeDownloadedImage(tmpPath, dir, channelID); err != nil {
		m.recordProfileMediaOperation(ctx, channelID, kind, url, start, err, 0, 0)
		log.Printf("[profile] %s normalize %s: %v", kind, channelID, err)
		return false
	}
	var bytes int64
	if fi, err := os.Stat(tmpPath); err == nil {
		bytes = fi.Size()
	}
	m.recordProfileMediaOperation(ctx, channelID, kind, url, start, nil, 1, bytes)
	if err := m.maintainProfileAssets(channelID); err != nil {
		log.Printf("[profile] maintain %s assets %s: %v", kind, channelID, err)
	}
	// No media_files bookkeeping — the file lives at the conventional disk
	// path and resolveAvatarPath/resolveBannerPath find it there.
	return true
}

func (m *Manager) maintainProfileAssets(channelID string) error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.MaintainChannelProfileAssets(channelID, time.Now().UnixMilli())
}

func (m *Manager) recordProfileMediaOperation(ctx context.Context, channelID, kind, rawURL string, start time.Time, err error, files int, bytes int64) {
	if m == nil || m.db == nil {
		return
	}
	platform := "http"
	if strings.HasPrefix(channelID, "twitter_") {
		platform = "twitter"
	} else if strings.HasPrefix(channelID, "instagram_") {
		platform = "instagram"
	} else if strings.HasPrefix(channelID, "tiktok_") {
		platform = "tiktok"
	} else if strings.HasPrefix(channelID, "youtube_") {
		platform = "youtube"
	}
	status := download.OperationStatusSuccess
	if err != nil {
		status = download.OperationStatusFailure
	}
	_ = m.db.RecordDownloaderOperation(ctx, model.DownloaderOperation{
		Operation:   "profile." + kind,
		Platform:    platform,
		Subject:     channelID,
		Tool:        "http",
		StartedAtMs: start.UnixMilli(),
		EndedAtMs:   time.Now().UnixMilli(),
		Status:      status,
		ErrorKind:   download.ClassifyError(err, nil),
		Error:       download.RedactText(errorText(err)),
		ItemCount:   1,
		FileCount:   files,
		MediaCount:  files,
		Bytes:       bytes,
		SummaryJSON: download.SummaryJSON(map[string]any{"url": rawURL}),
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (m *Manager) downloadInstagramProfileAvatar(ctx context.Context, channelID, dir string) (bool, error) {
	if m == nil || m.downloader == nil || m.downloader.GalleryDL == nil {
		return false, nil
	}
	handle := strings.TrimPrefix(channelID, "instagram_")
	if handle == "" || handle == channelID {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[profile] instagram avatar mkdir %s: %v", channelID, err)
		return false, err
	}
	tmpDir, err := os.MkdirTemp(dir, ".igloo-instagram-avatar-*")
	if err != nil {
		log.Printf("[profile] instagram avatar tmpdir %s: %v", channelID, err)
		return false, err
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	var cookieSets []download.CookieSet
	if m.db != nil {
		cookieSets = m.cookieSetsFor("instagram")
	} else if m.cfg != nil && m.cfg.CookiesDir != "" {
		for _, path := range cookieFileCandidates(m.cfg.CookiesDir, "instagram") {
			if _, err := os.Stat(path); err == nil {
				cookieSets = append(cookieSets, download.CookieSet{File: path})
			}
		}
	}
	if len(cookieSets) == 0 {
		cookieSets = []download.CookieSet{{}}
	}

	var lastErr error
	for i, cookies := range cookieSets {
		paths, err := m.downloader.GalleryDL.Download(ctx, "https://www.instagram.com/"+handle+"/avatar", tmpDir, channelID, cookies.File, cookies.Browser)
		if err != nil {
			lastErr = err
			if i+1 < len(cookieSets) && download.ClassifyError(err, nil) == download.ErrorKindAuth {
				continue
			}
			break
		}
		for _, path := range paths {
			if _, err := normalizeDownloadedImage(path, dir, channelID); err == nil {
				if err := m.maintainProfileAssets(channelID); err != nil {
					log.Printf("[profile] maintain instagram avatar assets %s: %v", channelID, err)
				}
				return true, nil
			} else {
				lastErr = err
			}
		}
	}
	if lastErr != nil {
		log.Printf("[profile] instagram avatar gallery-dl %s: %v", channelID, lastErr)
	}
	return false, lastErr
}

func (m *Manager) recordProfileFetchError(channelID string, existing *model.ChannelProfile, err error, now time.Time) {
	if errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		log.Printf("[profile] canceled %s: %v", channelID, err)
		return
	}
	tombstone := errors.Is(err, fetchprofile.ErrNotFound)
	if tombstone && strings.HasPrefix(channelID, "twitter_") {
		tombstone = false
	}
	failCount := 1
	if existing != nil {
		failCount = existing.FailCount + 1
	}
	retryDelay := time.Duration(math.Min(
		float64(time.Minute)*math.Pow(2, float64(failCount)),
		float64(profileMaxBackoff),
	))
	next := now.Add(retryDelay)
	row := model.ChannelProfile{
		ChannelID:   channelID,
		Platform:    platformForChannelID(channelID),
		FailCount:   failCount,
		NextRetryAt: &next,
		Tombstone:   tombstone,
	}
	if existing != nil {
		row.Platform = existing.Platform
		row.Handle = existing.Handle
		row.DisplayName = existing.DisplayName
		row.Bio = existing.Bio
		row.Website = existing.Website
		row.Followers = existing.Followers
		row.Following = existing.Following
		row.Verified = existing.Verified
		row.VerifiedType = existing.VerifiedType
		row.Protected = existing.Protected
		row.AvatarURL = existing.AvatarURL
		row.BannerURL = existing.BannerURL
		row.FetchedAt = existing.FetchedAt
	}
	if e := m.db.UpsertChannelProfile(row); e != nil {
		log.Printf("[profile] upsert on error %s: %v", channelID, e)
	}
	if tombstone {
		log.Printf("[profile] tombstoned %s: %v", channelID, err)
	} else {
		log.Printf("[profile] transient error %s (backoff %s): %v", channelID, retryDelay, err)
	}
}

func (m *Manager) recordInstagramAvatarFallbackError(channelID string, existing *model.ChannelProfile, err error, now time.Time) {
	if err == nil || m == nil || m.db == nil {
		return
	}
	next := now.Add(instagramProfileBackoff)
	failCount := 1
	row := model.ChannelProfile{
		ChannelID:   channelID,
		Platform:    "instagram",
		Handle:      strings.TrimPrefix(channelID, "instagram_"),
		FailCount:   failCount,
		NextRetryAt: &next,
	}
	if existing != nil {
		row.Platform = existing.Platform
		if row.Platform == "" {
			row.Platform = "instagram"
		}
		row.Handle = existing.Handle
		if row.Handle == "" {
			row.Handle = strings.TrimPrefix(channelID, "instagram_")
		}
		row.DisplayName = existing.DisplayName
		row.Bio = existing.Bio
		row.Website = existing.Website
		row.Followers = existing.Followers
		row.Following = existing.Following
		row.Verified = existing.Verified
		row.VerifiedType = existing.VerifiedType
		row.Protected = existing.Protected
		row.AvatarURL = existing.AvatarURL
		row.BannerURL = existing.BannerURL
		row.FetchedAt = existing.FetchedAt
		row.Tombstone = existing.Tombstone
		row.FailCount = existing.FailCount + 1
	}
	if e := m.db.UpsertChannelProfile(row); e != nil {
		log.Printf("[profile] upsert instagram avatar fallback error %s: %v", channelID, e)
		return
	}
	log.Printf("[profile] instagram avatar fallback error %s (backoff %s): %v", channelID, instagramProfileBackoff, err)
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
