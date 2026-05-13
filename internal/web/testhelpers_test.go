package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/screwys/igloo/internal/config"
	"github.com/screwys/igloo/internal/db"
	"github.com/screwys/igloo/internal/i18n"
	"github.com/screwys/igloo/internal/worker"
)

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
	tmp.Close()

	d, err := db.Open(path, t.TempDir())
	if err != nil {
		os.Remove(path)
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
		os.Remove(path)
	})

	cfg := &config.Config{SecretKey: "test-key", DataDir: t.TempDir()}
	s := &Server{
		db:      d,
		cfg:     cfg,
		store:   sessions.NewCookieStore([]byte("test-key")),
		workers: worker.NewManager(d, cfg),
		staticV: func(path string) string {
			return "/static/" + path + "?v=test"
		},
		i18n:          i18n.NewCatalog(),
		profileFlight: newProfileFlight(),
	}
	s.requestAvatar = s.workers.RequestAvatar

	mux := http.NewServeMux()
	s.registerFeedAPIRoutes(mux)
	s.registerFeedSourceAPIRoutes(mux)
	s.registerBookmarkAPIRoutes(mux)
	s.registerSyncAPIRoutes(mux)
	s.registerVideoAPIRoutes(mux)
	s.registerShortsAPIRoutes(mux)
	s.registerProfileAPIRoutes(mux)
	s.registerDeltaAPIRoutes(mux)
	s.registerAndroidSyncAPIRoutes(mux)
	s.registerMutationAPIRoutes(mux)
	s.registerChannelAPIRoutes(mux)
	s.registerThreadAPIRoutes(mux)
	s.registerI18NAPIRoutes(mux)
	mux.HandleFunc("GET /thread/{tweetID}", s.handlePageThread)
	mux.HandleFunc("GET /api/media/thumbnail/{videoID}", s.handleThumbnail)
	mux.HandleFunc("GET /api/media/avatar/{channelID}", s.handleChannelAvatar)

	return &testServer{Server: s, mux: mux}
}

// attachTestAuth returns a copy of r whose context has a userInfo with the
// given username under the real userContextKey — matching what enforceAuth
// would have set. Use "" to leave the request unauthenticated.
func attachTestAuth(r *http.Request, username string) *http.Request {
	if username == "" {
		return r
	}
	ctx := context.WithValue(r.Context(), userContextKey, &userInfo{
		Username: username,
		Role:     "user",
	})
	return r.WithContext(ctx)
}
