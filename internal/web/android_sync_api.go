package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/subtitlemeta"
	"github.com/screwys/igloo/internal/worker"
)

const (
	androidSyncItemPageCap         = 500
	androidSyncAssetPageCap        = 2000
	androidSyncFreshGenerationTTL  = 6 * time.Hour
	androidSyncFreshGenerationSkew = 5 * time.Minute
	androidSyncSourceDriftReuseTTL = 30 * time.Minute
	androidSyncFeedRankMaxRows     = 5000
)

func (s *Server) registerAndroidSyncAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/android/sync/generation/latest", s.handleAndroidSyncLatestGeneration)
	mux.HandleFunc("GET /api/android/sync/generation/{generationID}/items", s.handleAndroidSyncGenerationItems)
	mux.HandleFunc("GET /api/android/sync/generation/{generationID}/assets", s.handleAndroidSyncGenerationAssets)
	mux.HandleFunc("GET /api/android/sync/assets/{assetID}", s.handleAndroidSyncAsset)
	mux.HandleFunc("POST /api/android/sync/health", s.handleAndroidSyncHealth)
}

func (s *Server) handleAndroidSyncLatestGeneration(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	start := time.Now()
	retention, err := androidSyncRetentionSettingsFromRequest(r, cacheHealthSettings(loadCacheHealthFromDisk(s.cfg.DataDir)))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_retention", err.Error())
		return
	}
	gen, err := s.ensureAndroidSyncGeneration(user.Username, retention)
	if err != nil {
		slog.Error("android_sync_generation_failed", "err", err, "duration_ms", time.Since(start).Milliseconds())
		writeJSONError(w, http.StatusInternalServerError, "generation_failed", err.Error())
		return
	}
	slog.Info(
		"android_sync_generation_latest",
		"generation_id", gen.GenerationID,
		"items", gen.ItemCount,
		"assets", gen.AssetCount,
		"ready_assets", gen.ReadyAssetCount,
		"server_missing_assets", gen.ServerMissingAssetCount,
		"moments_days", retention.MomentsDays,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	writeJSON(w, http.StatusOK, map[string]any{"generation": gen})
}

func (s *Server) handleAndroidSyncGenerationItems(w http.ResponseWriter, r *http.Request) {
	genID := r.PathValue("generationID")
	if genID == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_generation", "generation id required")
		return
	}
	after := parseAndroidSyncAfter(r.URL.Query().Get("after"))
	items, err := s.db.ListAndroidSyncItems(genID, after, androidSyncItemPageCap)
	if err != nil {
		slog.Error("android_sync_items_read_failed", "generation_id", genID, "after", after, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "generation item read failed")
		return
	}
	if items == nil {
		items = []model.AndroidSyncItem{}
	}
	next, end := androidSyncPageCursor(items, androidSyncItemPageCap, func(item model.AndroidSyncItem) int64 { return item.Seq })
	slog.Info(
		"android_sync_items_page",
		"generation_id", genID,
		"after", after,
		"count", len(items),
		"next", next,
		"end", end,
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"generation_id": genID,
		"items":         items,
		"next":          next,
		"end_of_stream": end,
	})
}

func (s *Server) handleAndroidSyncGenerationAssets(w http.ResponseWriter, r *http.Request) {
	genID := r.PathValue("generationID")
	if genID == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_generation", "generation id required")
		return
	}
	after := parseAndroidSyncAfter(r.URL.Query().Get("after"))
	assets, err := s.db.ListAndroidSyncAssets(genID, after, androidSyncAssetPageCap)
	if err != nil {
		slog.Error("android_sync_assets_read_failed", "generation_id", genID, "after", after, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "db_error", "generation asset read failed")
		return
	}
	if assets == nil {
		assets = []model.AndroidSyncAsset{}
	}
	for i := range assets {
		if assets[i].State == "ready" {
			assets[i].ServerURL = "/api/android/sync/assets/" + assets[i].AssetID
		}
	}
	next, end := androidSyncPageCursor(assets, androidSyncAssetPageCap, func(asset model.AndroidSyncAsset) int64 { return asset.Seq })
	slog.Info(
		"android_sync_assets_page",
		"generation_id", genID,
		"after", after,
		"count", len(assets),
		"next", next,
		"end", end,
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"generation_id": genID,
		"assets":        assets,
		"next":          next,
		"end_of_stream": end,
	})
}

func (s *Server) handleAndroidSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := r.PathValue("assetID")
	if assetID == "" {
		http.NotFound(w, r)
		return
	}
	asset, err := s.db.GetAndroidSyncAsset(assetID)
	if err != nil {
		slog.Error("android_sync_asset_lookup_failed", "asset_id", assetID, "err", err)
		http.Error(w, "asset lookup failed", http.StatusInternalServerError)
		return
	}
	if asset == nil || asset.State != "ready" {
		http.NotFound(w, r)
		return
	}
	path := s.androidSyncAssetPath(*asset)
	if path == "" {
		http.NotFound(w, r)
		return
	}
	if asset.ContentType != "" {
		w.Header().Set("Content-Type", asset.ContentType)
	}
	w.Header().Set("Cache-Control", "private, no-transform")
	http.ServeFile(w, r, path)
}

func (s *Server) handleAndroidSyncHealth(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_json", "invalid health payload")
		return
	}
	generationID, _ := body["generation_id"].(string)
	if generationID == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_generation", "generation_id required")
		return
	}
	reportedAt := anyInt64(body["reported_at_ms"])
	if reportedAt <= 0 {
		reportedAt = time.Now().UnixMilli()
	}
	counts, _ := body["counts"].(map[string]any)
	bytes, _ := body["bytes"].(map[string]any)
	retention, _ := body["retention"].(map[string]any)
	raw, _ := json.Marshal(body)
	if err := s.db.RecordAndroidSyncHealth(
		generationID,
		reportedAt,
		raw,
		anyInt(counts["verified"]),
		anyInt(counts["pending"]),
		anyInt(counts["failed"]),
		anyInt(counts["missing"]),
		anyInt(counts["total"]),
		anyInt64(bytes["verified"]),
	); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "db_error", "health write failed")
		return
	}
	p := filepath.Join(s.cfg.DataDir, "logs", "android", "android_sync_health.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, raw, 0o644)
	if len(retention) > 0 {
		s.updateAndroidCacheHealthRetention(retention, reportedAt)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func androidSyncRetentionSettingsFromRequest(r *http.Request, fallback db.AndroidRetentionSettings) (db.AndroidRetentionSettings, error) {
	q := r.URL.Query()
	settings := fallback
	var err error
	if settings.FeedDays, err = optionalAndroidRetentionDaysQueryInt(q.Get("feed_days"), settings.FeedDays, "feed_days"); err != nil {
		return settings, err
	}
	if settings.YoutubeDays, err = optionalAndroidRetentionDaysQueryInt(q.Get("youtube_days"), settings.YoutubeDays, "youtube_days"); err != nil {
		return settings, err
	}
	if settings.MomentsDays, err = optionalAndroidRetentionDaysQueryInt(q.Get("moments_days"), settings.MomentsDays, "moments_days"); err != nil {
		return settings, err
	}
	if raw := strings.TrimSpace(q.Get("story_hours")); raw != "" {
		n, parseErr := strconv.Atoi(raw)
		if parseErr != nil || n < 0 {
			return settings, fmt.Errorf("story_hours must be a non-negative integer")
		}
		settings.StoryHours = db.NormalizeStoriesWindowHours(n)
	}
	return settings, nil
}

func optionalAndroidRetentionDaysQueryInt(raw string, fallback int, name string) (int, error) {
	n, err := optionalNonNegativeQueryInt(raw, fallback, name)
	if err != nil {
		return fallback, err
	}
	if strings.TrimSpace(raw) == "" {
		return n, nil
	}
	if !db.IsValidRetentionDays(n) {
		return fallback, fmt.Errorf("%s must be one of 0, 1, 2, 3, 7, 14, 30, 60, 90", name)
	}
	return n, nil
}

func optionalNonNegativeQueryInt(raw string, fallback int, name string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fallback, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return n, nil
}

func parseAndroidSyncAfter(raw string) int64 {
	if raw == "" {
		return 0
	}
	n, _ := strconv.ParseInt(raw, 10, 64)
	if n < 0 {
		return 0
	}
	return n
}

func androidSyncPageCursor[T any](rows []T, pageCap int, seq func(T) int64) (string, bool) {
	if len(rows) == 0 {
		return "", true
	}
	last := seq(rows[len(rows)-1])
	return strconv.FormatInt(last, 10), len(rows) < pageCap
}

func (s *Server) ensureAndroidSyncGeneration(username string, retention db.AndroidRetentionSettings) (*model.AndroidSyncGeneration, error) {
	nowMs := time.Now().UnixMilli()
	if latest, err := s.db.GetLatestAndroidSyncGeneration(); err != nil {
		return nil, err
	} else if androidSyncGenerationReusableDuringSourceDrift(latest, retention, nowMs) {
		return latest, nil
	}

	sourceVersion, err := s.db.AndroidSyncSourceVersion(retention)
	if err != nil {
		return nil, err
	}
	if latest, err := s.db.GetLatestAndroidSyncGeneration(); err != nil {
		return nil, err
	} else if latest != nil && latest.SourceVersion == sourceVersion && androidSyncGenerationFreshForRetention(latest, retention, nowMs) {
		return latest, nil
	}

	if existing, err := s.db.GetAndroidSyncGenerationBySource(sourceVersion); err != nil {
		return nil, err
	} else if existing != nil && androidSyncGenerationRetentionMatches(existing.Retention, retention) {
		return existing, nil
	}

	s.androidSyncGenerationMu.Lock()
	defer s.androidSyncGenerationMu.Unlock()

	nowMs = time.Now().UnixMilli()
	if latest, err := s.db.GetLatestAndroidSyncGeneration(); err != nil {
		return nil, err
	} else if androidSyncGenerationReusableDuringSourceDrift(latest, retention, nowMs) {
		return latest, nil
	}

	sourceVersion, err = s.db.AndroidSyncSourceVersion(retention)
	if err != nil {
		return nil, err
	}
	if latest, err := s.db.GetLatestAndroidSyncGeneration(); err != nil {
		return nil, err
	} else if latest != nil && latest.SourceVersion == sourceVersion && androidSyncGenerationFreshForRetention(latest, retention, nowMs) {
		return latest, nil
	}

	if existing, err := s.db.GetAndroidSyncGenerationBySource(sourceVersion); err != nil {
		return nil, err
	} else if existing != nil && androidSyncGenerationRetentionMatches(existing.Retention, retention) {
		return existing, nil
	}

	phaseStart := time.Now()
	sets, err := s.db.ListAndroidSyncDesiredSets(username, retention, nowMs)
	if err != nil {
		return nil, err
	}
	slog.Info(
		"android_sync_generation_phase",
		"phase", "desired_sets",
		"tweets", len(sets.Tweets),
		"videos", len(sets.Videos),
		"channels", len(sets.Channels),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)
	phaseStart = time.Now()
	items, contentCounts, err := s.buildAndroidSyncItems(username, sets)
	if err != nil {
		return nil, err
	}
	slog.Info(
		"android_sync_generation_phase",
		"phase", "items",
		"items", len(items),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)
	phaseStart = time.Now()
	assets, assetCounts, err := s.buildAndroidSyncAssets(username, sets)
	if err != nil {
		return nil, err
	}
	slog.Info(
		"android_sync_generation_phase",
		"phase", "assets",
		"assets", len(assets),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)

	readyAssets := 0
	serverMissingAssets := 0
	var totalBytes int64
	for i := range assets {
		assets[i].Seq = int64(i + 1)
		if assets[i].State == "server_missing" {
			serverMissingAssets++
			continue
		}
		readyAssets++
		totalBytes += assets[i].SizeBytes
	}
	for i := range items {
		items[i].Seq = int64(i + 1)
	}

	generationID := "android-sync-" + sourceVersion[:16]
	gen := model.AndroidSyncGeneration{
		GenerationID:            generationID,
		CreatedAtMs:             nowMs,
		Status:                  "ready",
		SourceVersion:           sourceVersion,
		Retention:               androidSyncGenerationRetentionPayload(retention),
		ItemCount:               len(items),
		AssetCount:              len(assets),
		ReadyAssetCount:         readyAssets,
		ServerMissingAssetCount: serverMissingAssets,
		TotalBytes:              totalBytes,
		ContentCounts:           contentCounts,
		AssetCounts:             assetCounts,
	}
	if err := s.db.StoreAndroidSyncGeneration(gen, items, assets); err != nil {
		return nil, err
	}
	return &gen, nil
}

func androidSyncGenerationRetentionPayload(retention db.AndroidRetentionSettings) map[string]int {
	return map[string]int{
		"feed_days":            retention.FeedDays,
		"youtube_days":         retention.YoutubeDays,
		"moments_days":         retention.MomentsDays,
		"story_hours":          retention.StoryHours,
		"materializer_version": db.AndroidSyncMaterializerVersion,
	}
}

func androidSyncGenerationFreshForRetention(gen *model.AndroidSyncGeneration, retention db.AndroidRetentionSettings, nowMs int64) bool {
	if gen == nil || gen.Status != "ready" {
		return false
	}
	if !androidSyncGenerationRetentionMatches(gen.Retention, retention) {
		return false
	}
	age := time.Duration(nowMs-gen.CreatedAtMs) * time.Millisecond
	if age < -androidSyncFreshGenerationSkew {
		return false
	}
	return age <= androidSyncFreshGenerationTTL
}

func androidSyncGenerationReusableDuringSourceDrift(gen *model.AndroidSyncGeneration, retention db.AndroidRetentionSettings, nowMs int64) bool {
	if !androidSyncGenerationFreshForRetention(gen, retention, nowMs) {
		return false
	}
	age := time.Duration(nowMs-gen.CreatedAtMs) * time.Millisecond
	return age <= androidSyncSourceDriftReuseTTL
}

func androidSyncGenerationRetentionMatches(raw map[string]int, retention db.AndroidRetentionSettings) bool {
	if raw == nil {
		return false
	}
	return raw["feed_days"] == retention.FeedDays &&
		raw["youtube_days"] == retention.YoutubeDays &&
		raw["moments_days"] == retention.MomentsDays &&
		raw["story_hours"] == retention.StoryHours &&
		raw["materializer_version"] == db.AndroidSyncMaterializerVersion
}

func (s *Server) buildAndroidSyncItems(username string, sets db.AndroidSyncDesiredSets) ([]model.AndroidSyncItem, map[string]int, error) {
	var out []model.AndroidSyncItem
	counts := map[string]int{}

	for _, channelID := range sets.SortedChannels() {
		ch, err := s.db.GetChannel(channelID)
		if err != nil {
			return nil, counts, err
		}
		profile, _ := s.db.GetChannelProfile(channelID)
		if profile != nil && profile.Tombstone {
			profile = nil
		}
		if ch == nil {
			if profile != nil {
				item, err := marshalAndroidSyncItem("channel_profiles", channelID, deltaBundle{
					PrimaryKind: "channel_profiles",
					Primary:     channelProfileToAttachment(profile, canonicalChannelProfileURL(channelID, profile, "")),
				})
				if err != nil {
					return nil, counts, err
				}
				out = append(out, item)
				counts["channel_profiles"]++
			}
			continue
		}
		attachments := map[string]any{}
		if profile != nil {
			attachments["channel_profile"] = channelProfileToAttachment(profile, canonicalChannelProfileURL(channelID, profile, ch.URL))
		}
		if settings, _ := s.db.GetChannelSettings(channelID); settings != nil {
			attachments["channel_settings"] = settings
		}
		primary := channelToBundlePrimary(*ch)
		attachUserStateFromPrimary("channels", primary, attachments)
		item, err := marshalAndroidSyncItem("channels", channelID, deltaBundle{
			PrimaryKind: "channels",
			Primary:     primary,
			Attachments: attachments,
		})
		if err != nil {
			return nil, counts, err
		}
		out = append(out, item)
		counts["channels"]++
	}

	tweetIDs := sets.SortedTweets()
	tweetsByID := map[string]model.FeedItem{}
	for _, chunk := range chunkStrings(tweetIDs, 300) {
		items, err := s.db.GetFeedItemsForTweetIDs(chunk)
		if err != nil {
			return nil, counts, err
		}
		var page []model.FeedItem
		for _, id := range chunk {
			if item, ok := items[id]; ok {
				page = append(page, item)
			}
		}
		page = feed.EnrichFeedItemsPreserveRows(s.db, page, username)
		for _, item := range page {
			tweetsByID[item.TweetID] = item
		}
	}
	bookmarks, _ := s.db.GetBookmarksForVideoIDsRich(tweetIDs)
	mutedHandles, _ := s.db.GetMutedAccounts()
	mutedHandleSet := normalizeHandleSet(mutedHandles)
	subscribeURLs := map[string]string{}
	for _, id := range tweetIDs {
		item, ok := tweetsByID[id]
		if !ok {
			continue
		}
		if item.ChannelID != "" {
			if _, ok := subscribeURLs[item.ChannelID]; !ok {
				subscribeURLs[item.ChannelID] = s.db.ResolveSubscribeURL(item.ChannelID)
			}
		}
		attachments := map[string]any{}
		if len(item.Retweeters) > 0 {
			attachments["retweet_sources"] = item.Retweeters
		}
		primary := feedItemToBundlePrimary(item, bookmarks, subscribeURLs, mutedHandleSet)
		attachUserStateFromPrimary("feed_items", primary, attachments)
		row, err := marshalAndroidSyncItem("feed_items", id, deltaBundle{
			PrimaryKind: "feed_items",
			Primary:     primary,
			Attachments: attachments,
		})
		if err != nil {
			return nil, counts, err
		}
		out = append(out, row)
		counts["feed_items"]++
	}
	if rankItem, ok, err := s.androidSyncFeedRankItem(username, sets.Tweets); err != nil {
		return nil, counts, err
	} else if ok {
		out = append(out, rankItem)
		counts["feed_rank"]++
	}

	videoIDs := sets.SortedVideos()
	videoBookmarks, _ := s.db.GetBookmarksForVideoIDsRich(videoIDs)
	videoReposts, _ := s.db.GetVideoRepostSourcesForVideoIDs(videoIDs)
	for _, videoID := range videoIDs {
		video, err := s.db.GetVideo(videoID)
		if err != nil {
			return nil, counts, err
		}
		if video == nil {
			continue
		}
		primary := videoToBundlePrimary(*video)
		if canonicalURL := s.androidSyncCanonicalVideoURL(*video); canonicalURL != "" {
			primary["canonical_url"] = canonicalURL
		}
		if bi, ok := videoBookmarks[video.VideoID]; ok {
			applyBookmarkBundleFields(primary, bi)
		}
		attachments := map[string]any{}
		if strings.HasPrefix(video.ChannelID, "youtube_") {
			comments, _ := s.db.GetComments(video.VideoID, youtubeCommentsCap)
			for i := range comments {
				comments[i].SetPublishedAtMs()
			}
			if len(comments) > 0 {
				attachments["video_comments"] = model.PresentComments(comments, model.CommentCreatorAuthorID(video.ChannelID))
			}
			if segments, _ := s.db.GetSponsorBlockSegments(video.VideoID); len(segments) > 0 {
				attachments["sponsorblock_segments"] = segments
			}
			if checked, _ := s.db.GetSponsorBlockChecked(video.VideoID); checked != nil {
				attachments["sponsorblock_checked"] = map[string]any{
					"video_id":           checked.VideoID,
					"checked_at_ms":      checked.CheckedAtMs,
					"video_age_at_check": checked.VideoAgeAtCheck,
				}
			}
		}
		if strings.HasPrefix(video.ChannelID, "tiktok_") {
			reposts := videoReposts[video.VideoID]
			if reposts == nil {
				reposts = []model.VideoRepostSource{}
			}
			attachments["video_repost_sources"] = reposts
		}
		attachUserStateFromPrimary("videos", primary, attachments)
		row, err := marshalAndroidSyncItem("videos", videoID, deltaBundle{
			PrimaryKind: "videos",
			Primary:     primary,
			Attachments: attachments,
		})
		if err != nil {
			return nil, counts, err
		}
		out = append(out, row)
		counts["videos"]++
	}

	return out, counts, nil
}

func (s *Server) androidSyncFeedRankItem(username string, desiredTweets map[string]struct{}) (model.AndroidSyncItem, bool, error) {
	snapshotAt, err := s.db.SnapshotComputedAt(username)
	if err != nil {
		return model.AndroidSyncItem{}, false, err
	}
	if snapshotAt <= 0 {
		return model.AndroidSyncItem{}, false, nil
	}

	rows := make([]map[string]any, 0)
	after := 0
	for {
		page, err := s.db.ListSnapshotPage(username, after, 200)
		if err != nil {
			return model.AndroidSyncItem{}, false, err
		}
		for _, row := range page {
			after = max(after, row.RankPosition)
			if _, ok := desiredTweets[row.Item.TweetID]; !ok {
				continue
			}
			if len(rows) >= androidSyncFeedRankMaxRows {
				break
			}
			rows = append(rows, map[string]any{
				"tweet_id":      row.Item.TweetID,
				"rank_position": row.RankPosition,
			})
		}
		if len(rows) >= androidSyncFeedRankMaxRows || len(page) < 200 {
			break
		}
	}

	item, err := marshalAndroidSyncItem("feed_rank", "snapshot", deltaBundle{
		PrimaryKind: "feed_rank",
		Primary: map[string]any{
			"snapshot_at": snapshotAt,
			"row_count":   len(rows),
			"rows":        rows,
		},
	})
	if err != nil {
		return model.AndroidSyncItem{}, false, err
	}
	return item, true, nil
}

func (s *Server) androidSyncCanonicalVideoURL(video model.Video) string {
	platform := androidSyncPlatformFromChannelID(video.ChannelID)
	rawID := androidSyncRawPlatformID(video.VideoID, platform)
	if rawID == "" {
		return ""
	}
	switch platform {
	case "youtube":
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(rawID)
	case "tiktok":
		handle := s.androidSyncChannelHandle(video.ChannelID)
		if handle == "" {
			return ""
		}
		return "https://www.tiktok.com/@" + url.PathEscape(handle) + "/video/" + url.PathEscape(rawID)
	case "instagram":
		switch {
		case strings.HasPrefix(rawID, "post_"):
			return "https://www.instagram.com/p/" + url.PathEscape(strings.TrimPrefix(rawID, "post_")) + "/"
		case strings.HasPrefix(rawID, "reel_"):
			return "https://www.instagram.com/reel/" + url.PathEscape(strings.TrimPrefix(rawID, "reel_")) + "/"
		default:
			return "https://www.instagram.com/reel/" + url.PathEscape(rawID) + "/"
		}
	case "twitter", "x":
		handle := s.androidSyncChannelHandle(video.ChannelID)
		if handle == "" {
			return ""
		}
		return "https://x.com/" + url.PathEscape(handle) + "/status/" + url.PathEscape(rawID)
	default:
		return ""
	}
}

func androidSyncRawPlatformID(id, platform string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	prefixes := []string{platform + "_"}
	if platform == "twitter" || platform == "x" {
		prefixes = append(prefixes, "twitter_", "x_")
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(id, prefix))
		}
	}
	if platform == "tiktok" && androidSyncAllDigits(id) {
		return id
	}
	if platform == "youtube" {
		return id
	}
	return ""
}

func androidSyncAllDigits(id string) bool {
	if id == "" {
		return false
	}
	for _, ch := range id {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func (s *Server) androidSyncChannelHandle(channelID string) string {
	if profile, err := s.db.GetChannelProfile(channelID); err == nil && profile != nil {
		if handle := strings.TrimPrefix(strings.TrimSpace(profile.Handle), "@"); handle != "" {
			return handle
		}
	}
	if ch, err := s.db.GetChannel(channelID); err == nil && ch != nil {
		if handle := strings.TrimPrefix(strings.TrimSpace(ch.SourceID), "@"); handle != "" {
			return handle
		}
	}
	return ""
}

func marshalAndroidSyncItem(kind, id string, payload any) (model.AndroidSyncItem, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return model.AndroidSyncItem{}, err
	}
	return model.AndroidSyncItem{ItemKind: kind, ItemID: id, PayloadJSON: raw}, nil
}

func chunkStrings(values []string, size int) [][]string {
	if size <= 0 || len(values) == 0 {
		return nil
	}
	var chunks [][]string
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, values[start:end])
	}
	return chunks
}

func (s *Server) buildAndroidSyncAssets(username string, sets db.AndroidSyncDesiredSets) ([]model.AndroidSyncAsset, map[string]int, error) {
	byKey := map[string]model.AndroidSyncAsset{}
	counts := map[string]int{}
	phaseStart := time.Now()
	tweetIDs := sets.SortedTweets()
	videoIDs := sets.SortedVideos()
	mediaVideoIDs := sets.SortedMediaVideos()
	channelIDs := sets.SortedChannels()

	feedRows, err := s.db.ListAndroidSyncMediaAssetRows("feed_media", tweetIDs)
	if err != nil {
		return nil, counts, err
	}
	s.addAndroidSyncMediaAssets(byKey, feedRows)
	slog.Info(
		"android_sync_asset_phase",
		"phase", "feed_media_rows",
		"rows", len(feedRows),
		"assets", len(byKey),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)

	phaseStart = time.Now()
	quoteRows, err := s.db.ListAndroidSyncQuoteMediaAssetRows(tweetIDs)
	if err != nil {
		return nil, counts, err
	}
	s.addAndroidSyncMediaAssets(byKey, quoteRows)
	slog.Info(
		"android_sync_asset_phase",
		"phase", "quote_media_rows",
		"rows", len(quoteRows),
		"assets", len(byKey),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)

	phaseStart = time.Now()
	for _, videoID := range mediaVideoIDs {
		video, err := s.db.GetVideo(videoID)
		if err != nil {
			return nil, counts, err
		}
		if video == nil {
			continue
		}
		for _, asset := range s.androidSyncVideoPlaybackAssets(*video) {
			addAndroidSyncAsset(byKey, asset)
		}
	}
	slog.Info(
		"android_sync_asset_phase",
		"phase", "video_playback_assets",
		"videos", len(mediaVideoIDs),
		"assets", len(byKey),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)

	phaseStart = time.Now()
	for _, videoID := range videoIDs {
		video, err := s.db.GetVideo(videoID)
		if err != nil {
			return nil, counts, err
		}
		if video == nil {
			continue
		}
		for _, asset := range s.androidSyncVideoMetadataAssets(*video) {
			addAndroidSyncAsset(byKey, asset)
		}
	}
	slog.Info(
		"android_sync_asset_phase",
		"phase", "video_metadata_assets",
		"videos", len(videoIDs),
		"assets", len(byKey),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)

	phaseStart = time.Now()
	thumbnailVideoIDs, err := s.db.ListAndroidSyncAlwaysThumbnailVideoIDs()
	if err != nil {
		return nil, counts, err
	}
	for _, videoID := range thumbnailVideoIDs {
		video, err := s.db.GetVideo(videoID)
		if err != nil {
			return nil, counts, err
		}
		if video == nil {
			continue
		}
		addAndroidSyncAsset(byKey, s.androidSyncVideoThumbnailAsset(*video, "thumbnail"))
	}
	slog.Info(
		"android_sync_asset_phase",
		"phase", "always_video_thumbnails",
		"videos", len(thumbnailVideoIDs),
		"assets", len(byKey),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)

	phaseStart = time.Now()
	for _, channelID := range channelIDs {
		for _, asset := range s.androidSyncChannelFallbackAssets(channelID, "retention") {
			addAndroidSyncAsset(byKey, asset)
		}
	}
	slog.Info(
		"android_sync_asset_phase",
		"phase", "channel_fallbacks",
		"channels", len(channelIDs),
		"assets", len(byKey),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)

	phaseStart = time.Now()
	profileChannelIDs, err := s.db.ListAndroidSyncProfileChannelIDs()
	if err != nil {
		return nil, counts, err
	}
	for _, channelID := range profileChannelIDs {
		for _, asset := range s.androidSyncChannelFallbackAssets(channelID, "profile") {
			addAndroidSyncAsset(byKey, asset)
		}
	}
	slog.Info(
		"android_sync_asset_phase",
		"phase", "profile_channel_assets",
		"channels", len(profileChannelIDs),
		"assets", len(byKey),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)

	out := make([]model.AndroidSyncAsset, 0, len(byKey))
	finalizedCount := 0
	for _, asset := range byKey {
		finalized := s.finalizeAndroidSyncAsset(asset)
		out = append(out, finalized)
		counts[finalized.AssetKind]++
		finalizedCount++
		if finalizedCount%1000 == 0 {
			slog.Info(
				"android_sync_asset_finalize_progress",
				"done", finalizedCount,
				"total", len(byKey),
			)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := androidSyncAssetPriority(out[i]), androidSyncAssetPriority(out[j])
		if pi != pj {
			return pi < pj
		}
		if out[i].EffectiveRecencyMs != out[j].EffectiveRecencyMs {
			return out[i].EffectiveRecencyMs > out[j].EffectiveRecencyMs
		}
		return out[i].AssetID < out[j].AssetID
	})
	return out, counts, nil
}

func (s *Server) addAndroidSyncMediaAssets(byKey map[string]model.AndroidSyncAsset, rows []db.AndroidSyncMediaAssetRow) {
	for _, row := range rows {
		for _, asset := range s.androidSyncAssetsFromMediaRow(row) {
			addAndroidSyncAsset(byKey, asset)
		}
	}
}

func addAndroidSyncAsset(byKey map[string]model.AndroidSyncAsset, asset model.AndroidSyncAsset) {
	if asset.AssetID == "" || asset.AssetKind == "" {
		return
	}
	key := asset.AssetID + "\x00" + asset.AssetKind
	if _, exists := byKey[key]; exists {
		return
	}
	byKey[key] = asset
}

func (s *Server) androidSyncAssetsFromMediaRow(row db.AndroidSyncMediaAssetRow) []model.AndroidSyncAsset {
	if androidSyncSkipsMediaRow(row) {
		return nil
	}
	serverURL := fmt.Sprintf("/api/media/slide/%s/%d", row.OwnerID, row.MediaIndex)
	if androidSyncMediaRowUsesStream(row) {
		serverURL = "/api/media/stream/" + row.OwnerID
	}
	contentType := androidSyncContentType(resolveDataPath(s.cfg.DataDir, row.FilePath))
	if contentType == "application/octet-stream" {
		if androidSyncMediaRowUsesStream(row) {
			contentType = "video/mp4"
		} else {
			contentType = "image/jpeg"
		}
	}
	out := []model.AndroidSyncAsset{{
		AssetID:            db.BuildManifestAssetID("twitter", "tweet", row.OwnerID, "post_media", row.MediaIndex),
		AssetKind:          "post_media",
		OwnerID:            row.OwnerID,
		OwnerKind:          "tweet",
		Bucket:             "twitter_media",
		ServerURL:          serverURL,
		ContentType:        contentType,
		SizeBytes:          row.FileSize,
		State:              "ready",
		RequiredReason:     "retention",
		EffectiveRecencyMs: row.RecencyMs,
	}}
	if row.MediaIndex == 0 && androidSyncMediaRowNeedsThumbnail(row) {
		out = append(out, model.AndroidSyncAsset{
			AssetID:            db.BuildManifestAssetID("twitter", "tweet", row.OwnerID, "post_thumbnail", 0),
			AssetKind:          "post_thumbnail",
			OwnerID:            row.OwnerID,
			OwnerKind:          "tweet",
			Bucket:             "twitter_media",
			ServerURL:          "/api/media/thumbnail/" + row.OwnerID,
			ContentType:        "image/jpeg",
			State:              "ready",
			RequiredReason:     "retention",
			EffectiveRecencyMs: row.RecencyMs,
		})
	}
	return out
}

func androidSyncSkipsMediaRow(row db.AndroidSyncMediaAssetRow) bool {
	if strings.EqualFold(strings.TrimSpace(row.MediaType), "audio") {
		return true
	}
	switch strings.ToLower(filepath.Ext(row.FilePath)) {
	case ".mp3", ".m4a", ".aac", ".ogg":
		return true
	default:
		return false
	}
}

func androidSyncMediaRowUsesStream(row db.AndroidSyncMediaAssetRow) bool {
	ext := strings.ToLower(filepath.Ext(row.FilePath))
	if androidSyncMediaPathIsImage(row.FilePath) {
		return false
	}
	switch ext {
	case ".mp4", ".webm", ".mkv", ".mov", ".m4v":
		return true
	default:
		return strings.EqualFold(row.MediaType, "video") || strings.EqualFold(row.MediaType, "gif")
	}
}

func androidSyncMediaRowNeedsThumbnail(row db.AndroidSyncMediaAssetRow) bool {
	if androidSyncSkipsMediaRow(row) {
		return false
	}
	return androidSyncMediaRowUsesStream(row) || strings.EqualFold(row.MediaType, "video") || strings.EqualFold(row.MediaType, "gif")
}

func androidSyncMediaPathIsImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".image":
		return true
	default:
		return false
	}
}

func androidSyncManifestEntryDesired(entry model.ManifestEntry, sets db.AndroidSyncDesiredSets) bool {
	switch entry.AssetKind {
	case "avatar", "banner":
		_, ok := sets.Channels[entry.OwnerID]
		return ok
	case "video_stream", "post_media", "post_audio":
		_, ok := sets.MediaVideos[entry.OwnerID]
		if ok {
			return true
		}
		_, ok = sets.Tweets[entry.OwnerID]
		return ok
	case "subtitle", "preview_track_json", "preview_sprite":
		_, ok := sets.Videos[entry.OwnerID]
		if ok {
			return true
		}
		_, ok = sets.Tweets[entry.OwnerID]
		return ok
	default:
		_, ok := sets.Tweets[entry.OwnerID]
		if ok {
			return true
		}
		_, ok = sets.Videos[entry.OwnerID]
		return ok
	}
}

func (s *Server) androidSyncAssetFromManifest(entry model.ManifestEntry) model.AndroidSyncAsset {
	return model.AndroidSyncAsset{
		AssetID:            entry.AssetID,
		AssetKind:          entry.AssetKind,
		OwnerID:            entry.OwnerID,
		OwnerKind:          entry.OwnerKind,
		Bucket:             entry.Bucket,
		ServerURL:          entry.ServerURL,
		ContentType:        entry.ContentType,
		SizeBytes:          entry.SizeHint,
		State:              "ready",
		RequiredReason:     entry.Scope,
		IsAuto:             entry.IsAuto,
		AudioLanguage:      entry.AudioLanguage,
		EffectiveRecencyMs: entry.EffectiveRecencyMs,
	}
}

func (s *Server) androidSyncVideoMetadataAssets(video model.Video) []model.AndroidSyncAsset {
	platform := androidSyncPlatformFromChannelID(video.ChannelID)
	ownerKind := androidSyncVideoOwnerKind(platform)
	bucket := androidSyncVideoBucket(platform)
	recency := int64(0)
	if video.PublishedAt != nil {
		recency = video.PublishedAt.UnixMilli()
	}
	out := []model.AndroidSyncAsset{
		s.androidSyncVideoThumbnailAsset(video, "metadata"),
	}
	if asset, ok := s.androidSyncVideoDearrowThumbnailAsset(video, "metadata"); ok {
		out = append(out, asset)
	}
	if strings.HasPrefix(video.ChannelID, "youtube_") {
		if s.androidSyncSubtitlePath(video.VideoID) != "" {
			isAuto, audioLang := s.androidSyncSubtitleMetadata(video)
			out = append(out, model.AndroidSyncAsset{
				AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "subtitle", 0),
				AssetKind:          "subtitle",
				OwnerID:            video.VideoID,
				OwnerKind:          ownerKind,
				Bucket:             bucket,
				ServerURL:          "/api/media/subtitle/" + video.VideoID,
				ContentType:        "text/vtt",
				State:              "ready",
				RequiredReason:     "metadata",
				IsAuto:             &isAuto,
				AudioLanguage:      audioLang,
				EffectiveRecencyMs: recency,
			})
		}
		if s.ensureAndroidSyncPreviewTrackJSON(video) && s.androidSyncPreviewPath(video.VideoID, "track.json") != "" {
			out = append(out, model.AndroidSyncAsset{
				AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "preview_track_json", 0),
				AssetKind:          "preview_track_json",
				OwnerID:            video.VideoID,
				OwnerKind:          ownerKind,
				Bucket:             bucket,
				ServerURL:          "/api/media/preview-track-json/" + video.VideoID,
				ContentType:        "application/json",
				State:              "ready",
				RequiredReason:     "metadata",
				EffectiveRecencyMs: recency,
			})
		}
		if s.androidSyncPreviewPath(video.VideoID, "sprite.jpg") != "" {
			out = append(out, model.AndroidSyncAsset{
				AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "preview_sprite", 0),
				AssetKind:          "preview_sprite",
				OwnerID:            video.VideoID,
				OwnerKind:          ownerKind,
				Bucket:             bucket,
				ServerURL:          "/api/media/preview-sprite/" + video.VideoID,
				ContentType:        "image/jpeg",
				State:              "ready",
				RequiredReason:     "metadata",
				EffectiveRecencyMs: recency,
			})
		}
	}
	return out
}

func (s *Server) androidSyncVideoPlaybackAssets(video model.Video) []model.AndroidSyncAsset {
	platform := androidSyncPlatformFromChannelID(video.ChannelID)
	ownerKind := androidSyncVideoOwnerKind(platform)
	bucket := androidSyncVideoBucket(platform)
	recency := int64(0)
	if video.PublishedAt != nil {
		recency = video.PublishedAt.UnixMilli()
	}
	out := []model.AndroidSyncAsset{
		s.androidSyncVideoThumbnailAsset(video, "retention"),
	}
	if asset, ok := s.androidSyncVideoDearrowThumbnailAsset(video, "retention"); ok {
		out = append(out, asset)
	}
	if androidSyncVideoIsStillMedia(video) {
		slideCount := video.MediaSlideCount
		if slideCount <= 0 {
			slideCount = 1
		}
		for i := 0; i < slideCount; i++ {
			path := s.androidSyncSlidePath(video.VideoID, i)
			contentType := androidSyncContentType(path)
			if contentType == "application/octet-stream" {
				contentType = "image/jpeg"
			}
			out = append(out, model.AndroidSyncAsset{
				AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "post_media", i),
				AssetKind:          "post_media",
				OwnerID:            video.VideoID,
				OwnerKind:          ownerKind,
				Bucket:             bucket,
				ServerURL:          fmt.Sprintf("/api/media/slide/%s/%d", video.VideoID, i),
				ContentType:        contentType,
				State:              "ready",
				RequiredReason:     "retention",
				EffectiveRecencyMs: recency,
			})
		}
		if s.androidSyncAudioPath(video.VideoID) != "" {
			out = append(out, model.AndroidSyncAsset{
				AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "post_audio", 0),
				AssetKind:          "post_audio",
				OwnerID:            video.VideoID,
				OwnerKind:          ownerKind,
				Bucket:             bucket,
				ServerURL:          "/api/media/audio/" + video.VideoID,
				ContentType:        "audio/mpeg",
				State:              "ready",
				RequiredReason:     "retention",
				EffectiveRecencyMs: recency,
			})
		}
		return out
	}
	out = append(out,
		model.AndroidSyncAsset{
			AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "video_stream", 0),
			AssetKind:          "video_stream",
			OwnerID:            video.VideoID,
			OwnerKind:          ownerKind,
			Bucket:             bucket,
			ServerURL:          "/api/media/stream/" + video.VideoID,
			ContentType:        "video/mp4",
			State:              "ready",
			RequiredReason:     "retention",
			EffectiveRecencyMs: recency,
		},
	)
	if s.androidSyncSubtitlePath(video.VideoID) != "" {
		isAuto, audioLang := s.androidSyncSubtitleMetadata(video)
		out = append(out, model.AndroidSyncAsset{
			AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "subtitle", 0),
			AssetKind:          "subtitle",
			OwnerID:            video.VideoID,
			OwnerKind:          ownerKind,
			Bucket:             bucket,
			ServerURL:          "/api/media/subtitle/" + video.VideoID,
			ContentType:        "text/vtt",
			State:              "ready",
			RequiredReason:     "retention",
			IsAuto:             &isAuto,
			AudioLanguage:      audioLang,
			EffectiveRecencyMs: recency,
		})
	}
	if s.ensureAndroidSyncPreviewTrackJSON(video) && s.androidSyncPreviewPath(video.VideoID, "track.json") != "" {
		out = append(out, model.AndroidSyncAsset{
			AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "preview_track_json", 0),
			AssetKind:          "preview_track_json",
			OwnerID:            video.VideoID,
			OwnerKind:          ownerKind,
			Bucket:             bucket,
			ServerURL:          "/api/media/preview-track-json/" + video.VideoID,
			ContentType:        "application/json",
			State:              "ready",
			RequiredReason:     "retention",
			EffectiveRecencyMs: recency,
		})
	}
	if s.androidSyncPreviewPath(video.VideoID, "sprite.jpg") != "" {
		out = append(out, model.AndroidSyncAsset{
			AssetID:            db.BuildManifestAssetID(platform, ownerKind, video.VideoID, "preview_sprite", 0),
			AssetKind:          "preview_sprite",
			OwnerID:            video.VideoID,
			OwnerKind:          ownerKind,
			Bucket:             bucket,
			ServerURL:          "/api/media/preview-sprite/" + video.VideoID,
			ContentType:        "image/jpeg",
			State:              "ready",
			RequiredReason:     "retention",
			EffectiveRecencyMs: recency,
		})
	}
	return out
}

func (s *Server) androidSyncVideoThumbnailAsset(video model.Video, reason string) model.AndroidSyncAsset {
	platform := androidSyncPlatformFromChannelID(video.ChannelID)
	recency := int64(0)
	if video.PublishedAt != nil {
		recency = video.PublishedAt.UnixMilli()
	}
	return model.AndroidSyncAsset{
		AssetID:            db.BuildManifestAssetID(platform, androidSyncVideoOwnerKind(platform), video.VideoID, "post_thumbnail", 0),
		AssetKind:          "post_thumbnail",
		OwnerID:            video.VideoID,
		OwnerKind:          androidSyncVideoOwnerKind(platform),
		Bucket:             androidSyncVideoBucket(platform),
		ServerURL:          "/api/media/thumbnail/" + video.VideoID,
		ContentType:        "image/jpeg",
		State:              "ready",
		RequiredReason:     reason,
		EffectiveRecencyMs: recency,
	}
}

func (s *Server) androidSyncVideoDearrowThumbnailAsset(video model.Video, reason string) (model.AndroidSyncAsset, bool) {
	if s.androidSyncVideoDearrowThumbnailPath(video.VideoID) == "" {
		return model.AndroidSyncAsset{}, false
	}
	platform := androidSyncPlatformFromChannelID(video.ChannelID)
	recency := int64(0)
	if video.PublishedAt != nil {
		recency = video.PublishedAt.UnixMilli()
	}
	return model.AndroidSyncAsset{
		AssetID:            db.BuildManifestAssetID(platform, androidSyncVideoOwnerKind(platform), video.VideoID, "dearrow_thumbnail", 0),
		AssetKind:          "dearrow_thumbnail",
		OwnerID:            video.VideoID,
		OwnerKind:          androidSyncVideoOwnerKind(platform),
		Bucket:             androidSyncVideoBucket(platform),
		ServerURL:          "/api/media/thumbnail/" + video.VideoID + "?da=1",
		ContentType:        "image/jpeg",
		State:              "ready",
		RequiredReason:     reason,
		EffectiveRecencyMs: recency,
	}, true
}

func androidSyncVideoIsStillMedia(video model.Video) bool {
	kind := strings.ToLower(strings.TrimSpace(video.MediaKind))
	return kind == "image" || kind == "slideshow"
}

func (s *Server) androidSyncChannelFallbackAssets(channelID string, reason string) []model.AndroidSyncAsset {
	if strings.TrimSpace(reason) == "" {
		reason = "retention"
	}
	platform := androidSyncPlatformFromChannelID(channelID)
	out := []model.AndroidSyncAsset{{
		AssetID:        db.BuildManifestAssetID(platform, "channel", channelID, "avatar", 0),
		AssetKind:      "avatar",
		OwnerID:        channelID,
		OwnerKind:      "channel",
		Bucket:         "avatars",
		ServerURL:      "/api/media/avatar/" + channelID,
		ContentType:    "image/jpeg",
		State:          "ready",
		RequiredReason: reason,
	}}
	profile, _ := s.db.GetChannelProfile(channelID)
	hasProfileBanner := profile != nil && strings.TrimSpace(profile.BannerURL) != ""
	if hasProfileBanner || s.resolveBannerPath(channelID) != "" {
		out = append(out, model.AndroidSyncAsset{
			AssetID:        db.BuildManifestAssetID(platform, "channel", channelID, "banner", 0),
			AssetKind:      "banner",
			OwnerID:        channelID,
			OwnerKind:      "channel",
			Bucket:         "banners",
			ServerURL:      "/api/media/banner/" + channelID,
			ContentType:    "image/jpeg",
			State:          "ready",
			RequiredReason: reason,
		})
	}
	return out
}

func (s *Server) finalizeAndroidSyncAsset(asset model.AndroidSyncAsset) model.AndroidSyncAsset {
	path := s.androidSyncAssetPath(asset)
	if path == "" {
		asset.State = "server_missing"
		asset.SizeBytes = 0
		asset.SHA256 = ""
		return asset
	}
	if asset.ContentType == "" {
		asset.ContentType = androidSyncContentType(path)
	}
	size, sum, err := hashFile(path)
	if err != nil {
		asset.State = "server_missing"
		asset.SizeBytes = 0
		asset.SHA256 = ""
		return asset
	}
	asset.State = "ready"
	asset.SizeBytes = size
	asset.SHA256 = sum
	return asset
}

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Server) androidSyncAssetPath(asset model.AndroidSyncAsset) string {
	switch asset.AssetKind {
	case "avatar":
		return s.resolveAvatarPath(asset.OwnerID)
	case "banner":
		return s.resolveBannerPath(asset.OwnerID)
	case "post_thumbnail":
		if path := s.androidSyncVideoThumbnailPath(asset.OwnerID); path != "" {
			return path
		}
		return s.resolveThumb(asset.OwnerID)
	case "dearrow_thumbnail":
		return s.androidSyncVideoDearrowThumbnailPath(asset.OwnerID)
	case "video_stream":
		return s.androidSyncStreamPath(asset.OwnerID)
	case "subtitle":
		return s.androidSyncSubtitlePath(asset.OwnerID)
	case "post_audio":
		return s.androidSyncAudioPath(asset.OwnerID)
	case "preview_track_json":
		return s.androidSyncPreviewPath(asset.OwnerID, "track.json")
	case "preview_sprite":
		return s.androidSyncPreviewPath(asset.OwnerID, "sprite.jpg")
	case "post_media":
		if strings.Contains(asset.ServerURL, "/api/media/stream/") {
			return s.androidSyncStreamPath(asset.OwnerID)
		}
		index := androidSyncSlideIndex(asset.ServerURL)
		if path := s.androidSyncSlidePath(asset.OwnerID, index); path != "" {
			return path
		}
		return s.findFeedMediaFile(asset.OwnerID, index)
	default:
		return ""
	}
}

func (s *Server) ensureAndroidSyncPreviewTrackJSON(video model.Video) bool {
	if s.androidSyncPreviewPath(video.VideoID, "track.json") != "" {
		return true
	}
	outDir := resolveDataPath(s.cfg.DataDir, filepath.Join("thumbnails", "previews", video.VideoID))
	if outDir == "" || s.androidSyncPreviewPath(video.VideoID, "sprite.jpg") == "" {
		return false
	}
	if err := worker.EnsurePreviewTrackJSON(outDir, worker.PreviewRequest{
		VideoID:  video.VideoID,
		Duration: float64(video.Duration),
	}); err != nil {
		slog.Warn("android_sync_preview_track_json_omitted", "video_id", video.VideoID, "err", err)
		return false
	}
	return s.androidSyncPreviewPath(video.VideoID, "track.json") != ""
}

func (s *Server) androidSyncPreviewPath(videoID, name string) string {
	path := resolveDataPath(s.cfg.DataDir, filepath.Join("thumbnails", "previews", videoID, name))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func (s *Server) androidSyncVideoThumbnailPath(videoID string) string {
	video, _ := s.db.GetVideo(videoID)
	if video == nil || strings.TrimSpace(video.ThumbnailPath) == "" {
		return ""
	}
	path := resolveDataPath(s.cfg.DataDir, video.ThumbnailPath)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func (s *Server) androidSyncVideoDearrowThumbnailPath(videoID string) string {
	video, _ := s.db.GetVideo(videoID)
	if video == nil || video.DearrowThumbPath == nil || strings.TrimSpace(*video.DearrowThumbPath) == "" {
		return ""
	}
	path := resolveDataPath(s.cfg.DataDir, strings.TrimSpace(*video.DearrowThumbPath))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func (s *Server) androidSyncStreamPath(ownerID string) string {
	video, _ := s.db.GetVideo(ownerID)
	if video != nil && video.FilePath != "" {
		path := resolveDataPath(s.cfg.DataDir, video.FilePath)
		if _, err := os.Stat(path); err == nil && androidSyncPathLooksVideo(path) {
			return path
		}
	}
	return s.findFeedMediaVideoFile(ownerID)
}

func androidSyncPathLooksVideo(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".webm", ".mkv", ".mov", ".m4v":
		return true
	default:
		return false
	}
}

func (s *Server) androidSyncSlidePath(videoID string, index int) string {
	video, _ := s.db.GetVideo(videoID)
	if video != nil {
		if path := androidSyncSlidePathFromVideo(s.cfg.DataDir, *video, index); path != "" {
			return path
		}
	}
	if path := s.findFeedMediaFile(videoID, index); path != "" {
		if fi, err := os.Stat(path); err == nil && fi.Size() >= 100 {
			return path
		}
	}
	return ""
}

func androidSyncSlidePathFromVideo(dataDir string, video model.Video, index int) string {
	if index < 0 {
		return ""
	}
	if meta := video.ParseMetadata(); meta != nil && index < len(meta.Slides) {
		if slide := meta.SlideAsMap(index); slide != nil {
			if pathVal, ok := slide["path"].(string); ok && pathVal != "" {
				absSlide := resolveDataPath(dataDir, pathVal)
				if _, err := os.Stat(absSlide); err == nil {
					return absSlide
				}
			}
			if urlVal, ok := slide["url"].(string); ok && urlVal != "" && video.FilePath != "" {
				absVideo := resolveDataPath(dataDir, video.FilePath)
				candidate := filepath.Join(filepath.Dir(absVideo), urlVal)
				if _, err := os.Stat(candidate); err == nil {
					return candidate
				}
			}
		}
	}
	if video.FilePath == "" {
		return ""
	}
	dir := filepath.Dir(resolveDataPath(dataDir, video.FilePath))
	fileIndex := index + 1
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", video.VideoID, fileIndex, ext))
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if index == 0 && androidSyncMediaPathIsImage(video.FilePath) {
		path := resolveDataPath(dataDir, video.FilePath)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (s *Server) androidSyncAudioPath(videoID string) string {
	video, _ := s.db.GetVideo(videoID)
	audioExts := []string{".mp3", ".m4a", ".ogg", ".aac"}
	if video != nil && video.FilePath != "" {
		dir := filepath.Dir(resolveDataPath(s.cfg.DataDir, video.FilePath))
		stem := strings.TrimSuffix(filepath.Base(video.FilePath), filepath.Ext(video.FilePath))
		for _, ext := range audioExts {
			for _, candidateStem := range []string{videoID, videoID + "_0", stem, stem + "_0"} {
				candidate := filepath.Join(dir, candidateStem+ext)
				if _, err := os.Stat(candidate); err == nil {
					return candidate
				}
			}
		}
	}
	return s.findFeedMediaAudioFile(videoID)
}

func (s *Server) androidSyncSubtitlePath(videoID string) string {
	video, _ := s.db.GetVideo(videoID)
	if video == nil || video.FilePath == "" {
		return ""
	}
	absFilePath := resolveDataPath(s.cfg.DataDir, video.FilePath)
	videoBase := strings.TrimSuffix(absFilePath, filepath.Ext(absFilePath))
	for _, suffix := range []string{".en.vtt", ".vtt"} {
		path := videoBase + suffix
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	dir := filepath.Dir(absFilePath)
	stem := filepath.Base(videoBase)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stem) && strings.HasSuffix(entry.Name(), ".vtt") {
			path := filepath.Join(dir, entry.Name())
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return ""
}

func (s *Server) androidSyncSubtitleMetadata(video model.Video) (bool, string) {
	subtitlePath := s.androidSyncSubtitlePath(video.VideoID)
	if subtitlePath == "" || video.FilePath == "" {
		return true, ""
	}
	absFilePath := resolveDataPath(s.cfg.DataDir, video.FilePath)
	stem := strings.TrimSuffix(filepath.Base(absFilePath), filepath.Ext(absFilePath))
	lang := subtitlemeta.TrackLang(stem, filepath.Base(subtitlePath))
	infoPath := filepath.Join(filepath.Dir(absFilePath), stem+".info.json")
	return subtitlemeta.IsAuto(infoPath, lang), subtitlemeta.Language(infoPath)
}

func androidSyncSlideIndex(serverURL string) int {
	parts := strings.Split(strings.Trim(serverURL, "/"), "/")
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func androidSyncAssetPriority(asset model.AndroidSyncAsset) int {
	if asset.State == "server_missing" {
		return 99
	}
	isBulkProfile := strings.EqualFold(strings.TrimSpace(asset.RequiredReason), "profile")
	switch asset.AssetKind {
	case "post_thumbnail":
		return 0
	case "banner":
		if isBulkProfile {
			return 8
		}
		return 1
	case "avatar":
		if isBulkProfile {
			return 9
		}
		return 2
	case "post_media":
		return 3
	case "post_audio":
		return 4
	case "video_stream":
		return 5
	case "subtitle":
		return 6
	case "dearrow_thumbnail":
		return 7
	case "preview_track_json":
		return 10
	case "preview_sprite":
		return 11
	default:
		return 12
	}
}

func androidSyncPlatformFromChannelID(id string) string {
	switch {
	case strings.HasPrefix(id, "twitter_"):
		return "twitter"
	case strings.HasPrefix(id, "youtube_"):
		return "youtube"
	case strings.HasPrefix(id, "tiktok_"):
		return "tiktok"
	case strings.HasPrefix(id, "instagram_"):
		return "instagram"
	default:
		if idx := strings.Index(id, "_"); idx > 0 {
			return id[:idx]
		}
		return "youtube"
	}
}

func androidSyncVideoOwnerKind(platform string) string {
	switch platform {
	case "twitter":
		return "tweet"
	case "tiktok":
		return "tiktok_video"
	case "instagram":
		return "instagram_reel"
	default:
		return "youtube_video"
	}
}

func androidSyncVideoBucket(platform string) string {
	switch platform {
	case "youtube":
		return "youtube_videos"
	case "twitter":
		return "twitter_media"
	default:
		return "shorts_videos"
	}
}

func androidSyncContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".image":
		return detectImageContentType(path)
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".mov":
		return "video/quicktime"
	case ".m4v":
		return "video/x-m4v"
	case ".vtt":
		return "text/vtt"
	default:
		return "application/octet-stream"
	}
}
