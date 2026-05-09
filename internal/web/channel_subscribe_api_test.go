package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestChannelSubscribeRouteFollowsExistingTempChannel(t *testing.T) {
	srv := newTestServer(t)
	const channelID = "youtube_UCtempchannel"
	if err := srv.db.AddChannel(model.Channel{
		ChannelID: channelID,
		SourceID:  "UCtempchannel",
		Name:      "Temp Channel",
		URL:       "https://www.youtube.com/channel/UCtempchannel",
		Platform:  "youtube",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/channels/"+channelID+"/subscribe", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !srv.db.IsChannelFollowed(channelID) {
		t.Fatal("expected existing temp channel to gain a follow row")
	}
	var body struct {
		Success    bool   `json:"success"`
		ChannelID  string `json:"channel_id"`
		Subscribed bool   `json:"subscribed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v; body = %s", err, rec.Body.String())
	}
	if !body.Success || !body.Subscribed || body.ChannelID != channelID {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestChannelSubscribeRouteRejectsUnknownChannel(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("POST", "/api/channels/youtube_UCmissing/subscribe", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if srv.db.IsChannelFollowed("youtube_UCmissing") {
		t.Fatal("unknown channel should not create a follow row")
	}
}

func TestChannelSubscribeRouteCanonicalizesBareYouTubeID(t *testing.T) {
	srv := newTestServer(t)
	const rawID = "UCtempchannel"
	const channelID = "youtube_" + rawID
	if err := srv.db.AddChannel(model.Channel{
		ChannelID: channelID,
		SourceID:  rawID,
		Name:      "Temp Channel",
		URL:       "https://www.youtube.com/channel/" + rawID,
		Platform:  "youtube",
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/channels/"+rawID+"/subscribe", nil)
	rec := httptest.NewRecorder()

	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !srv.db.IsChannelFollowed(channelID) {
		t.Fatal("expected canonical channel to gain a follow row")
	}
	if srv.db.IsChannelFollowed(rawID) {
		t.Fatal("bare YouTube id should not gain a follow row")
	}
}
