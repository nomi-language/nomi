package irbuild

import (
	"sort"
	"strconv"

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
//   - THE DEF IS THE PROCESS'S, built once from std's declaration by the
//     type registry (stdtypes.go) with the code that builds a program file's
//     interfaces, and found by the declaring module's origin and the name
//     (stdDeclaredIface). Every gen holds a value of it
//     through that one def: a program's files, the std modules lowered
//     with it, the shared lowering, and the projection with no gen, so a
//     std function that takes or returns one (`Json.encode_as(value:
//     ToJson)`) is lowered once for the process. A generic interface's def
//     is its template, instantiated per type arguments by each gen that
//     needs an instance (ifaceInstAt); the instance is that gen's.
//   - THE METHOD SYMBOLS ARE STD'S, not the gen's: no unit of a program
//     declares the interface, so every unit interns them in the first
//     unit's table (programSymbol), keyed by the declaration and, for an
//     instance, its portable arguments. A value boxed in one file then
//     dispatches to an impl recorded in another. A non-generic interface's
//     symbol is named by the interface alone (`std/hashable.Hashable.hash`,
//     link.go), and a program's table takes the symbol the shared stdlib's
//     objects link under that name (linkImport), so a shared body's
//     dispatch and the program's entries name one symbol; the entries a
//     dispatch selects among are each module's (ir.Module.Implement), and
//     each machine indexes its own.
//   - A STD TYPE'S IMPL is no impl block of the program. A value of a std
//     type enters as a program interface's would: a builtin scalar or a std
//     struct or enum unboxed (stdProgHeld), a container or leaf boxed under
//     its type's name (irBoxPlan). Each of its dispatch entries is a thunk
//     the program builds whose body is the direct call `Iface.method(...)`
//     with a value of that type (stdProgThunk), so the entry reaches
//     whatever that call reaches: a container family's operation, an
//     instance of std's generic body, a retained body or a host function.
//
// Identity is the declaration node of the interface the checker recorded,
// whose origin is a std module (stdDeclaredIface). `std` is unforgeable as a
// module name (analysis/manifest.go), and a std module lowered with the
// program (programStdModules) is excluded: its interfaces are the
// program's own.
//
// What stays per program is what a value's entry into the interface makes:
// the dispatch entries, the thunks for std types, the instances of a
// generic interface and the defaults instantiated per implementor
// (stdifacedefault.go). Only a gen that records entries for a program
// boxes a value into one of these interfaces (stdProgBuilds): its files,
// the std modules lowered with it, a shared std module's own cases
// (stdCases), and a std instance built for one of those, whose caller
// (stdProgCaller) builds the thunks and records the entries (stdProgBinds).
// The stdlib lowered once for the process records none, so a shared body
// that would box a value declines and is not retained.

// stdIfaceMethodToken is a std interface method's dispatch symbol: the
// declaration and the method's name, linked by both (link.go).
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

// stdProgBuilds reports whether this gen records dispatch entries for a
// program, and so may box a value into a std interface (stdProgBinds): a
// program's own gens, the std modules lowered with it and a shared std
// module's cases (stdCases), the gen that asks for a shared module's
// abstract bodies (StdAbstractBodies), whose modules nothing links, and a
// std instance built for one of those, which records through its caller
// (stdProgCaller); never the stdlib lowered once for the process.
func (g *gen) stdProgBuilds() bool {
	if g.stdProgCaller != nil {
		return true
	}
	abstract := g.dc != nil && g.dc.abstract
	return (g.stdModule == "" || g.programStd || g.stdCases || abstract) && g.stdInstCur == nil
}

// withStdSrc makes std module's file the source srcFA answers, for
// resolving the signatures of an interface it declares (resolveIface), and
// returns what restores the previous source. `Option<String>` in `Matcher.find_in` is
// resolved by the position std wrote it at (typeOf), which only std's
// file records; this file's maps hold nothing, or something else, there.
func (g *gen) withStdSrc(module string) func() {
	prev := g.stdSrc
	if lib := stdAnchorLib(); lib != nil {
		g.stdSrc = lib.Files[module]
	}
	return func() { g.stdSrc = prev }
}

// stdProgTemplate is the declaration of a std generic interface's template
// def, or nil.
func stdProgTemplate(d *ifaceDef) *ast.InterfaceDef {
	if d == nil || d.stdOrigin == "" || d.instOf != nil || d.why != "generic interface" {
		return nil
	}
	return d.decl
}

// stdProgInst is the std generic interface tpl's instance at subst, with
// types the checker's types for them or nil (ifaceInstAtTyped).
func (g *gen) stdProgInst(tpl *ast.InterfaceDef, subst map[string]kind, types map[string]analysis.Type) *ifaceInst {
	if stdProgTemplate(g.stdRegistryIface(tpl)) == nil {
		return nil
	}
	return g.ifaceInstAtTyped(tpl, subst, types)
}

// stdProgIfaceForImpl is the def impl block ib of a std interface binds
// into: the def of the interface the checker recorded for it (rec), or for
// a generic one the instance at the block's type arguments. nil when the
// block's interface is none of these.
func (g *gen) stdProgIfaceForImpl(ib *ast.ImplBlock, rec *analysis.ImplTypeArgs) *ifaceDef {
	d := g.stdDeclaredIface(rec.Origin, rec.Interface)
	if d == nil {
		return nil
	}
	if tpl := stdProgTemplate(d); tpl != nil {
		subst, types, why, _ := g.ifaceInstArgs(tpl, ib)
		if why != "" {
			return nil
		}
		if inst := g.stdProgInst(tpl, subst, types); inst != nil && inst.def.lowerable {
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
// enters boxed instead, and its entries are its box's (irBoxPlan). The
// thunks are built for kt, the checker's type k was read from (nil where
// the caller has none), or for a program type the impl's receiver
// (implDef.recvType). A thunk is built once per type name, by the first
// question asked of it. A gen that records no entries for a program
// (stdProgBuilds) binds nothing.
func (g *gen) stdProgBinds(d *ifaceDef, k kind, kt analysis.Type) bool {
	if c := g.stdProgCaller; c != nil {
		// A std instance records nothing of its own: its caller builds the
		// thunks and records the entries, as for a value it holds itself.
		return c.stdProgBinds(d, k, kt)
	}
	if !g.stdProgBuilds() {
		return false
	}
	if typ := g.stdProgHeldType(k); typ != nil {
		for _, m := range d.order {
			// A function no erased receiver can dispatch (`Steppable.step_by`'s
			// `Option<self>`) is refused at its call, as a program
			// interface's is, and asks nothing of the value.
			if m.dispatchable() && g.stdProgThunk(d, m, k, kt, typ.Name()) == nil {
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
			it := impl.items[m.name]
			if it == nil && g.stdProgThunked(d, m, impl.recvType) {
				// A function the block does not write, a default or one
				// answered by k's shape: its dispatch entry is a thunk of
				// the direct call at k (irRecordImpls). An enum with an
				// `embeds` variant is named as its box will name it, which
				// this boxing marks.
				typ := g.irImplDispatchTypeSym(k)
				if typ == nil && irBoxedEnum(k) {
					typ = g.irImplTypeSym(k)
				}
				if typ == nil || g.stdProgThunk(d, m, k, impl.recvType, typ.Name()) == nil {
					return false
				}
				continue
			}
			if m.dispatchable() && (it == nil || !it.lowerable) {
				return false
			}
		}
		return true
	}
	// Another file's impl: the program index, keyed by the declaration and
	// the arguments. A Nomi default that block does not write is answered by
	// the thunk that file records for it (irRecordImpls); any other default
	// may be one no dispatch entry would answer.
	for _, m := range d.order {
		if stdProgInheritsDefault(m) {
			continue
		}
		if m.dispatchable() && (m.decl == nil || m.decl.Body != nil || m.decl.Open || m.decl.Extern) {
			return false
		}
	}
	return g.impls != nil && g.impls.has[implKeyFor(d, k)]
}

// stdProgInheritsDefault reports whether m is a dispatchable function of a
// std interface with a Nomi body, which an implementor that does not write
// it inherits (`ToJson.to_json_field`).
func stdProgInheritsDefault(m *ifaceMethod) bool {
	return m.dispatchable() && m.decl != nil && m.decl.Body != nil && !m.decl.Extern
}

// stdProgThunked reports whether a block of the std interface d for a
// receiver of checker type recvType, which does not write m, answers it
// through a thunk of the direct call `Iface.m(...)` at the receiver
// (stdProgThunk), which is then its dispatch entry: m is a default the
// block inherits (stdProgInheritsDefault), or a function the checker
// records the receiver answers by its shape, as a derived Hashable's block
// writes no `hash` (analysis's derivesByShape).
func (g *gen) stdProgThunked(d *ifaceDef, m *ifaceMethod, recvType analysis.Type) bool {
	if stdProgInheritsDefault(m) {
		return true
	}
	if d.stdOrigin == "" || recvType == nil || !m.dispatchable() {
		return false
	}
	return g.stdIfaceCallRecord(g.stdIfaceType(d), m.name, len(m.params), recvType).Callee.Form == analysis.OperatorStructural
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
				// Built by stdProgBinds, for the held value's type.
				fn := g.stdProgThunk(d, m, k, nil, typ.Name())
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
// `Iface.method(...)` with those operands, recorded as a call for
// haveType, the checker's type have was read from (nil where the caller
// has none), and planned as a rendering's
// `Display.to_string(v)` is (irdisplayhole.go): the interface's container
// family first (ifaceContainerCall), then the qualified-call planner,
// which plans it from the function the record names whatever this file
// binds the bare name to (`Matcher.find_in` beside a local `Matcher`). So
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
func (g *gen) stdProgThunk(d *ifaceDef, m *ifaceMethod, have kind, haveType analysis.Type, typ string) *ir.Symbol {
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
	built := g.stdProgIsolated(func() bool { return g.buildStdProgThunk(d, m, have, haveType, sym) })
	if !built {
		if g.stdProgThunkFailed == nil {
			g.stdProgThunkFailed = map[stdProgThunkToken]bool{}
		}
		g.stdProgThunkFailed[token] = true
		return nil
	}
	return sym
}

func (g *gen) buildStdProgThunk(d *ifaceDef, m *ifaceMethod, have kind, haveType analysis.Type, sym *ir.Symbol) bool {
	params := stdProgScalarParams(m, have)
	declared := make([]ast.Param, len(params))
	operands := make([]ast.Node, len(params))
	for i := range declared {
		name := "%" + strconv.Itoa(i)
		declared[i] = ast.Param{Name: name, Line: 1, Col: 1}
		operands[i] = g.madeIdent(name, 1, 1)
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
	// The call names std's interface by its record, so whatever this file
	// binds the name to, it is planned from std's (recordedStdIface), and
	// is for the held value's type, so the instances it reaches are
	// entered with their checker types.
	defer g.carryCall(at, g.stdIfaceCallRecord(g.stdIfaceType(d), m.name, len(operands), haveType))()
	// The call answers what the checker types `Iface.method(x)` as for an
	// x of the held type (InterfaceResult), which a container's operation
	// it reaches reads as its result.
	defer g.carryType(at, g.instanceFA().InterfaceResult(g.stdIfaceType(d), m.name, haveType))()
	sh := g.irFuncShellAt(at, declared, irFuncSig{result: m.result}, params, sym)
	if sh == nil {
		return false
	}
	bl := &irScalarBuilder{g: g, sh: sh, f: sh.fn, b: sh.entry, returnKind: m.result}
	mobile := make([]bool, len(params))
	for i := range mobile {
		mobile[i] = true
	}
	args := irQualArgs{temps: sh.paramTemps, kinds: params, mobile: mobile, ok: true}
	var v ir.Temp
	var rk kind
	ok, handled := false, false
	if d.stdOrigin == "std/hashable" && d.decl.Name == "Hashable" && m.name == "hash" && len(params) == 1 {
		// The machine's hash, as a direct `Hashable.hash` of the held
		// value lowers (interfaceCall), of a value whose record answers
		// it so (hashByMachine). Any other impl is left to the routes
		// below.
		if bl.g.hashByMachine(bl.g.calleeAt(at), haveType) {
			v, rk, ok, handled = bl.hashValueOf(at, sh.paramTemps[0]), kindInt, true, true
		}
	}
	if irRetainedRecordKind(have) && d.stdOrigin == "std/json" && d.decl.Name == "ToJson" && m.name == "to_json" && len(params) == 1 {
		// A record's JSON is its fields' (irrecordjson.go).
		v, rk, ok = bl.recordToJsonOf(at, sh.paramTemps[0], have, haveType, m.result, "")
		handled = true
	}
	if irRetainedRecordKind(have) && d.stdOrigin == "std/json" && d.decl.Name == "ToJson" && m.name == "to_json_field" && len(params) == 1 {
		// The default, Some of the record's JSON.
		v, rk, ok = bl.recordToJsonFieldOf(at, sh.paramTemps[0], have, haveType, m.result)
		handled = true
	}
	if d.nomi == "Display" && m.name == "to_string" && len(params) == 1 {
		// A type whose Display is derived renders by its shape, as a
		// direct `Display.to_string` of it lowers (displayCall); any other
		// is left to the routes below. Display is a prelude interface no
		// program may redeclare, so the name is its identity
		// (irStdDisplayExistential).
		v, ok, handled = bl.displayCall(at, args)
		rk = kindString
	}
	if isStdDebug(d) && m.name == "inspect" && len(params) == 1 {
		// Debug, as a direct `Debug.inspect` of the held value lowers
		// (inspectCall): by its shape, its parts by their positions.
		var by callRoute
		v, rk, _, by = bl.inspectCall(at, args, m.name)
		ok, handled = by != "", by != ""
	}
	if !handled {
		// A std container's impl is a template instantiated at the call.
		v, rk, _, ok, handled = bl.ifaceContainerCall(at, args, m.name, haveType)
	}
	if !handled {
		// The call's record names std's interface, so the plan is std's
		// impl of this declaration whatever this file binds the name to (its
		// own `struct Matcher` beside std/regex's `impl Matcher for
		// Regex<C>`).
		plan := bl.qualImplPlan(at, args, d.decl.Name, m.name)
		if plan == nil {
			// No impl writes the method: the interface's default
			// (`ToJson.to_json_field`), instantiated at the held kind.
			v, rk, _, ok, handled = bl.stdIfaceDefaultCall(at, args)
			if !handled {
				return false
			}
		} else {
			v, rk, _, ok = bl.qualEmit(at, args, plan)
		}
	}
	if !ok || rk != m.result {
		return false
	}
	bl.resultCopy(at, v)
	bl.side(sh.result, irScalarSide{k: m.result})
	bl.b.SetTerm(ir.NewReturn(g.irNodePos(at), sh.result))
	g.irRequireLint(sh.fn, "a std interface function over a program type")
	g.irModule().AddFunc(sh.fn)
	return true
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
func (g *gen) stdProgBoxImpls(d *ifaceDef, have kind, haveType analysis.Type, typ *ir.Symbol) ([]irBoxedImpl, bool) {
	var impls []irBoxedImpl
	for _, m := range d.order {
		if !m.dispatchable() {
			continue
		}
		method := g.irIfaceMethodSym(d, m)
		fn := g.stdProgThunk(d, m, have, haveType, typ.Name())
		if method == nil || fn == nil {
			return nil, false
		}
		impls = append(impls, irBoxedImpl{method: method, typ: typ, fn: fn, owner: g})
	}
	return impls, true
}
