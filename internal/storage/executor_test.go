package storage

import (
	"context"
	"testing"
	"time"
)

func TestMediaExecutorKeepsStateLaneResponsiveDuringBulkMutation(t *testing.T) {
	executor := NewMediaExecutor()
	bulkEntered := make(chan struct{})
	releaseBulk := make(chan struct{})
	bulkDone := make(chan error, 1)
	go func() {
		bulkDone <- executor.Run(context.Background(), MediaLaneBulk, func() error {
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
