package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/download"
	"github.com/screwys/igloo/internal/fetchprofile"
	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/xfeed"
)

type downloaderReport struct {
	StartedAtMs int64                       `json:"started_at_ms"`
	EndedAtMs   int64                       `json:"ended_at_ms"`
	TempDir     string                      `json:"temp_dir"`
	Status      string                      `json:"status"`
	Rows        []downloaderReportRow       `json:"rows"`
	Operations  []model.DownloaderOperation `json:"operations"`
}

type downloaderReportRow struct {
	Name      string         `json:"name"`
	Platform  string         `json:"platform"`
	Operation string         `json:"operation"`
	Status    string         `json:"status"`
	ErrorKind string         `json:"error_kind,omitempty"`
	Error     string         `json:"error,omitempty"`
	Artifacts map[string]any `json:"artifacts,omitempty"`
	StartedMs int64          `json:"started_ms"`
	ElapsedMs int64          `json:"elapsed_ms"`
}

type reportVideoSample struct {
	Platform   string
	VideoID    string
	ChannelID  string
	SourceID   string
	ChannelURL string
}

type reportFeedSample struct {
	TweetID      string
	Author       string
	Source       string
	CanonicalURL string
}

func (s *Server) registerDownloaderReportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/downloader/report/run", s.handleDownloaderReportRun)
	mux.HandleFunc("GET /api/downloader/report/latest", s.handleDownloaderReportLatest)
	mux.HandleFunc("GET /api/downloader/operations", s.handleDownloaderOperations)
}

func (s *Server) handlePageDownloaderReport(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	props := s.pageProps(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="csrf-token" content="%s">
<title>Downloader Report</title>
<style>
body{font-family:system-ui,sans-serif;margin:24px;background:#101418;color:#ecf1f4}
button{background:#f0f4f8;color:#111820;border:0;border-radius:6px;padding:8px 12px;font-weight:650}
table{width:100%%;border-collapse:collapse;margin-top:18px;font-size:14px}
th,td{border-bottom:1px solid #2a333b;padding:8px;text-align:left;vertical-align:top}
.pass{color:#88d498}.fail{color:#ff8b8b}.skip{color:#c9b46b}pre{white-space:pre-wrap;background:#161c22;padding:12px;border-radius:6px}
</style></head>
<body>
<h1>Downloader Report</h1>
<button id="run">Run Temp Demo</button>
<span id="state"></span>
<table><thead><tr><th>Status</th><th>Platform</th><th>Check</th><th>Artifacts</th><th>Error</th></tr></thead><tbody id="rows"></tbody></table>
<pre id="raw"></pre>
	<script>
	const token=document.querySelector('meta[name="csrf-token"]').content;
	const rows=document.getElementById('rows'), raw=document.getElementById('raw'), state=document.getElementById('state');
	function appendCell(tr,text,className){
	 const td=document.createElement('td');
	 if(className) td.className=className;
	 td.textContent=text||'';
	 tr.appendChild(td);
	 return td;
	}
	function render(report){
	 rows.replaceChildren();
	 (report.rows||[]).forEach(r=>{
	  const tr=document.createElement('tr');
	  appendCell(tr,r.status||'',r.status||'');
	  appendCell(tr,r.platform||'');
	  appendCell(tr,r.name||'');
	  const artifactsCell=document.createElement('td');
	  const artifactsPre=document.createElement('pre');
	  artifactsPre.textContent=JSON.stringify(r.artifacts||{},null,2);
	  artifactsCell.appendChild(artifactsPre);
	  tr.appendChild(artifactsCell);
	  appendCell(tr,[(r.error_kind||''),(r.error||'')].filter(Boolean).join(' '));
	  rows.appendChild(tr);
	 });
	 raw.textContent=JSON.stringify(report,null,2);
	}
async function latest(){const res=await fetch('/api/downloader/report/latest'); if(res.ok) render(await res.json());}
document.getElementById('run').onclick=async()=>{state.textContent=' running'; const res=await fetch('/api/downloader/report/run',{method:'POST',headers:{'X-CSRF-Token':token}}); const data=await res.json(); state.textContent=' '+(data.status||'done'); render(data);};
latest();
</script></body></html>`, template.HTMLEscapeString(props.CSRFToken))
}

func (s *Server) handleDownloaderReportRun(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()
	report := s.runDownloaderReport(ctx)
	s.downloaderReportMu.Lock()
	s.downloaderReportLatest = &report
	s.downloaderReportMu.Unlock()
	writeAnyJSON(w, 200, report)
}

func (s *Server) handleDownloaderReportLatest(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	s.downloaderReportMu.Lock()
	report := s.downloaderReportLatest
	s.downloaderReportMu.Unlock()
	if report == nil {
		writeJSON(w, 200, map[string]any{"status": "none", "rows": []any{}})
		return
	}
	writeAnyJSON(w, 200, report)
}

func (s *Server) handleDownloaderOperations(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ops, err := s.db.ListDownloaderOperations(200)
	if err != nil {
		writeJSONError(w, 500, "operations_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"operations": ops})
}

func (s *Server) runDownloaderReport(ctx context.Context) downloaderReport {
	start := time.Now()
	tempDir, err := s.createDownloaderReportDir()
	report := downloaderReport{StartedAtMs: start.UnixMilli(), TempDir: tempDir, Status: "pass"}
	if err != nil {
		report.Status = "fail"
		report.Rows = append(report.Rows, downloaderReportRow{Name: "create temp dir", Status: "fail", ErrorKind: download.ErrorKindTemporary, Error: err.Error(), StartedMs: start.UnixMilli()})
		return report
	}
	if s.workers == nil {
		report.Status = "fail"
		report.Rows = append(report.Rows, downloaderReportRow{Name: "downloader availability", Status: "fail", ErrorKind: download.ErrorKindUnknown, Error: "worker manager is not configured", StartedMs: time.Now().UnixMilli()})
		report.EndedAtMs = time.Now().UnixMilli()
		return report
	}
	dl := s.workers.Downloader()
	if dl == nil {
		report.Status = "fail"
		report.Rows = append(report.Rows, downloaderReportRow{Name: "downloader availability", Status: "fail", ErrorKind: download.ErrorKindUnknown, Error: "downloader is not configured", StartedMs: time.Now().UnixMilli()})
		report.EndedAtMs = time.Now().UnixMilli()
		return report
	}

	s.addYouTubeReportRows(ctx, dl, tempDir, &report)
	s.addXReportRows(ctx, &report)
	s.addTikTokReportRows(ctx, dl, tempDir, &report)
	s.addInstagramReportRows(ctx, dl, tempDir, &report)

	if ops, err := s.db.ListDownloaderOperations(100); err == nil {
		report.Operations = ops
	}
	report.EndedAtMs = time.Now().UnixMilli()
	for _, row := range report.Rows {
		if row.Status == "fail" {
			report.Status = "fail"
			break
		}
	}
	return report
}

func (s *Server) createDownloaderReportDir() (string, error) {
	baseDir := filepath.Join(os.TempDir(), "igloo", "downloader-reports")
	if s != nil && s.cfg != nil && strings.TrimSpace(s.cfg.Storage.StateRoot()) != "" {
		baseDir = filepath.Join(s.cfg.Storage.StateRoot(), "tmp", "downloader-reports")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	pruneDownloaderReportDirs(baseDir, 3, 24*time.Hour)
	return os.MkdirTemp(baseDir, "run-*")
}

func pruneDownloaderReportDirs(baseDir string, keep int, maxAge time.Duration) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return
	}
	type reportDir struct {
		path    string
		modTime time.Time
	}
	var dirs []reportDir
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "run-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, reportDir{
			path:    filepath.Join(baseDir, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].modTime.After(dirs[j].modTime)
	})
	cutoff := time.Now().Add(-maxAge)
	for i, dir := range dirs {
		if i < keep && dir.modTime.After(cutoff) {
			continue
		}
		_ = os.RemoveAll(dir.path)
	}
}

func (s *Server) addYouTubeReportRows(ctx context.Context, dl *download.Downloader, tempDir string, report *downloaderReport) {
	sample := s.reportVideoSample("youtube")
	if sample.VideoID == "" {
		report.Rows = append(report.Rows, skipRow("youtube metadata", "youtube", "youtube.info", "no local YouTube video sample"))
		return
	}
	rawURL := "https://www.youtube.com/watch?v=" + sample.VideoID
	opts := s.reportDownloadOpts("youtube", filepath.Join(tempDir, "youtube"), "youtube-demo")
	row := s.runReportCheck(ctx, "youtube metadata", "youtube", "youtube.info", func(ctx context.Context) (map[string]any, error) {
		info, err := dl.YtDlp.FetchInfo(ctx, rawURL, opts)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"url":                rawURL,
			"title":              stringField(info, "title") != "",
			"description":        stringField(info, "description") != "",
			"channel_avatar_url": stringField(info, "channel_thumbnail") != "" || stringField(info, "thumbnail") != "",
			"channel_banner_url": findThumbnailURL(info, 1000) != "",
		}, nil
	})
	report.Rows = append(report.Rows, row)
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "youtube comments", "youtube", "youtube.comments", func(ctx context.Context) (map[string]any, error) {
		comments, err := dl.YtDlp.FetchComments(ctx, rawURL, 20, opts)
		if err != nil {
			return nil, err
		}
		thumbs := 0
		for _, c := range comments {
			if c.AuthorThumbnail != "" {
				thumbs++
			}
		}
		return map[string]any{"comment_count": len(comments), "author_thumbnail_count": thumbs}, nil
	}))
	if strings.HasPrefix(sample.ChannelID, "youtube_") {
		rawID := strings.TrimPrefix(sample.ChannelID, "youtube_")
		report.Rows = append(report.Rows, s.runReportCheck(ctx, "youtube profile", "youtube", "youtube.profile", func(ctx context.Context) (map[string]any, error) {
			p, err := fetchprofile.FetchYouTube(ctx, rawID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"display_name": p.DisplayName != "", "avatar_url": p.AvatarURL != "", "banner_url": p.BannerURL != ""}, nil
		}))
	}
}

func (s *Server) addXReportRows(ctx context.Context, report *downloaderReport) {
	sample := s.reportFeedSample()
	handle := sample.Author
	if handle == "" {
		handle = sample.Source
	}
	if handle == "" {
		report.Rows = append(report.Rows, skipRow("x timeline/status", "twitter", "x.gallerydl.dump", "no local X feed sample"))
		return
	}
	cookiesDir := ""
	if s.cfg != nil {
		cookiesDir = s.cfg.CookiesDir
	}
	client := xfeed.NewClient(cookiesDir)
	client.OperationSink = s.db
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "x timeline/status", "twitter", "x.gallerydl.dump", func(ctx context.Context) (map[string]any, error) {
		items, err := client.FetchTimeline(ctx, handle, 10)
		if err != nil {
			return nil, err
		}
		parentMedia, quoteMedia, replies, retweets, quotes := 0, 0, 0, 0, 0
		for _, item := range items {
			if item.MediaJSON != "" {
				parentMedia++
			}
			if item.QuoteMediaJSON != "" {
				quoteMedia++
			}
			if item.IsReply {
				replies++
			}
			if item.IsRetweet {
				retweets++
			}
			if item.QuoteTweetID != "" {
				quotes++
			}
		}
		return map[string]any{"item_count": len(items), "parent_media": parentMedia, "quote_media": quoteMedia, "replies": replies, "retweets": retweets, "quotes": quotes}, nil
	}))
}

func (s *Server) addTikTokReportRows(ctx context.Context, dl *download.Downloader, tempDir string, report *downloaderReport) {
	sample := s.reportVideoSample("tiktok")
	if sample.SourceID == "" {
		report.Rows = append(report.Rows, skipRow("tiktok post/repost/story/media", "tiktok", "tiktok.dump", "no local TikTok channel sample"))
		return
	}
	handle := strings.TrimPrefix(sample.SourceID, "@")
	opts := s.reportDownloadOpts("tiktok", filepath.Join(tempDir, "tiktok"), "tiktok-demo")
	var rawURL string
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "tiktok source window", "tiktok", "tiktok.channel_check", func(ctx context.Context) (map[string]any, error) {
		channelURL := sample.ChannelURL
		if channelURL == "" {
			channelURL = "https://www.tiktok.com/@" + handle
		}
		refs, err := dl.YtDlp.ChannelCheck(ctx, channelURL, 5)
		if len(refs) > 0 {
			if refs[0].URL != "" {
				rawURL = refs[0].URL
			} else if refs[0].VideoID != "" {
				rawURL = fmt.Sprintf("https://www.tiktok.com/@%s/video/%s", handle, refs[0].VideoID)
			}
		}
		return map[string]any{"item_count": len(refs), "media_candidate": rawURL != ""}, err
	}))
	if rawURL == "" && sample.VideoID != "" {
		videoID := strings.TrimPrefix(sample.VideoID, "tiktok_story_")
		rawURL = fmt.Sprintf("https://www.tiktok.com/@%s/video/%s", handle, videoID)
	}
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "tiktok media download", "tiktok", "media.download", func(ctx context.Context) (map[string]any, error) {
		if rawURL == "" {
			return map[string]any{"file_count": 0, "bytes": 0}, fmt.Errorf("no TikTok post URL candidate")
		}
		paths, err := dl.Download(ctx, rawURL, "video", opts)
		if err != nil {
			return nil, err
		}
		files, bytes := reportPathStats(paths)
		return map[string]any{"url": rawURL, "file_count": files, "bytes": bytes}, nil
	}))
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "tiktok repost dump", "tiktok", "tiktok.reposts", func(ctx context.Context) (map[string]any, error) {
		refs, err := dl.GalleryDL.Reposts(ctx, handle, 10, opts.Cookies)
		return map[string]any{"item_count": len(refs)}, err
	}))
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "tiktok story dump", "tiktok", "tiktok.stories", func(ctx context.Context) (map[string]any, error) {
		storyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		refs, err := dl.GalleryDL.TikTokStories(storyCtx, handle, 10, opts.Cookies)
		artifacts := map[string]any{"item_count": len(refs)}
		if err != nil {
			artifacts["classified_error_kind"] = download.ClassifyError(err, nil)
			artifacts["classified_error"] = download.RedactText(err.Error())
			return artifacts, nil
		}
		return artifacts, nil
	}))
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "tiktok profile", "tiktok", "tiktok.profile", func(ctx context.Context) (map[string]any, error) {
		p, err := fetchprofile.FetchTikTok(ctx, handle)
		if err != nil {
			return nil, err
		}
		return map[string]any{"display_name": p.DisplayName != "", "avatar_url": p.AvatarURL != "", "verified": p.Verified}, nil
	}))
}

func (s *Server) addInstagramReportRows(ctx context.Context, dl *download.Downloader, tempDir string, report *downloaderReport) {
	sample := s.reportVideoSample("instagram")
	if sample.SourceID == "" {
		report.Rows = append(report.Rows, skipRow("instagram post/reel/story/media", "instagram", "instagram.dump", "no local Instagram channel sample"))
		return
	}
	handle := strings.TrimPrefix(sample.SourceID, "@")
	opts := s.reportDownloadOpts("instagram", filepath.Join(tempDir, "instagram"), "instagram-demo")
	var rawURL string
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "instagram post/reel dump", "instagram", "instagram.dump", func(ctx context.Context) (map[string]any, error) {
		refs, err := dl.GalleryDL.InstagramChannel(ctx, handle, 10, opts.Cookies, opts.CookiesFromBrowser)
		for _, ref := range refs {
			if ref.URL != "" {
				rawURL = ref.URL
				break
			}
			if ref.VideoID != "" {
				rawURL = instagramReportURL(ref.VideoID)
				break
			}
		}
		return map[string]any{"item_count": len(refs)}, err
	}))
	if rawURL == "" && sample.VideoID != "" {
		rawURL = instagramReportURL(sample.VideoID)
	}
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "instagram media download", "instagram", "media.download", func(ctx context.Context) (map[string]any, error) {
		if rawURL == "" {
			return map[string]any{"file_count": 0, "bytes": 0}, fmt.Errorf("no Instagram post URL candidate")
		}
		paths, err := dl.Download(ctx, rawURL, "video", opts)
		if err != nil {
			return nil, err
		}
		files, bytes := reportPathStats(paths)
		return map[string]any{"url": rawURL, "file_count": files, "bytes": bytes}, nil
	}))
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "instagram story dump", "instagram", "instagram.stories", func(ctx context.Context) (map[string]any, error) {
		refs, err := dl.GalleryDL.InstagramStories(ctx, handle, 10, opts.Cookies, opts.CookiesFromBrowser)
		return map[string]any{"item_count": len(refs)}, err
	}))
	report.Rows = append(report.Rows, s.runReportCheck(ctx, "instagram profile", "instagram", "instagram.profile", func(ctx context.Context) (map[string]any, error) {
		p, err := dl.GalleryDL.InstagramProfile(ctx, handle, opts.Cookies, opts.CookiesFromBrowser)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, fmt.Errorf("instagram profile returned no profile")
		}
		return map[string]any{"display_name": p.DisplayName != "", "avatar_url": p.AvatarURL != "", "bio": p.Bio != ""}, nil
	}))
}

func (s *Server) runReportCheck(ctx context.Context, name, platform, operation string, fn func(context.Context) (map[string]any, error)) downloaderReportRow {
	start := time.Now()
	artifacts, err := fn(ctx)
	row := downloaderReportRow{Name: name, Platform: platform, Operation: operation, Status: "pass", Artifacts: artifacts, StartedMs: start.UnixMilli(), ElapsedMs: time.Since(start).Milliseconds()}
	if err != nil {
		row.Status = "fail"
		row.ErrorKind = download.ClassifyError(err, nil)
		row.Error = download.RedactText(err.Error())
	}
	return row
}

func skipRow(name, platform, operation, reason string) downloaderReportRow {
	return downloaderReportRow{Name: name, Platform: platform, Operation: operation, Status: "skip", Error: reason, StartedMs: time.Now().UnixMilli()}
}

func (s *Server) reportDownloadOpts(platform, outputDir, id string) download.Opts {
	fileEnabled := "1"
	if s.db != nil {
		fileEnabled, _ = s.db.GetSetting("cookies_"+platform+"_enabled", "1")
	}
	browser := ""
	if s.db != nil {
		browser, _ = s.db.GetSetting("cookies_"+platform+"_browser", "")
	}
	cookiesDir := ""
	if s.cfg != nil {
		cookiesDir = s.cfg.CookiesDir
	}
	sets := download.ResolveCookieSets(cookiesDir, platform, fileEnabled != "0", browser)
	cookiesFile, cookiesBrowser := download.CookieFileAndBrowser(sets)
	_ = os.MkdirAll(outputDir, 0o755)
	return download.Opts{OutputDir: outputDir, ID: id, Cookies: cookiesFile, CookiesFromBrowser: cookiesBrowser, CookieAlternates: sets}
}

func (s *Server) reportVideoSample(platform string) reportVideoSample {
	var sample reportVideoSample
	if s.db == nil {
		return sample
	}
	_ = s.db.WithRead(func(conn *sql.DB) error {
		row := conn.QueryRow(`
			SELECT v.video_id, v.channel_id, COALESCE(c.source_id, ''), COALESCE(c.url, '')
			FROM videos v
			JOIN channels c ON c.channel_id = v.channel_id
			WHERE c.platform = ? AND COALESCE(v.is_temp, 0) = 0
			ORDER BY v.downloaded_at DESC, v.id DESC
			LIMIT 1`, platform)
		if err := row.Scan(&sample.VideoID, &sample.ChannelID, &sample.SourceID, &sample.ChannelURL); err != nil {
			queueRow := conn.QueryRow(`
				SELECT q.video_id, q.channel_id, COALESCE(c.source_id, ''), COALESCE(c.url, '')
				FROM download_queue q
				JOIN channels c ON c.channel_id = q.channel_id
				WHERE c.platform = ?
				ORDER BY q.added_at DESC
				LIMIT 1`, platform)
			if queueErr := queueRow.Scan(&sample.VideoID, &sample.ChannelID, &sample.SourceID, &sample.ChannelURL); queueErr != nil {
				channelRow := conn.QueryRow(`
					SELECT channel_id, COALESCE(source_id, ''), COALESCE(url, '')
					FROM channels
					WHERE platform = ?
					ORDER BY last_checked ASC, id ASC
					LIMIT 1`, platform)
				_ = channelRow.Scan(&sample.ChannelID, &sample.SourceID, &sample.ChannelURL)
			}
		}
		sample.Platform = platform
		if sample.SourceID == "" && sample.ChannelID != "" {
			sample.SourceID = strings.TrimPrefix(sample.ChannelID, platform+"_")
		}
		return nil
	})
	return sample
}

func (s *Server) reportFeedSample() reportFeedSample {
	var sample reportFeedSample
	if s.db == nil {
		return sample
	}
	_ = s.db.WithRead(func(conn *sql.DB) error {
		row := conn.QueryRow(`
			SELECT tweet_id, COALESCE(author_handle, ''), COALESCE(source_handle, ''), COALESCE(canonical_url, '')
			FROM feed_items_resolved
			ORDER BY fetched_at DESC, published_at DESC
			LIMIT 1`)
		_ = row.Scan(&sample.TweetID, &sample.Author, &sample.Source, &sample.CanonicalURL)
		return nil
	})
	return sample
}

func stringField(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func findThumbnailURL(m map[string]any, minWidth int) string {
	items, _ := m["thumbnails"].([]any)
	for _, item := range items {
		obj, _ := item.(map[string]any)
		if stringField(obj, "url") == "" {
			continue
		}
		if width, _ := obj["width"].(float64); int(width) >= minWidth {
			return stringField(obj, "url")
		}
	}
	return ""
}

func reportPathStats(paths []string) (int, int64) {
	var bytes int64
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			bytes += fi.Size()
		}
	}
	return len(paths), bytes
}

func instagramReportURL(videoID string) string {
	raw := strings.TrimPrefix(videoID, "instagram_")
	switch {
	case strings.HasPrefix(raw, "post_"):
		return "https://www.instagram.com/p/" + strings.TrimPrefix(raw, "post_") + "/"
	case strings.HasPrefix(raw, "reel_"):
		return "https://www.instagram.com/reel/" + strings.TrimPrefix(raw, "reel_") + "/"
	default:
		return "https://www.instagram.com/reel/" + raw + "/"
	}
}

func writeAnyJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
