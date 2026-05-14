package lifecycleaudit

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	igloodb "github.com/screwys/igloo/internal/db"
)

const unclassifiedLifecycle = "unclassified"

var (
	deleteFromRE = regexp.MustCompile(`(?is)\bDELETE\s+FROM\s+((?:[A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*)`)
	dropTableRE  = regexp.MustCompile(`(?is)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?((?:[A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*)`)
)

type Options struct {
	Root         string `json:"root"`
	JSON         bool   `json:"-"`
	Strict       bool   `json:"strict"`
	IncludeTests bool   `json:"include_tests"`
}

type Report struct {
	Root       string         `json:"root"`
	Operations []Operation    `json:"operations"`
	Warnings   []Warning      `json:"warnings,omitempty"`
	Summary    map[string]int `json:"summary"`
}

type Operation struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Op        string `json:"op"`
	Table     string `json:"table"`
	Lifecycle string `json:"lifecycle"`
	SQL       string `json:"sql"`
}

type Warning struct {
	Code    string `json:"code"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Table   string `json:"table"`
	Message string `json:"message"`
}

type detectedOperation struct {
	op    string
	table string
	start int
}

type parsedOptions struct {
	Options
}

func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "lifecycle audit: %v\n", err)
		return 2
	}

	report, err := ReadReport(opts.Options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "lifecycle audit: %v\n", err)
		return 1
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			_, _ = fmt.Fprintf(stderr, "lifecycle audit: encode JSON: %v\n", err)
			return 1
		}
	} else {
		_, _ = fmt.Fprint(stdout, formatText(report))
	}
	if opts.Strict && len(report.Warnings) > 0 {
		return 1
	}
	return 0
}

func parseOptions(args []string) (parsedOptions, error) {
	opts := Options{Root: "."}
	fs := flag.NewFlagSet("lifecycle-audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.Root, "root", opts.Root, "source root to scan")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON output")
	fs.BoolVar(&opts.Strict, "strict", false, "fail when destructive SQL targets an unclassified table")
	fs.BoolVar(&opts.IncludeTests, "include-tests", false, "include Go test files")
	if err := fs.Parse(args); err != nil {
		return parsedOptions{}, err
	}
	if fs.NArg() != 0 {
		return parsedOptions{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	return parsedOptions{Options: opts}, nil
}

func ReadReport(opts Options) (Report, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root = "."
	}
	root = filepath.Clean(root)

	report := Report{
		Root:    root,
		Summary: make(map[string]int),
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if shouldSkipDir(entry.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		if !opts.IncludeTests && strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		ops, err := readFileOperations(fset, root, path)
		if err != nil {
			return err
		}
		for _, op := range ops {
			report.Operations = append(report.Operations, op)
			report.Summary[op.Lifecycle]++
			if op.Lifecycle == unclassifiedLifecycle {
				report.Warnings = append(report.Warnings, Warning{
					Code:    "unclassified_destructive_sql",
					File:    op.File,
					Line:    op.Line,
					Table:   op.Table,
					Message: "destructive SQL targets a table without lifecycle classification",
				})
			}
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	sort.Slice(report.Operations, func(i, j int) bool {
		if report.Operations[i].File != report.Operations[j].File {
			return report.Operations[i].File < report.Operations[j].File
		}
		if report.Operations[i].Line != report.Operations[j].Line {
			return report.Operations[i].Line < report.Operations[j].Line
		}
		if report.Operations[i].Op != report.Operations[j].Op {
			return report.Operations[i].Op < report.Operations[j].Op
		}
		return report.Operations[i].Table < report.Operations[j].Table
	})
	sort.Slice(report.Warnings, func(i, j int) bool {
		if report.Warnings[i].File != report.Warnings[j].File {
			return report.Warnings[i].File < report.Warnings[j].File
		}
		if report.Warnings[i].Line != report.Warnings[j].Line {
			return report.Warnings[i].Line < report.Warnings[j].Line
		}
		return report.Warnings[i].Table < report.Warnings[j].Table
	})
	return report, nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".agents", ".git", ".gradle", "build", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func readFileOperations(fset *token.FileSet, root, path string) ([]Operation, error) {
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	var operations []Operation
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		sqlText, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, detected := range detectDestructiveOperations(sqlText) {
			lifecycle, ok := classifyTable(detected.table)
			if !ok {
				lifecycle = unclassifiedLifecycle
			}
			operations = append(operations, Operation{
				File:      relPath,
				Line:      fset.Position(lit.Pos()).Line + strings.Count(sqlText[:detected.start], "\n"),
				Op:        detected.op,
				Table:     detected.table,
				Lifecycle: lifecycle,
				SQL:       sqlPreview(sqlText),
			})
		}
		return true
	})
	return operations, nil
}

func detectDestructiveOperations(sqlText string) []detectedOperation {
	var out []detectedOperation
	for _, match := range deleteFromRE.FindAllStringSubmatchIndex(sqlText, -1) {
		out = append(out, detectedOperation{
			op:    "DELETE",
			table: normalizeTable(sqlText[match[2]:match[3]]),
			start: match[0],
		})
	}
	for _, match := range dropTableRE.FindAllStringSubmatchIndex(sqlText, -1) {
		out = append(out, detectedOperation{
			op:    "DROP_TABLE",
			table: normalizeTable(sqlText[match[2]:match[3]]),
			start: match[0],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].start < out[j].start
	})
	return out
}

func normalizeTable(table string) string {
	return strings.Trim(table, "`\"[] ")
}

func classifyTable(table string) (string, bool) {
	if strings.HasPrefix(table, "temp.") {
		return "temporary", true
	}
	base := table
	if before, after, ok := strings.Cut(table, "."); ok {
		if before == "main" {
			base = after
		}
	}
	lifecycle, ok := igloodb.SchemaTableLifecycle(base)
	if ok {
		return lifecycle, true
	}
	lifecycle, ok = retiredTableLifecycle(base)
	if ok {
		return lifecycle, true
	}
	return "", false
}

func retiredTableLifecycle(table string) (string, bool) {
	switch table {
	case "channel_avatars", "reading_articles_cache", "reading_preferences", "saved_articles", "twitter_profiles":
		return "legacy_migration", true
	default:
		return "", false
	}
}

func sqlPreview(sqlText string) string {
	preview := strings.Join(strings.Fields(sqlText), " ")
	const maxPreview = 160
	if len(preview) <= maxPreview {
		return preview
	}
	return preview[:maxPreview-3] + "..."
}

func formatText(report Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "root: %s\n", report.Root)
	fmt.Fprintf(&b, "operations: %d\n", len(report.Operations))
	if len(report.Summary) > 0 {
		b.WriteString("summary:\n")
		for _, lifecycle := range orderedLifecycles(report.Summary) {
			fmt.Fprintf(&b, "  %-18s %d\n", lifecycle+":", report.Summary[lifecycle])
		}
	}
	if len(report.Warnings) > 0 {
		b.WriteString("warnings:\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "  - %s %s:%d table=%s: %s\n", warning.Code, warning.File, warning.Line, warning.Table, warning.Message)
		}
	}
	if len(report.Operations) > 0 {
		b.WriteString("destructive_sql:\n")
		for _, op := range report.Operations {
			fmt.Fprintf(&b, "  - %s:%d %-10s %-18s table=%s sql=%q\n", op.File, op.Line, op.Op, op.Lifecycle, op.Table, op.SQL)
		}
	}
	return b.String()
}

func orderedLifecycles(summary map[string]int) []string {
	preferred := []string{
		"archive",
		"maintained_state",
		"user_state",
		"queue",
		"derived_cache",
		"diagnostic",
		"security_state",
		"legacy_migration",
		"temporary",
		unclassifiedLifecycle,
	}
	rank := make(map[string]int, len(preferred))
	for index, lifecycle := range preferred {
		rank[lifecycle] = index
	}
	var out []string
	for _, lifecycle := range preferred {
		if _, ok := summary[lifecycle]; ok {
			out = append(out, lifecycle)
		}
	}
	for lifecycle := range summary {
		if _, ok := rank[lifecycle]; !ok {
			out = append(out, lifecycle)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		leftRank, leftKnown := rank[out[i]]
		rightRank, rightKnown := rank[out[j]]
		if leftKnown && rightKnown {
			return leftRank < rightRank
		}
		if leftKnown != rightKnown {
			return leftKnown
		}
		return out[i] < out[j]
	})
	return out
}
