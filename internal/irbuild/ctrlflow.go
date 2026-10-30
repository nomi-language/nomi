package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `break` and `continue`, and the one construct that gives them a boundary this
// builder lowers: `Iter.loop`.
//
// # Where a `break` binds
//
// A callback's outcome is one of five signals and `Iter.loop` reads all five.
// A `break` or `continue` binds to the iteration whose callback holds it in
// its own scope, as `return` binds to the nearest function: nothing unwinds
// through a call. A named function that uses one (an iter-sensitive function)
// signals only to an iteration it is directly the callback of, passed as the
// callback itself or forwarded to as a callback's whole body
// (irsignalcall.go). The checker rejects a `break` or `continue` anywhere
// else, and a use of an iter-sensitive function anywhere else
// (analysis/iter_sensitive.go), so this file lowers a control statement
// lexically inside the callback whose boundary it emitted, and a forwarding
// callback's call.
//
// # The encoding: Go's own break, because `loop` has no closure
//
// `pub host fn loop<S>(f: (S) -> S) -> S` (std/iter.nomi:120) never mentions
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
//	ctrlBreak         break        ->  answer = state; break L
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

// callbackCarriesSignal reports whether a call's callback ARGUMENT is a
// function literal (a lambda, or a capture, which lowers as one) whose own
// body can produce a control signal: a `break` or `continue` of its own.
//
// Separate from ctrlSignalIn because the two answer opposite questions about
// the same node: ctrlSignalIn stops AT a lambda (it is walking one lambda's
// own scope), and this one starts INSIDE the callback's. Folding them into
// one function with a flag makes it easy for a call site to pass the lambda
// itself and silently get false.
func (g *gen) callbackCarriesSignal(arg ast.Node) bool {
	switch t := arg.(type) {
	case *ast.Lambda:
		return ctrlSignalIn(t.Body)
	case *ast.Capture:
		return ctrlSignalIn(t.Body)
	}
	// A callback reached through a binding is refused for the seed's reason
	// before this matters, and a non-lambda cannot be walked for a boundary
	// this builder would own.
	return false
}

// irSignallingCallback reports whether owner.method is an operation whose
// second operand the VM drives as a signalling callback: every two-operand
// callback slot analysis/iter_sensitive.go's iterCallbackSlots admits.
// `Iter.loop` takes its callback first and is inlined (iterLoop).
func irSignallingCallback(owner, method string) bool {
	switch owner {
	case "Iter":
		switch method {
		case "reduce", "scan", "map_reduce", "map", "filter", "take_while", "drop_while", "flat_map", "each", "iterate", "find", "any", "all":
			return true
		}
	case "Map":
		return method == "map_values" || method == "map_keys"
	}
	return false
}

// irSeededIterMethod reports whether `Iter.method` takes its seed from its
// callback lambda's first-parameter default: `reduce`, `scan` and
// `map_reduce`.
func irSeededIterMethod(method string) bool {
	return method == "reduce" || method == "scan" || method == "map_reduce"
}

// namedCtlForm is the signalling form a named function's own body is lowered
// under when it uses `break` or `continue` in its own scope (an
// iter-sensitive function, analysis.IterSensitive, spec "Named Functions as
// Callbacks"), or irCtlNone. The checker admits such a function only as an
// iteration callback itself, so every caller reads its answer as a signal:
// an iteration driver, or an `Iter.loop` over it as a named step
// (signallingCall). It is always the adapter form, whatever its arity,
// since the loop has to tell its `continue` from a value; a reduction reads
// the adapter's skip and stop as keeping its accumulator.
func (g *gen) namedCtlForm(fd *ast.FuncDef) irCtlForm {
	if !analysis.IterSensitive(fd) {
		return irCtlNone
	}
	return irCtlAdapter
}

// --- Iter.reduce with a control signal ---------------------------------------

// --- Iter.loop ---------------------------------------------------------------

// --- the selectively-imported spelling ---------------------------------------
