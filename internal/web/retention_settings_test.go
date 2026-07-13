package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/db"
)

func TestGlobalRetentionDecreasePrunesExistingDemandImmediately(t *testing.T) {
	srv := newTestServer(t)
	seedWebRetentionSource(t, srv, "youtube_global_source", "youtube", 3)

	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"youtube_max_videos":1}`))
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuthRole(req, "sample_admin", "admin")
	rec := httptest.NewRecorder()
	srv.handleUpdateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d: %s", rec.Code, rec.Body.String())
	}
	assertWebRetentionCount(t, srv, "youtube_global_source", 1)
}

func TestChannelRetentionChangesPruneDecreasesAndRefreshIncreases(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("youtube_max_videos", "3"); err != nil {
		t.Fatal(err)
	}
	const channelID = "youtube_sample_source"
	seedWebRetentionSource(t, srv, channelID, "youtube", 3)

	postChannelMaxVideos(t, srv, channelID, 1)
	assertWebRetentionCount(t, srv, channelID, 1)

	if err := srv.db.ExecRaw(`UPDATE channels SET last_checked = 123 WHERE channel_id = ?`, channelID); err != nil {
		t.Fatal(err)
	}
	postChannelMaxVideos(t, srv, channelID, 3)
	assertChannelRefreshQueued(t, srv, channelID)
	assertWebRetentionCount(t, srv, channelID, 1)
}

func TestChannelSettingMutationPrunesDecreasesAndRefreshesIncreases(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.SetSetting("youtube_max_videos", "3"); err != nil {
		t.Fatal(err)
	}
	const channelID = "youtube_sample_source"
	seedWebRetentionSource(t, srv, channelID, "youtube", 3)

	status, body := mutationRequest(t, srv, http.MethodPut, "/api/mutations/channel_setting", `{
		"channel_id":"youtube_sample_source", "field":"max_videos", "value":1, "updated_at_ms":100
	}`)
	if status != http.StatusOK {
		t.Fatalf("decrease status = %d: %v", status, body)
	}
	assertWebRetentionCount(t, srv, channelID, 1)

	if err := srv.db.ExecRaw(`UPDATE channels SET last_checked = 456 WHERE channel_id = ?`, channelID); err != nil {
		t.Fatal(err)
	}
	status, body = mutationRequest(t, srv, http.MethodPut, "/api/mutations/channel_setting", `{
		"channel_id":"youtube_sample_source", "field":"max_videos", "value":3, "updated_at_ms":200
	}`)
	if status != http.StatusOK {
		t.Fatalf("increase status = %d: %v", status, body)
	}
	assertChannelRefreshQueued(t, srv, channelID)
	assertWebRetentionCount(t, srv, channelID, 1)
}

func seedWebRetentionSource(t *testing.T, srv *testServer, channelID, platform string, count int) {
	t.Helper()
	if err := srv.db.ExecRaw(`
		INSERT INTO channels (channel_id, source_id, name, url, platform, last_checked, created_at)
		VALUES (?, ?, 'Sample Source', '', ?, 10, 1);
		INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, 1)
	`, channelID, channelID, platform, channelID); err != nil {
		t.Fatal(err)
	}
	items := make([]db.VideoDesire, 0, count)
	for i := 1; i <= count; i++ {
		items = append(items, db.VideoDesire{
			VideoID:        fmt.Sprintf("%s_video_%d", channelID, i),
			OwnerChannelID: channelID,
			PublishedAtMs:  int64(i * 100),
			SourcePosition: count - i,
			Lane:           db.DownloadLaneBackfill,
		})
	}
	if _, err := srv.db.ReconcileVideoDesires(db.VideoDesireSnapshot{
		SourceChannelID: channelID,
		Component:       "uploads",
		Items:           items,
	}); err != nil {
		t.Fatal(err)
	}
}

func postChannelMaxVideos(t *testing.T, srv *testServer, channelID string, maxVideos int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/channels/"+channelID+"/settings", strings.NewReader(fmt.Sprintf(`{"max_videos":%d}`, maxVideos)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("channel settings status = %d: %s", rec.Code, rec.Body.String())
	}
}

func assertWebRetentionCount(t *testing.T, srv *testServer, channelID string, want int) {
	t.Helper()
	var desires, queued int
	if err := srv.db.QueryRow(`
		SELECT COUNT(DISTINCT video_id) FROM video_desires WHERE source_channel_id = ?
	`, channelID).Scan(&desires); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.QueryRow(`
		SELECT COUNT(*) FROM download_queue WHERE video_id LIKE ?
	`, channelID+"_video_%").Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if desires != want || queued != want {
		t.Fatalf("%s retention = %d desires / %d queued, want %d / %d", channelID, desires, queued, want, want)
	}
}

func assertChannelRefreshQueued(t *testing.T, srv *testServer, channelID string) {
	t.Helper()
	var checked int64
	if err := srv.db.QueryRow(`SELECT last_checked FROM channels WHERE channel_id = ?`, channelID).Scan(&checked); err != nil {
		t.Fatal(err)
	}
	if checked != 0 {
		t.Fatalf("last_checked = %d, want 0 for discovery", checked)
	}
}
