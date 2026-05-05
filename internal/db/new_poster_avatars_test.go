package db

import (
	"strconv"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// seedFeedItem inserts a row into feed_items. The raw author_avatar_url
// column holds placeholder values in production; we mirror that here by
// leaving it empty. published is a unix millis int.
func seedFeedItem(t *testing.T, d *DB, tweetID, author string, published int64) {
	t.Helper()
	if _, err := d.conn.Exec(`INSERT INTO feed_items
		(tweet_id, author_handle, author_avatar_url, body_text, published_at, algo_interest, algo_scored_at)
		VALUES (?, ?, '', ?, ?, ?, ?)`,
		tweetID, author, "body", published, 1.0, 0); err != nil {
		t.Fatalf("seed %s: %v", tweetID, err)
	}
}

func seedChannelProfileAvatar(t *testing.T, d *DB, author, avatarURL string) {
	t.Helper()
	now := time.Now()
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_" + author,
		Platform:  "twitter",
		Handle:    author,
		AvatarURL: avatarURL,
		FetchedAt: &now,
	}); err != nil {
		t.Fatalf("seed profile avatar %s: %v", author, err)
	}
}

func TestGetNewPosterAvatars_RanksBySnapshotThenRecency(t *testing.T) {
	d := openWritableTestDB(t)
	user := "alice"
	base := time.Now().UnixMilli()

	// known_head is oldest. Four newer items across four distinct authors.
	seedFeedItem(t, d, "t_head", "old_user", base)
	seedFeedItem(t, d, "t1", "u1", base+1000)
	seedFeedItem(t, d, "t2", "u2", base+2000)
	seedFeedItem(t, d, "t3", "u3", base+3000)
	seedFeedItem(t, d, "t4", "u4", base+4000)

	// Snapshot ranks t2 and t4 highest. t1 and t3 are not in the snapshot.
	if err := d.ReplaceFeedRankSnapshot(user, []SnapshotRow{
		{TweetID: "t2", RankPosition: 1, FinalScore: 99},
		{TweetID: "t4", RankPosition: 2, FinalScore: 50},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := d.GetNewPosterAvatars(user, "t_head", 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d: %+v", len(got), got)
	}
	// Snapshot winners first: u2 (score 99), u4 (score 50).
	if got[0].AuthorHandle != "u2" || got[1].AuthorHandle != "u4" {
		t.Fatalf("snapshot order wrong: %+v", got)
	}
	// Recency fill for the third slot. Newest non-snapshot author is u3 (t3).
	if got[2].AuthorHandle != "u3" {
		t.Fatalf("recency fill wrong, got %s: %+v", got[2].AuthorHandle, got)
	}
	// URL is the proxy form.
	if got[0].AuthorAvatarURL != "/api/media/avatar/twitter_u2" {
		t.Fatalf("avatar URL wrong: %s", got[0].AuthorAvatarURL)
	}
}

func TestGetNewPosterAvatars_DedupesByAuthor(t *testing.T) {
	d := openWritableTestDB(t)
	base := time.Now().UnixMilli()

	seedFeedItem(t, d, "t_head", "old_user", base)
	// Same author posts twice.
	seedFeedItem(t, d, "t1", "u1", base+1000)
	seedFeedItem(t, d, "t2", "u1", base+2000)
	seedFeedItem(t, d, "t3", "u2", base+3000)

	got, err := d.GetNewPosterAvatars("", "t_head", 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 unique authors, got %d: %+v", len(got), got)
	}
	seen := map[string]bool{}
	for _, a := range got {
		if seen[a.AuthorHandle] {
			t.Fatalf("duplicate author %s: %+v", a.AuthorHandle, got)
		}
		seen[a.AuthorHandle] = true
	}
}

func TestGetNewPosterAvatars_EmptyKnownHead(t *testing.T) {
	d := openWritableTestDB(t)
	got, err := d.GetNewPosterAvatars("alice", "", 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestGetNewPosterAvatars_UnknownKnownHead(t *testing.T) {
	d := openWritableTestDB(t)
	got, err := d.GetNewPosterAvatars("alice", "missing_"+strconv.Itoa(int(time.Now().UnixNano())), 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestGetNewPosterAvatars_PrefersChannelProfileAvatarURL(t *testing.T) {
	d := openWritableTestDB(t)
	base := time.Now().UnixMilli()

	seedFeedItem(t, d, "t_head", "old_user", base)
	seedFeedItem(t, d, "t1", "u1", base+1000)
	seedFeedItem(t, d, "t2", "u2", base+2000)
	seedChannelProfileAvatar(t, d, "u1", "https://pbs.twimg.com/profile_images/123/u1.jpg")

	got, err := d.GetNewPosterAvatars("", "t_head", 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 authors, got %+v", got)
	}
	if got[1].AuthorHandle != "u1" {
		t.Fatalf("want u1 second, got %+v", got)
	}
	if got[1].AuthorAvatarURL != "https://pbs.twimg.com/profile_images/123/u1.jpg" {
		t.Fatalf("want direct profile avatar URL, got %q", got[1].AuthorAvatarURL)
	}
	if got[0].AuthorAvatarURL != "/api/media/avatar/twitter_u2" {
		t.Fatalf("want proxy fallback for u2, got %q", got[0].AuthorAvatarURL)
	}
}
