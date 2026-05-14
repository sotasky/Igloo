package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/screwys/igloo/cmd/igloo-mcp/index"
)

var idx *index.CodeIndex

func getIndex() *index.CodeIndex {
	if idx == nil {
		root := os.Getenv("IGLOO_PROJECT_ROOT")
		if root == "" {
			var err error
			root, err = os.Getwd()
			if err != nil {
				root = "."
			}
		}
		idx = index.New(root)
		stats := idx.Build()
		log.Printf("index built: %s", stats)
	}
	return idx
}

func textResult(s string) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(s), nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		result, err := doctorStatus()
		if err != nil {
			fmt.Fprintln(os.Stderr, "doctor:", err)
			os.Exit(1)
		}
		fmt.Println(result)
		return
	}

	s := server.NewMCPServer("igloo", "2.0.0")

	s.AddTool(mcp.NewTool("trace_endpoint",
		mcp.WithDescription("Trace the full dependency chain for an API endpoint: handler, templates, DB tables, JS/Android callers."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Endpoint path, e.g. /api/feed/x")),
		mcp.WithString("method", mcp.Description("HTTP method (GET, POST, DELETE). Empty = all methods.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().TraceEndpoint(req.GetString("path", ""), req.GetString("method", "")))
	})

	s.AddTool(mcp.NewTool("trace_file",
		mcp.WithDescription("Show what depends on a file and what it depends on."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Relative path from project root, e.g. internal/web/feed_api.go")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().TraceFile(req.GetString("path", "")))
	})

	s.AddTool(mcp.NewTool("trace_db_table",
		mcp.WithDescription("Show all code locations that read or write a database table."),
		mcp.WithString("table", mcp.Required(), mcp.Description("Table name, e.g. feed_items")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().TraceDBTable(req.GetString("table", "")))
	})

	s.AddTool(mcp.NewTool("get_context",
		mcp.WithDescription("Get all relevant files for a domain area, grouped by layer."),
		mcp.WithString("area", mcp.Required(), mcp.Description("Domain area: feed, media, shorts, bookmarks, videos, channels, admin, auth, search, logs, sync, downloads, translate")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().GetContext(req.GetString("area", "")))
	})

	s.AddTool(mcp.NewTool("list_endpoints",
		mcp.WithDescription("List all API and page endpoints, optionally filtered by path, method, handler file, or area."),
		mcp.WithString("filter", mcp.Description("Optional filter string.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().ListEndpoints(req.GetString("filter", "")))
	})

	s.AddTool(mcp.NewTool("refresh_index",
		mcp.WithDescription("Re-scan the codebase and rebuild the index. Call after files have changed."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		idx = nil
		getIndex()
		return textResult("Index rebuilt")
	})

	s.AddTool(mcp.NewTool("find_symbol",
		mcp.WithDescription("Search for function, class, method, or property definitions across the codebase."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Symbol name or substring, e.g. GetFeedItems")),
		mcp.WithString("kind", mcp.Description("Filter: function, method, class, property, constant")),
		mcp.WithString("language", mcp.Description("Filter: go, kotlin, js, css")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().FindSymbol(req.GetString("name", ""), req.GetString("kind", ""), req.GetString("language", "")))
	})

	s.AddTool(mcp.NewTool("find_references",
		mcp.WithDescription("Find all call sites and usages of a symbol in Go code (receiver.Method patterns)."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Symbol name to find references for")),
		mcp.WithString("file", mcp.Description("Optional: restrict to this file path")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().FindReferences(req.GetString("name", ""), req.GetString("file", "")))
	})

	s.AddTool(mcp.NewTool("trace_data_flow",
		mcp.WithDescription("Trace how data flows across layers: DB table → Go handlers → API → JS/Android → Room."),
		mcp.WithString("entity", mcp.Required(), mcp.Description("DB table name, e.g. feed_items")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().TraceDataFlow(req.GetString("entity", ""), "", ""))
	})

	s.AddTool(mcp.NewTool("get_site_theme",
		mcp.WithDescription("Get all CSS custom properties (theme variables) grouped by section. Use before building UI."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().GetSiteTheme())
	})

	s.AddTool(mcp.NewTool("get_css_component",
		mcp.WithDescription("Get all CSS classes for a UI component, e.g. 'modal', 'btn', 'custom-select'."),
		mcp.WithString("component", mcp.Required(), mcp.Description("Component base name, e.g. modal, btn, prefs")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().GetCSSComponent(req.GetString("component", "")))
	})

	s.AddTool(mcp.NewTool("room_query",
		mcp.WithDescription("Execute a read-only SQL query against the Android Room database via the igloo server. Takes ~5-10s."),
		mcp.WithString("sql", mcp.Required(), mcp.Description("SQL query (SELECT or PRAGMA only). Max 200 rows.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := roomQuery(req.GetString("sql", ""))
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	// ── Server database tools ────────────────────────────────────────────

	s.AddTool(mcp.NewTool("server_query",
		mcp.WithDescription("Execute a read-only SQL query against the server SQLite database. Instant results. Use this instead of spawning sqlite3 CLI. Max 200 rows."),
		mcp.WithString("sql", mcp.Required(), mcp.Description("SQL query (SELECT, PRAGMA, EXPLAIN, or WITH only).")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := serverQuery(req.GetString("sql", ""))
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("list_db_tables",
		mcp.WithDescription("List all server database tables with row counts. Quick orientation for DB work."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := listDBTables()
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("db_schema",
		mcp.WithDescription("Show the schema for a specific table (columns, types, indexes, sample data) or all tables. Use before writing SQL."),
		mcp.WithString("table", mcp.Description("Table name. If empty, returns CREATE TABLE for all tables.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := dbSchema(req.GetString("table", ""))
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("db_summary",
		mcp.WithDescription("Quick server database overview: all tables with row counts, queue statuses, recent activity timestamps."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := dbSummary()
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	// ── Log tools ────────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("list_logs",
		mcp.WithDescription("List available log files with sizes."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := listLogFiles()
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("read_log",
		mcp.WithDescription("Read the last N lines of a server or android log file. Use list_logs first to see available files."),
		mcp.WithString("file", mcp.Required(), mcp.Description("Log file path relative to logs dir, e.g. 'igloo.log' or 'android/android.log'.")),
		mcp.WithNumber("lines", mcp.Description("Number of lines to read from the end. Default 100, max 1000.")),
		mcp.WithString("grep", mcp.Description("Optional: filter output to lines matching this pattern (plus 1 line context).")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		lines := int(req.GetFloat("lines", 100))
		result, err := readLog(req.GetString("file", ""), lines, req.GetString("grep", ""))
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("search_logs",
		mcp.WithDescription("Grep across all log files for a pattern. Returns matching lines with context."),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Regex pattern to search for.")),
		mcp.WithString("file", mcp.Description("Optional: restrict to one log file (relative to logs dir).")),
		mcp.WithNumber("context", mcp.Description("Lines of context around each match. Default 2.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := searchLogs(req.GetString("pattern", ""), req.GetString("file", ""), int(req.GetFloat("context", 2)))
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("recent_errors",
		mcp.WithDescription("Find recent errors across all log sources. Quick debugging entry point."),
		mcp.WithNumber("minutes", mcp.Description("How far back to look. Default 60.")),
		mcp.WithString("source", mcp.Description("Optional filter: 'server', 'android', or 'nginx'.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := recentErrors(int(req.GetFloat("minutes", 60)), req.GetString("source", ""))
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	// ── Operational tools ────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("system_status",
		mcp.WithDescription("Check health of all Igloo services: igloo.service, nginx, port binding, disk space, DB size."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := systemStatus()
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("pipeline_status",
		mcp.WithDescription("Queue health for download, feed media, and channel pipelines: counts, oldest pending, stuck jobs, recent errors."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := pipelineStatus()
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	s.AddTool(mcp.NewTool("doctor_status",
		mcp.WithDescription("Read-only local doctor report: DB/WAL size, dbstat, Android sync age, queue counts, profile/media readiness, downloader failures, masked recent errors."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := doctorStatus()
		if err != nil {
			return mcp.NewToolResultText("Error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(result), nil
	})

	// ── Worker map ───────────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("get_worker_map",
		mcp.WithDescription("Show all background workers/goroutines: their purpose, implementation file, and DB tables they touch."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().GetWorkerMap())
	})

	// ── Templ components ─────────────────────────────────────────────────

	s.AddTool(mcp.NewTool("get_templ_component",
		mcp.WithDescription("Get info about a templ UI component: params, file, callers. Use for frontend/template work."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Component name or substring, e.g. FeedPage, Base, VideoPlayer.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().GetTemplComponent(req.GetString("name", "")))
	})

	s.AddTool(mcp.NewTool("list_templ_components",
		mcp.WithDescription("List all templ UI components grouped by file."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().ListTemplComponents())
	})

	s.AddTool(mcp.NewTool("get_js_map",
		mcp.WithDescription("Overview of all JS files: purpose, functions, API calls, which pages load them. Use before JS work."),
		mcp.WithString("filter", mcp.Description("Optional: filter by filename or purpose substring.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().GetJSMap(req.GetString("filter", "")))
	})

	s.AddTool(mcp.NewTool("get_config_map",
		mcp.WithDescription("Overview of repo configuration files: MCP, Dependabot, GitHub Actions workflows, Compose services, and Semgrep rules."),
		mcp.WithString("filter", mcp.Description("Optional: filter by path, kind, action, service, image, rule, or command.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().GetConfigMap(req.GetString("filter", "")))
	})

	s.AddTool(mcp.NewTool("trace_page",
		mcp.WithDescription("Trace a web page's full rendering chain: handler → templates → JS files → API calls → server handlers → DB tables."),
		mcp.WithString("page", mcp.Required(), mcp.Description("Page name or path substring, e.g. 'feed', 'player', 'shorts'.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().TracePage(req.GetString("page", "")))
	})

	s.AddTool(mcp.NewTool("get_android_map",
		mcp.WithDescription("Full Android architecture for the rewritten app: feature routes/screens, ViewModels, data/service classes, network, sync/pipeline, DAOs, entities, navigation."),
		mcp.WithString("layer", mcp.Description("Optional: filter to 'screens', 'viewmodels', 'data' (or 'repositories'), 'net', 'sync', 'daos', 'entities', or 'navigation'.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().GetAndroidMap(req.GetString("layer", "")))
	})

	s.AddTool(mcp.NewTool("trace_setting",
		mcp.WithDescription("Trace a setting key across the full stack: DB storage, Go readers/writers, Android readers. Use before changing settings behavior."),
		mcp.WithString("key", mcp.Required(), mcp.Description("Setting key string, e.g. 'shorts_max_videos', 'auto_download'.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().TraceSetting(req.GetString("key", "")))
	})

	s.AddTool(mcp.NewTool("debug_tracker_schema",
		mcp.WithDescription("List all Android StatsLogger/DebugTracker events with their field names and source locations. Use before reading stats.jsonl logs or adding new debug events."),
		mcp.WithString("filter", mcp.Description("Optional: filter events by name substring, e.g. 'feed' or 'download'.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().DebugTrackerSchema(req.GetString("filter", "")))
	})

	s.AddTool(mcp.NewTool("trace_screen",
		mcp.WithDescription("Trace an Android screen's full data stack for the rewritten app: route UI → ViewModel → direct Room/service deps → API → server endpoint."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Screen name or substring, e.g. 'Feed', 'VideoPlayer', 'Channel'.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult(getIndex().TraceScreen(req.GetString("name", "")))
	})

	if err := server.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}

// roomQuery posts a SQL query to the igloo server and polls for the Android result.
func roomQuery(sql string) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	base := "https://127.0.0.1:8443"

	// Post the query
	body, _ := json.Marshal(map[string]string{"query": sql})
	resp, err := client.Post(base+"/api/logs/android/room-query", "application/json",
		strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("failed to post query: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("server returned %d for room-query POST", resp.StatusCode)
	}

	// Poll for result (Android checks every 5s)
	for i := 0; i < 12; i++ {
		time.Sleep(3 * time.Second)
		resp, err := client.Get(base + "/api/logs/android/room-query/result")
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if len(data) == 0 {
			continue
		}
		var envelope map[string]any
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue
		}
		// Server wraps in {"has_result": true, "result": {...}}
		result, _ := envelope["result"].(map[string]any)
		if result == nil {
			continue
		}
		if result["query"] != sql {
			continue
		}
		if errMsg, ok := result["error"].(string); ok && errMsg != "" {
			return "", fmt.Errorf("%s", errMsg)
		}
		cols, _ := result["columns"].([]any)
		rows, _ := result["rows"].([]any)
		rowCount, _ := result["row_count"].(float64)
		if len(cols) == 0 {
			return fmt.Sprintf("Query returned %d rows (no columns)", int(rowCount)), nil
		}
		var colStrs []string
		for _, c := range cols {
			colStrs = append(colStrs, fmt.Sprint(c))
		}
		lines := []string{strings.Join(colStrs, " | ")}
		var sep []string
		for _, c := range colStrs {
			dashes := c
			for len(dashes) < 6 {
				dashes += "-"
			}
			sep = append(sep, dashes)
		}
		lines = append(lines, strings.Join(sep, "-+-"))
		for _, row := range rows {
			if rowArr, ok := row.([]any); ok {
				var vals []string
				for _, v := range rowArr {
					if v == nil {
						vals = append(vals, "NULL")
					} else {
						vals = append(vals, fmt.Sprint(v))
					}
				}
				lines = append(lines, strings.Join(vals, " | "))
			}
		}
		return fmt.Sprintf("%d rows\n%s", int(rowCount), strings.Join(lines, "\n")), nil
	}
	return "", fmt.Errorf("timeout waiting for Android response (is the app open?)")
}
