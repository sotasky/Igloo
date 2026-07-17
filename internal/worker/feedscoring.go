package worker

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
	"github.com/screwys/igloo/internal/model"
)

func (m *Manager) runFeedScoringWorker(ctx context.Context) {
	log.Printf("[feed_scoring] worker started")
	periodic := time.NewTicker(time.Hour)
	defer periodic.Stop()

	lastRun := time.Time{}
	var kickTimer *time.Timer
	var kickTimerC <-chan time.Time
	stopKickTimer := func() {
		if kickTimer == nil {
			return
		}
		if !kickTimer.Stop() {
			select {
			case <-kickTimer.C:
			default:
			}
		}
		kickTimer = nil
		kickTimerC = nil
	}
	defer func() {
		stopKickTimer()
	}()

	runNow := func(forceRerank, forceRefill bool) {
		stopKickTimer()
		m.scoreFeedItems(ctx, forceRerank, forceRefill)
		lastRun = time.Now()
	}
	scheduleKick := func() {
		delay := time.Until(lastRun.Add(feedScoringKickMinInterval))
		if delay <= 0 {
			runNow(false, false)
			return
		}
		if kickTimerC == nil {
			kickTimer = time.NewTimer(delay)
			kickTimerC = kickTimer.C
		}
	}
	runNow(true, true)

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.feedScoringKick:
			scheduleKick()
		case <-kickTimerC:
			runNow(false, false)
		case <-periodic.C:
			runNow(true, true)
		}
	}
}

const feedSnapshotBuildTimeout = 30 * time.Second
const feedScoringKickMinInterval = 2 * time.Minute
const feedSnapshotMaxItems = 2000
const feedSnapshotRefillLowWater = 1500
const feedSnapshotRefillBatch = 500
const feedScoringDirtyBatch = 2000
const feedReplyResolutionBatch = 16
const feedReplyResolutionTimeout = 20 * time.Second

func (m *Manager) scoreFeedItems(ctx context.Context, forceRerank, forceRefill bool) {
	start := time.Now()
	m.setStatus("feed_scoring", workerStatus("feed_scoring", true, "scoring", ""))

	dirtyTweetIDs, moreDirty := m.runScoringPhase()
	if moreDirty {
		m.KickFeedScoring()
	}
	visible, err := m.db.CountVisibleFeedRankSnapshotContext(ctx)
	if err != nil {
		log.Printf("[feed_scoring] CountVisibleFeedRankSnapshot: %v", err)
		visible = feedSnapshotRefillLowWater
	}
	needsRefill := forceRefill || visible < feedSnapshotRefillLowWater
	if len(dirtyTweetIDs) == 0 && !forceRerank && !needsRefill {
		m.setStatus("feed_scoring", workerStatus("feed_scoring", true, "idle", ""))
		return
	}

	refillLimit := 0
	if needsRefill {
		refillLimit = feedSnapshotMaxItems - visible
		if refillLimit < feedSnapshotRefillBatch {
			refillLimit = feedSnapshotRefillBatch
		}
		if refillLimit > feedSnapshotMaxItems {
			refillLimit = feedSnapshotMaxItems
		}
	}
	snap := m.runSnapshotPhaseStats(ctx, dirtyTweetIDs, refillLimit, forceRerank || forceRefill)

	totalElapsed := time.Since(start).Round(time.Millisecond)
	detail := fmt.Sprintf("scored=%d refill=%d candidates=%d replies=%d/%d snap=%d/%s query=%s build=%s write=%s total=%s top=%s",
		len(dirtyTweetIDs), refillLimit, snap.candidates, snap.replyAttempts, snap.replyBlocked, snap.count, snap.totalDur, snap.queryDur,
		snap.buildDur, snap.writeDur, totalElapsed, snap.top)
	log.Printf("[feed_scoring] %s", detail)
	m.EmitFeed("feed_scoring", detail, "done")
	summary := fmt.Sprintf("Scored %d \u00b7 refilled %d \u00b7 %d candidates \u00b7 %s", len(dirtyTweetIDs), refillLimit, snap.candidates, totalElapsed)
	m.setStatus("feed_scoring", workerStatusWithSummary("feed_scoring", true, summary, detail, ""))
}

func (m *Manager) runScoringPhase() ([]string, bool) {
	items, err := m.db.GetUnscoredFeedItems(feedScoringDirtyBatch + 1)
	if err != nil {
		log.Printf("[feed_scoring] GetUnscoredFeedItems: %v", err)
		return nil, false
	}
	if len(items) == 0 {
		return nil, false
	}
	more := len(items) > feedScoringDirtyBatch
	if more {
		items = items[:feedScoringDirtyBatch]
	}

	// Collect unique handles and tokens for batch affinity lookup
	handlesNeeded := make(map[string]bool)
	tokensNeeded := make(map[string]bool)
	for _, item := range items {
		for _, h := range []string{item.SourceHandle, item.AuthorHandle} {
			if h != "" {
				handlesNeeded[strings.ToLower(h)] = true
			}
		}
		for _, tok := range feed.ExtractInterestTokens(item.BodyText) {
			tokensNeeded[tok] = true
		}
	}

	handleList := make([]string, 0, len(handlesNeeded))
	for h := range handlesNeeded {
		handleList = append(handleList, h)
	}
	tokenList := make([]string, 0, len(tokensNeeded))
	for t := range tokensNeeded {
		tokenList = append(tokenList, t)
	}

	accountRows, _ := m.db.GetAccountAffinityScores(handleList)
	tokenRows, _ := m.db.GetTokenAffinityScores(tokenList)
	stateScores, _ := m.db.BuildStateAccountScores()

	// Channel flags
	starredIDs, _ := m.db.GetStarredChannelIDs()
	channels, _ := m.db.GetSubscribedChannels()
	starredHandles := make(map[string]bool)
	followedHandles := make(map[string]bool)
	for _, ch := range channels {
		handle := strings.ToLower(ch.ChannelID)
		// Strip platform prefix (e.g. "twitter_handle" -> "handle")
		if idx := strings.Index(handle, "_"); idx >= 0 {
			handle = handle[idx+1:]
		}
		followedHandles[handle] = true
		if starredIDs[ch.ChannelID] {
			starredHandles[handle] = true
		}
	}

	ctx := feed.ScoringContext{
		StarredHandles:  starredHandles,
		FollowedHandles: followedHandles,
		AccountScores:   accountRows,
		StateScores:     stateScores,
		TokenScores:     tokenRows,
		NowMs:           time.Now().UnixMilli(),
	}

	// Score all items
	scores := make(map[string]float64, len(items))
	for _, item := range items {
		scores[item.TweetID] = feed.ComputeAlgoInterest(item, ctx)
	}

	if err := m.db.UpdateAlgoInterest(scores); err != nil {
		log.Printf("[feed_scoring] UpdateAlgoInterest: %v", err)
		return nil, false
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.TweetID)
	}
	return ids, more
}

type snapshotPhaseStats struct {
	candidates    int
	replyAttempts int
	replyBlocked  int
	count         int
	totalDur      time.Duration
	queryDur      time.Duration
	buildDur      time.Duration
	writeDur      time.Duration
	top           string
}

func (m *Manager) runSnapshotPhaseStats(
	ctx context.Context,
	dirtyTweetIDs []string,
	refillLimit int,
	retryIncomplete bool,
) snapshotPhaseStats {
	stats := snapshotPhaseStats{top: "[]"}
	snapStart := time.Now()
	snapCtx, cancel := context.WithTimeout(ctx, feedSnapshotBuildTimeout)
	defer cancel()

	queryStart := time.Now()
	pre, err := m.db.ListPreDiversityRankedCandidatesContext(snapCtx, dirtyTweetIDs, refillLimit)
	stats.queryDur = time.Since(queryStart).Round(time.Millisecond)
	if err != nil {
		log.Printf("[feed_scoring] ListPreDiversityRankedCandidates: %v", err)
		stats.totalDur = time.Since(snapStart).Round(time.Millisecond)
		return stats
	}
	stats.candidates = len(pre)
	pre, stats.replyAttempts, stats.replyBlocked, err = m.prepareRankCandidateReplies(
		snapCtx, pre, dirtyTweetIDs, refillLimit, retryIncomplete,
	)
	if err != nil {
		log.Printf("[feed_scoring] reply readiness: %v", err)
		stats.totalDur = time.Since(snapStart).Round(time.Millisecond)
		return stats
	}

	buildStart := time.Now()
	snapshot := feed.BuildSnapshot(pre, time.Now())
	stats.buildDur = time.Since(buildStart).Round(time.Millisecond)

	writeStart := time.Now()
	if err := m.db.ReplaceFeedRankSnapshot(snapshot); err != nil {
		log.Printf("[feed_scoring] ReplaceFeedRankSnapshot: %v", err)
		stats.writeDur = time.Since(writeStart).Round(time.Millisecond)
		stats.totalDur = time.Since(snapStart).Round(time.Millisecond)
		return stats
	}
	stats.writeDur = time.Since(writeStart).Round(time.Millisecond)
	stats.totalDur = time.Since(snapStart).Round(time.Millisecond)
	stats.count = len(snapshot)
	stats.top = snapshotTop10(snapshot)
	return stats
}

func (m *Manager) prepareRankCandidateReplies(
	ctx context.Context,
	pre []db.PreDiversitySnapshotRow,
	dirtyTweetIDs []string,
	refillLimit int,
	retryIncomplete bool,
) ([]db.PreDiversitySnapshotRow, int, int, error) {
	ids := make([]string, 0, len(pre))
	for _, row := range pre {
		ids = append(ids, row.TweetID)
	}
	incomplete, err := m.db.ListIncompleteReplyChainsContext(ctx, ids)
	if err != nil || len(incomplete) == 0 {
		return pre, 0, 0, err
	}
	beforeNode := make(map[string]string, len(incomplete))
	for _, row := range incomplete {
		beforeNode[row.SeedTweetID] = row.Item.TweetID
	}
	dirty := make(map[string]struct{}, len(dirtyTweetIDs))
	for _, tweetID := range dirtyTweetIDs {
		dirty[tweetID] = struct{}{}
	}

	attemptedItems := make(map[string]struct{}, feedReplyResolutionBatch)
	attemptedSeeds := make(map[string]struct{}, feedReplyResolutionBatch)
	toResolve := make([]model.FeedItem, 0, feedReplyResolutionBatch)
	for _, row := range incomplete {
		if !retryIncomplete {
			if _, ok := dirty[row.SeedTweetID]; !ok {
				continue
			}
		}
		if _, ok := attemptedItems[row.Item.TweetID]; ok {
			attemptedSeeds[row.SeedTweetID] = struct{}{}
			continue
		}
		if len(toResolve) >= feedReplyResolutionBatch {
			continue
		}
		attemptedItems[row.Item.TweetID] = struct{}{}
		attemptedSeeds[row.SeedTweetID] = struct{}{}
		toResolve = append(toResolve, row.Item)
	}
	if len(toResolve) > 0 {
		resolveCtx, cancel := context.WithTimeout(ctx, feedReplyResolutionTimeout)
		m.resolveReplyChains(resolveCtx, toResolve)
		cancel()
		m.KickProfileJobs()
		m.KickMediaWork()
		pre, err = m.db.ListPreDiversityRankedCandidatesContext(ctx, dirtyTweetIDs, refillLimit)
		if err != nil {
			return nil, len(toResolve), 0, err
		}
		ids = ids[:0]
		for _, row := range pre {
			ids = append(ids, row.TweetID)
		}
		incomplete, err = m.db.ListIncompleteReplyChainsContext(ctx, ids)
		if err != nil {
			return nil, len(toResolve), 0, err
		}
	}

	blocked := make(map[string]struct{}, len(incomplete))
	var retryIDs []string
	for _, row := range incomplete {
		blocked[row.SeedTweetID] = struct{}{}
		_, wasDirty := dirty[row.SeedTweetID]
		_, wasAttempted := attemptedSeeds[row.SeedTweetID]
		madeProgress := wasAttempted && beforeNode[row.SeedTweetID] != row.Item.TweetID
		if (retryIncomplete || wasDirty) && (!wasAttempted || madeProgress) {
			retryIDs = append(retryIDs, row.SeedTweetID)
		}
	}
	if len(retryIDs) > 0 {
		if err := m.db.InvalidateAlgoScore(retryIDs...); err != nil {
			return nil, len(toResolve), len(blocked), err
		}
		m.KickFeedScoring()
	}
	ready := pre[:0]
	for _, row := range pre {
		if _, excluded := blocked[row.TweetID]; !excluded {
			ready = append(ready, row)
		}
	}
	return ready, len(toResolve), len(blocked), nil
}

// snapshotTop10 returns a compact "tweet_id(final_score)" summary for the first
// up-to-10 rows, for logging + debugging.
func snapshotTop10(rows []db.SnapshotRow) string {
	n := len(rows)
	if n > 10 {
		n = 10
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("%s(%.2f)", rows[i].TweetID, rows[i].FinalScore)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
