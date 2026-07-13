package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	ytdlp "github.com/lrstanley/go-ytdlp"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

const DefaultCommentFetchLimit = 500

// ChannelInfoResult holds the resolved channel identity from yt-dlp.
type ChannelInfoResult struct {
	ID   string
	Name string
	URL  string
}

// ChannelInfo fetches channel metadata for the given URL without downloading any
// media. It uses --flat-playlist and limits to one item to minimise latency.
func (y *YtDlpWrapper) ChannelInfo(ctx context.Context, url string) (ChannelInfoResult, error) {
	start := time.Now()
	result, err := ytdlp.New().
		FlatPlaylist().
		PlaylistItems("1:1").
		NoWarnings().
		DumpJSON().
		Run(ctx, url)
	if err != nil {
		y.recordYtDlpOperationWithCounts(ctx, "youtube.channel_info", url, start, err, Opts{}, 0, 0, 0)
		return ChannelInfoResult{}, fmt.Errorf("yt-dlp channel info: %w", err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil {
		y.recordYtDlpOperationWithCounts(ctx, "youtube.channel_info", url, start, err, Opts{}, 0, 0, 0)
		return ChannelInfoResult{}, fmt.Errorf("parse yt-dlp channel info: %w", err)
	}
	if len(infos) == 0 {
		y.recordYtDlpOperationWithCounts(ctx, "youtube.channel_info", url, start, fmt.Errorf("yt-dlp returned no info"), Opts{}, 0, 0, 0)
		return ChannelInfoResult{}, fmt.Errorf("yt-dlp returned no info for %s", url)
	}

	info := infos[0]
	var res ChannelInfoResult
	if info.ChannelID != nil {
		res.ID = *info.ChannelID
	}
	if res.ID == "" && info.UploaderID != nil {
		res.ID = *info.UploaderID
	}
	if info.Channel != nil {
		res.Name = *info.Channel
	}
	if res.Name == "" && info.Uploader != nil {
		res.Name = *info.Uploader
	}
	if info.ChannelURL != nil {
		res.URL = *info.ChannelURL
	}
	if res.URL == "" && info.UploaderURL != nil {
		res.URL = *info.UploaderURL
	}
	if res.URL == "" && info.WebpageURL != nil {
		res.URL = *info.WebpageURL
	}
	if isYouTubeURL(url) || isYouTubeURL(res.URL) {
		res.ID = CanonicalizeYouTubeChannelID(res.ID, res.URL, url)
		if res.URL == "" && strings.HasPrefix(res.ID, "youtube_UC") {
			res.URL = "https://www.youtube.com/channel/" + strings.TrimPrefix(res.ID, "youtube_")
		}
	}

	if res.ID == "" {
		y.recordYtDlpOperationWithCounts(ctx, "youtube.channel_info", url, start, fmt.Errorf("yt-dlp did not return a channel ID"), Opts{}, 1, 0, 0)
		return res, fmt.Errorf("yt-dlp did not return a channel ID for %s", url)
	}
	y.recordYtDlpOperationWithCounts(ctx, "youtube.channel_info", url, start, nil, Opts{}, 1, 0, 0)
	return res, nil
}

// CanonicalizeYouTubeChannelID normalizes yt-dlp's mixed YouTube identity
// outputs to the server's canonical youtube_UC... form when possible.
func CanonicalizeYouTubeChannelID(rawID string, urls ...string) string {
	id := strings.TrimSpace(strings.TrimPrefix(rawID, "youtube_"))
	if id == "" || id == "temp" {
		return rawID
	}
	if looksLikeYouTubeChannelID(id) {
		return "youtube_" + id
	}
	for _, candidate := range urls {
		if extracted := extractYouTubeChannelIDFromURL(candidate); extracted != "" {
			return "youtube_" + extracted
		}
	}
	id = strings.TrimPrefix(id, "@")
	if id == "" {
		return ""
	}
	return "youtube_" + id
}

func isYouTubeURL(raw string) bool {
	host, _, ok := httpURLParts(raw)
	return ok && hostMatches(host, "youtube.com", "youtu.be")
}

func looksLikeYouTubeChannelID(id string) bool {
	id = strings.TrimSpace(id)
	return strings.HasPrefix(id, "UC") && len(id) >= 10
}

func extractYouTubeChannelIDFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err == nil {
		pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i := 0; i+1 < len(pathParts); i++ {
			if pathParts[i] == "channel" && looksLikeYouTubeChannelID(pathParts[i+1]) {
				return pathParts[i+1]
			}
		}
		if id := u.Query().Get("channel_id"); looksLikeYouTubeChannelID(id) {
			return id
		}
	}
	return ""
}

// VideoRef holds a video ID and title from a channel check.
type VideoRef struct {
	VideoID             string
	Title               string
	Duration            int
	URL                 string
	ChannelID           string
	AuthorHandle        string
	AuthorDisplayName   string
	AuthorAvatarURL     string
	IsRepost            bool
	PublishedAtMs       int64
	ReposterChannelID   string
	ReposterHandle      string
	ReposterDisplayName string
	ReposterAvatarURL   string
	RepostedAtMs        int64
}

const (
	SourceComponentDirect = "direct"
	SourceComponentReels  = "reels"
	SourceComponentPosts  = "posts"
)

// SourceWindow keeps authority scoped to the producer surface that was
// actually observed. A failed sibling must not make a successful window
// non-authoritative.
type SourceWindow struct {
	Component string
	Refs      []VideoRef
	Complete  bool
}

type SourceSnapshot struct {
	Windows []SourceWindow
}

// FlattenRefs is for diagnostics that do not need component authority.
func (s SourceSnapshot) FlattenRefs(limit int) []VideoRef {
	var refs []VideoRef
	for _, window := range s.Windows {
		refs = append(refs, window.Refs...)
	}
	return mergeSourceRefs(refs, limit)
}

// ChannelCheck fetches recent video IDs from a channel URL.
// Returns up to limit VideoRef entries.
func (y *YtDlpWrapper) ChannelCheck(ctx context.Context, url string, limit int) (SourceSnapshot, error) {
	start := time.Now()
	snapshot := SourceSnapshot{Windows: []SourceWindow{{Component: SourceComponentDirect}}}
	result, err := ytdlp.New().
		SkipDownload().
		NoWarnings().
		PlaylistItems(fmt.Sprintf(":%d", limit)).
		DumpJSON().
		Run(ctx, url)
	if err != nil {
		// Try to parse partial results even on error
		if result == nil {
			y.recordYtDlpOperationWithCounts(ctx, "channel.check", url, start, err, Opts{}, 0, 0, 0)
			return snapshot, fmt.Errorf("yt-dlp channel check: %w", err)
		}
	}

	infos, parseErr := result.GetExtractedInfo()
	if parseErr != nil {
		if err != nil {
			y.recordYtDlpOperationWithCounts(ctx, "channel.check", url, start, err, Opts{}, 0, 0, 0)
			return snapshot, fmt.Errorf("yt-dlp channel check: %w", err)
		}
		y.recordYtDlpOperationWithCounts(ctx, "channel.check", url, start, parseErr, Opts{}, 0, 0, 0)
		return snapshot, fmt.Errorf("parse yt-dlp channel check: %w", parseErr)
	}

	var refs []VideoRef
	for _, info := range infos {
		var r VideoRef
		r.VideoID = info.ID
		if r.VideoID == "" {
			continue
		}
		if info.Title != nil {
			r.Title = *info.Title
		}
		if info.Duration != nil {
			r.Duration = int(*info.Duration)
		}
		if info.Timestamp != nil {
			r.PublishedAtMs = int64(*info.Timestamp * 1000)
		}
		refs = append(refs, r)
	}
	y.recordYtDlpOperationWithCounts(ctx, "channel.check", url, start, err, Opts{}, len(refs), 0, 0)
	snapshot.Windows[0].Refs = refs
	snapshot.Windows[0].Complete = err == nil
	if err != nil {
		return snapshot, fmt.Errorf("yt-dlp channel check: %w", err)
	}
	return snapshot, nil
}

func firstOpts(opts []Opts) Opts {
	if len(opts) == 0 {
		return Opts{}
	}
	return opts[0]
}

func applyCookieAuth(cmd *ytdlp.Command, opts Opts) *ytdlp.Command {
	if opts.Cookies != "" {
		return cmd.Cookies(opts.Cookies)
	}
	if opts.CookiesFromBrowser != "" {
		return cmd.CookiesFromBrowser(opts.CookiesFromBrowser)
	}
	return cmd
}

func fetchInfoCommand(opts Opts) *ytdlp.Command {
	return applyCookieAuth(ytdlp.New().
		SkipDownload().
		NoWarnings().
		NoPlaylist().
		DumpJSON(), opts)
}

func commentExtractorArgs(maxComments int) string {
	if maxComments <= 0 {
		maxComments = DefaultCommentFetchLimit
	}
	maxRepliesPerThread := 100
	if maxComments < maxRepliesPerThread {
		maxRepliesPerThread = maxComments
	}
	return fmt.Sprintf("youtube:max_comments=%d,%d,%d,%d", maxComments, maxComments, maxComments, maxRepliesPerThread)
}

func fetchCommentsCommand(maxComments int, opts Opts) *ytdlp.Command {
	return applyCookieAuth(ytdlp.New().
		SkipDownload().
		NoWarnings().
		NoPlaylist().
		WriteComments().
		ExtractorArgs(commentExtractorArgs(maxComments)).
		DumpJSON(), opts)
}

// FetchInfo fetches metadata for a single URL without downloading.
func (y *YtDlpWrapper) FetchInfo(ctx context.Context, url string, opts ...Opts) (map[string]any, error) {
	start := time.Now()
	opt := firstOpts(opts)
	result, err := fetchInfoCommand(opt).Run(ctx, url)
	if err != nil {
		y.recordYtDlpOperationWithCounts(ctx, "youtube.info", url, start, err, opt, 0, 0, 0)
		return nil, fmt.Errorf("yt-dlp info: %w", err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil || len(infos) == 0 {
		if err == nil {
			err = fmt.Errorf("yt-dlp info: no results")
		}
		y.recordYtDlpOperationWithCounts(ctx, "youtube.info", url, start, err, opt, 0, 0, 0)
		return nil, fmt.Errorf("yt-dlp info: no results")
	}

	// Convert to map via JSON round-trip.
	data, err := json.Marshal(infos[0])
	if err != nil {
		return nil, err
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	y.recordYtDlpOperationWithCounts(ctx, "youtube.info", url, start, nil, opt, 1, 0, 0)
	return m, nil
}

func fetchPlaylistInfoCommand(opts Opts) *ytdlp.Command {
	return applyCookieAuth(ytdlp.New().
		FlatPlaylist().
		SkipDownload().
		NoWarnings().
		DumpJSON(), opts)
}

// FetchPlaylistInfo fetches playlist metadata without downloading.
func (y *YtDlpWrapper) FetchPlaylistInfo(ctx context.Context, url string, opts ...Opts) (map[string]any, error) {
	start := time.Now()
	opt := firstOpts(opts)
	result, err := fetchPlaylistInfoCommand(opt).Run(ctx, url)
	if err != nil {
		y.recordYtDlpOperationWithCounts(ctx, "youtube.playlist_info", url, start, err, opt, 0, 0, 0)
		return nil, fmt.Errorf("yt-dlp playlist info: %w", err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil || len(infos) == 0 {
		if err == nil {
			err = fmt.Errorf("yt-dlp playlist info: no results")
		}
		y.recordYtDlpOperationWithCounts(ctx, "youtube.playlist_info", url, start, err, opt, 0, 0, 0)
		return nil, fmt.Errorf("yt-dlp playlist info: no results")
	}

	data, err := json.Marshal(infos[0])
	if err != nil {
		return nil, err
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	y.recordYtDlpOperationWithCounts(ctx, "youtube.playlist_info", url, start, nil, opt, 1, 0, 0)
	return m, nil
}

// YtDlpWrapper wraps the go-ytdlp library for downloading videos.
type YtDlpWrapper struct {
	// CookiesDir is a directory containing cookies files (e.g. cookies.txt).
	// When Opts.Cookies is set, it is used as the cookies file path directly.
	CookiesDir    string
	OperationSink OperationSink
}

// Download returns only the media paths from DownloadCompleted.
func (y *YtDlpWrapper) Download(ctx context.Context, url string, opts Opts) ([]string, error) {
	completed, err := y.DownloadCompleted(ctx, url, opts)
	return completed.MediaPaths, err
}

// DownloadCompleted returns every exact output owned by this yt-dlp run.
func (y *YtDlpWrapper) DownloadCompleted(ctx context.Context, url string, opts Opts) (CompletedDownload, error) {
	start := time.Now()
	// Output template: {outputDir}/{id}.%(ext)s
	// If the caller provided an ID, use it; otherwise let yt-dlp pick.
	template := fmt.Sprintf("%s/%%(id)s.%%(ext)s", opts.OutputDir)
	if opts.ID != "" {
		template = fmt.Sprintf("%s/%s.%%(ext)s", opts.OutputDir, sanitizeDownloadID(opts.ID))
	}

	cmd := ytdlp.New().
		Output(template).
		NoPlaylist().
		PrintJSON().
		WriteInfoJSON().
		WriteThumbnail().
		ConvertThumbnails("jpg")

	if opts.Format != "" {
		cmd = cmd.Format(opts.Format)
	} else {
		cmd = cmd.FormatSort("res,ext:mp4:m4a")
	}
	cmd = cmd.MergeOutputFormat("mp4")

	// Subtitles are fetched in a separate yt-dlp pass below — bundling them
	// into the main download lets a transient subtitle error (e.g. YouTube
	// 429) abort the video download entirely.

	cmd = applyCookieAuth(cmd, opts)

	paths, err := runVideoDownload(ctx, cmd, url)
	files, bytes := summarizePaths(paths)
	y.recordYtDlpOperationWithCounts(ctx, "media.ytdlp", url, start, err, opts, 0, files, bytes)
	if err != nil {
		return completedYtDlpOutputs(opts, paths), WithOperationContext(err, "yt-dlp", CookieLabel(opts.Cookies, opts.CookiesFromBrowser))
	}

	completed := completedYtDlpOutputs(opts, paths)
	if opts.Subtitles && len(paths) > 0 {
		subtitlePaths, subtitleErr := y.DownloadSubtitles(ctx, url, opts)
		if subtitleErr != nil {
			log.Printf("[ytdlp] subtitle fetch %s: %v", opts.ID, subtitleErr)
		} else {
			completed.SubtitlePaths = subtitlePaths
		}
	}

	return completed, nil
}

func completedYtDlpOutputs(opts Opts, paths []string) CompletedDownload {
	completed := CompletedDownload{MediaPaths: uniqueRegularPaths(paths)}
	base := completedOutputBase(opts, completed.MediaPaths)
	if base == "" {
		return completed
	}
	completed.InfoJSONPath = regularPath(base + ".info.json")
	completed.ThumbnailPath = regularPath(base + ".jpg")
	return completed
}

func completedOutputBase(opts Opts, paths []string) string {
	if opts.ID != "" {
		return filepath.Join(opts.OutputDir, sanitizeDownloadID(opts.ID))
	}
	if len(paths) == 0 {
		return ""
	}
	return strings.TrimSuffix(paths[0], filepath.Ext(paths[0]))
}

// runVideoDownload executes the main yt-dlp download and extracts output paths,
// with fallbacks for non-fatal exit codes and schema-mismatch parse errors.
func runVideoDownload(ctx context.Context, cmd *ytdlp.Command, url string) ([]string, error) {
	result := CommandRunner{}.RunBuilt(ctx, cmd.BuildCommand(ctx, url))
	paths := extractFilenamesFromJSON(result.Stdout)
	if result.Err != nil {
		// yt-dlp may exit non-zero for non-fatal errors while the video
		// was written. Try to salvage filenames if the files exist.
		var existing []string
		for _, p := range paths {
			if fi, statErr := os.Stat(p); statErr == nil && fi.Size() > 0 {
				existing = append(existing, p)
			}
		}
		if len(existing) > 0 {
			return existing, nil
		}
		return nil, fmt.Errorf("yt-dlp: %w: %s", result.Err, strings.TrimSpace(string(result.Stderr)))
	}
	return paths, nil
}

func extractFilenamesFromJSON(output []byte) []string {
	var paths []string
	for _, payload := range JSONPayloads(output) {
		for _, raw := range FlattenJSONObjects(payload) {
			if filename, _ := raw["filename"].(string); filename != "" {
				paths = append(paths, filename)
			}
		}
	}
	return paths
}

// DownloadSubtitles runs a skip-download pass and returns the exact VTT files
// produced by that invocation. Callers can publish these without inspecting
// the destination directory or deriving siblings from a video path.
func (y *YtDlpWrapper) DownloadSubtitles(ctx context.Context, url string, opts Opts) ([]string, error) {
	subtitleDir := strings.TrimSpace(opts.SubtitleDir)
	if subtitleDir == "" || opts.ID == "" {
		return nil, fmt.Errorf("subtitle download requires an explicit state directory and output id")
	}
	if err := os.MkdirAll(subtitleDir, 0o755); err != nil {
		return nil, err
	}
	outputPath := filepath.Join(subtitleDir, sanitizeDownloadID(opts.ID)+".en.vtt")
	tmpDir, err := os.MkdirTemp(subtitleDir, ".subtitle-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	template := filepath.Join(tmpDir, "subtitle.%(ext)s")
	cmd := ytdlp.New().
		Output(template).
		NoPlaylist().
		SkipDownload().
		WriteSubs().
		WriteAutoSubs().
		SubLangs("en").
		SubFormat("vtt")

	cmd = applyCookieAuth(cmd, opts)

	if _, err := cmd.Run(ctx, url); err != nil {
		return nil, err
	}
	tmpPath := regularPath(filepath.Join(tmpDir, "subtitle.en.vtt"))
	if tmpPath == "" {
		return nil, fmt.Errorf("yt-dlp produced no English VTT for %s", opts.ID)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return nil, err
	}
	return []string{outputPath}, nil
}

// extractFilenamesFromRaw parses filenames from raw yt-dlp JSON output logs.
// Used as a fallback when GetExtractedInfo fails due to schema mismatches
// in fields we don't need (e.g. requested_subtitles format changes).
func extractFilenamesFromRaw(result *ytdlp.Result) []string {
	var paths []string
	for _, log := range result.OutputLogs {
		if log.JSON == nil {
			continue
		}
		var raw struct {
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(*log.JSON, &raw); err == nil && raw.Filename != "" {
			paths = append(paths, raw.Filename)
		}
	}
	return paths
}

// FetchComments fetches comments for a URL via yt-dlp without re-downloading media.
// Returns up to maxComments comments mapped to CommentInput for DB insertion.
func (y *YtDlpWrapper) FetchComments(ctx context.Context, url string, maxComments int, opts Opts) ([]db.CommentInput, error) {
	start := time.Now()
	result, err := fetchCommentsCommand(maxComments, opts).Run(ctx, url)
	if err != nil {
		y.recordYtDlpOperationWithCounts(ctx, "youtube.comments", url, start, err, opts, 0, 0, 0)
		return nil, fmt.Errorf("yt-dlp comments: %w", err)
	}

	infos, err := result.GetExtractedInfo()
	if err != nil || len(infos) == 0 {
		if err == nil {
			err = fmt.Errorf("yt-dlp comments: no results")
		}
		y.recordYtDlpOperationWithCounts(ctx, "youtube.comments", url, start, err, opts, 0, 0, 0)
		return nil, fmt.Errorf("yt-dlp comments: no results")
	}

	var out []db.CommentInput
	for _, c := range infos[0].Comments {
		if c == nil {
			continue
		}
		ci := db.CommentInput{}
		if c.ID != nil {
			ci.CommentID = *c.ID
		}
		if c.Text != nil {
			ci.Text = *c.Text
		}
		if c.Author != nil {
			ci.Author = *c.Author
		}
		if c.AuthorID != nil {
			ci.AuthorID = *c.AuthorID
		}
		if c.AuthorThumbnail != nil {
			ci.AuthorThumbnail = *c.AuthorThumbnail
		}
		if c.Timestamp != nil {
			ci.Timestamp = int64(*c.Timestamp)
		}
		if c.LikeCount != nil {
			ci.LikeCount = int(*c.LikeCount)
		}
		if c.Parent != nil && *c.Parent != "root" {
			ci.ParentID = *c.Parent
		}
		if ci.CommentID == "" || ci.Text == "" {
			continue
		}
		out = append(out, ci)
	}
	y.recordYtDlpOperationWithCounts(ctx, "youtube.comments", url, start, nil, opts, len(out), 0, 0)
	return out, nil
}

func (y *YtDlpWrapper) recordYtDlpOperationWithCounts(ctx context.Context, operation, url string, start time.Time, err error, opts Opts, items, files int, bytes int64) {
	recordOperation(ctx, y.OperationSink, model.DownloaderOperation{
		Operation:   operation,
		Platform:    platformFromURL(url),
		Subject:     subjectForURL(url),
		Tool:        "yt-dlp",
		StartedAtMs: start.UnixMilli(),
		EndedAtMs:   time.Now().UnixMilli(),
		Status:      statusForError(err),
		ErrorKind:   ClassifyFailure(err, nil, 0).Kind,
		Error:       errorString(err, nil),
		CookieLabel: CookieLabel(opts.Cookies, opts.CookiesFromBrowser),
		ItemCount:   items,
		FileCount:   files,
		MediaCount:  files,
		Bytes:       bytes,
	})
}

// ParseCommentsDumpJSON maps yt-dlp --dump-json output into DB comment rows.
// yt-dlp can emit one JSON object per line; each object may carry comments.
func ParseCommentsDumpJSON(output []byte) ([]db.CommentInput, error) {
	var out []db.CommentInput
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(output)))
	for {
		var info struct {
			Comments []map[string]any `json:"comments"`
		}
		if err := decoder.Decode(&info); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		for _, ce := range info.Comments {
			ci := db.CommentInput{
				CommentID:       stringFromCommentField(ce["id"]),
				Author:          stringFromCommentField(ce["author"]),
				AuthorID:        stringFromCommentField(ce["author_id"]),
				AuthorThumbnail: stringFromCommentField(ce["author_thumbnail"]),
				Text:            stringFromCommentField(ce["text"]),
				ParentID:        stringFromCommentField(ce["parent"]),
				LikeCount:       intFromCommentField(ce["like_count"]),
				Timestamp:       int64FromCommentField(ce["timestamp"]),
			}
			if ci.ParentID == "root" {
				ci.ParentID = ""
			}
			if ci.CommentID == "" || ci.CommentID == "<nil>" || ci.Text == "" {
				continue
			}
			out = append(out, ci)
		}
	}
	return out, nil
}

func stringFromCommentField(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func intFromCommentField(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	default:
		return 0
	}
}

func int64FromCommentField(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case json.Number:
		n, _ := x.Int64()
		return n
	default:
		return 0
	}
}
