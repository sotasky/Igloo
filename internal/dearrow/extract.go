package dearrow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// ExtractFrame captures one JPEG frame from videoPath at the given timestamp
// (seconds) and writes it to outPath. Caller must ensure the output directory
// exists. Cleans up a partial output file on error.
func ExtractFrame(ctx context.Context, videoPath string, timestamp float64, outPath string) error {
	// Place -ss BEFORE -i for keyframe-aligned fast seek. Acceptable for a
	// thumbnail — ffmpeg will pick the nearest keyframe, which is typically
	// within a second or two of the requested point. Output is JPEG at
	// quality 5 (lower = better; 5 is a good balance) and scaled to 480px
	// wide max to keep file size down.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-ss", strconv.FormatFloat(timestamp, 'f', 3, 64),
		"-i", videoPath,
		"-frames:v", "1",
		"-vf", "scale='min(480,iw)':-2",
		"-q:v", "5",
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("ffmpeg: %w (%s)", err, out)
	}
	return nil
}
