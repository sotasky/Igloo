package db

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

// TestGetAllSettings verifies GetAllSettings returns settings written by SetSetting.
func TestGetAllSettings(t *testing.T) {
	d := openWritableTestDB(t)

	if err := d.SetSetting("", "test_key_a", "value_a"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := d.SetSetting("", "test_key_b", "value_b"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	all, err := d.GetAllSettings()
	if err != nil {
		t.Fatalf("GetAllSettings: %v", err)
	}
	if all["test_key_a"] != "value_a" {
		t.Errorf("test_key_a: got %q, want %q", all["test_key_a"], "value_a")
	}
	if all["test_key_b"] != "value_b" {
		t.Errorf("test_key_b: got %q, want %q", all["test_key_b"], "value_b")
	}
}

// TestUpdateSettings verifies batch upsert adds new keys and overwrites existing ones.
func TestUpdateSettings(t *testing.T) {
	d := openWritableTestDB(t)

	// Seed one key
	if err := d.SetSetting("", "existing_key", "old_value"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	updates := map[string]string{
		"existing_key": "new_value",
		"brand_new":    "fresh",
	}
	if err := d.UpdateSettings(updates); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	all, err := d.GetAllSettings()
	if err != nil {
		t.Fatalf("GetAllSettings: %v", err)
	}
	if all["existing_key"] != "new_value" {
		t.Errorf("existing_key: got %q, want %q", all["existing_key"], "new_value")
	}
	if all["brand_new"] != "fresh" {
		t.Errorf("brand_new: got %q, want %q", all["brand_new"], "fresh")
	}
}

// TestAddAndGetChannel verifies adding and then retrieving a channel by ID.
func TestAddAndGetChannel(t *testing.T) {
	d := openWritableTestDB(t)

	ch := model.Channel{
		ChannelID:    "test_ch_001",
		Name:         "Test Channel",
		URL:          "https://example.com/test",
		Platform:     "youtube",
		IsSubscribed: true,
	}
	if err := d.AddChannel(ch); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	// MaxVideos now lives in the channel_settings side table.
	if err := d.UpdateChannelSettings("test_ch_001", map[string]any{"max_videos": 10}); err != nil {
		t.Fatalf("UpdateChannelSettings: %v", err)
	}

	got, err := d.GetChannelByID("test_ch_001")
	if err != nil {
		t.Fatalf("GetChannelByID: %v", err)
	}
	if got.ChannelID != "test_ch_001" {
		t.Errorf("ChannelID: got %q, want %q", got.ChannelID, "test_ch_001")
	}
	if got.Name != "Test Channel" {
		t.Errorf("Name: got %q, want %q", got.Name, "Test Channel")
	}
	if got.Platform != "youtube" {
		t.Errorf("Platform: got %q, want %q", got.Platform, "youtube")
	}
	if !got.IsSubscribed {
		t.Error("IsSubscribed should be true when set")
	}
	if got.IsStarred {
		t.Error("IsStarred should be false by default")
	}
	settings, err := d.GetChannelSettings("test_ch_001")
	if err != nil {
		t.Fatalf("GetChannelSettings: %v", err)
	}
	if settings.MaxVideos != 10 {
		t.Errorf("MaxVideos: got %d, want 10", settings.MaxVideos)
	}
}

// TestAddChannelDuplicate verifies a second insert with the same channel_id errors.
func TestAddChannelDuplicate(t *testing.T) {
	d := openWritableTestDB(t)

	ch := model.Channel{
		ChannelID: "dup_ch_001",
		Name:      "Dup Channel",
		Platform:  "youtube",
	}
	if err := d.AddChannel(ch); err != nil {
		t.Fatalf("first AddChannel: %v", err)
	}
	if err := d.AddChannel(ch); err == nil {
		t.Fatal("second AddChannel should have returned an error for duplicate channel_id")
	}
}

// TestDeleteChannel verifies a channel can be added and then deleted.
func TestDeleteChannel(t *testing.T) {
	d := openWritableTestDB(t)

	ch := model.Channel{
		ChannelID: "del_ch_001",
		Name:      "Delete Me",
		Platform:  "youtube",
	}
	if err := d.AddChannel(ch); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	if err := d.DeleteChannel("del_ch_001"); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	// Verify gone
	if _, err := d.GetChannelByID("del_ch_001"); err == nil {
		t.Fatal("GetChannelByID should error after deletion")
	}

	// Delete non-existent channel should error
	if err := d.DeleteChannel("del_ch_001"); err == nil {
		t.Fatal("DeleteChannel of already-deleted channel should error")
	}
}

// TestDeleteMediaFilesByOwner verifies media files are deleted by owner.
func TestDeleteMediaFilesByOwner(t *testing.T) {
	d := openWritableTestDB(t)

	mf := model.MediaFile{
		OwnerType:  "avatar",
		OwnerID:    "test_owner_999",
		MediaIndex: 0,
		FilePath:   "avatars/test_owner_999.jpg",
		MediaType:  "avatar",
	}
	if err := d.InsertMediaFile(mf); err != nil {
		t.Fatalf("InsertMediaFile: %v", err)
	}

	// Verify it was inserted
	if _, err := d.GetMediaFilePath("avatar", "test_owner_999", 0); err != nil {
		t.Fatalf("GetMediaFilePath: expected file, got error: %v", err)
	}

	// Delete by owner
	if err := d.DeleteMediaFilesByOwner("avatar", "test_owner_999"); err != nil {
		t.Fatalf("DeleteMediaFilesByOwner: %v", err)
	}

	// Verify gone
	if _, err := d.GetMediaFilePath("avatar", "test_owner_999", 0); err == nil {
		t.Fatal("GetMediaFilePath should error after deletion")
	}
}

// TestExportConfig verifies ExportConfig returns a valid structure.
func TestExportConfig(t *testing.T) {
	d := openWritableTestDB(t)

	// Add a channel
	ch := model.Channel{
		ChannelID:    "export_ch_001",
		Name:         "Export Test Channel",
		Platform:     "youtube",
		IsSubscribed: true,
	}
	if err := d.AddChannel(ch); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	// Set a setting
	if err := d.SetSetting("", "export_test_key", "export_test_val"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg, err := d.ExportConfig("")
	if err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}

	if cfg.Version != 1 {
		t.Errorf("Version: got %d, want 1", cfg.Version)
	}
	if cfg.ExportedAt.IsZero() {
		t.Error("ExportedAt should not be zero")
	}

	// Verify the new channel appears in subscriptions
	found := false
	for _, ch := range cfg.Subscriptions {
		if ch.ChannelID == "export_ch_001" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ExportConfig: expected export_ch_001 in Subscriptions")
	}

	// Verify settings are exported
	if cfg.Settings["export_test_key"] != "export_test_val" {
		t.Errorf("Settings[export_test_key]: got %q, want %q",
			cfg.Settings["export_test_key"], "export_test_val")
	}

	// Settings and Subscriptions should be non-nil maps/slices
	if cfg.Settings == nil {
		t.Error("Settings map should not be nil")
	}
}

func TestImportConfigPreservesDefaultCategoryBookmarkLabel(t *testing.T) {
	d := openWritableTestDB(t)

	cfg := ConfigExport{
		Version: 1,
		Bookmarks: []BookmarkExport{
			{VideoID: "default_bookmark", CustomTitle: "Saved Label"},
		},
	}
	result, err := d.ImportConfig(cfg, "alice", false)
	if err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}
	if result.AddedBookmarks != 1 {
		t.Fatalf("AddedBookmarks = %d, want 1", result.AddedBookmarks)
	}

	labels, err := d.GetBookmarkLabels("alice", "")
	if err != nil {
		t.Fatalf("GetBookmarkLabels: %v", err)
	}
	if len(labels) != 1 || labels[0] != "Saved Label" {
		t.Fatalf("labels = %#v, want Saved Label", labels)
	}
}
