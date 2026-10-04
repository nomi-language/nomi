package ir_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// TestLogic_ShortCircuitIsAControlFlowShape asserts the canonical shape of
// `and` and `or`, which is the class that makes basic blocks necessary.
//
// It asserts the shape rather than the emitted text because there is no
// consumer yet. What it can and does pin is the part a consumer cannot get
// wrong without diverging: WHICH BLOCK RUNS THE RIGHT OPERAND. For `and` the
// right operand runs when the left is true; for `or` when the left is false.
// Getting that backwards evaluates something Nomi says must not be evaluated.
func TestLogic_ShortCircuitIsAControlFlowShape(t *testing.T) {
	for _, tc := range []struct {
		op LogicCase
	}{
		{LogicCase{op: ir.LogicAnd, rhsOnTrue: true}},
		{LogicCase{op: ir.LogicOr, rhsOnTrue: false}},
	} {
		f := ir.NewFunc(ir.At("l.nomi", 1, 1), "f")
		entry := f.NewBlock(ir.At("l.nomi", 1, 1), "entry")
		lhs := f.NewTemp()
		entry.Append(ir.NewRefLocal(ir.At("l.nomi", 2, 3), lhs, ir.NewSymbol("a")))

		answer := f.NewTemp()
		opPos := ir.At("l.nomi", 2, 5)
		sc := ir.BeginShortCircuit(f.Region, entry, tc.op.op, opPos,
			ir.At("l.nomi", 2, 3), ir.At("l.nomi", 2, 9), answer, lhs)
		if sc.Op() != tc.op.op {
			t.Fatalf("%s: builder reports operator %s", tc.op.op, sc.Op())
		}

		rhsVal := f.NewTemp()
		sc.Rhs().Append(ir.NewRefLocal(ir.At("l.nomi", 2, 9), rhsVal, ir.NewSymbol("b")))
		join := sc.Finish(sc.Rhs(), ir.At("l.nomi", 2, 9), rhsVal)

		br, ok := entry.Term().(*ir.Branch)
		if !ok {
			t.Fatalf("%s: entry is terminated by %T, not a Branch. Short-circuit needs a real "+
				"branch", tc.op.op, entry.Term())
		}
		if br.Cond() != lhs {
			t.Errorf("%s: the branch tests %s, not the left operand %s", tc.op.op, br.Cond(), lhs)
		}
		if br.Pos() != opPos {
			t.Errorf("%s: the branch test is at %s, want the OPERATOR's position %s: a "+
				"branch test carries the operator's line", tc.op.op, br.Pos(), opPos)
		}

		gotRhsOnTrue := br.IfTrue() == sc.Rhs().ID()
		if gotRhsOnTrue != tc.op.rhsOnTrue {
			t.Errorf("%s: the right operand runs when the left is %v; want %v. This is the one "+
				"thing a consumer cannot get wrong without evaluating something Nomi says must "+
				"not be evaluated", tc.op.op, gotRhsOnTrue, tc.op.rhsOnTrue)
		}
		short := br.IfTrue()
		if gotRhsOnTrue {
			short = br.IfFalse()
		}
		if short != join.ID() {
			t.Errorf("%s: the short-circuit arm goes to %s, not the join %s",
				tc.op.op, short, join.ID())
		}

		// The left value is the answer when the left decides: `a and b`
		// evaluates to `b` itself rather than to a normalized sentinel, so the
		// destination must already hold the left value before the branch.
		var leftCopy *ir.Copy
		for _, in := range entry.Instrs() {
			if c, ok := in.(*ir.Copy); ok && c.Dst() == answer {
				leftCopy = c
			}
		}
		if leftCopy == nil {
			t.Errorf("%s: nothing copies the left value into the answer before the branch, so "+
				"the short-circuit arm leaves the answer undefined", tc.op.op)
		} else if leftCopy.Src() != lhs {
			t.Errorf("%s: the pre-branch copy reads %s, not the left operand %s",
				tc.op.op, leftCopy.Src(), lhs)
		}

		// And the right block ends by writing the answer and jumping to the
		// join. Finish does both so a consumer cannot forget either.
		instrs := sc.Rhs().Instrs()
		last, ok := instrs[len(instrs)-1].(*ir.Copy)
		if !ok || last.Dst() != answer || last.Src() != rhsVal {
			t.Errorf("%s: the right block does not end by copying its value into the answer; "+
				"last instruction is %s", tc.op.op, instrs[len(instrs)-1])
		}
		jump, ok := sc.Rhs().Term().(*ir.Jump)
		if !ok || jump.Target() != join.ID() {
			t.Errorf("%s: the right block is terminated by %s, not a jump to the join",
				tc.op.op, sc.Rhs().Term())
		}
	}
}

// LogicCase names a case of the table above so the fields read as claims.
type LogicCase struct {
	op        ir.LogicOp
	rhsOnTrue bool
}

// TestLogic_FinishIsSingleUse guards the builder against the one misuse that
// would silently produce two writes to the destination.
func TestLogic_FinishIsSingleUse(t *testing.T) {
	f := ir.NewFunc(ir.At("l.nomi", 1, 1), "f")
	entry := f.NewBlock(ir.At("l.nomi", 1, 1), "entry")
	lhs := f.NewTemp()
	entry.Append(ir.NewRefLocal(ir.At("l.nomi", 2, 3), lhs, ir.NewSymbol("a")))
	sc := ir.BeginShortCircuit(f.Region, entry, ir.LogicOr, ir.At("l.nomi", 2, 5),
		ir.At("l.nomi", 2, 3), ir.At("l.nomi", 2, 9), f.NewTemp(), lhs)
	sc.Finish(sc.Rhs(), ir.At("l.nomi", 2, 9), lhs)
	if got := recovered(func() { sc.Finish(sc.Rhs(), ir.At("l.nomi", 2, 9), lhs) }); got == "" {
		t.Error("Finish can be called twice, which appends a second copy and a second " +
			"terminator to a block that is already terminated")
	}
}

// TestLogic_AShortCircuitNeedsNoFunction checks that a short-circuit is
// buildable without a function.
//
// `logic` has a control-flow graph and no declaration. The builder reaches
// `and` several frames below any declaration, and the enclosing construct may
// be a lambda, a test body or a synthesized impl.
//
// So the block arena is `ir.Region` and a Func is a Region plus a name and a
// temporary namespace. If BeginShortCircuit took a Func, the only position a
// caller without one could pass would be a fabricated one, which the per-node
// position rule forbids.
func TestLogic_AShortCircuitNeedsNoFunction(t *testing.T) {
	opPos := ir.At("l.nomi", 7, 12)
	region := ir.NewRegion(opPos, "and")
	if region.Pos() != opPos {
		t.Errorf("the region reports %s, want the operator's position %s", region.Pos(), opPos)
	}
	entry := region.NewBlock(opPos, "entry")

	// The temporaries come from the CONSUMER's namespace, not from a Func:
	// that is the other half of the split. A Region hands out block ids and
	// nothing else.
	const lhs, dst, rhsVal ir.Temp = 1, 2, 3
	sc := ir.BeginShortCircuit(region, entry, ir.LogicAnd, opPos,
		ir.At("l.nomi", 7, 5), ir.At("l.nomi", 7, 18), dst, lhs)
	join := sc.Finish(sc.Rhs(), ir.At("l.nomi", 7, 18), rhsVal)

	if len(region.Blocks()) != 3 {
		t.Fatalf("the short-circuit shape has %d blocks in the region, want 3",
			len(region.Blocks()))
	}
	for i, b := range region.Blocks() {
		if b.ID() != ir.BlockID(i) {
			t.Errorf("block at index %d reports id %s; ids are unique within the REGION",
				i, b.ID())
		}
		if region.Block(b.ID()) != b {
			t.Errorf("Region.Block(%s) did not return the block at that id", b.ID())
		}
	}

	// THE JOIN IS UNTERMINATED, and a partial retarget cannot terminate it:
	// its successor is whatever the enclosing function does next, which for a
	// consumer that has retargeted one class is text it emits from the AST.
	// A Region that demanded every block be terminated would make a fragment
	// unrepresentable.
	if join.Term() != nil {
		t.Errorf("the join is terminated by %s; Finish must leave it open for the caller",
			join.Term())
	}

	// PLANT A POSITIVE: a Func is still a Region, so a whole-function consumer
	// builds the same shape through the same call.
	f := ir.NewFunc(ir.At("l.nomi", 1, 1), "f")
	fEntry := f.NewBlock(ir.At("l.nomi", 1, 1), "entry")
	ir.BeginShortCircuit(f.Region, fEntry, ir.LogicOr, opPos,
		ir.At("l.nomi", 7, 5), ir.At("l.nomi", 7, 18), f.NewTemp(), f.NewTemp())
	if len(f.Blocks()) != 3 {
		t.Errorf("a Func got %d blocks from the same call, want 3", len(f.Blocks()))
	}
	if f.Pos() != ir.At("l.nomi", 1, 1) {
		t.Errorf("Func.Pos is %s after the split; it must still be the DECLARATION's "+
			"position and not the region's label", f.Pos())
	}
}

// TestLogic_ARegionDemandsAPosition keeps the per-node position gate on
// NewRegion. Region exists because the position a Func would demand is not
// available; it would be a poor trade if the replacement demanded none at
// all.
func TestLogic_ARegionDemandsAPosition(t *testing.T) {
	if got := recovered(func() { ir.NewRegion(ir.Pos{}, "and") }); got == "" {
		t.Error("NewRegion accepted the zero Pos without panicking")
	}
	if got := recovered(func() {
		ir.NewRegion(ir.At("l.nomi", 1, 1), "and").NewBlock(ir.Pos{}, "b")
	}); got == "" {
		t.Error("Region.NewBlock accepted the zero Pos without panicking")
	}
}

// TestBlocks_AreWriteOnceAndOrdered pins the block invariants the four classes
// rely on. They are small and they are the whole of the CFG model: this
// package deliberately does not have a CFG library.
func TestBlocks_AreWriteOnceAndOrdered(t *testing.T) {
	f := buildShortCircuitFunc(t)

	if len(f.Blocks()) != 3 {
		t.Fatalf("the short-circuit shape has %d blocks, want 3 (entry, rhs, join)",
			len(f.Blocks()))
	}
	for i, b := range f.Blocks() {
		if b.ID() != ir.BlockID(i) {
			t.Errorf("block at index %d reports id %s", i, b.ID())
		}
		if got := f.Block(b.ID()); got != b {
			t.Errorf("Func.Block(%s) did not return the block at that index", b.ID())
		}
	}
	if f.Block(ir.BlockID(len(f.Blocks()))) != nil {
		t.Error("Func.Block returned a block for an out-of-range id")
	}

	entry := f.Blocks()[0]
	if got := recovered(func() {
		entry.Append(ir.NewUnit(ir.At(helperFile, 9, 9), f.NewTemp()))
	}); !strings.Contains(got, "after") {
		t.Errorf("Append after the terminator was accepted; panic was %q. An instruction "+
			"after a terminator is unreachable and its position would be a lie", got)
	}
	if got := recovered(func() {
		entry.SetTerm(ir.NewReturnUnit(ir.At(helperFile, 9, 9)))
	}); !strings.Contains(got, "already terminated") {
		t.Errorf("SetTerm on a terminated block was accepted; panic was %q", got)
	}

	// Successors and uses, appended into a caller's buffer rather than
	// allocated per call.
	var succ []ir.BlockID
	var uses []ir.Temp
	for _, b := range f.Blocks() {
		succ = b.Term().AppendSuccessors(succ)
		uses = b.Term().AppendUses(uses)
		for _, in := range b.Instrs() {
			uses = in.AppendUses(uses)
		}
	}
	// entry: branch -> 2 successors; rhs: jump -> 1; join: return -> 0.
	if len(succ) != 3 {
		t.Errorf("the shape has %d successor edges, want 3 (a branch's two and a jump's one)",
			len(succ))
	}
	if len(uses) == 0 {
		t.Error("nothing in the function reads a temporary, so the use walk proves nothing")
	}

	// Unterminated blocks are visible rather than silently empty.
	g := ir.NewFunc(ir.At(helperFile, 1, 1), "g")
	if b := g.NewBlock(ir.At(helperFile, 1, 1), "entry"); b.Term() != nil {
		t.Error("a fresh block reports a terminator")
	}
}

// TestReturn_UnitAndValueAreDistinct covers the one place NoTemp is a legal
// answer and the one place it is not.
func TestReturn_UnitAndValueAreDistinct(t *testing.T) {
	pos := ir.At(helperFile, 5, 1)
	unit := ir.NewReturnUnit(pos)
	if unit.HasVal() || unit.Val() != ir.NoTemp {
		t.Errorf("NewReturnUnit carries a value: %s", unit)
	}
	if got := unit.String(); got != "return" {
		t.Errorf("NewReturnUnit renders as %q", got)
	}
	val := ir.NewReturn(pos, 7)
	if !val.HasVal() || val.Val() != 7 {
		t.Errorf("NewReturn lost its value: %s", val)
	}
	if got := recovered(func() { ir.NewReturn(pos, ir.NoTemp) }); got == "" {
		t.Error("NewReturn accepted NoTemp; a value-returning return with no value is a hole " +
			"a consumer would have to guess about")
	}
	if got := recovered(func() { ir.NewBranch(pos, ir.NoTemp, 0, 1) }); got == "" {
		t.Error("NewBranch accepted NoTemp as a condition")
	}
}

func TestLogic_NestedOperandKeepsItsBranch(t *testing.T) {
	at := ir.At("nested.nomi", 2, 1)
	f := ir.NewFunc(at, "nested")
	entry := f.NewBlock(at, "entry")
	left := f.NewTemp()
	entry.Append(ir.NewBool(at, left, true))
	answer := f.NewTemp()
	outer := ir.BeginShortCircuit(f.Region, entry, ir.LogicAnd, at, at, at, answer, left)
	right := f.NewTemp()
	outer.Rhs().Append(ir.NewBool(at, right, false))
	innerAnswer := f.NewTemp()
	inner := ir.BeginShortCircuit(f.Region, outer.Rhs(), ir.LogicOr, at, at, at, innerAnswer, right)
	last := f.NewTemp()
	inner.Rhs().Append(ir.NewBool(at, last, true))
	innerJoin := inner.Finish(inner.Rhs(), at, last)
	join := outer.Finish(innerJoin, at, innerAnswer)
	join.SetTerm(ir.NewReturn(at, answer))
	if _, ok := outer.Rhs().Term().(*ir.Branch); !ok {
		t.Fatal("outer finish overwrote the nested branch")
	}
	if err := ir.Lint(f); err != nil {
		t.Fatal(err)
	}
}
