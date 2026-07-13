package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestGlobalXMediaLimitDecreasePrunesFollowedAndFeedSourceWindows(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("media_download_limit_default", "2"); err != nil {
		t.Fatal(err)
	}
	seedWebXRetentionChannel(t, srv, "twitter_global_x", "global_x", 2)
	if err := srv.db.UpsertFeedSource(model.FeedSource{
		SourceID: "twitter_sample_source", Platform: "twitter", SourceType: "list",
		ExternalID: "global", Label: "Global", URL: "https://x.com/i/lists/global", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	feedItems := webXRetentionItems("list_x", "sample_list", 2)
	if _, err := srv.db.UpsertFeedItems(feedItems); err != nil {
		t.Fatal(err)
	}
	for _, item := range feedItems {
		if err := srv.db.RecordFeedItemSources(item.TweetID, []string{"twitter_sample_source"}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"media_download_limit_default":1}`))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuthRole(req, "sample_admin", "admin")
	rec := httptest.NewRecorder()
	srv.handleUpdateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d: %s", rec.Code, rec.Body.String())
	}
	assertWebXAssetState(t, srv, "global_x_1", db.AssetStatePruned)
	assertWebXAssetState(t, srv, "global_x_2", db.AssetStateQueued)
	assertWebXAssetState(t, srv, "list_x_1", db.AssetStatePruned)
	assertWebXAssetState(t, srv, "list_x_2", db.AssetStateQueued)
	var sourceItems int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM feed_item_sources WHERE source_id = 'twitter_sample_source'`).Scan(&sourceItems); err != nil {
		t.Fatal(err)
	}
	if sourceItems != 1 {
		t.Fatalf("feed source items = %d, want 1", sourceItems)
	}
}

func TestChannelXMediaLimitDecreasePrunesAndIncreaseQueuesIngest(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("media_download_limit_default", "2"); err != nil {
		t.Fatal(err)
	}
	const channelID = "twitter_sample_channel"
	seedWebXRetentionChannel(t, srv, channelID, "sample_channel", 2)
	if err := srv.db.RecordIngestSuccess(channelID, float64(time.Now().Unix()), 0); err != nil {
		t.Fatal(err)
	}

	postChannelXMediaLimit(t, srv, channelID, 1)
	assertWebXAssetState(t, srv, "sample_channel_1", db.AssetStatePruned)
	assertWebXAssetState(t, srv, "sample_channel_2", db.AssetStateQueued)
	state, err := srv.db.GetIngestState(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSuccessAt == 0 {
		t.Fatal("decrease unexpectedly queued X discovery")
	}

	postChannelXMediaLimit(t, srv, channelID, 2)
	assertWebXAssetState(t, srv, "sample_channel_1", db.AssetStateQueued)
	state, err = srv.db.GetIngestState(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSuccessAt != 0 {
		t.Fatalf("widening last_success_at = %f, want 0", state.LastSuccessAt)
	}
}

func TestGlobalXMediaLimitIncreaseRestoresStoredWindowAndQueuesIngest(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("media_download_limit_default", "1"); err != nil {
		t.Fatal(err)
	}
	const channelID = "twitter_sample_source"
	seedWebXRetentionChannel(t, srv, channelID, "sample_source", 2)
	if _, err := srv.db.PruneXMediaRetentionForChannel(channelID, db.XMediaRetentionOptions{NowMs: 3000}); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.RecordIngestSuccess(channelID, float64(time.Now().Unix()), 0); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"media_download_limit_default":2}`))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuthRole(req, "sample_admin", "admin")
	rec := httptest.NewRecorder()
	srv.handleUpdateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d: %s", rec.Code, rec.Body.String())
	}
	assertWebXAssetState(t, srv, "sample_source_1", db.AssetStateQueued)
	state, err := srv.db.GetIngestState(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSuccessAt != 0 {
		t.Fatalf("global widening last_success_at = %f, want 0", state.LastSuccessAt)
	}
}

func TestChannelXMediaLimitMutationSharesRetentionEffects(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("media_download_limit_default", "2"); err != nil {
		t.Fatal(err)
	}
	const channelID = "twitter_sample_channel"
	seedWebXRetentionChannel(t, srv, channelID, "sample_channel", 2)
	if err := srv.db.ExecRaw(`
		INSERT INTO channel_settings (channel_id, media_download_limit, updated_at)
		VALUES (?, 2, 1)
	`, channelID); err != nil {
		t.Fatal(err)
	}
	status, body := mutationRequest(t, srv, http.MethodPut, "/api/mutations/channel_setting", `{
		"channel_id":"twitter_sample_channel", "field":"media_download_limit", "value":1, "updated_at_ms":100
	}`)
	if status != http.StatusOK {
		t.Fatalf("mutation status = %d: %v", status, body)
	}
	assertWebXAssetState(t, srv, "sample_channel_1", db.AssetStatePruned)
}

func seedWebXRetentionChannel(t *testing.T, srv *testServer, channelID, handle string, count int) {
	t.Helper()
	if err := srv.db.AddChannel(model.Channel{
		ChannelID: channelID, SourceID: handle, Name: "Sample X Source",
		Platform: "twitter", IsSubscribed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.db.UpsertFeedItems(webXRetentionItems(handle, handle, count)); err != nil {
		t.Fatal(err)
	}
}

func webXRetentionItems(prefix, handle string, count int) []model.FeedItem {
	items := make([]model.FeedItem, 0, count)
	for i := 1; i <= count; i++ {
		published := time.Unix(int64(i*100), 0)
		items = append(items, model.FeedItem{
			TweetID: fmt.Sprintf("%s_%d", prefix, i), SourceHandle: handle, AuthorHandle: handle,
			MediaJSON:   fmt.Sprintf(`[{"url":"https://cdn.example/%s_%d.jpg","type":"photo"}]`, prefix, i),
			PublishedAt: &published,
		})
	}
	return items
}

func postChannelXMediaLimit(t *testing.T, srv *testServer, channelID string, limit int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/"+channelID+"/settings", strings.NewReader(fmt.Sprintf(`{"media_download_limit":%d}`, limit)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("channel settings status = %d: %s", rec.Code, rec.Body.String())
	}
}

func assertWebXAssetState(t *testing.T, srv *testServer, ownerID, want string) {
	t.Helper()
	asset, err := srv.db.GetAsset(db.BuildAssetID("twitter", "tweet", ownerID, "post_media", 0), "post_media")
	if err != nil {
		t.Fatal(err)
	}
	if asset == nil || asset.State != want {
		t.Fatalf("asset %s = %+v, want state %s", ownerID, asset, want)
	}
}
