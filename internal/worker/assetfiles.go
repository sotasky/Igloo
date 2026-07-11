package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func extractFirstFrame(ctx context.Context, videoPath, outPath string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", videoPath,
		"-frames:v", "1",
		"-vf", "scale='min(480,iw)':-2",
		"-q:v", "5",
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
