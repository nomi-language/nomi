package irbuild

// A STDLIB MODULE'S `//!` PROMPT CASES, LOWERED FOR THE VM.
//
// `nomi test std/<module>` runs a module's attached cases in the module's own
// scope, with each case's owning type as its self type, using the lowering the
// VM already has. The stdlib is lowered once per process
// (stdlibLowering) and every module keeps its view (stdlibIndex.views): its
// AST, its analysis, its candidates and its `once` bindings. A module's cases
// are built by a gen made from that view exactly as a generic std instance is
// (stdinst.go): the same nodes, the same analysis, the full index as the
// sibling underlay, and a per-call instance table. The case bodies go into
// that gen's own `ir.Module`, which is never written into the cache; the VM
// links it beside the cached modules and the instances the cases reached.
//
// The full index, not the modules lowered before this one, because a test
// module is a leaf: nothing links against it, so a case may call any module.
//
// Each case is an ordinary test body (irtestbody.go), so what it admits and
// what it declines is the user test path's, including the VM-only retry. A
// case inside a type's items has that type as its self type (`implSelf`), so a
// bare same-owner call resolves as the member bodies' does.

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// stdModuleTestCases collects a stdlib module's cases in the order and under
// the names frontend.CollectTestCases gives them: each declaration's prompts
// and its items' (attachedTestCasesIn), and a top-level `test` or `tests`
// declaration through collectTestDecl.
func stdModuleTestCases(nodes []ast.Node) []testCaseDecl {
	var out []testCaseDecl
	for _, n := range nodes {
		out = append(out, attachedTestCasesIn([]ast.Node{n}, nil, "")...)
		if t, ok := n.(*ast.TestDecl); ok {
			out = append(out, collectTestDecl(t, n, nil, nil, "", testRefusal{})...)
		}
	}
	return out
}

// StdlibTestModule is the module name of a stdlib source file, or "" when file
// is not under the stdlib root: `<std>/calendar.nomi` is `calendar`.
func StdlibTestModule(file string) string {
	root, err := analysis.StdlibPath()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return ""
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if real, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = real
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		!strings.HasSuffix(rel, ".nomi") {
		return ""
	}
	return filepath.ToSlash(strings.TrimSuffix(rel, ".nomi"))
}

// GenerateStdlibTestIR lowers the test cases of stdlib module `module` for
// the VM and answers them as a Program and a Result, the shapes GenerateIR
// answers for a user test file: the entry module is the module's own
// declarations (so a runner plans its cases from them), Result.IR holds the
// case bodies, and Result.IRModules adds the cached stdlib and the instances
// the cases reached.
//
// nodes and fa are the module's checked declarations. Nil takes the cached
// lowering's own; the tour passes a re-analyzed copy of the module's source
// with one `test` appended (vmhost.StdlibReference), whose other
// declarations are the same Nomi as the cache's.
func GenerateStdlibTestIR(module string, nodes []ast.Node, fa *analysis.FileAnalysis) (*Program, *Result, error) {
	std := stdlibLowering()
	v := std.views[module]
	if v == nil {
		return nil, nil, fmt.Errorf("irbuild: std/%s is not a lowered stdlib module", module)
	}
	if nodes == nil {
		nodes, fa = v.nodes, v.fa
	} else {
		analysis.MarkTailCalls(nodes)
	}
	return generateStdlibTestIR(std, v, nodes, fa, std.irModules, nil)
}

// generateStdlibTestIR builds the cases of nodes, the module v lowered, against
// the index std, and links them beside libMods (the cached stdlib's modules,
// by package) and extra, which is linked after them.
func generateStdlibTestIR(std *stdlibIndex, v *stdModuleView, nodes []ast.Node, fa *analysis.FileAnalysis, libMods map[string]*ir.Module, extra []*ir.Module) (*Program, *Result, error) {
	module := v.module
	offer := make(map[*stdCandidate]bool, len(v.settled)+len(v.withCycles))
	for c := range v.settled {
		offer[c] = true
	}
	for c := range v.withCycles {
		offer[c] = true
	}
	g := newStdGen(v.module, v.path, v.pkg, nodes, fa, v.cands, offer, v.onces, std)
	insts := newStdInstances(std)
	g.stdInsts = insts
	// The prelude anchors are loaded on first use, and several builder arms
	// read `preludeByName` without asking for them (mapCallPlan's `Maybe`
	// check). A user unit has always loaded them by the time a test body is
	// built; a fresh std gen has not.
	g.loadPreludes()
	withSelf := func(c testCaseDecl) func() {
		prev := g.implSelf
		g.implSelf = c.self
		return func() { g.implSelf = prev }
	}
	cases := stdModuleTestCases(nodes)
	bodies := make([]*ast.Block, 0, len(cases))
	for _, c := range cases {
		bodies = append(bodies, c.body)
	}
	g.declareCaseBlockTypes(bodies)
	for _, c := range cases {
		undo := withSelf(c)
		g.testCase(c)
		undo()
	}
	// The VM-only retries resolve bare same-owner calls the same way.
	g.irRetryWalkOnlyTestBodiesWith(withSelf)
	var irMods []*ir.Module
	if g.irMod != nil {
		irMods = append(irMods, g.irMod)
	}
	pkgs := make([]string, 0, len(libMods))
	for pkg := range libMods {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	libs := make([]*ir.Module, 0, len(pkgs)+len(extra))
	for _, pkg := range pkgs {
		libs = append(libs, libMods[pkg])
	}
	libs = append(libs, extra...)
	instMods := insts.modules()
	for _, m := range instMods {
		irLintFinished(m)
	}
	libs = append(libs, instMods...)
	irWithdrawUnforceableReads(irMods, libs)
	for _, m := range irMods {
		irLintFinished(m)
	}
	prog := &Program{
		Modules:  []Module{{Path: v.path, Name: module, Nodes: nodes, FA: fa, DeclaresTests: true}},
		HasTests: true,
	}
	return prog, &Result{IR: irMods, irLibraries: libs}, nil
}

// stdTestOwnType reports that a test body of a stdlib module is being built.
// There a type the module declares is the std type itself rather than a
// declaration shadowing it, so an arm that declines a locally declared
// `Channel` or `Result` as a possible shadow does not apply. Each arm still
// checks the operands' identity (the channel spec, the prelude spec).
func (bl *irScalarBuilder) stdTestOwnType() bool {
	return bl.inTest && bl.g.stdModule != ""
}

// testImport admits an `import` written inside a test body as a statement
// with no effect, when it changes what no name means. std/compiler's prompts
// are written as separate programs and open with `import std/compiler` and
// `import std/strings.String`. The builder resolves a body's names against
// the module's own scope, so an import is admitted only when every name it
// binds resolves to the declaration the module scope already gives that name,
// or it is a whole-module import of the stdlib module whose test body this
// is, whose alias the builder resolves as that module's own file alias.
func (bl *irScalarBuilder) testImport(n *ast.ImportStmt) bool {
	g := bl.g
	no := func() bool {
		irDeclineNote("an `import` in a test body that rebinds a name the module scope binds differently")
		return false
	}
	if g.fa == nil || g.fa.ModuleScope == nil || n.Extern {
		return no()
	}
	bound := 0
	for _, sym := range g.fa.Definitions {
		if sym == nil || sym.Node != n {
			continue
		}
		bound++
		if sym.Kind == analysis.SymbolModule && g.stdModule != "" && n.ModuleAlias == nil &&
			len(n.Names) == 0 && importPathString(n) == "std/"+g.stdModule {
			continue
		}
		outer := resolveSymbol(g.fa.ModuleScope.Lookup(sym.Name))
		if outer == nil && n.ModuleAlias == nil && len(n.Names) == 1 && len(n.Aliases) == 1 && n.Aliases[0] != nil && sym.Resolved != nil {
			// `import std/maybe.Maybe as Local` in a body: a name the module
			// scope does not bind, so no name-keyed lookup can resolve it,
			// and every use the builder admits resolves by the checker's
			// reference at its position.
			continue
		}
		if outer == nil || outer != resolveSymbol(sym) {
			return no()
		}
	}
	if bound == 0 {
		return no()
	}
	return true
}

// importPathString is an import's module path as written, `std/compiler`.
func importPathString(n *ast.ImportStmt) string {
	parts := make([]string, len(n.ModulePath))
	for i, seg := range n.ModulePath {
		parts[i] = ast.ImportNodeName(seg)
	}
	return strings.Join(parts, "/")
}

// stdOwnFileQualifier answers a stdlib module's own file alias inside one of
// its test bodies: `compiler.check(...)` in std/compiler. The cached lowering's
// analysis binds that alias to one of the stdlib's module scopes, which is
// what stdFileQualifier matches. A module re-analyzed from edited source (a
// tour reference editor, vmhost.StdlibReference) binds it to its own
// new module scope instead, which is the same module.
func (bl *irScalarBuilder) stdOwnFileQualifier(owner string) (string, bool) {
	g := bl.g
	if !bl.stdTestOwnType() || g.fa == nil || owner != path.Base(g.stdModule) {
		return "", false
	}
	if scope := moduleScopeOf(g.fa, owner); scope != nil && scope == g.fa.ModuleScope {
		return g.stdModule, true
	}
	return "", false
}
