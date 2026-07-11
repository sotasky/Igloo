package db

import (
	"slices"
	"testing"
)

func TestListAndroidSyncFeedProjectionUsesStableIdentityOwnersAndCanonicalChildren(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name) VALUES
			('twitter_sample_source', 'twitter', 'source', 'Source'),
			('twitter_author', 'twitter', 'author', 'Profile Name'),
			('twitter_quote', 'twitter', 'quote', 'Quote Name'),
			('twitter_sample_reply', 'twitter', 'reply', 'Reply Name'),
			('twitter_reposter', 'twitter', 'reposter', 'Reposter Name'),
			('twitter_retweeter', 'twitter', 'retweeter', 'Retweeter Name');
		INSERT INTO feed_items (tweet_id, channel_id, published_at, fetched_at)
		VALUES ('sample_root', 'twitter_sample_reply', 1, 1);
		INSERT INTO feed_items (
			tweet_id, channel_id, reply_channel_id, reply_to_status, is_reply,
			published_at, fetched_at
		) VALUES ('sample_parent', 'twitter_sample_reply', 'twitter_sample_reply', 'sample_root', 1, 2, 2);
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, body_text, is_retweet,
			reposter_channel_id, quote_tweet_id, quote_channel_id, quote_body_text,
			reply_channel_id, reply_to_status, is_reply, content_hash, published_at, fetched_at
		) VALUES (
			'sample_tweet', 'twitter_sample_source', 'twitter_author', 'Body', 1,
			'twitter_reposter', 'sample_quote', 'twitter_quote', 'Quoted',
			'twitter_sample_reply', 'sample_parent', 1, 'sample_hash', 3, 3
		);
		INSERT INTO translations (tweet_id, field, source_lang, target_lang, translated_text, translated_at)
		VALUES
			('sample_tweet', 'body', 'en', 'tr', 'Gövde', 3),
			('sample_tweet', 'quote', 'ja', 'tr', 'Alıntı', 3);
		INSERT INTO retweet_sources (
			content_hash, retweeter_channel_id, tweet_id, published_at
		) VALUES ('sample_hash', 'twitter_retweeter', 'sample_tweet', 3);
		INSERT INTO settings (key, value) VALUES
			('translate_target_lang', 'tr'),
			('translate_skip_langs', 'ja')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
	`); err != nil {
		t.Fatal(err)
	}

	projection, err := d.ListAndroidSyncFeedProjection([]string{"sample_tweet"})
	if err != nil {
		t.Fatal(err)
	}
	item := projection.Rows["sample_tweet"].Item
	if item.SourceHandle != "" || item.AuthorHandle != "" || item.QuoteAuthorHandle != "" ||
		item.AuthorDisplayName != "" || item.RetweetedByHandle != "" ||
		item.RetweetedByDisplayName != "" || item.QuoteAuthorDisplayName != "" ||
		item.ReplyToHandle != "" {
		t.Fatalf("presentation fields leaked into sync projection = %+v", item)
	}
	if item.SourceChannelID != "twitter_sample_source" || item.ChannelID != "twitter_author" ||
		item.ReposterChannelID != "twitter_reposter" || item.QuoteChannelID != "twitter_quote" ||
		item.ReplyChannelID != "twitter_sample_reply" {
		t.Fatalf("identity owners = %+v", item)
	}
	if item.BodyTranslation != "Gövde" || item.QuoteTranslation != "" {
		t.Fatalf("translations = body %q quote %q", item.BodyTranslation, item.QuoteTranslation)
	}
	closure, err := d.ListAndroidSyncFeedClosureIDs([]string{"sample_tweet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(closure) != 3 || closure[0] != "sample_parent" || closure[1] != "sample_root" || closure[2] != "sample_tweet" {
		t.Fatalf("thread closure = %+v", closure)
	}
	rows := projection.RetweetSources["sample_hash"]
	if len(rows) != 1 || rows[0].RetweeterChannelID != "twitter_retweeter" || rows[0].RetweeterHandle != "" {
		t.Fatalf("retweet sources = %+v", rows)
	}
}

func TestListAndroidSyncFeedHydrationIDsIncludesPeersQuotesAndAncestors(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, content_hash, quote_tweet_id, reply_to_status, published_at, fetched_at) VALUES
			('sample_reply_root', '', '', '', 1, 1),
			('sample_requested', 'sample_hash', 'sample_quote', 'sample_reply_root', 5, 5),
			('sample_peer', 'sample_hash', 'sample_peer_quote', '', 2, 2),
			('sample_quote_root', '', '', '', 1, 1),
			('sample_quote', '', '', 'sample_quote_root', 1, 1),
			('sample_peer_quote', '', '', '', 1, 1),
			('sample_unrelated', '', '', '', 9, 9)
	`); err != nil {
		t.Fatal(err)
	}
	got, err := d.ListAndroidSyncFeedHydrationIDs([]string{"sample_requested"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"sample_peer", "sample_peer_quote", "sample_quote", "sample_quote_root",
		"sample_reply_root", "sample_requested",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("hydration ids = %v, want %v", got, want)
	}
}

func TestListAndroidSyncFeedEffectiveRecencyPropagatesRetweetThroughReplies(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (
			tweet_id, content_hash, reply_to_status, published_at, fetched_at
		) VALUES
			('sample_ancestor', '', '', 10, 10),
			('sample_reply', 'sample_reply_hash', 'sample_ancestor', 20, 20);
		INSERT INTO retweet_sources (
			content_hash, retweeter_channel_id, tweet_id, published_at
		) VALUES ('sample_reply_hash', 'twitter_sample_retweeter', 'sample_reply', 1000);
	`); err != nil {
		t.Fatal(err)
	}

	got, err := d.ListAndroidSyncFeedEffectiveRecency([]string{"sample_ancestor"})
	if err != nil {
		t.Fatal(err)
	}
	if got["sample_ancestor"] != 1000 {
		t.Fatalf("ancestor effective recency = %d, want 1000", got["sample_ancestor"])
	}
}

func TestListAndroidSyncFeedEffectiveRecencyBoundsEachRequestedRoot(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		WITH RECURSIVE chain(depth) AS (
			VALUES (0)
			UNION ALL
			SELECT depth + 1 FROM chain WHERE depth < 51
		)
		INSERT INTO feed_items (tweet_id, reply_to_status, published_at, fetched_at)
		SELECT printf('sample_thread_%02d', depth),
		       CASE WHEN depth = 0 THEN '' ELSE printf('sample_thread_%02d', depth - 1) END,
		       CASE depth WHEN 50 THEN 500 WHEN 51 THEN 1000 ELSE 0 END,
		       CASE depth WHEN 50 THEN 500 WHEN 51 THEN 1000 ELSE 0 END
		FROM chain
	`); err != nil {
		t.Fatal(err)
	}

	got, err := d.ListAndroidSyncFeedEffectiveRecency([]string{"sample_thread_00", "sample_thread_01"})
	if err != nil {
		t.Fatal(err)
	}
	if got["sample_thread_00"] != 500 {
		t.Fatalf("depth-51 recency crossed root bound: %d", got["sample_thread_00"])
	}
	if got["sample_thread_01"] != 1000 {
		t.Fatalf("depth-50 recency did not reach overlapping root: %d", got["sample_thread_01"])
	}
}
