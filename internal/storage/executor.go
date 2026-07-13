package storage

import (
	"context"
	"fmt"
)

type MediaLane string

const (
	MediaLaneState          MediaLane = "state_ssd"
	MediaLaneBulkForeground MediaLane = "bulk_foreground"
	MediaLaneBulkRegular    MediaLane = "bulk_regular"
	MediaLaneBulkBackground MediaLane = "bulk_background"
)

const mediaStateConcurrency = 2

// MediaExecutor is the single admission owner for file-producing work. One
// foreground and one background bulk producer may run; work within either lane
// remains serial. Small state assets converge independently.
type MediaExecutor struct {
	state      chan struct{}
	foreground chan struct{}
	background chan struct{}
}

func NewMediaExecutor() *MediaExecutor {
	return &MediaExecutor{
		state:      make(chan struct{}, mediaStateConcurrency),
		foreground: make(chan struct{}, 1),
		background: make(chan struct{}, 1),
	}
}

func (e *MediaExecutor) Run(ctx context.Context, lane MediaLane, work func() error) error {
	if e == nil {
		return work()
	}
	switch lane {
	case MediaLaneState:
		return e.run(ctx, e.state, work)
	case MediaLaneBulkForeground:
		return e.run(ctx, e.foreground, work)
	case MediaLaneBulkRegular, MediaLaneBulkBackground:
		return e.run(ctx, e.background, work)
	default:
		return fmt.Errorf("unknown media lane %q", lane)
	}
}

func (e *MediaExecutor) run(ctx context.Context, slot chan struct{}, work func() error) error {
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
		if err := ctx.Err(); err != nil {
			return err
		}
		return work()
	case <-ctx.Done():
		return ctx.Err()
	}
}
