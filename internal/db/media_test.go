package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/model"

	_ "modernc.org/sqlite"
)

// openFreshTestDB creates a brand-new empty DB (no production data needed).
// EnsureSchema now creates all tables including legacy ones.
func openFreshTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	d, err := Open(dbPath, tmpDir)
	if err != nil {
		t.Fatalf("Open fresh DB: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func seedAvatarCache(t *testing.T, d *DB, channelID, avatarURL, filename string, content []byte) {
	t.Helper()
	platform, handle, ok := strings.Cut(channelID, "_")
	if !ok {
		t.Fatalf("invalid channelID %q", channelID)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: channelID,
		Platform:  platform,
		Handle:    handle,
		AvatarURL: avatarURL,
	}); err != nil {
		t.Fatalf("seed channel profile %s: %v", channelID, err)
	}
	dir := filepath.Join(d.dataDir, "thumbnails", "avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir avatar dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), content, 0o644); err != nil {
		t.Fatalf("seed avatar file %s: %v", filename, err)
	}
}

func seedBannerCache(t *testing.T, d *DB, channelID, bannerURL, filename string, content []byte) {
	t.Helper()
	platform, handle, ok := strings.Cut(channelID, "_")
	if !ok {
		t.Fatalf("invalid channelID %q", channelID)
	}
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: channelID,
		Platform:  platform,
		Handle:    handle,
		BannerURL: bannerURL,
	}); err != nil {
		t.Fatalf("seed channel profile %s: %v", channelID, err)
	}
	dir := filepath.Join(d.dataDir, "thumbnails", "banners")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir banner dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), content, 0o644); err != nil {
		t.Fatalf("seed banner file %s: %v", filename, err)
	}
}

func TestInsertAndGetMediaFile(t *testing.T) {
	d := openWritableTestDB(t)

	mf := model.MediaFile{
		OwnerType:  "feed_media",
		OwnerID:    "test_tweet_001",
		MediaIndex: 0,
		FilePath:   "feed_media/test_tweet_001_0.jpg",
		MediaType:  "photo",
		SourceURL:  "https://example.com/img.jpg",
		FileSize:   12345,
	}

	if err := d.InsertMediaFile(mf); err != nil {
		t.Fatalf("InsertMediaFile: %v", err)
	}

	got, err := d.GetMediaFilePath("feed_media", "test_tweet_001", 0)
	if err != nil {
		t.Fatalf("GetMediaFilePath: %v", err)
	}
	if got != mf.FilePath {
		t.Errorf("expected path %q, got %q", mf.FilePath, got)
	}
}

func TestInsertMediaFileDuplicate(t *testing.T) {
	d := openWritableTestDB(t)

	mf := model.MediaFile{
		OwnerType:  "feed_media",
		OwnerID:    "test_tweet_dup",
		MediaIndex: 0,
		FilePath:   "feed_media/dup.jpg",
	}

	if err := d.InsertMediaFile(mf); err != nil {
		t.Fatalf("first InsertMediaFile: %v", err)
	}
	// Second insert with same key should be silently ignored.
	if err := d.InsertMediaFile(mf); err != nil {
		t.Fatalf("second InsertMediaFile (duplicate) should not error: %v", err)
	}
}

func TestInsertMediaFileBatch(t *testing.T) {
	d := openWritableTestDB(t)

	files := []model.MediaFile{
		{OwnerType: "feed_media", OwnerID: "batch_001", MediaIndex: 0, FilePath: "feed_media/batch_001_0.jpg", MediaType: "photo"},
		{OwnerType: "feed_media", OwnerID: "batch_001", MediaIndex: 1, FilePath: "feed_media/batch_001_1.jpg", MediaType: "photo"},
	}

	if err := d.InsertMediaFileBatch(files); err != nil {
		t.Fatalf("InsertMediaFileBatch: %v", err)
	}

	path, err := d.GetMediaFilePath("feed_media", "batch_001", 1)
	if err != nil {
		t.Fatalf("GetMediaFilePath index 1: %v", err)
	}
	if path != "feed_media/batch_001_1.jpg" {
		t.Errorf("unexpected path %q", path)
	}
}

func TestGetMediaFilePathNotFound(t *testing.T) {
	d := openWritableTestDB(t)

	_, err := d.GetMediaFilePath("feed_media", "nonexistent_xyz", 0)
	if err == nil {
		t.Error("expected error for nonexistent media file, got nil")
	}
}

func TestGetMediaManifestV2_BookmarkedScopeCoversAllAssetKinds(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, quote_tweet_id) VALUES
		('tweet_001', 'handle_a', 'author_a', '[{"type":"photo"}]', ''),
		('tweet_002', 'handle_b', 'author_b', '[{"type":"video"}]', 'quote_001')
	`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES
		('tweet_001', 'completed', 'image'),
		('tweet_002', 'completed', 'video')
	`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'tweet_001', 0, 'feed_media/tweet_001_0.jpg', 'photo', 5000),
		('feed_media', 'tweet_002', 0, 'feed_media/tweet_002_0.mp4', 'video', 90000),
		('quote_media', 'quote_001', 0, 'quote_media/quote_001_0.jpg', 'photo', 3000)
	`)
	seedAvatarCache(t, d, "twitter_author_a", "https://pbs.twimg.com/profile_images/a_normal.jpg", "twitter_author_a.jpg", []byte("jpg"))
	_ =
		// Bookmarked scope pulls from bookmarks.video_id — exercise the scope filter.
		d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'tweet_001'), ('', 'tweet_002')`)

	entries, nextMarker, _, err := d.GetMediaManifestV2("bookmarked", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}
	if nextMarker == "" {
		t.Errorf("expected non-empty next_marker")
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}

	// feed_media tweet_001 → post_media entry serving /api/media/slide.
	if e, ok := byKey["post_media/tweet_001"]; !ok {
		t.Error("expected post_media entry for tweet_001")
	} else {
		if e.ServerURL != "/api/media/slide/tweet_001/0" {
			t.Errorf("server_url = %q, want slide URL", e.ServerURL)
		}
		if e.AssetID != "twitter_tweet_tweet_001_post_media" {
			t.Errorf("asset_id = %q, want twitter_tweet_tweet_001_post_media", e.AssetID)
		}
		if e.Scope != "bookmarked" {
			t.Errorf("scope = %q, want bookmarked", e.Scope)
		}
		if e.Bucket != "twitter_media" {
			t.Errorf("bucket = %q, want twitter_media", e.Bucket)
		}
	}

	// Video tweet → /api/media/stream + content_type video/mp4.
	if e, ok := byKey["post_media/tweet_002"]; !ok {
		t.Error("expected post_media entry for tweet_002")
	} else {
		if e.ServerURL != "/api/media/stream/tweet_002" {
			t.Errorf("video server_url = %q", e.ServerURL)
		}
		if e.ContentType != "video/mp4" {
			t.Errorf("video content_type = %q", e.ContentType)
		}
	}

	// quote_media branch — quote_001 hangs off tweet_002.
	if _, ok := byKey["post_media/quote_001"]; !ok {
		t.Error("expected quote_media entry for quote_001")
	}

	// avatar entry for twitter_author_a.
	if e, ok := byKey["avatar/twitter_author_a"]; !ok {
		t.Error("expected avatar entry for twitter_author_a")
	} else {
		if e.ServerURL != "/api/media/avatar/twitter_author_a" {
			t.Errorf("avatar server_url = %q", e.ServerURL)
		}
		if e.AssetID != "twitter_channel_twitter_author_a_avatar" {
			t.Errorf("avatar asset_id = %q", e.AssetID)
		}
	}
}

func TestGetMediaManifestV2_IncludesRetweeterAvatarsForVisibleTweets(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, content_hash, media_json) VALUES
		('tweet_rt', 'poster_a', 'author_a', 'hash_rt', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES ('tweet_rt', 'completed', 'image')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'tweet_rt', 0, 'feed_media/tweet_rt_0.jpg', 'photo', 5000)`)
	_ = d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'tweet_rt')`)
	_ = d.ExecRaw(`INSERT INTO retweet_sources (content_hash, retweeter_handle, retweeter_display_name, tweet_id, published_at) VALUES
		('hash_rt', 'retweeter_b', 'Retweeter B', 'tweet_rt', 1)`)
	seedAvatarCache(
		t,
		d,
		"twitter_retweeter_b",
		"https://pbs.twimg.com/profile_images/retweeter_b_normal.jpg",
		"twitter_retweeter_b.jpg",
		[]byte("jpg"),
	)

	entries, _, _, err := d.GetMediaManifestV2("bookmarked", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}

	e, ok := byKey["avatar/twitter_retweeter_b"]
	if !ok {
		t.Fatal("expected avatar entry for retweeter_b")
	}
	if e.ServerURL != "/api/media/avatar/twitter_retweeter_b" {
		t.Fatalf("avatar server_url = %q, want /api/media/avatar/twitter_retweeter_b", e.ServerURL)
	}
	if e.AssetID != "twitter_channel_twitter_retweeter_b_avatar" {
		t.Fatalf("avatar asset_id = %q", e.AssetID)
	}
}

func TestGetMediaManifestV2_EmitsProfileAvatarBeforeServerFileExists(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('tweet_avatar_only', 'poster_a', 'author_a', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES ('tweet_avatar_only', 'completed', 'image')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'tweet_avatar_only', 0, 'feed_media/tweet_avatar_only_0.jpg', 'photo', 5000)`)
	_ = d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'tweet_avatar_only')`)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_author_a",
		Platform:  "twitter",
		Handle:    "author_a",
		AvatarURL: "https://pbs.twimg.com/profile_images/author_a_normal.jpg",
	}); err != nil {
		t.Fatalf("seed channel profile: %v", err)
	}

	entries, _, _, err := d.GetMediaManifestV2("bookmarked", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}
	e, ok := byKey["avatar/twitter_author_a"]
	if !ok {
		t.Fatal("expected avatar entry before server avatar file exists")
	}
	if e.ServerURL != "/api/media/avatar/twitter_author_a" {
		t.Fatalf("avatar server_url = %q, want /api/media/avatar/twitter_author_a", e.ServerURL)
	}
	if e.SizeHint != 0 {
		t.Fatalf("avatar size_hint = %d, want 0 before file exists", e.SizeHint)
	}
	if e.ContentType != "image/jpeg" {
		t.Fatalf("avatar content_type = %q, want image/jpeg fallback", e.ContentType)
	}
}

func TestGetMediaManifestV2_EmitsInlineFeedAvatarAfterProfileSeed(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, author_avatar_url, media_json) VALUES
		('tweet_inline_avatar', 'poster_a', 'author_inline', 'https://pbs.twimg.com/profile_images/555/author-inline_normal.jpg', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES ('tweet_inline_avatar', 'completed', 'image')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'tweet_inline_avatar', 0, 'feed_media/tweet_inline_avatar_0.jpg', 'photo', 5000)`)
	_ = d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'tweet_inline_avatar')`)
	if _, err := d.SeedChannelProfileRows(); err != nil {
		t.Fatalf("SeedChannelProfileRows: %v", err)
	}

	entries, _, _, err := d.GetMediaManifestV2("bookmarked", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}
	e, ok := byKey["avatar/twitter_author_inline"]
	if !ok {
		t.Fatal("expected avatar entry from feed-provided avatar URL")
	}
	if e.ServerURL != "/api/media/avatar/twitter_author_inline" {
		t.Fatalf("avatar server_url = %q, want /api/media/avatar/twitter_author_inline", e.ServerURL)
	}
	if e.SizeHint != 0 {
		t.Fatalf("avatar size_hint = %d, want 0 before file exists", e.SizeHint)
	}
}

func TestGetMediaManifestV2_SubscriptionsAvatarsPageFromProfiles(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_profile_only",
		Platform:  "twitter",
		Handle:    "profile_only",
		AvatarURL: "https://cdn.example/profile-only.jpg",
	}); err != nil {
		t.Fatalf("seed channel profile: %v", err)
	}

	entries, _, _, err := d.GetMediaManifestV2("subscriptions", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}

	for _, e := range entries {
		if e.AssetKind == "avatar" && e.OwnerID == "twitter_profile_only" {
			if e.ServerURL != "/api/media/avatar/twitter_profile_only" {
				t.Fatalf("avatar server_url = %q, want /api/media/avatar/twitter_profile_only", e.ServerURL)
			}
			return
		}
	}
	t.Fatal("expected subscriptions avatar entry from channel_profiles")
}

func TestGetMediaManifestV2_SubscriptionsIncludesCachedChannelBanners(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'tiktok_banner_a')`)
	seedBannerCache(
		t,
		d,
		"tiktok_banner_a",
		"igloo:synth-banner:video_1",
		"tiktok_banner_a.jpg",
		[]byte{0xff, 0xd8, 0xff, 0xd9},
	)

	entries, _, _, err := d.GetMediaManifestV2("subscriptions", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}
	e, ok := byKey["banner/tiktok_banner_a"]
	if !ok {
		t.Fatal("expected banner entry for followed TikTok channel")
	}
	if e.ServerURL != "/api/media/banner/tiktok_banner_a" {
		t.Fatalf("banner server_url = %q, want /api/media/banner/tiktok_banner_a", e.ServerURL)
	}
	if e.AssetID != "tiktok_channel_tiktok_banner_a_banner" {
		t.Fatalf("banner asset_id = %q, want tiktok_channel_tiktok_banner_a_banner", e.AssetID)
	}
	if e.Bucket != "banners" {
		t.Fatalf("banner bucket = %q, want banners", e.Bucket)
	}
	if e.ContentType != "image/jpeg" {
		t.Fatalf("banner content_type = %q, want image/jpeg", e.ContentType)
	}
	if e.SizeHint == 0 {
		t.Fatal("expected banner size_hint from cached file")
	}
}

func TestGetMediaManifestV2_IncludesQuoteAuthorAvatarMatchedByURL(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, quote_author_avatar_url, media_json) VALUES
		('tweet_quote', 'poster_a', 'author_a', 'https://pbs.twimg.com/profile_images/quote_user_normal.jpg', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES ('tweet_quote', 'completed', 'image')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'tweet_quote', 0, 'feed_media/tweet_quote_0.jpg', 'photo', 5000)`)
	_ = d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'tweet_quote')`)
	seedAvatarCache(
		t,
		d,
		"twitter_quote_user",
		"https://pbs.twimg.com/profile_images/quote_user_normal.jpg",
		"twitter_quote_user.jpg",
		[]byte("jpg"),
	)

	entries, _, _, err := d.GetMediaManifestV2("bookmarked", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}

	e, ok := byKey["avatar/twitter_quote_user"]
	if !ok {
		t.Fatal("expected avatar entry for quote avatar URL match")
	}
	if e.ServerURL != "/api/media/avatar/twitter_quote_user" {
		t.Fatalf("avatar server_url = %q, want /api/media/avatar/twitter_quote_user", e.ServerURL)
	}
}

func TestGetMediaManifestV2_CursorAdvance(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('t1', 'h', 'a', '[{"type":"photo"}]'),
		('t2', 'h', 'b', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status) VALUES ('t1','completed'),('t2','completed')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media','t1',0,'f/t1_0.jpg','photo',1),
		('feed_media','t2',0,'f/t2_0.jpg','photo',1)`)
	_ = d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('','t1'),('','t2')`)

	first, maxSeq1, _, err := d.GetMediaManifestV2("bookmarked", "", "", 1)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first page expected 1 entry, got %d", len(first))
	}
	second, _, _, err := d.GetMediaManifestV2("bookmarked", "", maxSeq1, 40)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	// The second page should return only rows with id > maxSeq1. Expect 1 more
	// entry (the other feed_media row) plus the avatar, if present.
	if len(second) == 0 {
		t.Errorf("expected cursor advance to surface remaining entries, got %d", len(second))
	}
	for _, e := range second {
		if e.OwnerID == first[0].OwnerID && e.AssetKind == first[0].AssetKind {
			t.Errorf("second page leaked first-page entry %+v", e)
		}
	}
}

func TestGetMediaManifestV2_PerBranchCursorSurfacesNewLowerRangeAssets(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_chan', 'Channel', 'youtube')`)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'youtube_chan')`)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'twitter_h')`)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('t_old', 'h', 'h', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status) VALUES ('t_old', 'completed')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 't_old', 0, 'f/t_old_0.jpg', 'photo', 1)`)
	_ = d.ExecRaw(`INSERT INTO videos (video_id, channel_id, title, thumbnail_path, file_path, file_size, published_at) VALUES
		('youtube_old', 'youtube_chan', 'Old', 'thumbs/youtube_old.jpg', 'videos/youtube_old.mp4', 10, 1)`)

	_, marker, _, err := d.GetMediaManifestV2("subscriptions", "", "", 40)
	if err != nil {
		t.Fatalf("initial manifest: %v", err)
	}
	if marker == "" {
		t.Fatal("expected marker after initial manifest")
	}
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('t_new', 'h', 'h', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status) VALUES ('t_new', 'completed')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 't_new', 0, 'f/t_new_0.jpg', 'photo', 1)`)
	_ = d.ExecRaw(`INSERT INTO videos (video_id, channel_id, title, thumbnail_path, file_path, file_size, published_at) VALUES
		('youtube_new', 'youtube_chan', 'New', 'thumbs/youtube_new.jpg', 'videos/youtube_new.mp4', 10, 2)`)

	entries, _, _, err := d.GetMediaManifestV2("subscriptions", "", marker, 40)
	if err != nil {
		t.Fatalf("delta manifest: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		found[e.AssetKind+"/"+e.OwnerID] = true
	}
	if !found["post_media/t_new"] {
		t.Fatalf("new feed media was skipped after prior high-range cursor; entries=%+v", entries)
	}
	if !found["video_stream/youtube_new"] {
		t.Fatalf("new video stream was skipped after prior higher range cursor; entries=%+v", entries)
	}
}

func TestGetMediaManifestV2_VideoAssetsSurfaceBeforeLargeFeedBacklog(t *testing.T) {
	d := openFreshTestDB(t)
	if err := os.MkdirAll(filepath.Join(d.dataDir, "thumbs"), 0o755); err != nil {
		t.Fatalf("mkdir thumbs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d.dataDir, "thumbs", "youtube_newest.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatalf("write youtube thumb: %v", err)
	}
	_ = d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES
		('youtube_chan', 'Video Channel', 'youtube'),
		('twitter_h', 'Feed Channel', 'twitter')`)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES
		('', 'youtube_chan'),
		('', 'twitter_h')`)
	_ = d.ExecRaw(`INSERT INTO videos (video_id, channel_id, title, thumbnail_path, file_path, file_size, published_at) VALUES
		('youtube_newest', 'youtube_chan', 'Newest', 'thumbs/youtube_newest.jpg', 'videos/youtube_newest.mp4', 10, 2)`)

	for i := range 300 {
		tweetID := fmt.Sprintf("tweet_%03d", i)
		_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES (?, 'h', 'h', '[{"type":"photo"}]')`, tweetID)
		_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status) VALUES (?, 'completed')`, tweetID)
		_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES ('feed_media', ?, 0, ?, 'photo', 1)`, tweetID, "f/"+tweetID+"_0.jpg")
	}

	entries, _, _, err := d.GetMediaManifestV2("subscriptions", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2 subscriptions: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		found[e.AssetKind+"/"+e.OwnerID] = true
	}
	if !found["video_stream/youtube_newest"] {
		t.Fatalf("video stream was starved behind feed backlog; entries=%+v", entries)
	}
	if !found["post_thumbnail/youtube_newest"] {
		t.Fatalf("video thumbnail was starved behind feed backlog; entries=%+v", entries)
	}
}

func TestGetMediaManifestV2_AvatarsSurfaceBeforeLargeFeedBacklog(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('twitter_h', 'Feed Channel', 'twitter')`)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'twitter_h')`)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_visible_author",
		Platform:  "twitter",
		Handle:    "visible_author",
		AvatarURL: "https://cdn.example/visible-author.jpg",
	}); err != nil {
		t.Fatalf("seed channel profile: %v", err)
	}

	for i := range 300 {
		tweetID := fmt.Sprintf("tweet_%03d", i)
		_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES (?, 'h', 'h', '[{"type":"photo"}]')`, tweetID)
		_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status) VALUES (?, 'completed')`, tweetID)
		_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES ('feed_media', ?, 0, ?, 'photo', 1)`, tweetID, "f/"+tweetID+"_0.jpg")
	}

	entries, _, _, err := d.GetMediaManifestV2("subscriptions", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2 subscriptions: %v", err)
	}

	for _, e := range entries {
		if e.AssetKind == "avatar" && e.OwnerID == "twitter_visible_author" {
			return
		}
	}
	t.Fatalf("avatar was starved behind feed backlog; entries=%+v", entries)
}

func TestGetMediaManifestV2_LegacyNumericCursorForcesReplay(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('t_replay', 'h', 'h', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'twitter_h')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status) VALUES ('t_replay', 'completed')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 't_replay', 0, 'f/t_replay_0.jpg', 'photo', 1)`)

	entries, _, _, err := d.GetMediaManifestV2("subscriptions", "", "4500000000000", 40)
	if err != nil {
		t.Fatalf("manifest with legacy cursor: %v", err)
	}
	for _, e := range entries {
		if e.AssetKind == "post_media" && e.OwnerID == "t_replay" {
			return
		}
	}
	t.Fatalf("legacy numeric cursor should replay lower-range assets, entries=%+v", entries)
}

func TestGetMediaManifestV2_LowLegacyCursorPreservesMediaProgress(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('t_old', 'h', 'h', '[{"type":"photo"}]'),
		('t_new', 'h', 'h', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'twitter_h')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status) VALUES ('t_old', 'completed'), ('t_new', 'completed')`)
	_ = d.ExecRaw(`INSERT INTO media_files (id, owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		(10, 'feed_media', 't_old', 0, 'f/t_old_0.jpg', 'photo', 1),
		(20, 'feed_media', 't_new', 0, 'f/t_new_0.jpg', 'photo', 1)`)

	entries, _, _, err := d.GetMediaManifestV2("subscriptions", "", "10", 40)
	if err != nil {
		t.Fatalf("manifest with low legacy cursor: %v", err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e.OwnerID] = true
	}
	if found["t_old"] {
		t.Fatalf("low legacy cursor should not replay already-seen feed media, entries=%+v", entries)
	}
	if !found["t_new"] {
		t.Fatalf("low legacy cursor should continue from the old media_files id, entries=%+v", entries)
	}
}

func TestGetMediaManifestV2_BookmarkedTwitterVideoStubUsesTwitterIdentity(t *testing.T) {
	d := openFreshTestDB(t)
	if err := os.MkdirAll(filepath.Join(d.dataDir, "thumbs"), 0o755); err != nil {
		t.Fatalf("mkdir thumbs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d.dataDir, "thumbs", "9000000000000000001.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}
	_ = d.ExecRaw(`INSERT INTO videos (
		video_id, channel_id, title, duration, thumbnail_path, file_path, file_size, published_at
	) VALUES (
		'9000000000000000001', 'twitter_author_alpha', 'X post 9000000000000000001', 12,
		'thumbs/9000000000000000001.jpg', 'videos/9000000000000000001.mp4', 1234, 1776880000000
	)`)
	_ = d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', '9000000000000000001')`)

	entries, _, _, err := d.GetMediaManifestV2("bookmarked", "", "", 20)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}

	stream, ok := byKey["video_stream/9000000000000000001"]
	if !ok {
		t.Fatal("expected video_stream entry for twitter bookmark stub")
	}
	if stream.Bucket != "twitter_media" {
		t.Fatalf("video_stream bucket = %q, want twitter_media", stream.Bucket)
	}
	if stream.OwnerKind != "tweet" {
		t.Fatalf("video_stream owner_kind = %q, want tweet", stream.OwnerKind)
	}
	if stream.AssetID != "twitter_tweet_9000000000000000001_video_stream" {
		t.Fatalf("video_stream asset_id = %q", stream.AssetID)
	}

	thumb, ok := byKey["post_thumbnail/9000000000000000001"]
	if !ok {
		t.Fatal("expected post_thumbnail entry for twitter bookmark stub")
	}
	if thumb.Bucket != "twitter_media" {
		t.Fatalf("post_thumbnail bucket = %q, want twitter_media", thumb.Bucket)
	}
	if thumb.OwnerKind != "tweet" {
		t.Fatalf("post_thumbnail owner_kind = %q, want tweet", thumb.OwnerKind)
	}
	if thumb.AssetID != "twitter_tweet_9000000000000000001_post_thumbnail" {
		t.Fatalf("post_thumbnail asset_id = %q", thumb.AssetID)
	}
}

func TestGetMediaManifestV2_VideoThumbnailUsesSiblingThumb(t *testing.T) {
	d := openFreshTestDB(t)
	videoDir := filepath.Join(d.dataDir, "media", "tiktok", "creator")
	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		t.Fatalf("mkdir video dir: %v", err)
	}
	thumbBytes := []byte("jpeg bytes")
	if err := os.WriteFile(filepath.Join(videoDir, "tiktok_vid.jpg"), thumbBytes, 0o644); err != nil {
		t.Fatalf("write sibling thumb: %v", err)
	}
	_ = d.ExecRaw(`INSERT INTO videos (
		video_id, channel_id, title, duration, thumbnail_path, file_path, file_size, published_at
	) VALUES (
		'tiktok_vid', 'tiktok_creator', 'Short', 12,
		'media/tiktok/creator/tiktok_vid.jpg', 'media/tiktok/creator/tiktok_vid.mp4', 1234, 1776880000000
	)`)
	_ = d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'tiktok_vid')`)

	entries, _, _, err := d.GetMediaManifestV2("bookmarked", "", "", 20)
	if err != nil {
		t.Fatalf("GetMediaManifestV2: %v", err)
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}

	thumb, ok := byKey["post_thumbnail/tiktok_vid"]
	if !ok {
		t.Fatalf("expected post_thumbnail entry for sibling TikTok thumb; entries=%+v", entries)
	}
	if thumb.AssetID != "tiktok_tiktok_video_tiktok_vid_post_thumbnail" {
		t.Fatalf("post_thumbnail asset_id = %q", thumb.AssetID)
	}
	if thumb.OwnerKind != "tiktok_video" {
		t.Fatalf("post_thumbnail owner_kind = %q", thumb.OwnerKind)
	}
	if thumb.Bucket != "shorts_videos" {
		t.Fatalf("post_thumbnail bucket = %q", thumb.Bucket)
	}
	if thumb.ServerURL != "/api/media/thumbnail/tiktok_vid" {
		t.Fatalf("post_thumbnail server_url = %q", thumb.ServerURL)
	}
	if thumb.ContentType != "image/jpeg" {
		t.Fatalf("post_thumbnail content_type = %q", thumb.ContentType)
	}
	if thumb.SizeHint != int64(len(thumbBytes)) {
		t.Fatalf("post_thumbnail size_hint = %d, want %d", thumb.SizeHint, len(thumbBytes))
	}
}

func TestGetMediaManifestV2_UsesActualFileContentTypes(t *testing.T) {
	d := openFreshTestDB(t)
	if err := os.MkdirAll(filepath.Join(d.dataDir, "videos"), 0o755); err != nil {
		t.Fatalf("mkdir videos: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(d.dataDir, "thumbs"), 0o755); err != nil {
		t.Fatalf("mkdir thumbs: %v", err)
	}
	subtitleBody := []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000 align:start position:0%\nhello\n")
	if err := os.WriteFile(filepath.Join(d.dataDir, "videos", "youtube_vid_1.en.vtt"), subtitleBody, 0o644); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d.dataDir, "thumbs", "youtube_vid_1.webp"), []byte("RIFFxxxxWEBP"), 0o644); err != nil {
		t.Fatalf("write video thumb: %v", err)
	}
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('sample_tweet_png', 'sample_source', 'sample_author_png', '[{"type":"photo"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status) VALUES ('sample_tweet_png', 'completed')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'sample_tweet_png', 0, 'feed_media/sample_tweet_png_0.png', 'photo', 5)`)
	_ = d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('twitter_sample_author_png', 'Sample Author PNG', 'twitter')`)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'twitter_sample_author_png')`)
	seedAvatarCache(t, d, "twitter_sample_author_png", "https://pbs.twimg.com/profile_images/png_normal.webp", "twitter_sample_author_png.webp", []byte("RIFFxxxxWEBP"))
	_ = d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_chan_1', 'Channel 1', 'youtube')`)
	_ = d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id) VALUES ('', 'youtube_chan_1')`)
	_ = d.ExecRaw(`INSERT INTO videos (video_id, channel_id, title, thumbnail_path, file_path, file_size, published_at) VALUES
		('youtube_vid_1', 'youtube_chan_1', 'Video 1', 'thumbs/youtube_vid_1.webp', 'videos/youtube_vid_1.webm', 123, 1)`)

	entries, _, _, err := d.GetMediaManifestV2("subscriptions", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2 subscriptions: %v", err)
	}

	byKey := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byKey[e.AssetKind+"/"+e.OwnerID] = e
	}

	if e, ok := byKey["post_media/sample_tweet_png"]; !ok {
		t.Fatal("expected post_media entry for sample_tweet_png")
	} else {
		if e.ContentType != "image/png" {
			t.Fatalf("post_media content_type = %q, want image/png", e.ContentType)
		}
		if e.ServerURL != "/api/media/slide/sample_tweet_png/0" {
			t.Fatalf("post_media server_url = %q, want /api/media/slide/sample_tweet_png/0", e.ServerURL)
		}
	}

	if e, ok := byKey["avatar/twitter_sample_author_png"]; !ok {
		t.Fatal("expected avatar entry for twitter_sample_author_png")
	} else if e.ContentType != "image/webp" {
		t.Fatalf("avatar content_type = %q, want image/webp", e.ContentType)
	}

	if e, ok := byKey["video_stream/youtube_vid_1"]; !ok {
		t.Fatal("expected video_stream entry for youtube_vid_1")
	} else {
		if e.ContentType != "video/webm" {
			t.Fatalf("video_stream content_type = %q, want video/webm", e.ContentType)
		}
		if e.ServerURL != "/api/media/stream/youtube_vid_1" {
			t.Fatalf("video_stream server_url = %q, want /api/media/stream/youtube_vid_1", e.ServerURL)
		}
	}

	if e, ok := byKey["post_thumbnail/youtube_vid_1"]; !ok {
		t.Fatal("expected post_thumbnail entry for youtube_vid_1")
	} else if e.ContentType != "image/webp" {
		t.Fatalf("video thumbnail content_type = %q, want image/webp", e.ContentType)
	}

	if e, ok := byKey["subtitle/youtube_vid_1"]; !ok {
		t.Fatal("expected subtitle entry for youtube_vid_1")
	} else {
		if e.ContentType != "text/vtt" {
			t.Fatalf("subtitle content_type = %q, want text/vtt", e.ContentType)
		}
		if e.ServerURL != "/api/media/subtitle/youtube_vid_1" {
			t.Fatalf("subtitle server_url = %q, want /api/media/subtitle/youtube_vid_1", e.ServerURL)
		}
		if e.SizeHint != int64(len(sanitizeManifestVTT(subtitleBody))) {
			t.Fatalf("subtitle size_hint = %d, want sanitized size %d", e.SizeHint, len(sanitizeManifestVTT(subtitleBody)))
		}
		if e.IsAuto == nil || !*e.IsAuto {
			t.Fatal("subtitle is_auto = false with no manual subtitle info, want true")
		}
	}
}

func TestGetMediaManifestV2_SkipsAudioOnlyFeedMediaAndUsesSlideTransportForImageFrames(t *testing.T) {
	d := openFreshTestDB(t)
	_ = d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('tiktok_slide', 'creator_a', 'creator_a', '[{"type":"video"}]')`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES
		('tiktok_slide', 'completed', 'slideshow')`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'tiktok_slide', 0, 'media/tiktok_slide_1.jpg', 'video', 100),
		('feed_media', 'tiktok_slide', 1, 'media/tiktok_slide_2.jpg', 'video', 100),
		('feed_media', 'tiktok_slide', 2, 'media/tiktok_slide.mp3', 'video', 100)`)
	_ = d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'tiktok_slide')`)

	entries, _, _, err := d.GetMediaManifestV2("bookmarked", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2 bookmarked: %v", err)
	}

	byAssetID := map[string]model.ManifestEntry{}
	for _, e := range entries {
		byAssetID[e.AssetID] = e
	}

	if _, ok := byAssetID["twitter_tweet_tiktok_slide_post_media_2"]; ok {
		t.Fatal("audio-only feed media should not be emitted in manifest")
	}

	first, ok := byAssetID["twitter_tweet_tiktok_slide_post_media"]
	if !ok {
		t.Fatal("expected first slideshow frame in manifest")
	}
	if first.ServerURL != "/api/media/slide/tiktok_slide/0" {
		t.Fatalf("first frame server_url = %q, want /api/media/slide/tiktok_slide/0", first.ServerURL)
	}
	if first.ContentType != "image/jpeg" {
		t.Fatalf("post_media content_type = %q, want image/jpeg", first.ContentType)
	}
	second, ok := byAssetID["twitter_tweet_tiktok_slide_post_media_1"]
	if !ok {
		t.Fatal("expected second slideshow frame in manifest")
	}
	if second.ServerURL != "/api/media/slide/tiktok_slide/1" {
		t.Fatalf("second frame server_url = %q, want /api/media/slide/tiktok_slide/1", second.ServerURL)
	}
}

func TestGetMediaManifestV2_UnknownScope(t *testing.T) {
	d := openFreshTestDB(t)
	_, _, _, err := d.GetMediaManifestV2("nope", "", "", 40)
	if err == nil {
		t.Error("expected error for unknown scope")
	}
}

func TestGetMediaManifest_Bookmarked(t *testing.T) {
	d := openFreshTestDB(t)
	_ =

		// Insert feed items — two tweets, only one bookmarked
		d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('tweet_100', 'handle_x', 'author_x', '[{"type":"photo"}]'),
		('tweet_200', 'handle_y', 'author_y', '[{"type":"photo"}]')
	`)
	_ =

		// Both have completed jobs
		d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES
		('tweet_100', 'completed', 'image'),
		('tweet_200', 'completed', 'image')
	`)
	_ =

		// Only tweet_100 is bookmarked
		d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'tweet_100')`)
	_ =

		// Media files for both
		d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'tweet_100', 0, 'feed_media/tweet_100_0.jpg', 'photo', 5000),
		('feed_media', 'tweet_200', 0, 'feed_media/tweet_200_0.jpg', 'photo', 5000)
	`)

	entries, _, _, err := d.GetMediaManifestV2("bookmarked", "", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2 bookmarked: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		found[e.OwnerID] = true
	}

	if !found["tweet_100"] {
		t.Error("expected bookmarked tweet_100 in manifest")
	}
	if found["tweet_200"] {
		t.Error("non-bookmarked tweet_200 should not appear in bookmarked manifest")
	}
}

func TestGetMediaManifest_Liked(t *testing.T) {
	d := openFreshTestDB(t)
	_ =

		// Two tweets, only one liked by testuser
		d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('tweet_l1', 'handle_l', 'author_l', '[{"type":"photo"}]'),
		('tweet_l2', 'handle_m', 'author_m', '[{"type":"photo"}]')
	`)
	_ = d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES
		('tweet_l1', 'completed', 'image'),
		('tweet_l2', 'completed', 'image')
	`)
	_ = d.ExecRaw(`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size) VALUES
		('feed_media', 'tweet_l1', 0, 'feed_media/tweet_l1_0.jpg', 'photo', 5000),
		('feed_media', 'tweet_l2', 0, 'feed_media/tweet_l2_0.jpg', 'photo', 5000)
	`)
	_ =
		// Only tweet_l1 liked by testuser
		d.ExecRaw(`INSERT INTO feed_likes (username, tweet_id) VALUES ('testuser', 'tweet_l1')`)

	entries, _, _, err := d.GetMediaManifestV2("liked", "testuser", "", 40)
	if err != nil {
		t.Fatalf("GetMediaManifestV2 liked: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		found[e.OwnerID] = true
	}
	if !found["tweet_l1"] {
		t.Error("expected liked tweet_l1 in manifest")
	}
	if found["tweet_l2"] {
		t.Error("non-liked tweet_l2 should not appear in liked manifest")
	}
}

func TestGetMediaHealth(t *testing.T) {
	d := openFreshTestDB(t)
	_ =

		// Insert feed items with media
		d.ExecRaw(`INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json) VALUES
		('tw_a', 'src_a', 'auth_a', '[{"type":"photo"}]'),
		('tw_b', 'src_b', 'auth_b', '[{"type":"video"}]'),
		('tw_c', 'src_c', 'auth_c', '[{"type":"photo"}]'),
		('tw_d', 'src_d', 'auth_d', '[{"type":"photo"}]'),
		('tw_e', 'src_e', 'auth_e', NULL)
	`)
	_ =

		// Jobs: 2 completed, 1 queued, 1 failed; tw_e has no media so no job
		d.ExecRaw(`INSERT INTO feed_media_jobs (tweet_id, status, media_kind) VALUES
		('tw_a', 'completed', 'image'),
		('tw_b', 'completed', 'video'),
		('tw_c', 'queued', 'image'),
		('tw_d', 'failed', 'image')
	`)

	stats, err := d.GetMediaHealth("subscriptions", "")
	if err != nil {
		t.Fatalf("GetMediaHealth: %v", err)
	}

	// 4 posts with non-null/non-empty media_json (tw_e has NULL)
	if stats.TotalPosts != 4 {
		t.Errorf("TotalPosts: got %d, want 4", stats.TotalPosts)
	}
	if stats.MediaReady != 2 {
		t.Errorf("MediaReady: got %d, want 2", stats.MediaReady)
	}
	if stats.MediaPending != 1 {
		t.Errorf("MediaPending: got %d, want 1", stats.MediaPending)
	}
	if stats.MediaFailed != 1 {
		t.Errorf("MediaFailed: got %d, want 1", stats.MediaFailed)
	}
	if len(stats.FailedTweetIDs) != 1 || stats.FailedTweetIDs[0] != "tw_d" {
		t.Errorf("FailedTweetIDs: got %v, want [tw_d]", stats.FailedTweetIDs)
	}
}
