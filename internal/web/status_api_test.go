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
		ChannelID:    "twitter__sample_handle",
		SourceID:     "_sample_handle",
		Name:         "_sample_handle",
		URL:          "https://x.com/_sample_handle",
		Platform:     "twitter",
		IsSubscribed: true,
	}); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	sources, _ := srv.buildFeedSources()
	for _, source := range sources {
		if source.Handle != "_sample_handle" {
			continue
		}
		if source.Status != "unknown" || source.DisplayStatus != "pending" {
			t.Fatalf("source status = %q display = %q; want unknown/pending", source.Status, source.DisplayStatus)
		}
		return
	}
	t.Fatalf("_sample_handle source missing from diagnostics: %#v", sources)
}

func TestCountReadyAvatarsCountsCanonicalChannelIdentityOnly(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.db.ExecRaw(`
		INSERT INTO assets (
			asset_id, asset_kind, owner_kind, owner_id, media_index,
			state, created_at_ms, updated_at_ms
		) VALUES
			('channel_avatar', 'avatar', 'channel', 'twitter_sample', 0, 'ready', 1, 1),
			('comment_avatar', 'avatar', 'comment_author', 'comment_sample', 0, 'ready', 1, 1),
			('retired_tweet_avatar', 'avatar', 'tweet', 'tweet_sample', 0, 'ready', 1, 1),
			('queued_channel_avatar', 'avatar', 'channel', 'twitter_pending', 0, 'queued', 1, 1)
	`); err != nil {
		t.Fatalf("seed avatar inventory: %v", err)
	}

	if got := srv.countReadyAvatars(); got != 1 {
		t.Fatalf("countReadyAvatars = %d, want one ready channel avatar", got)
	}
}
