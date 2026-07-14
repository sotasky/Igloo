package download

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGalleryDLDownloadStagesUnderDestinationAndMovesOutputs(t *testing.T) {
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
case "$out" in
  "$EXPECTED_DEST"/.gallerydl-*) ;;
  *) exit 42 ;;
esac
mkdir -p "$out"
printf 'image data' > "$out/slide.jpg"
printf 'audio data' > "$out/sound.mp3"
printf '{"id":"source"}' > "$out/slide.json"
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	destDir := t.TempDir()
	t.Setenv("EXPECTED_DEST", destDir)
	completed, err := (&GalleryDLWrapper{Runner: CommandRunner{}}).DownloadCompleted(
		context.Background(),
		"https://www.tiktok.com/@sample_handle/video/sample_video",
		destDir,
		"post",
		"",
	)
	if err != nil {
		t.Fatalf("DownloadCompleted returned error: %v", err)
	}
	paths := completed.MediaPaths

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
	if completed.InfoJSONPath != filepath.Join(destDir, "post.info.json") {
		t.Fatalf("info path = %q", completed.InfoJSONPath)
	}
	assertFileContent(t, completed.InfoJSONPath, `{"id":"source"}`)
	if completed.Metadata["id"] != "source" {
		t.Fatalf("metadata = %#v", completed.Metadata)
	}
}

func TestGalleryDLDownloadCompletedReturnsExactVideoThumbnail(t *testing.T) {
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
printf 'video data' > "$out/video.mp4"
printf 'thumbnail data' > "$out/cover.png"
printf '{"id":"source"}' > "$out/video.json"
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	destDir := t.TempDir()
	completed, err := (&GalleryDLWrapper{Runner: CommandRunner{}}).DownloadCompleted(
		context.Background(),
		"https://www.instagram.com/reel/sample/",
		destDir,
		"post",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.MediaPaths) != 1 || completed.MediaPaths[0] != filepath.Join(destDir, "post.mp4") {
		t.Fatalf("media paths = %#v", completed.MediaPaths)
	}
	if completed.ThumbnailPath != filepath.Join(destDir, "post.png") {
		t.Fatalf("thumbnail path = %q", completed.ThumbnailPath)
	}
	assertFileContent(t, completed.ThumbnailPath, "thumbnail data")
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
