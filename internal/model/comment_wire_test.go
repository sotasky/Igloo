package model

import (
	"encoding/json"
	"testing"
	"time"
)

// #14 — every wire timestamp must be INTEGER unix-millis, not an ISO string
// or seconds-encoded number. Comment was the most-recently-leaked offender
// (json:"published_at" backed by an `any` that emitted .Unix() seconds or
// the empty string); locking that down here.
func TestCommentPublishedAtMsIsJSONNumber(t *testing.T) {
	pub := time.Date(2026, 4, 20, 12, 34, 56, 789_000_000, time.UTC)
	c := Comment{
		VideoID:     "v123",
		CommentID:   "c456",
		PublishedAt: &pub,
	}
	c.SetPublishedAtMs()

	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got, ok := decoded["published_at"].(float64)
	if !ok {
		t.Fatalf("expected published_at to be JSON Number, got %T (%v)", decoded["published_at"], decoded["published_at"])
	}
	want := float64(pub.UnixMilli())
	if got != want {
		t.Errorf("published_at = %v, want %v (unix millis of %v)", got, want, pub)
	}
}

func TestCommentNilPublishedEmitsZero(t *testing.T) {
	c := Comment{VideoID: "v123", CommentID: "c456"}
	c.SetPublishedAtMs()

	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got, ok := decoded["published_at"].(float64)
	if !ok {
		t.Fatalf("expected published_at numeric (was string before #14), got %T", decoded["published_at"])
	}
	if got != 0 {
		t.Errorf("nil PublishedAt should emit 0, got %v", got)
	}
}
