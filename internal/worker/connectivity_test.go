package worker

import (
	"errors"
	"testing"
	"time"
)

func TestExternalNetworkStatePausesWithoutExtendingCorrelatedFailures(t *testing.T) {
	var state externalNetworkState
	now := time.Unix(1000, 0)
	transport, opened, recovered, retryAt := state.finish(now, errors.New("network is unreachable"))
	if !transport || !opened || recovered {
		t.Fatalf("first failure = transport %t opened %t recovered %t", transport, opened, recovered)
	}
	if want := now.Add(externalNetworkInitialBackoff); !retryAt.Equal(want) {
		t.Fatalf("retryAt = %s, want %s", retryAt, want)
	}
	if state.allowed(now.Add(time.Second)) {
		t.Fatal("work allowed during connectivity backoff")
	}

	_, opened, _, secondRetryAt := state.finish(now.Add(time.Second), errors.New("connection refused"))
	if opened || !secondRetryAt.Equal(retryAt) {
		t.Fatalf("correlated failure extended backoff: opened=%t retryAt=%s", opened, secondRetryAt)
	}
	if !state.allowed(retryAt) {
		t.Fatal("normal queued work was not allowed as the recovery probe")
	}
	if state.allowed(retryAt) {
		t.Fatal("more than one recovery probe was allowed")
	}
	if delay := state.retryDelay(retryAt); delay != externalNetworkProbePoll {
		t.Fatalf("probe poll delay = %s", delay)
	}
}

func TestExternalNetworkStateRecoversOnNonTransportResult(t *testing.T) {
	var state externalNetworkState
	now := time.Unix(1000, 0)
	state.finish(now, errors.New("temporary failure in name resolution"))
	transport, opened, recovered, _ := state.finish(now.Add(time.Second), errors.New("HTTP 429: Too Many Requests"))
	if transport || opened || !recovered {
		t.Fatalf("recovery result = transport %t opened %t recovered %t", transport, opened, recovered)
	}
	if !state.allowed(now.Add(time.Second)) {
		t.Fatal("work remained paused after a reachable source response")
	}
}

func TestExternalNetworkBackoffIsBounded(t *testing.T) {
	if got := externalNetworkBackoff(1); got != 30*time.Second {
		t.Fatalf("first backoff = %s", got)
	}
	if got := externalNetworkBackoff(20); got != externalNetworkMaxBackoff {
		t.Fatalf("bounded backoff = %s", got)
	}
}
