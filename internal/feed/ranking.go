package feed

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

const (
	recencyHalfLifeHours = 2.0
	seenDemotion         = 0.01
	starBoost            = 25.0
	followedBoost        = 5.0
	mediaBonus           = 3.0
	retweetPenalty       = -4.0
	freshBonusPeak       = 18.0
	freshBonusWindowH    = 6.0
	maxItemBoost         = 40.0

	// Affinity scoring (matching Python's feed_ranking.py)
	affinityAccountHalfLifeMs  = 30 * 24 * 3600 * 1000 // 30 days
	affinityTokenHalfLifeMs    = 14 * 24 * 3600 * 1000 // 14 days
	affinityAccountAuthorCoeff = 4.0
	affinityAccountSourceCoeff = 2.0
	affinityMaxAccountBoost    = 20.0
	affinityMaxTokenBoost      = 10.0
	affinityMaxItemBoost       = 40.0
	affinityMediaBoost         = 3.0
	affinityRetweetPenalty     = -4.0
)

// AlgorithmicFeedEnabled reports whether algorithmic ranking is on for web feed
// paths. When false, callers sort chronologically via SortFeedItemsChronological.
func AlgorithmicFeedEnabled(database *db.DB) bool {
	v, _ := database.GetSetting("algorithmic_feed_enabled", "false")
	return v == "true" || v == "1"
}

// SortFeedItemsChronological sorts items by PublishedAt descending, falling back
// to TweetID for stability when timestamps are missing or equal.
func SortFeedItemsChronological(items []model.FeedItem) []model.FeedItem {
	sort.SliceStable(items, func(i, j int) bool {
		var ti, tj int64
		if items[i].PublishedAt != nil {
			ti = items[i].PublishedAt.UnixMilli()
		}
		if items[j].PublishedAt != nil {
			tj = items[j].PublishedAt.UnixMilli()
		}
		if ti != tj {
			return ti > tj
		}
		return items[i].TweetID > items[j].TweetID
	})
	return items
}

// RankFeedItems sorts feed items by combined score (interest + recency - seen demotion).
func RankFeedItems(items []model.FeedItem) []model.FeedItem {
	for i := range items {
		if items[i].AlgoInterestScore == 0 {
			items[i].AlgoInterestScore = baseInterestScore(&items[i])
		}
	}

	// Compute effective scores (interest + freshness - seen demotion)
	// and store them so the client can sort by the full score.
	for i := range items {
		items[i].AlgoInterestScore = effectiveScore(&items[i])
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].AlgoInterestScore > items[j].AlgoInterestScore
	})
	return items
}

// deduplicateRetweets collapses retweets sharing the same ContentHash into one card.
// Original (non-retweet) tweets are always kept and never show retweeter info.
// Retweets of the same content collapse into one card with retweeter info.
func deduplicateRetweets(items []model.FeedItem) []model.FeedItem {
	seenRT := make(map[string]int) // content_hash → index in result (for retweets only)
	var result []model.FeedItem

	for i := range items {
		hash := items[i].ContentHash

		// No hash or quote tweets: always keep
		if hash == "" || items[i].QuoteTweetID != "" {
			result = append(result, items[i])
			continue
		}

		// Original tweets: always keep, never show retweeter badge
		if !items[i].IsRetweet {
			items[i].Retweeters = nil
			result = append(result, items[i])
			continue
		}

		// Retweet: collapse with other retweets of same content
		if idx, exists := seenRT[hash]; exists {
			if items[i].IsLiked {
				result[idx].IsLiked = true
			}
			if items[i].IsBookmarked {
				result[idx].IsBookmarked = true
			}
			continue
		}

		seenRT[hash] = len(result)
		result = append(result, items[i])
	}

	return result
}

func baseInterestScore(item *model.FeedItem) float64 {
	score := 0.0

	if item.ChannelIsStarred {
		score += starBoost
	} else if item.ChannelIsFollowed {
		score += followedBoost
	}

	if len(item.Media) > 0 || item.MediaJSON != "" {
		score += mediaBonus
	}
	if item.IsRetweet {
		score += retweetPenalty
	}

	if score > maxItemBoost {
		score = maxItemBoost
	}
	if score < 0 {
		score = 0
	}
	return score
}

func effectiveScore(item *model.FeedItem) float64 {
	var pubMs int64
	if item.PublishedAt != nil {
		pubMs = item.PublishedAt.UnixMilli()
	}
	score := combinedScore(item.AlgoInterestScore, pubMs)
	if item.IsSeen {
		score *= seenDemotion
	}
	return score
}

func combinedScore(interest float64, publishedAtMs int64) float64 {
	if publishedAtMs <= 0 {
		return interest
	}

	pubTime := time.UnixMilli(publishedAtMs)
	hours := time.Since(pubTime).Hours()
	if hours < 0 {
		hours = 0
	}

	decayed := interest * math.Pow(0.5, hours/recencyHalfLifeHours)

	freshBonus := 0.0
	if hours < freshBonusWindowH {
		freshBonus = freshBonusPeak * (1.0 - hours/freshBonusWindowH)
	}

	return decayed + freshBonus
}

func affinityDecayAt(score float64, lastEventMs int64, halfLifeMs int64, nowMs int64) float64 {
	if score <= 0 || lastEventMs <= 0 {
		return 0
	}
	ageMs := float64(nowMs - lastEventMs)
	if ageMs <= 0 {
		return score
	}
	return score * math.Pow(0.5, ageMs/float64(halfLifeMs))
}

// PersonalizeItems computes full algo_interest_score using affinity data.
// Called after basic enrichment (liked/seen/bookmarked/channel flags already set).
func PersonalizeItems(database *db.DB, items []model.FeedItem, username string) {
	if username == "" || len(items) == 0 {
		return
	}

	// Collect handles and tokens needed
	handlesNeeded := make(map[string]bool)
	tokensNeeded := make(map[string]bool)
	for _, item := range items {
		for _, h := range []string{item.SourceHandle, item.AuthorHandle} {
			if h != "" {
				handlesNeeded[strings.ToLower(h)] = true
			}
		}
		for _, tok := range ExtractInterestTokens(item.BodyText) {
			tokensNeeded[tok] = true
		}
	}

	handleList := mapKeys(handlesNeeded)
	tokenList := mapKeys(tokensNeeded)

	// Fetch affinity scores
	accountRows, _ := database.GetAccountAffinityScores(username, handleList)
	tokenRows, _ := database.GetTokenAffinityScores(username, tokenList)
	stateAccount, _ := database.BuildStateAccountScores(username)

	nowMs := time.Now().UnixMilli()

	for i := range items {
		item := &items[i]
		authorHandle := strings.ToLower(item.AuthorHandle)
		sourceHandle := strings.ToLower(item.SourceHandle)

		// Star boost
		starBoost := 0.0
		if item.ChannelIsStarred {
			starBoost = 25.0
		}

		// Account affinity: state + decay
		authorScore := 0.0
		if authorHandle != "" {
			authorScore += stateAccount[authorHandle]
			if row, ok := accountRows[authorHandle]; ok {
				authorScore += affinityDecayAt(row.Score, row.LastEventMs, affinityAccountHalfLifeMs, nowMs)
			}
		}
		sourceScore := 0.0
		if sourceHandle != "" && sourceHandle != authorHandle {
			sourceScore += stateAccount[sourceHandle]
			if row, ok := accountRows[sourceHandle]; ok {
				sourceScore += affinityDecayAt(row.Score, row.LastEventMs, affinityAccountHalfLifeMs, nowMs)
			}
		}
		accountBoost := math.Min(affinityMaxAccountBoost,
			affinityAccountAuthorCoeff*math.Log1p(math.Max(0, authorScore))+
				affinityAccountSourceCoeff*math.Log1p(math.Max(0, sourceScore)))

		// Token affinity
		tokenMatchSum := 0.0
		for _, tok := range ExtractInterestTokens(item.BodyText) {
			eff := 0.0
			if row, ok := tokenRows[tok]; ok {
				eff += affinityDecayAt(row.Score, row.LastEventMs, affinityTokenHalfLifeMs, nowMs)
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

		// Combine
		baseInterest := math.Min(affinityMaxItemBoost, math.Max(0, accountBoost+tokenMatchSum))
		mediaBonus := 0.0
		if len(item.Media) > 0 || item.MediaJSON != "" {
			mediaBonus = affinityMediaBoost
		}
		rtPenalty := 0.0
		if item.IsRetweet {
			rtPenalty = affinityRetweetPenalty
		}
		followedBoost := 0.0
		if !item.ChannelIsStarred && item.ChannelIsFollowed {
			followedBoost = 5.0
		}

		total := math.Max(0, baseInterest+starBoost+followedBoost+mediaBonus+rtPenalty)
		item.AlgoInterestScore = total
	}
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
