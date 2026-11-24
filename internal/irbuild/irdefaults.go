package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// FILLING THE PARAMETERS A CALL LEAVES OUT.
//
// The checker records, for each call that leaves a defaulted parameter
// out, the declaration whose defaults fill it and which slots
// (analysis.FileAnalysis.CallDefaults). fillCallDefaults is the one place
// a call's omitted operands are filled, from that record: every call
// route places the written operands at their slots and hands it the rest.
//
// A default is Nomi source of the file that declared it, so where it is
// lowered depends on that file:
//
//   - This file's (a std module's own call included): lowered at the call,
//     in the callee's scope, after the written operands (callDefaults).
//   - An interface function's: lowered at the call under the declaring
//     file's names (ifaceslotdefault.go), whichever file the call is in.
//   - An interface function's, called on an interface value: a call
//     dispatched on the receiver for each omitted slot, which runs the
//     accessor of the impl function the call reaches (ifaceslotdispatch.go).
//   - Another file's: a call of the declaring file's accessor for each
//     omitted slot, over the slots before it (siblingdefault.go), the
//     written operands held first so they run before any default. The
//     defaults of a callee whose file builds no accessors are lowered at
//     the call, reading the checker's answers about their nodes (what
//     each name names, each expression's type) from the declaring file's
//     analysis (lowerNamesFrom), whatever this file binds the names to.
//
// A default lowered at the call or in an accessor is outside the impl block
// or interface that declares it, so a bare call of a sibling in it
// (`extra: Int = side(q) * 2`) is lowered from the checker's record of the
// callee's owner (bareImplCall), as the same call in a body of the block is.
//
// A call into a std module from outside it does not reach fillCallDefaults:
// it fills each omitted slot with the module's own accessor
// (stdslotfill.go), through fillFromAccessors.

// defaultHome is where a call's callee is declared, for fillCallDefaults:
// the declaring file's gen, nil for this one; the accessor that gen builds
// for a slot, nil when it builds none; the instance of a generic `fn`
// whose defaults are lowered here, nil for any other callee; and the
// function an interface call reached.
type defaultHome struct {
	owner    *gen
	accessor func(slot int) *ir.Symbol
	// keyed: the accessor of a slot before operand keyAt, the receiver,
	// also takes it (keyedSlot). dispatched: accessor names a slot's
	// dispatch symbol, which a call dispatches on the receiver
	// (slotDispatchHome); otherwise it names one impl function's accessor
	// (implSlotHome).
	keyed, dispatched bool
	keyAt             int
	inst              *monoInst
	// params is the parameter list of the function the builder reached,
	// when the checker could not name it: a call of an interface function
	// on a type parameter (DefaultedCall.Decl an *ast.InterfaceMethod)
	// takes, at each instance, the defaults of the impl function it
	// reaches, and the interface's where that function writes none.
	params []ast.Param
}

// callDefaultsOf is the checker's record of what fills the parameters t
// leaves out, nil when it leaves none out. A call the builder makes for a
// function value (callFuncValueOf) carries the record of the value it
// stands for. The builder lowers copies of a
// written call (a pipe stage with its subject spliced in, a call
// requalified through its module's alias), which keep the written call's
// position, so a copy finds the record by it. The record names its
// declarations as this gen's module declares them (ownDefaults).
func (g *gen) callDefaultsOf(t *ast.Call) *analysis.DefaultedCall {
	fa := g.srcFA()
	if fa == nil || t == nil {
		return nil
	}
	if rec := g.synthDefaults[t]; rec != nil {
		return g.ownDefaults(rec)
	}
	if rec := fa.CallDefaults[t]; rec != nil {
		return g.ownDefaults(rec)
	}
	if len(fa.CallDefaults) == 0 {
		return nil
	}
	if g.callDefaultsAt == nil || g.callDefaultsFA != fa {
		g.callDefaultsFA = fa
		g.callDefaultsAt = make(map[analysis.Pos]*analysis.DefaultedCall, len(fa.CallDefaults))
		for c, rec := range fa.CallDefaults {
			g.callDefaultsAt[analysis.Pos{Line: c.Line, Col: c.Col}] = rec
		}
	}
	return g.ownDefaults(g.callDefaultsAt[analysis.Pos{Line: t.Line, Col: t.Col}])
}

// ownDefaults is rec with each declaration it names read as this gen's
// module declares it (ownDecl): in a std module checked from edited
// source, a call through the module's std name (a `//!` prompt's
// `compress.encode(...)`) is recorded against the shared analysis's copy,
// whose defaults are nodes this module's analysis holds nothing about, and
// the edited twin's are the ones lowered. rec itself, unchanged, when it
// names no twin.
func (g *gen) ownDefaults(rec *analysis.DefaultedCall) *analysis.DefaultedCall {
	if rec == nil || len(g.twins) == 0 {
		return rec
	}
	if own, done := g.ownDefaultsOf[rec]; done {
		return own
	}
	own := *rec
	own.Decl = g.ownDecl(rec.Decl)
	own.From = make([]ast.Node, len(rec.From))
	twinned := own.Decl != rec.Decl
	for i, from := range rec.From {
		own.From[i] = g.ownDecl(from)
		twinned = twinned || own.From[i] != from
	}
	if params, ok := declParams(own.Decl); ok && own.Decl != rec.Decl {
		own.Params = params
	}
	out := rec
	if twinned {
		out = &own
	}
	if g.ownDefaultsOf == nil {
		g.ownDefaultsOf = map[*analysis.DefaultedCall]*analysis.DefaultedCall{}
	}
	g.ownDefaultsOf[rec] = out
	return out
}

// fillCallDefaults fills each slot of a call t's operand vector that the
// call leaves out, as the checker recorded it: the plan's defaulted slots
// (argPlan.defaulted). kinds holds every parameter's kind and temps every
// written operand at its slot, ir.NoTemp at each defaulted one; mobile
// marks a written operand that may stay unforced. home is where the callee
// is declared. ok is false when a default does not lower.
func (bl *irScalarBuilder) fillCallDefaults(t *ast.Call, kinds []kind, temps []ir.Temp, mobile []bool, home defaultHome) bool {
	g := bl.g
	plan, planned := g.callPlan(t, len(temps))
	if !planned {
		panic("irbuild: fillCallDefaults: a call whose record places an argument past the callee's parameters")
	}
	omitted := plan.defaulted
	if len(omitted) == 0 {
		return true
	}
	// The checker records what fills the slots a call leaves out, at the
	// slots it routed the call's arguments to (analysis package doc,
	// "Elaboration"), and TestElaborationGate holds it.
	rec := g.callDefaultsOf(t)
	if rec == nil || len(rec.Params) != len(temps) || len(kinds) != len(temps) {
		panic("irbuild: a call leaving out parameters with no CallDefaults record over its slots")
	}
	for _, s := range omitted {
		if !slices.Contains(rec.Slots, s) {
			panic("irbuild: a call leaving out a slot its CallDefaults record does not name")
		}
	}
	if home.dispatched {
		return bl.fillFromAccessors(t, kinds, temps, mobile, home, len(temps))
	}
	for _, run := range defaultRuns(rec, home, omitted) {
		if !bl.fillDefaultRun(t, run, kinds, temps, mobile, home) {
			return false
		}
	}
	return true
}

// defaultRun is omitted slots of a call, ascending and with no other
// omitted slot between them, whose defaults one parameter list holds
// (defaultParams). Each run is filled under its own declaration's
// parameter names, after the runs before it.
type defaultRun struct {
	params []ast.Param
	slots  []int
}

// defaultRuns splits a call's omitted slots into runs by the parameter
// list holding each one's default: one run, unless the call reaches an
// impl function that declares some of them and its interface function the
// rest (analysis.DefaultedCall.From).
func defaultRuns(rec *analysis.DefaultedCall, home defaultHome, omitted []int) []defaultRun {
	var runs []defaultRun
	for _, s := range omitted {
		params := defaultParams(rec, home, s)
		if n := len(runs); n > 0 && &runs[n-1].params[0] == &params[0] {
			runs[n-1].slots = append(runs[n-1].slots, s)
			continue
		}
		runs = append(runs, defaultRun{params: params, slots: []int{s}})
	}
	return runs
}

// fillDefaultRun fills one run of a call t's omitted slots (defaultRuns),
// as fillCallDefaults does every omitted slot: where its declaration is,
// as the comment at the top of this file says.
func (bl *irScalarBuilder) fillDefaultRun(t *ast.Call, run defaultRun, kinds []kind, temps []ir.Temp, mobile []bool, home defaultHome) bool {
	g := bl.g
	params, omitted := run.params, run.slots
	through := omitted[len(omitted)-1] + 1
	sig := &fnSig{decl: &ast.FuncDef{Params: params}, params: kinds, instance: home.inst}
	o, iface := g.ifaceParamDefault(params[omitted[0]].Default)
	switch {
	case iface:
		if ifaceDefaultGeneric(o) {
			// A record lacking one leaves the frame nil, which callDefaults
			// declines at the default.
			sig.defaultFrame, sig.defaultTypes, _ = g.ifaceDefaultFrameAt(t, o)
		}
		// The implementor the defaults are filled for is the type the
		// call's record is for, at this instance: `Shape.area(c)`'s
		// Circle, and `T.area(x)`'s T as the instance binds it.
		sig.defaultSelfType = g.checkerTypeArg(g.calleeOf(t).For)
	case home.inst == nil:
		// A generic callee no instance of which is lowered here (an impl
		// function of a generic type's instance): its defaults are lowered
		// in a frame binding its type parameters at the call's type
		// arguments, as an instance's are.
		sig.defaultFrame, sig.defaultTypes = g.callFrameAt(t)
	}
	owner := home.owner
	if owner == nil || owner == g {
		return bl.callDefaultsThrough(sig, temps, through)
	}
	if iface {
		// An interface's defaults, which an impl inherits: each is filled
		// under its declaring file's names (ifaceslotdefault.go).
		return bl.callDefaultsThrough(sig, temps, through)
	}
	if byAccessor(home, omitted) {
		return bl.fillFromAccessors(t, kinds, temps, mobile, home, through)
	}
	if !g.defaultsLowerHere(home) {
		g.dc.note("a default naming what only its declaring file can, with no accessor for it")
		return false
	}
	// The defaults are the declaring file's nodes, so what the checker
	// recorded of them (what each name names, a bare `None`'s type) is in
	// its analysis, and is read there.
	if unit := slices.Index(g.reg.gens, owner); unit >= 0 {
		defer g.lowerNamesFrom(unit)()
	}
	return bl.callDefaultsThrough(sig, temps, through)
}

// callFrameAt binds every type parameter the checker instantiated call t at
// (analysis.CallTypeArgs of its callee's reference) to that type argument,
// as callTypeArgFrame binds them. Nil for a callee that is no generic
// function's reference, and when a type argument has no kind here.
func (g *gen) callFrameAt(t *ast.Call) (map[string]kind, map[string]analysis.Type) {
	args := g.callTypeArgs(t)
	if len(args) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(args))
	for n := range args {
		names = append(names, n)
	}
	slices.Sort(names)
	frame, types, ok := g.callTypeArgFrame(t, names)
	if !ok {
		return nil, nil
	}
	return frame, types
}

// callTypeArgs is the type arguments the checker instantiated call t's
// callee at, by type parameter (analysis.CallTypeArgs of its reference),
// nil for a callee with no reference.
func (g *gen) callTypeArgs(t *ast.Call) map[string]analysis.Type {
	line, col := calleeRefPos(t.Func)
	return analysis.CallTypeArgs(g.srcFA().References[analysis.Pos{Line: line, Col: col}])
}

// byAccessor reports whether the omitted slots of a call of another file's
// function are filled by the declaring file's accessors: when it builds
// one for each.
func byAccessor(home defaultHome, omitted []int) bool {
	if home.accessor == nil {
		return false
	}
	for _, s := range omitted {
		if home.accessor(s) == nil {
			return false
		}
	}
	return true
}

// defaultParams is the parameter list holding the default that fills slot:
// the one the record names for it (analysis.DefaultedCall.SlotSource), or,
// where that is an interface function's and the builder reached a function
// that declares a default there (defaultHome.params, a call on a type
// parameter's instance), that function's.
func defaultParams(rec *analysis.DefaultedCall, home defaultHome, slot int) []ast.Param {
	decl, params := rec.SlotSource(slot)
	if _, iface := decl.(*ast.InterfaceMethod); iface && len(home.params) == len(params) && home.params[slot].Default != nil {
		return home.params
	}
	return params
}

// defaultsLowerHere reports whether home's defaults may be lowered at the
// call: this file's own, or another file's whose analysis is at hand, since
// a default is lowered from its declaring file's records (lowerNamesFrom),
// whatever this file binds the names it mentions to.
func (g *gen) defaultsLowerHere(home defaultHome) bool {
	owner := home.owner
	return owner == nil || owner == g || (owner.fa != nil && g.fa != nil)
}

// fillFromAccessors fills each omitted slot of temps before through with a
// call to home's accessor for it (irFileSlotRetain), in ascending order,
// after every written operand is held, so the written arguments run first
// and each default runs once. A keyed home passes its receiver, operand
// keyAt, after the slots before an omitted one before it (keyedSlot); a
// dispatched home's calls dispatch on that receiver (slotDispatchHome).
func (bl *irScalarBuilder) fillFromAccessors(at ast.Node, params []kind, temps []ir.Temp, mobile []bool, home defaultHome, through int) bool {
	pos := bl.g.irNodePos(at)
	for i, t := range temps {
		if t == ir.NoTemp || (i < len(mobile) && mobile[i]) || irHeldValue(bl.f, t, bl.sides) {
			continue
		}
		cp := ir.NewCopy(pos, bl.f.NewTemp(), t)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: params[i], copy: irCopyForce})
		temps[i] = cp.Dst()
	}
	keyAt := -1
	if home.keyed {
		keyAt = home.keyAt
	}
	for slot := range temps[:through] {
		if temps[slot] != ir.NoTemp {
			continue
		}
		// params are the callee's held signature's.
		irHeldOperand(params[slot], "a defaulted parameter")
		// A home with no accessors (implSlotHome of an impl function no
		// accessor is built for) has none for the slot either.
		var sym *ir.Symbol
		if home.accessor != nil {
			sym = home.accessor(slot)
		}
		if sym == nil {
			bl.g.dc.note("a defaulted parameter with no accessor")
			return false
		}
		var c *ir.Call
		switch {
		case keyAt >= slot:
			// A receiver after the slot follows the operands before it
			// (keyedSlot).
			if temps[keyAt] == ir.NoTemp {
				panic("irbuild: fillFromAccessors: a keyed call's receiver is a written operand (erasedInterfacePlan, dispatchedCallHome, implSlotHome)")
			}
			operands := append(slices.Clone(temps[:slot]), temps[keyAt])
			if home.dispatched {
				c = ir.NewDispatchCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sym, slot, operands...)
			} else {
				c = ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sym, operands...)
			}
		case home.dispatched:
			c = ir.NewDispatchCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sym, keyAt, temps[:slot]...)
		default:
			c = ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sym, temps[:slot]...)
		}
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: params[slot]})
		cp := ir.NewCopy(pos, bl.f.NewTemp(), c.Dst())
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: params[slot], copy: irCopyForce})
		temps[slot] = cp.Dst()
	}
	return true
}

// filledOperands is a short call's operands against params, its callee's
// parameter kinds, and decl, its parameter list: each written operand at
// the slot a direct call gives it (slottedOperands), every omitted one
// filled (fillCallDefaults). A call that omits nothing answers its own
// operands.
func (bl *irScalarBuilder) filledOperands(t *ast.Call, args irQualArgs, params []kind, decl []ast.Param, home defaultHome) (irQualArgs, bool) {
	temps, mobile, ok := bl.g.slottedOperands(t, args, params, decl)
	home.params = decl
	if !ok || !bl.fillCallDefaults(t, params, temps, mobile, home) {
		return args, false
	}
	return irQualArgs{temps: temps, kinds: slices.Clone(params), mobile: make([]bool, len(temps)), ok: true}, true
}

// fileGen is the gen of the program file at path, nil for this gen's own
// file, a path no file of the program has, and "" (the stdlib).
func (g *gen) fileGen(path string) *gen {
	if path == "" || g.reg == nil || (g.fa != nil && g.fa.FilePath == path) {
		return nil
	}
	for _, o := range g.reg.gens {
		if o != nil && o.fa != nil && o.stdModule == "" && !o.programStd && o.fa.FilePath == path {
			return o
		}
	}
	return nil
}
