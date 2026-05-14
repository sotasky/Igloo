package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeIndexBuild(t *testing.T) {
	// Set up a minimal fake project root
	root := t.TempDir()

	// Create internal/web/
	webDir := filepath.Join(root, "internal", "web")
	_ = os.MkdirAll(webDir, 0755)
	_ = os.WriteFile(filepath.Join(webDir, "feed.go"), []byte(`package web

func (s *Server) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/feed/list", s.FeedList)
}

func (s *Server) FeedList(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.db.QueryContext(r.Context(), `+"`"+`SELECT id FROM feed_items`+"`"+`)
	s.render(w, r, "feed.html", nil)
}
`), 0644)

	// Create static/js/
	jsDir := filepath.Join(root, "static", "js")
	_ = os.MkdirAll(jsDir, 0755)
	_ = os.WriteFile(filepath.Join(jsDir, "feed.js"), []byte(`
function load() { apiJson('/api/feed/list'); }
`), 0644)

	// Create templates/
	tmplDir := filepath.Join(root, "templates")
	_ = os.MkdirAll(tmplDir, 0755)
	_ = os.WriteFile(filepath.Join(tmplDir, "feed.html"), []byte(`{{define "content"}}{{template "header" .}}{{end}}`), 0644)

	idx := New(root)
	idx.Build()

	result := idx.ListEndpoints("")
	if !strings.Contains(result, "/api/feed/list") {
		t.Errorf("ListEndpoints missing expected endpoint, got:\n%s", result)
	}

	traceResult := idx.TraceEndpoint("/api/feed/list", "")
	if !strings.Contains(traceResult, "feed_items") {
		t.Errorf("TraceEndpoint missing DB table, got:\n%s", traceResult)
	}
}

func TestCodeIndexBuildAndroidRewriteMap(t *testing.T) {
	root := t.TempDir()

	mustWrite := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite("android/app/src/main/java/com/screwy/igloo/ui/nav/AppNavHost.kt", `package com.screwy.igloo.ui.nav

import androidx.compose.runtime.Composable
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import com.screwy.igloo.feed.FeedRoute

@Composable
fun AppNavHost() {
    NavHost(navController, startDestination = "feed") {
        composable("feed") { FeedRoute(navController) }
        composable(route = "channel/{channel_id}") { FeedRoute(navController) }
    }
}
`)

	mustWrite("android/app/src/main/java/com/screwy/igloo/feed/FeedRoute.kt", `package com.screwy.igloo.feed

import androidx.compose.runtime.Composable
import org.koin.androidx.compose.koinViewModel

@Composable
fun FeedRoute(navController: Any) {
    val vm: FeedViewModel = koinViewModel()
}
`)

	mustWrite("android/app/src/main/java/com/screwy/igloo/feed/FeedViewModel.kt", `package com.screwy.igloo.feed

import androidx.lifecycle.ViewModel
import com.screwy.igloo.data.IglooDatabase

class FeedViewModel(
    private val db: IglooDatabase,
) : ViewModel() {
    fun load() {
        db.feedReadDao()
    }
}
`)

	mustWrite("android/app/src/main/java/com/screwy/igloo/data/IglooDatabase.kt", `package com.screwy.igloo.data

import androidx.room.RoomDatabase
import com.screwy.igloo.data.dao.FeedReadDao

abstract class IglooDatabase : RoomDatabase() {
    abstract fun feedReadDao(): FeedReadDao
}
`)

	mustWrite("android/app/src/main/java/com/screwy/igloo/data/dao/FeedReadDao.kt", `package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query

@Dao
interface FeedReadDao {
    @Query("SELECT * FROM feed_items")
    suspend fun feedFlow(): List<String>
}
`)

	mustWrite("android/app/src/main/java/com/screwy/igloo/data/entity/FeedItemEntity.kt", `package com.screwy.igloo.data.entity

import androidx.room.Entity

@Entity(tableName = "feed_items")
data class FeedItemEntity(val id: String)
`)

	idx := New(root)
	idx.Build()

	androidMap := idx.GetAndroidMap("")
	if !strings.Contains(androidMap, "FeedRoute") {
		t.Fatalf("expected FeedRoute in android map, got:\n%s", androidMap)
	}
	if !strings.Contains(androidMap, "FeedViewModel") {
		t.Fatalf("expected FeedViewModel in android map, got:\n%s", androidMap)
	}
	if !strings.Contains(androidMap, "FeedReadDao") {
		t.Fatalf("expected FeedReadDao in android map, got:\n%s", androidMap)
	}
	if !strings.Contains(androidMap, "channel/{channel_id}") {
		t.Fatalf("expected named route in nav order, got:\n%s", androidMap)
	}

	trace := idx.TraceScreen("Feed")
	if !strings.Contains(trace, "FeedViewModel") || !strings.Contains(trace, "FeedReadDao") || !strings.Contains(trace, "feed_items") {
		t.Fatalf("expected rewritten screen trace to include vm + dao + table, got:\n%s", trace)
	}
}

func TestCodeIndexBuildConfigMap(t *testing.T) {
	root := t.TempDir()

	mustWrite := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite(".mcp.json", `{
  "mcpServers": {
    "igloo": {
      "command": "./bin/igloo-mcp"
    }
  }
}`)
	mustWrite("compose.yaml", `services:
  igloo:
    image: ghcr.io/screwys/igloo:latest
`)
	mustWrite(".semgrep.yml", `rules:
  - id: igloo.shell-wrapper-command
`)
	mustWrite(".github/dependabot.yml", `version: 2
updates:
  - package-ecosystem: gomod
    directory: /
  - package-ecosystem: github-actions
    directory: /
`)
	mustWrite(".github/workflows/go-ci.yml", `name: Go CI
jobs:
  test:
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
`)

	idx := New(root)
	stats := idx.Build()
	if !strings.Contains(stats, "config_files=5") {
		t.Fatalf("expected config file count in build stats, got %s", stats)
	}

	configMap := idx.GetConfigMap("")
	for _, want := range []string{
		".mcp.json [mcp]",
		"command: ./bin/igloo-mcp",
		"compose.yaml [compose]",
		"service: igloo",
		"rule: igloo.shell-wrapper-command",
		".github/dependabot.yml [dependabot]",
		"ecosystem: gomod",
		"action: actions/setup-go@v6",
	} {
		if !strings.Contains(configMap, want) {
			t.Fatalf("expected %q in config map, got:\n%s", want, configMap)
		}
	}

	trace := idx.TraceFile(".github/workflows/go-ci.yml")
	if !strings.Contains(trace, "layer: config") || !strings.Contains(trace, "actions/checkout@v5") {
		t.Fatalf("expected workflow trace to include config layer and action, got:\n%s", trace)
	}
}
