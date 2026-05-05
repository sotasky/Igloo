package db

import "testing"

// TestSearchVideosFallback_MatchesOriginalTitle is the baseline: verifies the
// existing behavior didn't break.
func TestSearchVideosFallback_MatchesOriginalTitle(t *testing.T) {
	d := openFreshTestDB(t)
	seedSearchChannel(t, d, "UCy", "youtube")
	seedSearchVideo(t, d, "v1", "UCy", "linear algebra explained")
	seedSearchVideo(t, d, "v2", "UCy", "cooking pasta")

	got, err := d.searchVideosFallback("linear", 10)
	if err != nil {
		t.Fatalf("searchVideosFallback: %v", err)
	}
	if len(got) != 1 || got[0].VideoID != "v1" {
		t.Errorf("got %+v, want v1 only", got)
	}
}

// TestSearchVideosFallback_MatchesDearrowTitle is the new behavior.
func TestSearchVideosFallback_MatchesDearrowTitle(t *testing.T) {
	d := openFreshTestDB(t)
	seedSearchChannel(t, d, "UCy", "youtube")

	// Original is clickbait, DeArrow community title is the real subject.
	realTitle := "Linear Algebra Explained"
	seedSearchVideo(t, d, "v1", "UCy", "10 THINGS YOU NEVER KNEW!!!")
	if err := d.SetDearrowData("v1", &realTitle, nil, nil, 1_700_000_000_000); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}

	got, err := d.searchVideosFallback("linear", 10)
	if err != nil {
		t.Fatalf("searchVideosFallback: %v", err)
	}
	found := false
	for _, v := range got {
		if v.VideoID == "v1" {
			found = true
		}
	}
	if !found {
		t.Errorf("v1 should have matched via dearrow_title, got %+v", got)
	}
}

// TestSearchVideosFallback_MatchesDearrowCasualTitle covers the third column.
func TestSearchVideosFallback_MatchesDearrowCasualTitle(t *testing.T) {
	d := openFreshTestDB(t)
	seedSearchChannel(t, d, "UCy", "youtube")
	casual := "funny cat video"
	seedSearchVideo(t, d, "v1", "UCy", "Original Title")
	if err := d.SetDearrowData("v1", nil, &casual, nil, 1_700_000_000_000); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}

	got, err := d.searchVideosFallback("funny", 10)
	if err != nil {
		t.Fatalf("searchVideosFallback: %v", err)
	}
	found := false
	for _, v := range got {
		if v.VideoID == "v1" {
			found = true
		}
	}
	if !found {
		t.Errorf("v1 should have matched via dearrow_title_casual")
	}
}

// TestSearchVideosFallback_RanksPrefixMatchFirst verifies ordering still
// prefers prefix matches over word-starts, across all three title columns.
func TestSearchVideosFallback_RanksPrefixMatchFirst(t *testing.T) {
	d := openFreshTestDB(t)
	seedSearchChannel(t, d, "UCy", "youtube")
	// v1: word-start match on original title.
	seedSearchVideo(t, d, "v1", "UCy", "best linear algebra course")
	// v2: prefix match on dearrow_title.
	da := "linear algebra intro"
	seedSearchVideo(t, d, "v2", "UCy", "zzz unrelated original")
	if err := d.SetDearrowData("v2", &da, nil, nil, 1_700_000_000_000); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}

	got, err := d.searchVideosFallback("linear", 10)
	if err != nil {
		t.Fatalf("searchVideosFallback: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("want both v1 and v2, got %+v", got)
	}
	if got[0].VideoID != "v2" {
		t.Errorf("want v2 first (prefix match on dearrow_title), got %s", got[0].VideoID)
	}
}

// TestSearchVideosFallback_ScansDearrowFields ensures the returned Video
// has its dearrow fields loaded — Task 10's resolver depends on this.
func TestSearchVideosFallback_ScansDearrowFields(t *testing.T) {
	d := openFreshTestDB(t)
	seedSearchChannel(t, d, "UCy", "youtube")
	da := "Real Title"
	casual := "Casual Title"
	thumb := "thumbnails/dearrow/v1.jpg"
	seedSearchVideo(t, d, "v1", "UCy", "Original Clickbait real")
	if err := d.SetDearrowData("v1", &da, &casual, &thumb, 1_700_000_000_000); err != nil {
		t.Fatalf("SetDearrowData: %v", err)
	}

	got, err := d.searchVideosFallback("real", 10)
	if err != nil {
		t.Fatalf("searchVideosFallback: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no matches")
	}
	v := got[0]
	if v.DearrowTitle == nil || *v.DearrowTitle != "Real Title" {
		t.Errorf("DearrowTitle = %v, want 'Real Title'", v.DearrowTitle)
	}
	if v.DearrowTitleCasual == nil || *v.DearrowTitleCasual != "Casual Title" {
		t.Errorf("DearrowTitleCasual = %v", v.DearrowTitleCasual)
	}
	if v.DearrowThumbPath == nil || *v.DearrowThumbPath != thumb {
		t.Errorf("DearrowThumbPath = %v", v.DearrowThumbPath)
	}
}

// Test helpers.
func seedSearchChannel(t *testing.T, d *DB, channelID, platform string) {
	t.Helper()
	_, _ = d.conn.Exec(`INSERT OR IGNORE INTO channels (channel_id, name, platform) VALUES (?, ?, ?)`,
		channelID, "Test Channel", platform)
}

func seedSearchVideo(t *testing.T, d *DB, videoID, channelID, title string) {
	t.Helper()
	if err := d.InsertVideo(
		videoID, channelID, title, "",
		60, "", "videos/"+videoID+".mp4", 1024,
		1_700_000_000_000, "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo %s: %v", videoID, err)
	}
}

func TestSearchChannelsFast(t *testing.T) {
	d := openTestDB(t)
	// Check if FTS5 index exists
	var ready int
	d.conn.QueryRow("SELECT COALESCE((SELECT 1 FROM settings WHERE key='search_index_ready' AND value='1'),0)").Scan(&ready)
	if ready == 0 {
		t.Skip("search index not ready")
	}

	results, err := d.SearchChannelsFast("test", 10)
	if err != nil {
		t.Fatalf("SearchChannelsFast: %v", err)
	}
	_ = results
}

func TestSearchVideosFast(t *testing.T) {
	d := openTestDB(t)
	results, err := d.SearchVideosFast("test", 20)
	if err != nil {
		t.Fatalf("SearchVideosFast: %v", err)
	}
	_ = results
}

func TestSearchFeedItems(t *testing.T) {
	d := openTestDB(t)
	results, err := d.SearchFeedItems("test", 20)
	if err != nil {
		t.Fatalf("SearchFeedItems: %v", err)
	}
	_ = results
}
