package irbuild

import (
	"slices"
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A NAMED ARGUMENT THAT SKIPS A DEFAULTED PARAMETER.
//
// stdnamedarg.go placed named arguments onto a std callee's slots and then
// stated one restriction as a refusal:
//
//	CONTIGUITY. `stdlibInvoke` passes a dense positional prefix and
//	`stdCallArity` asks whether an arity-reduced wrapper exists for that
//	length, so the claimed slots must be exactly 0..n-1. Named arguments can
//	leave a HOLE — `Supervisor.new(restart: R, max_running: 1)` claims slots 0
//	and 2 — and no arity spells that.
//
// That is exactly right about the ARITY mechanism and it is the whole of the
// gap. The tour's "Where mutable state lives" block (concurrency.md:L1118)
// writes the hole:
//
//	Supervisor.new(max_running: 1, restart: Restart.Permanent)
//
// against `new(max_running: Int, shutdown_timeout: Duration = …, restart:
// Restart = …, backoff: Backoff = …, on_give_up: GiveUp = …)`, so slots 0 and 2
// are claimed and slot 1 — `shutdown_timeout`, which HAS a default — is not.
// Refusing it says the builder cannot name the default of a parameter whose
// default it already builds for the trailing case, which is not true.
//
// # WHY A PER-SLOT ACCESSOR AND NOT A WIDER WRAPPER FAMILY
//
// The arity wrappers are indexed by a LENGTH, and a hole needs a SLOT SET.
// Indexing them by slot set instead is 2^n functions per callee, decided at
// std-lowering time when no call site is known yet, so it prices a construct
// nobody wrote. One accessor per DEFAULTED PARAMETER is n functions and is
// enough: the holes are filled in ascending order, contiguity below the hole is
// what makes it a hole at all, and so slot i's accessor always has slots
// 0..i-1 in hand.
//
// It has to take them. A `fn`'s default MAY name an earlier parameter (the
// front end permits it for a `fn` and rejects it for an interface method —
// fillParamDefaults' header measures both), so the accessor's signature is the
// prefix and its body is `fillParamDefaults` over `params[:slot+1]`. That is
// the same single implementation of "what does an omitted argument mean" that
// the arity wrappers, module `fn` calls and impl-method calls already share, so
// there is still exactly one encoding of it in this package.
//
// # WHERE IT IS BUILT IS THE POINT, NOT AN IMPLEMENTATION DETAIL
//
// In the DECLARING module's gen, for emitStdDefaultWrappers' reason verbatim: a
// default is Nomi source belonging to the module that declared it, so
// `shutdown_timeout: Duration = Duration.seconds(5)` filled at the call site
// would need `Duration` resolvable in the CALLER's registry — and a caller that
// omits the argument has no reason to have imported it. Building the fill in
// std/supervisors' own module removes the question instead of answering it,
// and the caller sees a function of the prefix's shape.
//
// # EVALUATION ORDER
//
// Nomi evaluates every WRITTEN argument and then fills the omitted slots, and
// the arity wrappers reproduce that by construction — a default
// evaluated inside the callee necessarily runs after every argument at the call.
// An interior fill is built at the CALL, so it would otherwise interleave: a
// call's arguments are evaluated left to right, and a fill at slot 1 would run
// before a written argument at slot 2. So when a hole is being filled, EVERY
// written argument is forced into a temporary first — including the
// last-evaluated one, which the hole-free path deliberately leaves inline — and
// the fills are then the only thing left to evaluate. That restores "written
// arguments first, defaults after" for an effectful default.

// stdSlotName is the symbol name of the accessor that evaluates one std
// parameter's default.
func stdSlotName(f *stdFunc, slot int) string {
	return f.key + " default " + strconv.Itoa(slot)
}

// callDefaultFill reports whether one parameter has an accessor, read on the
// CANONICAL *stdFunc.
//
// Through the canonical pointer for callArityMin's reason: `bindStdSiblings` and
// `stdInstInvoke` both take `view := *f` copies, some of them before the
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

// emitStdSlotDefaults emits one accessor per defaulted parameter, recording each
// on f as it is written out.
//
// Every defaulted parameter and not only the interior ones, because whether a
// parameter is interior is a property of a CALL SITE and no call site is known
// here. A trailing default therefore gets both an accessor and its arity
// wrapper; the wrapper is what a short positional call uses, and the accessor is
// what a call that skips it by name uses.
//
// Only beside a body this module holds, as irStdArityRetain requires: a body
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
		// A parameter kind with no representation would put a meaningless Go
		// type in the accessor's signature. `emitStdFunc` succeeding does not
		// promise every parameter projected — `f.why` is about the whole
		// signature — so the check is here and not inherited.
		// kindInvalid: lookup — asks whether this slot has a Go type to write; skipping leaves the hole unfillable and the contiguity refusal stands.
		if f.params[i] == kindInvalid || !slotPrefixRepresentable(f, i) {
			continue
		}
		if f.defaultFill == nil {
			f.defaultFill = make([]bool, len(f.params))
		}
		f.defaultFill[i] = true
		g.irStdSlotRetain(f, fd, 0, i)
	}
}

// slotPrefixRepresentable reports whether every parameter BEFORE slot has a Go
// type, which the accessor's signature needs because a default may name an
// earlier parameter.
func slotPrefixRepresentable(f *stdFunc, slot int) bool {
	for i := range slot {
		// kindInvalid: lookup — one unrepresentable prefix parameter means no accessor signature can be written.
		if f.params[i] == kindInvalid {
			return false
		}
	}
	return true
}

// highestSlot is the last parameter slot the plan claims, plus one — the length
// a filled argument vector has.
func highestSlot(plan argPlan) int {
	high := 0
	for _, s := range plan.slots {
		if s+1 > high {
			high = s + 1
		}
	}
	return high
}

// A GENERIC STD FUNCTION'S DEFAULT, FROM OUTSIDE ITS MODULE.
//
// The accessors above are built beside a body the shared lowering retains,
// and it retains no generic one: `Iter.sort_by<T, K>(source, direction:
// Direction = Direction.Ascending, key)` has no `_DEFAULT<slot>`. A call
// that names `key:` and leaves `direction` out still needs the default as
// std/iter means it, where `Direction` is in scope and the checker's
// records of the default's nodes are; the caller's file may import
// neither. So the program builds the accessor, in a gen of the declaring
// module at the call's type arguments (stdInstances.defaultAccessor), and
// the call fills the slot with a call to it. This holds whether the
// function's body is instantiated from std's source or the builder lowers
// the operation itself, as it does `Iter.sort_by`.

// stdTemplateDefaults is the generic function of another std module that
// rec's defaults belong to, when a slot temps leaves out has a default
// that is no literal (irClosedLiteral, which reads the same in any file),
// or nil. A monomorphic std function's call never reaches here with a
// hole: stdShortCall plans it against the declaring module's accessors.
func (bl *irScalarBuilder) stdTemplateDefaults(fa *ast.FieldAccess, rec *analysis.DefaultedCall, temps []ir.Temp) *stdFunc {
	g := bl.g
	fd, isFunc := rec.Decl.(*ast.FuncDef)
	if !isFunc || g.std == nil || len(fd.Params) != len(temps) {
		return nil
	}
	literal := true
	for i, tmp := range temps {
		if tmp == ir.NoTemp && !irClosedLiteral(fd.Params[i].Default) {
			literal = false
		}
	}
	if literal {
		return nil
	}
	owner, module, isStd := bl.stdShortOwner(fa)
	if !isStd {
		return nil
	}
	for _, f := range g.stdShortCandidates(owner, fa.Field.Name, module) {
		if f.decl != fd || f.module == g.stdModule {
			continue
		}
		if g.stdInsts == nil {
			// The shared lowering builds no instance, so a body of it
			// that makes this call declines.
			if len(fd.TypeParams) != 0 {
				return f
			}
			continue
		}
		if tps, _ := g.stdInsts.stdTemplateParams(f); len(tps) != 0 {
			return f
		}
	}
	return nil
}

// fillStdTemplateDefaults fills each slot of temps the call t of f, a
// generic function of another std module, leaves out, with a call of the
// accessor the program builds for that default at t's type arguments.
// Every written operand is held first, so the written arguments run before
// any default, as they do at every other call.
func (bl *irScalarBuilder) fillStdTemplateDefaults(t *ast.Call, f *stdFunc, kinds []kind, temps []ir.Temp) bool {
	g := bl.g
	s := g.stdInsts
	if s == nil {
		g.dc.note("a generic stdlib function's default, outside a program: " + f.key)
		return false
	}
	// The call names the function itself, so every type argument is the
	// checker's for the call: no receiver selected an impl block.
	tps, _ := s.stdTemplateParams(f)
	args, ok := g.calleeTypeArgs(t.Func, tps)
	if !ok {
		return false
	}
	pos := g.irNodePos(t)
	for i, tmp := range temps {
		if tmp == ir.NoTemp || irHeldValue(bl.f, tmp, bl.sides) {
			continue
		}
		cp := ir.NewCopy(pos, bl.f.NewTemp(), tmp)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: kinds[i], copy: irCopyForce})
		temps[i] = cp.Dst()
	}
	for slot := range temps {
		if temps[slot] != ir.NoTemp {
			continue
		}
		if stdDefaultReadsParams(f.decl, slot) {
			g.dc.note("a generic stdlib function's default that reads an earlier parameter: " + f.key)
			return false
		}
		inst, why := s.defaultAccessor(f, tps, args, slot, g)
		if inst == nil {
			g.dc.note(why)
			return false
		}
		// The instance's signature is held to operand kinds (queue).
		k := g.stdProgHere(inst.view.params[slot])
		c := ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, inst.sym)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: k})
		cp := ir.NewCopy(pos, bl.f.NewTemp(), c.Dst())
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		temps[slot], kinds[slot] = cp.Dst(), k
	}
	return true
}

// stdDefaultReadsParams reports whether the default of fd's parameter slot
// may read a parameter before it: it names one, or one before it is a
// destructuring pattern, whose names it may read. A lambda parameter of
// the same name counts too, which only declines more.
func stdDefaultReadsParams(fd *ast.FuncDef, slot int) bool {
	var names []string
	for _, p := range fd.Params[:slot] {
		if p.Destructure != nil {
			return true
		}
		names = append(names, p.Name)
	}
	reads := false
	ast.Inspect(fd.Params[slot].Default, func(n ast.Node) bool {
		if id, isIdent := n.(*ast.Ident); isIdent && slices.Contains(names, id.Name) {
			reads = true
		}
		return !reads
	})
	return reads
}
