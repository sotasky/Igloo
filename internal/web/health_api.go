package web

import (
	"net/http"
	"time"
)

const (
	feedSnapshotHealthGrace          = 15 * time.Minute
	androidSyncHealthReportMaxAge    = 6 * time.Hour
	productHealthStatusHealthy       = "healthy"
	productHealthStatusDegraded      = "degraded"
	productHealthStatusUnhealthy     = "unhealthy"
	productHealthReasonNoData        = "no_data"
	productHealthReasonUnavailable   = "unavailable"
	productHealthReasonCurrent       = "current"
	productHealthReasonStale         = "stale"
	productHealthReasonMissingAssets = "missing_assets"
)

type productHealth struct {
	Status string
	Checks map[string]map[string]any
}

// /api/health/live is a liveness probe: no auth, no DB, no product state. It is
// used by Android reachability and container process checks.
func (s *Server) registerHealthAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health/live", s.handleHealthLive)
	mux.HandleFunc("GET /api/health", s.handleHealth)
}

func (s *Server) handleHealthLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status": "live",
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := s.productHealth(time.Now())
	statusCode := http.StatusOK
	if health.Status == productHealthStatusUnhealthy {
		statusCode = http.StatusServiceUnavailable
	}
	writeJSON(w, statusCode, health.response())
}

func (s *Server) productHealth(now time.Time) productHealth {
	checks := map[string]map[string]any{}
	status := productHealthStatusHealthy

	feedStatus, feed := s.feedSnapshotProductHealth(now)
	checks["feed_snapshot"] = feed
	status = mergeProductHealthStatus(status, feedStatus)

	syncStatus, sync := s.androidSyncProductHealth(now)
	checks["android_sync"] = sync
	status = mergeProductHealthStatus(status, syncStatus)

	return productHealth{
		Status: status,
		Checks: checks,
	}
}

func (h productHealth) response() map[string]any {
	return map[string]any{
		"ok":     h.Status == productHealthStatusHealthy,
		"status": h.Status,
		"checks": h.Checks,
	}
}

func mergeProductHealthStatus(current, next string) string {
	if current == productHealthStatusUnhealthy || next == productHealthStatusUnhealthy {
		return productHealthStatusUnhealthy
	}
	if current == productHealthStatusDegraded || next == productHealthStatusDegraded {
		return productHealthStatusDegraded
	}
	return productHealthStatusHealthy
}

func (s *Server) feedSnapshotProductHealth(now time.Time) (string, map[string]any) {
	check := map[string]any{
		"status": productHealthStatusHealthy,
		"reason": productHealthReasonCurrent,
	}
	if s.db == nil {
		check["status"] = productHealthStatusUnhealthy
		check["reason"] = productHealthReasonUnavailable
		return productHealthStatusUnhealthy, check
	}

	snapshot, err := s.db.GetFeedSnapshotHealth()
	if err != nil {
		check["status"] = productHealthStatusUnhealthy
		check["reason"] = err.Error()
		return productHealthStatusUnhealthy, check
	}
	check["users_checked"] = 1
	check["users_with_data"] = 0
	if snapshot.CandidateCount > 0 {
		check["users_with_data"] = 1
	}
	check["snapshot_at_ms"] = snapshot.SnapshotAtMs
	check["candidate_count"] = snapshot.CandidateCount
	check["latest_candidate_fetched_at_ms"] = snapshot.LatestCandidateFetchedAtMs
	check["latest_candidate_published_at_ms"] = snapshot.LatestCandidatePublishedAtMs
	check["fresh_items_since_snapshot"] = snapshot.FreshItemsSinceSnapshot
	if snapshot.SnapshotAtMs > 0 {
		check["snapshot_age_ms"] = now.UnixMilli() - snapshot.SnapshotAtMs
	}
	if lag := snapshot.LatestCandidateFetchedAtMs - snapshot.SnapshotAtMs; lag > 0 {
		check["snapshot_lag_ms"] = lag
	}

	if snapshot.CandidateCount == 0 {
		check["stale_users"] = 0
		check["reason"] = productHealthReasonNoData
		return productHealthStatusHealthy, check
	}

	latestAge := time.Duration(now.UnixMilli()-snapshot.LatestCandidateFetchedAtMs) * time.Millisecond
	if snapshot.SnapshotAtMs == 0 || (snapshot.FreshItemsSinceSnapshot > 0 && latestAge >= feedSnapshotHealthGrace) {
		check["stale_users"] = 1
		check["status"] = productHealthStatusUnhealthy
		check["reason"] = productHealthReasonStale
		check["stale_after_ms"] = feedSnapshotHealthGrace.Milliseconds()
		return productHealthStatusUnhealthy, check
	}

	check["stale_users"] = 0
	return productHealthStatusHealthy, check
}

func (s *Server) androidSyncProductHealth(now time.Time) (string, map[string]any) {
	check := map[string]any{
		"status": productHealthStatusHealthy,
		"reason": productHealthReasonCurrent,
	}
	if s.db == nil {
		check["status"] = productHealthStatusUnhealthy
		check["reason"] = productHealthReasonUnavailable
		return productHealthStatusUnhealthy, check
	}

	clock, err := s.db.GetAndroidSyncClock()
	if err != nil {
		check["status"] = productHealthStatusUnhealthy
		check["reason"] = err.Error()
		return productHealthStatusUnhealthy, check
	}
	health, err := s.db.GetLatestAndroidSyncHealthReport()
	if err != nil {
		check["status"] = productHealthStatusUnhealthy
		check["reason"] = err.Error()
		return productHealthStatusUnhealthy, check
	}

	check["server_revision"] = clock.Revision

	if health == nil {
		check["reason"] = productHealthReasonNoData
		return productHealthStatusHealthy, check
	}

	cursor, cursorErr := decodeAndroidSyncCursor(health.Cursor)
	if cursorErr != nil || cursor.Mode != "changes" || !androidSyncModelVersionSupported(cursor.Version) || cursor.Epoch != clock.Epoch {
		check["status"] = productHealthStatusUnhealthy
		check["reason"] = productHealthReasonStale
		return productHealthStatusUnhealthy, check
	}
	check["device_revision"] = cursor.Revision
	check["pending_revisions"] = max(int64(0), clock.Revision-cursor.Revision)
	check["latest_health_reported_at_ms"] = health.ReportedAtMs
	check["health_report_age_ms"] = now.UnixMilli() - health.ReportedAtMs
	check["total_assets"] = health.TotalAssets
	check["verified_assets"] = health.VerifiedAssets
	check["pending_assets"] = health.PendingAssets
	check["missing_assets"] = health.MissingAssets

	if time.Duration(now.UnixMilli()-health.ReportedAtMs)*time.Millisecond > androidSyncHealthReportMaxAge {
		check["status"] = productHealthStatusUnhealthy
		check["reason"] = productHealthReasonStale
		return productHealthStatusUnhealthy, check
	}
	if health.MissingAssets > 0 {
		check["status"] = productHealthStatusDegraded
		check["reason"] = productHealthReasonMissingAssets
		return productHealthStatusDegraded, check
	}

	return productHealthStatusHealthy, check
}
