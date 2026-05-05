package index

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var jsDescriptions = map[string]string{
	"site_base.js":               "Core UI: navigation, modal system, preferences, keyboard shortcuts",
	"src/shorts/index.js":        "ES module shorts page entry point",
	"src/shorts/playback.js":     "ES module shorts video and slideshow playback",
	"src/shorts/overlay.js":      "ES module shorts overlay navigation and windowing",
	"src/shorts/items.js":        "ES module shorts DOM builder and action handlers",
	"src/player/index.js":        "ES module player entry: init, speed menu, fullscreen, actions, keyboard, bookmark",
	"src/player/sponsorblock.js": "ES module SponsorBlock segment skipping and timeline colors",
	"src/player/comments.js":     "ES module comment tree builder, rendering, refresh, seek links",
	"src/player/preview.js":      "ES module VTT preview hover with sprite frame overlay",
	"src/player/progress.js":     "ES module watch position save/resume, autoplay next, sync",
	"src/player/subtitles.js":    "ES module subtitle track discovery and menu (reserved)",
	"video_cards.js":             "Shared video card rendering and thumbnail logic",
	"infinite_page.js":           "Generic infinite scroll pagination for list pages",
	"sync_poller.js":             "Polls server for sync status updates",
	"videojs_compat.js":          "Video.js / media-chrome compatibility layer",
	"media-chrome-loader.js":     "Lazy loader for media-chrome web components",
	"src/utils.js":               "ES module shared utilities: API fetch, icons, state helpers",
	"src/bookmark-menu.js":       "ES module global bookmark popover",
	"src/feed/index.js":          "ES module feed page entry point",
	"src/feed/media-overlay.js":  "ES module media overlay",
	"src/feed/inline-media.js":   "ES module inline video autoplay",
	"src/feed/text-clamp.js":     "ES module text clamp",
	"src/feed/dates.js":          "ES module relative date formatting",
	"src/feed/rerank.js":         "ES module feed reranking",
	"src/feed/seen.js":           "ES module seen tracking",
	"src/feed/translate.js":      "ES module translation",
	"src/feed/status.js":         "ES module feed status polling",
}

// CodeIndex is the in-memory codebase index.
type CodeIndex struct {
	root            string
	endpoints       map[string]*Endpoint  // key -> Endpoint
	files           map[string]*FileNode  // relpath -> FileNode
	tables          map[string][]TableRef // table -> refs
	areas           map[string][]string   // area -> relpaths
	symbols         []Symbol
	refs            []Reference
	apiCallers      map[string][]APICaller         // api path prefix -> callers
	templComps      map[string]*TemplComponentInfo // component name -> info
	workers         []WorkerInfo
	jsFiles         map[string]*JSFileInfo       // js filename -> info
	pageScripts     map[string][]PageScriptEntry // handler file -> entries
	esBundles       map[string][]ESBundleEntry   // handler file -> entries
	androidScreens  []AndroidScreenInfo
	androidVMs      []AndroidVMInfo
	androidRepos    []AndroidRepoInfo
	androidDAOs     []AndroidDAOInfo
	androidEntities []AndroidEntityInfo
	androidNavOrder []string
	androidClasses  map[string]*AndroidClassInfo
	debugEvents     []DebugEvent
	buildTime       time.Duration
}

// New returns an empty CodeIndex rooted at the given directory.
func New(root string) *CodeIndex {
	return &CodeIndex{
		root:           root,
		endpoints:      map[string]*Endpoint{},
		files:          map[string]*FileNode{},
		tables:         map[string][]TableRef{},
		areas:          map[string][]string{},
		apiCallers:     map[string][]APICaller{},
		templComps:     map[string]*TemplComponentInfo{},
		jsFiles:        map[string]*JSFileInfo{},
		pageScripts:    map[string][]PageScriptEntry{},
		esBundles:      map[string][]ESBundleEntry{},
		androidClasses: map[string]*AndroidClassInfo{},
	}
}

// Build scans all source files and populates the index. Returns a stats string.
func (idx *CodeIndex) Build() string {
	start := time.Now()
	idx.scanGoHandlers()
	idx.scanGoDBTables()
	idx.scanTemplComponents()
	idx.scanJS()
	idx.linkJSToPages()
	idx.scanAndroid()
	idx.computeAndroidGraph()
	idx.computeAreas()
	idx.scanGoSymbols()
	idx.scanCSSSymbols()
	idx.scanWorkers()
	idx.buildTime = time.Since(start)
	return fmt.Sprintf("endpoints=%d files=%d tables=%d areas=%d symbols=%d templ_components=%d workers=%d debug_events=%d time=%dms",
		len(idx.endpoints), len(idx.files), len(idx.tables),
		len(idx.areas), len(idx.symbols), len(idx.templComps), len(idx.workers),
		len(idx.debugEvents), idx.buildTime.Milliseconds())
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (idx *CodeIndex) readFile(relpath string) string {
	data, err := os.ReadFile(filepath.Join(idx.root, relpath))
	if err != nil {
		return ""
	}
	return string(data)
}

func (idx *CodeIndex) relpath(abs string) string {
	rel, err := filepath.Rel(idx.root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

func (idx *CodeIndex) ensureFile(relpath, layer string) *FileNode {
	if _, ok := idx.files[relpath]; !ok {
		idx.files[relpath] = &FileNode{Path: relpath, Layer: layer}
	}
	return idx.files[relpath]
}

func (idx *CodeIndex) addAPICaller(urlClean string, c APICaller) {
	url := strings.TrimRight(urlClean, "/")
	idx.apiCallers[url] = append(idx.apiCallers[url], c)
}

// ── scanners ──────────────────────────────────────────────────────────────────

func (idx *CodeIndex) scanGoHandlers() {
	webDir := filepath.Join(idx.root, "internal", "web")
	entries, _ := os.ReadDir(webDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		relpath := "internal/web/" + e.Name()
		source := idx.readFile(relpath)
		if source == "" {
			continue
		}
		result := ScanGoHandlers(source, relpath)
		node := idx.ensureFile(relpath, "server")
		for i := range result.Endpoints {
			ep := &result.Endpoints[i]
			idx.endpoints[ep.Key()] = ep
			node.Endpoints = append(node.Endpoints, ep.Key())
		}
		for _, t := range result.Tables {
			t.File = relpath
			idx.tables[t.Table] = append(idx.tables[t.Table], t)
			node.DBTables = append(node.DBTables, t)
		}
		for _, comp := range result.TemplComponents {
			node.Imports = append(node.Imports, comp)
		}
		if len(result.PageScripts) > 0 {
			idx.pageScripts[relpath] = result.PageScripts
		}
		if len(result.ESBundles) > 0 {
			idx.esBundles[relpath] = result.ESBundles
		}
	}
}

func (idx *CodeIndex) scanGoDBTables() {
	dirs := []string{
		"internal/db", "internal/feed", "internal/rsshub",
		"internal/download", "internal/worker", "internal/subscribe",
		"internal/settings", "internal/fxtwitter",
	}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(filepath.Join(idx.root, dir))
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			relpath := dir + "/" + e.Name()
			if node, ok := idx.files[relpath]; ok && len(node.DBTables) > 0 {
				continue
			}
			source := idx.readFile(relpath)
			if source == "" {
				continue
			}
			result := ScanGoHandlers(source, relpath)
			if len(result.Tables) == 0 {
				continue
			}
			node := idx.ensureFile(relpath, "server")
			for _, t := range result.Tables {
				t.File = relpath
				idx.tables[t.Table] = append(idx.tables[t.Table], t)
				node.DBTables = append(node.DBTables, t)
			}
		}
	}
}

func (idx *CodeIndex) scanJS() {
	jsDir := filepath.Join(idx.root, "static", "js")
	filepath.WalkDir(jsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		relpath := idx.relpath(path)
		// jsKey is relative to static/js/ (e.g. "src/feed/index.js", "site_base.js")
		jsKey := strings.TrimPrefix(relpath, "static/js/")

		source := idx.readFile(relpath)
		idx.ensureFile(relpath, "js")

		// Skip dist/ bundles for API call/symbol scanning — they duplicate src/ content
		if !strings.HasPrefix(jsKey, "dist/") {
			apiCalls := ScanJS(source, relpath)
			for _, c := range apiCalls {
				url := strings.SplitN(c.URL, "?", 2)[0]
				url = strings.TrimRight(url, "/")
				idx.addAPICaller(url, APICaller{
					File: relpath, Method: c.Method, Line: c.Line, Source: "js",
				})
			}

			syms := ScanJSSymbols(source, relpath)
			idx.symbols = append(idx.symbols, syms...)

			info := &JSFileInfo{
				Path:        relpath,
				Description: jsDescriptions[jsKey],
				Symbols:     syms,
				APICalls:    apiCalls,
			}
			idx.jsFiles[jsKey] = info
		}
		return nil
	})
}

func (idx *CodeIndex) scanAndroid() {
	ktBase := filepath.Join(idx.root, "android", "app", "src", "main", "java")
	filepath.WalkDir(ktBase, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".kt") {
			return nil
		}
		relpath := idx.relpath(path)
		if strings.Contains(relpath, ".gradle-home") || strings.Contains(relpath, "/build/") {
			return nil
		}
		source := idx.readFile(relpath)
		result := ScanKotlin(source, relpath)
		node := idx.ensureFile(relpath, "android")
		node.Exports = append(node.Exports, result.ViewModelRefs...)
		if result.ClassName != "" {
			node.Exports = append(node.Exports, result.ClassName)
		}
		for _, c := range result.APICalls {
			path2 := strings.TrimRight(c.Path, "/")
			idx.addAPICaller(path2, APICaller{
				File: relpath, Method: c.Method, Line: c.Line, Source: "android",
			})
		}
		for _, e := range result.Entities {
			tref := TableRef{
				Table: e.TableName, File: relpath, Line: 0,
				Mode: "read_write", Context: "@Entity(" + e.ClassName + ")",
			}
			idx.tables[e.TableName] = append(idx.tables[e.TableName], tref)
			node.DBTables = append(node.DBTables, tref)
		}
		for _, d := range result.DAOTables {
			if d.Table == "__upsert__" {
				continue
			}
			tref := TableRef{
				Table: d.Table, File: relpath, Line: d.Line,
				Mode: d.Mode, Context: d.Context,
			}
			idx.tables[d.Table] = append(idx.tables[d.Table], tref)
			node.DBTables = append(node.DBTables, tref)
		}
		idx.symbols = append(idx.symbols, result.Symbols...)
		idx.debugEvents = append(idx.debugEvents, ScanDebugEvents(source, relpath)...)
		return nil
	})
}

func (idx *CodeIndex) scanGoSymbols() {
	goDirs := []string{
		"cmd/igloo", "internal/auth", "internal/config", "internal/db",
		"internal/download", "internal/feed", "internal/model",
		"internal/rsshub", "internal/subscribe",
		"internal/web", "internal/worker",
		"internal/settings", "internal/fxtwitter",
	}
	for _, dir := range goDirs {
		entries, _ := os.ReadDir(filepath.Join(idx.root, dir))
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			relpath := dir + "/" + e.Name()
			source := idx.readFile(relpath)
			syms, refs := ExtractGoSymbols(source, relpath)
			idx.symbols = append(idx.symbols, syms...)
			idx.refs = append(idx.refs, refs...)
		}
	}
}

func (idx *CodeIndex) scanCSSSymbols() {
	cssDir := filepath.Join(idx.root, "static")
	filepath.WalkDir(cssDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".css") {
			return nil
		}
		relpath := idx.relpath(path)
		source := idx.readFile(relpath)
		idx.symbols = append(idx.symbols, ScanCSS(source, relpath)...)
		return nil
	})
}

func (idx *CodeIndex) computeAreas() {
	areaFiles := map[string]map[string]bool{}
	for _, ep := range idx.endpoints {
		if ep.Area != "" && ep.Area != "other" && ep.Area != "page" {
			if areaFiles[ep.Area] == nil {
				areaFiles[ep.Area] = map[string]bool{}
			}
			areaFiles[ep.Area][ep.HandlerFile] = true
		}
	}

	// Forward adjacency from imports
	forward := map[string][]string{}
	for relpath, node := range idx.files {
		forward[relpath] = append(forward[relpath], node.Imports...)
	}

	// BFS outward from seed files
	for area, seeds := range areaFiles {
		visited := map[string]bool{}
		queue := make([]string, 0, len(seeds))
		for f := range seeds {
			visited[f] = true
			queue = append(queue, f)
		}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, dep := range forward[cur] {
				if !visited[dep] {
					if _, ok := idx.files[dep]; ok {
						visited[dep] = true
						queue = append(queue, dep)
					}
				}
			}
		}
		// Also add API callers
		for _, ep := range idx.endpoints {
			if ep.Area == area {
				for _, c := range idx.findCallers(ep.Path) {
					visited[c.File] = true
				}
			}
		}
		for f := range visited {
			idx.areas[area] = append(idx.areas[area], f)
		}
	}

	// Populate ImportedBy
	for relpath, node := range idx.files {
		for _, imp := range node.Imports {
			if target, ok := idx.files[imp]; ok {
				target.ImportedBy = append(target.ImportedBy, relpath)
			}
		}
	}
}

var rGoPathParam = regexp.MustCompile(`\{[^}]+\}`)

func (idx *CodeIndex) findCallers(epPath string) []APICaller {
	epStatic := strings.TrimRight(rGoPathParam.ReplaceAllString(epPath, ""), "/")
	var out []APICaller
	for callerURL, callers := range idx.apiCallers {
		if callerURL == epStatic || callerURL == strings.TrimRight(epPath, "/") {
			out = append(out, callers...)
		} else if len(epStatic) > 5 && strings.HasPrefix(epStatic, callerURL) {
			out = append(out, callers...)
		} else if len(epStatic) > 5 && strings.HasPrefix(callerURL, epStatic) {
			out = append(out, callers...)
		}
	}
	return out
}

// ── query methods ─────────────────────────────────────────────────────────────

// TraceEndpoint returns handler, templates, callers, and DB tables for an endpoint.
func (idx *CodeIndex) TraceEndpoint(path, method string) string {
	var matches []*Endpoint
	for _, ep := range idx.endpoints {
		if strings.Contains(ep.Path, path) {
			if method == "" || strings.EqualFold(ep.Method, method) {
				matches = append(matches, ep)
			}
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No endpoints found matching '%s'", path)
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Path+matches[i].Method < matches[j].Path+matches[j].Method
	})

	var sb strings.Builder
	for _, ep := range matches {
		fmt.Fprintf(&sb, "%s %s\n", ep.Method, ep.Path)
		fmt.Fprintf(&sb, "  handler:  %s:%d -> %s()\n", ep.HandlerFile, ep.HandlerLine, ep.HandlerFunc)
		fmt.Fprintf(&sb, "  area:     %s\n", ep.Area)
		fmt.Fprintf(&sb, "  kind:     %s\n", ep.Kind)

		if node, ok := idx.files[ep.HandlerFile]; ok {
			if len(node.Imports) > 0 {
				var comps []string
				for _, imp := range node.Imports {
					if _, ok := idx.templComps[imp]; ok {
						comps = append(comps, imp)
					}
				}
				if len(comps) > 0 {
					fmt.Fprintf(&sb, "  templ:     %v\n", comps)
				}
			}
			reads := dedupTableNames(node.DBTables, "read", "read_write")
			writes := dedupTableNames(node.DBTables, "write", "read_write")
			if len(reads) > 0 {
				fmt.Fprintf(&sb, "  db_reads:  %v\n", reads)
			}
			if len(writes) > 0 {
				fmt.Fprintf(&sb, "  db_writes: %v\n", writes)
			}
		}

		callers := idx.findCallers(ep.Path)
		jsFiles := uniqueFiles(callers, "js")
		androidFiles := uniqueFiles(callers, "android")
		if len(jsFiles) > 0 {
			fmt.Fprintf(&sb, "  js:        %v\n", jsFiles)
		}
		if len(androidFiles) > 0 {
			fmt.Fprintf(&sb, "  android:   %v\n", androidFiles)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// TraceFile returns layer, endpoints, imports, exported names, and DB tables for a file.
func (idx *CodeIndex) TraceFile(path string) string {
	node, found := idx.files[path]
	if !found {
		for relpath, n := range idx.files {
			if strings.HasSuffix(relpath, path) || strings.Contains(relpath, path) {
				node = n
				path = relpath
				found = true
				break
			}
		}
	}
	if !found {
		return fmt.Sprintf("File not found in index: '%s'", path)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "File: %s\n  layer: %s\n", path, node.Layer)
	if len(node.Endpoints) > 0 {
		sb.WriteString("  endpoints:\n")
		for _, ek := range node.Endpoints {
			fmt.Fprintf(&sb, "    %s\n", ek)
		}
	}
	if len(node.Exports) > 0 {
		fmt.Fprintf(&sb, "  exports: %v\n", node.Exports)
	}
	if len(node.Imports) > 0 {
		fmt.Fprintf(&sb, "  imports: %v\n", node.Imports)
	}
	if len(node.ImportedBy) > 0 {
		fmt.Fprintf(&sb, "  imported_by: %v\n", node.ImportedBy)
	}
	if len(node.DBTables) > 0 {
		seen := map[string]bool{}
		var summary []string
		for _, t := range node.DBTables {
			k := t.Table + " (" + t.Mode + ")"
			if !seen[k] {
				seen[k] = true
				summary = append(summary, k)
			}
		}
		sort.Strings(summary)
		fmt.Fprintf(&sb, "  db_tables: %v\n", summary)
	}
	var memberAreas []string
	for area, files := range idx.areas {
		for _, f := range files {
			if f == path {
				memberAreas = append(memberAreas, area)
				break
			}
		}
	}
	if len(memberAreas) > 0 {
		sort.Strings(memberAreas)
		fmt.Fprintf(&sb, "  areas: %v\n", memberAreas)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// TraceDBTable returns all files that read/write a given table.
func (idx *CodeIndex) TraceDBTable(table string) string {
	tbl := strings.ToLower(table)
	refs, ok := idx.tables[tbl]
	if !ok {
		return fmt.Sprintf("Table '%s' not found in index", table)
	}
	byFile := map[string][]TableRef{}
	for _, r := range refs {
		byFile[r.File] = append(byFile[r.File], r)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Table: %s\n", tbl)
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		frefs := byFile[f]
		hasRead, hasWrite := false, false
		for _, r := range frefs {
			if r.Mode == "read" || r.Mode == "read_write" {
				hasRead = true
			}
			if r.Mode == "write" || r.Mode == "read_write" {
				hasWrite = true
			}
		}
		modes := []string{}
		if hasRead {
			modes = append(modes, "read")
		}
		if hasWrite {
			modes = append(modes, "write")
		}
		fmt.Fprintf(&sb, "  %s [%s]\n", f, strings.Join(modes, "+"))
		for _, r := range frefs {
			fmt.Fprintf(&sb, "    line %d: %s (%s)\n", r.Line, r.Context, r.Mode)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// GetContext returns all files and endpoints for a functional area.
func (idx *CodeIndex) GetContext(area string) string {
	areaLower := strings.ToLower(area)
	files, ok := idx.areas[areaLower]
	if !ok {
		var available []string
		for k := range idx.areas {
			available = append(available, k)
		}
		sort.Strings(available)
		return fmt.Sprintf("Area '%s' not found. Available: %v", area, available)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Area: %s\n\n", areaLower)

	var areaEPs []*Endpoint
	for _, ep := range idx.endpoints {
		if ep.Area == areaLower {
			areaEPs = append(areaEPs, ep)
		}
	}
	if len(areaEPs) > 0 {
		sb.WriteString("Endpoints:\n")
		sort.Slice(areaEPs, func(i, j int) bool {
			return areaEPs[i].Path+areaEPs[i].Method < areaEPs[j].Path+areaEPs[j].Method
		})
		for _, ep := range areaEPs {
			fmt.Fprintf(&sb, "  %-6s %s\n", ep.Method, ep.Path)
		}
		sb.WriteString("\n")
	}

	byLayer := map[string][]string{}
	for _, f := range files {
		node := idx.files[f]
		layer := "unknown"
		if node != nil {
			layer = node.Layer
		}
		byLayer[layer] = append(byLayer[layer], f)
	}
	for _, layer := range []string{"server", "template", "js", "android", "css"} {
		lf := byLayer[layer]
		if len(lf) > 0 {
			sort.Strings(lf)
			fmt.Fprintf(&sb, "%s files:\n", capitalize(layer))
			for _, f := range lf {
				fmt.Fprintf(&sb, "  %s\n", f)
			}
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ListEndpoints lists all endpoints, optionally filtered.
func (idx *CodeIndex) ListEndpoints(filter string) string {
	var eps []*Endpoint
	for _, ep := range idx.endpoints {
		if filter == "" ||
			strings.EqualFold(ep.Method, filter) ||
			strings.Contains(strings.ToLower(ep.Path), strings.ToLower(filter)) ||
			strings.Contains(strings.ToLower(ep.HandlerFile), strings.ToLower(filter)) ||
			strings.EqualFold(ep.Area, filter) {
			eps = append(eps, ep)
		}
	}
	if len(eps) == 0 {
		msg := "No endpoints found"
		if filter != "" {
			msg += fmt.Sprintf(" matching '%s'", filter)
		}
		return msg
	}
	sort.Slice(eps, func(i, j int) bool {
		return eps[i].Area+eps[i].Path+eps[i].Method < eps[j].Area+eps[j].Path+eps[j].Method
	})
	header := fmt.Sprintf("%-6s %-45s %-35s %s", "Method", "Path", "Handler", "Area")
	var sb strings.Builder
	sb.WriteString(header + "\n")
	sb.WriteString(strings.Repeat("-", len(header)) + "\n")
	for _, ep := range eps {
		handler := ep.HandlerFile + ":" + ep.HandlerFunc
		fmt.Fprintf(&sb, "%-6s %-45s %-35s %s\n", ep.Method, ep.Path, handler, ep.Area)
	}
	fmt.Fprintf(&sb, "\nTotal: %d endpoints", len(eps))
	return sb.String()
}

// TraceDataFlow traces how data flows from a DB table through the full stack.
// fromLayer and toLayer are reserved for future layer-range filtering; currently ignored.
func (idx *CodeIndex) TraceDataFlow(entity, fromLayer, toLayer string) string {
	_, _ = fromLayer, toLayer
	tbl := strings.ToLower(entity)
	if _, ok := idx.tables[tbl]; !ok {
		for k := range idx.tables {
			if strings.Contains(k, tbl) || strings.Contains(tbl, k) {
				tbl = k
				break
			}
		}
	}
	refs, ok := idx.tables[tbl]
	if !ok {
		var available []string
		for k := range idx.tables {
			available = append(available, k)
		}
		sort.Strings(available)
		return fmt.Sprintf("Table '%s' not found. Available: %v", entity, available)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Data flow: %s ===\n\n", tbl)

	// Layer 1: DB refs in internal/db/
	var dbRefs []TableRef
	for _, r := range refs {
		if strings.HasPrefix(r.File, "internal/db/") {
			dbRefs = append(dbRefs, r)
		}
	}
	fmt.Fprintf(&sb, "DB table: %s\n", tbl)
	for _, r := range dbRefs {
		fmt.Fprintf(&sb, "  %s:%d  %-10s  %s\n", r.File, r.Line, r.Mode, r.Context)
	}
	sb.WriteString("\n")

	// Layer 2: DB method symbols in those files
	dbFiles := map[string]bool{}
	for _, r := range dbRefs {
		dbFiles[r.File] = true
	}
	var dbMethodNames []string
	if len(dbFiles) > 0 {
		sb.WriteString("  |\n  v\nDatabase methods:\n")
		for _, sym := range idx.symbols {
			if dbFiles[sym.File] && (sym.Kind == "method" || sym.Kind == "function") {
				dbMethodNames = append(dbMethodNames, sym.Name)
				sig := sym.Name
				if sym.Signature != "" {
					sig = sym.Signature
				}
				fmt.Fprintf(&sb, "  %s:%d  %s\n", sym.File, sym.Line, sig)
			}
		}
		sb.WriteString("\n")
	}

	// Layer 3: Server handler refs
	dbMethodSet := map[string]bool{}
	for _, n := range dbMethodNames {
		dbMethodSet[n] = true
	}
	handlerFiles := map[string]bool{}
	var handlerLines []string
	for _, r := range refs {
		if !strings.HasPrefix(r.File, "internal/db/") && !strings.HasPrefix(r.File, "android/") {
			handlerFiles[r.File] = true
			handlerLines = append(handlerLines, fmt.Sprintf("  %s:%d  %-10s  %s", r.File, r.Line, r.Mode, r.Context))
		}
	}
	// Also find refs via symbol call graph
	candidatePrefixes := []string{"internal/web/", "internal/feed/", "internal/worker/", "internal/rsshub/"}
	for _, ref := range idx.refs {
		if !dbMethodSet[ref.SymbolName] {
			continue
		}
		for _, pfx := range candidatePrefixes {
			if strings.HasPrefix(ref.File, pfx) {
				handlerFiles[ref.File] = true
				handlerLines = append(handlerLines, fmt.Sprintf("  %s:%d  calls %s  %s", ref.File, ref.Line, ref.SymbolName, ref.Context))
				break
			}
		}
	}
	if len(handlerLines) > 0 {
		sb.WriteString("  |\n  v\nServer handlers:\n")
		for _, l := range handlerLines {
			sb.WriteString(l + "\n")
		}
		sb.WriteString("\n")
	}

	// Layer 4: API endpoints
	var matchedEPs []*Endpoint
	for _, ep := range idx.endpoints {
		if handlerFiles[ep.HandlerFile] {
			matchedEPs = append(matchedEPs, ep)
		}
	}
	if len(matchedEPs) > 0 {
		sb.WriteString("  |\n  v\nAPI endpoints:\n")
		sort.Slice(matchedEPs, func(i, j int) bool {
			return matchedEPs[i].Path+matchedEPs[i].Method < matchedEPs[j].Path+matchedEPs[j].Method
		})
		for _, ep := range matchedEPs {
			fmt.Fprintf(&sb, "  %-6s %s  (%s:%s)\n", ep.Method, ep.Path, ep.HandlerFile, ep.HandlerFunc)
		}
		sb.WriteString("\n")
	}

	// Layer 5: JS/Android callers
	var jsC, androidC []APICaller
	seen := map[string]bool{}
	for _, ep := range matchedEPs {
		for _, c := range idx.findCallers(ep.Path) {
			key := fmt.Sprintf("%s:%d:%s", c.File, c.Line, c.Source)
			if seen[key] {
				continue
			}
			seen[key] = true
			if c.Source == "js" {
				jsC = append(jsC, c)
			} else {
				androidC = append(androidC, c)
			}
		}
	}
	if len(jsC)+len(androidC) > 0 {
		sb.WriteString("  |\n  v\nClient callers:\n")
		for _, c := range jsC {
			fmt.Fprintf(&sb, "  [JS] %s:%d  %s\n", c.File, c.Line, c.Method)
		}
		for _, c := range androidC {
			fmt.Fprintf(&sb, "  [Android] %s:%d  %s\n", c.File, c.Line, c.Method)
		}
		sb.WriteString("\n")
	}

	// Layer 6: Android Room refs
	var androidRefs []TableRef
	for _, r := range refs {
		if strings.HasPrefix(r.File, "android/") {
			androidRefs = append(androidRefs, r)
		}
	}
	if len(androidRefs) > 0 {
		sb.WriteString("  |\n  v\nAndroid Room:\n")
		for _, r := range androidRefs {
			fmt.Fprintf(&sb, "  %s:%d  %-10s  %s\n", r.File, r.Line, r.Mode, r.Context)
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// FindSymbol searches symbols by name (substring), optionally filtered by kind and language.
func (idx *CodeIndex) FindSymbol(name, kind, language string) string {
	name = strings.ToLower(name)
	var matches []Symbol
	for _, s := range idx.symbols {
		if !strings.Contains(strings.ToLower(s.Name), name) {
			continue
		}
		if kind != "" && !strings.EqualFold(s.Kind, kind) {
			continue
		}
		if language != "" && !strings.EqualFold(s.Language, language) {
			continue
		}
		matches = append(matches, s)
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No symbols found matching '%s'", name)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Line < matches[j].Line
	})
	var sb strings.Builder
	for _, s := range matches {
		sig := s.Name
		if s.Signature != "" {
			sig = s.Signature
		}
		fmt.Fprintf(&sb, "%s:%d  [%s/%s]  %s\n", s.File, s.Line, s.Language, s.Kind, sig)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// FindReferences finds all call sites of a symbol, optionally restricted to a file.
func (idx *CodeIndex) FindReferences(name, file string) string {
	name = strings.ToLower(name)
	var matches []Reference
	for _, r := range idx.refs {
		if !strings.Contains(strings.ToLower(r.SymbolName), name) {
			continue
		}
		if file != "" && !strings.Contains(r.File, file) {
			continue
		}
		matches = append(matches, r)
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No references found for '%s'", name)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Line < matches[j].Line
	})
	var sb strings.Builder
	for _, r := range matches {
		fmt.Fprintf(&sb, "%s:%d  %s  %s\n", r.File, r.Line, r.SymbolName, r.Context)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// GetSiteTheme returns all CSS custom properties (theme variables).
func (idx *CodeIndex) GetSiteTheme() string {
	bySection := map[string][]Symbol{}
	var sections []string
	for _, s := range idx.symbols {
		if s.Language == "css" && s.Kind == "property" {
			sec := s.Parent
			if sec == "" {
				sec = "(global)"
			}
			if _, ok := bySection[sec]; !ok {
				sections = append(sections, sec)
			}
			bySection[sec] = append(bySection[sec], s)
		}
	}
	if len(bySection) == 0 {
		return "No CSS custom properties found in index"
	}
	var sb strings.Builder
	for _, sec := range sections {
		fmt.Fprintf(&sb, "%s:\n", sec)
		for _, s := range bySection[sec] {
			fmt.Fprintf(&sb, "  %s  (%s:%d)\n", s.Signature, s.File, s.Line)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// GetCSSComponent returns all CSS classes for a UI component.
func (idx *CodeIndex) GetCSSComponent(component string) string {
	comp := strings.ToLower(component)
	var matches []Symbol
	for _, s := range idx.symbols {
		if s.Language != "css" || s.Kind != "class" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(s.Name), comp) || strings.ToLower(s.Parent) == comp {
			matches = append(matches, s)
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No CSS classes found for component '%s'", component)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Line < matches[j].Line
	})
	var sb strings.Builder
	for _, s := range matches {
		fmt.Fprintf(&sb, ".%s  (%s:%d)\n", s.Name, s.File, s.Line)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dedupTableNames(tables []TableRef, modes ...string) []string {
	modeSet := map[string]bool{}
	for _, m := range modes {
		modeSet[m] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range tables {
		if modeSet[t.Mode] && !seen[t.Table] {
			seen[t.Table] = true
			out = append(out, t.Table)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueFiles(callers []APICaller, source string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range callers {
		if c.Source == source && !seen[c.File] {
			seen[c.File] = true
			out = append(out, c.File)
		}
	}
	sort.Strings(out)
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ── templ component scanning ─────────────────────────────────────────────────

func (idx *CodeIndex) scanTemplComponents() {
	templDir := filepath.Join(idx.root, "internal", "components")
	entries, _ := os.ReadDir(templDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".templ") {
			continue
		}
		relpath := "internal/components/" + e.Name()
		source := idx.readFile(relpath)
		if source == "" {
			continue
		}
		result := ScanTempl(source, relpath)
		node := idx.ensureFile(relpath, "template")

		for _, comp := range result.Components {
			info := &TemplComponentInfo{
				Name:   comp.Name,
				Params: comp.Params,
				File:   relpath,
				Line:   comp.Line,
			}
			idx.templComps[comp.Name] = info
			node.Exports = append(node.Exports, comp.Name)
			idx.symbols = append(idx.symbols, Symbol{
				Name:      comp.Name,
				Kind:      "function",
				File:      relpath,
				Line:      comp.Line,
				Language:  "templ",
				Signature: "templ " + comp.Name + "(" + comp.Params + ")",
			})
		}

		for _, call := range result.Calls {
			node.Imports = append(node.Imports, call)
		}

		idx.symbols = append(idx.symbols, result.Symbols...)
	}

	// Build cross-references
	for _, node := range idx.files {
		if !strings.HasSuffix(node.Path, ".templ") {
			continue
		}
		for _, call := range node.Imports {
			if comp, ok := idx.templComps[call]; ok {
				comp.CalledBy = append(comp.CalledBy, node.Path)
			}
		}
	}
}

// GetTemplComponent returns info about a templ component.
func (idx *CodeIndex) GetTemplComponent(name string) string {
	nameLower := strings.ToLower(name)

	// Exact match first
	if comp, ok := idx.templComps[name]; ok {
		return formatTemplComp(comp)
	}

	// Substring search
	var matches []*TemplComponentInfo
	for _, comp := range idx.templComps {
		if strings.Contains(strings.ToLower(comp.Name), nameLower) {
			matches = append(matches, comp)
		}
	}

	if len(matches) == 0 {
		var available []string
		for n := range idx.templComps {
			available = append(available, n)
		}
		sort.Strings(available)
		return fmt.Sprintf("No templ component found matching '%s'. Available: %v", name, available)
	}
	if len(matches) == 1 {
		return formatTemplComp(matches[0])
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d components matching '%s':\n\n", len(matches), name)
	for _, comp := range matches {
		fmt.Fprintf(&sb, "  templ %s(%s)  — %s:%d\n", comp.Name, comp.Params, comp.File, comp.Line)
	}
	return sb.String()
}

// ListTemplComponents returns all templ components grouped by file.
func (idx *CodeIndex) ListTemplComponents() string {
	if len(idx.templComps) == 0 {
		return "No templ components found in index"
	}

	byFile := map[string][]*TemplComponentInfo{}
	for _, comp := range idx.templComps {
		byFile[comp.File] = append(byFile[comp.File], comp)
	}

	var files []string
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	var sb strings.Builder
	for _, f := range files {
		comps := byFile[f]
		sort.Slice(comps, func(i, j int) bool { return comps[i].Line < comps[j].Line })
		fmt.Fprintf(&sb, "%s:\n", f)
		for _, comp := range comps {
			fmt.Fprintf(&sb, "  %-30s %s:%d\n", "templ "+comp.Name+"()", comp.File, comp.Line)
		}
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "Total: %d components", len(idx.templComps))
	return sb.String()
}

func formatTemplComp(comp *TemplComponentInfo) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "templ %s(%s)\n", comp.Name, comp.Params)
	fmt.Fprintf(&sb, "  file:  %s:%d\n", comp.File, comp.Line)
	if len(comp.CalledBy) > 0 {
		sort.Strings(comp.CalledBy)
		fmt.Fprintf(&sb, "  called_by: %v\n", comp.CalledBy)
	}
	if len(comp.Calls) > 0 {
		sort.Strings(comp.Calls)
		fmt.Fprintf(&sb, "  calls: %v\n", comp.Calls)
	}
	return sb.String()
}

// ── JS map ────────────────────────────────────────────────────────────────────

func (idx *CodeIndex) linkJSToPages() {
	// Global scripts from base.templ
	globalJS := []string{"htmx.min.js", "site_base.js", "sync_poller.js", "video_cards.js"}
	for _, js := range globalJS {
		if info, ok := idx.jsFiles[js]; ok {
			info.Pages = append(info.Pages, "(global — all pages)")
		}
	}

	// Helper to resolve endpoint path for a handler function
	resolvePagePath := func(handlerFile, handlerFunc string) string {
		for _, ep := range idx.endpoints {
			if ep.HandlerFile == handlerFile && ep.HandlerFunc == handlerFunc {
				return ep.Method + " " + ep.Path
			}
		}
		return handlerFunc
	}

	// Per-page scripts from PageScripts assignments
	for handlerFile, entries := range idx.pageScripts {
		for _, ps := range entries {
			pagePath := resolvePagePath(handlerFile, ps.HandlerFunc)
			for _, script := range ps.Scripts {
				jsName := strings.TrimPrefix(script, "js/")
				if info, ok := idx.jsFiles[jsName]; ok {
					info.Pages = append(info.Pages, pagePath)
				}
			}
		}
	}

	// Per-page ESBundle assignments (js/dist/feed.js → src modules)
	for handlerFile, entries := range idx.esBundles {
		for _, eb := range entries {
			pagePath := resolvePagePath(handlerFile, eb.HandlerFunc)
			bundleKey := strings.TrimPrefix(eb.Bundle, "js/")
			if info, ok := idx.jsFiles[bundleKey]; ok {
				info.Pages = append(info.Pages, pagePath)
			}
		}
	}
}

// GetJSMap returns a structured overview of all JS files.
func (idx *CodeIndex) GetJSMap(filter string) string {
	filter = strings.ToLower(filter)
	var sb strings.Builder
	sb.WriteString("=== JavaScript File Map ===\n\n")

	names := make([]string, 0, len(idx.jsFiles))
	for name := range idx.jsFiles {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		info := idx.jsFiles[name]
		if filter != "" && !strings.Contains(strings.ToLower(name), filter) &&
			!strings.Contains(strings.ToLower(info.Description), filter) {
			continue
		}

		fmt.Fprintf(&sb, "%s\n", info.Path)
		if info.Description != "" {
			fmt.Fprintf(&sb, "  purpose:   %s\n", info.Description)
		}
		if len(info.Pages) > 0 {
			fmt.Fprintf(&sb, "  pages:     %s\n", strings.Join(info.Pages, ", "))
		}
		if len(info.Symbols) > 0 {
			fmt.Fprintf(&sb, "  symbols:   %d\n", len(info.Symbols))
			for _, s := range info.Symbols {
				fmt.Fprintf(&sb, "    %s:%d  [%s]  %s\n", s.File, s.Line, s.Kind, s.Signature)
			}
		}
		if len(info.APICalls) > 0 {
			fmt.Fprintf(&sb, "  api_calls: %d\n", len(info.APICalls))
			for _, c := range info.APICalls {
				fmt.Fprintf(&sb, "    %s %s (line %d)\n", c.Method, c.URL, c.Line)
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ── worker scanning ────────────────────────────────────────────────────────────

func (idx *CodeIndex) scanWorkers() {
	managerSource := idx.readFile("internal/worker/manager.go")
	if managerSource == "" {
		return
	}
	idx.workers = ScanWorkerManager(managerSource)

	// Match worker funcs to their implementation files
	workerDir := filepath.Join(idx.root, "internal", "worker")
	entries, _ := os.ReadDir(workerDir)
	funcToFile := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		relpath := "internal/worker/" + e.Name()
		source := idx.readFile(relpath)
		for _, m := range rGoMethod.FindAllStringSubmatch(source, -1) {
			funcToFile[m[2]] = relpath
		}
		for _, m := range rGoFunc.FindAllStringSubmatch(source, -1) {
			funcToFile[m[1]] = relpath
		}
	}

	for i := range idx.workers {
		if f, ok := funcToFile[idx.workers[i].FuncName]; ok {
			idx.workers[i].File = f
		}
	}
}

// GetWorkerMap returns the formatted worker map.
func (idx *CodeIndex) GetWorkerMap() string {
	if len(idx.workers) == 0 {
		return "No workers found in index"
	}

	// Build file -> tables map
	fileTables := map[string][]string{}
	for _, w := range idx.workers {
		if w.File == "" {
			continue
		}
		seen := map[string]bool{}
		for tbl, refs := range idx.tables {
			for _, r := range refs {
				if r.File == w.File && !seen[tbl] {
					seen[tbl] = true
					fileTables[w.File] = append(fileTables[w.File], tbl)
				}
			}
		}
	}

	return FormatWorkerMap(idx.workers, fileTables)
}

// TracePage traces the full rendering chain for a web page: handler → template → JS files → API calls → server handlers → DB tables.
func (idx *CodeIndex) TracePage(page string) string {
	pageLower := strings.ToLower(page)

	// Find matching page endpoints
	var pageEPs []*Endpoint
	for _, ep := range idx.endpoints {
		if ep.Kind != "page" {
			continue
		}
		if strings.Contains(strings.ToLower(ep.Path), pageLower) ||
			strings.Contains(strings.ToLower(ep.HandlerFunc), pageLower) {
			pageEPs = append(pageEPs, ep)
		}
	}
	if len(pageEPs) == 0 {
		var pages []string
		for _, ep := range idx.endpoints {
			if ep.Kind == "page" {
				pages = append(pages, ep.Method+" "+ep.Path)
			}
		}
		sort.Strings(pages)
		return fmt.Sprintf("No page found matching '%s'. Available pages:\n  %s", page, strings.Join(pages, "\n  "))
	}

	var sb strings.Builder
	for _, ep := range pageEPs {
		fmt.Fprintf(&sb, "=== %s %s ===\n\n", ep.Method, ep.Path)

		// Layer 1: Handler
		fmt.Fprintf(&sb, "Handler:\n  %s:%d → %s()\n\n", ep.HandlerFile, ep.HandlerLine, ep.HandlerFunc)

		// Layer 2: Templ components used by handler
		if node, ok := idx.files[ep.HandlerFile]; ok {
			var comps []string
			for _, imp := range node.Imports {
				if comp, ok := idx.templComps[imp]; ok {
					comps = append(comps, fmt.Sprintf("%s (%s:%d)", comp.Name, comp.File, comp.Line))
				} else {
					comps = append(comps, imp)
				}
			}
			if len(comps) > 0 {
				sb.WriteString("Templ components:\n")
				for _, c := range comps {
					fmt.Fprintf(&sb, "  %s\n", c)
				}
				sb.WriteString("\n")
			}
		}

		// Layer 3: JavaScript — ESBundle + PageScripts + globals
		var jsEntries []string
		jsEntries = append(jsEntries, "static/js/site_base.js (global)", "static/js/video_cards.js (global)")

		// ESBundle for this handler
		if entries, ok := idx.esBundles[ep.HandlerFile]; ok {
			for _, eb := range entries {
				if eb.HandlerFunc == ep.HandlerFunc {
					jsEntries = append(jsEntries, "static/"+eb.Bundle+" (ES bundle)")
				}
			}
		}

		// PageScripts for this handler
		if entries, ok := idx.pageScripts[ep.HandlerFile]; ok {
			for _, ps := range entries {
				if ps.HandlerFunc == ep.HandlerFunc {
					for _, script := range ps.Scripts {
						jsEntries = append(jsEntries, "static/"+script)
					}
				}
			}
		}
		sb.WriteString("JavaScript:\n")
		for _, js := range jsEntries {
			fmt.Fprintf(&sb, "  %s\n", js)
		}
		sb.WriteString("\n")

		// Layer 4: API calls from page JS (bundle src modules + page scripts)
		var apiURLs []string
		seen := map[string]bool{}
		collectAPICalls := func(jsKey string) {
			if info, ok := idx.jsFiles[jsKey]; ok {
				for _, call := range info.APICalls {
					key := call.Method + " " + call.URL
					if !seen[key] {
						seen[key] = true
						apiURLs = append(apiURLs, key)
					}
				}
			}
		}

		// Collect from ESBundle's source modules
		if entries, ok := idx.esBundles[ep.HandlerFile]; ok {
			for _, eb := range entries {
				if eb.HandlerFunc == ep.HandlerFunc {
					bundleKey := strings.TrimPrefix(eb.Bundle, "js/")
					// Map dist/feed.js → src/feed/ prefix
					if strings.HasPrefix(bundleKey, "dist/") {
						srcPrefix := "src/" + strings.TrimPrefix(strings.TrimSuffix(bundleKey, ".js"), "dist/") + "/"
						for jsKey := range idx.jsFiles {
							if strings.HasPrefix(jsKey, srcPrefix) {
								collectAPICalls(jsKey)
							}
						}
					}
					collectAPICalls(bundleKey)
				}
			}
		}

		// Collect from PageScripts
		if entries, ok := idx.pageScripts[ep.HandlerFile]; ok {
			for _, ps := range entries {
				if ps.HandlerFunc == ep.HandlerFunc {
					for _, script := range ps.Scripts {
						collectAPICalls(strings.TrimPrefix(script, "js/"))
					}
				}
			}
		}

		if len(apiURLs) > 0 {
			sb.WriteString("API calls from JS:\n")
			for _, url := range apiURLs {
				fmt.Fprintf(&sb, "  %s\n", url)
			}
			sb.WriteString("\n")
		}

		// Layer 5: Server handlers for those API calls
		var handlerFiles []string
		handlerSeen := map[string]bool{}
		for _, url := range apiURLs {
			parts := strings.SplitN(url, " ", 2)
			if len(parts) != 2 {
				continue
			}
			for _, apiEP := range idx.endpoints {
				if strings.Contains(apiEP.Path, parts[1]) {
					key := apiEP.HandlerFile + ":" + apiEP.HandlerFunc
					if !handlerSeen[key] {
						handlerSeen[key] = true
						handlerFiles = append(handlerFiles, fmt.Sprintf("  %s → %s:%d %s()", apiEP.Method+" "+apiEP.Path, apiEP.HandlerFile, apiEP.HandlerLine, apiEP.HandlerFunc))
					}
				}
			}
		}
		if len(handlerFiles) > 0 {
			sb.WriteString("Server handlers for those APIs:\n")
			for _, h := range handlerFiles {
				fmt.Fprintf(&sb, "%s\n", h)
			}
			sb.WriteString("\n")
		}

		// Layer 6: DB tables touched by the page handler
		if node, ok := idx.files[ep.HandlerFile]; ok && len(node.DBTables) > 0 {
			reads := dedupTableNames(node.DBTables, "read", "read_write")
			writes := dedupTableNames(node.DBTables, "write", "read_write")
			if len(reads) > 0 || len(writes) > 0 {
				sb.WriteString("DB tables:\n")
				if len(reads) > 0 {
					fmt.Fprintf(&sb, "  reads:  %v\n", reads)
				}
				if len(writes) > 0 {
					fmt.Fprintf(&sb, "  writes: %v\n", writes)
				}
				sb.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ── Android architecture graph ────────────────────────────────────────────────

func (idx *CodeIndex) computeAndroidGraph() {
	type fileScan struct {
		relpath string
		result  KotlinScanResult
	}
	var scans []fileScan

	idx.androidScreens = nil
	idx.androidVMs = nil
	idx.androidRepos = nil
	idx.androidDAOs = nil
	idx.androidEntities = nil
	idx.androidNavOrder = nil
	idx.androidClasses = map[string]*AndroidClassInfo{}

	ktBase := filepath.Join(idx.root, "android", "app", "src", "main", "java")
	filepath.WalkDir(ktBase, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".kt") {
			return nil
		}
		relpath := idx.relpath(path)
		if strings.Contains(relpath, ".gradle-home") || strings.Contains(relpath, "/build/") {
			return nil
		}
		source := idx.readFile(relpath)
		result := ScanKotlin(source, relpath)
		scans = append(scans, fileScan{relpath, result})
		return nil
	})

	daoMethodToType := scanIglooDatabaseDAOMap(idx.readFile("android/app/src/main/java/com/screwy/igloo/data/IglooDatabase.kt"))
	navTargets, navOrder := scanNavTargets(idx.readFile("android/app/src/main/java/com/screwy/igloo/ui/nav/AppNavHost.kt"))
	if len(navOrder) > 0 {
		idx.androidNavOrder = navOrder
	}

	daoByName := map[string]AndroidDAOInfo{}

	// DAOs (current rewrite: data/dao/*.kt)
	for _, s := range scans {
		if !strings.Contains(s.relpath, "/data/dao/") {
			continue
		}
		var tables []string
		seen := map[string]bool{}
		readCount, writeCount := 0, 0
		for _, d := range s.result.DAOTables {
			if d.Table != "__upsert__" && !seen[d.Table] {
				seen[d.Table] = true
				tables = append(tables, d.Table)
			}
			if d.Mode == "read" {
				readCount++
			} else {
				writeCount++
			}
		}
		info := AndroidDAOInfo{
			Name: s.result.ClassName, File: s.relpath,
			Tables: tables, ReadCount: readCount, WriteCount: writeCount,
		}
		idx.androidDAOs = append(idx.androidDAOs, info)
		daoByName[info.Name] = info
	}

	// Entities (current rewrite: data/entity/*.kt)
	for _, s := range scans {
		if !strings.Contains(s.relpath, "/data/entity/") {
			continue
		}
		for _, e := range s.result.Entities {
			idx.androidEntities = append(idx.androidEntities, AndroidEntityInfo{
				ClassName: e.ClassName, TableName: e.TableName, File: s.relpath,
			})
		}
	}

	// Generic class index so trace_screen can follow the rewrite's feature-sliced
	// services/data deps instead of assuming v1 repository folders.
	for _, s := range scans {
		if s.result.ClassName == "" {
			continue
		}
		source := idx.readFile(s.relpath)
		directDAOs := scanDirectDAOUsage(source, daoMethodToType)
		info := &AndroidClassInfo{
			Name:         s.result.ClassName,
			File:         s.relpath,
			Kind:         androidKindForPath(s.relpath, s.result.ClassName),
			Dependencies: dedupStrings(constructorTypes(s.result.ConstructorDeps)),
			DirectDAOs:   directDAOs,
			APICalls:     s.result.APICalls,
		}
		idx.androidClasses[info.Name] = info
	}

	// Screens (rewrite: feature Route/Host files, not ui/screen/* classes)
	vmByScreen := map[string]string{}
	for _, s := range scans {
		if !isAndroidScreenFile(s.relpath) {
			continue
		}
		source := idx.readFile(s.relpath)
		lineCount := strings.Count(source, "\n")
		screenName := routeFunctionName(s.relpath, s.result)
		if screenName == "" {
			continue
		}
		navRoute := navTargets[screenName]
		var vm string
		if len(s.result.ViewModelRefs) > 0 {
			vm = s.result.ViewModelRefs[0]
		}
		idx.androidScreens = append(idx.androidScreens, AndroidScreenInfo{
			Name: screenName, File: s.relpath,
			NavRoute: navRoute, ViewModel: vm, LineCount: lineCount,
		})
		if vm != "" {
			vmByScreen[vm] = screenName
		}
	}

	// ViewModels (rewrite: feature-sliced */*ViewModel.kt)
	for _, s := range scans {
		if !strings.HasSuffix(s.relpath, "ViewModel.kt") {
			continue
		}
		deps := dedupStrings(constructorTypes(s.result.ConstructorDeps))
		directDAOs := scanDirectDAOUsage(idx.readFile(s.relpath), daoMethodToType)
		var screens []string
		if screenName, ok := vmByScreen[s.result.ClassName]; ok {
			screens = append(screens, screenName)
		}
		idx.androidVMs = append(idx.androidVMs, AndroidVMInfo{
			Name: s.result.ClassName, File: s.relpath,
			Dependencies: deps, DirectDAOs: directDAOs, Screens: screens,
		})
	}

	// Data/service classes. Keep the existing "repositories" slice populated with
	// whatever service-style classes the rewrite actually has, so older callers
	// still get useful output.
	for _, s := range scans {
		info := idx.androidClasses[s.result.ClassName]
		if info == nil {
			continue
		}
		switch info.Kind {
		case "viewmodel", "dao", "entity", "screen", "net", "sync", "navigation":
			continue
		}
		idx.androidRepos = append(idx.androidRepos, AndroidRepoInfo{
			Name: info.Name, File: info.File,
			DAOs: info.DirectDAOs, APICalls: info.APICalls,
		})
	}

	// Backfill direct DAO usage from the generic class map into the DAO slice for trace_screen.
	for i := range idx.androidVMs {
		idx.androidVMs[i].DirectDAOs = filterExistingDAOs(idx.androidVMs[i].DirectDAOs, daoByName)
	}
	for i := range idx.androidRepos {
		idx.androidRepos[i].DAOs = filterExistingDAOs(idx.androidRepos[i].DAOs, daoByName)
	}
}

// GetAndroidMap returns the full Android architecture overview.
func (idx *CodeIndex) GetAndroidMap(layer string) string {
	layer = strings.ToLower(layer)
	var sb strings.Builder
	sb.WriteString("=== Android Architecture Map ===\n\n")

	if layer == "" || layer == "screens" {
		sb.WriteString("Screens:\n")
		sort.Slice(idx.androidScreens, func(i, j int) bool {
			return idx.androidScreens[i].Name < idx.androidScreens[j].Name
		})
		for _, s := range idx.androidScreens {
			fmt.Fprintf(&sb, "  %-25s %s  (%d lines)", s.Name, s.File, s.LineCount)
			if s.ViewModel != "" {
				fmt.Fprintf(&sb, "  → %s", s.ViewModel)
			}
			if s.NavRoute != "" {
				fmt.Fprintf(&sb, "  [route: %s]", s.NavRoute)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if layer == "" || layer == "viewmodels" {
		sb.WriteString("ViewModels:\n")
		sort.Slice(idx.androidVMs, func(i, j int) bool {
			return idx.androidVMs[i].Name < idx.androidVMs[j].Name
		})
		for _, vm := range idx.androidVMs {
			fmt.Fprintf(&sb, "  %-25s %s\n", vm.Name, vm.File)
			if len(vm.Dependencies) > 0 {
				fmt.Fprintf(&sb, "    deps:    %v\n", vm.Dependencies)
			}
			if len(vm.DirectDAOs) > 0 {
				fmt.Fprintf(&sb, "    daos:    %v\n", vm.DirectDAOs)
			}
			if len(vm.Screens) > 0 {
				fmt.Fprintf(&sb, "    screens: %v\n", vm.Screens)
			}
		}
		sb.WriteString("\n")
	}

	if layer == "" || layer == "repositories" || layer == "data" {
		sb.WriteString("Data + Service Classes:\n")
		sort.Slice(idx.androidRepos, func(i, j int) bool {
			return idx.androidRepos[i].Name < idx.androidRepos[j].Name
		})
		for _, r := range idx.androidRepos {
			fmt.Fprintf(&sb, "  %-25s %s\n", r.Name, r.File)
			if info := idx.androidClasses[r.Name]; info != nil && len(info.Dependencies) > 0 {
				fmt.Fprintf(&sb, "    deps:      %v\n", info.Dependencies)
			}
			if len(r.DAOs) > 0 {
				fmt.Fprintf(&sb, "    daos:      %v\n", r.DAOs)
			}
			if len(r.APICalls) > 0 {
				fmt.Fprintf(&sb, "    api_calls: %d\n", len(r.APICalls))
				for _, c := range r.APICalls {
					fmt.Fprintf(&sb, "      %s %s\n", c.Method, c.Path)
				}
			}
		}
		sb.WriteString("\n")
	}

	if layer == "" || layer == "net" {
		sb.WriteString("Network:\n")
		for _, cls := range idx.sortedAndroidClassesByKind("net") {
			fmt.Fprintf(&sb, "  %-25s %s\n", cls.Name, cls.File)
			if len(cls.APICalls) > 0 {
				for _, c := range cls.APICalls {
					fmt.Fprintf(&sb, "    %s %s\n", c.Method, c.Path)
				}
			}
		}
		sb.WriteString("\n")
	}

	if layer == "" || layer == "sync" {
		sb.WriteString("Sync + Pipeline:\n")
		for _, cls := range idx.sortedAndroidClassesByKind("sync", "media", "outbox") {
			fmt.Fprintf(&sb, "  %-25s %s\n", cls.Name, cls.File)
			if len(cls.Dependencies) > 0 {
				fmt.Fprintf(&sb, "    deps: %v\n", cls.Dependencies)
			}
			if len(cls.DirectDAOs) > 0 {
				fmt.Fprintf(&sb, "    daos: %v\n", cls.DirectDAOs)
			}
			if len(cls.APICalls) > 0 {
				fmt.Fprintf(&sb, "    api_calls: %d\n", len(cls.APICalls))
			}
		}
		sb.WriteString("\n")
	}

	if layer == "" || layer == "daos" {
		sb.WriteString("DAOs:\n")
		sort.Slice(idx.androidDAOs, func(i, j int) bool {
			return idx.androidDAOs[i].Name < idx.androidDAOs[j].Name
		})
		for _, d := range idx.androidDAOs {
			fmt.Fprintf(&sb, "  %-25s %s  tables=%v  reads=%d writes=%d\n",
				d.Name, d.File, d.Tables, d.ReadCount, d.WriteCount)
		}
		sb.WriteString("\n")
	}

	if layer == "" || layer == "entities" {
		sb.WriteString("Entities:\n")
		sort.Slice(idx.androidEntities, func(i, j int) bool {
			return idx.androidEntities[i].ClassName < idx.androidEntities[j].ClassName
		})
		for _, e := range idx.androidEntities {
			fmt.Fprintf(&sb, "  %-25s → %-20s %s\n", e.ClassName, e.TableName, e.File)
		}
		sb.WriteString("\n")
	}

	if layer == "" || layer == "navigation" {
		if len(idx.androidNavOrder) > 0 {
			sb.WriteString("Navigation (route order from AppNavHost):\n")
			for i, route := range idx.androidNavOrder {
				fmt.Fprintf(&sb, "  %d. %s\n", i+1, route)
			}
			sb.WriteString("\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// TraceScreen traces an Android screen's full data stack: UI → ViewModel → direct Room/service deps → API → server.
func (idx *CodeIndex) TraceScreen(name string) string {
	nameLower := strings.ToLower(name)

	var matches []AndroidScreenInfo
	for _, s := range idx.androidScreens {
		if strings.Contains(strings.ToLower(s.Name), nameLower) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 0 {
		var available []string
		for _, s := range idx.androidScreens {
			available = append(available, s.Name)
		}
		sort.Strings(available)
		return fmt.Sprintf("No screen found matching '%s'. Available: %v", name, available)
	}

	var sb strings.Builder
	for _, screen := range matches {
		fmt.Fprintf(&sb, "=== %s ===\n\n", screen.Name)
		fmt.Fprintf(&sb, "%s (%s, %d lines)\n", screen.Name, screen.File, screen.LineCount)
		if screen.NavRoute != "" {
			fmt.Fprintf(&sb, "  route: %s\n", screen.NavRoute)
		}

		if screen.ViewModel == "" {
			sb.WriteString("  (no ViewModel)\n\n")
			continue
		}

		var vm *AndroidVMInfo
		for i := range idx.androidVMs {
			if idx.androidVMs[i].Name == screen.ViewModel {
				vm = &idx.androidVMs[i]
				break
			}
		}
		if vm == nil {
			fmt.Fprintf(&sb, "  |\n  v uses\n%s (not found in viewmodel index)\n\n", screen.ViewModel)
			continue
		}

		fmt.Fprintf(&sb, "  |\n  v uses\n%s (%s)\n", vm.Name, vm.File)
		if len(vm.Dependencies) > 0 {
			fmt.Fprintf(&sb, "  deps: %v\n", vm.Dependencies)
		}
		traceAndroidDAOs(&sb, idx, vm.DirectDAOs)

		seenClasses := map[string]bool{vm.Name: true}
		for _, depName := range vm.Dependencies {
			idx.traceAndroidClass(&sb, depName, "  ", seenClasses)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (idx *CodeIndex) sortedAndroidClassesByKind(kinds ...string) []*AndroidClassInfo {
	kindSet := map[string]bool{}
	for _, kind := range kinds {
		kindSet[kind] = true
	}
	var out []*AndroidClassInfo
	for _, cls := range idx.androidClasses {
		if kindSet[cls.Kind] {
			out = append(out, cls)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (idx *CodeIndex) traceAndroidClass(sb *strings.Builder, className, indent string, seen map[string]bool) {
	if seen[className] {
		return
	}
	seen[className] = true

	info := idx.androidClasses[className]
	if info == nil {
		fmt.Fprintf(sb, "%s|\n%s v uses\n%s (not found in android class index)\n", indent, indent, className)
		return
	}

	fmt.Fprintf(sb, "%s|\n%s v uses\n%s (%s)\n", indent, indent, info.Name, info.File)
	if len(info.Dependencies) > 0 {
		fmt.Fprintf(sb, "%s deps: %v\n", indent, info.Dependencies)
	}
	traceAndroidDAOs(sb, idx, info.DirectDAOs)
	traceAndroidAPICalls(sb, idx, info.APICalls, indent)

	nextIndent := indent + "  "
	for _, dep := range info.Dependencies {
		if dep == info.Name {
			continue
		}
		if child := idx.androidClasses[dep]; child != nil && (len(child.APICalls) > 0 || len(child.DirectDAOs) > 0) {
			idx.traceAndroidClass(sb, dep, nextIndent, seen)
		}
	}
}

func traceAndroidDAOs(sb *strings.Builder, idx *CodeIndex, daoNames []string) {
	for _, daoName := range daoNames {
		for i := range idx.androidDAOs {
			dao := idx.androidDAOs[i]
			if dao.Name == daoName {
				fmt.Fprintf(sb, "  |\n  v uses\n%s (%s)  tables=%v  reads=%d writes=%d\n",
					dao.Name, dao.File, dao.Tables, dao.ReadCount, dao.WriteCount)
				break
			}
		}
	}
}

func traceAndroidAPICalls(sb *strings.Builder, idx *CodeIndex, calls []KotlinAPICall, indent string) {
	if len(calls) == 0 {
		return
	}
	fmt.Fprintf(sb, "%s|\n%s v calls\n%sServer API:\n", indent, indent, indent)
	for _, call := range calls {
		fmt.Fprintf(sb, "%s %s %s\n", indent, call.Method, call.Path)
		for _, ep := range idx.endpoints {
			epPath := strings.TrimRight(rGoPathParam.ReplaceAllString(ep.Path, ""), "/")
			callPath := strings.TrimRight(call.Path, "/")
			if strings.HasPrefix(callPath, epPath) || strings.HasPrefix(epPath, callPath) {
				if ep.Method == call.Method || call.Method == "" {
					fmt.Fprintf(sb, "%s   → %s:%d %s()\n", indent, ep.HandlerFile, ep.HandlerLine, ep.HandlerFunc)
					break
				}
			}
		}
	}
}

func constructorTypes(deps []ConstructorDep) []string {
	out := make([]string, 0, len(deps))
	for _, dep := range deps {
		out = append(out, dep.Type)
	}
	return out
}

func dedupStrings(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func isAndroidScreenFile(relpath string) bool {
	if !strings.HasPrefix(relpath, "android/app/src/main/java/com/screwy/igloo/") {
		return false
	}
	if strings.Contains(relpath, "/ui/component/") || strings.Contains(relpath, "/ui/nav/") {
		return false
	}
	return strings.HasSuffix(relpath, "Route.kt") || strings.HasSuffix(relpath, "Host.kt")
}

func routeFunctionName(relpath string, result KotlinScanResult) string {
	base := strings.TrimSuffix(filepath.Base(relpath), ".kt")
	for _, sym := range result.Symbols {
		if sym.Kind != "function" {
			continue
		}
		if sym.Name == base {
			return sym.Name
		}
	}
	for _, sym := range result.Symbols {
		if sym.Kind == "function" && (strings.HasSuffix(sym.Name, "Route") || strings.HasSuffix(sym.Name, "Host")) {
			return sym.Name
		}
	}
	return ""
}

func androidKindForPath(relpath, className string) string {
	switch {
	case strings.HasSuffix(relpath, "ViewModel.kt") || strings.HasSuffix(className, "ViewModel"):
		return "viewmodel"
	case strings.Contains(relpath, "/data/dao/"):
		return "dao"
	case strings.Contains(relpath, "/data/entity/"):
		return "entity"
	case strings.Contains(relpath, "/net/"):
		return "net"
	case strings.Contains(relpath, "/sync/"):
		return "sync"
	case strings.Contains(relpath, "/media/"):
		return "media"
	case strings.Contains(relpath, "/outbox/"):
		return "outbox"
	case strings.Contains(relpath, "/auth/") && strings.HasSuffix(className, "Repo"):
		return "repo"
	case strings.Contains(relpath, "/data/"):
		return "data"
	default:
		return "service"
	}
}

func scanIglooDatabaseDAOMap(source string) map[string]string {
	out := map[string]string{}
	for _, m := range rDbAbstractDao.FindAllStringSubmatch(source, -1) {
		out[m[1]] = m[2]
	}
	return out
}

func scanDirectDAOUsage(source string, daoMethodToType map[string]string) []string {
	var out []string
	for _, m := range rDbDaoCall.FindAllStringSubmatch(source, -1) {
		if daoType := daoMethodToType[m[1]]; daoType != "" {
			out = append(out, daoType)
		}
	}
	return dedupStrings(out)
}

func filterExistingDAOs(names []string, daoByName map[string]AndroidDAOInfo) []string {
	var out []string
	for _, name := range dedupStrings(names) {
		if _, ok := daoByName[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

func scanNavTargets(source string) (map[string]string, []string) {
	targets := map[string]string{}
	var order []string
	if source == "" {
		return targets, order
	}

	lines := strings.Split(source, "\n")
	var (
		currentRoute string
		currentBlock []string
		depth        int
		capturing    bool
	)
	routePattern := regexp.MustCompile(`composable\s*\(\s*(?:route\s*=\s*)?"([^"]+)"`)
	targetPattern := regexp.MustCompile(`\b([A-Z]\w*(?:Route|Host))\s*\(`)

	flush := func() {
		if currentRoute == "" {
			return
		}
		order = append(order, currentRoute)
		block := strings.Join(currentBlock, "\n")
		if m := targetPattern.FindStringSubmatch(block); m != nil {
			targets[m[1]] = currentRoute
		}
		currentRoute = ""
		currentBlock = nil
		depth = 0
		capturing = false
	}

	for _, line := range lines {
		if !capturing {
			if m := routePattern.FindStringSubmatch(line); m != nil {
				currentRoute = m[1]
				currentBlock = []string{line}
				depth = strings.Count(line, "{") - strings.Count(line, "}")
				capturing = true
				if depth <= 0 {
					flush()
				}
			}
			continue
		}

		currentBlock = append(currentBlock, line)
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth <= 0 {
			flush()
		}
	}

	return targets, dedupStrings(order)
}

// DebugTrackerSchema returns a formatted reference of all StatsLogger.log events and their fields.
func (idx *CodeIndex) DebugTrackerSchema(filter string) string {
	if len(idx.debugEvents) == 0 {
		return "No debug events found. Run refresh_index if files changed."
	}

	type eventInfo struct {
		fields []string
		sites  []string
	}
	events := map[string]*eventInfo{}
	var order []string

	for _, ev := range idx.debugEvents {
		info, exists := events[ev.Name]
		if !exists {
			info = &eventInfo{}
			events[ev.Name] = info
			order = append(order, ev.Name)
		}
		site := fmt.Sprintf("%s:%d", ev.File, ev.Line)
		info.sites = append(info.sites, site)

		seen := map[string]bool{}
		for _, f := range info.fields {
			seen[f] = true
		}
		for _, f := range ev.Fields {
			if !seen[f.Key] {
				seen[f.Key] = true
				info.fields = append(info.fields, f.Key)
			}
		}
	}

	sort.Strings(order)

	var sb strings.Builder
	sb.WriteString("=== StatsLogger Event Schema ===\n\n")

	matched := 0
	for _, name := range order {
		if filter != "" && !strings.Contains(name, filter) {
			continue
		}
		matched++
		info := events[name]
		fmt.Fprintf(&sb, "%s:\n", name)
		if len(info.fields) > 0 {
			fmt.Fprintf(&sb, "  fields: %s\n", strings.Join(info.fields, ", "))
		} else {
			sb.WriteString("  fields: (none)\n")
		}
		for _, s := range info.sites {
			fmt.Fprintf(&sb, "  source: %s\n", s)
		}
		sb.WriteString("\n")
	}

	if matched == 0 {
		return fmt.Sprintf("No events matching '%s'. %d total events available.", filter, len(order))
	}

	fmt.Fprintf(&sb, "Total: %d events", matched)
	return sb.String()
}

// TraceSetting traces a setting key across Go and Kotlin source: where it's stored, read, and used.
func (idx *CodeIndex) TraceSetting(key string) string {
	if key == "" {
		return "Setting key required."
	}

	type hit struct {
		file    string
		line    int
		context string
		layer   string
	}
	var hits []hit

	grepFile := func(relpath, layer string) {
		source := idx.readFile(relpath)
		if source == "" {
			return
		}
		lines := strings.Split(source, "\n")
		for i, line := range lines {
			if strings.Contains(line, key) {
				ctx := strings.TrimSpace(line)
				if len(ctx) > 120 {
					ctx = ctx[:117] + "..."
				}
				hits = append(hits, hit{
					file: relpath, line: i + 1, context: ctx, layer: layer,
				})
			}
		}
	}

	// Scan Go files
	for _, dir := range []string{
		"internal/db", "internal/web", "internal/worker", "internal/feed",
		"internal/config", "internal/settings", "internal/subscribe",
	} {
		entries, _ := os.ReadDir(filepath.Join(idx.root, dir))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				grepFile(filepath.Join(dir, e.Name()), "server")
			}
		}
	}

	// Scan Kotlin files
	ktBase := filepath.Join(idx.root, "android", "app", "src", "main", "java")
	filepath.WalkDir(ktBase, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".kt") {
			return nil
		}
		grepFile(idx.relpath(path), "android")
		return nil
	})

	if len(hits) == 0 {
		return fmt.Sprintf("No references to '%s' found in server or Android code.", key)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Setting: %s ===\n\n", key)

	// Check settings table
	for _, ref := range idx.tables["settings"] {
		fmt.Fprintf(&sb, "DB table 'settings': %s:%d (%s)\n", ref.File, ref.Line, ref.Mode)
	}
	if len(idx.tables["settings"]) > 0 {
		sb.WriteString("\n")
	}

	// Group hits by layer
	var serverHits, androidHits []hit
	for _, h := range hits {
		if h.layer == "server" {
			serverHits = append(serverHits, h)
		} else {
			androidHits = append(androidHits, h)
		}
	}

	if len(serverHits) > 0 {
		sb.WriteString("Go (server):\n")
		for _, h := range serverHits {
			fmt.Fprintf(&sb, "  %s:%d  %s\n", h.file, h.line, h.context)
		}
		sb.WriteString("\n")
	}

	if len(androidHits) > 0 {
		sb.WriteString("Kotlin (Android):\n")
		for _, h := range androidHits {
			fmt.Fprintf(&sb, "  %s:%d  %s\n", h.file, h.line, h.context)
		}
		sb.WriteString("\n")
	}

	fmt.Fprintf(&sb, "Total: %d references", len(hits))
	return sb.String()
}
