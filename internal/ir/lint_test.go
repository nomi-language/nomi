package ir

// THE PLANTED VIOLATIONS. One per rule, each shown failing, each beside a
// control that passes — which is the whole reason this file is as long as it
// is. A lint assertion whose instrument was never demonstrated failing is
// worth nothing, and this package has already had two probes that passed
// vacuously.

import (
	"fmt"
	"strings"
	"testing"
)

func lintFile() string { return "lint.nomi" }

// wellFormed is the control: `fn total(a) { a + 1 }` as this IR represents it.
// One block, one terminator, every temporary defined by an instruction ahead
// of its use, every position naming a line and a file, and the PARAMETER `a`
// declared, so the `RefLocal` reading it resolves against something. Without
// that declaration the fixture would be an instance of the defect
// RuleLocalDeclared exists for.
func wellFormed() *Func {
	at := func(line int) Pos { return At(lintFile(), line, 1) }
	f := NewFunc(at(1), "total")
	symA := NewSymbol("a")
	typedParam(f, symA, IntType)
	b := f.NewBlock(at(1), "entry")
	a := NewRefLocal(at(2), f.NewTemp(), symA)
	b.Append(a)
	one := NewInt(at(2), f.NewTemp(), 1)
	b.Append(one)
	sum := NewArith(at(2), f.NewTemp(), OpAdd, IntArith(OverflowFaults), a.Dst(), one.Dst())
	b.Append(sum)
	b.SetTerm(NewReturn(at(2), sum.Dst()))
	return f
}

func violationsFor(t *testing.T, f *Func, rule LintRule) []Violation {
	t.Helper()
	err := Lint(f)
	if err == nil {
		return nil
	}
	le, ok := err.(*LintError)
	if !ok {
		t.Fatalf("Lint returned %T, not *LintError", err)
	}
	var out []Violation
	for _, v := range le.Violations {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

// TestLint_TheControlIsClean is what makes every plant below meaningful: if
// the well-formed function did not pass, a firing rule would say nothing about
// the plant.
func TestLint_TheControlIsClean(t *testing.T) {
	if err := Lint(wellFormed()); err != nil {
		t.Fatalf("the control function is well formed and Lint rejected it:\n%v", err)
	}
}

// TestLintModule_TheControlIsClean is the module container's control, and it
// is what makes the plant below mean anything.
func TestLintModule_TheControlIsClean(t *testing.T) {
	m := wellFormedModule()
	if err := LintModule(m); err != nil {
		t.Fatalf("the control module is well formed and LintModule rejected it:\n%v", err)
	}
	if len(m.Funcs()) != 2 || len(m.Cells()) != 2 {
		t.Fatalf("the control holds %d funcs and %d cells, so the plant below is not "+
			"checking a populated container", len(m.Funcs()), len(m.Cells()))
	}
}

// wellFormedModule is a module holding two distinct functions and two
// distinct storage cells: two same-NAMED functions among them, because
// RuleModuleDeclaredOnce is about identity and a rule that reported those
// would be the printed-name comparison `Symbol`'s header forbids.
func wellFormedModule() *Module {
	tbl := NewTable()
	clock, _ := tbl.Concrete("clockDecl", "Clock")
	logger, _ := tbl.Concrete("loggerDecl", "Logger")
	valued(clock, NewHandleType(NewSymbol("Clock")))
	valued(logger, NewHandleType(NewSymbol("Logger")))
	m := NewModule(lintFile())
	m.AddFunc(wellFormed())
	m.AddFunc(wellFormed())
	m.DeclareCell(tbl.Symbol("clockDecl", "appCell_Clock"), clock)
	m.DeclareCell(tbl.Symbol("loggerDecl", "appCell_Logger"), logger)
	return m
}

// TestLintModule_DeclaredOnceCatchesItsPlants plants RuleModuleDeclaredOnce in
// both directions the container has: a function recorded twice, and storage
// declared twice for one declaration identity.
//
// BOTH ARE REACHABLE FROM OUTSIDE THIS PACKAGE, unlike RulePositionValid's
// two, and that is `Module.AddFunc`'s argued laxness doing its job: a
// duplicate is a fact about the SET, a producer may legitimately re-ask for a
// declaration it already made, and a container that panicked would push that
// bookkeeping back onto every producer.
func TestLintModule_DeclaredOnceCatchesItsPlants(t *testing.T) {
	t.Run("two bodies for one function identity", func(t *testing.T) {
		for _, duplicate := range []bool{false, true} {
			m := NewModule(lintFile())
			shared := NewSymbol("same_name")
			for i := 0; i < 2; i++ {
				sym := shared
				if i == 1 && !duplicate {
					sym = NewSymbol("same_name")
				}
				at := At(lintFile(), i+1, 1)
				f := NewFuncFor(at, sym)
				b := f.NewBlock(at, "entry")
				n := NewInt(at, f.NewTemp(), int64(i))
				b.Append(n)
				b.SetTerm(NewReturn(at, n.Dst()))
				m.AddFunc(f)
			}
			got := moduleViolationsFor(t, m, RuleModuleDeclaredOnce)
			want := 0
			if duplicate {
				want = 1
			}
			if len(got) != want {
				t.Fatalf("duplicate=%v: want %d violations, got %v", duplicate, want, got)
			}
		}
	})
	t.Run("one function recorded twice", func(t *testing.T) {
		m := wellFormedModule()
		m.AddFunc(m.Funcs()[0])

		got := moduleViolationsFor(t, m, RuleModuleDeclaredOnce)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if got[0].What != "func total" || !strings.Contains(got[0].Why, "twice") {
			t.Errorf("wrong diagnosis: %v", got[0])
		}
	})

	t.Run("one declaration's storage declared twice", func(t *testing.T) {
		// THE TABLE IS WHAT MAKES THIS A DUPLICATE. Two `DeclareCell` calls
		// with two freshly MINTED symbols would be two declarations, which is
		// correct and not a violation; interning both on one producer token
		// gives one Symbol, and one declaration with two homes is the
		// producer bug.
		tbl := NewTable()
		ty, _ := tbl.Concrete("clockDecl", "Clock")
		valued(ty, NewHandleType(NewSymbol("Clock")))
		m := NewModule(lintFile())
		m.DeclareCell(tbl.Symbol("clockDecl", "appCell_Clock"), ty)
		m.DeclareCell(tbl.Symbol("clockDecl", "appCell_Clock"), ty)

		got := moduleViolationsFor(t, m, RuleModuleDeclaredOnce)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if got[0].What != "cell appCell_Clock" {
			t.Errorf("the violation blames %q", got[0].What)
		}
	})

	t.Run("two same-named declarations are not a duplicate", func(t *testing.T) {
		// THE CONTROL WITH THE MOST POWER HERE. Without it, a rule that
		// compared printed names would read identically on the two plants
		// above.
		tbl := NewTable()
		a, _ := tbl.Concrete("oneDecl", "Clock")
		b, _ := tbl.Concrete("twoDecl", "Clock")
		valued(a, NewHandleType(NewSymbol("Clock")))
		valued(b, NewHandleType(NewSymbol("Clock")))
		m := NewModule(lintFile())
		m.DeclareCell(tbl.Symbol("oneDecl", "appCell_Clock"), a)
		m.DeclareCell(tbl.Symbol("twoDecl", "appCell_Clock"), b)
		if err := LintModule(m); err != nil {
			t.Fatalf("two declarations sharing a printed name were reported as one "+
				"declared twice, so the rule compares names rather than identities:\n%v", err)
		}
	})

	t.Run("a function's own violations surface through the module", func(t *testing.T) {
		// LintModule is the entry a producer with a container calls, so a
		// malformed FUNCTION inside a well-formed module must still fail.
		m := wellFormedModule()
		m.Funcs()[0].Blocks()[0].term = nil
		got := moduleViolationsFor(t, m, RuleBlockTerminated)
		if len(got) != 1 {
			t.Fatalf("expected the function's own violation, got %d: %v", len(got), got)
		}
	})
}

func moduleViolationsFor(t *testing.T, m *Module, rule LintRule) []Violation {
	t.Helper()
	err := LintModule(m)
	if err == nil {
		return nil
	}
	le, ok := err.(*LintError)
	if !ok {
		t.Fatalf("LintModule returned %T, not *LintError", err)
	}
	if !strings.Contains(le.Subject, lintFile()) {
		t.Errorf("the error names %q rather than the module", le.Subject)
	}
	var out []Violation
	for _, v := range le.Violations {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

// TestLint_BlockTerminatedCatchesItsPlant plants RuleBlockTerminated: a block
// with instructions and no terminator, which is exactly what a producer that
// forgot a `SetTerm` leaves behind. `Block.SetTerm` rejects a SECOND
// terminator and nothing in this package requires a first, which is why the
// rule has power.
func TestLint_BlockTerminatedCatchesItsPlant(t *testing.T) {
	f := wellFormed()
	// PLANT: strip the terminator the control set.
	f.Blocks()[0].term = nil

	got := violationsFor(t, f, RuleBlockTerminated)
	if len(got) != 1 {
		t.Fatalf("expected exactly one %q violation, got %d: %v", RuleBlockTerminated, len(got), got)
	}
	if !strings.Contains(got[0].Why, "no terminator") {
		t.Errorf("the violation does not say what is wrong: %v", got[0])
	}
	if got[0].What != "b0" {
		t.Errorf("the violation blames %q, not the block: %v", got[0].What, got[0])
	}

	// AND A SECOND PLANT, because one unterminated block could be caught by a
	// rule that only ever looks at the entry. A second block, added and left
	// open, has to be reported too.
	f2 := wellFormed()
	f2.NewBlock(At(lintFile(), 3, 1), "orphan")
	if got := violationsFor(t, f2, RuleBlockTerminated); len(got) != 1 || got[0].What != "b1" {
		t.Fatalf("a non-entry unterminated block was not reported: %v", got)
	}
}

// TestLint_TempDefinedBeforeUseCatchesItsPlants plants
// RuleTempDefinedBeforeUse three ways, because the rule has three distinct
// failures and catching one says nothing about the other two.
func TestLint_TempDefinedBeforeUseCatchesItsPlants(t *testing.T) {
	at := func(line int) Pos { return At(lintFile(), line, 1) }

	t.Run("no definition anywhere", func(t *testing.T) {
		f := NewFunc(at(1), "total")
		b := f.NewBlock(at(1), "entry")
		one := NewInt(at(2), f.NewTemp(), 1)
		b.Append(one)
		// PLANT: a temporary allocated and never written by an instruction.
		// This is the shape `irVals` made invisible — a Temp whose only
		// referent was a Go source string outside the representation.
		ghost := f.NewTemp()
		sum := NewArith(at(2), f.NewTemp(), OpAdd, IntArith(OverflowFaults), ghost, one.Dst())
		b.Append(sum)
		b.SetTerm(NewReturn(at(2), sum.Dst()))

		got := violationsFor(t, f, RuleTempDefinedBeforeUse)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0].Why, "nothing in this function defines") {
			t.Errorf("wrong diagnosis: %v", got[0])
		}
		if f.Def(ghost) != nil {
			t.Error("Func.Def answered for a temporary no instruction wrote")
		}
	})

	t.Run("used before its definition in the same block", func(t *testing.T) {
		f := NewFunc(at(1), "total")
		b := f.NewBlock(at(1), "entry")
		one := NewInt(at(2), f.NewTemp(), 1)
		later := NewInt(at(2), f.NewTemp(), 2)
		// PLANT: the arith reads `later` and `later` is appended AFTER it. The
		// definition exists, so the "no definition anywhere" arm above cannot
		// catch this one.
		sum := NewArith(at(2), f.NewTemp(), OpAdd, IntArith(OverflowFaults), later.Dst(), one.Dst())
		b.Append(one)
		b.Append(sum)
		b.Append(later)
		b.SetTerm(NewReturn(at(2), sum.Dst()))

		got := violationsFor(t, f, RuleTempDefinedBeforeUse)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0].Why, "not on every path") {
			t.Errorf("wrong diagnosis: %v", got[0])
		}
		if f.Def(later.Dst()) != Instr(later) {
			t.Error("Func.Def did not answer the instruction that wrote the temporary")
		}
	})

	t.Run("defined on one path of two", func(t *testing.T) {
		// b0 branches to b1 and b2; only b1 writes `only`. The join reads it.
		// THIS IS WHERE "EVERY PATH" AND "SOME DEFINITION DOMINATES" COME
		// APART in the other direction, and it is why the analysis is a
		// must-reach intersection rather than a dominator query.
		f := NewFunc(at(1), "total")
		entry := f.NewBlock(at(1), "entry")
		cond := NewBool(at(2), f.NewTemp(), true)
		entry.Append(cond)
		yes := f.NewBlock(at(3), "yes")
		no := f.NewBlock(at(4), "no")
		join := f.NewBlock(at(5), "join")
		entry.SetTerm(NewBranch(at(2), cond.Dst(), yes.ID(), no.ID()))
		only := NewInt(at(3), f.NewTemp(), 7)
		yes.Append(only)
		yes.SetTerm(NewJump(at(3), join.ID()))
		no.SetTerm(NewJump(at(4), join.ID()))
		join.SetTerm(NewReturn(at(5), only.Dst()))

		got := violationsFor(t, f, RuleTempDefinedBeforeUse)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0].Why, "not on every path") {
			t.Errorf("wrong diagnosis: %v", got[0])
		}

		// AND THE CONTROL FOR THIS ARM, which is the half that would be
		// missing if the rule were dominance: the SAME graph with BOTH arms
		// writing one destination passes. That is `gen.slot` / `gen.fixSlot`'s
		// own shape and this IR is deliberately not SSA, so a rule that
		// rejected it would reject the builder's commonest branch lowering.
		f2 := NewFunc(at(1), "total")
		e2 := f2.NewBlock(at(1), "entry")
		c2 := NewBool(at(2), f2.NewTemp(), true)
		e2.Append(c2)
		slot := f2.NewTemp()
		y2 := f2.NewBlock(at(3), "yes")
		n2 := f2.NewBlock(at(4), "no")
		j2 := f2.NewBlock(at(5), "join")
		e2.SetTerm(NewBranch(at(2), c2.Dst(), y2.ID(), n2.ID()))
		sevenA := NewInt(at(3), f2.NewTemp(), 7)
		y2.Append(sevenA)
		y2.Append(NewCopy(at(3), slot, sevenA.Dst()))
		y2.SetTerm(NewJump(at(3), j2.ID()))
		sevenB := NewInt(at(4), f2.NewTemp(), 8)
		n2.Append(sevenB)
		n2.Append(NewCopy(at(4), slot, sevenB.Dst()))
		n2.SetTerm(NewJump(at(4), j2.ID()))
		j2.SetTerm(NewReturn(at(5), slot))
		if err := Lint(f2); err != nil {
			t.Fatalf("two arms writing one destination is this IR's own shape and Lint "+
				"rejected it, so the rule is dominance rather than reachability:\n%v", err)
		}
	})

	t.Run("used in a block nothing reaches", func(t *testing.T) {
		// An unreachable block gets an EMPTY in-set, which is the conservative
		// choice lint.go states. The alternative — an intersection over no
		// predecessors, which is "everything" — makes this pass vacuously,
		// and this is the plant that distinguishes the two.
		f := NewFunc(at(1), "total")
		entry := f.NewBlock(at(1), "entry")
		one := NewInt(at(2), f.NewTemp(), 1)
		entry.Append(one)
		entry.SetTerm(NewReturn(at(2), one.Dst()))
		orphan := f.NewBlock(at(3), "orphan")
		orphan.SetTerm(NewReturn(at(3), one.Dst()))

		got := violationsFor(t, f, RuleTempDefinedBeforeUse)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if got[0].What != "b1 term (return t1)" {
			t.Errorf("the violation blames %q rather than the orphan's terminator", got[0].What)
		}
	})
}

// TestLint_PositionValidCatchesItsPlants plants RulePositionValid twice, and
// both plants are now composite literals inside this package.
//
// `At` rejects an empty file, so a position with no file cannot be built
// through a constructor; see TestPos_TheConstructorRejectsAPositionWithNoFile,
// which is the positive for that gate. What remains reachable, for the file
// and the line alike, is a Pos written by a composite literal or a field write
// inside this package, which is what any pass here that rebuilt a node would
// do.
func TestLint_PositionValidCatchesItsPlants(t *testing.T) {
	t.Run("a position with no file", func(t *testing.T) {
		f := NewFunc(At(lintFile(), 1, 1), "total")
		b := f.NewBlock(At(lintFile(), 1, 1), "entry")
		one := NewInt(At(lintFile(), 2, 1), f.NewTemp(), 1)
		b.Append(one)
		b.SetTerm(NewReturn(At(lintFile(), 2, 1), one.Dst()))
		// PLANT: the file cleared after construction, on the function and on
		// the instruction.
		f.Region.pos = Pos{line: 1, col: 1}
		one.pos = Pos{line: 2, col: 1}

		got := violationsFor(t, f, RulePositionValid)
		if len(got) != 2 {
			t.Fatalf("expected two violations — the func and the instruction — got %d: %v",
				len(got), got)
		}
		for _, v := range got {
			if !strings.Contains(v.Why, "no file") {
				t.Errorf("wrong diagnosis: %v", v)
			}
		}
		if got[0].What != "func total" || got[1].What != "b0 instr 0 (t1 = const int 1)" {
			t.Errorf("the violations blame %q and %q", got[0].What, got[1].What)
		}
	})

	t.Run("a position with no line", func(t *testing.T) {
		// PLANT: the zero Pos, written onto a block after construction. From
		// outside this package `Block.Pos` is read-only and `NewBlock` panics
		// on it; inside, a composite literal or a field write reaches it, and
		// so would any future pass in this package that rebuilt a node.
		f := wellFormed()
		f.Blocks()[0].pos = Pos{}

		got := violationsFor(t, f, RulePositionValid)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0].Why, "no line") {
			t.Errorf("wrong diagnosis: %v", got[0])
		}
	})
}

// TestLint_ReportsEveryViolationNotTheFirst is the property the file header
// claims: a producer that got one node wrong usually got a family wrong.
func TestLint_ReportsEveryViolationNotTheFirst(t *testing.T) {
	f := wellFormed()
	f.Blocks()[0].term = nil
	f.Blocks()[0].pos = Pos{}
	err := Lint(f)
	if err == nil {
		t.Fatal("two plants and Lint passed")
	}
	le := err.(*LintError)
	if len(le.Violations) != 2 {
		t.Fatalf("expected two violations, got %d:\n%v", len(le.Violations), err)
	}
	rules := map[LintRule]bool{}
	for _, v := range le.Violations {
		rules[v.Rule] = true
	}
	if !rules[RuleBlockTerminated] || !rules[RulePositionValid] {
		t.Errorf("both rules should have fired, got %v", rules)
	}
	if !strings.Contains(err.Error(), "2 violations in func total") {
		t.Errorf("the error does not summarize: %v", err)
	}
}

// TestPos_TheConstructorRejectsAPositionWithNoFile is the positive for `At`'s
// empty-file gate, and it is what makes the narrowing of RulePositionValid's
// population a closure rather than a loss.
//
// The CONTROL is the second half: a position with a file is accepted, so the
// panic is about the empty file and not about positions in general.
func TestPos_TheConstructorRejectsAPositionWithNoFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() Pos
	}{
		{"At", func() Pos { return At("", 7, 1) }},
		{"AtSynthesized", func() Pos { return AtSynthesized("", 7, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("a position with no file was accepted")
				}
				if !strings.Contains(fmt.Sprint(r), "needs the file it names a line in") {
					t.Errorf("the panic is about something else: %v", r)
				}
			}()
			_ = tc.build()
		})
	}

	if p := At(lintFile(), 7, 1); p.File() != lintFile() || !p.IsValid() {
		t.Errorf("the control did not survive the gate: %v", p)
	}
}

// TestFunc_DefIsWhatMakesATempAValue checks the def table directly. It is
// checked separately from Lint because Lint could pass on a function whose
// def table was never filled if the rule read the blocks instead of the table.
func TestFunc_DefIsWhatMakesATempAValue(t *testing.T) {
	f := wellFormed()
	entry := f.Blocks()[0]
	// Four: the parameter's temporary plus the three the body's
	// instructions write. A parameter's temporary is allocated by `AddParam`
	// and written by the CALLER, which is the seeding `lintTempDefs` is told
	// about rather than left to infer.
	if f.NumTemps() != 4 {
		t.Fatalf("the control allocates one parameter and three instruction "+
			"temporaries, NumTemps says %d", f.NumTemps())
	}
	for i, in := range entry.Instrs() {
		if got := f.Def(in.Dst()); got != in {
			t.Errorf("instr %d writes %s and Def answers %v, not %v", i, in.Dst(), got, in)
		}
	}
	if f.Def(NoTemp) != nil {
		t.Error("Def answered for NoTemp")
	}
	if f.Def(Temp(f.NumTemps()+1)) != nil {
		t.Error("Def answered for a temporary outside the namespace")
	}

	// A bare region records nothing: a region is a fragment with no temporary
	// namespace of its own (see Region's header).
	r := NewRegion(At(lintFile(), 1, 1), "shortcircuit")
	rb := r.NewBlock(At(lintFile(), 1, 1), "rhs")
	if rb.owner != nil {
		t.Error("a region's block claims an owning function")
	}
	rb.Append(NewInt(At(lintFile(), 1, 1), Temp(1), 1))
	// Nothing to assert about a def table there is no function to hold; the
	// check is that Append did not panic reaching through a nil owner.
}
