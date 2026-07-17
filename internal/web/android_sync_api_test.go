package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/worker"
)

type androidSyncPageResponse struct {
	Changes     []model.AndroidSyncChange `json:"changes"`
	NextCursor  string                    `json:"next_cursor"`
	EndOfStream bool                      `json:"end_of_stream"`
}

const androidSyncTestRetentionQuery = "feed_days=7&youtube_days=7&moments_days=7&story_hours=48"
const androidSyncTestFullYoutubeMetadataQuery = androidSyncTestRetentionQuery + "&full_youtube_metadata=1"

func TestAndroidSyncFlatStreamConvergesChangedAndDeletedOwners(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	seedAndroidSyncContent(t, srv)

	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	for _, kind := range []string{"feed", "video", "channel", "retweet_sources", "asset", "channel_follow"} {
		if findAndroidSyncChange(bootstrap.Changes, kind, "") == nil {
			t.Fatalf("bootstrap kinds missing %q: %+v", kind, androidSyncChangeKeys(bootstrap.Changes))
		}
	}
	assertFlatAndroidSyncPayloads(t, bootstrap.Changes)
	assertAndroidSyncChangesUnique(t, bootstrap.Changes)

	if err := srv.db.ExecRaw(`UPDATE feed_items SET body_text = 'Changed body' WHERE tweet_id = 'sample_tweet'`); err != nil {
		t.Fatal(err)
	}
	page := requestAndroidSyncPage(t, srv, "/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+bootstrap.NextCursor)
	feed := findAndroidSyncChange(page.Changes, "feed", "sample_tweet")
	if feed == nil || feed.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("changed feed missing: %+v", androidSyncChangeKeys(page.Changes))
	}
	var feedPayload androidSyncFeedPayload
	if err := json.Unmarshal(feed.PayloadJSON, &feedPayload); err != nil {
		t.Fatal(err)
	}
	if feedPayload.Item.BodyText != "Changed body" {
		t.Fatalf("changed body = %q", feedPayload.Item.BodyText)
	}
	if findAndroidSyncChange(page.Changes, "video", "sample_video") != nil {
		t.Fatalf("unrelated video rematerialized: %+v", androidSyncChangeKeys(page.Changes))
	}
	assertAndroidSyncChangesUnique(t, page.Changes)

	if err := srv.db.ExecRaw(`DELETE FROM feed_items WHERE tweet_id = 'sample_tweet'`); err != nil {
		t.Fatal(err)
	}
	page = requestAndroidSyncPage(t, srv, "/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+page.NextCursor)
	deleted := findAndroidSyncChange(page.Changes, "feed", "sample_tweet")
	if deleted == nil || deleted.Operation != model.AndroidSyncOperationDelete || len(deleted.PayloadJSON) != 0 {
		t.Fatalf("delete change = %+v in %+v", deleted, androidSyncChangeKeys(page.Changes))
	}
}

func TestAndroidSyncFullYoutubeMetadataIsOptInAndCursorBound(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	old := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform)
		VALUES ('youtube_sample_channel', 'sample_channel', 'Sample Channel', 'youtube');
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('youtube_sample_channel', ?);
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_old_video', 'youtube_sample_channel', 'youtube_video', 'Old Video', ?);
	`, old, old); err != nil {
		t.Fatal(err)
	}
	streamPath := filepath.Join("media", "youtube", "sample_old_video.mp4")
	mustWriteFile(t, filepath.Join(srv.cfg.Storage.StateRoot(), streamPath), []byte("stream"))
	storeReadyWebTestAsset(t, srv, db.Asset{
		AssetID: "sample_old_video_stream", AssetKind: "video_stream",
		OwnerKind: "youtube_video", OwnerID: "sample_old_video",
		FilePath: streamPath, ContentType: "video/mp4",
	})

	legacy := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	legacyCursor, err := decodeAndroidSyncCursor(legacy.NextCursor)
	if err != nil || legacyCursor.Version != androidSyncLegacyModelVersion {
		t.Fatalf("legacy bootstrap cursor = %+v / %v", legacyCursor, err)
	}
	if change := findAndroidSyncChange(legacy.Changes, "video", "sample_old_video"); change != nil && change.Operation == model.AndroidSyncOperationUpsert {
		t.Fatalf("legacy bootstrap transferred old video: %+v", change)
	}
	legacyChanges := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+legacy.NextCursor)
	legacyChangesCursor, err := decodeAndroidSyncCursor(legacyChanges.NextCursor)
	if err != nil || legacyChangesCursor.Version != androidSyncLegacyModelVersion {
		t.Fatalf("legacy changes cursor = %+v / %v", legacyChangesCursor, err)
	}

	reset := requestAndroidSync(t, srv, http.MethodGet,
		"/api/android/sync/changes?"+androidSyncTestFullYoutubeMetadataQuery+"&after="+legacy.NextCursor,
		nil, true)
	if reset.Code != http.StatusConflict || !strings.Contains(reset.Body.String(), "sync_reset_required") {
		t.Fatalf("v1 cursor upgrade response = %d %s", reset.Code, reset.Body.String())
	}

	legacySelection := emptyAndroidSyncDesiredSets()
	srv.storeAndroidSyncSession("sample_legacy_bootstrap", &androidSyncSession{
		Version:       androidSyncLegacyModelVersion,
		Mode:          "bootstrap",
		Epoch:         legacyCursor.Epoch,
		Through:       legacyCursor.Revision,
		RetentionHash: legacyCursor.Retention,
		Selection:     &legacySelection,
		CreatedAt:     time.Now(),
	})
	bootstrapCursor, err := encodeAndroidSyncCursor(androidSyncCursor{
		Version: androidSyncLegacyModelVersion, Mode: "bootstrap", Epoch: legacyCursor.Epoch,
		Session: "sample_legacy_bootstrap", Retention: legacyCursor.Retention,
	})
	if err != nil {
		t.Fatal(err)
	}
	reset = requestAndroidSync(t, srv, http.MethodGet,
		"/api/android/sync/bootstrap?"+androidSyncTestFullYoutubeMetadataQuery+"&after="+bootstrapCursor,
		nil, true)
	if reset.Code != http.StatusConflict || !strings.Contains(reset.Body.String(), "sync_reset_required") {
		t.Fatalf("v1 bootstrap upgrade response = %d %s", reset.Code, reset.Body.String())
	}

	full := requestAndroidSyncPage(t, srv,
		"/api/android/sync/bootstrap?"+androidSyncTestFullYoutubeMetadataQuery)
	fullCursor, err := decodeAndroidSyncCursor(full.NextCursor)
	if err != nil || fullCursor.Version != androidSyncModelVersion {
		t.Fatalf("full bootstrap cursor = %+v / %v", fullCursor, err)
	}
	if change := findAndroidSyncChange(full.Changes, "video", "sample_old_video"); change == nil || change.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("full bootstrap old video = %+v in %+v", change, androidSyncChangeKeys(full.Changes))
	}
	if change := findAndroidSyncChange(full.Changes, "asset", "sample_old_video_stream"); change == nil || change.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("full bootstrap old video descriptor = %+v in %+v", change, androidSyncChangeKeys(full.Changes))
	}
	fullChanges := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+full.NextCursor)
	fullChangesCursor, err := decodeAndroidSyncCursor(fullChanges.NextCursor)
	if err != nil || fullChangesCursor.Version != androidSyncModelVersion {
		t.Fatalf("v2 cursor without flag = %+v / %v", fullChangesCursor, err)
	}
}

func TestAndroidSyncMetadataOnlyYoutubeVideoKeepsStreamDescriptor(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	old := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform)
		VALUES ('youtube_sample_channel', 'sample_channel', 'Sample Channel', 'youtube');
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_old_video', 'youtube_sample_channel', 'youtube_video', 'Old Video', ?);
	`, old); err != nil {
		t.Fatal(err)
	}
	streamPath := filepath.Join("media", "youtube", "sample_old_video.mp4")
	mustWriteFile(t, filepath.Join(srv.cfg.Storage.StateRoot(), streamPath), []byte("stream"))
	stream := storeReadyWebTestAsset(t, srv, db.Asset{
		AssetID: "sample_old_video_stream", AssetKind: "video_stream",
		OwnerKind: "youtube_video", OwnerID: "sample_old_video",
		FilePath: streamPath, ContentType: "video/mp4",
	})
	audioPath := filepath.Join("media", "youtube", "sample_old_video.m4a")
	mustWriteFile(t, filepath.Join(srv.cfg.Storage.StateRoot(), audioPath), []byte("audio"))
	audio := storeReadyWebTestAsset(t, srv, db.Asset{
		AssetID: "sample_old_video_audio", AssetKind: "post_audio",
		OwnerKind: "youtube_video", OwnerID: "sample_old_video",
		FilePath: audioPath, ContentType: "audio/mp4",
	})

	page := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestFullYoutubeMetadataQuery)
	if change := findAndroidSyncChange(page.Changes, "video", "sample_old_video"); change == nil || change.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("metadata-only video change = %+v in %+v", change, androidSyncChangeKeys(page.Changes))
	}
	for _, assetID := range []string{stream.AssetID, audio.AssetID} {
		if change := findAndroidSyncChange(page.Changes, "asset", assetID); change == nil || change.Operation != model.AndroidSyncOperationUpsert {
			t.Fatalf("metadata-only video primary descriptor %q = %+v in %+v", assetID, change, androidSyncChangeKeys(page.Changes))
		}
	}
}

func TestAndroidSyncChannelProfileChangeDoesNotRematerializeChannelVideos(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	seedAndroidSyncContent(t, srv)
	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestFullYoutubeMetadataQuery)

	if err := srv.db.ExecRaw(`
		UPDATE channel_profiles
		SET display_name = 'Updated Channel'
		WHERE channel_id = 'youtube_sample_channel'
	`); err != nil {
		t.Fatal(err)
	}

	page := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestFullYoutubeMetadataQuery+"&after="+bootstrap.NextCursor)
	if change := findAndroidSyncChange(page.Changes, "channel", "youtube_sample_channel"); change == nil || change.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("channel profile update = %+v in %+v", change, androidSyncChangeKeys(page.Changes))
	}
	if change := findAndroidSyncChange(page.Changes, "video", "sample_video"); change != nil {
		t.Fatalf("channel profile update rematerialized video: %+v", androidSyncChangeKeys(page.Changes))
	}
}

func TestAndroidSyncChangesKeepReadyYoutubeVideoWhenDesireIsRemoved(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform)
		VALUES ('youtube_sample_channel', 'sample_channel', 'Sample Channel', 'youtube');
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES ('youtube_sample_channel', ?);
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_video', 'youtube_sample_channel', 'youtube_video', 'Sample Video', ?);
		INSERT INTO video_desires (source_channel_id, source_component, video_id, source_position, lane)
		VALUES ('youtube_sample_channel', 'uploads', 'sample_video', 1, 'current');
	`, now, now); err != nil {
		t.Fatal(err)
	}
	streamPath := filepath.Join("media", "youtube", "sample_video.mp4")
	mustWriteFile(t, filepath.Join(srv.cfg.Storage.StateRoot(), streamPath), []byte("stream"))
	storeReadyWebTestAsset(t, srv, db.Asset{
		AssetID: "sample_video_stream", AssetKind: "video_stream",
		OwnerKind: "youtube_video", OwnerID: "sample_video",
		FilePath: streamPath, ContentType: "video/mp4",
	})

	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestFullYoutubeMetadataQuery)
	if change := findAndroidSyncChange(bootstrap.Changes, "video", "sample_video"); change == nil || change.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("bootstrap video = %+v in %+v", change, androidSyncChangeKeys(bootstrap.Changes))
	}
	if err := srv.db.ExecRaw(`
		DELETE FROM video_desires
		WHERE source_channel_id = 'youtube_sample_channel'
		  AND source_component = 'uploads'
		  AND video_id = 'sample_video'
	`); err != nil {
		t.Fatal(err)
	}

	page := requestAndroidSyncPage(t, srv, "/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+bootstrap.NextCursor)
	if change := findAndroidSyncChange(page.Changes, "video", "sample_video"); change != nil {
		t.Fatalf("removed desire changed ready web-library video = %+v in %+v", change, androidSyncChangeKeys(page.Changes))
	}
}

func TestAndroidSyncChangesAddVideoWhenPrimaryAssetBecomesReady(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform)
		VALUES ('youtube_sample_channel', 'sample_channel', 'Sample Channel', 'youtube');
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_new_video', 'youtube_sample_channel', 'youtube_video', 'New Video', ?);
	`, now); err != nil {
		t.Fatal(err)
	}

	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestFullYoutubeMetadataQuery)
	if change := findAndroidSyncChange(bootstrap.Changes, "video", "sample_new_video"); change != nil {
		t.Fatalf("unready video entered bootstrap: %+v", change)
	}

	streamPath := filepath.Join("media", "youtube", "sample_new_video.mp4")
	mustWriteFile(t, filepath.Join(srv.cfg.Storage.StateRoot(), streamPath), []byte("stream"))
	storeReadyWebTestAsset(t, srv, db.Asset{
		AssetID: "sample_new_video_stream", AssetKind: "video_stream",
		OwnerKind: "youtube_video", OwnerID: "sample_new_video",
		FilePath: streamPath, ContentType: "video/mp4",
	})

	page := requestAndroidSyncPage(t, srv, "/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+bootstrap.NextCursor)
	if change := findAndroidSyncChange(page.Changes, "video", "sample_new_video"); change == nil || change.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("ready video change = %+v in %+v", change, androidSyncChangeKeys(page.Changes))
	}
}

func TestAndroidSyncTemporaryVideoPayloadMarksItTemporary(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	old := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at, is_temp)
		VALUES ('sample_temporary_video', 'youtube_sample_channel', 'youtube_video', 'Temporary Video', ?, 1);
	`, old); err != nil {
		t.Fatal(err)
	}

	page := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?feed_days=0&youtube_days=0&moments_days=0&story_hours=0&full_youtube_metadata=1")
	change := findAndroidSyncChange(page.Changes, "video", "sample_temporary_video")
	if change == nil || change.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("temporary video change = %+v in %+v", change, androidSyncChangeKeys(page.Changes))
	}
	var payload androidSyncVideoPayload
	if err := json.Unmarshal(change.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Item.IsTemp {
		t.Fatalf("temporary video payload = %+v, want is_temp=true", payload.Item)
	}
}

func TestAndroidSyncRetweetHeadRehydratesSameHashFeedOwners(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	seedAndroidSyncContent(t, srv)
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, channel_id, body_text, content_hash, published_at, fetched_at)
		VALUES ('sample_peer', 'twitter_sample_author', 'Peer', 'sample_hash', ?, ?)
	`, time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	if err := srv.db.ExecRaw(`UPDATE retweet_sources SET published_at = published_at + 1 WHERE content_hash = 'sample_hash'`); err != nil {
		t.Fatal(err)
	}
	page := requestAndroidSyncPage(t, srv, "/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+bootstrap.NextCursor)
	for _, id := range []string{"sample_tweet", "sample_peer"} {
		if findAndroidSyncChange(page.Changes, "feed", id) == nil {
			t.Fatalf("same-hash owner %q not rehydrated: %+v", id, androidSyncChangeKeys(page.Changes))
		}
	}
	assertAndroidSyncChangesUnique(t, page.Changes)
}

func TestAndroidSyncChangesApplyCanonicalSelectionInBothDirections(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_settings (channel_id, include_reposts, updated_at)
		VALUES ('twitter_sample_source', 0, ?);
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, is_retweet,
			content_hash, published_at, fetched_at
		) VALUES (
			'sample_repost', 'twitter_sample_source', 'twitter_sample_author', 1,
			'sample_hash', ?, ?
		);
		INSERT INTO retweet_sources (
			content_hash, retweeter_channel_id, tweet_id, published_at
		) VALUES ('sample_hash', 'twitter_sample_reposter', 'sample_repost', ?);
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_video', 'youtube_sample_channel', 'youtube_video', 'Sample', ?);
	`, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}

	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	if change := findAndroidSyncChange(bootstrap.Changes, "feed", "sample_repost"); change == nil || change.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("allowed retweet missing from bootstrap: %+v", androidSyncChangeKeys(bootstrap.Changes))
	}
	if change := findAndroidSyncChange(bootstrap.Changes, "video", "sample_video"); change != nil {
		t.Fatalf("unfollowed video entered bootstrap: %+v", change)
	}

	if err := srv.db.ExecRaw(`
		DELETE FROM retweet_sources WHERE content_hash = 'sample_hash';
		UPDATE videos SET title = 'Changed' WHERE video_id = 'sample_video';
	`); err != nil {
		t.Fatal(err)
	}
	page := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+bootstrap.NextCursor)
	for _, key := range [][2]string{{"feed", "sample_repost"}, {"video", "sample_video"}} {
		change := findAndroidSyncChange(page.Changes, key[0], key[1])
		if change == nil || change.Operation != model.AndroidSyncOperationDelete {
			t.Fatalf("excluded content %v was not tombstoned: %+v", key, androidSyncChangeKeys(page.Changes))
		}
	}
	if group := findAndroidSyncChange(page.Changes, "retweet_sources", "sample_hash"); group == nil || group.Operation != model.AndroidSyncOperationDelete {
		t.Fatalf("empty retweet group was not tombstoned: %+v", androidSyncChangeKeys(page.Changes))
	}

	if err := srv.db.ExecRaw(`INSERT INTO feed_likes (tweet_id, liked_at) VALUES ('sample_repost', ?)`, now+1); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.AddBookmark("sample_video", 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	page = requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+page.NextCursor)
	for _, key := range [][2]string{{"feed", "sample_repost"}, {"video", "sample_video"}} {
		change := findAndroidSyncChange(page.Changes, key[0], key[1])
		if change == nil || change.Operation != model.AndroidSyncOperationUpsert {
			t.Fatalf("protected content %v was not admitted: %+v", key, androidSyncChangeKeys(page.Changes))
		}
	}

	if err := srv.db.ExecRaw(`
		DELETE FROM feed_likes WHERE tweet_id = 'sample_repost';
		DELETE FROM bookmarks WHERE video_id = 'sample_video';
	`); err != nil {
		t.Fatal(err)
	}
	page = requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+page.NextCursor)
	for _, key := range [][2]string{{"feed", "sample_repost"}, {"video", "sample_video"}} {
		change := findAndroidSyncChange(page.Changes, key[0], key[1])
		if change == nil || change.Operation != model.AndroidSyncOperationDelete {
			t.Fatalf("unprotected content %v was not tombstoned: %+v", key, androidSyncChangeKeys(page.Changes))
		}
	}
}

func TestAndroidSyncChangesRemoveDetachedFeedDependencies(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	now := time.Now().UnixMilli()
	old := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	if err := srv.db.ExecRaw(`
		WITH timing(recent_ms, old_ms) AS (VALUES (?, ?))
		INSERT INTO feed_items (
			tweet_id, content_hash, quote_tweet_id, reply_to_status, published_at, fetched_at
		)
		SELECT 'sample_hash_update_root', 'sample_hash_update', '', '', recent_ms, recent_ms FROM timing
		UNION ALL SELECT 'sample_hash_update_peer', 'sample_hash_update', '', '', old_ms, old_ms FROM timing
		UNION ALL SELECT 'sample_hash_update_peer_extra', 'sample_hash_update', '', '', old_ms, old_ms FROM timing
		UNION ALL SELECT 'sample_hash_delete_root', 'sample_hash_delete', '', '', recent_ms, recent_ms FROM timing
		UNION ALL SELECT 'sample_hash_delete_peer', 'sample_hash_delete', '', '', old_ms, old_ms FROM timing
		UNION ALL SELECT 'sample_hash_delete_peer_extra', 'sample_hash_delete', '', '', old_ms, old_ms FROM timing
		UNION ALL SELECT 'sample_quote_update_root', '', 'sample_quote_update_target', '', recent_ms, recent_ms FROM timing
		UNION ALL SELECT 'sample_quote_update_target', '', '', '', old_ms, old_ms FROM timing
		UNION ALL SELECT 'sample_quote_delete_root', '', 'sample_quote_delete_target', '', recent_ms, recent_ms FROM timing
		UNION ALL SELECT 'sample_quote_delete_target', '', '', '', old_ms, old_ms FROM timing
		UNION ALL SELECT 'sample_reply_update_root', '', '', 'sample_reply_update_parent', recent_ms, recent_ms FROM timing
		UNION ALL SELECT 'sample_reply_update_parent', '', '', '', old_ms, old_ms FROM timing
		UNION ALL SELECT 'sample_reply_delete_root', '', '', 'sample_reply_delete_parent', recent_ms, recent_ms FROM timing
		UNION ALL SELECT 'sample_reply_delete_parent', '', '', '', old_ms, old_ms FROM timing
	`, now, old); err != nil {
		t.Fatal(err)
	}

	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	oldDependencies := []string{
		"sample_hash_update_peer", "sample_hash_update_peer_extra",
		"sample_hash_delete_peer", "sample_hash_delete_peer_extra",
		"sample_quote_update_target", "sample_quote_delete_target",
		"sample_reply_update_parent", "sample_reply_delete_parent",
	}
	for _, id := range oldDependencies {
		change := findAndroidSyncChange(bootstrap.Changes, "feed", id)
		if change == nil || change.Operation != model.AndroidSyncOperationUpsert {
			t.Fatalf("bootstrap dependency %q missing: %+v", id, androidSyncChangeKeys(bootstrap.Changes))
		}
	}

	if err := srv.db.ExecRaw(`
		UPDATE feed_items SET content_hash = 'sample_hash_update_new'
		WHERE tweet_id = 'sample_hash_update_root';
		DELETE FROM feed_items WHERE tweet_id = 'sample_hash_delete_root';
		UPDATE feed_items SET quote_tweet_id = '' WHERE tweet_id = 'sample_quote_update_root';
		DELETE FROM feed_items WHERE tweet_id = 'sample_quote_delete_root';
		UPDATE feed_items SET reply_to_status = '' WHERE tweet_id = 'sample_reply_update_root';
		DELETE FROM feed_items WHERE tweet_id = 'sample_reply_delete_root';
	`); err != nil {
		t.Fatal(err)
	}
	page := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+bootstrap.NextCursor)
	for _, id := range oldDependencies {
		change := findAndroidSyncChange(page.Changes, "feed", id)
		if change == nil || change.Operation != model.AndroidSyncOperationDelete {
			t.Fatalf("detached dependency %q was not deleted: %+v", id, androidSyncChangeKeys(page.Changes))
		}
	}
}

func TestAndroidSyncChangesApplyCanonicalFeedRankCap(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	const rows = androidSyncFeedRankMaxRows + 1
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (channel_id, followed_at) VALUES ('twitter_sample_source', 1);
		WITH RECURSIVE seq(n) AS (
			VALUES (1)
			UNION ALL
			SELECT n + 1 FROM seq WHERE n < ?
		)
		INSERT INTO feed_items (tweet_id, source_channel_id, channel_id, published_at, fetched_at)
		SELECT printf('sample_rank_%04d', n), 'twitter_sample_source', 'twitter_sample_source', ?, ? FROM seq;
		WITH RECURSIVE seq(n) AS (
			VALUES (1)
			UNION ALL
			SELECT n + 1 FROM seq WHERE n < ?
		)
		INSERT INTO feed_rank_snapshot (
			tweet_id, rank_position, base_score, decay_factor, freshness_bonus,
			jitter, diversity_demoted_by, final_score, computed_at
		)
		SELECT printf('sample_rank_%04d', n), n, 0, 0, 0, 0, 0, 0, 1 FROM seq
	`, rows, now, now, rows); err != nil {
		t.Fatal(err)
	}
	desired := emptyAndroidSyncDesiredSets()
	insideID := fmt.Sprintf("sample_rank_%04d", androidSyncFeedRankMaxRows)
	beyondID := fmt.Sprintf("sample_rank_%04d", rows)
	feedRanks, err := srv.db.ListAndroidSyncDesiredFeedRanksAmong(
		7, now, []string{insideID, beyondID}, androidSyncFeedRankMaxRows,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired.FeedRanks = feedRanks
	changes, err := srv.materializeAndroidSyncHeads(srv.db, []model.AndroidSyncHead{
		{OwnerKind: "feed_rank", OwnerID: insideID},
		{OwnerKind: "feed_rank", OwnerID: beyondID},
	}, &desired)
	if err != nil {
		t.Fatal(err)
	}
	inside := findAndroidSyncChange(changes, "feed_rank", insideID)
	if inside == nil || inside.Operation != model.AndroidSyncOperationUpsert {
		t.Fatalf("rank inside cap = %+v in %+v", inside, androidSyncChangeKeys(changes))
	}
	beyond := findAndroidSyncChange(changes, "feed_rank", beyondID)
	if beyond == nil || beyond.Operation != model.AndroidSyncOperationDelete {
		t.Fatalf("rank beyond cap = %+v in %+v", beyond, androidSyncChangeKeys(changes))
	}
}

func TestAndroidSyncZeroRetentionBootstrapsOnlyProtectedContent(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	old := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, platform)
		VALUES ('youtube_sample', 'sample', 'Sample', 'youtube'), ('twitter_sample', 'sample', 'Sample', 'twitter');
		INSERT INTO channel_follows (channel_id, followed_at) VALUES ('youtube_sample', 1);
		INSERT INTO feed_items (tweet_id, channel_id, content_hash, published_at, fetched_at) VALUES
			('feed_protected', 'twitter_sample', 'hash_protected', ?, ?),
			('feed_unprotected', 'twitter_sample', 'hash_unprotected', ?, ?);
		INSERT INTO videos (video_id, channel_id, owner_kind, published_at) VALUES
			('video_protected', 'youtube_sample', 'youtube_video', ?),
			('video_unprotected', 'youtube_sample', 'youtube_video', ?);
	`, old, old, old, old, old, old); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.AddBookmark("feed_protected", 0, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.AddBookmark("video_protected", 0, "", "", ""); err != nil {
		t.Fatal(err)
	}

	page := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?feed_days=0&youtube_days=0&moments_days=0&story_hours=0")
	for _, key := range [][2]string{{"feed", "feed_protected"}, {"video", "video_protected"}} {
		if findAndroidSyncChange(page.Changes, key[0], key[1]) == nil {
			t.Fatalf("protected owner missing: %v in %+v", key, androidSyncChangeKeys(page.Changes))
		}
	}
	for _, key := range [][2]string{{"feed", "feed_unprotected"}, {"video", "video_unprotected"}} {
		if findAndroidSyncChange(page.Changes, key[0], key[1]) != nil {
			t.Fatalf("disabled bucket admitted owner: %v in %+v", key, androidSyncChangeKeys(page.Changes))
		}
	}
}

func TestAndroidSyncChangesRejectRetentionAndRevisionMismatch(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	seedAndroidSyncContent(t, srv)
	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)

	rec := requestAndroidSync(t, srv, http.MethodGet,
		"/api/android/sync/changes?feed_days=14&youtube_days=7&moments_days=7&story_hours=48&after="+bootstrap.NextCursor, nil, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("retention mismatch status = %d body=%s", rec.Code, rec.Body.String())
	}

	cursor, err := decodeAndroidSyncCursor(bootstrap.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	cursor.Revision += 100
	ahead, err := encodeAndroidSyncCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	rec = requestAndroidSync(t, srv, http.MethodGet,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+ahead, nil, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("ahead cursor status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAndroidSyncBootstrapFinalPageCanBeRetried(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	if err := srv.db.ExecRaw(`
		WITH RECURSIVE seq(n) AS (
			VALUES (1)
			UNION ALL
			SELECT n + 1 FROM seq WHERE n < 1001
		)
		INSERT INTO feed_seen (tweet_id, seen_at)
		SELECT printf('sample_post_%04d', n), n FROM seq
	`); err != nil {
		t.Fatal(err)
	}

	first := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	if first.EndOfStream {
		t.Fatalf("first bootstrap page unexpectedly ended with %d changes", len(first.Changes))
	}
	cursor := first.NextCursor
	var finalPath string
	var final androidSyncPageResponse
	for page := 0; page < 10; page++ {
		path := "/api/android/sync/bootstrap?" + androidSyncTestRetentionQuery + "&after=" + cursor
		response := requestAndroidSyncPage(t, srv, path)
		if response.EndOfStream {
			finalPath = path
			final = response
			break
		}
		cursor = response.NextCursor
	}
	if finalPath == "" {
		t.Fatal("bootstrap did not finish within the bounded page count")
	}
	retry := requestAndroidSyncPage(t, srv, finalPath)
	if !retry.EndOfStream || retry.NextCursor != final.NextCursor ||
		!reflect.DeepEqual(androidSyncChangeKeys(retry.Changes), androidSyncChangeKeys(final.Changes)) {
		t.Fatalf("final page retry changed: first=%+v retry=%+v", final, retry)
	}

	changes := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+final.NextCursor)
	if !changes.EndOfStream || len(changes.Changes) != 0 {
		t.Fatalf("final bootstrap cursor did not converge: %+v", changes)
	}
}

func TestAndroidSyncChangesSessionBoundsPagesWithoutRetainingWholeSelection(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_settings (channel_id, include_reposts, updated_at)
		VALUES ('twitter_sample_source', 0, 1)
	`); err != nil {
		t.Fatal(err)
	}
	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestFullYoutubeMetadataQuery)
	nowMs := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		WITH RECURSIVE seq(n) AS (
			VALUES (1)
			UNION ALL
			SELECT n + 1 FROM seq WHERE n < 500
		)
		INSERT INTO feed_seen (tweet_id, seen_at)
		SELECT printf('sample_seen_%04d', n), n FROM seq;
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, is_retweet,
			content_hash, published_at, fetched_at
		) VALUES (
			'sample_session_feed', 'twitter_sample_source', 'twitter_sample_author', 1,
			'sample_session_hash', ?, ?
		);
		INSERT INTO retweet_sources (content_hash, retweeter_channel_id, tweet_id, published_at)
		VALUES ('sample_session_hash', 'twitter_sample_reposter', 'sample_session_feed', ?)
	`, nowMs, nowMs, nowMs); err != nil {
		t.Fatal(err)
	}

	firstPath := "/api/android/sync/changes?" + androidSyncTestFullYoutubeMetadataQuery + "&after=" + bootstrap.NextCursor
	first := requestAndroidSyncPage(t, srv, firstPath)
	if first.EndOfStream {
		t.Fatalf("first changes page unexpectedly ended: %+v", androidSyncChangeKeys(first.Changes))
	}
	firstCursor, err := decodeAndroidSyncCursor(first.NextCursor)
	if err != nil || firstCursor.Session == "" {
		t.Fatalf("changes session cursor = %+v / %v", firstCursor, err)
	}
	srv.androidSyncSessionMu.Lock()
	selection := srv.androidSyncSessions[firstCursor.Session].Selection
	srv.androidSyncSessionMu.Unlock()
	if selection != nil {
		t.Fatal("changes session retained an unbounded desired selection")
	}
	if err := srv.db.ExecRaw(`
		DELETE FROM retweet_sources WHERE content_hash = 'sample_session_hash';
		INSERT INTO feed_seen (tweet_id, seen_at) VALUES ('sample_after_through', ?)
	`, nowMs+1); err != nil {
		t.Fatal(err)
	}

	finalPath := "/api/android/sync/changes?" + androidSyncTestFullYoutubeMetadataQuery + "&after=" + first.NextCursor
	final := requestAndroidSyncPage(t, srv, finalPath)
	if !final.EndOfStream {
		t.Fatalf("bounded changes session did not end: %+v", androidSyncChangeKeys(final.Changes))
	}
	feed := findAndroidSyncChange(final.Changes, "feed", "sample_session_feed")
	if feed == nil || feed.Operation != model.AndroidSyncOperationDelete {
		t.Fatalf("post-through content state was not applied: %+v", androidSyncChangeKeys(final.Changes))
	}
	if findAndroidSyncChange(final.Changes, "feed_seen", "sample_after_through") != nil {
		t.Fatalf("post-through head leaked into session: %+v", androidSyncChangeKeys(final.Changes))
	}
	retry := requestAndroidSyncPage(t, srv, finalPath)
	if !retry.EndOfStream || retry.NextCursor != final.NextCursor ||
		!reflect.DeepEqual(androidSyncChangeKeys(retry.Changes), androidSyncChangeKeys(final.Changes)) {
		t.Fatalf("final changes page retry changed: first=%+v retry=%+v", final, retry)
	}

	next := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestFullYoutubeMetadataQuery+"&after="+final.NextCursor)
	if findAndroidSyncChange(next.Changes, "feed_seen", "sample_after_through") == nil {
		t.Fatalf("post-through head did not enter next stream: %+v", androidSyncChangeKeys(next.Changes))
	}
	removed := findAndroidSyncChange(next.Changes, "feed", "sample_session_feed")
	if removed == nil || removed.Operation != model.AndroidSyncOperationDelete {
		t.Fatalf("next stream did not apply changed selection: %+v", androidSyncChangeKeys(next.Changes))
	}
}

func TestAndroidSyncChangesSessionResumesAfterCacheLoss(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	if err := srv.db.ExecRaw(`
		WITH RECURSIVE seq(n) AS (
			VALUES (1)
			UNION ALL
			SELECT n + 1 FROM seq WHERE n < 501
		)
		INSERT INTO feed_seen (tweet_id, seen_at)
		SELECT printf('sample_resume_seen_%04d', n), n FROM seq
	`); err != nil {
		t.Fatal(err)
	}

	first := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+bootstrap.NextCursor)
	if first.EndOfStream {
		t.Fatalf("first changes page unexpectedly ended: %+v", androidSyncChangeKeys(first.Changes))
	}
	srv.androidSyncSessionMu.Lock()
	srv.androidSyncSessions = nil
	srv.androidSyncSessionMu.Unlock()

	resumed := requestAndroidSyncPage(t, srv,
		"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+first.NextCursor)
	if !resumed.EndOfStream || findAndroidSyncChange(resumed.Changes, "feed_seen", "sample_resume_seen_0501") == nil {
		t.Fatalf("changes session did not resume from the committed revision: %+v", androidSyncChangeKeys(resumed.Changes))
	}
}

func TestAndroidSyncAssetFileIsAuthenticatedAndRevisionBound(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	asset := seedAndroidSyncContent(t, srv)
	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	change := findAndroidSyncChange(bootstrap.Changes, "asset", asset.AssetID)
	if change == nil {
		t.Fatalf("asset descriptor missing: %+v", androidSyncChangeKeys(bootstrap.Changes))
	}
	var descriptor model.AndroidSyncAsset
	if err := json.Unmarshal(change.PayloadJSON, &descriptor); err != nil {
		t.Fatal(err)
	}
	path := "/api/android/sync/assets/" + asset.AssetID + "/file?revision=" + jsonNumber(descriptor.Revision)
	if rec := requestAndroidSync(t, srv, http.MethodGet, path, nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated asset status = %d", rec.Code)
	}
	rec := requestAndroidSync(t, srv, http.MethodGet, path, nil, true)
	if rec.Code != http.StatusOK || rec.Body.String() != "ready-image" {
		t.Fatalf("asset response = %d %q", rec.Code, rec.Body.String())
	}
	stalePath := "/api/android/sync/assets/" + asset.AssetID + "/file?revision=" + jsonNumber(descriptor.Revision+1)
	if rec := requestAndroidSync(t, srv, http.MethodGet, stalePath, nil, true); rec.Code != http.StatusConflict {
		t.Fatalf("stale descriptor status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAndroidSyncAssetFileWithdrawsUnavailableReadyDescriptor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mismatched",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				mustWriteFile(t, path, []byte("changed-and-longer"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAndroidSyncTestServer(t)
			asset := seedAndroidSyncContent(t, srv)
			bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
			change := findAndroidSyncChange(bootstrap.Changes, "asset", asset.AssetID)
			if change == nil {
				t.Fatalf("asset descriptor missing: %+v", androidSyncChangeKeys(bootstrap.Changes))
			}
			var descriptor model.AndroidSyncAsset
			if err := json.Unmarshal(change.PayloadJSON, &descriptor); err != nil {
				t.Fatal(err)
			}
			var headBefore int64
			if err := srv.db.QueryRow(`
				SELECT revision FROM android_sync_heads
				WHERE owner_kind = 'asset' AND owner_id = ?
			`, asset.AssetID).Scan(&headBefore); err != nil {
				t.Fatal(err)
			}

			tc.mutate(t, filepath.Join(srv.cfg.Storage.StateRoot(), asset.FilePath))
			path := "/api/android/sync/assets/" + asset.AssetID + "/file?revision=" + jsonNumber(descriptor.Revision)
			rec := requestAndroidSync(t, srv, http.MethodGet, path, nil, true)
			if rec.Code != http.StatusConflict {
				t.Fatalf("unavailable bytes status = %d body=%s", rec.Code, rec.Body.String())
			}

			current, err := srv.db.GetAndroidSyncAssetByID(asset.AssetID)
			if err != nil || current == nil {
				t.Fatalf("asset after unavailable bytes = %+v / %v", current, err)
			}
			if current.State != db.AssetStateServerMissing || current.Revision <= descriptor.Revision {
				t.Fatalf("asset was not withdrawn: before=%+v after=%+v", descriptor, current)
			}
			var headAfter int64
			if err := srv.db.QueryRow(`
				SELECT revision FROM android_sync_heads
				WHERE owner_kind = 'asset' AND owner_id = ?
			`, asset.AssetID).Scan(&headAfter); err != nil {
				t.Fatal(err)
			}
			if headAfter <= headBefore {
				t.Fatalf("asset head did not advance: %d -> %d", headBefore, headAfter)
			}

			changes := requestAndroidSyncPage(t, srv,
				"/api/android/sync/changes?"+androidSyncTestRetentionQuery+"&after="+bootstrap.NextCursor)
			updated := findAndroidSyncChange(changes.Changes, "asset", asset.AssetID)
			if updated == nil || updated.Operation != model.AndroidSyncOperationUpsert {
				t.Fatalf("withdrawn asset change missing: %+v", androidSyncChangeKeys(changes.Changes))
			}
			var updatedDescriptor model.AndroidSyncAsset
			if err := json.Unmarshal(updated.PayloadJSON, &updatedDescriptor); err != nil {
				t.Fatal(err)
			}
			if updatedDescriptor.State != db.AssetStateServerMissing || updatedDescriptor.Revision != current.Revision {
				t.Fatalf("withdrawn descriptor = %+v, asset = %+v", updatedDescriptor, current)
			}
		})
	}
}

func TestAndroidSyncReadyAssetPreservesStoredMetadata(t *testing.T) {
	stored := db.Asset{
		AssetID:     "asset_sample",
		AssetKind:   "post_media",
		OwnerID:     "post_sample",
		OwnerKind:   "tweet",
		ContentType: "application/octet-stream",
		SizeBytes:   128,
		Revision:    1,
		State:       db.AssetStateReady,
	}

	got := (&Server{}).androidSyncAssetFromInventory(stored)
	if got.State != "ready" || got.ContentType != stored.ContentType ||
		got.SizeBytes != stored.SizeBytes || got.Revision != stored.Revision {
		t.Fatalf("ready metadata changed: got=%+v stored=%+v", got, stored)
	}
}

func TestAndroidSyncHealthValidatesAuthenticatedCursor(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	seedAndroidSyncContent(t, srv)
	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	body := map[string]any{
		"cursor": bootstrap.NextCursor, "reported_at_ms": int64(1000),
		"retention": map[string]int{"feed_days": 7, "youtube_days": 7, "moments_days": 7, "story_hours": 48},
		"counts":    map[string]int{"total": 3, "verified": 1, "pending": 1, "missing": 1},
		"bytes":     map[string]int64{"verified": 128},
	}
	if rec := requestAndroidSync(t, srv, http.MethodPost, "/api/android/sync/health", body, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated health status = %d", rec.Code)
	}
	rec := requestAndroidSync(t, srv, http.MethodPost, "/api/android/sync/health", body, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", rec.Code, rec.Body.String())
	}
	report, err := srv.db.GetLatestAndroidSyncHealthReport()
	if err != nil || report == nil || report.Cursor != bootstrap.NextCursor || report.VerifiedAssets != 1 || report.VerifiedBytes != 128 {
		t.Fatalf("health report = %+v / %v", report, err)
	}
}

func TestAndroidSyncHealthDoesNotOwnFeedRetention(t *testing.T) {
	srv := newAndroidSyncTestServer(t)
	bootstrap := requestAndroidSyncPage(t, srv, "/api/android/sync/bootstrap?"+androidSyncTestRetentionQuery)
	applied, err := srv.db.GetAndroidFeedRetention()
	if err != nil || applied == nil || applied.FeedDays != 7 {
		t.Fatalf("bootstrap retention = %+v / %v", applied, err)
	}
	if err := srv.db.RecordAndroidFeedRetention(1, 1234); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"cursor": bootstrap.NextCursor, "reported_at_ms": int64(2000),
		"retention": map[string]int{"feed_days": 7, "youtube_days": 7, "moments_days": 7, "story_hours": 48},
		"counts":    map[string]int{"total": 1, "verified": 0, "pending": 1, "missing": 0},
		"bytes":     map[string]int64{"verified": 0},
	}
	if rec := requestAndroidSync(t, srv, http.MethodPost, "/api/android/sync/health", body, true); rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := srv.db.GetAndroidFeedRetention()
	if err != nil || stored == nil || stored.FeedDays != 1 || stored.ReconciledAtMs != 1234 {
		t.Fatalf("health changed retention owner: %+v / %v", stored, err)
	}
	if err := srv.db.RecordAndroidSyncHealth("sample_diagnostic", 3000,
		[]byte(`{"retention":{"feed_days":3,"youtube_days":3,"moments_days":3,"story_hours":24}}`),
		0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if fallback := srv.androidSyncRetentionFallback(); fallback.FeedDays != 7 {
		t.Fatalf("health changed request fallback: %+v", fallback)
	}
}

func TestAndroidSyncRetentionSettingsPreserveOff(t *testing.T) {
	fallback := db.AndroidRetentionSettings{FeedDays: 7, YoutubeDays: 7, MomentsDays: 7, StoryHours: 48}
	req := httptest.NewRequest("GET", "/?feed_days=0&youtube_days=0&moments_days=0&story_hours=0", nil)
	got, err := androidSyncRetentionSettingsFromRequest(req, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if got != (db.AndroidRetentionSettings{}) {
		t.Fatalf("off retention = %+v", got)
	}
}

func assertFlatAndroidSyncPayloads(t *testing.T, changes []model.AndroidSyncChange) {
	t.Helper()
	for _, kind := range []string{"feed", "video", "channel"} {
		change := findAndroidSyncChange(changes, kind, "")
		if change == nil {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(change.PayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		for _, nested := range []string{"channels", "assets", "retweet_sources"} {
			if _, ok := payload[nested]; ok {
				t.Fatalf("%s payload nests %s: %#v", kind, nested, payload)
			}
		}
	}
}

func seedAndroidSyncContent(t *testing.T, srv *testServer) db.Asset {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, handle, display_name) VALUES
			('twitter_sample_author', 'twitter', 'sample_author', 'Sample Author'),
			('twitter_sample_reposter', 'twitter', 'sample_reposter', 'Sample Reposter'),
			('youtube_sample_channel', 'youtube', 'sample_channel', 'Sample Channel');
		INSERT INTO channels (channel_id, source_id, name, platform) VALUES
			('twitter_sample_author', 'sample_author', 'Sample Author', 'twitter'),
			('twitter_sample_reposter', 'sample_reposter', 'Sample Reposter', 'twitter'),
			('youtube_sample_channel', 'sample_channel', 'Sample Channel', 'youtube');
		INSERT INTO channel_follows (channel_id, followed_at) VALUES ('youtube_sample_channel', ?);
		INSERT INTO feed_items (
			tweet_id, source_channel_id, channel_id, body_text, content_hash, published_at, fetched_at
		) VALUES ('sample_tweet', 'twitter_sample_author', 'twitter_sample_author', 'Sample body', 'sample_hash', ?, ?);
		INSERT INTO retweet_sources (content_hash, retweeter_channel_id, tweet_id, published_at)
		VALUES ('sample_hash', 'twitter_sample_reposter', 'sample_tweet', ?);
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_video', 'youtube_sample_channel', 'youtube_video', 'Sample Video', ?);
	`, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	streamPath := filepath.Join("media", "youtube", "sample_video.mp4")
	mustWriteFile(t, filepath.Join(srv.cfg.Storage.StateRoot(), streamPath), []byte("stream"))
	storeReadyWebTestAsset(t, srv, db.Asset{
		AssetID: "sample_video_stream", AssetKind: "video_stream",
		OwnerKind: "youtube_video", OwnerID: "sample_video",
		FilePath: streamPath, ContentType: "video/mp4",
	})
	relPath := filepath.Join("media", "twitter", "sample-avatar.jpg")
	mustWriteFile(t, filepath.Join(srv.cfg.Storage.StateRoot(), relPath), []byte("ready-image"))
	return storeReadyWebTestAsset(t, srv, db.Asset{
		AssetID: "sample_avatar", AssetKind: "avatar", OwnerKind: "channel", OwnerID: "twitter_sample_author",
		FilePath: relPath, ContentType: "image/jpeg",
	})
}

func requestAndroidSyncPage(t *testing.T, srv *testServer, path string) androidSyncPageResponse {
	t.Helper()
	rec := requestAndroidSync(t, srv, http.MethodGet, path, nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d body=%s", path, rec.Code, rec.Body.String())
	}
	var envelope struct {
		Changes json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(envelope.Changes, []byte("null")) {
		t.Fatalf("GET %s returned null changes", path)
	}
	var page androidSyncPageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == "" {
		t.Fatalf("GET %s returned empty cursor", path)
	}
	return page
}

func requestAndroidSync(t *testing.T, srv *testServer, method, path string, body any, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		req = attachTestAuth(req, "sample_user")
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	return rec
}

func findAndroidSyncChange(changes []model.AndroidSyncChange, kind, id string) *model.AndroidSyncChange {
	for i := range changes {
		if changes[i].OwnerKind == kind && (id == "" || changes[i].OwnerID == id) {
			return &changes[i]
		}
	}
	return nil
}

func androidSyncChangeKeys(changes []model.AndroidSyncChange) [][2]string {
	out := make([][2]string, 0, len(changes))
	for _, change := range changes {
		out = append(out, [2]string{change.OwnerKind, change.OwnerID})
	}
	return out
}

func assertAndroidSyncChangesUnique(t *testing.T, changes []model.AndroidSyncChange) {
	t.Helper()
	seen := make(map[[2]string]struct{}, len(changes))
	for _, key := range androidSyncChangeKeys(changes) {
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate flat change %v in %+v", key, androidSyncChangeKeys(changes))
		}
		seen[key] = struct{}{}
	}
}

func jsonNumber(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func storeReadyWebTestAsset(t *testing.T, srv *testServer, asset db.Asset) db.Asset {
	t.Helper()
	if err := srv.db.StoreReadyAsset(asset, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	got, err := srv.db.GetAsset(asset.AssetID, asset.AssetKind)
	if err != nil || got == nil {
		t.Fatalf("asset = %+v / %v", got, err)
	}
	return *got
}

func newAndroidSyncTestServer(t *testing.T) *testServer {
	t.Helper()
	tmp, err := os.CreateTemp("", "igloo-sync-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := tmp.Name()
	_ = tmp.Close()
	dataDir := t.TempDir()
	database, err := db.OpenPath(dbPath, dataDir)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatal(err)
	}
	if err := database.RecordAndroidFeedRetention(0, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		_ = os.Remove(dbPath)
	})

	cfg := testWebConfig(t, dataDir)
	server := &Server{db: database, cfg: cfg, store: sessions.NewCookieStore([]byte("test-key")), workers: worker.NewManager(database, cfg)}
	mux := http.NewServeMux()
	server.registerAndroidSyncAPIRoutes(mux)
	return &testServer{Server: server, mux: mux}
}

func mustWriteFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}
