package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestToRelPath(t *testing.T) {
	tests := []struct {
		baseDir string
		absPath string
		want    string
	}{
		{"/data", "/data/media/twitter/user/file.jpg", "media/twitter/user/file.jpg"},
		{"/data", "/other/path/file.jpg", "/other/path/file.jpg"},
		{"/data/", "/data/media/file.jpg", "media/file.jpg"},
		{"/data", "/data/file.jpg", "file.jpg"},
	}
	for _, tt := range tests {
		got := toRelPath(tt.baseDir, tt.absPath)
		if got != tt.want {
			t.Errorf("toRelPath(%q, %q) = %q, want %q", tt.baseDir, tt.absPath, got, tt.want)
		}
	}
}

// TestQuoteMediaDBInsertion verifies that quote_media records can be inserted
// and queried via the same media_files table used for feed_media.
func TestQuoteMediaDBInsertion(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "igloo-feedmedia-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	d, err := db.Open(tmpPath, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	// Insert parent media + quote media for the same parent tweet.
	files := []model.MediaFile{
		{
			OwnerType:  "feed_media",
			OwnerID:    "1234567890",
			MediaIndex: 0,
			FilePath:   "media/twitter/userA/1234567890_0.jpg",
			MediaType:  "photo",
			SourceURL:  "https://pbs.twimg.com/media/parent.jpg",
		},
		{
			OwnerType:  "quote_media",
			OwnerID:    "9876543210",
			MediaIndex: 0,
			FilePath:   "media/twitter/userB/9876543210_0.jpg",
			MediaType:  "photo",
			SourceURL:  "https://pbs.twimg.com/media/quote.jpg",
		},
		{
			OwnerType:  "quote_media",
			OwnerID:    "9876543210",
			MediaIndex: 1,
			FilePath:   "media/twitter/userB/9876543210_1.jpg",
			MediaType:  "photo",
			SourceURL:  "https://pbs.twimg.com/media/quote2.jpg",
		},
	}

	if err := d.InsertMediaFileBatch(files); err != nil {
		t.Fatalf("InsertMediaFileBatch: %v", err)
	}

	// Verify feed_media records.
	feedFiles, err := d.GetMediaFilesByOwnerType("feed_media")
	if err != nil {
		t.Fatalf("GetMediaFilesByOwnerType(feed_media): %v", err)
	}
	if len(feedFiles) != 1 {
		t.Errorf("expected 1 feed_media file, got %d", len(feedFiles))
	}

	// Verify quote_media records.
	quoteFiles, err := d.GetMediaFilesByOwnerType("quote_media")
	if err != nil {
		t.Fatalf("GetMediaFilesByOwnerType(quote_media): %v", err)
	}
	if len(quoteFiles) != 2 {
		t.Errorf("expected 2 quote_media files, got %d", len(quoteFiles))
	}

	// Verify individual lookup works for quote_media.
	path, err := d.GetMediaFilePath("quote_media", "9876543210", 0)
	if err != nil {
		t.Fatalf("GetMediaFilePath(quote_media, 0): %v", err)
	}
	if path != "media/twitter/userB/9876543210_0.jpg" {
		t.Errorf("unexpected path: %s", path)
	}

	// Verify UNIQUE constraint: re-inserting same (owner_type, owner_id, media_index) is ignored.
	dupeFiles := []model.MediaFile{
		{
			OwnerType:  "quote_media",
			OwnerID:    "9876543210",
			MediaIndex: 0,
			FilePath:   "media/twitter/userB/different.jpg",
			MediaType:  "photo",
			SourceURL:  "https://pbs.twimg.com/media/different.jpg",
		},
	}
	if err := d.InsertMediaFileBatch(dupeFiles); err != nil {
		t.Fatalf("InsertMediaFileBatch (dupe): %v", err)
	}

	// Path should remain the original (INSERT OR IGNORE).
	path, err = d.GetMediaFilePath("quote_media", "9876543210", 0)
	if err != nil {
		t.Fatalf("GetMediaFilePath after dupe: %v", err)
	}
	if path != "media/twitter/userB/9876543210_0.jpg" {
		t.Errorf("UNIQUE violation: path changed to %s", path)
	}
}

// TestParseMediaIncludesQuoteMedia verifies that ParseMedia() populates
// both Media and QuoteMedia slices from their respective JSON fields.
func TestParseMediaIncludesQuoteMedia(t *testing.T) {
	item := model.FeedItem{
		MediaJSON:      `[{"url":"https://pbs.twimg.com/media/parent.jpg","type":"photo"}]`,
		QuoteMediaJSON: `[{"url":"https://pbs.twimg.com/media/quote.jpg","type":"photo"},{"url":"https://pbs.twimg.com/media/quote2.jpg","type":"photo"}]`,
	}
	item.ParseMedia()

	if len(item.Media) != 1 {
		t.Errorf("expected 1 parent media ref, got %d", len(item.Media))
	}
	if len(item.QuoteMedia) != 2 {
		t.Errorf("expected 2 quote media refs, got %d", len(item.QuoteMedia))
	}
	if len(item.QuoteMedia) > 0 && item.QuoteMedia[0].Type != "photo" {
		t.Errorf("expected quote media type 'photo', got %q", item.QuoteMedia[0].Type)
	}
}

func TestProcessOneMediaJobStoresTransientRetryDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary upstream failure", http.StatusInternalServerError)
	}))
	defer server.Close()
	d := newFeedMediaWorkerTestDB(t, "feed_retry_transient", server.URL+"/media.jpg", "photo")
	if err := d.ExecRaw(`UPDATE feed_media_jobs SET retry_count=1 WHERE tweet_id='feed_retry_transient'`); err != nil {
		t.Fatalf("set retry count: %v", err)
	}
	m := newFeedMediaTestManager(d, t.TempDir())
	job := db.FeedMediaJobRow{TweetID: "feed_retry_transient", SourceHandle: "twitter_sample", MediaKind: "image", RetryCount: 1, LeaseOwner: "worker-a"}

	before := time.Now().UnixMilli()
	m.processOneMediaJob(t.Context(), job, t.TempDir())

	status, retryCount, nextAttempt, kind := readFeedMediaJobState(t, d, job.TweetID)
	if status != "queued" || retryCount != 2 || kind != "temporary" || nextAttempt <= before {
		t.Fatalf("job state = status=%q retry=%d next=%d kind=%q, want queued retry=2 delayed temporary", status, retryCount, nextAttempt, kind)
	}
}

func TestProcessOneMediaJobMarksPermanentFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()
	d := newFeedMediaWorkerTestDB(t, "feed_retry_permanent", server.URL+"/media.jpg", "photo")
	m := newFeedMediaTestManager(d, t.TempDir())
	job := db.FeedMediaJobRow{TweetID: "feed_retry_permanent", SourceHandle: "twitter_sample", MediaKind: "image", RetryCount: 0, LeaseOwner: "worker-a"}

	m.processOneMediaJob(t.Context(), job, t.TempDir())

	status, retryCount, nextAttempt, kind := readFeedMediaJobState(t, d, job.TweetID)
	if status != "failed" || retryCount != 1 || kind != "permanent_http" || nextAttempt != 0 {
		t.Fatalf("job state = status=%q retry=%d next=%d kind=%q, want failed retry=1 permanent_http", status, retryCount, nextAttempt, kind)
	}
}

func TestProcessOneMediaJobPrunesNotFoundAfterCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	d := newFeedMediaWorkerTestDB(t, "feed_retry_not_found", server.URL+"/media.jpg", "photo")
	m := newFeedMediaTestManager(d, t.TempDir())
	job := db.FeedMediaJobRow{TweetID: "feed_retry_not_found", SourceHandle: "twitter_sample", MediaKind: "image", RetryCount: maxRetries404, LeaseOwner: "worker-a"}

	m.processOneMediaJob(t.Context(), job, t.TempDir())

	status, retryCount, nextAttempt, kind := readFeedMediaJobState(t, d, job.TweetID)
	if status != "pruned" || retryCount != maxRetries404+1 || kind != "not_found" || nextAttempt != 0 {
		t.Fatalf("job state = status=%q retry=%d next=%d kind=%q, want pruned retry=%d not_found", status, retryCount, nextAttempt, kind, maxRetries404+1)
	}
}

func TestProcessOneMediaJobCompletionClearsLease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = fmt.Fprint(w, "image-bytes")
	}))
	defer server.Close()
	d := newFeedMediaWorkerTestDB(t, "feed_retry_complete", server.URL+"/media.jpg", "photo")
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`UPDATE feed_media_jobs SET lease_owner='worker-a', lease_until_ms=? WHERE tweet_id='feed_retry_complete'`, now+60000); err != nil {
		t.Fatalf("set lease: %v", err)
	}
	m := newFeedMediaTestManager(d, t.TempDir())
	job := db.FeedMediaJobRow{TweetID: "feed_retry_complete", SourceHandle: "twitter_sample", MediaKind: "image", RetryCount: 0, LeaseOwner: "worker-a"}

	m.processOneMediaJob(t.Context(), job, t.TempDir())

	status, retryCount, nextAttempt, kind := readFeedMediaJobState(t, d, job.TweetID)
	var owner string
	var leaseUntil int64
	if err := d.QueryRow(`SELECT COALESCE(lease_owner,''), COALESCE(lease_until_ms,0) FROM feed_media_jobs WHERE tweet_id=?`, job.TweetID).Scan(&owner, &leaseUntil); err != nil {
		t.Fatalf("query lease: %v", err)
	}
	if status != "completed" || retryCount != 0 || nextAttempt != 0 || kind != "" || owner != "" || leaseUntil != 0 {
		t.Fatalf("job state = status=%q retry=%d next=%d kind=%q owner=%q lease=%d, want completed cleared", status, retryCount, nextAttempt, kind, owner, leaseUntil)
	}
}

func newFeedMediaWorkerTestDB(t *testing.T, tweetID, mediaURL, mediaKind string) *db.DB {
	t.Helper()
	d := newTestWorkerDB(t)
	mediaJSON := fmt.Sprintf(`[{"url":%q,"type":%q}]`, mediaURL, mediaKind)
	if err := d.ExecRaw(`
		INSERT INTO feed_items
			(tweet_id, source_handle, author_handle, media_json, canonical_url, published_at, fetched_at)
		VALUES (?, 'twitter_sample', 'sample', ?, 'https://x.com/sample/status/1', 1, 1)
	`, tweetID, mediaJSON); err != nil {
		t.Fatalf("insert feed item: %v", err)
	}
	now := time.Now().UnixMilli()
	if err := d.ExecRaw(`
		INSERT INTO feed_media_jobs
			(tweet_id, source_handle, status, media_kind, retry_count, lease_owner, lease_until_ms, created_at, updated_at)
		VALUES (?, 'twitter_sample', 'processing', ?, 0, 'worker-a', ?, 1, 1)
	`, tweetID, mediaKind, now+60000); err != nil {
		t.Fatalf("insert feed media job: %v", err)
	}
	return d
}

func newFeedMediaTestManager(d *db.DB, dataDir string) *Manager {
	return &Manager{
		db:           d,
		cfg:          testCfg(dataDir),
		downloader:   testDownloader(),
		activity:     NewActivityRing(10),
		feedActivity: NewActivityRing(10),
	}
}

func readFeedMediaJobState(t *testing.T, d *db.DB, tweetID string) (status string, retryCount int, nextAttempt int64, kind string) {
	t.Helper()
	if err := d.QueryRow(`
		SELECT status, retry_count, COALESCE(next_attempt_at_ms,0), COALESCE(last_error_kind,'')
		FROM feed_media_jobs WHERE tweet_id=?
	`, tweetID).Scan(&status, &retryCount, &nextAttempt, &kind); err != nil {
		t.Fatalf("query feed media job: %v", err)
	}
	return status, retryCount, nextAttempt, kind
}
