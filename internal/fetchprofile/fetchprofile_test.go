package fetchprofile

import (
	"context"
	"strings"
	"testing"
)

func TestFetchDispatchUnknownPlatform(t *testing.T) {
	_, err := Fetch(context.Background(), "unknown_x")
	if err == nil || !strings.Contains(err.Error(), "unknown platform") {
		t.Fatalf("expected unknown platform error, got: %v", err)
	}
}
