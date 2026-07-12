package db

import "testing"

func TestTweetMediaAvailabilityDoesNotCrossOwnerKinds(t *testing.T) {
	d := openWritableTestDB(t)
	publishAssetMetadataForTest(t, d, Asset{AssetID: "wrong-owner", AssetKind: "video_stream", OwnerKind: "youtube_video", OwnerID: "sample_a", FilePath: "media/youtube/shared.mp4", ContentType: "video/mp4"}, 1)
	upsertAssetForTest(t, d, Asset{AssetID: "tweet-owner", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_a", SourceURL: "https://example.test/shared.jpg", ContentType: "image/jpeg"}, 1)

	availability, err := d.GetTweetMediaAssetAvailability([]string{"sample_a"})
	if err != nil {
		t.Fatal(err)
	}
	got := availability["sample_a"]
	if !got.Declared || !got.Pending || got.ReadyMedia || got.ReadyVideo {
		t.Fatalf("tweet availability crossed owner kinds: %+v", got)
	}
}

func TestAndroidInventoryUsesPersistedTweetOwner(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, published_at, fetched_at)
		VALUES ('sample_b', 1, 1);
		INSERT INTO videos (video_id, channel_id, owner_kind, title, duration, published_at)
		VALUES ('sample_b', '', 'tweet', 'Saved post', 0, 1)
	`); err != nil {
		t.Fatal(err)
	}
	publishAssetMetadataForTest(t, d, Asset{AssetID: "tweet-asset", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_b", FilePath: "media/twitter/shared.jpg"}, 1)
	publishAssetMetadataForTest(t, d, Asset{AssetID: "wrong-video-asset", AssetKind: "post_media", OwnerKind: "youtube_video", OwnerID: "sample_b", FilePath: "media/youtube/shared.jpg"}, 1)

	assets, err := d.ListAndroidSyncAssetInventoryRows(AndroidSyncDesiredSets{
		Tweets:      map[string]struct{}{"sample_b": {}},
		Videos:      map[string]struct{}{"sample_b": {}},
		MediaVideos: map[string]struct{}{"sample_b": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || assets[0].OwnerKind != "tweet" || assets[0].AssetID != "tweet-asset" {
		t.Fatalf("tweet-owned feed stub assets = %+v", assets)
	}
}
