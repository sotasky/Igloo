package worker

import (
	"context"
	"errors"
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
	got := publishedAtForJob(map[string]any{"title": "Queued date"}, db.DownloadQueueRow{PublishedAtMs: publishedAtMs})
	if got != publishedAtMs {
		t.Fatalf("publishedAtForJob fallback = %d, want %d", got, publishedAtMs)
	}

	got = publishedAtForJob(map[string]any{"timestamp": float64(1700000000)}, db.DownloadQueueRow{PublishedAtMs: publishedAtMs})
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
	if err := d.SetSetting("", "cookies_twitter_enabled", "0"); err != nil {
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
	if err := d.SetSetting("", "cookies_instagram_enabled", "0"); err != nil {
		t.Fatalf("SetSetting enabled: %v", err)
	}
	if err := d.SetSetting("", "cookies_instagram_browser", "firefox"); err != nil {
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
	if err := d.SetSetting("", "cookies_instagram_browser", "firefox"); err != nil {
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

func TestStartDownloadQueueLeaseRenewalExtendsOwnedJob(t *testing.T) {
	d := newTestWorkerDB(t)
	now := time.Now().UnixMilli()
	videoID := "dlq_worker_lease_renew"
	if err := d.ExecRaw(`
		INSERT INTO download_queue
			(video_id, channel_id, title, status, lease_owner, lease_until_ms, next_attempt_at_ms, added_at)
		VALUES (?, 'youtube_test_channel', 'Renew Lease', 'processing', 'download-current', ?, 0, ?)
	`, videoID, now+10, now); err != nil {
		t.Fatalf("insert download queue row: %v", err)
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
	stop := m.startDownloadQueueLeaseRenewal(context.Background(), db.DownloadQueueRow{
		VideoID:    videoID,
		LeaseOwner: "download-current",
	})
	if stop == nil {
		t.Fatal("startDownloadQueueLeaseRenewal returned nil")
	}
	defer stop()

	deadline := time.After(250 * time.Millisecond)
	for {
		var leaseUntil int64
		if err := d.QueryRow(`SELECT COALESCE(lease_until_ms,0) FROM download_queue WHERE video_id=?`, videoID).Scan(&leaseUntil); err != nil {
			t.Fatalf("query lease: %v", err)
		}
		if leaseUntil > now+10 {
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

func TestFailDownloadJobPersistsClassification(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantKind   string
		wantStrat  string
		wantNext   bool
	}{
		{
			name:       "auth",
			err:        errors.New("login required; cookies missing"),
			wantStatus: "failed",
			wantKind:   download.ErrorKindAuth,
			wantStrat:  download.ErrorStrategyPermanent,
		},
		{
			name:       "permanent_http",
			err:        &download.HTTPStatusError{StatusCode: 403, URL: "https://example.invalid/video"},
			wantStatus: "failed",
			wantKind:   download.ErrorKindPermanentHTTP,
			wantStrat:  download.ErrorStrategyPermanent,
		},
		{
			name:       "rate_limit",
			err:        &download.HTTPStatusError{StatusCode: 429, URL: "https://example.invalid/video"},
			wantStatus: "pending",
			wantKind:   download.ErrorKindRateLimit,
			wantStrat:  download.ErrorStrategyRetry,
			wantNext:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newTestWorkerDB(t)
			now := time.Now().UnixMilli()
			videoID := "dlq_classify_" + tt.name
			if err := d.ExecRaw(`
				INSERT INTO download_queue
					(video_id, channel_id, title, status, retry_count, lease_owner, lease_until_ms, next_attempt_at_ms, added_at)
				VALUES (?, 'youtube_test_channel', 'Classify Failure', 'processing', 0, 'download-current', ?, 0, ?)
			`, videoID, now+60000, now); err != nil {
				t.Fatalf("insert download queue row: %v", err)
			}
			m := &Manager{db: d}
			m.failDownloadJob(db.DownloadQueueRow{
				VideoID:    videoID,
				RetryCount: 0,
				LeaseOwner: "download-current",
			}, tt.err)

			var status, kind, strategy, owner string
			var retries int
			var nextAttempt, leaseUntil int64
			if err := d.QueryRow(`
				SELECT status, retry_count, COALESCE(next_attempt_at_ms,0),
				       COALESCE(last_error_kind,''), COALESCE(last_error_strategy,''),
				       COALESCE(lease_owner,''), COALESCE(lease_until_ms,0)
				FROM download_queue WHERE video_id=?
			`, videoID).Scan(&status, &retries, &nextAttempt, &kind, &strategy, &owner, &leaseUntil); err != nil {
				t.Fatalf("query download queue row: %v", err)
			}
			if status != tt.wantStatus || retries != 1 || kind != tt.wantKind || strategy != tt.wantStrat || owner != "" || leaseUntil != 0 {
				t.Fatalf("row = status=%q retries=%d kind=%q strategy=%q owner=%q lease=%d, want status=%q retry=1 kind=%q strategy=%q cleared lease",
					status, retries, kind, strategy, owner, leaseUntil, tt.wantStatus, tt.wantKind, tt.wantStrat)
			}
			if tt.wantNext && nextAttempt <= now {
				t.Fatalf("next_attempt_at_ms = %d, want after %d", nextAttempt, now)
			}
			if !tt.wantNext && nextAttempt != 0 {
				t.Fatalf("next_attempt_at_ms = %d, want 0", nextAttempt)
			}
		})
	}
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

func TestPlatformSemFor(t *testing.T) {
	youtubeSem := platformSemFor("youtube")
	if cap(youtubeSem) != 3 {
		t.Errorf("youtube semaphore cap = %d, want 3", cap(youtubeSem))
	}

	tt := platformSemFor("tiktok")
	if cap(tt) != 1 {
		t.Errorf("tiktok semaphore cap = %d, want 1", cap(tt))
	}

	ig := platformSemFor("instagram")
	if cap(ig) != 1 {
		t.Errorf("instagram semaphore cap = %d, want 1", cap(ig))
	}

	// Unknown platform falls back to the youtube semaphore.
	unknown := platformSemFor("unknown")
	if cap(unknown) != cap(youtubeSem) {
		t.Errorf("unknown platform semaphore cap = %d, want %d", cap(unknown), cap(youtubeSem))
	}
}

func TestFindSiblingThumbnail(t *testing.T) {
	// Create a temp directory with a mock thumbnail.
	dir := t.TempDir()

	// No thumbnail exists.
	got := findSiblingThumbnail(dir+"/test_video.mp4", "test_video")
	if got != "" {
		t.Errorf("expected empty for missing thumbnail, got %q", got)
	}

	// Create a .jpg thumbnail.
	thumbPath := dir + "/test_video.jpg"
	if err := writeTestFile(thumbPath); err != nil {
		t.Fatal(err)
	}

	got = findSiblingThumbnail(dir+"/test_video.mp4", "test_video")
	if got != thumbPath {
		t.Errorf("findSiblingThumbnail() = %q, want %q", got, thumbPath)
	}
}

func TestLoadInfoJSON(t *testing.T) {
	dir := t.TempDir()

	// No file exists.
	got := loadInfoJSON(dir+"/vid.mp4", "vid")
	if got != nil {
		t.Errorf("expected nil for missing info.json, got %v", got)
	}

	// Write valid info.json.
	infoPath := dir + "/vid.info.json"
	if err := writeTestFileContent(infoPath, `{"title":"Test","duration":120}`); err != nil {
		t.Fatal(err)
	}

	got = loadInfoJSON(dir+"/vid.mp4", "vid")
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got["title"] != "Test" {
		t.Errorf("title = %v, want Test", got["title"])
	}
	if d, ok := got["duration"].(float64); !ok || d != 120 {
		t.Errorf("duration = %v, want 120", got["duration"])
	}

	// Write invalid JSON.
	invalidPath := dir + "/bad.info.json"
	if err := writeTestFileContent(invalidPath, `{not json}`); err != nil {
		t.Fatal(err)
	}
	got = loadInfoJSON(dir+"/bad.mp4", "bad")
	if got != nil {
		t.Errorf("expected nil for invalid JSON, got %v", got)
	}
}

func writeTestFile(path string) error {
	return writeTestFileContent(path, "test content")
}

func writeTestFileContent(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
