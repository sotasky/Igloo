package db

import (
	"testing"
)

func TestMarkDearrowChecked_SetsTimestamp(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_testchan', 'Test Channel', 'youtube')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.InsertVideo(
		"vid1", "youtube_testchan", "Original Title", "desc",
		60, "thumbs/orig.jpg", "videos/orig.mp4", 1024,
		1_700_000_000_000, "", "video", 0, false,
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
	if got.DearrowThumbPath != nil {
		t.Errorf("DearrowThumbPath should be nil, got %v", got.DearrowThumbPath)
	}
}

func TestSetDearrowData_RoundTrip(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_testchan', 'Test Channel', 'youtube')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.InsertVideo(
		"vid1", "youtube_testchan", "Original Title", "desc",
		60, "thumbs/orig.jpg", "videos/orig.mp4", 1024,
		1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}

	title := "Better"
	titleCasual := "Casual"
	thumbPath := "thumbnails/dearrow/vid1.jpg"
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
	if got.DearrowThumbPath == nil || *got.DearrowThumbPath != "thumbnails/dearrow/vid1.jpg" {
		t.Errorf("DearrowThumbPath = %v, want 'thumbnails/dearrow/vid1.jpg'", got.DearrowThumbPath)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs != 1_700_000_100_000 {
		t.Errorf("DearrowCheckedAtMs = %v, want 1700000100000", got.DearrowCheckedAtMs)
	}
}

func TestSetDearrowData_NilClearsField(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_testchan', 'Test Channel', 'youtube')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.InsertVideo(
		"vid1", "youtube_testchan", "Original Title", "desc",
		60, "thumbs/orig.jpg", "videos/orig.mp4", 1024,
		1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}

	// First call: set all fields.
	title := "Better"
	titleCasual := "Casual"
	thumbPath := "thumbnails/dearrow/vid1.jpg"
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
	if got.DearrowThumbPath != nil {
		t.Errorf("DearrowThumbPath should be nil after clear, got %v", got.DearrowThumbPath)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs != 1_700_000_200_000 {
		t.Errorf("DearrowCheckedAtMs = %v, want 1700000200000", got.DearrowCheckedAtMs)
	}
}

func TestListVideosNeedingDearrow(t *testing.T) {
	d := openFreshTestDB(t)

	// nowMs is a fixed reference point.
	nowMs := int64(1_700_000_000_000)

	// Channels: one YouTube, one Twitter.
	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_ychan', 'YouTube Channel', 'youtube')`); err != nil {
		t.Fatalf("insert youtube channel: %v", err)
	}
	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('twitter_tchan', 'Twitter Channel', 'twitter')`); err != nil {
		t.Fatalf("insert twitter channel: %v", err)
	}

	insertVideo := func(videoID, channelID string, publishedAtMs int64) {
		t.Helper()
		if err := d.InsertVideo(
			videoID, channelID, "Title", "desc",
			60, "thumbs/t.jpg", "videos/v.mp4", 1024,
			publishedAtMs, "", "video", 0, false,
		); err != nil {
			t.Fatalf("InsertVideo %s: %v", videoID, err)
		}
	}

	// y_never: YouTube, dearrow_checked_at NULL → expected.
	insertVideo("y_never", "youtube_ychan", nowMs-dayMsDearrow)

	// y_stale_young: YouTube, checked 2d ago, no data, published 1d ago → expected.
	insertVideo("y_stale_young", "youtube_ychan", nowMs-dayMsDearrow)
	if err := d.MarkDearrowChecked("y_stale_young", nowMs-2*dayMsDearrow); err != nil {
		t.Fatalf("MarkDearrowChecked y_stale_young: %v", err)
	}

	// y_stale_old: YouTube, checked 1d ago, no data, published 30d ago → excluded (too old).
	insertVideo("y_stale_old", "youtube_ychan", nowMs-30*dayMsDearrow)
	if err := d.MarkDearrowChecked("y_stale_old", nowMs-dayMsDearrow); err != nil {
		t.Fatalf("MarkDearrowChecked y_stale_old: %v", err)
	}

	// y_has_data: YouTube, checked 1h ago, has dearrow_title → excluded.
	insertVideo("y_has_data", "youtube_ychan", nowMs-dayMsDearrow)
	daTitle := "Community Title"
	if err := d.SetDearrowData("y_has_data", &daTitle, nil, nil, nowMs-3_600_000); err != nil {
		t.Fatalf("SetDearrowData y_has_data: %v", err)
	}

	// t_never: Twitter, never checked → excluded (wrong platform).
	insertVideo("t_never", "twitter_tchan", nowMs-dayMsDearrow)

	ids, err := d.ListVideosNeedingDearrow(nowMs, 100)
	if err != nil {
		t.Fatalf("ListVideosNeedingDearrow: %v", err)
	}

	// Build a set for order-independent comparison.
	got := make(map[string]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}

	want := map[string]bool{
		"y_never":       true,
		"y_stale_young": true,
	}

	for id := range want {
		if !got[id] {
			t.Errorf("expected %q in result, but it was missing; full result: %v", id, ids)
		}
	}
	for id := range got {
		if !want[id] {
			t.Errorf("unexpected %q in result; full result: %v", id, ids)
		}
	}
}

// TestListVideosNeedingDearrow_CheckedAtExactlyOneDayAgoIsExcluded proves
// the retry threshold is "strictly more than 24h ago", not "at least 24h".
// A video checked exactly dayMsDearrow ago should not appear until more time passes.
func TestListVideosNeedingDearrow_CheckedAtExactlyOneDayAgoIsExcluded(t *testing.T) {
	d := openFreshTestDB(t)
	nowMs := int64(1_700_000_000_000)

	if err := d.ExecRaw(`INSERT INTO channels (channel_id, name, platform) VALUES ('youtube_ychan', 'YouTube Channel', 'youtube')`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.InsertVideo(
		"y_boundary", "youtube_ychan", "Title", "desc",
		60, "thumbs/t.jpg", "videos/v.mp4", 1024,
		nowMs-2*dayMsDearrow, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}
	if err := d.MarkDearrowChecked("y_boundary", nowMs-dayMsDearrow); err != nil {
		t.Fatalf("MarkDearrowChecked: %v", err)
	}
	ids, err := d.ListVideosNeedingDearrow(nowMs, 100)
	if err != nil {
		t.Fatalf("ListVideosNeedingDearrow: %v", err)
	}
	for _, id := range ids {
		if id == "y_boundary" {
			t.Errorf("y_boundary should NOT be eligible at exactly dayMs ago (strict <)")
		}
	}
}
