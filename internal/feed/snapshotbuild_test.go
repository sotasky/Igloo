package feed

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
)

func TestBuildSnapshot_AssignsSequentialPositions(t *testing.T) {
	in := []db.PreDiversitySnapshotRow{
		{TweetID: "a", AuthorHandle: "u1", BaseScore: 10, DecayFactor: 1, FreshnessBonus: 0},
		{TweetID: "b", AuthorHandle: "u2", BaseScore: 9, DecayFactor: 1, FreshnessBonus: 0},
		{TweetID: "c", AuthorHandle: "u3", BaseScore: 8, DecayFactor: 1, FreshnessBonus: 0},
	}
	out := BuildSnapshot(in, time.Unix(0, 0))
	if len(out) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(out))
	}
	for i, r := range out {
		if r.RankPosition != i+1 {
			t.Errorf("row %d rank_position = %d, want %d", i, r.RankPosition, i+1)
		}
	}
}

func TestBuildSnapshot_DiversityBreaksAuthorClumps(t *testing.T) {
	// Two items by "u1" at the top, then "u2" slightly lower — diversity penalty
	// should pull u2 ahead of the second u1.
	in := []db.PreDiversitySnapshotRow{
		{TweetID: "a1", AuthorHandle: "u1", BaseScore: 10, DecayFactor: 1},
		{TweetID: "a2", AuthorHandle: "u1", BaseScore: 9.5, DecayFactor: 1},
		{TweetID: "b1", AuthorHandle: "u2", BaseScore: 9.2, DecayFactor: 1},
	}
	out := BuildSnapshot(in, time.Unix(0, 0))
	if out[0].TweetID != "a1" {
		t.Errorf("position 1 = %q, want a1", out[0].TweetID)
	}
	// a2 base = 9.5 + jitter, minus the author penalty.
	// b1 base = 9.2 + jitter, no penalty.
	// b1 should win position 2.
	if out[1].TweetID != "b1" {
		t.Errorf("position 2 = %q, want b1 (diversity should break u1 clump)", out[1].TweetID)
	}
}

func TestBuildSnapshot_EmptyInput(t *testing.T) {
	out := BuildSnapshot(nil, time.Unix(0, 0))
	if out != nil {
		t.Errorf("expected nil for empty input, got %v", out)
	}
}

func TestJitter_DeterministicWithinHour(t *testing.T) {
	salt := "12345"
	a := jitterFor("tweet_x", salt)
	b := jitterFor("tweet_x", salt)
	if a != b {
		t.Errorf("same input produced different jitter: %v vs %v", a, b)
	}
	c := jitterFor("tweet_y", salt)
	if a == c {
		t.Errorf("different tweets produced identical jitter")
	}
}

func TestJitter_RangeBounded(t *testing.T) {
	salt := "anything"
	for i := 0; i < 1000; i++ {
		v := jitterFor("tweet_"+string(rune(i)), salt)
		if v < -jitterRangePerTweet/2-1e-9 || v > jitterRangePerTweet/2+1e-9 {
			t.Fatalf("jitter %v out of range ±%v", v, jitterRangePerTweet/2)
		}
	}
}

func TestBuildSnapshot_RecordsBreakdown(t *testing.T) {
	in := []db.PreDiversitySnapshotRow{
		{TweetID: "x", AuthorHandle: "u", BaseScore: 7, DecayFactor: 0.5, FreshnessBonus: 2},
	}
	out := BuildSnapshot(in, time.Unix(0, 0))
	if len(out) != 1 {
		t.Fatal("expected 1 row")
	}
	r := out[0]
	if r.BaseScore != 7 || r.DecayFactor != 0.5 || r.FreshnessBonus != 2 {
		t.Errorf("breakdown not preserved: %+v", r)
	}
	// FinalScore should equal base*decay + freshness + jitter, with no diversity penalty
	// (only one item, no recency window collision). Single author, single position.
	want := 7*0.5 + 2 + r.Jitter
	if absFloat(r.FinalScore-want) > 1e-9 {
		t.Errorf("final_score = %v, want %v (base*decay + freshness + jitter)", r.FinalScore, want)
	}
	if r.DiversityDemotedBy != 0 {
		t.Errorf("diversity_demoted_by = %v, want 0 for first item", r.DiversityDemotedBy)
	}
}

func TestBuildSnapshot_DiversityDemotedByRecorded(t *testing.T) {
	// Two items, same author. Second should have author penalty recorded.
	in := []db.PreDiversitySnapshotRow{
		{TweetID: "a", AuthorHandle: "u", BaseScore: 10, DecayFactor: 1, FreshnessBonus: 0},
		{TweetID: "b", AuthorHandle: "u", BaseScore: 9, DecayFactor: 1, FreshnessBonus: 0},
	}
	out := BuildSnapshot(in, time.Unix(0, 0))
	if out[0].DiversityDemotedBy != 0 {
		t.Errorf("first item should have no demotion, got %v", out[0].DiversityDemotedBy)
	}
	if out[1].DiversityDemotedBy != diversityAuthorPen {
		t.Errorf("second item demotion = %v, want %v", out[1].DiversityDemotedBy, diversityAuthorPen)
	}
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
