package feed

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestCombinedScore(t *testing.T) {
	now := time.Now()
	recent := now.Add(-1 * time.Hour).UnixMilli()
	old := now.Add(-72 * time.Hour).UnixMilli()

	recentScore := combinedScore(10.0, recent)
	oldScore := combinedScore(10.0, old)

	if recentScore <= oldScore {
		t.Errorf("recent item should score higher: recent=%f old=%f", recentScore, oldScore)
	}
}

func TestSeenDemotion(t *testing.T) {
	item1 := model.FeedItem{AlgoInterestScore: 20.0, IsSeen: false}
	item2 := model.FeedItem{AlgoInterestScore: 20.0, IsSeen: true}

	s1 := effectiveScore(&item1)
	s2 := effectiveScore(&item2)

	if s2 >= s1 {
		t.Errorf("seen item should score lower: unseen=%f seen=%f", s1, s2)
	}
}

func TestRankFeedItems(t *testing.T) {
	now := time.Now()
	items := []model.FeedItem{
		{TweetID: "old", AlgoInterestScore: 5.0},
		{TweetID: "new", AlgoInterestScore: 20.0},
		{TweetID: "seen", AlgoInterestScore: 15.0, IsSeen: true},
	}

	items[0].PublishedAt = timePtr(now.Add(-3 * time.Hour))
	items[1].PublishedAt = timePtr(now.Add(-1 * time.Hour))
	items[2].PublishedAt = timePtr(now.Add(-2 * time.Hour))

	ranked := RankFeedItems(items)

	if ranked[0].TweetID != "new" {
		t.Errorf("expected 'new' first, got %q", ranked[0].TweetID)
	}
	if ranked[len(ranked)-1].TweetID != "seen" {
		t.Errorf("expected 'seen' last, got %q", ranked[len(ranked)-1].TweetID)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
