package worker

import (
	"context"
	"errors"
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
	discoveryMaintenanceInterval   = 10 * time.Minute
	discoveryFallbackWakeDelay     = time.Minute
	discoveryMaxScheduledWakeDelay = 30 * time.Minute
	discoveryChannelCheckTimeout   = 2 * time.Minute
	discoveryCurrentHeadLimit      = 8

	sourceComponentReposts = "reposts"
	sourceComponentTagged  = "tagged"
	sourceComponentStories = "stories"
)

type discoveryPlatformState struct {
	active    bool
	lastStart time.Time
}

type discoveryResult struct {
	platform string
}

func (m *Manager) runScheduler(ctx context.Context) {
	states := make(map[string]*discoveryPlatformState, len(discoveryPlatforms()))
	for _, platform := range discoveryPlatforms() {
		states[platform] = &discoveryPlatformState{}
	}
	completed := make(chan discoveryResult, len(states))
	var workers sync.WaitGroup
	defer workers.Wait()

	log.Printf("[scheduler] platform scheduler started")
	m.setStatus("scheduler", workerStatus("scheduler", true, "platform scheduler running", ""))
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

		nextWake := nextMaintenance
		started := false
		for _, platform := range discoveryPlatforms() {
			state := states[platform]
			if state.active || (m.cfg != nil && !m.cfg.PlatformEnabled(platform)) {
				continue
			}
			channel, count, err := m.db.NextSubscribedChannel(platform)
			if err != nil {
				log.Printf("[scheduler] next %s channel: %v", platform, err)
				candidate := now.Add(discoveryFallbackWakeDelay)
				if candidate.Before(nextWake) {
					nextWake = candidate
				}
				continue
			}
			if channel == nil || count == 0 {
				continue
			}
			readyAt := discoveryReadyAt(*channel, count, m.platformFetchDelay(platform), state.lastStart)
			if readyAt.After(now) {
				if readyAt.Before(nextWake) {
					nextWake = readyAt
				}
				continue
			}

			state.active = true
			state.lastStart = now
			started = true
			workers.Add(1)
			go func(platform string, channel model.Channel) {
				defer workers.Done()
				m.processDiscoveryChannel(ctx, platform, channel)
				select {
				case completed <- discoveryResult{platform: platform}:
				case <-ctx.Done():
				}
			}(platform, *channel)
		}
		if started {
			continue
		}

		wait := time.Until(nextWake)
		if wait <= 0 {
			wait = time.Second
		}
		if wait > discoveryMaxScheduledWakeDelay {
			wait = discoveryMaxScheduledWakeDelay
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case result := <-completed:
			timer.Stop()
			if state := states[result.platform]; state != nil {
				state.active = false
			}
		case <-m.discoveryKick:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func discoveryReadyAt(channel model.Channel, channelCount int, delay time.Duration, lastStart time.Time) time.Time {
	var readyAt time.Time
	if channel.LastChecked != nil && !channel.LastChecked.IsZero() {
		readyAt = channel.LastChecked.Add(delay * time.Duration(channelCount))
	}
	if startReady := lastStart.Add(delay); startReady.After(readyAt) {
		readyAt = startReady
	}
	return readyAt
}

func (m *Manager) TriggerDownloadCycle(force bool) {
	if force {
		for _, platform := range discoveryPlatforms() {
			if m.cfg != nil && !m.cfg.PlatformEnabled(platform) {
				continue
			}
			if _, err := m.db.ClearPlatformChecked(platform); err != nil {
				log.Printf("[scheduler] force %s refresh: %v", platform, err)
			}
		}
	}
	m.KickDiscovery()
}

func (m *Manager) runDiscoveryMaintenance() {
	if n, err := m.db.MaintainVideoRetention(time.Now().UnixMilli()); err != nil {
		log.Printf("[scheduler] MaintainVideoRetention: %v", err)
	} else if n > 0 {
		log.Printf("[scheduler] retired %d expired or unowned videos", n)
	}
}

func (m *Manager) processDiscoveryChannel(ctx context.Context, platform string, channel model.Channel) {
	if ctx.Err() != nil || !m.db.IsChannelFollowed(channel.ChannelID) {
		return
	}
	if platform == "twitter" || (m.cfg != nil && !m.cfg.PlatformEnabled(platform)) {
		return
	}

	started := time.Now()
	log.Printf("[scheduler] checking %s (%s)", channel.Name, channel.ChannelID)
	m.emitSchedulerEvent(fmt.Sprintf("Checking: %s", channel.Name), "start", channel.ChannelID, platform)

	checkCtx, cancel := context.WithTimeout(ctx, discoveryChannelCheckTimeout)
	snapshot, fetchErr := m.checkChannel(checkCtx, channel)
	cancel()
	if ctx.Err() != nil || !m.db.IsChannelFollowed(channel.ChannelID) {
		return
	}
	if platform == "tiktok" || platform == "instagram" {
		storyCtx, storyCancel := context.WithTimeout(ctx, discoveryChannelCheckTimeout)
		storyWindow, storyErr := m.nativeStoryWindow(storyCtx, channel)
		storyCancel()
		snapshot.Windows = append(snapshot.Windows, storyWindow)
		fetchErr = errors.Join(fetchErr, storyErr)
	}
	if ctx.Err() != nil || !m.db.IsChannelFollowed(channel.ChannelID) {
		return
	}

	added, reconcileErr := m.applyDiscoverySnapshot(channel, snapshot)
	if added > 0 {
		m.KickMediaWork()
	}
	if reconcileErr != nil {
		log.Printf("[scheduler] apply %s failed: %v", channel.Name, reconcileErr)
		m.emitSchedulerEvent(fmt.Sprintf("Check failed: %s — %v", channel.Name, reconcileErr), "error", channel.ChannelID, platform)
		m.setStatus("scheduler", workerStatus("scheduler", true, fmt.Sprintf("failed %s", channel.ChannelID), reconcileErr.Error()))
		return
	}

	if fetchErr != nil {
		log.Printf("[scheduler] partial check %s: %v", channel.Name, fetchErr)
		m.emitSchedulerEvent(fmt.Sprintf("Partial check: %s — %v", channel.Name, fetchErr), "error", channel.ChannelID, platform)
	} else if added > 0 {
		log.Printf("[scheduler] queued %d videos for %s", added, channel.Name)
		m.emitSchedulerEvent(fmt.Sprintf("Queued %d for %s", added, channel.Name), "queue", channel.ChannelID, platform)
	} else {
		m.emitSchedulerEvent(fmt.Sprintf("Up to date: %s", channel.Name), "done", channel.ChannelID, platform)
	}

	elapsed := time.Since(started).Round(time.Millisecond)
	detail := fmt.Sprintf("checked %s: %d queued (%s)", channel.ChannelID, added, elapsed)
	if fetchErr != nil {
		m.setStatus("scheduler", workerStatus("scheduler", true, detail, fetchErr.Error()))
	} else {
		m.setStatus("scheduler", workerStatus("scheduler", true, detail, ""))
	}
}

func (m *Manager) applyDiscoverySnapshot(channel model.Channel, snapshot download.SourceSnapshot) (int, error) {
	added, reconcileErr := m.reconcileSourceSnapshot(channel, snapshot)
	checkedErr := m.db.UpdateChannelChecked(channel.ChannelID)
	return added, errors.Join(reconcileErr, checkedErr)
}

func (m *Manager) platformFetchDelay(platform string) time.Duration {
	secs := m.db.IntSetting(platformFetchDelaySettingKey(platform))
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
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

func discoveryPlatforms() []string {
	return []string{"youtube", "tiktok", "instagram"}
}

func (m *Manager) checkChannel(ctx context.Context, channel model.Channel) (download.SourceSnapshot, error) {
	limit := m.getChannelMaxVideos(channel)
	if m.downloader == nil {
		return download.SourceSnapshot{}, errors.New("download service is unavailable")
	}

	if channel.Platform == "instagram" {
		handle := instagramHandleForChannel(channel)
		if m.downloader.GalleryDL == nil {
			return download.SourceSnapshot{Windows: []download.SourceWindow{
				{Component: download.SourceComponentReels},
				{Component: download.SourceComponentPosts},
				{Component: sourceComponentTagged},
			}}, errors.New("instagram channel checker is unavailable")
		}
		if handle == "" {
			return download.SourceSnapshot{Windows: []download.SourceWindow{
				{Component: download.SourceComponentReels},
				{Component: download.SourceComponentPosts},
				{Component: sourceComponentTagged},
			}}, errors.New("instagram channel handle is empty")
		}
		cookiesFile, cookiesBrowser := m.cookieFileAndBrowserFor("instagram")
		snapshot, channelErr := m.downloader.GalleryDL.InstagramChannel(ctx, handle, limit, cookiesFile, cookiesBrowser)
		snapshot = ensureSourceWindows(snapshot, download.SourceComponentReels, download.SourceComponentPosts)

		taggedWindow := download.SourceWindow{Component: sourceComponentTagged, Complete: true}
		var taggedErr error
		if m.db.InstagramIncludeTaggedForChannel(channel.ChannelID) {
			taggedWindow.Refs, taggedErr = m.downloader.GalleryDL.InstagramTagged(ctx, handle, limit, cookiesFile, cookiesBrowser)
			taggedWindow.Complete = taggedErr == nil
			setIntroducerMetadata(taggedWindow.Refs, channel, handle)
		}
		snapshot.Windows = append(snapshot.Windows, taggedWindow)
		return snapshot, errors.Join(channelErr, taggedErr)
	}

	if m.downloader.YtDlp == nil {
		return download.SourceSnapshot{}, errors.New("yt-dlp channel checker is unavailable")
	}
	url := channel.URL
	if channel.Platform == "youtube" && !strings.HasSuffix(url, "/videos") {
		url = strings.TrimRight(url, "/") + "/videos"
	}
	snapshot, channelErr := m.downloader.YtDlp.ChannelCheck(ctx, url, limit)
	if channelErr != nil && channel.Platform == "youtube" && strings.HasSuffix(url, "/videos") {
		fallback, fallbackErr := m.downloader.YtDlp.ChannelCheck(ctx, strings.TrimSuffix(url, "/videos"), limit)
		if fallbackErr == nil {
			snapshot, channelErr = fallback, nil
		} else {
			if len(fallback.FlattenRefs(0)) > len(snapshot.FlattenRefs(0)) {
				snapshot = fallback
			}
			channelErr = errors.Join(channelErr, fallbackErr)
		}
	}
	snapshot = ensureSourceWindows(snapshot, download.SourceComponentDirect)

	if channel.Platform == "tiktok" {
		repostWindow := download.SourceWindow{Component: sourceComponentReposts, Complete: true}
		var repostErr error
		if m.db.MomentsIncludeRepostsForChannel(channel.ChannelID) {
			if m.downloader.GalleryDL == nil {
				repostWindow.Complete = false
				repostErr = errors.New("tiktok repost checker is unavailable")
			} else {
				handle := tiktokHandleForChannel(channel)
				cookiesFile, _ := m.cookiesFor("tiktok")
				repostWindow.Refs, repostErr = m.downloader.GalleryDL.Reposts(ctx, handle, limit, cookiesFile)
				repostWindow.Complete = repostErr == nil
				setIntroducerMetadata(repostWindow.Refs, channel, handle)
			}
		}
		snapshot.Windows = append(snapshot.Windows, repostWindow)
		channelErr = errors.Join(channelErr, repostErr)
	}
	return snapshot, channelErr
}

func ensureSourceWindows(snapshot download.SourceSnapshot, components ...string) download.SourceSnapshot {
	present := make(map[string]struct{}, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		present[window.Component] = struct{}{}
	}
	for _, component := range components {
		if _, ok := present[component]; !ok {
			snapshot.Windows = append(snapshot.Windows, download.SourceWindow{Component: component})
		}
	}
	return snapshot
}

func setIntroducerMetadata(refs []download.VideoRef, channel model.Channel, handle string) {
	for i := range refs {
		refs[i].IsRepost = true
		if refs[i].ReposterChannelID == "" {
			refs[i].ReposterChannelID = channel.ChannelID
		}
		if refs[i].ReposterHandle == "" {
			refs[i].ReposterHandle = handle
		}
		if refs[i].ReposterDisplayName == "" {
			refs[i].ReposterDisplayName = channel.Name
		}
	}
}

func (m *Manager) ensureIntroducedOwner(ref download.VideoRef) error {
	if ref.ChannelID == "" {
		return nil
	}
	switch {
	case strings.HasPrefix(ref.ChannelID, "tiktok_"):
		if err := m.db.EnsureTikTokChannelForRepost(ref.ChannelID, ref.AuthorHandle, ref.AuthorDisplayName); err != nil {
			return fmt.Errorf("ensure repost author %s: %w", ref.ChannelID, err)
		}
		m.KickProfileJobs()
	case strings.HasPrefix(ref.ChannelID, "instagram_"):
		if err := m.db.EnsureInstagramChannelForTagged(ref.ChannelID, ref.AuthorHandle, ref.AuthorDisplayName, ""); err != nil {
			return fmt.Errorf("ensure tagged owner %s: %w", ref.ChannelID, err)
		}
		m.KickProfileJobs()
	}
	return nil
}

func instagramHandleForChannel(channel model.Channel) string {
	for _, value := range []string{channel.Handle, channel.SourceID} {
		handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "@"))
		if handle != "" {
			return handle
		}
	}
	raw := strings.TrimSpace(channel.URL)
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
	return strings.TrimPrefix(channel.ChannelID, "instagram_")
}

func tiktokHandleForChannel(channel model.Channel) string {
	for _, value := range []string{channel.Handle, channel.SourceID} {
		handle := model.NormalizeTikTokHandle(value)
		if handle != "" && !model.IsTikTokInternalID(handle) {
			return handle
		}
	}
	if idx := strings.LastIndex(channel.URL, "/@"); idx >= 0 {
		rest := channel.URL[idx+2:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			rest = rest[:slash]
		}
		handle := model.NormalizeTikTokHandle(rest)
		if handle != "" && !model.IsTikTokInternalID(handle) {
			return handle
		}
	}
	return model.TikTokHandleFromChannelID(channel.ChannelID)
}

func classifySourceWindowLanes(previousIDs []string, refs []download.VideoRef) map[string]db.DownloadLane {
	previous := make(map[string]struct{}, len(previousIDs))
	for _, id := range previousIDs {
		if id = strings.TrimSpace(id); id != "" {
			previous[id] = struct{}{}
		}
	}
	firstExisting := -1
	for i, ref := range refs {
		if _, ok := previous[strings.TrimSpace(ref.VideoID)]; ok {
			firstExisting = i
			break
		}
	}

	lanes := make(map[string]db.DownloadLane, len(refs))
	firstValid := true
	for i, ref := range refs {
		videoID := strings.TrimSpace(ref.VideoID)
		if videoID == "" {
			continue
		}
		lane := db.DownloadLaneBackfill
		switch {
		case len(previous) == 0 && firstValid:
			lane = db.DownloadLaneCurrent
		case len(previous) > 0 && firstExisting < 0 && i < discoveryCurrentHeadLimit:
			lane = db.DownloadLaneCurrent
		case len(previous) > 0 && i < firstExisting:
			lane = db.DownloadLaneCurrent
		}
		lanes[videoID] = lane
		firstValid = false
	}
	return lanes
}

func (m *Manager) getChannelMaxVideos(channel model.Channel) int {
	if settings, err := m.db.GetChannelSettings(channel.ChannelID); err == nil && settings != nil && settings.MaxVideos > 0 {
		return settings.MaxVideos
	}
	key := "youtube_max_videos"
	if channel.Platform == "tiktok" {
		key = "shorts_max_videos"
	} else if channel.Platform == "instagram" {
		key = "instagram_max_videos"
	}
	return m.db.IntSetting(key)
}

func (m *Manager) reconcileSourceSnapshot(channel model.Channel, snapshot download.SourceSnapshot) (int, error) {
	limit := m.getChannelMaxVideos(channel)
	owners := desiredOwnersForSnapshot(channel, snapshot)
	for _, owner := range owners {
		if owner.channelID == channel.ChannelID {
			continue
		}
		if err := m.ensureIntroducedOwner(owner.ref); err != nil {
			return 0, err
		}
	}
	seen := make(map[string]struct{}, len(snapshot.Windows))
	plans := make([]sourceWindowPlan, 0, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		component := strings.TrimSpace(window.Component)
		if component == "" {
			return 0, errors.New("source window component is empty")
		}
		if _, exists := seen[component]; exists {
			return 0, fmt.Errorf("duplicate source window component %s", component)
		}
		seen[component] = struct{}{}
		window.Component = component
		plan, err := m.buildSourceWindowPlan(channel, window, owners)
		if err != nil {
			return 0, err
		}
		plans = append(plans, plan)
	}
	plans = boundSourceWindowPlans(plans, limit)
	components := make([]db.VideoDesireSnapshot, 0, len(plans))
	for _, plan := range plans {
		components = append(components, db.VideoDesireSnapshot{
			SourceChannelID: channel.ChannelID,
			Component:       plan.window.Component,
			Items:           plan.items,
		})
	}
	added, err := m.db.ReconcileVideoDesireSource(db.VideoDesireSourceSnapshot{
		SourceChannelID: channel.ChannelID,
		Components:      components,
	})
	if err != nil {
		return 0, fmt.Errorf("reconcile source %s: %w", channel.ChannelID, err)
	}
	var provenanceErrors []error
	for _, plan := range plans {
		if !plan.introduced {
			continue
		}
		rows := introducedRowsForSource(channel, plan.window.Refs)
		if plan.window.Complete {
			if _, err := m.db.ReplaceVideoRepostSourcesForReposter(channel.ChannelID, rows); err != nil {
				provenanceErrors = append(provenanceErrors, fmt.Errorf("replace introduced sources %s: %w", channel.ChannelID, err))
			}
		} else if _, err := m.db.UpsertVideoRepostSources(rows); err != nil {
			provenanceErrors = append(provenanceErrors, fmt.Errorf("observe introduced sources %s: %w", channel.ChannelID, err))
		}
	}
	if err := m.db.PruneVideoRepostSourcesForDesires(channel.ChannelID); err != nil {
		provenanceErrors = append(provenanceErrors, fmt.Errorf("prune introduced sources %s: %w", channel.ChannelID, err))
	}
	return added, errors.Join(provenanceErrors...)
}

func (m *Manager) reconcileSourceWindow(channel model.Channel, window download.SourceWindow) (int, error) {
	return m.reconcileSourceSnapshot(channel, download.SourceSnapshot{Windows: []download.SourceWindow{window}})
}

type desiredOwner struct {
	channelID     string
	ref           download.VideoRef
	authoritative bool
}

func desiredOwnersForSnapshot(channel model.Channel, snapshot download.SourceSnapshot) map[string]desiredOwner {
	owners := make(map[string]desiredOwner)
	for _, window := range snapshot.Windows {
		for _, ref := range window.Refs {
			videoID := strings.TrimSpace(ref.VideoID)
			if videoID == "" {
				continue
			}
			if _, exists := owners[videoID]; !exists {
				owners[videoID] = desiredOwner{channelID: channel.ChannelID, ref: ref}
			}
		}
	}
	for _, window := range snapshot.Windows {
		component := strings.TrimSpace(window.Component)
		if component != sourceComponentReposts && component != sourceComponentTagged {
			continue
		}
		for _, ref := range window.Refs {
			videoID := strings.TrimSpace(ref.VideoID)
			ownerID := strings.TrimSpace(ref.ChannelID)
			if videoID == "" || ownerID == "" {
				continue
			}
			if current, exists := owners[videoID]; !exists || current.channelID == channel.ChannelID {
				owners[videoID] = desiredOwner{channelID: ownerID, ref: ref, authoritative: true}
			}
		}
	}
	return owners
}

type sourceWindowPlan struct {
	window     download.SourceWindow
	items      []db.VideoDesire
	introduced bool
}

func (m *Manager) buildSourceWindowPlan(
	channel model.Channel,
	window download.SourceWindow,
	owners map[string]desiredOwner,
) (sourceWindowPlan, error) {
	plan := sourceWindowPlan{
		window:     window,
		introduced: window.Component == sourceComponentReposts || window.Component == sourceComponentTagged,
	}
	previous, err := m.db.GetVideoDesireWindow(channel.ChannelID, window.Component)
	if err != nil {
		return plan, fmt.Errorf("get desired %s window for %s: %w", window.Component, channel.ChannelID, err)
	}
	previousByID := make(map[string]db.VideoDesireWindowItem, len(previous))
	previousIDs := make([]string, 0, len(previous))
	for _, item := range previous {
		previousByID[item.VideoID] = item
		previousIDs = append(previousIDs, item.VideoID)
	}
	lanes := classifySourceWindowLanes(previousIDs, window.Refs)
	refsByID := make(map[string]download.VideoRef, len(window.Refs))
	incomingIDs := make([]string, 0, len(window.Refs))
	for _, ref := range window.Refs {
		videoID := strings.TrimSpace(ref.VideoID)
		if videoID == "" {
			continue
		}
		if _, duplicate := refsByID[videoID]; duplicate {
			continue
		}
		refsByID[videoID] = ref
		incomingIDs = append(incomingIDs, videoID)
	}
	orderedIDs := incomingIDs
	if !window.Complete {
		orderedIDs = mergePartialSourceOrder(previous, incomingIDs)
	}
	desires := make([]db.VideoDesire, 0, len(orderedIDs))
	for position, videoID := range orderedIDs {
		ref, incoming := refsByID[videoID]
		existing, existed := previousByID[videoID]
		if !incoming && !existed {
			continue
		}
		ownerChannelID := channel.ChannelID
		ownerAuthoritative := false
		title := ""
		publishedAtMs := int64(0)
		freshnessAtMs := int64(0)
		if incoming {
			title = ref.Title
			publishedAtMs = ref.PublishedAtMs
			freshnessAtMs = ref.PublishedAtMs
			if ref.RepostedAtMs > 0 {
				freshnessAtMs = ref.RepostedAtMs
			}
			if owner, ok := owners[videoID]; ok {
				ownerChannelID = owner.channelID
				ownerAuthoritative = owner.authoritative
			}
		} else {
			ownerChannelID = existing.OwnerChannelID
			title = existing.Title
			publishedAtMs = existing.PublishedAtMs
			freshnessAtMs = existing.FreshnessAtMs
		}
		if existed {
			if strings.TrimSpace(title) == "" {
				title = existing.Title
			}
			if publishedAtMs <= 0 {
				publishedAtMs = existing.PublishedAtMs
			}
		}
		lane := lanes[videoID]
		if existed {
			lane = existing.Lane
		} else if lane == "" {
			lane = db.DownloadLaneBackfill
		}
		if window.Component == sourceComponentStories {
			lane = db.DownloadLaneCurrent
		}
		desires = append(desires, db.VideoDesire{
			VideoID:            videoID,
			OwnerChannelID:     ownerChannelID,
			OwnerAuthoritative: ownerAuthoritative,
			Title:              title,
			PublishedAtMs:      publishedAtMs,
			FreshnessAtMs:      freshnessAtMs,
			SourcePosition:     position,
			Lane:               lane,
		})
	}
	plan.items = desires
	return plan, nil
}

func boundSourceWindowPlans(plans []sourceWindowPlan, limit int) []sourceWindowPlan {
	for index := range plans {
		if plans[index].window.Component == sourceComponentStories && len(plans[index].items) > nativeStoryFetchLimit {
			plans[index].items = plans[index].items[:nativeStoryFetchLimit]
		}
	}
	if limit <= 0 {
		return plans
	}
	type candidate struct {
		videoID        string
		freshnessAtMs  int64
		sourcePosition int
		componentOrder int
	}
	candidatesByID := make(map[string]candidate)
	for componentOrder, plan := range plans {
		if plan.window.Component == sourceComponentStories {
			continue
		}
		for _, item := range plan.items {
			current, exists := candidatesByID[item.VideoID]
			if !exists {
				candidatesByID[item.VideoID] = candidate{
					videoID: item.VideoID, freshnessAtMs: item.FreshnessAtMs,
					sourcePosition: item.SourcePosition, componentOrder: componentOrder,
				}
				continue
			}
			if item.FreshnessAtMs > current.freshnessAtMs {
				current.freshnessAtMs = item.FreshnessAtMs
			}
			if item.SourcePosition < current.sourcePosition ||
				(item.SourcePosition == current.sourcePosition && componentOrder < current.componentOrder) {
				current.sourcePosition = item.SourcePosition
				current.componentOrder = componentOrder
			}
			candidatesByID[item.VideoID] = current
		}
	}
	candidates := make([]candidate, 0, len(candidatesByID))
	for _, candidate := range candidatesByID {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if (left.freshnessAtMs == 0) != (right.freshnessAtMs == 0) {
			return left.freshnessAtMs == 0
		}
		if left.freshnessAtMs != right.freshnessAtMs {
			return left.freshnessAtMs > right.freshnessAtMs
		}
		if left.sourcePosition != right.sourcePosition {
			return left.sourcePosition < right.sourcePosition
		}
		if left.componentOrder != right.componentOrder {
			return left.componentOrder < right.componentOrder
		}
		return left.videoID < right.videoID
	})
	if len(candidates) <= limit {
		return plans
	}
	kept := make(map[string]struct{}, limit)
	for _, candidate := range candidates[:limit] {
		kept[candidate.videoID] = struct{}{}
	}
	for planIndex := range plans {
		if plans[planIndex].window.Component == sourceComponentStories {
			continue
		}
		items := plans[planIndex].items[:0]
		for _, item := range plans[planIndex].items {
			if _, exists := kept[item.VideoID]; exists {
				items = append(items, item)
			}
		}
		plans[planIndex].items = items
	}
	return plans
}

func mergePartialSourceOrder(previous []db.VideoDesireWindowItem, incoming []string) []string {
	previousSet := make(map[string]struct{}, len(previous))
	for _, item := range previous {
		previousSet[item.VideoID] = struct{}{}
	}
	before := make(map[string][]string)
	var pending []string
	lastAnchor := ""
	for _, videoID := range incoming {
		if _, exists := previousSet[videoID]; !exists {
			pending = append(pending, videoID)
			continue
		}
		if len(pending) > 0 {
			before[videoID] = append(before[videoID], pending...)
			pending = nil
		}
		lastAnchor = videoID
	}
	if lastAnchor == "" {
		ordered := append([]string(nil), incoming...)
		for _, item := range previous {
			ordered = append(ordered, item.VideoID)
		}
		return ordered
	}
	ordered := make([]string, 0, len(previous)+len(incoming))
	for _, item := range previous {
		ordered = append(ordered, before[item.VideoID]...)
		ordered = append(ordered, item.VideoID)
		if item.VideoID == lastAnchor {
			ordered = append(ordered, pending...)
		}
	}
	return ordered
}

func introducedRowsForSource(channel model.Channel, refs []download.VideoRef) []model.VideoRepostSource {
	sourceChannelID := strings.TrimSpace(channel.ChannelID)
	if sourceChannelID == "" {
		return nil
	}
	sourceHandle := ""
	if channel.Platform == "instagram" {
		sourceHandle = instagramHandleForChannel(channel)
	} else if channel.Platform == "tiktok" {
		sourceHandle = tiktokHandleForChannel(channel)
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
			displayName = channel.Name
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
