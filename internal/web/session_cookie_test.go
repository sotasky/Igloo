package web

import (
	"net/http"
	"testing"
)

func TestNewSessionStoreUsesExplicitCookieOptions(t *testing.T) {
	store := newSessionStore("test-secret")
	opts := store.Options

	if opts == nil {
		t.Fatal("store options are nil")
	}
	if opts.Path != "/" {
		t.Fatalf("Path = %q", opts.Path)
	}
	if opts.MaxAge != sessionCookieMaxAge {
		t.Fatalf("MaxAge = %d", opts.MaxAge)
	}
	if !opts.Secure {
		t.Fatal("Secure = false")
	}
	if !opts.HttpOnly {
		t.Fatal("HttpOnly = false")
	}
	if opts.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v", opts.SameSite)
	}
}
