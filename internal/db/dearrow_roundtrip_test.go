package db

import (
	"path/filepath"
	"testing"
)

func TestVideoDearrowColumnsRoundTrip(t *testing.T) {
	d := openFreshTestDB(t)

	// Insert a plain video — dearrow columns will start NULL.
	if err := d.InsertVideo(
		"vid1", "youtube_alice", "youtube_video", "Original Clickbait!!!", "desc",
		60, 1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}

	// Verify dearrow fields are nil after plain insert.
	got, err := d.GetVideo("vid1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got == nil {
		t.Fatal("GetVideo returned nil")
	}
	if got.DearrowTitle != nil || got.DearrowTitleCasual != nil || got.DearrowCheckedAtMs != nil {
		t.Errorf("expected all dearrow fields nil after plain insert, got: title=%v casual=%v checked=%v",
			got.DearrowTitle, got.DearrowTitleCasual, got.DearrowCheckedAtMs)
	}
	if thumb, err := d.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "vid1", 0); err != nil || thumb != nil {
		t.Fatalf("plain video DeArrow asset = %+v, err = %v", thumb, err)
	}

	// Set dearrow fields via the proper setter.
	betterTitle := "Better Title"
	casualTitle := "Casual Title"
	thumbPath := "thumbnails/dearrow/vid1.jpg"
	writeDBTestFile(t, filepath.Join(d.storage.StateRoot(), thumbPath), []byte("dearrow thumbnail"))
	if err := d.SetDearrowData("vid1", &betterTitle, &casualTitle, &thumbPath, int64(1_700_000_100_000)); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}

	got, err = d.GetVideo("vid1")
	if err != nil {
		t.Fatalf("GetVideo after UPDATE: %v", err)
	}
	if got.DearrowTitle == nil || *got.DearrowTitle != "Better Title" {
		t.Errorf("DearrowTitle = %v, want 'Better Title'", got.DearrowTitle)
	}
	if got.DearrowTitleCasual == nil || *got.DearrowTitleCasual != "Casual Title" {
		t.Errorf("DearrowTitleCasual = %v, want 'Casual Title'", got.DearrowTitleCasual)
	}
	thumb, err := d.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "vid1", 0)
	if err != nil || thumb == nil || thumb.FilePath != thumbPath {
		t.Errorf("DeArrow thumbnail asset = %+v, err = %v", thumb, err)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs != 1_700_000_100_000 {
		t.Errorf("DearrowCheckedAtMs = %v", got.DearrowCheckedAtMs)
	}
}
