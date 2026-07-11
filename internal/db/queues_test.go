package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaimContentAssetsLeavesProfileIdentityOutsideQueue(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	assets := []Asset{
		{
			AssetID:   BuildAssetID("twitter", "tweet", "sample_post", "post_media", 0),
			AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post",
			SourceURL: "https://example.test/media.jpg", State: AssetStateQueued,
		},
		{
			AssetID:   BuildAssetID("twitter", "tweet", "sample_quote", "avatar", 0),
			AssetKind: "avatar", OwnerKind: "tweet", OwnerID: "sample_quote",
			SourceURL: "https://example.test/avatar.jpg", State: AssetStateQueued, RequiredReason: "quote_avatar",
		},
		{
			AssetID:   BuildAssetID("twitter", "channel", "twitter_sample", "avatar", 0),
			AssetKind: "avatar", OwnerKind: "channel", OwnerID: "twitter_sample",
			SourceURL: "https://example.test/channel.jpg", State: AssetStateQueued, RequiredReason: "identity",
		},
	}
	for _, asset := range assets {
		upsertAssetForTest(t, d, asset, now)
	}
	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "x-worker", NowMs: now + 1, LeaseMs: 1000, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].AssetKind != "post_media" {
		t.Fatalf("claimed X content assets = %+v, want only post media", claimed)
	}
	for _, identity := range assets[1:] {
		stored, err := d.GetAsset(identity.AssetID, "avatar")
		if err != nil {
			t.Fatal(err)
		}
		if stored == nil || stored.State != AssetStateQueued || stored.LeaseOwner != "" {
			t.Fatalf("identity asset was claimed by X content: %+v", stored)
		}
	}
}

func TestContentAssetLeasePublishesFingerprintAndRejectsStaleOwner(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	asset := Asset{
		AssetID:   BuildAssetID("twitter", "tweet", "test_publish", "post_media", 0),
		AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "test_publish",
		SourceURL: "https://example.test/media.jpg", State: AssetStateQueued,
	}
	upsertAssetForTest(t, d, asset, now)
	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "x-worker", NowMs: now + 1, LeaseMs: 1000, Limit: 1,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v err=%v", claimed, err)
	}
	key := filepath.Join("media", "twitter", "test_publish", "immutable.jpg")
	path, err := d.storage.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("canonical bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := claimed[0]
	result.FilePath = key
	result.ContentType = "image/jpeg"
	if err := d.CompleteAssetDownload(result, "stale-worker", now+2); !errors.Is(err, ErrQueueLeaseNotHeld) {
		t.Fatalf("stale completion error = %v", err)
	}
	if err := d.CompleteAssetDownload(result, "x-worker", now+3); err != nil {
		t.Fatal(err)
	}
	stored, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.State != AssetStateReady || stored.FilePath != key || len(stored.SHA256) != 64 || stored.FileMtimeNs == 0 {
		t.Fatalf("published asset = %+v", stored)
	}
}

func TestXContentDownloadStatusUsesCanonicalAssets(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	upsertAssetForTest(t, d, Asset{
		AssetID:   BuildAssetID("twitter", "tweet", "test_retry", "post_media", 0),
		AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "test_retry",
		SourceURL: "https://example.test/retry.jpg", State: AssetStateFailed,
		Attempts: 3, LastError: "temporary",
	}, now)
	promoted, err := d.RetryXContentForTweet("test_retry")
	if err != nil || !promoted {
		t.Fatalf("promoted=%t err=%v", promoted, err)
	}
	queued, processing, err := d.CountPendingXContentDownloads()
	if err != nil || queued != 1 || processing != 0 {
		t.Fatalf("counts queued=%d processing=%d err=%v", queued, processing, err)
	}
	active, pending, err := d.ListPendingXContentDownloads()
	if err != nil || len(active) != 0 || len(pending) != 1 || pending[0].TweetID != "test_retry" {
		t.Fatalf("status active=%+v pending=%+v err=%v", active, pending, err)
	}
}
