package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// #12 — appendClientLog writes one JSON line per entry to the named
// file under logs/android/, attributing user + device_id when present.
func TestClientLogServerAppendsBatch(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	srv.cfg.DataDir = dataDir

	body := `{
	  "device_id": "dev-123",
	  "entries": [
	    {"event": "sync_start", "level": "info", "timestamp_ms": 1745100000000, "fields": {"cycle": 1}},
	    {"event": "sync_done",  "level": "info", "timestamp_ms": 1745100050000}
	  ]
	}`
	req := httptest.NewRequest("POST", "/api/logs/server", strings.NewReader(body))
	req = attachTestAuth(req, "alice")
	rec := httptest.NewRecorder()
	srv.handleClientLogServer(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d — %s", rec.Code, rec.Body.String())
	}

	logPath := filepath.Join(dataDir, "logs", "android", "server.log")
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	var rows []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var row map[string]any
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("decode log row: %v (%s)", err, sc.Text())
		}
		rows = append(rows, row)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0]["event"] != "sync_start" {
		t.Errorf("row[0] event = %v, want sync_start", rows[0]["event"])
	}
	if rows[0]["device_id"] != "dev-123" {
		t.Errorf("device_id missing or wrong: %v", rows[0]["device_id"])
	}
	if rows[0]["user"] != "alice" {
		t.Errorf("user attribution missing: %v", rows[0]["user"])
	}
	if rows[1]["event"] != "sync_done" {
		t.Errorf("row[1] event = %v", rows[1]["event"])
	}
}

func TestClientLogDebugWritesToDebugFile(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	srv.cfg.DataDir = dataDir

	body := `{"entries": [{"event": "ingest_bundle", "timestamp_ms": 1745100000000}]}`
	rec := httptest.NewRecorder()
	srv.handleClientLogDebug(rec, httptest.NewRequest("POST", "/api/logs/debug", strings.NewReader(body)))

	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "logs", "android", "debug.log")); err != nil {
		t.Errorf("debug.log not created: %v", err)
	}
}

func TestClientLogMomentsWritesPersistentJSONL(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	srv.cfg.DataDir = dataDir

	body := `{"device_id":"web-moments","entries":[{"event":"moments_video_debug","level":"debug","timestamp_ms":1745100000000,"fields":{"id":"short_1","bands":{"bottom":{"darkPct":100}}}}]}`
	rec := httptest.NewRecorder()
	srv.handleClientLogMoments(rec, httptest.NewRequest("POST", "/api/logs/moments", strings.NewReader(body)))

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	logPath := filepath.Join(dataDir, "logs", "moments", "debug.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read moments debug log: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &row); err != nil {
		t.Fatalf("decode moments debug row: %v (%s)", err, string(raw))
	}
	if row["event"] != "moments_video_debug" {
		t.Fatalf("event = %v, want moments_video_debug", row["event"])
	}
	if row["device_id"] != "web-moments" {
		t.Fatalf("device_id = %v, want web-moments", row["device_id"])
	}
}

func TestClientLogCapsBatchSize(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	srv.cfg.DataDir = dataDir

	// 200 entries — server caps at 100.
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := 0; i < 200; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"event":"e","timestamp_ms":1}`)
	}
	b.WriteString(`]}`)

	rec := httptest.NewRecorder()
	srv.handleClientLogServer(rec, httptest.NewRequest("POST", "/api/logs/server", strings.NewReader(b.String())))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	written, _ := resp["written"].(float64)
	if int(written) != 100 {
		t.Errorf("expected cap at 100, got %v", written)
	}
}

func TestClientLogRejectsInvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	srv.cfg.DataDir = t.TempDir()

	rec := httptest.NewRecorder()
	srv.handleClientLogServer(rec, httptest.NewRequest("POST", "/api/logs/server", strings.NewReader("{not json")))

	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error_code"] != "invalid_body" {
		t.Errorf("expected error_code=invalid_body, got %v", body["error_code"])
	}
}

func TestAndroidStatusRendersPersistentSyncHealth(t *testing.T) {
	srv := newTestServer(t)
	nowMs := time.Now().Add(-2 * time.Minute).UnixMilli()
	gen := model.AndroidSyncGeneration{
		GenerationID:            "android-sync-healthtest",
		CreatedAtMs:             nowMs - 30_000,
		Status:                  "ready",
		SourceVersion:           "healthtest-source",
		Retention:               map[string]int{"feed_days": 7, "youtube_days": 7, "moments_days": 7, "story_hours": 48},
		ItemCount:               44,
		AssetCount:              100,
		ReadyAssetCount:         95,
		ServerMissingAssetCount: 5,
		ContentCounts:           map[string]int{"feed_items": 12, "videos": 32},
		AssetCounts:             map[string]int{"post_media": 8, "post_thumbnail": 4, "video_stream": 80, "avatar": 8},
	}
	if err := srv.db.StoreAndroidSyncGeneration(gen, nil, nil); err != nil {
		t.Fatalf("store generation: %v", err)
	}
	if err := srv.db.RecordAndroidSyncHealth(
		gen.GenerationID,
		nowMs,
		[]byte(`{"retention":{"feed_days":7,"youtube_days":7,"moments_days":7,"story_hours":48},"counts":{"total":100,"verified":75,"pending":20,"failed":0,"missing":5},"bytes":{"verified":1048576}}`),
		75,
		20,
		0,
		5,
		100,
		1048576,
	); err != nil {
		t.Fatalf("record health: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/logs/android/status?fmt=html", nil)
	srv.handleAndroidStatus(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d - %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Generation", "Device assets", "75%", "75/100", "Asset Health", "Server missing"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Never") {
		t.Fatalf("dashboard should not render transient Never state:\n%s", body)
	}
}
