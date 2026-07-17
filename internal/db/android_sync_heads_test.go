package db

import (
	"database/sql"
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

func TestAndroidSyncHeadsTrackTemporaryVideoTransitions(t *testing.T) {
	t.Run("completed video upsert", func(t *testing.T) {
		d := openWritableTestDB(t)
		const videoID = "sample_temporary_upsert"
		if err := d.ExecRaw(`
			INSERT INTO videos (video_id, channel_id, owner_kind, title, is_temp)
			VALUES (?, 'youtube_sample_channel', 'youtube_video', 'Sample video', 1)
		`, videoID); err != nil {
			t.Fatal(err)
		}
		video := CompletedVideo{
			VideoID: videoID, ChannelID: "youtube_sample_channel", OwnerKind: "youtube_video",
			Title: "Sample video",
		}

		before := requireAndroidSyncHead(t, d, "video", videoID)
		if err := d.WithWrite(func(tx *sql.Tx) error {
			return upsertVideoMetadataTx(tx, video)
		}); err != nil {
			t.Fatal(err)
		}
		afterClear := requireAndroidSyncHead(t, d, "video", videoID)
		if afterClear.Revision <= before.Revision {
			t.Fatalf("clearing temporary state did not advance head: before=%+v after=%+v", before, afterClear)
		}

		video.IsTemp = true
		if err := d.WithWrite(func(tx *sql.Tx) error {
			return upsertVideoMetadataTx(tx, video)
		}); err != nil {
			t.Fatal(err)
		}
		afterSet := requireAndroidSyncHead(t, d, "video", videoID)
		if afterSet.Revision <= afterClear.Revision {
			t.Fatalf("setting temporary state did not advance head: before=%+v after=%+v", afterClear, afterSet)
		}

		if err := d.WithWrite(func(tx *sql.Tx) error {
			return upsertVideoMetadataTx(tx, video)
		}); err != nil {
			t.Fatal(err)
		}
		if stable := requireAndroidSyncHead(t, d, "video", videoID); stable != afterSet {
			t.Fatalf("unchanged temporary state advanced head: before=%+v after=%+v", afterSet, stable)
		}
	})

	t.Run("desire reconciliation", func(t *testing.T) {
		d := openWritableTestDB(t)
		const (
			source  = "youtube_sample_source"
			videoID = "sample_temporary_desire"
		)
		seedVideoDesireChannels(t, d, source)
		if err := d.ExecRaw(`
			INSERT INTO videos (video_id, channel_id, owner_kind, title, is_temp)
			VALUES (?, ?, 'youtube_video', 'Sample video', 1)
		`, videoID, source); err != nil {
			t.Fatal(err)
		}
		before := requireAndroidSyncHead(t, d, "video", videoID)
		if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
			SourceChannelID: source,
			Component:       "uploads",
			Items: []VideoDesire{{
				VideoID: videoID, OwnerChannelID: source,
				Lane: DownloadLaneCurrent,
			}},
		}); err != nil {
			t.Fatal(err)
		}
		after := requireAndroidSyncHead(t, d, "video", videoID)
		if after.Revision <= before.Revision {
			t.Fatalf("reconciling temporary video did not advance head: before=%+v after=%+v", before, after)
		}
	})

	t.Run("retention", func(t *testing.T) {
		d := openWritableTestDB(t)
		const videoID = "sample_temporary_retention"
		const nowMs = int64(3 * 24 * 60 * 60 * 1000)
		if err := d.ExecRaw(`
			INSERT INTO videos (video_id, channel_id, owner_kind, title, is_temp, downloaded_at)
			VALUES (?, 'youtube_sample_channel', 'youtube_video', 'Sample video', 1, ?)
		`, videoID, nowMs-int64(2*24*60*60*1000)); err != nil {
			t.Fatal(err)
		}
		if err := d.ExecRaw(`INSERT INTO bookmarks (video_id, bookmarked_at) VALUES (?, ?)`, videoID, nowMs); err != nil {
			t.Fatal(err)
		}
		before := requireAndroidSyncHead(t, d, "video", videoID)
		if _, err := d.MaintainVideoRetention(nowMs); err != nil {
			t.Fatal(err)
		}
		after := requireAndroidSyncHead(t, d, "video", videoID)
		if after.Revision <= before.Revision {
			t.Fatalf("retiring temporary state did not advance head: before=%+v after=%+v", before, after)
		}
		var isTemp bool
		if err := d.QueryRow(`SELECT is_temp FROM videos WHERE video_id = ?`, videoID).Scan(&isTemp); err != nil {
			t.Fatal(err)
		}
		if isTemp {
			t.Fatal("retention left the video temporary")
		}
	})
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
	asset := Asset{AssetID: "sample_asset", AssetKind: "post_media", OwnerKind: "tweet", OwnerID: "sample_post", SourceURL: "https://example.test/sample.jpg", RequiredReason: "first"}
	upsertAssetForTest(t, d, asset, 1)
	before := requireAndroidSyncHead(t, d, "asset", "sample_asset")
	var assetRevisionBefore int64
	if err := d.QueryRow(`SELECT revision FROM assets WHERE asset_id = 'sample_asset'`).Scan(&assetRevisionBefore); err != nil {
		t.Fatal(err)
	}

	asset.RequiredReason = "second"
	upsertAssetForTest(t, d, asset, 2)
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
