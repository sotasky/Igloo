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
)

// #12 — appendClientLog writes one JSON line per entry to the named
// file under logs/android/, attributing user + device_id when present.
func TestClientLogServerAppendsBatch(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	setTestStateRoot(t, srv.cfg, dataDir)

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
	defer func() {
		_ = f.Close()
	}()

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
	setTestStateRoot(t, srv.cfg, dataDir)

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

func TestLogPathForTypeRejectsTraversalType(t *testing.T) {
	srv := newTestServer(t)
	setTestStateRoot(t, srv.cfg, t.TempDir())

	if _, ok := srv.logPathForType("../../outside"); ok {
		t.Fatal("traversal log type was accepted")
	}
	path, ok := srv.logPathForType("server")
	if !ok {
		t.Fatal("server log type was rejected")
	}
	if want := filepath.Join(srv.cfg.Storage.StateRoot(), "logs", "server", "server.log"); path != want {
		t.Fatalf("server log path = %q, want %q", path, want)
	}
}

func TestServerRawLogHTMLKeepsShellWhenLogIsAbsent(t *testing.T) {
	srv := newTestServer(t)
	setTestStateRoot(t, srv.cfg, t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/logs/server/read?type=server&fmt=html&raw_filter=errors", nil)
	srv.handleLogsServer(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %q, want HTML", rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{"Server Log", `id="sv-raw-log-console"`, "Errors"} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty raw log fragment missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `"success"`) {
		t.Fatalf("empty raw log fragment rendered JSON:\n%s", body)
	}
}

func TestClientLogMomentsWritesPersistentJSONL(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	setTestStateRoot(t, srv.cfg, dataDir)

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

func TestClientLogMomentsRotatesBeforeAppend(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	setTestStateRoot(t, srv.cfg, dataDir)

	logPath := filepath.Join(dataDir, "logs", "moments", "debug.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(logPath, bytes.Repeat([]byte("x"), int(momentsLogRotateByte)+1), 0o644); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	body := `{"device_id":"web-moments","entries":[{"event":"moments_video_debug","level":"debug","timestamp_ms":1745100000000}]}`
	rec := httptest.NewRecorder()
	srv.handleClientLogMoments(rec, httptest.NewRequest("POST", "/api/logs/moments", strings.NewReader(body)))

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read new log: %v", err)
	}
	if bytes.Contains(raw, []byte("xxxxx")) {
		t.Fatalf("new log kept old oversized content")
	}
	if !bytes.Contains(raw, []byte("moments_video_debug")) {
		t.Fatalf("new log missing appended event: %s", string(raw))
	}
}

func TestClientLogCapsBatchSize(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	setTestStateRoot(t, srv.cfg, dataDir)

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
	setTestStateRoot(t, srv.cfg, t.TempDir())

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
	clock, err := srv.db.GetAndroidSyncClock()
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := encodeAndroidSyncCursor(androidSyncCursor{
		Version: androidSyncModelVersion, Mode: "changes", Epoch: clock.Epoch,
		Revision: clock.Revision, Retention: androidSyncRetentionHash(srv.androidSyncRetentionFallback()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.db.RecordAndroidSyncHealth(
		cursor,
		nowMs,
		[]byte(`{"retention":{"feed_days":7,"youtube_days":7,"moments_days":7,"story_hours":48},"counts":{"total":100,"verified":75,"pending":20,"missing":5},"bytes":{"verified":1048576}}`),
		75,
		20,
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
	for _, want := range []string{"Device assets", "75%", "75/100", "Asset Health", "Server missing", "5 missing assets", "Feed 7 days"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Never") {
		t.Fatalf("dashboard should not render transient Never state:\n%s", body)
	}
}

func TestAndroidStatusRendersStructuredActivityDetails(t *testing.T) {
	srv := newTestServer(t)
	dataDir := t.TempDir()
	setTestStateRoot(t, srv.cfg, dataDir)
	logPath := filepath.Join(dataDir, "logs", "android", "server.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	entries := strings.Join([]string{
		`{"timestamp_ms":1,"level":"info","event":"android_sync_health_reported","fields":{"cursor":"opaque-cursor-value","uploaded":true,"verified":75,"pending":20,"missing":5,"total":100,"upload_elapsed_ms":120}}`,
		`{"timestamp_ms":2,"level":"info","event":"android_sync_asset_drain_done","fields":{"downloaded":2,"verified_existing":5,"deferred":1}}`,
		`{"timestamp_ms":3,"level":"info","event":"android_sync_metadata_retry","fields":{"label":"changes","attempt":2}}`,
	}, "\n")
	if err := os.WriteFile(logPath, []byte(entries+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/logs/android/status?fmt=html", nil)
	srv.handleAndroidStatus(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d - %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"uploaded · 75/100 verified · 20 pending · 5 missing · 120 ms",
		"2 downloaded · 5 already present · 1 deferred",
		"changes · attempt 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "opaque-cursor-value") {
		t.Fatalf("dashboard exposed raw activity fields:\n%s", body)
	}
}
