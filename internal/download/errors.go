package download

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

type FailureClassification struct {
	Kind       string
	Strategy   string
	Permanent  bool
	RetryDelay time.Duration
}

// OperationContextError carries the concrete downloader backend and credential
// source that produced an error while preserving normal error unwrapping.
type OperationContextError struct {
	Tool        string
	CookieLabel string
	Err         error
}

func (e *OperationContextError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *OperationContextError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func WithOperationContext(err error, tool, cookieLabel string) error {
	if err == nil {
		return nil
	}
	var existing *OperationContextError
	if errors.As(err, &existing) {
		return err
	}
	return &OperationContextError{Tool: strings.TrimSpace(tool), CookieLabel: strings.TrimSpace(cookieLabel), Err: err}
}

func ErrorOperationContext(err error) (tool, cookieLabel string) {
	var ctxErr *OperationContextError
	if errors.As(err, &ctxErr) && ctxErr != nil {
		return ctxErr.Tool, ctxErr.CookieLabel
	}
	return "", ""
}

func ClassifyError(err error, output []byte) string {
	text := strings.ToLower(string(output))
	if err != nil {
		text += "\n" + strings.ToLower(err.Error())
	}
	switch {
	case errors.Is(err, context.Canceled):
		return ErrorKindCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorKindTemporary
	}
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == 429 {
			return ErrorKindRateLimit
		}
		if httpErr.StatusCode == 404 || httpErr.StatusCode == 410 {
			return ErrorKindNotFound
		}
		if httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 {
			if containsAuthSignal(text) {
				return ErrorKindAuth
			}
			return ErrorKindPermanentHTTP
		}
		if httpErr.StatusCode >= 500 {
			return ErrorKindTemporary
		}
	}
	if containsAny(text, "429", "too many requests", "rate limit", "rate-limit", "ratelimit", "rate limited") {
		return ErrorKindRateLimit
	}
	if containsInstagramAccessThrottleSignal(text) {
		return ErrorKindRateLimit
	}
	if containsAny(text, "service unavailable", "http error 500", "http error 502", "http error 503", "http error 504") {
		return ErrorKindTemporary
	}
	if IsTransportFailure(err, output) {
		return ErrorKindTemporary
	}
	if containsAuthSignal(text) {
		return ErrorKindAuth
	}
	if containsAny(text, "not found", "notfound", "404", "410", "no such user", "requested post not available", "unavailable") {
		return ErrorKindNotFound
	}
	if containsAny(text, "403 forbidden", "http error 403", "error 403", "status 403", "unexpected status 403", "forbidden") {
		return ErrorKindPermanentHTTP
	}
	if containsAny(text, "no files downloaded", "no video formats", "no results", "empty result", "returned no info", "returned no") {
		return ErrorKindEmptyResult
	}
	if containsAny(text, "invalid character", "unexpected eof", "parse", "decode", "unmarshal") {
		return ErrorKindParse
	}
	if err != nil {
		return ErrorKindUnknown
	}
	return ""
}

// IsTransportFailure reports failures that say the network path failed rather
// than the remote account, item, credentials, or response. Callers use this to
// avoid charging shared connectivity outages to individual jobs.
func IsTransportFailure(err error, output []byte) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	text := strings.ToLower(err.Error() + "\n" + string(output))
	return containsAny(text,
		"network is unreachable",
		"network unreachable",
		"no route to host",
		"temporary failure in name resolution",
		"name or service not known",
		"could not resolve host",
		"failed to resolve",
		"connection refused",
		"connection reset",
		"connection aborted",
		"connection timed out",
		"connect timeout",
		"read timeout",
		"i/o timeout",
		"tls handshake timeout",
		"deadline exceeded",
	)
}

func ErrorKind(err error) string {
	return ClassifyError(err, nil)
}

func ClassifyFailure(err error, output []byte, attempt int) FailureClassification {
	kind := ClassifyError(err, output)
	permanent := false
	switch kind {
	case ErrorKindAuth, ErrorKindPermanentHTTP, ErrorKindEmptyResult:
		permanent = true
	}
	if err == nil {
		return FailureClassification{Kind: kind}
	}
	if permanent {
		return FailureClassification{Kind: kind, Strategy: ErrorStrategyPermanent, Permanent: true}
	}
	delay := retryDelayForKind(kind, attempt)
	return FailureClassification{Kind: kind, Strategy: ErrorStrategyRetry, RetryDelay: delay}
}

func retryDelayForKind(kind string, attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	switch kind {
	case ErrorKindRateLimit:
		delay := time.Hour
		for i := 1; i < attempt && delay < 12*time.Hour; i++ {
			delay *= 2
		}
		if delay > 12*time.Hour {
			return 12 * time.Hour
		}
		return delay
	default:
		delay := 30 * time.Second
		for i := 0; i < attempt && delay < 6*time.Hour; i++ {
			delay *= 2
		}
		if delay > 6*time.Hour {
			return 6 * time.Hour
		}
		return delay
	}
}

func commandError(tool string, result CommandResult) error {
	if result.Err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w: %s", tool, result.Err, RedactText(string(result.CombinedOutput())))
}

func errorString(err error, output []byte) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(output) > 0 {
		msg += ": " + string(output)
	}
	return RedactText(msg)
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func containsInstagramAccessThrottleSignal(s string) bool {
	if !strings.Contains(s, "instagram") {
		return false
	}
	if containsAny(s, "api is not granting access", "redirect loop detected") {
		return true
	}
	return strings.Contains(s, "400 bad request") &&
		strings.Contains(s, "/api/v1/media/")
}

func containsAuthSignal(s string) bool {
	return containsAny(s,
		"login required",
		"redirect to login",
		"login page",
		"locked behind the login",
		"not logged in",
		"authentication",
		"authorizationerror",
		"authrequired",
		"unauthorized",
		"use --cookies",
		"use --cookies-from-browser",
		"cookies missing",
		"cookies are missing",
		"cookies required",
		"cookies are no longer valid",
		"invalid cookies",
		"expired cookies",
	)
}
