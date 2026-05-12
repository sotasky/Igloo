package language

import "testing"

func TestDisplayNameExpandsCodesAndPreservesLabels(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{in: "ko", want: "Korean"},
		{in: "kr", want: "Kanuri"},
		{in: "Korean", want: "Korean"},
		{in: "ja-JP", want: "Japanese (Japan)"},
		{in: "und", want: ""},
	} {
		if got := DisplayName(tc.in); got != tc.want {
			t.Fatalf("DisplayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLanguageMatchingAcceptsNamesAndCodes(t *testing.T) {
	if !Matches("Korean", "ko") {
		t.Fatal("Korean should match ko")
	}
	if Matches("Korean", "en") {
		t.Fatal("Korean should not match en")
	}
	if Matches("kr", "ko") {
		t.Fatal("kr is Kanuri and should not match Korean")
	}
}

func TestInSetAcceptsNamesAndCodes(t *testing.T) {
	set := map[string]bool{"ko": true}
	if !InSet("Korean", set) {
		t.Fatal("Korean should match ko skip set")
	}
	if InSet("kr", set) {
		t.Fatal("kr should not match ko skip set")
	}
}

func TestIsUnknown(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", true},
		{"und", true},
		{"unknown", true},
		{"qme", true},
		{"qam", true},
		{"qct", true},
		{"qht", true},
		{"qst", true},
		{"zxx", true},
		{"ko", false},
		{"en-US", false},
		{"Korean", false},
	}
	for _, tc := range cases {
		if got := IsUnknown(tc.value); got != tc.want {
			t.Fatalf("IsUnknown(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
