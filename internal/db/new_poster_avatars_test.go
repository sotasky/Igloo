package db

import (
	"strconv"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func seedFeedItem(t *testing.T, d *DB, tweetID, author string, published int64) {
	t.Helper()
	seedFeedItemFetched(t, d, tweetID, author, published, published)
}

func seedFeedItemFetched(t *testing.T, d *DB, tweetID, author string, published, fetched int64) {
	t.Helper()
	if _, err := d.conn.Exec(`
		INSERT OR IGNORE INTO channel_follows (channel_id, followed_at)
		VALUES (?, ?)
	`, "twitter_"+author, fetched); err != nil {
		t.Fatalf("seed follow %s: %v", author, err)
	}
	if _, err := d.conn.Exec(`
		INSERT OR IGNORE INTO channel_profiles (channel_id, platform, handle, observed_at_ms)
		VALUES (?, 'twitter', ?, ?)
	`, "twitter_"+author, author, fetched); err != nil {
		t.Fatalf("seed profile %s: %v", author, err)
	}
	if _, err := d.conn.Exec(`INSERT INTO feed_items
		(tweet_id, channel_id, body_text, published_at, fetched_at, algo_interest, algo_scored_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tweetID, "twitter_"+author, "body", published, fetched, 1.0, 0); err != nil {
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
	base := time.Now().UnixMilli()

	// known_head is oldest. Four newer items across four distinct authors.
	seedFeedItem(t, d, "t_head", "old_user", base)
	seedFeedItem(t, d, "t1", "u1", base+1000)
	seedFeedItem(t, d, "t2", "u2", base+2000)
	seedFeedItem(t, d, "t3", "u3", base+3000)
	seedFeedItem(t, d, "t4", "u4", base+4000)

	// Snapshot ranks t2 and t4 highest. t1 and t3 are not in the snapshot.
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{
		{TweetID: "t2", RankPosition: 1, FinalScore: 99},
		{TweetID: "t4", RankPosition: 2, FinalScore: 50},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := d.GetNewPosterAvatars("t_head", 3)
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

	got, err := d.GetNewPosterAvatars("t_head", 3)
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

func TestGetNewPosterAvatars_UsesFetchedAtAnchor(t *testing.T) {
	d := openWritableTestDB(t)
	base := time.Now().UnixMilli()

	seedFeedItemFetched(t, d, "t_head", "old_user", base+10_000, base)
	seedFeedItemFetched(t, d, "t_newly_fetched", "late_arrival", base-10_000, base+1000)

	got, err := d.GetNewPosterAvatars("t_head", 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 || got[0].AuthorHandle != "late_arrival" {
		t.Fatalf("want newly fetched older post, got %+v", got)
	}
}

func TestGetNewPosterAvatars_EmptyKnownHead(t *testing.T) {
	d := openWritableTestDB(t)
	got, err := d.GetNewPosterAvatars("", 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestGetNewPosterAvatars_UnknownKnownHead(t *testing.T) {
	d := openWritableTestDB(t)
	got, err := d.GetNewPosterAvatars("missing_"+strconv.Itoa(int(time.Now().UnixNano())), 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestGetNewPosterAvatars_UsesCanonicalAvatarURLs(t *testing.T) {
	d := openWritableTestDB(t)
	base := time.Now().UnixMilli()

	seedFeedItem(t, d, "t_head", "old_user", base)
	seedFeedItem(t, d, "t1", "u1", base+1000)
	seedFeedItem(t, d, "t2", "u2", base+2000)
	seedChannelProfileAvatar(t, d, "u1", "https://pbs.twimg.com/profile_images/123/u1.jpg")

	got, err := d.GetNewPosterAvatars("t_head", 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 authors, got %+v", got)
	}
	if got[1].AuthorHandle != "u1" {
		t.Fatalf("want u1 second, got %+v", got)
	}
	if got[1].AuthorAvatarURL != "/api/media/avatar/twitter_u1" {
		t.Fatalf("want canonical avatar URL for u1, got %q", got[1].AuthorAvatarURL)
	}
	if got[0].AuthorAvatarURL != "/api/media/avatar/twitter_u2" {
		t.Fatalf("want proxy fallback for u2, got %q", got[0].AuthorAvatarURL)
	}
}
