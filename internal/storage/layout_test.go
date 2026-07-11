package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func markRoot(t *testing.T, root, marker string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, marker), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultLayoutMapsOnlyMediaNamespace(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	markRoot(t, stateRoot, stateRootMarker)
	layout, err := New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(stateRoot, "media")
	if layout.StateRoot() != stateRoot || layout.MediaRoot() != mediaRoot {
		t.Fatalf("roots = %q, %q", layout.StateRoot(), layout.MediaRoot())
	}
	if layout.DatabasePath() != filepath.Join(stateRoot, "igloo.db") {
		t.Fatalf("database = %q", layout.DatabasePath())
	}

	for key, want := range map[string]string{
		"thumbnails/post/item.jpg":     filepath.Join(stateRoot, "thumbnails", "post", "item.jpg"),
		"media/twitter/item/video.mp4": filepath.Join(mediaRoot, "twitter", "item", "video.mp4"),
	} {
		got, err := layout.Path(key)
		if err != nil || got != want {
			t.Fatalf("Path(%q) = %q, %v", key, got, err)
		}
		if roundTrip, err := layout.Key(got); err != nil || roundTrip != key {
			t.Fatalf("Key(Path(%q)) = %q, %v", key, roundTrip, err)
		}
	}
	for _, key := range []string{"", ".", "media", "/tmp/item", "../item", "safe/../item", "media/../../item"} {
		if _, err := layout.Path(key); err == nil {
			t.Errorf("Path(%q) succeeded", key)
		}
	}
	if _, err := layout.Key("relative/item"); err == nil {
		t.Fatal("Key accepted a relative path")
	}
	if _, err := layout.Key(filepath.Join(filepath.Dir(stateRoot), "outside", "item")); err == nil {
		t.Fatal("Key accepted a path outside both roots")
	}

	explicit, err := New(stateRoot, mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := explicit.Ensure(); err != nil {
		t.Fatal(err)
	}
}

func TestStateRootMustBeMarkedAndRemainMounted(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	layout, err := New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err == nil {
		t.Fatal("missing state root was accepted")
	}
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("Ensure provisioned state root: %v", err)
	}
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err == nil {
		t.Fatal("unmarked state root was accepted")
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "media")); !os.IsNotExist(err) {
		t.Fatalf("Ensure created media before marker validation: %v", err)
	}
	markRoot(t, stateRoot, stateRootMarker)
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(stateRoot, stateRootMarker)); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.Path("thumbnails/item.jpg"); err == nil {
		t.Fatal("state path survived marker removal")
	}
	if _, err := layout.Path("media/source/item.mp4"); err == nil {
		t.Fatal("co-located media path survived state marker removal")
	}
}

func TestExternalMediaRootIsIndependentAndMarked(t *testing.T) {
	base := t.TempDir()
	stateRoot := filepath.Join(base, "state")
	mediaRoot := filepath.Join(base, "external")
	markRoot(t, stateRoot, stateRootMarker)
	layout, err := New(stateRoot, mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err == nil {
		t.Fatal("missing external root was accepted")
	}
	if _, err := os.Stat(mediaRoot); !os.IsNotExist(err) {
		t.Fatalf("Ensure provisioned external root: %v", err)
	}
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err == nil {
		t.Fatal("unmarked external root was accepted")
	}
	markRoot(t, mediaRoot, mediaRootMarker)
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if got, _ := layout.Path("media/source/item.mp4"); got != filepath.Join(mediaRoot, "source", "item.mp4") {
		t.Fatalf("external media path = %q", got)
	}
	if got, _ := layout.Path("thumbnails/source/item.jpg"); got != filepath.Join(stateRoot, "thumbnails", "source", "item.jpg") {
		t.Fatalf("state-derived path = %q", got)
	}
	if _, err := layout.Key(filepath.Join(stateRoot, "media", "item.mp4")); err == nil {
		t.Fatal("reserved state media namespace was exposed")
	}
	if err := os.Remove(filepath.Join(mediaRoot, mediaRootMarker)); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.Path("media/source/item.mp4"); err == nil {
		t.Fatal("external media path survived marker removal")
	}
	if _, err := layout.Path("thumbnails/source/item.jpg"); err != nil {
		t.Fatalf("state path depends on external media: %v", err)
	}
}

func TestLayoutRejectsOverlappingAndAliasedRoots(t *testing.T) {
	base := t.TempDir()
	stateRoot := filepath.Join(base, "state")
	mediaRoot := filepath.Join(base, "media")
	if _, err := New(stateRoot, filepath.Join(stateRoot, "bulk")); err == nil {
		t.Fatal("external root inside state was accepted")
	}
	if _, err := New(filepath.Join(mediaRoot, "state"), mediaRoot); err == nil {
		t.Fatal("state root inside external root was accepted")
	}
	stateMedia := filepath.Join(stateRoot, "bulk")
	if err := os.MkdirAll(stateMedia, 0o755); err != nil {
		t.Fatal(err)
	}
	mediaLink := filepath.Join(base, "media-link")
	if err := os.Symlink(stateMedia, mediaLink); err != nil {
		t.Fatal(err)
	}
	if _, err := New(stateRoot, mediaLink); err == nil {
		t.Fatal("external root aliased into state was accepted")
	}
}

func TestWriteAndRestorePathsRejectChildSymlinks(t *testing.T) {
	stateRoot := t.TempDir()
	outside := t.TempDir()
	markRoot(t, stateRoot, stateRootMarker)
	if err := os.Symlink(outside, filepath.Join(stateRoot, "thumbnails")); err != nil {
		t.Fatal(err)
	}
	layout, err := New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layout.WritePath("thumbnails/item.jpg"); err == nil {
		t.Fatal("WritePath followed a child symlink")
	}

	configRoot := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(configRoot, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateContainedPath(configRoot, filepath.Join(configRoot, "nested", "config.json")); err == nil {
		t.Fatal("restore path followed a child symlink")
	}
	if err := ValidateContainedPath(configRoot, filepath.Join(configRoot, "plain", "config.json")); err != nil {
		t.Fatalf("contained path rejected: %v", err)
	}
}

func TestRootMarkerMustBeARegularFile(t *testing.T) {
	base := t.TempDir()
	stateRoot := filepath.Join(base, "state")
	if err := os.MkdirAll(stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "marker")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(stateRoot, stateRootMarker)); err != nil {
		t.Fatal(err)
	}
	layout, err := New(stateRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err == nil {
		t.Fatal("symlink marker was accepted")
	}
}
