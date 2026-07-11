package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/model"
)

const (
	androidSyncModelVersion       = 1
	androidSyncChangePageSize     = 500
	androidSyncBootstrapPageSize  = 1000
	androidSyncBootstrapLifetime  = 30 * time.Minute
	androidSyncMaxBootstrapCaches = 4
)

type androidSyncCursor struct {
	Version   int    `json:"v"`
	Mode      string `json:"m"`
	Epoch     string `json:"e"`
	Revision  int64  `json:"r,omitempty"`
	Session   string `json:"s,omitempty"`
	Index     int    `json:"i,omitempty"`
	Retention string `json:"t,omitempty"`
}

type androidSyncBootstrapSession struct {
	Epoch         string
	Through       int64
	RetentionHash string
	Heads         []model.AndroidSyncHead
	CreatedAt     time.Time
}

func (s *Server) handleAndroidSyncBootstrap(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	retention, err := androidSyncRetentionSettingsFromRequest(r, s.androidSyncRetentionFallback())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_retention", err.Error())
		return
	}
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	if after == "" {
		s.startAndroidSyncBootstrap(w, retention)
		return
	}
	cursor, err := decodeAndroidSyncCursor(after)
	if err != nil || cursor.Mode != "bootstrap" || cursor.Version != androidSyncModelVersion {
		writeAndroidSyncResetRequired(w)
		return
	}
	s.writeAndroidSyncBootstrapPage(w, cursor, retention)
}

func (s *Server) startAndroidSyncBootstrap(w http.ResponseWriter, retention db.AndroidRetentionSettings) {
	var session *androidSyncBootstrapSession
	err := s.db.WithReadSnapshot(func(snapshot *db.DB) error {
		clock, err := snapshot.GetAndroidSyncClock()
		if err != nil {
			return err
		}
		heads, err := s.buildAndroidSyncBootstrapHeads(snapshot, retention, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		session = &androidSyncBootstrapSession{
			Epoch:         clock.Epoch,
			Through:       clock.Revision,
			RetentionHash: androidSyncRetentionHash(retention),
			Heads:         heads,
			CreatedAt:     time.Now(),
		}
		return nil
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "bootstrap_failed", err.Error())
		return
	}
	sessionID, err := newAndroidSyncSessionID()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "bootstrap_failed", "bootstrap token failed")
		return
	}
	s.androidSyncBootstrapMu.Lock()
	s.pruneAndroidSyncBootstrapsLocked(time.Now())
	if s.androidSyncBootstraps == nil {
		s.androidSyncBootstraps = make(map[string]*androidSyncBootstrapSession)
	}
	for len(s.androidSyncBootstraps) >= androidSyncMaxBootstrapCaches {
		var oldestID string
		var oldest time.Time
		for id, candidate := range s.androidSyncBootstraps {
			if oldestID == "" || candidate.CreatedAt.Before(oldest) {
				oldestID, oldest = id, candidate.CreatedAt
			}
		}
		delete(s.androidSyncBootstraps, oldestID)
	}
	s.androidSyncBootstraps[sessionID] = session
	s.androidSyncBootstrapMu.Unlock()
	s.writeAndroidSyncBootstrapPage(w, androidSyncCursor{
		Version: androidSyncModelVersion, Mode: "bootstrap", Epoch: session.Epoch,
		Session: sessionID, Retention: session.RetentionHash,
	}, retention)
}

func (s *Server) writeAndroidSyncBootstrapPage(w http.ResponseWriter, cursor androidSyncCursor, retention db.AndroidRetentionSettings) {
	if cursor.Index < 0 || cursor.Retention != androidSyncRetentionHash(retention) {
		writeAndroidSyncResetRequired(w)
		return
	}
	s.androidSyncBootstrapMu.Lock()
	s.pruneAndroidSyncBootstrapsLocked(time.Now())
	session := s.androidSyncBootstraps[cursor.Session]
	if session == nil || session.Epoch != cursor.Epoch || session.RetentionHash != cursor.Retention {
		s.androidSyncBootstrapMu.Unlock()
		writeAndroidSyncResetRequired(w)
		return
	}
	start := cursor.Index
	if start > len(session.Heads) {
		s.androidSyncBootstrapMu.Unlock()
		writeAndroidSyncResetRequired(w)
		return
	}
	end := min(start+androidSyncBootstrapPageSize, len(session.Heads))
	heads := append([]model.AndroidSyncHead(nil), session.Heads[start:end]...)
	finished := end == len(session.Heads)
	var next androidSyncCursor
	if finished {
		next = androidSyncCursor{
			Version: androidSyncModelVersion, Mode: "changes",
			Epoch: session.Epoch, Revision: session.Through, Retention: session.RetentionHash,
		}
	} else {
		next = cursor
		next.Index = end
	}
	s.androidSyncBootstrapMu.Unlock()
	var changes []model.AndroidSyncChange
	err := s.db.WithReadSnapshot(func(snapshot *db.DB) error {
		clock, err := snapshot.GetAndroidSyncClock()
		if err != nil {
			return err
		}
		if clock.Epoch != session.Epoch {
			return errAndroidSyncResetRequired
		}
		changes, err = s.materializeAndroidSyncHeads(snapshot, heads, nil)
		return err
	})
	if err != nil {
		if err == errAndroidSyncResetRequired {
			writeAndroidSyncResetRequired(w)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "bootstrap_failed", err.Error())
		return
	}
	encoded, err := encodeAndroidSyncCursor(next)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "cursor_failed", "sync cursor failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"changes": changes, "next_cursor": encoded, "end_of_stream": finished,
	})
}

func (s *Server) handleAndroidSyncChanges(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	cursor, err := decodeAndroidSyncCursor(strings.TrimSpace(r.URL.Query().Get("after")))
	retention, retentionErr := androidSyncRetentionSettingsFromRequest(r, s.androidSyncRetentionFallback())
	if retentionErr != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_retention", retentionErr.Error())
		return
	}
	retentionHash := androidSyncRetentionHash(retention)
	if err != nil || cursor.Mode != "changes" || cursor.Version != androidSyncModelVersion ||
		cursor.Revision < 0 || cursor.Retention != retentionHash {
		writeAndroidSyncResetRequired(w)
		return
	}
	var changes []model.AndroidSyncChange
	var nextRevision int64
	var finished bool
	err = s.db.WithReadSnapshot(func(snapshot *db.DB) error {
		clock, err := snapshot.GetAndroidSyncClock()
		if err != nil {
			return err
		}
		if clock.Epoch != cursor.Epoch || cursor.Revision > clock.Revision {
			return errAndroidSyncResetRequired
		}
		heads, err := snapshot.ListAndroidSyncHeads(cursor.Revision, androidSyncChangePageSize+1)
		if err != nil {
			return err
		}
		finished = len(heads) <= androidSyncChangePageSize
		if !finished {
			heads = heads[:androidSyncChangePageSize]
		}
		var desired *db.AndroidSyncDesiredSets
		for _, head := range heads {
			if head.OwnerKind == "feed" || head.OwnerKind == "video" ||
				head.OwnerKind == "retweet_sources" || head.OwnerKind == "feed_rank" {
				selection, err := snapshot.ListAndroidSyncDesiredContent(retention, time.Now().UnixMilli())
				if err != nil {
					return err
				}
				desired = &selection
				break
			}
		}
		changes, err = s.materializeAndroidSyncHeads(snapshot, heads, desired)
		if err != nil {
			return err
		}
		if finished {
			nextRevision = clock.Revision
		} else {
			nextRevision = heads[len(heads)-1].Revision
		}
		return nil
	})
	if err != nil {
		if err == errAndroidSyncResetRequired {
			writeAndroidSyncResetRequired(w)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "changes_failed", err.Error())
		return
	}
	next, err := encodeAndroidSyncCursor(androidSyncCursor{
		Version: androidSyncModelVersion, Mode: "changes", Epoch: cursor.Epoch,
		Revision: nextRevision, Retention: retentionHash,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "cursor_failed", "sync cursor failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"changes": changes, "next_cursor": next, "end_of_stream": finished,
	})
}

func (s *Server) handleAndroidSyncAssetFile(w http.ResponseWriter, r *http.Request) {
	if userFromContext(r.Context()) == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	assetID := strings.TrimSpace(r.PathValue("assetID"))
	revision, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("revision")), 10, 64)
	if assetID == "" || err != nil || revision <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_asset_revision", "asset id and positive revision are required")
		return
	}
	if !s.tryAcquireAndroidSyncAssetServeSlot() {
		w.Header().Set("Retry-After", strconv.Itoa(androidSyncAssetRetryAfterSecs))
		http.Error(w, "asset server busy", http.StatusTooManyRequests)
		return
	}
	defer s.releaseAndroidSyncAssetServeSlot()
	asset, err := s.db.GetAndroidSyncAssetByID(assetID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "asset_lookup_failed", "asset lookup failed")
		return
	}
	if asset == nil || asset.State != db.AssetStateReady || strings.TrimSpace(asset.FilePath) == "" {
		http.NotFound(w, r)
		return
	}
	if asset.Revision != revision {
		writeJSONError(w, http.StatusConflict, "asset_changed", "asset descriptor changed")
		return
	}
	path, err := s.cfg.Storage.Path(asset.FilePath)
	if err != nil {
		s.withdrawAndroidSyncAsset(w, *asset)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.withdrawAndroidSyncAsset(w, *asset)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "asset_read_failed", "asset file could not be read")
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		if os.IsNotExist(err) {
			s.withdrawAndroidSyncAsset(w, *asset)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "asset_read_failed", "asset file could not be read")
		return
	}
	if !info.Mode().IsRegular() || info.Size() != asset.SizeBytes ||
		(asset.FileMtimeNs > 0 && info.ModTime().UnixNano() != asset.FileMtimeNs) {
		s.withdrawAndroidSyncAsset(w, *asset)
		return
	}
	current, err := s.db.GetAndroidSyncAssetByID(assetID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "asset_lookup_failed", "asset lookup failed")
		return
	}
	if current == nil || current.State != db.AssetStateReady || current.Revision != revision ||
		current.FilePath != asset.FilePath || current.SizeBytes != asset.SizeBytes ||
		current.FileMtimeNs != asset.FileMtimeNs || !strings.EqualFold(current.SHA256, asset.SHA256) {
		writeJSONError(w, http.StatusConflict, "asset_changed", "asset descriptor changed")
		return
	}
	if current.ContentType != "" {
		w.Header().Set("Content-Type", current.ContentType)
	}
	w.Header().Set("ETag", `"`+current.SHA256+`"`)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func (s *Server) withdrawAndroidSyncAsset(w http.ResponseWriter, asset db.Asset) {
	if _, err := s.db.MarkReadyAssetUnavailable(asset, time.Now().UnixMilli()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "asset_update_failed", "asset state could not be updated")
		return
	}
	writeJSONError(w, http.StatusConflict, "asset_changed", "asset bytes changed")
}

var errAndroidSyncResetRequired = fmt.Errorf("sync reset required")

func writeAndroidSyncResetRequired(w http.ResponseWriter) {
	writeJSONError(w, http.StatusConflict, "sync_reset_required", "sync cursor does not match this server")
}

func encodeAndroidSyncCursor(cursor androidSyncCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeAndroidSyncCursor(raw string) (androidSyncCursor, error) {
	var cursor androidSyncCursor
	if strings.TrimSpace(raw) == "" {
		return cursor, fmt.Errorf("cursor required")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return cursor, err
	}
	if cursor.Epoch == "" {
		return cursor, fmt.Errorf("cursor epoch required")
	}
	return cursor, nil
}

func androidSyncRetentionHash(retention db.AndroidRetentionSettings) string {
	raw := fmt.Sprintf("%d/%d/%d/%d", retention.FeedDays, retention.YoutubeDays, retention.MomentsDays, retention.StoryHours)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

func newAndroidSyncSessionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Server) pruneAndroidSyncBootstrapsLocked(now time.Time) {
	for id, session := range s.androidSyncBootstraps {
		if now.Sub(session.CreatedAt) > androidSyncBootstrapLifetime {
			delete(s.androidSyncBootstraps, id)
		}
	}
}

func marshalAndroidSyncChange(ownerKind, ownerID, bucket string, retainAtMs int64, payload any) (model.AndroidSyncChange, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return model.AndroidSyncChange{}, err
	}
	return model.AndroidSyncChange{
		OwnerKind: ownerKind, OwnerID: ownerID, Operation: model.AndroidSyncOperationUpsert,
		RetentionBucket: bucket, RetainAtMs: retainAtMs, PayloadJSON: raw,
	}, nil
}

func androidSyncDeleteChange(ownerKind, ownerID string) model.AndroidSyncChange {
	return model.AndroidSyncChange{
		OwnerKind: ownerKind, OwnerID: ownerID, Operation: model.AndroidSyncOperationDelete,
	}
}
