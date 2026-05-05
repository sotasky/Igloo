package worker

import (
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
)

func TestSnapshotTop10_Compact(t *testing.T) {
	rows := []db.SnapshotRow{
		{TweetID: "a", FinalScore: 9.99},
		{TweetID: "b", FinalScore: 8.5},
	}
	got := snapshotTop10(rows)
	want := "[a(9.99),b(8.50)]"
	if got != want {
		t.Errorf("snapshotTop10 = %q, want %q", got, want)
	}
}

func TestSnapshotTop10_Truncates(t *testing.T) {
	rows := make([]db.SnapshotRow, 15)
	for i := range rows {
		rows[i] = db.SnapshotRow{TweetID: "x", FinalScore: float64(i)}
	}
	got := snapshotTop10(rows)
	// 10 entries ⇒ 9 commas separating them inside the brackets.
	commas := 0
	for i := 0; i < len(got); i++ {
		if got[i] == ',' {
			commas++
		}
	}
	if commas != 9 {
		t.Errorf("expected 9 commas (10 entries), got %d in %q", commas, got)
	}
}

func TestSnapshotTop10_Empty(t *testing.T) {
	got := snapshotTop10(nil)
	if got != "[]" {
		t.Errorf("snapshotTop10(nil) = %q, want %q", got, "[]")
	}
}

// Smoke test: BuildSnapshot integrates cleanly with the db.SnapshotRow shape.
func TestBuildSnapshot_ReturnsSnapshotRowType(t *testing.T) {
	pre := []db.PreDiversitySnapshotRow{
		{TweetID: "a", AuthorHandle: "u", BaseScore: 3, DecayFactor: 1, FreshnessBonus: 1},
		{TweetID: "b", AuthorHandle: "v", BaseScore: 2, DecayFactor: 1, FreshnessBonus: 1},
	}
	got := feed.BuildSnapshot(pre, time.Unix(0, 0))
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	if got[0].RankPosition != 1 || got[1].RankPosition != 2 {
		t.Errorf("positions = %d, %d; want 1, 2", got[0].RankPosition, got[1].RankPosition)
	}
}
