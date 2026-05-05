package i18n

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const DefaultLanguage = "en"

type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Catalog struct {
	defaultLanguage string
	messages        map[string]map[string]string
	languageNames   map[string]string
	keys            map[string]bool
}

func NewCatalog() *Catalog {
	return &Catalog{
		defaultLanguage: DefaultLanguage,
		messages:        map[string]map[string]string{},
		languageNames: map[string]string{
			DefaultLanguage: "English",
		},
		keys: map[string]bool{},
	}
}

func LoadDir(dir string) (*Catalog, error) {
	c := NewCatalog()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		switch filepath.Ext(entry.Name()) {
		case ".toml":
			if err := c.LoadTOMLFile(path); err != nil {
				return c, err
			}
		case ".properties":
			if err := c.LoadPropertiesFile(path); err != nil {
				return c, err
			}
		case ".po":
			if err := c.LoadPOFile(path); err != nil {
				return c, err
			}
		}
	}
	return c, nil
}

func (c *Catalog) LoadTOMLFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lang := languageFromFilename(path)
	if lang == "" {
		lang = DefaultLanguage
	}
	messages, header, err := parseFlatTOML(string(data))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if headerLang := headerValue(header, "Language"); headerLang != "" {
		lang = NormalizeLanguage(headerLang)
	}
	if name := headerValue(header, "Language-Name"); name != "" {
		c.languageNames[lang] = name
	}
	if _, ok := c.messages[lang]; !ok {
		c.messages[lang] = map[string]string{}
	}
	for key, value := range messages {
		if key == "" {
			continue
		}
		c.keys[key] = true
		if value != "" || lang == c.defaultLanguage {
			c.messages[lang][key] = value
		}
	}
	return nil
}

func (c *Catalog) LoadPropertiesFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lang := languageFromFilename(path)
	if lang == "" {
		lang = DefaultLanguage
	}
	messages, header := parseProperties(string(data))
	if headerLang := headerValue(header, "Language"); headerLang != "" {
		lang = NormalizeLanguage(headerLang)
	}
	if name := headerValue(header, "Language-Name"); name != "" {
		c.languageNames[lang] = name
	}
	if _, ok := c.messages[lang]; !ok {
		c.messages[lang] = map[string]string{}
	}
	for key, value := range messages {
		if key == "" {
			continue
		}
		c.keys[key] = true
		if value != "" || lang == c.defaultLanguage {
			c.messages[lang][key] = value
		}
	}
	return nil
}

func (c *Catalog) LoadPOFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lang := languageFromFilename(path)
	entries, header := parsePO(string(data))
	if headerLang := headerValue(header, "Language"); headerLang != "" {
		lang = NormalizeLanguage(headerLang)
	}
	if lang == "" {
		lang = DefaultLanguage
	}
	if name := headerValue(header, "Language-Name"); name != "" {
		c.languageNames[lang] = name
	}
	if _, ok := c.messages[lang]; !ok {
		c.messages[lang] = map[string]string{}
	}
	for _, entry := range entries {
		if entry.msgid == "" {
			continue
		}
		c.keys[entry.msgid] = true
		if entry.msgstr != "" {
			c.messages[lang][entry.msgid] = entry.msgstr
		} else if lang == c.defaultLanguage {
			c.messages[lang][entry.msgid] = entry.msgid
		}
	}
	return nil
}

func (c *Catalog) DefaultLanguage() string {
	if c == nil || c.defaultLanguage == "" {
		return DefaultLanguage
	}
	return c.defaultLanguage
}

func (c *Catalog) ResolveLanguage(raw string) string {
	lang := NormalizeLanguage(raw)
	if lang == "" {
		return c.DefaultLanguage()
	}
	if c.HasLanguage(lang) {
		return lang
	}
	if base, _, ok := strings.Cut(lang, "-"); ok && c.HasLanguage(base) {
		return base
	}
	return c.DefaultLanguage()
}

func (c *Catalog) MatchAcceptLanguage(header string) string {
	if strings.TrimSpace(header) == "" {
		return c.DefaultLanguage()
	}
	type pref struct {
		lang string
		q    float64
		idx  int
	}
	var prefs []pref
	for idx, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lang := part
		q := 1.0
		if head, tail, ok := strings.Cut(part, ";"); ok {
			lang = strings.TrimSpace(head)
			for _, param := range strings.Split(tail, ";") {
				k, v, ok := strings.Cut(strings.TrimSpace(param), "=")
				if ok && strings.EqualFold(k, "q") {
					if parsed, err := strconv.ParseFloat(v, 64); err == nil {
						q = parsed
					}
				}
			}
		}
		prefs = append(prefs, pref{lang: NormalizeLanguage(lang), q: q, idx: idx})
	}
	sort.SliceStable(prefs, func(i, j int) bool {
		if prefs[i].q == prefs[j].q {
			return prefs[i].idx < prefs[j].idx
		}
		return prefs[i].q > prefs[j].q
	})
	for _, p := range prefs {
		if p.lang == "*" {
			return c.DefaultLanguage()
		}
		if lang := c.ResolveLanguage(p.lang); lang != c.DefaultLanguage() || c.hasDefaultLanguageMatch(p.lang) {
			return lang
		}
	}
	return c.DefaultLanguage()
}

func (c *Catalog) HasLanguage(lang string) bool {
	if c == nil {
		return lang == DefaultLanguage
	}
	lang = NormalizeLanguage(lang)
	if lang == c.defaultLanguage {
		return true
	}
	_, ok := c.messages[lang]
	return ok
}

func (c *Catalog) hasDefaultLanguageMatch(lang string) bool {
	def := c.DefaultLanguage()
	return lang == def || strings.HasPrefix(lang, def+"-")
}

func (c *Catalog) Languages() []Language {
	seen := map[string]bool{c.DefaultLanguage(): true}
	for lang := range c.messages {
		seen[lang] = true
	}
	codes := make([]string, 0, len(seen))
	for lang := range seen {
		codes = append(codes, lang)
	}
	sort.Strings(codes)
	out := make([]Language, 0, len(codes))
	for _, code := range codes {
		out = append(out, Language{Code: code, Name: c.languageName(code)})
	}
	return out
}

func (c *Catalog) Messages(lang string) map[string]string {
	out := map[string]string{}
	if c == nil {
		return out
	}
	lang = c.ResolveLanguage(lang)
	for key := range c.keys {
		out[key] = c.T(lang, key)
	}
	return out
}

func (c *Catalog) T(lang, key string, fallback ...string) string {
	if c == nil || key == "" {
		return fallbackText(key, fallback...)
	}
	lang = c.ResolveLanguage(lang)
	if msg := c.messages[lang][key]; msg != "" {
		return msg
	}
	if msg := c.messages[c.DefaultLanguage()][key]; msg != "" {
		return msg
	}
	return fallbackText(key, fallback...)
}

func fallbackText(key string, fallback ...string) string {
	if len(fallback) > 0 {
		return fallback[0]
	}
	return key
}

func NormalizeLanguage(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "_", "-"))
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i == 0 {
			parts[i] = strings.ToLower(part)
			continue
		}
		if len(part) == 2 {
			parts[i] = strings.ToUpper(part)
		} else {
			parts[i] = strings.ToLower(part)
		}
	}
	return strings.Join(parts, "-")
}

func (c *Catalog) languageName(code string) string {
	if c != nil {
		if name := strings.TrimSpace(c.languageNames[code]); name != "" {
			return name
		}
	}
	return code
}

func languageFromFilename(path string) string {
	base := filepath.Base(path)
	return NormalizeLanguage(strings.TrimSuffix(base, filepath.Ext(base)))
}

func parseFlatTOML(src string) (map[string]string, string, error) {
	messages := map[string]string{}
	var header strings.Builder
	for lineNo, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			text := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if strings.Contains(text, ":") {
				header.WriteString(text)
				header.WriteByte('\n')
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, "", fmt.Errorf("line %d: expected key = \"value\"", lineNo+1)
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t.") {
			return nil, "", fmt.Errorf("line %d: invalid key %q", lineNo+1, key)
		}
		text, err := parseTOMLString(strings.TrimSpace(value))
		if err != nil {
			return nil, "", fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		messages[key] = text
	}
	return messages, header.String(), nil
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

func parseProperties(src string) (map[string]string, string) {
	messages := map[string]string{}
	var header strings.Builder
	for _, line := range logicalPropertyLines(src) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			text := strings.TrimSpace(strings.TrimLeft(trimmed, "#!"))
			if strings.Contains(text, ":") {
				header.WriteString(text)
				header.WriteByte('\n')
			}
			continue
		}
		key, value, ok := splitPropertyLine(line)
		if !ok {
			continue
		}
		key = unescapeProperty(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		messages[key] = unescapeProperty(value)
	}
	return messages, header.String()
}

func logicalPropertyLines(src string) []string {
	rawLines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	var current strings.Builder
	continued := false
	for _, raw := range rawLines {
		line := strings.TrimRight(raw, "\r")
		if continued {
			line = strings.TrimLeft(line, " \t\f")
		}
		current.WriteString(line)
		if hasPropertyContinuation(line) {
			text := current.String()
			current.Reset()
			current.WriteString(text[:len(text)-1])
			continued = true
			continue
		}
		lines = append(lines, current.String())
		current.Reset()
		continued = false
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

func hasPropertyContinuation(line string) bool {
	slashes := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		slashes++
	}
	return slashes%2 == 1
}

func splitPropertyLine(line string) (string, string, bool) {
	i := 0
	for i < len(line) && isPropertySpace(line[i]) {
		i++
	}
	start := i
	escaped := false
	for i < len(line) {
		ch := line[i]
		if escaped {
			escaped = false
			i++
			continue
		}
		if ch == '\\' {
			escaped = true
			i++
			continue
		}
		if ch == '=' || ch == ':' || isPropertySpace(ch) {
			break
		}
		i++
	}
	if start == i {
		return "", "", false
	}
	key := line[start:i]
	for i < len(line) && isPropertySpace(line[i]) {
		i++
	}
	if i < len(line) && (line[i] == '=' || line[i] == ':') {
		i++
	}
	for i < len(line) && isPropertySpace(line[i]) {
		i++
	}
	return key, line[i:], true
}

func isPropertySpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\f'
}

func unescapeProperty(raw string) string {
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch != '\\' || i == len(raw)-1 {
			out.WriteByte(ch)
			continue
		}
		i++
		switch raw[i] {
		case 't':
			out.WriteByte('\t')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 'f':
			out.WriteByte('\f')
		case 'u':
			if i+4 >= len(raw) {
				out.WriteString(`\u`)
				continue
			}
			v, err := strconv.ParseUint(raw[i+1:i+5], 16, 16)
			if err != nil {
				out.WriteString(`\u`)
				continue
			}
			r := rune(v)
			i += 4
			if utf16.IsSurrogate(r) && i+6 < len(raw) && raw[i+1] == '\\' && raw[i+2] == 'u' {
				if low, err := strconv.ParseUint(raw[i+3:i+7], 16, 16); err == nil {
					if decoded := utf16.DecodeRune(r, rune(low)); decoded != '\uFFFD' {
						r = decoded
						i += 6
					}
				}
			}
			out.WriteRune(r)
		default:
			out.WriteByte(raw[i])
		}
	}
	return out.String()
}

type poEntry struct {
	msgid  string
	msgstr string
}

func parsePO(src string) ([]poEntry, string) {
	var entries []poEntry
	var cur poEntry
	var state string
	var header string
	commit := func() {
		if cur.msgid == "" && cur.msgstr != "" {
			header = cur.msgstr
		}
		if cur.msgid != "" || cur.msgstr != "" {
			entries = append(entries, cur)
		}
		cur = poEntry{}
		state = ""
	}

	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			commit()
		case strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "msgid "):
			cur.msgid = parsePOString(line[len("msgid "):])
			state = "msgid"
		case strings.HasPrefix(line, "msgstr "):
			cur.msgstr = parsePOString(line[len("msgstr "):])
			state = "msgstr"
		case strings.HasPrefix(line, "msgstr["):
			if idx := strings.Index(line, " "); idx >= 0 {
				cur.msgstr = parsePOString(line[idx+1:])
				state = "msgstr"
			}
		case strings.HasPrefix(line, "\""):
			switch state {
			case "msgid":
				cur.msgid += parsePOString(line)
			case "msgstr":
				cur.msgstr += parsePOString(line)
			}
		}
	}
	commit()
	return entries, header
}

func parsePOString(raw string) string {
	s, err := strconv.Unquote(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return s
}

func headerValue(header, key string) string {
	key = strings.ToLower(key)
	for _, line := range strings.Split(header, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(name)) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
