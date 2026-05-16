package feed

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestEnrichMediaStatus(t *testing.T) {
	item := model.FeedItem{
		TweetID:   "123",
		MediaJSON: `[{"type":"photo","url":"https://example.com/photo.jpg"}]`,
	}
	item.ParseMedia()

	jobs := map[string]model.FeedMediaJob{
		"123": {TweetID: "123", Status: "completed", MediaKind: "image", SlideCount: 1},
	}
	enrichMediaStatus(&item, jobs)

	if item.MediaStatus != "ready" {
		t.Errorf("expected status ready, got %q", item.MediaStatus)
	}
	if item.MediaKind != "image" {
		t.Errorf("expected kind image, got %q", item.MediaKind)
	}
}

func TestEnrichMediaStatusCDN(t *testing.T) {
	item := model.FeedItem{
		TweetID:   "456",
		MediaJSON: `[{"type":"photo","url":"https://cdn.example.com/img.jpg"}]`,
	}
	item.ParseMedia()

	enrichMediaStatus(&item, map[string]model.FeedMediaJob{})

	if item.MediaStatus != "cdn" {
		t.Errorf("expected cdn, got %q", item.MediaStatus)
	}
	if item.MediaKind != "image" {
		t.Errorf("expected image, got %q", item.MediaKind)
	}
}

func TestEnrichMediaStatusQuoteOnly(t *testing.T) {
	// Parent has no media but a completed job (quote-only media tweet).
	// MediaKind and MediaStatus should NOT be set on the parent.
	item := model.FeedItem{
		TweetID:        "789",
		QuoteTweetID:   "qt_789",
		QuoteMediaJSON: `[{"type":"photo","url":"https://example.com/qt.jpg"}]`,
	}
	item.ParseMedia()

	jobs := map[string]model.FeedMediaJob{
		"789": {TweetID: "789", Status: "completed", MediaKind: "image", SlideCount: 0},
	}

	enrichMediaStatus(&item, jobs)

	if item.MediaStatus != "" {
		t.Errorf("expected empty MediaStatus for quote-only tweet, got %q", item.MediaStatus)
	}
	if item.MediaKind != "" {
		t.Errorf("expected empty MediaKind for quote-only tweet, got %q", item.MediaKind)
	}
}

func TestAnnotateChannelFlags(t *testing.T) {
	item := model.FeedItem{
		AuthorHandle: "user_a",
		SourceHandle: "user_b",
	}
	channels := map[string]model.Channel{
		"twitter_user_a": {ChannelID: "twitter_user_a", IsSubscribed: true, IsStarred: true},
	}

	annotateChannelFlags(&item, channels, map[string]string{})

	if item.ChannelID != "twitter_user_a" {
		t.Errorf("expected channel_id twitter_user_a, got %q", item.ChannelID)
	}
	if !item.ChannelIsFollowed {
		t.Error("expected channel_is_followed=true")
	}
	if !item.ChannelIsStarred {
		t.Error("expected channel_is_starred=true")
	}
}

func TestAnnotateChannelFlagsKeepsAuthorFollowTargetSeparateFromFollowedSource(t *testing.T) {
	item := model.FeedItem{
		AuthorHandle: "post_author",
		SourceHandle: "followed_source",
		IsRetweet:    true,
		QuoteTweetID: "quoted_status",
	}
	channels := map[string]model.Channel{
		"twitter_followed_source": {ChannelID: "twitter_followed_source", IsSubscribed: true},
	}

	annotateChannelFlags(&item, channels, map[string]string{})

	if item.ChannelID != "twitter_post_author" {
		t.Fatalf("expected channel_id twitter_post_author, got %q", item.ChannelID)
	}
	if item.ReposterChannelID != "twitter_followed_source" {
		t.Fatalf("expected repost source twitter_followed_source, got %q", item.ReposterChannelID)
	}
	if !item.ChannelIsFollowed {
		t.Fatal("expected channel_is_followed to inherit followed source for ranking")
	}
	if item.FollowTargetFollowed {
		t.Fatal("expected follow target to remain the unfollowed displayed author")
	}
}

func TestAnnotateChannelFlags_ResolvesQuoteAvatarByKnownURL(t *testing.T) {
	item := model.FeedItem{
		AuthorHandle:           "user_a",
		QuoteAuthorAvatarURL:   "https://pbs.twimg.com/profile_images/777/photo.jpg",
		QuoteAuthorHandle:      "",
		QuoteAuthorDisplayName: "Quote User",
	}

	annotateChannelFlags(
		&item,
		map[string]model.Channel{},
		map[string]string{"https://pbs.twimg.com/profile_images/777/photo.jpg": "twitter_quote_user"},
	)

	if item.QuoteAuthorAvatarURL != "/api/media/avatar/twitter_quote_user" {
		t.Fatalf("expected quote avatar to be proxied, got %q", item.QuoteAuthorAvatarURL)
	}
	if item.QuoteChannelID != "twitter_quote_user" {
		t.Fatalf("expected quote channel id to resolve, got %q", item.QuoteChannelID)
	}
	if item.QuoteAuthorHandle != "quote_user" {
		t.Fatalf("expected quote author handle backfilled from channel id, got %q", item.QuoteAuthorHandle)
	}
}

func TestAnnotateChannelFlags_FallsBackToSyntheticQuoteAvatarID(t *testing.T) {
	avatarURL := "https://pbs.twimg.com/profile_images/888/photo.jpg"
	item := model.FeedItem{
		AuthorHandle:         "user_a",
		QuoteAuthorAvatarURL: avatarURL,
	}

	annotateChannelFlags(&item, map[string]model.Channel{}, map[string]string{})

	want := "/api/media/avatar/" + model.SyntheticTwitterAvatarChannelID(avatarURL)
	if item.QuoteAuthorAvatarURL != want {
		t.Fatalf("expected synthetic quote avatar URL %q, got %q", want, item.QuoteAuthorAvatarURL)
	}
	if item.QuoteAuthorHandle != "" {
		t.Fatalf("expected no synthetic handle backfill, got %q", item.QuoteAuthorHandle)
	}
}

func TestEnrichFeedItemsBackfillsDisplayNamesFromChannelProfiles(t *testing.T) {
	d := openWritableFeedTestDB(t)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_author_alpha",
		Platform:    "twitter",
		Handle:      "author_alpha",
		DisplayName: "Display From Profile",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_reposter_beta",
		Platform:    "twitter",
		Handle:      "reposter_beta",
		DisplayName: "Reposter Name",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile reposter: %v", err)
	}

	items := []model.FeedItem{{
		TweetID:           "tweet_1",
		AuthorHandle:      "author_alpha",
		RetweetedByHandle: "reposter_beta",
		QuoteTweetID:      "quote_1",
		QuoteAuthorHandle: "author_alpha",
		QuoteMediaJSON:    `[{"type":"photo","url":"https://example.com/q.jpg"}]`,
		PublishedAt:       nil,
	}}
	items[0].ParseMedia()

	got := EnrichFeedItems(d, items, "")
	if got[0].AuthorDisplayName != "Display From Profile" {
		t.Fatalf("author display name: got %q", got[0].AuthorDisplayName)
	}
	if got[0].RetweetedByDisplayName != "Reposter Name" {
		t.Fatalf("retweeted-by display name: got %q", got[0].RetweetedByDisplayName)
	}
	if got[0].QuoteAuthorDisplayName != "Display From Profile" {
		t.Fatalf("quote author display name: got %q", got[0].QuoteAuthorDisplayName)
	}
}

func TestEnrichFeedItemsCollapsesSiblingReplyBranchesToFirstRankedLeaf(t *testing.T) {
	d := openWritableFeedTestDB(t)
	rootAt := time.Unix(100, 0).UTC()
	parentAAt := time.Unix(110, 0).UTC()
	leafAAt := time.Unix(120, 0).UTC()
	parentBAt := time.Unix(130, 0).UTC()
	leafBAt := time.Unix(140, 0).UTC()

	_, err := d.UpsertFeedItems([]model.FeedItem{
		{TweetID: "thread_root", AuthorHandle: "sample_author_root", BodyText: "root", PublishedAt: &rootAt, FetchedAt: rootAt, ContentHash: "hash_thread_root", CanonicalTweetID: "thread_root"},
		{TweetID: "thread_parent_a", AuthorHandle: "sample_author_parent", BodyText: "parent a", IsReply: true, ReplyToHandle: "sample_author_root", ReplyToStatus: "thread_root", PublishedAt: &parentAAt, FetchedAt: parentAAt, ContentHash: "hash_thread_parent_a", CanonicalTweetID: "thread_parent_a"},
		{TweetID: "thread_leaf_a", AuthorHandle: "sample_author_leaf_a", BodyText: "leaf a", IsReply: true, ReplyToHandle: "sample_author_parent", ReplyToStatus: "thread_parent_a", PublishedAt: &leafAAt, FetchedAt: leafAAt, ContentHash: "hash_thread_leaf_a", CanonicalTweetID: "thread_leaf_a"},
		{TweetID: "thread_parent_b", AuthorHandle: "sample_author_parent", BodyText: "parent b", IsReply: true, ReplyToHandle: "sample_author_root", ReplyToStatus: "thread_root", PublishedAt: &parentBAt, FetchedAt: parentBAt, ContentHash: "hash_thread_parent_b", CanonicalTweetID: "thread_parent_b"},
		{TweetID: "thread_leaf_b", AuthorHandle: "sample_author_leaf_b", BodyText: "leaf b", IsReply: true, ReplyToHandle: "sample_author_parent", ReplyToStatus: "thread_parent_b", PublishedAt: &leafBAt, FetchedAt: leafBAt, ContentHash: "hash_thread_leaf_b", CanonicalTweetID: "thread_leaf_b"},
	})
	if err != nil {
		t.Fatalf("UpsertFeedItems: %v", err)
	}

	stored, err := d.GetFeedItemsForTweetIDs([]string{
		"thread_root",
		"thread_parent_a",
		"thread_leaf_a",
		"thread_parent_b",
		"thread_leaf_b",
	})
	if err != nil {
		t.Fatalf("GetFeedItemsForTweetIDs: %v", err)
	}
	input := []model.FeedItem{
		stored["thread_leaf_b"],
		stored["thread_leaf_a"],
		stored["thread_parent_b"],
		stored["thread_parent_a"],
		stored["thread_root"],
	}

	got := EnrichFeedItems(d, input, "")
	gotIDs := make([]string, 0, len(got))
	for _, item := range got {
		gotIDs = append(gotIDs, item.TweetID)
	}
	if want := []string{"thread_leaf_b"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("enriched IDs = %v, want %v", gotIDs, want)
	}

	gotChainIDs := make([]string, 0, len(got[0].ThreadChain))
	for _, item := range got[0].ThreadChain {
		gotChainIDs = append(gotChainIDs, item.TweetID)
	}
	if want := []string{"thread_root", "thread_parent_b"}; !reflect.DeepEqual(gotChainIDs, want) {
		t.Fatalf("thread chain IDs = %v, want %v", gotChainIDs, want)
	}
}

func TestEnrichFeedItemsRepairsHandleLikeDisplayNamesFromProfiles(t *testing.T) {
	d := openWritableFeedTestDB(t)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_author_alpha",
		Platform:    "twitter",
		Handle:      "author_alpha",
		DisplayName: "Readable Author",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile author: %v", err)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_reposter_beta",
		Platform:    "twitter",
		Handle:      "reposter_beta",
		DisplayName: "Readable Reposter",
	}); err != nil {
		t.Fatalf("UpsertChannelProfile reposter: %v", err)
	}

	items := []model.FeedItem{{
		TweetID:                "tweet_2",
		AuthorHandle:           "author_alpha",
		AuthorDisplayName:      "@author_alpha",
		RetweetedByHandle:      "reposter_beta",
		RetweetedByDisplayName: "reposter_beta",
		QuoteTweetID:           "quote_2",
		QuoteAuthorHandle:      "author_alpha",
		QuoteAuthorDisplayName: "author_alpha",
	}}
	got := EnrichFeedItems(d, items, "")
	if got[0].AuthorDisplayName != "Readable Author" {
		t.Fatalf("author display name: got %q", got[0].AuthorDisplayName)
	}
	if got[0].RetweetedByDisplayName != "Readable Reposter" {
		t.Fatalf("retweeted-by display name: got %q", got[0].RetweetedByDisplayName)
	}
	if got[0].QuoteAuthorDisplayName != "Readable Author" {
		t.Fatalf("quote author display name: got %q", got[0].QuoteAuthorDisplayName)
	}
}

func TestEnrichFeedItemsUsesConfiguredTranslationTarget(t *testing.T) {
	d := openWritableFeedTestDB(t)
	if err := d.SetSetting("", "translate_target_lang", "fr"); err != nil {
		t.Fatalf("SetSetting translate target: %v", err)
	}
	if err := d.SetTranslation("tweet_translate", "body", "tr", "en", "Hello"); err != nil {
		t.Fatalf("SetTranslation en: %v", err)
	}
	if err := d.SetTranslation("tweet_translate", "body", "tr", "fr", "Bonjour"); err != nil {
		t.Fatalf("SetTranslation fr: %v", err)
	}

	got := EnrichFeedItems(d, []model.FeedItem{{
		TweetID:      "tweet_translate",
		AuthorHandle: "author_alpha",
	}}, "")
	if got[0].BodyTranslation != "Bonjour" {
		t.Fatalf("BodyTranslation = %q, want configured fr translation", got[0].BodyTranslation)
	}
}

func TestEnrichFeedItemsSkipsConfiguredTranslationLanguages(t *testing.T) {
	d := openWritableFeedTestDB(t)
	if err := d.SetSetting("", "translate_skip_langs", "ja"); err != nil {
		t.Fatalf("SetSetting translate skip langs: %v", err)
	}
	if err := d.SetTranslation("tweet_translate", "body", "ja", "en", "Blanc"); err != nil {
		t.Fatalf("SetTranslation body: %v", err)
	}

	got := EnrichFeedItems(d, []model.FeedItem{{
		TweetID:      "tweet_translate",
		AuthorHandle: "author_alpha",
	}}, "")
	if got[0].BodyTranslation != "" {
		t.Fatalf("BodyTranslation = %q, want empty for skipped source language", got[0].BodyTranslation)
	}
}

func TestEnrichFeedItemsSkipsNoopTranslations(t *testing.T) {
	d := openWritableFeedTestDB(t)
	if err := d.SetTranslation("sample_tweet_translate", "body", "Indonesian", "en", "@sample_parent What if sample topic"); err != nil {
		t.Fatalf("SetTranslation body: %v", err)
	}
	if err := d.SetTranslation("sample_tweet_translate", "quote", "Indonesian", "en", "same quote text"); err != nil {
		t.Fatalf("SetTranslation quote: %v", err)
	}

	got := EnrichFeedItems(d, []model.FeedItem{{
		TweetID:       "sample_tweet_translate",
		AuthorHandle:  "sample_author_alpha",
		BodyText:      "@sample_parent What if sample topic",
		QuoteTweetID:  "sample_quote_1",
		QuoteBodyText: "same\nquote text",
	}}, "")
	if got[0].BodyTranslation != "" || got[0].BodySourceLang != "" {
		t.Fatalf("body translation = (%q, %q), want empty no-op", got[0].BodyTranslation, got[0].BodySourceLang)
	}
	if got[0].QuoteTranslation != "" || got[0].QuoteSourceLang != "" {
		t.Fatalf("quote translation = (%q, %q), want empty no-op", got[0].QuoteTranslation, got[0].QuoteSourceLang)
	}
}

func openWritableFeedTestDB(t *testing.T) *db.DB {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "igloo-feed-test-*.db")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()

	d, err := db.Open(tmpPath, filepath.Join(t.TempDir(), "data"))
	if err != nil {
		_ = os.Remove(tmpPath)
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Close()
		_ = os.Remove(tmpPath)
	})
	return d
}
