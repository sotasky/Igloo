package db

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestAndroidSyncHeadsCoalesceCanonicalOwnerChanges(t *testing.T) {
	d := openWritableTestDB(t)
	clock, err := d.GetAndroidSyncClock()
	if err != nil {
		t.Fatal(err)
	}
	if clock.Epoch == "" || clock.Revision != 0 {
		t.Fatalf("initial clock = %+v", clock)
	}

	if err := d.ExecRaw(`
		INSERT INTO feed_items (tweet_id, body_text, published_at, fetched_at)
		VALUES ('sample_post', 'first', 100, 100)
	`); err != nil {
		t.Fatal(err)
	}
	first := requireAndroidSyncHead(t, d, "feed", "sample_post")
	if first.Revision <= 0 {
		t.Fatalf("insert head = %+v", first)
	}

	if err := d.ExecRaw(`UPDATE feed_items SET fetched_at = 200 WHERE tweet_id = 'sample_post'`); err != nil {
		t.Fatal(err)
	}
	if got := requireAndroidSyncHead(t, d, "feed", "sample_post"); got != first {
		t.Fatalf("non-payload update changed head: before=%+v after=%+v", first, got)
	}

	if err := d.ExecRaw(`UPDATE feed_items SET body_text = 'second' WHERE tweet_id = 'sample_post'`); err != nil {
		t.Fatal(err)
	}
	second := requireAndroidSyncHead(t, d, "feed", "sample_post")
	if second.Revision <= first.Revision {
		t.Fatalf("payload update head = %+v after %+v", second, first)
	}

	if err := d.ExecRaw(`DELETE FROM feed_items WHERE tweet_id = 'sample_post'`); err != nil {
		t.Fatal(err)
	}
	deleted := requireAndroidSyncHead(t, d, "feed", "sample_post")
	if deleted.Revision <= second.Revision {
		t.Fatalf("delete head = %+v after %+v", deleted, second)
	}
	if heads, err := d.ListAndroidSyncHeads(second.Revision, 10); err != nil {
		t.Fatal(err)
	} else if len(heads) != 1 || heads[0] != deleted {
		t.Fatalf("heads after %d = %+v", second.Revision, heads)
	}
}

func TestAndroidSyncHeadsMapAttachmentsAndStateToOwners(t *testing.T) {
	d := openWritableTestDB(t)
	for _, statement := range []string{
		`INSERT INTO videos (video_id, channel_id, owner_kind, title) VALUES ('sample_video', 'youtube_sample', 'youtube_video', 'Sample')`,
		`INSERT INTO video_comments (video_id, comment_id, text) VALUES ('sample_video', 'sample_comment', 'first')`,
		`INSERT INTO retweet_sources (content_hash, retweeter_channel_id, tweet_id) VALUES ('sample_hash', 'twitter_sample', 'sample_post')`,
		`INSERT INTO bookmark_categories (name, created_at) VALUES ('Sample', 1)`,
	} {
		if err := d.ExecRaw(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range [][2]string{
		{"video", "sample_video"},
		{"retweet_sources", "sample_hash"},
		{"bookmark_category", "1"},
	} {
		_ = requireAndroidSyncHead(t, d, key[0], key[1])
	}
	beforeCommentDelete := requireAndroidSyncHead(t, d, "video", "sample_video")
	if err := d.ExecRaw(`DELETE FROM video_comments WHERE video_id = 'sample_video' AND comment_id = 'sample_comment'`); err != nil {
		t.Fatal(err)
	}
	afterCommentDelete := requireAndroidSyncHead(t, d, "video", "sample_video")
	if afterCommentDelete.Revision <= beforeCommentDelete.Revision {
		t.Fatalf("child delete head = %+v after %+v", afterCommentDelete, beforeCommentDelete)
	}
	if err := d.ExecRaw(`DELETE FROM retweet_sources WHERE content_hash = 'sample_hash'`); err != nil {
		t.Fatal(err)
	}
	_ = requireAndroidSyncHead(t, d, "retweet_sources", "sample_hash")
}

func TestAndroidSyncHeadsFilterSettingsAndHydrateProtectedContent(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`INSERT INTO settings (key, value) VALUES ('unrelated', 'one')`); err != nil {
		t.Fatal(err)
	}
	if heads, err := d.ListAndroidSyncHeads(0, 10); err != nil {
		t.Fatal(err)
	} else if len(heads) != 0 {
		t.Fatalf("unrelated setting produced heads: %+v", heads)
	}
	if err := d.ExecRaw(`INSERT INTO settings (key, value) VALUES ('translate_target_lang', 'tr')`); err != nil {
		t.Fatal(err)
	}
	_ = requireAndroidSyncHead(t, d, "setting", "translate_target_lang")

	if err := d.ExecRaw(`INSERT INTO feed_likes (tweet_id, liked_at) VALUES ('sample_protected', 1)`); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"feed_like", "feed", "video"} {
		_ = requireAndroidSyncHead(t, d, kind, "sample_protected")
	}
	oldFeed := requireAndroidSyncHead(t, d, "feed", "sample_protected")
	if err := d.ExecRaw(`UPDATE feed_likes SET tweet_id = 'sample_moved' WHERE tweet_id = 'sample_protected'`); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"feed_like", "feed", "video"} {
		_ = requireAndroidSyncHead(t, d, kind, "sample_moved")
	}
	if movedOld := requireAndroidSyncHead(t, d, "feed", "sample_protected"); movedOld.Revision <= oldFeed.Revision {
		t.Fatalf("old protection owner head did not advance: before=%+v after=%+v", oldFeed, movedOld)
	}
	beforeDelete := requireAndroidSyncHead(t, d, "feed", "sample_moved")
	if err := d.ExecRaw(`DELETE FROM feed_likes WHERE tweet_id = 'sample_moved'`); err != nil {
		t.Fatal(err)
	}
	if deleted := requireAndroidSyncHead(t, d, "feed", "sample_moved"); deleted.Revision <= beforeDelete.Revision {
		t.Fatalf("deleted protection owner head did not advance: before=%+v after=%+v", beforeDelete, deleted)
	}
}

func TestAndroidSyncAssetRevisionAlwaysAdvancesOwnerHead(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, state, required_reason
		) VALUES ('sample_asset', 'post_media', 'tweet', 'sample_post', 'server_missing', 'first')
	`); err != nil {
		t.Fatal(err)
	}
	before := requireAndroidSyncHead(t, d, "asset", "sample_asset")
	var assetRevisionBefore int64
	if err := d.QueryRow(`SELECT revision FROM assets WHERE asset_id = 'sample_asset'`).Scan(&assetRevisionBefore); err != nil {
		t.Fatal(err)
	}

	if err := d.ExecRaw(`UPDATE assets SET required_reason = 'second' WHERE asset_id = 'sample_asset'`); err != nil {
		t.Fatal(err)
	}
	after := requireAndroidSyncHead(t, d, "asset", "sample_asset")
	var assetRevisionAfter int64
	if err := d.QueryRow(`SELECT revision FROM assets WHERE asset_id = 'sample_asset'`).Scan(&assetRevisionAfter); err != nil {
		t.Fatal(err)
	}
	if assetRevisionAfter <= assetRevisionBefore || after.Revision <= before.Revision {
		t.Fatalf("asset/head revisions did not advance: asset %d->%d head %d->%d",
			assetRevisionBefore, assetRevisionAfter, before.Revision, after.Revision)
	}
}

func requireAndroidSyncHead(t *testing.T, d *DB, ownerKind, ownerID string) model.AndroidSyncHead {
	t.Helper()
	var head model.AndroidSyncHead
	err := d.QueryRow(`
		SELECT owner_kind, owner_id, revision
		FROM android_sync_heads
		WHERE owner_kind = ? AND owner_id = ?
	`, ownerKind, ownerID).Scan(&head.OwnerKind, &head.OwnerID, &head.Revision)
	if err != nil {
		t.Fatalf("read Android sync head %s/%s: %v", ownerKind, ownerID, err)
	}
	return head
}
