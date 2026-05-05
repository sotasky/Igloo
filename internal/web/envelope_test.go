package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWriteJSONEnvelopeInjectsOkAndServerTime(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONEnvelope(rec, 200, map[string]any{"foo": "bar"})

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("expected ok=true, got %v", body["ok"])
	}
	if _, ok := body["server_time_ms"].(float64); !ok {
		t.Errorf("expected server_time_ms numeric, got %T (%v)", body["server_time_ms"], body["server_time_ms"])
	}
	if body["foo"] != "bar" {
		t.Errorf("expected foo=bar, got %v", body["foo"])
	}
}

func TestWriteJSONEnvelopeMarksErrorOk(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONEnvelope(rec, 500, map[string]any{"error": "db error"})

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != false {
		t.Errorf("expected ok=false on 500, got %v", body["ok"])
	}
	if body["error"] != "db error" {
		t.Errorf("legacy error field should be preserved, got %v", body["error"])
	}
}

func TestWriteJSONEnvelopeRespectsExplicitOk(t *testing.T) {
	// Callers can override ok when status alone isn't sufficient — leave their choice alone.
	rec := httptest.NewRecorder()
	writeJSONEnvelope(rec, 200, map[string]any{"ok": false, "reason": "partial"})

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != false {
		t.Errorf("expected explicit ok=false to survive, got %v", body["ok"])
	}
}

func TestWriteJSONErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, 401, "access_token_expired", "Token expired")

	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != false {
		t.Errorf("expected ok=false, got %v", body["ok"])
	}
	if body["error_code"] != "access_token_expired" {
		t.Errorf("expected error_code=access_token_expired, got %v", body["error_code"])
	}
	if body["error_message"] != "Token expired" {
		t.Errorf("expected error_message, got %v", body["error_message"])
	}
	if _, ok := body["server_time_ms"].(float64); !ok {
		t.Errorf("expected server_time_ms numeric")
	}
}

func TestApiPathGatesNonJSONRoutes(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/feed", true},
		{"/api/mutations/like", true},
		{"/api/media/avatar/youtube_abc", false},
		{"/api/media/thumbnail/vid123", false},
		{"/api/media/subtitle/abc.vtt", false},
		{"/api/media/manifest", false},
		{"/api/media/manifest/health", false},
		{"/api/config/export", false},
		{"/api/config/export-full", false},
		{"/channels", false},
		{"/", false},
	}
	for _, tc := range tests {
		if got := apiPath(tc.path); got != tc.want {
			t.Errorf("apiPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
