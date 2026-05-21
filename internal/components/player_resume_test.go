package components

import (
	"testing"

	"github.com/screwys/igloo/internal/model"
)

func TestResumePositionResetsAtEnd(t *testing.T) {
	video := model.Video{
		Duration:         600,
		PlaybackPosition: 599.2,
	}

	if got := resumePosition(video); got != "0.0" {
		t.Fatalf("resumePosition near end = %q, want 0.0", got)
	}
}

func TestResumePositionKeepsOrdinaryProgress(t *testing.T) {
	video := model.Video{
		Duration:         600,
		PlaybackPosition: 480,
	}

	if got := resumePosition(video); got != "480.0" {
		t.Fatalf("resumePosition mid-video = %q, want 480.0", got)
	}
}
