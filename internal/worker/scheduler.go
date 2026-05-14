package worker

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/model"
)

// runScheduler periodically checks channels for new content.
func (m *Manager) runScheduler(ctx context.Context) {
	// Initial delay to let server start.
	select {
	case <-time.After(15 * time.Second):
	case <-ctx.Done():
		return
	}

	log.Printf("[scheduler] running initial cycle")
	m.runSchedulerCycle(ctx, false)

	for {
		select {
		case <-time.After(60 * time.Second):
			m.runSchedulerCycle(ctx, false)
		case <-ctx.Done():
			return
		}
	}
}

// TriggerDownloadCycle can be called from API handlers.
func (m *Manager) TriggerDownloadCycle(force bool) {
	go func() {
		m.runSchedulerCycle(m.ctx, force)
	}()
}

func (m *Manager) runSchedulerCycle(ctx context.Context, force bool) {
	start := time.Now()
	m.Emit("scheduler", "Starting download cycle", "info")
	m.setStatus("scheduler", workerStatus("scheduler", true, "running cycle", ""))

	// Reset stale queue items.
	if n, err := m.db.ResetStaleDownloadQueueItems(); err != nil {
		log.Printf("[scheduler] ResetStaleDownloadQueueItems: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] reset %d stale download queue items", n)
	}
	if n, err := m.db.ClearFailedDownloadQueueItems(); err != nil {
		log.Printf("[scheduler] ClearFailedDownloadQueueItems: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] reset %d failed download queue items for retry", n)
	}

	// Cleanup temp videos.
	m.cleanupTempVideos()
	if n, err := m.db.DeleteExpiredStoryVideos(time.Now().UnixMilli(), m.cfg.DataDir); err != nil {
		log.Printf("[scheduler] DeleteExpiredStoryVideos: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] deleted %d expired native stories", n)
	}

	channels, err := m.db.GetSubscribedChannels()
	if err != nil {
		log.Printf("[scheduler] GetSubscribedChannels: %v", err)
		return
	}

	byPlatform := m.discoveryChannelsByPlatform(channels, force, time.Now())

	var (
		queuedMu sync.Mutex
		queued   int
		wg       sync.WaitGroup
	)

	for platform, chs := range byPlatform {
		wg.Add(1)
		go func(platform string, chs []model.Channel) {
			defer wg.Done()
			for i, ch := range chs {
				if ctx.Err() != nil {
					return
				}
				if i > 0 {
					if !m.waitForPlatformFetchDelay(ctx, platform) {
						return
					}
				}

				log.Printf("[scheduler] checking %s (%s)", ch.Name, ch.ChannelID)
				m.emitSchedulerEvent(fmt.Sprintf("Checking: %s", ch.Name), "start", ch.ChannelID, ch.Platform)

				refs, err := m.checkChannel(ctx, ch)
				if err != nil {
					log.Printf("[scheduler] check %s failed: %v", ch.Name, err)
					m.emitSchedulerEvent(fmt.Sprintf("Check failed: %s — %v", ch.Name, err), "error", ch.ChannelID, ch.Platform)
					_ = m.db.UpdateChannelChecked(ch.ChannelID)
					continue
				}
				if ch.Platform == "instagram" {
					m.rememberInstagramProfileFromRefs(ch, refs)
				}
				if ch.Platform == "tiktok" || ch.Platform == "instagram" {
					handle := ch.SourceID
					if ch.Platform == "tiktok" {
						handle = tiktokHandleForChannel(ch)
					} else {
						handle = instagramHandleForChannel(ch)
					}
					m.refreshNativeStoriesForChannel(ctx, ch.ChannelID, ch.Platform, handle, ch.Name)
				}

				added := m.reconcileSourceWindow(ch, refs)
				_ = m.db.UpdateChannelChecked(ch.ChannelID)

				if added > 0 {
					queuedMu.Lock()
					queued += added
					queuedMu.Unlock()
					log.Printf("[scheduler] queued %d videos for %s", added, ch.Name)
					m.emitSchedulerEvent(fmt.Sprintf("Queued %d for %s", added, ch.Name), "queue", ch.ChannelID, ch.Platform)
				} else {
					m.emitSchedulerEvent(fmt.Sprintf("Up to date: %s", ch.Name), "done", ch.ChannelID, ch.Platform)
				}
			}
		}(platform, chs)
	}
	wg.Wait()

	if queued > 0 {
		m.KickDownloadPool()
	}

	// Enforce channel limits.
	for _, ch := range channels {
		if ch.Platform == "twitter" {
			continue
		}
		m.enforceChannelLimit(ch)
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	log.Printf("[scheduler] cycle done: queued %d videos (%s)", queued, elapsed)
	m.Emit("scheduler", fmt.Sprintf("Download cycle complete: %d queued", queued), "done")
	m.setStatus("scheduler", workerStatus("scheduler", true,
		fmt.Sprintf("cycle done: %d queued (%s)", queued, elapsed), ""))
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

func (m *Manager) waitForPlatformFetchDelay(ctx context.Context, platform string) bool {
	delay := m.platformFetchDelay(platform)
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) checkChannel(ctx context.Context, ch model.Channel) ([]download.VideoRef, error) {
	url := ch.URL
	if ch.Platform == "youtube" && !strings.HasSuffix(url, "/videos") {
		url = strings.TrimRight(url, "/") + "/videos"
	}

	limit := m.getChannelMaxVideos(ch)
	if ch.Platform == "instagram" && m.downloader != nil && m.downloader.GalleryDL != nil {
		handle := instagramHandleForChannel(ch)
		cookiesFile, _ := m.cookiesFor("instagram")
		refs, err := m.downloader.GalleryDL.InstagramChannel(ctx, handle, limit, cookiesFile)
		if err != nil {
			return refs, err
		}
		if m.db.InstagramIncludeTaggedForChannel(ch.ChannelID) {
			tagged, taggedErr := m.downloader.GalleryDL.InstagramTagged(ctx, handle, limit, cookiesFile)
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
