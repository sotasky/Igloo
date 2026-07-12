package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkDearrowChecked_SetsTimestamp(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_testchan', 'Test Channel', 'youtube')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.InsertVideo(
		"vid1", "youtube_testchan", "youtube_video", "Original Title", "desc",
		60, 1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}

	if err := d.MarkDearrowChecked("vid1", 1_700_000_000_000); err != nil {
		t.Fatalf("MarkDearrowChecked: %v", err)
	}

	got, err := d.GetVideo("vid1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got == nil {
		t.Fatal("GetVideo returned nil")
	}
	if got.DearrowCheckedAtMs == nil {
		t.Fatal("DearrowCheckedAtMs is nil, want 1_700_000_000_000")
	}
	if *got.DearrowCheckedAtMs != 1_700_000_000_000 {
		t.Errorf("DearrowCheckedAtMs = %d, want 1700000000000", *got.DearrowCheckedAtMs)
	}
	if got.DearrowTitle != nil {
		t.Errorf("DearrowTitle should be nil, got %v", got.DearrowTitle)
	}
	if got.DearrowTitleCasual != nil {
		t.Errorf("DearrowTitleCasual should be nil, got %v", got.DearrowTitleCasual)
	}
	if thumb, err := d.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "vid1", 0); err != nil || thumb != nil {
		t.Errorf("DeArrow thumbnail asset = %+v, err = %v; want none", thumb, err)
	}
}

func TestSetDearrowData_RoundTrip(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_testchan', 'Test Channel', 'youtube')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.InsertVideo(
		"vid1", "youtube_testchan", "youtube_video", "Original Title", "desc",
		60, 1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}

	title := "Better"
	titleCasual := "Casual"
	thumbPath := "thumbnails/dearrow/vid1.jpg"
	collidingThumb := "thumbnails/dearrow/tiktok-vid1.jpg"
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), thumbPath), []byte("dearrow thumbnail"))
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), collidingThumb), []byte("tiktok thumbnail"))
	if err := d.StoreReadyAsset(Asset{
		AssetID: BuildAssetID("tiktok", "tiktok_video", "test_video_1", "dearrow_thumbnail", 0), AssetKind: "dearrow_thumbnail",
		OwnerKind: "tiktok_video", OwnerID: "test_video_1", FilePath: collidingThumb,
	}, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.SetDearrowData("vid1", &title, &titleCasual, &thumbPath, 1_700_000_100_000); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}

	got, err := d.GetVideo("vid1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got == nil {
		t.Fatal("GetVideo returned nil")
	}
	if got.DearrowTitle == nil || *got.DearrowTitle != "Better" {
		t.Errorf("DearrowTitle = %v, want 'Better'", got.DearrowTitle)
	}
	if got.DearrowTitleCasual == nil || *got.DearrowTitleCasual != "Casual" {
		t.Errorf("DearrowTitleCasual = %v, want 'Casual'", got.DearrowTitleCasual)
	}
	thumb, err := d.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "vid1", 0)
	if err != nil || thumb == nil || thumb.FilePath != thumbPath {
		t.Errorf("DeArrow thumbnail asset = %+v, err = %v", thumb, err)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs != 1_700_000_100_000 {
		t.Errorf("DearrowCheckedAtMs = %v, want 1700000100000", got.DearrowCheckedAtMs)
	}
	colliding, err := d.GetAssetByOwnerIdentity("dearrow_thumbnail", "tiktok_video", "test_video_1", 0)
	if err != nil || colliding == nil || colliding.FilePath != collidingThumb {
		t.Fatalf("colliding DeArrow asset changed: %+v %v", colliding, err)
	}
	if _, err := os.Stat(filepath.Join(d.storage.StateRoot(), collidingThumb)); err != nil {
		t.Fatalf("colliding DeArrow file was removed: %v", err)
	}
}

func TestSetDearrowData_NilClearsField(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_test_channel', 'Test Channel', 'youtube')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.InsertVideo(
		"vid1", "youtube_testchan", "youtube_video", "Original Title", "desc",
		60, 1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}

	// First call: set all fields.
	title := "Better"
	titleCasual := "Casual"
	thumbPath := "thumbnails/dearrow/vid1.jpg"
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), thumbPath), []byte("dearrow thumbnail"))
	if err := d.SetDearrowData("vid1", &title, &titleCasual, &thumbPath, 1_700_000_100_000); err != nil {
		t.Fatalf("SetDearrowData (set): %v", err)
	}

	// Second call: nil all value pointers with a new timestamp.
	if err := d.SetDearrowData("vid1", nil, nil, nil, 1_700_000_200_000); err != nil {
		t.Fatalf("SetDearrowData (nil): %v", err)
	}

	got, err := d.GetVideo("vid1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got == nil {
		t.Fatal("GetVideo returned nil")
	}
	if got.DearrowTitle != nil {
		t.Errorf("DearrowTitle should be nil after clear, got %v", got.DearrowTitle)
	}
	if got.DearrowTitleCasual != nil {
		t.Errorf("DearrowTitleCasual should be nil after clear, got %v", got.DearrowTitleCasual)
	}
	if thumb, err := d.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "vid1", 0); err != nil || thumb != nil {
		t.Errorf("DeArrow thumbnail asset = %+v, err = %v; want removed", thumb, err)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs != 1_700_000_200_000 {
		t.Errorf("DearrowCheckedAtMs = %v, want 1700000200000", got.DearrowCheckedAtMs)
	}
}

func TestSetDearrowTitlesPreservesMissingFieldsAndReadyThumbnail(t *testing.T) {
	d := openFreshTestDB(t)
	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_testchan', 'Test Channel', 'youtube')`); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertVideo("vid1", "youtube_test_channel", "youtube_video", "Original", "", 60, 1, "", "video", 0, false); err != nil {
		t.Fatal(err)
	}
	oldTitle, oldCasual := "Old title", "Old casual"
	thumb := "thumbnails/dearrow/ready.jpg"
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), thumb), []byte("ready thumbnail"))
	if err := d.SetDearrowData("vid1", &oldTitle, &oldCasual, &thumb, 1); err != nil {
		t.Fatal(err)
	}
	newTitle := "New title"
	if err := d.SetDearrowTitles("vid1", &newTitle, nil, 2); err != nil {
		t.Fatal(err)
	}
	video, err := d.GetVideo("vid1")
	if err != nil {
		t.Fatal(err)
	}
	if video.DearrowTitle == nil || *video.DearrowTitle != newTitle ||
		video.DearrowTitleCasual == nil || *video.DearrowTitleCasual != oldCasual {
		t.Fatalf("partial title update cleared ready branding: %+v", video)
	}
	asset, err := d.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "vid1", 0)
	if err != nil || asset == nil || asset.FilePath != thumb {
		t.Fatalf("partial title update changed ready thumbnail: %+v, err = %v", asset, err)
	}
}
