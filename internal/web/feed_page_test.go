package web

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
)

func TestHandlePageFeedKeepsHTMXCursorWhenSnapshotChanges(t *testing.T) {
	srv := newTestServer(t)
	user := "alice"
	now := time.Now().UnixMilli()

	var firstSnapshot []db.SnapshotRow
	for i := 1; i <= 45; i++ {
		id := fmt.Sprintf("t%02d", i)
		if err := srv.db.ExecRaw(`INSERT INTO feed_items
			(tweet_id, channel_id, body_text, published_at, algo_interest, algo_scored_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id, "twitter_sample_author", "body "+id, now-int64(i), 1.0, 1); err != nil {
			t.Fatal(err)
		}
		firstSnapshot = append(firstSnapshot, db.SnapshotRow{TweetID: id, RankPosition: i, FinalScore: float64(100 - i)})
	}

	if err := srv.db.ReplaceFeedRankSnapshot(firstSnapshot); err != nil {
		t.Fatal(err)
	}
	oldSnapAt := int64(1000)
	if err := srv.db.ExecRaw(`UPDATE feed_rank_snapshot SET computed_at = ?`, oldSnapAt); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"t1", "t2"} {
		if err := srv.db.ExecRaw(`INSERT INTO feed_seen (tweet_id, seen_at) VALUES (?, ?)`,
			id, now); err != nil {
			t.Fatal(err)
		}
	}

	var secondSnapshot []db.SnapshotRow
	for i := 3; i <= 45; i++ {
		id := fmt.Sprintf("t%02d", i)
		secondSnapshot = append(secondSnapshot, db.SnapshotRow{
			TweetID:      id,
			RankPosition: i - 2,
			FinalScore:   float64(100 - i),
		})
	}
	if err := srv.db.ReplaceFeedRankSnapshot(secondSnapshot); err != nil {
		t.Fatal(err)
	}
	newSnapAt := int64(2000)
	if err := srv.db.ExecRaw(`UPDATE feed_rank_snapshot SET computed_at = ?`, newSnapAt); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/feed?offset=40&snapshot_at=1000", nil)
	req.Header.Set("HX-Request", "true")
	req = attachTestAuth(req, user)
	rec := httptest.NewRecorder()
	srv.handlePageFeed(rec, req)

	body := rec.Body.String()
	for _, duplicate := range []string{"body t03", "body t40"} {
		if strings.Contains(body, duplicate) {
			t.Fatalf("stale htmx cursor response re-sent already loaded %q:\n%s", duplicate, body)
		}
	}
	for _, want := range []string{"body t43", "body t44", "body t45"} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %q after stale cursor continued:\n%s", want, body)
		}
	}
}

func TestHandlePageFeedCarriesSnapshotAtInNextCursor(t *testing.T) {
	srv := newTestServer(t)
	user := "alice"
	now := time.Now().UnixMilli()

	var rows []db.SnapshotRow
	for i := 1; i <= 41; i++ {
		id := fmt.Sprintf("t%02d", i)
		if err := srv.db.ExecRaw(`INSERT INTO feed_items
			(tweet_id, channel_id, body_text, published_at, algo_interest, algo_scored_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id, "twitter_sample_author", "body "+id, now-int64(i), 1.0, 1); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, db.SnapshotRow{TweetID: id, RankPosition: i, FinalScore: float64(100 - i)})
	}
	if err := srv.db.ReplaceFeedRankSnapshot(rows); err != nil {
		t.Fatal(err)
	}
	snapAt := int64(1234)
	if err := srv.db.ExecRaw(`UPDATE feed_rank_snapshot SET computed_at = ?`, snapAt); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/feed", nil)
	req.Header.Set("HX-Request", "true")
	req = attachTestAuth(req, user)
	rec := httptest.NewRecorder()
	srv.handlePageFeed(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "snapshot_at=1234") {
		t.Fatalf("next cursor did not carry snapshot_at:\n%s", body)
	}
}
