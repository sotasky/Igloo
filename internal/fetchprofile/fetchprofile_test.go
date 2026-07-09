package fetchprofile

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFetchDispatchUnknownPlatform(t *testing.T) {
	_, err := Fetch(context.Background(), "unknown_x")
	if err == nil || !strings.Contains(err.Error(), "unknown platform") {
		t.Fatalf("expected unknown platform error, got: %v", err)
	}
}

func TestValidateChannelIdentityRequiresDisplayName(t *testing.T) {
	for _, profile := range []*Profile{nil, {
		ChannelID: "youtube_sample_channel",
		Platform:  "youtube",
		Handle:    "@sample",
	}} {
		err := ValidateChannelIdentity("youtube_sample_channel", profile)
		if !errors.Is(err, ErrIncompleteProfile) {
			t.Fatalf("ValidateChannelIdentity(%+v) error = %v, want ErrIncompleteProfile", profile, err)
		}
	}
}
