package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// Sibling FILES: a user's `test_helpers.has_diagnostic(d, s)` reaching the
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
// A Nomi MODULE is the directory tree under a nomi.toml: the distribution and
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
// So aliases (`import test as test_env`) and self-name imports (`import
// todo/foo` from inside `todo`) resolve through the front end that already
// decided them, with nothing re-derived here. Re-deriving Nomi's import resolution in the backend is the mistake
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
// `Int` is the same kind in every file and needs none. See scalarKind, which states
// the same boundary for the stdlib.
//
// # Reference cycles between files
//
// Nomi's analyzer resolves names whole-program, so the file import graph may
// be cyclic and order never matters. The builder gives each file a unit
// package name (package doc, "Unit packages"), and every file of one import
// cycle shares one (component.go).

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
	// defaults is true when any parameter carries a default value. A call
	// that omits one fills it through the declaring unit's accessor
	// (siblingdefault.go), and a function value of it declines.
	defaults bool
	// params0 is the callee's parameter AST, carried so a SHORT call can read
	// the default EXPRESSIONS rather than only know they exist: a call whose
	// callee's file builds no accessor lowers them at the call, from the
	// declaring file's records (lowerNamesFrom).
	params0 []ast.Param
	// generic marks a generic declaration. Its `why` stays set, because no
	// monomorphic signature exists to call; a call site builds an instance
	// in the declaring file instead (siblinggeneric.go).
	generic bool
	// private marks a non-`pub` declaration: callable across files only from
	// its file's partner test (`shapes_test.nomi` for `shapes.nomi`, spec §14
	// *Visibility*). Every other caller refuses it as a private sibling file
	// function, so a builder that resolves names across the program never
	// lowers a call the language rejects.
	private bool
}

func (f *fileFunc) lowerable() bool { return f.why == "" }

// siblingPrivate reports whether g's file may not call unit to's function f
// because f is private to its file and g's file is not that file's partner
// test. It notes the refusal when it answers true.
func (g *gen) siblingPrivate(to int, f *fileFunc) bool {
	if f == nil || !f.private || g.partnerTestOf(to) {
		return false
	}
	g.dc.note("private sibling file function")
	return true
}

// partnerTestOf reports whether g's file is unit to's partner test, by the
// analyzer's rule (analysis.FileAnalysis.PartnerTestOf): the same directory,
// and to's file name with `_test` before `.nomi`. A module key cannot answer
// it, since a test file checked as the entry is keyed by its name alone.
func (g *gen) partnerTestOf(to int) bool {
	x := g.files
	if x == nil || g.fileUnit < 0 || g.fileUnit >= len(x.units) || to < 0 || to >= len(x.units) {
		return false
	}
	return x.units[g.fileUnit].fa.PartnerTestOf(x.units[to].fa)
}

// hasPartnerTest reports whether g's file has its partner test in the
// program, the one other file that may call its private functions.
func (g *gen) hasPartnerTest() bool {
	x := g.files
	if x == nil || g.fileUnit < 0 || g.fileUnit >= len(x.units) {
		return false
	}
	src := x.units[g.fileUnit].fa
	for _, u := range x.units {
		if u != nil && u.fa.PartnerTestOf(src) {
			return true
		}
	}
	return false
}

// fileUnit is one user file.
type fileUnit struct {
	// key is the analyzer's module-path key, and what a refusal names.
	key string
	// scope is the file's ModuleScope, the identity a qualifier resolves to.
	// Nil when the file was checked without an analysis library, in which case
	// nothing can resolve to it and every call into it refuses.
	scope *analysis.Scope
	// fa is the file's analysis, which the partner-test rule reads its path
	// from. Nil when scope is.
	fa *analysis.FileAnalysis
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
// the thing this name resolves to?". Discriminating on which field is set, rather than
// keeping two maps, is what makes it impossible for a call site to find a
// `once` and lower it as a function.
type fileSite struct {
	unit int
	fn   *fileFunc
	once *ast.OnceBinding
	// host marks a user file's `host fn`, whose fn is hostShellOf's shell.
	host bool
}

// fileIndex is every user file in the program.
type fileIndex struct {
	units   []*fileUnit
	byScope map[*analysis.Scope]int
	// byDecl locates a top-level `fn` or `once` by the node that declares it,
	// which is how a mention finds its declaring unit and what it may do with
	// the declaration: a bare one (a selective import, aliased or not) and a
	// qualified one (`file.name`, whose member the checker resolves to the
	// declaration; siblingFuncAt) alike.
	byDecl map[ast.Node]fileSite
	// implMembers locates every impl function in the PROGRAM by the
	// declaration of the type it is for. A type-qualified call in one file
	// reaches another file's impl block through it; see siblingimpl.go, which
	// owns it and states why the question is program-wide.
	implMembers map[implMemberKey][]implMemberSite
	// ownerMembers locates every owner function in the program by the
	// declaration of its owner (a generic type or an interface) and its
	// name: the functions of an inherent block on a generic owner that take
	// no receiver, which no instance of the owner holds (ownerimpl.go). Kept
	// apart from implMembers, which answers for a receiver's impls.
	ownerMembers map[implMemberKey][]implMemberSite
}

// buildFileIndex indexes a program's files. It is called before any file
// is lowered, because a call site in one file asks what another declares.
func buildFileIndex(p *Program, reg *typeRegistry) *fileIndex {
	x := &fileIndex{
		units:   make([]*fileUnit, len(p.Modules)),
		byScope: make(map[*analysis.Scope]int, len(p.Modules)),
		byDecl:  map[ast.Node]fileSite{},
	}
	for i := range p.Modules {
		m := &p.Modules[i]
		u := &fileUnit{key: m.Name}
		if m.FA != nil {
			u.scope = m.FA.ModuleScope
			u.fa = m.FA
		}
		for _, n := range m.Nodes {
			if isSynthesized(n) {
				continue
			}
			switch d := n.(type) {
			case *ast.FuncDef:
				// One declaration, one entry: keyed on the node, so two
				// declarations cannot collide.
				x.byDecl[d] = fileSite{unit: i, fn: fileFuncShell(reg, d, strings.HasPrefix(m.Name, "std/"))}
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
				// A `host fn` is a callable a sibling file may name,
				// so it belongs in this index exactly as a `fn` does. Keyed on
				// the EXTERN node, because that is what the analyzer resolves
				// a reference to; the shell is what the signature and the Go
				// name are read off. See hostpkg.go.
				if !hostTableFn(d) {
					continue
				}
				x.byDecl[d] = fileSite{unit: i, fn: fileFuncShell(reg, hostShellOf(d), false), host: true}
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
	x.indexEditedStdTwins(p)
	return x
}

// indexEditedStdTwins lets a module lowered beside a std module checked from
// edited source (a reference editor's) reach the edited module's functions.
// Such a module (std/storage/testing beside an edited std/storage) was
// checked against the shared analysis, so its `storage.check_key` names the
// shared copy's module scope and FuncDef: each resolves to the edited unit
// and its function of the same name (implFuncTwins), as the type registry
// resolves the shared copy's types.
func (x *fileIndex) indexEditedStdTwins(p *Program) {
	for i := range p.Modules {
		m := &p.Modules[i]
		if m.twins == nil {
			continue
		}
		if name, inStd := strings.CutPrefix(m.Name, "std/"); inStd {
			if shared := std.Shared().Files[name]; shared != nil && shared.ModuleScope != nil {
				if _, dup := x.byScope[shared.ModuleScope]; !dup {
					x.byScope[shared.ModuleScope] = i
				}
			}
		}
		for shared, edited := range m.twins {
			if site, ok := x.byDecl[edited]; ok {
				if _, dup := x.byDecl[shared]; !dup {
					x.byDecl[shared] = site
				}
			}
		}
	}
}

// resolveSignatures types every callable sibling function, once every file's
// type table exists.
//
// Split from buildFileIndex because a signature may name a type declared in ANY
// file, so no signature can be typed until every file's declarations are known.
// The kinds produced belong to the DECLARING gen and
// are translated at the call site through importKind.
func (x *fileIndex) resolveSignatures(gens []*gen) {
	for i := range x.units {
		g := gens[i]
		for _, n := range g.nodes {
			if isSynthesized(n) {
				continue
			}
			// A `host fn`'s signature is typed here too, through the
			// SAME shell buildFileIndex indexed — so the cross-file kinds and
			// the declaring gen's own `g.funcs` entry are resolved from one
			// declaration rather than from two independently built ones. See
			// hostpkg.go.
			var fd *ast.FuncDef
			switch d := n.(type) {
			case *ast.FuncDef:
				fd = d
			case *ast.ExternFunc:
				if !hostTableFn(d) {
					continue
				}
				fd = hostShellOf(d)
			default:
				continue
			}
			f := x.byDecl[n].fn
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
//
// A std module lowered with the program (inStd) is the exception: its
// private functions are what an expansion's hidden import calls
// (`phrase$.compiled`), and the analyzer refuses any other file that names
// one ("`phrase.compiled` is internal to std/phrase"), so a call the
// builder sees is one the language admits.
func fileFuncShell(reg *typeRegistry, fd *ast.FuncDef, inStd bool) *fileFunc {
	f := &fileFunc{name: fd.Name, decl: fd}
	for _, p := range fd.Params {
		if p.Default != nil {
			f.defaults = true
		}
	}
	// A private function lowers in its own file, and the only other file
	// that may call it is that file's partner test, which the call site asks
	// (gen.siblingPrivate).
	f.private = !fd.Public && !inStd
	if len(fd.TypeParams) > 0 || len(fd.WhereClauses) > 0 {
		// No monomorphic signature exists to type or call. A call site
		// instantiates the declaration in its own file's gen at the call's
		// type arguments and calls that instance (siblinggeneric.go).
		f.why, f.generic = "generic sibling file function", true
		return f
	}
	// A `host fn` has no Body and IS lowerable, so the body clause is
	// asked about the shell rather than about the shape. Everything else here
	// still applies to one: it must be `pub` to be reachable and it must not be
	// generic. Its unit builds the accessors a call that omits a defaulted
	// parameter fills it with, as for a `fn` (irHostFnRetain). See
	// hostpkg.go's hostShellOf.
	_, host := hostExtOf(fd)
	if len(fd.Decorators) > 0 || (fd.Body == nil && !host) {
		// Its own file refuses it under `decorator` / `function without a
		// body`, so the whole program is refused either way and the call site
		// reports the same thing a local call to it would — which, since the
		// cascade fix, is nothing.
		f.why = "call to an unlowered function"
		return f
	}
	return f
}

// typeFileFunc types one sibling function's signature in its OWN file's gen.
//
// In the declaring gen and nowhere else: `pub fn build() -> EffectsApp` names
// `EffectsApp` through ITS file's scope, and resolving that name at a caller
// that never imported it would answer nothing — or, worse, answer with a
// DIFFERENT `EffectsApp` the caller does import, which is a silent wrong answer
// rather than a refusal. So the kinds here belong to the declaring gen, and
// a call site translates them into the caller's through importKind. That
// translation is what makes the boundary crossable: a *typeDef belongs to
// exactly one file's gen, which is why the caller gets its own. See
// foreign.go.
//
// The parameter shapes it admits are exactly the ones paramKind resolves, and
// that is a contract rather than a coincidence: paramKind's own doc says it is
// the ONE implementation of "which parameters resolve", so a second walk here
// that refused a shape funcDecl lowers would refuse a call the declaring file
// lowers, for instance a destructuring parameter whose pattern head NAMES a
// type and is its own annotation (`pub fn shift(Point{x, y}) -> Int`).
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
	f.why = "call to an unlowered function"
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

// siblingFuncAt is unit to's function that field, the member of a call or
// reference `file.name`, names: the declaration the checker resolved it to
// (its reference at field, fileIndex.byDecl). Nil when that is no function
// of that unit.
func (g *gen) siblingFuncAt(to int, field *ast.Ident) *fileFunc {
	if g.files == nil || field == nil {
		return nil
	}
	sym := resolveSymbol(g.identRef(field))
	if sym == nil {
		return nil
	}
	site, ok := g.files.byDecl[sym.Node]
	if !ok || site.unit != to {
		return nil
	}
	return site.fn
}

// moduleScopeOf follows a name to the member scope of the file API object it
// is bound to, or nil when it is not bound to one. Only a file import binds
// one: a file is never re-exported, so a selective import's proxy never
// stands for a file.
func moduleScopeOf(fa *analysis.FileAnalysis, name string) *analysis.Scope {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	return symbolModuleScope(fa.ModuleScope.Lookup(name))
}

func symbolModuleScope(sym *analysis.Symbol) *analysis.Scope {
	if sym == nil || sym.Kind != analysis.SymbolModule {
		return nil
	}
	return sym.ModuleScope
}

// identRef is what the identifier id names where the builder lowers it: the
// checker's reference at it (analysis.FileAnalysis.References) in the file
// whose source is lowered (srcFA). It is the one place the builder asks what
// a written name names, whether it stands as a qualifier (`io.print`), a
// callee or a read: a module, a parameter, a binding, a function, a `once`.
//
// It answers nil for an identifier the builder wrote itself (madeIdent):
// such an identifier often stands at a written node's position, where
// References names that node's symbol and not the name the builder wrote,
// so its record is never read. Such an identifier that reads a local
// carries its key (madeRead); a qualifier of one that names no local is
// resolved by its spelling (moduleScopeOf), and nothing else is.
func (g *gen) identRef(id *ast.Ident) *analysis.Symbol {
	if g.unchecked[id] {
		return nil
	}
	return refAt(g.srcFA(), id)
}

// refAt is the checker's reference at the written identifier id in fa.
func refAt(fa *analysis.FileAnalysis, id *ast.Ident) *analysis.Symbol {
	if fa == nil {
		return nil
	}
	return fa.References[analysis.Pos{Line: id.Line, Col: id.Col}]
}

// madeIdent is an identifier the builder writes and lowers, marked
// (markUnchecked) so that identRef never reads the record at its position:
// a qualifier, a callee or an operand of code the builder respells
// (`hex.encode` for a std module's own bare `encode`, `%record.x` over a
// record's fields, a partial application's holes).
func (g *gen) madeIdent(name string, line, col int) *ast.Ident {
	id := &ast.Ident{Name: name, Line: line, Col: col}
	g.markUnchecked(id)
	return id
}

// qualifierScope is the file the qualifier id names: the module the
// checker's reference at it names, a block's `import` (which binds its
// name for the rest of its block only, testImport admits it) as well as
// the file's, and nil when the reference names anything else. A local
// value named like an import is that value: `io = Printer{print: say}`
// then `io.print(s)` calls the local's field, not std/io's print.
func (g *gen) qualifierScope(id *ast.Ident) *analysis.Scope {
	return scopeOfRef(g.srcFA(), g.identRef(id), id.Name)
}

// scopeOfRef is the file a qualifier spelled name, whose reference is ref,
// names in fa: the module ref names, else, for a name with no reference,
// the file its spelling binds in fa's module scope.
func scopeOfRef(fa *analysis.FileAnalysis, ref *analysis.Symbol, name string) *analysis.Scope {
	if ref != nil {
		return symbolModuleScope(ref)
	}
	return moduleScopeOf(fa, name)
}

// namesLocal reports whether id names a value a body binds: a qualifier
// that does is a value whose field a call reads, never a file, and a
// callee that does is called through the value. It is whether id has a
// local's key (readKey): the checker resolved it to a body's binding
// (bodyBinding), or the builder wrote it to read one of its own.
func (bl *irScalarBuilder) namesLocal(id *ast.Ident) bool {
	_, local, _ := bl.readKey(id)
	return local
}

// bodyBinding reports whether sym, a reference the checker recorded in
// this gen's source, is a value a body binds: a parameter (a lambda's and
// an Iter.loop state's among them), a binding (a pattern's and a test's
// among them), or a function declared in a body, which the module scope
// does not hold. A module function is read as this gen's module declares
// it (ownDecl): in a std module checked from edited source, the shared
// analysis's copy of one is the module's own.
func (g *gen) bodyBinding(sym *analysis.Symbol) bool {
	switch sym.Kind {
	case analysis.SymbolParam, analysis.SymbolBinding:
		return true
	case analysis.SymbolFunction:
		fa := g.srcFA()
		fd, isFunc := sym.Node.(*ast.FuncDef)
		if !isFunc || sym.Resolved != nil || fa == nil || fa.ModuleScope == nil {
			return false
		}
		top := fa.ModuleScope.Lookup(sym.Name)
		return top == nil || top.Node != g.ownDecl(fd)
	}
	return false
}

// namesOwnFile reports whether id, a qualifier, names this gen's own file:
// its module scope, or a scope the file index resolves to this unit, which
// for a std module checked from edited source is also the shared analysis's
// copy of the module (indexEditedStdTwins).
func (g *gen) namesOwnFile(id *ast.Ident) bool {
	scope := g.qualifierScope(id)
	if scope == nil {
		return false
	}
	if fa := g.srcFA(); fa != nil && scope == fa.ModuleScope {
		return true
	}
	if g.files == nil {
		return false
	}
	i, ok := g.files.byScope[scope]
	return ok && i == g.fileUnit
}

// lookupQualifier resolves a file qualifier to a sibling's unit index: the
// file qualifierScope names, when it is another file than this one. A
// qualifier naming this file (namesOwnFile) names no sibling.
func (g *gen) lookupQualifier(id *ast.Ident) (int, bool) {
	scope := g.qualifierScope(id)
	if scope == nil || g.files == nil {
		return 0, false
	}
	i, ok := g.files.byScope[scope]
	return i, ok && i != g.fileUnit
}

// stdFileQualifier resolves a qualifier bound to a STDLIB file's API object
// to that file's CANONICAL name, reporting whether it was one at all.
//
// Identified POSITIVELY, against the analyzer's own map of stdlib module
// scopes, rather than by "not one of ours". The negative test is true for a
// std file and also for a user file this index failed to resolve, and the
// second is a bug that would then report itself under the first's name.
//
// The CANONICAL name rather than the spelling, because `import std::timer as t`
// binds `t` and the registry is keyed on the file: reading the local spelling
// would make an aliased import unresolvable and it would then refuse under a
// key that says the extern is missing.
func (g *gen) stdFileQualifier(id *ast.Ident) (string, bool) {
	if g.fa == nil {
		return "", false
	}
	return stdFileOfScope(g.fa, g.qualifierScope(id))
}

// stdFileOfScope is stdFileQualifier's answer for a file's member scope.
func stdFileOfScope(fa *analysis.FileAnalysis, scope *analysis.Scope) (string, bool) {
	if scope == nil {
		return "", false
	}
	for canon, std := range fa.Program().StdlibModuleScopes {
		if isProgramStd(canon) {
			continue
		}
		if std == scope {
			return canon, true
		}
	}
	return "", false
}

// --- call sites -------------------------------------------------------------
