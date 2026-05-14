package download

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type closeErrorWriter struct {
	bytes.Buffer
	closeErr error
	closed   bool
}

func (w *closeErrorWriter) Close() error {
	w.closed = true
	return w.closeErr
}

func TestCopyStreamAndCloseReturnsDestinationCloseError(t *testing.T) {
	closeErr := errors.New("delayed writeback failed")
	dest := &closeErrorWriter{closeErr: closeErr}

	err := copyStreamAndClose(strings.NewReader("video data"), dest)
	if !errors.Is(err, closeErr) {
		t.Fatalf("copyStreamAndClose error = %v, want close error %v", err, closeErr)
	}
	if !dest.closed {
		t.Fatal("copyStreamAndClose did not close destination")
	}
	if got := dest.String(); got != "video data" {
		t.Fatalf("destination content = %q, want %q", got, "video data")
	}
}

func TestGalleryDLDownloadCopiesImageAudioAndInfo(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "gallery-dl"), `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -D)
      shift
      out="$1"
      ;;
  esac
  shift
done
mkdir -p "$out"
printf 'image data' > "$out/slide.jpg"
printf 'audio data' > "$out/sound.mp3"
printf '{"id":"source"}' > "$out/slide.json"
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	destDir := t.TempDir()
	paths, err := (&GalleryDLWrapper{Runner: CommandRunner{}}).Download(
		context.Background(),
		"https://www.tiktok.com/@sample_handle/video/sample_video",
		destDir,
		"post",
		"",
	)
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}

	wantPaths := []string{
		filepath.Join(destDir, "post_1.jpg"),
		filepath.Join(destDir, "post.mp3"),
	}
	if len(paths) != len(wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	for i := range wantPaths {
		if paths[i] != wantPaths[i] {
			t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
		}
	}
	assertFileContent(t, filepath.Join(destDir, "post_1.jpg"), "image data")
	assertFileContent(t, filepath.Join(destDir, "post.mp3"), "audio data")
	assertFileContent(t, filepath.Join(destDir, "post.info.json"), `{"id":"source"}`)
}

func TestEnforceGalleryDLOutputLimitsRejectsLargeFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "slide.jpg"), []byte("large"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	err = enforceGalleryDLOutputLimits(entries, galleryDLOutputLimits{
		imageAudioFileBytes: 4,
		videoFileBytes:      100,
		metadataFileBytes:   100,
		otherFileBytes:      100,
		totalBytes:          100,
	})
	if err == nil {
		t.Fatal("expected size limit error")
	}
	if !strings.Contains(err.Error(), "slide.jpg") || !strings.Contains(err.Error(), "5 bytes") {
		t.Fatalf("error = %v, want file size detail", err)
	}
}

func TestEnforceGalleryDLOutputLimitsRejectsLargeTotal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "slide.jpg"), []byte("abc"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sound.mp3"), []byte("def"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	err = enforceGalleryDLOutputLimits(entries, galleryDLOutputLimits{
		imageAudioFileBytes: 10,
		videoFileBytes:      10,
		metadataFileBytes:   10,
		otherFileBytes:      10,
		totalBytes:          5,
	})
	if err == nil {
		t.Fatal("expected total limit error")
	}
	if !strings.Contains(err.Error(), "total exceeds 5 bytes") {
		t.Fatalf("error = %v, want total size detail", err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
