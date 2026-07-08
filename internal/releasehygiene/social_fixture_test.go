package releasehygiene

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

type finding struct {
	path   string
	line   int
	value  string
	reason string
}

type urlRule struct {
	platform string
	kind     string
	re       *regexp.Regexp
	skip     map[string]bool
}

var (
	syntheticTokens = []string{"sample", "example", "test", "fixture", "demo"}

	allowMarker = "igloo-hygiene: allow-social-fixture"

	baselinePath = "internal/releasehygiene/social_fixture_baseline.txt"

	rawIdentityRe = regexp.MustCompile(`(?i)\b(ChannelID|channel_id|channelId|ReposterChannelID|reposter_channel_id|reposterChannelId|ownerId|owner_id|SourceID|source_id|sourceId|SourceHandle|source_handle|sourceHandle|AuthorHandle|author_handle|authorHandle|QuoteAuthorHandle|quote_author_handle|RetweetedByHandle|retweeted_by_handle|ReposterHandle|reposter_handle|account_handles|accountHandles|TweetID|tweet_id|tweetId|VideoID|video_id|videoId|Handle|handle)"?\s*[:=]\s*["']@?([A-Za-z0-9_.@,-]+)["']`)

	identityContextRe = regexp.MustCompile(`(?i)(ChannelID|channel_id|channelId|ownerId|owner_id|SourceID|source_id|sourceId|VideoID|video_id|videoId|TweetID|tweet_id|tweetId|AuthorHandle|author_handle|authorHandle|QuoteAuthorHandle|quote_author_handle|RetweetedByHandle|retweeted_by_handle|ReposterChannelID|reposter_channel_id|ReposterHandle|reposter_handle|SourceHandle|source_handle|sourceHandle|profile_url|profileUrl|canonical_url|canonicalUrl|tweetUrl|TweetURL|url|URL|Handle|handle|account_handles|accountHandles|/channels/|/api/media/(?:avatar|banner)/)`)

	urlRules = []urlRule{
		{
			platform: "twitter",
			kind:     "X/Twitter handle URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?(?:x|twitter)\.com/([A-Za-z0-9_.-]+)`),
			skip: map[string]bool{
				"i": true, "intent": true, "share": true, "search": true, "home": true, "settings": true,
			},
		},
		{
			platform: "tiktok",
			kind:     "TikTok profile URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?tiktok\.com/@([A-Za-z0-9_.-]+)`),
		},
		{
			platform: "tiktok",
			kind:     "TikTok video ID URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?tiktok\.com/@[A-Za-z0-9_.-]+/(?:video|photo)/([A-Za-z0-9_-]+)`),
		},
		{
			platform: "instagram",
			kind:     "Instagram story handle URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?instagram\.com/stories/([A-Za-z0-9_.-]+)`),
		},
		{
			platform: "instagram",
			kind:     "Instagram profile URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?instagram\.com/([A-Za-z0-9_.-]+)`),
			skip: map[string]bool{
				"p": true, "reel": true, "tv": true, "stories": true, "explore": true, "accounts": true, "direct": true,
			},
		},
		{
			platform: "instagram",
			kind:     "Instagram post ID URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?instagram\.com/(?:p|reel|tv)/([A-Za-z0-9_-]+)`),
		},
		{
			platform: "youtube",
			kind:     "YouTube channel ID URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?youtube\.com/channel/([A-Za-z0-9_-]+)`),
		},
		{
			platform: "youtube",
			kind:     "YouTube handle URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?youtube\.com/@([A-Za-z0-9_.-]+)`),
		},
		{
			platform: "youtube",
			kind:     "YouTube video ID URL",
			re:       regexp.MustCompile(`https?://(?:www\.)?youtube\.com/watch\?v=([A-Za-z0-9_-]+)`),
		},
	}
)

func TestSocialFixtureIdentitiesUseSampleNames(t *testing.T) {
	root := repoRoot(t)
	paths := gitTrackedFiles(t, root)

	var findings []finding
	for _, path := range paths {
		if skipPath(path) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Contains(data, []byte{0}) || !utf8.Valid(data) {
			continue
		}
		findings = append(findings, scanFile(path, string(data))...)
	}
	if len(findings) == 0 {
		return
	}
	sortFindings(findings)
	if os.Getenv("IGLOO_UPDATE_SOCIAL_FIXTURE_BASELINE") == "1" {
		writeBaseline(t, root, findings)
		return
	}

	baseline := readBaseline(t, root)
	newFindings, staleBaseline := compareBaseline(findings, baseline)
	if len(newFindings) == 0 && staleBaseline == 0 {
		t.Logf("known legacy social fixture debt remains: %d findings; new generic social identities must use sample_* names", len(findings))
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "social fixture identities must use obvious sample/test names; prefer sample_* values or add %q with a reason for intentional exceptions\n", allowMarker)
	if len(newFindings) > 0 {
		fmt.Fprintf(&b, "new findings: %d\n", len(newFindings))
		for i, f := range newFindings {
			if i >= 80 {
				fmt.Fprintf(&b, "... and %d more\n", len(newFindings)-i)
				break
			}
			fmt.Fprintf(&b, "%s:%d: %s (%s)\n", f.path, f.line, f.value, f.reason)
		}
	}
	if staleBaseline > 0 {
		if len(newFindings) > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "baseline has %d stale entries; regenerate it after intentional fixture cleanup with:\n", staleBaseline)
		fmt.Fprintf(&b, "IGLOO_UPDATE_SOCIAL_FIXTURE_BASELINE=1 go test ./internal/releasehygiene -run TestSocialFixtureIdentitiesUseSampleNames -count=1\n")
	}
	t.Fatal(b.String())
}

func sortFindings(findings []finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].path != findings[j].path {
			return findings[i].path < findings[j].path
		}
		if findings[i].line != findings[j].line {
			return findings[i].line < findings[j].line
		}
		if findings[i].value != findings[j].value {
			return findings[i].value < findings[j].value
		}
		return findings[i].reason < findings[j].reason
	})
}

func readBaseline(t *testing.T, root string) map[string]int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, baselinePath))
	if err != nil {
		t.Fatalf("read %s: %v", baselinePath, err)
	}
	counts := map[string]int{}
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(line) {
			t.Fatalf("%s:%d: invalid baseline hash %q", baselinePath, lineNo+1, line)
		}
		counts[line]++
	}
	return counts
}

func writeBaseline(t *testing.T, root string, findings []finding) {
	t.Helper()
	hashes := make([]string, 0, len(findings))
	for _, finding := range findings {
		hashes = append(hashes, findingBaselineHash(finding))
	}
	sort.Strings(hashes)

	var b strings.Builder
	b.WriteString("# Generated by:\n")
	b.WriteString("# IGLOO_UPDATE_SOCIAL_FIXTURE_BASELINE=1 go test ./internal/releasehygiene -run TestSocialFixtureIdentitiesUseSampleNames -count=1\n")
	b.WriteString("# Each line is a SHA-256 hash of path, value, and reason for known legacy fixture debt.\n")
	for _, hash := range hashes {
		b.WriteString(hash)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(root, baselinePath), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", baselinePath, err)
	}
	t.Logf("wrote %d baseline entries to %s", len(hashes), baselinePath)
}

func compareBaseline(findings []finding, baseline map[string]int) ([]finding, int) {
	remaining := make(map[string]int, len(baseline))
	for hash, count := range baseline {
		remaining[hash] = count
	}

	var newFindings []finding
	for _, finding := range findings {
		hash := findingBaselineHash(finding)
		if remaining[hash] > 0 {
			remaining[hash]--
			continue
		}
		newFindings = append(newFindings, finding)
	}

	stale := 0
	for _, count := range remaining {
		stale += count
	}
	return newFindings, stale
}

func findingBaselineHash(f finding) string {
	sum := sha256.Sum256([]byte(f.path + "\x00" + f.value + "\x00" + f.reason))
	return fmt.Sprintf("%x", sum[:])
}

func TestSocialFixtureBaselineCatchesNewFindings(t *testing.T) {
	baseline := map[string]int{}
	old := finding{path: "example_test.go", line: 1, value: "twitter_old", reason: "social handle/source literal"}
	baseline[findingBaselineHash(old)] = 1

	newFinding := finding{path: "example_test.go", line: 2, value: "twitter_new", reason: "social handle/source literal"}
	got, stale := compareBaseline([]finding{old, newFinding}, baseline)
	if stale != 0 {
		t.Fatalf("stale = %d, want 0", stale)
	}
	if len(got) != 1 || got[0] != newFinding {
		t.Fatalf("new findings = %+v, want %+v", got, newFinding)
	}

	got, stale = compareBaseline(nil, baseline)
	if len(got) != 0 {
		t.Fatalf("new findings after cleanup = %+v, want none", got)
	}
	if stale != 1 {
		t.Fatalf("stale after cleanup = %d, want 1", stale)
	}
}

func TestSocialFixtureScannerExamples(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantValues []string
	}{
		{
			name: "x channel fixture needs sample marker",
			content: `
if err := srv.db.AddChannel(model.Channel{
	ChannelID: "twitter__specific_handle",
	SourceID:  "_specific_handle",
	Name:      "_specific_handle",
	URL:       "https://x.com/_specific_handle",
	Platform:  "twitter",
}); err != nil {
	t.Fatal(err)
}`,
			wantValues: []string{"_specific_handle", "twitter__specific_handle"},
		},
		{
			name: "generic author and tweet IDs still need sample marker",
			content: `
items := []model.FeedItem{
	{
		TweetID:      "tweet-kagi-rate-limited-older",
		AuthorHandle: "author_older",
		BodyText:     "hello",
		Lang:         "en",
	},
}`,
			wantValues: []string{"author_older", "tweet-kagi-rate-limited-older"},
		},
		{
			name: "sql tuples need sample identities too",
			content: `
if _, err := d.conn.Exec(` + "`" + `
	INSERT INTO videos (video_id, channel_id, title, duration, published_at, sync_seq)
	VALUES ('7777777777777777777', 'tiktok_specific0day', 'Old title', 0, 0, 0)
` + "`" + `); err != nil {
	t.Fatal(err)
}`,
			wantValues: []string{"7777777777777777777", "tiktok_specific0day"},
		},
		{
			name: "sql author handles need sample identities too",
			content: `
if _, err := d.conn.Exec(` + "`" + `
	INSERT INTO feed_items (tweet_id, author_handle, source_handle, body_text, published_at)
	VALUES ('prior_target_seen', 'SpecificHandle', 'SpecificHandle', 'body', ?)
` + "`" + `); err != nil {
	t.Fatal(err)
}`,
			wantValues: []string{"SpecificHandle"},
		},
		{
			name: "sample prefix cannot preserve a real-looking handle tail",
			content: `
channel := model.Channel{
	ChannelID: "tiktok_sample_specific0day",
	Platform:  "tiktok",
}`,
			wantValues: []string{"tiktok_sample_specific0day"},
		},
		{
			name: "sample fixtures are accepted",
			content: `
item := model.FeedItem{
	TweetID:      "sample_tweet_kagi_rate_limited_older",
	AuthorHandle: "sample_author_older",
	BodyText:     "hello",
	Lang:         "en",
}
channel := model.Channel{
	ChannelID: "twitter__sample_handle",
	SourceID:  "_sample_handle",
	Name:      "_sample_handle",
	URL:       "https://x.com/_sample_handle",
	Platform:  "twitter",
}
video := model.Video{
	VideoID:   "9000000000000000000",
	ChannelID: "tiktok_sample",
}
otherVideo := model.Video{
	VideoID:   "sample_plain_video",
	ChannelID: "tiktok_sample_plain_author",
}`,
		},
		{
			name: "dynamic and structural urls are accepted",
			content: `
url := "https://x.com/" + handle
url = fmt.Sprintf("https://x.com/%s", handle)
match := "https://x.com/i/lists/*"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := scanFile("example_test.go", tt.content)
			gotValues := uniqueFindingValues(findings)
			if !sameStrings(gotValues, tt.wantValues) {
				t.Fatalf("values = %v, want %v; findings: %+v", gotValues, tt.wantValues, findings)
			}
		})
	}
}

func scanFile(path, content string) []finding {
	lines := strings.Split(content, "\n")
	var findings []finding
	for i, line := range lines {
		if lineAllowed(line) {
			continue
		}
		for _, rule := range urlRules {
			for _, match := range rule.re.FindAllStringSubmatch(line, -1) {
				if len(match) < 2 {
					continue
				}
				value := strings.Trim(match[1], `"'`)
				if rule.skip[strings.ToLower(value)] {
					continue
				}
				if reason, ok := socialIdentityFindingReason(rule.platform, value, rule.kind); ok {
					findings = append(findings, finding{path: path, line: i + 1, value: value, reason: reason})
				}
			}
		}
		if isFixturePath(path) && strings.Contains(strings.ToLower(line), "values") {
			findings = append(findings, scanSQLInsertValues(path, lines, i)...)
		}
		if isFixturePath(path) && hasIdentityContext(lines, i) {
			for _, match := range rawIdentityRe.FindAllStringSubmatch(line, -1) {
				if len(match) < 3 {
					continue
				}
				field := match[1]
				for _, value := range splitRawIdentityValues(match[2]) {
					if !looksConcreteIdentity(value) {
						continue
					}
					if !shouldCheckRawIdentity(field, value) {
						continue
					}
					if reason, ok := socialIdentityFindingReason("", value, "social handle/source literal"); ok {
						findings = append(findings, finding{path: path, line: i + 1, value: value, reason: reason})
					}
				}
			}
		}
	}
	return findings
}

func uniqueFindingValues(findings []finding) []string {
	seen := make(map[string]bool, len(findings))
	for _, finding := range findings {
		seen[finding.value] = true
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func repoRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func gitTrackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	parts := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		paths = append(paths, string(part))
	}
	return paths
}

func skipPath(path string) bool {
	base := filepath.Base(path)
	if path == "internal/releasehygiene/social_fixture_test.go" ||
		strings.HasPrefix(path, "static/dist/") ||
		strings.HasPrefix(path, "android/.gradle/") ||
		strings.HasPrefix(path, "android/app/build/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".mp4", ".webm", ".mp3", ".ogg", ".ico", ".jar", ".keystore", ".db", ".sqlite":
		return true
	}
	return false
}

func isFixturePath(path string) bool {
	base := filepath.Base(path)
	return strings.Contains(path, "/testdata/") ||
		strings.Contains(path, "/src/test/") ||
		strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, ".test.mjs") ||
		strings.HasSuffix(base, ".test.js") ||
		strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".test.kt") ||
		strings.HasPrefix(path, "locales/") ||
		strings.HasPrefix(path, "docs/")
}

func lineAllowed(line string) bool {
	idx := strings.Index(line, allowMarker)
	if idx < 0 {
		return false
	}
	return strings.TrimSpace(line[idx+len(allowMarker):]) != ""
}

func hasIdentityContext(lines []string, idx int) bool {
	if identityContextRe.MatchString(lines[idx]) {
		return true
	}
	start := max(0, idx-4)
	end := min(len(lines), idx+5)
	for _, line := range lines[start:end] {
		if strings.Contains(line, "Platform:") ||
			strings.Contains(line, `"platform"`) ||
			strings.Contains(line, "platform =") ||
			strings.Contains(line, "channel_id") ||
			strings.Contains(line, "ChannelID") ||
			strings.Contains(line, "channelId") {
			if strings.Contains(line, "twitter") ||
				strings.Contains(line, "tiktok") ||
				strings.Contains(line, "instagram") ||
				strings.Contains(line, "youtube") ||
				strings.Contains(line, "x.com") {
				return true
			}
		}
	}
	return false
}

func splitRawIdentityValues(value string) []string {
	value = strings.Trim(value, `"'`)
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '@'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, ` "'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func scanSQLInsertValues(path string, lines []string, idx int) []finding {
	start := max(0, idx-8)
	end := min(len(lines), idx+4)
	window := strings.Join(lines[start:end], "\n")

	columns, values, ok := extractSQLInsertColumnsAndValues(window)
	if !ok {
		return nil
	}

	var findings []finding
	for i, column := range columns {
		if i >= len(values) {
			break
		}
		value, ok := trimSQLStringLiteral(values[i])
		if !ok || !looksConcreteIdentity(value) {
			continue
		}
		platform, reason, ok := sqlIdentityColumn(column, value)
		if !ok {
			continue
		}
		if reason, ok := socialIdentityFindingReason(platform, value, reason); ok {
			findings = append(findings, finding{path: path, line: idx + 1, value: value, reason: reason})
		}
	}
	return findings
}

func extractSQLInsertColumnsAndValues(sql string) ([]string, []string, bool) {
	lower := strings.ToLower(sql)
	valuesIdx := strings.Index(lower, "values")
	if valuesIdx < 0 {
		return nil, nil, false
	}
	beforeValues := sql[:valuesIdx]
	valuesPart := sql[valuesIdx+len("values"):]

	colClose := strings.LastIndex(beforeValues, ")")
	if colClose < 0 {
		return nil, nil, false
	}
	colOpen := strings.LastIndex(beforeValues[:colClose], "(")
	if colOpen < 0 {
		return nil, nil, false
	}

	valueOpen := strings.Index(valuesPart, "(")
	if valueOpen < 0 {
		return nil, nil, false
	}
	valueClose := findSQLListClose(valuesPart, valueOpen)
	if valueClose < 0 {
		return nil, nil, false
	}

	return splitSQLList(beforeValues[colOpen+1 : colClose]), splitSQLList(valuesPart[valueOpen+1 : valueClose]), true
}

func findSQLListClose(s string, open int) int {
	inQuote := false
	for i := open + 1; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
		case ')':
			if !inQuote {
				return i
			}
		}
	}
	return -1
}

func splitSQLList(s string) []string {
	var parts []string
	start := 0
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if inQuote && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inQuote = !inQuote
		case ',':
			if !inQuote {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

func trimSQLStringLiteral(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		return "", false
	}
	return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), true
}

func sqlIdentityColumn(column, value string) (platform, reason string, ok bool) {
	column = strings.ToLower(strings.Trim(column, ` "[]`))
	normalized := strings.ToLower(strings.Trim(value, ` "'@`))
	switch column {
	case "channel_id", "source_id", "source_handle", "author_handle", "quote_author_handle", "retweeted_by_handle", "reposter_handle", "reposter_channel_id":
		return socialPlatformFromValue(normalized), "SQL " + column + " literal", true
	case "tweet_id", "video_id":
		if looksSocialPostID(normalized) {
			return socialPlatformFromValue(normalized), "SQL " + column + " literal", true
		}
	}
	return "", "", false
}

func socialPlatformFromValue(value string) string {
	switch {
	case strings.HasPrefix(value, "tiktok_"):
		return "tiktok"
	case strings.HasPrefix(value, "instagram_"):
		return "instagram"
	case strings.HasPrefix(value, "youtube_"):
		return "youtube"
	case strings.HasPrefix(value, "twitter_") || strings.HasPrefix(value, "x_"):
		return "twitter"
	default:
		return ""
	}
}

func looksConcreteIdentity(value string) bool {
	if value == "" {
		return false
	}
	if strings.ContainsAny(value, "${}%+*/<>") {
		return false
	}
	lower := strings.ToLower(value)
	if lower == "user" || lower == "username" || lower == "handle" || lower == "channel" {
		return true
	}
	if len(value) == 1 {
		return false
	}
	return regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`).MatchString(value)
}

func socialIdentityFindingReason(platform, value, defaultReason string) (string, bool) {
	if malformedSampleIdentity(value) {
		return "sample-prefixed social identity keeps a non-generic tail", true
	}
	if !syntheticIdentity(platform, value) {
		return defaultReason, true
	}
	return "", false
}

func shouldCheckRawIdentity(field, value string) bool {
	field = strings.ToLower(field)
	value = strings.ToLower(strings.Trim(value, ` "'@`))
	switch field {
	case "tweetid", "tweet_id", "videoid", "video_id":
		return looksSocialPostID(value)
	default:
		return true
	}
}

func looksSocialPostID(value string) bool {
	if strings.HasPrefix(value, "twitter_") ||
		strings.HasPrefix(value, "tiktok_") ||
		strings.HasPrefix(value, "instagram_") ||
		strings.HasPrefix(value, "youtube_") {
		return true
	}
	if strings.Contains(value, "tweet") || strings.Contains(value, "post") || strings.Contains(value, "reel") {
		return true
	}
	if regexp.MustCompile(`^[0-9]{8,}$`).MatchString(value) {
		return true
	}
	return false
}

func syntheticIdentity(platform, value string) bool {
	normalized := strings.ToLower(strings.Trim(value, ` "'@`))
	if normalized == "" {
		return true
	}
	if acceptableSampleIdentity(normalized) {
		return true
	}
	if containsIdentityToken(normalized, "sample") {
		return false
	}
	for _, token := range syntheticTokens[1:] {
		if containsIdentityToken(normalized, token) {
			return true
		}
	}
	if regexp.MustCompile(`^900[0-9]{15,}$`).MatchString(normalized) {
		return true
	}
	if platform == "youtube" && strings.HasPrefix(normalized, "ucexample") {
		return true
	}
	return false
}

func malformedSampleIdentity(value string) bool {
	normalized := strings.ToLower(strings.Trim(value, ` "'@`))
	return containsIdentityToken(normalized, "sample") && !acceptableSampleIdentity(normalized)
}

func acceptableSampleIdentity(value string) bool {
	tokens := socialIdentityTokens(value)
	for i, token := range tokens {
		if token != "sample" {
			continue
		}
		if i == len(tokens)-1 {
			return true
		}
		return allowedSampleTailToken(tokens[i+1])
	}
	return false
}

func containsIdentityToken(value, want string) bool {
	for _, token := range socialIdentityTokens(value) {
		if token == want {
			return true
		}
	}
	return false
}

func socialIdentityTokens(value string) []string {
	value = strings.ToLower(strings.Trim(value, ` "'@`))
	for _, prefix := range []string{"twitter__", "twitter_", "x_", "tiktok_", "instagram_", "youtube_"} {
		if strings.HasPrefix(value, prefix) {
			value = strings.TrimPrefix(value, prefix)
			break
		}
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			tokens = append(tokens, part)
		}
	}
	return tokens
}

func allowedSampleTailToken(token string) bool {
	switch token {
	case "a", "b", "alpha", "archive", "author", "avatar", "banner", "beta",
		"bookmark", "category", "ch", "channel", "child", "clip", "collection",
		"creator", "del", "deleted", "delta", "demo", "dup", "duplicate",
		"example", "existing", "export", "first", "fixture", "followed",
		"gamma", "ghost", "handle", "image", "import", "imported", "media",
		"missing", "muted", "new", "newer", "old", "older", "one", "parent",
		"photo", "plain", "post", "profile", "quote", "reply", "repost", "reposter",
		"root", "second", "seen", "slide", "slides", "slideshow", "source",
		"star", "starred", "story", "target", "test", "tweet", "two", "unfollowed",
		"unseen", "user", "video":
		return true
	}
	if regexp.MustCompile(`^[0-9]+$`).MatchString(token) {
		return true
	}
	if strings.HasPrefix(token, "uc") &&
		(strings.Contains(token, "example") || strings.Contains(token, "import") || strings.Contains(token, "sample")) {
		return true
	}
	return false
}
