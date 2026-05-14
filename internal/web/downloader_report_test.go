package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestDownloaderReportEndpointsRequireAdmin(t *testing.T) {
	srv := newTestServer(t)
	for _, tc := range []struct {
		name   string
		method string
		path   string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"latest", http.MethodGet, "/api/downloader/report/latest", srv.handleDownloaderReportLatest},
		{"operations", http.MethodGet, "/api/downloader/operations", srv.handleDownloaderOperations},
		{"run", http.MethodPost, "/api/downloader/report/run", srv.handleDownloaderReportRun},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req = req.WithContext(contextWithUser(req, "alice", "user"))
			rec := httptest.NewRecorder()
			tc.call(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDownloaderReportPageDoesNotUseInnerHTMLForReportRows(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/temp/downloader-report", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()

	srv.handlePageDownloaderReport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "innerHTML") {
		t.Fatalf("report page still uses innerHTML: %s", body)
	}
	for _, want := range []string{"textContent", "replaceChildren"} {
		if !strings.Contains(body, want) {
			t.Fatalf("report page missing %q in:\n%s", want, body)
		}
	}
}

func TestDownloaderReportDirUsesIglooDataTmpAndPrunesOldRuns(t *testing.T) {
	srv := newTestServer(t)
	base := filepath.Join(srv.cfg.DataDir, "tmp", "downloader-reports")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	oldDir := filepath.Join(base, "run-old")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	dir, err := srv.createDownloaderReportDir()
	if err != nil {
		t.Fatalf("createDownloaderReportDir: %v", err)
	}
	if !strings.HasPrefix(dir, base+string(os.PathSeparator)) {
		t.Fatalf("report dir = %q, want under %q", dir, base)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old report dir was not pruned, stat err = %v", err)
	}
}

func TestDownloaderOperationsEndpointReturnsRedactedSummaries(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.RecordDownloaderOperation(context.Background(), model.DownloaderOperation{
		Operation:   "x.gallerydl.dump",
		Platform:    "twitter",
		Subject:     "https://x.com/example",
		Tool:        "gallery-dl",
		StartedAtMs: time.Now().UnixMilli(),
		EndedAtMs:   time.Now().UnixMilli(),
		Status:      "failure",
		ErrorKind:   "auth",
		Error:       "cookies=***",
		CookieLabel: "x.com_cookies.txt",
		SummaryJSON: `{"args":["--cookies","***"]}`,
	}); err != nil {
		t.Fatalf("RecordDownloaderOperation: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/downloader/operations", nil)
	req = req.WithContext(contextWithUser(req, "admin", "admin"))
	rec := httptest.NewRecorder()
	srv.handleDownloaderOperations(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Operations []model.DownloaderOperation `json:"operations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Operations) != 1 {
		t.Fatalf("operations = %#v", body.Operations)
	}
	if body.Operations[0].CookieLabel != "x.com_cookies.txt" || body.Operations[0].ErrorKind != "auth" {
		t.Fatalf("operation = %#v", body.Operations[0])
	}
}
