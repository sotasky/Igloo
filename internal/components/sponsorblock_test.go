package components

import "testing"

func TestPrefsDataSBValFallsBackToCategoryDefaults(t *testing.T) {
	prefs := PrefsData{}

	cases := map[string]string{
		"sponsor":        "silent",
		"selfpromo":      "silent",
		"interaction":    "silent",
		"intro":          "ask",
		"outro":          "ask",
		"preview":        "ask",
		"filler":         "ask",
		"music_offtopic": "ask",
	}

	for cat, want := range cases {
		if got := prefs.SBVal(cat); got != want {
			t.Fatalf("SBVal(%q) = %q, want %q", cat, got, want)
		}
	}
}
