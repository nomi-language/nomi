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
// what it declines is the user test path's, including the build deferred to
// the end of the walk (irRetryWalkOnlyTestBodies). A case inside an impl
// block's items has the block's receiver as its self type (`implSelf`), so a
// bare same-owner call resolves as the member bodies' does. A bare call to
// one of the module's own top-level functions, or its bare name as a value,
// is what the module's own file alias spells (`encode(b)` is `hex.encode(b)`:
// ownSiblingCall, stdOwnFuncRef), so named arguments, defaults, assertion
// rows and callbacks reach it as they reach the qualified spelling.

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/std"
)

// stdModuleTestCases collects a stdlib module's cases in the order and under
// the names frontend.CollectTestCases gives them: each declaration's prompts
// and its items' (attachedTestCasesIn), and a top-level `test` or `group`
// declaration through collectTestDecl.
func stdModuleTestCases(nodes []ast.Node) []testCaseDecl {
	var out []testCaseDecl
	for _, n := range nodes {
		out = append(out, attachedTestCasesIn([]ast.Node{n}, nil, "")...)
		if t, ok := n.(*ast.TestDecl); ok {
			out = append(out, collectTestDecl(t, n, nil, nil, "", testRefusal{}, nil)...)
		}
	}
	return out
}

// GenerateStdlibTestIR lowers the test cases of stdlib module `module` for
// the VM and answers them as a Program and a Result, the shapes GenerateIR
// answers for a user test file: the entry module is the module's own
// declarations (so a runner plans its cases from them), Result.IR holds the
// case bodies, and Result.IRModules adds the cached stdlib and the instances
// the cases reached.
//
// The module is the one embedded in the binary, its declarations the
// cached lowering's; GenerateEditedStdlibTestIR lowers an edited one. hooks
// observe the cases' declines.
func GenerateStdlibTestIR(module string, hooks DeclineHooks) (*Program, *Result, error) {
	ensureStdModule(module)
	if isProgramStd(module) {
		return generateProgramStdTestIR(module, nil, nil, hooks)
	}
	std := stdlibLowering()
	v := std.views[module]
	if v == nil {
		return nil, nil, fmt.Errorf("irbuild: std/%s is not a lowered stdlib module", module)
	}
	tests, err := sharedStdTestDecls(module)
	if err != nil {
		return nil, nil, err
	}
	nodes := append(slices.Clip(v.nodes), tests...)
	return generateStdlibTestIR(std, v, nodes, nodes, v.fa, std.irModules, nil, false, newDeclineCensus(hooks))
}

// GenerateStdlibReferenceIR lowers test, one case appended to the embedded
// stdlib module `module` and checked in its scope (fa, from
// analysis.FileAnalysis.CheckAddedTest over the shared analysis of the
// module), as GenerateStdlibTestIR lowers the module's own cases: the
// module's declarations are the shared ones, already lowered, so only test
// is built. A reference editor runs one this way (vmhost.StdlibReference).
// hooks observe the case's declines.
func GenerateStdlibReferenceIR(module string, test *ast.TestDecl, fa *analysis.FileAnalysis, hooks DeclineHooks) (*Program, *Result, error) {
	cases := []ast.Node{test}
	analysis.MarkTailCalls(cases)
	ensureStdModule(module)
	if isProgramStd(module) {
		return generateProgramStdReferenceIR(module, test, fa, hooks)
	}
	std := stdlibLowering()
	v := std.views[module]
	if v == nil {
		return nil, nil, fmt.Errorf("irbuild: std/%s is not a lowered stdlib module", module)
	}
	stdBodies(module)
	nodes := append(slices.Clip(v.nodes), test)
	return generateStdlibTestIR(std, v, nodes, cases, fa, std.irModules, nil, true, newDeclineCensus(hooks))
}

// generateStdlibTestIR builds the cases of cases, nodes of the module v
// lowered (nodes, as fa checked them), against the index std, and links them
// beside libMods (the cached stdlib's modules, by package) and extra, which
// is linked after them. With link, the listed std modules the cases import
// link the kept lowering rather than being lowered with them. dc is the
// lowering's census.
func generateStdlibTestIR(std *stdlibIndex, v *stdModuleView, nodes, cases []ast.Node, fa *analysis.FileAnalysis, libMods map[string]*ir.Module, extra []*ir.Module, link bool, dc *declineCensus) (*Program, *Result, error) {
	module := v.module
	newCasesGen := func(in *programBuild) *gen {
		g := v.genIn(nodes, fa, std, in)
		g.stdCases = true
		return g
	}
	if listed := listedStdModulesOfCases(cases); len(listed) > 0 {
		return generateStdlibTestIRWithProgramStd(v, nodes, cases, fa, listed, newCasesGen, extra, link, dc.hooks)
	}
	g := newCasesGen(nil)
	g.dc = dc
	insts := newStdInstances(std, dc)
	g.stdInsts = insts
	g.lowerStdCases(cases)
	if g.irMod != nil {
		// The module's own impls are the shared lowering's; a std type the
		// cases hold as a std interface value, unboxed or boxed, dispatches
		// through entries they record.
		g.stdProgHeldImpls()
		g.irRecordBoxedImpls()
	}
	var irMods []*ir.Module
	if g.irMod != nil {
		irMods = append(irMods, g.irMod)
	}
	instMods := insts.modules()
	for _, m := range instMods {
		irLintFinished(m)
	}
	// The cached modules of the cases' closure (linkedShared), then the
	// edited module's own lowering and the cases' instances.
	libs := append(append(std.linkedShared(libMods, append([]string{module}, importedStdModules(nodes, true)...), irMods, extra, instMods), extra...), instMods...)
	irWithdrawUnforceableReads(dc, irMods, libs)
	for _, m := range irMods {
		irLintFinished(m)
	}
	prog := &Program{
		Modules:  []Module{{Path: v.path, Name: module, Nodes: nodes, FA: fa, DeclaresTests: true}},
		HasTests: true,
	}
	return prog, &Result{IR: irMods, irLibraries: libs, Literals: g.literalSites}, nil
}

// lowerStdCases builds the `//!` and `test` cases of nodes, a stdlib
// module's, on g, each with its owning type as its self type.
func (g *gen) lowerStdCases(nodes []ast.Node) {
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
	// The deferred builds resolve bare same-owner calls the same way.
	g.irRetryWalkOnlyTestBodiesWith(withSelf)
}

// listedStdModulesOfCases is each module in programStdModules that the cases
// of nodes, a shared stdlib module's, import (`//! import std::path`). A
// shared module itself imports none (TestProgramStdModulesQualify), so every
// one is a case's.
func listedStdModulesOfCases(nodes []ast.Node) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range importedStdModules(nodes, true) {
		if isProgramStd(name) && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// generateStdlibTestIRWithProgramStd builds the cases of a shared stdlib
// module whose cases import listed std modules (listed). Those modules are
// lowered as a program's files are (GenerateIR, withProgramStd), and the
// cases are built on the module's own gen inside that program
// (Program.stdCases): a call or a type of a listed module resolves as a
// sibling file's, and everything else as the module's cases always do.
func generateStdlibTestIRWithProgramStd(v *stdModuleView, nodes, cases []ast.Node, fa *analysis.FileAnalysis, listed []string, newCasesGen func(*programBuild) *gen, extra []*ir.Module, link bool, hooks DeclineHooks) (*Program, *Result, error) {
	lib := std.Shared()
	p := &Program{linkAllStd: link, Declines: hooks}
	for _, name := range listed {
		stdBodies(name)
		p.Modules = append(p.Modules, Module{
			Path:  lib.SourcePath(name),
			Name:  "std/" + name,
			Nodes: programStdModuleNodes()[name],
			FA:    lib.Files[name],
		})
	}
	p.stdCases = func(b *programBuild) *gen {
		g := newCasesGen(b)
		g.lowerStdCases(cases)
		return g
	}
	// The refusal is the listed modules' own cases, which no module here is
	// the entry of; a body the cases reach and the builder declined blocks
	// its case as anywhere else.
	res, _, err := GenerateIR(p)
	if err != nil {
		return nil, nil, err
	}
	// An edited module's own lowering links after the cached modules, as
	// generateStdlibTestIR links it, and before the program's instances.
	cached := res.sharedStd
	res.irLibraries = append(append(slices.Clip(res.irLibraries[:cached]), extra...), res.irLibraries[cached:]...)
	prog := &Program{
		Modules:  []Module{{Path: v.path, Name: v.module, Nodes: nodes, FA: fa, DeclaresTests: true}},
		HasTests: true,
	}
	return prog, res, nil
}

// stdTestOwnType reports that a test body of a stdlib module is being built.
// There a type the module declares is the std type itself rather than a
// declaration shadowing it, so an arm that declines a locally declared
// `Channel` or `Result` as a possible shadow does not apply. Each arm still
// checks the operands' identity (the channel spec, the prelude spec).
//
// A lambda in the test body is built by a child builder, which is not
// itself a test body (its `return` leaves the lambda), so the test is
// whichever enclosing builder is one. Every body the module's cases gen
// builds is the cases': a test body, or a thunk that dispatches a std type
// a case holds as a std interface value (stdProgThunk).
func (bl *irScalarBuilder) stdTestOwnType() bool {
	if bl.g.stdModule == "" {
		return false
	}
	if bl.g.stdCases {
		return true
	}
	for scope := bl; scope != nil; scope = scope.parent {
		if scope.inTest {
			return true
		}
	}
	return false
}

// testImport admits an `import` at the top of a block (a test body, a `//!`
// case, a function body) as a statement with no effect. Each name it binds is
// one of:
//
//   - a name the module scope does not bind (`import std::duration.Duration`,
//     `import std::io`, `import std::option.Option as Local`). No name-keyed
//     lookup against the module scope finds it, so such a lookup declines,
//     and every use the builder admits resolves by the checker's reference
//     at its position (qualifierScope for a file qualifier).
//   - a name the module scope binds to the same declaration (std/compiler's
//     prompts open with `import std::strings.String`), or to the same module
//     (std/hash's prompts open with `import std::hex`, which the module
//     imports as `hex`).
//   - a whole-module import of the stdlib module whose test body this is,
//     whose alias the builder resolves as that module's own file alias.
//   - a monomorphic std struct, enum or distinct type, whatever the module
//     scope binds the name to (`import std::duration.Duration` beside a local
//     `struct Duration`). A type position is the type the checker recorded
//     there, and a call qualified with it is the function its record names
//     (typeQualCall).
//   - a function, whatever the module scope binds the name to (`import
//     std::io.print` beside a local `fn print` or `once print`). A call or value
//     of the name resolves by the checker's reference at it (blockImported),
//     never by g.funcs.
//   - a file, whatever file the module scope binds the name to (`import
//     std::hex as codec` beside the file's `import std::base64 as codec`). A
//     call or value through the qualifier resolves by the checker's
//     reference at it (qualifierScope), and a type named through it by the
//     checker's reference at the type's name.
//
// Any other import that rebinds a name the module scope binds to something
// else is declined: a name-keyed lookup would answer the module scope's
// binding.
func (bl *irScalarBuilder) testImport(n *ast.ImportStmt) bool {
	g := bl.g
	no := func() bool {
		bl.g.dc.note("an `import` in a block that rebinds a name the module scope binds differently")
		return false
	}
	if g.fa == nil || g.fa.ModuleScope == nil {
		return no()
	}
	bound := 0
	for _, sym := range g.srcFA().Definitions {
		if sym == nil || sym.Node != n {
			continue
		}
		bound++
		if sym.Kind == analysis.SymbolModule && g.stdModule != "" && n.ModuleAlias == nil &&
			len(n.Names) == 0 && importPathString(n) == "std/"+g.stdModule {
			continue
		}
		outer := resolveSymbol(g.fa.ModuleScope.Lookup(sym.Name))
		if outer == nil {
			continue
		}
		real := resolveSymbol(sym)
		if scope := symbolModuleScope(real); scope != nil && scope == symbolModuleScope(outer) {
			continue
		}
		if outer != real && !g.stdTypeRebind(real) && !g.twinOf(real, outer) && !functionRebind(real, outer) && !moduleRebind(real) {
			return no()
		}
	}
	if bound == 0 {
		return no()
	}
	return true
}

// blockImported reports whether the bare name id resolves, by the checker's
// reference at it, through a block `import`. Such a name shadows a module
// declaration of the same spelling for the rest of its block (`import
// std::io.print` in a test body of a file that declares `fn print`), so a
// call or value of it skips the module's functions (g.funcs) and resolves
// as the import does.
func (g *gen) blockImported(id *ast.Ident) bool {
	if g.fa == nil || id == nil {
		return false
	}
	fa := g.srcFA()
	if len(fa.BlockImportScopes) == 0 {
		return false
	}
	for s := fa.References[analysis.Pos{Line: id.Line, Col: id.Col}]; s != nil; s = s.Resolved {
		if imp, ok := s.Node.(*ast.ImportStmt); ok && fa.BlockImportScopes[imp] != nil {
			return true
		}
	}
	return false
}

// functionRebind reports whether a block import binds a function over a
// name the module scope binds to something else (a function or a `once`),
// which blockImported resolves.
func functionRebind(real, _ *analysis.Symbol) bool {
	return real.Kind == analysis.SymbolFunction
}

// moduleRebind reports whether a block import binds a file over a name the
// module scope binds to another file, which qualifierScope resolves.
func moduleRebind(real *analysis.Symbol) bool {
	return real.Kind == analysis.SymbolModule
}

// importPathString is an import's module path as written, `std/compiler`.
func importPathString(n *ast.ImportStmt) string {
	parts := make([]string, len(n.ModulePath))
	for i, seg := range n.ModulePath {
		parts[i] = ast.ImportNodeName(seg)
	}
	return strings.Join(parts, "/")
}

// ownModuleCall answers the bare call a file's call to one of its own
// functions through its own file alias is: `sql.transaction(...)` inside
// std/sql, in a body, a `//!` prompt or a reference editor (only a std file
// may name itself; the checker rejects the spelling elsewhere). The checker
// binds the alias to the file's own module scope, and the function is one
// this gen declares, so the call lowers as `transaction(...)` does,
// generic or not. The copy's callee stands at the field's position, where the checker
// recorded the reference and its instantiated signature; a piped call stays
// the piped call. A function the cached std lowering holds as a sibling
// (stdSiblingDecls) keeps stdOwnFileQualifier's route. In a std module
// checked from edited source, a `//!` prompt's `import std::<module>`
// binds the shared analysis's copy of the module, which is this file all
// the same (namesOwnFile), and its function is this gen's (funcDeclAt
// reads it through ownDecl).
func (bl *irScalarBuilder) ownModuleCall(t *ast.Call, fa *ast.FieldAccess) (*ast.Call, bool) {
	g := bl.g
	obj, isIdent := ast.QualifierIdent(fa.Object)
	if !isIdent || fa.Field == nil || bl.namesLocal(obj) || !g.namesOwnFile(obj) {
		return nil, false
	}
	if decl := g.funcDeclAt(fa.Field.Line, fa.Field.Col); g.sigByDecl[decl] == nil || g.stdSiblingDecls[decl] != nil {
		return nil, false
	}
	// The bare callee stands at the field, whose reference is the
	// function, so it reads no local of the name.
	bare := *t
	bare.Func = &ast.Ident{Name: fa.Field.Name, Line: fa.Field.Line, Col: fa.Field.Col}
	if bl.pipedCall == t {
		bl.pipedCall = &bare
	}
	return &bare, true
}

// ownSiblingCall is ownModuleCall's converse, for a stdlib module the
// process lowers once: a bare call to a top-level function of the module
// the lowering holds as a sibling (stdSiblingDecls, f) is the call through the
// module's own file alias, `encode(b)` in std/hex the call `hex.encode(b)`
// is, whose route places named arguments and defaults (namedQualCall) and,
// in an assertion subject, records the call's rows. In one of the module's
// own test bodies every such call takes it (qualFilePlan through
// stdOwnFileQualifier); in a body of the module only one naming an argument
// does (qualFilePlan through ownSiblingPlan), since a positional one is
// stdFuncPlan's already. The copy's field stands at the callee's position,
// where the checker recorded the reference; a piped call stays the piped
// call, and an Iter.loop's named step its step.
func (bl *irScalarBuilder) ownSiblingCall(t *ast.Call, f *stdFunc, hasNamed bool) (*ast.Call, bool) {
	g := bl.g
	callee := t.Func.(*ast.Ident)
	if f.recv != "" || g.stdModule == "" {
		return nil, false
	}
	alias := g.madeIdent(path.Base(g.stdModule), callee.Line, callee.Col)
	if bl.namesLocal(alias) {
		return nil, false
	}
	if bl.stdTestOwnType() {
		if _, isStd := bl.stdOwnFileQualifier(alias); !isStd {
			return nil, false
		}
	} else if !hasNamed || !bl.ownModuleAlias(alias) {
		return nil, false
	}
	qualified := *t
	qualified.Func = &ast.FieldAccess{
		Object: alias,
		Field:  &ast.Ident{Name: callee.Name, Line: callee.Line, Col: callee.Col},
		Line:   callee.Line, Col: callee.Col,
	}
	if bl.pipedCall == t {
		bl.pipedCall = &qualified
	}
	if bl.signalCall == t {
		// An Iter.loop's named step (irsignalcall.go).
		bl.signalCall = &qualified
	}
	return &qualified, true
}

// ownModuleAlias reports whether owner is this stdlib module's own file
// alias, bound to the module's own scope.
func (bl *irScalarBuilder) ownModuleAlias(owner *ast.Ident) bool {
	g := bl.g
	if g.stdModule == "" || g.fa == nil || owner.Name != path.Base(g.stdModule) {
		return false
	}
	scope := g.qualifierScope(owner)
	return scope != nil && scope == g.fa.ModuleScope
}

// ownSiblingPlan plans `hex.add3(...)` in a body of std/hex (ownSiblingCall
// writes it): the module's own top-level function, a sibling of the
// lowering, as a bare positional call to it plans.
func (bl *irScalarBuilder) ownSiblingPlan(t *ast.Call, args irQualArgs, owner *ast.Ident) *irQualPlan {
	if bl.stdTestOwnType() || !bl.ownModuleAlias(owner) {
		return nil
	}
	fa, _ := t.Func.(*ast.FieldAccess)
	if fa == nil || fa.Field == nil {
		return nil
	}
	f := bl.g.stdSiblingDecls[bl.g.funcDeclAt(fa.Field.Line, fa.Field.Col)]
	if f == nil || f.recv != "" {
		return nil
	}
	return bl.stdFuncPlan(t, args, f)
}

// stdOwnFileQualifier answers a stdlib module's own file alias inside one of
// its test bodies: `compiler.check(...)` in std/compiler. The cached lowering's
// analysis binds that alias to one of the stdlib's module scopes, which is
// what stdFileQualifier matches. A module re-analyzed from edited source (a
// tour reference editor, vmhost.StdlibReference) binds it to its own
// new module scope instead, which is the same module.
func (bl *irScalarBuilder) stdOwnFileQualifier(owner *ast.Ident) (string, bool) {
	g := bl.g
	if !bl.stdTestOwnType() || g.fa == nil || owner.Name != path.Base(g.stdModule) {
		return "", false
	}
	if scope := g.qualifierScope(owner); scope != nil && scope == g.fa.ModuleScope {
		return g.stdModule, true
	}
	return "", false
}
