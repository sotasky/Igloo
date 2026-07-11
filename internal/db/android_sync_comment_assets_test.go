package db

import (
	"path/filepath"
	"testing"
)

func TestListAndroidSyncCommentAuthorAssetsUsesTopSyncedComments(t *testing.T) {
	d := openWritableTestDB(t)
	const nowMs = int64(10 * 24 * 60 * 60 * 1000)
	const published = nowMs - int64(24*60*60*1000)

	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES
			('sample_video_1', 'youtube_sample_channel', 'youtube_video', 'Video', ?),
			('sample_video_other', 'tiktok_sample_channel', 'tiktok_video', 'Other', ?)
	`, published, published); err != nil {
		t.Fatalf("insert videos: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_comments (
			video_id, comment_id, author_name, author_id, text, like_count, published_at
		) VALUES
			('sample_video_1', 'sample_comment_1', 'Commenter One', 'UCcommenterOne', 'hello', 50, ?),
			('sample_video_1', 'sample_comment_2', 'Commenter Two', 'youtube_UCcommenterTwo', 'hello', 40, ?),
			('sample_video_1', 'sample_comment_3', 'Commenter Three', 'UCcommenterThree', 'hello', 1, ?),
			('sample_video_other', 'sample_comment_4', 'Other', 'UCother', 'hello', 100, ?)
	`, published, published, published, published); err != nil {
		t.Fatalf("insert comments: %v", err)
	}

	for _, ownerID := range []string{
		"youtube_UCcommenterOne",
		"youtube_UCcommenterTwo",
		"youtube_UCcommenterThree",
		"youtube_UCother",
	} {
		rel := filepath.Join("thumbnails", "avatars", ownerID+"-comment.jpg")
		writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), rel), []byte(ownerID))
		if err := d.StoreReadyAsset(Asset{
			AssetID:        BuildAssetID("youtube", "comment_author", ownerID, "avatar", 0),
			AssetKind:      "avatar",
			OwnerKind:      "comment_author",
			OwnerID:        ownerID,
			FilePath:       rel,
			ContentType:    "image/jpeg",
			RequiredReason: "comment_avatar",
		}, nowMs); err != nil {
			t.Fatalf("store %s: %v", ownerID, err)
		}
	}

	rows, err := d.ListAndroidSyncCommentAuthorAssets([]string{"sample_video_1", "sample_video_other"}, 2)
	if err != nil {
		t.Fatalf("ListAndroidSyncCommentAuthorAssets: %v", err)
	}
	got := map[string]Asset{}
	for _, row := range rows {
		got[row.Asset.OwnerID] = row.Asset
	}
	for _, ownerID := range []string{"youtube_UCcommenterOne", "youtube_UCcommenterTwo"} {
		asset, ok := got[ownerID]
		if !ok || asset.OwnerKind != "comment_author" || asset.AssetKind != "avatar" || asset.State != AssetStateReady {
			t.Fatalf("%s asset = %+v", ownerID, asset)
		}
	}
	if _, ok := got["youtube_UCcommenterThree"]; ok {
		t.Fatalf("low-ranked comment avatar should not be selected: %+v", got)
	}
	if _, ok := got["youtube_UCother"]; ok {
		t.Fatalf("non-YouTube video comment avatar should not be selected: %+v", got)
	}
}
