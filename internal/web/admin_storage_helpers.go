package web

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/subscribe"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// atomicWrite writes data to dest using a temp-file + rename pattern.
func atomicWrite(dest string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".upload_*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dest)
}

func (s *Server) removeCanonicalAssetFiles(keys []string) {
	if s == nil || s.db == nil {
		return
	}
	for _, key := range keys {
		if _, err := s.db.RemoveAssetFileIfUnreferenced(key); err != nil {
			slog.Warn("remove retired canonical asset", "key", key, "err", err)
		}
	}
}

// importSubscriptionList resolves and adds a list of URLs/handles.
// Returns (added, skipped) counts.
func (s *Server) importSubscriptionList(ctx context.Context, urls []string) (int, int) {
	added, skipped := 0, 0
	for _, rawURL := range urls {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			skipped++
			continue
		}
		platform := subscribe.DetectPlatform(rawURL, "")
		if !s.platformEnabled(platform) {
			slog.Warn("importSubscriptionList disabled platform", "url", rawURL, "platform", platform)
			skipped++
			continue
		}
		subCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ch, err := subscribe.ResolveChannel(subCtx, rawURL, platform, s.workers.Downloader())
		cancel()
		if err != nil {
			slog.Warn("importSubscriptionList resolve", "url", rawURL, "err", err)
			skipped++
			continue
		}
		if err := s.db.AddChannel(ch); err != nil {
			skipped++
			continue
		}
		added++
	}
	return added, skipped
}

// importChannelList adds pre-resolved channels.
// Returns (added, skipped) counts.
func (s *Server) importChannelList(ctx context.Context, channels []model.Channel) (int, int) {
	added, skipped := 0, 0
	for _, ch := range channels {
		if !s.platformEnabled(ch.Platform) {
			skipped++
			continue
		}
		if err := s.db.AddChannel(ch); err != nil {
			skipped++
			continue
		}
		added++
	}
	return added, skipped
}

// parseOPML extracts YouTube channels from an OPML byte slice.
// Handles both attribute orderings: xmlUrl before text, and text before xmlUrl.
func parseOPML(data []byte) []model.Channel {
	// Match xmlUrl="...youtube..." anywhere before or after text="..."
	reA := regexp.MustCompile(`xmlUrl="([^"]*youtube[^"]*)"[^>]*text="([^"]*)"`)
	reB := regexp.MustCompile(`text="([^"]*)"[^>]*xmlUrl="([^"]*youtube[^"]*)"`)
	reChannelID := regexp.MustCompile(`channel_id=([A-Za-z0-9_-]+)`)

	seen := make(map[string]bool)
	var channels []model.Channel

	addChannel := func(feedURL, name string) {
		m := reChannelID.FindStringSubmatch(feedURL)
		if len(m) < 2 {
			return
		}
		rawID := m[1]
		channelID := "youtube_" + strings.TrimPrefix(rawID, "youtube_")
		if seen[channelID] {
			return
		}
		seen[channelID] = true
		channels = append(channels, model.Channel{
			ChannelID:    channelID,
			SourceID:     strings.TrimPrefix(channelID, "youtube_"),
			Name:         name,
			URL:          "https://www.youtube.com/channel/" + strings.TrimPrefix(channelID, "youtube_"),
			Platform:     "youtube",
			IsSubscribed: true,
		})
	}

	for _, m := range reA.FindAllSubmatch(data, -1) {
		addChannel(string(m[1]), string(m[2]))
	}
	for _, m := range reB.FindAllSubmatch(data, -1) {
		addChannel(string(m[2]), string(m[1]))
	}

	return channels
}
