package tourgo

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/virtualproject"
)

// A tour module is a Nomi module a tour block imports as a program on disk
// imports a module its go.mod requires, with nothing in the block that
// says where it comes from: the Databases chapter's `import sqlite` is the
// same line a program written by hand has. The tour links each one from
// its directory in this repository, under lib/: its Nomi sources
// (modules_sources_gen.go, which `go generate ./internal/tourgo` writes
// from the tree), and in the browser the adapters of its `go` bindings
// (linked_modules_gen.go's linkedModules), generated as ffirun generates a
// wrapper's, over the js/wasm build of its Go side: the sqlite driver's
// talks to SQLite's official WebAssembly build, which the tour's worker
// loads before a run (run-worker.js).
//
// The compiler's go.mod requires no tour module. A Nomi module it required
// would be a dependency of every program whose go.mod is the compiler's
// (the corpus), and the sqlite driver's module requires modernc.org/sqlite.
// A js/wasm build of cmd/nomi-wasm runs instead in the Go workspace
// GoWork, which puts each tour module's directory beside the compiler's
// module, so linked_modules_gen.go imports the module's Go package from
// the tree; the js/wasm build of that package imports no
// modernc.org/sqlite. Outside the browser no tour module's Go side is
// linked (modules_native.go), and Unlinked says so.
//
// Source attaches the modules a block imports to its source, as the
// sections of a project that names modules (a go.mod that requires each
// one, its files under ModulesDir), which vmhost.LoadMarked writes and
// loads as it loads any such block; MarkedHosts then answers the modules'
// bindings beside the block's own. Every runner of tour blocks loads a
// block through Source.

// Module is one Nomi module the tour links for its blocks.
type Module struct {
	// Name is the module's name, its nomi.toml's, which a block imports
	// (`import sqlite`, `import sqlite/describe`).
	Name string
	// Path is its Go module path, as a go.mod requires it.
	Path string
	// Dir is its directory in this repository, slash-separated.
	Dir string
	// Requires is the names of the tour modules its go.mod requires, which
	// Source attaches with it (sqlkit's prepare tool imports sqlite).
	Requires []string
	// Files is the text of its nomi.toml and Nomi files, its tests left
	// out, by their path in Dir: modules_sources_gen.go's copy of the tree.
	Files map[string]string
}

// Modules is every module the tour links.
var Modules = []Module{
	{Name: "sqlite", Path: "github.com/nomi-language/nomi/lib/sql/sqlite", Dir: "lib/sql/sqlite", Files: moduleSources["sqlite"]},
	{Name: "sqlkit", Path: "github.com/nomi-language/nomi/lib/sql/sqlkit", Dir: "lib/sql/sqlkit", Requires: []string{"sqlite"}, Files: moduleSources["sqlkit"]},
}

// GoWork is the Go workspace file, relative to the repository root, that
// every js/wasm build of the tour (cmd/nomi-wasm) names as GOWORK: the
// compiler's module and the directory of each of Modules.
const GoWork = "internal/tourgo/tour.work"

// ModulesDir is the directory of a block's project that Source writes the
// modules it imports under, one directory each, named by the module.
const ModulesDir = "tour_modules"

// Source is a tour block's source as the tour loads it: src unchanged,
// unless its Nomi files import one of Modules and it names no modules of
// its own (it has no go.mod section, whose requirements would be the
// block's to state). Then it is src's files (a single-file block's as
// main.nomi), a go.mod that requires and replaces each module the block
// imports and each tour module those require, and each one's files under
// ModulesDir, with a go.mod that requires and replaces its own.
func Source(src string) string {
	project, used := importedBy(src)
	if len(used) == 0 {
		return src
	}
	var b strings.Builder
	if strings.Contains(src, "// FILE:") {
		b.WriteString(src)
	} else {
		b.WriteString("// FILE: " + project.EntryName + ".nomi\n" + src)
	}
	if !strings.HasSuffix(src, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n// FILE: go.mod\nmodule tour\n")
	for _, m := range used {
		b.WriteString("\nrequire " + m.Path + " v0.0.0\n")
		b.WriteString("\nreplace " + m.Path + " => ./" + ModulesDir + "/" + m.Name + "\n")
	}
	for _, m := range used {
		dir := ModulesDir + "/" + m.Name
		b.WriteString("\n// FILE: " + dir + "/go.mod\nmodule " + m.Path + "\n")
		for _, name := range m.Requires {
			dep := moduleNamed(name)
			b.WriteString("\nrequire " + dep.Path + " v0.0.0\n")
			b.WriteString("\nreplace " + dep.Path + " => ../" + dep.Name + "\n")
		}
		for _, name := range moduleFiles(m) {
			text := m.Files[name]
			b.WriteString("\n// FILE: " + dir + "/" + name + "\n")
			b.WriteString(text)
			if len(text) > 0 && text[len(text)-1] != '\n' {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// Imported is the names of the modules Source attaches to src.
func Imported(src string) []string {
	_, used := importedBy(src)
	names := []string{}
	for _, m := range used {
		names = append(names, m.Name)
	}
	return names
}

// Unlinked is the names of the modules src imports whose Go side this
// binary does not link: every one outside the browser, since only the
// tour's js/wasm build links a module's Go side. A runner of tour blocks
// outside the browser (the doctests, the tour recorder, the VM harness)
// leaves such a block to the staged wasm, which TestTourWasm runs under
// Node with SQLite's WebAssembly build.
func Unlinked(src string) []string {
	_, used := importedBy(src)
	var out []string
	for _, m := range used {
		linkedHere := false
		for _, lm := range linkedModules {
			if lm.module == m.Path {
				linkedHere = true
			}
		}
		if !linkedHere {
			out = append(out, m.Name)
		}
	}
	return out
}

// importedBy is src's project and the modules Source attaches to it.
func importedBy(src string) (virtualproject.Project, []Module) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil || project.Files != nil {
		return project, nil
	}
	sources := []string{project.EntrySource}
	for name, text := range project.VirtualFiles {
		if !strings.HasSuffix(name, ".toml") {
			sources = append(sources, text)
		}
	}
	needed := map[string]bool{}
	var need func(name string)
	need = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		for _, dep := range moduleNamed(name).Requires {
			need(dep)
		}
	}
	for _, m := range Modules {
		for _, text := range sources {
			if imports(text, m.Name) {
				need(m.Name)
				break
			}
		}
	}
	var used []Module
	for _, m := range Modules {
		if needed[m.Name] {
			used = append(used, m)
		}
	}
	return project, used
}

// moduleNamed is the module of Modules named name.
func moduleNamed(name string) Module {
	for _, m := range Modules {
		if m.Name == name {
			return m
		}
	}
	panic("tourgo: no tour module " + name)
}

// moduleFiles is the paths of m's files, in order.
func moduleFiles(m Module) []string {
	out := make([]string, 0, len(m.Files))
	for name := range m.Files {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// imports reports whether the Nomi source text imports a file of the
// module named module: an import whose path starts with that segment
// (`sqlite`, `sqlite/describe`, `sqlite.{memory}`). A file that does not
// parse is read as far as it does, so an example being edited keeps its
// modules.
func imports(text, module string) bool {
	if !strings.Contains(text, module) {
		return false
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(text))
	var stmts []*ast.ImportStmt
	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.ImportStmt:
			stmts = append(stmts, x)
		case *ast.ImportBlock:
			stmts = append(stmts, x.Entries...)
		}
	}
	for _, s := range stmts {
		if len(s.ModulePath) == 0 {
			continue
		}
		if id, ok := s.ModulePath[0].(*ast.Ident); ok && id.Name == module {
			return true
		}
	}
	return false
}

// carried is the modules a block's project carries under ModulesDir, as
// Source attached them.
func carried(project virtualproject.Project) []int {
	var out []int
	for i, m := range Modules {
		if _, ok := project.Files[ModulesDir+"/"+m.Name+"/go.mod"]; ok {
			out = append(out, i)
		}
	}
	return out
}

// withModules is hosts with the adapters of the modules numbered mods
// beside it.
func withModules(hosts func(*hostadapt.Env) (map[string]hostadapt.Func, error), mods []int) func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
	return func(env *hostadapt.Env) (map[string]hostadapt.Func, error) {
		out := map[string]hostadapt.Func{}
		if hosts != nil {
			own, err := hosts(env)
			if err != nil {
				return nil, err
			}
			for k, f := range own {
				out[k] = f
			}
		}
		for _, i := range mods {
			for _, lm := range linkedModules {
				if lm.module != Modules[i].Path {
					continue
				}
				funcs, err := lm.bind(env)
				if err != nil {
					return nil, err
				}
				for k, f := range funcs {
					out[k] = f
				}
			}
		}
		return out, nil
	}
}

// linkedModule is the adapters of one tour module's bindings, by the
// module's path: bind answers them by the key a program crosses under,
// which a block cannot change, since it does not show the module.
type linkedModule struct {
	module string
	bind   func(*hostadapt.Env) (map[string]hostadapt.Func, error)
}

// ToolHosts is the host table of `nomi tool <module> <name>` run in the
// project of src, a block Source attached its modules to: MarkedHosts',
// with the bindings of the tool's own file, tools/<name>.nomi, also under
// the keys a program whose entry it is crosses under, their bare names
// (`config_text` for `tools/prepare.config_text`), since the tool runs as
// that program.
func ToolHosts(src, name string) (func(*hostadapt.Env) (map[string]hostadapt.Func, error), error) {
	hosts, err := MarkedHosts(src)
	if err != nil || hosts == nil {
		return hosts, err
	}
	prefix := "tools/" + name + "."
	return func(env *hostadapt.Env) (map[string]hostadapt.Func, error) {
		funcs, err := hosts(env)
		if err != nil {
			return nil, err
		}
		out := make(map[string]hostadapt.Func, len(funcs))
		for k, f := range funcs {
			out[k] = f
			if bare, ok := strings.CutPrefix(k, prefix); ok {
				out[bare] = f
			}
		}
		return out, nil
	}, nil
}
