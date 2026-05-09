package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #11 — mutation endpoint dispatcher. Worked-example test for `like`
// covers toggle set/clear + sync_version advance + sync_stream field.
// Remaining kinds get basic tests to lock their envelopes down.

func TestMutationLikeSetAndClear(t *testing.T) {
	srv := newTestServer(t)

	// SET: insert a feed_likes row + advance sync_version.
	set := postMutation(t, srv, "POST", "/api/mutations/like", "alice", `{
	  "tweet_id": "tw_like",
	  "action": "set",
	  "updated_at_ms": 1745100000000
	}`)
	if set["sync_stream"] != "feed" {
		t.Errorf("sync_stream = %v, want feed", set["sync_stream"])
	}
	v1, ok := set["sync_version"].(float64)
	if !ok || v1 == 0 {
		t.Fatalf("expected non-zero sync_version, got %v", set["sync_version"])
	}

	// Verify side-table write landed.
	var count int
	srv.db.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE username='alice' AND tweet_id='tw_like'`).Scan(&count)
	if count != 1 {
		t.Errorf("feed_likes row missing after set, got %d", count)
	}
	var likeValue string
	srv.db.QueryRow(`SELECT value FROM sync_changes WHERE type='like' AND item_id='tw_like' ORDER BY version DESC LIMIT 1`).Scan(&likeValue)
	if !strings.Contains(likeValue, `"action":"set"`) || !strings.Contains(likeValue, `"liked":true`) {
		t.Fatalf("like sync value = %s, want action set and liked true", likeValue)
	}

	// CLEAR: delete the row + advance version again.
	clear := postMutation(t, srv, "POST", "/api/mutations/like", "alice", `{
	  "tweet_id": "tw_like",
	  "action": "clear",
	  "updated_at_ms": 1745100001000
	}`)
	v2, _ := clear["sync_version"].(float64)
	if v2 <= v1 {
		t.Errorf("sync_version should advance on clear: %v → %v", v1, v2)
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE username='alice' AND tweet_id='tw_like'`).Scan(&count)
	if count != 0 {
		t.Errorf("feed_likes row should be gone after clear, got %d", count)
	}
	srv.db.QueryRow(`SELECT value FROM sync_changes WHERE type='like' AND item_id='tw_like' ORDER BY version DESC LIMIT 1`).Scan(&likeValue)
	if !strings.Contains(likeValue, `"action":"clear"`) || !strings.Contains(likeValue, `"liked":false`) {
		t.Fatalf("like clear sync value = %s, want action clear and liked false", likeValue)
	}
}

func TestMutationLikeRejectsInvalidAction(t *testing.T) {
	srv := newTestServer(t)
	body := `{"tweet_id":"tw","action":"toggle","updated_at_ms":1}`
	resp := postMutation(t, srv, "POST", "/api/mutations/like", "alice", body)
	// Error envelope carries error_code=invalid_body when apply-fn returns errInvalidAction.
	if resp["error_code"] != "invalid_body" {
		t.Errorf("expected error_code=invalid_body, got %v", resp["error_code"])
	}
}

func TestMutationCreateCategoryEchoesProvisionalID(t *testing.T) {
	// Critical flow: client submits provisional_id, response carries
	// real category_id + provisional_id so the outbox dispatcher can
	// cascade-update bookmarks inside the same Room transaction.
	srv := newTestServer(t)
	resp := postMutation(t, srv, "POST", "/api/mutations/create_category", "alice", `{
	  "name": "Linux",
	  "provisional_id": "-7",
	  "updated_at_ms": 1745100000000
	}`)
	if resp["provisional_id"] != "-7" {
		t.Errorf("provisional_id should echo: got %v", resp["provisional_id"])
	}
	if _, ok := resp["category_id"].(float64); !ok {
		t.Errorf("expected numeric category_id, got %T (%v)", resp["category_id"], resp["category_id"])
	}
	if _, ok := resp["sync_version"].(float64); !ok {
		t.Errorf("expected sync_version")
	}
	categoryID := int64(resp["category_id"].(float64))
	var categoryValue string
	if err := srv.db.QueryRow(
		`SELECT value FROM sync_changes WHERE type='bookmark_category' AND item_id=? ORDER BY version DESC LIMIT 1`,
		fmt.Sprintf("%d", categoryID),
	).Scan(&categoryValue); err != nil {
		t.Fatalf("bookmark_category sync row missing: %v", err)
	}
	if !strings.Contains(categoryValue, `"action":"set"`) || !strings.Contains(categoryValue, `"name":"Linux"`) {
		t.Fatalf("bookmark_category sync value = %s, want set Linux", categoryValue)
	}
}

func TestMutationSeenBatched(t *testing.T) {
	srv := newTestServer(t)
	ids := []string{"tw_a", "tw_b", "tw_c"}
	body := fmt.Sprintf(`{"tweet_ids":["%s"],"updated_at_ms":%d}`,
		strings.Join(ids, `","`), time.Now().UnixMilli())
	resp := postMutation(t, srv, "POST", "/api/mutations/seen", "alice", body)
	marked, _ := resp["marked"].(float64)
	if int(marked) != 3 {
		t.Errorf("marked = %v, want 3", marked)
	}
	var count int
	srv.db.QueryRow(`SELECT COUNT(*) FROM feed_seen WHERE username='alice'`).Scan(&count)
	if count != 3 {
		t.Errorf("feed_seen rows = %d, want 3", count)
	}
}

func TestMutationFollowRejectsPathTraversalChannelID(t *testing.T) {
	srv := newTestServer(t)
	resp := postMutation(t, srv, "POST", "/api/mutations/follow", "alice", `{
	  "channel_id": "youtube_../../../escape", "action": "set", "updated_at_ms": 1
	}`)
	if resp["error_code"] != "invalid_body" {
		t.Fatalf("error_code = %v, want invalid_body", resp["error_code"])
	}
	var count int
	srv.db.QueryRow(`SELECT COUNT(*) FROM channel_follows WHERE channel_id='youtube_../../../escape'`).Scan(&count)
	if count != 0 {
		t.Fatalf("follow row created for invalid channel_id, count=%d", count)
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE channel_id='youtube_../../../escape'`).Scan(&count)
	if count != 0 {
		t.Fatalf("channel row created for invalid channel_id, count=%d", count)
	}
}

func TestMutationFollowRejectsBareYouTubeChannelID(t *testing.T) {
	srv := newTestServer(t)
	resp := postMutation(t, srv, "POST", "/api/mutations/follow", "alice", `{
	  "channel_id": "UCbarechannel", "action": "set", "updated_at_ms": 1
	}`)
	if resp["error_code"] != "invalid_body" {
		t.Fatalf("error_code = %v, want invalid_body", resp["error_code"])
	}
	var count int
	srv.db.QueryRow(`SELECT COUNT(*) FROM channel_follows WHERE channel_id='UCbarechannel'`).Scan(&count)
	if count != 0 {
		t.Fatalf("follow row created for bare YouTube channel_id, count=%d", count)
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE channel_id='UCbarechannel'`).Scan(&count)
	if count != 0 {
		t.Fatalf("channel row created for bare YouTube channel_id, count=%d", count)
	}
}

func TestMutationFollowAndStar(t *testing.T) {
	srv := newTestServer(t)
	postMutation(t, srv, "POST", "/api/mutations/follow", "alice", `{
	  "channel_id": "youtube_alice", "action": "set", "updated_at_ms": 1
	}`)
	postMutation(t, srv, "POST", "/api/mutations/star", "alice", `{
	  "channel_id": "youtube_alice", "action": "set", "updated_at_ms": 1
	}`)
	var fcount, scount, channelCount int
	srv.db.QueryRow(`SELECT COUNT(*) FROM channel_follows WHERE channel_id='youtube_alice'`).Scan(&fcount)
	srv.db.QueryRow(`SELECT COUNT(*) FROM channel_stars WHERE channel_id='youtube_alice'`).Scan(&scount)
	srv.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE channel_id='youtube_alice'`).Scan(&channelCount)
	if fcount != 1 || scount != 1 {
		t.Errorf("follow=%d star=%d, want both 1", fcount, scount)
	}
	if channelCount != 1 {
		t.Errorf("follow mutation should create channel stub, got %d rows", channelCount)
	}
	var followValue string
	srv.db.QueryRow(`SELECT value FROM sync_changes WHERE type='follow' AND item_id='youtube_alice' ORDER BY version DESC LIMIT 1`).Scan(&followValue)
	if !strings.Contains(followValue, `"action":"set"`) || !strings.Contains(followValue, `"followed":true`) {
		t.Fatalf("follow sync value = %s, want action set and followed true", followValue)
	}
}

func TestMutationFollowClearPurgesUnbookmarkedVideoChannelContent(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, sync_seq)
		VALUES ('tiktok_purge', 'Purge Me', 'tiktok', 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_profiles (channel_id, platform, display_name, avatar_url, banner_url)
		VALUES ('tiktok_purge', 'tiktok', 'Purge Me', 'avatar', 'banner')
	`); err != nil {
		t.Fatalf("insert channel profile: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'tiktok_purge', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, title, file_path, thumbnail_path, file_size, published_at, sync_seq)
		VALUES
			('purged_short', 'tiktok_purge', 'Purged', 'videos/purged.mp4', 'thumbs/purged.jpg', 10, 1, 1),
			('saved_short', 'tiktok_purge', 'Saved', 'videos/saved.mp4', 'thumbs/saved.jpg', 10, 1, 2)
	`); err != nil {
		t.Fatalf("insert videos: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, bookmarked_at)
		VALUES ('alice', 'saved_short', 2)
	`); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size)
		VALUES
			('feed_media', 'purged_short', 0, 'slides/purged_0.jpg', 'photo', 10),
			('feed_media', 'saved_short', 0, 'slides/saved_0.jpg', 'photo', 10),
			('avatar', 'tiktok_purge', 0, 'avatars/tiktok_purge.jpg', 'photo', 10)
	`); err != nil {
		t.Fatalf("insert media files: %v", err)
	}
	for _, rel := range []string{
		"videos/purged.mp4",
		"thumbs/purged.jpg",
		"slides/purged_0.jpg",
		"videos/saved.mp4",
		"thumbs/saved.jpg",
		"slides/saved_0.jpg",
		"avatars/tiktok_purge.jpg",
	} {
		path := filepath.Join(srv.cfg.DataDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	resp := postMutation(t, srv, "POST", "/api/mutations/follow", "alice", `{
	  "channel_id": "tiktok_purge", "action": "clear", "updated_at_ms": 3
	}`)
	if resp["error_code"] != nil {
		t.Fatalf("follow clear failed: %v", resp)
	}

	var count int
	srv.db.QueryRow(`SELECT COUNT(*) FROM channel_follows WHERE channel_id='tiktok_purge'`).Scan(&count)
	if count != 0 {
		t.Fatalf("channel_follows count = %d, want 0", count)
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM videos WHERE video_id='purged_short'`).Scan(&count)
	if count != 0 {
		t.Fatalf("purged_short should be deleted, count=%d", count)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "videos/purged.mp4")); !os.IsNotExist(err) {
		t.Fatalf("purged media file should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "slides/purged_0.jpg")); !os.IsNotExist(err) {
		t.Fatalf("purged slide file should be removed, stat err=%v", err)
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM videos WHERE video_id='saved_short'`).Scan(&count)
	if count != 1 {
		t.Fatalf("bookmarked saved_short should survive, count=%d", count)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "videos/saved.mp4")); err != nil {
		t.Fatalf("bookmarked media file should survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "slides/saved_0.jpg")); err != nil {
		t.Fatalf("bookmarked slide file should survive: %v", err)
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE channel_id='tiktok_purge'`).Scan(&count)
	if count != 1 {
		t.Fatalf("channel row should survive for bookmarked video profile info, count=%d", count)
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM channel_profiles WHERE channel_id='tiktok_purge'`).Scan(&count)
	if count != 1 {
		t.Fatalf("channel profile should survive for bookmarked video profile info, count=%d", count)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "avatars/tiktok_purge.jpg")); err != nil {
		t.Fatalf("profile media should survive for bookmarked video: %v", err)
	}
}

func TestMutationFollowClearPurgesUnfollowedTwitterMediaFiles(t *testing.T) {
	srv := newTestServer(t)

	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, sync_seq)
		VALUES ('twitter_drop', 'Drop', 'twitter', 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES ('', 'twitter_drop', 1)
	`); err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO feed_items (tweet_id, author_handle, source_handle, published_at, fetched_at)
		VALUES
			('tw_drop', 'drop', 'drop', 1, 1),
			('tw_saved', 'drop', 'drop', 2, 2)
	`); err != nil {
		t.Fatalf("insert feed items: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO bookmarks (user_id, video_id, bookmarked_at)
		VALUES ('alice', 'tw_saved', 2)
	`); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}
	if err := srv.db.ExecRaw(`
		INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size)
		VALUES
			('feed_media', 'tw_drop', 0, 'twitter/drop-feed.jpg', 'photo', 10),
			('quote_media', 'tw_drop', 0, 'twitter/drop-quote.jpg', 'photo', 10),
			('feed_media', 'tw_saved', 0, 'twitter/saved-feed.jpg', 'photo', 10)
	`); err != nil {
		t.Fatalf("insert media files: %v", err)
	}
	for _, rel := range []string{
		"twitter/drop-feed.jpg",
		"twitter/drop-quote.jpg",
		"twitter/saved-feed.jpg",
	} {
		path := filepath.Join(srv.cfg.DataDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	resp := postMutation(t, srv, "POST", "/api/mutations/follow", "alice", `{
	  "channel_id": "twitter_drop", "action": "clear", "updated_at_ms": 3
	}`)
	if resp["error_code"] != nil {
		t.Fatalf("follow clear failed: %v", resp)
	}

	var count int
	srv.db.QueryRow(`SELECT COUNT(*) FROM feed_items WHERE tweet_id='tw_drop'`).Scan(&count)
	if count != 0 {
		t.Fatalf("tw_drop should be deleted, count=%d", count)
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM media_files WHERE owner_id='tw_drop'`).Scan(&count)
	if count != 0 {
		t.Fatalf("tw_drop media_files should be deleted, count=%d", count)
	}
	for _, rel := range []string{"twitter/drop-feed.jpg", "twitter/drop-quote.jpg"} {
		if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("purged twitter media file %s should be removed, stat err=%v", rel, err)
		}
	}
	srv.db.QueryRow(`SELECT COUNT(*) FROM feed_items WHERE tweet_id='tw_saved'`).Scan(&count)
	if count != 1 {
		t.Fatalf("bookmarked tw_saved should survive, count=%d", count)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "twitter/saved-feed.jpg")); err != nil {
		t.Fatalf("bookmarked twitter media file should survive: %v", err)
	}
}

func TestMutationMute(t *testing.T) {
	srv := newTestServer(t)
	postMutation(t, srv, "POST", "/api/mutations/mute", "alice", `{
	  "handle": "spammer", "action": "set", "updated_at_ms": 1
	}`)
	var count int
	srv.db.QueryRow(`SELECT COUNT(*) FROM muted_accounts WHERE handle='spammer'`).Scan(&count)
	if count != 1 {
		t.Errorf("muted_accounts count = %d, want 1", count)
	}
}

func TestMutationBookmark(t *testing.T) {
	srv := newTestServer(t)
	postMutation(t, srv, "POST", "/api/mutations/bookmark", "alice", `{
	  "video_id": "v_bm", "action": "set", "custom_title": "My pick",
	  "account_handles": "author_handle,quoted_handle", "updated_at_ms": 123
	}`)
	var count int
	var handles string
	srv.db.QueryRow(`SELECT COUNT(*), COALESCE(account_handles, '') FROM bookmarks WHERE user_id='alice' AND video_id='v_bm'`).Scan(&count, &handles)
	if count != 1 {
		t.Errorf("bookmark count = %d, want 1", count)
	}
	if handles != "author_handle,quoted_handle" {
		t.Errorf("account_handles = %q, want propagated payload", handles)
	}
}

func TestMutationBookmarkArchivesCategoryMedia(t *testing.T) {
	srv := newTestServer(t)

	archiveDir := t.TempDir()
	categoryID, err := srv.db.CreateBookmarkCategory("alice", "Archive", archiveDir)
	if err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}

	relPath := filepath.Join("feed_media", "tw_archive_0.jpg")
	fullPath := filepath.Join(srv.cfg.DataDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir media dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("image-bytes"), 0o644); err != nil {
		t.Fatalf("write media fixture: %v", err)
	}
	if err := srv.db.ExecRaw(
		`INSERT INTO media_files (owner_type, owner_id, media_index, file_path, media_type, file_size)
		 VALUES ('feed_media', 'tw_archive', 0, ?, 'photo', ?)`,
		relPath, len("image-bytes"),
	); err != nil {
		t.Fatalf("insert media_files: %v", err)
	}

	resp := postMutation(t, srv, "POST", "/api/mutations/bookmark", "alice", fmt.Sprintf(`{
	  "video_id": "tw_archive",
	  "action": "set",
	  "category_id": %d,
	  "custom_title": "Saved Label",
	  "account_handles": "author_handle",
	  "media_indices": "0",
	  "updated_at_ms": 123
	}`, categoryID))
	if resp["error_code"] != nil {
		t.Fatalf("bookmark mutation failed: %v", resp)
	}

	want := filepath.Join(archiveDir, "author_handle Saved Label 001.jpg")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(want); err == nil {
			break
		}
		if time.Now().After(deadline) {
			entries, _ := os.ReadDir(archiveDir)
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("archived media %q not found; entries=%v", filepath.Base(want), names)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMutationProgress(t *testing.T) {
	srv := newTestServer(t)
	postMutation(t, srv, "PUT", "/api/mutations/progress", "alice", `{
	  "video_id": "v_prog", "position": 42.5, "duration": 120, "source": "web",
	  "updated_at_ms": 1745100000000
	}`)
	var pos float64
	srv.db.QueryRow(`SELECT playback_position FROM watch_history WHERE user_id='alice' AND video_id='v_prog'`).Scan(&pos)
	if pos != 42.5 {
		t.Errorf("progress position = %v, want 42.5", pos)
	}
}

func TestMutationLikeSetMarksSeenAndEmitsSeenDelta(t *testing.T) {
	srv := newTestServer(t)

	resp := postMutation(t, srv, "POST", "/api/mutations/like", "alice", `{
	  "tweet_id": "tw_like_seen",
	  "action": "set",
	  "updated_at_ms": 1745100000000
	}`)
	if _, ok := resp["sync_version"].(float64); !ok {
		t.Fatalf("expected sync_version, got %v", resp)
	}

	var liked, seen, seenChanges int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_likes WHERE username='alice' AND tweet_id='tw_like_seen'`).Scan(&liked); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_seen WHERE username='alice' AND tweet_id='tw_like_seen'`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM sync_changes WHERE type='seen' AND item_id='tw_like_seen'`).Scan(&seenChanges); err != nil {
		t.Fatal(err)
	}
	if liked != 1 || seen != 1 || seenChanges != 1 {
		t.Fatalf("liked=%d seen=%d seenChanges=%d, want all 1", liked, seen, seenChanges)
	}
}

func TestMutationChannelSettingWhitelistsField(t *testing.T) {
	srv := newTestServer(t)
	// channel_settings has a FK to channels — seed the parent row.
	srv.db.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_x', 'x', 'youtube')`)

	// Bogus field should 400.
	resp := postMutation(t, srv, "PUT", "/api/mutations/channel_setting", "alice", `{
	  "channel_id": "youtube_x", "field": "DROP TABLE", "value": 1, "updated_at_ms": 1
	}`)
	if resp["error_code"] != "invalid_body" {
		t.Errorf("expected error_code=invalid_body for unknown field, got %v", resp["error_code"])
	}
	// Allowed field should succeed.
	resp = postMutation(t, srv, "PUT", "/api/mutations/channel_setting", "alice", `{
	  "channel_id": "youtube_x", "field": "include_reposts", "value": 0, "updated_at_ms": 1
	}`)
	if _, ok := resp["sync_version"].(float64); !ok {
		t.Errorf("expected sync_version on successful channel_setting, got %v", resp)
	}
}

func TestMutationDeltaReturnsInteractionStream(t *testing.T) {
	srv := newTestServer(t)

	beforeVersion, _ := srv.db.GetCurrentSyncVersion()
	if err := srv.db.RecordSyncChange("media_ready", "asset_internal", `{"ready":true}`); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.RecordSyncChange("like", "tw_delta_like", `{"action":"set"}`); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.RecordSyncChange("seen", "tw_delta_seen", `{"tweet_ids":["tw_delta_seen"],"updated_at_ms":1}`); err != nil {
		t.Fatal(err)
	}

	first := getJSON(t, srv, fmt.Sprintf("/api/mutations/delta?since=%d&limit=1", beforeVersion), "alice")
	changes, ok := first["changes"].([]any)
	if !ok || len(changes) != 1 {
		t.Fatalf("first changes = %#v, want one change", first["changes"])
	}
	if first["truncated"] != true {
		t.Fatalf("truncated = %v, want true", first["truncated"])
	}
	change := changes[0].(map[string]any)
	if change["type"] != "like" || change["item_id"] != "tw_delta_like" {
		t.Fatalf("first change = %v, want like/tw_delta_like", change)
	}
	value, ok := change["value"].(map[string]any)
	if !ok || value["action"] != "set" {
		t.Fatalf("value = %#v, want raw JSON object", change["value"])
	}

	since := int64(change["version"].(float64))
	second := getJSON(t, srv, fmt.Sprintf("/api/mutations/delta?since=%d", since), "alice")
	secondChanges := second["changes"].([]any)
	if len(secondChanges) != 1 {
		t.Fatalf("second changes = %#v, want one change", second["changes"])
	}
	secondChange := secondChanges[0].(map[string]any)
	if secondChange["type"] != "seen" {
		t.Fatalf("second change = %v, want seen", secondChange)
	}
}

func TestMutationDeltaScopesBookmarkCategoryStream(t *testing.T) {
	srv := newTestServer(t)

	beforeVersion, _ := srv.db.GetCurrentSyncVersion()
	postMutation(t, srv, "POST", "/api/mutations/create_category", "alice", `{
	  "name": "Linux",
	  "provisional_id": "-7",
	  "updated_at_ms": 1745100000000
	}`)
	postMutation(t, srv, "POST", "/api/mutations/create_category", "bob", `{
	  "name": "Private Bob",
	  "provisional_id": "-8",
	  "updated_at_ms": 1745100000001
	}`)

	body := getJSON(t, srv, fmt.Sprintf("/api/mutations/delta?since=%d&limit=10", beforeVersion), "alice")
	changes, ok := body["changes"].([]any)
	if !ok {
		t.Fatalf("changes = %#v, want array", body["changes"])
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %#v, want alice create_category and bookmark_category only", body["changes"])
	}
	for _, raw := range changes {
		change := raw.(map[string]any)
		if change["type"] != "create_category" && change["type"] != "bookmark_category" {
			t.Fatalf("change = %#v, want category mutation", change)
		}
		value := change["value"].(map[string]any)
		if value["user_id"] != "alice" {
			t.Fatalf("value = %#v, want alice-scoped category value", value)
		}
		if strings.Contains(fmt.Sprint(value), "Private Bob") {
			t.Fatalf("alice delta leaked bob category metadata: %#v", value)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func getJSON(t *testing.T, srv *testServer, path, user string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if user != "" {
		req = attachTestAuth(req, user)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return parsed
}

func postMutation(t *testing.T, srv *testServer, method, path, user, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req = attachTestAuth(req, user)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	var parsed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return parsed
}
