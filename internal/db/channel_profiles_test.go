package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestUpsertChannelProfileStoresMetadataOnly(t *testing.T) {
	d := openWritableTestDB(t)
	fetchedAt := time.UnixMilli(2000)
	profile := model.ChannelProfile{
		ChannelID:   "twitter_sample_profile",
		Platform:    "twitter",
		Handle:      "sample_profile",
		DisplayName: "Sample Profile",
		Bio:         "sample bio",
		Followers:   42,
		AvatarURL:   "https://pbs.twimg.com/profile_images/1/sample.jpg",
		BannerURL:   "https://pbs.twimg.com/profile_banners/1/sample.jpg",
		FetchedAt:   &fetchedAt,
	}
	if err := d.UpsertChannelProfile(profile); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetChannelProfile(profile.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.DisplayName != profile.DisplayName || got.Bio != profile.Bio || got.Followers != 42 || got.AvatarURL != "" || got.BannerURL != "" {
		t.Fatalf("profile = %+v", got)
	}
	for _, kind := range []string{"avatar", "banner"} {
		asset, err := d.GetAssetByOwnerIdentity(kind, "channel", profile.ChannelID, 0)
		if err != nil || asset != nil {
			t.Fatalf("%s asset = %+v err=%v", kind, asset, err)
		}
	}
}

func TestUpsertChannelProfileDoesNotMutateCanonicalAssets(t *testing.T) {
	d := openWritableTestDB(t)
	const channelID = "youtube_UC_test_profile"
	rel := filepath.Join("thumbnails", "avatars", channelID+"-ready.png")
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), rel), []byte("ready-avatar"))
	if err := d.StoreReadyAsset(Asset{
		AssetID:   BuildAssetID("youtube", "channel", channelID, "avatar", 0),
		AssetKind: "avatar", OwnerKind: "channel", OwnerID: channelID,
		SourceURL: "https://example.test/ready-avatar.jpg", FilePath: rel,
	}, 1000); err != nil {
		t.Fatal(err)
	}
	profile := model.ChannelProfile{
		ChannelID: channelID,
		Platform:  "youtube",
		Handle:    "sample_profile",
		AvatarURL: "https://example.test/must-not-replace.jpg",
		BannerURL: "https://example.test/banner.jpg",
	}
	if err := d.UpsertChannelProfile(profile); err != nil {
		t.Fatal(err)
	}
	if banner, err := d.GetAssetByOwnerIdentity("banner", "channel", profile.ChannelID, 0); err != nil || banner != nil {
		t.Fatalf("metadata upsert created banner = %+v err=%v", banner, err)
	}
	got, err := d.GetYouTubeChannelProfileByHandle("@sample_profile")
	if err != nil || got == nil || got.BannerURL != "" || got.AvatarURL != "https://example.test/ready-avatar.jpg" {
		t.Fatalf("profile lookup = %+v err=%v", got, err)
	}
}

func TestGetTwitterChannelProfilesByHandlesReturnsVisibleIdentityOnly(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_test_visible",
		Platform:    "twitter",
		Handle:      "test_visible",
		DisplayName: "Visible Name",
		AvatarURL:   "https://example.test/avatar.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	profiles, err := d.GetTwitterChannelProfilesByHandles([]string{"@TEST_VISIBLE", "@test_visible"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := profiles["test_visible"]
	if !ok || got.DisplayName != "Visible Name" || got.AvatarURL != "" {
		t.Fatalf("visible profile = %+v", got)
	}
}
