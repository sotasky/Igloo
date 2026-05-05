package index

import (
	"regexp"
	"strings"
)

var (
	rJSApiJson  = regexp.MustCompile(`apiJson\s*\(\s*['"]([^'"]+)['"]`)
	rJSApiFetch = regexp.MustCompile(`apiFetch\s*\(\s*['"]([^'"]+)['"]`)
	rJSFetch    = regexp.MustCompile(`fetch\s*\(\s*['"]([^'"]*\/api\/[^'"]+)['"]`)
	rJSApi      = regexp.MustCompile(`(?:^|[^\w])api\s*\(\s*['"]([^'"]*\/api\/[^'"]+)['"]`)
	rJSMethod   = regexp.MustCompile(`method\s*:\s*['"](\w+)['"]`)

	rJSFunc       = regexp.MustCompile(`(?m)^(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s+(\w+)\s*\(`)
	rJSClass      = regexp.MustCompile(`(?m)^(?:export\s+(?:default\s+)?)?class\s+(\w+)`)
	rJSConstArrow = regexp.MustCompile(`(?m)^(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s+)?\(`)
)

// JSCall represents an API call found in a JS file.
type JSCall struct {
	URL    string
	Method string
	Line   int
}

// ScanJSSymbols extracts function, class, and arrow function definitions from JS source.
func ScanJSSymbols(source, filepath string) []Symbol {
	var syms []Symbol
	lineOf := func(pos int) int { return strings.Count(source[:pos], "\n") + 1 }

	for _, m := range rJSFunc.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		syms = append(syms, Symbol{
			Name: name, Kind: "function", File: filepath,
			Line: lineOf(m[0]), Language: "js",
			Signature: "function " + name + "()",
		})
	}
	for _, m := range rJSClass.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		syms = append(syms, Symbol{
			Name: name, Kind: "class", File: filepath,
			Line: lineOf(m[0]), Language: "js",
			Signature: "class " + name,
		})
	}
	for _, m := range rJSConstArrow.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		syms = append(syms, Symbol{
			Name: name, Kind: "function", File: filepath,
			Line: lineOf(m[0]), Language: "js",
			Signature: "const " + name + " = () =>",
		})
	}
	return syms
}

// ScanJS parses a JS file for API call patterns (apiJson, apiFetch, fetch, api).
func ScanJS(source, filepath string) []JSCall {
	var calls []JSCall
	lines := strings.Split(source, "\n")

	extractMethod := func(line string, nextLine string) string {
		for _, s := range []string{line, nextLine} {
			if m := rJSMethod.FindStringSubmatch(s); m != nil {
				return strings.ToUpper(m[1])
			}
		}
		return "GET"
	}

	for i, line := range lines {
		lineno := i + 1
		nextLine := ""
		if lineno < len(lines) {
			nextLine = lines[lineno]
		}
		method := extractMethod(line, nextLine)
		for _, re := range []*regexp.Regexp{rJSApiJson, rJSApiFetch, rJSFetch, rJSApi} {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				calls = append(calls, JSCall{URL: m[1], Method: method, Line: lineno})
			}
		}
	}
	_ = filepath
	return calls
}
