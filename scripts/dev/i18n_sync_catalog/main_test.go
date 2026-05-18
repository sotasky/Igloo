package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedCatalogOutputsAreCurrent(t *testing.T) {
	chdirRepoRoot(t)

	outputs, err := generateCatalogOutputs()
	if err != nil {
		t.Fatalf("generateCatalogOutputs: %v", err)
	}
	for _, output := range outputs {
		got, err := os.ReadFile(output.Path)
		if err != nil {
			t.Errorf("%s: %v", output.Path, err)
			continue
		}
		if strings.HasSuffix(output.Path, ".xml") {
			assertWellFormedXML(t, output.Path, got)
		}
		if !bytes.Equal(got, output.Data) {
			t.Errorf("%s is out of date; run go run ./scripts/dev/i18n_sync_catalog", output.Path)
		}
	}
}

func TestAndroidStringEscapePositionsMultipleUnindexedFormats(t *testing.T) {
	got := androidStringEscape("%d with media \u00b7 %d text")
	want := "%1$d with media \u00b7 %2$d text"
	if got != want {
		t.Fatalf("androidStringEscape() = %q, want %q", got, want)
	}
}

func chdirRepoRoot(t *testing.T) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(cwd, "../../.."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func assertWellFormedXML(t *testing.T, path string, data []byte) {
	t.Helper()
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := decoder.Token(); err != nil {
			if err == io.EOF {
				return
			}
			t.Fatalf("%s is not well-formed XML: %v", path, err)
		}
	}
}

func TestAndroidStringEscapeLeavesSingleAndIndexedFormats(t *testing.T) {
	tests := []string{
		"Open media %d",
		"Pick # to download (%1$d/%2$d)",
		"Volume %1$d%%",
	}
	for _, input := range tests {
		if got := androidStringEscape(input); got != input {
			t.Fatalf("androidStringEscape(%q) = %q, want unchanged", input, got)
		}
	}
}

func TestAndroidStringEscapeConvertsGoQuoteFormats(t *testing.T) {
	tests := map[string]string{
		"Nothing found for %q": "Nothing found for %1$s",
		"Delete %1$q?":         "Delete %1$s?",
		"Move %q into %d rows": "Move %1$s into %2$d rows",
	}
	for input, want := range tests {
		if got := androidStringEscape(input); got != want {
			t.Fatalf("androidStringEscape(%q) = %q, want %q", input, got, want)
		}
	}
}
