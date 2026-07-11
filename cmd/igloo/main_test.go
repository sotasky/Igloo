package main

import (
	"errors"
	"testing"

	"github.com/screwys/igloo/internal/config"
)

func TestInitialConfigErrorAllowsPendingRestore(t *testing.T) {
	injected := errors.New("invalid current config")
	cfg := &config.Config{ConfigError: injected}
	if err := initialConfigError(cfg, false); !errors.Is(err, injected) {
		t.Fatalf("initialConfigError without restore = %v", err)
	}
	if err := initialConfigError(cfg, true); err != nil {
		t.Fatalf("pending restore was blocked by current config: %v", err)
	}
}
