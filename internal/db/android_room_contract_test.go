package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type roomSchemaFile struct {
	Database struct {
		Version  int          `json:"version"`
		Entities []roomEntity `json:"entities"`
	} `json:"database"`
}

type roomEntity struct {
	TableName string      `json:"tableName"`
	Fields    []roomField `json:"fields"`
}

type roomField struct {
	ColumnName string `json:"columnName"`
}

func TestAndroidRoomSchemaV40Owners(t *testing.T) {
	schema := readAndroidRoomSchema(t)
	if schema.Database.Version != 40 {
		t.Fatalf("Room schema version = %d, want 40", schema.Database.Version)
	}

	tables := make(map[string][]string, len(schema.Database.Entities))
	for _, entity := range schema.Database.Entities {
		columns := make([]string, 0, len(entity.Fields))
		for _, field := range entity.Fields {
			columns = append(columns, field.ColumnName)
		}
		tables[entity.TableName] = columns
	}

	assertRoomColumns(t, tables, "android_sync_state",
		"id", "mode", "cursor", "feed_days", "youtube_days", "moments_days",
		"story_hours", "bootstrap_required")
	assertRoomColumns(t, tables, "android_sync_heads",
		"owner_kind", "owner_id", "retention_bucket", "retain_at_ms", "bootstrap_seen")
	assertRoomColumns(t, tables, "android_sync_assets",
		"asset_id", "asset_kind", "media_index",
		"owner_id", "owner_kind", "bucket", "content_type", "size_bytes", "sha256",
		"revision", "subtitle_is_auto", "state", "local_path", "verified_at_ms", "next_attempt_at_ms")
	assertRoomColumns(t, tables, "muted_channels", "channel_id", "muted_at")
	assertRoomColumns(t, tables, "moments_cursors",
		"scope", "video_id", "position_ms", "sort_at_ms", "updated_at_ms")

	assertRoomContains(t, tables, "feed_items",
		"tweet_id", "source_channel_id", "reposter_channel_id", "quote_channel_id",
		"reply_channel_id", "channel_id")
	assertRoomContains(t, tables, "channel_profiles",
		"channel_id", "handle", "display_name", "bio", "followers", "following")

	for _, table := range []string{
		"android_content_state",
		"android_asset_snapshots",
		"android_sync_generations",
		"android_sync_items",
		"mutation_state",
		"muted_accounts",
	} {
		if _, exists := tables[table]; exists {
			t.Errorf("legacy Room table %q still exists", table)
		}
	}
	assertRoomExcludes(t, tables, "feed_items",
		"source_handle", "author_handle", "author_display_name", "author_avatar_url",
		"quote_author_handle", "quote_author_display_name", "quote_author_avatar_url",
		"reply_to_handle", "sync_seq")
	assertRoomExcludes(t, tables, "android_sync_assets",
		"generation_id", "server_url", "server_state", "required_reason",
		"effective_recency_ms", "file_size", "attempt_count", "last_error", "updated_at_ms")
}

func assertRoomColumns(t *testing.T, tables map[string][]string, table string, want ...string) {
	t.Helper()
	got, exists := tables[table]
	if !exists {
		t.Fatalf("Room table %q is missing", table)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Room columns for %s = %v, want %v", table, got, want)
	}
}

func assertRoomContains(t *testing.T, tables map[string][]string, table string, want ...string) {
	t.Helper()
	got, exists := tables[table]
	if !exists {
		t.Fatalf("Room table %q is missing", table)
	}
	columns := make(map[string]bool, len(got))
	for _, column := range got {
		columns[column] = true
	}
	for _, column := range want {
		if !columns[column] {
			t.Errorf("Room column %s.%s is missing", table, column)
		}
	}
}

func assertRoomExcludes(t *testing.T, tables map[string][]string, table string, unwanted ...string) {
	t.Helper()
	columns := make(map[string]bool, len(tables[table]))
	for _, column := range tables[table] {
		columns[column] = true
	}
	for _, column := range unwanted {
		if columns[column] {
			t.Errorf("legacy Room column %s.%s still exists", table, column)
		}
	}
}

func readAndroidRoomSchema(t *testing.T) roomSchemaFile {
	t.Helper()
	dir := filepath.Join("..", "..", "android", "app", "schemas", "com.screwy.igloo.data.IglooDatabase")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"40.json"}) {
		t.Fatalf("Room schema files = %v, want only 40.json", names)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "40.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema roomSchemaFile
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}
