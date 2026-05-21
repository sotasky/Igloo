package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/feed"
	"github.com/screwys/igloo/internal/model"
)

// Bundle-delta endpoints. Four content streams, one generic envelope shape:
//
//	{ bundles: [{primary_kind, primary, attachments}],
//	  next_marker, end_of_stream }
//
// Cursor is `max(sync_seq)` of the returned batch; client treats it as
// opaque. These are transport chunk sizes, not product limits. Keep them
// large enough that a cursor reset can drain to convergence in a reasonable
// number of requests.
const (
	feedDeltaCap     = 500
	shortsDeltaCap   = 200
	youtubeDeltaCap  = 80
	channelsDeltaCap = 200
)

// youtubeCommentsCap caps how many comments ride inline per YouTube
// bundle. Scrolling past the cap is a separate paginated endpoint.
const youtubeCommentsCap = 50

func (s *Server) registerDeltaAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/feed/delta", s.handleFeedDelta)
	mux.HandleFunc("GET /api/shorts/delta", s.handleShortsDelta)
	mux.HandleFunc("GET /api/videos/delta", s.handleVideosDelta)
	mux.HandleFunc("GET /api/channels/delta", s.handleChannelsDelta)
}

type deltaBundle struct {
	PrimaryKind string         `json:"primary_kind"`
	Primary     map[string]any `json:"primary"`
	Attachments map[string]any `json:"attachments,omitempty"`
}

type deltaResponse struct {
	Bundles     []deltaBundle `json:"bundles"`
	NextMarker  string        `json:"next_marker"`
	EndOfStream bool          `json:"end_of_stream"`
}

// parseSinceMarker decodes the opaque cursor. Empty/invalid → 0 (full
// re-sync). Server owns the encoding — clients echo what they got.
func parseSinceMarker(raw string) int64 {
	if raw == "" {
		return 0
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	return 0
}

func parseOptionalMillis(raw string) int64 {
	if raw == "" {
		return 0
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		return n
	}
	return 0
}

// writeDelta writes the bundle envelope through writeJSON, adding the
// per-stream sync_version + sync_stream envelope fields (#10).
func writeDelta(w http.ResponseWriter, resp deltaResponse, streamName string, syncVersion int64) {
	body := map[string]any{
		"bundles":       resp.Bundles,
		"next_marker":   resp.NextMarker,
		"end_of_stream": resp.EndOfStream,
	}
	if syncVersion > 0 {
		body["sync_version"] = syncVersion
		body["sync_stream"] = streamName
	}
	writeJSON(w, 200, body)
}

// ── Twitter feed delta (#6 + #8) ──────────────────────────────────────

func (s *Server) handleFeedDelta(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	since := parseSinceMarker(r.URL.Query().Get("since"))
	cutoffMs := parseOptionalMillis(r.URL.Query().Get("cutoff_ms"))

	items, maxSeq, err := s.db.ListFeedItemsForDelta(user.Username, since, feedDeltaCap, cutoffMs)
	if err != nil {
		slog.Error("ListFeedItemsForDelta", "since", since, "cutoff_ms", cutoffMs, "err", err)
		writeJSONError(w, 500, "db_error", "delta read failed")
		return
	}
	rawCount := len(items)
	items = feed.EnrichFeedItemsPreserveRows(s.db, items, user.Username)
	bookmarks, _ := s.db.GetBookmarksForVideoIDsRich(collectTweetIDs(items))
	mutedHandles, _ := s.db.GetMutedAccounts()
	mutedHandleSet := normalizeHandleSet(mutedHandles)
	subscribeURLs := make(map[string]string, len(items))
	for _, it := range items {
		if it.ChannelID == "" {
			continue
		}
		if _, ok := subscribeURLs[it.ChannelID]; !ok {
			subscribeURLs[it.ChannelID] = s.db.ResolveSubscribeURL(it.ChannelID)
		}
	}

	bundles := make([]deltaBundle, 0, len(items))
	for _, it := range items {
		primary := feedItemToBundlePrimary(it, bookmarks, subscribeURLs, mutedHandleSet)
		attachments := map[string]any{}
		attachments["feed_thread_context"] = feed.ThreadContextRows(s.db, it)
		if len(it.Retweeters) > 0 {
			attachments["retweet_sources"] = it.Retweeters
		}
		attachUserStateFromPrimary("feed_items", primary, attachments)
		bundles = append(bundles, deltaBundle{
			PrimaryKind: "feed_items",
			Primary:     primary,
			Attachments: attachments,
		})
	}

	writeDelta(w, deltaResponse{
		Bundles:     bundles,
		NextMarker:  formatMarker(maxSeq, since),
		EndOfStream: rawCount < feedDeltaCap,
	}, "feed", maxSeq)
}

// ── Shorts (TikTok + Instagram) delta (#6) ───────────────────────────

func (s *Server) handleShortsDelta(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	since := parseSinceMarker(r.URL.Query().Get("since"))

	videos, maxSeq, err := s.db.ListVideosForDelta([]string{"tiktok", "instagram"}, since, shortsDeltaCap)
	if err != nil {
		slog.Error("ListVideosForDelta shorts", "since", since, "err", err)
		writeJSONError(w, 500, "db_error", "delta read failed")
		return
	}
	videoIDs := make([]string, 0, len(videos))
	for _, v := range videos {
		videoIDs = append(videoIDs, v.VideoID)
	}
	bookmarks, _ := s.db.GetBookmarksForVideoIDsRich(videoIDs)
	repostsByVideo, _ := s.db.GetVideoRepostSourcesForVideoIDs(videoIDs)

	bundles := make([]deltaBundle, 0, len(videos))
	for _, v := range videos {
		primary := videoToBundlePrimary(v)
		if canonicalURL := s.androidSyncCanonicalVideoURL(v); canonicalURL != "" {
			primary["canonical_url"] = canonicalURL
		}
		if bi, ok := bookmarks[v.VideoID]; ok {
			applyBookmarkBundleFields(primary, bi)
		}
		attachments := map[string]any{}
		if isShortsChannelID(v.ChannelID) {
			reposts := repostsByVideo[v.VideoID]
			if reposts == nil {
				reposts = []model.VideoRepostSource{}
			}
			attachments["video_repost_sources"] = reposts
		}
		attachUserStateFromPrimary("videos", primary, attachments)
		bundles = append(bundles, deltaBundle{
			PrimaryKind: "videos",
			Primary:     primary,
			Attachments: attachments,
		})
	}

	writeDelta(w, deltaResponse{
		Bundles:     bundles,
		NextMarker:  formatMarker(maxSeq, since),
		EndOfStream: len(videos) < shortsDeltaCap,
	}, "shorts", maxSeq)
}

// ── YouTube delta (#6 + #7 inline attachments) ───────────────────────

func (s *Server) handleVideosDelta(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	since := parseSinceMarker(r.URL.Query().Get("since"))

	videos, maxSeq, err := s.db.ListVideosForDelta([]string{"youtube"}, since, youtubeDeltaCap)
	if err != nil {
		slog.Error("ListVideosForDelta youtube", "since", since, "err", err)
		writeJSONError(w, 500, "db_error", "delta read failed")
		return
	}
	videoIDs := make([]string, 0, len(videos))
	for _, v := range videos {
		videoIDs = append(videoIDs, v.VideoID)
	}
	bookmarks, _ := s.db.GetBookmarksForVideoIDsRich(videoIDs)

	bundles := make([]deltaBundle, 0, len(videos))
	for _, v := range videos {
		primary := videoToBundlePrimary(v)
		if canonicalURL := s.androidSyncCanonicalVideoURL(v); canonicalURL != "" {
			primary["canonical_url"] = canonicalURL
		}
		if bi, ok := bookmarks[v.VideoID]; ok {
			applyBookmarkBundleFields(primary, bi)
		}
		attachments := map[string]any{}

		// #7 — top-N comments inline. Paginated "show more" lives
		// outside the delta contract (deferred).
		comments, _ := s.db.GetComments(v.VideoID, youtubeCommentsCap)
		for i := range comments {
			comments[i].SetPublishedAtMs()
		}
		if len(comments) > 0 {
			attachments["video_comments"] = model.PresentComments(comments, model.CommentCreatorAuthorID(v.ChannelID))
		}
		segments, _ := s.db.GetSponsorBlockSegments(v.VideoID)
		if len(segments) > 0 {
			attachments["sponsorblock_segments"] = segments
		}
		if checked, _ := s.db.GetSponsorBlockChecked(v.VideoID); checked != nil {
			// Singleton marker row — distinguishes "checked, no
			// segments" from "haven't checked yet."
			attachments["sponsorblock_checked"] = map[string]any{
				"video_id":           checked.VideoID,
				"checked_at_ms":      checked.CheckedAtMs,
				"video_age_at_check": checked.VideoAgeAtCheck,
			}
		}
		attachUserStateFromPrimary("videos", primary, attachments)
		bundles = append(bundles, deltaBundle{
			PrimaryKind: "videos",
			Primary:     primary,
			Attachments: attachments,
		})
	}

	writeDelta(w, deltaResponse{
		Bundles:     bundles,
		NextMarker:  formatMarker(maxSeq, since),
		EndOfStream: len(videos) < youtubeDeltaCap,
	}, "youtube_videos", maxSeq)
}

// ── Channels delta (#6) ──────────────────────────────────────────────

func (s *Server) handleChannelsDelta(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, 401, "unauthenticated", "authentication required")
		return
	}
	since := parseSinceMarker(r.URL.Query().Get("since"))

	channels, maxSeq, err := s.db.ListChannelsForDelta(since, channelsDeltaCap)
	if err != nil {
		slog.Error("ListChannelsForDelta", "since", since, "err", err)
		writeJSONError(w, 500, "db_error", "delta read failed")
		return
	}

	bundles := make([]deltaBundle, 0, len(channels))
	for _, c := range channels {
		primary := channelToBundlePrimary(c)
		attachments := map[string]any{}
		if profile, _ := s.db.GetChannelProfile(c.ChannelID); profile != nil {
			attachments["channel_profile"] = channelProfileToAttachment(profile, canonicalChannelProfileURL(c.ChannelID, profile, c.URL))
		}
		if cs, _ := s.db.GetChannelSettings(c.ChannelID); cs != nil {
			attachments["channel_settings"] = cs
		}
		attachUserStateFromPrimary("channels", primary, attachments)
		bundles = append(bundles, deltaBundle{
			PrimaryKind: "channels",
			Primary:     primary,
			Attachments: attachments,
		})
	}

	writeDelta(w, deltaResponse{
		Bundles:     bundles,
		NextMarker:  formatMarker(maxSeq, since),
		EndOfStream: len(channels) < channelsDeltaCap,
	}, "channels", maxSeq)
}

// ── Helpers ──────────────────────────────────────────────────────────

func collectTweetIDs(items []model.FeedItem) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.TweetID)
	}
	return ids
}

func normalizeHandleSet(handles []string) map[string]struct{} {
	out := make(map[string]struct{}, len(handles))
	for _, handle := range handles {
		normalized := strings.ToLower(strings.TrimSpace(handle))
		if normalized == "" {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

// formatMarker emits the cursor for the next request. When the batch
// was empty (maxSeq == 0), echo the previous since so the client's
// cursor doesn't regress.
func formatMarker(maxSeq, prevSince int64) string {
	if maxSeq <= 0 {
		return strconv.FormatInt(prevSince, 10)
	}
	return strconv.FormatInt(maxSeq, 10)
}

// feedItemToBundlePrimary builds the bundle.primary map for a Twitter
// feed_items row. User state is split into attachments.user_state before
// serialization; this primary keeps only content and media fields.
func feedItemToBundlePrimary(
	item model.FeedItem,
	bookmarkInfo map[string]db.BookmarkInfo,
	subscribeURLs map[string]string,
	mutedHandleSet map[string]struct{},
) map[string]any {
	m := feedItemToJSON(item, bookmarkInfo, subscribeURLs)
	m["sync_seq"] = item.SyncSeq
	if bi, ok := bookmarkInfo[item.TweetID]; ok {
		applyBookmarkBundleFields(m, bi)
	} else {
		// Enrichment may mark content-hash siblings as bookmarked for display.
		// Delta payloads are authoritative Room mutations, so only direct
		// bookmark rows may materialize into Android's bookmarks table.
		m["is_bookmarked"] = false
		delete(m, "bookmarked_at")
		delete(m, "bookmark_category_id")
		delete(m, "bookmark_custom_title")
		delete(m, "bookmark_account_handles")
		delete(m, "bookmark_media_indices")
	}
	_, authorMuted := mutedHandleSet[strings.ToLower(strings.TrimSpace(item.AuthorHandle))]
	m["is_author_muted"] = authorMuted
	if handle := strings.ToLower(strings.TrimSpace(item.RetweetedByHandle)); handle != "" {
		_, reposterMuted := mutedHandleSet[handle]
		m["is_reposter_muted"] = reposterMuted
	}
	// Per #8, both media JSON blobs ride on every row that has either,
	// verbatim — Android upserts without inspection.
	if item.MediaJSON != "" {
		m["media_json"] = item.MediaJSON
	}
	if item.QuoteMediaJSON != "" {
		m["quote_media_json"] = item.QuoteMediaJSON
	}
	return m
}

func applyBookmarkBundleFields(m map[string]any, bi db.BookmarkInfo) {
	m["is_bookmarked"] = true
	m["bookmarked_at"] = bi.BookmarkedAtMs
	if bi.CategoryID != nil {
		m["bookmark_category_id"] = *bi.CategoryID
	}
	m["bookmark_custom_title"] = bi.CustomTitle
	m["bookmark_account_handles"] = bi.AccountHandles
	m["bookmark_media_indices"] = bi.MediaIndices
}

func videoToBundlePrimary(v model.Video) map[string]any {
	m := videoToJSON(v)
	// videoToJSON doesn't expose sync_seq — surface it for cursor logic.
	m["sync_seq"] = v.SyncSeq
	m["channel_is_followed"] = v.IsSubscribed
	m["channel_is_starred"] = v.IsStarred
	if v.MetadataJSON != "" {
		m["metadata_json"] = model.VideoMetadataJSONWithCountLabels(v.MetadataJSON)
	}
	return m
}

func channelToBundlePrimary(c model.Channel) map[string]any {
	m := map[string]any{
		"channel_id":          c.ChannelID,
		"source_id":           c.SourceID,
		"name":                c.Name,
		"url":                 c.URL,
		"platform":            c.Platform,
		"avatar_url":          c.AvatarURL,
		"quality":             c.Quality,
		"video_count":         c.VideoCount,
		"channel_is_followed": c.IsSubscribed,
		"channel_is_starred":  c.IsStarred,
		"sync_seq":            c.SyncSeq,
	}
	if c.LastChecked != nil {
		m["last_checked"] = c.LastChecked.UnixMilli()
		m["last_checked_ms"] = c.LastChecked.UnixMilli()
	}
	if !c.CreatedAt.IsZero() {
		m["created_at"] = c.CreatedAt.UnixMilli()
		m["created_at_ms"] = c.CreatedAt.UnixMilli()
	}
	return m
}

func channelProfileToAttachment(p *model.ChannelProfile, profileURL string) map[string]any {
	out := map[string]any{
		"channel_id":      p.ChannelID,
		"platform":        p.Platform,
		"handle":          p.Handle,
		"display_name":    p.DisplayName,
		"bio":             p.Bio,
		"website":         p.Website,
		"followers":       p.Followers,
		"followers_label": profileCountLabel(p.Followers),
		"following":       p.Following,
		"following_label": profileCountLabel(p.Following),
		"verified":        p.Verified,
		"verified_type":   p.VerifiedType,
		"protected":       p.Protected,
		"avatar_url":      p.AvatarURL,
		"banner_url":      p.BannerURL,
	}
	if profileURL != "" {
		out["profile_url"] = profileURL
	}
	return out
}

func canonicalChannelProfileURL(channelID string, profile *model.ChannelProfile, storedURL string) string {
	if url := strings.TrimSpace(storedURL); url != "" {
		return url
	}
	platform := strings.TrimSpace(profile.Platform)
	handle := strings.TrimPrefix(strings.TrimSpace(profile.Handle), "@")
	if platform == "" || handle == "" {
		platform, handle = channelIDPlatformHandle(channelID)
	}
	switch strings.ToLower(platform) {
	case "twitter", "x":
		if handle == "" {
			return ""
		}
		return "https://x.com/" + handle
	case "tiktok":
		if handle == "" {
			return ""
		}
		return "https://www.tiktok.com/@" + handle
	case "instagram":
		if handle == "" {
			return ""
		}
		return "https://www.instagram.com/" + handle + "/"
	case "youtube":
		if handle != "" && strings.HasPrefix(handle, "@") {
			return "https://www.youtube.com/" + handle
		}
		if handle != "" && !strings.HasPrefix(strings.ToUpper(handle), "UC") {
			return "https://www.youtube.com/@" + handle
		}
		if _, raw := channelIDPlatformHandle(channelID); raw != "" {
			return "https://www.youtube.com/channel/" + raw
		}
	}
	return ""
}

func profileCountLabel(count int) string {
	if count <= 0 {
		return ""
	}
	return model.ProfileCountLabel(count)
}

func attachUserStateFromPrimary(primaryKind string, primary map[string]any, attachments map[string]any) {
	state := map[string]any{"version": 1}
	switch primaryKind {
	case "feed_items":
		tweetID := userStateString(primary["tweet_id"])
		if tweetID != "" {
			if liked, ok := userStateBool(primary["is_liked"]); ok {
				row := map[string]any{"tweet_id": tweetID, "liked": liked}
				if liked {
					if at := userStateInt64(primary["published_at"]); at > 0 {
						row["liked_at"] = at
					}
				}
				appendUserStateRow(state, "feed_likes", row)
			}
			if seen, ok := userStateBool(primary["is_seen"]); ok && seen {
				row := map[string]any{"tweet_id": tweetID, "seen": true}
				if at := userStateInt64(primary["published_at"]); at > 0 {
					row["seen_at"] = at
				}
				appendUserStateRow(state, "feed_seen", row)
			}
			if bookmarked, ok := userStateBool(primary["is_bookmarked"]); ok {
				appendUserStateRow(state, "bookmarks", bookmarkUserStateRow(tweetID, bookmarked, primary))
			}
		}
		channelID := userStateString(primary["channel_id"])
		appendChannelUserStateRows(state, channelID, primary)
		if muted, ok := userStateBool(primary["is_author_muted"]); ok {
			if handle := userStateString(primary["author_handle"]); handle != "" {
				appendUserStateRow(state, "muted_accounts", map[string]any{"handle": handle, "muted": muted})
			}
		}
		if muted, ok := userStateBool(primary["is_reposter_muted"]); ok {
			if handle := userStateString(primary["retweeted_by_handle"]); handle != "" {
				appendUserStateRow(state, "muted_accounts", map[string]any{"handle": handle, "muted": muted})
			}
		}
	case "videos":
		videoID := userStateString(primary["video_id"])
		if videoID != "" {
			if bookmarked, ok := userStateBool(primary["is_bookmarked"]); ok {
				appendUserStateRow(state, "bookmarks", bookmarkUserStateRow(videoID, bookmarked, primary))
			}
		}
		appendChannelUserStateRows(state, userStateString(primary["channel_id"]), primary)
	case "channels":
		appendChannelUserStateRows(state, userStateString(primary["channel_id"]), primary)
	}
	if len(state) > 1 {
		attachments["user_state"] = state
	}
	stripPrimaryUserState(primary)
}

func appendChannelUserStateRows(state map[string]any, channelID string, primary map[string]any) {
	if channelID == "" {
		return
	}
	if followed, ok := userStateBool(primary["channel_is_followed"]); ok {
		appendUserStateRow(state, "channel_follows", map[string]any{
			"channel_id": channelID,
			"followed":   followed,
		})
	}
	if starred, ok := userStateBool(primary["channel_is_starred"]); ok {
		appendUserStateRow(state, "channel_stars", map[string]any{
			"channel_id": channelID,
			"starred":    starred,
		})
	}
}

func bookmarkUserStateRow(videoID string, bookmarked bool, primary map[string]any) map[string]any {
	row := map[string]any{
		"video_id":   videoID,
		"bookmarked": bookmarked,
	}
	if !bookmarked {
		return row
	}
	copyIfPresent(row, primary, "bookmarked_at", "bookmarked_at")
	copyIfPresent(row, primary, "category_id", "bookmark_category_id")
	copyIfPresent(row, primary, "custom_title", "bookmark_custom_title")
	copyIfPresent(row, primary, "account_handles", "bookmark_account_handles")
	copyIfPresent(row, primary, "media_indices", "bookmark_media_indices")
	return row
}

func copyIfPresent(dst map[string]any, src map[string]any, dstKey, srcKey string) {
	if value, ok := src[srcKey]; ok && value != nil {
		dst[dstKey] = value
	}
}

func appendUserStateRow(state map[string]any, key string, row map[string]any) {
	rows, _ := state[key].([]map[string]any)
	state[key] = append(rows, row)
}

func stripPrimaryUserState(primary map[string]any) {
	for _, key := range []string{
		"is_liked",
		"is_bookmarked",
		"bookmarked",
		"is_seen",
		"bookmarked_at",
		"bookmark_category_id",
		"bookmark_custom_title",
		"bookmark_account_handles",
		"bookmark_media_indices",
		"channel_is_followed",
		"channel_is_starred",
		"is_followed",
		"is_starred",
		"is_author_muted",
		"is_reposter_muted",
		"is_moment_viewed",
		"playback_position",
		"progress_updated_at_ms",
		"progress_source",
		"last_watched",
		"watched",
	} {
		delete(primary, key)
	}
}

func userStateString(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func userStateBool(value any) (bool, bool) {
	if b, ok := value.(bool); ok {
		return b, true
	}
	return false, false
}

func userStateInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}
