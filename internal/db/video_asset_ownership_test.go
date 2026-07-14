package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/storage"
)

func TestStoreCompletedVideoPublicationDoesNotWaitForDownloadLane(t *testing.T) {
	d := openWritableTestDB(t)
	key := filepath.Join("media", "instagram", "sample_foreground.mp4")
	path, err := d.storage.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	writeDBTestFile(t, path, []byte("foreground video"))

	started := make(chan struct{})
	release := make(chan struct{})
	backgroundDone := make(chan error, 1)
	go func() {
		backgroundDone <- d.storage.MediaExecutor().Run(context.Background(), storage.MediaLaneBulkBackground, func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	storeDone := make(chan error, 1)
	go func() {
		storeDone <- d.StoreCompletedVideo(CompletedVideo{
			VideoID: "sample_foreground", ChannelID: "instagram_sample", OwnerKind: "instagram_reel",
			Assets: []Asset{{AssetKind: "video_stream", FilePath: key}},
		})
	}()

	select {
	case err := <-storeDone:
		if err != nil {
			close(release)
			<-backgroundDone
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		<-backgroundDone
		t.Fatal("completed video publication waited for the download lane")
	}
	close(release)
	if err := <-backgroundDone; err != nil {
		t.Fatal(err)
	}
}

func TestStoreCompletedVideoObservesInstagramAccountsAndDropsRawAvatars(t *testing.T) {
	d := openWritableTestDB(t)
	key := filepath.Join("media", "instagram", "sample_video.mp4")
	path, err := d.storage.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	writeDBTestFile(t, path, []byte("video"))
	if err := d.StoreCompletedVideo(CompletedVideo{
		VideoID: "sample_video", ChannelID: "instagram_sample_author", OwnerKind: "instagram_reel",
		MetadataJSON: `{"coauthors":[{"username":"sample_author","full_name":"Author"},{"username":"sample_creator","full_name":"Creator","profile_pic_url":"https://cdn.example/creator.jpg"}],"tagged_users":[{"username":"sample_creator","full_name":"Duplicate"},{"username":"sample_user","full_name":"Tagged","profile_pic_url":"https://cdn.example/tagged.jpg"}]}`,
		Assets:       []Asset{{AssetKind: "video_stream", FilePath: key}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, channelID := range []string{"instagram_sample_creator", "instagram_sample_user"} {
		profile, err := d.GetChannelProfile(channelID)
		if err != nil || profile == nil {
			t.Fatalf("profile %s = %+v, %v", channelID, profile, err)
		}
		job, err := d.GetProfileJob(channelID)
		if err != nil || job == nil || job.RequestedRevision <= job.CompletedRevision {
			t.Fatalf("profile job %s = %+v, %v", channelID, job, err)
		}
	}
	var stored string
	if err := d.QueryRow(`SELECT metadata_json FROM videos WHERE video_id = 'sample_video'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "profile_pic_url") || strings.Contains(stored, "cdn.example") {
		t.Fatalf("stored metadata retained raw avatar source: %s", stored)
	}
}

func TestStoreCompletedVideoReplacesPrimaryAssetsAndPreservesDerivedThumbnail(t *testing.T) {
	d := openWritableTestDB(t)
	videoID := "sample_video"
	oldVideo := filepath.Join("media", "youtube", videoID+".mp4")
	oldThumb := filepath.Join("thumbnails", "videos", "youtube", videoID+".jpg")
	newVideo := filepath.Join("media", "youtube", videoID+"-new.mp4")
	newThumb := filepath.Join("thumbnails", "videos", "youtube", videoID+"-new.jpg")
	dearrow := filepath.Join("thumbnails", "dearrow", videoID+".jpg")
	track := filepath.Join("thumbnails", "previews", videoID, "track.json")
	sprite := filepath.Join("thumbnails", "previews", videoID, "sprite.jpg")
	for key, body := range map[string]string{
		oldVideo: "old video", oldThumb: "old thumb", newVideo: "new video", newThumb: "new thumb",
		dearrow: "dearrow", track: `{}`, sprite: "sprite",
	} {
		path, err := d.storage.Path(key)
		if err != nil {
			t.Fatal(err)
		}
		writeDBTestFile(t, path, []byte(body))
	}

	store := func(assets []Asset) {
		t.Helper()
		if err := d.StoreCompletedVideo(CompletedVideo{
			VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video", Title: "Sample",
			MediaKind: "video", Assets: assets,
		}); err != nil {
			t.Fatalf("StoreCompletedVideo: %v", err)
		}
	}
	store([]Asset{{AssetKind: "video_stream", FilePath: oldVideo}})
	if err := d.StoreVideoThumbnailAsset(videoID, Asset{FilePath: oldThumb, ContentType: "image/jpeg"}, 100); err != nil {
		t.Fatalf("StoreVideoThumbnailAsset: %v", err)
	}
	if err := d.SetDearrowData(videoID, nil, nil, &dearrow, 100); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}
	oldStream, err := d.GetAssetByOwnerIdentity("video_stream", "youtube_video", videoID, 0)
	if err != nil || oldStream == nil {
		t.Fatalf("old stream asset: %+v %v", oldStream, err)
	}
	if err := d.StoreVideoPreviewAssets(videoID, oldStream.Revision, track, sprite, 100); err != nil {
		t.Fatalf("StoreVideoPreviewAssets: %v", err)
	}

	store([]Asset{{AssetKind: "video_stream", FilePath: newVideo}})

	oldVideoPath, _ := d.storage.Path(oldVideo)
	if _, err := os.Stat(oldVideoPath); !os.IsNotExist(err) {
		t.Fatalf("retired primary file still exists: %s", oldVideo)
	}
	thumbnail, err := d.GetAssetByOwnerIdentity("post_thumbnail", "youtube_video", videoID, 0)
	if err != nil || thumbnail == nil || thumbnail.FilePath != oldThumb {
		t.Fatalf("primary replacement changed thumbnail: %+v %v", thumbnail, err)
	}
	oldThumbPath, _ := d.storage.Path(oldThumb)
	if body, err := os.ReadFile(oldThumbPath); err != nil || string(body) != "old thumb" {
		t.Fatalf("primary replacement changed thumbnail bytes: body=%q err=%v", body, err)
	}
	missingThumb := filepath.Join("thumbnails", "videos", "youtube", videoID+"-missing.jpg")
	if err := d.StoreVideoThumbnailAsset(videoID, Asset{FilePath: missingThumb, ContentType: "image/jpeg"}, 101); err == nil {
		t.Fatal("missing thumbnail replacement succeeded")
	}
	thumbnail, err = d.GetAssetByOwnerIdentity("post_thumbnail", "youtube_video", videoID, 0)
	if err != nil || thumbnail == nil || thumbnail.FilePath != oldThumb {
		t.Fatalf("failed thumbnail replacement changed canonical asset: %+v %v", thumbnail, err)
	}
	if err := d.StoreVideoThumbnailAsset(videoID, Asset{FilePath: newThumb, ContentType: "image/jpeg"}, 102); err != nil {
		t.Fatalf("replace thumbnail: %v", err)
	}
	thumbnail, err = d.GetAssetByOwnerIdentity("post_thumbnail", "youtube_video", videoID, 0)
	if err != nil || thumbnail == nil || thumbnail.FilePath != newThumb {
		t.Fatalf("thumbnail replacement = %+v %v", thumbnail, err)
	}
	if _, err := os.Stat(oldThumbPath); !os.IsNotExist(err) {
		t.Fatalf("replaced thumbnail file still exists: %v", err)
	}
	for _, assetKind := range []string{"dearrow_thumbnail"} {
		asset, err := d.GetAssetByOwnerIdentity(assetKind, "youtube_video", videoID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if asset == nil || asset.State != AssetStateReady || asset.FilePath == "" {
			t.Fatalf("derived asset was not preserved: %s %+v", assetKind, asset)
		}
	}
	for _, assetKind := range []string{"preview_track_json", "preview_sprite"} {
		asset, err := d.GetAssetByOwnerIdentity(assetKind, "youtube_video", videoID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if asset != nil {
			t.Fatalf("changed stream retained %s: %+v", assetKind, asset)
		}
	}
	for _, key := range []string{track, sprite} {
		path, _ := d.storage.Path(key)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("changed stream retained preview file %s", key)
		}
	}
	stream, err := d.GetAssetByOwnerIdentity("video_stream", "youtube_video", videoID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stream == nil || stream.FilePath != newVideo || stream.FileMtimeNs <= 0 {
		t.Fatalf("replacement stream mismatch: %+v", stream)
	}
	video, err := d.GetVideo(videoID)
	if err != nil {
		t.Fatal(err)
	}
	if video == nil {
		t.Fatal("completed video metadata is missing")
	}
}

func TestStoreCompletedVideoMissingOutputDoesNotCommitMetadata(t *testing.T) {
	d := openWritableTestDB(t)
	err := d.StoreCompletedVideo(CompletedVideo{
		VideoID: "missing_output", ChannelID: "youtube_sample", OwnerKind: "youtube_video", Title: "Must not commit",
		Assets: []Asset{{
			AssetKind: "video_stream",
			FilePath:  filepath.Join("media", "youtube", "missing.mp4"),
		}},
	})
	if err == nil {
		t.Fatal("StoreCompletedVideo succeeded for a missing output")
	}
	video, getErr := d.GetVideo("missing_output")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if video != nil {
		t.Fatalf("metadata committed before output validation succeeded: %+v", video)
	}
}

func TestStoreCompletedVideoDoesNotRetireCollidingOwnerAssets(t *testing.T) {
	d := openWritableTestDB(t)
	videoID := "shared-id"
	oldYouTube := filepath.Join("media", "youtube", videoID+"-old.mp4")
	newYouTube := filepath.Join("media", "youtube", videoID+"-new.mp4")
	tiktokStream := filepath.Join("media", "tiktok", videoID+".mp4")
	tiktokPreview := filepath.Join("thumbnails", "previews", "tiktok-shared-id", "sprite.jpg")
	for key, body := range map[string]string{
		oldYouTube: "old youtube", newYouTube: "new youtube", tiktokStream: "tiktok stream", tiktokPreview: "sprite",
	} {
		path, _ := d.storage.Path(key)
		writeDBTestFile(t, path, []byte(body))
	}
	storeYouTube := func(key string) {
		t.Helper()
		if err := d.StoreCompletedVideo(CompletedVideo{
			VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video",
			Assets: []Asset{{AssetKind: "video_stream", FilePath: key}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	storeYouTube(oldYouTube)
	for _, asset := range []Asset{
		{AssetID: BuildAssetID("tiktok", "tiktok_video", videoID, "video_stream", 0), AssetKind: "video_stream", OwnerKind: "tiktok_video", OwnerID: videoID, FilePath: tiktokStream},
		{AssetID: BuildAssetID("tiktok", "tiktok_video", videoID, "preview_sprite", 0), AssetKind: "preview_sprite", OwnerKind: "tiktok_video", OwnerID: videoID, FilePath: tiktokPreview},
	} {
		if err := d.StoreReadyAsset(asset, 1); err != nil {
			t.Fatal(err)
		}
	}

	storeYouTube(newYouTube)
	for kind, key := range map[string]string{"video_stream": tiktokStream, "preview_sprite": tiktokPreview} {
		asset, err := d.GetAssetByOwnerIdentity(kind, "tiktok_video", videoID, 0)
		if err != nil || asset == nil || asset.FilePath != key {
			t.Fatalf("colliding %s asset changed: %+v %v", kind, asset, err)
		}
		path, _ := d.storage.Path(key)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("colliding %s file was removed: %v", kind, err)
		}
	}
}

func TestStoreCompletedVideoRejectsCrossOwnerIDCollision(t *testing.T) {
	d := openWritableTestDB(t)
	const videoID = "shared-provider-id"
	youtubeKey := filepath.Join("media", "youtube", videoID+".mp4")
	tiktokKey := filepath.Join("media", "tiktok", videoID+".mp4")
	for key, body := range map[string]string{youtubeKey: "youtube", tiktokKey: "tiktok"} {
		path, err := d.storage.Path(key)
		if err != nil {
			t.Fatal(err)
		}
		writeDBTestFile(t, path, []byte(body))
	}
	if err := d.StoreCompletedVideo(CompletedVideo{
		VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video", MediaKind: "video",
		Assets: []Asset{{AssetKind: "video_stream", FilePath: youtubeKey}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.StoreCompletedVideo(CompletedVideo{
		VideoID: videoID, ChannelID: "tiktok_sample", OwnerKind: "tiktok_video", MediaKind: "video",
		Assets: []Asset{{AssetKind: "video_stream", FilePath: tiktokKey}},
	}); err == nil {
		t.Fatal("cross-owner video id collision was accepted")
	}
	video, err := d.GetVideo(videoID)
	if err != nil || video == nil || video.OwnerKind != "youtube_video" {
		t.Fatalf("original video changed: %+v, err=%v", video, err)
	}
	if asset, err := d.GetAssetByOwnerIdentity("video_stream", "tiktok_video", videoID, 0); err != nil || asset != nil {
		t.Fatalf("colliding asset published: %+v, err=%v", asset, err)
	}
}

func TestVideoReadinessUsesExactOwnerIdentity(t *testing.T) {
	d := openWritableTestDB(t)
	videoID := "shared-readiness-id"
	if err := d.InsertVideo(videoID, "youtube_sample", "youtube_video", "Sample", "", 60, 1, "", "video", 0, false); err != nil {
		t.Fatal(err)
	}

	tiktokKey := filepath.Join("media", "tiktok", videoID+".mp4")
	tiktokPath, _ := d.storage.Path(tiktokKey)
	writeDBTestFile(t, tiktokPath, []byte("tiktok collision"))
	if err := d.StoreReadyAsset(Asset{
		AssetID: BuildAssetID("tiktok", "tiktok_video", videoID, "video_stream", 0), AssetKind: "video_stream",
		OwnerKind: "tiktok_video", OwnerID: videoID, FilePath: tiktokKey,
	}, 1); err != nil {
		t.Fatal(err)
	}

	assertReadiness := func(want bool, wantBytes int64) {
		t.Helper()
		downloaded, err := d.IsVideoDownloaded(videoID)
		if err != nil || downloaded != want {
			t.Fatalf("IsVideoDownloaded = %v, err = %v; want %v", downloaded, err, want)
		}
		count, err := d.GetVideoCount(GetVideosOpts{})
		if err != nil || count != boolInt(want) {
			t.Fatalf("GetVideoCount = %d, err = %v; want %d", count, err, boolInt(want))
		}
		stats, err := d.GetDashboardStats()
		if err != nil || stats["videos_total"] != boolInt(want) {
			t.Fatalf("dashboard videos_total = %v, err = %v; want %d", stats["videos_total"], err, boolInt(want))
		}
		unwatched, totalBytes, err := d.GetVideoStats()
		if err != nil || unwatched != boolInt(want) || totalBytes != wantBytes {
			t.Fatalf("GetVideoStats = (%d, %d), err = %v; want (%d, %d)", unwatched, totalBytes, err, boolInt(want), wantBytes)
		}
	}
	assertReadiness(false, 0)

	youtubeKey := filepath.Join("media", "youtube", videoID+".mp4")
	youtubeBody := []byte("youtube owner")
	youtubePath, _ := d.storage.Path(youtubeKey)
	writeDBTestFile(t, youtubePath, youtubeBody)
	if err := d.StoreReadyAsset(Asset{
		AssetID: BuildAssetID("youtube", "youtube_video", videoID, "video_stream", 0), AssetKind: "video_stream",
		OwnerKind: "youtube_video", OwnerID: videoID, FilePath: youtubeKey,
	}, 2); err != nil {
		t.Fatal(err)
	}
	assertReadiness(true, int64(len(youtubeBody)))
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestDeleteVideoWithFileRemovesOnlyExactOwnedFiles(t *testing.T) {
	d := openWritableTestDB(t)
	videoID := "sample_delete"
	owned := filepath.Join("media", "youtube", videoID+".mp4")
	colliding := filepath.Join("media", "tiktok", videoID+".mp4")
	unrelated := filepath.Join("media", "youtube", videoID+"-other.mp4")
	for key, body := range map[string]string{owned: "owned", colliding: "colliding", unrelated: "unrelated"} {
		path, err := d.storage.Path(key)
		if err != nil {
			t.Fatal(err)
		}
		writeDBTestFile(t, path, []byte(body))
	}
	if err := d.StoreCompletedVideo(CompletedVideo{
		VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video",
		Assets: []Asset{{AssetKind: "video_stream", FilePath: owned}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.StoreReadyAsset(Asset{
		AssetID:   BuildAssetID("tiktok", "tiktok_video", videoID, "video_stream", 0),
		AssetKind: "video_stream", OwnerKind: "tiktok_video", OwnerID: videoID, FilePath: colliding,
	}, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteVideoWithFile(videoID); err != nil {
		t.Fatal(err)
	}
	ownedPath, _ := d.storage.Path(owned)
	if _, err := os.Stat(ownedPath); !os.IsNotExist(err) {
		t.Fatalf("owned file still exists: %v", err)
	}
	unrelatedPath, _ := d.storage.Path(unrelated)
	if _, err := os.Stat(unrelatedPath); err != nil {
		t.Fatalf("same-prefix unrelated file was removed: %v", err)
	}
	collidingAsset, err := d.GetAssetByOwnerIdentity("video_stream", "tiktok_video", videoID, 0)
	if err != nil || collidingAsset == nil || collidingAsset.FilePath != colliding {
		t.Fatalf("colliding owner asset = %+v, err=%v", collidingAsset, err)
	}
	collidingPath, _ := d.storage.Path(colliding)
	if _, err := os.Stat(collidingPath); err != nil {
		t.Fatalf("colliding owner file was removed: %v", err)
	}
	video, err := d.GetVideo(videoID)
	if err != nil {
		t.Fatal(err)
	}
	if video != nil {
		t.Fatalf("video row survived delete: %+v", video)
	}
}

func TestStoreCompletedVideoPreservesSubtitlesUntilExplicitReplacementSucceeds(t *testing.T) {
	d := openWritableTestDB(t)
	videoID := "subtitle_preservation"
	stream1 := filepath.Join("media", "youtube", videoID+"-one.mp4")
	stream2 := filepath.Join("media", "youtube", videoID+"-two.mp4")
	subtitle := filepath.Join("subtitles", "youtube", videoID+".en.vtt")
	for key, body := range map[string]string{
		stream1: "first stream", stream2: "second stream", subtitle: "WEBVTT\n\nold",
	} {
		path, _ := d.storage.Path(key)
		writeDBTestFile(t, path, []byte(body))
	}
	store := func(path string) {
		t.Helper()
		if err := d.StoreCompletedVideo(CompletedVideo{
			VideoID: videoID, ChannelID: "youtube_sample", OwnerKind: "youtube_video",
			Assets: []Asset{{AssetKind: "video_stream", FilePath: path}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	store(stream1)
	if err := d.StoreVideoSubtitleAssets(videoID, []Asset{{AssetKind: "subtitle", FilePath: subtitle}}, 1); err != nil {
		t.Fatal(err)
	}
	store(stream2)

	old, err := d.GetAssetByOwnerIdentity("subtitle", "youtube_video", videoID, 0)
	if err != nil || old == nil {
		t.Fatalf("subtitle disappeared when main download omitted it: %+v %v", old, err)
	}
	missing := filepath.Join("subtitles", "youtube", videoID+"-missing.en.vtt")
	if err := d.StoreVideoSubtitleAssets(videoID, []Asset{{AssetKind: "subtitle", FilePath: missing}}, 2); err == nil {
		t.Fatal("missing subtitle replacement succeeded")
	}
	after, err := d.GetAssetByOwnerIdentity("subtitle", "youtube_video", videoID, 0)
	if err != nil || after == nil || after.FilePath != subtitle || after.FileMtimeNs != old.FileMtimeNs {
		t.Fatalf("failed replacement changed ready subtitle: before=%+v after=%+v err=%v", old, after, err)
	}
	path, _ := d.storage.Path(subtitle)
	if body, err := os.ReadFile(path); err != nil || string(body) != "WEBVTT\n\nold" {
		t.Fatalf("ready subtitle bytes changed: body=%q err=%v", body, err)
	}
}

func TestPendingVideoPreviewsDeriveFromCanonicalStreamRevision(t *testing.T) {
	d := openWritableTestDB(t)
	store := func(videoID, ownerKind, path string, downloadedAt int64) *Asset {
		t.Helper()
		fullPath, err := d.storage.Path(path)
		if err != nil {
			t.Fatal(err)
		}
		writeDBTestFile(t, fullPath, []byte("stream:"+videoID))
		if err := d.StoreCompletedVideo(CompletedVideo{
			VideoID: videoID, ChannelID: "sample_channel", OwnerKind: ownerKind,
			Duration: 30, Assets: []Asset{{AssetKind: "video_stream", FilePath: path}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := d.ExecRaw(`UPDATE videos SET downloaded_at = ? WHERE video_id = ?`, downloadedAt, videoID); err != nil {
			t.Fatal(err)
		}
		asset, err := d.GetAssetByOwnerIdentity("video_stream", ownerKind, videoID, 0)
		if err != nil || asset == nil {
			t.Fatalf("stream %s = %+v, %v", videoID, asset, err)
		}
		return asset
	}

	ready := store("sample_ready", "youtube_video", "media/youtube/sample_ready.mp4", 100)
	store("sample_old", "instagram_reel", "media/instagram/sample_old.mp4", 200)
	store("sample_new", "tiktok_video", "media/tiktok/sample_new.mp4", 300)
	track := "thumbnails/previews/sample_ready/track.json"
	sprite := "thumbnails/previews/sample_ready/sprite.jpg"
	for path, body := range map[string]string{track: `{}`, sprite: "sprite"} {
		fullPath, err := d.storage.Path(path)
		if err != nil {
			t.Fatal(err)
		}
		writeDBTestFile(t, fullPath, []byte(body))
	}
	if err := d.StoreVideoPreviewAssets("sample_ready", ready.Revision, track, sprite, 1); err != nil {
		t.Fatal(err)
	}

	candidates, err := d.ListPendingVideoPreviews(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].VideoID != "sample_new" || candidates[1].VideoID != "sample_old" {
		t.Fatalf("pending previews = %+v", candidates)
	}
	if candidates[0].OwnerKind != "tiktok_video" || candidates[0].InputRevision <= 0 || candidates[0].Duration != 30 {
		t.Fatalf("newest candidate lost canonical input: %+v", candidates[0])
	}
	count, err := d.CountPendingVideoPreviews()
	if err != nil || count != 2 {
		t.Fatalf("pending count = %d, %v", count, err)
	}
	if candidate, err := d.GetPendingVideoPreview("sample_ready"); err != nil || candidate != nil {
		t.Fatalf("ready preview returned as pending: %+v, %v", candidate, err)
	}
}
