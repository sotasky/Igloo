package components

import (
	"reflect"
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestRepostEntries_MultiReposter(t *testing.T) {
	item := model.FeedItem{
		Retweeters: []model.RetweeterInfo{
			{Handle: "Alice", DisplayName: "Alice A.", ChannelID: "twitter_alice"},
			{Handle: "BoB", DisplayName: "", ChannelID: "twitter_bob"},
		},
	}
	got := repostEntries(item)
	want := []repostEntry{
		{Label: "Alice A.", Handle: "alice", ChannelID: "twitter_alice"},
		{Label: "BoB", Handle: "bob", ChannelID: "twitter_bob"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repostEntries mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestRepostEntries_SingleLegacyField(t *testing.T) {
	item := model.FeedItem{
		IsRetweet:              true,
		RetweetedByHandle:      "Carol",
		RetweetedByDisplayName: "Carol C.",
		ReposterChannelID:      "twitter_carol",
	}
	got := repostEntries(item)
	want := []repostEntry{
		{Label: "Carol C.", Handle: "carol", ChannelID: "twitter_carol"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repostEntries mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestRepostEntries_SingleLegacyHandleOnly(t *testing.T) {
	item := model.FeedItem{
		IsRetweet:         true,
		RetweetedByHandle: "dave",
		ReposterChannelID: "twitter_dave",
	}
	got := repostEntries(item)
	want := []repostEntry{
		{Label: "dave", Handle: "dave", ChannelID: "twitter_dave"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repostEntries mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestRepostEntries_SingleLegacySourceHandleFallback(t *testing.T) {
	item := model.FeedItem{
		IsRetweet:    true,
		SourceHandle: "eve",
	}
	got := repostEntries(item)
	want := []repostEntry{
		{Label: "eve", Handle: "eve", ChannelID: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repostEntries mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestRepostEntries_NotARetweet(t *testing.T) {
	item := model.FeedItem{IsRetweet: false}
	if got := repostEntries(item); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestRepostEntries_RetweetWithNoLabelFields(t *testing.T) {
	item := model.FeedItem{IsRetweet: true}
	if got := repostEntries(item); got != nil {
		t.Fatalf("expected nil (no label fields), got %+v", got)
	}
}

func TestSplitRepostCap_Under(t *testing.T) {
	entries := []repostEntry{
		{Label: "A"}, {Label: "B"}, {Label: "C"},
	}
	visible, hidden := splitRepostCap(entries, 6)
	if len(visible) != 3 || hidden != nil {
		t.Fatalf("under-cap: visible=%d hidden=%v", len(visible), hidden)
	}
}

func TestSplitRepostCap_Exact(t *testing.T) {
	entries := make([]repostEntry, 6)
	for i := range entries {
		entries[i] = repostEntry{Label: string(rune('A' + i))}
	}
	visible, hidden := splitRepostCap(entries, 6)
	if len(visible) != 6 || hidden != nil {
		t.Fatalf("exact-cap: visible=%d hidden=%v", len(visible), hidden)
	}
}

func TestSplitRepostCap_Over(t *testing.T) {
	entries := make([]repostEntry, 9)
	for i := range entries {
		entries[i] = repostEntry{Label: string(rune('A' + i))}
	}
	visible, hidden := splitRepostCap(entries, 6)
	if len(visible) != 6 || len(hidden) != 3 {
		t.Fatalf("over-cap: visible=%d hidden=%d", len(visible), len(hidden))
	}
	if visible[0].Label != "A" || visible[5].Label != "F" {
		t.Fatalf("visible order wrong: %+v", visible)
	}
	if hidden[0].Label != "G" || hidden[2].Label != "I" {
		t.Fatalf("hidden order wrong: %+v", hidden)
	}
}
