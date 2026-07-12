package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/storage"
)

func TestStoreReadyBulkAssetWaitsForMediaExecutor(t *testing.T) {
	d := openWritableTestDB(t)
	path, err := d.storage.WritePath("media/youtube/sample.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, 0o644); err != nil {
		t.Fatal(err)
	}

	bulkEntered := make(chan struct{})
	releaseBulk := make(chan struct{})
	go func() {
		_ = d.storage.MediaExecutor().Run(context.Background(), storage.MediaLaneBulk, func() error {
			close(bulkEntered)
			<-releaseBulk
			return nil
		})
	}()
	<-bulkEntered

	stored := make(chan error, 1)
	go func() {
		stored <- d.StoreReadyAsset(Asset{
			AssetID: "sample_stream", AssetKind: "video_stream", OwnerKind: "youtube_video", OwnerID: "sample",
			FilePath: "media/youtube/sample.mp4", ContentType: "video/mp4",
		}, 1)
	}()
	select {
	case err := <-stored:
		t.Fatalf("bulk asset bypassed executor: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseBulk)
	if err := <-stored; err != nil {
		t.Fatal(err)
	}
}

func upsertAssetForTest(t *testing.T, d *DB, asset Asset, nowMs int64) {
	t.Helper()
	if asset.State == AssetStateReady {
		t.Fatal("ready test assets must use storeReadyAssetForTest")
	}
	asset = normalizeAsset(asset, nowMs)
	if err := d.WithWrite(func(tx *sql.Tx) error { return upsertAssetTx(tx, asset) }); err != nil {
		t.Fatalf("upsert test asset: %v", err)
	}
}

func storeReadyAssetForTest(t *testing.T, d *DB, asset Asset, nowMs int64) {
	t.Helper()
	path, err := d.storage.Path(asset.FilePath)
	if err != nil {
		t.Fatalf("resolve test asset: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test asset directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("asset:"+asset.AssetID), 0o644); err != nil {
		t.Fatalf("write test asset: %v", err)
	}
	if err := d.StoreReadyAsset(asset, nowMs); err != nil {
		t.Fatalf("store ready test asset: %v", err)
	}
}

func publishAssetMetadataForTest(t *testing.T, d *DB, asset Asset, nowMs int64) {
	t.Helper()
	asset.State = AssetStateReady
	if asset.ContentType == "" {
		asset.ContentType = "image/jpeg"
	}
	if asset.SizeBytes <= 0 {
		asset.SizeBytes = 1
	}
	if asset.SHA256 == "" {
		asset.SHA256 = "fixture"
	}
	if asset.FileMtimeNs <= 0 {
		asset.FileMtimeNs = 1
	}
	asset = normalizeAsset(asset, nowMs)
	if err := d.WithWrite(func(tx *sql.Tx) error { return upsertAssetTx(tx, asset) }); err != nil {
		t.Fatalf("publish test asset metadata: %v", err)
	}
}

func TestListReadyAssetsForOwnersKeepsOwnerKindsDistinct(t *testing.T) {
	d := openWritableTestDB(t)
	for _, asset := range []Asset{
		{AssetID: "sample_media", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post", FilePath: "media/twitter/sample.jpg"},
		{AssetID: "sample_photo", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_first", FilePath: "media/twitter/other.jpg"},
		{AssetID: "sample_avatar", AssetKind: "avatar", OwnerKind: "channel", OwnerID: "sample_post", FilePath: "thumbnails/avatars/sample.jpg"},
	} {
		publishAssetMetadataForTest(t, d, asset, 1)
	}

	assets, err := d.ListReadyAssetsForOwners([]AssetOwnerRef{
		{OwnerKind: "tweet", OwnerID: "sample_first"},
		{OwnerKind: "channel", OwnerID: "sample_post"},
		{OwnerKind: "tweet", OwnerID: "sample_post"},
		{OwnerKind: "tweet", OwnerID: "sample_post"},
	}, []string{"post_media"})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || assets[0].AssetID != "sample_photo" || assets[1].AssetID != "sample_media" {
		t.Fatalf("ready post media = %+v", assets)
	}
}

func TestStoreReadyAssetOwnsChecksumAndRevision(t *testing.T) {
	d := openWritableTestDB(t)
	rel := filepath.Join("media", "twitter", "sample", "asset.jpg")
	abs := filepath.Join(d.storage.StateRoot(), rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(raw string, mtime time.Time) {
		t.Helper()
		if err := os.WriteFile(abs, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(abs, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	store := func(nowMs int64) Asset {
		t.Helper()
		if err := d.StoreReadyAsset(Asset{
			AssetID:        "twitter_tweet_sample_post_media_0",
			AssetKind:      "post_media",
			OwnerKind:      "tweet",
			OwnerID:        "sample",
			FilePath:       rel,
			ContentType:    "image/jpeg",
			RequiredReason: "retention",
		}, nowMs); err != nil {
			t.Fatal(err)
		}
		got, err := d.GetAsset("twitter_tweet_sample_post_media_0", "post_media")
		if err != nil || got == nil {
			t.Fatalf("GetAsset: %+v / %v", got, err)
		}
		return *got
	}

	baseTime := time.Unix(100, 0)
	write("first", baseTime)
	first := store(1000)
	firstSum := sha256.Sum256([]byte("first"))
	if first.SHA256 != hex.EncodeToString(firstSum[:]) || first.SizeBytes != 5 || first.Revision != 1 {
		t.Fatalf("first asset metadata = %+v", first)
	}

	unchanged := store(2000)
	if unchanged.Revision != first.Revision || unchanged.SHA256 != first.SHA256 {
		t.Fatalf("unchanged declaration revised asset: before=%+v after=%+v", first, unchanged)
	}

	write("other", baseTime.Add(time.Second))
	replaced := store(3000)
	otherSum := sha256.Sum256([]byte("other"))
	if replaced.SHA256 != hex.EncodeToString(otherSum[:]) || replaced.SizeBytes != 5 {
		t.Fatalf("replacement metadata = %+v", replaced)
	}
	if replaced.Revision != first.Revision+1 {
		t.Fatalf("replacement revision = %d, want %d", replaced.Revision, first.Revision+1)
	}
}

func TestMarkReadyAssetUnavailableIsFileIdentityConditional(t *testing.T) {
	d := openWritableTestDB(t)
	assetID := "sample_ready_asset"
	storeReadyAssetForTest(t, d, Asset{
		AssetID: assetID, AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post",
		FilePath: "media/twitter/sample-ready.jpg", ContentType: "image/jpeg",
	}, 1000)
	before, err := d.GetAsset(assetID, "post_media")
	if err != nil || before == nil {
		t.Fatalf("ready asset = %+v / %v", before, err)
	}
	headBefore := requireAndroidSyncHead(t, d, "asset", assetID)

	staleRevision := *before
	staleRevision.Revision++
	changed, err := d.MarkReadyAssetUnavailable(staleRevision, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("stale revision withdrew the ready asset")
	}
	if err := d.ExecRaw(`
		UPDATE media_objects SET file_mtime_ns = file_mtime_ns + 1
		WHERE object_id = (SELECT object_id FROM assets WHERE asset_id = 'sample_ready_asset')
	`); err != nil {
		t.Fatal(err)
	}
	changed, err = d.MarkReadyAssetUnavailable(*before, 1002)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("stale file identity withdrew the revalidated asset")
	}
	current, err := d.GetAsset(assetID, "post_media")
	if err != nil || current == nil || current.State != AssetStateReady || current.Revision != before.Revision || current.FileMtimeNs == before.FileMtimeNs {
		t.Fatalf("revalidated asset = %+v / %v, before=%+v", current, err, before)
	}

	changed, err = d.MarkReadyAssetUnavailable(*current, 1003)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("current ready file identity was not withdrawn")
	}
	after, err := d.GetAsset(assetID, "post_media")
	if err != nil || after == nil {
		t.Fatalf("withdrawn asset = %+v / %v", after, err)
	}
	headAfter := requireAndroidSyncHead(t, d, "asset", assetID)
	if after.State != AssetStateServerMissing || after.Revision <= current.Revision || headAfter.Revision <= headBefore.Revision {
		t.Fatalf("withdrawal did not advance asset/head: asset=%+v head=%+v -> asset=%+v head=%+v",
			current, headBefore, after, headAfter)
	}
}

func TestStoreReadyAssetWaitsForFileAndDirectoryDurability(t *testing.T) {
	d := openWritableTestDB(t)
	key := filepath.Join("media", "twitter", "sample", "durable.jpg")
	path, err := d.storage.WritePath(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("durable bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	asset := Asset{
		AssetID: "twitter_tweet_sample_media_post_media", AssetKind: "post_media",
		OwnerKind: "tweet", OwnerID: "sample_media", FilePath: key, ContentType: "image/jpeg",
	}
	injected := errors.New("directory sync failed")
	var order []string
	d.readyAssetDurability = func(path string) error {
		assertNotReady := func() error {
			var count int
			if err := d.QueryRow(`
				SELECT COUNT(*) FROM assets a JOIN media_objects mo ON mo.object_id = a.object_id
				WHERE a.asset_id = ? AND mo.published_revision > 0
			`, asset.AssetID).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return errors.New("asset became ready before durability completed")
			}
			return nil
		}
		return makeReadyAssetDurableWith(path, func(file *os.File) error {
			order = append(order, "file")
			if err := assertNotReady(); err != nil {
				return err
			}
			return file.Sync()
		}, func(string) error {
			order = append(order, "directory")
			if err := assertNotReady(); err != nil {
				return err
			}
			return injected
		})
	}

	if err := d.StoreReadyAsset(asset, 1000); !errors.Is(err, injected) {
		t.Fatalf("StoreReadyAsset error = %v, want injected directory sync failure", err)
	}
	if len(order) != 2 || order[0] != "file" || order[1] != "directory" {
		t.Fatalf("durability order = %v, want file then directory", order)
	}
	if stored, err := d.GetAsset(asset.AssetID, asset.AssetKind); err != nil || stored != nil {
		t.Fatalf("failed durability published asset: %+v, %v", stored, err)
	}

	d.readyAssetDurability = nil
	if err := d.StoreReadyAsset(asset, 1001); err != nil {
		t.Fatal(err)
	}
	if stored, err := d.GetAsset(asset.AssetID, asset.AssetKind); err != nil || stored == nil || stored.State != AssetStateReady {
		t.Fatalf("durable asset was not published: %+v, %v", stored, err)
	}
	durabilityCalls := 0
	d.readyAssetDurability = func(string) error {
		durabilityCalls++
		return injected
	}
	if err := d.StoreReadyAsset(asset, 1002); err != nil {
		t.Fatalf("unchanged ready asset repeated durability work: %v", err)
	}
	if durabilityCalls != 0 {
		t.Fatalf("unchanged ready asset durability calls = %d", durabilityCalls)
	}
}

func TestStoreReadyAssetRequiresPublishedContentMetadata(t *testing.T) {
	d := openWritableTestDB(t)
	write := func(key string, body []byte) {
		t.Helper()
		path, err := d.storage.Path(key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("media/test/unknown.bin", []byte("body"))
	if err := d.StoreReadyAsset(Asset{
		AssetID: "test_unknown", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "test_unknown",
		FilePath: "media/test/unknown.bin",
	}, 1); err == nil {
		t.Fatal("StoreReadyAsset accepted an unknown content type")
	}

	write("media/test/empty.jpg", nil)
	if err := d.StoreReadyAsset(Asset{
		AssetID: "test_empty", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "test_empty",
		FilePath: "media/test/empty.jpg", ContentType: "image/jpeg",
	}, 1); err == nil {
		t.Fatal("StoreReadyAsset accepted an empty file")
	}

	write("media/test/fake.mp4", []byte("<!doctype html><html></html>"))
	if err := d.StoreReadyAsset(Asset{
		AssetID: "test_html", AssetKind: "video_stream", OwnerKind: "youtube_video", OwnerID: "test_html",
		FilePath: "media/test/fake.mp4", ContentType: "video/mp4",
	}, 1); err == nil {
		t.Fatal("StoreReadyAsset accepted HTML as video")
	}
}

func TestCanonicalAssetContentTypeUsesBytesAndNormalizesM4A(t *testing.T) {
	pngPath := filepath.Join(t.TempDir(), "payload.tmp")
	if err := os.WriteFile(pngPath, []byte("\x89PNG\r\n\x1a\nactual-png"), 0o644); err != nil {
		t.Fatal(err)
	}
	contentType, err := CanonicalAssetContentType(pngPath, "thumbnails/avatar.jpg", "avatar", "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", contentType)
	}

	audioPath := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(audioPath, []byte("unrecognized-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	contentType, err = CanonicalAssetContentType(audioPath, "media/sample.m4a", "post_audio", "audio/x-m4a")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "audio/mp4" {
		t.Fatalf("content type = %q, want audio/mp4", contentType)
	}
}

func TestXSourceObservationPreservesSuccessfulCapture(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		ownerID   = "sample_source_change"
		assetKind = "post_media"
		oldSource = "https://example.test/old.jpg"
		newSource = "https://example.test/new.jpg"
	)
	key := filepath.Join("media", "twitter", ownerID, "captured.jpg")
	path, err := d.storage.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	writeDBTestFile(t, path, []byte("captured bytes"))
	assetID := BuildAssetID("twitter", "tweet", ownerID, assetKind, 0)
	if err := d.StoreReadyAsset(Asset{
		AssetID: assetID, AssetKind: assetKind, OwnerKind: "tweet", OwnerID: ownerID,
		SourceURL: oldSource, FilePath: key, ContentType: "image/jpeg", RequiredReason: "retention",
	}, 1000); err != nil {
		t.Fatal(err)
	}
	before, err := d.GetAsset(assetID, assetKind)
	if err != nil || before == nil {
		t.Fatalf("ready asset = %+v, err = %v", before, err)
	}

	err = d.WithWrite(func(tx *sql.Tx) error {
		return declareSourceAssetTx(tx, Asset{
			AssetID: assetID, AssetKind: assetKind, OwnerKind: "tweet", OwnerID: ownerID,
			SourceURL: newSource, ContentType: "image/jpeg", State: AssetStateQueued,
			RequiredReason: "retention",
		}, 2000)
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := d.GetAsset(assetID, assetKind)
	if err != nil || after == nil {
		t.Fatalf("asset after new observation = %+v, err = %v", after, err)
	}
	if after.State != AssetStateReady || after.SourceURL != newSource || after.PublishedSourceURL != oldSource ||
		after.FilePath != before.FilePath || after.SHA256 != before.SHA256 || after.Revision != before.Revision+1 {
		t.Fatalf("successful capture was demoted by a source observation: before=%+v after=%+v", before, after)
	}
	claimed, err := d.ClaimContentAssetDownloadBatch(LeaseOptions{
		Owner: "sample-worker", NowMs: 2001, LeaseMs: time.Minute.Milliseconds(), Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].SourceURL != newSource || claimed[0].PublishedSourceURL != oldSource {
		t.Fatalf("replacement claim did not preserve published capture: %+v", claimed)
	}
}

func TestReadyAssetPublicationIsIdempotent(t *testing.T) {
	d := openWritableTestDB(t)

	asset := Asset{
		AssetID:        BuildAssetID("twitter", "tweet", "sample_tweet_asset_a", "post_media", 0),
		AssetKind:      "post_media",
		OwnerKind:      "tweet",
		OwnerID:        "sample_tweet_asset_a",
		MediaIndex:     0,
		FilePath:       "media/twitter/sample/sample_tweet_asset_a_0.jpg",
		ContentType:    "image/jpeg",
		State:          AssetStateReady,
		RequiredReason: "retention",
	}
	storeReadyAssetForTest(t, d, asset, 1000)
	asset.FilePath = "media/twitter/sample/sample_tweet_asset_a_0_new.jpg"
	storeReadyAssetForTest(t, d, asset, 2000)

	got, err := d.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if got == nil {
		t.Fatal("asset missing after upsert")
	}
	if got.SizeBytes != int64(len("asset:"+asset.AssetID)) || got.FilePath != asset.FilePath || got.SHA256 == "" || got.FileMtimeNs <= 0 {
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

func TestListAndroidSyncAssetInventoryRowsFiltersOwnerKind(t *testing.T) {
	d := openWritableTestDB(t)
	sharedID := "shared_owner"
	rows := []Asset{
		{
			AssetID:     BuildAssetID("twitter", "tweet", sharedID, "post_media", 0),
			AssetKind:   "post_media",
			OwnerKind:   "tweet",
			OwnerID:     sharedID,
			MediaIndex:  0,
			FilePath:    "media/twitter/sample/shared-owner.jpg",
			ContentType: "image/jpeg",
			State:       AssetStateReady,
		},
		{
			AssetID:     BuildAssetID("twitter", "channel", sharedID, "avatar", 0),
			AssetKind:   "avatar",
			OwnerKind:   "channel",
			OwnerID:     sharedID,
			MediaIndex:  0,
			FilePath:    "thumbnails/avatars/shared-owner.jpg",
			ContentType: "image/jpeg",
			State:       AssetStateReady,
		},
	}
	for _, row := range rows {
		storeReadyAssetForTest(t, d, row, 1000)
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

func TestCompletedVideoWritePublishesAssetInventory(t *testing.T) {
	d := openWritableTestDB(t)

	videoRelPath := filepath.Join("media", "youtube", "sample_video_asset.mp4")
	thumbRelPath := filepath.Join("thumbnails", "videos", "youtube", "sample_video_asset.jpg")
	subtitleRelPath := filepath.Join("subtitles", "youtube", "sample_video_asset.en.vtt")
	dearrowRelPath := filepath.Join("thumbnails", "dearrow", "sample_video_asset.jpg")
	previewTrackRelPath := filepath.Join("thumbnails", "previews", "sample_video_asset", "track.json")
	previewSpriteRelPath := filepath.Join("thumbnails", "previews", "sample_video_asset", "sprite.jpg")
	videoPath, _ := d.storage.Path(videoRelPath)
	thumbPath, _ := d.storage.Path(thumbRelPath)
	subtitlePath, _ := d.storage.Path(subtitleRelPath)
	writeDBTestFile(t, videoPath, []byte("video-stream"))
	writeDBTestFile(t, thumbPath, []byte("video-thumb"))
	writeDBTestFile(t, subtitlePath, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nhi\n"))

	if err := d.StoreCompletedVideo(CompletedVideo{
		VideoID: "sample_video_asset", ChannelID: "youtube_sample_channel", OwnerKind: "youtube_video", Title: "Sample",
		Duration: 60, PublishedAtMs: 1234, MediaKind: "video",
		Assets: []Asset{
			{AssetKind: "video_stream", FilePath: videoRelPath, ContentType: "video/mp4"},
			{AssetKind: "post_thumbnail", FilePath: thumbRelPath, ContentType: "image/jpeg"},
		},
	}); err != nil {
		t.Fatalf("StoreCompletedVideo: %v", err)
	}
	if err := d.StoreVideoSubtitleAssets("sample_video_asset", []Asset{{
		AssetKind: "subtitle", FilePath: subtitleRelPath, ContentType: "text/vtt",
	}}, 1234); err != nil {
		t.Fatalf("StoreVideoSubtitleAssets: %v", err)
	}

	wantReady := []struct {
		kind string
		id   string
		path string
	}{
		{"video_stream", BuildAssetID("youtube", "youtube_video", "sample_video_asset", "video_stream", 0), videoRelPath},
		{"post_thumbnail", BuildAssetID("youtube", "youtube_video", "sample_video_asset", "post_thumbnail", 0), thumbRelPath},
		{"subtitle", BuildAssetID("youtube", "youtube_video", "sample_video_asset", "subtitle", 0), subtitleRelPath},
	}
	for _, tt := range wantReady {
		got, err := d.GetAsset(tt.id, tt.kind)
		if err != nil {
			t.Fatalf("GetAsset %s: %v", tt.kind, err)
		}
		if got == nil || got.State != AssetStateReady || got.FilePath != tt.path || got.RequiredReason != "retention" {
			t.Fatalf("%s asset mismatch: %+v", tt.kind, got)
		}
	}

	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), dearrowRelPath), []byte("dearrow-thumb"))
	if err := d.SetDearrowData("sample_video_asset", nil, nil, &dearrowRelPath, 2000); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}
	dearrowID := BuildAssetID("youtube", "youtube_video", "sample_video_asset", "dearrow_thumbnail", 0)
	gotDearrow, err := d.GetAsset(dearrowID, "dearrow_thumbnail")
	if err != nil {
		t.Fatalf("GetAsset dearrow: %v", err)
	}
	if gotDearrow == nil || gotDearrow.State != AssetStateReady || gotDearrow.FilePath != dearrowRelPath {
		t.Fatalf("dearrow asset mismatch: %+v", gotDearrow)
	}

	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), previewTrackRelPath), []byte(`{"frames":[]}`))
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), previewSpriteRelPath), []byte("sprite"))
	streamAsset, err := d.GetAssetByOwnerIdentity("video_stream", "youtube_video", "sample_video_asset", 0)
	if err != nil || streamAsset == nil {
		t.Fatalf("video stream asset: %+v %v", streamAsset, err)
	}
	if err := d.StoreVideoPreviewAssets("sample_video_asset", streamAsset.SHA256, previewTrackRelPath, previewSpriteRelPath, 3000); err != nil {
		t.Fatalf("StoreVideoPreviewAssets: %v", err)
	}
	for _, tt := range []struct {
		kind string
		id   string
		path string
	}{
		{"preview_track_json", BuildAssetID("youtube", "youtube_video", "sample_video_asset", "preview_track_json", 0), previewTrackRelPath},
		{"preview_sprite", BuildAssetID("youtube", "youtube_video", "sample_video_asset", "preview_sprite", 0), previewSpriteRelPath},
	} {
		got, err := d.GetAsset(tt.id, tt.kind)
		if err != nil {
			t.Fatalf("GetAsset %s: %v", tt.kind, err)
		}
		if got == nil || got.State != AssetStateReady || got.FilePath != tt.path {
			t.Fatalf("%s asset mismatch: %+v", tt.kind, got)
		}
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
