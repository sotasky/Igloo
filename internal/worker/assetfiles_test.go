package worker

import (
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractRepresentativeFrameSkipsBlankCandidate(t *testing.T) {
	root := t.TempDir()
	black := filepath.Join(root, "black.jpg")
	white := filepath.Join(root, "white.jpg")
	writeSolidJPEG(t, black, color.Black)
	writeSolidJPEG(t, white, color.White)

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ffprobe := "#!/bin/sh\nprintf '10\\n'\n"
	ffmpeg := `#!/bin/sh
set -eu
count=0
if [ -f "$IGLOO_FRAME_COUNT" ]; then count=$(cat "$IGLOO_FRAME_COUNT"); fi
count=$((count + 1))
printf '%s' "$count" > "$IGLOO_FRAME_COUNT"
for output in "$@"; do :; done
if [ "$count" -eq 1 ]; then cp "$IGLOO_BLACK_FRAME" "$output"; else cp "$IGLOO_VISIBLE_FRAME" "$output"; fi
`
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(ffprobe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "ffmpeg"), []byte(ffmpeg), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("IGLOO_FRAME_COUNT", filepath.Join(root, "count"))
	t.Setenv("IGLOO_BLACK_FRAME", black)
	t.Setenv("IGLOO_VISIBLE_FRAME", white)

	out := filepath.Join(root, "representative.jpg")
	if err := extractRepresentativeFrame(context.Background(), filepath.Join(root, "video.mp4"), out); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(root, "count"))
	if err != nil || strings.TrimSpace(string(calls)) != "2" {
		t.Fatalf("ffmpeg calls = %q, err=%v", calls, err)
	}
	visible, err := thumbnailFrameVisible(out)
	if err != nil || !visible {
		t.Fatalf("selected frame visible=%v err=%v", visible, err)
	}
}

func writeSolidJPEG(t *testing.T, path string, fill color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, fill)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(file, img, nil); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
