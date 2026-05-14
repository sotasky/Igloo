package dearrow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func ensureFfmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
}

func TestExtractFrame_WritesJPEG(t *testing.T) {
	ensureFfmpeg(t)

	out := filepath.Join(t.TempDir(), "frame.jpg")
	if err := ExtractFrame(t.Context(), "testdata/tiny.mp4", 2.5, out); err != nil {
		t.Fatalf("ExtractFrame: %v", err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Size() < 100 {
		t.Errorf("output size = %d bytes, want > 100", info.Size())
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer func() {
		_ = f.Close()
	}()
	magic := make([]byte, 3)
	if _, err := f.Read(magic); err != nil {
		t.Fatalf("read magic: %v", err)
	}
	if magic[0] != 0xFF || magic[1] != 0xD8 || magic[2] != 0xFF {
		t.Errorf("JPEG magic = %#x %#x %#x, want 0xFF 0xD8 0xFF", magic[0], magic[1], magic[2])
	}
}

func TestExtractFrame_CleansUpOnFailure(t *testing.T) {
	ensureFfmpeg(t)

	out := filepath.Join(t.TempDir(), "frame.jpg")
	err := ExtractFrame(t.Context(), "testdata/does-not-exist.mp4", 1.0, out)
	if err == nil {
		t.Fatal("expected error for missing video path, got nil")
	}

	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("expected output file to be cleaned up after error, but it exists")
	}
}

func TestExtractFrame_ContextCancellation(t *testing.T) {
	ensureFfmpeg(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	out := filepath.Join(t.TempDir(), "frame.jpg")
	err := ExtractFrame(ctx, "testdata/tiny.mp4", 2.5, out)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}
