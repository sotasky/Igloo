package storage

import (
	"context"
	"fmt"
)

type MediaLane string

const (
	MediaLaneState MediaLane = "state_ssd"
	MediaLaneBulk  MediaLane = "bulk_hdd"
)

// MediaExecutor is the single admission owner for file-producing work. Bulk
// mutations are exclusive; small state assets may converge independently.
type MediaExecutor struct {
	bulk  chan struct{}
	state chan struct{}
}

func NewMediaExecutor() *MediaExecutor {
	return &MediaExecutor{bulk: make(chan struct{}, 1), state: make(chan struct{}, 2)}
}

func (e *MediaExecutor) Run(ctx context.Context, lane MediaLane, work func() error) error {
	if e == nil {
		return work()
	}
	var admission chan struct{}
	switch lane {
	case MediaLaneBulk:
		admission = e.bulk
	case MediaLaneState:
		admission = e.state
	default:
		return fmt.Errorf("unknown media lane %q", lane)
	}
	select {
	case admission <- struct{}{}:
		defer func() { <-admission }()
		return work()
	case <-ctx.Done():
		return ctx.Err()
	}
}
