package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestAssetInventoryUpsertIsIdempotent(t *testing.T) {
	d := openWritableTestDB(t)

	asset := Asset{
		AssetID:        BuildManifestAssetID("twitter", "tweet", "sample_tweet_asset_a", "post_media", 0),
		AssetKind:      "post_media",
		OwnerKind:      "tweet",
		OwnerID:        "sample_tweet_asset_a",
		MediaIndex:     0,
		FilePath:       "media/twitter/sample/sample_tweet_asset_a_0.jpg",
		ContentType:    "image/jpeg",
		SizeBytes:      123,
		State:          AssetStateReady,
		RequiredReason: "retention",
	}
	if err := d.UpsertAsset(asset, 1000); err != nil {
		t.Fatalf("first UpsertAsset: %v", err)
	}
	asset.SizeBytes = 456
	asset.FilePath = "media/twitter/sample/sample_tweet_asset_a_0_new.jpg"
	if err := d.UpsertAsset(asset, 2000); err != nil {
		t.Fatalf("second UpsertAsset: %v", err)
	}

	got, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if got == nil {
		t.Fatal("asset missing after upsert")
	}
	if got.SizeBytes != 456 || got.FilePath != asset.FilePath {
		t.Fatalf("asset was not updated: %+v", *got)
	}
	if got.CreatedAtMs != 1000 || got.UpdatedAtMs != 2000 {
		t.Fatalf("timestamps = created %d updated %d, want 1000/2000", got.CreatedAtMs, got.UpdatedAtMs)
	}

	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM assets WHERE asset_id = ? AND asset_kind = ?`, asset.AssetID, asset.AssetKind).Scan(&count); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	if count != 1 {
		t.Fatalf("asset rows = %d, want 1", count)
	}
}

func TestRefreshAssetFileStateMarksReadyAndServerMissing(t *testing.T) {
	d := openWritableTestDB(t)
	relPath := filepath.Join("media", "twitter", "sample", "ready.jpg")
	writeDBTestFile(t, filepath.Join(d.dataDir, relPath), []byte("ready-image"))

	ready := Asset{
		AssetID:    BuildManifestAssetID("twitter", "tweet", "sample_tweet_ready", "post_media", 0),
		AssetKind:  "post_media",
		OwnerKind:  "tweet",
		OwnerID:    "sample_tweet_ready",
		FilePath:   relPath,
		MediaIndex: 0,
		State:      AssetStateQueued,
	}
	if err := d.UpsertAsset(ready, 1000); err != nil {
		t.Fatalf("upsert ready asset: %v", err)
	}
	if err := d.RefreshAssetFileState(ready.AssetID, ready.AssetKind, 2000); err != nil {
		t.Fatalf("refresh ready asset: %v", err)
	}
	gotReady, err := d.GetAsset(ready.AssetID, ready.AssetKind)
	if err != nil {
		t.Fatalf("get ready asset: %v", err)
	}
	if gotReady.State != AssetStateReady || gotReady.SizeBytes != int64(len("ready-image")) {
		t.Fatalf("ready asset state/size = %s/%d", gotReady.State, gotReady.SizeBytes)
	}

	missing := Asset{
		AssetID:    BuildManifestAssetID("twitter", "tweet", "sample_tweet_missing", "post_media", 0),
		AssetKind:  "post_media",
		OwnerKind:  "tweet",
		OwnerID:    "sample_tweet_missing",
		FilePath:   filepath.Join("media", "twitter", "sample", "missing.jpg"),
		MediaIndex: 0,
		State:      AssetStateQueued,
	}
	if err := d.UpsertAsset(missing, 1000); err != nil {
		t.Fatalf("upsert missing asset: %v", err)
	}
	if err := d.RefreshAssetFileState(missing.AssetID, missing.AssetKind, 2000); err != nil {
		t.Fatalf("refresh missing asset: %v", err)
	}
	gotMissing, err := d.GetAsset(missing.AssetID, missing.AssetKind)
	if err != nil {
		t.Fatalf("get missing asset: %v", err)
	}
	if gotMissing.State != AssetStateServerMissing || gotMissing.SizeBytes != 0 {
		t.Fatalf("missing asset state/size = %s/%d", gotMissing.State, gotMissing.SizeBytes)
	}
}

func TestBackfillAssetsFromExistingPaths(t *testing.T) {
	d := openWritableTestDB(t)

	writeDBTestFile(t, filepath.Join(d.dataDir, "media", "twitter", "sample", "tweet_media.jpg"), []byte("tweet-media"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "media", "twitter", "sample", "quote_media.mp4"), []byte("quote-video"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "thumbnails", "generated", "quote_asset.jpg"), []byte("quote-thumb"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "videos", "youtube", "vid.mp4"), []byte("video-stream"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "videos", "youtube", "vid.jpg"), []byte("video-thumb"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "videos", "youtube", "vid.en.vtt"), []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nhi\n"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "thumbnails", "dearrow", "youtube_vid.jpg"), []byte("dearrow-thumb"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "thumbnails", "previews", "youtube_vid", "track.json"), []byte(`{"frames":[]}`))
	writeDBTestFile(t, filepath.Join(d.dataDir, "thumbnails", "previews", "youtube_vid", "sprite.jpg"), []byte("sprite"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "thumbnails", "avatars", "youtube_sample.jpg"), []byte("avatar"))
	writeDBTestFile(t, filepath.Join(d.dataDir, "thumbnails", "banners", "youtube_sample.jpg"), []byte("banner"))

	if err := d.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size)
		VALUES
			('feed_media', 'tweet_asset', 0, 'media/twitter/sample/tweet_media.jpg', 'photo', 'https://example.test/tweet.jpg', 11),
			('quote_media', 'quote_asset', 0, 'media/twitter/sample/quote_media.mp4', 'video', 'https://example.test/quote.mp4', 11)
	`); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (
			video_id, channel_id, title, thumbnail_path, file_path, file_size,
			published_at, dearrow_thumb_path
		) VALUES (
			'youtube_vid', 'youtube_sample', 'Video',
			'videos/youtube/vid.jpg', 'videos/youtube/vid.mp4', 12,
			1234, 'thumbnails/dearrow/youtube_vid.jpg'
		)
	`); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, avatar_url, banner_url, fetched_at)
		VALUES ('youtube_sample', 'youtube', 'chan', 'https://example.test/avatar.jpg', 'https://example.test/banner.jpg', 1234)
	`); err != nil {
		t.Fatalf("insert profile: %v", err)
	}

	n, err := d.BackfillAssetsFromExistingPaths(5000)
	if err != nil {
		t.Fatalf("BackfillAssetsFromExistingPaths: %v", err)
	}
	if n < 10 {
		t.Fatalf("backfilled %d assets, want at least 10", n)
	}

	want := []struct {
		id   string
		kind string
	}{
		{BuildManifestAssetID("twitter", "tweet", "tweet_asset", "post_media", 0), "post_media"},
		{BuildManifestAssetID("twitter", "tweet", "quote_asset", "post_media", 0), "post_media"},
		{BuildManifestAssetID("twitter", "tweet", "quote_asset", "post_thumbnail", 0), "post_thumbnail"},
		{BuildManifestAssetID("youtube", "youtube_video", "youtube_vid", "video_stream", 0), "video_stream"},
		{BuildManifestAssetID("youtube", "youtube_video", "youtube_vid", "post_thumbnail", 0), "post_thumbnail"},
		{BuildManifestAssetID("youtube", "youtube_video", "youtube_vid", "dearrow_thumbnail", 0), "dearrow_thumbnail"},
		{BuildManifestAssetID("youtube", "youtube_video", "youtube_vid", "subtitle", 0), "subtitle"},
		{BuildManifestAssetID("youtube", "youtube_video", "youtube_vid", "preview_track_json", 0), "preview_track_json"},
		{BuildManifestAssetID("youtube", "youtube_video", "youtube_vid", "preview_sprite", 0), "preview_sprite"},
		{BuildManifestAssetID("youtube", "channel", "youtube_sample", "avatar", 0), "avatar"},
		{BuildManifestAssetID("youtube", "channel", "youtube_sample", "banner", 0), "banner"},
	}
	for _, tt := range want {
		got, err := d.GetAsset(tt.id, tt.kind)
		if err != nil {
			t.Fatalf("GetAsset %s/%s: %v", tt.id, tt.kind, err)
		}
		if got == nil {
			t.Fatalf("missing asset %s/%s", tt.id, tt.kind)
		}
		if got.State != AssetStateReady {
			t.Fatalf("asset %s state = %s, want ready", tt.id, got.State)
		}
	}
}

func TestReconcileAssetInventoryFromExistingPathsIsBoundedAndDryRun(t *testing.T) {
	d := openWritableTestDB(t)

	existingRelPath := filepath.Join("media", "twitter", "sample", "sample_tweet_reconcile_existing.jpg")
	firstRelPath := filepath.Join("media", "twitter", "sample", "sample_tweet_reconcile_first.jpg")
	secondRelPath := filepath.Join("media", "twitter", "sample", "sample_tweet_reconcile_second.jpg")
	writeDBTestFile(t, filepath.Join(d.dataDir, existingRelPath), []byte("existing-media"))
	writeDBTestFile(t, filepath.Join(d.dataDir, firstRelPath), []byte("first-media"))
	writeDBTestFile(t, filepath.Join(d.dataDir, secondRelPath), []byte("second-media"))

	if err := d.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size)
		VALUES
			('feed_media', 'sample_tweet_reconcile_existing', 0, ?, 'photo', 'https://example.test/existing.jpg', 14),
			('feed_media', 'sample_tweet_reconcile_first', 0, ?, 'photo', 'https://example.test/first.jpg', 11),
			('feed_media', 'sample_tweet_reconcile_second', 0, ?, 'photo', 'https://example.test/second.jpg', 12)
	`, existingRelPath, firstRelPath, secondRelPath); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}
	existingID := BuildManifestAssetID("twitter", "tweet", "sample_tweet_reconcile_existing", "post_media", 0)
	if err := d.UpsertAsset(Asset{
		AssetID:    existingID,
		AssetKind:  "post_media",
		OwnerKind:  "tweet",
		OwnerID:    "sample_tweet_reconcile_existing",
		MediaIndex: 0,
		FilePath:   existingRelPath,
		State:      AssetStateReady,
	}, 1000); err != nil {
		t.Fatalf("seed existing asset: %v", err)
	}

	firstID := BuildManifestAssetID("twitter", "tweet", "sample_tweet_reconcile_first", "post_media", 0)
	secondID := BuildManifestAssetID("twitter", "tweet", "sample_tweet_reconcile_second", "post_media", 0)
	dry, err := d.ReconcileAssetInventoryFromExistingPaths(AssetInventoryReconcileOptions{
		NowMs:  5000,
		Limit:  1,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry run reconcile: %v", err)
	}
	if dry.Candidates != 1 || dry.Written != 0 || !dry.LimitReached || dry.SkippedExisting != 1 {
		t.Fatalf("dry run result = %+v", dry)
	}
	if dry.ByKind["post_media"].Ready != 1 {
		t.Fatalf("dry run by-kind result = %+v", dry.ByKind["post_media"])
	}
	if got, err := d.GetAsset(firstID, "post_media"); err != nil || got != nil {
		t.Fatalf("dry run wrote first asset: got=%+v err=%v", got, err)
	}

	first, err := d.ReconcileAssetInventoryFromExistingPaths(AssetInventoryReconcileOptions{
		NowMs: 6000,
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if first.Candidates != 1 || first.Written != 1 || !first.LimitReached || first.SkippedExisting != 1 {
		t.Fatalf("first reconcile result = %+v", first)
	}
	gotFirst, err := d.GetAsset(firstID, "post_media")
	if err != nil {
		t.Fatalf("get first asset: %v", err)
	}
	if gotFirst == nil || gotFirst.FilePath != firstRelPath || gotFirst.State != AssetStateReady {
		t.Fatalf("first asset mismatch: %+v", gotFirst)
	}
	if got, err := d.GetAsset(secondID, "post_media"); err != nil || got != nil {
		t.Fatalf("first reconcile wrote second asset: got=%+v err=%v", got, err)
	}

	second, err := d.ReconcileAssetInventoryFromExistingPaths(AssetInventoryReconcileOptions{
		NowMs: 7000,
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if second.Candidates != 1 || second.Written != 1 || !second.LimitReached || second.SkippedExisting != 2 {
		t.Fatalf("second reconcile result = %+v", second)
	}
	gotSecond, err := d.GetAsset(secondID, "post_media")
	if err != nil {
		t.Fatalf("get second asset: %v", err)
	}
	if gotSecond == nil || gotSecond.FilePath != secondRelPath || gotSecond.State != AssetStateReady {
		t.Fatalf("second asset mismatch: %+v", gotSecond)
	}
}

func TestBackfillAssetsFromExistingPathsPreservesRetryStateUntilFileReady(t *testing.T) {
	d := openWritableTestDB(t)
	missingRelPath := filepath.Join("media", "twitter", "sample", "retry_missing.jpg")
	if err := d.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size)
		VALUES ('feed_media', 'sample_tweet_retry_missing', 0, ?, 'photo', 'https://example.test/missing.jpg', 25)
	`, missingRelPath); err != nil {
		t.Fatalf("insert missing media_files: %v", err)
	}
	missingID := BuildManifestAssetID("twitter", "tweet", "sample_tweet_retry_missing", "post_media", 0)
	if err := d.UpsertAsset(Asset{
		AssetID:         missingID,
		AssetKind:       "post_media",
		OwnerKind:       "tweet",
		OwnerID:         "sample_tweet_retry_missing",
		MediaIndex:      0,
		FilePath:        missingRelPath,
		State:           AssetStateFailed,
		LastErrorKind:   "transient_http",
		LastError:       "temporary fetch failure",
		Attempts:        3,
		NextAttemptAtMs: 9000,
	}, 1000); err != nil {
		t.Fatalf("seed failed asset: %v", err)
	}

	if _, err := d.BackfillAssetsFromExistingPaths(2000); err != nil {
		t.Fatalf("BackfillAssetsFromExistingPaths missing: %v", err)
	}
	gotMissing, err := d.GetAsset(missingID, "post_media")
	if err != nil {
		t.Fatalf("GetAsset missing: %v", err)
	}
	if gotMissing.State != AssetStateFailed ||
		gotMissing.LastErrorKind != "transient_http" ||
		gotMissing.LastError != "temporary fetch failure" ||
		gotMissing.Attempts != 3 ||
		gotMissing.NextAttemptAtMs != 9000 {
		t.Fatalf("backfill clobbered failed retry state: %+v", *gotMissing)
	}

	readyRelPath := filepath.Join("media", "twitter", "sample", "retry_ready.jpg")
	writeDBTestFile(t, filepath.Join(d.dataDir, readyRelPath), []byte("ready after retry"))
	if err := d.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, source_url, file_size)
		VALUES ('feed_media', 'sample_tweet_retry_ready', 0, ?, 'photo', 'https://example.test/ready.jpg', 16)
	`, readyRelPath); err != nil {
		t.Fatalf("insert ready media_files: %v", err)
	}
	readyID := BuildManifestAssetID("twitter", "tweet", "sample_tweet_retry_ready", "post_media", 0)
	if err := d.UpsertAsset(Asset{
		AssetID:         readyID,
		AssetKind:       "post_media",
		OwnerKind:       "tweet",
		OwnerID:         "sample_tweet_retry_ready",
		MediaIndex:      0,
		FilePath:        readyRelPath,
		State:           AssetStatePermanentMissing,
		LastErrorKind:   "permanent_http",
		LastError:       "gone",
		Attempts:        5,
		NextAttemptAtMs: 12000,
	}, 1000); err != nil {
		t.Fatalf("seed permanent asset: %v", err)
	}

	if _, err := d.BackfillAssetsFromExistingPaths(3000); err != nil {
		t.Fatalf("BackfillAssetsFromExistingPaths ready: %v", err)
	}
	gotReady, err := d.GetAsset(readyID, "post_media")
	if err != nil {
		t.Fatalf("GetAsset ready: %v", err)
	}
	if gotReady.State != AssetStateReady ||
		gotReady.LastErrorKind != "" ||
		gotReady.LastError != "" ||
		gotReady.Attempts != 0 ||
		gotReady.NextAttemptAtMs != 0 {
		t.Fatalf("ready backfill did not clear retry state: %+v", *gotReady)
	}
}

func TestListAndroidSyncAssetInventoryRowsFiltersOwnerKind(t *testing.T) {
	d := openWritableTestDB(t)
	sharedID := "shared_owner"
	rows := []Asset{
		{
			AssetID:    BuildManifestAssetID("twitter", "tweet", sharedID, "post_media", 0),
			AssetKind:  "post_media",
			OwnerKind:  "tweet",
			OwnerID:    sharedID,
			MediaIndex: 0,
			State:      AssetStateReady,
		},
		{
			AssetID:    BuildManifestAssetID("twitter", "channel", sharedID, "avatar", 0),
			AssetKind:  "avatar",
			OwnerKind:  "channel",
			OwnerID:    sharedID,
			MediaIndex: 0,
			State:      AssetStateReady,
		},
	}
	for _, row := range rows {
		if err := d.UpsertAsset(row, 1000); err != nil {
			t.Fatalf("UpsertAsset %s: %v", row.AssetKind, err)
		}
	}

	tweetRows, err := d.ListAndroidSyncAssetInventoryRows(AndroidSyncDesiredSets{
		Tweets:      map[string]struct{}{sharedID: {}},
		Videos:      map[string]struct{}{},
		MediaVideos: map[string]struct{}{},
		Channels:    map[string]struct{}{},
	})
	if err != nil {
		t.Fatalf("ListAndroidSyncAssetInventoryRows tweets: %v", err)
	}
	if len(tweetRows) != 1 || tweetRows[0].OwnerKind != "tweet" || tweetRows[0].AssetKind != "post_media" {
		t.Fatalf("tweet desired set returned wrong-domain rows: %+v", tweetRows)
	}

	channelRows, err := d.ListAndroidSyncAssetInventoryRows(AndroidSyncDesiredSets{
		Tweets:      map[string]struct{}{},
		Videos:      map[string]struct{}{},
		MediaVideos: map[string]struct{}{},
		Channels:    map[string]struct{}{sharedID: {}},
	})
	if err != nil {
		t.Fatalf("ListAndroidSyncAssetInventoryRows channels: %v", err)
	}
	if len(channelRows) != 1 || channelRows[0].OwnerKind != "channel" || channelRows[0].AssetKind != "avatar" {
		t.Fatalf("channel desired set returned wrong-domain rows: %+v", channelRows)
	}
}

func TestInsertMediaFileMaintainsAssetInventory(t *testing.T) {
	d := openWritableTestDB(t)
	relPath := filepath.Join("media", "twitter", "sample", "sample_tweet_insert_0.jpg")
	writeDBTestFile(t, filepath.Join(d.dataDir, relPath), []byte("inserted-media"))

	if err := d.InsertMediaFile(model.MediaFile{
		OwnerType:  "feed_media",
		OwnerID:    "sample_tweet_insert",
		MediaIndex: 0,
		FilePath:   relPath,
		MediaType:  "photo",
		SourceURL:  "https://example.test/insert.jpg",
		FileSize:   14,
	}); err != nil {
		t.Fatalf("InsertMediaFile: %v", err)
	}

	got, err := d.GetAsset(BuildManifestAssetID("twitter", "tweet", "sample_tweet_insert", "post_media", 0), "post_media")
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if got == nil {
		t.Fatal("inserted media asset missing")
	}
	if got.State != AssetStateReady || got.FilePath != relPath || got.SourceURL != "https://example.test/insert.jpg" {
		t.Fatalf("inserted media asset mismatch: %+v", *got)
	}
}

func TestInsertMediaFileDuplicateKeepsAssetInventoryOnPersistedRow(t *testing.T) {
	d := openWritableTestDB(t)

	firstRelPath := filepath.Join("media", "twitter", "sample", "tweet_duplicate_0.jpg")
	secondRelPath := filepath.Join("media", "twitter", "sample", "tweet_duplicate_ignored_0.jpg")
	writeDBTestFile(t, filepath.Join(d.dataDir, firstRelPath), []byte("first persisted media"))
	writeDBTestFile(t, filepath.Join(d.dataDir, secondRelPath), []byte("ignored duplicate media"))

	first := model.MediaFile{
		OwnerType:  "feed_media",
		OwnerID:    "sample_tweet_duplicate_asset",
		MediaIndex: 0,
		FilePath:   firstRelPath,
		MediaType:  "photo",
		SourceURL:  "https://example.test/first.jpg",
		FileSize:   21,
	}
	duplicate := model.MediaFile{
		OwnerType:  "feed_media",
		OwnerID:    first.OwnerID,
		MediaIndex: first.MediaIndex,
		FilePath:   secondRelPath,
		MediaType:  "photo",
		SourceURL:  "https://example.test/second.jpg",
		FileSize:   23,
	}
	if err := d.InsertMediaFile(first); err != nil {
		t.Fatalf("first InsertMediaFile: %v", err)
	}
	if err := d.InsertMediaFile(duplicate); err != nil {
		t.Fatalf("duplicate InsertMediaFile: %v", err)
	}

	assetID := BuildManifestAssetID("twitter", "tweet", first.OwnerID, "post_media", 0)
	got, err := d.GetAsset(assetID, "post_media")
	if err != nil {
		t.Fatalf("GetAsset after single duplicate: %v", err)
	}
	if got == nil || got.FilePath != firstRelPath || got.SourceURL != first.SourceURL || got.SizeBytes != int64(len("first persisted media")) {
		t.Fatalf("single duplicate asset diverged from persisted media_files row: %+v", got)
	}

	batchFirst := model.MediaFile{
		OwnerType:  "feed_media",
		OwnerID:    "sample_tweet_duplicate_batch_asset",
		MediaIndex: 0,
		FilePath:   firstRelPath,
		MediaType:  "photo",
		SourceURL:  "https://example.test/batch-first.jpg",
		FileSize:   21,
	}
	batchDuplicate := model.MediaFile{
		OwnerType:  "feed_media",
		OwnerID:    batchFirst.OwnerID,
		MediaIndex: batchFirst.MediaIndex,
		FilePath:   secondRelPath,
		MediaType:  "photo",
		SourceURL:  "https://example.test/batch-second.jpg",
		FileSize:   23,
	}
	if err := d.InsertMediaFileBatch([]model.MediaFile{batchFirst}); err != nil {
		t.Fatalf("first InsertMediaFileBatch: %v", err)
	}
	if err := d.InsertMediaFileBatch([]model.MediaFile{batchDuplicate}); err != nil {
		t.Fatalf("duplicate InsertMediaFileBatch: %v", err)
	}
	batchAssetID := BuildManifestAssetID("twitter", "tweet", batchFirst.OwnerID, "post_media", 0)
	gotBatch, err := d.GetAsset(batchAssetID, "post_media")
	if err != nil {
		t.Fatalf("GetAsset after batch duplicate: %v", err)
	}
	if gotBatch == nil || gotBatch.FilePath != firstRelPath || gotBatch.SourceURL != batchFirst.SourceURL || gotBatch.SizeBytes != int64(len("first persisted media")) {
		t.Fatalf("batch duplicate asset diverged from persisted media_files row: %+v", gotBatch)
	}
}

func writeDBTestFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
