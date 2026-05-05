package index

import (
	"regexp"
	"strings"
)

var (
	rTemplFunc    = regexp.MustCompile(`(?m)^templ\s+(\w+)\s*\(([^)]*)\)\s*\{`)
	rTemplCSS     = regexp.MustCompile(`(?m)^css\s+(\w+)\s*\(([^)]*)\)\s*\{`)
	rTemplCall    = regexp.MustCompile(`@(\w+)\s*\(`)
	rTemplGoFunc  = regexp.MustCompile(`(?m)^func\s+(\w+)\s*\(([^)]*)\)(?:\s+\S+)?\s*\{`)
	rTemplMethod  = regexp.MustCompile(`(?m)^func\s+\(\s*\w+\s+\*?(\w+)\s*\)\s+(\w+)\s*\(([^)]*)\)`)
	rTemplGoType  = regexp.MustCompile(`(?m)^type\s+(\w+)\s+(struct|interface)\s*\{`)
)

// TemplResult is the output of ScanTempl.
type TemplResult struct {
	Components []TemplComponent // templ Xxx() definitions
	CSSBlocks  []string         // css Xxx() definitions
	Calls      []string         // @Xxx() calls to other components
	Symbols    []Symbol         // Go func/type symbols defined in the file
}

// TemplComponent represents a templ component definition.
type TemplComponent struct {
	Name   string
	Params string
	Line   int
}

// ScanTempl parses a .templ file for component definitions, CSS blocks, and cross-references.
func ScanTempl(source, fp string) TemplResult {
	var result TemplResult
	lineOf := func(pos int) int { return strings.Count(source[:pos], "\n") + 1 }

	// templ components
	for _, m := range rTemplFunc.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		params := strings.TrimSpace(source[m[4]:m[5]])
		result.Components = append(result.Components, TemplComponent{
			Name: name, Params: params, Line: lineOf(m[0]),
		})
	}

	// CSS blocks
	for _, m := range rTemplCSS.FindAllStringSubmatch(source, -1) {
		result.CSSBlocks = append(result.CSSBlocks, m[1])
	}

	// Component calls
	seen := map[string]bool{}
	for _, m := range rTemplCall.FindAllStringSubmatch(source, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			result.Calls = append(result.Calls, m[1])
		}
	}

	// Go symbols (funcs, types defined in templ files)
	for _, m := range rTemplGoType.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		kind := "class"
		if source[m[4]:m[5]] == "interface" {
			kind = "interface"
		}
		result.Symbols = append(result.Symbols, Symbol{
			Name: name, Kind: kind, File: fp, Line: lineOf(m[0]),
			Language: "go", Signature: "type " + name + " " + source[m[4]:m[5]],
		})
	}

	methodPos := map[int]bool{}
	for _, m := range rTemplMethod.FindAllStringSubmatchIndex(source, -1) {
		methodPos[m[0]] = true
		receiver := source[m[2]:m[3]]
		name := source[m[4]:m[5]]
		args := strings.TrimSpace(source[m[6]:m[7]])
		result.Symbols = append(result.Symbols, Symbol{
			Name: name, Kind: "method", File: fp, Line: lineOf(m[0]),
			Language: "go",
			Signature: "func (" + receiver + ") " + name + "(" + args + ")",
			Parent:    receiver,
		})
	}

	for _, m := range rTemplGoFunc.FindAllStringSubmatchIndex(source, -1) {
		if methodPos[m[0]] {
			continue
		}
		name := source[m[2]:m[3]]
		args := strings.TrimSpace(source[m[4]:m[5]])
		result.Symbols = append(result.Symbols, Symbol{
			Name: name, Kind: "function", File: fp, Line: lineOf(m[0]),
			Language: "go", Signature: "func " + name + "(" + args + ")",
		})
	}

	return result
}
