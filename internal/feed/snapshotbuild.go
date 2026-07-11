package feed

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
)

// Tunables — match the curve and weights previously used in static/js/src/feed/rerank.js
// so behavior is comparable while we migrate.
const (
	diversityWindow     = 6    // recent-author window for MMR demotion
	diversityAuthorPen  = 5.0  // demote if author seen in last N
	diversitySourcePen  = 2.5  // demote if source_handle seen in last N
	diversityRelatedPen = 12.0 // demote if quoted/canonical tweet seen in last N
	jitterRangePerTweet = 0.38 // total spread; per-tweet jitter is centered in ±half

	nearbyRepostMergeRankDistance = 150
	nearbyRepostMergeWindow       = 12 * time.Hour
)

// BuildSnapshot turns a pre-diversity ranked list into the final snapshot rows
// by applying author/source/related-content diversity demotion (MMR-like greedy) and a
// deterministic per-hour jitter. The returned rows are in final rank order
// with rank_position assigned 1..N.
func BuildSnapshot(in []db.PreDiversitySnapshotRow, now time.Time) []db.SnapshotRow {
	if len(in) == 0 {
		return nil
	}
	hourSalt := strconv.FormatInt(now.Truncate(time.Hour).Unix(), 10)

	type cand struct {
		row          db.PreDiversitySnapshotRow
		authorLower  string
		sourceLower  string
		relatedLower string
		jitter       float64
		base         float64 // base*decay + freshness + jitter (pre-diversity)
		used         bool
	}

	cands := make([]cand, len(in))
	order := make([]int, len(in))
	for i, r := range in {
		j := jitterFor(r.TweetID, hourSalt)
		score := r.BaseScore*r.DecayFactor + r.FreshnessBonus - r.ReplyPenalty
		if score < 0 {
			score = 0
		}
		cands[i] = cand{
			row:          r,
			authorLower:  strings.ToLower(r.ChannelID),
			sourceLower:  strings.ToLower(r.SourceChannelID),
			relatedLower: strings.ToLower(r.RelatedContentKey),
			jitter:       j,
			base:         score + j,
		}
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		leftIdx := order[i]
		rightIdx := order[j]
		leftBase := cands[leftIdx].base
		rightBase := cands[rightIdx].base
		if leftBase == rightBase {
			return leftIdx < rightIdx
		}
		return leftBase > rightBase
	})

	out := make([]db.SnapshotRow, 0, len(cands))
	var recentAuthors recentWindow
	var recentSources recentWindow
	var recentRelated recentWindow

	for pos := 1; pos <= len(cands); pos++ {
		bestIdx := -1
		bestScore := -1e18
		var bestDemoted float64
		for _, i := range order {
			if bestIdx >= 0 && cands[i].base < bestScore {
				break
			}
			if cands[i].used {
				continue
			}
			s := cands[i].base
			demoted := 0.0
			if cands[i].authorLower != "" && recentAuthors.contains(cands[i].authorLower) {
				s -= diversityAuthorPen
				demoted += diversityAuthorPen
			}
			if cands[i].sourceLower != "" && recentSources.contains(cands[i].sourceLower) {
				s -= diversitySourcePen
				demoted += diversitySourcePen
			}
			if cands[i].relatedLower != "" && recentRelated.contains(cands[i].relatedLower) {
				s -= diversityRelatedPen
				demoted += diversityRelatedPen
			}
			if bestIdx < 0 || s > bestScore || (s == bestScore && i < bestIdx) {
				bestScore = s
				bestIdx = i
				bestDemoted = demoted
			}
		}
		if bestIdx < 0 {
			break
		}
		c := cands[bestIdx]
		cands[bestIdx].used = true

		out = append(out, db.SnapshotRow{
			TweetID:            c.row.TweetID,
			RankPosition:       pos,
			BaseScore:          c.row.BaseScore,
			DecayFactor:        c.row.DecayFactor,
			FreshnessBonus:     c.row.FreshnessBonus,
			Jitter:             c.jitter,
			DiversityDemotedBy: bestDemoted,
			FinalScore:         bestScore,
		})

		recentAuthors.push(c.authorLower)
		recentSources.push(c.sourceLower)
		recentRelated.push(c.relatedLower)
	}
	out = mergeNearbyOriginalsIntoPureReposts(out, in)
	out = compactThreadRoots(out, in)
	return compactPureRepostsIntoThreadRepresentatives(out, in)
}

func compactThreadRoots(rows []db.SnapshotRow, meta []db.PreDiversitySnapshotRow) []db.SnapshotRow {
	if len(rows) < 2 || len(meta) == 0 {
		return rows
	}
	metadata := make(map[string]db.PreDiversitySnapshotRow, len(meta))
	for _, row := range meta {
		metadata[row.TweetID] = row
	}

	rootIndex := make(map[string]int, len(rows))
	out := make([]db.SnapshotRow, 0, len(rows))
	for _, row := range rows {
		rowMeta, ok := metadata[row.TweetID]
		if !ok {
			out = append(out, row)
			continue
		}
		rootID := strings.ToLower(strings.TrimSpace(rowMeta.ThreadRootID))
		if rootID == "" {
			out = append(out, row)
			continue
		}
		existingIndex, exists := rootIndex[rootID]
		if !exists {
			rootIndex[rootID] = len(out)
			out = append(out, row)
			continue
		}
		existingMeta := metadata[out[existingIndex].TweetID]
		if !existingMeta.IsReply && rowMeta.IsReply {
			row.RankPosition = out[existingIndex].RankPosition
			out[existingIndex] = row
		}
	}
	for i := range out {
		out[i].RankPosition = i + 1
	}
	return out
}

// compactPureRepostsIntoThreadRepresentatives collapses a pure repost and the
// thread that starts from the repost target into one snapshot row. The thread
// card is the visible representative, but the group keeps the best rank/score.
func compactPureRepostsIntoThreadRepresentatives(rows []db.SnapshotRow, meta []db.PreDiversitySnapshotRow) []db.SnapshotRow {
	if len(rows) < 2 || len(meta) == 0 {
		return rows
	}
	metadata := make(map[string]db.PreDiversitySnapshotRow, len(meta))
	for _, row := range meta {
		metadata[row.TweetID] = row
	}

	type mergeGroup struct {
		bestRow   db.SnapshotRow
		threadID  string
		hasPure   bool
		hasThread bool
	}
	groups := make(map[string]mergeGroup, len(rows))
	for _, row := range rows {
		rowMeta, ok := metadata[row.TweetID]
		if !ok {
			continue
		}
		rootID, isThreadRepresentative, isPureRepost := threadRepostMergeRoot(rowMeta)
		if rootID == "" {
			continue
		}
		group, exists := groups[rootID]
		if !exists {
			group.bestRow = row
		}
		if isThreadRepresentative && !group.hasThread {
			group.threadID = row.TweetID
			group.hasThread = true
		}
		if isPureRepost {
			group.hasPure = true
		}
		groups[rootID] = group
	}

	emitted := make(map[string]bool, len(groups))
	out := make([]db.SnapshotRow, 0, len(rows))
	for _, row := range rows {
		rowMeta, ok := metadata[row.TweetID]
		if !ok {
			out = append(out, row)
			continue
		}
		rootID, _, _ := threadRepostMergeRoot(rowMeta)
		group, ok := groups[rootID]
		if rootID == "" || !ok || !group.hasThread || !group.hasPure {
			out = append(out, row)
			continue
		}
		if emitted[rootID] {
			continue
		}
		promoted := group.bestRow
		promoted.TweetID = group.threadID
		out = append(out, promoted)
		emitted[rootID] = true
	}
	for i := range out {
		out[i].RankPosition = i + 1
	}
	return out
}

func threadRepostMergeRoot(row db.PreDiversitySnapshotRow) (rootID string, isThreadRepresentative bool, isPureRepost bool) {
	if isPureRepostSnapshotRow(row) {
		rootID = strings.ToLower(strings.TrimSpace(row.RepostTargetThreadRootID))
		return rootID, false, rootID != ""
	}
	if row.IsReply {
		rootID = strings.ToLower(strings.TrimSpace(row.ThreadRootID))
		return rootID, rootID != "", false
	}
	return "", false, false
}

func mergeNearbyOriginalsIntoPureReposts(rows []db.SnapshotRow, meta []db.PreDiversitySnapshotRow) []db.SnapshotRow {
	if len(rows) < 2 || len(meta) == 0 {
		return rows
	}
	metadata := make(map[string]db.PreDiversitySnapshotRow, len(meta))
	for _, row := range meta {
		metadata[row.TweetID] = row
	}

	type positionedOriginal struct {
		index int
		row   db.PreDiversitySnapshotRow
	}
	originalsByHash := make(map[string][]positionedOriginal)
	for i, snapshotRow := range rows {
		row, ok := metadata[snapshotRow.TweetID]
		if !ok || row.ContentHash == "" || row.IsRetweet {
			continue
		}
		originalsByHash[row.ContentHash] = append(originalsByHash[row.ContentHash], positionedOriginal{
			index: i,
			row:   row,
		})
	}
	if len(originalsByHash) == 0 {
		return rows
	}

	hideOriginal := make(map[int]bool)
	for i, snapshotRow := range rows {
		repost, ok := metadata[snapshotRow.TweetID]
		if !ok || !isPureRepostSnapshotRow(repost) {
			continue
		}
		for _, original := range originalsByHash[repost.ContentHash] {
			if absInt(i-original.index) > nearbyRepostMergeRankDistance {
				continue
			}
			if !withinNearbyRepostWindow(repost.PublishedAtMs, original.row.PublishedAtMs) {
				continue
			}
			hideOriginal[original.index] = true
		}
	}
	if len(hideOriginal) == 0 {
		return rows
	}

	out := make([]db.SnapshotRow, 0, len(rows)-len(hideOriginal))
	for i, row := range rows {
		if hideOriginal[i] {
			continue
		}
		row.RankPosition = len(out) + 1
		out = append(out, row)
	}
	return out
}

func isPureRepostSnapshotRow(row db.PreDiversitySnapshotRow) bool {
	return row.IsRetweet && row.ContentHash != "" && strings.TrimSpace(row.QuoteTweetID) == ""
}

func withinNearbyRepostWindow(leftMs, rightMs int64) bool {
	if leftMs <= 0 || rightMs <= 0 {
		return false
	}
	return time.Duration(absInt64(leftMs-rightMs))*time.Millisecond <= nearbyRepostMergeWindow
}

// jitterFor produces a deterministic ±jitterRangePerTweet/2 value for a
// (tweet_id, hour_salt) pair using FNV-1a. Same hour ⇒ same value across requests.
func jitterFor(tweetID, hourSalt string) float64 {
	h := fnv32a(tweetID)
	h ^= uint32('|')
	h *= 16777619
	for i := 0; i < len(hourSalt); i++ {
		h ^= uint32(hourSalt[i])
		h *= 16777619
	}
	frac := float64(h) / 4294967295.0
	return (frac - 0.5) * jitterRangePerTweet
}

func fnv32a(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

type recentWindow struct {
	values [diversityWindow]string
	len    int
}

func (w *recentWindow) contains(value string) bool {
	for i := 0; i < w.len; i++ {
		if w.values[i] == value {
			return true
		}
	}
	return false
}

func (w *recentWindow) push(value string) {
	if value == "" {
		return
	}
	if w.len < diversityWindow {
		w.values[w.len] = value
		w.len++
		return
	}
	copy(w.values[:], w.values[1:])
	w.values[diversityWindow-1] = value
}

func pushWindow(buf []string, value string, max int) []string {
	if value == "" {
		return buf
	}
	buf = append(buf, value)
	if len(buf) > max {
		buf = buf[len(buf)-max:]
	}
	return buf
}

func containsLower(buf []string, candidate string) bool {
	low := strings.ToLower(candidate)
	for _, v := range buf {
		if v == low {
			return true
		}
	}
	return false
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
