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
// read from std's sources, which package std installs (SetStdSources), and
// only when a diagnostic asks: each source is parsed once per process, and
// nothing is checked. Internal modules (internalStdModules) are never
// suggested, and neither is a module with no public declarations.

var (
	stdSourcesMu sync.Mutex
	stdSources   func() map[string]string
	stdIndexOnce *sync.Once
	stdIndex     *stdExportIndex
)

// stdExportIndex is, for each public std type name, the modules that
// export a type of that name, and the set of public std modules.
type stdExportIndex struct {
	types   map[string][]string
	modules map[string]bool
}

// SetStdSources installs sources, which answers each std module's source
// by module name (`http` for std/http.nomi). Package std calls it.
func SetStdSources(sources func() map[string]string) {
	stdSourcesMu.Lock()
	defer stdSourcesMu.Unlock()
	stdSources, stdIndexOnce, stdIndex = sources, nil, nil
	if sources != nil {
		stdIndexOnce = new(sync.Once)
	}
}

func stdExports() *stdExportIndex {
	stdSourcesMu.Lock()
	once, sources := stdIndexOnce, stdSources
	stdSourcesMu.Unlock()
	if once == nil {
		return nil
	}
	once.Do(func() {
		idx := buildStdExportIndex(sources())
		stdSourcesMu.Lock()
		stdIndex = idx
		stdSourcesMu.Unlock()
	})
	stdSourcesMu.Lock()
	defer stdSourcesMu.Unlock()
	return stdIndex
}

func buildStdExportIndex(sources map[string]string) *stdExportIndex {
	idx := &stdExportIndex{types: map[string][]string{}, modules: map[string]bool{}}
	for module, src := range sources {
		if _, internal := internalStdModules[module]; internal || module == "prelude" {
			continue
		}
		nodes, err := parser.Parse(lexer.Lex(src))
		if err != nil {
			continue
		}
		public := false
		for _, n := range nodes {
			if name, pub := stdExportedType(n); pub {
				public = true
				idx.types[name] = append(idx.types[name], module)
			} else if stdNodePublic(n) {
				public = true
			}
		}
		if public {
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
// name, in name order; none when std's sources are not installed.
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
		return fmt.Sprintf("import `std::%s.%s`", modules[0], name)
	}
	imports := make([]string, len(modules))
	for i, m := range modules {
		imports[i] = fmt.Sprintf("`std::%s.%s`", m, name)
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
