package analysis

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// The std modules a name a file does not bind could come from, for the
// help that names the import to write ("import `std::http.Method`"). A
// process builds only the std modules its programs reach, so the answer is
// what every std module exports (StdModuleExport), which package std
// installs (SetStdExports): read from the std artifact it builds the
// stdlib from, which records each module's when it extracts the module's
// interface, or else from std's sources, each parsed once per process. It
// is read only when a diagnostic asks, and nothing is checked. Internal
// modules (internalStdModules) are never suggested, and neither is a
// module with no public declarations.

var (
	stdExportsMu   sync.Mutex
	stdExportsFunc func() map[string]StdModuleExport
	stdIndexOnce   *sync.Once
	stdIndex       *stdExportIndex
)

// stdExportIndex is, for each public std type name, the modules that
// export a type of that name, and the set of public std modules.
type stdExportIndex struct {
	types   map[string][]string
	modules map[string]bool
}

// StdModuleExport is what one std module offers the import help: the
// public types it declares, in declaration order, and whether it declares
// anything public.
type StdModuleExport struct {
	Types  []string
	Public bool
}

// StdModuleExportOf is what module, whose declarations are nodes, exports
// to the import help. An internal module and the prelude export nothing.
func StdModuleExportOf(module string, nodes []ast.Node) StdModuleExport {
	var out StdModuleExport
	if _, internal := internalStdModules[module]; internal || module == "prelude" {
		return out
	}
	for _, n := range nodes {
		if name, pub := stdExportedType(n); pub {
			out.Public = true
			out.Types = append(out.Types, name)
		} else if stdNodePublic(n) {
			out.Public = true
		}
	}
	return out
}

// StdExportsFromSources is what each module of sources, by module name
// (`http` for std/http.nomi), exports to the import help, each source
// parsed. A source that does not parse exports nothing.
func StdExportsFromSources(sources map[string]string) map[string]StdModuleExport {
	out := make(map[string]StdModuleExport, len(sources))
	for module, src := range sources {
		nodes, err := parser.Parse(lexer.Lex(src))
		if err != nil {
			continue
		}
		out[module] = StdModuleExportOf(module, nodes)
	}
	return out
}

// SetStdExports installs exports, which answers what each std module
// exports to the import help, by module name. Package std calls it.
func SetStdExports(exports func() map[string]StdModuleExport) {
	stdExportsMu.Lock()
	defer stdExportsMu.Unlock()
	stdExportsFunc, stdIndexOnce, stdIndex = exports, nil, nil
	if exports != nil {
		stdIndexOnce = new(sync.Once)
	}
}

func stdExports() *stdExportIndex {
	stdExportsMu.Lock()
	once, exports := stdIndexOnce, stdExportsFunc
	stdExportsMu.Unlock()
	if once == nil {
		return nil
	}
	once.Do(func() {
		idx := buildStdExportIndex(exports())
		stdExportsMu.Lock()
		stdIndex = idx
		stdExportsMu.Unlock()
	})
	stdExportsMu.Lock()
	defer stdExportsMu.Unlock()
	return stdIndex
}

func buildStdExportIndex(exports map[string]StdModuleExport) *stdExportIndex {
	idx := &stdExportIndex{types: map[string][]string{}, modules: map[string]bool{}}
	for module, e := range exports {
		for _, name := range e.Types {
			idx.types[name] = append(idx.types[name], module)
		}
		if e.Public {
			idx.modules[module] = true
		}
	}
	for name := range idx.types {
		sort.Strings(idx.types[name])
	}
	return idx
}

// stdExportedType is the type n declares and whether it is public.
func stdExportedType(n ast.Node) (string, bool) {
	switch d := n.(type) {
	case *ast.StructDef:
		return d.Name, d.Public
	case *ast.EnumDef:
		return d.Name, d.Public
	case *ast.TypeDef:
		return d.Name, d.Public
	case *ast.TypeAlias:
		return d.Name, d.Public
	case *ast.InterfaceDef:
		return d.Name, d.Public
	case *ast.ExternType:
		return d.Name, d.Public
	}
	return "", false
}

// stdNodePublic reports whether n is a public function, host function or
// once.
func stdNodePublic(n ast.Node) bool {
	switch d := n.(type) {
	case *ast.FuncDef:
		return d.Public
	case *ast.ExternFunc:
		return d.Public
	case *ast.OnceBinding:
		return d.Public
	}
	return false
}

// stdTypeModules are the public std modules that export a type named
// name, in name order; none when std's exports are not installed.
func stdTypeModules(name string) []string {
	if idx := stdExports(); idx != nil {
		return idx.types[name]
	}
	return nil
}

// stdModuleExists reports whether name is a public std module.
func stdModuleExists(name string) bool {
	idx := stdExports()
	return idx != nil && idx.modules[name]
}

// maxImportChoices is how many std modules a help lists for a type name
// several export. Past it the list is noise, and the help names none.
const maxImportChoices = 3

// stdTypeImportHint is the help for name, a type the file names without
// binding it: the import that brings it when one std module exports it,
// each import when a few do, "" when none or too many do.
func stdTypeImportHint(name string) string {
	modules := stdTypeModules(name)
	switch {
	case len(modules) == 0 || len(modules) > maxImportChoices:
		return ""
	case len(modules) == 1:
		return fmt.Sprintf("import `std::%s.%s`", stdImportPath(modules[0]), name)
	}
	imports := make([]string, len(modules))
	for i, m := range modules {
		imports[i] = fmt.Sprintf("`std::%s.%s`", stdImportPath(m), name)
	}
	last := len(imports) - 1
	return fmt.Sprintf("import %s or %s", strings.Join(imports[:last], ", "), imports[last])
}

// stdModuleImportHint is the help for name, a value name the file does
// not bind that names a public std module: `io.print` without `import
// std::io`. "" for any other name.
func stdModuleImportHint(name string) string {
	if !stdModuleExists(name) {
		return ""
	}
	return fmt.Sprintf("import `std::%s`", name)
}

// stdImportPath spells a std module name as the segments of an import path
// after `std::`: "fs/testing" is `fs::testing`.
func stdImportPath(module string) string {
	return strings.ReplaceAll(module, "/", "::")
}
