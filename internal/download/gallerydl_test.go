package download

import (
	"bytes"
	"errors"
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
