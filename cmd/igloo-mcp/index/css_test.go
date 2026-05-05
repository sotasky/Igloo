package index

import "testing"

func TestScanCSS(t *testing.T) {
	src := `/* Colors */
:root {
  --color-bg: #1a1a2e;
  --color-text: #e0e0e0;
}

.modal { display: none; }
.modal-header { font-size: 1.2em; }
.btn { cursor: pointer; }
`
	syms := ScanCSS(src, "static/css/main.css")

	var vars, classes int
	for _, s := range syms {
		if s.Kind == "property" {
			vars++
		}
		if s.Kind == "class" {
			classes++
		}
	}
	if vars != 2 {
		t.Errorf("expected 2 CSS vars, got %d", vars)
	}
	if classes != 3 {
		t.Errorf("expected 3 CSS classes, got %d", classes)
	}

	// modal-header should have parent "modal"
	for _, s := range syms {
		if s.Name == "modal-header" && s.Parent != "modal" {
			t.Errorf("expected modal-header parent=modal, got %s", s.Parent)
		}
	}
}
