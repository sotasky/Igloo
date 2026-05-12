package web

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestTwitterSourceHandleUsesSourceID(t *testing.T) {
	got := twitterSourceHandle(model.Channel{
		ChannelID: "twitter_user_a",
		SourceID:  "@User_A",
		Platform:  "twitter",
	})
	if got != "user_a" {
		t.Fatalf("twitterSourceHandle = %q; want user_a", got)
	}
}

func TestTwitterSourceHandleFallsBackToChannelID(t *testing.T) {
	got := twitterSourceHandle(model.Channel{
		ChannelID: "twitter_user_b",
		Platform:  "twitter",
	})
	if got != "user_b" {
		t.Fatalf("twitterSourceHandle = %q; want user_b", got)
	}
}

func TestBuildFeedSourcesShowsNeverIngestedChannelAsPending(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.AddChannel(model.Channel{
		ChannelID:    "twitter__me_moe",
		SourceID:     "_me_moe",
		Name:         "_me_moe",
		URL:          "https://x.com/_me_moe",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	sources, _ := srv.buildFeedSources()
	for _, source := range sources {
		if source.Handle != "_me_moe" {
			continue
		}
		if source.Status != "unknown" || source.DisplayStatus != "pending" {
			t.Fatalf("source status = %q display = %q; want unknown/pending", source.Status, source.DisplayStatus)
		}
		return
	}
	t.Fatalf("_me_moe source missing from diagnostics: %#v", sources)
}
