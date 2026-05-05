package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/dearrow"
)

// stubDearrowClient lets us inject a canned Result or error.
type stubDearrowClient struct {
	res    dearrow.Result
	err    error
	called bool
	callsN atomic.Int32
}

func (s *stubDearrowClient) Fetch(_ context.Context, _ string) (dearrow.Result, error) {
	s.called = true
	s.callsN.Add(1)
	return s.res, s.err
}

func (s *stubDearrowClient) calls() int {
	return int(s.callsN.Load())
}

// stubDearrowExtract writes fake bytes to outPath so FileExists returns true.
func stubDearrowExtract(_ context.Context, _ string, _ float64, out string) error {
	return os.WriteFile(out, []byte{0xFF, 0xD8, 0xFF, 'x'}, 0o644)
}

// newTestManagerWithDearrow builds a Manager wired with a DeArrow fetcher
// whose client and extractor are stubbed. Returns the Manager and the client
// stub (for inspection). dataDir is under t.TempDir().
func newTestManagerWithDearrow(t *testing.T, res dearrow.Result, clientErr error) (*Manager, *stubDearrowClient) {
	t.Helper()
	d := newTestWorkerDB(t)
	dataDir := t.TempDir()
	cfg := testCfg(dataDir)
	client := &stubDearrowClient{res: res, err: clientErr}
	m := &Manager{
		db:  d,
		cfg: cfg,
		dearrowFetcher: &dearrow.Fetcher{
			Client:  client,
			Extract: stubDearrowExtract,
			ThumbDir: filepath.Join(dataDir, "thumbnails", "dearrow"),
		},
	}
	return m, client
}

// seedVideo inserts a YouTube channel + video so SetDearrowData/MarkDearrowChecked
// have a row to update.
func seedVideo(t *testing.T, m *Manager, videoID string) {
	t.Helper()
	_ = m.db.ExecRaw(
		`INSERT INTO channels (channel_id, name, platform) VALUES (?, ?, ?)`,
		"youtube_testchan", "test", "youtube",
	)
	if err := m.db.InsertVideo(
		videoID, "youtube_testchan", "Original Title", "",
		60, "", "videos/"+videoID+".mp4", 1024,
		time.Now().UnixMilli(), "", "video", 0, false,
	); err != nil {
		t.Fatalf("InsertVideo: %v", err)
	}
}

func TestTriggerDearrowFetch_YouTubePersistsFullBranding(t *testing.T) {
	title := "Real"
	casual := "Casual"
	ts := 12.5
	res := dearrow.Result{
		Title:          &title,
		CasualTitle:    &casual,
		ThumbTimestamp: &ts,
	}

	m, _ := newTestManagerWithDearrow(t, res, nil)
	seedVideo(t, m, "v1")

	ctx := context.Background()
	m.triggerDearrowFetch(ctx, "v1", "videos/v1.mp4", "youtube")

	got, err := m.db.GetVideo("v1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got == nil {
		t.Fatal("video not found")
	}

	if got.DearrowTitle == nil || *got.DearrowTitle != "Real" {
		t.Errorf("DearrowTitle = %v, want Real", got.DearrowTitle)
	}
	if got.DearrowTitleCasual == nil || *got.DearrowTitleCasual != "Casual" {
		t.Errorf("DearrowTitleCasual = %v, want Casual", got.DearrowTitleCasual)
	}
	if got.DearrowThumbPath == nil {
		t.Fatal("DearrowThumbPath is nil, want a path ending with v1.jpg")
	}
	if !strings.HasSuffix(*got.DearrowThumbPath, "v1.jpg") {
		t.Errorf("DearrowThumbPath = %q, want suffix v1.jpg", *got.DearrowThumbPath)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs <= 0 {
		t.Errorf("DearrowCheckedAtMs = %v, want > 0", got.DearrowCheckedAtMs)
	}

	// Assert thumbnail file exists.
	thumbAbs := filepath.Join(m.cfg.DataDir, *got.DearrowThumbPath)
	if _, err := os.Stat(thumbAbs); err != nil {
		t.Errorf("thumbnail file not found at %s: %v", thumbAbs, err)
	}
}

func TestTriggerDearrowFetch_NonYouTubeIsNoOp(t *testing.T) {
	title := "Real"
	res := dearrow.Result{Title: &title}
	m, client := newTestManagerWithDearrow(t, res, nil)
	seedVideo(t, m, "v1")

	ctx := context.Background()
	m.triggerDearrowFetch(ctx, "v1", "videos/v1.mp4", "twitter")

	got, err := m.db.GetVideo("v1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got.DearrowCheckedAtMs != nil {
		t.Errorf("DearrowCheckedAtMs = %v, want nil (no write should have happened)", got.DearrowCheckedAtMs)
	}
	if client.called {
		t.Error("stub client was called; expected no-op for non-YouTube platform")
	}
}

func TestTriggerDearrowFetch_NilFetcherIsNoOp(t *testing.T) {
	d := newTestWorkerDB(t)
	dataDir := t.TempDir()
	m := &Manager{
		db:             d,
		cfg:            testCfg(dataDir),
		dearrowFetcher: nil,
	}

	// Seed video directly on the db — no Manager helper for nil-fetcher case.
	_ = d.ExecRaw(
		`INSERT INTO channels (channel_id, name, platform) VALUES (?, ?, ?)`,
		"youtube_testchan", "test", "youtube",
	)
	_ = d.InsertVideo(
		"v1", "youtube_testchan", "Title", "",
		60, "", "videos/v1.mp4", 1024,
		time.Now().UnixMilli(), "", "video", 0, false,
	)

	ctx := context.Background()
	// Should not panic.
	m.triggerDearrowFetch(ctx, "v1", "videos/v1.mp4", "youtube")

	got, err := d.GetVideo("v1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got.DearrowCheckedAtMs != nil {
		t.Errorf("DearrowCheckedAtMs = %v, want nil (nil fetcher is a no-op)", got.DearrowCheckedAtMs)
	}
}

func TestTriggerDearrowFetch_APIErrorMarksChecked(t *testing.T) {
	m, _ := newTestManagerWithDearrow(t, dearrow.Result{}, errDearrowStub)
	seedVideo(t, m, "v1")

	ctx := context.Background()
	m.triggerDearrowFetch(ctx, "v1", "videos/v1.mp4", "youtube")

	got, err := m.db.GetVideo("v1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs <= 0 {
		t.Errorf("DearrowCheckedAtMs = %v, want > 0 (MarkDearrowChecked should fire on error)", got.DearrowCheckedAtMs)
	}
	if got.DearrowTitle != nil || got.DearrowTitleCasual != nil || got.DearrowThumbPath != nil {
		t.Errorf("value columns should be nil on pure error: title=%v casual=%v thumb=%v",
			got.DearrowTitle, got.DearrowTitleCasual, got.DearrowThumbPath)
	}
}

func TestTriggerDearrowFetch_ThumbPathStoredRelative(t *testing.T) {
	title := "X"
	ts := 3.0
	res := dearrow.Result{
		Title:          &title,
		ThumbTimestamp: &ts,
	}

	m, _ := newTestManagerWithDearrow(t, res, nil)
	seedVideo(t, m, "v1")

	ctx := context.Background()
	m.triggerDearrowFetch(ctx, "v1", "videos/v1.mp4", "youtube")

	got, err := m.db.GetVideo("v1")
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got.DearrowThumbPath == nil {
		t.Fatal("DearrowThumbPath is nil")
	}
	if filepath.IsAbs(*got.DearrowThumbPath) {
		t.Errorf("DearrowThumbPath = %q is absolute; want relative", *got.DearrowThumbPath)
	}
	if !strings.HasSuffix(*got.DearrowThumbPath, "thumbnails/dearrow/v1.jpg") {
		t.Errorf("DearrowThumbPath = %q, want suffix thumbnails/dearrow/v1.jpg", *got.DearrowThumbPath)
	}
}

// errDearrowStub is a sentinel error for tests.
type dearrowStubError struct{}

func (dearrowStubError) Error() string { return "dearrow stub error" }

var errDearrowStub = dearrowStubError{}
