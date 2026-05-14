package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func TestDownloaderOperationInsertListAndPrune(t *testing.T) {
	tmp, err := os.CreateTemp("", "igloo-downloader-ops-*.db")
	if err != nil {
		t.Fatalf("temp db: %v", err)
	}
	path := tmp.Name()
	_ = tmp.Close()
	t.Cleanup(func() { _ = os.Remove(path) })

	d, err := Open(path, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		_ = d.Close()
	}()

	now := time.Now().UnixMilli()
	if err := d.RecordDownloaderOperation(context.Background(), model.DownloaderOperation{
		Operation:   "x.gallerydl.dump",
		Platform:    "twitter",
		Subject:     "https://x.com/example",
		Tool:        "gallery-dl",
		StartedAtMs: now - 100,
		EndedAtMs:   now,
		Status:      "success",
		ItemCount:   3,
		SummaryJSON: `{"args":["--cookies","***"]}`,
	}); err != nil {
		t.Fatalf("RecordDownloaderOperation: %v", err)
	}
	if err := d.RecordDownloaderOperation(context.Background(), model.DownloaderOperation{
		Operation:   "media.download",
		Platform:    "youtube",
		Tool:        "yt-dlp",
		StartedAtMs: now + 1,
		EndedAtMs:   now + 2,
		Status:      "failure",
		ErrorKind:   "rate_limit",
		Error:       "HTTP 429",
	}); err != nil {
		t.Fatalf("RecordDownloaderOperation 2: %v", err)
	}

	ops, err := d.ListDownloaderOperations(10)
	if err != nil {
		t.Fatalf("ListDownloaderOperations: %v", err)
	}
	if len(ops) != 2 || ops[0].Platform != "youtube" || ops[1].Platform != "twitter" {
		t.Fatalf("operations ordering = %#v", ops)
	}
	counts, err := d.DownloaderOperationCountsSince(now - 1000)
	if err != nil {
		t.Fatalf("DownloaderOperationCountsSince: %v", err)
	}
	if len(counts) != 2 {
		t.Fatalf("counts = %#v", counts)
	}
	if err := d.PruneDownloaderOperations(1, 24*time.Hour); err != nil {
		t.Fatalf("PruneDownloaderOperations: %v", err)
	}
	ops, err = d.ListDownloaderOperations(10)
	if err != nil {
		t.Fatalf("List after prune: %v", err)
	}
	if len(ops) != 1 || ops[0].Platform != "youtube" {
		t.Fatalf("after prune = %#v", ops)
	}
}
