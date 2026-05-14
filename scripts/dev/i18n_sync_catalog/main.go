package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	catalogDir           = "locales/app"
	baseCatalogPath      = "locales/app/en.toml"
	androidResDir        = "android/app/src/main/res"
	androidBaseValues    = "android/app/src/main/res/values/strings.xml"
	androidLocaleOptions = "android/app/src/main/res/values/locale_options.xml"
	androidLocaleConfig  = "android/app/src/main/res/xml/locales_config.xml"
)

type localeMeta struct {
	Tag  string
	Name string
}

func main() {
	baseMessages := readOptionalTOML(baseCatalogPath)
	webMessages := mustCollectWebMessages()
	for key, value := range webMessages {
		baseMessages[key] = value
	}
	if len(baseMessages) == 0 {
		fail("no i18n strings found")
	}
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		fail("%v", err)
	}
	if err := writeTOML(baseCatalogPath, baseMessages, "en", "English"); err != nil {
		fail("%v", err)
	}
	catalogs, err := readCatalogs(catalogDir)
	if err != nil {
		fail("%v", err)
	}
	if _, ok := catalogs["en"]; !ok {
		catalogs["en"] = baseMessages
	}
	if err := writeAndroidResources(catalogs); err != nil {
		fail("%v", err)
	}
	if err := writeAndroidLocaleMetadata(catalogDir); err != nil {
		fail("%v", err)
	}
}

func mustCollectWebMessages() map[string]string {
	messages := map[string]string{}
	if err := filepath.WalkDir("internal", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return collectFile(path, messages)
	}); err != nil {
		fail("%v", err)
	}
	return messages
}

func collectFile(path string, messages map[string]string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}
	var firstErr error
	ast.Inspect(file, func(n ast.Node) bool {
		if firstErr != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(call.Fun)
		switch name {
		case "L":
			firstErr = collectCall(fset, call, messages, 1, 2)
		case "N":
			firstErr = collectCall(fset, call, messages, 0, 1)
		case "T":
			firstErr = collectCall(fset, call, messages, 0, 1)
		}
		return firstErr == nil
	})
	return firstErr
}

func callName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	default:
		return ""
	}
}

func collectCall(fset *token.FileSet, call *ast.CallExpr, messages map[string]string, keyIdx, valueIdx int) error {
	if len(call.Args) <= keyIdx {
		return nil
	}
	key, ok := stringLiteral(call.Args[keyIdx])
	if !ok {
		return nil
	}
	if !validKey(key) {
		return fmt.Errorf("%s: i18n key %q must be Android-safe snake_case", fset.Position(call.Pos()), key)
	}
	if len(call.Args) <= valueIdx {
		return fmt.Errorf("%s: i18n key %q is missing an English source string", fset.Position(call.Pos()), key)
	}
	value, ok := stringLiteral(call.Args[valueIdx])
	if !ok {
		return fmt.Errorf("%s: i18n key %q uses a non-literal English source string", fset.Position(call.Pos()), key)
	}
	if existing, ok := messages[key]; ok && existing != value {
		return fmt.Errorf("%s: i18n key %q has conflicting source strings %q and %q", fset.Position(call.Pos()), key, existing, value)
	}
	messages[key] = value
	return nil
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			if i == 0 && r >= '0' && r <= '9' {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func readCatalogs(dir string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		lang := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		out[lang] = readOptionalTOML(filepath.Join(dir, entry.Name()))
	}
	return out, nil
}

func readOptionalTOML(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}
		}
		fail("%v", err)
	}
	messages, err := parseFlatTOML(string(data))
	if err != nil {
		fail("%s: %v", path, err)
	}
	return messages
}

func readLocaleMetas(dir string) ([]localeMeta, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	metas := make([]localeMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		lang := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		tag := languageTag(lang)
		if tag == "" {
			continue
		}
		metas = append(metas, localeMeta{
			Tag:  tag,
			Name: readLanguageName(filepath.Join(dir, entry.Name()), tag),
		})
	}
	sort.Slice(metas, func(i, j int) bool {
		if metas[i].Tag == "en" {
			return true
		}
		if metas[j].Tag == "en" {
			return false
		}
		return metas[i].Tag < metas[j].Tag
	})
	return metas, nil
}

func readLanguageName(path, fallback string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	for _, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		const prefix = "# Language-Name:"
		if strings.HasPrefix(line, prefix) {
			name := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if name != "" {
				return name
			}
		}
	}
	return fallback
}

func parseFlatTOML(src string) (map[string]string, error) {
	messages := map[string]string{}
	for lineNo, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = \"value\"", lineNo+1)
		}
		key = strings.TrimSpace(key)
		if !validKey(key) {
			return nil, fmt.Errorf("line %d: invalid key %q", lineNo+1, key)
		}
		value, err := parseTOMLString(strings.TrimSpace(rawValue))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		messages[key] = value
	}
	return messages, nil
}

func parseTOMLString(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("missing string value")
	}
	if strings.HasPrefix(raw, "\"") {
		end := 1
		escaped := false
		for end < len(raw) {
			ch := raw[end]
			if escaped {
				escaped = false
				end++
				continue
			}
			if ch == '\\' {
				escaped = true
				end++
				continue
			}
			if ch == '"' {
				break
			}
			end++
		}
		if end >= len(raw) || raw[end] != '"' {
			return "", fmt.Errorf("unterminated string")
		}
		tail := strings.TrimSpace(raw[end+1:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", fmt.Errorf("unexpected text after string")
		}
		value, err := strconv.Unquote(raw[:end+1])
		if err != nil {
			return "", err
		}
		return value, nil
	}
	if strings.HasPrefix(raw, "'") {
		end := strings.Index(raw[1:], "'")
		if end < 0 {
			return "", fmt.Errorf("unterminated literal string")
		}
		end++
		tail := strings.TrimSpace(raw[end+1:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", fmt.Errorf("unexpected text after string")
		}
		return raw[1:end], nil
	}
	return "", fmt.Errorf("expected quoted string")
}

func writeTOML(path string, messages map[string]string, lang, name string) error {
	keys := sortedKeys(messages)
	var buf bytes.Buffer
	buf.WriteString("# Igloo shared source strings.\n")
	buf.WriteString("# Language: " + lang + "\n")
	buf.WriteString("# Language-Name: " + name + "\n")
	buf.WriteString("# Weblate: TOML file, monolingual base language file.\n")
	buf.WriteString("# Translate values and keep keys stable.\n\n")
	for _, key := range keys {
		fmt.Fprintf(&buf, "%s = %s\n", key, strconv.Quote(messages[key]))
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func writeAndroidResources(catalogs map[string]map[string]string) error {
	for lang, messages := range catalogs {
		path := androidStringsPath(lang)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := writeAndroidStrings(path, lang, messages); err != nil {
			return err
		}
	}
	return nil
}

func writeAndroidLocaleMetadata(catalogDir string) error {
	metas, err := readLocaleMetas(catalogDir)
	if err != nil {
		return err
	}
	if len(metas) == 0 {
		metas = []localeMeta{{Tag: "en", Name: "English"}}
	}
	if err := writeAndroidLocaleOptions(metas); err != nil {
		return err
	}
	return writeAndroidLocaleConfig(metas)
}

func writeAndroidLocaleOptions(metas []localeMeta) error {
	if err := os.MkdirAll(filepath.Dir(androidLocaleOptions), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("<resources>\n")
	buf.WriteString("    <string-array name=\"supported_locale_tags\" translatable=\"false\">\n")
	for _, meta := range metas {
		fmt.Fprintf(&buf, "        <item>%s</item>\n", xmlEscape(meta.Tag))
	}
	buf.WriteString("    </string-array>\n")
	buf.WriteString("    <string-array name=\"supported_locale_labels\" translatable=\"false\">\n")
	for _, meta := range metas {
		fmt.Fprintf(&buf, "        <item>%s</item>\n", xmlEscape(meta.Name))
	}
	buf.WriteString("    </string-array>\n")
	buf.WriteString("</resources>\n")
	return os.WriteFile(androidLocaleOptions, buf.Bytes(), 0o644)
}

func writeAndroidLocaleConfig(metas []localeMeta) error {
	if err := os.MkdirAll(filepath.Dir(androidLocaleConfig), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n")
	buf.WriteString("<locale-config xmlns:android=\"http://schemas.android.com/apk/res/android\">\n")
	for _, meta := range metas {
		fmt.Fprintf(&buf, "    <locale android:name=%q />\n", meta.Tag)
	}
	buf.WriteString("</locale-config>\n")
	return os.WriteFile(androidLocaleConfig, buf.Bytes(), 0o644)
}

func androidStringsPath(lang string) string {
	if lang == "en" || lang == "" {
		return androidBaseValues
	}
	return filepath.Join(androidResDir, "values-"+androidQualifier(lang), "strings.xml")
}

func androidQualifier(lang string) string {
	parts := strings.Split(strings.ReplaceAll(lang, "_", "-"), "-")
	if len(parts) == 1 {
		return strings.ToLower(parts[0])
	}
	return strings.ToLower(parts[0]) + "-r" + strings.ToUpper(parts[1])
}

func languageTag(lang string) string {
	parts := strings.Split(strings.ReplaceAll(lang, "_", "-"), "-")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	if len(parts) == 1 {
		return strings.ToLower(parts[0])
	}
	return strings.ToLower(parts[0]) + "-" + strings.ToUpper(parts[1])
}

func writeAndroidStrings(path, lang string, messages map[string]string) error {
	keys := sortedKeys(messages)
	var buf bytes.Buffer
	buf.WriteString("<resources>\n")
	for _, key := range keys {
		if lang != "en" && key == "app_name" {
			continue
		}
		value := androidStringEscape(messages[key])
		if lang == "en" && key == "app_name" {
			fmt.Fprintf(&buf, "    <string name=%q translatable=\"false\">%s</string>\n", key, value)
			continue
		}
		fmt.Fprintf(&buf, "    <string name=%q>%s</string>\n", key, value)
	}
	buf.WriteString("</resources>\n")
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func sortedKeys(messages map[string]string) []string {
	keys := make([]string, 0, len(messages))
	for key := range messages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func androidStringEscape(s string) string {
	var hasGoQuoteFormat bool
	s, hasGoQuoteFormat = androidReplaceGoQuoteFormats(s)
	s = androidPositionUnindexedFormats(s, hasGoQuoteFormat)
	escaped := xmlEscape(s)
	escaped = strings.ReplaceAll(escaped, "&#39;", `\'`)
	escaped = strings.ReplaceAll(escaped, "&#34;", `\"`)
	return escaped
}

type androidFormatPlaceholder struct {
	start int
	end   int
}

func androidPositionUnindexedFormats(s string, forceSingle bool) string {
	placeholders := make([]androidFormatPlaceholder, 0, 2)
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+1 >= len(s) {
			continue
		}
		if s[i+1] == '%' {
			i++
			continue
		}
		end, ok := unindexedAndroidFormatEnd(s, i+1)
		if !ok {
			continue
		}
		placeholders = append(placeholders, androidFormatPlaceholder{start: i, end: end})
		i = end - 1
	}
	if len(placeholders) == 0 || (len(placeholders) == 1 && !forceSingle) {
		return s
	}

	var buf strings.Builder
	last := 0
	for idx, placeholder := range placeholders {
		buf.WriteString(s[last : placeholder.start+1])
		fmt.Fprintf(&buf, "%d$", idx+1)
		buf.WriteString(s[placeholder.start+1 : placeholder.end])
		last = placeholder.end
	}
	buf.WriteString(s[last:])
	return buf.String()
}

func androidReplaceGoQuoteFormats(s string) (string, bool) {
	var buf strings.Builder
	last := 0
	changed := false
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+1 >= len(s) {
			continue
		}
		if s[i+1] == '%' {
			i++
			continue
		}
		end, verbPos, ok := androidFormatEnd(s, i+1)
		if !ok || s[verbPos] != 'q' {
			continue
		}
		buf.WriteString(s[last:verbPos])
		buf.WriteByte('s')
		last = verbPos + 1
		i = end - 1
		changed = true
	}
	if !changed {
		return s, false
	}
	buf.WriteString(s[last:])
	return buf.String(), true
}

func androidFormatEnd(s string, pos int) (end int, verbPos int, ok bool) {
	if pos < len(s) && s[pos] == '[' {
		return 0, 0, false
	}
	start := pos
	for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
		pos++
	}
	if pos > start && pos < len(s) && s[pos] == '$' {
		pos++
	}
	for pos < len(s) && strings.ContainsRune("#+- 0,(", rune(s[pos])) {
		pos++
	}
	for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
		pos++
	}
	if pos < len(s) && s[pos] == '.' {
		pos++
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			pos++
		}
	}
	if pos >= len(s) {
		return 0, 0, false
	}
	switch s[pos] {
	case 'd', 's', 'f', 'q':
		return pos + 1, pos, true
	default:
		return 0, 0, false
	}
}

func unindexedAndroidFormatEnd(s string, pos int) (int, bool) {
	if pos < len(s) && s[pos] == '[' {
		return 0, false
	}
	start := pos
	for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
		pos++
	}
	if pos > start && pos < len(s) && s[pos] == '$' {
		return 0, false
	}
	for pos < len(s) && strings.ContainsRune("#+- 0,(", rune(s[pos])) {
		pos++
	}
	for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
		pos++
	}
	if pos < len(s) && s[pos] == '.' {
		pos++
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			pos++
		}
	}
	if pos >= len(s) {
		return 0, false
	}
	switch s[pos] {
	case 'd', 's', 'f':
		return pos + 1, true
	default:
		return 0, false
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "i18n_sync_catalog: "+format+"\n", args...)
	os.Exit(1)
}
