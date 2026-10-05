package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Sibling FILES: a user's `test_helpers.has_diagnostic?(d, s)` reaching the
// builder at all.
//
// # Where sibling files come from
//
// A builder given only the ENTRY file's tree has no callee to lower for a call
// into a sibling file. The stdlib takes its modules from std.Load(); for user
// files, frontend.Checker.CheckFile reports the sibling files the front end has
// already loaded and checked, and Analyze carries each as a Module.
//
// # Terminology
//
// A Nomi MODULE is the nomi.toml + go.mod directory tree: the distribution and
// orphan-rule unit. A FILE is the import-graph node and the VISIBILITY unit.
// `test_helpers.f(x)` is file-qualified access through an imported file API
// object and it reaches free functions only — an interface-impl method reached
// that way is an analyzer error. So nothing here crosses a distribution
// boundary and none of it is a cross-module design problem.
//
// # Resolution is the analyzer's, by pointer
//
// A qualifier is resolved by looking the name up in the file's own
// ModuleScope: the analyzer answers with a SymbolModule whose ModuleScope is
// the imported file's, and that scope pointer is matched against each unit's.
// So aliases (`import test as test_env`), self-name imports (`import
// todo/foo` from inside `todo`) and re-export facades (`import facade.{leaf,
// parser_lib}`, where `leaf` is a binding facade re-exported) all resolve
// through the front end that already decided them, with nothing re-derived
// here. Re-deriving Nomi's import resolution in the backend is the mistake
// irbuild.go's package comment names: a backend on a divergent front end
// reports different answers than `nomi run` for the same file.
//
// # The scalar boundary, and why it is the same one the stdlib has
//
// A cross-file call lowers only when every parameter type and the result type
// is a scalar, or a type a mirror translates. A named type's identity IS the
// *typeDef its declaration produced (types.go), and a *typeDef belongs to
// exactly one file's gen, so two files cannot exchange a struct, enum or
// distinct type without a translation between them (foreign.go's mirrors).
// `Int` is `int64` in every file and needs none. See scalarKind, which states
// the same boundary for the stdlib.
//
// # Reference cycles between files
//
// Nomi's analyzer resolves names whole-program, so the file import graph MAY
// be cyclic and order never matters. The builder assigns each file a Go
// package name in its kind text and tracks which files reference which:
//
//   - The cycle is detected on the cross-file REFERENCE graph, not the import
//     graph. `import` alone adds no edge, so two files that import each other
//     and reference nothing of each other's lower fine.
//   - Every member of a reference cycle shares one package (component.go),
//     and closesGoCycle refuses an edge only when it would still close a
//     cycle across packages, at the position of the reference that closes it.

// fileFunc is one sibling file's top-level function as a call site sees it.
type fileFunc struct {
	name   string
	params []kind
	result kind
	// decl is the DECLARATION this shell describes, and it is here so a call
	// site and the declaring unit can agree on ONE `ir.Symbol` for it.
	//
	// `ir.Table` is per gen, so if `irCalleeSym` in the calling unit and
	// `irFuncShellFor` in the declaring unit each interned a symbol for one
	// declaration, `ir.Module.FuncFor`, which resolves by POINTER, would
	// answer nil for every cross-unit callee. `qualSiblingPlan` interns in the
	// DECLARING gen's table off this node, which is the same rule
	// `resolveSignatures` follows for the kinds ("the kinds produced belong
	// to the DECLARING gen").
	//
	// FOR A `host fn` THIS IS `hostShellOf`'s SYNTHESIZED NODE, not a node the
	// programmer wrote, and the consequence is stated rather than left to be
	// discovered: the declaring unit builds no `ir.Func` for an extern, so
	// interning on it produces a symbol no module declares and the call still
	// reports `LINKING`.
	decl *ast.FuncDef
	// why names the refusal a call site reports when this function cannot be
	// called across a file boundary, and detail qualifies it. Empty when the
	// function is callable.
	//
	// Recorded rather than omitted: a function this builder will not call
	// across files must still be FOUND, so the call site refuses under the
	// reason it was refused for — `private sibling file function` — instead of
	// under "no such function", which would report a different gap.
	why string
	// echo marks a `why` the DECLARING file already reports at the
	// declaration's own position, so the call site DECLINES instead of naming
	// a second construct for one gap.
	//
	// A decorated sibling `pub fn helper` puts `decorator` at the decorator
	// node and the `fn` in helpers.nomi; reporting `call to an unlowered
	// function` at the call in main.nomi as well would count it twice, and a
	// LOCAL call to a decorated function declines. So the call site does what
	// a local call would. A non-scalar signature is echoed the same way: every
	// user file in the import graph is lowered, so that file's own funcDecl
	// refuses the signature at the declaration's position (see typeFileFunc).
	// The remaining two `why` values are call-site-only — a private or generic
	// declaration lowers fine in its own file — so they must still report,
	// which is why this is a flag and not a blanket rule.
	echo bool
	// defaults is true when any parameter carries a default value. A call
	// that omits one fills it through the declaring unit's accessor
	// (siblingdefault.go), and a function value of it declines.
	defaults bool
	// params0 is the callee's parameter AST, carried so a SHORT call can read
	// the default EXPRESSIONS rather than only know they exist. Needed because
	// filling a cross-file default is admissible exactly when every name the
	// expression mentions resolves to the same declaration from both files —
	// portableDefault's question, asked here over a file function's parameters.
	params0 []ast.Param
	// generic marks a generic declaration. Its `why` stays set, because no
	// monomorphic signature exists to call; a call site builds an instance
	// in the declaring file instead (siblinggeneric.go).
	generic bool
}

func (f *fileFunc) lowerable() bool { return f.why == "" }

// fileUnit is one user file, under the Go package name its kind text uses.
type fileUnit struct {
	// key is the analyzer's module-path key, and what a refusal names.
	key string
	pkg string
	// scope is the file's ModuleScope, the identity a qualifier resolves to.
	// Nil when the file was checked without an analysis library, in which case
	// nothing can resolve to it and every call into it refuses.
	scope *analysis.Scope
	funcs map[string]*fileFunc
}

// fileSite is one sibling declaration located by its DECLARATION NODE: which
// unit declares it, and what a reference site may do with it.
//
// Keyed on the node rather than on the name because the two spellings of one
// declaration are not the same string. `import api.{length as len}` binds `len`
// locally while the declaring unit's table is keyed `length`, so a name-keyed
// lookup in the callee's unit misses the alias — and would then refuse under a
// key that says the function does not exist. A type identity spelled as a
// string fails silently, and the same rule applies to a callable.
//
// Exactly one of fn and once is set. Two kinds of declaration share one index
// because they share the question a bare mention asks — "which file declares
// the thing this name resolves to?" — and closeReferences needs the answer for
// both: a bare `once` read emits `nomimodN.NomiOnce_x`, which is a Go import as
// surely as a bare call is. Discriminating on which field is set, rather than
// keeping two maps, is what makes it impossible for a call site to find a
// `once` and lower it as a function.
type fileSite struct {
	unit int
	fn   *fileFunc
	once *ast.OnceBinding
	// host marks a Go-bound `host fn`, whose fn is hostShellOf's shell.
	host bool
}

// fileIndex is every user file in the program, plus the reachability closure
// of the cross-file reference graph.
type fileIndex struct {
	units   []*fileUnit
	byScope map[*analysis.Scope]int
	// byDecl locates a top-level `fn` or `once` by the node that declares it,
	// which is how a BARE mention — a selective import, aliased or not —
	// finds its declaring unit. A qualified mention resolves through byScope
	// instead, because it names the file rather than the declaration.
	byDecl map[ast.Node]fileSite
	// implMembers locates every impl function in the PROGRAM by the
	// declaration of the type it is for. A type-qualified call in one file
	// reaches another file's impl block through it; see siblingimpl.go, which
	// owns it and states why the question is program-wide.
	implMembers map[implMemberKey][]implMemberSite
	// implUnits is which units declare an impl block for which type
	// DECLARATION, read as syntax before any type is resolved. closeReferences
	// needs it to record the Go import edge a type-qualified call emits; see
	// implUnitsByDecl.
	implUnits map[ast.Node][]int
	// reaches[i][j] is true when file i references file j, directly or
	// transitively. reaches[j][i] at an i→j reference site therefore means
	// that reference CLOSES a cycle, which is the exact condition Go rejects.
	reaches [][]bool
}

// buildFileIndex indexes a program's files and closes the reference graph.
//
// Called before any file is lowered, because the answer a call site needs —
// "does this reference close a cycle?" — is a property of the whole program
// and cannot be known from inside one file's lowering.
func buildFileIndex(p *Program, reg *typeRegistry) *fileIndex {
	x := &fileIndex{
		units:     make([]*fileUnit, len(p.Modules)),
		byScope:   make(map[*analysis.Scope]int, len(p.Modules)),
		byDecl:    map[ast.Node]fileSite{},
		implUnits: implUnitsByDecl(p),
	}
	for i := range p.Modules {
		m := &p.Modules[i]
		u := &fileUnit{key: m.Name, pkg: reg.pkgOf[i], funcs: map[string]*fileFunc{}}
		if m.FA != nil {
			u.scope = m.FA.ModuleScope
		}
		for _, n := range m.Nodes {
			if isSynthesized(n) {
				continue
			}
			switch d := n.(type) {
			case *ast.FuncDef:
				if d.ImplFunction {
					continue
				}
				shell := fileFuncShell(reg, d)
				u.funcs[d.Name] = shell
				// One declaration, one entry. A duplicate `fn` name in one
				// file is an analyzer error, so the name-keyed table above
				// cannot lose an entry either — but this map is keyed on the
				// node and so cannot collide at all.
				x.byDecl[d] = fileSite{unit: i, fn: shell}
			case *ast.OnceBinding:
				// No shell: a `once`'s kind is settled in declareOnces, which
				// runs after this index exists, and the declaring gen's own
				// table is the single place it lives. What this index owes a
				// reference site is the UNIT, which is exactly what a
				// reference-graph edge is made of.
				x.byDecl[d] = fileSite{unit: i, once: d}
			case *ast.ImplBlock:
				// An owner-level `once` is found the same way: the reference
				// `Policy.default` resolves to the binding's declaration.
				for _, item := range d.Items {
					if ob, isOnce := item.(*ast.OnceBinding); isOnce {
						x.byDecl[ob] = fileSite{unit: i, once: ob}
					}
				}

			case *ast.ExternFunc:
				// A Go-bound `host fn` is a callable a sibling file may name,
				// so it belongs in this index exactly as a `fn` does. Keyed on
				// the EXTERN node, because that is what the analyzer resolves
				// a reference to; the shell is what the signature and the Go
				// name are read off. See hostpkg.go.
				if !hostCallable(d) {
					continue
				}
				shell := fileFuncShell(reg, hostShellOf(d))
				u.funcs[d.Name] = shell
				x.byDecl[d] = fileSite{unit: i, fn: shell, host: true}
			}
		}
		x.units[i] = u
		// A duplicate scope pointer would make one file answer for two units;
		// first wins and the second is simply unreachable by qualifier, which
		// refuses rather than picking by arrival order.
		if u.scope != nil {
			if _, dup := x.byScope[u.scope]; !dup {
				x.byScope[u.scope] = i
			}
		}
	}
	x.closeReferences(p, reg)
	return x
}

// resolveSignatures types every callable sibling function, once every file's
// type table exists.
//
// Split from buildFileIndex because a signature may name a type declared in ANY
// file, so no signature can be typed until every file's declarations are known
// — and the cycle refusal a type reference can trigger needs the reference graph
// this index already closed. The kinds produced belong to the DECLARING gen and
// are translated at the call site through importKind.
func (x *fileIndex) resolveSignatures(gens []*gen) {
	for i, u := range x.units {
		g := gens[i]
		for _, n := range g.nodes {
			if isSynthesized(n) {
				continue
			}
			// A Go-bound `host fn`'s signature is typed here too, through the
			// SAME shell buildFileIndex indexed — so the cross-file kinds and
			// the declaring gen's own `g.funcs` entry are resolved from one
			// declaration rather than from two independently built ones. See
			// hostpkg.go.
			var fd *ast.FuncDef
			switch d := n.(type) {
			case *ast.FuncDef:
				if d.ImplFunction {
					continue
				}
				fd = d
			case *ast.ExternFunc:
				if !hostCallable(d) {
					continue
				}
				fd = hostShellOf(d)
			default:
				continue
			}
			f := u.funcs[fd.Name]
			if f == nil || f.why != "" {
				continue
			}
			g.typeFileFunc(f, fd)
		}
	}
}

// fileFuncShell projects a top-level declaration onto what a cross-file call
// site may do with it, minus the types — those need every file's type table and
// are filled in by typeFileFunc.
//
// The visibility check is the load-bearing one and it is NOT redundant with
// the analyzer's. `pub` controls cross-file visibility, so a non-`pub`
// top-level declaration is FILE-PRIVATE — but every lowered function links by
// symbol across the program's modules, so a sibling file's private function
// is reachable purely because both files landed in one program, and a builder
// that resolved names without checking `pub` would lower a program the
// language REJECTS. That is invisible to any output comparison: such a program
// has no recorded output, so there is no second observation to disagree with.
// It is a divergence in the ANALYZER's direction, and a named refusal is the
// only thing that catches it.
func fileFuncShell(reg *typeRegistry, fd *ast.FuncDef) *fileFunc {
	f := &fileFunc{name: fd.Name, decl: fd}
	for _, p := range fd.Params {
		if p.Default != nil {
			f.defaults = true
		}
	}
	if !fd.Public {
		f.why = "private sibling file function"
		return f
	}
	if len(fd.TypeParams) > 0 || len(fd.WhereClauses) > 0 {
		// No monomorphic signature exists to type or call. A call site
		// instantiates the declaration in its own file's gen at the call's
		// type arguments and calls that instance (siblinggeneric.go).
		f.why, f.generic = "generic sibling file function", true
		return f
	}
	// A Go-bound `host fn` has no Body and IS lowerable, so the body clause is
	// asked about the shell rather than about the shape. Everything else here
	// still applies to one: it must be `pub` to be reachable and it must not be
	// generic. A call omitting one of its defaulted parameters declines, since
	// its unit builds no default accessors. See hostpkg.go's hostShellOf.
	_, host := hostExtOf(fd)
	if len(fd.Decorators) > 0 || (fd.Body == nil && !host) {
		// Its own file refuses it under `decorator` / `function without a
		// body`, so the whole program is refused either way and the call site
		// reports the same thing a local call to it would — which, since the
		// cascade fix, is nothing. See fileFunc.echo.
		f.why, f.echo = "call to an unlowered function", true
		return f
	}
	return f
}

// typeFileFunc types one sibling function's signature in its OWN file's gen.
//
// In the declaring gen and nowhere else: `pub fn build(): EffectsApp` names
// `EffectsApp` through ITS file's scope, and resolving that name at a caller
// that never imported it would answer nothing — or, worse, answer with a
// DIFFERENT `EffectsApp` the caller does import, which is a silent wrong answer
// rather than a refusal. So the kinds here belong to the declaring package, and
// a call site translates them into the caller's through importKind. That
// translation is what makes the boundary crossable: a *typeDef belongs to
// exactly one file's gen, which is why the caller gets its own. See
// foreign.go.
//
// The parameter shapes it admits are exactly the ones paramKind resolves, and
// that is a contract rather than a coincidence: paramKind's own doc says it is
// the ONE implementation of "which parameters resolve", so a second walk here
// that refused a shape funcDecl lowers would refuse a call the declaring file
// compiles, for instance a destructuring parameter whose pattern head NAMES a
// type and is its own annotation (`pub fn shift(Point{x, y}): Int`).
//
// A signature this builder cannot type is ECHOED, not reported. Every user file
// in the import graph is lowered, so that file's own funcDecl already names the
// TYPE's gap at the declaration's position — `stdlib type`, `unlowered type`,
// `generic type` — and a second construct at the call site would count one
// obstacle twice under a name nobody can lower.
//
// The key survives for the ONE case the echo cannot cover: an annotation typeOf
// refuses and typeRefusal cannot name, where the declaring file's funcDecl
// declines too and nothing would be reported at all. That agreement is held by
// a TEST (TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses) rather than by
// the type system, so the fence is re-armed here at the seam instead of being
// removed on the strength of it.
func (g *gen) typeFileFunc(f *fileFunc, fd *ast.FuncDef) {
	for _, p := range fd.Params {
		k := g.paramKind(p)
		// kindInvalid: cascade — refuseFileSig echoes the declaring file's own report.
		if k == kindInvalid {
			g.refuseFileSig(f, p.TypeAnnotation)
			return
		}
		f.params = append(f.params, k)
	}
	f.result = kindUnit
	if fd.ReturnTypeExpr != nil {
		f.result = g.typeOf(fd.ReturnTypeExpr)
		// kindInvalid: cascade — as above, for the return annotation.
		if f.result == kindInvalid {
			g.refuseFileSig(f, fd.ReturnTypeExpr)
			return
		}
	}
}

// refuseFileSig records why a sibling function's signature is uncallable, and
// whether the call site should DECLINE rather than name it.
//
// `te` is the annotation that failed, or nil when the parameter had none —
// which funcDecl reports as `parameter without a declared type` and
// patternParamKind as `destructuring parameter`, both unconditionally, so the
// echo is safe there without asking anything.
func (g *gen) refuseFileSig(f *fileFunc, te ast.TypeExpr) {
	f.params = nil
	if te != nil {
		if _, _, named := g.typeRefusal(te); !named {
			f.why = "non-scalar sibling file signature"
			return
		}
	}
	f.why, f.echo = "call to an unlowered function", true
}

// closeReferences records which files reference which, and closes the
// relation transitively.
//
// The edge set is deliberately a SUPERSET of what lowering references: every
// `owner.member` where owner resolves to another file counts, whether or not
// the site is a call, whether or not it is inside a subtree that will be
// refused, and whether or not `owner` happens to be shadowed by a local at
// that point. Over-counting can only over-refuse a cycle; under-counting
// would miss one.
//
// A BARE name is an edge on the same footing as `owner.member`, and it has to
// be: `import api.{make}` then `make("Ada")` spells `nomimodN.Nomi_make`, which
// references the file as surely as `api.make("Ada")` does; once.go's
// importedOnce depends on this edge. Because a bare Ident is
// looked up in the file's MODULE scope, a local variable that shadows an
// imported name contributes an edge it does not really need; that is the same
// over-counting the shadowed-`owner` case above already accepts, for the same
// reason, and it can only over-refuse a cycle.
//
// A TYPE reference is an edge on the same footing, and it has to be: naming
// another file's struct spells `nomimodN.NomiT_X`, which references the file as
// surely as calling its function does. With mirrors, leaving type references
// out would let file A call into B while B names A's type, a cycle the graph
// would miss.
// The names are collected reflectively (foreign.go's typeNamesIn) rather than
// from the import statements, so an alias, a re-export facade and a selective
// import all resolve through the front end that already decided them.
//
// Scoped field reads also name the field's declared value type in their kind.
// Their type-origin edges are included in both this graph and nomiImportGraph,
// so component partitioning accounts for imports absent from the source text.
func (x *fileIndex) closeReferences(p *Program, reg *typeRegistry) {
	n := len(x.units)
	x.reaches = make([][]bool, n)
	for i := range x.reaches {
		x.reaches[i] = make([]bool, n)
	}
	for i := range p.Modules {
		scopedTypeEdges(p, i, func(j int) { x.reaches[i][j] = true })
		fa := p.Modules[i].FA
		if fa == nil {
			continue
		}
		names := map[string]bool{}
		for _, node := range p.Modules[i].Nodes {
			x.walkReferences(fa, i, node)
			typeNamesIn(node, names)
			typeQualifierNamesIn(node, names)
		}
		if reg == nil {
			continue
		}
		for name := range names {
			sym := resolvedTypeSymbol(fa, name)
			if sym == nil || sym.Node == nil {
				continue
			}
			if o := reg.byDecl[sym.Node]; o != nil && o.unit != i {
				x.reaches[i][o.unit] = true
			}
		}
	}
	// Warshall: n is the number of files in one program's import graph, so
	// the cubic closure is a few thousand operations at the outside.
	for k := range n {
		for i := range n {
			if !x.reaches[i][k] {
				continue
			}
			for j := range n {
				if x.reaches[k][j] {
					x.reaches[i][j] = true
				}
			}
		}
	}
}

func (x *fileIndex) walkReferences(fa *analysis.FileAnalysis, from int, n ast.Node) {
	if isNilNode(n) {
		return
	}
	switch t := n.(type) {
	case *ast.FieldAccess:
		if owner, named := t.Object.(*ast.Ident); named {
			if to, ok := x.lookupQualifier(fa, owner); ok && to != from {
				x.reaches[from][to] = true
			}
		}
		if owner, isType := t.Object.(*ast.TypeIdent); isType {
			// `T.member` may resolve to an impl block in ANY file of the
			// module, including one this file neither imports nor mentions
			// otherwise — the orphan rule admits `impl I for T` in T's module
			// and a Nomi module is several FILES. So the Go import edge a
			// type-qualified call emits is not implied by any edge above, and it
			// can be implied by nothing at all: `thing.nomi` with
			// a bare `import alpha` that references nothing of alpha, plus
			// `impl Alpha for Thing` in alpha.nomi, resolves `Thing.go` from a
			// third file which mentions only `thing`. Every recorded edge
			// there is to `thing`, and Go would still be handed
			// main -> alpha. Under-counting is the direction this file refuses
			// to be wrong in; see the doc comment. See siblingimpl.go.
			for _, to := range x.implUnitsFor(fa, owner.Name) {
				if to != from {
					x.reaches[from][to] = true
				}
			}
		}
	case *ast.Ident:
		if site, ok := x.lookupBare(fa, t); ok && site.unit != from {
			x.reaches[from][site.unit] = true
		}
	}
	for _, child := range childNodes(n) {
		x.walkReferences(fa, from, child)
	}
}

// implUnitsFor is every unit declaring an impl block for the type `name`
// resolves to, from this file's point of view.
//
// Resolved through the analyzer and then keyed on the DECLARATION NODE, so two
// files each declaring a `Point` do not answer for each other — fileSite's
// rule, and the same one implMemberKey follows.
func (x *fileIndex) implUnitsFor(fa *analysis.FileAnalysis, name string) []int {
	sym := resolvedTypeSymbol(fa, name)
	if sym == nil || sym.Node == nil {
		return nil
	}
	return x.implUnits[sym.Node]
}

// implUnitsByDecl indexes which units declare an impl block for which type
// declaration, before any type is resolved.
//
// Before, because closeReferences needs it and closeReferences runs before the
// first gen exists. So the receiver is read as SYNTAX — every type name it
// mentions, resolved through the declaring file's own scope — rather than as a
// kind. Deliberately a SUPERSET for closeReferences' stated reason: a name in a
// receiver this builder will refuse still counts, because over-counting can
// only over-refuse a cycle while under-counting misses one.
func implUnitsByDecl(p *Program) map[ast.Node][]int {
	out := map[ast.Node][]int{}
	for i := range p.Modules {
		fa := p.Modules[i].FA
		if fa == nil {
			continue
		}
		for _, node := range p.Modules[i].Nodes {
			ib, ok := node.(*ast.ImplBlock)
			if !ok || isNilNode(ib.Receiver) {
				continue
			}
			names := map[string]bool{}
			typeNamesIn(ib.Receiver, names)
			for name := range names {
				sym := resolvedTypeSymbol(fa, name)
				if sym == nil || sym.Node == nil {
					continue
				}
				if !slices.Contains(out[sym.Node], i) {
					out[sym.Node] = append(out[sym.Node], i)
				}
			}
		}
	}
	return out
}

// lookupBare resolves a BARE name to the sibling `fn` declaration it names,
// through the analyzer's own proxy chain and then by declaration node.
//
// The node is the identity and the name is not: `import api.{length as len}`
// binds `len` to a proxy whose Resolved carries the declaring file's
// *ast.FuncDef, whose own Name is `length`. Reading the local spelling would
// miss every alias, and reading the resolved NAME would then have to guess
// which file to read it in.
//
// Answers for a name the file declares ITSELF too, and that is correct rather
// than incidental: the module scope resolves a local top-level `fn` to its own
// declaration node, so the site reports this same unit and every caller
// compares against its own index before treating the answer as cross-file.
func (x *fileIndex) lookupBare(fa *analysis.FileAnalysis, id *ast.Ident) (fileSite, bool) {
	sym := resolvedBareSymbolAt(fa, id)
	if sym == nil || sym.Node == nil {
		return fileSite{}, false
	}
	site, ok := x.byDecl[sym.Node]
	return site, ok
}

// moduleScopeOf follows a name to the member scope of the file API object it
// is bound to, or nil when it is not bound to one.
//
// The chain is followed rather than read once because a SELECTIVE import
// records a PROXY symbol whose own ModuleScope is nil and whose Resolved is
// the real binding. That is how a re-export facade resolves: the test file
// writes `import facade.{leaf, parser_lib}`, facade writes `import leaf
// export`, and `leaf` in the test's scope is a proxy onto facade's binding,
// whose ModuleScope is leaf.nomi's own. Reading only the proxy would answer
// nil and report the whole facade as a stdlib call, a mis-NAMED refusal,
// which is worse than a missing one because it points at the wrong cause.
//
// Bounded because a proxy chain is data, not a bounded shape.
func moduleScopeOf(fa *analysis.FileAnalysis, name string) *analysis.Scope {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	return symbolModuleScope(fa.ModuleScope.Lookup(name))
}

func symbolModuleScope(sym *analysis.Symbol) *analysis.Scope {
	for range 8 {
		if sym == nil || sym.Kind != analysis.SymbolModule {
			return nil
		}
		if sym.ModuleScope != nil {
			return sym.ModuleScope
		}
		sym = sym.Resolved
	}
	return nil
}

// qualifierScope is moduleScopeOf for the qualifier written at id. A name the
// module scope does not bind may still name a file: `import std/io` at the
// top of a block binds `io` in that block only (testImport admits it), and
// the checker's reference at id is that import's symbol.
func qualifierScope(fa *analysis.FileAnalysis, id *ast.Ident) *analysis.Scope {
	if scope := moduleScopeOf(fa, id.Name); scope != nil || fa == nil {
		return scope
	}
	return symbolModuleScope(fa.References[analysis.Pos{Line: id.Line, Col: id.Col}])
}

// lookupQualifier resolves a file qualifier to a unit index, using the
// analyzer's own answer for what the name is bound to.
func (x *fileIndex) lookupQualifier(fa *analysis.FileAnalysis, id *ast.Ident) (int, bool) {
	scope := qualifierScope(fa, id)
	if scope == nil {
		return 0, false
	}
	i, ok := x.byScope[scope]
	return i, ok
}

// stdFileQualifier resolves a name bound to a STDLIB file's API object to that
// file's CANONICAL name, reporting whether it was one at all.
//
// Identified POSITIVELY, against the analyzer's own map of stdlib module
// scopes, rather than by "not one of ours". The negative test is true for a
// std file and also for a user file this index failed to resolve, and the
// second is a bug that would then report itself under the first's name.
//
// The CANONICAL name rather than the spelling, because `import std/timer as t`
// binds `t` and the registry is keyed on the file: reading the local spelling
// would make an aliased import unresolvable and it would then refuse under a
// key that says the extern is missing. Same correction canonicalStdFile makes
// for the permanence classifier.
func stdFileQualifier(fa *analysis.FileAnalysis, id *ast.Ident) (string, bool) {
	if fa == nil {
		return "", false
	}
	return stdFileOfScope(fa, qualifierScope(fa, id))
}

// stdFileOfScope is stdFileQualifier's answer for a file's member scope.
func stdFileOfScope(fa *analysis.FileAnalysis, scope *analysis.Scope) (string, bool) {
	if scope == nil {
		return "", false
	}
	for canon, std := range fa.StdlibModuleScopes {
		if std == scope {
			return canon, true
		}
	}
	return "", false
}

// --- call sites -------------------------------------------------------------

// typeQualifierNamesIn collects every type name that appears only as the
// QUALIFIER of a field access, which is the one position `typeNamesIn` cannot
// see.
//
// `typeNamesIn` walks TYPE expressions — annotations, field types, signatures
// — and a variant reached through its own enum's name appears in none of
// them. The lowered access's kind names that enum's type, so it is a
// reference edge with nothing in the type positions behind it.
//
// A DOTTED NAME IS ONE NAME SPELLED WITH A DOT, and that is why it has to be
// reassembled here rather than read off a single node. `Signal.Level.Low` is
// a `FieldAccess` whose object is a `FieldAccess` whose object is the
// `*ast.TypeIdent` `Signal`, so the chain has to be joined back up: `Signal`,
// then `Signal.Level`, then `Signal.Level.Low`. On the entry of
// `14-modules-and-packaging/dotted_type_names/`, a walk without the join
// collects exactly the wrong pieces:
//
//	typeNamesIn        Level, Signal, Probe, Probe.Local, Probe.Reading, …
//	resolvedTypeSymbol Signal -> nil   Level -> nil   Signal.Level -> FOUND
//
// `Probe.Reading` is in the type set because an annotation spells it; nothing
// in that file annotates `Signal.Level` at all, and the entry says so in its
// own words — "No annotation anywhere spells `Signal.Level` — the qualifier
// is the only mention, and it is a use".
//
// The entry's reference to `Signal.Level` would then be missing from the
// graph. That program would still be right only because `signal.nomi`
// references nothing of the entry; a missing edge is the under-count
// `closeReferences`' header says this file must not make.
//
// Not restricted to a dotted name: the plain `Json.Null` shape is the same
// position and the same reference. A plain enum name nearly always also
// appears in an annotation somewhere in the file, which is the coincidence a
// dotted one removes.
//
// Over-counting is the safe direction — an extra edge can only over-refuse a
// cycle or over-merge a component — so every prefix of the chain is recorded
// and resolution is left to `closeReferences`' existing `resolvedTypeSymbol`
// lookup, which answers nil for a name that is not a type.
func typeQualifierNamesIn(n ast.Node, out map[string]bool) {
	if isNilNode(n) {
		return
	}
	if f, isField := n.(*ast.FieldAccess); isField {
		for _, name := range typeChainNames(f) {
			out[name] = true
		}
	}
	for _, child := range childNodes(n) {
		typeQualifierNamesIn(child, out)
	}
}

// typeChainNames is every dotted prefix of a field-access chain rooted at a
// type name, longest last, or nil when the root is not a type name.
//
// The outermost access is passed in and the chain is walked INWARD, so the
// segments come out reversed and are re-reversed here. Called once per
// `*ast.FieldAccess` the walk meets, including the inner ones, which is
// redundant work producing prefixes the outer call already produced — and
// cheaper than threading "am I the top of a chain" through the walk for a set
// that dedupes anyway.
func typeChainNames(f *ast.FieldAccess) []string {
	var fields []string
	node := f
	for {
		if node.Field == nil {
			return nil
		}
		fields = append(fields, node.Field.Name)
		switch inner := node.Object.(type) {
		case *ast.FieldAccess:
			node = inner
		case *ast.TypeIdent:
			if inner.Name == "" {
				return nil
			}
			names := make([]string, 0, len(fields)+1)
			name := inner.Name
			names = append(names, name)
			for i := len(fields) - 1; i >= 0; i-- {
				if fields[i] == "" {
					return names
				}
				name += "." + fields[i]
				names = append(names, name)
			}
			return names
		default:
			return nil
		}
	}
}
