package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPServerSetsTimeouts(t *testing.T) {
	handler := http.NewServeMux()
	srv := newHTTPServer(":0", handler)

	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout = %s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 5*time.Minute {
		t.Fatalf("WriteTimeout = %s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout = %s", srv.IdleTimeout)
	}
	if srv.Handler != handler {
		t.Fatal("handler was not preserved")
	}
}
