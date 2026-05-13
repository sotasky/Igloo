package main

import (
	"strings"
	"testing"

	"github.com/screwys/igloo/internal/db"
)

func TestParseOptionsRequiresPositiveLimit(t *testing.T) {
	if _, err := parseOptions([]string{"-limit=0"}); err == nil {
		t.Fatal("parseOptions accepted zero limit")
	}
}

func TestFormatTextSortsKinds(t *testing.T) {
	text := formatText(report{
		Mode:     "dry_run",
		Database: "/tmp/igloo.db",
		DataDir:  "/tmp/igloo",
		Result: db.AssetInventoryReconcileResult{
			DryRun:          true,
			Limit:           10,
			LimitReached:    true,
			Candidates:      2,
			SkippedExisting: 3,
			ByKind: map[string]db.AssetInventoryReconcileKindResult{
				"video_stream": {Candidates: 1, Ready: 1},
				"avatar":       {Candidates: 1, ServerMissing: 1},
			},
		},
	})
	avatarIndex := strings.Index(text, "  avatar:")
	videoIndex := strings.Index(text, "  video_stream:")
	if avatarIndex < 0 || videoIndex < 0 || avatarIndex > videoIndex {
		t.Fatalf("kind output not sorted:\n%s", text)
	}
	if !strings.Contains(text, "limit=10 limit_reached=true") {
		t.Fatalf("missing limit line:\n%s", text)
	}
}
