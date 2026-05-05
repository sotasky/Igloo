package main

import "testing"

func TestAndroidStringEscapePositionsMultipleUnindexedFormats(t *testing.T) {
	got := androidStringEscape("%d with media \u00b7 %d text")
	want := "%1$d with media \u00b7 %2$d text"
	if got != want {
		t.Fatalf("androidStringEscape() = %q, want %q", got, want)
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
