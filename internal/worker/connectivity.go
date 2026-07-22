package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/screwys/igloo/internal/download"
)

const (
	externalNetworkInitialBackoff = 30 * time.Second
	externalNetworkMaxBackoff     = 5 * time.Minute
	externalNetworkProbePoll      = time.Second
)

// externalNetworkState is the shared circuit breaker for background internet
// work. It uses normal queued work as the recovery probe, so checking whether
// connectivity returned does not add a separate polling request.
type externalNetworkState struct {
	mu          sync.Mutex
	unavailable bool
	failures    int
	retryAt     time.Time
	probeActive bool
}

func (s *externalNetworkState) allowed(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unavailable {
		return true
	}
	if now.Before(s.retryAt) || s.probeActive {
		return false
	}
	s.probeActive = true
	return true
}

func (s *externalNetworkState) retryDelay(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unavailable || !now.Before(s.retryAt) {
		if !s.probeActive {
			return 0
		}
		return externalNetworkProbePoll
	}
	return s.retryAt.Sub(now)
}

func (s *externalNetworkState) finish(now time.Time, err error) (transport, opened, recovered bool, retryAt time.Time) {
	transport = download.IsTransportFailure(err, nil)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !transport {
		if s.unavailable {
			recovered = true
		}
		s.unavailable = false
		s.failures = 0
		s.retryAt = time.Time{}
		s.probeActive = false
		return transport, false, recovered, time.Time{}
	}

	opened = !s.unavailable
	if !s.unavailable || !now.Before(s.retryAt) {
		s.failures++
		delay := externalNetworkBackoff(s.failures)
		s.retryAt = now.Add(delay)
	}
	s.unavailable = true
	s.probeActive = false
	return transport, opened, false, s.retryAt
}

func (s *externalNetworkState) isUnavailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unavailable
}

func externalNetworkBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := externalNetworkInitialBackoff
	for i := 1; i < failures && delay < externalNetworkMaxBackoff; i++ {
		delay *= 2
	}
	if delay > externalNetworkMaxBackoff {
		return externalNetworkMaxBackoff
	}
	return delay
}

func (m *Manager) externalWorkAllowed(now time.Time) bool {
	if m == nil {
		return false
	}
	return m.externalNetwork.allowed(now)
}

func (m *Manager) externalRetryDelay(now time.Time) time.Duration {
	if m == nil {
		return 0
	}
	return m.externalNetwork.retryDelay(now)
}

// ReportExternalResult updates the shared connectivity circuit breaker. The
// return value tells the caller that this was a transport failure and should
// not be charged to the individual channel or job.
func (m *Manager) ReportExternalResult(err error) bool {
	if errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if m == nil {
		return download.IsTransportFailure(err, nil)
	}
	transport, opened, recovered, retryAt := m.externalNetwork.finish(time.Now(), err)
	if opened {
		message := fmt.Sprintf("Internet unavailable; external work paused until %s", retryAt.Format(time.RFC3339))
		log.Printf("[network] %s", message)
		if m.activity != nil {
			m.Emit("network", message, "warning")
		}
		if m.feedActivity != nil {
			m.EmitFeed(xIngestActivitySource, "Internet unavailable; feed ingest paused", "warning")
		}
	}
	if transport {
		m.scheduleExternalWake(retryAt)
	}
	if recovered {
		m.stopExternalWake()
		log.Printf("[network] internet access restored; external work resumed")
		if m.activity != nil {
			m.Emit("network", "Internet access restored; external work resumed", "done")
		}
		if m.feedActivity != nil {
			m.EmitFeed(xIngestActivitySource, "Internet access restored; feed ingest resumed", "done")
		}
		m.wakeExternalWorkers()
	}
	return transport
}

func (m *Manager) scheduleExternalWake(at time.Time) {
	if m == nil || at.IsZero() {
		return
	}
	delay := time.Until(at)
	if delay < 0 {
		delay = 0
	}
	m.externalWakeMu.Lock()
	defer m.externalWakeMu.Unlock()
	if m.externalWake != nil {
		m.externalWake.Stop()
	}
	m.externalWake = time.AfterFunc(delay, func() {
		if m.ctx != nil && m.ctx.Err() != nil {
			return
		}
		m.wakeExternalWorkers()
	})
}

func (m *Manager) stopExternalWake() {
	if m == nil {
		return
	}
	m.externalWakeMu.Lock()
	defer m.externalWakeMu.Unlock()
	if m.externalWake != nil {
		m.externalWake.Stop()
		m.externalWake = nil
	}
}

func (m *Manager) wakeExternalWorkers() {
	m.KickDiscovery()
	m.KickMediaWork()
	m.KickProfileJobs()
	m.KickIngest()
}
