package feed

import (
	"math"
	"strings"

	"github.com/screwys/igloo/internal/db"
)

// ScoringContext holds pre-fetched data needed to score a batch of items.
type ScoringContext struct {
	StarredHandles  map[string]bool           // lowercase handle -> starred
	FollowedHandles map[string]bool           // lowercase handle -> subscribed
	AccountScores   map[string]db.AffinityRow // from GetAccountAffinityScores
	StateScores     map[string]float64        // from BuildStateAccountScores
	TokenScores     map[string]db.AffinityRow // from GetTokenAffinityScores
	NowMs           int64                     // current time in ms for decay
}

// ComputeAlgoInterest computes the time-independent affinity score for a single item.
// Returns a value in [0, 40].
func ComputeAlgoInterest(item db.ScoringItem, ctx ScoringContext) float64 {
	authorHandle := strings.ToLower(item.AuthorHandle)
	sourceHandle := strings.ToLower(item.SourceHandle)

	// Star / followed boost
	isStarred := ctx.StarredHandles[authorHandle] || ctx.StarredHandles[sourceHandle]
	isFollowed := ctx.FollowedHandles[authorHandle] || ctx.FollowedHandles[sourceHandle]

	starBoostVal := 0.0
	if isStarred {
		starBoostVal = 25.0
	}
	followedBoostVal := 0.0
	if !isStarred && isFollowed {
		followedBoostVal = 5.0
	}

	// Account affinity
	authorScore := 0.0
	if authorHandle != "" {
		authorScore += ctx.StateScores[authorHandle]
		if row, ok := ctx.AccountScores[authorHandle]; ok {
			authorScore += affinityDecayAt(row.Score, row.LastEventMs, affinityAccountHalfLifeMs, ctx.NowMs)
		}
	}
	sourceScore := 0.0
	if sourceHandle != "" && sourceHandle != authorHandle {
		sourceScore = ctx.StateScores[sourceHandle]
		if row, ok := ctx.AccountScores[sourceHandle]; ok {
			sourceScore += affinityDecayAt(row.Score, row.LastEventMs, affinityAccountHalfLifeMs, ctx.NowMs)
		}
	}
	accountBoost := math.Min(affinityMaxAccountBoost,
		affinityAccountAuthorCoeff*math.Log1p(math.Max(0, authorScore))+
			affinityAccountSourceCoeff*math.Log1p(math.Max(0, sourceScore)))

	// Token affinity
	tokenMatchSum := 0.0
	for _, tok := range ExtractInterestTokens(item.BodyText) {
		eff := 0.0
		if row, ok := ctx.TokenScores[tok]; ok {
			eff += affinityDecayAt(row.Score, row.LastEventMs, affinityTokenHalfLifeMs, ctx.NowMs)
		}
		if eff <= 0 {
			continue
		}
		tokenMatchSum += math.Min(1.0, 0.5*math.Log1p(eff))
		if tokenMatchSum >= affinityMaxTokenBoost {
			tokenMatchSum = affinityMaxTokenBoost
			break
		}
	}

	// Media bonus / retweet penalty
	mediaBonusVal := 0.0
	if item.MediaJSON != "" && item.MediaJSON != "[]" {
		mediaBonusVal = affinityMediaBoost
	}
	rtPenaltyVal := 0.0
	if item.IsRetweet {
		rtPenaltyVal = affinityRetweetPenalty
	}

	baseInterest := math.Min(affinityMaxItemBoost, math.Max(0, accountBoost+tokenMatchSum))
	total := math.Max(0, baseInterest+starBoostVal+followedBoostVal+mediaBonusVal+rtPenaltyVal)
	return total
}
