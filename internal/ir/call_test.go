package ir_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func callPos() ir.Pos { return ir.At("call.nomi", 7, 3) }

// TestIRCall_TheThreeFormsAreDistinguishable is the plant every negative below
// needs: the three constructors must build three DIFFERENT forms, or a test
// that says "a dispatched call carries a key" would pass on a direct one.
func TestIRCall_TheThreeFormsAreDistinguishable(t *testing.T) {
	tbl := ir.NewTable()
	f := tbl.Symbol("f", "f")
	m := tbl.Symbol("Display.to_string", "Display.to_string")

	direct := ir.NewCall(callPos(), 1, ir.OrdinaryCall, f, 2, 3)
	disp := ir.NewDispatchCall(callPos(), 4, ir.OrdinaryCall, m, 0, 5)
	indir := ir.NewIndirectCall(callPos(), 6, ir.OrdinaryCall, 7, 8)

	if direct.Form() != ir.CalleeDirect || disp.Form() != ir.CalleeDispatched ||
		indir.Form() != ir.CalleeIndirect {
		t.Fatalf("the three constructors built %v / %v / %v; every form-specific check "+
			"below is vacuous unless they differ", direct.Form(), disp.Form(), indir.Form())
	}
	if direct.Callee() != f || disp.Callee() != m {
		t.Errorf("a call's callee is not the symbol it was built with")
	}
	if indir.Callee() != nil {
		t.Errorf("an indirect call names a declaration (%v); its callee is an operand", indir.Callee())
	}
	if indir.Fn() != 7 {
		t.Errorf("the indirect callee operand is %v, want t7", indir.Fn())
	}
	if direct.Fn() != ir.NoTemp || disp.Fn() != ir.NoTemp {
		t.Errorf("a call with a resolved callee also carries a callee OPERAND, which is "+
			"two answers to one question: %v / %v", direct.Fn(), disp.Fn())
	}
	if direct.KeyAt() != -1 || indir.KeyAt() != -1 {
		t.Errorf("a non-dispatched call reports a dispatch key (%d / %d). Zero would be "+
			"indistinguishable from dispatching on operand zero, which is why the absent "+
			"value is -1", direct.KeyAt(), indir.KeyAt())
	}
	if disp.KeyAt() != 0 {
		t.Errorf("the dispatch key is operand %d, want 0", disp.KeyAt())
	}
	// AppendUses must include the callee operand for the indirect form and
	// must not invent one for the others: a consumer walking uses to build a
	// def-use chain would otherwise miss the callee entirely.
	if got := len(indir.AppendUses(nil)); got != 2 {
		t.Errorf("an indirect call uses %d temporaries, want 2 (the callee and one argument)", got)
	}
	if got := len(direct.AppendUses(nil)); got != 2 {
		t.Errorf("a direct call with two arguments uses %d temporaries, want 2", got)
	}
}

// TestIRCall_TheDispatchKeyCheckHasPower plants the accepting case beside the
// rejecting ones. A constructor that rejected every key would pass every
// rejection assertion here.
func TestIRCall_TheDispatchKeyCheckHasPower(t *testing.T) {
	tbl := ir.NewTable()
	m := tbl.Symbol("Tagger.tag", "Tagger.tag")

	// THE PLANT. `interface Tagger { fn tag(label: String, target: self) }`
	// dispatches on argument ONE, which is the whole reason this is a required
	// parameter and not a field defaulting to zero.
	one := ir.NewDispatchCall(callPos(), 1, ir.OrdinaryCall, m, 1, 2, 3)
	if one.KeyAt() != 1 {
		t.Fatalf("a key of 1 over two operands was not accepted, so the rejections below " +
			"prove nothing")
	}

	for _, tc := range []struct {
		name  string
		keyAt int
		args  []ir.Temp
	}{
		{"past the end", 2, []ir.Temp{2, 3}},
		{"negative", -1, []ir.Temp{2, 3}},
		{"no operands at all", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("a dispatched call keyed on operand %d of %d was accepted. "+
						"The key selects the implementation, so an out-of-range one is a "+
						"call that dispatches on nothing", tc.keyAt, len(tc.args))
				}
			}()
			ir.NewDispatchCall(callPos(), 1, ir.OrdinaryCall, m, tc.keyAt, tc.args...)
		})
	}
}

// TestIRCall_AnAbsentOperandIsRejected is the check that separates this node
// from `arith`, where NoTemp means "there is no such operand".
func TestIRCall_AnAbsentOperandIsRejected(t *testing.T) {
	tbl := ir.NewTable()
	f := tbl.Symbol("f", "f")

	// THE PLANT: a call with no operands at all is legal — `f()` — so the
	// rejection below is about a HOLE and not about emptiness.
	if got := ir.NewCall(callPos(), 1, ir.OrdinaryCall, f).NumArgs(); got != 0 {
		t.Fatalf("a zero-argument call was built with %d operands", got)
	}

	for _, tc := range []struct {
		name string
		args []ir.Temp
	}{
		{"a hole in the middle", []ir.Temp{2, ir.NoTemp, 3}},
		{"a hole at the end", []ir.Temp{2, ir.NoTemp}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("a call with an absent operand was accepted. Every slot of a " +
						"call is a value — a Unit argument still has a temporary — so a " +
						"NoTemp is a producer that skipped a slot and would emit a call " +
						"with a hole in its argument list")
				}
			}()
			ir.NewCall(callPos(), 1, ir.OrdinaryCall, f, tc.args...)
		})
	}
}

// TestIRCall_EveryConstructorRejectsAnUnstatedSite is `CallSite`'s half of the
// enforcement story `Pos` has: omitting it is a compile error, and passing the
// zero value is a panic.
func TestIRCall_EveryConstructorRejectsAnUnstatedSite(t *testing.T) {
	tbl := ir.NewTable()
	f := tbl.Symbol("f", "f")

	// THE PLANT, both ways round, because a constructor that rejected every
	// site would pass the rejections and a constructor that rejected none
	// would pass nothing else.
	if !ir.NewCall(callPos(), 1, ir.TailCall, f, 2).Tail() {
		t.Fatalf("ir.TailCall did not produce a tail call")
	}
	if ir.NewCall(callPos(), 1, ir.OrdinaryCall, f, 2).Tail() {
		t.Fatalf("ir.OrdinaryCall produced a tail call")
	}

	for _, tc := range []struct {
		name string
		make func()
	}{
		{"direct", func() { ir.NewCall(callPos(), 1, 0, f, 2) }},
		{"dispatched", func() { ir.NewDispatchCall(callPos(), 1, 0, f, 0, 2) }},
		{"indirect", func() { ir.NewIndirectCall(callPos(), 1, 0, 2, 3) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("a %s call with no stated site was accepted. The zero CallSite "+
						"would silently mean OrdinaryCall, and a tail call reported as an "+
						"ordinary one is the bit both consumers act on", tc.name)
				}
			}()
			tc.make()
		})
	}
}

// TestIRCall_ACalleeIsRequiredWhereTheFormNamesOne.
func TestIRCall_ACalleeIsRequiredWhereTheFormNamesOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func()
	}{
		{"direct with no symbol", func() { ir.NewCall(callPos(), 1, ir.OrdinaryCall, nil, 2) }},
		{"dispatched with no method", func() {
			ir.NewDispatchCall(callPos(), 1, ir.OrdinaryCall, nil, 0, 2)
		}},
		{"indirect with no callee operand", func() {
			ir.NewIndirectCall(callPos(), 1, ir.OrdinaryCall, ir.NoTemp, 2)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("%s was accepted, which is a call that names nothing to call", tc.name)
				}
			}()
			tc.make()
		})
	}
}

// TestIRCall_APositionIsMandatory holds this class to the per-node position
// rule.
func TestIRCall_APositionIsMandatory(t *testing.T) {
	tbl := ir.NewTable()
	f := tbl.Symbol("f", "f")
	defer func() {
		if recover() == nil {
			t.Errorf("a call with the zero position was accepted. A call needs the " +
				"call site's own position because it becomes the caller frame's line " +
				"in a stack trace")
		}
	}()
	ir.NewCall(ir.Pos{}, 1, ir.OrdinaryCall, f, 2)
}

// TestIRCall_TheCalleeIsAnIdentityAndNotAName is `Table.Symbol`'s contract read
// at this class: one declaration has one symbol however many calls name it, and
// two same-named declarations are two symbols.
//
// THIS IS THE PROPERTY THAT MAKES A `*Symbol` ENOUGH. Selection has already
// happened at a call, so the node only has to NAME its callee — but naming is
// worth nothing if two reads of one declaration are two names.
func TestIRCall_TheCalleeIsAnIdentityAndNotAName(t *testing.T) {
	tbl := ir.NewTable()

	type decl struct{ n int }
	calendarError, jsonError := &decl{1}, &decl{2}

	a := tbl.Symbol(calendarError, "Error.to_string")
	b := tbl.Symbol(calendarError, "Error.to_string")
	c := tbl.Symbol(jsonError, "Error.to_string")

	if a != b {
		t.Errorf("two calls to one declaration named two symbols, which makes the callee a " +
			"receipt for a lookup rather than an identity")
	}
	if a == c {
		t.Errorf("two same-named declarations in two modules named ONE symbol. That is the " +
			"defect this repository has already had: `calendar.Error` and `json.Error` " +
			"compared equal and produced `expected Error, got Error`")
	}
	if tbl.Symbols() != 2 {
		t.Errorf("three reads of two declarations interned %d symbols, want 2", tbl.Symbols())
	}
	// The symbols are usable as callees and the calls stay distinguishable.
	if ir.NewCall(callPos(), 1, ir.OrdinaryCall, a, 2).Callee() ==
		ir.NewCall(callPos(), 3, ir.OrdinaryCall, c, 4).Callee() {
		t.Errorf("two calls to two declarations name one callee")
	}
}

// TestIRCall_AHostCallIsNotAForm is the COLLAPSE, asserted rather than left to
// the prose. A reader sizing this class names five populations; three of them
// are forms, one collapses and one is refused.
//
// The collapse: whether the callee's body is Nomi or Go is a fact about the
// DECLARATION. Two calls that differ only in that are the same instruction, and
// the model has no way to tell them apart — which is the assertion.
func TestIRCall_AHostCallIsNotAForm(t *testing.T) {
	tbl := ir.NewTable()

	type decl struct{ n int }
	nomiBodied, hostBacked := &decl{1}, &decl{2}
	nomiFn := tbl.Symbol(nomiBodied, "strings.String.split")
	hostFn := tbl.Symbol(hostBacked, "int.Int.abs")

	a := ir.NewCall(callPos(), 1, ir.OrdinaryCall, nomiFn, 2)
	b := ir.NewCall(callPos(), 3, ir.OrdinaryCall, hostFn, 4)

	if a.Form() != b.Form() {
		t.Errorf("a Nomi-bodied callee and a host-backed one produced %v and %v. "+
			"Splitting them would put the callee's implementation LANGUAGE in the opcode, "+
			"and every host function is one operation, not one each", a.Form(), b.Form())
	}
	if a.NumArgs() != b.NumArgs() {
		t.Errorf("the two shapes disagree about arity, which they must not")
	}
}

// TestIRCall_TailIsTheOnlyPredicate pins the field count: a change adding a
// second predicate to this node rewrites this test and states its three-part
// argument.
func TestIRCall_TailIsTheOnlyPredicate(t *testing.T) {
	tbl := ir.NewTable()
	f := tbl.Symbol("f", "f")

	ordinary := ir.NewCall(callPos(), 1, ir.OrdinaryCall, f, 2)
	tail := ir.NewCall(callPos(), 1, ir.TailCall, f, 2)

	// The two differ in EXACTLY the predicate, and in nothing else a consumer
	// reads. That is what makes `Tail()` a bit on the node rather than a
	// different operation.
	if ordinary.Form() != tail.Form() || ordinary.Callee() != tail.Callee() ||
		ordinary.NumArgs() != tail.NumArgs() || ordinary.KeyAt() != tail.KeyAt() {
		t.Errorf("tail position changed something other than the predicate")
	}
	if ordinary.Tail() == tail.Tail() {
		t.Errorf("the predicate does not vary between OrdinaryCall and TailCall")
	}
	if ordinary.Site() != ir.OrdinaryCall || tail.Site() != ir.TailCall {
		t.Errorf("Site() does not round-trip the value the constructor was given")
	}
	// AND IT IS AVAILABLE AT EVERY FORM, including the indirect one. That is
	// deliberate: a call through a function value can be in tail position, and
	// a model that could not express the shape would make it uncountable.
	if !ir.NewIndirectCall(callPos(), 1, ir.TailCall, 2, 3).Tail() {
		t.Errorf("an indirect call cannot be in tail position in this model. " +
			"`fn applies?(v: Int, f: (Int) -> Bool): Bool { f(v) }` is one, and it is the " +
			"shape of every one-line higher-order wrapper")
	}
}

// TestIRCall_TheRenderingNamesTheFormAndTheSite, because a node's String is
// what a debugging reader trusts, and a rendering that hid the form or the site
// would make two different instructions read identically.
func TestIRCall_TheRenderingNamesTheFormAndTheSite(t *testing.T) {
	tbl := ir.NewTable()
	f := tbl.Symbol("f", "shift")
	m := tbl.Symbol("Tagger.tag", "Tagger.tag")

	for _, tc := range []struct {
		node *ir.Call
		want []string
	}{
		{ir.NewCall(callPos(), 4, ir.OrdinaryCall, f, 1, 2), []string{"t4 = call shift(t1, t2)"}},
		{ir.NewCall(callPos(), 4, ir.TailCall, f, 1), []string{"t4 = tail call shift(t1)"}},
		{ir.NewDispatchCall(callPos(), 4, ir.OrdinaryCall, m, 1, 1, 2),
			[]string{"dispatch", "Tagger.tag@1"}},
		{ir.NewIndirectCall(callPos(), 4, ir.OrdinaryCall, 9, 1), []string{"value t9"}},
	} {
		got := tc.node.String()
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%q does not contain %q", got, w)
			}
		}
	}
}

// TestIRCall_ACallIsStraightLine is the model's def-use chain, built with real
// temporaries so the consumer-side pin at zero has something to be measured
// against.
//
// `f(g(x), h(x))` is four instructions in one block, no branch and no
// terminator: two calls whose destinations are the third's operands. Nothing
// about a calling convention appears — the frame, the argument order, the
// defaults and the coercions are all below this level.
func TestIRCall_ACallIsStraightLine(t *testing.T) {
	fn := ir.NewFunc(callPos(), "main")
	b := fn.NewBlock(callPos(), "entry")
	tbl := ir.NewTable()

	x := fn.NewTemp()
	gd, hd, fd := fn.NewTemp(), fn.NewTemp(), fn.NewTemp()
	g := tbl.Symbol("g", "g")
	h := tbl.Symbol("h", "h")
	f := tbl.Symbol("f", "f")

	b.Append(ir.NewCall(callPos(), gd, ir.OrdinaryCall, g, x))
	b.Append(ir.NewCall(callPos(), hd, ir.OrdinaryCall, h, x))
	b.Append(ir.NewCall(callPos(), fd, ir.TailCall, f, gd, hd))
	b.SetTerm(ir.NewReturn(callPos(), fd))

	if len(b.Instrs()) != 3 {
		t.Fatalf("built %d instructions, want 3", len(b.Instrs()))
	}
	last, isCall := b.Instrs()[2].(*ir.Call)
	if !isCall {
		t.Fatalf("the third instruction is %T", b.Instrs()[2])
	}
	if last.Arg(0) != gd || last.Arg(1) != hd {
		t.Errorf("the outer call reads %v and %v; it must read its predecessors' "+
			"destinations %v and %v", last.Arg(0), last.Arg(1), gd, hd)
	}
	if !last.Tail() {
		t.Errorf("the outer call is in tail position and the model does not say so")
	}
	for _, in := range b.Instrs() {
		if !in.Pos().IsValid() {
			t.Errorf("%v carries no position", in)
		}
	}
}
