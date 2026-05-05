package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/screwys/igloo/internal/components"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
	"github.com/screwys/igloo/internal/model"
)

const webPageSize = 200

func main() {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".local/share/igloo/igloo.db")
	dataDir := filepath.Join(home, ".local/share/igloo")

	d, err := db.OpenReadOnly(dbPath, dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	fmt.Println("=== Page Load Benchmark ===")
	fmt.Println()

	// --- Shorts page ---
	fmt.Println("-- Shorts Page --")

	opts := db.GetVideosOpts{Platform: "shorts", Limit: 10000, OrderAsc: true, MomentsMode: "all"}

	t := time.Now()
	count, _ := d.GetVideoCount(opts)
	fmt.Printf("  GetVideoCount(shorts):       %6dms  (%d)\n", time.Since(t).Milliseconds(), count)

	t = time.Now()
	shorts, _ := d.GetVideos(opts)
	fmt.Printf("  GetVideos(shorts):           %6dms  (%d rows)\n", time.Since(t).Milliseconds(), len(shorts))

	pageOpts := db.GetVideosOpts{Platform: "shorts", Limit: 10000, OrderAsc: true, MomentsMode: "all", ExcludeMetadata: true}
	t = time.Now()
	shortsPage, _ := d.GetVideos(pageOpts)
	fmt.Printf("  GetVideos(shorts page):      %6dms  (%d rows)\n", time.Since(t).Milliseconds(), len(shortsPage))

	t = time.Now()
	var shortsHTML bytes.Buffer
	components.ShortsPage(benchPageProps("Moments", "shorts"), shortsPage, nil, false, model.Pager{Page: 1, PerPage: 10000, Total: count}, "", "all", 96, 96).Render(context.Background(), &shortsHTML)
	fmt.Printf("  Render ShortsPage:           %6dms  (%d KB)\n", time.Since(t).Milliseconds(), shortsHTML.Len()/1024)

	fmt.Println()

	// --- Feed page ---
	fmt.Println("-- Feed Page --")
	username := "admin"

	t = time.Now()
	items, _ := d.ListFeedItemsPage(10000, nil, username)
	dur := time.Since(t)
	fmt.Printf("  ListFeedItemsPage(10000):    %6dms  (%d items)\n", dur.Milliseconds(), len(items))

	t = time.Now()
	items = feed.EnrichFeedItems(d, items, username)
	dur = time.Since(t)
	fmt.Printf("  EnrichFeedItems:             %6dms  (%d items)\n", dur.Milliseconds(), len(items))

	t = time.Now()
	items = feed.RankFeedItems(items)
	dur = time.Since(t)
	fmt.Printf("  RankFeedItems:               %6dms  (%d items)\n", dur.Milliseconds(), len(items))

	// Fast ranked query (new path)
	t = time.Now()
	ranked, _ := d.ListRankedFeedItems("admin", 41, 0)
	dur = time.Since(t)
	fmt.Printf("  ListRankedFeedItems(41):     %6dms  (%d items)  << NEW\n", dur.Milliseconds(), len(ranked))

	fmt.Println()

	// --- Sidebar ---
	fmt.Println("-- Sidebar --")
	t = time.Now()
	d.GetSubscribedChannels()
	d.GetAllVideoCountsByChannel()
	fmt.Printf("  Combined sidebar:            %6dms\n", time.Since(t).Milliseconds())

	fmt.Println()

	// --- Channels page ---
	fmt.Println("-- Channels Page --")

	t = time.Now()
	channels, _ := d.GetSubscribedChannels()
	fmt.Printf("  GetSubscribedChannels:       %6dms  (%d rows)\n", time.Since(t).Milliseconds(), len(channels))

	t = time.Now()
	counts, _ := d.GetAllVideoCountsByChannel()
	fmt.Printf("  GetAllVideoCountsByChannel:  %6dms  (%d rows)\n", time.Since(t).Milliseconds(), len(counts))

	t = time.Now()
	vids, _ := d.GetLatestVideosPerChannel(8)
	total := 0
	for _, v := range vids {
		total += len(v)
	}
	fmt.Printf("  GetLatestVideosPerChannel:   %6dms  (%d channels, %d videos)\n", time.Since(t).Milliseconds(), len(vids), total)

	// Simulate filtered batch (20 channels)
	var sampleVideoIDs, sampleHandles []string
	for i, ch := range channels {
		if i >= 20 {
			break
		}
		if ch.Platform == "twitter" {
			h := ch.ChannelID
			if idx := len("twitter_"); len(h) > idx {
				h = h[idx:]
			}
			sampleHandles = append(sampleHandles, h)
		} else {
			sampleVideoIDs = append(sampleVideoIDs, ch.ChannelID)
		}
	}
	t = time.Now()
	d.GetLatestVideosPerChannel(8, sampleVideoIDs...)
	d.GetLatestFeedMediaPerAuthor(8, sampleHandles...)
	fmt.Printf("  Filtered batch (20 ch):     %6dms\n", time.Since(t).Milliseconds())

	t = time.Now()
	feedMap, _ := d.GetLatestFeedMediaPerAuthor(6)
	totalFeed := 0
	for _, f := range feedMap {
		totalFeed += len(f)
	}
	fmt.Printf("  GetLatestFeedMediaPerAuthor: %6dms  (%d authors, %d items)\n", time.Since(t).Milliseconds(), len(feedMap), totalFeed)

	fmt.Println()

	// --- Bookmarks page ---
	fmt.Println("-- Bookmarks Page --")

	bOpts := db.GetBookmarksOpts{UserID: "admin", Limit: 10000}
	t = time.Now()
	bCount, _ := d.GetBookmarkCount(bOpts)
	fmt.Printf("  GetBookmarkCount:            %6dms  (%d bookmarks)\n", time.Since(t).Milliseconds(), bCount)

	t = time.Now()
	bmarks, _ := d.GetBookmarks(bOpts)
	fmt.Printf("  GetBookmarks:                %6dms  (%d rows)\n", time.Since(t).Milliseconds(), len(bmarks))

	bPageOpts := db.GetBookmarksOpts{UserID: username, Limit: webPageSize}
	t = time.Now()
	bmarksPage, _ := d.GetBookmarks(bPageOpts)
	fmt.Printf("  GetBookmarks(page):          %6dms  (%d rows)\n", time.Since(t).Milliseconds(), len(bmarksPage))

	cats, _ := d.GetBookmarkCategories(username)
	t = time.Now()
	var bookmarksHTML bytes.Buffer
	components.BookmarksPage(benchPageProps("Bookmarks", "bookmarks"), bmarksPage, cats, 0, model.Pager{Page: 1, PerPage: webPageSize, Total: bCount}).Render(context.Background(), &bookmarksHTML)
	fmt.Printf("  Render BookmarksPage:        %6dms  (%d KB)\n", time.Since(t).Milliseconds(), bookmarksHTML.Len()/1024)

	fmt.Println()
}

func benchPageProps(title, activeNav string) components.PageProps {
	return components.PageProps{
		CSRFToken:     "bench",
		UserRole:      "admin",
		Username:      "admin",
		UserPlatforms: []string{"youtube", "tiktok", "instagram", "twitter"},
		PageTitle:     title,
		ActiveNav:     activeNav,
		ShortcutConfig: map[string]string{
			"feed.like":         "l",
			"feed.bookmark":     "b",
			"feed.share":        "s",
			"feed.translate":    "t",
			"feed.media":        "f",
			"shorts.autoplay":   "a",
			"shorts.bookmark":   "b",
			"shorts.share":      "s",
			"shorts.grid":       "c",
			"player.fullscreen": "f",
			"player.bookmark":   "b",
			"player.share":      "s",
			"player.autoplay":   "a",
		},
		Language: "en",
		Text:     map[string]string{},
		StaticV: func(path string) string {
			return "/static/" + path
		},
		ESBundle: "js/dist/shorts.js",
	}
}
