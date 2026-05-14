package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type tweetMediaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tweetMediaRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHandleTweetMediaMoveRejectsHTMLDisguisedAsMP4(t *testing.T) {
	srv := newTestServer(t)

	stagingDir := t.TempDir()
	archiveDir := t.TempDir()
	categoryID, err := srv.db.CreateBookmarkCategory("alice", "Memes", archiveDir)
	if err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}
	if err := srv.db.SetSetting("", "x_media_staging_dir", stagingDir); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	stagedName := "tmp_2052388503678337240_0.mp4"
	stagedPath := filepath.Join(stagingDir, stagedName)
	if err := os.WriteFile(stagedPath, []byte("<!DOCTYPE html><html><body>not a video</body></html>"), 0o644); err != nil {
		t.Fatalf("write staged fixture: %v", err)
	}

	body := strings.NewReader(`{
		"handle": "compliantvc",
		"label": "nokia",
		"category_id": ` + strconv.FormatInt(categoryID, 10) + `,
		"staged_files": [{"staging_name": "` + stagedName + `", "ext": ".mp4"}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tweet-media-move", body)
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuthRole(req, "alice", "admin")
	rec := httptest.NewRecorder()

	srv.handleTweetMediaMove(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool     `json:"success"`
		Moved   []string `json:"moved"`
		Failed  []string `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success {
		t.Fatalf("success: got true, response=%s", rec.Body.String())
	}
	if len(resp.Moved) != 0 {
		t.Fatalf("moved: got %v, want none", resp.Moved)
	}
	if len(resp.Failed) != 1 || resp.Failed[0] != stagedName {
		t.Fatalf("failed: got %v, want [%s]", resp.Failed, stagedName)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "compliantvc nokia 001.mp4")); !os.IsNotExist(err) {
		t.Fatalf("invalid mp4 was archived, stat err=%v", err)
	}
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatalf("invalid staging file should be removed, stat err=%v", err)
	}
}

func TestNormalizeTweetMediaExtRejectsTraversal(t *testing.T) {
	for _, raw := range []string{".mp4/../../escape", `jpg\..\escape`, "..jpg"} {
		if _, err := normalizeTweetMediaExt(raw, "fallback.jpg"); err == nil {
			t.Fatalf("normalizeTweetMediaExt(%q) succeeded, want error", raw)
		}
	}
	if got, err := normalizeTweetMediaExt("png", "fallback.jpg"); err != nil || got != ".png" {
		t.Fatalf("normalizeTweetMediaExt png = %q, %v; want .png nil", got, err)
	}
}

func TestHandleTweetMediaDlArchivesDirectMediaURL(t *testing.T) {
	srv := newTestServer(t)

	archiveDir := t.TempDir()
	categoryID, err := srv.db.CreateBookmarkCategory("sample_user", "Clips", archiveDir)
	if err != nil {
		t.Fatalf("CreateBookmarkCategory: %v", err)
	}

	var requestedURL string
	srv.workers.Downloader().HTTP.AllowPrivateHosts = true
	srv.workers.Downloader().HTTP.Client = &http.Client{Transport: tweetMediaRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestedURL = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("\x00\x00\x00\x18ftypmp42sample")),
			Request:    req,
		}, nil
	})}

	body := strings.NewReader(`{
		"tweet_url": "https://x.com/sample_handle/status/111",
		"media_url": "https://video.twimg.com/ext_tw_video/999/pu/vid/640x360/high.mp4?tag=12",
		"media_id": "999",
		"media_index": 0,
		"handle": "sample_handle",
		"label": "clip",
		"category_id": ` + strconv.FormatInt(categoryID, 10) + `
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tweet-media-dl", body)
	req.Header.Set("Content-Type", "application/json")
	req = attachTestAuthRole(req, "sample_user", "admin")
	rec := httptest.NewRecorder()

	srv.handleTweetMediaDl(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d - %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool     `json:"success"`
		Moved   []string `json:"moved"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Success || len(resp.Moved) != 1 || resp.Moved[0] != "sample_handle clip 001.mp4" {
		t.Fatalf("response = %#v", resp)
	}
	if requestedURL != "https://video.twimg.com/ext_tw_video/999/pu/vid/640x360/high.mp4?tag=12" {
		t.Fatalf("requested URL = %q", requestedURL)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "sample_handle clip 001.mp4")); err != nil {
		t.Fatalf("archived direct video: %v", err)
	}
}
