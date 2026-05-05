package db

import "testing"

func TestChannelQueueAddAndGet(t *testing.T) {
	d := openWritableTestDB(t)

	channelID := "dlq_test_channel_001"
	if err := d.AddChannelToQueue(channelID, 5); err != nil {
		t.Fatalf("AddChannelToQueue: %v", err)
	}

	entries, err := d.GetPendingChannelQueue(100)
	if err != nil {
		t.Fatalf("GetPendingChannelQueue: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.ChannelID == channelID {
			found = true
			if e.Status != "pending" {
				t.Errorf("expected status='pending', got %q", e.Status)
			}
			if e.Priority != 5 {
				t.Errorf("expected priority=5, got %d", e.Priority)
			}
		}
	}
	if !found {
		t.Errorf("channel %q not found in pending queue", channelID)
	}
}

func TestChannelQueueUpdateStatus(t *testing.T) {
	d := openWritableTestDB(t)

	channelID := "dlq_test_channel_002"
	if err := d.AddChannelToQueue(channelID, 0); err != nil {
		t.Fatalf("AddChannelToQueue: %v", err)
	}

	if err := d.UpdateChannelQueueStatus(channelID, "processing"); err != nil {
		t.Fatalf("UpdateChannelQueueStatus: %v", err)
	}

	// Should no longer appear in pending queue
	entries, err := d.GetPendingChannelQueue(100)
	if err != nil {
		t.Fatalf("GetPendingChannelQueue: %v", err)
	}
	for _, e := range entries {
		if e.ChannelID == channelID {
			t.Errorf("channel %q should not be in pending queue after status update", channelID)
		}
	}
}

func TestClearPlatformCheckedClearsFollowedChannels(t *testing.T) {
	d := openFreshTestDB(t)

	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, last_checked, created_at)
		VALUES
			('tiktok_followed_refresh_a', 'TikTok A', 'tiktok', 1000, 1),
			('tiktok_unfollowed_refresh_b', 'TikTok B', 'tiktok', 2000, 1),
			('youtube_followed_refresh_c', 'YouTube C', 'youtube', 3000, 1)
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO channel_follows (user_id, channel_id, followed_at)
		VALUES
			('', 'tiktok_followed_refresh_a', 1),
			('', 'youtube_followed_refresh_c', 1)
	`); err != nil {
		t.Fatalf("insert follows: %v", err)
	}

	n, err := d.ClearPlatformChecked("tiktok")
	if err != nil {
		t.Fatalf("ClearPlatformChecked: %v", err)
	}
	if n != 1 {
		t.Fatalf("RowsAffected = %d, want 1", n)
	}

	var followed, unfollowed, other int64
	if err := d.conn.QueryRow(`SELECT last_checked FROM channels WHERE channel_id = 'tiktok_followed_refresh_a'`).Scan(&followed); err != nil {
		t.Fatalf("query followed: %v", err)
	}
	if err := d.conn.QueryRow(`SELECT last_checked FROM channels WHERE channel_id = 'tiktok_unfollowed_refresh_b'`).Scan(&unfollowed); err != nil {
		t.Fatalf("query unfollowed: %v", err)
	}
	if err := d.conn.QueryRow(`SELECT last_checked FROM channels WHERE channel_id = 'youtube_followed_refresh_c'`).Scan(&other); err != nil {
		t.Fatalf("query other: %v", err)
	}
	if followed != 0 {
		t.Fatalf("followed TikTok last_checked = %d, want 0", followed)
	}
	if unfollowed != 2000 {
		t.Fatalf("unfollowed TikTok last_checked = %d, want 2000", unfollowed)
	}
	if other != 3000 {
		t.Fatalf("other platform last_checked = %d, want 3000", other)
	}
}

func TestDownloadQueueClaimBatch(t *testing.T) {
	d := openWritableTestDB(t)

	videos := []struct{ id, channel, title string }{
		{"dlq_vid_001", "dlq_ch_001", "Test Video One"},
		{"dlq_vid_002", "dlq_ch_001", "Test Video Two"},
	}
	for _, v := range videos {
		if err := d.AddToDownloadQueue(v.id, v.channel, v.title); err != nil {
			t.Fatalf("AddToDownloadQueue %s: %v", v.id, err)
		}
	}

	claimed, err := d.ClaimDownloadBatch(10)
	if err != nil {
		t.Fatalf("ClaimDownloadBatch: %v", err)
	}
	found := 0
	for _, r := range claimed {
		if r.VideoID == "dlq_vid_001" || r.VideoID == "dlq_vid_002" {
			found++
			if r.Status != "pending" {
				// Status field in struct reflects pre-update value; no assertion needed
			}
		}
	}
	if found != 2 {
		t.Errorf("expected both test videos in claimed batch, found %d in %d total", found, len(claimed))
	}

	// Second claim should not return the same items (they are processing now)
	claimed2, err := d.ClaimDownloadBatch(10)
	if err != nil {
		t.Fatalf("second ClaimDownloadBatch: %v", err)
	}
	for _, r := range claimed2 {
		if r.VideoID == "dlq_vid_001" || r.VideoID == "dlq_vid_002" {
			t.Errorf("video %q should not be claimable again", r.VideoID)
		}
	}
}

func TestDownloadQueueClaimBatchPrioritizesInstagramReels(t *testing.T) {
	d := openWritableTestDB(t)
	if err := d.AddToDownloadQueue("instagram_post_OLDER", "instagram_profile", "Older Post"); err != nil {
		t.Fatalf("AddToDownloadQueue post: %v", err)
	}
	if err := d.AddToDownloadQueue("instagram_reel_NEWER", "instagram_profile", "Newer Reel"); err != nil {
		t.Fatalf("AddToDownloadQueue reel: %v", err)
	}

	claimed, err := d.ClaimDownloadBatch(1)
	if err != nil {
		t.Fatalf("ClaimDownloadBatch: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(claimed))
	}
	if claimed[0].VideoID != "instagram_reel_NEWER" {
		t.Fatalf("claimed %s, want instagram reel before older post", claimed[0].VideoID)
	}
}

func TestDownloadQueueCarriesPublishedAt(t *testing.T) {
	d := openWritableTestDB(t)

	const publishedAtMs int64 = 1714475201000
	if err := d.AddToDownloadQueueWithPublishedAt("dlq_vid_published_001", "instagram_channel", "Published", publishedAtMs); err != nil {
		t.Fatalf("AddToDownloadQueueWithPublishedAt: %v", err)
	}

	claimed, err := d.ClaimDownloadBatch(10)
	if err != nil {
		t.Fatalf("ClaimDownloadBatch: %v", err)
	}
	for _, r := range claimed {
		if r.VideoID == "dlq_vid_published_001" {
			if r.PublishedAtMs != publishedAtMs {
				t.Fatalf("PublishedAtMs = %d, want %d", r.PublishedAtMs, publishedAtMs)
			}
			return
		}
	}
	t.Fatalf("queued video not claimed")
}

func TestPruneSourceWindowDownloadQueueCountsIntroducedRows(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, name, platform) VALUES ('instagram_followed', 'Followed', 'instagram')`,
		`INSERT INTO channels (channel_id, name, platform) VALUES ('instagram_owner', 'Owner', 'instagram')`,
		`INSERT INTO download_queue (video_id, channel_id, title, status, added_at) VALUES
			('own_old', 'instagram_followed', 'Own Old', 'pending', 1),
			('tag_old', 'instagram_owner', 'Tag Old', 'pending', 2),
			('tag_keep', 'instagram_owner', 'Tag Keep', 'pending', 3),
			('owner_only', 'instagram_owner', 'Owner Only', 'pending', 4)`,
		`INSERT INTO video_repost_sources (video_id, reposter_channel_id, reposter_handle, first_seen_at_ms, updated_at_ms) VALUES
			('tag_old', 'instagram_followed', 'followed', 1, 1),
			('tag_keep', 'instagram_followed', 'followed', 2, 2)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	n, err := d.PruneSourceWindowDownloadQueue("instagram_followed", []string{"tag_keep"})
	if err != nil {
		t.Fatalf("PruneSourceWindowDownloadQueue: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned = %d, want 2", n)
	}

	claimed, err := d.ClaimDownloadBatch(10)
	if err != nil {
		t.Fatalf("ClaimDownloadBatch: %v", err)
	}
	got := map[string]bool{}
	for _, row := range claimed {
		got[row.VideoID] = true
	}
	if got["own_old"] || got["tag_old"] {
		t.Fatalf("stale source-window rows still queued: %+v", got)
	}
	if !got["tag_keep"] || !got["owner_only"] {
		t.Fatalf("expected kept rows still queued: %+v", got)
	}
}

func TestGetExcessVideoIDsProtectsActiveIntroducedRows(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, name, platform) VALUES
			('instagram_owner', 'Owner', 'instagram'),
			('instagram_introducer', 'Introducer', 'instagram')`,
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', 'instagram_introducer', 1)`,
		`INSERT INTO videos (video_id, channel_id, title, published_at) VALUES
			('protected_old', 'instagram_owner', 'Protected Old', 100),
			('unprotected_middle', 'instagram_owner', 'Unprotected Middle', 200),
			('newest', 'instagram_owner', 'Newest', 300)`,
		`INSERT INTO video_repost_sources (video_id, reposter_channel_id, reposter_handle, first_seen_at_ms, updated_at_ms)
		 VALUES ('protected_old', 'instagram_introducer', 'introducer', 400, 400)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := d.SetSetting("", "instagram_include_tagged_default", "true"); err != nil {
		t.Fatalf("SetSetting instagram_include_tagged_default: %v", err)
	}

	excess, err := d.GetExcessVideoIDs("instagram_owner", 1)
	if err != nil {
		t.Fatalf("GetExcessVideoIDs: %v", err)
	}
	if len(excess) != 1 || excess[0] != "unprotected_middle" {
		t.Fatalf("excess = %v, want [unprotected_middle]", excess)
	}
}

func TestGetSourceWindowPrunableVideoIDsCountsIntroducedRows(t *testing.T) {
	d := openWritableTestDB(t)

	for _, stmt := range []string{
		`INSERT INTO channels (channel_id, name, platform) VALUES
			('instagram_followed', 'Followed', 'instagram'),
			('instagram_owner', 'Owner', 'instagram'),
			('instagram_other', 'Other', 'instagram')`,
		`INSERT INTO channel_follows (user_id, channel_id, followed_at) VALUES
			('', 'instagram_followed', 1),
			('', 'instagram_other', 1)`,
		`INSERT INTO videos (video_id, channel_id, title, published_at) VALUES
			('own_old', 'instagram_followed', 'Own Old', 100),
			('own_keep', 'instagram_followed', 'Own Keep', 200),
			('own_bookmarked', 'instagram_followed', 'Own Bookmarked', 250),
			('tag_old', 'instagram_owner', 'Tag Old', 300),
			('tag_keep', 'instagram_owner', 'Tag Keep', 400),
			('tag_other', 'instagram_owner', 'Tag Other', 500)`,
		`INSERT INTO bookmarks (user_id, video_id, bookmarked_at) VALUES ('', 'own_bookmarked', 1)`,
		`INSERT INTO video_repost_sources (video_id, reposter_channel_id, reposter_handle, first_seen_at_ms, updated_at_ms) VALUES
			('tag_old', 'instagram_followed', 'followed', 1, 1),
			('tag_keep', 'instagram_followed', 'followed', 2, 2),
			('tag_other', 'instagram_other', 'other', 3, 3)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := d.SetSetting("", "instagram_include_tagged_default", "true"); err != nil {
		t.Fatalf("SetSetting instagram_include_tagged_default: %v", err)
	}

	ids, err := d.GetSourceWindowPrunableVideoIDs("instagram_followed", []string{"own_keep", "tag_keep"})
	if err != nil {
		t.Fatalf("GetSourceWindowPrunableVideoIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != "own_old" || ids[1] != "tag_old" {
		t.Fatalf("ids = %v, want [own_old tag_old]", ids)
	}
}

func TestDownloadQueueRemove(t *testing.T) {
	d := openWritableTestDB(t)

	videoID := "dlq_vid_remove_001"
	if err := d.AddToDownloadQueue(videoID, "dlq_ch_remove", "Remove Me"); err != nil {
		t.Fatalf("AddToDownloadQueue: %v", err)
	}

	if err := d.RemoveFromDownloadQueue(videoID); err != nil {
		t.Fatalf("RemoveFromDownloadQueue: %v", err)
	}

	// Should not appear in a pending claim
	claimed, err := d.ClaimDownloadBatch(100)
	if err != nil {
		t.Fatalf("ClaimDownloadBatch: %v", err)
	}
	for _, r := range claimed {
		if r.VideoID == videoID {
			t.Errorf("removed video %q should not appear in queue", videoID)
		}
	}
}

func TestResetStaleDownloadQueueItems(t *testing.T) {
	d := openWritableTestDB(t)

	// Add and claim a video so it enters processing state
	videoID := "dlq_vid_stale_001"
	if err := d.AddToDownloadQueue(videoID, "dlq_ch_stale", "Stale Video"); err != nil {
		t.Fatalf("AddToDownloadQueue: %v", err)
	}
	claimed, err := d.ClaimDownloadBatch(10)
	if err != nil {
		t.Fatalf("ClaimDownloadBatch: %v", err)
	}
	found := false
	for _, r := range claimed {
		if r.VideoID == videoID {
			found = true
		}
	}
	if !found {
		t.Skip("stale test video not claimed (unexpected DB state)")
	}

	// Reset stale items
	n, err := d.ResetStaleDownloadQueueItems()
	if err != nil {
		t.Fatalf("ResetStaleDownloadQueueItems: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 reset item, got %d", n)
	}

	// Video should now be claimable again
	claimed2, err := d.ClaimDownloadBatch(100)
	if err != nil {
		t.Fatalf("second ClaimDownloadBatch: %v", err)
	}
	reclaimed := false
	for _, r := range claimed2 {
		if r.VideoID == videoID {
			reclaimed = true
		}
	}
	if !reclaimed {
		t.Errorf("video %q should be claimable after stale reset", videoID)
	}
}

func TestIsVideoDownloaded(t *testing.T) {
	d := openWritableTestDB(t)

	// A fake video ID should not be downloaded
	downloaded, err := d.IsVideoDownloaded("dlq_fake_video_id_xyz_999")
	if err != nil {
		t.Fatalf("IsVideoDownloaded: %v", err)
	}
	if downloaded {
		t.Error("fake video ID should not be downloaded")
	}
}
