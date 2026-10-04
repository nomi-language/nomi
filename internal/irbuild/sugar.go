package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// Parameter-shape sugar: default values, destructuring parameters, and the slot
// an argument actually fills.
//
// All three are places where one Nomi parameter is not one lowered parameter.
// They are together because they share one rule: a parameter list is resolved
// in the CALLEE's environment, and by SLOT rather than by position.
//
// # Where a default is evaluated, and why it is not the call site
//
// It reads like call-site sugar — Go has no default arguments, so something at
// the call has to supply the value — but Nomi does not evaluate it there. For
// each parameter the call left out, the default is evaluated in the callee's
// scope: a fresh environment off the FUNCTION'S OWN CLOSURE, after parameters
// 0..i-1 have been bound into it. Three consequences, and each one is
// observable:
//
//  1. A default may name an earlier PARAMETER. `fn f(a: Int, b: Int = a + 1)`
//     works, because `a` is already bound when `b`'s default runs, and so
//     does `c: Int = b * 10` after it.
//  2. A default may NOT name a caller local, and a default that names a
//     module-level binding sees the MODULE's, even where the caller shadows
//     that name. Emitting the expression in the caller's scope would resolve it
//     the other way round — which is the whole reason fillDefaults swaps the
//     scope stack instead of pushing onto it.
//  3. It is evaluated once per activation that omits it, and NOT at all when
//     the argument is supplied — so a default with a side effect fires exactly
//     as often as the call omits it. testdata/defaults.nomi prints from inside
//     a default and calls the function both ways, which is what distinguishes
//     this from a lowering that hoisted the default to a constant.
//
// That scope is also an assertion-trace boundary, so nothing a default computes
// reaches an enclosing assertion's report. Same seam as a lambda body.
//
// # Why no wrapper function per arity
//
// The obvious encoding is one function per callable arity, forwarding to the
// full one. It is correct and it is not needed: this builder already knows every
// call site, so the arity is a compile-time fact and the expansion can be
// inlined at the call with the callee's scope reconstructed. A wrapper would add
// a function and a call per arity for no semantic gain, and the interesting
// property — that an activation allocates nothing — is preserved either way,
// which BenchmarkDefaultedCall measures rather than assumes.
//
// # A destructuring parameter
//
// One value, several names. Go has no destructuring, so the value arrives in
// one anonymous Go parameter and each name the pattern introduces becomes an
// ordinary local read out of it. lambda.go already does this for a lambda; a
// `fn` parameter routes through the same destructure(). The pattern is
// IRREFUTABLE — the checker rejects a
// refutable one with a "use a `case` in the body" diagnostic — so there is no
// mismatch branch, and destructureEnum's single-variant guard is the enforceable
// half of that claim.

// paramKind is a parameter's kind, resolved without emitting a refusal.
//
// signature() runs before any body, for every declaration in the module
// including ones no call reaches, so it must not blame a position — funcDecl
// names the reason for the parameters it actually emits. The two must agree on
// WHICH parameters resolve, which is why this is the one implementation and
// patternParamKind is its reporting counterpart.
func (g *gen) paramKind(p ast.Param) kind {
	if p.TypeAnnotation != nil {
		return g.typeOf(p.TypeAnnotation)
	}
	if p.Destructure == nil {
		// No annotation and no pattern: a `fn` parameter must declare its type,
		// and a defaulted one is no exception here — inferring it from the
		// default would be a second inference path inside a backend.
		return kindInvalid
	}
	// The self-typing rule: a pattern whose head NAMES a type is its own
	// annotation. See patternParamKind for the reporting version.
	var head ast.TypeExpr
	switch pat := p.Destructure.(type) {
	case *ast.StructPattern:
		head = pat.TypeName
	case *ast.EnumPattern:
		head = pat.Variant
	}
	if head == nil {
		return kindInvalid
	}
	owner, member, ok := patternHead(head)
	if !ok {
		return kindInvalid
	}
	name := owner
	if name == "" {
		name = member
	}
	if d, found := g.types[name]; found && d.lowerable {
		return named(d)
	}
	return kindInvalid
}

// --- call-site argument placement ------------------------------------------

// argExpr is the expression an argument contributes. For a named argument that
// is its VALUE; the name is placement, not a value.
func argExpr(a ast.Node) ast.Node {
	if na, ok := a.(*ast.NamedArg); ok {
		return na.Value
	}
	return a
}

// sigParamNames is the callee's parameter names positionally, or nil when the
// signature has no declaration behind it.
//
// nil is the honest answer rather than a placeholder: a synthesized signature
// records parameter KINDS only, so there is no name to match a named argument
// against and argSlotPlan must decline rather than guess a slot.
func sigParamNames(sig *fnSig) []string {
	if sig == nil || sig.decl == nil || len(sig.decl.Params) != len(sig.params) {
		return nil
	}
	names := make([]string, len(sig.decl.Params))
	for i, p := range sig.decl.Params {
		names[i] = p.Name
	}
	return names
}

// argPlan is the resolved placement of a call's written arguments.
type argPlan struct {
	// slots maps written-argument index to the parameter slot it fills. A
	// parameter slot absent from this vector is one no argument claimed, which
	// is what fillDefaults completes.
	slots []int
	// order is the written-argument indices in EVALUATION order, which is NOT written order once a named argument is present. See
	// the header note below.
	order []int
}

// argSlotPlan maps each written argument to the parameter slot it fills, and
// says in what order they are evaluated.
//
// This is one of several implementations of one rule and the requirement is
// that they agree: the analyzer's own copy, this, and whatever consumes it.
//
// # ANOTHER WALK EXISTS AND IS DELIBERATELY NOT A MEMBER
//
// A PARTIAL APPLICATION's arguments are placed by a DIFFERENT rule: its
// positional index never skips a slot a name has claimed and it has no
// trailing-callback routing. The two disagree on a reachable program:
//
//	fn two_skip(req: Int, opt_a: String = "a", cb: (Int) -> Int): Int { cb(req) }
//	p = two_skip(5, opt_a: "z", _)
//
//	nomi run     ->  line 6: parameter 'opt_a' already has a value    exit 1
//	this walk    ->  the hole SKIPS the claimed slot and lands on `cb`
//
// So routing a partial through here lowers a program Nomi refuses to run.
// `partialSlotPlan` in placeholder.go is the walk a partial must use. Named
// here because "they agree" read at face value is exactly the sentence that
// would send a reader to this function.
//
// `names` is the callee's parameter names, positionally. Pass nil — or a
// short slice — and named arguments cannot be placed; the caller must refuse
// them under `named argument` rather than guess. Positional behaviour is
// unaffected by nil, so a caller with no names available keeps working.
//
// Reports false when a named argument names no parameter, or when two arguments
// claim one slot. Both are errors ("unknown parameter" / "already has a
// value"), so this DECLINES rather than lowering a diagnostic for them.
//
// # EVALUATION ORDER IS NOT WRITTEN ORDER, and this is a DIFF not a refusal
//
// Nomi PARTITIONS the written arguments into positionals and named arguments
// and then drains them in sequence, so every positional is evaluated before
// any named argument and a named argument written FIRST is evaluated LAST.
// With an effectful argument:
//
//	fn p(tag: String): String { io.print("eval " + tag)  tag }
//	io.print(f(b: p("B"), p("A")))      =>  eval A / eval B / A/B
//
// Written order is B-then-A; evaluation order is A-then-B. A caller that
// lowers each argument in turn over `t.Args` would evaluate them in written
// order and differ on any call mixing a named argument before an effectful
// positional. `order` exists for exactly that, and every caller that
// hoists MUST walk it instead of the argument slice.
//
// Two neighbouring positions do not have the hazard:
// struct-literal field initialisers and list-literal elements both evaluate in
// WRITTEN order (a struct literal uses written order, NOT declared-field
// order). The argument list is the odd one out because it is the one
// position whose written sequence is partitioned before evaluation.
//
// # The three placement rules
//
//  1. Positionals fill left-to-right, skipping slots a name has claimed — but
//     ONLY for a positional written AFTER a named argument. That asymmetry is
//     what makes `each(items, opts: 8, handler)` route `handler` past claimed
//     slot 1 while keeping `add(1, a: 2)` the duplicate it reads as.
//  2. The trailing-lambda exception: when a call under-fills
//     and its last positional is a lambda or a block — or a bare name whose own
//     slot does not take a function while the last parameter does — that
//     argument MOVES to the last slot and the slot it vacates falls back to its
//     default. SUPPRESSED when a named argument targets the last parameter
//     (`namedTargetsLast` below), so
//     `Task.spawn_all(items, |x| f(x), max_running: 8)` does not move the lambda
//     onto `max_running`.
//  3. Named arguments fill their own slots last.
//
// Rule 2 has to be decided BEFORE the arguments are lowered, because it is the
// SLOT and not the position whose declared type each argument is checked
// against. Reading it off the position instead type-checks `|n| n + 1` as an Int
// and reports a mismatch for a call the checker accepted — and in the shape
// where the two slots agree, `fn pick(a: Int = 1, b: Int = 2)` called
// `pick({ 5 })`, it silently fills the wrong one and answers 52 where the
// right answer is 15.
func argSlotPlan(args []ast.Node, params []kind, names []string) (argPlan, bool) {
	plan := argPlan{
		slots: make([]int, len(args)),
		order: make([]int, 0, len(args)),
	}
	for i := range plan.slots {
		plan.slots[i] = -1
	}
	// Partition into positionals and named arguments. `afterNamed` records which
	// positionals may skip a claimed slot; see rule 1.
	var positional, named []int
	var afterNamed []bool
	sawNamed := false
	for i, a := range args {
		if _, isNamed := a.(*ast.NamedArg); isNamed {
			named = append(named, i)
			sawNamed = true
			continue
		}
		positional = append(positional, i)
		afterNamed = append(afterNamed, sawNamed)
	}
	paramIdx := make(map[string]int, len(names))
	for i, n := range names {
		if i >= len(params) {
			break
		}
		paramIdx[n] = i
	}
	claimedByName := make([]bool, len(params))
	for _, i := range named {
		slot, ok := paramIdx[args[i].(*ast.NamedArg).Name]
		if !ok {
			// No name table, or a name no parameter has. Either way this walk
			// cannot place it and must not pretend to.
			return argPlan{}, false
		}
		if claimedByName[slot] {
			return argPlan{}, false
		}
		claimedByName[slot] = true
	}
	filled := make([]bool, len(params))
	next := 0
	for n, i := range positional {
		if afterNamed[n] {
			for next < len(params) && claimedByName[next] {
				next++
			}
		}
		if next >= len(params) {
			// More positionals than slots. The arity check reports this; the
			// plan just declines to invent a slot.
			return argPlan{}, false
		}
		plan.slots[i] = next
		filled[next] = true
		next++
	}
	// Rule 2, with its restriction.
	namedTargetsLast := len(params) > 0 && claimedByName[len(params)-1]
	if len(positional) > 0 && len(positional) <= len(params) && !namedTargetsLast {
		lastWritten := positional[len(positional)-1]
		lastSlot := plan.slots[lastWritten]
		lastParam := len(params) - 1
		if lastSlot != lastParam && !filled[lastParam] {
			routed := false
			switch args[lastWritten].(type) {
			case *ast.Lambda, *ast.Block, *ast.FieldAccessor:
				routed = true
			case *ast.Ident:
				// The SLOT the argument currently occupies decides, not its
				// written position. Identical while every argument is
				// positional; different the moment a name claimed an earlier
				// slot, which is why this reads lastSlot.
				routed = params[lastParam].tag == tagFunc && params[lastSlot].tag != tagFunc
			}
			if routed {
				plan.slots[lastWritten] = lastParam
				filled[lastParam] = true
				filled[lastSlot] = false
			}
		}
	}
	for _, i := range named {
		slot := paramIdx[args[i].(*ast.NamedArg).Name]
		if filled[slot] {
			// A positional already took the slot this name asks for —
			// `add(1, a: 2)`, which is the "already has a value" error.
			return argPlan{}, false
		}
		plan.slots[i] = slot
		filled[slot] = true
	}
	// EVALUATION ORDER: every positional, then every named. See the header.
	plan.order = append(plan.order, positional...)
	plan.order = append(plan.order, named...)
	return plan, true
}
