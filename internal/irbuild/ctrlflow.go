package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// `break` and `continue`, and the one construct that gives them a boundary this
// builder lowers: `Iter.loop`.
//
// # Where a `break` binds, which is three rules and not one
//
// A callback's outcome is one of five signals and `Iter.loop` reads all five.
// What is not obvious is where a `break` BINDS. Nomi's `return` is
// lambda-scoped, and `break` is not:
//
//   - Bare `break` and `continue` are NON-LOCAL. A continue is never caught at
//     a function boundary, and a break carrying Unit is re-raised there, so
//     both unwind past every enclosing lambda and `fn` to the nearest
//     iter-callback boundary.
//   - `break v` with a non-Unit `v` SPLITS. `v` becomes the enclosing
//     function's own result and a pending break is armed on the caller; the
//     enclosing callback then runs TO COMPLETION and its own value becomes the
//     break's payload. So a `break 700` inside a
//     lambda nested in a loop callback neither unwinds nor delivers 700 — the
//     loop ends with whatever the OUTER body evaluated to.
//   - A `break` with no reachable boundary is a STATIC error, from a whole-file
//     analysis of break/continue propagation
//     (analysis/iter_sensitive.go:1152).
//
// The consequence for this file is that only the FIRST shape — a control
// statement lexically inside the callback whose boundary we emitted — is
// lowered. Everything else takes the `break` / `continue` refusal, because
// reproducing a payload the language silently rewrites is not something to
// improvise.
//
// # The encoding: Go's own break, because `loop` has no closure
//
// `pub host fn loop<S>(f: (S) -> S): S` (std/iter.nomi:120) never mentions
// `Iter`. It has no source, no `Seq` and no `each_while` — it is a
// state-threading loop whose `break` is its terminator. So the call is lowered
// as an INLINE Go `for`, the callback's body is emitted directly into it, and
// the five signals become ordinary Go statements. No signal value, no second
// return, no sentinel, and nothing allocated: `loop` is not on the `Seq` hot
// path at all, so the per-element allocation question does not arise for it.
//
// The mapping, one line per signal:
//
//	ctrlBreakWith(v)  break v      ->  answer = v; break L
//	ctrlBreak         break        ->  answer = rt.Unit{}; break L
//	ctrlContinue      continue     ->  continue L
//	ctrlReturnWith(v) return v/tail->  state = v, then loop
//	ctrlReturn        Unit tail    ->  (no assignment), then loop
//
// `continue` in `Iter.loop` retries with the SAME state. Go's `continue` in a
// `for {}` with no post statement does exactly that, because the state is
// advanced only by the assignment at the bottom of the body, which `continue`
// skips.
//
// Labeled `break L` / `continue L` rather than bare, so a Nomi `break` inside a
// `case` arm that lowered to a Go `switch` still targets the loop.
//
// # Why the boundary cannot leak into a nested lambda
//
// Not a syntactic pre-walk. `gen.ctrl` is saved and cleared at each function
// boundary, as gen.result and gen.inferResult are, so a `break` inside a
// lambda nested in the body finds no boundary and falls to the refusal. The default-refuse posture is what makes
// that sound without whole-program analysis: every control statement in the
// tree refuses unless a boundary claims it, so an unclaimed one refuses the
// whole file rather than being silently dropped.

// ctrlBoundary is the innermost control boundary being emitted.
//
// One struct rather than a set of gen fields because it is saved and restored
// as a unit, and because a nested `Iter.loop` inside another's body has to
// resolve against its OWN boundary — node identity is not needed here the way
// it is for the reduce seed, since the builder reaches the inner boundary only
// while it is the innermost one.
type ctrlBoundary struct {
}

// --- deciding where a callback shape is widened -------------------------------

// ctrlSignalIn reports whether a node's OWN scope can produce a control signal.
//
// This is the specialization rule, and it exists so the general lambda
// representation does not have to carry one: only a callback whose body really
// can `break` or `continue` gets the widened `(acc, bool)` Go type, and every
// other lowered lambda stays a bare func literal Go's inliner sees through.
//
// Does NOT descend into a nested `ast.Lambda` or `ast.FuncDef`, and not by
// analogy with `return`: the answer for a
// nested `break v` is a payload REWRITE — the value is local, the termination
// is not, and the enclosing callback's own value replaces the payload. So a
// nested signal is neither this
// callback's nor nothing, and the honest lowering is to leave it refused. A
// nested `break` therefore does not widen this callback (no over-count) and
// still refuses at its own position (no silent drop).
//
// Reflective child walk rather than a hand-written switch, for childNodes'
// reason: a node kind somebody forgets would silently return no children, and
// here that would mean a callback that needed widening did not get it.
func ctrlSignalIn(n ast.Node) bool {
	if isNilNode(n) {
		return false
	}
	switch n.(type) {
	case *ast.Break, *ast.Continue:
		return true
	case *ast.Lambda, *ast.FuncDef:
		return false
	}
	for _, c := range childNodes(n) {
		if ctrlSignalIn(c) {
			return true
		}
	}
	return false
}

// callbackCarriesSignal reports whether a call's callback ARGUMENT is a lambda
// literal whose own body can produce a control signal.
//
// Separate from ctrlSignalIn because the two answer opposite questions about
// the same node: ctrlSignalIn stops AT a lambda (it is walking one lambda's own
// scope), and this one starts INSIDE the callback's. Folding them into one
// function with a flag makes it easy for a call site to pass the lambda itself
// and silently get false.
func callbackCarriesSignal(arg ast.Node) bool {
	lam, isLambda := arg.(*ast.Lambda)
	if !isLambda {
		// A callback reached through a binding is refused for the seed's reason
		// before this matters, and a non-lambda cannot be walked for a boundary
		// this builder would own.
		return false
	}
	return ctrlSignalIn(lam.Body)
}

// irNamedCtlForm is the signalling form a named function's own body is lowered
// under when it uses `break` or `continue` at its own boundary (an
// iter-sensitive function, spec "Named Functions as Callbacks"). The checker
// admits such a function only as an iteration callback, so every use of its
// value is one, and its arity says which: two parameters is a reduction's
// `(acc, item)`, one is an adapter's or `Iter.each`'s element. Any other
// arity, or no direct signal, is irCtlNone.
func irNamedCtlForm(fd *ast.FuncDef) irCtlForm {
	if fd == nil || fd.Body == nil || !ctrlSignalIn(fd.Body) {
		return irCtlNone
	}
	switch len(fd.Params) {
	case 2:
		return irCtlReduce
	case 1:
		return irCtlAdapter
	}
	return irCtlNone
}

// --- Iter.reduce with a control signal ---------------------------------------

// --- Iter.loop ---------------------------------------------------------------

// --- the selectively-imported spelling ---------------------------------------
