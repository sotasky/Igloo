package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/dearrow"
)

// TestDearrowOnce_ProcessesMissingYouTubeVideos seeds 3 YouTube videos that
// have never been checked, runs one scan, and verifies all three had their
// branding written.
func TestDearrowOnce_ProcessesMissingYouTubeVideos(t *testing.T) {
	// Override sleep so the 3-video pass doesn't take 1.5s.
	old := dearrowPerFetchSleep
	dearrowPerFetchSleep = time.Millisecond
	defer func() { dearrowPerFetchSleep = old }()

	realTitle := "Better"
	m, client := newTestManagerWithDearrow(t, dearrow.Result{Title: &realTitle}, nil)
	seedVideo(t, m, "v1")
	seedVideo(t, m, "v2")
	seedVideo(t, m, "v3")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	n := m.dearrowOnce(ctx)
	if n != 3 {
		t.Fatalf("processed = %d, want 3", n)
	}
	if client.calls() != 3 {
		t.Fatalf("client calls = %d, want 3", client.calls())
	}
	for _, id := range []string{"v1", "v2", "v3"} {
		v, _ := m.db.GetVideo(id)
		if v.DearrowTitle == nil || *v.DearrowTitle != "Better" {
			t.Errorf("%s: DearrowTitle = %v, want 'Better'", id, v.DearrowTitle)
		}
	}
}

// TestDearrowOnce_NoOpWhenNothingNeedsCheck seeds no videos, runs once, and
// verifies no client calls are made.
func TestDearrowOnce_NoOpWhenNothingNeedsCheck(t *testing.T) {
	m, client := newTestManagerWithDearrow(t, dearrow.Result{}, nil)
	n := m.dearrowOnce(t.Context())
	if n != 0 {
		t.Errorf("processed = %d, want 0", n)
	}
	if client.calls() != 0 {
		t.Errorf("client calls = %d, want 0", client.calls())
	}
}

// TestDearrowOnce_NilFetcherNoOp ensures a Manager without a fetcher doesn't
// panic and returns 0 immediately.
func TestDearrowOnce_NilFetcherNoOp(t *testing.T) {
	d := newTestWorkerDB(t)
	m := &Manager{db: d, cfg: testCfg(t.TempDir())}
	// dearrowFetcher left nil.
	if n := m.dearrowOnce(t.Context()); n != 0 {
		t.Errorf("processed = %d, want 0", n)
	}
}

// TestDearrowOnce_ContextCancellationStopsMidLoop seeds several videos and
// cancels the context immediately. Verifies the loop exits without processing
// all videos.
func TestDearrowOnce_ContextCancellationStopsMidLoop(t *testing.T) {
	realTitle := "X"
	m, _ := newTestManagerWithDearrow(t, dearrow.Result{Title: &realTitle}, nil)
	for i := 1; i <= 10; i++ {
		seedVideo(t, m, fmt.Sprintf("v%d", i))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancelled before call
	n := m.dearrowOnce(ctx)
	if n >= 10 {
		t.Errorf("processed = %d, want < 10 (cancel should stop early)", n)
	}
}
