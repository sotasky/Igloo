package index

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	rCSSVar     = regexp.MustCompile(`(--[\w-]+)\s*:\s*([^;]+);`)
	rCSSClass   = regexp.MustCompile(`^\s*\.([\w-]+)`)
	rCSSComment = regexp.MustCompile(`/\*\s*(.+?)\s*\*/`)
)

// ScanCSS extracts CSS custom properties (theme vars) and class selectors as Symbols.
func ScanCSS(source, fp string) []Symbol {
	if strings.TrimSpace(source) == "" {
		return nil
	}

	stem := strings.TrimSuffix(filepath.Base(fp), filepath.Ext(fp))
	lines := strings.Split(source, "\n")
	var syms []Symbol
	seenClasses := map[string]bool{}
	currentSection := ""
	inRoot := false

	for i, line := range lines {
		lineno := i + 1
		stripped := strings.TrimSpace(line)

		if strings.Contains(stripped, ":root") {
			inRoot = true
		}
		if inRoot && strings.Contains(stripped, "}") && !strings.Contains(stripped, "--") {
			inRoot = false
		}

		if m := rCSSComment.FindStringSubmatch(stripped); m != nil && !strings.HasPrefix(stripped, ".") {
			currentSection = strings.TrimSpace(m[1])
		}

		// CSS variables
		for _, m := range rCSSVar.FindAllStringSubmatch(stripped, -1) {
			varName := m[1]
			varVal := strings.TrimSpace(m[2])
			parent := ""
			if inRoot {
				parent = ":root"
			}
			syms = append(syms, Symbol{
				Name:      varName,
				Kind:      "property",
				File:      fp,
				Line:      lineno,
				Language:  "css",
				Signature: varName + ": " + varVal,
				Parent:    parent,
			})
			_ = stem
			_ = currentSection
		}

		// Class selectors
		if m := rCSSClass.FindStringSubmatch(stripped); m != nil {
			cls := m[1]
			if !seenClasses[cls] {
				seenClasses[cls] = true
				base, _, _ := strings.Cut(cls, "-")
				parent := ""
				if base != cls {
					parent = base
				}
				syms = append(syms, Symbol{
					Name:      cls,
					Kind:      "class",
					File:      fp,
					Line:      lineno,
					Language:  "css",
					Signature: "." + cls,
					Parent:    parent,
				})
			}
		}
	}
	return syms
}
