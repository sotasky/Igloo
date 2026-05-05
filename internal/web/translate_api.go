package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/translate"
)

func (s *Server) registerTranslateAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/translate", s.handleTranslate)
}

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TweetID    string `json:"tweet_id"`
		Field      string `json:"field"`
		TargetLang string `json:"target_lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	body.TweetID = strings.TrimSpace(body.TweetID)
	body.Field = strings.TrimSpace(body.Field)
	body.TargetLang = strings.ToLower(strings.TrimSpace(body.TargetLang))
	if body.TweetID == "" || body.TargetLang == "" {
		writeJSON(w, 400, map[string]any{"error": "tweet_id and target_lang required"})
		return
	}
	if body.Field != "body" && body.Field != "quote" {
		writeJSON(w, 400, map[string]any{"error": "field must be 'body' or 'quote'"})
		return
	}

	translateCtx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	result, err := translate.FeedText(translateCtx, s.db, body.TweetID, body.Field, body.TargetLang)
	if err != nil {
		s.writeTranslateError(w, body.TweetID, body.Field, err)
		return
	}

	resp := map[string]any{
		"translated_text": result.TranslatedText,
		"source_lang":     result.SourceLang,
		"target_lang":     result.TargetLang,
	}
	if result.Provider != "" && result.Provider != "cache" {
		resp["provider"] = result.Provider
	}
	writeJSON(w, 200, resp)
}

func (s *Server) writeTranslateError(w http.ResponseWriter, tweetID, field string, err error) {
	var already translate.AlreadyTargetLanguageError
	switch {
	case errors.As(err, &already):
		writeJSON(w, http.StatusOK, map[string]any{"error": "Already in target language", "source_lang": already.SourceLang})
	case errors.Is(err, translate.ErrUnsupportedField):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "field must be 'body' or 'quote'"})
	case errors.Is(err, translate.ErrNoText):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "no text to translate"})
	case errors.Is(err, translate.ErrFeedItemNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "feed item not found"})
	case errors.Is(err, translate.ErrNotConfigured):
		slog.Error("translate", "tweet_id", tweetID, "field", field, "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "translation provider not configured"})
	case errors.Is(err, translate.ErrTranslationFailed):
		slog.Error("translate", "tweet_id", tweetID, "field", field, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "translation failed"})
	default:
		slog.Error("translate", "tweet_id", tweetID, "field", field, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "translation failed"})
	}
}
