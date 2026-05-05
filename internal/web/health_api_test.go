package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestHealthHandlerShape(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/health", nil)
	s.handleHealth(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("expected ok=true, got %v", body["ok"])
	}
	ts, ok := body["server_time_ms"].(float64)
	if !ok {
		t.Fatalf("expected server_time_ms numeric, got %T", body["server_time_ms"])
	}
	if ts <= 0 {
		t.Errorf("server_time_ms should be positive, got %v", ts)
	}
}
