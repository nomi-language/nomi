package irbuild

import (
	"slices"

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
// (analysis/iter_sensitive.go), so this file's predicates cover a control
// statement lexically inside the callback it belongs to, and a forwarding
// callback's call.
//
// # The two encodings
//
// `pub host fn loop<S>(f: (S) -> S) -> S` never mentions `Iter`. It has no
// source, no `Seq` and no `each_while`: it is a state-threading loop whose
// `break` is its terminator. The call is lowered inline, as blocks of the
// enclosing function, and each signal is a write and a jump (irloop.go).
//
// A callback the VM drives (irSignallingCallback) is a function of its own,
// so its `break` or `continue` cannot be a jump. A callback whose own scope
// holds one is lowered under a signalling form (irCtlForm, irearlyreturn.go)
// whose answer tells the driver whether to keep the value, skip or stop.

// --- deciding where a callback shape is widened -------------------------------

// ctrlSignalIn reports whether a node's OWN scope can produce a control signal.
//
// This is the specialization rule, and it exists so the general lambda
// representation does not have to carry one: only a callback whose body
// can `break` or `continue` is lowered under a signalling form (irCtlForm),
// and every other lowered lambda answers its plain value.
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

// irSignallingCallback reports whether the std function key names, by what
// the call's record says it is qualified with (recordedQualRoute), is an operation
// whose second operand the VM drives as a signalling callback: every
// two-operand callback slot analysis/iter_sensitive.go's iterCallbackSlots
// admits. `Iter.loop` takes its callback first and is inlined (iterLoop).
// A program's type of any name has no std identity, so it never answers.
func irSignallingCallback(key stdQualKey) bool {
	switch key.Owner {
	case stdIterType:
		switch key.Method {
		case "reduce", "scan", "map_reduce", "map", "filter", "take_while", "drop_while", "flat_map", "each", "iterate", "find", "any_match", "all_match":
			return true
		}
	case stdMapType:
		return key.Method == "map_values" || key.Method == "map_keys"
	}
	return false
}

// irSeededIterMethods are the Iter methods that fold from a starting
// value, their `from` argument.
var irSeededIterMethods = []string{"reduce", "scan", "map_reduce"}

// irSeededIterMethod reports whether `Iter.method` folds from a starting
// value (irSeededIterMethods).
func irSeededIterMethod(method string) bool {
	return slices.Contains(irSeededIterMethods, method)
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
