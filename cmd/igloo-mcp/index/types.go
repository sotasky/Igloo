package index

// Endpoint represents an HTTP handler route.
type Endpoint struct {
	Method      string
	Path        string
	HandlerFile string
	HandlerFunc string
	HandlerLine int
	Area        string
	Kind        string // "api" or "page"
}

// Key returns the unique map key for this endpoint.
func (e *Endpoint) Key() string { return e.Method + " " + e.Path }

// FileNode represents a file in the dependency graph.
type FileNode struct {
	Path       string
	Layer      string // "server", "template", "js", "android", "css"
	Endpoints  []string
	Exports    []string
	Imports    []string
	ImportedBy []string
	DBTables   []TableRef
}

// TableRef represents a SQL table access in a file.
type TableRef struct {
	Table   string
	File    string
	Line    int
	Mode    string // "read", "write", "read_write"
	Context string
}

// Symbol represents a named code symbol (function, class, property, etc.).
type Symbol struct {
	Name      string
	Kind      string // "function", "method", "class", "interface", "property", "constant", "variable"
	File      string
	Line      int
	Language  string // "go", "kotlin", "js", "css"
	Signature string
	Parent    string // receiver type for methods, component base for CSS classes
}

// Reference represents a call site or usage of a symbol.
type Reference struct {
	SymbolName   string
	QualifiedRef string
	File         string
	Line         int
	Context      string
}

// Dependency represents a directed edge in the file graph.
type Dependency struct {
	Source string
	Target string
	Kind   string // "template_include", "api_call", "import"
}

// APICaller records a JS/Android call to an API path.
type APICaller struct {
	File   string
	Method string
	Line   int
	Source string // "js" or "android"
}

// JSFileInfo holds the structured overview of a JS file.
type JSFileInfo struct {
	Path        string
	Description string
	Symbols     []Symbol
	APICalls    []JSCall
	Pages       []string // handler functions / endpoint paths that load this JS
}

// AndroidScreenInfo describes an Android screen and its dependency chain.
type AndroidScreenInfo struct {
	Name      string
	File      string
	NavRoute  string
	ViewModel string
	LineCount int
}

// AndroidVMInfo describes a ViewModel and its dependencies.
type AndroidVMInfo struct {
	Name         string
	File         string
	Dependencies []string
	DirectDAOs   []string
	Screens      []string
}

// AndroidRepoInfo describes a Repository and its dependencies.
type AndroidRepoInfo struct {
	Name     string
	File     string
	DAOs     []string
	APICalls []KotlinAPICall
}

// AndroidDAOInfo describes a DAO.
type AndroidDAOInfo struct {
	Name       string
	File       string
	Tables     []string
	ReadCount  int
	WriteCount int
}

// AndroidEntityInfo describes a Room entity.
type AndroidEntityInfo struct {
	ClassName string
	TableName string
	File      string
}

// AndroidClassInfo describes a non-UI Android class so trace_screen can follow
// the rewritten app's direct service/data dependencies instead of assuming a
// repository-only layer.
type AndroidClassInfo struct {
	Name         string
	File         string
	Kind         string
	Dependencies []string
	DirectDAOs   []string
	APICalls     []KotlinAPICall
}

// TemplComponentInfo represents an indexed templ component.
type TemplComponentInfo struct {
	Name     string
	Params   string
	File     string
	Line     int
	CalledBy []string // files that call this component
	Calls    []string // components this one calls
}
