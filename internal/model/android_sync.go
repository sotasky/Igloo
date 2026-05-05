package model

import "encoding/json"

// AndroidSyncGeneration describes one immutable server-owned Android mirror.
// Page cursors are numeric seq values inside this generation only.
type AndroidSyncGeneration struct {
	GenerationID            string         `json:"generation_id"`
	CreatedAtMs             int64          `json:"created_at_ms"`
	Status                  string         `json:"status"`
	SourceVersion           string         `json:"source_version"`
	Retention               map[string]int `json:"retention"`
	ItemCount               int            `json:"item_count"`
	AssetCount              int            `json:"asset_count"`
	ReadyAssetCount         int            `json:"ready_asset_count"`
	ServerMissingAssetCount int            `json:"server_missing_asset_count"`
	TotalBytes              int64          `json:"total_bytes"`
	ContentCounts           map[string]int `json:"content_counts"`
	AssetCounts             map[string]int `json:"asset_counts"`
}

// AndroidSyncItem is a content row inside a generation. PayloadJSON is the
// authoritative server DTO for the item_kind.
type AndroidSyncItem struct {
	GenerationID string          `json:"-"`
	Seq          int64           `json:"seq"`
	ItemKind     string          `json:"item_kind"`
	ItemID       string          `json:"item_id"`
	PayloadJSON  json.RawMessage `json:"payload"`
}

// AndroidSyncAsset is one desired binary asset for a generation. State is
// "ready" when the server can serve and hash it, or "server_missing" when the
// content row is desired but the backing file is absent server-side.
type AndroidSyncAsset struct {
	GenerationID       string `json:"-"`
	Seq                int64  `json:"seq"`
	AssetID            string `json:"asset_id"`
	AssetKind          string `json:"asset_kind"`
	OwnerID            string `json:"owner_id"`
	OwnerKind          string `json:"owner_kind"`
	Bucket             string `json:"bucket"`
	ServerURL          string `json:"server_url"`
	ContentType        string `json:"content_type,omitempty"`
	SizeBytes          int64  `json:"size_bytes"`
	SHA256             string `json:"sha256,omitempty"`
	State              string `json:"state"`
	RequiredReason     string `json:"required_reason,omitempty"`
	IsAuto             *bool  `json:"is_auto,omitempty"`
	AudioLanguage      string `json:"audio_language,omitempty"`
	EffectiveRecencyMs int64  `json:"effective_recency_ms"`
}
