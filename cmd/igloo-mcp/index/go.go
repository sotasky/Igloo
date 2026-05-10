package index

import (
	"regexp"
	"strings"
)

// ── Handler scanning ─────────────────────────────────────────────────────────

var (
	rHandlerRoute     = regexp.MustCompile(`mux\.Handle(?:Func)?\(\s*"(\w+)\s+([^"]+)"\s*,\s*(?:s\.)?(\w+)`)
	rTmplRender       = regexp.MustCompile(`(?:s\.render|ExecuteTemplate)\s*\([^,]*,\s*(?:[^,]*,\s*)?["']([^"']+)["']`)
	rSQLRead          = regexp.MustCompile(`(?i)(?:SELECT\s+.*?\s+FROM|FROM|JOIN)\s+(\w+)`)
	rSQLWrite         = regexp.MustCompile(`(?i)(?:INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM)\s+(\w+)`)
	rPageScripts      = regexp.MustCompile(`PageScripts\s*=\s*\[\]string\{([^}]+)\}`)
	rPageScriptsEntry = regexp.MustCompile(`"([^"]+)"`)
	rESBundle         = regexp.MustCompile(`ESBundle\s*=\s*"([^"]+)"`)
	rTemplComponent   = regexp.MustCompile(`components\.([A-Z]\w+)\s*\(`)
)

var sqlKeywords = map[string]bool{
	"select": true, "from": true, "where": true, "join": true, "left": true,
	"right": true, "inner": true, "outer": true, "on": true, "and": true,
	"or": true, "not": true, "in": true, "as": true, "set": true, "values": true,
	"into": true, "order": true, "group": true, "by": true, "having": true,
	"limit": true, "offset": true, "union": true, "insert": true, "update": true,
	"delete": true, "create": true, "drop": true, "alter": true, "index": true,
	"table": true, "view": true, "trigger": true, "case": true, "when": true,
	"then": true, "else": true, "end": true, "null": true, "true": true,
	"false": true, "like": true, "between": true, "exists": true, "distinct": true,
	"count": true, "sum": true, "avg": true, "min": true, "max": true,
	"coalesce": true, "ifnull": true, "begin": true, "commit": true,
	"rollback": true, "pragma": true,
	// Single-letter aliases and common Go false positives
	"a": true, "b": true, "c": true, "d": true, "e": true, "f": true,
	"t": true, "r": true, "s": true, "v": true, "w": true, "m": true,
	"an": true, "id": true, "api": true, "err": true, "row": true, "rows": true,
	"body": true, "category": true, "tx": true, "db": true, "ctx": true,
	"key": true, "val": true, "ok": true, "the": true, "current": true,
	"dir": true, "disk": true, "download": true, "extension": true,
	"filename": true, "ingest": true, "job": true, "likes": true, "link": true,
	"media": true, "metadata": true, "open": true, "phase": true,
	"platform": true, "quote": true, "shares": true, "sourcehandle": true,
	"sponsorblock": true, "storage": true, "suffix": true, "text": true,
	"tweet": true, "uploaded": true, "url": true, "youtube": true, "is_starred": true,
	// Comment false positives (FROM x in English sentences)
	"cdn": true, "lost": true, "scratch": true, "disk_path": true,
	"x_feed_fetch_delay": true, "youtube_fetch_delay": true, "tiktok_fetch_delay": true,
	"instagram_fetch_delay": true, "it": true, "this": true, "that": true,
	"which": true, "each": true, "any": true, "there": true, "here": true,
}

var areaRules = []struct{ prefix, area string }{
	{"/api/feed/", "feed"}, {"/api/rsshub/", "feed"},
	{"/api/thumbnail", "media"}, {"/api/preview", "media"},
	{"/api/stream/", "media"}, {"/api/slide/", "media"},
	{"/api/video", "videos"}, {"/api/channel", "channels"},
	{"/api/bookmark", "bookmarks"},
	{"/api/admin/", "admin"}, {"/api/auth/", "auth"},
	{"/api/search/", "search"}, {"/api/logs/", "logs"},
	{"/api/subscribe", "channels"}, {"/api/unsubscribe", "channels"},
	{"/api/download", "downloads"}, {"/api/status", "admin"},
	{"/api/sync/", "sync"}, {"/api/translate/", "translate"},
	{"/api/subtitle/", "videos"}, {"/api/shorts/", "shorts"},
	{"/api/media/", "media"}, {"/api/quick-download", "downloads"},
	{"/api/cancel-download", "downloads"}, {"/api/queue", "admin"},
	{"/api/stats", "admin"}, {"/api/resume", "admin"}, {"/api/stop", "admin"},
	{"/api/profile-card", "channels"}, {"/api/server/", "admin"},
	{"/feed", "feed"}, {"/liked", "feed"}, {"/shorts", "shorts"},
	{"/videos", "videos"}, {"/channels", "channels"}, {"/creators", "channels"},
	{"/bookmarks", "bookmarks"},
	{"/login", "auth"}, {"/logout", "auth"},
	{"/player/", "videos"}, {"/analytics", "admin"},
}

func classifyArea(path string) string {
	for _, r := range areaRules {
		if strings.HasPrefix(path, r.prefix) {
			return r.area
		}
	}
	return "other"
}

// PageScriptEntry maps a handler function to its PageScripts assignment.
type PageScriptEntry struct {
	HandlerFunc string
	Scripts     []string
	Line        int
}

// ESBundleEntry maps a handler function to its ESBundle assignment.
type ESBundleEntry struct {
	HandlerFunc string
	Bundle      string
	Line        int
}

// GoHandlerResult is the output of ScanGoHandlers.
type GoHandlerResult struct {
	Endpoints       []Endpoint
	Templates       []string
	Tables          []TableRef
	PageScripts     []PageScriptEntry
	ESBundles       []ESBundleEntry
	TemplComponents []string
}

// ScanGoHandlers parses a Go source file for HTTP routes, template renders, and SQL table refs.
func ScanGoHandlers(source, filepath string) GoHandlerResult {
	var result GoHandlerResult
	lines := strings.Split(source, "\n")

	// Routes
	for _, m := range rHandlerRoute.FindAllStringSubmatchIndex(source, -1) {
		method := source[m[2]:m[3]]
		path := source[m[4]:m[5]]
		handler := source[m[6]:m[7]]
		line := strings.Count(source[:m[0]], "\n") + 1
		kind := "page"
		if strings.HasPrefix(path, "/api/") {
			kind = "api"
		}
		result.Endpoints = append(result.Endpoints, Endpoint{
			Method:      method,
			Path:        path,
			HandlerFile: filepath,
			HandlerFunc: handler,
			HandlerLine: line,
			Area:        classifyArea(path),
			Kind:        kind,
		})
	}

	// Templates
	seen := map[string]bool{}
	for _, m := range rTmplRender.FindAllStringSubmatch(source, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result.Templates = append(result.Templates, m[1])
		}
	}

	// SQL table refs (line-by-line)
	for i, line := range lines {
		lineno := i + 1
		for _, m := range rSQLRead.FindAllStringSubmatch(line, -1) {
			tbl := strings.Trim(m[1], "`\"")
			if !sqlKeywords[strings.ToLower(tbl)] {
				result.Tables = append(result.Tables, TableRef{
					Table: strings.ToLower(tbl), File: filepath,
					Line: lineno, Mode: "read", Context: "SELECT",
				})
			}
		}
		for _, m := range rSQLWrite.FindAllStringSubmatch(line, -1) {
			tbl := strings.Trim(m[1], "`\"")
			if !sqlKeywords[strings.ToLower(tbl)] {
				upper := strings.ToUpper(line)
				ctx := "DELETE"
				if strings.Contains(upper, "INSERT") {
					ctx = "INSERT"
				} else if strings.Contains(upper, "UPDATE") {
					ctx = "UPDATE"
				}
				result.Tables = append(result.Tables, TableRef{
					Table: strings.ToLower(tbl), File: filepath,
					Line: lineno, Mode: "write", Context: ctx,
				})
			}
		}
	}

	// Build function line ranges for PageScripts/ESBundle attribution
	type funcRange struct {
		name string
		line int
	}
	var funcRanges []funcRange
	for i, l := range lines {
		if fm := rGoMethod.FindStringSubmatch(l); fm != nil {
			funcRanges = append(funcRanges, funcRange{name: fm[2], line: i + 1})
		}
	}
	nearestFunc := func(atLine int) string {
		name := ""
		for _, fr := range funcRanges {
			if fr.line <= atLine {
				name = fr.name
			}
		}
		return name
	}

	for _, m := range rPageScripts.FindAllStringSubmatchIndex(source, -1) {
		psLine := strings.Count(source[:m[0]], "\n") + 1
		entries := source[m[2]:m[3]]
		var scripts []string
		for _, e := range rPageScriptsEntry.FindAllStringSubmatch(entries, -1) {
			scripts = append(scripts, e[1])
		}
		result.PageScripts = append(result.PageScripts, PageScriptEntry{
			HandlerFunc: nearestFunc(psLine), Scripts: scripts, Line: psLine,
		})
	}

	// ESBundle assignments (p.ESBundle = "js/dist/player.js")
	for _, m := range rESBundle.FindAllStringSubmatchIndex(source, -1) {
		line := strings.Count(source[:m[0]], "\n") + 1
		bundle := source[m[2]:m[3]]
		result.ESBundles = append(result.ESBundles, ESBundleEntry{
			HandlerFunc: nearestFunc(line), Bundle: bundle, Line: line,
		})
	}

	// Templ component calls (components.FeedPage(...))
	compSeen := map[string]bool{}
	for _, m := range rTemplComponent.FindAllStringSubmatch(source, -1) {
		if !compSeen[m[1]] {
			compSeen[m[1]] = true
			result.TemplComponents = append(result.TemplComponents, m[1])
		}
	}

	return result
}

// ── Symbol extraction ─────────────────────────────────────────────────────────

var (
	rGoPkg         = regexp.MustCompile(`(?m)^package\s+(\w+)`)
	rGoFunc        = regexp.MustCompile(`(?m)^func\s+(\w+)\s*\(([^)]*)\)(?:\s+(?:\([^)]*\)|[\w.*\[\]]+))?\s*\{`)
	rGoMethod      = regexp.MustCompile(`(?m)^func\s+\(\s*\w+\s+\*?(\w+)\s*\)\s+(\w+)\s*\(([^)]*)\)(?:\s+(?:\([^)]*\)|[\w.*\[\]]+))?\s*\{`)
	rGoType        = regexp.MustCompile(`(?m)^type\s+(\w+)\s+(struct|interface)\s*\{`)
	rGoVar         = regexp.MustCompile(`(?m)^var\s+([A-Z]\w+)\s+`)
	rGoConst1      = regexp.MustCompile(`(?m)^const\s+([A-Z]\w+)\s+`)
	rGoConst2      = regexp.MustCompile(`(?ms)^const\s*\(([^)]+)\)`)
	rGoConst2Entry = regexp.MustCompile(`(?m)^\s+([A-Z]\w+)\s*=`)
	rGoCall        = regexp.MustCompile(`(\w+)\.(\w+)\s*\(`)
)

var skipReceivers = map[string]bool{
	"fmt": true, "log": true, "slog": true, "os": true, "io": true,
	"strings": true, "strconv": true, "time": true, "json": true,
	"http": true, "filepath": true, "bytes": true, "sort": true,
	"sync": true, "context": true, "errors": true, "regexp": true,
	"math": true, "encoding": true, "crypto": true, "bufio": true,
	"testing": true, "template": true, "net": true, "url": true,
	"xml": true, "html": true, "sql": true,
}

// ExtractGoSymbols extracts symbol definitions and call references from a Go source file.
func ExtractGoSymbols(source, filepath string) ([]Symbol, []Reference) {
	var syms []Symbol
	var refs []Reference

	pkg := ""
	if m := rGoPkg.FindStringSubmatch(source); m != nil {
		pkg = m[1]
	}
	lines := strings.Split(source, "\n")

	lineOf := func(pos int) int { return strings.Count(source[:pos], "\n") + 1 }

	// Types
	for _, m := range rGoType.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		kind := "class"
		if source[m[4]:m[5]] == "interface" {
			kind = "interface"
		}
		ln := lineOf(m[0])
		syms = append(syms, Symbol{
			Name: name, Kind: kind, File: filepath, Line: ln,
			Language: "go", Signature: "type " + name + " " + source[m[4]:m[5]],
			Parent: pkg,
		})
	}

	// Methods (collect positions to skip in func pass)
	methodPos := map[int]bool{}
	for _, m := range rGoMethod.FindAllStringSubmatchIndex(source, -1) {
		methodPos[m[0]] = true
		receiver := source[m[2]:m[3]]
		name := source[m[4]:m[5]]
		args := strings.TrimSpace(source[m[6]:m[7]])
		ln := lineOf(m[0])
		syms = append(syms, Symbol{
			Name: name, Kind: "method", File: filepath, Line: ln,
			Language:  "go",
			Signature: "func (" + receiver + ") " + name + "(" + args + ")",
			Parent:    receiver,
		})
	}

	// Functions
	for _, m := range rGoFunc.FindAllStringSubmatchIndex(source, -1) {
		if methodPos[m[0]] {
			continue
		}
		name := source[m[2]:m[3]]
		args := strings.TrimSpace(source[m[4]:m[5]])
		ln := lineOf(m[0])
		syms = append(syms, Symbol{
			Name: name, Kind: "function", File: filepath, Line: ln,
			Language: "go", Signature: "func " + name + "(" + args + ")",
		})
	}

	// Package-level vars (exported)
	for _, m := range rGoVar.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		syms = append(syms, Symbol{
			Name: name, Kind: "variable", File: filepath,
			Line: lineOf(m[0]), Language: "go",
		})
	}

	// Constants (single-line, exported)
	for _, m := range rGoConst1.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		syms = append(syms, Symbol{
			Name: name, Kind: "constant", File: filepath,
			Line: lineOf(m[0]), Language: "go",
		})
	}

	// Constants (block form, exported)
	for _, bm := range rGoConst2.FindAllStringSubmatchIndex(source, -1) {
		blockStart := lineOf(bm[0])
		block := source[bm[2]:bm[3]]
		for _, em := range rGoConst2Entry.FindAllStringSubmatchIndex(block, -1) {
			name := block[em[2]:em[3]]
			ln := blockStart + strings.Count(block[:em[0]], "\n")
			syms = append(syms, Symbol{
				Name: name, Kind: "constant", File: filepath,
				Line: ln, Language: "go",
			})
		}
	}

	// Call references: receiver.Method(
	for _, m := range rGoCall.FindAllStringSubmatchIndex(source, -1) {
		receiver := source[m[2]:m[3]]
		callee := source[m[4]:m[5]]
		if skipReceivers[receiver] {
			continue
		}
		ln := lineOf(m[0])
		ctx := ""
		if ln-1 < len(lines) {
			ctx = strings.TrimSpace(lines[ln-1])
		}
		refs = append(refs, Reference{
			SymbolName:   callee,
			QualifiedRef: receiver + "." + callee,
			File:         filepath,
			Line:         ln,
			Context:      ctx,
		})
	}

	return syms, refs
}
