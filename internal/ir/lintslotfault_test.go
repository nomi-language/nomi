package ir

// Plants for RuleSlotDeclaredBeforeUse and RuleFaultEdgeResolved, plus the
// controls that make them mean anything, plus the two shapes those rules turn
// on: a slot is not a definition, and a parameter is defined at entry.
//
// Same discipline as lint_test.go: every plant is shown failing beside a
// control that passes, because a lint assertion whose instrument was never
// demonstrated failing is worth nothing.

import (
	"fmt"
	"strings"
	"testing"
)

func v3At(line int) Pos { return At("v3.nomi", line, 1) }

func v3Int() *Type {
	t := NewTable()
	ty, _ := t.Concrete("Int", "Int")
	return valued(ty, IntType)
}

// slotControl is `fn pick(flag) { r = if flag { 1 } else { 2 }; return r }`
// as this IR represents it: ONE slot declared in the entry block, written by
// BOTH arms, read by the join's Return.
//
// It is the canonical slot shape and it is the control for every plant
// below. A VM allocates one frame register for `r`; each arm writes it and
// the join returns it.
func slotControl() (*Func, Temp) {
	f := NewFunc(v3At(1), "pick")
	flag := typedParam(f, NewSymbol("flag"), BoolType)
	entry := f.NewBlock(v3At(1), "entry")
	slot := f.NewTemp()
	entry.Append(NewSlot(v3At(1), slot, v3Int()))

	then := f.NewBlock(v3At(2), "then")
	els := f.NewBlock(v3At(2), "else")
	join := f.NewBlock(v3At(2), "join")
	entry.SetTerm(NewBranch(v3At(2), flag, then.ID(), els.ID()))

	one := NewInt(v3At(2), f.NewTemp(), 1)
	then.Append(one)
	then.Append(NewCopy(v3At(2), slot, one.Dst()))
	then.SetTerm(NewJump(v3At(2), join.ID()))

	two := NewInt(v3At(2), f.NewTemp(), 2)
	els.Append(two)
	els.Append(NewCopy(v3At(2), slot, two.Dst()))
	els.SetTerm(NewJump(v3At(2), join.ID()))

	join.SetTerm(NewReturn(v3At(3), slot))
	return f, slot
}

func TestLintSlotFault_TheSlotControlIsClean(t *testing.T) {
	f, _ := slotControl()
	if err := Lint(f); err != nil {
		t.Fatalf("the two-arm slot shape is well formed and Lint rejected it:\n%v", err)
	}
}

// TestLintSlotFault_AParameterIsDefinedAtEntry is the positive for the seeding
// `Func.AddParam` required.
//
// Without it the rule would be wrong for every function a VM runs. Nothing in
// a body writes a parameter's temporary (the caller does), so without the
// seeding a read of one would report "nothing in this function defines t1".
// The control
// above reads `flag` in its Branch and passes; this asserts the mechanism
// directly rather than relying on that.
func TestLintSlotFault_AParameterIsDefinedAtEntry(t *testing.T) {
	f := NewFunc(v3At(1), "id")
	x := typedParam(f, NewSymbol("x"), IntType)
	b := f.NewBlock(v3At(1), "entry")
	b.SetTerm(NewReturn(v3At(2), x))
	if err := Lint(f); err != nil {
		t.Fatalf("a parameter read is not a violation and Lint reported one:\n%v", err)
	}
	// The plant: the same graph with the parameter NOT declared. Lint must
	// fire, or the check above would pass for a function with no parameters
	// too and would be measuring nothing.
	g := NewFunc(v3At(1), "id")
	t1 := g.NewTemp()
	gb := g.NewBlock(v3At(1), "entry")
	gb.SetTerm(NewReturn(v3At(2), t1))
	vs := violationsFor(t, g, RuleTempDefinedBeforeUse)
	if len(vs) != 1 {
		t.Fatalf("an undeclared temporary read by the Return: want 1 violation, got %d", len(vs))
	}
}

// TestLint_ASlotIsNotADefinition is the plant slot.go's central design
// decision rests on.
//
// `Slot.Dst()` is NoTemp deliberately, so a declaration does not satisfy
// RuleTempDefinedBeforeUse. Here ONE arm writes the slot and the other does
// not, and the Return reads it: the must analysis has to report it. If a Slot
// counted as a definition this would pass, and the many-writers shape
// `gen.slot`/`gen.fixSlot` produces would stop being checkable at exactly the
// point a VM starts depending on it.
func TestLint_ASlotIsNotADefinition(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprintf("insert_after_region=%t", delayed), func(t *testing.T) {
			f := NewFunc(v3At(1), "pick")
			flag := typedParam(f, NewSymbol("flag"), BoolType)
			entry := f.NewBlock(v3At(1), "entry")
			slot := f.NewTemp()
			if !delayed {
				entry.Append(NewSlot(v3At(1), slot, v3Int()))
			}
			then := f.NewBlock(v3At(2), "then")
			els := f.NewBlock(v3At(2), "else")
			join := f.NewBlock(v3At(2), "join")
			entry.SetTerm(NewBranch(v3At(2), flag, then.ID(), els.ID()))
			one := NewInt(v3At(2), f.NewTemp(), 1)
			then.Append(one)
			then.Append(NewCopy(v3At(2), slot, one.Dst()))
			then.SetTerm(NewJump(v3At(2), join.ID()))
			// The else arm writes nothing.
			els.SetTerm(NewJump(v3At(2), join.ID()))
			join.SetTerm(NewReturn(v3At(3), slot))
			if delayed {
				entry.InsertSlot(0, NewSlot(v3At(1), slot, v3Int()))
			}

			vs := violationsFor(t, f, RuleTempDefinedBeforeUse)
			if len(vs) != 1 {
				t.Fatalf("one arm writes the slot and the other does not: want 1 violation, got %d\n%v",
					len(vs), Lint(f))
			}
			if !strings.Contains(vs[0].Why, "not on every path") {
				t.Fatalf("want the every-path failure, got %q", vs[0].Why)
			}
			// And the slot rule itself must NOT fire: the storage is declared on
			// every path, it is the VALUE that is not written on one. Two rules, two
			// questions; a plant that tripped both would not distinguish them.
			if sv := violationsFor(t, f, RuleSlotDeclaredBeforeUse); len(sv) != 0 {
				t.Fatalf("the storage IS declared on every path; RuleSlotDeclaredBeforeUse fired %d times:\n%v",
					len(sv), sv)
			}
		})
	}
}

// TestLint_SlotDeclaredBeforeUseCatchesItsPlants plants
// RuleSlotDeclaredBeforeUse in both directions it has.
func TestLint_SlotDeclaredBeforeUseCatchesItsPlants(t *testing.T) {
	t.Run("one temp declared by two slots in one block", func(t *testing.T) {
		f := NewFunc(v3At(1), "twice")
		entry := f.NewBlock(v3At(1), "entry")
		slot := f.NewTemp()
		entry.Append(NewSlot(v3At(1), slot, v3Int()))
		one := NewInt(v3At(2), f.NewTemp(), 1)
		entry.Append(one)
		entry.Append(NewSlot(v3At(2), slot, v3Int()))
		entry.Append(NewCopy(v3At(2), slot, one.Dst()))
		entry.SetTerm(NewReturn(v3At(3), slot))
		vs := violationsFor(t, f, RuleSlotDeclaredBeforeUse)
		if len(vs) != 1 {
			t.Fatalf("a temporary declared by two slots: want 1 violation, got %d\n%v",
				len(vs), Lint(f))
		}
		if !strings.Contains(vs[0].Why, "already declared at") {
			t.Fatalf("want a duplicate-declaration failure, got %q", vs[0].Why)
		}
	})

	t.Run("one temp declared by two slots in two blocks", func(t *testing.T) {
		// Neither block holds both, which is what the per-block scan cannot
		// see: two arms of a branch each declaring the answer's storage.
		f := NewFunc(v3At(1), "pick")
		flag := typedParam(f, NewSymbol("flag"), BoolType)
		entry := f.NewBlock(v3At(1), "entry")
		slot := f.NewTemp()
		then := f.NewBlock(v3At(2), "then")
		els := f.NewBlock(v3At(2), "else")
		join := f.NewBlock(v3At(2), "join")
		entry.SetTerm(NewBranch(v3At(2), flag, then.ID(), els.ID()))
		one := NewInt(v3At(2), f.NewTemp(), 1)
		then.Append(NewSlot(v3At(2), slot, v3Int()))
		then.Append(one)
		then.Append(NewCopy(v3At(2), slot, one.Dst()))
		then.SetTerm(NewJump(v3At(2), join.ID()))
		two := NewInt(v3At(2), f.NewTemp(), 2)
		els.Append(NewSlot(v3At(2), slot, v3Int()))
		els.Append(two)
		els.Append(NewCopy(v3At(2), slot, two.Dst()))
		els.SetTerm(NewJump(v3At(2), join.ID()))
		join.SetTerm(NewReturn(v3At(3), slot))
		vs := violationsFor(t, f, RuleSlotDeclaredBeforeUse)
		if len(vs) != 1 {
			t.Fatalf("one storage declared on two arms: want 1 violation, got %d\n%v",
				len(vs), Lint(f))
		}
		if !strings.Contains(vs[0].Why, "already declared at") {
			t.Fatalf("want the cross-block duplicate failure, got %q", vs[0].Why)
		}
	})

	t.Run("a write on a path the declaration does not reach", func(t *testing.T) {
		// The Slot moves INTO the then arm, so the else arm writes storage
		// that has not been declared on its path. This is the producer bug a
		// frame allocator crashes on.
		f := NewFunc(v3At(1), "pick")
		flag := typedParam(f, NewSymbol("flag"), BoolType)
		entry := f.NewBlock(v3At(1), "entry")
		slot := f.NewTemp()
		then := f.NewBlock(v3At(2), "then")
		els := f.NewBlock(v3At(2), "else")
		join := f.NewBlock(v3At(2), "join")
		entry.SetTerm(NewBranch(v3At(2), flag, then.ID(), els.ID()))
		then.Append(NewSlot(v3At(2), slot, v3Int()))
		one := NewInt(v3At(2), f.NewTemp(), 1)
		then.Append(one)
		then.Append(NewCopy(v3At(2), slot, one.Dst()))
		then.SetTerm(NewJump(v3At(2), join.ID()))
		two := NewInt(v3At(2), f.NewTemp(), 2)
		els.Append(two)
		els.Append(NewCopy(v3At(2), slot, two.Dst()))
		els.SetTerm(NewJump(v3At(2), join.ID()))
		join.SetTerm(NewReturn(v3At(3), slot))

		vs := violationsFor(t, f, RuleSlotDeclaredBeforeUse)
		if len(vs) == 0 {
			t.Fatalf("the else arm writes undeclared storage and Lint accepted it")
		}
		if !strings.Contains(vs[0].Why, "not declared on every path") {
			t.Fatalf("want the every-path declaration failure, got %q", vs[0].Why)
		}
	})
}

// faultControl is a function whose entry block CAN fault — an Int `/`, which
// `Arith.Faults` reports as div0|overflow — with a handler block named by the
// block's exceptional edge.
func faultControl() *Func {
	f := NewFunc(v3At(1), "half")
	a := typedParam(f, NewSymbol("a"), IntType)
	b := typedParam(f, NewSymbol("b"), IntType)
	entry := f.NewBlock(v3At(1), "entry")
	handler := f.NewBlock(v3At(3), "handler")
	q := NewArith(v3At(2), f.NewTemp(), OpDiv, IntArith(OverflowFaults), a, b)
	entry.Append(q)
	entry.SetFault(v3At(3), handler.ID())
	entry.SetTerm(NewReturn(v3At(2), q.Dst()))
	zero := NewInt(v3At(3), f.NewTemp(), 0)
	handler.Append(zero)
	handler.SetTerm(NewReturn(v3At(3), zero.Dst()))
	return f
}

func TestLintSlotFault_TheFaultControlIsClean(t *testing.T) {
	if err := Lint(faultControl()); err != nil {
		t.Fatalf("a faulting block with a handler is well formed and Lint rejected it:\n%v", err)
	}
}

// TestLintSlotFault_NoFaultEdgeIsLegal is the other half of the default fault.go
// records: a block that CAN fault and names no handler is a function whose
// faults leave it, which is what the VM does. A rule that
// required an edge would report every arithmetic function in the corpus.
func TestLintSlotFault_NoFaultEdgeIsLegal(t *testing.T) {
	f := NewFunc(v3At(1), "half")
	a := typedParam(f, NewSymbol("a"), IntType)
	b := typedParam(f, NewSymbol("b"), IntType)
	entry := f.NewBlock(v3At(1), "entry")
	q := NewArith(v3At(2), f.NewTemp(), OpDiv, IntArith(OverflowFaults), a, b)
	entry.Append(q)
	entry.SetTerm(NewReturn(v3At(2), q.Dst()))
	if err := Lint(f); err != nil {
		t.Fatalf("an unhandled fault is legal and Lint reported it:\n%v", err)
	}
	if !entry.CanFault() {
		t.Fatalf("Int `/` faults div0|overflow and CanFault said no; the control measures nothing")
	}
}

// TestLint_FaultEdgeResolvedCatchesItsPlants plants RuleFaultEdgeResolved in
// both directions it has.
func TestLint_FaultEdgeResolvedCatchesItsPlants(t *testing.T) {
	t.Run("the edge names no block of this function", func(t *testing.T) {
		f := NewFunc(v3At(1), "half")
		a := typedParam(f, NewSymbol("a"), IntType)
		b := typedParam(f, NewSymbol("b"), IntType)
		entry := f.NewBlock(v3At(1), "entry")
		q := NewArith(v3At(2), f.NewTemp(), OpDiv, IntArith(OverflowFaults), a, b)
		entry.Append(q)
		entry.SetFault(v3At(3), BlockID(9))
		entry.SetTerm(NewReturn(v3At(2), q.Dst()))
		vs := violationsFor(t, f, RuleFaultEdgeResolved)
		if len(vs) != 1 {
			t.Fatalf("a dangling fault edge: want 1 violation, got %d", len(vs))
		}
		if !strings.Contains(vs[0].Why, "not a block of this function") {
			t.Fatalf("want the dangling-target failure, got %q", vs[0].Why)
		}
	})

	t.Run("the edge is on a block where nothing can fault", func(t *testing.T) {
		f := NewFunc(v3At(1), "one")
		entry := f.NewBlock(v3At(1), "entry")
		handler := f.NewBlock(v3At(3), "handler")
		one := NewInt(v3At(2), f.NewTemp(), 1)
		entry.Append(one)
		entry.SetFault(v3At(3), handler.ID())
		entry.SetTerm(NewReturn(v3At(2), one.Dst()))
		handler.SetTerm(NewReturn(v3At(3), one.Dst()))
		vs := violationsFor(t, f, RuleFaultEdgeResolved)
		if len(vs) != 1 {
			t.Fatalf("a handler for a block that cannot fault: want 1 violation, got %d", len(vs))
		}
		if !strings.Contains(vs[0].Why, "nothing in this block can fault") {
			t.Fatalf("want the dead-handler failure, got %q", vs[0].Why)
		}
	})
}

// TestLintSlotFault_TheFaultEdgeCarriesOnlyPreFaultDefinitions is the def-use half of
// the fault edge, and it is the one property the exceptional edge adds that a normal
// edge could not.
//
// Control leaves a block at the FIRST instruction that can fault, so a
// handler may read what the block computed before it and nothing at or after
// it. A linter that propagated the whole block's definitions along the fault
// edge would tell the handler it can read a temporary the fault prevented
// being written — and would accept the graph below, which a VM would run
// straight into an empty register.
func TestLintSlotFault_TheFaultEdgeCarriesOnlyPreFaultDefinitions(t *testing.T) {
	build := func(handlerReads func(q, before Temp) Temp) *Func {
		f := NewFunc(v3At(1), "half")
		a := typedParam(f, NewSymbol("a"), IntType)
		b := typedParam(f, NewSymbol("b"), IntType)
		entry := f.NewBlock(v3At(1), "entry")
		handler := f.NewBlock(v3At(4), "handler")
		before := NewInt(v3At(2), f.NewTemp(), 7)
		entry.Append(before)
		q := NewArith(v3At(3), f.NewTemp(), OpDiv, IntArith(OverflowFaults), a, b)
		entry.Append(q)
		entry.SetFault(v3At(4), handler.ID())
		entry.SetTerm(NewReturn(v3At(3), q.Dst()))
		handler.SetTerm(NewReturn(v3At(4), handlerReads(q.Dst(), before.Dst())))
		return f
	}
	if err := Lint(build(func(_, before Temp) Temp { return before })); err != nil {
		t.Fatalf("the handler reads a value computed BEFORE the fault and Lint reported it:\n%v", err)
	}
	after := build(func(q, _ Temp) Temp { return q })
	vs := violationsFor(t, after, RuleTempDefinedBeforeUse)
	if len(vs) != 1 {
		t.Fatalf("the handler reads the faulting operation's own result: want 1 violation, got %d\n%v",
			len(vs), Lint(after))
	}
}

// TestSlot_TheConstructorRejectsWhatOneCallCanBeWrongAbout is the division
// slot.go states: a constructor rejects what ONE CALL can be wrong about and
// Lint reports what only the graph can be.
func TestSlot_TheConstructorRejectsWhatOneCallCanBeWrongAbout(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func()
	}{
		{"no name", func() { NewSlot(v3At(1), NoTemp, v3Int()) }},
		{"no type", func() { NewSlot(v3At(1), Temp(1), nil) }},
		{"no position", func() { NewSlot(Pos{}, Temp(1), v3Int()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewSlot accepted a slot with %s", tc.name)
				}
			}()
			tc.call()
		})
	}
	// The control: a well-formed slot constructs.
	s := NewSlot(v3At(1), Temp(1), v3Int())
	if s.Dst() != NoTemp {
		t.Fatalf("a slot declares storage and defines no value; Dst() is %v", s.Dst())
	}
	if s.Slot() != Temp(1) {
		t.Fatalf("Slot() is %v, want t1", s.Slot())
	}
}

// TestConcat_TheJoinIsNAryAndNeedsTwo pins concat.go's decision: a join of one
// is that operand and a join of zero is the empty String constant, so neither
// is constructable.
func TestConcat_TheJoinIsNAryAndNeedsTwo(t *testing.T) {
	for _, n := range []int{0, 1} {
		parts := make([]Temp, n)
		for i := range parts {
			parts[i] = Temp(i + 1)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewConcat accepted a join of %d", n)
				}
			}()
			NewConcat(v3At(1), Temp(9), parts...)
		}()
	}
	c := NewConcat(v3At(1), Temp(9), Temp(1), Temp(2), Temp(3))
	if c.NumParts() != 3 {
		t.Fatalf("NumParts is %d, want 3", c.NumParts())
	}
	if got := c.String(); got != "t9 = concat t1 t2 t3" {
		t.Fatalf("String() is %q", got)
	}
}

// TestMatchLit_TheAnswerIsOptional pins the opt-in half of match.go's
// retirement: the default constructor is unchanged and the new one writes.
func TestMatchLit_TheAnswerIsOptional(t *testing.T) {
	plain := NewMatchLit(v3At(1), Temp(1), Temp(2))
	if plain.Dst() != NoTemp || plain.Answers() {
		t.Fatalf("NewMatchLit must be unchanged: Dst=%v Answers=%v", plain.Dst(), plain.Answers())
	}
	answered := NewMatchLitInto(v3At(1), Temp(3), Temp(1), Temp(2))
	if answered.Dst() != Temp(3) || !answered.Answers() {
		t.Fatalf("NewMatchLitInto must write its answer: Dst=%v Answers=%v",
			answered.Dst(), answered.Answers())
	}
}

// TestCall_TheCrossingIsMarkedAndItsBoundaryIsStated pins the marker
// `docs/roadmap.md`'s debugger entry asks to be preserved, and pins what it
// does NOT cover, so the gap is a recorded one rather than a surprise.
func TestCall_TheCrossingIsMarkedAndItsBoundaryIsStated(t *testing.T) {
	callee := NewSymbol("io.print")
	if NewCall(v3At(1), Temp(2), OrdinaryCall, callee, Temp(1)).Crosses() {
		t.Fatalf("an ordinary direct call does not cross into Go")
	}
	host := NewHostCall(v3At(1), Temp(2), OrdinaryCall, callee, Temp(1))
	if !host.Crosses() {
		t.Fatalf("NewHostCall must mark the crossing")
	}
	if got := host.String(); !strings.Contains(got, "host io.print") {
		t.Fatalf("the crossing must be visible in a diagnostic; got %q", got)
	}
	// The stated boundary: a dispatched or indirect call landing on a host
	// implementation is a real crossing and is UNMARKED, because the callee
	// is not known when the instruction is built.
	if NewDispatchCall(v3At(1), Temp(2), OrdinaryCall, callee, 0, Temp(1)).Crosses() {
		t.Fatalf("a dispatched call cannot know its callee and must answer false")
	}
	if NewIndirectCall(v3At(1), Temp(2), OrdinaryCall, Temp(1), Temp(3)).Crosses() {
		t.Fatalf("an indirect call cannot know its callee and must answer false")
	}
}
