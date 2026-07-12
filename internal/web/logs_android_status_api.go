package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
)

type androidStatusLogEntry struct {
	TimestampMs int64          `json:"timestamp_ms"`
	Level       string         `json:"level"`
	Event       string         `json:"event"`
	Fields      map[string]any `json:"fields"`
}

type androidStatusError struct {
	Tag       string `json:"tag"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
	Count     int    `json:"count"`
}

func (s *Server) handleAndroidStatus(w http.ResponseWriter, r *http.Request) {
	clock, err := s.db.GetAndroidSyncClock()
	if err != nil {
		slog.Warn("android dashboard sync clock failed", "err", err)
	}
	health, err := s.db.GetLatestAndroidSyncHealthReport()
	if err != nil {
		slog.Warn("android dashboard health failed", "err", err)
	}

	ready, missing := 0, 0
	if err := s.db.QueryRow(`
		SELECT
			COALESCE(SUM(mo.published_revision > 0 AND mo.file_path != ''), 0),
			COALESCE(SUM(mo.published_revision = 0 AND mo.job_state IN ('server_missing', 'permanent_missing')), 0)
		FROM assets a JOIN media_objects mo ON mo.object_id = a.desired_object_id
		WHERE a.lifecycle_state != 'pruned'
	`).Scan(&ready, &missing); err != nil {
		slog.Warn("android dashboard asset summary failed", "err", err)
	}

	entries := readAndroidStatusLogEntries(s.cfg.Storage.StateRoot(), 500)
	activity, errors, warnings := summarizeAndroidStatusLogs(entries)
	retention := db.AndroidRetentionSettings{FeedDays: 7, YoutubeDays: 7, MomentsDays: 7, StoryHours: 48}
	if health != nil && health.HasRetention {
		retention = health.Retention
	}

	if r.URL.Query().Get("fmt") == "html" {
		filter := r.URL.Query().Get("filter")
		if filter == "" {
			filter = "all"
		}
		data := components.AndroidDashboardData{
			ErrorCount:   len(errors),
			WarningCount: len(warnings),
			LogFilter:    filter,
		}
		if health != nil {
			data.DeviceVerified = health.VerifiedAssets
			data.DevicePending = health.PendingAssets
			data.DeviceMissing = health.MissingAssets
			data.DeviceTotal = health.TotalAssets
			data.DevicePercent = androidStatusPercent(health.VerifiedAssets, health.TotalAssets)
			data.DeviceBytes = formatAndroidStatusBytes(health.VerifiedBytes)
			data.SyncCompletedHMS = time.UnixMilli(health.ReportedAtMs).Local().Format("15:04:05")
			data.SyncAgo = formatAndroidStatusAgo(time.Since(time.UnixMilli(health.ReportedAtMs)))
		}
		data.CacheHealth = androidStatusAssetRows(health, ready+missing > 0, ready, missing)
		for _, entry := range activity {
			data.Activity = append(data.Activity, components.AndroidLogEntry{
				Timestamp: androidStatusClock(entry.TimestampMs),
				Tag:       entry.Event, Message: androidStatusMessage(entry),
				LevelCSS: androidStatusLevelCSS(entry.Level),
			})
		}
		for _, entry := range errors {
			data.Errors = append(data.Errors, components.AndroidErrorEntry{
				Tag: entry.Tag, Message: entry.Message, Timestamp: entry.Timestamp, Count: entry.Count,
			})
		}
		for _, entry := range warnings {
			data.Warnings = append(data.Warnings, components.AndroidWarningEntry{
				Tag: entry.Tag, Message: entry.Message,
			})
		}
		w.Header().Set("Content-Type", "text/html")
		_ = components.AndroidDashboard(s.pageProps(w, r), data).Render(r.Context(), w)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sync_revision": clock.Revision,
		"health":        health,
		"retention": map[string]int{
			"feed_days": retention.FeedDays, "youtube_days": retention.YoutubeDays,
			"moments_days": retention.MomentsDays, "story_hours": retention.StoryHours,
		},
		"server_assets": map[string]int{"ready": ready, "missing": missing},
		"activity":      activity,
		"errors":        errors,
		"warnings":      warnings,
	})
}

func readAndroidStatusLogEntries(dataRoot string, limit int) []androidStatusLogEntry {
	lines, err := readLastLines(filepath.Join(dataRoot, "logs", "android", "server.log"), limit)
	if err != nil {
		return []androidStatusLogEntry{}
	}
	out := make([]androidStatusLogEntry, 0, len(lines))
	for _, line := range lines {
		var entry androidStatusLogEntry
		if json.Unmarshal([]byte(line), &entry) != nil || strings.TrimSpace(entry.Event) == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func summarizeAndroidStatusLogs(entries []androidStatusLogEntry) ([]androidStatusLogEntry, []androidStatusError, []androidStatusError) {
	activity := make([]androidStatusLogEntry, 0, min(50, len(entries)))
	errors := make([]androidStatusError, 0)
	warnings := make([]androidStatusError, 0)
	errorIndex := map[string]int{}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if len(activity) < 50 {
			activity = append(activity, entry)
		}
		level := strings.ToLower(entry.Level)
		if level != "error" && level != "warn" && level != "warning" {
			continue
		}
		row := androidStatusError{
			Tag: entry.Event, Message: androidStatusMessage(entry),
			Timestamp: androidStatusClock(entry.TimestampMs), Count: 1,
		}
		if level == "error" {
			key := row.Tag + "\x00" + row.Message
			if index, ok := errorIndex[key]; ok {
				errors[index].Count++
				continue
			}
			errorIndex[key] = len(errors)
			errors = append(errors, row)
		} else if len(warnings) < 20 {
			warnings = append(warnings, row)
		}
	}
	return activity, errors, warnings
}

func androidStatusMessage(entry androidStatusLogEntry) string {
	for _, key := range []string{"message", "error", "reason"} {
		if value, ok := entry.Fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func androidStatusClock(timestampMs int64) string {
	if timestampMs <= 0 {
		return ""
	}
	return time.UnixMilli(timestampMs).Local().Format("15:04:05")
}

func androidStatusLevelCSS(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "log-lvl-err"
	case "warn", "warning":
		return "log-lvl-warn"
	default:
		return "log-lvl-info"
	}
}

func formatAndroidStatusAgo(age time.Duration) string {
	seconds := max(0, int(age.Seconds()))
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds ago", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm ago", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh %dm ago", seconds/3600, (seconds%3600)/60)
	default:
		return fmt.Sprintf("%dd ago", seconds/86400)
	}
}

func formatAndroidStatusBytes(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/(1<<10))
	case bytes > 0:
		return fmt.Sprintf("%d B", bytes)
	default:
		return ""
	}
}

func androidStatusPercent(value, total int) int {
	if total <= 0 {
		return 0
	}
	return min(100, max(0, value*100/total))
}

func androidStatusAssetRows(report *db.AndroidSyncHealthReport, hasSnapshot bool, ready, missing int) []components.AndroidCacheRow {
	if report == nil {
		if !hasSnapshot || ready+missing == 0 {
			return nil
		}
		total := ready + missing
		return []components.AndroidCacheRow{
			{Label: "Server ready", Cached: ready, Total: total, Percent: androidStatusPercent(ready, total), BarCSS: "an-cache-bar-good"},
			{Label: "Server missing", Cached: missing, Total: total, Percent: androidStatusPercent(missing, total), BarCSS: "an-cache-bar-bad"},
		}
	}
	if report.TotalAssets <= 0 {
		return nil
	}
	return []components.AndroidCacheRow{
		{Label: "Verified", Cached: report.VerifiedAssets, Total: report.TotalAssets, Percent: androidStatusPercent(report.VerifiedAssets, report.TotalAssets), BarCSS: "an-cache-bar-good"},
		{Label: "Pending", Cached: report.PendingAssets, Total: report.TotalAssets, Percent: androidStatusPercent(report.PendingAssets, report.TotalAssets), BarCSS: "an-cache-bar-ok"},
		{Label: "Server missing", Cached: report.MissingAssets, Total: report.TotalAssets, Percent: androidStatusPercent(report.MissingAssets, report.TotalAssets), BarCSS: "an-cache-bar-bad"},
	}
}
