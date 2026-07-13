package storage

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestMediaExecutorKeepsStateLaneResponsiveDuringBulkMutation(t *testing.T) {
	executor := NewMediaExecutor()
	bulkEntered := make(chan struct{})
	releaseBulk := make(chan struct{})
	bulkDone := make(chan error, 1)
	go func() {
		bulkDone <- executor.Run(context.Background(), MediaLaneBulkBackground, func() error {
			close(bulkEntered)
			<-releaseBulk
			return nil
		})
	}()
	<-bulkEntered

	stateDone := make(chan error, 1)
	go func() {
		stateDone <- executor.Run(context.Background(), MediaLaneState, func() error { return nil })
	}()
	select {
	case err := <-stateDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("state asset waited behind bulk media mutation")
	}
	close(releaseBulk)
	if err := <-bulkDone; err != nil {
		t.Fatal(err)
	}
}

func TestMediaExecutorRunsForegroundAlongsideBackground(t *testing.T) {
	executor := NewMediaExecutor()
	backgroundEntered := make(chan struct{})
	releaseBackground := make(chan struct{})
	backgroundDone := runBulkTestWork(executor, MediaLaneBulkRegular, func() error {
		close(backgroundEntered)
		<-releaseBackground
		return nil
	})
	<-backgroundEntered

	foregroundEntered := make(chan struct{})
	foregroundDone := runBulkTestWork(executor, MediaLaneBulkForeground, func() error {
		close(foregroundEntered)
		return nil
	})
	select {
	case <-foregroundEntered:
	case <-time.After(time.Second):
		t.Fatal("foreground work waited behind background work")
	}
	if err := <-foregroundDone; err != nil {
		t.Fatal(err)
	}
	close(releaseBackground)
	if err := <-backgroundDone; err != nil {
		t.Fatal(err)
	}
}

func TestMediaExecutorSerializesBackgroundWork(t *testing.T) {
	executor := NewMediaExecutor()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := runBulkTestWork(executor, MediaLaneBulkRegular, func() error {
		close(firstEntered)
		<-releaseFirst
		return nil
	})
	<-firstEntered

	secondEntered := make(chan struct{})
	secondDone := runBulkTestWork(executor, MediaLaneBulkBackground, func() error {
		close(secondEntered)
		return nil
	})
	select {
	case <-secondEntered:
		t.Fatal("two background mutations ran together")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("queued background mutation did not run")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestMediaExecutorCancelsWorkWaitingForItsLane(t *testing.T) {
	executor := NewMediaExecutor()
	activeEntered := make(chan struct{})
	releaseActive := make(chan struct{})
	activeDone := runBulkTestWork(executor, MediaLaneBulkForeground, func() error {
		close(activeEntered)
		<-releaseActive
		return nil
	})
	<-activeEntered

	canceledCtx, cancel := context.WithCancel(context.Background())
	var canceledWorkRan atomic.Bool
	canceledDone := runBulkTestWorkWithContext(executor, canceledCtx, MediaLaneBulkForeground, func() error {
		canceledWorkRan.Store(true)
		return nil
	})
	cancel()

	select {
	case err := <-canceledDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued bulk mutation did not return")
	}
	close(releaseActive)
	if err := <-activeDone; err != nil {
		t.Fatal(err)
	}
	if canceledWorkRan.Load() {
		t.Fatal("canceled queued bulk callback ran")
	}
}

func runBulkTestWork(executor *MediaExecutor, lane MediaLane, work func() error) <-chan error {
	return runBulkTestWorkWithContext(executor, context.Background(), lane, work)
}

func runBulkTestWorkWithContext(executor *MediaExecutor, ctx context.Context, lane MediaLane, work func() error) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- executor.Run(ctx, lane, work)
	}()
	return done
}
