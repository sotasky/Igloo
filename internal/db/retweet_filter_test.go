package db

import (
	"testing"
)

// fixtureChannel inserts a channels row plus a channel_settings row carrying
// the given include_reposts value (0 or 1). handle is the bare lowercase
// form; the channel id is derived as 'twitter_<handle>'.
func fixtureChannel(t *testing.T, d *DB, handle string, includeReposts int) {
	t.Helper()
	channelID := "twitter_" + handle
	if _, err := d.conn.Exec(`
		INSERT INTO channels (channel_id, name, platform)
		VALUES (?, ?, 'twitter')
	`, channelID, handle); err != nil {
		t.Fatalf("insert channel %s: %v", handle, err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES (?, 0)
	`, channelID); err != nil {
		t.Fatalf("insert channel_follows %s: %v", handle, err)
	}
	if _, err := d.conn.Exec(`
		INSERT INTO channel_settings (channel_id, include_reposts, updated_at)
		VALUES (?, ?, 0)
	`, channelID, includeReposts); err != nil {
		t.Fatalf("insert channel_settings %s: %v", handle, err)
	}
}

// fixtureFeedItem inserts a feed_items row. Pass empty strings for fields
// that don't apply.
func fixtureFeedItem(
	t *testing.T, d *DB,
	tweetID, sourceHandle, authorHandle string,
	isRetweet bool,
	contentHash string,
	quoteTweetID, quoteAuthorHandle string,
) {
	t.Helper()
	rt := 0
	if isRetweet {
		rt = 1
	}
	channelID := func(handle string) any {
		if handle == "" {
			return nil
		}
		return "twitter_" + handle
	}
	_, err := d.conn.Exec(`
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, is_retweet,
			content_hash, canonical_tweet_id,
			quote_tweet_id, quote_channel_id,
			published_at, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000, CAST(strftime('%s','now') AS INTEGER) * 1000)
	`,
		tweetID, channelID(sourceHandle), channelID(authorHandle), rt,
		nilOrStr(contentHash), tweetID,
		nilOrStr(quoteTweetID), channelID(quoteAuthorHandle),
	)
	if err != nil {
		t.Fatalf("insert feed_item %s: %v", tweetID, err)
	}
}

// fixtureRetweetSource inserts a retweet_sources row.
func fixtureRetweetSource(t *testing.T, d *DB, contentHash, retweeterHandle, tweetID string) {
	t.Helper()
	_, err := d.conn.Exec(`
		INSERT INTO retweet_sources (content_hash, retweeter_channel_id, tweet_id, published_at)
		VALUES (?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000)
	`, contentHash, "twitter_"+retweeterHandle, tweetID)
	if err != nil {
		t.Fatalf("insert retweet_source %s: %v", tweetID, err)
	}
}

func nilOrStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// queryVisibleTweetIDs runs ListFeedItemsPage and returns the tweet_ids
// in result order.
func queryVisibleTweetIDs(t *testing.T, d *DB) []string {
	t.Helper()
	items, err := d.ListFeedItemsPage(100, nil, false)
	if err != nil {
		t.Fatalf("ListFeedItemsPage: %v", err)
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.TweetID
	}
	return ids
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestRetweetFilter_PureRT_SoleRetweeter_Hidden(t *testing.T) {
	d := openWritableTestDB(t)
	fixtureChannel(t, d, "muted_a", 0) // muted
	fixtureFeedItem(t, d, "rt1", "muted_a", "original_b", true, "hashX", "", "")
	fixtureRetweetSource(t, d, "hashX", "muted_a", "rt1")

	ids := queryVisibleTweetIDs(t, d)
	if contains(ids, "rt1") {
		t.Errorf("rt1 should be hidden: only retweeter is muted; got %v", ids)
	}
}

func TestRetweetFilter_PureRT_OneOfTwoMuted_Visible(t *testing.T) {
	d := openWritableTestDB(t)
	fixtureChannel(t, d, "muted_a", 0)
	fixtureChannel(t, d, "open_b", 1)

	// Canonical row attributed to muted_a
	fixtureFeedItem(t, d, "rt1", "muted_a", "original_c", true, "hashX", "", "")
	fixtureRetweetSource(t, d, "hashX", "muted_a", "rt1")
	fixtureRetweetSource(t, d, "hashX", "open_b", "rt2")

	ids := queryVisibleTweetIDs(t, d)
	if !contains(ids, "rt1") {
		t.Errorf("rt1 should be visible: open_b also retweeted; got %v", ids)
	}
}

func TestRetweetFilter_SelfRT_AlwaysVisible(t *testing.T) {
	d := openWritableTestDB(t)
	fixtureChannel(t, d, "muted_a", 0)
	// source == author == muted_a
	fixtureFeedItem(t, d, "self1", "muted_a", "muted_a", true, "hashY", "", "")
	fixtureRetweetSource(t, d, "hashY", "muted_a", "self1")

	ids := queryVisibleTweetIDs(t, d)
	if !contains(ids, "self1") {
		t.Errorf("self-RT should pass through; got %v", ids)
	}
}

func TestRetweetFilter_QuoteTweet_OtherAuthor_Hidden(t *testing.T) {
	d := openWritableTestDB(t)
	fixtureChannel(t, d, "muted_a", 0)
	// is_retweet=0, quote_tweet_id set, author=muted_a, quote_author=other
	fixtureFeedItem(t, d, "qt1", "muted_a", "muted_a", false, "", "qsrc1", "other_b")

	ids := queryVisibleTweetIDs(t, d)
	if contains(ids, "qt1") {
		t.Errorf("qt1 should be hidden: muted author quoting other; got %v", ids)
	}
}

func TestRetweetFilter_SelfQT_Visible(t *testing.T) {
	d := openWritableTestDB(t)
	fixtureChannel(t, d, "muted_a", 0)
	// author == quote_author == muted_a
	fixtureFeedItem(t, d, "qt2", "muted_a", "muted_a", false, "", "qsrc2", "muted_a")

	ids := queryVisibleTweetIDs(t, d)
	if !contains(ids, "qt2") {
		t.Errorf("self-QT should pass through; got %v", ids)
	}
}

func TestRetweetFilter_UnmutedAuthor_Visible(t *testing.T) {
	d := openWritableTestDB(t)
	fixtureChannel(t, d, "open_a", 1)
	fixtureFeedItem(t, d, "qt3", "open_a", "open_a", false, "", "qsrc3", "other_b")

	ids := queryVisibleTweetIDs(t, d)
	if !contains(ids, "qt3") {
		t.Errorf("unmuted author's QT should be visible; got %v", ids)
	}
}

func TestRetweetFilter_RankedPath_DedupAware(t *testing.T) {
	d := openWritableTestDB(t)
	fixtureChannel(t, d, "muted_a", 0)
	fixtureChannel(t, d, "open_b", 1)
	fixtureFeedItem(t, d, "rt_ranked", "muted_a", "original_c", true, "hashR", "", "")
	fixtureRetweetSource(t, d, "hashR", "muted_a", "rt_ranked")
	fixtureRetweetSource(t, d, "hashR", "open_b", "rt_ranked_b")

	items, err := d.ListRankedFeedItems(100, 0)
	if err != nil {
		t.Fatalf("ListRankedFeedItems: %v", err)
	}
	found := false
	for _, it := range items {
		if it.TweetID == "rt_ranked" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("rt_ranked should be visible (open_b also retweeted)")
	}
}
