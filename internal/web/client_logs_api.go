package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// #12 — unified client-log ingest endpoints.
//
// POST /api/logs/server  — always-on, significant events + errors.
//   Appended to ~/.local/share/igloo/logs/android/server.log.
// POST /api/logs/debug   — debug-mode-gated Android/client diagnostics.
//   Appended to ~/.local/share/igloo/logs/android/debug.log.
// POST /api/logs/moments — debug-mode-gated web Moments diagnostics.
//   Appended to ~/.local/share/igloo/logs/moments/debug.jsonl.
//
// Replaces the v1 /api/logs/android/* sprawl (audit Part 34 + Bundle D
// "three sinks → one upload channel"). Both endpoints accept a JSON
// batch payload — client caps at 100 per call; server accepts up to that.

type clientLogEntry struct {
	Level       string         `json:"level,omitempty"`
	Event       string         `json:"event"`
	Fields      map[string]any `json:"fields,omitempty"`
	TimestampMs int64          `json:"timestamp_ms"`
}

type clientLogBatch struct {
	Entries  []clientLogEntry `json:"entries"`
	DeviceID string           `json:"device_id,omitempty"`
}

const (
	clientLogMaxEntries  = 100
	clientLogMaxBodyByte = 256 * 1024 // 256 KB / batch
	clientLogRotateByte  = 10 * 1024 * 1024
	momentsLogRotateByte = 5 * 1024 * 1024
)

var clientLogMu sync.Mutex

func (s *Server) registerClientLogsAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/logs/server", s.handleClientLogServer)
	mux.HandleFunc("POST /api/logs/debug", s.handleClientLogDebug)
	mux.HandleFunc("POST /api/logs/moments", s.handleClientLogMoments)
}

func (s *Server) handleClientLogServer(w http.ResponseWriter, r *http.Request) {
	s.appendClientLog(w, r, filepath.Join("android", "server.log"))
}

func (s *Server) handleClientLogDebug(w http.ResponseWriter, r *http.Request) {
	s.appendClientLog(w, r, filepath.Join("android", "debug.log"))
}

func (s *Server) handleClientLogMoments(w http.ResponseWriter, r *http.Request) {
	s.appendClientLog(w, r, filepath.Join("moments", "debug.jsonl"))
}

func (s *Server) appendClientLog(w http.ResponseWriter, r *http.Request, logRelPath string) {
	r.Body = http.MaxBytesReader(w, r.Body, clientLogMaxBodyByte)
	var batch clientLogBatch
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		writeJSONError(w, 400, "invalid_body", "invalid JSON")
		return
	}
	if len(batch.Entries) == 0 {
		writeJSON(w, 200, map[string]any{"written": 0})
		return
	}
	if len(batch.Entries) > clientLogMaxEntries {
		batch.Entries = batch.Entries[:clientLogMaxEntries]
	}

	user := userFromContext(r.Context())
	username := ""
	if user != nil {
		username = user.Username
	}

	path := filepath.Join(s.cfg.Storage.StateRoot(), "logs", logRelPath)
	logDir := filepath.Dir(path)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		writeJSONError(w, 500, "log_write_failed", fmt.Sprintf("mkdir: %v", err))
		return
	}

	clientLogMu.Lock()
	defer clientLogMu.Unlock()

	if err := rotateClientLogIfNeeded(path, clientLogRotateLimit(logRelPath)); err != nil {
		writeJSONError(w, 500, "log_write_failed", fmt.Sprintf("rotate: %v", err))
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		writeJSONError(w, 500, "log_write_failed", fmt.Sprintf("open: %v", err))
		return
	}
	defer func() {
		_ = f.Close()
	}()

	written := 0
	receivedAtMs := time.Now().UnixMilli()
	for _, e := range batch.Entries {
		// Each line is a self-describing JSON object: timestamps for
		// both client emit time and server receive time, plus the
		// envelope-style attribution fields (username, device_id) that
		// debug viewers can filter on.
		row := map[string]any{
			"timestamp_ms":   e.TimestampMs,
			"received_at_ms": receivedAtMs,
			"event":          e.Event,
		}
		if e.Level != "" {
			row["level"] = e.Level
		}
		if len(e.Fields) > 0 {
			row["fields"] = e.Fields
		}
		if batch.DeviceID != "" {
			row["device_id"] = batch.DeviceID
		}
		if username != "" {
			row["user"] = username
		}
		buf, mErr := json.Marshal(row)
		if mErr != nil {
			continue
		}
		buf = append(buf, '\n')
		if _, wErr := f.Write(buf); wErr != nil {
			writeJSONError(w, 500, "log_write_failed", fmt.Sprintf("write: %v", wErr))
			return
		}
		written++
	}

	writeJSON(w, 200, map[string]any{"written": written})
}

func clientLogRotateLimit(logRelPath string) int64 {
	switch filepath.ToSlash(logRelPath) {
	case "moments/debug.jsonl":
		return momentsLogRotateByte
	default:
		return clientLogRotateByte
	}
}

func rotateClientLogIfNeeded(path string, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Size() <= maxBytes {
		return nil
	}
	rotated := path + ".1"
	_ = os.Remove(rotated)
	return os.Rename(path, rotated)
}
