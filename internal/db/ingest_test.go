package db

import (
	"math"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestGetIngestStateNew(t *testing.T) {
	d := openWritableTestDB(t)

	s, err := d.GetIngestState("handle_never_seen")
	if err != nil {
		t.Fatalf("GetIngestState: %v", err)
	}
	if s.Handle != "handle_never_seen" {
		t.Errorf("expected handle %q, got %q", "handle_never_seen", s.Handle)
	}
	if s.FailCount != 0 {
		t.Errorf("expected zero fail_count, got %d", s.FailCount)
	}
	if s.NextRetryAt != 0 {
		t.Errorf("expected zero next_retry_at, got %f", s.NextRetryAt)
	}
}

func TestCountFeedItemsBySourceChannelUsesCanonicalRows(t *testing.T) {
	d := openWritableTestDB(t)
	now := time.Now().UTC()
	items := []model.FeedItem{
		{TweetID: "sample_source_count_one", SourceHandle: "sample_one", AuthorHandle: "sample_one", BodyText: "one", PublishedAt: &now, FetchedAt: now},
		{TweetID: "sample_source_count_two", SourceHandle: "sample_one", AuthorHandle: "sample_one", BodyText: "two", PublishedAt: &now, FetchedAt: now},
		{TweetID: "sample_source_count_three", SourceHandle: "sample_two", AuthorHandle: "sample_two", BodyText: "three", PublishedAt: &now, FetchedAt: now},
	}
	if _, err := d.UpsertFeedItems(items); err != nil {
		t.Fatalf("seed feed items: %v", err)
	}

	counts, err := d.CountFeedItemsBySourceChannel()
	if err != nil {
		t.Fatalf("CountFeedItemsBySourceChannel: %v", err)
	}
	if counts["twitter_sample_one"] != 2 || counts["twitter_sample_two"] != 1 {
		t.Fatalf("counts = %#v", counts)
	}
}

func TestUpdateIngestStateSuccess(t *testing.T) {
	d := openWritableTestDB(t)

	handle := "handle_success_test"

	// Simulate a prior failure to set fail_count > 0
	if err := d.RecordIngestFailure(handle, "timeout", 0); err != nil {
		t.Fatalf("RecordIngestFailure: %v", err)
	}
	s, _ := d.GetIngestState(handle)
	if s.FailCount != 1 {
		t.Fatalf("expected fail_count=1 after failure, got %d", s.FailCount)
	}

	// Record success — should reset fail_count
	now := float64(time.Now().Unix())
	if err := d.RecordIngestSuccess(handle, now, 250.0); err != nil {
		t.Fatalf("RecordIngestSuccess: %v", err)
	}

	s, err := d.GetIngestState(handle)
	if err != nil {
		t.Fatalf("GetIngestState after success: %v", err)
	}
	if s.FailCount != 0 {
		t.Errorf("expected fail_count=0 after success, got %d", s.FailCount)
	}
	if s.NextRetryAt != 0 {
		t.Errorf("expected next_retry_at=0 after success, got %f", s.NextRetryAt)
	}
	if s.LastSuccessAt == 0 {
		t.Error("expected last_success_at to be set")
	}
	if s.LastError != "" {
		t.Errorf("expected last_error to be cleared after success, got %q", s.LastError)
	}
	if s.LastHTTPStatus != 0 {
		t.Errorf("expected last_http_status to be cleared after success, got %d", s.LastHTTPStatus)
	}
}

func TestUpdateIngestStateFailure(t *testing.T) {
	d := openWritableTestDB(t)

	handle := "handle_failure_test"

	// First failure (non-transient 429): fail_count=1, next_retry_at in the future
	if err := d.RecordIngestFailure(handle, "rate limited", 429); err != nil {
		t.Fatalf("RecordIngestFailure: %v", err)
	}

	s, err := d.GetIngestState(handle)
	if err != nil {
		t.Fatalf("GetIngestState: %v", err)
	}
	if s.FailCount != 1 {
		t.Errorf("expected fail_count=1, got %d", s.FailCount)
	}
	if s.NextRetryAt <= float64(time.Now().Unix()) {
		t.Errorf("expected next_retry_at in the future, got %f (now=%d)", s.NextRetryAt, time.Now().Unix())
	}
	if s.LastError != "rate limited" {
		t.Errorf("expected last_error=%q, got %q", "rate limited", s.LastError)
	}
	if s.LastHTTPStatus != 429 {
		t.Errorf("expected last_http_status=429, got %d", s.LastHTTPStatus)
	}

	// Second failure (non-transient 400): fail_count increments to 2
	if err := d.RecordIngestFailure(handle, "bad request", 400); err != nil {
		t.Fatalf("second RecordIngestFailure: %v", err)
	}
	s, _ = d.GetIngestState(handle)
	if s.FailCount != 2 {
		t.Errorf("expected fail_count=2 after second failure, got %d", s.FailCount)
	}
}

func TestGetReadyHandles(t *testing.T) {
	d := openWritableTestDB(t)

	handles := []string{"ready_h1", "ready_h2", "backoff_h3"}

	// h1 and h2: no state yet → always ready
	// h3: record a failure so it has a future next_retry_at
	if err := d.RecordIngestFailure("backoff_h3", "err", 500); err != nil {
		t.Fatalf("RecordIngestFailure: %v", err)
	}

	ready, notDue, cooling := d.FilterReadyHandles(handles, 0)

	if cooling != 1 {
		t.Errorf("expected cooling=1, got %d", cooling)
	}
	if notDue != 0 {
		t.Errorf("expected notDue=0, got %d", notDue)
	}

	// backoff_h3 should be filtered out
	for _, h := range ready {
		if h == "backoff_h3" {
			t.Error("backoff_h3 should not be in ready handles")
		}
	}

	// h1 and h2 should be present
	readySet := make(map[string]bool)
	for _, h := range ready {
		readySet[h] = true
	}
	for _, want := range []string{"ready_h1", "ready_h2"} {
		if !readySet[want] {
			t.Errorf("expected %q in ready handles", want)
		}
	}
}

func TestFilterReadyHandlesNotDue(t *testing.T) {
	d := openWritableTestDB(t)

	handles := []string{"fresh_h1", "stale_h2", "new_h3"}

	now := float64(time.Now().Unix())
	interval := 600.0 // 10 minutes

	// fresh_h1: fetched 60s ago — not due
	if err := d.RecordIngestSuccess("fresh_h1", now-60, 100); err != nil {
		t.Fatalf("RecordIngestSuccess fresh_h1: %v", err)
	}
	// stale_h2: fetched 700s ago — due
	if err := d.RecordIngestSuccess("stale_h2", now-700, 100); err != nil {
		t.Fatalf("RecordIngestSuccess stale_h2: %v", err)
	}
	// new_h3: never fetched — due

	ready, notDue, cooling := d.FilterReadyHandles(handles, interval)

	if notDue != 1 {
		t.Errorf("expected notDue=1, got %d", notDue)
	}
	if cooling != 0 {
		t.Errorf("expected cooling=0, got %d", cooling)
	}

	readySet := make(map[string]bool)
	for _, h := range ready {
		readySet[h] = true
	}
	if readySet["fresh_h1"] {
		t.Error("fresh_h1 should not be ready (fetched 60s ago, interval=600s)")
	}
	for _, want := range []string{"stale_h2", "new_h3"} {
		if !readySet[want] {
			t.Errorf("expected %q in ready handles", want)
		}
	}
}

func TestRecordIngestFailureTransient(t *testing.T) {
	d := openWritableTestDB(t)

	handle := "handle_transient_test"

	// 503 is transient: fail_count should stay 0, next_retry ~60s from now
	if err := d.RecordIngestFailure(handle, "HTTP 503: Service Unavailable", 503); err != nil {
		t.Fatalf("RecordIngestFailure: %v", err)
	}

	s, err := d.GetIngestState(handle)
	if err != nil {
		t.Fatalf("GetIngestState: %v", err)
	}
	if s.FailCount != 1 {
		t.Errorf("transient 503: expected fail_count=1, got %d", s.FailCount)
	}

	now := float64(time.Now().Unix())
	expectedRetry := now + 60
	tolerance := 5.0 // seconds
	if math.Abs(s.NextRetryAt-expectedRetry) > tolerance {
		t.Errorf("transient 503: expected next_retry_at ~%.0f (now+60), got %.0f (delta=%.1f)",
			expectedRetry, s.NextRetryAt, s.NextRetryAt-now)
	}

	// Second 503: fail_count increments but retry stays flat 60s
	if err := d.RecordIngestFailure(handle, "HTTP 503: Service Unavailable", 503); err != nil {
		t.Fatalf("second RecordIngestFailure: %v", err)
	}
	s, _ = d.GetIngestState(handle)
	if s.FailCount != 2 {
		t.Errorf("second transient 503: expected fail_count=2, got %d", s.FailCount)
	}
}

func TestRecordIngestFailureRateLimit(t *testing.T) {
	d := openWritableTestDB(t)

	handle := "handle_ratelimit_test"

	// 429 is non-transient: should increment fail_count with exponential backoff
	// fail_count=1: backoff = min(120 * 2^max(0, 1-1), 1800) = min(120*1, 1800) = 120
	if err := d.RecordIngestFailure(handle, "HTTP 429: Too Many Requests", 429); err != nil {
		t.Fatalf("RecordIngestFailure: %v", err)
	}

	s, err := d.GetIngestState(handle)
	if err != nil {
		t.Fatalf("GetIngestState: %v", err)
	}
	if s.FailCount != 1 {
		t.Errorf("429 failure 1: expected fail_count=1, got %d", s.FailCount)
	}

	now := float64(time.Now().Unix())
	expectedBackoff := 120.0 // 120 * 2^0
	expectedRetry := now + expectedBackoff
	tolerance := 5.0
	if math.Abs(s.NextRetryAt-expectedRetry) > tolerance {
		t.Errorf("429 failure 1: expected next_retry_at ~%.0f (now+%.0f), got %.0f",
			expectedRetry, expectedBackoff, s.NextRetryAt)
	}

	// Second 429: fail_count=2, backoff = min(120 * 2^1, 1800) = 240
	if err := d.RecordIngestFailure(handle, "HTTP 429: Too Many Requests", 429); err != nil {
		t.Fatalf("second RecordIngestFailure: %v", err)
	}
	s, _ = d.GetIngestState(handle)
	if s.FailCount != 2 {
		t.Errorf("429 failure 2: expected fail_count=2, got %d", s.FailCount)
	}

	now = float64(time.Now().Unix())
	expectedBackoff = 240.0 // 120 * 2^1
	expectedRetry = now + expectedBackoff
	if math.Abs(s.NextRetryAt-expectedRetry) > tolerance {
		t.Errorf("429 failure 2: expected next_retry_at ~%.0f (now+%.0f), got %.0f",
			expectedRetry, expectedBackoff, s.NextRetryAt)
	}
}

func TestRecordIngestFailureMaxBackoff(t *testing.T) {
	d := openWritableTestDB(t)

	handle := "handle_maxbackoff_test"

	// Record 20 rate-limit failures; exponential backoff should cap at 1800
	for i := 0; i < 20; i++ {
		if err := d.RecordIngestFailure(handle, "HTTP 429: Too Many Requests", 429); err != nil {
			t.Fatalf("RecordIngestFailure iteration %d: %v", i, err)
		}
	}

	s, err := d.GetIngestState(handle)
	if err != nil {
		t.Fatalf("GetIngestState: %v", err)
	}
	if s.FailCount != 20 {
		t.Errorf("expected fail_count=20, got %d", s.FailCount)
	}

	now := float64(time.Now().Unix())
	maxBackoff := 1800.0
	expectedRetry := now + maxBackoff
	tolerance := 5.0
	if math.Abs(s.NextRetryAt-expectedRetry) > tolerance {
		t.Errorf("expected next_retry_at ~%.0f (now+%.0f cap), got %.0f (delta=%.1f)",
			expectedRetry, maxBackoff, s.NextRetryAt, s.NextRetryAt-now)
	}
}

func TestResetIngestBackoff(t *testing.T) {
	d := openWritableTestDB(t)

	handle := "handle_reset_test"

	// Record some failures to set fail_count > 0 and next_retry_at > 0
	for i := 0; i < 3; i++ {
		if err := d.RecordIngestFailure(handle, "HTTP 429: Too Many Requests", 429); err != nil {
			t.Fatalf("RecordIngestFailure: %v", err)
		}
	}

	s, _ := d.GetIngestState(handle)
	if s.FailCount == 0 {
		t.Fatal("expected fail_count > 0 before reset")
	}
	if s.NextRetryAt == 0 {
		t.Fatal("expected next_retry_at > 0 before reset")
	}

	// Reset all backoff state
	if err := d.ResetIngestBackoff(); err != nil {
		t.Fatalf("ResetIngestBackoff: %v", err)
	}

	s, err := d.GetIngestState(handle)
	if err != nil {
		t.Fatalf("GetIngestState after reset: %v", err)
	}
	if s.FailCount != 0 {
		t.Errorf("expected fail_count=0 after reset, got %d", s.FailCount)
	}
	if s.NextRetryAt != 0 {
		t.Errorf("expected next_retry_at=0 after reset, got %f", s.NextRetryAt)
	}
}

func TestResetExpiredIngestBackoffKeepsActiveBackoff(t *testing.T) {
	d := openWritableTestDB(t)

	active := "handle_active_backoff"
	expired := "handle_expired_backoff"
	if err := d.RecordIngestFailure(active, "HTTP 503: Service Unavailable", 503); err != nil {
		t.Fatalf("RecordIngestFailure active: %v", err)
	}
	if err := d.RecordIngestFailure(expired, "HTTP 503: Service Unavailable", 503); err != nil {
		t.Fatalf("RecordIngestFailure expired: %v", err)
	}
	_, err := d.conn.Exec(
		"UPDATE ingest_state SET next_retry_at = ?, fail_count = 3 WHERE handle = ?",
		float64(time.Now().Add(-time.Minute).Unix()),
		expired,
	)
	if err != nil {
		t.Fatalf("age expired backoff: %v", err)
	}

	if err := d.ResetExpiredIngestBackoff(); err != nil {
		t.Fatalf("ResetExpiredIngestBackoff: %v", err)
	}

	activeState, err := d.GetIngestState(active)
	if err != nil {
		t.Fatalf("GetIngestState active: %v", err)
	}
	if activeState.FailCount == 0 {
		t.Fatal("active backoff fail_count should be preserved")
	}
	if activeState.NextRetryAt <= float64(time.Now().Unix()) {
		t.Fatalf("active next_retry_at should remain in the future, got %f", activeState.NextRetryAt)
	}

	expiredState, err := d.GetIngestState(expired)
	if err != nil {
		t.Fatalf("GetIngestState expired: %v", err)
	}
	if expiredState.FailCount != 0 {
		t.Errorf("expired backoff fail_count = %d, want 0", expiredState.FailCount)
	}
	if expiredState.NextRetryAt != 0 {
		t.Errorf("expired next_retry_at = %f, want 0", expiredState.NextRetryAt)
	}
}
