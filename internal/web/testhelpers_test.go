package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/i18n"
	"github.com/screwys/igloo/internal/storage"
	"github.com/screwys/igloo/internal/worker"
)

func testWebConfig(t *testing.T, stateRoot string) *config.Config {
	t.Helper()
	markWebTestStateRoot(t, stateRoot)
	layout, err := storage.New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	return &config.Config{SecretKey: "test-key", Storage: layout}
}

func setTestStateRoot(t *testing.T, cfg *config.Config, stateRoot string) {
	t.Helper()
	markWebTestStateRoot(t, stateRoot)
	layout, err := storage.New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Storage = layout
}

func markWebTestStateRoot(t *testing.T, stateRoot string) {
	t.Helper()
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateRoot, ".igloo-state-root"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeInto(raw []byte, into any) error {
	return json.Unmarshal(raw, into)
}

// testServer bundles the live Server with a test-only mux so we can dispatch
// requests directly without the real middleware chain. Auth is injected via
// attachTestAuth; unauthenticated handlers should see userFromContext(ctx) == nil.
type testServer struct {
	*Server
	mux *http.ServeMux
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	tmp, err := os.CreateTemp("", "igloo-test-*.db")
	if err != nil {
		t.Fatalf("temp db: %v", err)
	}
	path := tmp.Name()
	_ = tmp.Close()

	stateRoot := t.TempDir()
	d, err := db.OpenPath(path, stateRoot)
	if err != nil {
		_ = os.Remove(path)
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Close()
		_ = os.Remove(path)
	})

	cfg := testWebConfig(t, stateRoot)
	s := &Server{
		db:      d,
		cfg:     cfg,
		store:   sessions.NewCookieStore([]byte("test-key")),
		workers: worker.NewManager(d, cfg),
		staticV: func(path string) string {
			return "/static/" + path + "?v=test"
		},
		i18n: i18n.NewCatalog(),
	}

	mux := http.NewServeMux()
	s.registerFeedAPIRoutes(mux)
	s.registerFeedSourceAPIRoutes(mux)
	s.registerBookmarkAPIRoutes(mux)
	s.registerSyncAPIRoutes(mux)
	s.registerVideoAPIRoutes(mux)
	s.registerShortsAPIRoutes(mux)
	s.registerProfileAPIRoutes(mux)
	s.registerAndroidSyncAPIRoutes(mux)
	s.registerMutationAPIRoutes(mux)
	s.registerChannelAPIRoutes(mux)
	s.registerThreadAPIRoutes(mux)
	s.registerI18NAPIRoutes(mux)
	mux.HandleFunc("GET /thread/{tweetID}", s.handlePageThread)
	mux.HandleFunc("GET /api/media/thumbnail/{videoID}", s.handleThumbnail)
	mux.HandleFunc("GET /api/media/avatar/{channelID}", s.handleChannelAvatar)
	mux.HandleFunc("GET /api/media/comment-avatar/{ownerID}", s.handleCommentAuthorAvatar)

	return &testServer{Server: s, mux: mux}
}

// attachTestAuth returns a copy of r whose context has a userInfo with the
// given username under the real userContextKey — matching what enforceAuth
// would have set. Use "" to leave the request unauthenticated.
func attachTestAuth(r *http.Request, username string) *http.Request {
	return attachTestAuthRole(r, username, "user")
}

func attachTestAuthRole(r *http.Request, username, role string) *http.Request {
	if username == "" {
		return r
	}
	if role == "" {
		role = "user"
	}
	ctx := context.WithValue(r.Context(), userContextKey, &userInfo{
		Username: username,
		Role:     role,
	})
	return r.WithContext(ctx)
}
