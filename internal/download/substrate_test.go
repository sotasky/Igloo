package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/screwys/igloo/internal/model"
)

type memoryOperationSink struct {
	ops []model.DownloaderOperation
}

func (s *memoryOperationSink) RecordDownloaderOperation(ctx context.Context, op model.DownloaderOperation) error {
	s.ops = append(s.ops, op)
	return nil
}

func TestCommandRunnerCapturesExitAndRedactsArgs(t *testing.T) {
	result := CommandRunner{}.Run(context.Background(), "sh", []string{"-c", "echo out; echo err >&2; exit 7", "--cookies", "/tmp/private-cookies.txt"}, CommandOptions{})
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if result.Err == nil {
		t.Fatal("expected non-zero command error")
	}
	if !strings.Contains(string(result.CombinedOutput()), "out") || !strings.Contains(string(result.CombinedOutput()), "err") {
		t.Fatalf("combined output missing stdout/stderr: %q", string(result.CombinedOutput()))
	}
	redacted := strings.Join(RedactArgs([]string{"--cookies", "/tmp/private-cookies.txt", "--cookies-from-browser=firefox"}), " ")
	if strings.Contains(redacted, "private-cookies") || strings.Contains(redacted, "firefox") {
		t.Fatalf("args were not redacted: %s", redacted)
	}
}

func TestCommandRunnerFindsToolsFromCommonUserBins(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	toolPath := filepath.Join(binDir, "igloo-test-tool")
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\necho common-path-tool\n"), 0o755); err != nil {
		t.Fatalf("write tool: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	result := CommandRunner{}.Run(context.Background(), "igloo-test-tool", nil, CommandOptions{})
	if result.Err != nil {
		t.Fatalf("run tool from common path: %v", result.Err)
	}
	if got := strings.TrimSpace(string(result.CombinedOutput())); got != "common-path-tool" {
		t.Fatalf("output = %q, want common-path-tool", got)
	}
}

func TestJSONPayloadsHandlesMixedDownloaderOutput(t *testing.T) {
	raw := []byte("[gallery-dl][info] log line\n{\"id\":\"one\"}\n[\n {\"id\":\"two\"},\n [{\"id\":\"three\"}]\n]\n")
	payloads := JSONPayloads(raw)
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d, want 2", len(payloads))
	}
	var ids []string
	for _, payload := range payloads {
		for _, obj := range FlattenJSONObjects(payload) {
			if id, _ := obj["id"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	}
	got := strings.Join(ids, ",")
	if got != "one,two,three" {
		t.Fatalf("flattened ids = %s", got)
	}
}

func TestClassifyErrorPatterns(t *testing.T) {
	tests := []struct {
		name string
		err  error
		out  []byte
		want string
	}{
		{"auth", errors.New("login required; cookies missing"), nil, ErrorKindAuth},
		{"rate", errors.New("HTTP Error 429: Too Many Requests"), nil, ErrorKindRateLimit},
		{"hyphenated_rate_limit", errors.New("Requested content is not available, rate-limit reached or login required. Use --cookies for authentication"), nil, ErrorKindRateLimit},
		{
			"instagram_api_access_denial",
			errors.New("yt-dlp: exit status 1 WARNING: [Instagram] SAMPLE: Instagram API is not granting access ERROR: [Instagram] SAMPLE: Unable to download webpage: HTTP Error 302: Found (redirect loop detected)"),
			nil,
			ErrorKindRateLimit,
		},
		{
			"instagram_media_info_bad_request",
			errors.New("gallery-dl: exit status 4: [instagram][error] HttpError: '400 Bad Request' for 'https://instagram.invalid/api/v1/media/1234567890/info/'"),
			nil,
			ErrorKindRateLimit,
		},
		{"not_found", nil, []byte("Requested post not available"), ErrorKindNotFound},
		{
			"cookie_log_with_forbidden_media",
			errors.New("exit status 4"),
			[]byte("[cookies][info] Extracted 733 cookies from Firefox\n[downloader.http][warning] '403 Forbidden' for 'https://scontent.example/avatar.jpg'"),
			ErrorKindPermanentHTTP,
		},
		{"empty", errors.New("gallery-dl: no files downloaded"), nil, ErrorKindEmptyResult},
		{"no_video_formats", errors.New("[Instagram] ABC123: No video formats found!"), nil, ErrorKindEmptyResult},
		{"parse", errors.New("invalid character '<' looking for beginning of value"), nil, ErrorKindParse},
		{"canceled", context.Canceled, nil, ErrorKindCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyError(tt.err, tt.out); got != tt.want {
				t.Fatalf("kind = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyFailurePermanentAndRetryablePolicy(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		output    []byte
		attempt   int
		wantKind  string
		permanent bool
		minDelay  time.Duration
	}{
		{
			name:      "auth is permanent",
			err:       errors.New("login required; cookies missing"),
			wantKind:  ErrorKindAuth,
			permanent: true,
		},
		{
			name:      "empty result is permanent",
			err:       errors.New("no files downloaded"),
			wantKind:  ErrorKindEmptyResult,
			permanent: true,
		},
		{
			name:      "rate limit uses long retry",
			err:       errors.New("HTTP Error 429: Too Many Requests"),
			attempt:   1,
			wantKind:  ErrorKindRateLimit,
			permanent: false,
			minDelay:  time.Hour,
		},
		{
			name:      "temporary uses exponential retry",
			err:       context.DeadlineExceeded,
			attempt:   2,
			wantKind:  ErrorKindTemporary,
			permanent: false,
			minDelay:  2 * time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyFailure(tt.err, tt.output, tt.attempt)
			if got.Kind != tt.wantKind || got.Permanent != tt.permanent {
				t.Fatalf("classification = %+v, want kind=%q permanent=%v", got, tt.wantKind, tt.permanent)
			}
			if !got.Permanent && got.RetryDelay < tt.minDelay {
				t.Fatalf("retry delay = %s, want at least %s", got.RetryDelay, tt.minDelay)
			}
			if got.Permanent && got.RetryDelay != 0 {
				t.Fatalf("permanent retry delay = %s, want 0", got.RetryDelay)
			}
		})
	}
}

func TestRecordOperationRedactsPrivatePathsInSummary(t *testing.T) {
	sink := &memoryOperationSink{}
	recordOperation(context.Background(), sink, model.DownloaderOperation{
		Operation:   "media.test",
		Platform:    "twitter",
		Tool:        "gallery-dl",
		Status:      OperationStatusFailure,
		ErrorKind:   ErrorKindAuth,
		Error:       "gallery-dl --cookies /home/sample/.config/igloo/x.com_cookies.txt auth_token=secret",
		SummaryJSON: `{"args":["--cookies","/home/sample/.config/igloo/x.com_cookies.txt"],"token":"secret"}`,
	})
	if len(sink.ops) != 1 {
		t.Fatalf("operation count = %d, want 1", len(sink.ops))
	}
	op := sink.ops[0]
	if strings.Contains(op.Error, "/home/sample") || strings.Contains(op.SummaryJSON, "/home/sample") || strings.Contains(op.Error, "secret") || strings.Contains(op.SummaryJSON, "secret") {
		t.Fatalf("operation leaked private data: error=%q summary=%q", op.Error, op.SummaryJSON)
	}
}

func TestCookieResolverUsesPlatformSpecificFilesAndRotationCandidates(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"x.com_cookies_b.txt",
		"x.com_cookies_a.txt",
		"www.instagram.com_cookies.txt",
		"youtube.com_cookies.txt",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
			t.Fatalf("write cookie %s: %v", name, err)
		}
	}
	xFiles := DiscoverCookieFiles(dir, "twitter")
	if len(xFiles) != 2 || filepath.Base(xFiles[0].Path) != "x.com_cookies_a.txt" || filepath.Base(xFiles[1].Path) != "x.com_cookies_b.txt" {
		t.Fatalf("twitter cookie candidates = %#v", xFiles)
	}
	insta := ResolveCookieSet(dir, "instagram", true, "firefox")
	if filepath.Base(insta.File) != "www.instagram.com_cookies.txt" || insta.Browser != "" {
		t.Fatalf("instagram cookies = %#v", insta)
	}
	instaSets := ResolveCookieSets(dir, "instagram", true, "firefox")
	if len(instaSets) != 2 || filepath.Base(instaSets[0].File) != "www.instagram.com_cookies.txt" || instaSets[1].Browser != "firefox" {
		t.Fatalf("instagram cookie sets = %#v", instaSets)
	}
	youtube := ResolveCookieSet(dir, "youtube", false, "firefox")
	if youtube.File != "" || youtube.Browser != "firefox" {
		t.Fatalf("youtube browser fallback = %#v", youtube)
	}

	file, browser := CookieFileAndBrowser(instaSets)
	if filepath.Base(file) != "www.instagram.com_cookies.txt" || browser != "firefox" {
		t.Fatalf("instagram file/browser = %q/%q, want cookie file and firefox", file, browser)
	}
}

func TestDownloaderRecordsDirectHTTPFailureOperation(t *testing.T) {
	sink := &memoryOperationSink{}
	d := NewDownloader("")
	d.SetOperationSink(sink)
	_, err := d.Download(context.Background(), "http://127.0.0.1/media.jpg", "photo", Opts{OutputDir: t.TempDir(), ID: "photo"})
	if err == nil {
		t.Fatal("expected blocked local host error")
	}
	if len(sink.ops) != 1 {
		t.Fatalf("operation count = %d, want 1", len(sink.ops))
	}
	op := sink.ops[0]
	if op.Operation != "media.download" || op.Tool != "http" || op.Status != OperationStatusFailure || op.FileCount != 0 || op.Error == "" {
		t.Fatalf("operation = %#v", op)
	}
	if op.ElapsedMs < 0 || op.EndedAtMs < op.StartedAtMs || time.UnixMilli(op.StartedAtMs).IsZero() {
		t.Fatalf("invalid timing: %#v", op)
	}
}
