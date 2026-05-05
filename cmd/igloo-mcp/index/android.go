package index

import (
	"regexp"
	"strings"
)

var (
	rKtorCall      = regexp.MustCompile(`client\.(get|post|delete|put|patch)\s*\(\s*"([^"]+)"`)
	rAPIPath       = regexp.MustCompile(`\$baseUrl(/api/[^"$]+)`)
	rDynamic       = regexp.MustCompile(`\$\w+.*$`)
	rEntitySingle  = regexp.MustCompile(`@Entity\s*\(\s*tableName\s*=\s*"([^"]+)"`)
	rEntityOpen    = regexp.MustCompile(`@Entity\s*\(\s*$`)
	rTableName     = regexp.MustCompile(`tableName\s*=\s*"([^"]+)"`)
	rDataClass     = regexp.MustCompile(`data\s+class\s+(\w+)`)
	rDaoQuery1     = regexp.MustCompile(`@Query\s*\(\s*"([^"]+)"\s*\)`)
	rDaoQueryOpen  = regexp.MustCompile(`@Query\s*\(\s*"""`)
	rDaoQueryBare  = regexp.MustCompile(`@Query\s*\(\s*$`)
	rDaoWrite      = regexp.MustCompile(`@(Upsert|Insert|Delete)\b`)
	rNavRoute      = regexp.MustCompile(`composable\s*\(\s*(?:route\s*=\s*)?"([^"]+)"`)
	rVMRef         = regexp.MustCompile(`(?:koinViewModel|getViewModel)\s*<\s*(\w+)\s*>`)
	rVMParam       = regexp.MustCompile(`:\s*(\w+ViewModel)\s*=\s*(?:koinViewModel|getViewModel)\s*\(`)
	rIglooImport   = regexp.MustCompile(`(?m)^import\s+com\.screwy\.igloo\.(.+)`)
	rClassDecl     = regexp.MustCompile(`(?:class|interface|object)\s+(\w+)`)
	rKtFun         = regexp.MustCompile(`(?m)^[ \t]*(?:(?:private|internal|public|protected|override|suspend|inline|open|abstract|data|sealed|enum|companion|lateinit|const|external|actual|expect)\s+)*fun\s+(\w+)\s*\(([^)]*)\)\s*(?::\s*([^\s{=\n]+))?`)
	rKtProp        = regexp.MustCompile(`(?m)^[ \t]*(?:(?:private|internal|public|protected|override|suspend|inline|open|abstract|data|sealed|enum|companion|lateinit|const|external|actual|expect)\s+)*(?:val|var)\s+([a-zA-Z]\w*)\s*(?::\s*([^\s=\n]+))?`)
	rSQLTable      = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE|INSERT\s+INTO|DELETE\s+FROM)\s+(\w+)`)
	rKtConstructor = regexp.MustCompile(`(?ms)class\s+(\w+)\s*\(([^)]+)\)`)
	rKtCtorParam   = regexp.MustCompile(`(?:(?:private|protected|internal|public)\s+)?(?:(?:val|var)\s+)?(\w+)\s*:\s*(\w+)`)
	rDbDaoCall     = regexp.MustCompile(`\bdb\.(\w+Dao)\s*\(\s*\)`)
	rDbAbstractDao = regexp.MustCompile(`(?m)^\s*abstract\s+fun\s+(\w+)\s*\(\s*\)\s*:\s*(\w+)`)
)

var ktSQLSkip = map[string]bool{
	"set": true, "values": true, "select": true, "where": true, "order": true,
	"group": true, "limit": true, "offset": true, "having": true, "join": true,
	"inner": true, "outer": true, "left": true, "right": true, "cross": true,
	"on": true, "and": true, "or": true, "not": true, "as": true, "by": true,
	"asc": true, "desc": true, "null": true, "true": true, "false": true,
	"case": true, "when": true, "then": true, "else": true, "end": true,
	"in": true, "exists": true, "between": true, "like": true, "distinct": true,
	"count": true, "sum": true, "avg": true, "min": true, "max": true, "all": true,
}

// DebugEvent is a StatsLogger.log event extracted from Kotlin source.
type DebugEvent struct {
	Name   string
	Fields []DebugField
	File   string
	Line   int
}

// DebugField is a key-value pair in a StatsLogger.log call.
type DebugField struct {
	Key  string
	Expr string
}

var (
	rStatsLogStart = regexp.MustCompile(`StatsLogger\.log\(`)
	rEventName     = regexp.MustCompile(`"([^"]+)"`)
	rPairKey       = regexp.MustCompile(`"(\w+)"\s+to\s+`)
)

// ScanDebugEvents extracts all StatsLogger.log calls from Kotlin source.
func ScanDebugEvents(source, fp string) []DebugEvent {
	var events []DebugEvent
	lines := strings.Split(source, "\n")

	for i := 0; i < len(lines); i++ {
		if !rStatsLogStart.MatchString(lines[i]) {
			continue
		}
		startLine := i + 1

		// Collect all lines of this call until balanced parens
		var blockLines []string
		depth := 0
		for j := i; j < len(lines); j++ {
			blockLines = append(blockLines, lines[j])
			for _, ch := range lines[j] {
				if ch == '(' {
					depth++
				} else if ch == ')' {
					depth--
				}
			}
			if depth <= 0 {
				break
			}
		}
		block := strings.Join(blockLines, " ")

		// Extract event name: first quoted string after StatsLogger.log(
		idx := strings.Index(block, "StatsLogger.log(")
		if idx < 0 {
			continue
		}
		rest := block[idx+len("StatsLogger.log("):]
		nameMatch := rEventName.FindStringSubmatch(rest)
		if nameMatch == nil {
			continue
		}
		ev := DebugEvent{Name: nameMatch[1], File: fp, Line: startLine}

		for _, pm := range rPairKey.FindAllStringSubmatch(block, -1) {
			ev.Fields = append(ev.Fields, DebugField{Key: pm[1]})
		}
		events = append(events, ev)
	}
	return events
}

// KotlinAPICall is an HTTP call extracted from Kotlin source.
type KotlinAPICall struct {
	Path   string
	Method string
	Line   int
}

// KotlinEntity is a Room @Entity.
type KotlinEntity struct {
	TableName string
	ClassName string
}

// KotlinDAOTable is a table reference extracted from a @Query annotation.
type KotlinDAOTable struct {
	Table   string
	Mode    string // "read" or "write"
	Line    int
	Context string
}

// ConstructorDep represents a constructor parameter dependency.
type ConstructorDep struct {
	Name string
	Type string
}

// KotlinScanResult is the full output of ScanKotlin.
type KotlinScanResult struct {
	APICalls        []KotlinAPICall
	Entities        []KotlinEntity
	DAOTables       []KotlinDAOTable
	ViewModelRefs   []string
	NavRoutes       []string
	Imports         []string
	ClassName       string
	Symbols         []Symbol
	ConstructorDeps []ConstructorDep
}

// ScanKotlin parses a Kotlin source file for Android-relevant metadata and symbols.
func ScanKotlin(source, fp string) KotlinScanResult {
	var result KotlinScanResult
	lines := strings.Split(source, "\n")

	seenVMs := map[string]bool{}
	seenDAOTables := map[string]bool{}

	var pendingEntityTable string
	inEntityBlock := false
	entityBlockDepth := 0
	inQueryBlock := false
	var queryBuffer []string
	queryStartLine := 0
	// inQueryPending: we saw `@Query(` bare on a prior line and are
	// waiting for the `"""` opener on a subsequent line. This is the
	// dominant formatting style in Igloo's Kotlin DAOs.
	inQueryPending := false
	queryPendingLine := 0

	for i, line := range lines {
		lineno := i + 1
		stripped := strings.TrimSpace(line)

		// Imports
		if m := rIglooImport.FindStringSubmatch(stripped); m != nil {
			result.Imports = append(result.Imports, m[1])
			continue
		}

		// Class name (first class/interface/object)
		if result.ClassName == "" && !strings.HasPrefix(stripped, "//") {
			if m := rClassDecl.FindStringSubmatch(line); m != nil {
				result.ClassName = m[1]
			}
		}

		// Multiline @Query accumulation
		if inQueryBlock {
			queryBuffer = append(queryBuffer, line)
			if strings.Contains(line, `"""`) && len(queryBuffer) > 1 {
				fullSQL := strings.Join(queryBuffer, " ")
				result.DAOTables = append(result.DAOTables, extractDAOTables(fullSQL, queryStartLine, seenDAOTables)...)
				inQueryBlock = false
				queryBuffer = nil
			}
			continue
		}

		// Pending: saw `@Query(` on a prior line; look for the `"""` opener.
		if inQueryPending {
			if strings.Contains(line, `"""`) {
				// Single-line triple-quoted body: """SELECT...""" on this line.
				if _, after, found := strings.Cut(line, `"""`); found && strings.Contains(after, `"""`) {
					result.DAOTables = append(result.DAOTables, extractDAOTables(line, queryPendingLine, seenDAOTables)...)
				} else {
					inQueryBlock = true
					queryBuffer = []string{line}
					queryStartLine = queryPendingLine
				}
				inQueryPending = false
				continue
			}
			if stripped == "" {
				continue
			}
			// Non-"""/non-blank after @Query(: not a triple-quoted query
			// (could be a constant reference). Abandon pending state and
			// fall through to normal line processing.
			inQueryPending = false
		}

		// Multiline @Entity block
		if inEntityBlock {
			if m := rTableName.FindStringSubmatch(line); m != nil {
				pendingEntityTable = m[1]
			}
			entityBlockDepth += strings.Count(line, "(") - strings.Count(line, ")")
			if entityBlockDepth <= 0 {
				inEntityBlock = false
			}
			continue
		}

		// Ktor API calls
		for _, m := range rKtorCall.FindAllStringSubmatch(line, -1) {
			method := strings.ToUpper(m[1])
			urlStr := m[2]
			if pm := rAPIPath.FindStringSubmatch(urlStr); pm != nil {
				path := rDynamic.ReplaceAllString(pm[1], "")
				result.APICalls = append(result.APICalls, KotlinAPICall{
					Path: path, Method: method, Line: lineno,
				})
			}
		}

		// @Entity single-line
		if m := rEntitySingle.FindStringSubmatch(line); m != nil {
			pendingEntityTable = m[1]
		} else if rEntityOpen.MatchString(stripped) {
			inEntityBlock = true
			entityBlockDepth = strings.Count(line, "(") - strings.Count(line, ")")
		}

		// data class — pair with pending @Entity; any other class clears the pending table
		if m := rDataClass.FindStringSubmatch(line); m != nil {
			if pendingEntityTable != "" {
				result.Entities = append(result.Entities, KotlinEntity{
					TableName: pendingEntityTable,
					ClassName: m[1],
				})
				pendingEntityTable = ""
			}
		} else if rClassDecl.MatchString(line) {
			pendingEntityTable = ""
		}

		// @Query single-line
		if m := rDaoQuery1.FindStringSubmatch(line); m != nil {
			result.DAOTables = append(result.DAOTables, extractDAOTables(m[1], lineno, seenDAOTables)...)
		} else if rDaoQueryOpen.MatchString(line) {
			// Detect single-line triple-quoted: @Query("""SELECT...""") — both """ on same line
			if _, after, found := strings.Cut(line, `"""`); found && strings.Contains(after, `"""`) {
				result.DAOTables = append(result.DAOTables, extractDAOTables(line, lineno, seenDAOTables)...)
			} else {
				inQueryBlock = true
				queryBuffer = []string{line}
				queryStartLine = lineno
			}
		} else if rDaoQueryBare.MatchString(stripped) {
			// `@Query(` bare on a line; body (""" or constant) is on a following line.
			inQueryPending = true
			queryPendingLine = lineno
		}

		// @Upsert/@Insert/@Delete
		if rDaoWrite.MatchString(line) {
			result.DAOTables = append(result.DAOTables, KotlinDAOTable{
				Table: "__upsert__", Mode: "write", Line: lineno,
			})
		}

		// Navigation routes
		for _, m := range rNavRoute.FindAllStringSubmatch(line, -1) {
			result.NavRoutes = append(result.NavRoutes, m[1])
		}

		// ViewModel refs
		for _, m := range rVMRef.FindAllStringSubmatch(line, -1) {
			if !seenVMs[m[1]] {
				seenVMs[m[1]] = true
				result.ViewModelRefs = append(result.ViewModelRefs, m[1])
			}
		}
		for _, m := range rVMParam.FindAllStringSubmatch(line, -1) {
			if !seenVMs[m[1]] {
				seenVMs[m[1]] = true
				result.ViewModelRefs = append(result.ViewModelRefs, m[1])
			}
		}
	}

	// Constructor parameters — find the class matching ClassName (or first if no ClassName)
	for _, m := range rKtConstructor.FindAllStringSubmatch(source, -1) {
		if result.ClassName != "" && m[1] != result.ClassName {
			continue
		}
		for _, pm := range rKtCtorParam.FindAllStringSubmatch(m[2], -1) {
			result.ConstructorDeps = append(result.ConstructorDeps, ConstructorDep{
				Name: pm[1], Type: pm[2],
			})
		}
		break
	}

	// Symbol extraction
	lineOf := func(pos int) int { return strings.Count(source[:pos], "\n") + 1 }
	for _, m := range rKtFun.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		args := ""
		if m[4] >= 0 {
			args = strings.TrimSpace(source[m[4]:m[5]])
		}
		result.Symbols = append(result.Symbols, Symbol{
			Name:      name,
			Kind:      "function",
			File:      fp,
			Line:      lineOf(m[0]),
			Language:  "kotlin",
			Signature: "fun " + name + "(" + args + ")",
		})
	}
	seenProps := map[string]bool{}
	for _, m := range rKtProp.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		if !seenProps[name] {
			seenProps[name] = true
			result.Symbols = append(result.Symbols, Symbol{
				Name: name, Kind: "property", File: fp,
				Line: lineOf(m[0]), Language: "kotlin",
			})
		}
	}

	return result
}

func extractDAOTables(sql string, lineno int, seen map[string]bool) []KotlinDAOTable {
	var out []KotlinDAOTable
	isWrite := func(s string) bool {
		u := strings.ToUpper(strings.TrimSpace(s))
		return strings.HasPrefix(u, "UPDATE") || strings.HasPrefix(u, "INSERT") || strings.HasPrefix(u, "DELETE")
	}
	write := isWrite(sql)
	for _, m := range rSQLTable.FindAllStringSubmatch(sql, -1) {
		tbl := strings.ToLower(m[1])
		if ktSQLSkip[tbl] {
			continue
		}
		mode := "read"
		if write {
			mode = "write"
		}
		key := tbl + ":" + mode
		if seen[key] {
			continue
		}
		seen[key] = true
		ctx := strings.Join(strings.Fields(sql), " ")
		if len(ctx) > 60 {
			ctx = ctx[:60]
		}
		out = append(out, KotlinDAOTable{
			Table: tbl, Mode: mode, Line: lineno, Context: ctx,
		})
	}
	return out
}
