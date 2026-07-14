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
	}, true, DownloadLaneBackfill)
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

func TestContentAssetQueueExcludesTweetsWhenXIsDisabled(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	for _, asset := range []Asset{
		{
			AssetID:   BuildAssetID("twitter", "tweet", "sample_post", "post_media", 0),
			AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post",
			SourceURL: "https://example.test/post.jpg", State: AssetStateQueued,
		},
		{
			AssetID:   BuildAssetID("youtube", "comment_author", "sample_author", "avatar", 0),
			AssetKind: "avatar", OwnerKind: "comment_author", OwnerID: "sample_author",
			SourceURL: "https://example.test/commenter.jpg", State: AssetStateQueued,
		},
	} {
		upsertAssetForTest(t, d, asset, now)
	}

	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "content-worker", NowMs: now + 1, LeaseMs: 1000, Limit: 10,
	}, false, DownloadLaneCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].OwnerKind != "comment_author" {
		t.Fatalf("claimed with X disabled = %+v, want only comment avatar", claimed)
	}
	if err := d.ExecRaw(`DELETE FROM assets WHERE owner_kind = 'comment_author'`); err != nil {
		t.Fatal(err)
	}
	delay, err := d.NextMediaWorkDelay(now+2, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if delay != 5*time.Minute {
		t.Fatalf("next delay with only excluded X work = %s, want idle delay", delay)
	}
}

func TestClaimContentAssetsPrefersNewestPublishedPostOverQueueRefresh(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`INSERT INTO feed_items (tweet_id, published_at) VALUES
		('sample_old_post', ?), ('sample_new_post', ?)`, now-2000, now-1000); err != nil {
		t.Fatal(err)
	}
	upsertAssetForTest(t, d, Asset{
		AssetID:   BuildAssetID("twitter", "tweet", "sample_new_post", "post_media", 0),
		AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_new_post",
		SourceURL: "https://example.test/new.jpg", State: AssetStateQueued,
	}, now)
	upsertAssetForTest(t, d, Asset{
		AssetID:   BuildAssetID("twitter", "tweet", "sample_old_post", "post_media", 0),
		AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_old_post",
		SourceURL: "https://example.test/old.jpg", State: AssetStateQueued,
	}, now+100)

	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "x-worker", NowMs: now + 101, LeaseMs: 1000, Limit: 1,
	}, true, DownloadLaneCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].OwnerID != "sample_new_post" {
		t.Fatalf("claimed = %+v, want newest published post", claimed)
	}
}

func TestClaimContentAssetsPrefersPinnedFeedGeneration(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`INSERT INTO feed_items (tweet_id, published_at) VALUES
		('sample_ranked_first', ?), ('sample_newer_post', ?)`, now-2000, now-1000); err != nil {
		t.Fatal(err)
	}
	for _, ownerID := range []string{"sample_ranked_first", "sample_newer_post"} {
		upsertAssetForTest(t, d, Asset{
			AssetID:   BuildAssetID("twitter", "tweet", ownerID, "post_media", 0),
			AssetKind: "post_media", OwnerKind: "tweet", OwnerID: ownerID,
			SourceURL: "https://example.test/" + ownerID + ".jpg", State: AssetStateQueued,
		}, now)
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{
		{TweetID: "sample_ranked_first", RankPosition: 1},
		{TweetID: "sample_newer_post", RankPosition: 2},
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "x-worker", NowMs: now + 1, LeaseMs: 1000, Limit: 1,
	}, true, DownloadLaneCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].OwnerID != "sample_ranked_first" {
		t.Fatalf("claimed = %+v, want first pinned feed owner", claimed)
	}
}

func TestContentAssetCurrentLanePreservesRetainedSnapshotPages(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`INSERT INTO feed_items (tweet_id, published_at) VALUES
		('sample_bookmark', ?), ('sample_new', ?)`, now-(72*time.Hour).Milliseconds(), now); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{{TweetID: "sample_bookmark", RankPosition: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceFeedRankSnapshot([]SnapshotRow{{TweetID: "sample_new", RankPosition: 1}}); err != nil {
		t.Fatal(err)
	}
	upsertAssetForTest(t, d, Asset{
		AssetID:   BuildAssetID("twitter", "tweet", "sample_bookmark", "post_media", 0),
		AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_bookmark",
		SourceURL: "https://example.test/pinned.jpg", State: AssetStateQueued,
	}, now)

	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "current-worker", NowMs: now + 1, LeaseMs: 1000, Limit: 1,
	}, true, DownloadLaneCurrent)
	if err != nil || len(claimed) != 1 || claimed[0].OwnerID != "sample_bookmark" {
		t.Fatalf("retained snapshot claim = %+v, %v", claimed, err)
	}
}

func TestContentAssetLanesSeparateVisibleQuoteFromHistoricalBackfill(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, quote_tweet_id, published_at)
		VALUES ('sample_post', 'sample_quote', ?)
	`, now-1000); err != nil {
		t.Fatal(err)
	}
	for _, ownerID := range []string{"sample_quote", "sample_old"} {
		upsertAssetForTest(t, d, Asset{
			AssetID:   BuildAssetID("twitter", "tweet", ownerID, "post_media", 0),
			AssetKind: "post_media", OwnerKind: "tweet", OwnerID: ownerID,
			SourceURL: "https://example.test/" + ownerID + ".jpg", State: AssetStateQueued,
		}, now)
	}
	current, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "current-worker", NowMs: now + 1, LeaseMs: 1000, Limit: 1,
	}, true, DownloadLaneCurrent)
	if err != nil || len(current) != 1 || current[0].OwnerID != "sample_quote" {
		t.Fatalf("current claim = %+v, %v", current, err)
	}
	backfill, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "backfill-worker", NowMs: now + 1, LeaseMs: 1000, Limit: 1,
	}, true, DownloadLaneBackfill)
	if err != nil || len(backfill) != 1 || backfill[0].OwnerID != "sample_old" {
		t.Fatalf("backfill claim = %+v, %v", backfill, err)
	}
}

func TestContentAssetLeasePublishesCompletedFileAndRejectsStaleOwner(t *testing.T) {
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
	}, true, DownloadLaneBackfill)
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
	if stored == nil || stored.State != AssetStateReady || stored.FilePath != key || stored.SizeBytes == 0 || stored.FileMtimeNs == 0 {
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

func TestPrunedXContentStopsQueueWorkWithoutRetiringSharedDemand(t *testing.T) {
	d := openFreshTestDB(t)
	const sourceURL = "https://example.test/shared-queue.jpg"
	for _, ownerID := range []string{"sample_pruned_owner", "sample_active_owner"} {
		upsertAssetForTest(t, d, Asset{
			AssetID:   BuildAssetID("twitter", "tweet", ownerID, "post_media", 0),
			AssetKind: "post_media", OwnerKind: "tweet", OwnerID: ownerID,
			SourceURL: sourceURL, State: AssetStateQueued,
		}, 1000)
	}
	if err := d.ExecRaw(`
		UPDATE media_objects
		SET job_state = 'downloading', attempts = 4,
		    last_error_kind = 'temporary', last_error = 'retrying',
		    lease_owner = 'sample-worker', lease_until_ms = 9000
		WHERE object_key = ?
	`, "source:"+sourceURL); err != nil {
		t.Fatal(err)
	}

	if _, err := d.markXContentAssetsPruned([]string{"sample_pruned_owner"}, 2000); err != nil {
		t.Fatal(err)
	}
	processing, pending, err := d.ListPendingXContentDownloads()
	if err != nil || len(processing) != 1 || processing[0].TweetID != "sample_active_owner" || len(pending) != 0 {
		t.Fatalf("queue after shared prune = processing %+v pending %+v err %v", processing, pending, err)
	}
	queuedCount, processingCount, err := d.CountPendingXContentDownloads()
	if err != nil || queuedCount != 0 || processingCount != 1 {
		t.Fatalf("counts after shared prune = queued %d processing %d err %v", queuedCount, processingCount, err)
	}

	if _, err := d.markXContentAssetsPruned([]string{"sample_active_owner"}, 3000); err != nil {
		t.Fatal(err)
	}
	var state, leaseOwner, kind, message string
	var attempts int
	var leaseUntil, nextAttempt int64
	if err := d.QueryRow(`
		SELECT job_state, attempts, next_attempt_at_ms, last_error_kind, last_error,
		       lease_owner, lease_until_ms
		FROM media_objects WHERE object_key = ?
	`, "source:"+sourceURL).Scan(&state, &attempts, &nextAttempt, &kind, &message, &leaseOwner, &leaseUntil); err != nil {
		t.Fatal(err)
	}
	if state != AssetStatePruned || attempts != 0 || nextAttempt != 0 || kind != "" || message != "" || leaseOwner != "" || leaseUntil != 0 {
		t.Fatalf("retired object = state %q attempts %d next %d kind %q message %q lease %q/%d", state, attempts, nextAttempt, kind, message, leaseOwner, leaseUntil)
	}
	processing, pending, err = d.ListPendingXContentDownloads()
	if err != nil || len(processing) != 0 || len(pending) != 0 {
		t.Fatalf("queue after final prune = processing %+v pending %+v err %v", processing, pending, err)
	}
	queuedCount, processingCount, err = d.CountPendingXContentDownloads()
	if err != nil || queuedCount != 0 || processingCount != 0 {
		t.Fatalf("final counts = queued %d processing %d err %v", queuedCount, processingCount, err)
	}
}
