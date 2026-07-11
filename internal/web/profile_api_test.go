package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

func TestProfileCardRendersCommittedProfileWithoutRequestingWork(t *testing.T) {
	srv := newTestServer(t)
	stale := time.Now().Add(-48 * time.Hour).UTC()
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_sample_author",
		Platform:    "twitter",
		Handle:      "sample_author",
		DisplayName: "Sample Author",
		Followers:   10,
		FetchedAt:   &stale,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/profile-card/twitter_sample_author", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Sample Author") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Igloo-Profile-Refreshing"); got != "" {
		t.Fatalf("render response advertised request-time refresh: %q", got)
	}
	job, err := srv.db.GetProfileJob("twitter_sample_author")
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Fatalf("profile render created durable work: %+v", job)
	}
	got, err := srv.db.GetChannelProfile("twitter_sample_author")
	if err != nil || got == nil || got.FetchedAt == nil || got.FetchedAt.UnixMilli() != stale.UnixMilli() {
		t.Fatalf("profile changed during render: %+v / %v", got, err)
	}
}

func TestProfileCardMissIsReadOnly(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/profile-card/twitter_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	profile, err := srv.db.GetChannelProfile("twitter_missing")
	if err != nil || profile != nil {
		t.Fatalf("missing profile was created: %+v / %v", profile, err)
	}
	job, err := srv.db.GetProfileJob("twitter_missing")
	if err != nil || job != nil {
		t.Fatalf("missing profile queued work: %+v / %v", job, err)
	}
}

func TestProfileCardUsesCanonicalBannerReadiness(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now().UTC()
	const channelID = "tiktok_sample_creator"
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   channelID,
		Platform:    "tiktok",
		Handle:      "sample_creator",
		DisplayName: "Sample Creator",
		BannerURL:   "https://cdn.example.invalid/banner.jpg",
		FetchedAt:   &now,
	}); err != nil {
		t.Fatal(err)
	}

	render := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/profile-card/"+channelID, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if body := render(); !strings.Contains(body, "profile-card--no-banner") {
		t.Fatalf("queued banner rendered as ready: %s", body)
	}

	relPath := filepath.Join("thumbnails", "banners", channelID+".jpg")
	absPath := filepath.Join(srv.cfg.Storage.StateRoot(), relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("ready-banner"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.StoreReadyAsset(db.Asset{
		AssetID:        db.BuildAssetID("tiktok", "channel", channelID, "banner", 0),
		AssetKind:      "banner",
		OwnerKind:      "channel",
		OwnerID:        channelID,
		SourceURL:      "https://cdn.example.invalid/banner.jpg",
		FilePath:       relPath,
		ContentType:    "image/jpeg",
		RequiredReason: "identity",
	}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if body := render(); strings.Contains(body, "profile-card--no-banner") || !strings.Contains(body, "/api/media/banner/"+channelID) {
		t.Fatalf("ready canonical banner did not render: %s", body)
	}
}

func TestProfileCardTombstoned404(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID: "twitter_sample_ghost",
		Platform:  "twitter",
		Tombstone: true,
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/profile-card/twitter_sample_ghost", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestProfileCardInvalidChannelID400(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/profile-card/bad", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestProfileOnlyRefreshEnqueuesDurableJob(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.UpsertChannelProfile(model.ChannelProfile{
		ChannelID:   "twitter_test_refresh",
		Platform:    "twitter",
		Handle:      "test_refresh",
		DisplayName: "Sample Refresh",
	}); err != nil {
		t.Fatal(err)
	}

	for wantRevision := int64(1); wantRevision <= 2; wantRevision++ {
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/channels/twitter_test_refresh/refresh", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("refresh %d status = %d: %s", wantRevision, rec.Code, rec.Body.String())
		}
		job, err := srv.db.GetProfileJob("twitter_test_refresh")
		if err != nil || job == nil || job.RequestedRevision != wantRevision {
			t.Fatalf("refresh %d job = %+v / %v", wantRevision, job, err)
		}
	}
}
