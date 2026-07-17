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
	androidSyncLegacyModelVersion = 1
	androidSyncModelVersion       = 2
	androidSyncChangePageSize     = 500
	androidSyncBootstrapPageSize  = 250
	androidSyncSessionLifetime    = 30 * time.Minute
	androidSyncMaxSessions        = 4
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

type androidSyncSession struct {
	Version       int
	Mode          string
	Epoch         string
	Through       int64
	RetentionHash string
	Heads         []model.AndroidSyncHead
	// Bootstrap takes a complete, frozen selection before it starts paging.
	// Changes deliberately derive the selection from the current bounded head
	// page so a stale cursor does not materialize every missed owner up front.
	Selection *db.AndroidSyncDesiredSets
	CreatedAt time.Time
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
	fullYoutubeMetadata := androidSyncFullYoutubeMetadataRequested(r)
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	if after == "" {
		if s.workers != nil {
			if err := s.workers.ApplyAndroidFeedRetention(retention.FeedDays); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "retention_failed", "media retention reconciliation failed")
				return
			}
		}
		version := androidSyncLegacyModelVersion
		if fullYoutubeMetadata {
			version = androidSyncModelVersion
		}
		s.startAndroidSyncBootstrap(w, retention, version)
		return
	}
	cursor, err := decodeAndroidSyncCursor(after)
	if err != nil || cursor.Mode != "bootstrap" || !androidSyncModelVersionSupported(cursor.Version) ||
		(fullYoutubeMetadata && cursor.Version != androidSyncModelVersion) {
		writeAndroidSyncResetRequired(w)
		return
	}
	s.writeAndroidSyncBootstrapPage(w, cursor, retention)
}

func (s *Server) startAndroidSyncBootstrap(w http.ResponseWriter, retention db.AndroidRetentionSettings, version int) {
	var session *androidSyncSession
	err := s.db.WithReadSnapshot(func(snapshot *db.DB) error {
		clock, err := snapshot.GetAndroidSyncClock()
		if err != nil {
			return err
		}
		heads, selection, err := s.buildAndroidSyncBootstrapSelection(
			snapshot, retention, time.Now().UnixMilli(), androidSyncFullYoutubeMetadataForVersion(version),
		)
		if err != nil {
			return err
		}
		session = &androidSyncSession{
			Version:       version,
			Mode:          "bootstrap",
			Epoch:         clock.Epoch,
			Through:       clock.Revision,
			RetentionHash: androidSyncRetentionHash(retention),
			Heads:         heads,
			Selection:     &selection,
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
	s.storeAndroidSyncSession(sessionID, session)
	s.writeAndroidSyncBootstrapPage(w, androidSyncCursor{
		Version: session.Version, Mode: "bootstrap", Epoch: session.Epoch,
		Session: sessionID, Retention: session.RetentionHash,
	}, retention)
}

func (s *Server) writeAndroidSyncBootstrapPage(w http.ResponseWriter, cursor androidSyncCursor, retention db.AndroidRetentionSettings) {
	if cursor.Index < 0 || cursor.Retention != androidSyncRetentionHash(retention) {
		writeAndroidSyncResetRequired(w)
		return
	}
	s.androidSyncSessionMu.Lock()
	s.pruneAndroidSyncSessionsLocked(time.Now())
	session := s.androidSyncSessions[cursor.Session]
	if session == nil || session.Version != cursor.Version || session.Mode != "bootstrap" ||
		session.Epoch != cursor.Epoch || session.RetentionHash != cursor.Retention {
		s.androidSyncSessionMu.Unlock()
		writeAndroidSyncResetRequired(w)
		return
	}
	start := cursor.Index
	if start > len(session.Heads) {
		s.androidSyncSessionMu.Unlock()
		writeAndroidSyncResetRequired(w)
		return
	}
	end := min(start+androidSyncBootstrapPageSize, len(session.Heads))
	heads := append([]model.AndroidSyncHead(nil), session.Heads[start:end]...)
	finished := end == len(session.Heads)
	var next androidSyncCursor
	if finished {
		next = androidSyncCursor{
			Version: session.Version, Mode: "changes",
			Epoch: session.Epoch, Revision: session.Through, Retention: session.RetentionHash,
		}
	} else {
		next = cursor
		next.Index = end
	}
	s.androidSyncSessionMu.Unlock()
	var changes []model.AndroidSyncChange
	err := s.db.WithReadSnapshot(func(snapshot *db.DB) error {
		clock, err := snapshot.GetAndroidSyncClock()
		if err != nil {
			return err
		}
		if clock.Epoch != session.Epoch {
			return errAndroidSyncResetRequired
		}
		changes, err = s.materializeAndroidSyncHeads(snapshot, heads, session.Selection)
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
	fullYoutubeMetadata := androidSyncFullYoutubeMetadataRequested(r)
	retention, retentionErr := androidSyncRetentionSettingsFromRequest(r, s.androidSyncRetentionFallback())
	if retentionErr != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_retention", retentionErr.Error())
		return
	}
	retentionHash := androidSyncRetentionHash(retention)
	if err != nil || cursor.Mode != "changes" || !androidSyncModelVersionSupported(cursor.Version) ||
		(fullYoutubeMetadata && cursor.Version != androidSyncModelVersion) ||
		cursor.Revision < 0 || cursor.Retention != retentionHash {
		writeAndroidSyncResetRequired(w)
		return
	}
	sessionID := cursor.Session
	var session *androidSyncSession
	newSession := sessionID == ""
	if !newSession {
		session = s.getAndroidSyncSession(sessionID)
		if session == nil {
			newSession = true
			sessionID = ""
		} else if session.Version != cursor.Version || session.Mode != "changes" ||
			session.Epoch != cursor.Epoch || session.RetentionHash != retentionHash {
			writeAndroidSyncResetRequired(w)
			return
		}
	}
	if newSession {
		if s.workers != nil {
			if err := s.workers.ApplyAndroidFeedRetention(retention.FeedDays); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "retention_failed", "media retention reconciliation failed")
				return
			}
		}
		sessionID, err = newAndroidSyncSessionID()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "changes_failed", "sync session failed")
			return
		}
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
		if newSession {
			session = &androidSyncSession{
				Version:       cursor.Version,
				Mode:          "changes",
				Epoch:         clock.Epoch,
				Through:       clock.Revision,
				RetentionHash: retentionHash,
				CreatedAt:     time.Now(),
			}
		}
		if cursor.Revision > session.Through {
			return errAndroidSyncResetRequired
		}
		heads, err := snapshot.ListAndroidSyncHeadsThrough(cursor.Revision, session.Through, androidSyncChangePageSize+1)
		if err != nil {
			return err
		}
		finished = len(heads) <= androidSyncChangePageSize
		if !finished {
			heads = heads[:androidSyncChangePageSize]
		}
		selection := emptyAndroidSyncDesiredSets()
		if len(heads) > 0 {
			selection, err = s.buildAndroidSyncChangeSelection(
				snapshot, retention, time.Now().UnixMilli(), heads,
				androidSyncFullYoutubeMetadataForVersion(session.Version),
			)
			if err != nil {
				return err
			}
		}
		changes, err = s.materializeAndroidSyncHeads(snapshot, heads, &selection)
		if err != nil {
			return err
		}
		if finished {
			nextRevision = session.Through
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
	if newSession && !finished {
		s.storeAndroidSyncSession(sessionID, session)
	}
	nextSession := sessionID
	if finished {
		nextSession = ""
	}
	next, err := encodeAndroidSyncCursor(androidSyncCursor{
		Version: cursor.Version, Mode: "changes", Epoch: cursor.Epoch,
		Revision: nextRevision, Session: nextSession, Retention: retentionHash,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "cursor_failed", "sync cursor failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"changes": changes, "next_cursor": next, "end_of_stream": finished,
	})
}

func androidSyncModelVersionSupported(version int) bool {
	return version == androidSyncLegacyModelVersion || version == androidSyncModelVersion
}

func androidSyncFullYoutubeMetadataRequested(r *http.Request) bool {
	return r.URL.Query().Get("full_youtube_metadata") == "1"
}

func androidSyncFullYoutubeMetadataForVersion(version int) bool {
	return version == androidSyncModelVersion
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
		current.FileMtimeNs != asset.FileMtimeNs {
		writeJSONError(w, http.StatusConflict, "asset_changed", "asset descriptor changed")
		return
	}
	if current.ContentType != "" {
		w.Header().Set("Content-Type", current.ContentType)
	}
	w.Header().Set("ETag", fmt.Sprintf(`"revision-%d"`, current.Revision))
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

func (s *Server) pruneAndroidSyncSessionsLocked(now time.Time) {
	for id, session := range s.androidSyncSessions {
		if now.Sub(session.CreatedAt) > androidSyncSessionLifetime {
			delete(s.androidSyncSessions, id)
		}
	}
}

func (s *Server) getAndroidSyncSession(id string) *androidSyncSession {
	s.androidSyncSessionMu.Lock()
	defer s.androidSyncSessionMu.Unlock()
	s.pruneAndroidSyncSessionsLocked(time.Now())
	return s.androidSyncSessions[id]
}

func (s *Server) storeAndroidSyncSession(id string, session *androidSyncSession) {
	s.androidSyncSessionMu.Lock()
	defer s.androidSyncSessionMu.Unlock()
	s.pruneAndroidSyncSessionsLocked(time.Now())
	if s.androidSyncSessions == nil {
		s.androidSyncSessions = make(map[string]*androidSyncSession)
	}
	for len(s.androidSyncSessions) >= androidSyncMaxSessions {
		var oldestID string
		var oldest time.Time
		for candidateID, candidate := range s.androidSyncSessions {
			if oldestID == "" || candidate.CreatedAt.Before(oldest) {
				oldestID, oldest = candidateID, candidate.CreatedAt
			}
		}
		delete(s.androidSyncSessions, oldestID)
	}
	s.androidSyncSessions[id] = session
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
