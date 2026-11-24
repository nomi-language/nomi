package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A STD FUNCTION'S DEFAULT, FROM OUTSIDE ITS BODY.
//
// A call of a std function that leaves a defaulted parameter out, by
// writing fewer arguments (`Float.round(x)`) or by naming one past it
// (`Supervisor.new(max_running: 1, restart: Restart.Permanent)`, which
// leaves `shutdown_timeout` out), fills each omitted slot with a call of
// that parameter's accessor and then calls the full body. That is the one
// way a std default is filled from outside its module, trailing or not.
//
// # ONE ACCESSOR PER DEFAULTED PARAMETER
//
// Which slots a call leaves out is a property of the CALL, and no call site
// is known when the module is lowered. One accessor per defaulted parameter
// is n functions and serves every call: the omitted slots are filled in
// ascending order, so slot i's accessor always has slots 0..i-1 in hand,
// written or filled.
//
// It has to take them. A default MAY name an earlier parameter, or a name
// an earlier destructuring parameter binds, so the accessor's signature is
// the prefix and its body is slot's default, lowered with the prefix in
// hand. The builder is the one every accessor uses, a program file's and
// an interface function's too (irSlotAccessor, through irStdSlotRetain).
//
// # WHERE IT IS BUILT IS THE POINT, NOT AN IMPLEMENTATION DETAIL
//
// In the DECLARING module's gen: a default is Nomi source belonging to the
// module that declared it, so `shutdown_timeout: Duration =
// Duration.seconds(5)` filled at the call site would need `Duration`
// resolvable in the CALLER's registry, and a caller that omits the argument
// has no reason to have imported it. Worse, a default naming a module-level
// binding would resolve against whatever the caller has under that
// spelling. Building the fill in std/supervisors' own module removes the
// question instead of answering it, and the caller sees a function of the
// prefix's shape. The fill itself is callDefaults (ircalldefaults.go), the
// evaluator every call's defaults go through.
//
// # EVALUATION ORDER
//
// Nomi evaluates every WRITTEN argument and then fills the omitted slots in
// parameter order, each once (spec §7 *Default Arguments*). The fills are
// built at the CALL, after the written arguments, by fillFromAccessors:
// every written operand is held first, so none is left to run after a
// default, and each filled slot is held before the next accessor reads it.
//
// # COST
//
// A trailing default could instead be filled inside a wrapper of the
// shorter arity that calls the full body. Measured on `Float.round(x, s)`
// in a loop of five million calls, the accessor costs the same as such a
// wrapper for one omitted parameter, and about 25 to 35 ns more per call
// for two (`Float.round(x)`: two accessor calls against one wrapper call).
// The wrappers were 50 functions of the shared lowering, about 15 KB of the
// std bundle, and a second fill path beside the accessors.

// stdSlotName is the symbol name of the accessor that evaluates one std
// parameter's default.
func stdSlotName(f *stdFunc, slot int) string {
	return f.key + " default " + strconv.Itoa(slot)
}

// callDefaultFill reports whether one parameter has an accessor, read on the
// CANONICAL *stdFunc.
//
// Through the canonical pointer, for stdFunc.canon's reason: `bindStdSiblings` and
// `stdInstances.queue` both take `view := *f` copies, some of them before the
// lowering pass has run, and a frozen copy would report an accessor as absent
// for one that has since been built. Assigned only after `g.speculate`
// built it successfully, so a live read can never name a function the pass
// declined to build.
func callDefaultFill(f *stdFunc, slot int) bool {
	fills := f.defaultFill
	if f.canon != nil {
		fills = f.canon.defaultFill
	}
	return slot >= 0 && slot < len(fills) && fills[slot]
}

// emitStdSlotDefaults builds one accessor per defaulted parameter, recording
// each on f as it is built.
//
// Every defaulted parameter, because which ones a call leaves out is a
// property of a CALL SITE and no call site is known here.
//
// Only beside a body this module holds, since a call of the accessors calls
// the body next: a body
// the shared lowering does not retain is built per program with its
// accessors (stdInstances.build) under the same linkage names, so the shared
// lowering defining some of them too would give a program two definitions
// of one name.
func (g *gen) emitStdSlotDefaults(f *stdFunc, fd *ast.FuncDef) {
	if len(fd.Params) != len(f.params) {
		return
	}
	if g.irMod == nil || g.irMod.FuncFor(g.irCalleeSym(fd, f.key)) == nil {
		return
	}
	for i, p := range fd.Params {
		if p.Default == nil {
			continue
		}
		// A parameter kind with no representation gives the accessor no
		// signature to build. `emitStdFunc` succeeding does not promise every
		// parameter projected, since `f.why` is about the whole signature, so
		// the check is here and not inherited.
		// kindInvalid: lookup — asks whether this slot's kind is representable; skipping leaves the slot unfillable, so a call leaving it out declines.
		if f.params[i] == kindInvalid || !slotPrefixRepresentable(f, i) {
			continue
		}
		if f.defaultFill == nil {
			f.defaultFill = make([]bool, len(f.params))
		}
		f.defaultFill[i] = true
		g.irStdSlotRetain(f, fd, i)
	}
}

// slotPrefixRepresentable reports whether every parameter BEFORE slot has a
// representable kind, which the accessor's signature needs because a default
// may name an earlier parameter.
func slotPrefixRepresentable(f *stdFunc, slot int) bool {
	for i := range slot {
		// kindInvalid: lookup — one unrepresentable prefix parameter means no accessor signature can be built.
		if f.params[i] == kindInvalid {
			return false
		}
	}
	return true
}

// A GENERIC STD FUNCTION'S DEFAULT, FROM OUTSIDE ITS MODULE.
//
// The accessors above are built beside a body the shared lowering retains,
// and it retains no generic one: `Iter.sort_by<T, K>(source, direction:
// Direction = Direction.Ascending, key)` has no default accessor. A call
// that names `key:` and leaves `direction` out still needs the default as
// std/iter means it, where `Direction` is in scope and the checker's
// records of the default's nodes are; the caller's file may import
// neither. So the program builds the accessor, in a gen of the declaring
// module at the call's type arguments (stdInstances.defaultAccessor), with
// the same builder and over the same prefix as every other accessor, and
// the call fills the slot with a call to it over the operands before the
// slot. This holds whether the
// function's body is instantiated from std's source or the builder lowers
// the operation itself, as it does `Iter.sort_by`.

// stdTemplateDefaults is the generic function of another std module that
// rec's defaults belong to, or nil. A monomorphic std function's call never
// reaches here with a hole: stdShortCall plans it against the declaring
// module's accessors.
func (bl *irScalarBuilder) stdTemplateDefaults(fa *ast.FieldAccess, rec *analysis.DefaultedCall) *stdFunc {
	g := bl.g
	fd, isFunc := rec.Decl.(*ast.FuncDef)
	if !isFunc || g.std == nil || len(fd.Params) != len(rec.Params) {
		return nil
	}
	f := g.stdFuncByID(g.srcFA().StdFuncOf(fd))
	if f == nil || f.decl != fd || f.module == g.stdModule {
		return nil
	}
	if g.stdInsts == nil {
		// The shared lowering builds no instance, so a body of it that
		// makes this call declines.
		if len(fd.TypeParams) != 0 {
			return f
		}
		return nil
	}
	if tps, _ := g.stdInsts.stdTemplateParams(f); len(tps) != 0 {
		return f
	}
	return nil
}

// fillStdTemplateDefaults fills each slot of temps the call t of f, a
// generic function of another std module, leaves to its default
// (defaulted, ascending), with a call of the accessor the program builds
// for that default at t's type arguments, passing it the operands before
// slot. Every written operand is held first, so the written arguments run
// before any default, as they do at every other call, and each filled
// slot is held before the next accessor reads it.
func (bl *irScalarBuilder) fillStdTemplateDefaults(t *ast.Call, f *stdFunc, kinds []kind, temps []ir.Temp, defaulted []int) bool {
	g := bl.g
	s := g.stdInsts
	if s == nil {
		panic("irbuild: fillStdTemplateDefaults: only a body of the shared lowering has no instance table, and no std body names a parameter past the omitted default of another module's generic function (Iter.sort_by is the one such function)")
	}
	// The call names the function itself, so every type argument is the
	// checker's for the call: no receiver selected an impl block.
	tps, _ := s.stdTemplateParams(f)
	args, types, ok := g.calleeTypeArgs(t.Func, tps)
	if !ok {
		return false
	}
	accessors, ok := g.stdDefaultAccessors(f, tps, args, types, defaulted)
	if !ok {
		return false
	}
	bl.fillStdInstDefaults(t, kinds, temps, accessors)
	return true
}

// stdDefaultAccessors is the accessor of each default of f's instance at
// args that a call leaves out (defaulted, ascending), built at the same
// type arguments (stdInstances.defaultAccessor). ok is false when one does
// not build, whose reason it notes.
func (g *gen) stdDefaultAccessors(f *stdFunc, tps []string, args []kind, types []analysis.Type, defaulted []int) ([]*stdInst, bool) {
	out := make([]*stdInst, len(defaulted))
	for i, slot := range defaulted {
		inst, why := g.stdInsts.defaultAccessor(f, tps, args, types, slot, g)
		if inst == nil {
			g.dc.note(why)
			return nil, false
		}
		out[i] = inst
	}
	return out, true
}

// fillStdInstDefaults fills the slot of temps each of accessors answers
// (stdDefaultAccessors), ascending, with a call of it over the operands
// before the slot. Every written operand is held first, so the written
// arguments run before any default, as they do at every other call, and
// each filled slot is held before the next accessor reads it.
func (bl *irScalarBuilder) fillStdInstDefaults(at ast.Node, kinds []kind, temps []ir.Temp, accessors []*stdInst) {
	g := bl.g
	pos := g.irNodePos(at)
	for i, tmp := range temps {
		if tmp == ir.NoTemp || irHeldValue(bl.f, tmp, bl.sides) {
			continue
		}
		cp := ir.NewCopy(pos, bl.f.NewTemp(), tmp)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: kinds[i], copy: irCopyForce})
		temps[i] = cp.Dst()
	}
	for _, inst := range accessors {
		slot := inst.slot
		// The instance's signature is held to operand kinds (queue).
		k := inst.view.params[slot]
		c := ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, inst.sym, temps[:slot]...)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: k})
		cp := ir.NewCopy(pos, bl.f.NewTemp(), c.Dst())
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		temps[slot], kinds[slot] = cp.Dst(), k
	}
}
