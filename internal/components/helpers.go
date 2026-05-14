package components

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/language"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/settings"
)

func videoTitle(v model.Video) string {
	if v.Title != "" {
		return v.Title
	}
	if v.Description != "" {
		return v.Description
	}
	if v.Platform == "youtube" {
		return v.VideoID
	}
	return ""
}

func videoThumbURL(v model.Video) string {
	if v.ThumbnailURL != "" {
		return v.ThumbnailURL
	}
	return "/static/default_thumb.png"
}

func videoAvatarURL(v model.Video) string {
	if v.AvatarURL != "" {
		return v.AvatarURL
	}
	return "/static/default_thumb.png"
}

func videoChannelName(v model.Video) string {
	if v.ChannelName != "" {
		return v.ChannelName
	}
	// Channel IDs are stored as "platform_handle" — strip the prefix for display.
	for _, prefix := range []string{"twitter_", "tiktok_", "instagram_", "youtube_"} {
		if handle, ok := strings.CutPrefix(v.ChannelID, prefix); ok {
			if handle != "" {
				return handle
			}
			return v.ChannelID
		}
	}
	return v.ChannelID
}

func videoRepostLabel(v model.Video) string {
	if !v.RepostIntroduced || v.RepostCount <= 0 {
		return ""
	}
	name := model.RepostAuthorLabel(v.ReposterDisplayName, v.ReposterHandle)
	if name == "" {
		return ""
	}
	if v.RepostCount > 1 {
		return fmt.Sprintf("%s and %d others reposted", name, v.RepostCount-1)
	}
	return name + " reposted"
}

func videoRepostChannelID(v model.Video) string {
	if !v.RepostIntroduced || v.RepostCount <= 0 {
		return ""
	}
	return strings.TrimSpace(v.ReposterChannelID)
}

func videoRepostHandle(v model.Video) string {
	if videoRepostChannelID(v) == "" {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(v.ReposterHandle), "@")
}

func videoRepostDisplayName(v model.Video) string {
	if videoRepostChannelID(v) == "" {
		return ""
	}
	return strings.TrimSpace(v.ReposterDisplayName)
}

func videoRepostAvatarURL(v model.Video) string {
	channelID := videoRepostChannelID(v)
	if channelID == "" {
		return ""
	}
	return "/api/media/avatar/" + channelID
}

// videoAuthorHandle returns the raw handle (no platform prefix) for use
// in bookmark account pills. YouTube channel IDs are opaque (UCxxx) so
// we return the channel name there instead.
func videoAuthorHandle(v model.Video) string {
	for _, prefix := range []string{"twitter_", "tiktok_", "instagram_"} {
		if handle, ok := strings.CutPrefix(v.ChannelID, prefix); ok && handle != "" {
			return handle
		}
	}
	return ""
}

func videoWebpageURL(v model.Video) string {
	if v.Metadata != nil && v.Metadata.WebpageURL != "" {
		return v.Metadata.WebpageURL
	}
	// Construct URL from platform + video ID when metadata is missing (e.g. bookmark stubs).
	switch v.Platform {
	case "twitter":
		handle, _ := strings.CutPrefix(v.ChannelID, "twitter_")
		if handle == "" {
			handle = "i" // x.com/i/status/{id} works without a handle
		}
		return "https://x.com/" + handle + "/status/" + v.VideoID
	case "tiktok":
		if handle, ok := strings.CutPrefix(v.ChannelID, "tiktok_"); ok && handle != "" {
			return "https://www.tiktok.com/@" + handle + "/video/" + v.VideoID
		}
	case "instagram":
		shortcode := strings.TrimPrefix(strings.TrimPrefix(v.VideoID, "instagram_post_"), "instagram_reel_")
		if shortcode != "" && shortcode != v.VideoID {
			if strings.HasPrefix(v.VideoID, "instagram_post_") {
				return "https://www.instagram.com/p/" + shortcode + "/"
			}
			return "https://www.instagram.com/reel/" + shortcode + "/"
		}
	}
	return ""
}

func videoTaggedAccounts(v model.Video) []model.RetweeterInfo {
	meta := v.ParseMetadata()
	if meta == nil || (len(meta.Coauthors) == 0 && len(meta.TaggedUsers) == 0) {
		return nil
	}
	author := strings.ToLower(strings.TrimPrefix(videoAuthorHandle(v), "@"))
	seen := map[string]struct{}{}
	accounts := make([]model.InstagramAccount, 0, len(meta.Coauthors)+len(meta.TaggedUsers))
	accounts = append(accounts, meta.Coauthors...)
	accounts = append(accounts, meta.TaggedUsers...)
	out := make([]model.RetweeterInfo, 0, len(accounts))
	for _, account := range accounts {
		handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(account.Username), "@"))
		if handle == "" || handle == author {
			continue
		}
		if _, ok := seen[handle]; ok {
			continue
		}
		seen[handle] = struct{}{}
		out = append(out, model.RetweeterInfo{
			Handle:      handle,
			DisplayName: strings.TrimSpace(account.FullName),
			ChannelID:   "instagram_" + handle,
			AvatarURL:   strings.TrimSpace(account.ProfilePicURL),
			IsTagged:    true,
		})
	}
	return out
}

func boolDataAttr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func storyStateAttr(state string) string {
	switch state {
	case model.StoryStateNew, model.StoryStateSeen:
		return state
	default:
		return model.StoryStateNone
	}
}

func storyRingClass(base, state string) string {
	classes := []string{base}
	switch storyStateAttr(state) {
	case model.StoryStateNew:
		classes = append(classes, "has-story", "story-new")
	case model.StoryStateSeen:
		classes = append(classes, "has-story", "story-seen")
	}
	return strings.Join(classes, " ")
}

func storyFirstVideoID(status model.StoryStatus) string {
	if status.FirstUnseenVideoID != "" {
		return status.FirstUnseenVideoID
	}
	return status.FirstVideoID
}

func storyChannelFirstVideoID(ch model.StoryChannel) string {
	if ch.FirstUnseenVideoID != "" {
		return ch.FirstUnseenVideoID
	}
	return ch.FirstVideoID
}

func storyLatestTime(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms)
	return &t
}

func storyChannelMeta(p PageProps, ch model.StoryChannel) string {
	count := ch.Count
	if count <= 0 {
		return ""
	}
	label := L(p, "stories_count_one", "1 story")
	if count != 1 {
		label = LF(p, "stories_count_many", "%1$d stories", count)
	}
	if ch.LatestAtMs > 0 {
		if rel := RelativeTimeText(p, storyLatestTime(ch.LatestAtMs)); rel != "" {
			return label + " · " + rel
		}
	}
	return label
}

func bookmarkCategoryIDStr(id *int64) string {
	if id != nil {
		return fmt.Sprintf("%d", *id)
	}
	return ""
}

func publishedAtStr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.UnixMilli(), 10)
}

func videoCardMediaType(v model.Video) string {
	if v.MediaSlideCount > 1 {
		return "slideshow"
	}
	kind := strings.ToLower(strings.TrimSpace(v.MediaKind))
	switch kind {
	case "slideshow":
		return "slideshow"
	case "image", "photo":
		return "image"
	default:
		return "video"
	}
}

func videoCardMediaTypeLabel(p PageProps, mediaType string) string {
	switch mediaType {
	case "slideshow":
		return L(p, "media_type_slideshow", "Slideshow")
	case "image":
		return L(p, "media_type_image", "Single image")
	default:
		return L(p, "media_type_video", "Video")
	}
}

func videoCardMediaTypeIcon(mediaType string) string {
	switch mediaType {
	case "slideshow":
		return `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="3" width="13" height="14" rx="2"></rect><rect x="4" y="7" width="13" height="14" rx="2"></rect><path d="M6 18l3.2-3.2 2.1 2.1 1.7-1.7 3 3.8"></path><circle cx="9" cy="11" r="1.2"></circle></svg>`
	case "image":
		return `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="5" width="16" height="14" rx="2"></rect><circle cx="9" cy="10" r="1.4"></circle><path d="M5 17l4-4 3 3 2-2 5 5"></path></svg>`
	default:
		return `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5v14l11-7z"></path></svg>`
	}
}

// mapStr safely extracts a string value from a map[string]any.
func mapStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// mapInt safely extracts an int value from a map[string]any (handles int and float64).
func mapInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

// toJSONAttr marshals v to a JSON string for use in data attributes.
func toJSONAttr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// feedAuthorLabel returns the display label for a feed item author.
func feedAuthorLabel(item model.FeedItem) string {
	if item.AuthorDisplayName != "" {
		return item.AuthorDisplayName
	}
	if item.AuthorHandle != "" {
		return item.AuthorHandle
	}
	if item.SourceHandle != "" {
		return item.SourceHandle
	}
	return "X post"
}

// feedRepostedLabel returns the display label for who reposted a feed item.
func feedRepostedLabel(item model.FeedItem) string {
	if item.RetweetedByDisplayName != "" {
		return item.RetweetedByDisplayName
	}
	if item.RetweetedByHandle != "" {
		return item.RetweetedByHandle
	}
	if item.SourceHandle != "" {
		return item.SourceHandle
	}
	return ""
}

// mediaGridCount clamps a media count to at most 4 for grid display.
func mediaGridCount(count int) int {
	if count > 4 {
		return 4
	}
	return count
}

// hasForeignLang returns true if the feed item has non-English or unknown language text.
func hasForeignLang(item model.FeedItem) bool {
	return hasBodyTranslatePill(item) || hasQuoteTranslatePill(item)
}

func hasBodyTranslatePill(item model.FeedItem) bool {
	return hasTranslatableLang(item.Lang, item.BodyText)
}

func hasQuoteTranslatePill(item model.FeedItem) bool {
	return hasTranslatableLang(item.QuoteLang, item.QuoteBodyText)
}

func hasTranslatableLang(lang string, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(lang))
	return normalized == "" || normalized != "en"
}

// retweeterLabel returns a display label for a RetweeterInfo.
func retweeterLabel(rt model.RetweeterInfo) string {
	if rt.DisplayName != "" {
		return rt.DisplayName
	}
	return rt.Handle
}

// repostEntry is the rendering-ready form of a reposter for feedRepostLine.
type repostEntry struct {
	Label     string // display name if non-empty, else handle
	Handle    string // lowercased, leading '@' stripped
	ChannelID string // may be "" if unknown
}

// normalizeHandle lowercases a handle and strips any leading '@'.
func normalizeHandle(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "@")
	return strings.ToLower(h)
}

func feedExternalURL(item model.FeedItem) string {
	if url := safeExternalHTTPURL(item.CanonicalURL); url != "" {
		return url
	}
	tweetID := strings.TrimSpace(item.TweetID)
	if tweetID == "" {
		return ""
	}
	handle := normalizeHandle(item.AuthorHandle)
	if handle == "" {
		handle = normalizeHandle(item.SourceHandle)
	}
	if handle == "" {
		handle = "i"
	}
	return "https://x.com/" + handle + "/status/" + tweetID
}

// Feed cards may render stored canonical URLs as browser-openable links, so
// preserve only absolute HTTP(S) URLs and rebuild X status links otherwise.
func safeExternalHTTPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil {
		return ""
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return ""
	}
	return raw
}

// repostEntries returns the ordered list of reposter entries to render, or
// nil if there's nothing to show.
//
//   - If item.Retweeters is non-empty, returns one entry per element.
//   - Else, if item.IsRetweet is true and any of RetweetedByDisplayName,
//     RetweetedByHandle, or SourceHandle is set, returns a single synthetic
//     entry (using feedRepostedLabel for the label).
//   - Else returns nil.
func repostEntries(item model.FeedItem) []repostEntry {
	if len(item.Retweeters) > 0 {
		out := make([]repostEntry, 0, len(item.Retweeters))
		for _, rt := range item.Retweeters {
			out = append(out, repostEntry{
				Label:     retweeterLabel(rt),
				Handle:    normalizeHandle(rt.Handle),
				ChannelID: rt.ChannelID,
			})
		}
		return out
	}
	if !item.IsRetweet {
		return nil
	}
	label := feedRepostedLabel(item)
	if label == "" {
		return nil
	}
	handle := item.RetweetedByHandle
	if handle == "" {
		handle = item.SourceHandle
	}
	return []repostEntry{{
		Label:     label,
		Handle:    normalizeHandle(handle),
		ChannelID: item.ReposterChannelID,
	}}
}

// splitRepostCap returns (visible, hidden) where visible is the first capN
// entries and hidden is the remainder. When len(entries) <= capN, hidden is
// nil.
func splitRepostCap(entries []repostEntry, capN int) (visible, hidden []repostEntry) {
	if len(entries) <= capN {
		return entries, nil
	}
	return entries[:capN], entries[capN:]
}

// isPureRepost returns true for a retweet that isn't a quote tweet.
func isPureRepost(item model.FeedItem) bool {
	return item.IsRetweet && item.QuoteTweetID == ""
}

// feedSourceChannelID returns the channel ID of the account that brought
// this item into the feed — the retweeter for retweets, the author otherwise.
func feedSourceChannelID(item model.FeedItem) string {
	if item.ReposterChannelID != "" {
		return item.ReposterChannelID
	}
	return item.ChannelID
}

// feedFollowHandle returns the displayed author's handle for the follow button.
func feedFollowHandle(item model.FeedItem) string {
	if item.AuthorHandle != "" {
		return item.AuthorHandle
	}
	if item.SourceHandle != "" {
		return item.SourceHandle
	}
	return item.AuthorHandle
}

// feedAlgoScore formats the algorithm interest score for a data attribute.
func feedAlgoScore(score float64) string {
	return fmt.Sprintf("%.1f", score)
}

func threadVisibleAncestorStart(chain []model.FeedItem) int {
	if len(chain) <= 1 {
		return 0
	}
	return len(chain) - 1
}

func threadCapsuleVisible(chain []model.FeedItem) bool {
	return len(chain) > 1
}

func threadCapsulePostCount(item model.FeedItem) int {
	return len(item.ThreadChain) + 1
}

func threadCapsulePeopleCount(item model.FeedItem) int {
	seen := make(map[string]bool)
	add := func(handle string) {
		handle = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(handle)), "@")
		if handle != "" {
			seen[handle] = true
		}
	}
	for _, ancestor := range item.ThreadChain {
		add(ancestor.AuthorHandle)
	}
	add(item.AuthorHandle)
	return len(seen)
}

// feedPublishedAtStr returns the published_at time as a string or empty.
func feedPublishedAtStr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.UnixMilli(), 10)
}

func feedTranslateSourceLabel(sourceLang string) string {
	return language.DisplayName(sourceLang)
}

// stripThreadNoise returns a copy of item with conversation-thread-specific
// presentation cleanup applied:
//   - Strip a leading "@<reply_to_handle> " from BodyText. The mention is
//     redundant context inside a thread (the parent card right above
//     already shows the author).
//
// Thread-level retweet context ("X reposted") renders once on the thread
// wrapper, so we don't touch IsRetweet here.
//
// Operates on a local copy; the caller's item is unchanged.
func stripThreadNoise(item model.FeedItem) model.FeedItem {
	if item.IsReply && item.ReplyToHandle != "" && item.BodyText != "" {
		prefix := "@" + item.ReplyToHandle
		body := item.BodyText
		if strings.HasPrefix(body, prefix) {
			rest := body[len(prefix):]
			// Only strip when the next char is a separator — avoid stripping
			// when @reply_to_handle is a substring of the actual content.
			if rest == "" || rest[0] == ' ' || rest[0] == '\n' || rest[0] == '\t' || rest[0] == ',' || rest[0] == ':' {
				item.BodyText = strings.TrimSpace(rest)
			}
		}
	}
	return item
}

// feedTranslateLabel returns the translate label for a feed item.
// Cached translation source labels come from the translator, not ingest-time
// language detection.
func feedTranslateLabel(item model.FeedItem) string {
	if item.BodyTranslation != "" {
		return translateLabelFromLang(item.BodySourceLang)
	}
	if item.QuoteTranslation != "" {
		return translateLabelFromLang(item.QuoteSourceLang)
	}
	return "Show translation"
}

func translateLabelFromLang(lang string) string {
	label := language.DisplayName(lang)
	if label == "" {
		return "Translated"
	}
	return "Translated from " + label
}

// feedQuoteLabel returns the display label for a quoted post author.
func feedQuoteLabel(item model.FeedItem) string {
	if item.QuoteAuthorDisplayName != "" {
		return item.QuoteAuthorDisplayName
	}
	if item.QuoteAuthorHandle != "" {
		return item.QuoteAuthorHandle
	}
	return "Quoted post"
}

// heartFill returns "currentColor" or "none" for SVG fill based on active state.
func heartFill(active bool) string {
	if active {
		return "currentColor"
	}
	return "none"
}

// resumePosition returns the playback resume position as a formatted string.
// Short-form videos always resume from 0.
func resumePosition(v model.Video) string {
	pos := v.PlaybackPosition
	if v.IsShortForm {
		pos = 0.0
	}
	return fmt.Sprintf("%.1f", pos)
}

// int64Str formats an int64 as a string for use in data attributes.
func int64Str(n int64) string {
	return fmt.Sprintf("%d", n)
}

// channelInitial returns the uppercase first character of the channel name.
func channelInitial(v model.Video) string {
	name := videoChannelName(v)
	if name == "" {
		name = "U"
	}
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "U"
}

// retweetersChannelIDsJSON returns a JSON array of channel IDs for the given
// retweeters, e.g. ["twitter_a","twitter_b"]. Returns "[]" when the list is
// empty so the data attribute is always valid JSON.
func retweetersChannelIDsJSON(rts []model.RetweeterInfo) string {
	if len(rts) == 0 {
		return "[]"
	}
	ids := make([]string, len(rts))
	for i, r := range rts {
		ids[i] = r.ChannelID
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

// XChannelInfo holds data for rendering an X channel header on feed pages.
type XChannelInfo struct {
	Handle      string
	DisplayName string
	ChannelID   string
	IsFollowing bool
	IsStarred   bool
	Profile     *model.ChannelProfile
}

// UserDisplay holds data for rendering a user row in the admin panel.
type UserDisplay struct {
	Username    string
	Role        string
	Platforms   []string
	PlatformCSV string // comma-separated for edit button onclick
}

func platformLabel(p string) string {
	switch p {
	case "youtube":
		return "Videos"
	case "twitter":
		return "Feed"
	case "tiktok":
		return "TikTok"
	case "instagram":
		return "Instagram"
	default:
		return p
	}
}

// SidebarStatusData holds the data needed to render the sidebar status fragment.
type SidebarStatusData struct {
	ActivityMsg   string
	IsDownloading bool
	QueueTotal    int
	StopRequested bool
}

// BookmarkCategoryDisplay holds data for rendering a bookmark category row.
type BookmarkCategoryDisplay struct {
	ID          int64
	Name        string
	ArchivePath string
	Slug        string // for placeholder text
}

// CookieRowData holds data for rendering a cookie platform row.
type CookieRowData struct {
	Platform  string
	Name      string
	Exists    bool
	Enabled   bool
	Browser   string
	FileCount int
}

func hasPlat(platforms []string, p string) bool {
	for _, v := range platforms {
		if v == p {
			return true
		}
	}
	return false
}

// ChannelSettingsData holds per-channel settings for the channel settings form.
type ChannelSettingsData struct {
	Quality            string
	MaxVideos          int
	DownloadSubtitles  bool
	MediaOnly          bool
	MediaDownloadLimit int
}

// isXPlatform returns true for twitter/x platforms.
func isXPlatform(platform string) bool {
	p := strings.ToLower(strings.TrimSpace(platform))
	return p == "twitter" || p == "x"
}

// isYouTubePlatform returns true for youtube platform (or empty = default youtube).
func isYouTubePlatform(platform string) bool {
	p := strings.ToLower(strings.TrimSpace(platform))
	return p == "youtube" || p == ""
}

// DownloadsStatusData holds data for rendering the downloads dashboard panel.
type DownloadsStatusData struct {
	QueueActive  int
	QueueQueued  int
	QueueTotal   int
	SessionDone  int
	SessionFail  int
	LastElapsed  string
	LastChannel  string
	LastPlatform string
	Activity     []ActivityEntry
	Recent       []ActivityEntry
	Filter       string // "all", "active", "failed", "completed"
	TzOffsetSec  int
}

// ActivityEntry represents a single activity log/table entry.
type ActivityEntry struct {
	Time      string
	Timestamp int64
	Message   string
	Status    string
	ChannelID string
	Platform  string
	Kind      string
	Stage     string
}

// DlLogClass returns the CSS class for a download log entry.
func DlLogClass(e ActivityEntry) string {
	if e.Kind == "channel" && e.Status == "error" {
		return "log-dl-fail"
	}
	if e.Kind == "channel" {
		return "log-dl-check"
	}
	if e.Stage == "queue" {
		return "log-dl-queue"
	}
	switch {
	case e.Kind == "video" && e.Status == "start":
		return "log-dl-active"
	case e.Kind == "video" && e.Status == "done":
		return "log-dl-ok"
	case e.Kind == "video" && e.Status == "error":
		return "log-dl-fail"
	case e.Kind == "video" && e.Status == "skipped":
		return "log-msg"
	}
	return "log-msg"
}

// DlStatusBadgeClass returns the CSS dot class for a status.
func DlStatusBadgeClass(status string) string {
	switch status {
	case "start":
		return "ok"
	case "done":
		return "ok"
	case "error":
		return "er"
	default:
		return "wn"
	}
}

// DlStatusLabel returns the display label for a status.
func DlStatusLabel(status string) string {
	switch status {
	case "start":
		return "Downloading"
	case "done":
		return "Completed"
	case "error":
		return "Failed"
	case "skipped":
		return "Skipped"
	default:
		return status
	}
}

// FormatHMS formats a unix timestamp to HH:MM:SS using the given tz offset.
func FormatHMS(ts int64, tzOffsetSec int) string {
	t := time.Unix(ts, 0).UTC().Add(time.Duration(tzOffsetSec) * time.Second)
	return t.Format("15:04:05")
}

// FormatHM formats a unix timestamp to HH:MM using the given tz offset.
func FormatHM(ts int64, tzOffsetSec int) string {
	t := time.Unix(ts, 0).UTC().Add(time.Duration(tzOffsetSec) * time.Second)
	return t.Format("15:04")
}

// DlTitleFromMessage extracts a short title from a download message.
func DlTitleFromMessage(msg string) string {
	// Strip "Downloading: ", "Completed: ", etc. prefix and " — elapsed" suffix
	for _, prefix := range []string{"Downloading: ", "Completed: ", "Failed: ", "Skipped: "} {
		if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
			msg = msg[len(prefix):]
			break
		}
	}
	if idx := strings.Index(msg, " \u2014 "); idx >= 0 {
		msg = msg[:idx]
	}
	return msg
}

// DlFilterMatch returns true if the entry matches the given filter.
func DlFilterMatch(e ActivityEntry, filter string) bool {
	switch filter {
	case "active":
		return e.Status == "start"
	case "failed":
		return e.Status == "error"
	case "completed":
		return e.Status == "done"
	default:
		return true
	}
}

// ServerDashboardData holds data for rendering the server dashboard panel.
type ServerDashboardData struct {
	// Stat cards
	UptimeText     string // "2d 3h" or "4h 12m"
	UptimeStarted  string // "since 2026-04-16 18:00:00"
	Errors24h      int
	ErrorsDelta    int
	MemoryMB       float64
	MemoryHistory  []float64
	DBSizeMB       float64
	WALSizeMB      float64
	TableCount     int
	StorageGB      float64
	VideoStorageGB float64
	AvgMBPerVideo  float64
	VideosTotal    int
	VideosWatched  int
	SourcesOK      int
	SourcesCool    int
	SourcesFail    int
	// DB sections
	ChannelsTotal        int
	ChannelsByPlat       map[string]int
	FeedItemsCount       int
	LocalFeedCount       int
	BookmarksCount       int
	CommentsCount        int
	AvatarCount          int
	MediaReady           int
	MediaQueued          int
	MediaFailed          int
	SourceHealthOK       int
	SourceHealthCooling  int
	SourceHealthFailed   int
	SourceAvgLatencyMs   int
	PreviewReady         int
	PreviewPending       int
	PreviewUnsupported   int
	AnalyticsTotal       int
	AnalyticsAppStarts   int
	AnalyticsVideoOpens  int
	AnalyticsSyncs       int
	DownloadQueuePending int
	DownloadQueueFailed  int
	SponsorBlockChecked  int
	SponsorBlockSegments int
	// Activity log
	Activity  []ServerActivityEntry
	Errors    []ServerErrorEntry
	Warnings  []ServerErrorEntry
	LogFilter string // "all", "errors", "warnings", "info"
	// Processes
	Processes []ServerProcessCard
}

// ServerActivityEntry is an activity log line for the server dashboard.
type ServerActivityEntry struct {
	Time    string
	Status  string // "error", "warning", "done", "info"
	Source  string
	Message string
}

// ServerErrorEntry is a single error or warning row.
type ServerErrorEntry struct {
	Time    string
	Message string
	Count   int
}

// ServerProcessCard is a worker/process card.
type ServerProcessCard struct {
	Name   string
	Status string // "running", "idle", "stopped"
	Detail string
}

// ServerLogFilterMatch returns true if the activity entry matches the filter.
func ServerLogFilterMatch(e ServerActivityEntry, filter string) bool {
	switch filter {
	case "errors":
		return e.Status == "error"
	case "warnings":
		return e.Status == "warning"
	case "info":
		return e.Status != "error" && e.Status != "warning"
	default:
		return true
	}
}

// ServerLogFilterCount counts entries matching a filter.
func ServerLogFilterCount(entries []ServerActivityEntry, filter string) int {
	if filter == "all" {
		return len(entries)
	}
	n := 0
	for _, e := range entries {
		if ServerLogFilterMatch(e, filter) {
			n++
		}
	}
	return n
}

// videoWatchedPct returns the watched percentage for videos ring chart.
func videoWatchedPct(watched, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(watched) / float64(total) * 100
}

// sourcePct returns a segment's percentage of the total for source ring chart.
func sourcePct(count, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(count) / float64(total) * 100
}

// serverLevelClass returns CSS class for a server log level.
func serverLevelClass(status string) string {
	switch status {
	case "error":
		return "log-lvl-err"
	case "warning":
		return "log-lvl-warn"
	default:
		return "log-lvl-info"
	}
}

// serverLevelText returns the display text for a server log level.
func serverLevelText(status string) string {
	switch status {
	case "error":
		return "ERROR"
	case "warning":
		return "WARN"
	case "done":
		return "DONE"
	default:
		return "INFO"
	}
}

// ServerProcessStatusClass returns CSS class for process status badge.
func ServerProcessStatusClass(status string) string {
	switch status {
	case "running":
		return "running"
	case "idle":
		return "idle"
	default:
		return "stopped"
	}
}

// UptimeText formats uptime seconds: "Xd Yh" for >=24h, else Go elapsed.
func UptimeText(seconds int, elapsed string) string {
	if seconds >= 86400 {
		d := seconds / 86400
		h := (seconds % 86400) / 3600
		return fmt.Sprintf("%dd %dh", d, h)
	}
	if elapsed == "" {
		return "\u2014"
	}
	return elapsed
}

// SparklineBar renders a single bar of a sparkline inline.
type SparklineBar struct {
	HeightPx int
	Color    string
}

// SparklineBars computes the bars for a sparkline from a slice of values.
func SparklineBars(values []float64) []SparklineBar {
	if len(values) == 0 {
		return nil
	}
	max := values[0]
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	if max == 0 {
		max = 1
	}
	threshold := max * 0.8
	bars := make([]SparklineBar, len(values))
	for i, v := range values {
		h := int((v / max) * 18)
		if h < 2 {
			h = 2
		}
		color := "var(--status-success)"
		if v > threshold {
			color = "var(--status-warning)"
		}
		bars[i] = SparklineBar{HeightPx: h, Color: color}
	}
	return bars
}

// RingSegment is one arc of a mini ring chart.
type RingSegment struct {
	Value      float64 // percent
	Color      string
	Count      int
	Label      string
	DashArray  string // pre-formatted "X.X Y.Y"
	DashOffset string // pre-formatted "-X.X" or ""
}

// BuildRingSegments computes dash arrays for ring chart segments.
func BuildRingSegments(segs []RingSegment) []RingSegment {
	circ := 2 * 3.141592653589793 * 16 // r=16
	offset := 0.0
	for i := range segs {
		dash := (segs[i].Value / 100) * circ
		segs[i].DashArray = fmt.Sprintf("%.1f %.1f", dash, circ)
		if offset > 0 {
			segs[i].DashOffset = fmt.Sprintf("-%.1f", offset)
		}
		offset += dash
	}
	return segs
}

// FmtNum formats an int with comma thousands separator.
func FmtNum(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// ErrorsDeltaArrow returns the arrow character for errors delta.
func ErrorsDeltaArrow(delta int) string {
	if delta < 0 {
		return "\u2193"
	}
	if delta > 0 {
		return "\u2191"
	}
	return ""
}

// Abs returns the absolute value of an int.
func Abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ServerRawLogData holds data for the raw server log section (separate endpoint).
type ServerRawLogData struct {
	Lines  []ServerRawLogLine
	Filter string // "all", "errors", "warnings", "info"
}

// ServerRawLogLine is a single raw log file line, classified.
type ServerRawLogLine struct {
	Text  string
	Level string // "ERROR", "WARNING", "INFO", ""
}

// ServerRawLogFilterMatch returns true if a raw log line matches the filter.
func ServerRawLogFilterMatch(line ServerRawLogLine, filter string) bool {
	switch filter {
	case "errors":
		return line.Level == "ERROR"
	case "warnings":
		return line.Level == "WARNING"
	case "info":
		return line.Level == "INFO"
	default:
		return true
	}
}

// ServerRawLogFilterCount counts lines matching a filter.
func ServerRawLogFilterCount(lines []ServerRawLogLine, filter string) int {
	if filter == "all" {
		return len(lines)
	}
	n := 0
	for _, l := range lines {
		if ServerRawLogFilterMatch(l, filter) {
			n++
		}
	}
	return n
}

// AndroidDashboardData holds data for rendering the Android dashboard panel.
type AndroidDashboardData struct {
	SyncAgo           string
	SyncCompletedHMS  string
	SyncDuration      string
	GenerationItems   int
	GenerationAssets  int
	GenerationReady   int
	GenerationMissing int
	DevicePercent     int
	DeviceVerified    int
	DevicePending     int
	DeviceFailed      int
	DeviceMissing     int
	DeviceTotal       int
	DeviceBytes       string
	StepsCompleted    int
	StepsTotal        int
	FeedItemsTotal    int
	FeedItemsMedia    int
	ErrorCount        int
	WarningCount      int
	Pipeline          []AndroidPipelineStep
	PipelineFooter    string
	CacheHealth       []AndroidCacheRow
	Activity          []AndroidLogEntry
	Errors            []AndroidErrorEntry
	Warnings          []AndroidWarningEntry
	LogFilter         string // "all", "errors", "warnings"
	ForceSyncPending  bool
	RoomQuery         *AndroidRoomResult
}

// AndroidPipelineStep is a single sync step row.
type AndroidPipelineStep struct {
	Name     string
	Status   string // "done", "pending", "error", etc.
	Duration string // pre-formatted ("3.2s" or "45ms")
}

// AndroidCacheRow is a cache category health bar.
type AndroidCacheRow struct {
	Label     string
	Cached    int
	Total     int
	Percent   int
	BarCSS    string // "an-cache-bar-good/ok/bad"
	Retention string
}

// AndroidLogEntry is a log console line.
type AndroidLogEntry struct {
	Timestamp string // localized HH:MM:SS
	Tag       string
	Message   string
	LevelCSS  string // "log-lvl-info/warn/err"
}

// AndroidErrorEntry is a deduplicated error group.
type AndroidErrorEntry struct {
	Tag       string
	Message   string
	Timestamp string // formatted "last seen HH:MM:SS"
	FirstSeen string // formatted "first seen HH:MM:SS" or ""
	Count     int
}

// AndroidWarningEntry is a warning line.
type AndroidWarningEntry struct {
	Tag     string
	Message string
}

// AndroidRoomResult is the result of a Room DB query.
type AndroidRoomResult struct {
	Error    string
	Query    string
	RowCount int
	Columns  []string
	Rows     [][]string
}

// AndroidLogFilterCount returns count of log entries matching a filter.
func AndroidLogFilterCount(entries []AndroidLogEntry, filter string) int {
	if filter == "all" {
		return len(entries)
	}
	n := 0
	for _, e := range entries {
		if filter == "errors" && e.LevelCSS == "log-lvl-err" {
			n++
		}
		if filter == "warnings" && e.LevelCSS == "log-lvl-warn" {
			n++
		}
	}
	return n
}

// anErrorClass returns a semantic class for the error count value.
func anErrorClass(count int) string {
	if count > 0 {
		return "status-error-text"
	}
	return ""
}

// AndroidLogFilterMatch returns true if the log entry matches the filter.
func AndroidLogFilterMatch(e AndroidLogEntry, filter string) bool {
	switch filter {
	case "errors":
		return e.LevelCSS == "log-lvl-err"
	case "warnings":
		return e.LevelCSS == "log-lvl-warn"
	default:
		return true
	}
}

// FeedDashboardData holds data for rendering the feed dashboard panel.
type FeedDashboardData struct {
	IngestEnabled  bool
	IngestRunning  bool
	IngestPaused   bool
	IngestDone     int
	IngestTotal    int
	IngestMeta     string // "fetching", "starting", "3m ago", etc.
	IngestFailures int
	IngestCooling  int
	IngestNotDue   int
	ParsedTotal    int
	ParsedMedia    int
	ParsedText     int
	MediaReady     int
	MediaPending   int
	MediaFailed    int
	MediaTotal     int
	Activity       []FeedActivityEntry
	Sources        []FeedSourceEntry
	Filter         string
	TzOffsetSec    int
	XIngestStatus  string // "healthy", "failing", etc.
}

// FeedActivityEntry is a single feed activity log line.
type FeedActivityEntry struct {
	Timestamp int64
	Kind      string // "ingest", "ok", "error", "timeout", "media"
	Message   string
}

// FeedSourceEntry is a row in the feed sources diagnostics table.
type FeedSourceEntry struct {
	Handle         string
	Status         string // "ok", "cooling", "failing", "degraded", "unknown"
	DisplayStatus  string // relaxed status for display
	LastSuccessAt  int64
	ItemCount      int
	LastError      string
	LastHTTPStatus int
}

// FeedLogClass returns CSS class for a feed activity log line.
func FeedLogClass(kind string) string {
	return "log-feed-" + kind
}

// FeedSourceDotClass returns the CSS dot class for a source status.
func FeedSourceDotClass(status string) string {
	switch status {
	case "ok":
		return "ok"
	case "cooling", "pending":
		return "wn"
	default:
		return "er"
	}
}

// FeedSourceIsFailed returns true if the source is failing/degraded.
func FeedSourceIsFailed(status string) bool {
	return status == "failing" || status == "degraded"
}

// FeedSourceFilterMatch returns true if source matches the filter.
func FeedSourceFilterMatch(s FeedSourceEntry, filter string) bool {
	st := s.DisplayStatus
	switch filter {
	case "failed":
		return st == "failing" || st == "degraded"
	case "cooling":
		return st == "cooling"
	case "ok":
		return st == "ok"
	default:
		return true
	}
}

// FeedSourceFilterCount counts sources matching a filter.
func FeedSourceFilterCount(sources []FeedSourceEntry, filter string) int {
	if filter == "all" {
		return len(sources)
	}
	n := 0
	for _, s := range sources {
		if FeedSourceFilterMatch(s, filter) {
			n++
		}
	}
	return n
}

// FeedSourceShortError returns a truncated error message for display.
func FeedSourceShortError(s FeedSourceEntry) string {
	if !FeedSourceIsFailed(s.DisplayStatus) {
		return ""
	}
	if s.LastHTTPStatus != 0 && s.LastHTTPStatus != 500 {
		return fmt.Sprintf("%d", s.LastHTTPStatus)
	}
	if s.LastError != "" {
		if len(s.LastError) > 30 {
			return s.LastError[:30] + "\u2026"
		}
		return s.LastError
	}
	return ""
}

// TimeAgo formats a unix timestamp as relative time ("3m ago", "2h ago", etc.)
func TimeAgo(epoch int64) string {
	if epoch == 0 {
		return "\u2014"
	}
	sec := int(time.Now().Unix() - epoch)
	if sec < 0 {
		sec = 0
	}
	if sec < 60 {
		return fmt.Sprintf("%ds ago", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm ago", sec/60)
	}
	if sec < 86400 {
		return fmt.Sprintf("%dh ago", sec/3600)
	}
	return fmt.Sprintf("%dd ago", sec/86400)
}

func TimeAgoText(p PageProps, epoch int64) string {
	if epoch == 0 {
		return "\u2014"
	}
	t := time.Unix(epoch, 0)
	return RelativeTimeText(p, &t)
}

// LastOkClass returns a semantic status class if the last success is stale.
func LastOkClass(lastSuccessAt int64) string {
	if lastSuccessAt == 0 {
		return ""
	}
	ageMs := (time.Now().Unix() - lastSuccessAt) * 1000
	if ageMs > 12*3600*1000 {
		return "feed-last-ok-stale"
	}
	return ""
}

// dlFilterActiveClass returns " active" if the filter matches the current one.
func dlFilterActiveClass(current, key string) string {
	if current == key {
		return " active"
	}
	return ""
}

// dlFilterCount counts entries matching a filter.
func dlFilterCount(entries []ActivityEntry, filter string) int {
	if filter == "all" {
		return len(entries)
	}
	n := 0
	for _, e := range entries {
		if DlFilterMatch(e, filter) {
			n++
		}
	}
	return n
}

// intOrEmpty returns an int as a string, or empty string if zero.
func intOrEmpty(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

// translateBackendLabel returns the display label for a translate backend value.
func translateBackendLabel(val string) string {
	switch val {
	case "google":
		return "Google Cloud Translation"
	case "deepl":
		return "DeepL API"
	case "kagi_cli":
		return "Kagi CLI"
	default:
		return "Disabled"
	}
}

// customSelectOptionClass returns the CSS class for a custom-select-option.
func customSelectOptionClass(current, value string) string {
	if current == value {
		return "custom-select-option active"
	}
	return "custom-select-option"
}

// translateAPIConfigStyle returns display style for translate API config section.
func translateAPIConfigStyle(backend string) string {
	if backend == "google" || backend == "deepl" || backend == "openai_compat" {
		return ""
	}
	return "display:none;"
}

func translateModelConfigStyle(backend string) string {
	if backend == "openai_compat" {
		return ""
	}
	return "display:none;"
}

func translateAPIKeyHintStyle(backend string) string {
	if backend == "google" || backend == "deepl" {
		return ""
	}
	return "display:none;"
}

// translateLookaheadStyle returns display style for the lazy lookahead setting.
func translateLookaheadStyle(mode string) string {
	if settings.NormalizeTranslateAutoMode(mode) == settings.TranslateAutoLazy {
		return ""
	}
	return "display:none;"
}

// PrefsData holds the current settings for rendering the preferences form pre-filled.
type PrefsData struct {
	Settings map[string]any
}

// str returns a string setting value or the given fallback.
func (p PrefsData) Str(key, fallback string) string {
	if v, ok := p.Settings[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return fallback
}

// Bool returns true if the setting is truthy ("true", "1", bool true).
func (p PrefsData) Bool(key string) bool {
	v, ok := p.Settings[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true" || b == "1"
	}
	return false
}

// DearrowMode returns the user's normalized DeArrow mode: off|default|casual.
// Drives the video card + player renderers' choice between original and
// DeArrow-voted titles/thumbnails.
func (p PrefsData) DearrowMode() string {
	return settings.NormalizeDearrowMode(p.Str("dearrow_mode", "off"))
}

// VideoTitle returns the display title for v, respecting the user's DeArrow mode.
// Pure function — deterministic for a given (mode, v). Safe to call from templ.
func (p PrefsData) VideoTitle(v model.Video) string {
	if v.Title == "" {
		return fallbackVideoTitle(v)
	}
	return resolveDearrowTitle(p.DearrowMode(), v.Title, v.DearrowTitle, v.DearrowTitleCasual)
}

// VideoThumbURL returns the thumbnail URL for v, respecting DeArrow mode.
func (p PrefsData) VideoThumbURL(v model.Video) string {
	mode := p.DearrowMode()
	if mode == "off" || v.DearrowThumbPath == nil || *v.DearrowThumbPath == "" {
		if v.ThumbnailURL != "" {
			return v.ThumbnailURL
		}
		return "/api/media/thumbnail/" + v.VideoID
	}
	// Whatever URL is in v.ThumbnailURL (may include a query), append ?da=1.
	base := v.ThumbnailURL
	if base == "" {
		base = "/api/media/thumbnail/" + v.VideoID
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "da=1"
}

// fallbackVideoTitle replicates the existing videoTitle's behavior for the
// no-title case — description or video_id fallback.
func fallbackVideoTitle(v model.Video) string {
	if v.Title != "" {
		return v.Title
	}
	if v.Description != "" {
		return v.Description
	}
	if v.Platform == "youtube" {
		return v.VideoID
	}
	return ""
}

// resolveDearrowTitle is a package-local copy of web.ResolveDearrowTitle to
// avoid importing the web package from components (which would be a layering
// violation — components should be a leaf dep of web, not vice versa).
func resolveDearrowTitle(mode, original string, dearrow, dearrowCasual *string) string {
	if mode == "casual" {
		if dearrowCasual != nil && *dearrowCasual != "" {
			return *dearrowCasual
		}
	}
	if mode == "casual" || mode == "default" {
		if dearrow != nil && *dearrow != "" {
			return *dearrow
		}
	}
	return original
}

// SBVal returns the SponsorBlock action for a category from the sponsorblock_categories setting.
func (p PrefsData) SBVal(cat string) string {
	raw := p.Str("sponsorblock_categories", "")
	for _, entry := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
		if len(parts) == 2 && parts[0] == cat {
			return parts[1]
		}
	}
	return settings.SponsorBlockDefaultAction(cat)
}

// SkipLangs returns the ordered list of language codes to skip for auto-translate.
func (p PrefsData) SkipLangs() []string {
	raw := p.Str("translate_skip_langs", "")
	var result []string
	for _, lang := range strings.Split(raw, ",") {
		lang = strings.TrimSpace(lang)
		if lang != "" {
			result = append(result, lang)
		}
	}
	return result
}

// TargetLangOptions returns the deduplicated list of codes to show in the
// translation target dropdown: English first, then the user's skip-listed
// languages, plus the currently-selected target (in case it was removed
// from the skip list but is still stored).
func (p PrefsData) TargetLangOptions() []string {
	current := p.Str("translate_target_lang", "en")
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		c = strings.TrimSpace(strings.ToLower(c))
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		out = append(out, c)
	}
	add("en")
	for _, c := range p.SkipLangs() {
		add(c)
	}
	add(current)
	return out
}

// Shortcuts returns the shortcuts map from settings.
func (p PrefsData) Shortcuts() map[string]string {
	v, ok := p.Settings["shortcuts"]
	if !ok {
		return nil
	}
	switch m := v.(type) {
	case map[string]string:
		return m
	case map[string]any:
		result := make(map[string]string, len(m))
		for k, val := range m {
			result[k] = fmt.Sprintf("%v", val)
		}
		return result
	}
	return nil
}
