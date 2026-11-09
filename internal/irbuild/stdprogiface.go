package irbuild

import (
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A STD INTERFACE HELD AS A VALUE, for every std interface stdiface.go has no
// rt table for: `h: Hashable = p`, `m: Matcher = "a"`, `c: Contains<Int> =
// bag`.
//
// The VM needs no rt table to dispatch: a value held as an interface is the
// concrete value itself, and a dispatched call names the interface method's
// symbol, which the program's modules map to a body per run-time type name
// (ir.Module.Implement, vm.dispatchTarget). So a std interface is lowered as
// a program interface is, with three differences:
//
//   - THE DEF IS BUILT FROM STD'S DECLARATION, in each gen that names the
//     interface, as ifaceInstAt builds a generic interface's instance: the
//     same resolveIface over the declaration, so its method kinds are this
//     gen's. The declaration is std's source, so while it resolves srcFA
//     answers std's file (withStdSrc) and a type expression naming no
//     substituted parameter resolves in std's scope (stdSrcTypeOf): std's
//     `Json` stays std's in a file that declares its own. A type the anchors
//     miss is not resolved through a name this file binds to another
//     declaration (stdSrcShadowed). A generic
//     interface's def is its template, instantiated per type arguments by
//     ifaceInstAt.
//   - THE METHOD SYMBOLS ARE THE PROGRAM'S, not the gen's: no unit declares
//     the interface, so every unit interns them in the first unit's table
//     (programSymbol), keyed by the declaration and, for an instance, its
//     portable arguments. A value boxed in one file then dispatches to an
//     impl recorded in another.
//   - A STD TYPE'S IMPL is no impl block of the program. A value of a std
//     type enters as a program interface's would: a builtin scalar or a std
//     struct or enum unboxed (stdProgHeld), a container or leaf boxed under
//     its type's name (irBoxPlan). Each of its dispatch entries is a thunk
//     the program builds whose body is the direct call `Iface.method(...)`
//     with a value of that type (stdProgThunk), so the entry reaches
//     whatever that call reaches: a container family's operation, an
//     instance of std's generic body, a retained body or a host function.
//
// Identity is the declaration node, reached the way a program reaches it:
// the name resolves in this file's scope to an `*ast.InterfaceDef` whose
// solved type's origin is a std module, after this file's own and its
// siblings' interfaces have had their say (ifaceNamed first, as
// stdIfaceNamed asks). `std` is unforgeable as a module name
// (analysis/manifest.go), and a std module lowered with the program
// (programStdModules) is excluded: its interfaces are the program's own.
//
// Only a program's gens build these defs: its files, the std modules
// lowered with it, and a shared std module's own cases (stdCases). The
// stdlib lowered once for the process holds no program symbol, so a shared
// std function whose signature names one of these interfaces
// (`String.split(s, separator: Matcher)`) is not retained there: the index
// marks it stdProgSigWhy, and a program builds it as an instance with no
// type arguments (stdinst.go). That instance's gen has no defs of its own.
// Its caller is stdProgCaller, and every def it asks for is the caller's:
// by origin and name (stdProgIfaceAt), which the program's std scope
// answers first, since a program may read std's declarations from the
// stored interfaces while the instance reads the shared analysis's; and
// for an interface the instance's own module declares (`ToJson` in
// `Json.encode_as`), in place of the module's local def (stdProgOwnIface).
// A std type the instance holds as one is recorded, and its thunks built,
// by the caller (stdProgBinds), so the parameter, the body's dispatch and
// the dispatch entries name one def and the program's symbols.

// stdIfaceMethodToken is a std interface method's dispatch symbol in the
// program table: the declaration and the method's name.
type stdIfaceMethodToken struct {
	decl   *ast.InterfaceDef
	method string
}

// programSymbol interns token in the program's one table for symbols no unit
// declares: the first unit's, or this gen's when there is no program.
func (g *gen) programSymbol(token any, name string) *ir.Symbol {
	if c := g.stdProgCaller; c != nil {
		return c.programSymbol(token, name)
	}
	if g.reg != nil && len(g.reg.gens) > 0 && g.reg.gens[0] != nil {
		return g.reg.gens[0].irTypes().Symbol(token, name)
	}
	return g.irTypes().Symbol(token, name)
}

// stdProgMethodSym is the dispatch symbol of std interface d's method m, or
// nil when d is no std interface's def.
func (g *gen) stdProgMethodSym(d *ifaceDef, m *ifaceMethod) *ir.Symbol {
	switch {
	case d.stdOrigin == "":
		return nil
	case d.instOf != nil:
		token := instMethodToken{tpl: d.instOf, args: portableArgsKey(d.instArgs), method: m.name}
		return g.programSymbol(token, ifaceInstLabel(d)+"."+m.name)
	}
	return g.programSymbol(stdIfaceMethodToken{decl: d.decl, method: m.name}, d.nomi+"."+m.name)
}

// stdProgBuilds reports whether this gen has defs for std interfaces:
// a program's own gens, the std modules lowered with it and a shared std
// module's cases (stdCases), and a std instance built for one of those,
// which uses its caller's (stdProgCaller); never the stdlib lowered once
// for the process.
func (g *gen) stdProgBuilds() bool {
	if g.stdProgCaller != nil {
		return true
	}
	return (g.stdModule == "" || g.programStd || g.stdCases) && g.stdInstCur == nil
}

// stdProgIfaceNamed is the def of the std interface a bare name names in
// this file, or nil.
func (g *gen) stdProgIfaceNamed(name string) *ifaceDef {
	if !g.stdProgBuilds() || g.fa == nil {
		return nil
	}
	if d, imported := g.blockImportedIface(name); imported {
		// A block's import binds the name (bindBlockImports).
		if d.stdOrigin != "" {
			return d
		}
		return nil
	}
	if _, local := g.ifaceNamed(name); local {
		return nil
	}
	if _, rt := g.stdIfaceNamed(name); rt {
		return nil
	}
	return g.stdProgIfaceOf(resolvedTypeSymbol(g.fa, name))
}

// stdProgOwnIface is, in a std instance built for a program
// (stdProgCaller), the caller's def of d, an interface the instance's own
// std module declares, when a program holds it as a value through its own
// def (stdProgIfaceOf), or nil: a value of it crosses the call as the
// caller's existential, so the instance's parameters and body name the
// same kind.
func (g *gen) stdProgOwnIface(d *ifaceDef) *ifaceDef {
	c := g.stdProgCaller
	if c == nil || d == nil || d.decl == nil || g.stdModule == "" || isProgramStd(g.stdModule) {
		return nil
	}
	pd := c.stdProgIfaceAt("std/"+g.stdModule, d.decl.Name)
	if pd == nil || !pd.lowerable {
		return nil
	}
	return pd
}

// stdProgHere is k, a kind another gen resolved, with a std interface it
// holds as a value (not a generic one's instance) replaced by this gen's
// def of the same declaration. A std instance with such a parameter is
// built once for the program, at its first caller's def, and each file
// passes the value at its own: the VM's existential is the value, and the
// dispatch symbols are the program's.
func (g *gen) stdProgHere(k kind) kind {
	if k.tag != tagIface || k.iface == nil || k.iface.stdOrigin == "" || k.iface.instOf != nil || k.iface.decl == nil {
		return k
	}
	if d := g.stdProgIfaceAt(k.iface.stdOrigin, k.iface.decl.Name); d != nil && d.lowerable {
		return existential(d)
	}
	return k
}

// stdProgQualifiedIface is the def of the std interface a module-qualified
// name (`matcher.Matcher`) names, read through the qualifier's scope, or
// nil.
func (g *gen) stdProgQualifiedIface(qualifier, name string) *ifaceDef {
	if !g.stdProgBuilds() {
		return nil
	}
	return g.stdProgIfaceOf(resolvedScopeSymbol(g.moduleScope(qualifier), name))
}

// stdProgIfaceOf is the def of the std interface sym resolves to, built on
// first use, or nil when sym is none. An interface with an rt table
// (stdiface.go), std's structural `Struct` and std's `Iter` keep their own
// representations.
func (g *gen) stdProgIfaceOf(sym *analysis.Symbol) *ifaceDef {
	if sym == nil {
		return nil
	}
	if c := g.stdProgCaller; c != nil {
		// An instance's interface value is its caller's, so the call and
		// the body agree on its kind. The instance reads std's declarations
		// from the shared lowering's analysis, so the declaration is found
		// again by its origin and name, as the caller projects a checked
		// type (stdProgSolved).
		it, isIface := sym.Type.(*analysis.InterfaceType)
		if !isIface {
			return nil
		}
		return c.stdProgIfaceAt(it.Origin, it.Name)
	}
	decl, isDecl := sym.Node.(*ast.InterfaceDef)
	it, isIface := sym.Type.(*analysis.InterfaceType)
	if !isDecl || !isIface || !strings.HasPrefix(it.Origin, "std/") {
		return nil
	}
	module := strings.TrimPrefix(it.Origin, "std/")
	if isProgramStd(module) || decl.Name == structIfaceName || decl.Name == "Iter" {
		return nil
	}
	if d := g.stdProgIfaces[decl]; d != nil {
		return d
	}
	g.loadStdIfaces()
	if i, rt := g.stdIfaceByName[decl.Name]; rt && stdIfaceSpecs[i].module == it.Origin {
		return nil
	}
	d := &ifaceDef{
		nomi:      decl.Name,
		decl:      decl,
		methods:   map[string]*ifaceMethod{},
		lowerable: true,
		unit:      -1,
		stdOrigin: it.Origin,
	}
	if g.stdProgIfaces == nil {
		g.stdProgIfaces = map[*ast.InterfaceDef]*ifaceDef{}
	}
	g.stdProgIfaces[decl] = d
	switch {
	case len(decl.WhereClauses) > 0:
		// An interface-level where clause is a constraint no dispatch
		// checks, as ifaceTemplate refuses one of a program's own.
		d.lowerable, d.why = false, "std interface signature"
	case len(decl.TypeParams) > 0:
		// A template, instantiated per type arguments (stdProgInst), as
		// declareIfaces marks a program's own.
		d.lowerable, d.why, d.useSiteOnly = false, "generic interface", true
	default:
		g.resolveIface(d)
	}
	return d
}

// stdSrcTypeOf is te, written in the std file being resolved (stdSrc),
// resolved in that module's scope (stdTypeKind), when it names no type
// parameter this gen substitutes and the module's anchors resolve it.
func (g *gen) stdSrcTypeOf(te ast.TypeExpr) (kind, bool) {
	names := map[string]bool{}
	walkNames(te, names, pickAnyName)
	for name := range names {
		if _, subst := g.genericSubstKind(name); subst {
			return kindInvalid, false
		}
	}
	anchors, cached := g.stdSrcAnchors[g.stdSrc]
	if !cached {
		anchors = stdAnchorsOf(g.stdSrc)
		if g.stdSrcAnchors == nil {
			g.stdSrcAnchors = map[*analysis.FileAnalysis]stdAnchors{}
		}
		g.stdSrcAnchors[g.stdSrc] = anchors
	}
	// kindInvalid: lookup — a miss answers false, and the caller resolves the type its usual way, which refuses what nothing resolves.
	if k := stdTypeKind(te, anchors); k != kindInvalid {
		return k, true
	}
	return kindInvalid, false
}

// withStdSrc makes std module's file the source srcFA answers, for
// resolving the signatures of an interface it declares (resolveIface), and
// returns what restores the previous source. `Maybe<String>` in `Matcher.find_in` is
// resolved by the position std wrote it at (preludeTypeOf), which only std's
// file records; this file's maps hold nothing, or something else, there.
func (g *gen) withStdSrc(module string) func() {
	prev := g.stdSrc
	if lib := stdAnchorLib(); lib != nil {
		g.stdSrc = lib.Files[module]
	}
	return func() { g.stdSrc = prev }
}

// stdSrcShadowed reports whether te, written in the std file being
// resolved (stdSrc) and missed by its anchors (stdSrcTypeOf), is headed by a
// name this file binds to a different declaration than std's file does.
// The arms typeOf falls through to look a head name up in this gen's
// tables, where this file's own `Json` would stand in for std's; nominal
// identity is the declaring file, so such a name is refused here and its
// function is a gap of its own, while the interface still lowers. A name
// this file does not bind is std's alone (`k: ToJson = xs` needs no import
// of `Json`), and a type parameter being substituted is this gen's.
// Components are asked on their own as typeOf recurses into them.
func (g *gen) stdSrcShadowed(te ast.TypeExpr) bool {
	var name string
	switch t := te.(type) {
	case *ast.SimpleType:
		name = t.Name
	case *ast.GenericType:
		name = t.Name
	case *ast.QualifiedType:
		name = t.Module
	default:
		return false
	}
	if _, subst := g.genericSubstKind(name); subst {
		return false
	}
	here := resolvedTypeSymbol(g.fa, name)
	if here == nil {
		return false
	}
	there := resolvedTypeSymbol(g.stdSrc, name)
	return there == nil || there.Node != here.Node
}

// stdProgTemplate is the declaration of a std generic interface's template
// def, or nil.
func stdProgTemplate(d *ifaceDef) *ast.InterfaceDef {
	if d == nil || d.stdOrigin == "" || d.instOf != nil || d.why != "generic interface" {
		return nil
	}
	return d.decl
}

// stdProgInst is the std generic interface tpl's instance at subst.
func (g *gen) stdProgInst(tpl *ast.InterfaceDef, subst map[string]kind) *ifaceInst {
	if d := g.stdProgIfaces[tpl]; d == nil || stdProgTemplate(d) == nil {
		return nil
	}
	return g.ifaceInstAt(tpl, subst)
}

// stdProgIfaceForImpl is the def an impl block of a std interface binds
// into: the interface's, or for a generic one the instance at the block's
// type arguments. nil when the block's interface is none of these.
func (g *gen) stdProgIfaceForImpl(ib *ast.ImplBlock) *ifaceDef {
	var d *ifaceDef
	switch te := ib.Interface.(type) {
	case *ast.QualifiedType:
		if qualifier, name, ok := qualifiedTypeParts(te); ok {
			d = g.stdProgQualifiedIface(qualifier, name)
		} else if gt, generic := te.Member.(*ast.GenericType); generic {
			d = g.stdProgQualifiedIface(te.Module, gt.Name)
		}
	default:
		if name, named := ifaceHeaderName(te); named {
			d = g.stdProgIfaceNamed(name)
		}
	}
	if d == nil {
		return nil
	}
	if tpl := stdProgTemplate(d); tpl != nil {
		subst, why, _ := g.ifaceInstArgs(tpl, ib)
		if why != "" {
			return nil
		}
		if inst := g.stdProgInst(tpl, subst); inst != nil && inst.def.lowerable {
			return inst.def
		}
		return nil
	}
	if !d.lowerable {
		return nil
	}
	return d
}

// stdProgBinds reports whether a value of kind k may be held as std
// interface d: a program type with an impl of it that has every function
// d can dispatch on an erased receiver, or a std type that enters unboxed
// (stdProgHeldType) whose every such function a thunk reaches
// (stdProgThunk). A std type answered yes is recorded, so its dispatch
// entries are recorded (stdProgHeldImpls). A std container or leaf type
// enters boxed instead, and its entries are its box's (irBoxPlan).
func (g *gen) stdProgBinds(d *ifaceDef, k kind) bool {
	if c := g.stdProgCaller; c != nil {
		// A std instance records nothing of its own: its caller builds the
		// thunks and records the entries, as for a value it holds itself.
		return c.stdProgBinds(d, k)
	}
	if typ := g.stdProgHeldType(k); typ != nil {
		for _, m := range d.order {
			// A function no erased receiver can dispatch (`Steppable.step_by`'s
			// `Maybe<self>`) is refused at its call, as a program
			// interface's is, and asks nothing of the value.
			if m.dispatchable() && g.stdProgThunk(d, m, k, typ.Name()) == nil {
				return false
			}
		}
		if g.stdProgHeld == nil {
			g.stdProgHeld = map[*ifaceDef]map[kind]bool{}
		}
		if g.stdProgHeld[d] == nil {
			g.stdProgHeld[d] = map[kind]bool{}
		}
		g.stdProgHeld[d][k] = true
		return true
	}
	impl := g.implsByIface[g.implsKey(d)][k]
	if impl == nil || impl.stdIface != d {
		impl = nil
		for _, cand := range g.instImpls[g.implsKey(d)][k] {
			if cand.stdIface == d {
				impl = cand
			}
		}
	}
	if impl != nil {
		if !impl.lowerable {
			return false
		}
		for _, m := range d.order {
			if it := impl.items[m.name]; m.dispatchable() && (it == nil || !it.lowerable) {
				return false
			}
		}
		return true
	}
	// Another file's impl: the program index, keyed by the declaration and
	// the arguments. A function with a default body may be one that block
	// does not write, which no dispatch entry would answer.
	for _, m := range d.order {
		if m.dispatchable() && (m.decl == nil || m.decl.Body != nil || m.decl.Open || m.decl.Extern) {
			return false
		}
	}
	return g.impls != nil && g.impls.has[implKeyFor(d, k)]
}

// stdProgScalarParams is m's parameter kinds with each self position k.
func stdProgScalarParams(m *ifaceMethod, k kind) []kind {
	params := make([]kind, len(m.params))
	for i := range m.params {
		if m.shape.selfTyped(i) {
			params[i] = k
			continue
		}
		params[i] = m.params[i]
	}
	return params
}

// stdProgHeldType is the type symbol a dispatch selects by for a value of
// the std type k that is held as a std interface unboxed, because its
// run-time value names its type: a builtin scalar, or a std struct or enum
// (`Date`, `Json`). nil for any other kind: a program type, whose impls are
// its own, or a std container or leaf, which enters boxed (irBoxPlan).
func (g *gen) stdProgHeldType(k kind) *ir.Symbol {
	if name, scalar := irDispatchScalarName(k); scalar {
		return g.irTypes().Symbol(irScalarTypeToken{name}, name)
	}
	if _, std := stdNamedRecv(k); !std {
		return nil
	}
	if vt := g.irValType(k); vt == nil || irStdLeafBoxes(k, vt) {
		return nil
	}
	return g.irImplTypeSym(k)
}

// stdProgHeldImpls records, for each std type a value was held at unboxed as
// a std interface, each function's dispatch entry: the thunk that makes the
// call a direct one makes (stdProgThunk).
func (g *gen) stdProgHeldImpls() {
	type entry struct {
		method, typ, fn *ir.Symbol
	}
	var entries []entry
	for d, kinds := range g.stdProgHeld {
		for k := range kinds {
			typ := g.stdProgHeldType(k)
			for _, m := range d.order {
				if !m.dispatchable() {
					continue
				}
				method := g.stdProgMethodSym(d, m)
				fn := g.stdProgThunk(d, m, k, typ.Name())
				if method != nil && fn != nil {
					entries = append(entries, entry{method, typ, fn})
				}
			}
		}
	}
	// Sorted so the module's entries do not follow map order.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].method.Name() != entries[j].method.Name() {
			return entries[i].method.Name() < entries[j].method.Name()
		}
		return entries[i].typ.Name() < entries[j].typ.Name()
	})
	for _, e := range entries {
		g.irMod.Implement(e.method, e.typ, e.fn)
	}
}

// stdProgThunkToken identifies the function that dispatches std interface
// method method of decl at the std type a box names.
type stdProgThunkToken struct {
	decl   *ast.InterfaceDef
	args   string
	method string
	typ    string
}

// stdProgThunk builds, once per gen, the dispatch entry of std interface d's
// method m for a value of kind have held as d: a function of the method's
// operands, each self position at have, whose body is the call
// `Iface.method(...)` with those operands, planned as a rendering's
// `Display.to_string(v)` is (irdisplayhole.go): the interface's container
// family first (ifaceContainerCall), then the qualified-call planner; or,
// where this file binds the bare name to something else, the call through
// the declaring module (`matcher.Matcher.find_in`, stdModuleIfacePlan). So
// std's impl is whatever a direct call with a value of have reaches: a
// family operation (`List.contains`), an instance of std's generic body, a
// retained body (`Int.hash`) or a host function (`Regex.find_in`). nil when
// that call does not lower.
//
// The function names no source. Its nodes sit at line 1, column 1, where no
// reference or definition is ever recorded (every top-level form starts with
// a keyword or `@`), so a lookup the planner keys by the callee's position
// finds nothing there. A refusal while building it is discarded with the
// function (stdProgIsolated): the program's answer is the boxing site's.
func (g *gen) stdProgThunk(d *ifaceDef, m *ifaceMethod, have kind, typ string) *ir.Symbol {
	if d.decl == nil {
		return nil
	}
	token := stdProgThunkToken{decl: d.decl, args: portableArgsKey(d.instArgs), method: m.name, typ: typ}
	sym := g.irCalleeSym(token, typ+"."+m.name)
	if g.irModule().FuncFor(sym) != nil {
		return sym
	}
	if g.stdProgThunkFailed[token] {
		return nil
	}
	built := g.stdProgIsolated(func() bool { return g.buildStdProgThunk(d, m, have, sym) })
	if !built {
		if g.stdProgThunkFailed == nil {
			g.stdProgThunkFailed = map[stdProgThunkToken]bool{}
		}
		g.stdProgThunkFailed[token] = true
		return nil
	}
	return sym
}

func (g *gen) buildStdProgThunk(d *ifaceDef, m *ifaceMethod, have kind, sym *ir.Symbol) bool {
	params := stdProgScalarParams(m, have)
	declared := make([]ast.Param, len(params))
	operands := make([]ast.Node, len(params))
	for i := range declared {
		name := "%" + strconv.Itoa(i)
		declared[i] = ast.Param{Name: name, Line: 1, Col: 1}
		operands[i] = &ast.Ident{Name: name, Line: 1, Col: 1}
	}
	at := &ast.Call{
		Func: &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: d.decl.Name, Line: 1, Col: 1},
			Field:  &ast.Ident{Name: m.name, Line: 1, Col: 1},
			Line:   1, Col: 1,
		},
		Args: operands,
		Line: 1,
		Col:  1,
	}
	sh := g.irFuncShellAt(at, declared, irFuncSig{result: m.result}, params, sym)
	if sh == nil {
		return false
	}
	bl := &irScalarBuilder{g: g, sh: sh, f: sh.fn, b: sh.entry, returnKind: m.result,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	mobile := make([]bool, len(params))
	for i := range mobile {
		mobile[i] = true
	}
	args := irQualArgs{temps: sh.paramTemps, kinds: params, mobile: mobile, ok: true}
	var v ir.Temp
	var rk kind
	ok, handled := false, false
	if irStructuralBoxes(have) && d.stdOrigin == "std/hashable" && d.decl.Name == "Hashable" && m.name == "hash" && len(params) == 1 {
		// A tuple or record has no impl to reach: its hash is the derive
		// mix over its parts, as a direct `Hashable.hash` of it lowers
		// (irtuplehash.go).
		v, rk, _, ok = bl.tupleHashOf(at, sh.paramTemps[0], have)
		handled = true
	}
	if irRetainedRecordKind(have) && d.stdOrigin == "std/json" && d.decl.Name == "ToJson" && m.name == "to_json" && len(params) == 1 {
		// A record's JSON is its fields' (irrecordjson.go).
		v, rk, ok = bl.recordToJsonOf(at, sh.paramTemps[0], have, m.result, "")
		handled = true
	}
	if !handled && (g.stdProgBareNames(d) || g.stdCases) {
		// A std container's impl is a template instantiated at the call,
		// which a std module's cases reach under their module's own name
		// for its interface (`ToJson` in std/json's cases) too.
		v, rk, _, ok, handled = bl.ifaceContainerCall(at, args, d.decl.Name, m.name)
	}
	if !handled {
		var plan *irQualPlan
		if g.stdProgBareNames(d) {
			plan = bl.qualImplPlan(at, args, d.decl.Name, m.name)
		} else {
			// This file binds the name to something else (its own
			// `struct Matcher`): std's impl of this declaration, wherever
			// std writes it (`impl Matcher for Regex<C>` in std/regex).
			plan = bl.stdFuncPlan(at, args, g.stdProgImplOf(d, m, args.kinds))
		}
		if plan == nil {
			return false
		}
		v, rk, _, ok = bl.qualEmit(at, args, plan)
	}
	if !ok || rk != m.result {
		return false
	}
	bl.resultCopy(at, v)
	bl.side(sh.result, irScalarSide{k: m.result})
	bl.b.SetTerm(ir.NewReturn(g.irNodePos(at), sh.result))
	if err := g.irLint(sh.fn); err != nil {
		return false
	}
	g.irModule().AddFunc(sh.fn)
	return true
}

// stdProgImplOf is std's function for interface d's method m at the
// operand kinds: an impl whose header names d's declaration in the scope of
// the std module that writes it, which is where the header was resolved.
// nil when std has none.
func (g *gen) stdProgImplOf(d *ifaceDef, m *ifaceMethod, kinds []kind) *stdFunc {
	if g.std == nil || len(kinds) == 0 {
		return nil
	}
	decl := d.decl
	if d.instOf != nil {
		decl = d.instOf
	}
	views := g.std.views
	if g.stdInsts != nil {
		// A std module's cases gen indexes the modules before its own;
		// the lowering's views hold every module, its own among them.
		views = g.stdInsts.std.views
	}
	var fs []*stdFunc
	for _, f := range g.std.byIface[decl.Name+"."+m.name][kinds[m.shape.recvAt()]] {
		v := views[f.module]
		if v == nil {
			continue
		}
		if sym := resolvedTypeSymbol(v.fa, f.iface); sym != nil && sym.Node == decl {
			fs = append(fs, f)
		}
	}
	return stdPick(fs, kinds)
}

// stdProgIsolated runs build, a function built while another body is being
// lowered, and discards what a failed build left: the gen's refusals
// (snapshot), and in every case the census's record of the attempt, which
// belongs to the body around it (as attemptClosure sets it aside). It
// reports build's answer.
func (g *gen) stdProgIsolated(build func() bool) bool {
	resume := g.dc.suspend()
	unmute := irMuteObservers()
	restore := g.snapshot()
	errs := len(g.errs)
	ok := build()
	if !ok || len(g.errs) != errs {
		restore()
		ok = false
	}
	unmute()
	resume()
	return ok
}

// stdProgBoxImpls is the dispatch entries a value of the std type have,
// boxed as typ, needs as std interface d: a thunk per function an erased
// receiver can dispatch (stdProgThunk). false when one does not build.
func (g *gen) stdProgBoxImpls(d *ifaceDef, have kind, typ *ir.Symbol) ([]irBoxedImpl, bool) {
	var impls []irBoxedImpl
	for _, m := range d.order {
		if !m.dispatchable() {
			continue
		}
		method := g.irIfaceMethodSym(d, m)
		fn := g.stdProgThunk(d, m, have, typ.Name())
		if method == nil || fn == nil {
			return nil, false
		}
		impls = append(impls, irBoxedImpl{method: method, typ: typ, fn: fn, owner: g})
	}
	return impls, true
}

// stdProgErasedPlan dispatches an interface-qualified call
// (`Hashable.hash(h)`, `Contains.contains(c, 2)`, `matcher.Matcher.find_in(m,
// s)`) whose receiver is held as the std interface owner names, or nil. The
// operand's def is the interface's: owner names it when this file resolves
// owner to that interface (or its template), or, for a call qualified
// through the interface's module, when this file names no interface owner
// at all and the def's declaration is spelled owner.
func (bl *irScalarBuilder) stdProgErasedPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	if !args.ok {
		return nil
	}
	named := bl.g.stdProgIfaceNamed(owner)
	if named == nil && bl.g.stdProgBareConflict(owner) {
		return nil
	}
	return bl.stdProgErasedPlanOf(t, args, named, owner, method)
}

// stdProgErasedPlanOf dispatches a call whose receiver is held as std
// interface named, the def the call's qualifier resolves to, or nil. A nil
// named stands for a qualifier this file binds to nothing, which names the
// held interface whose declaration is spelled owner.
func (bl *irScalarBuilder) stdProgErasedPlanOf(t *ast.Call, args irQualArgs, named *ifaceDef, owner, method string) *irQualPlan {
	for _, k := range args.kinds {
		if k.tag != tagIface || k.iface == nil || k.iface.stdOrigin == "" {
			continue
		}
		held := k.iface
		switch {
		case named == nil:
			if held.decl == nil || held.decl.Name != owner {
				continue
			}
		case held != named && (held.instOf == nil || held.instOf != named.decl):
			continue
		}
		m := held.methods[method]
		if m == nil {
			return nil
		}
		return bl.erasedInterfacePlan(t, args, held, m)
	}
	return nil
}

// stdProgBareNames reports whether the bare name of std interface d's
// declaration, as a call's qualifier in this file, names that interface:
// it resolves to the declaration, or to nothing this file declares or
// imports, which leaves the call to std's index.
func (g *gen) stdProgBareNames(d *ifaceDef) bool {
	name := d.decl.Name
	if d.stdShared {
		// std's Display or Debug, an interface with an rt table, which no
		// std-program def stands for: the bare name is std's unless this
		// file binds it to an interface of its own.
		if _, local := g.ifaceNamed(name); local {
			return false
		}
		sym := resolvedTypeSymbol(g.fa, name)
		if sym == nil {
			return true
		}
		it, isIface := sym.Type.(*analysis.InterfaceType)
		return isIface && strings.HasPrefix(it.Origin, "std/")
	}
	if named := g.stdProgIfaceNamed(name); named != nil {
		return named.decl == d.decl || named.decl == d.instOf
	}
	return !g.stdProgBareConflict(name)
}

// stdProgBareConflict reports whether this file binds name to something
// other than a std interface, which a module-qualified call's bare owner
// must then not be read as.
func (g *gen) stdProgBareConflict(name string) bool {
	if _, local := g.ifaceNamed(name); local {
		return true
	}
	return resolvedTypeSymbol(g.fa, name) != nil
}

// stdProgSolved is the def of the std interface the checker solved for a
// position the program does not annotate, or names through a module
// qualifier, or nil (stdProgIfaceAt).
func (g *gen) stdProgSolved(ty *analysis.InterfaceType) *ifaceDef {
	return g.stdProgIfaceAt(ty.Origin, ty.Name)
}

// stdProgIfaceAt is the def of the interface std module origin declares as
// name, or nil: the declaration that module's scope binds to the name. The
// origin is the identity, so no program interface of the same name can
// answer.
func (g *gen) stdProgIfaceAt(origin, name string) *ifaceDef {
	if !g.stdProgBuilds() || !strings.HasPrefix(origin, "std/") {
		return nil
	}
	module := strings.TrimPrefix(origin, "std/")
	if g.fa != nil {
		// The program's own std scope first: the declaration a file's
		// import reaches (stdProgIfaceNamed), which may have been read from
		// the stdlib's stored interfaces rather than the shared analysis,
		// so the two routes reach one def.
		if scope := g.fa.Program().StdlibModuleScopes[module]; scope != nil {
			if d := g.stdProgIfaceOf(resolvedScopeSymbol(scope, name)); d != nil {
				return d
			}
		}
	}
	lib := stdAnchorLib()
	if lib == nil {
		return nil
	}
	owner := lib.Files[module]
	if owner == nil {
		return nil
	}
	return g.stdProgIfaceOf(resolvedScopeSymbol(owner.ModuleScope, name))
}
