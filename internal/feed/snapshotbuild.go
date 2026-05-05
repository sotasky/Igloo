package feed

import (
	"hash/fnv"
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
	jitterRangePerTweet = 0.38 // total spread; per-tweet jitter is centered in ±half
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
		row    db.PreDiversitySnapshotRow
		jitter float64
		base   float64 // base*decay + freshness + jitter (pre-diversity)
		used   bool
	}

	cands := make([]cand, len(in))
	for i, r := range in {
		j := jitterFor(r.TweetID, hourSalt)
		cands[i] = cand{
			row:    r,
			jitter: j,
			base:   r.BaseScore*r.DecayFactor + r.FreshnessBonus + j,
		}
	}

	out := make([]db.SnapshotRow, 0, len(cands))
	recentAuthors := make([]string, 0, diversityWindow)
	recentSources := make([]string, 0, diversityWindow)

	for pos := 1; pos <= len(cands); pos++ {
		bestIdx := -1
		bestScore := -1e18
		var bestDemoted float64
		for i := range cands {
			if cands[i].used {
				continue
			}
			s := cands[i].base
			demoted := 0.0
			if cands[i].row.AuthorHandle != "" && containsLower(recentAuthors, cands[i].row.AuthorHandle) {
				s -= diversityAuthorPen
				demoted += diversityAuthorPen
			}
			if cands[i].row.SourceHandle != "" && containsLower(recentSources, cands[i].row.SourceHandle) {
				s -= diversitySourcePen
				demoted += diversitySourcePen
			}
			if s > bestScore {
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

		recentAuthors = pushWindow(recentAuthors, strings.ToLower(c.row.AuthorHandle), diversityWindow)
		recentSources = pushWindow(recentSources, strings.ToLower(c.row.SourceHandle), diversityWindow)
	}
	return out
}

// jitterFor produces a deterministic ±jitterRangePerTweet/2 value for a
// (tweet_id, hour_salt) pair using FNV-1a. Same hour ⇒ same value across requests.
func jitterFor(tweetID, hourSalt string) float64 {
	h := fnv.New32a()
	h.Write([]byte(tweetID))
	h.Write([]byte{'|'})
	h.Write([]byte(hourSalt))
	frac := float64(h.Sum32()) / 4294967295.0
	return (frac - 0.5) * jitterRangePerTweet
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
