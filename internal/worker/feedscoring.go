package worker

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
)

func (m *Manager) runFeedScoringWorker(ctx context.Context) {
	log.Printf("[feed_scoring] worker started")

	// Immediate first run on startup
	m.scoreFeedItems()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.feedScoringKick:
			m.scoreFeedItems()
		case <-ticker.C:
			m.scoreFeedItems()
		}
	}
}

// scoringUsername is the single user that owns scored + snapshotted state.
// The codebase is single-user today; this constant makes the assumption explicit
// and gives future multi-user work a grep anchor.
const scoringUsername = "admin"

func (m *Manager) scoreFeedItems() {
	start := time.Now()
	m.setStatus("feed_scoring", workerStatus("feed_scoring", true, "scoring", ""))

	scored := m.runScoringPhase()

	// Rebuild the snapshot on every tick so time-decay drift stays fresh even
	// when no items needed re-scoring.
	snapCount, snapDur, snapTop := m.runSnapshotPhase(scoringUsername)

	totalElapsed := time.Since(start).Round(time.Millisecond)
	detail := fmt.Sprintf("scored=%d snap=%d/%s total=%s top=%s",
		scored, snapCount, snapDur, totalElapsed, snapTop)
	log.Printf("[feed_scoring] %s", detail)
	m.EmitFeed("feed_scoring", detail, "done")
	m.setStatus("feed_scoring", workerStatus("feed_scoring", true, detail, ""))
}

// runScoringPhase re-scores any items flagged unscored. Returns the count scored.
// On error, logs and returns 0 so the snapshot phase can still run against the
// last-good algo_interest values.
func (m *Manager) runScoringPhase() int {
	items, err := m.db.GetUnscoredFeedItems(0)
	if err != nil {
		log.Printf("[feed_scoring] GetUnscoredFeedItems: %v", err)
		return 0
	}
	if len(items) == 0 {
		return 0
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

	// Build scoring context — single-user system
	accountRows, _ := m.db.GetAccountAffinityScores(scoringUsername, handleList)
	tokenRows, _ := m.db.GetTokenAffinityScores(scoringUsername, tokenList)
	stateScores, _ := m.db.BuildStateAccountScores(scoringUsername)

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
		return 0
	}
	return len(scores)
}

// runSnapshotPhase rebuilds the feed_rank_snapshot for the given user. Returns
// (row_count, elapsed_rounded_ms, compact_top_10_string). On error, logs and
// returns zero values — the prior snapshot is preserved because
// ReplaceFeedRankSnapshot is a no-op on empty rows.
func (m *Manager) runSnapshotPhase(username string) (int, time.Duration, string) {
	snapStart := time.Now()

	pre, err := m.db.ListPreDiversityRanked(username)
	if err != nil {
		log.Printf("[feed_scoring] ListPreDiversityRanked: %v", err)
		return 0, 0, "[]"
	}
	snapshot := feed.BuildSnapshot(pre, time.Now())
	if err := m.db.ReplaceFeedRankSnapshot(username, snapshot); err != nil {
		log.Printf("[feed_scoring] ReplaceFeedRankSnapshot: %v", err)
		return 0, 0, "[]"
	}
	return len(snapshot), time.Since(snapStart).Round(time.Millisecond), snapshotTop10(snapshot)
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
