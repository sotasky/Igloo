package download

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const (
	OperationStatusSuccess = "success"
	OperationStatusFailure = "failure"

	ErrorKindAuth          = "auth"
	ErrorKindRateLimit     = "rate_limit"
	ErrorKindNotFound      = "not_found"
	ErrorKindPermanentHTTP = "permanent_http"
	ErrorKindTemporary     = "temporary"
	ErrorKindEmptyResult   = "empty_result"
	ErrorKindParse         = "parse"
	ErrorKindCanceled      = "canceled"
	ErrorKindUnknown       = "unknown"

	ErrorStrategyRetry     = "retry"
	ErrorStrategyPermanent = "permanent"
)

// OperationSink records downloader operation summaries.
type OperationSink interface {
	RecordDownloaderOperation(ctx context.Context, op model.DownloaderOperation) error
}

func recordOperation(ctx context.Context, sink OperationSink, op model.DownloaderOperation) {
	if sink == nil {
		return
	}
	if op.StartedAtMs == 0 {
		op.StartedAtMs = time.Now().UnixMilli()
	}
	if op.EndedAtMs == 0 {
		op.EndedAtMs = time.Now().UnixMilli()
	}
	if op.ElapsedMs == 0 && op.EndedAtMs >= op.StartedAtMs {
		op.ElapsedMs = op.EndedAtMs - op.StartedAtMs
	}
	if op.Status == "" {
		op.Status = OperationStatusSuccess
	}
	op.Error = RedactText(op.Error)
	op.SummaryJSON = RedactText(op.SummaryJSON)
	if err := sink.RecordDownloaderOperation(ctx, op); err != nil {
		log.Printf("[download] record operation %s/%s: %v", op.Platform, op.Operation, err)
	}
}

func operationSummaryJSON(fields map[string]any) string {
	return SummaryJSON(fields)
}

func SummaryJSON(fields map[string]any) string {
	if len(fields) == 0 {
		return ""
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return ""
	}
	s := RedactText(string(data))
	if len(s) > 2000 {
		return s[:2000]
	}
	return s
}

func platformFromURL(rawURL string) string {
	switch {
	case IsTikTokURL(rawURL):
		return "tiktok"
	case IsInstagramURL(rawURL):
		return "instagram"
	case isYouTubeURL(rawURL):
		return "youtube"
	case isTwitterURL(rawURL):
		return "twitter"
	default:
		return "http"
	}
}

func isTwitterURL(rawURL string) bool {
	host, _, ok := httpURLParts(rawURL)
	return ok && (hostMatches(host, "x.com", "twitter.com", "fxtwitter.com", "vxtwitter.com", "pbs.twimg.com", "video.twimg.com"))
}

func statusForError(err error) string {
	if err == nil {
		return OperationStatusSuccess
	}
	return OperationStatusFailure
}

func subjectForURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if len(rawURL) <= 180 {
		return rawURL
	}
	return rawURL[:180]
}
