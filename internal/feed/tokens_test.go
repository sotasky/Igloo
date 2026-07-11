package feed

import "testing"

func TestExtractInterestTokens(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"hashtags", "Check out #golang and #webdev", []string{"#golang", "#webdev"}},
		{"mentions", "Thanks @user_a for the tip", []string{"@user_a"}},
		{"mixed", "#art by @artist_b is great", []string{"#art", "@artist_b"}},
		{"empty", "", nil},
		{"no tokens", "just plain text here", nil},
		{"dedup", "#go #go #go", []string{"#go"}},
		{"lowercase", "#GoLang @UserA", []string{"#golang", "@usera"}},
		{"exclude email and URL", "mail user@sample.test https://x.com/@inside/status/1 @outside", []string{"@outside"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractInterestTokens(tt.text)
			if len(got) != len(tt.want) {
				t.Errorf("ExtractInterestTokens(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
