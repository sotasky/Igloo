package db

import (
	"testing"
)

func TestVideoDearrowColumnsRoundTrip(t *testing.T) {
	d := openFreshTestDB(t)

	// Insert a plain video — dearrow columns will start NULL.
	if err := d.InsertVideo(
		"vid1", "youtube_alice", "Original Clickbait!!!", "desc",
		60, "thumbs/orig.jpg", "videos/orig.mp4", 1024,
		1_700_000_000_000, "", "video", 0, false,
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
	if got.DearrowTitle != nil || got.DearrowTitleCasual != nil ||
		got.DearrowThumbPath != nil || got.DearrowCheckedAtMs != nil {
		t.Errorf("expected all dearrow fields nil after plain insert, got: title=%v casual=%v thumb=%v checked=%v",
			got.DearrowTitle, got.DearrowTitleCasual, got.DearrowThumbPath, got.DearrowCheckedAtMs)
	}

	// Set dearrow fields via the proper setter.
	betterTitle := "Better Title"
	casualTitle := "Casual Title"
	thumbPath := "thumbnails/dearrow/vid1.jpg"
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
	if got.DearrowThumbPath == nil || *got.DearrowThumbPath != "thumbnails/dearrow/vid1.jpg" {
		t.Errorf("DearrowThumbPath = %v", got.DearrowThumbPath)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs != 1_700_000_100_000 {
		t.Errorf("DearrowCheckedAtMs = %v", got.DearrowCheckedAtMs)
	}
}
