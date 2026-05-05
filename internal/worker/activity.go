package worker

import (
	"sync"
	"time"
)

// ActivityEvent represents a recent server activity entry.
type ActivityEvent struct {
	Time      string `json:"time"`      // HH:MM:SS
	Timestamp int64  `json:"timestamp"` // unix seconds
	Source    string `json:"source"`    // e.g. "rsshub", "download", "feed_media", "scheduler"
	Message   string `json:"message"`
	Status    string `json:"status"` // "info", "done", "error", "warning", "start", "skipped"
	// Download-specific fields (for the downloads table)
	ChannelID string `json:"channel_id,omitempty"`
	Platform  string `json:"platform,omitempty"`
	Kind      string `json:"kind,omitempty"` // "video", "channel", etc.
}

// ActivityRing is a thread-safe ring buffer of recent events.
type ActivityRing struct {
	mu    sync.RWMutex
	items []ActivityEvent
	cap   int
}

// NewActivityRing creates a ring buffer with the given capacity.
func NewActivityRing(capacity int) *ActivityRing {
	return &ActivityRing{
		items: make([]ActivityEvent, 0, capacity),
		cap:   capacity,
	}
}

// Push adds an event to the ring, evicting the oldest if at capacity.
func (r *ActivityRing) Push(e ActivityEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) >= r.cap {
		r.items = r.items[1:]
	}
	r.items = append(r.items, e)
}

// Snapshot returns a copy of all events, newest last.
func (r *ActivityRing) Snapshot() []ActivityEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ActivityEvent, len(r.items))
	copy(out, r.items)
	return out
}

// Last returns the most recent n events, newest last.
func (r *ActivityRing) Last(n int) []ActivityEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n >= len(r.items) {
		out := make([]ActivityEvent, len(r.items))
		copy(out, r.items)
		return out
	}
	out := make([]ActivityEvent, n)
	copy(out, r.items[len(r.items)-n:])
	return out
}

// LastByKind returns the most recent n events matching the given kind, newest last.
func (r *ActivityRing) LastByKind(kind string, n int) []ActivityEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ActivityEvent
	for i := len(r.items) - 1; i >= 0 && len(out) < n; i-- {
		if r.items[i].Kind == kind {
			out = append(out, r.items[i])
		}
	}
	// Reverse to newest-last order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ByStatus filters events by status.
func (r *ActivityRing) ByStatus(status string) []ActivityEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ActivityEvent
	for _, e := range r.items {
		if e.Status == status {
			out = append(out, e)
		}
	}
	return out
}

// makeEvent is a helper to build an ActivityEvent with the current time.
func makeEvent(source, message, status string) ActivityEvent {
	now := time.Now()
	return ActivityEvent{
		Time:      now.Format("15:04:05"),
		Timestamp: now.Unix(),
		Source:    source,
		Message:   message,
		Status:    status,
	}
}
