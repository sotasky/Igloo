package worker

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

const (
	discoveryWorkerCount           = 4
	discoveryChannelQueueStaleAge  = 30 * time.Minute
	discoveryMaintenanceInterval   = 10 * time.Minute
	discoveryFallbackWakeDelay     = time.Minute
	discoveryMaxScheduledWakeDelay = 30 * time.Minute
)

type platformDiscoveryGate struct {
	mu        sync.Mutex
	active    map[string]int
	lastStart map[string]time.Time
}

func newPlatformDiscoveryGate() *platformDiscoveryGate {
	return &platformDiscoveryGate{
		active:    make(map[string]int),
		lastStart: make(map[string]time.Time),
	}
}

func (g *platformDiscoveryGate) eligiblePlatforms(platforms []string, delayFor func(string) time.Duration, now time.Time) []string {
	if g == nil {
		return platforms
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	ready := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if platform == "" || g.active[platform] >= 1 {
			continue
		}
		if last := g.lastStart[platform]; !last.IsZero() && now.Before(last.Add(delayFor(platform))) {
			continue
		}
		ready = append(ready, platform)
	}
	return ready
}

func (g *platformDiscoveryGate) markStart(platform string, now time.Time) {
	if g == nil {
		return
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return
	}
	g.mu.Lock()
	g.active[platform]++
	g.lastStart[platform] = now
	g.mu.Unlock()
}

func (g *platformDiscoveryGate) markDone(platform string) {
	if g == nil {
		return
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return
	}
	g.mu.Lock()
	if g.active[platform] > 0 {
		g.active[platform]--
	}
	g.mu.Unlock()
}

func (g *platformDiscoveryGate) nextReadyDelay(platforms []string, delayFor func(string) time.Duration, now time.Time) time.Duration {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var min time.Duration
	for _, platform := range platforms {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if platform == "" || g.active[platform] > 0 {
			continue
		}
		last := g.lastStart[platform]
		if last.IsZero() {
			return 0
		}
		wait := last.Add(delayFor(platform)).Sub(now)
		if wait <= 0 {
			return 0
		}
		if min == 0 || wait < min {
			min = wait
		}
	}
	return min
}

// runScheduler continuously dispatches non-X platform discovery through a shared worker pool.
func (m *Manager) runScheduler(ctx context.Context) {
	if m.discoveryGate == nil {
		m.discoveryGate = newPlatformDiscoveryGate()
	}
	if m.discoveryJobs == nil {
		m.discoveryJobs = make(chan db.ChannelQueueRow, discoveryWorkerCount)
	}
	if n, err := m.db.ResetProcessingChannelQueueItems(); err != nil {
		log.Printf("[scheduler] ResetProcessingChannelQueueItems: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] reset %d in-flight channel checks on startup", n)
	}
	var workerWG sync.WaitGroup
	for i := 0; i < discoveryWorkerCount; i++ {
		workerWG.Add(1)
		go func(workerID int) {
			defer workerWG.Done()
			m.runDiscoveryWorker(ctx, workerID)
		}(i + 1)
	}
	defer workerWG.Wait()

	log.Printf("[scheduler] discovery dispatcher started")
	m.setStatus("scheduler", workerStatus("scheduler", true, "discovery dispatcher running", ""))
	nextMaintenance := time.Now()

	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		if !now.Before(nextMaintenance) {
			m.runDiscoveryMaintenance()
			nextMaintenance = now.Add(discoveryMaintenanceInterval)
		}
		if queued := m.materializeDiscoveryChannels(now, false); queued > 0 {
			log.Printf("[scheduler] queued %d due channel checks", queued)
		}
		if dispatched := m.dispatchReadyDiscoveryJobs(ctx); dispatched > 0 {
			continue
		}
		wait := m.nextDiscoveryWakeDelay(time.Now(), nextMaintenance)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.discoveryKick:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// TriggerDownloadCycle can be called from API handlers.
func (m *Manager) TriggerDownloadCycle(force bool) {
	if force {
		_ = m.materializeDiscoveryChannels(time.Now(), true)
	}
	m.KickDiscovery()
}

func (m *Manager) runDiscoveryMaintenance() {
	if n, err := m.db.ResetStaleDownloadQueueItems(); err != nil {
		log.Printf("[scheduler] ResetStaleDownloadQueueItems: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] reset %d stale download queue items", n)
	}
	if n, err := m.db.ResetStaleChannelQueueItems(discoveryChannelQueueStaleAge); err != nil {
		log.Printf("[scheduler] ResetStaleChannelQueueItems: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] reset %d stale channel checks", n)
	}
	if n, err := m.db.ClearFailedDownloadQueueItems(); err != nil {
		log.Printf("[scheduler] ClearFailedDownloadQueueItems: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] reset %d failed download queue items for retry", n)
	}

	m.cleanupTempVideos()
	dataDir := ""
	if m.cfg != nil {
		dataDir = m.cfg.DataDir
	}
	if n, err := m.db.DeleteExpiredStoryVideos(time.Now().UnixMilli(), dataDir); err != nil {
		log.Printf("[scheduler] DeleteExpiredStoryVideos: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] deleted %d expired native stories", n)
	}
}

func (m *Manager) materializeDiscoveryChannels(now time.Time, force bool) int {
	channels, err := m.db.GetSubscribedChannels()
	if err != nil {
		log.Printf("[scheduler] GetSubscribedChannels: %v", err)
		return 0
	}
	byPlatform := m.discoveryChannelsByPlatform(channels, force, now)
	queued := 0
	for _, chs := range byPlatform {
		for _, ch := range chs {
			changed, err := m.db.EnqueueChannelCheck(ch.ChannelID, 0)
			if err != nil {
				log.Printf("[scheduler] AddChannelToQueue %s: %v", ch.ChannelID, err)
				continue
			}
			if changed {
				queued++
			}
		}
	}
	return queued
}

func (m *Manager) dispatchReadyDiscoveryJobs(ctx context.Context) int {
	if m.discoveryJobs == nil || m.discoveryGate == nil {
		return 0
	}
	dispatched := 0
	for len(m.discoveryJobs) < cap(m.discoveryJobs) {
		ready := m.discoveryGate.eligiblePlatforms(discoveryPlatforms(), m.platformFetchDelay, time.Now())
		if len(ready) == 0 {
			return dispatched
		}
		job, ok, err := m.db.ClaimNextChannelQueue(ready)
		if err != nil {
			log.Printf("[scheduler] ClaimNextChannelQueue: %v", err)
			return dispatched
		}
		if !ok {
			return dispatched
		}
		start := time.Now()
		m.discoveryGate.markStart(job.Platform, start)
		select {
		case m.discoveryJobs <- job:
			dispatched++
		case <-ctx.Done():
			m.discoveryGate.markDone(job.Platform)
			return dispatched
		}
	}
	return dispatched
}

func (m *Manager) runDiscoveryWorker(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-m.discoveryJobs:
			m.processDiscoveryJob(ctx, workerID, job)
			if m.discoveryGate != nil {
				m.discoveryGate.markDone(job.Platform)
			}
			m.KickDiscovery()
		}
	}
}

func (m *Manager) processDiscoveryJob(ctx context.Context, workerID int, job db.ChannelQueueRow) {
	if ctx.Err() != nil {
		return
	}
	start := time.Now()
	ch, err := m.db.GetChannel(job.ChannelID)
	if err != nil || ch == nil {
		log.Printf("[scheduler] worker %d missing channel %s: %v", workerID, job.ChannelID, err)
		_ = m.db.CompleteChannelQueue(job.ChannelID)
		return
	}
	if ch.Platform == "twitter" || (m.cfg != nil && !m.cfg.PlatformEnabled(ch.Platform)) {
		_ = m.db.CompleteChannelQueue(job.ChannelID)
		return
	}

	log.Printf("[scheduler] worker %d checking %s (%s)", workerID, ch.Name, ch.ChannelID)
	m.emitSchedulerEvent(fmt.Sprintf("Checking: %s", ch.Name), "start", ch.ChannelID, ch.Platform)

	refs, err := m.checkChannel(ctx, *ch)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("[scheduler] check %s failed: %v", ch.Name, err)
		m.emitSchedulerEvent(fmt.Sprintf("Check failed: %s — %v", ch.Name, err), "error", ch.ChannelID, ch.Platform)
		_ = m.db.UpdateChannelChecked(ch.ChannelID)
		_ = m.db.CompleteChannelQueue(ch.ChannelID)
		m.setStatus("scheduler", workerStatus("scheduler", true, fmt.Sprintf("worker %d failed %s", workerID, ch.ChannelID), err.Error()))
		return
	}
	if ch.Platform == "instagram" {
		m.rememberInstagramProfileFromRefs(*ch, refs)
	}
	if ch.Platform == "tiktok" || ch.Platform == "instagram" {
		handle := tiktokHandleForChannel(*ch)
		if ch.Platform == "instagram" {
			handle = instagramHandleForChannel(*ch)
		}
		m.refreshNativeStoriesForChannel(ctx, ch.ChannelID, ch.Platform, handle, ch.Name)
	}

	added := m.reconcileSourceWindow(*ch, refs)
	_ = m.db.UpdateChannelChecked(ch.ChannelID)
	m.enforceChannelLimit(*ch)
	_ = m.db.CompleteChannelQueue(ch.ChannelID)
	if added > 0 {
		log.Printf("[scheduler] queued %d videos for %s", added, ch.Name)
		m.emitSchedulerEvent(fmt.Sprintf("Queued %d for %s", added, ch.Name), "queue", ch.ChannelID, ch.Platform)
		m.KickDownloadPool()
	} else {
		m.emitSchedulerEvent(fmt.Sprintf("Up to date: %s", ch.Name), "done", ch.ChannelID, ch.Platform)
	}
	elapsed := time.Since(start).Round(time.Millisecond)
	m.setStatus("scheduler", workerStatus("scheduler", true, fmt.Sprintf("worker %d checked %s: %d queued (%s)", workerID, ch.ChannelID, added, elapsed), ""))
}

func (m *Manager) nextDiscoveryWakeDelay(now, nextMaintenance time.Time) time.Duration {
	wait := m.nextDiscoveryDueDelay(now)
	if m.discoveryGate != nil {
		if gateWait := m.discoveryGate.nextReadyDelay(discoveryPlatforms(), m.platformFetchDelay, now); gateWait > 0 && (wait == 0 || gateWait < wait) {
			wait = gateWait
		}
	}
	if maintWait := nextMaintenance.Sub(now); maintWait > 0 && (wait == 0 || maintWait < wait) {
		wait = maintWait
	}
	if wait <= 0 {
		return time.Second
	}
	if wait > discoveryMaxScheduledWakeDelay {
		return discoveryMaxScheduledWakeDelay
	}
	return wait
}

func (m *Manager) nextDiscoveryDueDelay(now time.Time) time.Duration {
	channels, err := m.db.GetSubscribedChannels()
	if err != nil {
		log.Printf("[scheduler] GetSubscribedChannels: %v", err)
		return discoveryFallbackWakeDelay
	}
	byPlatform := make(map[string][]model.Channel)
	for _, ch := range channels {
		if ch.Platform == "twitter" {
			continue
		}
		if m.cfg != nil && !m.cfg.PlatformEnabled(ch.Platform) {
			continue
		}
		byPlatform[ch.Platform] = append(byPlatform[ch.Platform], ch)
	}
	var wait time.Duration
	for platform, chs := range byPlatform {
		interval := m.platformDiscoveryCycleInterval(platform, len(chs))
		if interval <= 0 {
			return 0
		}
		for _, ch := range chs {
			if ch.LastChecked == nil {
				return 0
			}
			remaining := interval - now.Sub(*ch.LastChecked)
			if remaining <= 0 {
				return 0
			}
			if wait == 0 || remaining < wait {
				wait = remaining
			}
		}
	}
	if wait == 0 {
		return discoveryFallbackWakeDelay
	}
	return wait
}

func (m *Manager) platformFetchDelay(platform string) time.Duration {
	key := platformFetchDelaySettingKey(platform)
	secs := m.db.IntSetting(key)
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
}

func (m *Manager) discoveryChannelsByPlatform(channels []model.Channel, force bool, now time.Time) map[string][]model.Channel {
	allByPlatform := make(map[string][]model.Channel)
	for _, ch := range channels {
		if ch.Platform == "twitter" {
			continue
		}
		if m.cfg != nil && !m.cfg.PlatformEnabled(ch.Platform) {
			continue
		}
		allByPlatform[ch.Platform] = append(allByPlatform[ch.Platform], ch)
	}

	byPlatform := make(map[string][]model.Channel, len(allByPlatform))
	for platform, chs := range allByPlatform {
		sortChannelsByLastChecked(chs)
		if force {
			byPlatform[platform] = chs
			continue
		}
		ready := readyDiscoveryChannels(chs, m.platformDiscoveryCycleInterval(platform, len(chs)), now)
		if len(ready) > 0 {
			byPlatform[platform] = ready
		}
	}
	return byPlatform
}

func platformFetchDelaySettingKey(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "tiktok":
		return "tiktok_fetch_delay"
	case "instagram":
		return "instagram_fetch_delay"
	default:
		return "youtube_fetch_delay"
	}
}

func (m *Manager) platformDiscoveryCycleInterval(platform string, channelCount int) time.Duration {
	if channelCount <= 0 {
		return 0
	}
	return m.platformFetchDelay(platform) * time.Duration(channelCount)
}

func discoveryPlatforms() []string {
	return []string{"youtube", "tiktok", "instagram"}
}

func readyDiscoveryChannels(channels []model.Channel, interval time.Duration, now time.Time) []model.Channel {
	if len(channels) == 0 {
		return nil
	}
	if interval <= 0 {
		return append([]model.Channel(nil), channels...)
	}
	ready := make([]model.Channel, 0, len(channels))
	for _, ch := range channels {
		if ch.LastChecked == nil || now.Sub(*ch.LastChecked) >= interval {
			ready = append(ready, ch)
		}
	}
	return ready
}

func sortChannelsByLastChecked(channels []model.Channel) {
	sort.SliceStable(channels, func(i, j int) bool {
		left := channels[i].LastChecked
		right := channels[j].LastChecked
		switch {
		case left == nil && right == nil:
			return channels[i].ChannelID < channels[j].ChannelID
		case left == nil:
			return true
		case right == nil:
			return false
		default:
			if left.Equal(*right) {
				return channels[i].ChannelID < channels[j].ChannelID
			}
			return left.Before(*right)
		}
	})
}

func (m *Manager) checkChannel(ctx context.Context, ch model.Channel) ([]download.VideoRef, error) {
	url := ch.URL
	if ch.Platform == "youtube" && !strings.HasSuffix(url, "/videos") {
		url = strings.TrimRight(url, "/") + "/videos"
	}

	limit := m.getChannelMaxVideos(ch)
	if ch.Platform == "instagram" && m.downloader != nil && m.downloader.GalleryDL != nil {
		handle := instagramHandleForChannel(ch)
		cookiesFile, cookiesBrowser := m.cookieFileAndBrowserFor("instagram")
		refs, err := m.downloader.GalleryDL.InstagramChannel(ctx, handle, limit, cookiesFile, cookiesBrowser)
		if err != nil {
			return refs, err
		}
		if m.db.InstagramIncludeTaggedForChannel(ch.ChannelID) {
			tagged, taggedErr := m.downloader.GalleryDL.InstagramTagged(ctx, handle, limit, cookiesFile, cookiesBrowser)
			if taggedErr != nil {
				log.Printf("[scheduler] instagram tagged check %s: %v", ch.ChannelID, taggedErr)
			} else if len(tagged) > 0 {
				for i := range tagged {
					if tagged[i].ReposterChannelID == "" {
						tagged[i].ReposterChannelID = ch.ChannelID
					}
					if tagged[i].ReposterHandle == "" {
						tagged[i].ReposterHandle = handle
					}
					if tagged[i].ReposterDisplayName == "" {
						tagged[i].ReposterDisplayName = ch.Name
					}
					m.ensureIntroducedOwner(tagged[i])
				}
				refs = mergeVideoRefs(refs, tagged, limit)
			}
		}
		return refs, nil
	}

	refs, err := m.downloader.YtDlp.ChannelCheck(ctx, url, limit)
	if err != nil && ch.Platform == "youtube" && strings.HasSuffix(url, "/videos") {
		refs, err = m.downloader.YtDlp.ChannelCheck(ctx, strings.TrimSuffix(url, "/videos"), limit)
	}
	if err != nil {
		return refs, err
	}
	if ch.Platform == "tiktok" && m.downloader != nil && m.downloader.GalleryDL != nil && m.db.MomentsIncludeRepostsForChannel(ch.ChannelID) {
		handle := tiktokHandleForChannel(ch)
		cookiesFile, _ := m.cookiesFor("tiktok")
		reposts, repostErr := m.downloader.GalleryDL.Reposts(ctx, handle, limit, cookiesFile)
		if repostErr != nil {
			log.Printf("[scheduler] tiktok repost check %s: %v", ch.ChannelID, repostErr)
		} else if len(reposts) > 0 {
			for i := range reposts {
				if reposts[i].ReposterChannelID == "" {
					reposts[i].ReposterChannelID = ch.ChannelID
				}
				if reposts[i].ReposterHandle == "" {
					reposts[i].ReposterHandle = handle
				}
				if reposts[i].ReposterDisplayName == "" {
					reposts[i].ReposterDisplayName = ch.Name
				}
				m.ensureIntroducedOwner(reposts[i])
			}
			refs = mergeVideoRefs(refs, reposts, limit)
		}
	}
	return refs, err
}

func (m *Manager) ensureIntroducedOwner(ref download.VideoRef) {
	if ref.ChannelID == "" {
		return
	}
	switch {
	case strings.HasPrefix(ref.ChannelID, "tiktok_"):
		if err := m.db.EnsureTikTokChannelForRepost(ref.ChannelID, ref.AuthorHandle, ref.AuthorDisplayName); err != nil {
			log.Printf("[scheduler] ensure repost author %s: %v", ref.ChannelID, err)
			return
		}
		m.RequestAvatar(ref.ChannelID)
	case strings.HasPrefix(ref.ChannelID, "instagram_"):
		if err := m.db.EnsureInstagramChannelForTagged(ref.ChannelID, ref.AuthorHandle, ref.AuthorDisplayName, ""); err != nil {
			log.Printf("[scheduler] ensure tagged owner %s: %v", ref.ChannelID, err)
			return
		}
		m.RequestAvatar(ref.ChannelID)
	}
}

func (m *Manager) rememberInstagramProfileFromRefs(ch model.Channel, refs []download.VideoRef) {
	if len(refs) == 0 {
		return
	}
	handle := instagramHandleForChannel(ch)
	profile := model.ChannelProfile{
		ChannelID:   ch.ChannelID,
		Platform:    "instagram",
		Handle:      handle,
		DisplayName: ch.Name,
	}
	if existing, err := m.db.GetChannelProfile(ch.ChannelID); err != nil {
		log.Printf("[scheduler] get instagram profile %s: %v", ch.ChannelID, err)
	} else if existing != nil {
		profile.Bio = existing.Bio
		profile.Website = existing.Website
		profile.Followers = existing.Followers
		profile.Following = existing.Following
		profile.Verified = existing.Verified
		profile.VerifiedType = existing.VerifiedType
		profile.Protected = existing.Protected
		profile.AvatarURL = existing.AvatarURL
		profile.BannerURL = existing.BannerURL
		profile.FetchedAt = existing.FetchedAt
		profile.FailCount = existing.FailCount
		profile.NextRetryAt = existing.NextRetryAt
		profile.Tombstone = existing.Tombstone
	}
	for _, ref := range refs {
		if ref.IsRepost && ref.ReposterChannelID == ch.ChannelID {
			if ref.ReposterHandle != "" {
				profile.Handle = ref.ReposterHandle
			}
			if ref.ReposterDisplayName != "" {
				profile.DisplayName = ref.ReposterDisplayName
			}
			if ref.ReposterAvatarURL != "" {
				profile.AvatarURL = ref.ReposterAvatarURL
			}
			if profile.Handle != "" && profile.DisplayName != "" && profile.AvatarURL != "" {
				break
			}
			continue
		}
		if ref.IsRepost && ref.ChannelID != "" && ref.ChannelID != ch.ChannelID {
			continue
		}
		if ref.AuthorHandle != "" {
			profile.Handle = ref.AuthorHandle
		}
		if ref.AuthorDisplayName != "" {
			profile.DisplayName = ref.AuthorDisplayName
		}
		if ref.AuthorAvatarURL != "" {
			profile.AvatarURL = ref.AuthorAvatarURL
		}
		if profile.Handle != "" && profile.DisplayName != "" && profile.AvatarURL != "" {
			break
		}
	}
	if profile.Handle == "" {
		profile.Handle = strings.TrimPrefix(ch.ChannelID, "instagram_")
	}
	if profile.DisplayName == "" {
		profile.DisplayName = profile.Handle
	}
	if err := m.db.UpsertChannelProfile(profile); err != nil {
		log.Printf("[scheduler] upsert instagram profile %s: %v", ch.ChannelID, err)
		return
	}
	if profile.AvatarURL != "" {
		m.RequestAvatar(ch.ChannelID)
	}
}

func instagramHandleForChannel(ch model.Channel) string {
	for _, value := range []string{ch.Handle, ch.SourceID} {
		handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "@"))
		if handle != "" {
			return handle
		}
	}
	raw := strings.TrimSpace(ch.URL)
	for _, prefix := range []string{"https://www.instagram.com/", "https://instagram.com/", "http://www.instagram.com/", "http://instagram.com/"} {
		if strings.HasPrefix(raw, prefix) {
			rest := strings.TrimPrefix(raw, prefix)
			if slash := strings.Index(rest, "/"); slash >= 0 {
				rest = rest[:slash]
			}
			if rest != "" {
				return strings.ToLower(rest)
			}
		}
	}
	return strings.TrimPrefix(ch.ChannelID, "instagram_")
}

func tiktokHandleForChannel(ch model.Channel) string {
	for _, value := range []string{ch.Handle, ch.SourceID} {
		handle := model.NormalizeTikTokHandle(value)
		if handle != "" && !model.IsTikTokInternalID(handle) {
			return handle
		}
	}
	if idx := strings.LastIndex(ch.URL, "/@"); idx >= 0 {
		rest := ch.URL[idx+2:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			rest = rest[:slash]
		}
		handle := model.NormalizeTikTokHandle(rest)
		if handle != "" && !model.IsTikTokInternalID(handle) {
			return handle
		}
	}
	return model.TikTokHandleFromChannelID(ch.ChannelID)
}

func mergeVideoRefs(primary, extra []download.VideoRef, limit int) []download.VideoRef {
	if limit <= 0 {
		limit = len(primary) + len(extra)
	}
	seen := map[string]struct{}{}
	merged := make([]download.VideoRef, 0, len(primary)+len(extra))
	for _, group := range [][]download.VideoRef{primary, extra} {
		for _, ref := range group {
			if strings.TrimSpace(ref.VideoID) == "" {
				continue
			}
			if _, ok := seen[ref.VideoID]; ok {
				continue
			}
			seen[ref.VideoID] = struct{}{}
			merged = append(merged, ref)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		left := videoRefMomentMs(merged[i])
		right := videoRefMomentMs(merged[j])
		if left == right {
			return false
		}
		if left == 0 {
			return false
		}
		if right == 0 {
			return true
		}
		return left > right
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

func videoRefMomentMs(ref download.VideoRef) int64 {
	if ref.IsRepost && ref.RepostedAtMs > 0 {
		return ref.RepostedAtMs
	}
	return ref.PublishedAtMs
}

func (m *Manager) getChannelMaxVideos(ch model.Channel) int {
	if s, err := m.db.GetChannelSettings(ch.ChannelID); err == nil && s != nil && s.MaxVideos > 0 {
		return s.MaxVideos
	}
	key := "youtube_max_videos"
	if ch.Platform == "tiktok" {
		key = "shorts_max_videos"
	} else if ch.Platform == "instagram" {
		key = "instagram_max_videos"
	}
	return m.db.IntSetting(key)
}

func (m *Manager) reconcileSourceWindow(ch model.Channel, refs []download.VideoRef) int {
	maxVideos := m.getChannelMaxVideos(ch)
	if len(refs) > maxVideos {
		refs = refs[:maxVideos]
	}
	if len(refs) == 0 {
		return 0
	}
	m.primeShortFormMentionProfiles(ch.Platform, refs)

	allowedIDs := make([]string, 0, len(refs))
	var ownerAllowedIDs []string
	for _, r := range refs {
		if strings.TrimSpace(r.VideoID) == "" {
			continue
		}
		allowedIDs = append(allowedIDs, r.VideoID)
		if r.IsRepost && r.ChannelID != "" && r.ChannelID != ch.ChannelID {
			continue
		}
		ownerAllowedIDs = append(ownerAllowedIDs, r.VideoID)
	}

	if ch.Platform == "tiktok" || ch.Platform == "instagram" {
		m.syncIntroducedSourceWindow(ch, refs, allowedIDs)
	}

	// Prune download queue.
	if len(ownerAllowedIDs) > 0 {
		if pruned, _ := m.db.PruneDownloadQueue(ch.ChannelID, ownerAllowedIDs); pruned > 0 {
			log.Printf("[scheduler] pruned %d queue items for %s", pruned, ch.Name)
		}
	}

	// Queue missing. YouTube: oldest first (reverse). TikTok: as-is (newest first).
	ordered := refs
	if ch.Platform == "youtube" {
		ordered = make([]download.VideoRef, len(refs))
		for i, r := range refs {
			ordered[len(refs)-1-i] = r
		}
	}

	added := 0
	for _, r := range ordered {
		if downloaded, _ := m.db.IsVideoDownloaded(r.VideoID); downloaded {
			continue
		}
		queueChannelID := ch.ChannelID
		if r.IsRepost && r.ChannelID != "" {
			queueChannelID = r.ChannelID
		}
		if err := m.db.AddToDownloadQueueWithPublishedAt(r.VideoID, queueChannelID, r.Title, r.PublishedAtMs); err != nil {
			log.Printf("[scheduler] AddToDownloadQueue %s: %v", r.VideoID, err)
			continue
		}
		added++
	}
	return added
}

func (m *Manager) primeShortFormMentionProfiles(platform string, refs []download.VideoRef) {
	if m == nil || m.db == nil || len(refs) == 0 {
		return
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform != "tiktok" && platform != "instagram" {
		return
	}
	texts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if text := strings.TrimSpace(ref.Title); text != "" && strings.Contains(text, "@") {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return
	}
	channelIDs, n, err := m.db.SeedShortFormMentionProfileRowsForTextsWithIDs(platform, texts)
	if err != nil {
		log.Printf("[scheduler] seed %s mention profiles: %v", platform, err)
	} else if n > 0 {
		log.Printf("[scheduler] seeded %d %s mention profile rows", n, platform)
	}
	for _, channelID := range channelIDs {
		m.RequestAvatar(channelID)
	}
}

func (m *Manager) syncIntroducedSourceWindow(ch model.Channel, refs []download.VideoRef, allowedIDs []string) {
	if m == nil || m.db == nil {
		return
	}
	if pruned, err := m.db.PruneSourceWindowDownloadQueue(ch.ChannelID, allowedIDs); err != nil {
		log.Printf("[scheduler] prune source-window queue %s: %v", ch.ChannelID, err)
	} else if pruned > 0 {
		log.Printf("[scheduler] pruned %d source-window queue items for %s", pruned, ch.Name)
	}
	prunable, err := m.db.GetSourceWindowPrunableVideoIDs(ch.ChannelID, allowedIDs)
	if err != nil {
		log.Printf("[scheduler] source-window prune candidates %s: %v", ch.ChannelID, err)
		return
	}
	rows := introducedRowsForSource(ch, refs)
	if _, err := m.db.ReplaceVideoRepostSourcesForReposter(ch.ChannelID, rows); err != nil {
		log.Printf("[scheduler] replace introduced sources %s: %v", ch.ChannelID, err)
		return
	}
	if len(prunable) == 0 || m.cfg == nil {
		return
	}
	deleted := 0
	for _, videoID := range prunable {
		if err := m.db.DeleteVideoWithFile(videoID, m.cfg.DataDir); err != nil {
			log.Printf("[scheduler] delete source-window excess %s: %v", videoID, err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		log.Printf("[scheduler] deleted %d source-window excess videos for %s", deleted, ch.Name)
	}
}

func introducedRowsForSource(ch model.Channel, refs []download.VideoRef) []model.VideoRepostSource {
	sourceChannelID := strings.TrimSpace(ch.ChannelID)
	if sourceChannelID == "" {
		return nil
	}
	sourceHandle := ""
	switch ch.Platform {
	case "instagram":
		sourceHandle = instagramHandleForChannel(ch)
	case "tiktok":
		sourceHandle = tiktokHandleForChannel(ch)
	}
	rows := make([]model.VideoRepostSource, 0)
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if !ref.IsRepost || strings.TrimSpace(ref.VideoID) == "" || ref.ChannelID == "" || ref.ChannelID == sourceChannelID {
			continue
		}
		if _, ok := seen[ref.VideoID]; ok {
			continue
		}
		seen[ref.VideoID] = struct{}{}
		handle := strings.TrimSpace(ref.ReposterHandle)
		if handle == "" {
			handle = sourceHandle
		}
		displayName := strings.TrimSpace(ref.ReposterDisplayName)
		if displayName == "" {
			displayName = ch.Name
		}
		rows = append(rows, model.VideoRepostSource{
			VideoID:             ref.VideoID,
			ReposterChannelID:   sourceChannelID,
			ReposterHandle:      handle,
			ReposterDisplayName: displayName,
			RepostedAtMs:        ref.RepostedAtMs,
		})
	}
	return rows
}

func (m *Manager) enforceChannelLimit(ch model.Channel) {
	maxVideos := m.getChannelMaxVideos(ch)
	excess, err := m.db.GetExcessVideoIDs(ch.ChannelID, maxVideos)
	if err != nil || len(excess) == 0 {
		return
	}
	for _, videoID := range excess {
		if err := m.db.DeleteVideoWithFile(videoID, m.cfg.DataDir); err != nil {
			log.Printf("[scheduler] delete excess %s: %v", videoID, err)
		}
	}
	log.Printf("[scheduler] deleted %d excess videos for %s", len(excess), ch.Name)
}

func (m *Manager) cleanupTempVideos() {
	temps, err := m.db.GetTempVideos()
	if err != nil {
		log.Printf("[scheduler] GetTempVideos: %v", err)
		return
	}
	cleaned := 0
	for _, v := range temps {
		if v.IsPinned {
			continue
		}
		if v.DownloadedAt.IsZero() || time.Since(v.DownloadedAt) < 24*time.Hour {
			continue
		}
		if err := m.db.DeleteVideoWithFile(v.VideoID, m.cfg.DataDir); err != nil {
			log.Printf("[scheduler] cleanup temp %s: %v", v.VideoID, err)
		} else {
			cleaned++
		}
	}
	if cleaned > 0 {
		log.Printf("[scheduler] cleaned %d expired temp videos", cleaned)
	}
}
