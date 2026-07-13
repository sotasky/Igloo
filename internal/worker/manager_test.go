package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/config"
)

// TestManagerStartAndShutdown verifies that a worker can be launched,
// reaches the Running state, and stops cleanly on Shutdown.
func TestManagerStartAndShutdown(t *testing.T) {
	cfg := &config.Config{}
	m := NewManager(nil, cfg)

	// Override downloader to nil — we won't use it.
	m.downloader = nil

	started := make(chan struct{})
	done := make(chan struct{})

	// Launch a synthetic worker that signals start, waits for ctx, then exits.
	m.launch("test_worker", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(done)
	})

	// Wait for the worker to start.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start within 2s")
	}

	// Verify Running=true in status.
	statuses := m.Status()
	var found bool
	for _, s := range statuses {
		if s.Name == "test_worker" {
			found = true
			if !s.Running {
				t.Errorf("expected Running=true, got false")
			}
		}
	}
	if !found {
		t.Error("test_worker not found in status list")
	}

	// Shutdown should cancel context and wait for all goroutines.
	shutdownDone := make(chan struct{})
	go func() {
		m.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop within 2s after Shutdown")
	}

	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return within 2s")
	}
}

func TestManagerShutdownTimeoutReturnsWhenWorkerIgnoresCancel(t *testing.T) {
	cfg := &config.Config{}
	m := NewManager(nil, cfg)
	m.downloader = nil

	blocked := make(chan struct{})
	release := make(chan struct{})
	m.launch("stubborn_worker", func(ctx context.Context) {
		close(blocked)
		<-release
	})

	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start within 2s")
	}

	if stopped := m.ShutdownTimeout(10 * time.Millisecond); stopped {
		t.Fatal("expected ShutdownTimeout to report an unclean shutdown")
	}

	close(release)
	m.Shutdown()
}

func TestManagerMediaKick(t *testing.T) {
	cfg := &config.Config{}
	m := NewManager(nil, cfg)
	m.downloader = nil

	// Channel is buffered(1); multiple kicks must not block.
	for i := 0; i < 10; i++ {
		m.KickMediaWork()
	}

	for name, kick := range map[string]chan struct{}{
		"current":  m.mediaCurrentKick,
		"backfill": m.mediaBackfillKick,
	} {
		select {
		case <-kick:
		default:
			t.Fatalf("expected a signal in %s media channel after kicks", name)
		}
		select {
		case <-kick:
			t.Fatalf("expected %s media channel to coalesce kicks", name)
		default:
		}
	}
}

// TestManagerPanicRecovery verifies that a panicking worker is recovered
// and does not crash the test process.
func TestManagerPanicRecovery(t *testing.T) {
	cfg := &config.Config{}
	m := NewManager(nil, cfg)
	m.downloader = nil

	var recovered atomic.Bool

	m.launch("panicking_worker", func(ctx context.Context) {
		recovered.Store(true)
		panic("intentional test panic")
	})

	// Give goroutine time to panic and recover.
	time.Sleep(200 * time.Millisecond)

	if !recovered.Load() {
		t.Fatal("panicking worker never ran")
	}

	// Shutdown should complete without hanging.
	done := make(chan struct{})
	go func() {
		m.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown hung after panic recovery")
	}

	// Status should reflect the panic.
	for _, s := range m.Status() {
		if s.Name == "panicking_worker" {
			if s.Error == "" {
				t.Errorf("expected non-empty Error for panicking worker")
			}
			if s.Running {
				t.Errorf("expected Running=false after panic")
			}
		}
	}
}

// TestClassifyMediaKind tests the media kind classifier.
func TestClassifyMediaKind(t *testing.T) {
	tests := []struct {
		mediaJSON string
		want      string
	}{
		{``, "unknown"},
		{`[{"type":"photo","url":"https://..."}]`, "image"},
		{`[{"type":"video","url":"https://..."}]`, "video"},
		{`[{"type":"gif","url":"https://..."}]`, "video"},
		{`[{"url":"https://..."}]`, "image"},
	}

	for _, tt := range tests {
		got := classifyMediaKind(tt.mediaJSON)
		if got != tt.want {
			t.Errorf("classifyMediaKind(%q) = %q, want %q", tt.mediaJSON, got, tt.want)
		}
	}
}
