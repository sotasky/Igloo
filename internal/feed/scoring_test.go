package feed

import (
	"testing"

	"github.com/screwys/igloo/internal/db"
)

func TestComputeAlgoInterest_StarredChannel(t *testing.T) {
	item := db.ScoringItem{
		TweetID:      "1",
		AuthorHandle: "testuser",
		MediaJSON:    `[{"type":"photo"}]`,
	}
	ctx := ScoringContext{
		StarredHandles:  map[string]bool{"testuser": true},
		FollowedHandles: map[string]bool{"testuser": true},
	}
	score := ComputeAlgoInterest(item, ctx)
	// Star boost (25) + media bonus (3) = 28
	if score < 27.9 || score > 28.1 {
		t.Errorf("expected ~28.0, got %f", score)
	}
}

func TestComputeAlgoInterest_FollowedNotStarred(t *testing.T) {
	item := db.ScoringItem{
		TweetID:      "2",
		AuthorHandle: "other",
	}
	ctx := ScoringContext{
		FollowedHandles: map[string]bool{"other": true},
	}
	score := ComputeAlgoInterest(item, ctx)
	// Followed boost only = 5
	if score < 4.9 || score > 5.1 {
		t.Errorf("expected ~5.0, got %f", score)
	}
}

func TestComputeAlgoInterest_RetweetPenalty(t *testing.T) {
	item := db.ScoringItem{
		TweetID:      "3",
		AuthorHandle: "user",
		IsRetweet:    true,
	}
	ctx := ScoringContext{
		StarredHandles:  map[string]bool{"user": true},
		FollowedHandles: map[string]bool{"user": true},
	}
	score := ComputeAlgoInterest(item, ctx)
	// Star (25) + retweet penalty (-4) = 21
	if score < 20.9 || score > 21.1 {
		t.Errorf("expected ~21.0, got %f", score)
	}
}

func TestComputeAlgoInterest_ZeroForUnknown(t *testing.T) {
	item := db.ScoringItem{
		TweetID:      "4",
		AuthorHandle: "nobody",
	}
	ctx := ScoringContext{}
	score := ComputeAlgoInterest(item, ctx)
	if score != 0 {
		t.Errorf("expected 0, got %f", score)
	}
}
