package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestTwitterSourceHandleUsesSourceID(t *testing.T) {
	got := twitterSourceHandle(model.Channel{
		ChannelID: "twitter_user_a",
		SourceID:  "@User_A",
		Platform:  "twitter",
	})
	if got != "user_a" {
		t.Fatalf("twitterSourceHandle = %q; want user_a", got)
	}
}

func TestTwitterSourceHandleFallsBackToChannelID(t *testing.T) {
	got := twitterSourceHandle(model.Channel{
		ChannelID: "twitter_user_b",
		Platform:  "twitter",
	})
	if got != "user_b" {
		t.Fatalf("twitterSourceHandle = %q; want user_b", got)
	}
}

func TestBuildFeedSourcesShowsNeverIngestedChannelAsPending(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "twitter__sample_handle",
		SourceID:     "_sample_handle",
		Name:         "_sample_handle",
		URL:          "https://x.com/_sample_handle",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	sources, _ := srv.buildFeedSources()
	for _, source := range sources {
		if source.Handle != "_sample_handle" {
			continue
		}
		if source.Status != "pending" {
			t.Fatalf("source status = %q; want pending", source.Status)
		}
		return
	}
	t.Fatalf("_sample_handle source missing from diagnostics: %#v", sources)
}

func TestBuildFeedSourcesDoesNotHideARecordedFailureBehindRecentSuccess(t *testing.T) {
	srv := newTestServer(t)
	const channelID = "twitter_sample_recent"
	if err := srv.db.AddChannel(model.Channel{
		ChannelID: channelID, SourceID: "sample_recent", Name: "sample_recent",
		URL: "https://x.com/sample_recent", Platform: "twitter", IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := srv.db.RecordIngestSuccess(channelID, float64(time.Now().Unix()), 10); err != nil {
		t.Fatalf("RecordIngestSuccess: %v", err)
	}
	if err := srv.db.RecordIngestFailure(channelID, "source rejected the request", 0); err != nil {
		t.Fatalf("RecordIngestFailure: %v", err)
	}

	sources, _ := srv.buildFeedSources()
	for _, source := range sources {
		if source.Handle == "sample_recent" {
			if source.Status != "cooling" {
				t.Fatalf("source status = %q; want cooling", source.Status)
			}
			return
		}
	}
	t.Fatal("sample_recent source missing from diagnostics")
}

func TestBuildFeedSourcesUsesSourceChannelCounts(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "twitter_sample_source",
		SourceID:     "sample_source",
		Name:         "sample_source",
		URL:          "https://x.com/sample_source",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("add channel: %v", err)
	}
	now := time.Now().UTC()
	if _, err := srv.db.UpsertFeedItems([]model.FeedItem{
		{TweetID: "sample_source_count_one", SourceHandle: "sample_source", AuthorHandle: "sample_source", BodyText: "one", PublishedAt: &now, FetchedAt: now},
		{TweetID: "sample_source_count_two", SourceHandle: "sample_source", AuthorHandle: "sample_source", BodyText: "two", PublishedAt: &now, FetchedAt: now},
	}); err != nil {
		t.Fatalf("seed feed items: %v", err)
	}

	sources, _ := srv.buildFeedSources()
	for _, source := range sources {
		if source.Handle == "sample_source" {
			if source.ItemCount != 2 {
				t.Fatalf("item count = %d, want 2", source.ItemCount)
			}
			return
		}
	}
	t.Fatalf("source missing from diagnostics: %#v", sources)
}

func TestCountReadyAvatarsCountsCanonicalChannelIdentityOnly(t *testing.T) {
	srv := newTestServer(t)
	storeReadyMediaAsset(t, srv, "twitter", "channel", "twitter_sample", "avatar", 0, "thumbnails/avatars/twitter_sample.jpg", "image/jpeg", testJPEGBytes())
	storeReadyMediaAsset(t, srv, "youtube", "comment_author", "comment_sample", "avatar", 0, "thumbnails/avatars/comment_sample.jpg", "image/jpeg", testJPEGBytes())
	storeReadyMediaAsset(t, srv, "twitter", "tweet", "tweet_sample", "avatar", 0, "thumbnails/avatars/tweet_sample.jpg", "image/jpeg", testJPEGBytes())
	if err := srv.db.DeclareAsset(db.Asset{AssetID: "queued_channel_avatar", AssetKind: "avatar", OwnerKind: "channel", OwnerID: "twitter_sample_new", SourceURL: "https://example.test/avatar.jpg"}, 1); err != nil {
		t.Fatalf("seed queued avatar: %v", err)
	}

	if got, err := srv.countReadyAvatars(); err != nil || got != 1 {
		t.Fatalf("countReadyAvatars = %d, want one ready channel avatar", got)
	}
}

func TestServerDashboardInventorySurvivesProcessCacheReset(t *testing.T) {
	srv := newTestServer(t)
	updatedAt := time.Now().Add(-time.Minute).UTC()
	want := components.ServerDashboardData{
		ChannelsTotal:  42,
		ChannelsByPlat: map[string]int{"twitter": 7},
		LocalFeedCount: 11,
		StorageGB:      12.5,
	}
	srv.saveServerDashboardInventory(want, updatedAt)

	restarted := &Server{cfg: srv.cfg}
	restarted.loadServerDashboardInventory()
	got, ok := restarted.serverDashboardStaticData()
	if !ok {
		t.Fatal("persisted inventory was not restored")
	}
	if got.ChannelsTotal != want.ChannelsTotal || got.LocalFeedCount != want.LocalFeedCount || got.StorageGB != want.StorageGB || got.ChannelsByPlat["twitter"] != want.ChannelsByPlat["twitter"] {
		t.Fatalf("restored inventory = %#v, want %#v", got, want)
	}
}

func TestServerStatusSeparatesLiveAndInventoryFragments(t *testing.T) {
	srv := newTestServer(t)
	srv.dashboardInventoryMu.Lock()
	data, err := srv.serverDashboardStaticDataFresh()
	if err != nil {
		t.Fatalf("serverDashboardStaticDataFresh: %v", err)
	}
	srv.dashboardInventory = &serverDashboardInventory{
		data:      data,
		updatedAt: time.Now(),
	}
	srv.dashboardInventoryMu.Unlock()

	liveRec := httptest.NewRecorder()
	liveReq := httptest.NewRequest("GET", "/api/server/status?fmt=html&part=live", nil)
	srv.handleServerStatus(liveRec, liveReq)
	if liveRec.Code != 200 {
		t.Fatalf("live status = %d: %s", liveRec.Code, liveRec.Body.String())
	}
	liveBody := liveRec.Body.String()
	for _, want := range []string{`id="sv-live-stats"`, `id="sv-workers-content" hx-swap-oob="innerHTML"`, "Workers &amp; Errors", "Activity"} {
		if !strings.Contains(liveBody, want) {
			t.Fatalf("live fragment missing %q:\n%s", want, liveBody)
		}
	}
	if strings.Contains(liveBody, "Local feed") {
		t.Fatalf("live fragment should not render database inventory:\n%s", liveBody)
	}
	if strings.Contains(liveBody, `sv-raw-log-section`) {
		t.Fatalf("live fragment should not replace the raw server log:\n%s", liveBody)
	}

	statsRec := httptest.NewRecorder()
	statsReq := httptest.NewRequest("GET", "/api/server/status?fmt=html&part=stats", nil)
	srv.handleServerStatus(statsRec, statsReq)
	if statsRec.Code != 200 {
		t.Fatalf("inventory status = %d: %s", statsRec.Code, statsRec.Body.String())
	}
	statsBody := statsRec.Body.String()
	for _, want := range []string{`id="sv-static-stats"`, `id="sv-static-db"`, "Local feed"} {
		if !strings.Contains(statsBody, want) {
			t.Fatalf("inventory fragment missing %q:\n%s", want, statsBody)
		}
	}
}

func TestFeedStatusSeparatesLiveAndSourcesFragments(t *testing.T) {
	srv := newTestServer(t)

	liveRec := httptest.NewRecorder()
	liveReq := httptest.NewRequest("GET", "/api/feed/status?fmt=html&part=live", nil)
	srv.handleFeedStatus(liveRec, liveReq)
	if liveRec.Code != 200 {
		t.Fatalf("live status = %d: %s", liveRec.Code, liveRec.Body.String())
	}
	liveBody := liveRec.Body.String()
	for _, want := range []string{`id="feed-live-stats"`, `id="feed-live-content"`, "Activity"} {
		if !strings.Contains(liveBody, want) {
			t.Fatalf("live fragment missing %q:\n%s", want, liveBody)
		}
	}
	if strings.Contains(liveBody, `id="feed-sources-content"`) {
		t.Fatalf("live fragment should not replace sources:\n%s", liveBody)
	}

	sourcesRec := httptest.NewRecorder()
	sourcesReq := httptest.NewRequest("GET", "/api/feed/status?fmt=html&part=sources", nil)
	srv.handleFeedStatus(sourcesRec, sourcesReq)
	if sourcesRec.Code != 200 {
		t.Fatalf("sources status = %d: %s", sourcesRec.Code, sourcesRec.Body.String())
	}
	sourcesBody := sourcesRec.Body.String()
	for _, want := range []string{`id="feed-sources-content"`, "Sources", "logs-feed-sources-load"} {
		if !strings.Contains(sourcesBody, want) {
			t.Fatalf("sources fragment missing %q:\n%s", want, sourcesBody)
		}
	}
	if strings.Contains(sourcesBody, `id="feed-live-content"`) {
		t.Fatalf("sources fragment should not replace live activity:\n%s", sourcesBody)
	}
}
