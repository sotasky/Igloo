package worker

import (
	"context"
	"fmt"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"strconv"
)

func extractRepresentativeFrame(ctx context.Context, videoPath, outPath string) error {
	duration, _ := probePreviewDuration(ctx, videoPath)
	var timestamps []float64
	if duration > 0 {
		if duration <= 1 {
			timestamps = []float64{duration / 2, 0}
		} else {
			timestamps = []float64{math.Max(0.5, duration*0.1), duration * 0.35, duration * 0.65, 0}
		}
	} else {
		timestamps = []float64{1, 3, 0}
	}

	var lastErr error
	for _, timestamp := range timestamps {
		args := []string{"-y", "-nostdin", "-hide_banner", "-loglevel", "error"}
		if timestamp > 0 {
			args = append(args, "-ss", strconv.FormatFloat(timestamp, 'f', 3, 64))
		}
		args = append(args,
			"-i", videoPath,
			"-frames:v", "1",
			"-vf", "scale='min(480,iw)':-2",
			"-q:v", "5",
			outPath,
		)
		out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("%w: %s", err, out)
			_ = os.Remove(outPath)
			continue
		}
		visible, err := thumbnailFrameVisible(outPath)
		if err != nil {
			lastErr = err
			_ = os.Remove(outPath)
			continue
		}
		if visible {
			return nil
		}
		lastErr = fmt.Errorf("frame at %.3fs is blank", timestamp)
		_ = os.Remove(outPath)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no frame candidates")
	}
	return lastErr
}

func thumbnailFrameVisible(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	img, err := jpeg.Decode(file)
	if err != nil {
		return false, err
	}
	bounds := img.Bounds()
	var luminance, bright, samples uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 2 {
			r, g, b, _ := img.At(x, y).RGBA()
			y := (299*(r>>8) + 587*(g>>8) + 114*(b>>8)) / 1000
			luminance += uint64(y)
			if y >= 12 {
				bright++
			}
			samples++
		}
	}
	if samples == 0 {
		return false, nil
	}
	return luminance/samples >= 2 || bright*1000 >= samples, nil
}
