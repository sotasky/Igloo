package db

import (
	"errors"
	"testing"
	"time"
)

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

func TestChannelQueueClaimNextFiltersPlatformAndPriority(t *testing.T) {
	d := openFreshTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, last_checked, created_at)
		VALUES
			('youtube_sample_channel_claim_low', 'YouTube Low', 'youtube', 0, 1),
			('tiktok_sample_channel_claim_high', 'TikTok High', 'tiktok', 0, 1),
			('instagram_sample_channel_claim_skip', 'Instagram Skip', 'instagram', 0, 1)
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.AddChannelToQueue("youtube_sample_channel_claim_low", 1); err != nil {
		t.Fatalf("AddChannelToQueue youtube: %v", err)
	}
	if err := d.AddChannelToQueue("tiktok_sample_channel_claim_high", 10); err != nil {
		t.Fatalf("AddChannelToQueue tiktok: %v", err)
	}
	if err := d.AddChannelToQueue("instagram_sample_channel_claim_skip", 20); err != nil {
		t.Fatalf("AddChannelToQueue instagram: %v", err)
	}

	claimed, ok, err := d.ClaimNextChannelQueue([]string{"youtube", "tiktok"})
	if err != nil {
		t.Fatalf("ClaimNextChannelQueue: %v", err)
	}
	if !ok {
		t.Fatal("ClaimNextChannelQueue returned no row")
	}
	if claimed.ChannelID != "tiktok_sample_channel_claim_high" || claimed.Platform != "tiktok" || claimed.Status != "processing" {
		t.Fatalf("claimed = %+v, want tiktok processing row", claimed)
	}

	entries, err := d.GetPendingChannelQueue(10)
	if err != nil {
		t.Fatalf("GetPendingChannelQueue: %v", err)
	}
	for _, entry := range entries {
		if entry.ChannelID == claimed.ChannelID {
			t.Fatalf("claimed row still pending: %+v", entry)
		}
	}
}

func TestChannelQueueEnqueueDoesNotResetProcessing(t *testing.T) {
	d := openFreshTestDB(t)
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform, last_checked, created_at)
		VALUES ('youtube_sample_channel_processing', 'YouTube Processing', 'youtube', 0, 1)
	`); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := d.AddChannelToQueue("youtube_sample_channel_processing", 1); err != nil {
		t.Fatalf("AddChannelToQueue: %v", err)
	}
	if _, ok, err := d.ClaimNextChannelQueue([]string{"youtube"}); err != nil || !ok {
		t.Fatalf("ClaimNextChannelQueue ok=%v err=%v", ok, err)
	}
	if err := d.AddChannelToQueue("youtube_sample_channel_processing", 10); err != nil {
		t.Fatalf("AddChannelToQueue processing: %v", err)
	}

	var status string
	var priority int
	if err := d.QueryRow(`SELECT status, priority FROM channel_queue WHERE channel_id='youtube_sample_channel_processing'`).Scan(&status, &priority); err != nil {
		t.Fatalf("query channel_queue: %v", err)
	}
	if status != "processing" || priority != 1 {
		t.Fatalf("processing enqueue changed row to status=%q priority=%d, want processing/1", status, priority)
	}
}

func TestResetStaleChannelQueueItems(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO channel_queue (channel_id, status, priority, added_at, started_at)
		VALUES
			('youtube_sample_channel_stale', 'processing', 0, ?, ?),
			('youtube_sample_channel_active', 'processing', 0, ?, ?)
	`, now, now-int64(time.Hour/time.Millisecond), now, now); err != nil {
		t.Fatalf("insert queue rows: %v", err)
	}

	n, err := d.ResetStaleChannelQueueItems(30 * time.Minute)
	if err != nil {
		t.Fatalf("ResetStaleChannelQueueItems: %v", err)
	}
	if n != 1 {
		t.Fatalf("reset count = %d, want 1", n)
	}

	var stale, active string
	if err := d.QueryRow(`SELECT status FROM channel_queue WHERE channel_id='youtube_sample_channel_stale'`).Scan(&stale); err != nil {
		t.Fatalf("query stale: %v", err)
	}
	if err := d.QueryRow(`SELECT status FROM channel_queue WHERE channel_id='youtube_sample_channel_active'`).Scan(&active); err != nil {
		t.Fatalf("query active: %v", err)
	}
	if stale != "pending" || active != "processing" {
		t.Fatalf("statuses = stale:%s active:%s, want pending/processing", stale, active)
	}
}

func TestResetProcessingChannelQueueItems(t *testing.T) {
	d := openFreshTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO channel_queue (channel_id, status, priority, added_at, started_at)
		VALUES
			('youtube_sample_channel_startup_a', 'processing', 0, ?, ?),
			('youtube_sample_channel_startup_b', 'pending', 0, ?, 0)
	`, now, now, now); err != nil {
		t.Fatalf("insert queue rows: %v", err)
	}

	n, err := d.ResetProcessingChannelQueueItems()
	if err != nil {
		t.Fatalf("ResetProcessingChannelQueueItems: %v", err)
	}
	if n != 1 {
		t.Fatalf("reset count = %d, want 1", n)
	}

	var processing, pending string
	if err := d.QueryRow(`SELECT status FROM channel_queue WHERE channel_id='youtube_sample_channel_startup_a'`).Scan(&processing); err != nil {
		t.Fatalf("query processing: %v", err)
	}
	if err := d.QueryRow(`SELECT status FROM channel_queue WHERE channel_id='youtube_sample_channel_startup_b'`).Scan(&pending); err != nil {
		t.Fatalf("query pending: %v", err)
	}
	if processing != "pending" || pending != "pending" {
		t.Fatalf("statuses = processing:%s pending:%s, want pending/pending", processing, pending)
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
		INSERT INTO channel_follows (channel_id, followed_at)
		VALUES
			('tiktok_followed_refresh_a', 1),
			('youtube_followed_refresh_c', 1)
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
		`INSERT INTO video_repost_sources (video_id, reposter_channel_id, first_seen_at_ms, updated_at_ms) VALUES
			('tag_old', 'instagram_followed', 1, 1),
			('tag_keep', 'instagram_followed', 2, 2)`,
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
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES ('instagram_introducer', 1)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at) VALUES
			('protected_old', 'instagram_owner', 'instagram_reel', 'Protected Old', 100),
			('unprotected_middle', 'instagram_owner', 'instagram_reel', 'Unprotected Middle', 200),
			('newest', 'instagram_owner', 'instagram_reel', 'Newest', 300)`,
		`INSERT INTO video_repost_sources (video_id, reposter_channel_id, first_seen_at_ms, updated_at_ms)
		 VALUES ('protected_old', 'instagram_introducer', 400, 400)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := d.SetSetting("instagram_include_tagged_default", "true"); err != nil {
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
		`INSERT INTO channel_follows (channel_id, followed_at) VALUES
			('instagram_followed', 1),
			('instagram_other', 1)`,
		`INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at) VALUES
			('own_old', 'instagram_followed', 'instagram_reel', 'Own Old', 100),
			('own_keep', 'instagram_followed', 'instagram_reel', 'Own Keep', 200),
			('own_bookmarked', 'instagram_followed', 'instagram_reel', 'Own Bookmarked', 250),
			('tag_old', 'instagram_owner', 'instagram_reel', 'Tag Old', 300),
			('tag_keep', 'instagram_owner', 'instagram_reel', 'Tag Keep', 400),
			('tag_other', 'instagram_owner', 'instagram_reel', 'Tag Other', 500)`,
		`INSERT INTO bookmarks (video_id, bookmarked_at) VALUES ('own_bookmarked', 1)`,
		`INSERT INTO video_repost_sources (video_id, reposter_channel_id, first_seen_at_ms, updated_at_ms) VALUES
			('tag_old', 'instagram_followed', 1, 1),
			('tag_keep', 'instagram_followed', 2, 2),
			('tag_other', 'instagram_other', 3, 3)`,
	} {
		if err := d.ExecRaw(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := d.SetSetting("instagram_include_tagged_default", "true"); err != nil {
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
	now := time.Now().UnixMilli()
	claimed, err := d.ClaimDownloadBatchWithLease(LeaseOptions{
		Owner:   "download-remove",
		NowMs:   now,
		LeaseMs: int64(time.Minute / time.Millisecond),
		Limit:   1,
	})
	if err != nil {
		t.Fatalf("ClaimDownloadBatchWithLease: %v", err)
	}
	if len(claimed) != 1 || claimed[0].VideoID != videoID {
		t.Fatalf("claimed = %+v, want %s", claimed, videoID)
	}

	if err := d.RemoveFromDownloadQueue(videoID, "download-remove"); err != nil {
		t.Fatalf("RemoveFromDownloadQueue: %v", err)
	}

	// Should not appear in a pending claim
	claimedAfter, err := d.ClaimDownloadBatch(100)
	if err != nil {
		t.Fatalf("ClaimDownloadBatch: %v", err)
	}
	for _, r := range claimedAfter {
		if r.VideoID == videoID {
			t.Errorf("removed video %q should not appear in queue", videoID)
		}
	}
}

func TestDownloadQueueTerminalUpdatesRequireCurrentLeaseOwner(t *testing.T) {
	tests := []struct {
		name string
		run  func(*DB, string, int64) error
	}{
		{
			name: "status",
			run: func(d *DB, videoID string, now int64) error {
				return d.UpdateDownloadQueueStatus(videoID, "download-stale", "failed", "login required", 3, "auth", "permanent", 0, now)
			},
		},
		{
			name: "delete",
			run: func(d *DB, videoID string, now int64) error {
				return d.RemoveFromDownloadQueue(videoID, "download-stale")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := openWritableTestDB(t)
			now := time.Now().UnixMilli()
			videoID := "dlq_terminal_" + tt.name
			if err := d.ExecRaw(`
				INSERT INTO download_queue
					(video_id, channel_id, title, status, retry_count, lease_owner, lease_until_ms, next_attempt_at_ms, added_at)
				VALUES (?, 'youtube_test_channel', 'Terminal Lease', 'processing', 2, 'download-current', ?, 0, ?)
			`, videoID, now+60000, now); err != nil {
				t.Fatalf("insert download queue row: %v", err)
			}

			err := tt.run(d, videoID, now)
			if !errors.Is(err, ErrQueueLeaseNotHeld) {
				t.Fatalf("%s stale owner error = %v, want ErrQueueLeaseNotHeld", tt.name, err)
			}

			var status, owner, kind, strategy, msg string
			var retries int
			var leaseUntil, nextAttempt int64
			if err := d.QueryRow(`
				SELECT status, retry_count, COALESCE(next_attempt_at_ms,0),
				       COALESCE(last_error_kind,''), COALESCE(last_error_strategy,''),
				       COALESCE(error,''), COALESCE(lease_owner,''), COALESCE(lease_until_ms,0)
				FROM download_queue WHERE video_id=?
			`, videoID).Scan(&status, &retries, &nextAttempt, &kind, &strategy, &msg, &owner, &leaseUntil); err != nil {
				t.Fatalf("query download queue row: %v", err)
			}
			if status != "processing" || retries != 2 || nextAttempt != 0 || kind != "" || strategy != "" || msg != "" || owner != "download-current" || leaseUntil == 0 {
				t.Fatalf("stale %s changed row: status=%q retries=%d next=%d kind=%q strategy=%q msg=%q owner=%q lease=%d",
					tt.name, status, retries, nextAttempt, kind, strategy, msg, owner, leaseUntil)
			}
		})
	}
}

func TestDownloadQueueStatusPersistsErrorKindAndStrategy(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		kind         string
		strategy     string
		delay        time.Duration
		wantNext     bool
		wantCleared  bool
		wantRetryCnt int
	}{
		{
			name:         "rate limit retry",
			status:       "pending",
			kind:         "rate_limit",
			strategy:     "retry",
			delay:        time.Hour,
			wantNext:     true,
			wantRetryCnt: 1,
		},
		{
			name:         "auth permanent",
			status:       "failed",
			kind:         "auth",
			strategy:     "permanent",
			wantRetryCnt: 1,
		},
		{
			name:         "success clears stale classification",
			status:       "completed",
			kind:         "",
			strategy:     "",
			wantCleared:  true,
			wantRetryCnt: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := openWritableTestDB(t)
			now := time.Now().UnixMilli()
			videoID := "dlq_classification_" + tt.name
			if err := d.ExecRaw(`
				INSERT INTO download_queue
					(video_id, channel_id, title, status, retry_count, error,
					 last_error_kind, last_error_strategy, lease_owner, lease_until_ms,
					 next_attempt_at_ms, added_at)
				VALUES (?, 'youtube_test_channel', 'Classification', 'processing', 0,
				        'old error', 'temporary', 'retry', 'download-current', ?, 0, ?)
			`, videoID, now+60000, now); err != nil {
				t.Fatalf("insert download queue row: %v", err)
			}

			if err := d.UpdateDownloadQueueStatus(videoID, "download-current", tt.status, "classified error", tt.wantRetryCnt, tt.kind, tt.strategy, tt.delay, now); err != nil {
				t.Fatalf("UpdateDownloadQueueStatus: %v", err)
			}

			var status, kind, strategy, msg, owner string
			var retries int
			var nextAttempt, leaseUntil int64
			if err := d.QueryRow(`
				SELECT status, retry_count, COALESCE(next_attempt_at_ms,0),
				       COALESCE(last_error_kind,''), COALESCE(last_error_strategy,''),
				       COALESCE(error,''), COALESCE(lease_owner,''), COALESCE(lease_until_ms,0)
				FROM download_queue WHERE video_id=?
			`, videoID).Scan(&status, &retries, &nextAttempt, &kind, &strategy, &msg, &owner, &leaseUntil); err != nil {
				t.Fatalf("query download queue row: %v", err)
			}
			if status != tt.status || retries != tt.wantRetryCnt || kind != tt.kind || strategy != tt.strategy || owner != "" || leaseUntil != 0 {
				t.Fatalf("row = status=%q retries=%d kind=%q strategy=%q owner=%q lease=%d, want status=%q retries=%d kind=%q strategy=%q cleared lease",
					status, retries, kind, strategy, owner, leaseUntil, tt.status, tt.wantRetryCnt, tt.kind, tt.strategy)
			}
			if tt.wantNext && nextAttempt != now+tt.delay.Milliseconds() {
				t.Fatalf("next_attempt_at_ms = %d, want %d", nextAttempt, now+tt.delay.Milliseconds())
			}
			if !tt.wantNext && nextAttempt != 0 {
				t.Fatalf("next_attempt_at_ms = %d, want 0", nextAttempt)
			}
			if tt.wantCleared {
				if msg != "" {
					t.Fatalf("completed row kept error %q", msg)
				}
			} else if msg != "classified error" {
				t.Fatalf("error = %q, want classified error", msg)
			}
		})
	}
}

func TestResetDownloadAuthFailuresForPlatform(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO channels (channel_id, name, platform) VALUES
			('instagram_sample', 'Instagram Sample', 'instagram'),
			('youtube_sample', 'YouTube Sample', 'youtube')
	`); err != nil {
		t.Fatalf("insert channels: %v", err)
	}
	if err := d.ExecRaw(`
		INSERT INTO download_queue
			(video_id, channel_id, title, status, retry_count, error,
			 last_error_kind, last_error_strategy, lease_owner, lease_until_ms,
			 next_attempt_at_ms, added_at, started_at, completed_at, tool, cookie_label)
		VALUES
			('sample_instagram_auth', 'instagram_sample', 'Auth Failed', 'failed', 3,
			 'login required', 'auth', 'permanent', '', 0, 0, ?, ?, ?, 'yt-dlp', 'stale.txt'),
			('sample_instagram_rate', 'instagram_sample', 'Rate Failed', 'failed', 5,
			 'rate limited', 'rate_limit', 'retry', '', 0, 0, ?, ?, ?, 'yt-dlp', 'browser:firefox'),
			('sample_youtube_auth', 'youtube_sample', 'Other Platform', 'failed', 2,
			 'login required', 'auth', 'permanent', '', 0, 0, ?, ?, ?, 'yt-dlp', 'stale.txt')
	`, now, now, now, now, now, now, now, now, now); err != nil {
		t.Fatalf("insert download queue: %v", err)
	}

	n, err := d.ResetDownloadAuthFailuresForPlatform("instagram")
	if err != nil {
		t.Fatalf("ResetDownloadAuthFailuresForPlatform: %v", err)
	}
	if n != 1 {
		t.Fatalf("affected = %d, want 1", n)
	}

	var status, kind, strategy, msg, tool, label string
	var retries int
	var started, completed int64
	if err := d.QueryRow(`
		SELECT status, retry_count, COALESCE(error,''), COALESCE(last_error_kind,''),
		       COALESCE(last_error_strategy,''), started_at, completed_at,
		       COALESCE(tool,''), COALESCE(cookie_label,'')
		  FROM download_queue
		 WHERE video_id='sample_instagram_auth'
	`).Scan(&status, &retries, &msg, &kind, &strategy, &started, &completed, &tool, &label); err != nil {
		t.Fatalf("query reset row: %v", err)
	}
	if status != "pending" || retries != 0 || msg != "" || kind != "" || strategy != "" || started != 0 || completed != 0 || tool != "" || label != "" {
		t.Fatalf("reset row = status=%q retries=%d msg=%q kind=%q strategy=%q started=%d completed=%d tool=%q label=%q",
			status, retries, msg, kind, strategy, started, completed, tool, label)
	}

	rows, err := d.conn.Query(`
		SELECT video_id, status, retry_count, COALESCE(last_error_kind,'')
		  FROM download_queue
		 WHERE video_id IN ('sample_instagram_rate', 'sample_youtube_auth')
		 ORDER BY video_id
	`)
	if err != nil {
		t.Fatalf("query untouched rows: %v", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		var videoID, rowStatus, rowKind string
		var rowRetries int
		if err := rows.Scan(&videoID, &rowStatus, &rowRetries, &rowKind); err != nil {
			t.Fatalf("scan untouched row: %v", err)
		}
		if rowStatus != "failed" {
			t.Fatalf("%s status = %q, want failed", videoID, rowStatus)
		}
		if videoID == "sample_instagram_rate" && (rowRetries != 5 || rowKind != "rate_limit") {
			t.Fatalf("%s row = retries=%d kind=%q, want rate failure untouched", videoID, rowRetries, rowKind)
		}
		if videoID == "sample_youtube_auth" && (rowRetries != 2 || rowKind != "auth") {
			t.Fatalf("%s row = retries=%d kind=%q, want other platform untouched", videoID, rowRetries, rowKind)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("untouched rows: %v", err)
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

	if err := d.ExecRaw(`UPDATE download_queue SET lease_until_ms=? WHERE video_id=?`, time.Now().UnixMilli()-1, videoID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	// Reset expired leased items
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

func TestClaimDownloadBatchWithLeaseExcludesActiveLease(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO download_queue
			(video_id, channel_id, title, status, next_attempt_at_ms, added_at)
		VALUES ('dlq_lease_001', 'youtube_test_channel', 'Lease Proof', 'pending', 0, ?)
	`, now); err != nil {
		t.Fatalf("insert download queue row: %v", err)
	}

	first, err := d.ClaimDownloadBatchWithLease(LeaseOptions{
		Owner:      "download-a",
		NowMs:      now,
		LeaseMs:    int64(time.Minute / time.Millisecond),
		Limit:      1,
		StatusFrom: "pending",
		StatusTo:   "processing",
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if len(first) != 1 || first[0].VideoID != "dlq_lease_001" {
		t.Fatalf("first claim = %+v, want dlq_lease_001", first)
	}

	second, err := d.ClaimDownloadBatchWithLease(LeaseOptions{
		Owner:      "download-b",
		NowMs:      now + 1,
		LeaseMs:    int64(time.Minute / time.Millisecond),
		Limit:      1,
		StatusFrom: "pending",
		StatusTo:   "processing",
	})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("active lease was claimed by another worker: %+v", second)
	}
}

func TestClaimDownloadBatchWithLeaseAllowsExpiredLease(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO download_queue
			(video_id, channel_id, title, status, lease_owner, lease_until_ms, next_attempt_at_ms, added_at)
		VALUES ('dlq_lease_expired', 'youtube_test_channel', 'Expired Lease', 'processing', 'download-a', ?, 0, ?)
	`, now-1, now); err != nil {
		t.Fatalf("insert download queue row: %v", err)
	}

	claimed, err := d.ClaimDownloadBatchWithLease(LeaseOptions{
		Owner:      "download-b",
		NowMs:      now,
		LeaseMs:    int64(time.Minute / time.Millisecond),
		Limit:      1,
		StatusFrom: "pending",
		StatusTo:   "processing",
	})
	if err != nil {
		t.Fatalf("claim expired: %v", err)
	}
	if len(claimed) != 1 || claimed[0].VideoID != "dlq_lease_expired" {
		t.Fatalf("claim expired = %+v, want dlq_lease_expired", claimed)
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
