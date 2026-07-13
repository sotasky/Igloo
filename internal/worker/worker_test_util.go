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
	"github.com/screwys/igloo/internal/storage"
)

func newTestWorkerDB(t *testing.T) *db.DB {
	return newTestWorkerDBAt(t, t.TempDir())
}

func openWorkerTestDB(t *testing.T) *db.DB {
	return newTestWorkerDB(t)
}

func newTestWorkerDBAt(t *testing.T, stateRoot string) *db.DB {
	t.Helper()
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp("", "igloo-worker-test-*.db")
	if err != nil {
		t.Fatalf("create temp db file: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	d, err := db.OpenPath(dbPath, stateRoot)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := d.RecordAndroidFeedRetention(0, 1); err != nil {
		t.Fatalf("initialize Android feed retention: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func testCfg(dataDir string) *config.Config {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, ".igloo-state-root"), nil, 0o644); err != nil {
		panic(err)
	}
	layout, err := storage.New(dataDir, "")
	if err != nil {
		panic(err)
	}
	return &config.Config{Storage: layout}
}

// testDownloader returns a minimal Downloader whose HTTP component uses
// the real HTTP client — tests spin up httptest.Server stubs, so real HTTP is fine.
func testDownloader() *download.Downloader {
	d := download.NewDownloader(filepath.Join(os.TempDir(), "no-such-cookies"))
	d.HTTP = &download.HTTPDownloader{Client: &http.Client{}, AllowPrivateHosts: true}
	return d
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
