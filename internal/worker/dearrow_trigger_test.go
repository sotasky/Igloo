package worker

import (
	"context"
	"errors"
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
	dataDir := t.TempDir()
	d := newTestWorkerDBAt(t, dataDir)
	cfg := testCfg(dataDir)
	client := &stubDearrowClient{res: res, err: clientErr}
	m := &Manager{
		db:  d,
		cfg: cfg,
		dearrowFetcher: &dearrow.Fetcher{
			Client:   client,
			Extract:  stubDearrowExtract,
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
		videoID, "youtube_testchan", "youtube_video", "Original Title", "",
		60, time.Now().UnixMilli(), "", "video", 0, false,
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
	thumb, err := m.db.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "v1", 0)
	if err != nil || thumb == nil {
		t.Fatalf("DeArrow thumbnail asset = %+v, err = %v", thumb, err)
	}
	if !strings.HasSuffix(thumb.FilePath, ".jpg") {
		t.Errorf("DeArrow thumbnail path = %q, want jpg", thumb.FilePath)
	}
	if got.DearrowCheckedAtMs == nil || *got.DearrowCheckedAtMs <= 0 {
		t.Errorf("DearrowCheckedAtMs = %v, want > 0", got.DearrowCheckedAtMs)
	}

	// Assert thumbnail file exists.
	thumbAbs, err := m.cfg.Storage.Path(thumb.FilePath)
	if err != nil {
		t.Fatal(err)
	}
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
		"v1", "youtube_testchan", "youtube_video", "Title", "",
		60, time.Now().UnixMilli(), "", "video", 0, false,
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
	if got.DearrowTitle != nil || got.DearrowTitleCasual != nil {
		t.Errorf("value columns should be nil on pure error: title=%v casual=%v",
			got.DearrowTitle, got.DearrowTitleCasual)
	}
	if thumb, err := m.db.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "v1", 0); err != nil || thumb != nil {
		t.Errorf("DeArrow thumbnail asset = %+v, err = %v; want none", thumb, err)
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

	thumb, err := m.db.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "v1", 0)
	if err != nil || thumb == nil {
		t.Fatalf("DeArrow thumbnail asset = %+v, err = %v", thumb, err)
	}
	if filepath.IsAbs(thumb.FilePath) {
		t.Errorf("DeArrow thumbnail path = %q is absolute; want relative", thumb.FilePath)
	}
	if !strings.HasPrefix(thumb.FilePath, "thumbnails/dearrow/") || !strings.HasSuffix(thumb.FilePath, ".jpg") {
		t.Errorf("DeArrow thumbnail path = %q, want unique jpg under thumbnails/dearrow", thumb.FilePath)
	}
}

func TestTriggerDearrowFetch_ExtractionFailurePreservesReadyThumbnail(t *testing.T) {
	newTitle := "Updated title"
	ts := 4.0
	m, _ := newTestManagerWithDearrow(t, dearrow.Result{Title: &newTitle, ThumbTimestamp: &ts}, nil)
	seedVideo(t, m, "v1")

	oldKey := "thumbnails/dearrow/existing.jpg"
	oldPath, err := m.cfg.Storage.Path(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("ready thumbnail"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.db.SetDearrowData("v1", nil, nil, &oldKey, 1); err != nil {
		t.Fatal(err)
	}
	m.dearrowFetcher.Extract = func(context.Context, string, float64, string) error {
		return errors.New("extract failed")
	}

	m.triggerDearrowFetch(context.Background(), "v1", "videos/v1.mp4", "youtube")

	video, err := m.db.GetVideo("v1")
	if err != nil {
		t.Fatal(err)
	}
	if video.DearrowTitle == nil || *video.DearrowTitle != newTitle {
		t.Fatalf("partial title was not persisted: %+v", video.DearrowTitle)
	}
	asset, err := m.db.GetAssetByOwnerIdentity("dearrow_thumbnail", "youtube_video", "v1", 0)
	if err != nil || asset == nil || asset.FilePath != oldKey {
		t.Fatalf("ready thumbnail was replaced on extraction failure: %+v, err = %v", asset, err)
	}
	if body, err := os.ReadFile(oldPath); err != nil || string(body) != "ready thumbnail" {
		t.Fatalf("ready thumbnail bytes changed: body=%q err=%v", body, err)
	}
}

// errDearrowStub is a sentinel error for tests.
type dearrowStubError struct{}

func (dearrowStubError) Error() string { return "dearrow stub error" }

var errDearrowStub = dearrowStubError{}
