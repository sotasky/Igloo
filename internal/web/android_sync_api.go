package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

const (
	androidSyncAssetServeLimit     = 4
	androidSyncAssetRetryAfterSecs = 30
	androidSyncFeedRankMaxRows     = 5000
	youtubeCommentsCap             = 50
)

type androidSyncFeedPayload struct {
	Item androidSyncFeedItem `json:"item"`
}

type androidSyncFeedItem struct {
	TweetID           string `json:"tweet_id"`
	SourceChannelID   string `json:"source_channel_id"`
	ChannelID         string `json:"channel_id"`
	BodyText          string `json:"body_text"`
	Lang              string `json:"lang"`
	IsRetweet         bool   `json:"is_retweet"`
	ReposterChannelID string `json:"reposter_channel_id"`
	QuoteTweetID      string `json:"quote_tweet_id"`
	QuoteChannelID    string `json:"quote_channel_id"`
	QuoteBodyText     string `json:"quote_body_text"`
	QuoteLang         string `json:"quote_lang"`
	QuoteMediaJSON    string `json:"quote_media_json"`
	QuotePublishedAt  int64  `json:"quote_published_at"`
	QuoteCanonicalURL string `json:"quote_canonical_url"`
	MediaJSON         string `json:"media_json"`
	Views             int64  `json:"views"`
	Likes             int64  `json:"likes"`
	Retweets          int64  `json:"retweets"`
	CanonicalURL      string `json:"canonical_url"`
	CanonicalTweetID  string `json:"canonical_tweet_id"`
	ReplyChannelID    string `json:"reply_channel_id"`
	ReplyToStatus     string `json:"reply_to_status"`
	IsReply           bool   `json:"is_reply"`
	IsGhost           bool   `json:"is_ghost"`
	ContentHash       string `json:"content_hash"`
	BodyTranslation   string `json:"body_translation"`
	BodySourceLang    string `json:"body_source_lang"`
	QuoteTranslation  string `json:"quote_translation"`
	QuoteSourceLang   string `json:"quote_source_lang"`
	PublishedAt       int64  `json:"published_at"`
}

type androidSyncVideoPayload struct {
	Item                 androidSyncVideoItem           `json:"item"`
	Comments             []androidSyncComment           `json:"comments"`
	SponsorBlockSegments []db.SponsorBlockSegment       `json:"sponsorblock_segments"`
	SponsorBlockChecked  *androidSyncSponsorBlockCheck  `json:"sponsorblock_checked"`
	RepostSources        []androidSyncVideoRepostSource `json:"repost_sources"`
}

type androidSyncVideoRepostSource struct {
	ReposterChannelID string `json:"reposter_channel_id"`
	RepostedAtMs      int64  `json:"reposted_at_ms"`
	FirstSeenAtMs     int64  `json:"first_seen_at_ms"`
	UpdatedAtMs       int64  `json:"updated_at_ms"`
}

type androidSyncComment struct {
	CommentID   string `json:"id"`
	ParentID    string `json:"parent"`
	AuthorName  string `json:"author"`
	AuthorID    string `json:"author_id"`
	Text        string `json:"text"`
	LikeCount   int    `json:"like_count"`
	PublishedAt int64  `json:"published_at"`
}

type androidSyncVideoItem struct {
	VideoID            string  `json:"video_id"`
	ChannelID          string  `json:"channel_id"`
	OwnerKind          string  `json:"owner_kind"`
	Title              string  `json:"title"`
	Description        string  `json:"description"`
	Duration           int     `json:"duration"`
	PublishedAt        int64   `json:"published_at"`
	MediaKind          string  `json:"media_kind"`
	SlideCount         int     `json:"slide_count"`
	SourceKind         string  `json:"source_kind"`
	MetadataJSON       string  `json:"metadata_json"`
	CanonicalURL       string  `json:"canonical_url"`
	DearrowTitle       *string `json:"dearrow_title"`
	DearrowTitleCasual *string `json:"dearrow_title_casual"`
}

type androidSyncSponsorBlockCheck struct {
	CheckedAtMs     int64  `json:"checked_at_ms"`
	VideoAgeAtCheck string `json:"video_age_at_check"`
}

type androidSyncChannelPayload struct {
	Channel *androidSyncChannel        `json:"channel"`
	Profile *androidSyncChannelProfile `json:"profile"`
}

type androidSyncChannel struct {
	ChannelID string `json:"channel_id"`
	SourceID  string `json:"source_id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Platform  string `json:"platform"`
}

type androidSyncChannelProfile struct {
	ChannelID    string `json:"channel_id"`
	Platform     string `json:"platform"`
	Handle       string `json:"handle"`
	DisplayName  string `json:"display_name"`
	Bio          string `json:"bio"`
	Website      string `json:"website"`
	Followers    int    `json:"followers"`
	Following    int    `json:"following"`
	Verified     bool   `json:"verified"`
	VerifiedType string `json:"verified_type"`
	Protected    bool   `json:"protected"`
}

type androidSyncRetweetSource struct {
	ContentHash        string `json:"content_hash"`
	RetweeterChannelID string `json:"retweeter_channel_id"`
	TweetID            string `json:"tweet_id"`
	PublishedAt        int64  `json:"published_at"`
}

type androidSyncHealthRequest struct {
	Cursor       string                     `json:"cursor"`
	ReportedAtMs int64                      `json:"reported_at_ms"`
	Retention    androidSyncHealthRetention `json:"retention"`
	Counts       androidSyncHealthCounts    `json:"counts"`
	Bytes        androidSyncHealthBytes     `json:"bytes"`
}

type androidSyncHealthRetention struct {
	FeedDays    int `json:"feed_days"`
	YoutubeDays int `json:"youtube_days"`
	MomentsDays int `json:"moments_days"`
	StoryHours  int `json:"story_hours"`
}

type androidSyncHealthCounts struct {
	Total    int `json:"total"`
	Verified int `json:"verified"`
	Pending  int `json:"pending"`
	Missing  int `json:"missing"`
}

type androidSyncHealthBytes struct {
	Verified int64 `json:"verified"`
}

func (s *Server) registerAndroidSyncAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/android/sync/bootstrap", s.handleAndroidSyncBootstrap)
	mux.HandleFunc("GET /api/android/sync/changes", s.handleAndroidSyncChanges)
	mux.HandleFunc("GET /api/android/sync/assets/{assetID}/file", s.handleAndroidSyncAssetFile)
	mux.HandleFunc("POST /api/android/sync/health", s.handleAndroidSyncHealth)
}

func (s *Server) tryAcquireAndroidSyncAssetServeSlot() bool {
	s.androidSyncAssetServeSemOnce.Do(func() {
		s.androidSyncAssetServeSemaphore = make(chan struct{}, androidSyncAssetServeLimit)
	})
	select {
	case s.androidSyncAssetServeSemaphore <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseAndroidSyncAssetServeSlot() {
	select {
	case <-s.androidSyncAssetServeSemaphore:
	default:
	}
}

func (s *Server) handleAndroidSyncHealth(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var body androidSyncHealthRequest
	if err := decodeJSON(w, r, &body); err != nil {
		if requestBodyTooLarge(err) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", requestBodyTooLargeMessage)
			return
		}
		writeJSONError(w, http.StatusBadRequest, "bad_json", "invalid health payload")
		return
	}
	body.Cursor = strings.TrimSpace(body.Cursor)
	cursor, cursorErr := decodeAndroidSyncCursor(body.Cursor)
	if cursorErr != nil || cursor.Mode != "changes" || cursor.Version != androidSyncModelVersion || cursor.Revision < 0 {
		writeAndroidSyncResetRequired(w)
		return
	}
	if body.Counts.Total < 0 || body.Counts.Verified < 0 || body.Counts.Pending < 0 ||
		body.Counts.Missing < 0 || body.Bytes.Verified < 0 || body.Retention.FeedDays < 0 ||
		body.Retention.YoutubeDays < 0 || body.Retention.MomentsDays < 0 || body.Retention.StoryHours < 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "health values must be non-negative")
		return
	}
	if !db.IsValidRetentionDays(body.Retention.FeedDays) ||
		!db.IsValidRetentionDays(body.Retention.YoutubeDays) ||
		!db.IsValidRetentionDays(body.Retention.MomentsDays) {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "invalid retention days")
		return
	}
	if body.Retention.StoryHours > 0 {
		body.Retention.StoryHours = db.NormalizeStoriesWindowHours(body.Retention.StoryHours)
	}
	retention := db.AndroidRetentionSettings{
		FeedDays: body.Retention.FeedDays, YoutubeDays: body.Retention.YoutubeDays,
		MomentsDays: body.Retention.MomentsDays, StoryHours: body.Retention.StoryHours,
	}
	clock, err := s.db.GetAndroidSyncClock()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "db_error", "health validation failed")
		return
	}
	if cursor.Epoch != clock.Epoch || cursor.Revision > clock.Revision || cursor.Retention != androidSyncRetentionHash(retention) {
		writeAndroidSyncResetRequired(w)
		return
	}
	reportedAt := body.ReportedAtMs
	if reportedAt <= 0 {
		reportedAt = time.Now().UnixMilli()
	}
	raw, err := json.Marshal(body)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "health_failed", "health report encoding failed")
		return
	}
	if err := s.db.RecordAndroidSyncHealth(
		body.Cursor,
		reportedAt,
		raw,
		body.Counts.Verified,
		body.Counts.Pending,
		body.Counts.Missing,
		body.Counts.Total,
		body.Bytes.Verified,
	); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "db_error", "health write failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) androidSyncRetentionFallback() db.AndroidRetentionSettings {
	if report, err := s.db.GetLatestAndroidSyncHealthReport(); err == nil && report != nil && report.HasRetention {
		return report.Retention
	}
	return db.AndroidRetentionSettings{FeedDays: 7, YoutubeDays: 7, MomentsDays: 7, StoryHours: 48}
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
		settings.StoryHours = n
		if n > 0 {
			settings.StoryHours = db.NormalizeStoriesWindowHours(n)
		}
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

func androidSyncFeedItemFromModel(item model.FeedItem) androidSyncFeedItem {
	out := androidSyncFeedItem{
		TweetID:           item.TweetID,
		SourceChannelID:   item.SourceChannelID,
		ChannelID:         item.ChannelID,
		BodyText:          item.BodyText,
		Lang:              item.Lang,
		IsRetweet:         item.IsRetweet,
		ReposterChannelID: item.ReposterChannelID,
		QuoteTweetID:      item.QuoteTweetID,
		QuoteChannelID:    item.QuoteChannelID,
		QuoteBodyText:     item.QuoteBodyText,
		QuoteLang:         item.QuoteLang,
		QuoteMediaJSON:    item.QuoteMediaJSON,
		QuoteCanonicalURL: androidSyncQuoteCanonicalURL(item),
		MediaJSON:         item.MediaJSON,
		Views:             item.Views,
		Likes:             item.Likes,
		Retweets:          item.Retweets,
		CanonicalURL:      androidSyncFeedCanonicalURL(item),
		CanonicalTweetID:  item.CanonicalTweetID,
		ReplyChannelID:    item.ReplyChannelID,
		ReplyToStatus:     item.ReplyToStatus,
		IsReply:           item.IsReply,
		IsGhost:           item.IsGhost,
		ContentHash:       item.ContentHash,
		BodyTranslation:   item.BodyTranslation,
		BodySourceLang:    item.BodySourceLang,
		QuoteTranslation:  item.QuoteTranslation,
		QuoteSourceLang:   item.QuoteSourceLang,
	}
	if item.QuotePublishedAt != nil {
		out.QuotePublishedAt = item.QuotePublishedAt.UnixMilli()
	}
	if item.PublishedAt != nil {
		out.PublishedAt = item.PublishedAt.UnixMilli()
	}
	return out
}

func androidSyncVideoItemFromProjection(projection db.AndroidSyncVideoProjection) androidSyncVideoItem {
	video := projection.Video
	out := androidSyncVideoItem{
		VideoID:            video.VideoID,
		ChannelID:          video.ChannelID,
		OwnerKind:          video.OwnerKind,
		Title:              video.Title,
		Description:        video.Description,
		Duration:           video.Duration,
		MediaKind:          video.MediaKind,
		SlideCount:         video.MediaSlideCount,
		SourceKind:         video.SourceKind,
		MetadataJSON:       video.MetadataJSON,
		CanonicalURL:       androidSyncCanonicalVideoURL(video),
		DearrowTitle:       video.DearrowTitle,
		DearrowTitleCasual: video.DearrowTitleCasual,
	}
	if video.PublishedAt != nil {
		out.PublishedAt = video.PublishedAt.UnixMilli()
	}
	return out
}

func androidSyncCommentFromModel(comment model.Comment) androidSyncComment {
	out := androidSyncComment{
		CommentID:   comment.CommentID,
		ParentID:    comment.ParentID,
		AuthorName:  comment.AuthorName,
		AuthorID:    comment.AuthorID,
		Text:        comment.Text,
		LikeCount:   comment.LikeCount,
		PublishedAt: comment.PublishedAtMs,
	}
	return out
}

func androidSyncChannelPayloadFromProjection(projection db.AndroidSyncChannelProjection) androidSyncChannelPayload {
	payload := androidSyncChannelPayload{}
	if channel := projection.Channel; channel != nil {
		payload.Channel = &androidSyncChannel{
			ChannelID: channel.ChannelID,
			SourceID:  channel.SourceID,
			Name:      channel.Name,
			URL:       channel.URL,
			Platform:  channel.Platform,
		}
	}
	if profile := projection.Profile; profile != nil {
		payload.Profile = &androidSyncChannelProfile{
			ChannelID:    profile.ChannelID,
			Platform:     profile.Platform,
			Handle:       profile.Handle,
			DisplayName:  profile.DisplayName,
			Bio:          profile.Bio,
			Website:      profile.Website,
			Followers:    profile.Followers,
			Following:    profile.Following,
			Verified:     profile.Verified,
			VerifiedType: profile.VerifiedType,
			Protected:    profile.Protected,
		}
	}
	return payload
}

func androidSyncCanonicalVideoURL(video model.Video) string {
	platform := androidSyncPlatformForOwnerKind(video.OwnerKind)
	if platform == "" {
		return ""
	}
	rawID := androidSyncRawPlatformID(video.VideoID, platform)
	if rawID == "" {
		return ""
	}
	switch platform {
	case "youtube":
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(rawID)
	case "tiktok":
		handle := strings.TrimPrefix(strings.TrimSpace(video.ChannelID), "tiktok_")
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
		return androidSyncXStatusURL(rawID)
	default:
		return ""
	}
}

func androidSyncFeedCanonicalURL(item model.FeedItem) string {
	if raw := strings.TrimSpace(item.CanonicalURL); strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return raw
	}
	id := item.CanonicalTweetID
	if strings.TrimSpace(id) == "" {
		id = item.TweetID
	}
	return androidSyncXStatusURL(id)
}

func androidSyncQuoteCanonicalURL(item model.FeedItem) string {
	if raw := strings.TrimSpace(item.QuoteCanonicalURL); strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return raw
	}
	return androidSyncXStatusURL(item.QuoteTweetID)
}

func androidSyncXStatusURL(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "https://x.com/i/status/" + url.PathEscape(id)
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

func (s *Server) buildAndroidSyncAssets(database *db.DB, sets db.AndroidSyncDesiredSets) ([]model.AndroidSyncAsset, map[string]int, error) {
	counts := map[string]int{}
	phaseStart := time.Now()
	inventoryRows, err := database.ListAndroidSyncAssetInventoryRows(sets)
	if err != nil {
		return nil, counts, err
	}
	commentRows, err := database.ListAndroidSyncCommentAuthorAssets(sets.SortedVideos(), youtubeCommentsCap)
	if err != nil {
		return nil, counts, err
	}
	out := make([]model.AndroidSyncAsset, 0, len(inventoryRows)+len(commentRows))
	assetIndex := make(map[string]int, len(inventoryRows)+len(commentRows))
	appendAsset := func(row db.Asset, effectiveRecencyMs int64) {
		asset := s.androidSyncAssetFromInventory(row)
		if effectiveRecencyMs <= 0 {
			effectiveRecencyMs = row.UpdatedAtMs
		}
		asset.EffectiveRecencyMs = effectiveRecencyMs
		if index, ok := assetIndex[asset.AssetID]; ok {
			if effectiveRecencyMs > out[index].EffectiveRecencyMs {
				out[index].EffectiveRecencyMs = effectiveRecencyMs
			}
			return
		}
		assetIndex[asset.AssetID] = len(out)
		out = append(out, asset)
		counts[asset.AssetKind]++
	}
	for _, row := range inventoryRows {
		if !androidSyncInventoryAssetDesired(row, sets) {
			continue
		}
		appendAsset(row, 0)
	}
	for _, selected := range commentRows {
		appendAsset(selected.Asset, selected.RecencyMs)
	}
	slog.Info(
		"android_sync_asset_phase",
		"phase", "asset_inventory",
		"rows", len(inventoryRows),
		"assets", len(out),
		"duration_ms", time.Since(phaseStart).Milliseconds(),
	)
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

func androidSyncInventoryAssetDesired(row db.Asset, sets db.AndroidSyncDesiredSets) bool {
	switch row.AssetKind {
	case "avatar", "banner":
		_, ok := sets.Channels[row.OwnerID]
		return ok
	case "video_stream":
		_, ok := sets.MediaVideos[row.OwnerID]
		return ok
	case "post_media", "post_audio":
		if _, ok := sets.MediaVideos[row.OwnerID]; ok {
			return true
		}
		return sets.HasTweetAssetOwner(row.OwnerID)
	case "subtitle", "preview_track_json", "preview_sprite", "post_thumbnail", "dearrow_thumbnail":
		if _, ok := sets.Videos[row.OwnerID]; ok {
			return true
		}
		if _, ok := sets.MediaVideos[row.OwnerID]; ok {
			return true
		}
		return sets.HasTweetAssetOwner(row.OwnerID)
	default:
		if sets.HasTweetAssetOwner(row.OwnerID) {
			return true
		}
		_, ok := sets.Videos[row.OwnerID]
		return ok
	}
}

func (s *Server) androidSyncAssetFromInventory(row db.Asset) model.AndroidSyncAsset {
	ready := row.State == db.AssetStateReady
	state := "server_missing"
	contentType := ""
	sizeBytes := int64(0)
	sha256 := ""
	if ready {
		state = "ready"
		contentType = row.ContentType
		if contentType == "application/octet-stream" {
			contentType = ""
		}
		sizeBytes = row.SizeBytes
		sha256 = row.SHA256
	}
	asset := model.AndroidSyncAsset{
		AssetID:            row.AssetID,
		AssetKind:          row.AssetKind,
		MediaIndex:         row.MediaIndex,
		OwnerID:            row.OwnerID,
		OwnerKind:          row.OwnerKind,
		Bucket:             androidSyncInventoryBucket(row),
		ContentType:        contentType,
		SizeBytes:          sizeBytes,
		SHA256:             sha256,
		Revision:           row.Revision,
		State:              state,
		IsAuto:             row.IsAuto,
		EffectiveRecencyMs: 0,
	}
	return asset
}

func androidSyncInventoryBucket(row db.Asset) string {
	switch row.AssetKind {
	case "avatar":
		return "avatars"
	case "banner":
		return "banners"
	}
	return androidSyncVideoBucket(androidSyncPlatformForOwnerKind(row.OwnerKind))
}

func androidSyncAssetPriority(asset model.AndroidSyncAsset) int {
	switch asset.AssetKind {
	case "avatar":
		return 0
	case "post_thumbnail":
		return 1
	case "dearrow_thumbnail":
		return 2
	case "banner":
		return 3
	case "post_media":
		return 4
	case "post_audio":
		return 5
	case "video_stream":
		return 6
	case "subtitle":
		return 7
	case "preview_track_json":
		return 8
	case "preview_sprite":
		return 9
	default:
		return 10
	}
}

func androidSyncPlatformForOwnerKind(ownerKind string) string {
	switch ownerKind {
	case "tweet":
		return "twitter"
	case "youtube_video":
		return "youtube"
	case "tiktok_video":
		return "tiktok"
	case "instagram_reel":
		return "instagram"
	default:
		return ""
	}
}

func androidSyncVideoBucket(platform string) string {
	switch platform {
	case "youtube":
		return "youtube_videos"
	case "twitter":
		return "twitter_media"
	case "tiktok", "instagram":
		return "shorts_videos"
	default:
		return ""
	}
}
