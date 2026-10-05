package irbuild

// A function body opened as an `ir.Func` and retained past lowering for the
// VM.
//
// A node's position may span lines (a triple-quoted literal's `${…}` hole, a
// pipe chain written one stage per line); `ir.Pos` carries an end for that.
// See irspan.go.
//
// `ir.Func.Def(t)` is the instruction that writes t. A retained function
// answers for its own operands; `ir.Lint` checks the answer is total and
// ordered. The result slot is an `ir.Slot` in the graph, and the copy into it
// is an `ir.Copy` in the function's own namespace.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irScalarArithShape names the one function shape this mechanism retains, so a
// report and a probe agree on the name.
const irScalarArithShape = "scalar arithmetic body"

// irFuncFrame is the builder's kind for each temporary a destructuring
// prologue names in one function.
type irFuncFrame struct {
	kinds []kind
}

func newIRFuncFrame(f *ir.Func) *irFuncFrame {
	return &irFuncFrame{kinds: make([]kind, f.NumTemps()+1)}
}

// kindOf is the kind the prologue recorded for t, or kindInvalid.
func (fr *irFuncFrame) kindOf(t ir.Temp) kind {
	if int(t) >= len(fr.kinds) {
		return kindInvalid
	}
	return fr.kinds[t]
}

// setKind records the kind t holds.
func (fr *irFuncFrame) setKind(t ir.Temp, k kind) {
	for int(t) >= len(fr.kinds) {
		fr.kinds = append(fr.kinds, kindInvalid)
	}
	fr.kinds[t] = k
}

// --- observation ------------------------------------------------------------

// irFuncObserved is a test-only hook, nil in production, called once per
// function the builder lowers a body for: with the function's retained
// `ir.Func` and whether the Go reader read it back off the graph, or with
// nil when the shape declined.
//
// It has two producers, and the first argument says which. `funcDecl`
// reports a user program's module-scope `fn`; `emitStdFunc` reports a
// Nomi-bodied `std/` declaration. The two populations are not separable by
// the name, so the origin travels on the `irFuncSig`. See irstdbody.go.
//
// It is a hook rather than a counter field because a caller that needs it runs
// over the whole corpus through `Generate`, which hands back no gen. It costs
// one nil check per lowered function.
//
// The builder's grammar is wider than the Go reader spells, so "retained" and
// "read back to Go" are two figures, and the readBack argument reports the
// second.
var irFuncObserved func(origin irFuncOrigin, name string, f *ir.Func, readBack bool)

// --- the lowering pass ------------------------------------------------------

// irRetentionDeclined is a test-only hook, nil in production, fired once per
// build attempt the builder DECLINED — with everything a FULL per-body
// blocker walk needs.
//
// WHY THE FIRST-DECLINE CENSUS CANNOT ANSWER THIS. `IRDeclineObserved`
// reports the FIRST reason an attempt declined for and nothing else —
// `irDeclineSeen` is the guard and irdecline.go's header says so. So a body
// recorded against `*ast.Call` may also hold a `Case` and a `StringInterp`
// the builder never reached, and a cumulative retention curve built on the
// census would read far steeper than reality. The walk that answers "which
// constructs does THIS body need" has to re-enter the builder AFTER it gave
// up, which only a caller holding the builder's inputs can do.
//
// FIRED ONLY ON A DECLINE, and that is load-bearing rather than tidy. The
// walk calls `bl.lower`, which mints temporaries on `sh.fn` and appends to
// its blocks. On a decline `sh.fn` is ORPHANED — `irScalarLower` adds it to
// the module only on the success path below — so the mutation reaches no
// retained graph and no `ir.Lint` run. On a RETAINED body the same walk would
// corrupt the graph the VM is about to read. A retained body's blocker set is
// empty by definition, so restricting the hook loses nothing.
//
// FIRED AFTER `irFuncObserved`, so the two hooks are independent: a caller
// can read the first decline reason inside `irFuncObserved` before the walk
// drives `bl.lower`, which calls `irDeclineNote` many more times.
var irRetentionDeclined func(g *gen, fd *ast.FuncDef, sig irFuncSig, plan *tailPlan, sh *irFuncShell)

// irScalarLower is `funcDecl`'s body lowering for this shape: build, lint,
// retain, and read back WHEN THE GO READER CAN READ THE GRAPH. It answers
// whether it took the body.
//
// LINT RUNS BEFORE THE GO READER READS THE FUNCTION, which is the contract, and
// a violation PANICS. That is `requirePos`'s discipline one level up: a
// malformed `ir.Func` is a producer bug with no user input that reaches it, and
// the alternative — recording a refusal — would let a wrong graph reach a
// consumer while the tally reported a missing feature. See internal/ir/lint.go.
//
// `ir.LintModuleAdded` RATHER THAN `ir.Lint`, and it is checked at the same point
// rather than once at the end of the unit, for two reasons. A module-level violation — one declaration recorded
// twice — is a producer bug whose cause is the call that made the duplicate,
// so reporting it at the next retention is reporting it as close to the cause
// as this producer can get. And a unit whose LAST function is malformed would
// otherwise have lowered every other body before anything looked, which is
// the order `requirePos` exists to avoid.
//
// ADDED, NOT WHOLE: `LintModuleAdded` lints what the module gained since the
// last call, with the declared-once rule kept over the whole set. Re-linting
// every earlier function at every retention would be quadratic in the number
// of functions. `irLintFinished` runs the whole
// `ir.LintModule` once per module before any consumer sees it, which also
// covers a function changed after it was recorded.
//
// A RETAINED FUNCTION THE GO READER CANNOT SPELL IS STILL RETAINED AND STILL
// LINTED, because the VM reads the graph. Its Go body is a stub, and
// `irUnreadableWhy` names the reason.
//
// A DISCARDED LOWERING IS NOT OBSERVED AT ALL. A probe run retains nothing and
// is discarded, and a stdlib module's two fixed points re-lower every
// unsettled candidate on every round and throw the result away, so counting
// either would count a lowering that never happened.
func (g *gen) irScalarLower(fd *ast.FuncDef, sig irFuncSig, plan *tailPlan, sh *irFuncShell, want kind) (kind, bool) {
	// A body the reader does not spell leaves no Go naming a package, so the
	// imports its build recorded are dropped with it.
	p, ok := g.irScalarBuild(fd, sig, plan, sh)
	if !ok {
		irDeclineClose()
		if irFuncObserved != nil {
			irFuncObserved(sig.origin, sig.name, nil, false)
		}
		if irRetentionDeclined != nil {
			irRetentionDeclined(g, fd, sig, plan, sh)
		}
		return kindInvalid, false
	}
	if g.stdModule != "" {
		// A stdlib body replaces the graph irPrebuildStdBodies built for it.
		if old := g.irModule().FuncFor(p.fn.Sym()); old != nil {
			g.irModule().RemoveFunc(old)
		}
	}
	g.irModule().AddFunc(p.fn)
	if err := ir.LintModuleAdded(g.irModule()); err != nil {
		panic("irbuild: " + irScalarArithShape + ": " + err.Error())
	}
	if irFuncObserved != nil {
		irFuncObserved(sig.origin, sig.name, p.fn, true)
	}
	return p.result, true
}

// irLintFinished lints a finished module whole, once, before a consumer reads
// it. See irScalarLower for why the per-retention lint covers only what was
// added. A violation panics, for the same reason it does there.
func irLintFinished(m *ir.Module) {
	if m == nil {
		return
	}
	if err := ir.LintModule(m); err != nil {
		panic("irbuild: finished module: " + err.Error())
	}
}
