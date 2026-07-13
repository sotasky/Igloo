package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
)

func TestExtractPublishedAt(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		want     int64
	}{
		{
			name:     "nil metadata",
			metadata: nil,
			want:     0,
		},
		{
			name:     "empty metadata",
			metadata: map[string]any{},
			want:     0,
		},
		{
			name:     "unix timestamp",
			metadata: map[string]any{"timestamp": float64(1700000000)},
			want:     1700000000 * 1000,
		},
		{
			name:     "release_timestamp takes priority",
			metadata: map[string]any{"release_timestamp": float64(1700000000), "upload_date": "20240101"},
			want:     1700000000 * 1000,
		},
		{
			name:     "zero timestamp skipped",
			metadata: map[string]any{"timestamp": float64(0), "upload_date": "20240315"},
			want:     1710460800 * 1000, // 2024-03-15 UTC midnight
		},
		{
			name:     "eight digit date",
			metadata: map[string]any{"upload_date": "20240315"},
			want:     1710460800 * 1000,
		},
		{
			name:     "ISO date",
			metadata: map[string]any{"release_date": "2024-03-15"},
			want:     1710460800 * 1000,
		},
		{
			name:     "ISO datetime",
			metadata: map[string]any{"published_at": "2024-03-15T10:30:00Z"},
			want:     1710498600 * 1000,
		},
		{
			name:     "ISO datetime no Z",
			metadata: map[string]any{"created_at": "2024-03-15T10:30:00"},
			want:     1710498600 * 1000,
		},
		{
			name:     "integer timestamp",
			metadata: map[string]any{"timestamp": int64(1700000000)},
			want:     1700000000 * 1000,
		},
		{
			name:     "non-numeric timestamp ignored",
			metadata: map[string]any{"timestamp": "not-a-number", "upload_date": "20240101"},
			want:     1704067200 * 1000, // 2024-01-01 UTC midnight
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPublishedAt(tt.metadata)
			if got != tt.want {
				t.Errorf("extractPublishedAt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPublishedAtForJobFallsBackToQueuedPublishedAt(t *testing.T) {
	const publishedAtMs int64 = 1714475201000
	got := publishedAtForJob(map[string]any{"title": "Queued date"}, db.DownloadWork{PublishedAtMs: publishedAtMs})
	if got != publishedAtMs {
		t.Fatalf("publishedAtForJob fallback = %d, want %d", got, publishedAtMs)
	}

	got = publishedAtForJob(map[string]any{"timestamp": float64(1700000000)}, db.DownloadWork{PublishedAtMs: publishedAtMs})
	if got != 1700000000*1000 {
		t.Fatalf("publishedAtForJob metadata priority = %d", got)
	}
}

func TestVideoDescriptionFromMetadataFallsBackToGalleryDLDesc(t *testing.T) {
	const fullCaption = "full TikTok caption #tag @creator"

	got := videoDescriptionFromMetadata(map[string]any{
		"desc": fullCaption,
	})

	if got != fullCaption {
		t.Fatalf("videoDescriptionFromMetadata = %q, want %q", got, fullCaption)
	}
}

func TestVideoTitleFromMetadataUsesFullCaptionWhenQueuedTitleIsTruncated(t *testing.T) {
	const fullCaption = "full TikTok caption #tag @creator"

	got := videoTitleFromMetadata(
		map[string]any{"desc": fullCaption},
		"full TikTok caption #tag...",
	)

	if got != fullCaption {
		t.Fatalf("videoTitleFromMetadata = %q, want %q", got, fullCaption)
	}
}

func TestVideoTitleFromMetadataKeepsNonTruncatedQueuedTitle(t *testing.T) {
	got := videoTitleFromMetadata(
		map[string]any{"desc": "long social caption body"},
		"Short title",
	)

	if got != "Short title" {
		t.Fatalf("videoTitleFromMetadata = %q, want Short title", got)
	}
}

func TestResolveFormatString(t *testing.T) {
	tests := []struct {
		platform string
		quality  string
		want     string
	}{
		{"tiktok", "", "bv*+ba/bv*/b"},
		{"tiktok", "1080p", "bv*+ba/bv*/b"},
		{"tiktok", "best", "bv*+ba/bv*/b"},
		{"instagram", "", "bv*+ba/bv*/b"},
		{"instagram", "720p", "bv*+ba/bv*/b"},
		{"youtube", "2160p", "bestvideo[height<=2160]+bestaudio/best[height<=2160]/best"},
		{"youtube", "1440p", "bestvideo[height<=1440]+bestaudio/best[height<=1440]/best"},
		{"youtube", "1080p", "bestvideo[height<=1080]+bestaudio/best[height<=1080]/best"},
		{"youtube", "720p", "bestvideo[height<=720]+bestaudio/best[height<=720]/best"},
		{"youtube", "480p", "bestvideo[height<=480]+bestaudio/best[height<=480]/best"},
		{"youtube", "best", "best"},
		{"youtube", "", "bestvideo[height<=1080]+bestaudio/best[height<=1080]/best"},
		{"youtube", "unknown", "bestvideo[height<=1080]+bestaudio/best[height<=1080]/best"},
	}

	for _, tt := range tests {
		name := tt.platform + "/" + tt.quality
		if tt.quality == "" {
			name = tt.platform + "/default"
		}
		t.Run(name, func(t *testing.T) {
			got := resolveFormatString(tt.platform, tt.quality)
			if got != tt.want {
				t.Errorf("resolveFormatString(%q, %q) = %q, want %q", tt.platform, tt.quality, got, tt.want)
			}
		})
	}
}

func TestBuildSourceURL(t *testing.T) {
	tests := []struct {
		platform string
		sourceID string
		videoID  string
		want     string
	}{
		{
			platform: "youtube",
			sourceID: "UC_some_channel",
			videoID:  "dQw4w9WgXcQ",
			want:     "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		},
		{
			platform: "tiktok",
			sourceID: "user_example",
			videoID:  "7234567890123456789",
			want:     "https://www.tiktok.com/@user_example/video/7234567890123456789",
		},
		{
			platform: "tiktok",
			sourceID: "user_example",
			videoID:  "tiktok_story_sample",
			want:     "https://www.tiktok.com/@user_example/video/sample",
		},
		{
			platform: "instagram",
			sourceID: "user_example",
			videoID:  "instagram_reel_ABC123",
			want:     "https://www.instagram.com/reel/ABC123/",
		},
		{
			platform: "instagram",
			sourceID: "user_example",
			videoID:  "instagram_post_DEF456",
			want:     "https://www.instagram.com/p/DEF456/",
		},
		{
			platform: "instagram",
			sourceID: "user_example",
			videoID:  "instagram_story_sample",
			want:     "https://www.instagram.com/stories/user_example/sample/",
		},
		{
			platform: "unknown",
			sourceID: "some_channel",
			videoID:  "abc123",
			want:     "https://www.youtube.com/watch?v=abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			got := buildSourceURL(tt.platform, tt.sourceID, tt.videoID)
			if got != tt.want {
				t.Errorf("buildSourceURL(%q, %q, %q) = %q, want %q",
					tt.platform, tt.sourceID, tt.videoID, got, tt.want)
			}
		})
	}
}

func TestCookiesForAcceptsDomainNamedInstagramCookieFile(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, "www.instagram.com_cookies.txt")
	if err := os.WriteFile(cookiePath, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	m := &Manager{
		cfg: &config.Config{CookiesDir: dir},
		db:  newTestWorkerDB(t),
	}
	gotFile, gotBrowser := m.cookiesFor("instagram")
	if gotFile != cookiePath {
		t.Fatalf("cookiesFor instagram file = %q, want %q", gotFile, cookiePath)
	}
	if gotBrowser != "" {
		t.Fatalf("cookiesFor instagram browser = %q, want empty", gotBrowser)
	}
}

func TestCookiesForSkipsDisabledCookieFile(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, "twitter_cookies.txt")
	if err := os.WriteFile(cookiePath, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	d := newTestWorkerDB(t)
	if err := d.SetSetting("cookies_twitter_enabled", "0"); err != nil {
		t.Fatalf("SetSetting enabled: %v", err)
	}

	m := &Manager{
		cfg: &config.Config{CookiesDir: dir},
		db:  d,
	}
	gotFile, gotBrowser := m.cookiesFor("twitter")
	if gotFile != "" {
		t.Fatalf("cookiesFor disabled twitter file = %q, want empty", gotFile)
	}
	if gotBrowser != "" {
		t.Fatalf("cookiesFor disabled twitter browser = %q, want empty", gotBrowser)
	}
}

func TestCookiesForFallsBackToBrowserWhenCookieFileDisabled(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, "instagram_cookies.txt")
	if err := os.WriteFile(cookiePath, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	d := newTestWorkerDB(t)
	if err := d.SetSetting("cookies_instagram_enabled", "0"); err != nil {
		t.Fatalf("SetSetting enabled: %v", err)
	}
	if err := d.SetSetting("cookies_instagram_browser", "firefox"); err != nil {
		t.Fatalf("SetSetting browser: %v", err)
	}

	m := &Manager{
		cfg: &config.Config{CookiesDir: dir},
		db:  d,
	}
	gotFile, gotBrowser := m.cookiesFor("instagram")
	if gotFile != "" {
		t.Fatalf("cookiesFor disabled instagram file = %q, want empty", gotFile)
	}
	if gotBrowser != "firefox" {
		t.Fatalf("cookiesFor instagram browser = %q, want firefox", gotBrowser)
	}
}

func TestCookieFileAndBrowserForKeepsBrowserFallbackWithCookieFile(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, "instagram_cookies.txt")
	if err := os.WriteFile(cookiePath, []byte("# stale\n"), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	d := newTestWorkerDB(t)
	if err := d.SetSetting("cookies_instagram_browser", "firefox"); err != nil {
		t.Fatalf("SetSetting browser: %v", err)
	}

	m := &Manager{
		cfg: &config.Config{CookiesDir: dir},
		db:  d,
	}
	gotFile, gotBrowser := m.cookieFileAndBrowserFor("instagram")
	if gotFile != cookiePath {
		t.Fatalf("cookieFileAndBrowserFor file = %q, want %q", gotFile, cookiePath)
	}
	if gotBrowser != "firefox" {
		t.Fatalf("cookieFileAndBrowserFor browser = %q, want firefox", gotBrowser)
	}
}

func TestDownloadPoolLeaseOwnerIsProcessScoped(t *testing.T) {
	got := downloadPoolLeaseOwner()
	if got == "" || got == "downloadpool:legacy" {
		t.Fatalf("downloadPoolLeaseOwner = %q, want process scoped non-legacy owner", got)
	}
}

func TestStartDownloadWorkLeaseRenewalExtendsOwnedJob(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now()
	job := claimDownloadWorkForTest(
		t, d, "youtube_sample_channel", "youtube_sample_channel",
		"sample_video_lease", db.DownloadLaneCurrent, "download-current", now,
	)
	initialLease := time.Now().Add(10 * time.Millisecond).UnixMilli()
	if err := d.ExecRaw(`UPDATE download_queue SET lease_until_ms=? WHERE video_id=?`, initialLease, job.VideoID); err != nil {
		t.Fatalf("shorten initial lease: %v", err)
	}

	oldDuration := downloadPoolLeaseDuration
	oldInterval := downloadPoolLeaseRenewInterval
	downloadPoolLeaseDuration = 40 * time.Millisecond
	downloadPoolLeaseRenewInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		downloadPoolLeaseDuration = oldDuration
		downloadPoolLeaseRenewInterval = oldInterval
	})

	m := &Manager{db: d}
	stop := m.startDownloadWorkLeaseRenewal(context.Background(), job)
	if stop == nil {
		t.Fatal("startDownloadWorkLeaseRenewal returned nil")
	}
	defer stop()

	deadline := time.After(250 * time.Millisecond)
	for {
		var leaseUntil int64
		if err := d.QueryRow(`SELECT lease_until_ms FROM download_queue WHERE video_id=?`, job.VideoID).Scan(&leaseUntil); err != nil {
			t.Fatalf("query lease: %v", err)
		}
		if leaseUntil > initialLease {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("lease was not renewed, lease_until_ms=%d", leaseUntil)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestFailDownloadJobRetriesRecoverableFailures(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantKind string
	}{
		{
			name:     "auth",
			err:      errors.New("login required; cookies missing"),
			wantKind: download.ErrorKindAuth,
		},
		{
			name:     "http_403",
			err:      &download.HTTPStatusError{StatusCode: 403, URL: "https://example.invalid/video"},
			wantKind: download.ErrorKindPermanentHTTP,
		},
		{
			name:     "rate_limit",
			err:      &download.HTTPStatusError{StatusCode: 429, URL: "https://example.invalid/video"},
			wantKind: download.ErrorKindRateLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestWorkerDB(t)
			now := time.Now()
			videoID := "sample_video_" + tt.name
			job := claimDownloadWorkForTest(
				t, d, "youtube_sample_channel", "youtube_sample_channel",
				videoID, db.DownloadLaneCurrent, "download-current", now,
			)
			m := &Manager{db: d}
			m.failDownloadJob(job, tt.err)

			var status, kind, owner string
			var retries int
			var nextAttempt, leaseUntil int64
			if err := d.QueryRow(`
				SELECT status, retry_count, COALESCE(next_attempt_at_ms,0),
				       COALESCE(last_error_kind,''), lease_owner, lease_until_ms
				FROM download_queue WHERE video_id=?
			`, videoID).Scan(&status, &retries, &nextAttempt, &kind, &owner, &leaseUntil); err != nil {
				t.Fatalf("query download queue row: %v", err)
			}
			if status != "pending" || retries != 1 || kind != tt.wantKind || owner != "" || leaseUntil != 0 {
				t.Fatalf("row = status=%q retries=%d kind=%q owner=%q lease=%d, want pending retry=1 kind=%q with cleared lease",
					status, retries, kind, owner, leaseUntil, tt.wantKind)
			}
			if nextAttempt <= now.UnixMilli() {
				t.Fatalf("next_attempt_at_ms = %d, want after %d", nextAttempt, now.UnixMilli())
			}
		})
	}
}

func TestFailDownloadJobRetriesGalleryDLUnavailableResult(t *testing.T) {
	bin := t.TempDir()
	tool := filepath.Join(bin, "gallery-dl")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'Requested post not available\\n'\n"), 0o755); err != nil {
		t.Fatalf("write gallery-dl: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, producerErr := (&download.GalleryDLWrapper{Runner: download.CommandRunner{}}).DownloadCompleted(
		context.Background(),
		"https://www.tiktok.com/@sample_handle/video/sample_video",
		t.TempDir(),
		"sample_video",
		"",
	)
	if producerErr == nil {
		t.Fatal("gallery-dl unavailable result returned no error")
	}
	var statusErr *download.HTTPStatusError
	if errors.As(producerErr, &statusErr) {
		t.Fatalf("textual unavailable result fabricated HTTP status %d", statusErr.StatusCode)
	}

	d := newTestWorkerDB(t)
	now := time.Now()
	job := claimDownloadWorkForTest(
		t, d, "tiktok_sample_channel", "tiktok_sample_channel",
		"sample_video_unavailable", db.DownloadLaneCurrent, "download-current", now,
	)
	(&Manager{db: d}).failDownloadJob(job, fmt.Errorf("download: %w", producerErr))

	var status, kind, leaseOwner string
	var retries int
	var nextAttempt, leaseUntil int64
	if err := d.QueryRow(`
		SELECT status, retry_count, next_attempt_at_ms, last_error_kind, lease_owner, lease_until_ms
		FROM download_queue WHERE video_id = ?
	`, job.VideoID).Scan(&status, &retries, &nextAttempt, &kind, &leaseOwner, &leaseUntil); err != nil {
		t.Fatalf("query retried download work: %v", err)
	}
	if status != "pending" || retries != 1 || kind != download.ErrorKindNotFound ||
		nextAttempt <= now.UnixMilli() || leaseOwner != "" || leaseUntil != 0 {
		t.Fatalf("retried work = status=%q retries=%d next=%d kind=%q owner=%q lease=%d",
			status, retries, nextAttempt, kind, leaseOwner, leaseUntil)
	}
}

func TestFailDownloadJobBlocksNotFoundWork(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now()
	videoID := "sample_video_missing"
	job := claimDownloadWorkForTest(
		t, d, "youtube_sample_channel", "youtube_sample_channel",
		videoID, db.DownloadLaneCurrent, "download-current", now,
	)
	m := &Manager{db: d}
	m.failDownloadJob(job, fmt.Errorf("download: %w", download.WithOperationContext(
		&download.HTTPStatusError{StatusCode: 404, URL: "https://example.invalid/video"},
		"http", "",
	)))

	var status, kind, reason, leaseOwner string
	var leaseUntil int64
	if err := d.QueryRow(`
		SELECT status, last_error_kind, last_error, lease_owner, lease_until_ms
		FROM download_queue WHERE video_id=?
	`, videoID).Scan(&status, &kind, &reason, &leaseOwner, &leaseUntil); err != nil {
		t.Fatalf("query blocked download work: %v", err)
	}
	if status != "blocked" || kind != download.ErrorKindNotFound || reason == "" || leaseOwner != "" || leaseUntil != 0 {
		t.Fatalf("blocked work = status=%q kind=%q reason=%q owner=%q lease=%d", status, kind, reason, leaseOwner, leaseUntil)
	}
}

func TestDownloadPlatformBackoffSkipsDueWork(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now()
	firstID := "instagram_post_rate_first"
	secondID := "instagram_post_rate_second"
	if err := d.FollowChannel("instagram_sample_channel"); err != nil {
		t.Fatalf("FollowChannel: %v", err)
	}
	_, err := d.ReconcileVideoDesires(db.VideoDesireSnapshot{
		SourceChannelID: "instagram_sample_channel",
		Component:       "posts",
		Items: []db.VideoDesire{
			{VideoID: firstID, OwnerChannelID: "instagram_sample_channel", SourcePosition: 0, Lane: db.DownloadLaneCurrent},
			{VideoID: secondID, OwnerChannelID: "instagram_sample_channel", SourcePosition: 1, Lane: db.DownloadLaneCurrent},
		},
	})
	if err != nil {
		t.Fatalf("ReconcileVideoDesires: %v", err)
	}

	m := &Manager{db: d}
	first, ok, err := m.claimDownloadWorkInLane("download-current", db.DownloadLaneCurrent, now)
	if err != nil {
		t.Fatalf("claim first work: %v", err)
	}
	if !ok || first.VideoID != firstID {
		t.Fatalf("first claim = (%q, %t), want %q", first.VideoID, ok, firstID)
	}
	m.failDownloadJob(first, &download.HTTPStatusError{StatusCode: 429, URL: "https://example.invalid/video"})

	claimed, ok, err := m.claimDownloadWorkInLane("download-current", db.DownloadLaneCurrent, now.Add(time.Second))
	if err != nil {
		t.Fatalf("claim during platform cooldown: %v", err)
	}
	if ok {
		t.Fatalf("claim during platform cooldown = %q, want none", claimed.VideoID)
	}

	direct, ok, err := d.ClaimDownloadWork("direct-proof", db.DownloadLaneCurrent, "instagram", now.Add(time.Second).UnixMilli(), time.Minute)
	if err != nil {
		t.Fatalf("direct claim: %v", err)
	}
	if !ok || direct.VideoID != secondID {
		t.Fatalf("direct claim = %+v, %v, want due work %q", direct, ok, secondID)
	}
}

func TestClaimDownloadWorkExcludesDisabledPlatforms(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now()
	if err := d.FollowChannel("instagram_sample_channel"); err != nil {
		t.Fatalf("FollowChannel: %v", err)
	}
	if _, err := d.ReconcileVideoDesires(db.VideoDesireSnapshot{
		SourceChannelID: "instagram_sample_channel",
		Component:       "posts",
		Items: []db.VideoDesire{{
			VideoID:        "instagram_sample_post_disabled",
			OwnerChannelID: "instagram_sample_channel",
			Lane:           db.DownloadLaneCurrent,
		}},
	}); err != nil {
		t.Fatalf("ReconcileVideoDesires: %v", err)
	}

	m := &Manager{
		db:  d,
		cfg: &config.Config{EnabledPlatformSet: map[string]bool{"youtube": true}},
	}
	if work, ok, err := m.claimDownloadWorkInLane("download-current", db.DownloadLaneCurrent, now); err != nil {
		t.Fatalf("claim disabled work: %v", err)
	} else if ok {
		t.Fatalf("claimed disabled work: %+v", work)
	}

	work, ok, err := d.ClaimDownloadWork("direct-proof", db.DownloadLaneCurrent, "instagram", now.UnixMilli(), time.Minute)
	if err != nil {
		t.Fatalf("direct claim: %v", err)
	}
	if !ok || work.VideoID != "instagram_sample_post_disabled" {
		t.Fatalf("direct claim = (%q, %t), want pending disabled work", work.VideoID, ok)
	}
}

func claimDownloadWorkForTest(
	t *testing.T,
	d *db.DB,
	sourceChannelID, ownerChannelID, videoID string,
	lane db.DownloadLane,
	leaseOwner string,
	now time.Time,
) db.DownloadWork {
	t.Helper()
	if err := d.FollowChannel(sourceChannelID); err != nil {
		t.Fatalf("FollowChannel: %v", err)
	}
	if _, err := d.ReconcileVideoDesires(db.VideoDesireSnapshot{
		SourceChannelID: sourceChannelID,
		Component:       "direct",
		Items: []db.VideoDesire{{
			VideoID: videoID, OwnerChannelID: ownerChannelID,
			Title: "Sample video", Lane: lane,
		}},
	}); err != nil {
		t.Fatalf("ReconcileVideoDesires: %v", err)
	}
	platform := platformFromDownloadChannelID(ownerChannelID)
	if platform == "" {
		platform = "youtube"
	}
	work, ok, err := d.ClaimDownloadWork(leaseOwner, lane, platform, now.UnixMilli(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimDownloadWork: %v", err)
	}
	if !ok {
		t.Fatal("ClaimDownloadWork returned no work")
	}
	return work
}

func TestParseDateString(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"", 0},
		{"  ", 0},
		{"20240315", 1710460800 * 1000},
		{"2024-03-15", 1710460800 * 1000},
		{"2024-03-15T10:30:00Z", 1710498600 * 1000},
		{"2024-03-15T10:30:00", 1710498600 * 1000},
		{"2024-03-15 10:30:00", 1710498600 * 1000},
		{"garbage", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDateString(tt.input)
			if got != tt.want {
				t.Errorf("parseDateString(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
