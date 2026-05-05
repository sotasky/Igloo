package download

import "testing"

func TestIsInstagramURLRecognizesNativeHostsOnly(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://www.instagram.com/reel/ABC123/", true},
		{"https://instagram.com/p/DEF456/", true},
		{"https://vxinstagram.com/reel/ABC123/", false},
		{"https://www.vxinstagram.com/p/DEF456/", false},
		{"https://kkinstagram.com/reel/ABC123/", false},
		{"https://cdn.kkinstagram.com/p/DEF456/", false},
		{"https://example.com/instagram.com/reel/ABC123/", false},
		{"file:///tmp/instagram.mp4", false},
	}

	for _, tt := range tests {
		if got := IsInstagramURL(tt.raw); got != tt.want {
			t.Fatalf("IsInstagramURL(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestCanonicalInstagramURLLeavesMirrorHostsAlone(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{
			raw:  "https://vxinstagram.com/reel/ABC123/?utm_source=x",
			want: "https://vxinstagram.com/reel/ABC123/?utm_source=x",
		},
		{
			raw:  "http://kkinstagram.com/p/DEF456/",
			want: "http://kkinstagram.com/p/DEF456/",
		},
		{
			raw:  "https://example.com/p/DEF456/",
			want: "https://example.com/p/DEF456/",
		},
	}

	for _, tt := range tests {
		if got := canonicalInstagramURL(tt.raw); got != tt.want {
			t.Fatalf("canonicalInstagramURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
