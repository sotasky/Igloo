package persistenceaudit

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunReportsLifecycles(t *testing.T) {
	dbPath := createAuditFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"-db", dbPath, "-top", "2"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"db: " + dbPath,
		"lifecycles:",
		"warnings:",
		"unclassified_tables",
		"archive:",
		"maintained_state:",
		"derived_cache:",
		"unclassified:",
		"feed_items",
		"assets",
		"android_sync_items",
		"custom_table",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReadReportClassifiesGroups(t *testing.T) {
	dbPath := createAuditFixture(t)

	report, err := ReadReport(dbPath, 1)
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	groups := make(map[string]LifecycleGroup)
	for _, group := range report.Groups {
		groups[group.Lifecycle] = group
	}
	if groups["archive"].Tables != 1 || groups["archive"].Rows != 2 {
		t.Fatalf("archive group = %+v", groups["archive"])
	}
	if groups["maintained_state"].Tables != 1 || groups["maintained_state"].Rows != 1 {
		t.Fatalf("maintained group = %+v", groups["maintained_state"])
	}
	if groups["derived_cache"].Tables != 1 || groups["derived_cache"].Rows != 3 {
		t.Fatalf("derived group = %+v", groups["derived_cache"])
	}
	if groups["unclassified"].Tables != 1 || groups["unclassified"].Rows != 1 {
		t.Fatalf("unclassified group = %+v", groups["unclassified"])
	}
	if len(groups["archive"].TopTables) != 1 || groups["archive"].TopTables[0].Name != "feed_items" {
		t.Fatalf("archive top tables = %+v", groups["archive"].TopTables)
	}
	if len(report.Warnings) == 0 || report.Warnings[0].Code != "unclassified_tables" {
		t.Fatalf("warnings = %+v, want unclassified_tables", report.Warnings)
	}
}

func TestParseOptionsRejectsNegativeTop(t *testing.T) {
	if _, err := parseOptions([]string{"-top", "-1"}); err == nil {
		t.Fatal("parseOptions accepted negative top")
	}
}

func createAuditFixture(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "igloo.db")
	conn, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	for _, stmt := range []string{
		`CREATE TABLE feed_items (tweet_id TEXT PRIMARY KEY, body TEXT)`,
		`CREATE TABLE assets (asset_id TEXT PRIMARY KEY, state TEXT)`,
		`CREATE TABLE android_sync_items (generation_id TEXT, seq INTEGER, payload_json TEXT, PRIMARY KEY (generation_id, seq))`,
		`CREATE TABLE custom_table (id INTEGER PRIMARY KEY, value TEXT)`,
		`INSERT INTO feed_items (tweet_id, body) VALUES ('sample_post_1', 'body'), ('sample_post_2', 'body')`,
		`INSERT INTO assets (asset_id, state) VALUES ('sample_asset_1', 'ready')`,
		`INSERT INTO android_sync_items (generation_id, seq, payload_json) VALUES ('sample_generation', 1, '{}'), ('sample_generation', 2, '{}'), ('sample_generation', 3, '{}')`,
		`INSERT INTO custom_table (value) VALUES ('sample')`,
	} {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("exec fixture statement %q: %v", stmt, err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("stat fixture db: %v", err)
	}
	return dbPath
}
