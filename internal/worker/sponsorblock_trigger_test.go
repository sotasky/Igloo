package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/dearrow"
	"github.com/screwys/igloo/internal/sponsorblock"
)

// stubSBClient lets us inject canned segments / error without real network.
type stubSBClient struct {
	segments []sponsorblock.Segment
	err      error
	callsN   int
}

func (s *stubSBClient) Fetch(_ context.Context, _ string) ([]sponsorblock.Segment, error) {
	s.callsN++
	if s.err != nil {
		return nil, s.err
	}
	return s.segments, nil
}

func (s *stubSBClient) calls() int { return s.callsN }

// TestYoutubeEnrichOnce_CoFetchesDearrowAndSponsorBlock seeds a single YouTube
// video, runs one enrichment pass, and verifies both APIs were called and
// both kinds of data landed in the DB.
func TestYoutubeEnrichOnce_CoFetchesDearrowAndSponsorBlock(t *testing.T) {
	old := dearrowPerFetchSleep
	dearrowPerFetchSleep = time.Millisecond
	defer func() { dearrowPerFetchSleep = old }()

	realTitle := "Community"
	m, daClient := newTestManagerWithDearrow(t, dearrow.Result{Title: &realTitle}, nil)

	sbStub := &stubSBClient{segments: []sponsorblock.Segment{
		{Start: 10, End: 20, Category: "sponsor"},
	}}
	m.sponsorblockClient = sbStub

	seedVideo(t, m, "v1")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if n := m.youtubeEnrichOnce(ctx); n != 1 {
		t.Fatalf("processed = %d, want 1", n)
	}
	if daClient.calls() != 1 {
		t.Errorf("dearrow calls = %d, want 1", daClient.calls())
	}
	if sbStub.calls() != 1 {
		t.Errorf("sponsorblock calls = %d, want 1", sbStub.calls())
	}

	// DeArrow title landed.
	v, err := m.db.GetVideo("v1")
	if err != nil || v == nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if v.DearrowTitle == nil || *v.DearrowTitle != "Community" {
		t.Errorf("DearrowTitle = %v, want Community", v.DearrowTitle)
	}

	// SponsorBlock segment + checked-marker landed.
	segs, err := m.db.GetSponsorBlockSegments("v1")
	if err != nil {
		t.Fatalf("GetSponsorBlockSegments: %v", err)
	}
	if len(segs) != 1 || segs[0].Category != "sponsor" {
		t.Errorf("segments = %+v, want one 'sponsor' segment", segs)
	}
	checked, err := m.db.GetSponsorBlockChecked("v1")
	if err != nil || checked == nil {
		t.Fatalf("GetSponsorBlockChecked: %v (checked=%v)", err, checked)
	}
}

// TestYoutubeEnrichOnce_MarksCheckedOnSBError ensures a failed SB fetch still
// writes a sponsorblock_checked row so the video doesn't hot-loop every tick.
func TestYoutubeEnrichOnce_MarksCheckedOnSBError(t *testing.T) {
	old := dearrowPerFetchSleep
	dearrowPerFetchSleep = time.Millisecond
	defer func() { dearrowPerFetchSleep = old }()

	m, _ := newTestManagerWithDearrow(t, dearrow.Result{}, nil)
	m.sponsorblockClient = &stubSBClient{err: errors.New("network down")}
	seedVideo(t, m, "v1")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_ = m.youtubeEnrichOnce(ctx)

	checked, err := m.db.GetSponsorBlockChecked("v1")
	if err != nil {
		t.Fatalf("GetSponsorBlockChecked: %v", err)
	}
	if checked == nil {
		t.Fatal("checked row missing after SB error; worker would re-fetch forever")
	}
}

// TestYoutubeEnrichOnce_SkipsSBWhenClientNil is the test-mode path: a Manager
// with no sponsorblockClient shouldn't try to hit the network or write an
// empty SB-checked row.
func TestYoutubeEnrichOnce_SkipsSBWhenClientNil(t *testing.T) {
	old := dearrowPerFetchSleep
	dearrowPerFetchSleep = time.Millisecond
	defer func() { dearrowPerFetchSleep = old }()

	m, _ := newTestManagerWithDearrow(t, dearrow.Result{}, nil)
	// m.sponsorblockClient deliberately left nil.
	seedVideo(t, m, "v1")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_ = m.youtubeEnrichOnce(ctx)

	checked, _ := m.db.GetSponsorBlockChecked("v1")
	if checked != nil {
		t.Error("SB-checked row written despite nil client")
	}
}
