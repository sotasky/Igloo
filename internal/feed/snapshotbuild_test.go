package feed

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"strings"
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

func TestBuildSnapshot_DiversityBreaksConversationClumps(t *testing.T) {
	in := []db.PreDiversitySnapshotRow{
		{TweetID: "reply_a", AuthorHandle: "sample_author_a", SourceHandle: "sample_source_a", ConversationKey: "sample_root", BaseScore: 30, DecayFactor: 1},
		{TweetID: "reply_b", AuthorHandle: "sample_author_b", SourceHandle: "sample_source_b", ConversationKey: "sample_root", BaseScore: 29, DecayFactor: 1},
		{TweetID: "other", AuthorHandle: "sample_author_c", SourceHandle: "sample_source_c", ConversationKey: "other", BaseScore: 28.5, DecayFactor: 1},
	}
	out := BuildSnapshot(in, time.Unix(0, 0))
	if out[0].TweetID != "reply_a" {
		t.Fatalf("position 1 = %q, want reply_a", out[0].TweetID)
	}
	if out[1].TweetID != "other" {
		t.Fatalf("position 2 = %q, want other after conversation diversity", out[1].TweetID)
	}
	if out[2].DiversityDemotedBy != diversityConversationPen {
		t.Fatalf("reply_b demotion = %.1f, want %.1f", out[2].DiversityDemotedBy, diversityConversationPen)
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

func TestJitter_MatchesStandardFNV1a(t *testing.T) {
	cases := []struct {
		tweetID  string
		hourSalt string
	}{
		{"tweet_a", "0"},
		{"tweet_b", "1778191200"},
		{"123456789", "9999999999"},
	}
	for _, tc := range cases {
		got := jitterFor(tc.tweetID, tc.hourSalt)
		want := jitterForStandardFNVForTest(tc.tweetID, tc.hourSalt)
		if got != want {
			t.Fatalf("jitterFor(%q, %q) = %.17g, want %.17g", tc.tweetID, tc.hourSalt, got, want)
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

func TestBuildSnapshot_AppliesReplyPenalty(t *testing.T) {
	now := time.Unix(1700000000, 0)
	in := []db.PreDiversitySnapshotRow{
		{TweetID: "reply", AuthorHandle: "u", BaseScore: 10, DecayFactor: 1, FreshnessBonus: 0, ReplyPenalty: 4},
	}
	out := BuildSnapshot(in, now)
	if len(out) != 1 {
		t.Fatal("expected 1 row")
	}
	want := 6 + jitterFor("reply", fmt.Sprintf("%d", now.Truncate(time.Hour).Unix()))
	if absFloat(out[0].FinalScore-want) > 1e-9 {
		t.Errorf("final_score = %v, want %v", out[0].FinalScore, want)
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

func TestBuildSnapshot_MatchesExhaustiveGreedySelection(t *testing.T) {
	now := time.Unix(1778191200, 0)
	in := syntheticSnapshotRows(240)
	got := BuildSnapshot(in, now)
	want := buildSnapshotExhaustiveForTest(in, now)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("optimized snapshot differs from exhaustive greedy\nfirst diff: %s", firstSnapshotDiff(got, want))
	}
}

func BenchmarkBuildSnapshotDense2000(b *testing.B) {
	in := syntheticSnapshotRows(2000)
	now := time.Unix(1778191200, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := BuildSnapshot(in, now)
		if len(out) != len(in) {
			b.Fatalf("snapshot rows = %d, want %d", len(out), len(in))
		}
	}
}

func buildSnapshotExhaustiveForTest(in []db.PreDiversitySnapshotRow, now time.Time) []db.SnapshotRow {
	if len(in) == 0 {
		return nil
	}
	hourSalt := fmt.Sprintf("%d", now.Truncate(time.Hour).Unix())

	type cand struct {
		row    db.PreDiversitySnapshotRow
		jitter float64
		base   float64
		used   bool
	}

	cands := make([]cand, len(in))
	for i, r := range in {
		j := jitterFor(r.TweetID, hourSalt)
		score := r.BaseScore*r.DecayFactor + r.FreshnessBonus - r.ReplyPenalty
		if score < 0 {
			score = 0
		}
		cands[i] = cand{
			row:    r,
			jitter: j,
			base:   score + j,
		}
	}

	out := make([]db.SnapshotRow, 0, len(cands))
	recentAuthors := make([]string, 0, diversityWindow)
	recentSources := make([]string, 0, diversityWindow)
	recentConversations := make([]string, 0, diversityWindow)

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
			if cands[i].row.ConversationKey != "" && containsLower(recentConversations, cands[i].row.ConversationKey) {
				s -= diversityConversationPen
				demoted += diversityConversationPen
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
		recentConversations = pushWindow(recentConversations, strings.ToLower(c.row.ConversationKey), diversityWindow)
	}
	return out
}

func jitterForStandardFNVForTest(tweetID, hourSalt string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(tweetID))
	_, _ = h.Write([]byte{'|'})
	_, _ = h.Write([]byte(hourSalt))
	frac := float64(h.Sum32()) / 4294967295.0
	return (frac - 0.5) * jitterRangePerTweet
}

func syntheticSnapshotRows(n int) []db.PreDiversitySnapshotRow {
	rows := make([]db.PreDiversitySnapshotRow, n)
	for i := 0; i < n; i++ {
		rows[i] = db.PreDiversitySnapshotRow{
			TweetID:         fmt.Sprintf("tw_%04d", i),
			AuthorHandle:    fmt.Sprintf("author_%02d", i%17),
			SourceHandle:    fmt.Sprintf("source_%02d", (i/3)%11),
			ConversationKey: fmt.Sprintf("conversation_%02d", (i/2)%23),
			BaseScore:       200 - float64(i%80)*0.07 - float64(i/80)*0.5,
			DecayFactor:     1 - float64(i%5)*0.03,
			FreshnessBonus:  float64((i*7)%13) * 0.21,
		}
	}
	return rows
}

func firstSnapshotDiff(got, want []db.SnapshotRow) string {
	if len(got) != len(want) {
		return fmt.Sprintf("length got %d want %d", len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			return fmt.Sprintf("row %d got %+v want %+v", i, got[i], want[i])
		}
	}
	return "none"
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
