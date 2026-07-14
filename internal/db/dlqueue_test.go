package db

import (
	"errors"
	"testing"
	"time"
)

func TestReconcileVideoDesiresReplacesOneComponent(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		source = "instagram_sample_source"
		owner  = "instagram_sample_author"
	)
	seedVideoDesireChannels(t, d, source, owner)

	reels := VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "reels",
		Items: []VideoDesire{
			{VideoID: "sample_shared", OwnerChannelID: owner, Title: "Shared", PublishedAtMs: 30, SourcePosition: 0, Lane: DownloadLaneBackfill},
			{VideoID: "instagram_reel_sample", OwnerChannelID: owner, Title: "Reel", PublishedAtMs: 20, SourcePosition: 1, Lane: DownloadLaneBackfill},
		},
	}
	added, err := d.ReconcileVideoDesires(reels)
	if err != nil || added != 2 {
		t.Fatalf("reconcile reels = %d, %v", added, err)
	}
	posts := VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "posts",
		Items: []VideoDesire{
			{VideoID: "sample_shared", OwnerChannelID: owner, Title: "New shared title", PublishedAtMs: 40, SourcePosition: 0, Lane: DownloadLaneCurrent},
			{VideoID: "sample_post", OwnerChannelID: owner, Title: "Post", PublishedAtMs: 10, SourcePosition: 1, Lane: DownloadLaneCurrent},
		},
	}
	added, err = d.ReconcileVideoDesires(posts)
	if err != nil || added != 1 {
		t.Fatalf("reconcile posts = %d, %v", added, err)
	}

	reels.Items = reels.Items[1:]
	if _, err := d.ReconcileVideoDesires(reels); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE video_id = 'sample_shared'`); got != 1 {
		t.Fatalf("shared roots = %d", got)
	}
	var ownerID, title string
	var published int64
	if err := d.QueryRow(`
		SELECT owner_channel_id, title, published_at_ms
		FROM download_queue WHERE video_id = 'sample_shared'
	`).Scan(&ownerID, &title, &published); err != nil {
		t.Fatal(err)
	}
	if ownerID != owner || title != "New shared title" || published != 40 {
		t.Fatalf("queue payload = owner %q title %q published %d", ownerID, title, published)
	}
}

func TestReconcileVideoDesiresReportsReactivatedWork(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "youtube_sample_source"
	seedVideoDesireChannels(t, d, source)
	snapshot := VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "direct",
		Items: []VideoDesire{{
			VideoID: "sample_reactivated", OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}
	if added, err := d.ReconcileVideoDesires(snapshot); err != nil || added != 1 {
		t.Fatalf("initial reconcile = %d, %v", added, err)
	}
	snapshot.Items = nil
	if added, err := d.ReconcileVideoDesires(snapshot); err != nil || added != 0 {
		t.Fatalf("remove desire = %d, %v", added, err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM download_queue WHERE video_id = 'sample_reactivated'`); got != 1 {
		t.Fatalf("dormant queue rows = %d", got)
	}

	snapshot.Items = []VideoDesire{{
		VideoID: "sample_reactivated", OwnerChannelID: source,
		SourcePosition: 0, Lane: DownloadLaneCurrent,
	}}
	if added, err := d.ReconcileVideoDesires(snapshot); err != nil || added != 1 {
		t.Fatalf("reactivated reconcile = %d, %v", added, err)
	}
}

func TestReconcileVideoDesiresReactivatesBlockedWorkAfterComponentTransfer(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "youtube_sample_source"
	seedVideoDesireChannels(t, d, source)
	snapshot := VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "uploads",
		Items: []VideoDesire{{
			VideoID: "sample_transferred", OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}
	if _, err := d.ReconcileVideoDesires(snapshot); err != nil {
		t.Fatal(err)
	}
	job, ok, err := d.ClaimDownloadWork("sample-worker", DownloadLaneCurrent, "youtube", 100, time.Second)
	if err != nil || !ok {
		t.Fatalf("claim = %+v, %v, %v", job, ok, err)
	}
	if err := d.BlockDownloadWork(job.VideoID, job.LeaseOwner, "not found"); err != nil {
		t.Fatal(err)
	}
	if added, err := d.ReconcileVideoDesireSource(VideoDesireSourceSnapshot{
		SourceChannelID: source,
		Components: []VideoDesireSnapshot{
			{
				Component: "shorts",
				Items: []VideoDesire{{
					VideoID: "sample_transferred", OwnerChannelID: source,
					SourcePosition: 0, Lane: DownloadLaneCurrent,
				}},
			},
			{Component: "uploads"},
		},
	}); err != nil || added != 1 {
		t.Fatalf("transfer reconcile = %d, %v", added, err)
	}
	var status string
	var retryCount int
	if err := d.QueryRow(`
		SELECT status, retry_count FROM download_queue WHERE video_id = 'sample_transferred'
	`).Scan(&status, &retryCount); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || retryCount != 0 {
		t.Fatalf("reactivated queue = %q retry %d", status, retryCount)
	}
}

func TestReconcileVideoDesiresRejectsMissingOwner(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "youtube_sample_source"
	seedVideoDesireChannels(t, d, source)

	added, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "direct",
		Items: []VideoDesire{{
			VideoID: "sample_missing_owner", OwnerChannelID: "youtube_missing_owner",
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	})
	if err == nil || added != 0 {
		t.Fatalf("missing owner reconcile = %d, %v", added, err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE video_id = 'sample_missing_owner'`); got != 0 {
		t.Fatalf("missing-owner desires = %d", got)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM download_queue WHERE video_id = 'sample_missing_owner'`); got != 0 {
		t.Fatalf("missing-owner queue rows = %d", got)
	}
}

func TestClaimDownloadWorkDerivesLaneAndSource(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "tiktok_sample_source"
	seedVideoDesireChannels(t, d, source)

	backfill := VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "archive",
		Items: []VideoDesire{
			{VideoID: "sample_shared", OwnerChannelID: source, SourcePosition: 2, Lane: DownloadLaneBackfill},
			{VideoID: "sample_tail", OwnerChannelID: source, SourcePosition: 5, Lane: DownloadLaneBackfill},
		},
	}
	current := VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "current",
		Items: []VideoDesire{
			{VideoID: "sample_shared", OwnerChannelID: source, SourcePosition: 0, Lane: DownloadLaneCurrent},
		},
	}
	for _, snapshot := range []VideoDesireSnapshot{backfill, current} {
		if _, err := d.ReconcileVideoDesires(snapshot); err != nil {
			t.Fatal(err)
		}
	}

	job, ok, err := d.ClaimDownloadWork("worker-current", DownloadLaneCurrent, "tiktok", 100, time.Second)
	if err != nil || !ok {
		t.Fatalf("current claim = %+v, %v, %v", job, ok, err)
	}
	if job.VideoID != "sample_shared" || job.SourceChannelID != source || job.SourceComponent != "current" ||
		job.OwnerChannelID != source || job.Lane != DownloadLaneCurrent {
		t.Fatalf("current work = %+v", job)
	}
	if err := d.RetryDownloadWork(job.VideoID, job.LeaseOwner, "transient", "retry", time.Millisecond, 100); err != nil {
		t.Fatal(err)
	}
	current.Items = nil
	if _, err := d.ReconcileVideoDesires(current); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.ClaimDownloadWork("wrong-lane", DownloadLaneCurrent, "tiktok", 102, time.Second); err != nil || ok {
		t.Fatalf("demoted current claim ok=%v err=%v", ok, err)
	}
	job, ok, err = d.ClaimDownloadWork("worker-backfill", DownloadLaneBackfill, "tiktok", 102, time.Second)
	if err != nil || !ok || job.VideoID != "sample_shared" || job.SourceComponent != "archive" || job.Lane != DownloadLaneBackfill {
		t.Fatalf("backfill claim = %+v, %v, %v", job, ok, err)
	}
	if _, ok, err := d.ClaimDownloadWork("wrong-platform", DownloadLaneBackfill, "youtube", 102, time.Second); err != nil || ok {
		t.Fatalf("platform filter ok=%v err=%v", ok, err)
	}
}

func TestVideoDesireFreshnessIgnoresObservationTime(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "youtube_sample_source"
	seedVideoDesireChannels(t, d, source)
	for videoID, publishedAt := range map[string]int64{
		"sample_canonical_old": 100,
		"sample_canonical_new": 200,
	} {
		seedTestVideo(t, d, videoID, source)
		if err := d.ExecRaw(`UPDATE videos SET published_at = ? WHERE video_id = ?`, publishedAt, videoID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "direct",
		Items: []VideoDesire{
			{VideoID: "sample_canonical_old", OwnerChannelID: source, SourcePosition: 0, Lane: DownloadLaneBackfill},
			{VideoID: "sample_canonical_new", OwnerChannelID: source, SourcePosition: 1, Lane: DownloadLaneBackfill},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		INSERT INTO video_repost_sources
			(video_id, reposter_channel_id, reposted_at_ms, first_seen_at_ms, updated_at_ms)
		VALUES
			('sample_canonical_old', ?, 0, 1000, 1000),
			('sample_canonical_new', ?, 0, 1, 1)
	`, source, source); err != nil {
		t.Fatal(err)
	}
	window, err := d.GetVideoDesireWindow(source, "direct")
	if err != nil {
		t.Fatal(err)
	}
	freshness := make(map[string]int64, len(window))
	for _, item := range window {
		freshness[item.VideoID] = item.FreshnessAtMs
	}
	if freshness["sample_canonical_old"] != 100 || freshness["sample_canonical_new"] != 200 {
		t.Fatalf("window freshness = %+v", freshness)
	}
	if err := d.EnforceVideoDesireLimit(source, 1); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE source_channel_id = ? AND video_id = 'sample_canonical_new'`, source); got != 1 {
		t.Fatalf("newer canonical desire count = %d", got)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE source_channel_id = ? AND video_id = 'sample_canonical_old'`, source); got != 0 {
		t.Fatalf("older canonical desire count = %d", got)
	}
}

func TestDownloadWorkLeaseRetryAndBlock(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "instagram_sample_source"
	seedVideoDesireChannels(t, d, source)
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "posts",
		Items: []VideoDesire{{
			VideoID: "sample_retry", OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	job, ok, err := d.ClaimDownloadWork("worker-a", DownloadLaneCurrent, "instagram", 100, 10*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("first claim = %+v, %v, %v", job, ok, err)
	}
	if _, ok, err := d.ClaimDownloadWork("worker-b", DownloadLaneCurrent, "instagram", 105, time.Second); err != nil || ok {
		t.Fatalf("active lease was reclaimed: ok=%v err=%v", ok, err)
	}
	if err := d.RenewDownloadWorkLease(job.VideoID, "worker-b", 105, time.Second); !errors.Is(err, ErrQueueLeaseNotHeld) {
		t.Fatalf("wrong-owner renew = %v", err)
	}
	if err := d.RetryDownloadWork(job.VideoID, job.LeaseOwner, "auth", "credential unavailable", time.Hour, 105); err != nil {
		t.Fatal(err)
	}
	if n, err := d.WakeDownloadAuthRetriesForPlatform("instagram"); err != nil || n != 1 {
		t.Fatalf("wake auth = %d, %v", n, err)
	}
	job, ok, err = d.ClaimDownloadWork("worker-b", DownloadLaneCurrent, "instagram", 106, time.Second)
	if err != nil || !ok || job.RetryCount != 0 {
		t.Fatalf("woken claim = %+v, %v, %v", job, ok, err)
	}
	if err := d.BlockDownloadWork(job.VideoID, job.LeaseOwner, "not found"); err != nil {
		t.Fatal(err)
	}
	var status, kind, message string
	if err := d.QueryRow(`
		SELECT status, last_error_kind, last_error
		FROM download_queue WHERE video_id = ?
	`, job.VideoID).Scan(&status, &kind, &message); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || kind != "not_found" || message != "not found" {
		t.Fatalf("blocked row = %q %q %q", status, kind, message)
	}
	if _, ok, err := d.ClaimDownloadWork("worker-c", DownloadLaneCurrent, "instagram", 200, time.Second); err != nil || ok {
		t.Fatalf("blocked work was claimable: ok=%v err=%v", ok, err)
	}
}

func TestCompleteDownloadWorkRequiresReadyMediaAndOwnedLease(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		source = "youtube_sample_source"
		video  = "sample_completion"
	)
	seedVideoDesireChannels(t, d, source)
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "direct",
		Items: []VideoDesire{{
			VideoID: video, OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	job, ok, err := d.ClaimDownloadWork("worker-a", DownloadLaneCurrent, "youtube", 100, 10*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("claim = %+v, %v, %v", job, ok, err)
	}
	if err := d.CompleteDownloadWork(video, job.LeaseOwner); !errors.Is(err, ErrDownloadNotReady) {
		t.Fatalf("completion without media = %v", err)
	}
	expired, ok, err := d.ClaimDownloadWork("worker-b", DownloadLaneCurrent, "youtube", 111, time.Second)
	if err != nil || !ok || expired.LeaseOwner != "worker-b" {
		t.Fatalf("expired claim = %+v, %v, %v", expired, ok, err)
	}
	seedTestVideo(t, d, video, source)
	if err := d.CompleteDownloadWork(video, "worker-a"); !errors.Is(err, ErrQueueLeaseNotHeld) {
		t.Fatalf("stale completion = %v", err)
	}
	if err := d.CompleteDownloadWork(video, expired.LeaseOwner); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM download_queue WHERE video_id = ?`, video); got != 0 {
		t.Fatalf("completed rows = %d", got)
	}
}

func TestMaintainVideoRetentionOwnsQueueAndCanonicalCleanup(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "youtube_sample_source"
	seedVideoDesireChannels(t, d, source)
	nowMs := int64(10 * 24 * time.Hour / time.Millisecond)
	oldMs := nowMs - int64(3*24*time.Hour/time.Millisecond)
	if err := d.SetSetting("stories_window_hours", "48"); err != nil {
		t.Fatal(err)
	}

	for _, videoID := range []string{
		"sample_unrooted", "sample_bookmarked", "sample_liked", "sample_pinned",
		"sample_custom_source", "sample_expired_temp", "sample_expired_story",
		"sample_active_temp", "sample_desired_ready",
	} {
		seedTestVideo(t, d, videoID, source)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO bookmarks (video_id, bookmarked_at) VALUES ('sample_bookmarked', ?)`, []any{nowMs}},
		{`INSERT INTO feed_likes (tweet_id, liked_at) VALUES ('sample_liked', ?)`, []any{nowMs}},
		{`UPDATE videos SET is_pinned = 1 WHERE video_id = 'sample_pinned'`, nil},
		{`UPDATE videos SET source_kind = 'manual' WHERE video_id = 'sample_custom_source'`, nil},
		{`UPDATE videos SET is_temp = 1, downloaded_at = ? WHERE video_id = 'sample_expired_temp'`, []any{oldMs}},
		{`UPDATE videos SET source_kind = 'story', published_at = ? WHERE video_id = 'sample_expired_story'`, []any{oldMs}},
		{`UPDATE videos SET is_temp = 1, downloaded_at = ? WHERE video_id = 'sample_active_temp'`, []any{nowMs - int64(time.Hour/time.Millisecond)}},
	} {
		if err := d.ExecRaw(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at)
		VALUES ('sample_tweet_owned', 'twitter_sample_author', 'tweet', 'Post media', 1)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "direct",
		Items: []VideoDesire{{
			VideoID: "sample_desired_ready", OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	orphan := VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "orphan",
		Items: []VideoDesire{{
			VideoID: "sample_orphan_work", OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneBackfill,
		}},
	}
	if _, err := d.ReconcileVideoDesires(orphan); err != nil {
		t.Fatal(err)
	}
	orphan.Items = nil
	if _, err := d.ReconcileVideoDesires(orphan); err != nil {
		t.Fatal(err)
	}

	active := VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "active",
		Items: []VideoDesire{{
			VideoID: "sample_active_work", OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneBackfill,
		}},
	}
	if _, err := d.ReconcileVideoDesires(active); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.ClaimDownloadWork("worker-active", DownloadLaneBackfill, "youtube", nowMs, time.Hour); err != nil || !ok {
		t.Fatalf("active claim ok=%v err=%v", ok, err)
	}
	active.Items = nil
	if _, err := d.ReconcileVideoDesires(active); err != nil {
		t.Fatal(err)
	}

	collected, err := d.MaintainVideoRetention(nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if collected != 3 {
		t.Fatalf("collected = %d, want unrooted plus expired temp/story", collected)
	}
	for _, videoID := range []string{"sample_unrooted", "sample_expired_temp", "sample_expired_story"} {
		if got := testRowCount(t, d, `SELECT COUNT(*) FROM videos WHERE video_id = ?`, videoID); got != 0 {
			t.Fatalf("collected video %s remained", videoID)
		}
	}
	for _, videoID := range []string{
		"sample_bookmarked", "sample_liked", "sample_pinned", "sample_custom_source",
		"sample_active_temp", "sample_desired_ready", "sample_tweet_owned",
	} {
		if got := testRowCount(t, d, `SELECT COUNT(*) FROM videos WHERE video_id = ?`, videoID); got != 1 {
			t.Fatalf("protected video %s was removed", videoID)
		}
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM download_queue WHERE video_id = 'sample_orphan_work'`); got != 0 {
		t.Fatalf("orphan work remained: %d", got)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM download_queue WHERE video_id = 'sample_active_work' AND status = 'processing'`); got != 1 {
		t.Fatalf("active lease was removed: %d", got)
	}
}

func TestMaintainVideoRetentionDoesNotRewriteStableVideos(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "youtube_sample_source"
	seedVideoDesireChannels(t, d, source)
	seedTestVideo(t, d, "sample_stable", source)
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "direct",
		Items: []VideoDesire{{
			VideoID: "sample_stable", OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecRaw(`
		CREATE TABLE video_update_audit (video_id TEXT NOT NULL);
		CREATE TRIGGER test_video_update_audit
		AFTER UPDATE ON videos
		BEGIN
			INSERT INTO video_update_audit(video_id) VALUES (new.video_id);
		END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MaintainVideoRetention(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_update_audit`); got != 0 {
		t.Fatalf("stable retention rewrote %d videos", got)
	}
}

func TestMaintainVideoRetentionExpiresStoryDesiresAndPendingWork(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		source     = "tiktok_sample_source"
		oldStory   = "sample_story_old"
		freshStory = "sample_story_new"
	)
	seedVideoDesireChannels(t, d, source)
	if err := d.SetSetting("stories_window_hours", "48"); err != nil {
		t.Fatal(err)
	}
	nowMs := int64(10 * 24 * time.Hour / time.Millisecond)
	oldMs := nowMs - int64(3*24*time.Hour/time.Millisecond)
	freshMs := nowMs - int64(time.Hour/time.Millisecond)
	if err := d.ExecRaw(`
		INSERT INTO videos (video_id, channel_id, owner_kind, title, published_at, source_kind)
		VALUES (?, ?, 'tiktok_video', 'Stored story', ?, 'story')
	`, freshStory, source, freshMs); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "stories",
		Items: []VideoDesire{
			{VideoID: oldStory, OwnerChannelID: source, PublishedAtMs: oldMs, SourcePosition: 0, Lane: DownloadLaneCurrent},
			{VideoID: freshStory, OwnerChannelID: source, PublishedAtMs: freshMs, SourcePosition: 1, Lane: DownloadLaneCurrent},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var sourceKind string
	if err := d.QueryRow(`SELECT source_kind FROM videos WHERE video_id = ?`, freshStory).Scan(&sourceKind); err != nil {
		t.Fatal(err)
	}
	if sourceKind != "story" {
		t.Fatalf("reobserved story source kind = %q", sourceKind)
	}

	if _, err := d.MaintainVideoRetention(nowMs); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE video_id = ?`, oldStory); got != 0 {
		t.Fatalf("expired story desires = %d", got)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM download_queue WHERE video_id = ?`, oldStory); got != 0 {
		t.Fatalf("expired story work = %d", got)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE video_id = ?`, freshStory); got != 1 {
		t.Fatalf("fresh story desires = %d", got)
	}
}

func TestUnfollowStopsWorkAndKeepsHistoricalVideoOwnership(t *testing.T) {
	d := openWritableTestDB(t)
	const (
		first  = "youtube_sample_first"
		second = "youtube_sample_second"
		owner  = "youtube_sample_author"
		video  = "sample_shared_video"
	)
	seedVideoDesireChannels(t, d, first, second, owner)
	seedTestVideo(t, d, video, owner)
	for _, source := range []string{first, second} {
		if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
			SourceChannelID: source,
			Component:       "direct",
			Items: []VideoDesire{{
				VideoID: video, OwnerChannelID: owner,
				SourcePosition: 0, Lane: DownloadLaneCurrent,
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.UnfollowChannel(first); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MaintainVideoRetention(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE video_id = ?`, video); got != 2 {
		t.Fatalf("shared desires after first unfollow = %d", got)
	}
	if err := d.UnfollowChannel(second); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM videos WHERE video_id = ?`, video); got != 1 {
		t.Fatal("unfollow performed mutation-local canonical cleanup")
	}
	if delay, err := d.NextMediaWorkDelay(time.Now().UnixMilli(), []string{"youtube"}, false, DownloadLaneCurrent); err != nil || delay != 5*time.Minute {
		t.Fatalf("unfollowed work delay=%v err=%v", delay, err)
	}
	if _, ok, err := d.ClaimDownloadWork("worker", DownloadLaneCurrent, "youtube", time.Now().UnixMilli(), time.Minute); err != nil || ok {
		t.Fatalf("unfollowed work claim ok=%v err=%v", ok, err)
	}
	if _, err := d.MaintainVideoRetention(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM download_queue WHERE video_id = ?`, video); got != 0 {
		t.Fatalf("maintenance retained inactive work: %d", got)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE video_id = ?`, video); got != 2 {
		t.Fatalf("maintenance removed historical desires: %d", got)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM videos WHERE video_id = ?`, video); got != 1 {
		t.Fatalf("maintenance removed historical video: %d", got)
	}
	added, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: first,
		Component:       "direct",
		Items:           []VideoDesire{{VideoID: "sample_late", OwnerChannelID: owner, Lane: DownloadLaneCurrent}},
	})
	if err != nil || added != 0 || testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE source_channel_id = ?`, first) != 1 {
		t.Fatalf("unfollowed reconcile added=%d err=%v", added, err)
	}
}

func TestVideoDesireOwnerConflictUsesCanonicalOwner(t *testing.T) {
	d := openWritableTestDB(t)
	seedVideoDesireChannels(t, d, "tiktok_sample_source", "tiktok_sample_author", "tiktok_sample_second")
	initial := VideoDesireSnapshot{
		SourceChannelID: "tiktok_sample_source",
		Component:       "direct",
		Items: []VideoDesire{{
			VideoID: "sample_identity", OwnerChannelID: "tiktok_sample_author",
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}
	if _, err := d.ReconcileVideoDesires(initial); err != nil {
		t.Fatal(err)
	}
	initial.Items = []VideoDesire{
		{VideoID: "sample_new", OwnerChannelID: "tiktok_sample_author", SourcePosition: 0, Lane: DownloadLaneCurrent},
		{VideoID: "sample_identity", OwnerChannelID: "tiktok_sample_second", SourcePosition: 1, Lane: DownloadLaneCurrent},
	}
	if _, err := d.ReconcileVideoDesires(initial); err != nil {
		t.Fatal(err)
	}
	if got := testRowCount(t, d, `SELECT COUNT(*) FROM video_desires WHERE video_id = 'sample_new'`); got != 1 {
		t.Fatalf("new desire count = %d", got)
	}
	var owner string
	if err := d.QueryRow(`SELECT owner_channel_id FROM download_queue WHERE video_id = 'sample_identity'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "tiktok_sample_author" {
		t.Fatalf("canonical owner = %q", owner)
	}
}

func TestVideoDesireAuthoritativeOwnerUpgradesPendingWork(t *testing.T) {
	d := openWritableTestDB(t)
	const source = "instagram_sample_source"
	seedVideoDesireChannels(t, d, source, "instagram_sample_author")
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: source,
		Component:       "posts",
		Items: []VideoDesire{{
			VideoID: "instagram_sample_post", OwnerChannelID: source,
			SourcePosition: 0, Lane: DownloadLaneCurrent,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReconcileVideoDesireSource(VideoDesireSourceSnapshot{
		SourceChannelID: source,
		Components: []VideoDesireSnapshot{
			{Component: "posts"},
			{
				Component: "tagged",
				Items: []VideoDesire{{
					VideoID: "instagram_sample_post", OwnerChannelID: "instagram_sample_author",
					OwnerAuthoritative: true, SourcePosition: 0, Lane: DownloadLaneCurrent,
				}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := d.QueryRow(`
		SELECT owner_channel_id FROM download_queue WHERE video_id = 'instagram_sample_post'
	`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "instagram_sample_author" {
		t.Fatalf("canonical owner = %q", owner)
	}
}

func TestNextMediaWorkDelayIncludesDownloadLeases(t *testing.T) {
	d := openWritableTestDB(t)
	seedVideoDesireChannels(t, d, "youtube_sample_source")
	if _, err := d.ReconcileVideoDesires(VideoDesireSnapshot{
		SourceChannelID: "youtube_sample_source",
		Component:       "direct",
		Items: []VideoDesire{{
			VideoID: "sample_due", OwnerChannelID: "youtube_sample_source",
			SourcePosition: 0, Lane: DownloadLaneBackfill,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	delay, err := d.NextMediaWorkDelay(1000, []string{"youtube"}, true, DownloadLaneBackfill)
	if err != nil || delay != 0 {
		t.Fatalf("pending delay = %v, %v", delay, err)
	}
	if _, ok, err := d.ClaimDownloadWork("worker-a", DownloadLaneBackfill, "youtube", 1000, 500*time.Millisecond); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	delay, err = d.NextMediaWorkDelay(1100, []string{"youtube"}, true, DownloadLaneBackfill)
	if err != nil || delay != 400*time.Millisecond {
		t.Fatalf("leased delay = %v, %v", delay, err)
	}
}

func testRowCount(t *testing.T, d *DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := d.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func seedVideoDesireChannels(t *testing.T, d *DB, channelIDs ...string) {
	t.Helper()
	for _, channelID := range channelIDs {
		platform := "youtube"
		switch {
		case len(channelID) >= len("instagram_") && channelID[:len("instagram_")] == "instagram_":
			platform = "instagram"
		case len(channelID) >= len("tiktok_") && channelID[:len("tiktok_")] == "tiktok_":
			platform = "tiktok"
		}
		if err := d.ExecRaw(`
			INSERT OR IGNORE INTO channels (channel_id, name, platform, created_at)
			VALUES (?, 'Sample Channel', ?, 1)
		`, channelID, platform); err != nil {
			t.Fatalf("seed channel %s: %v", channelID, err)
		}
		if err := d.ExecRaw(`
			INSERT OR IGNORE INTO channel_follows (channel_id, followed_at)
			VALUES (?, 1)
		`, channelID); err != nil {
			t.Fatalf("seed follow %s: %v", channelID, err)
		}
	}
}
