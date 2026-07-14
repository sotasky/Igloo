package model

import "encoding/json"

const (
	AndroidSyncOperationUpsert = "upsert"
	AndroidSyncOperationDelete = "delete"
)

// AndroidSyncHead is the compact server-side convergence index. One row owns
// the latest observable state for one Android mirror owner.
type AndroidSyncHead struct {
	OwnerKind string
	OwnerID   string
	Revision  int64
}

// AndroidSyncChange is the typed transport unit shared by bootstrap and
// incremental sync pages. Delete rows deliberately carry no payload.
type AndroidSyncChange struct {
	OwnerKind       string          `json:"owner_kind"`
	OwnerID         string          `json:"owner_id"`
	Operation       string          `json:"operation"`
	RetentionBucket string          `json:"retention_bucket"`
	RetainAtMs      int64           `json:"retain_at_ms"`
	PayloadJSON     json.RawMessage `json:"payload,omitempty"`
}

type AndroidSyncAsset struct {
	AssetID            string `json:"asset_id"`
	AssetKind          string `json:"asset_kind"`
	MediaIndex         int    `json:"media_index"`
	OwnerID            string `json:"owner_id"`
	OwnerKind          string `json:"owner_kind"`
	Bucket             string `json:"bucket"`
	ContentType        string `json:"content_type"`
	SizeBytes          int64  `json:"size_bytes"`
	Revision           int64  `json:"revision"`
	State              string `json:"state"`
	IsAuto             *bool  `json:"is_auto"`
	EffectiveRecencyMs int64  `json:"-"`
}
