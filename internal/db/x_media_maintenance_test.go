package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestDedupeXMediaBySourceURLRewritesRowsAndRemovesUnreferencedFile(t *testing.T) {
	d := openFreshTestDB(t)
	sourceURL := "https://pbs.twimg.com/media/sample_dedupe.jpg"
	canonicalRel := filepath.Join("media", "twitter", "sample", "sample_dedupe_canonical.jpg")
	duplicateRel := filepath.Join("media", "twitter", "sample", "sample_dedupe_duplicate.jpg")
	canonicalBody := []byte("canonical-media-body")
	duplicateBody := []byte("dupe")
	writeDBTestFile(t, filepath.Join(d.dataDir, canonicalRel), canonicalBody)
	writeDBTestFile(t, filepath.Join(d.dataDir, duplicateRel), duplicateBody)

	if err := d.InsertMediaFileBatch([]model.MediaFile{
		{
			OwnerType:  "feed_media",
			OwnerID:    "sample_tweet_dedupe_a",
			MediaIndex: 0,
			FilePath:   canonicalRel,
			MediaType:  "photo",
			SourceURL:  sourceURL,
			FileSize:   int64(len(canonicalBody)),
		},
		{
			OwnerType:  "feed_media",
			OwnerID:    "sample_tweet_dedupe_b",
			MediaIndex: 0,
			FilePath:   duplicateRel,
			MediaType:  "photo",
			SourceURL:  sourceURL,
			FileSize:   int64(len(duplicateBody)),
		},
	}); err != nil {
		t.Fatalf("InsertMediaFileBatch: %v", err)
	}

	result, err := d.DedupeXMediaBySourceURL(XMediaDedupeOptions{Limit: 10, DryRun: false, NowMs: 2000})
	if err != nil {
		t.Fatalf("DedupeXMediaBySourceURL: %v", err)
	}
	if result.Groups != 1 || result.RowsRewritten != 1 || result.AssetRowsUpdated != 1 {
		t.Fatalf("unexpected dedupe result: %+v", result)
	}
	if result.FileRemoval.Removed != 1 || result.FileRemoval.RemovedBytes != int64(len(duplicateBody)) {
		t.Fatalf("unexpected file removal result: %+v", result.FileRemoval)
	}

	for _, tweetID := range []string{"sample_tweet_dedupe_a", "sample_tweet_dedupe_b"} {
		got, err := d.GetMediaFilePath("feed_media", tweetID, 0)
		if err != nil {
			t.Fatalf("GetMediaFilePath %s: %v", tweetID, err)
		}
		if got != canonicalRel {
			t.Fatalf("%s path = %q, want %q", tweetID, got, canonicalRel)
		}
	}
	asset, err := d.GetAsset(BuildManifestAssetID("twitter", "tweet", "sample_tweet_dedupe_b", "post_media", 0), "post_media")
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if asset == nil || asset.FilePath != canonicalRel || asset.SizeBytes != int64(len(canonicalBody)) || asset.State != AssetStateReady {
		t.Fatalf("deduped asset mismatch: %+v", asset)
	}
	if _, err := os.Stat(filepath.Join(d.dataDir, duplicateRel)); !os.IsNotExist(err) {
		t.Fatalf("duplicate file still exists or stat failed unexpectedly: %v", err)
	}
}

func TestPruneXMediaRetentionKeepsLatestAndProtected(t *testing.T) {
	d := openFreshTestDB(t)
	channelID := "twitter_sample_source"
	sourceHandle := "sample_source"
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
		VALUES (?, ?, 'Sample Source', '', 'twitter', 1)
	`, channelID, sourceHandle); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.ExecRaw(`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', ?, 1)`, channelID); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := d.ExecRaw(`INSERT INTO channel_settings (channel_id, media_download_limit, updated_at) VALUES (?, 2, 1)`, channelID); err != nil {
		t.Fatalf("insert channel settings: %v", err)
	}

	seedXRetentionMedia(t, d, "sample_tweet_new", sourceHandle, 400, []byte("new-media"))
	seedXRetentionMedia(t, d, "sample_tweet_bookmarked", sourceHandle, 300, []byte("bookmarked-media"))
	seedXRetentionMedia(t, d, "sample_tweet_keep", sourceHandle, 200, []byte("keep-media"))
	seedXRetentionMedia(t, d, "sample_tweet_prune", sourceHandle, 100, []byte("prune-media"))
	seedXQuoteRetentionMedia(t, d, "sample_tweet_prune", "sample_quote_prune", sourceHandle, []byte("quote-media"))
	seedQueuedXRetentionJob(t, d, "sample_tweet_queued_prune", sourceHandle, 50)
	if err := d.ExecRaw(`UPDATE media_files SET file_size = 0 WHERE owner_id = 'sample_tweet_prune'`); err != nil {
		t.Fatalf("zero recorded prune media size: %v", err)
	}
	if err := d.ExecRaw(`INSERT INTO bookmarks (user_id, video_id) VALUES ('', 'sample_tweet_bookmarked')`); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	result, err := d.PruneXMediaRetentionForChannel(channelID, XMediaRetentionOptions{DryRun: false, NowMs: 3000})
	if err != nil {
		t.Fatalf("PruneXMediaRetentionForChannel: %v", err)
	}
	if result.SourcesScanned != 1 || result.SourcesOverLimit != 1 || result.ProtectedItems != 1 || result.PrunedItems != 2 {
		t.Fatalf("unexpected retention result: %+v", result)
	}
	if result.MediaRowsDeleted != 2 || result.AssetRowsDeleted != 2 || result.JobsMarkedPruned != 2 || result.FileRemoval.Removed != 2 {
		t.Fatalf("unexpected retention deletes: %+v", result)
	}
	if result.CandidateFileBytes != int64(len("prune-media")+len("quote-media")) {
		t.Fatalf("candidate file bytes = %d, want actual file size", result.CandidateFileBytes)
	}

	if _, err := d.GetMediaFilePath("feed_media", "sample_tweet_prune", 0); err == nil {
		t.Fatal("pruned tweet media row still exists")
	}
	if _, err := d.GetMediaFilePath("quote_media", "sample_quote_prune", 0); err == nil {
		t.Fatal("pruned quote media row still exists")
	}
	for _, tweetID := range []string{"sample_tweet_new", "sample_tweet_bookmarked", "sample_tweet_keep"} {
		if _, err := d.GetMediaFilePath("feed_media", tweetID, 0); err != nil {
			t.Fatalf("expected retained media for %s: %v", tweetID, err)
		}
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM feed_media_jobs WHERE tweet_id = 'sample_tweet_prune'`).Scan(&status); err != nil {
		t.Fatalf("query pruned job: %v", err)
	}
	if status != "pruned" {
		t.Fatalf("pruned job status = %q, want pruned", status)
	}
	if err := d.QueryRow(`SELECT status FROM feed_media_jobs WHERE tweet_id = 'sample_tweet_queued_prune'`).Scan(&status); err != nil {
		t.Fatalf("query queued pruned job: %v", err)
	}
	if status != "pruned" {
		t.Fatalf("queued pruned job status = %q, want pruned", status)
	}
	prunedPath := filepath.Join(d.dataDir, "media", "twitter", sourceHandle, "sample_tweet_prune.jpg")
	if _, err := os.Stat(prunedPath); !os.IsNotExist(err) {
		t.Fatalf("pruned file still exists or stat failed unexpectedly: %v", err)
	}
	prunedQuotePath := filepath.Join(d.dataDir, "media", "twitter", sourceHandle, "sample_quote_prune.jpg")
	if _, err := os.Stat(prunedQuotePath); !os.IsNotExist(err) {
		t.Fatalf("pruned quote file still exists or stat failed unexpectedly: %v", err)
	}
	var bookmarkCount int
	if err := d.QueryRow(`SELECT COUNT(*) FROM bookmarks WHERE video_id = 'sample_tweet_bookmarked'`).Scan(&bookmarkCount); err != nil {
		t.Fatalf("query bookmark count: %v", err)
	}
	if bookmarkCount != 1 {
		t.Fatalf("bookmark count = %d, want 1", bookmarkCount)
	}
}

func TestMaintainReadyAssetFileStatesMarksMissingAndUpdatesSize(t *testing.T) {
	d := openFreshTestDB(t)
	liveRel := filepath.Join("media", "twitter", "sample", "sample_asset_live.jpg")
	liveBody := []byte("live-asset")
	writeDBTestFile(t, filepath.Join(d.dataDir, liveRel), liveBody)
	missingRel := filepath.Join("media", "twitter", "sample", "sample_asset_missing.jpg")

	if err := d.UpsertAsset(Asset{
		AssetID:        BuildManifestAssetID("twitter", "tweet", "sample_tweet_asset_live", "post_media", 0),
		AssetKind:      "post_media",
		OwnerKind:      "tweet",
		OwnerID:        "sample_tweet_asset_live",
		MediaIndex:     0,
		FilePath:       liveRel,
		ContentType:    "image/jpeg",
		SizeBytes:      1,
		State:          AssetStateReady,
		RequiredReason: "retention",
	}, 1000); err != nil {
		t.Fatalf("upsert live asset: %v", err)
	}
	if err := d.UpsertAsset(Asset{
		AssetID:        BuildManifestAssetID("twitter", "tweet", "sample_tweet_asset_missing", "post_media", 0),
		AssetKind:      "post_media",
		OwnerKind:      "tweet",
		OwnerID:        "sample_tweet_asset_missing",
		MediaIndex:     0,
		FilePath:       missingRel,
		ContentType:    "image/jpeg",
		SizeBytes:      100,
		State:          AssetStateReady,
		RequiredReason: "retention",
	}, 1000); err != nil {
		t.Fatalf("upsert missing asset: %v", err)
	}

	result, err := d.MaintainReadyAssetFileStates(AssetFileStateMaintenanceOptions{Limit: 10, DryRun: false, NowMs: 4000})
	if err != nil {
		t.Fatalf("MaintainReadyAssetFileStates: %v", err)
	}
	if result.Checked != 2 || result.Missing != 1 || result.SizeChanged != 1 || result.Updated != 2 {
		t.Fatalf("unexpected asset maintenance result: %+v", result)
	}

	live, err := d.GetAsset(BuildManifestAssetID("twitter", "tweet", "sample_tweet_asset_live", "post_media", 0), "post_media")
	if err != nil {
		t.Fatalf("GetAsset live: %v", err)
	}
	if live == nil || live.State != AssetStateReady || live.SizeBytes != int64(len(liveBody)) {
		t.Fatalf("live asset mismatch: %+v", live)
	}
	missing, err := d.GetAsset(BuildManifestAssetID("twitter", "tweet", "sample_tweet_asset_missing", "post_media", 0), "post_media")
	if err != nil {
		t.Fatalf("GetAsset missing: %v", err)
	}
	if missing == nil || missing.State != AssetStateServerMissing || missing.SizeBytes != 0 {
		t.Fatalf("missing asset mismatch: %+v", missing)
	}
}

func seedXRetentionMedia(t *testing.T, d *DB, tweetID, sourceHandle string, publishedAt int64, body []byte) {
	t.Helper()
	relPath := filepath.Join("media", "twitter", sourceHandle, tweetID+".jpg")
	writeDBTestFile(t, filepath.Join(d.dataDir, relPath), body)
	sourceURL := "https://pbs.twimg.com/media/" + tweetID + ".jpg"
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, published_at, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, tweetID, sourceHandle, sourceHandle, `[{"url":"`+sourceURL+`","type":"photo"}]`, publishedAt, publishedAt); err != nil {
		t.Fatalf("insert feed item %s: %v", tweetID, err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_media_jobs (tweet_id, status, media_kind, completed_at_ms, updated_at)
		VALUES (?, 'completed', 'image', ?, ?)
	`, tweetID, publishedAt, publishedAt); err != nil {
		t.Fatalf("insert feed media job %s: %v", tweetID, err)
	}
	if err := d.InsertMediaFileBatch([]model.MediaFile{{
		OwnerType:  "feed_media",
		OwnerID:    tweetID,
		MediaIndex: 0,
		FilePath:   relPath,
		MediaType:  "photo",
		SourceURL:  sourceURL,
		FileSize:   int64(len(body)),
	}}); err != nil {
		t.Fatalf("insert media file %s: %v", tweetID, err)
	}
}

func seedXQuoteRetentionMedia(t *testing.T, d *DB, parentTweetID, quoteTweetID, sourceHandle string, body []byte) {
	t.Helper()
	if err := d.ExecRaw(`UPDATE feed_items SET quote_tweet_id = ? WHERE tweet_id = ?`, quoteTweetID, parentTweetID); err != nil {
		t.Fatalf("set quote tweet id: %v", err)
	}
	relPath := filepath.Join("media", "twitter", sourceHandle, quoteTweetID+".jpg")
	writeDBTestFile(t, filepath.Join(d.dataDir, relPath), body)
	sourceURL := "https://pbs.twimg.com/media/" + quoteTweetID + ".jpg"
	if err := d.InsertMediaFileBatch([]model.MediaFile{{
		OwnerType:  "quote_media",
		OwnerID:    quoteTweetID,
		MediaIndex: 0,
		FilePath:   relPath,
		MediaType:  "photo",
		SourceURL:  sourceURL,
		FileSize:   int64(len(body)),
	}}); err != nil {
		t.Fatalf("insert quote media file %s: %v", quoteTweetID, err)
	}
}

func seedQueuedXRetentionJob(t *testing.T, d *DB, tweetID, sourceHandle string, publishedAt int64) {
	t.Helper()
	sourceURL := "https://pbs.twimg.com/media/" + tweetID + ".jpg"
	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, source_handle, author_handle, media_json, published_at, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, tweetID, sourceHandle, sourceHandle, `[{"url":"`+sourceURL+`","type":"photo"}]`, publishedAt, publishedAt); err != nil {
		t.Fatalf("insert queued feed item %s: %v", tweetID, err)
	}
	if err := d.ExecRaw(`
		INSERT INTO feed_media_jobs (tweet_id, status, media_kind, updated_at)
		VALUES (?, 'queued', 'image', ?)
	`, tweetID, publishedAt); err != nil {
		t.Fatalf("insert queued feed media job %s: %v", tweetID, err)
	}
}
