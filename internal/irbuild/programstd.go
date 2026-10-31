package irbuild

import (
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// Std modules lowered with the program that reaches them.
//
// Most of the stdlib is lowered once per process (stdlibLowering), and a
// shared body can only name a type that has a process-wide representation:
// an rt Go type with a row in stdstruct.go, stdenum.go or opaque.go, and for
// an interface used as a value an rt dispatch table (stdiface.go). A module
// listed in programStdModules has none of that. GenerateIR appends each one
// a program's files import, directly or through another listed module, to
// the program as one more Module, built from the shared analysis
// (std.Shared), and lowers it as it lowers the program's own files: its types
// are the program's types, keyed by their declaration node in the program's
// type registry, so a program has one descriptor for each, which every file
// of the program names; its interfaces are program interfaces, boxed and
// dispatched by the VM's ordinary path; a
// shared generic std body over one of its types (List<Uuid>, a Map key,
// Iter.sort, Debug, Display, equality, hashing) is instantiated per program,
// as it is for any type the program declares (stdinst.go). Nothing of a
// listed module is cached: its lowering belongs to the program, and the next
// program lowers it again.
//
// # Which modules are listed
//
// The rule is the list below, checked by TestProgramStdModulesQualify. A
// listed module
//
//   - declares no `host fn` and no `host type`: every body is Nomi, and the
//     builder lowers it as it lowers a program's own file;
//   - is imported by no module lowered once for the process, since a shared
//     body cannot name a type each program declares again;
//   - checks and lowers with no error or decline, its `//!` cases included.
//
// The prelude types, the collections, Iter, Maybe and Result, and every
// module with a host function stay shared: the VM has intrinsics and rows
// for them, and their host functions cross into Go over rt types. A new
// module written in Nomi needs no rt type and no row: list it here.
var programStdModules = map[string]bool{
	"fs":          true,
	"fs_testing":  true,
	"message":     true,
	"money":       true,
	"path":        true,
	"process":     true,
	"reverse":     true,
	"bits":        true,
	"sql":         true,
	"sql_testing": true,
	"uuid":        true,
}

// isProgramStd reports whether the std module named name (`uuid`, without
// `std/`) is lowered with each program rather than once for the process. A
// test-only module under `_fixtures/` (std.FixturesLoaded) always is: that
// is what the fixtures are for.
func isProgramStd(name string) bool {
	return programStdModules[name] || strings.HasPrefix(name, "_fixtures/")
}

// programStdModuleNodes is each listed module's analyzed nodes from the shared
// analysis, with their tail calls marked once for the process: std.Load does
// not mark them (stdModuleContext's reason), and the nodes are shared by
// every program that lowers them, possibly at once.
var programStdModuleNodes = sync.OnceValue(func() map[string][]ast.Node {
	lib := std.Shared()
	out := map[string][]ast.Node{}
	for name, nodes := range lib.Nodes {
		if isProgramStd(name) {
			analysis.MarkTailCalls(nodes)
			out[name] = nodes
		}
	}
	return out
})

// withProgramStd answers p with every listed std module its files reach
// appended as a Module, or p itself when they reach none. p's own Modules are
// left as they were: a caller reads them as the program's files.
func withProgramStd(p *Program) *Program {
	if len(programStdModuleNodes()) == 0 {
		return p
	}
	seen := map[string]bool{}
	for i := range p.Modules {
		// A listed module that is itself the entry: its `//!` cases
		// (generateProgramStdTestIR).
		if name, inStd := strings.CutPrefix(p.Modules[i].Name, "std/"); inStd {
			seen[name] = true
		}
	}
	var added []string
	var reach func(nodes []ast.Node, inStd bool)
	reach = func(nodes []ast.Node, inStd bool) {
		for _, name := range importedStdModules(nodes, inStd) {
			if !isProgramStd(name) || seen[name] {
				continue
			}
			seen[name] = true
			added = append(added, name)
			reach(programStdModuleNodes()[name], true)
		}
	}
	for i := range p.Modules {
		_, inStd := strings.CutPrefix(p.Modules[i].Name, "std/")
		reach(p.Modules[i].Nodes, inStd)
	}
	if len(added) == 0 {
		return p
	}
	slices.Sort(added)
	lib := std.Shared()
	q := *p
	q.Modules = append(slices.Clip(p.Modules), make([]Module, 0, len(added))...)
	for _, name := range added {
		q.Modules = append(q.Modules, Module{
			Path:  lib.SourcePath(name),
			Name:  "std/" + name,
			Nodes: programStdModuleNodes()[name],
			FA:    lib.Files[name],
		})
	}
	return &q
}

// importedStdModules is the std module of each `import` in nodes, at the top
// level and in blocks, by its name under std/ (`uuid`). A program's file
// writes the module as `std/uuid`; a std module names a sibling bare
// (`import uuid.Uuid`), which inStd says, but its `//!` prompts are written
// as a program's code and name it `std/uuid` (`//! import std/sql_testing.Fake`
// in std/sql.nomi), so a std/ path counts in a std module too.
func importedStdModules(nodes []ast.Node, inStd bool) []string {
	var out []string
	add := func(s *ast.ImportStmt) {
		n := s.FileSegments
		if n <= 0 || n > len(s.ModulePath) {
			n = len(s.ModulePath)
		}
		segs := make([]string, 0, n)
		for _, seg := range s.ModulePath[:n] {
			segs = append(segs, strings.Split(ast.ImportNodeName(seg), "/")...)
		}
		switch {
		case len(segs) >= 2 && segs[0] == "std":
			segs = segs[1:]
		case !inStd:
			return
		}
		out = append(out, strings.Join(segs, "/"))
	}
	for _, n := range nodes {
		ast.Inspect(n, func(m ast.Node) bool {
			switch s := m.(type) {
			case *ast.ImportStmt:
				add(s)
				return false
			case *ast.ImportBlock:
				for _, e := range s.Entries {
					add(e)
				}
				return false
			}
			return true
		})
	}
	return out
}

// generateProgramStdTestIR builds the `//!` cases of a listed std module (`nomi
// test std/uuid.nomi`, a reference editor) as a test file's cases are built:
// the module is the entry, and every other listed module it reaches is lowered
// beside it. nodes and fa are the module as checked from edited source, or nil
// for the shared analysis's.
func generateProgramStdTestIR(module string, nodes []ast.Node, fa *analysis.FileAnalysis) (*Program, *Result, error) {
	lib := std.Shared()
	var twins map[ast.Node]ast.Node
	if nodes == nil {
		nodes, fa = programStdModuleNodes()[module], lib.Files[module]
	} else {
		analysis.MarkTailCalls(nodes)
		twins = implFuncTwins(programStdModuleNodes()[module], nodes)
	}
	p := &Program{
		Modules:  []Module{{Path: lib.SourcePath(module), Name: "std/" + module, Nodes: nodes, FA: fa, twins: twins}},
		HasTests: true,
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		return nil, nil, err
	}
	return p, res, nil
}

// implFuncTwins maps each top-level declaration and impl function of shared,
// the shared analysis's copy of a std module, to the one of the same kind and
// name (and, for an impl function, the same receiver and interface) in
// edited, the module checked from edited source. A check of the edited module
// resolves a typed literal's handler through the stdlib's impl index
// (Symbol.DispatchImpl), and a block import of the module's own names, which
// a reference editor's context writes, through the stdlib's module scopes:
// both name the shared copy, and the program lowers the edited one.
func implFuncTwins(shared, edited []ast.Node) map[ast.Node]ast.Node {
	keys := func(nodes []ast.Node, each func(key string, n ast.Node)) {
		for _, n := range nodes {
			switch d := n.(type) {
			case *ast.ImplBlock:
				for _, item := range d.Items {
					if fd, ok := item.(*ast.FuncDef); ok {
						each("impl "+typeText(d.Receiver)+" "+typeText(d.Interface)+" "+fd.Name, fd)
					}
				}
			case *ast.StructDef:
				each("struct "+d.Name, d)
			case *ast.EnumDef:
				each("enum "+d.Name, d)
			case *ast.TypeDef:
				each("type "+d.Name, d)
			case *ast.InterfaceDef:
				each("interface "+d.Name, d)
			case *ast.FuncDef:
				each("fn "+d.Name, d)
			}
		}
	}
	byKey := map[string]ast.Node{}
	keys(edited, func(key string, n ast.Node) { byKey[key] = n })
	twins := map[ast.Node]ast.Node{}
	keys(shared, func(key string, n ast.Node) {
		if twin := byKey[key]; twin != nil {
			twins[n] = twin
		}
	})
	return twins
}

// twinOf reports whether a, a symbol of the shared analysis's copy of this
// module, declares what b declares in this module checked from edited
// source: a block import of the module's own names in its edited copy.
func (g *gen) twinOf(a, b *analysis.Symbol) bool {
	return a != nil && b != nil && a.Node != nil && g.twins[a.Node] == b.Node
}
