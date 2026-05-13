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
	diversityWindow          = 6    // recent-author window for MMR demotion
	diversityAuthorPen       = 5.0  // demote if author seen in last N
	diversitySourcePen       = 2.5  // demote if source_handle seen in last N
	diversityConversationPen = 7.5  // demote if conversation root seen in last N
	jitterRangePerTweet      = 0.38 // total spread; per-tweet jitter is centered in ±half
)

// BuildSnapshot turns a pre-diversity ranked list into the final snapshot rows
// by applying author/source diversity demotion (MMR-like greedy) and a
// deterministic per-hour jitter. The returned rows are in final rank order
// with rank_position assigned 1..N.
func BuildSnapshot(in []db.PreDiversitySnapshotRow, now time.Time) []db.SnapshotRow {
	if len(in) == 0 {
		return nil
	}
	hourSalt := strconv.FormatInt(now.Truncate(time.Hour).Unix(), 10)

	type cand struct {
		row               db.PreDiversitySnapshotRow
		authorLower       string
		sourceLower       string
		conversationLower string
		jitter            float64
		base              float64 // base*decay + freshness + jitter (pre-diversity)
		used              bool
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
			row:               r,
			authorLower:       strings.ToLower(r.AuthorHandle),
			sourceLower:       strings.ToLower(r.SourceHandle),
			conversationLower: strings.ToLower(r.ConversationKey),
			jitter:            j,
			base:              score + j,
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
	var recentConversations recentWindow

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
			if cands[i].conversationLower != "" && recentConversations.contains(cands[i].conversationLower) {
				s -= diversityConversationPen
				demoted += diversityConversationPen
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
		recentConversations.push(c.conversationLower)
	}
	return out
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
