package db

import "testing"

func TestTweetMediaAvailabilityDoesNotCrossOwnerKinds(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			file_path, content_type, state, created_at_ms, updated_at_ms
		) VALUES
			('wrong-owner', 'video_stream', 'youtube_video', 'shared-content-id', 0,
			 'media/youtube/shared.mp4', 'video/mp4', 'ready', 1, 1),
			('tweet-owner', 'post_media', 'tweet', 'shared-content-id', 0,
			 '', 'image/jpeg', 'queued', 1, 1)
	`); err != nil {
		t.Fatal(err)
	}

	availability, err := d.GetTweetMediaAssetAvailability([]string{"shared-content-id"})
	if err != nil {
		t.Fatal(err)
	}
	got := availability["shared-content-id"]
	if !got.Declared || !got.Pending || got.ReadyMedia || got.ReadyVideo {
		t.Fatalf("tweet availability crossed owner kinds: %+v", got)
	}
}

func TestAndroidInventoryUsesPersistedTweetOwner(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, published_at, fetched_at)
		VALUES ('shared-stub-id', 1, 1);
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES ('shared-stub-id', '', 'tweet', 'Saved post', 0, 1);
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			file_path, state, created_at_ms, updated_at_ms
		) VALUES
			('tweet-asset', 'post_media', 'tweet', 'shared-stub-id', 0,
			 'media/twitter/shared.jpg', 'ready', 1, 1),
			('wrong-video-asset', 'post_media', 'youtube_video', 'shared-stub-id', 0,
			 'media/youtube/shared.jpg', 'ready', 1, 1)
	`); err != nil {
		t.Fatal(err)
	}

	assets, err := d.ListAndroidSyncAssetInventoryRows(AndroidSyncDesiredSets{
		Tweets:      map[string]struct{}{"shared-stub-id": {}},
		Videos:      map[string]struct{}{"shared-stub-id": {}},
		MediaVideos: map[string]struct{}{"shared-stub-id": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].OwnerKind != "tweet" || assets[0].AssetID != "tweet-asset" {
		t.Fatalf("tweet-owned feed stub assets = %+v", assets)
	}
}
