package analysis

import (
	"sort"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ast"
)

// StdSource is the process's stdlib as a build against it sees it when the
// stdlib is built a module at a time (std.Shared): the analyses a build is
// handed (its stdlibFAs) hold the modules built so far, which are closed
// under import, and a build that reaches another asks for it here before it
// builds any file. The answers then are what they would be over every
// module, since a program can reach no declaration of a module it does not
// import, directly or through the modules it imports.
type StdSource interface {
	// Extend answers have with the named modules, and every module they
	// import, built: the analyses of the largest stdlib built so far, which
	// holds have's. A name that is no module of the stdlib is left out.
	// Have itself when it is no part of this stdlib.
	Extend(have map[string]*FileAnalysis, names []string) map[string]*FileAnalysis
	// ModuleNames answers every module of the stdlib, built or not, by its
	// name under std/ ("io", "prelude"), sorted.
	ModuleNames() []string
	// Base answers the base have forms (BuildStdBase), built on the first
	// call for it, when have is a stdlib the source built; otherwise nil.
	Base(have map[string]*FileAnalysis) *StdBase
	// Built answers the module's analysis when the source has built it,
	// building nothing; otherwise nil.
	Built(name string) *FileAnalysis
}

var stdSource atomic.Pointer[StdSource]

// SetStdSource installs the process's stdlib source (std.Shared's).
func SetStdSource(s StdSource) { stdSource.Store(&s) }

// currentStdSource is the installed StdSource, or nil.
func currentStdSource() StdSource {
	if p := stdSource.Load(); p != nil {
		return *p
	}
	return nil
}

// stdModuleNamesOf answers the stdlib's module names for a build handed the
// analyses have: every module the installed source has when have holds
// some, since have may be a lazily built stdlib's part, and otherwise
// have's own.
func stdModuleNamesOf(have map[string]*FileAnalysis) []string {
	if src := currentStdSource(); src != nil && len(have) > 0 {
		return src.ModuleNames()
	}
	names := make([]string, 0, len(have))
	for name := range have {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// BuildStdDeclarationsOver is BuildStdDeclarations for modules beyond those
// a stdlib built from interfaces already holds: entryNodes import the new
// modules, have is the analyses built so far (closed under import), and the
// answer is each new module's analysis and nodes by key. A new module's
// import of a built one resolves to have's analysis of it, as a program's
// import does, so the modules built a few at a time are the modules built at
// once (TestShared_LazyModulesAreEagerModules).
func BuildStdDeclarationsOver(entryNodes []ast.Node, loader FileLoader, have map[string]*FileAnalysis) (map[string]*FileAnalysis, map[string][]ast.Node) {
	// Every built module's scope, as the build of every module names each
	// one's (Program.StdlibModuleScopes).
	scopes := make(map[string]*Scope, len(have))
	for name, fa := range have {
		scopes[name] = fa.ModuleScope
	}
	_, cache, nodes := buildProjectWithCache(entryNodes, nil, scopes, have, "", "", "", false, loader, nil, &buildInputs{deferStdBodies: true})
	out := make(map[string]*FileAnalysis, len(cache))
	for key, fa := range cache {
		name, ok := strings.CutPrefix(key, "std/")
		if !ok || have[name] == fa {
			continue
		}
		out[key] = fa
	}
	for _, fa := range out {
		fa.Program().Onces.infer()
	}
	return out, nodes
}

// StdModuleImports answers the stdlib modules module's nodes import, by
// name under std/, sorted: at the top level, in blocks, and in attached
// tests, since checking the module's bodies resolves each of them. A
// stdlib built a module at a time builds a module with every module this
// names.
func StdModuleImports(module string, nodes []ast.Node) []string {
	seen := map[string]bool{}
	add := func(n ast.Node) {
		for _, path := range importPathsOf(n) {
			path = QualifyIntraStdlibImport("std/"+module, path)
			if len(path) > 1 && path[0] == "std" {
				seen[strings.Join(path[1:], "/")] = true
			}
		}
	}
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		ast.Inspect(n, func(m ast.Node) bool {
			switch m.(type) {
			case *ast.ImportStmt, *ast.ImportBlock:
				add(m)
				return false
			}
			for _, at := range ast.AttachedTestsOf(m) {
				if at.Body != nil {
					visit(at.Body)
				}
			}
			return true
		})
	}
	for _, n := range nodes {
		visit(n)
	}
	delete(seen, module)
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// isSharedStdFile reports whether fa, a build's analysis of the stdlib
// module key, is the shared stdlib's own rather than a copy the build made:
// have's analysis of it, or, since a build extends the stdlib it was handed
// with the modules it reaches (StdSource.Extend), the installed source's.
// A caller's have is the snapshot it held before the build, which lacks a
// module the build was the first to reach.
func isSharedStdFile(key string, fa *FileAnalysis, have map[string]*FileAnalysis) bool {
	name, ok := strings.CutPrefix(key, "std/")
	if !ok || fa == nil {
		return false
	}
	if have[name] == fa {
		return true
	}
	if src := currentStdSource(); src != nil && len(have) > 0 {
		return src.Built(name) == fa
	}
	return false
}
