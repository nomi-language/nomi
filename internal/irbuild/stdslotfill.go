package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
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
func (g *gen) emitStdSlotDefaults(f *stdFunc, fd *ast.FuncDef) {
	if len(fd.Params) != len(f.params) {
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
		g.irStdSlotRetain(f, fd, i)
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
