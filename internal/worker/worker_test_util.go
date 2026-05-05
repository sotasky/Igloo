package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/download"
)

func newTestWorkerDB(t *testing.T) *db.DB {
	t.Helper()
	f, err := os.CreateTemp("", "igloo-worker-test-*.db")
	if err != nil {
		t.Fatalf("create temp db file: %v", err)
	}
	dbPath := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(dbPath) })

	d, err := db.Open(dbPath, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func testCfg(dataDir string) *config.Config {
	return &config.Config{DataDir: dataDir}
}

// testDownloader returns a minimal Downloader whose HTTP component uses
// the real HTTP client — tests spin up httptest.Server stubs, so real HTTP is fine.
func testDownloader() *download.Downloader {
	return download.NewDownloader(filepath.Join(os.TempDir(), "no-such-cookies"))
}

type stubBannerSrv struct {
	*httptest.Server
	hits atomic.Int32
}

func (s *stubBannerSrv) Hits() int32 { return s.hits.Load() }

func startStubBannerServer(t *testing.T) *stubBannerSrv {
	t.Helper()
	s := &stubBannerSrv{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = fmt.Fprint(w, "fakebannerbytes")
	}))
	return s
}
