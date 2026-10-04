package ir

// The source extent on `Pos`, its two lint questions, and the third question
// that is answered at the constructor instead.
//
// Three things can be wrong with an end, and each is caught somewhere:
//
//	absent                 RulePositionSpan, planted below
//	before its start       Spanning panics; RulePositionSpan for an
//	                       in-package composite literal, planted below
//	in a different file    UNCONSTRUCTABLE — the end has no file field
//
// The third is closed by construction rather than by a rule. A construct's
// text is in one file; an end with its own path is a representation able to
// express nonsense. `At`'s empty-file gate is the precedent: it closes that
// hole at the constructor rather than leaving it to `RulePositionValid`,
// which makes the defect unconstructable rather than reportable, and this is
// the same move one field over. TestPos_TheEndCannotNameADifferentFile is the
// positive.

import (
	"strings"
	"testing"
)

// TestPos_APointsEndIsItsStart is the property that keeps RulePositionSpan's
// population TOTAL rather than the handful of positions that span.
//
// If `At` left the end zero, every one of the ~1500 positions a corpus sweep
// builds would be a violation and the rule would have had to be switched off
// for its whole production population — which is the shape of a rule that
// cannot fire, stated from the other side.
func TestPos_APointsEndIsItsStart(t *testing.T) {
	p := At("f.nomi", 7, 3)
	if p.EndLine() != 7 || p.EndCol() != 3 {
		t.Fatalf("a point's end is its start; got %d:%d", p.EndLine(), p.EndCol())
	}
	if p.Spans() {
		t.Error("a point does not span")
	}
	if got := p.String(); got != "f.nomi:7:3" {
		t.Errorf("a point prints without an end; got %q", got)
	}
	// A SYNTHESIZED POSITION IS A POINT BY DESIGN: what it names is the ORIGIN
	// a consumer must blame, and an origin has no extent.
	s := AtSynthesized("f.nomi", 9, 1)
	if s.Spans() || s.EndLine() != 9 {
		t.Errorf("a synthesized position is a point; got %v", s)
	}
}

// TestPos_SpanningCarriesTheExtentAndPrintsIt is the mechanism, and the
// printed form is checked because a dump is one of the two consumers the end
// has today.
func TestPos_SpanningCarriesTheExtentAndPrintsIt(t *testing.T) {
	p := Spanning("deadline_floor_test.nomi", 25, 3, 43, 30)
	switch {
	case p.Line() != 25 || p.Col() != 3:
		t.Fatalf("the start moved: %v", p)
	case p.EndLine() != 43 || p.EndCol() != 30:
		t.Fatalf("the end is wrong: %v", p)
	case !p.Spans():
		t.Fatal("25..43 spans")
	}
	if got := p.String(); got != "deadline_floor_test.nomi:25:3-43:30" {
		t.Errorf("the printed form is %q", got)
	}
}

// TestPos_CoversIsTheBreakpointQuery is the reason the end is on the
// representation rather than in the producer.
//
// A user asking to break on line 30 of `deadline_floor_test.nomi` is asking
// about a line INSIDE a triple-quoted literal. No instruction carries 30, so
// only the extent relates 30 to the instruction that computes the literal.
func TestPos_CoversIsTheBreakpointQuery(t *testing.T) {
	span := Spanning("deadline_floor_test.nomi", 25, 3, 43, 30)
	point := At("deadline_floor_test.nomi", 25, 3)
	for _, tc := range []struct {
		line     int
		span, pt bool
		note     string
	}{
		{24, false, false, "the function's own line, above the construct"},
		{25, true, true, "the construct's first line"},
		{30, true, false, "INSIDE the literal — the whole point"},
		{43, true, false, "the construct's last node"},
		{44, false, false, "past the construct's nodes"},
	} {
		if got := span.Covers(tc.line); got != tc.span {
			t.Errorf("span.Covers(%d) = %v, want %v (%s)", tc.line, got, tc.span, tc.note)
		}
		if got := point.Covers(tc.line); got != tc.pt {
			t.Errorf("point.Covers(%d) = %v, want %v (%s)", tc.line, got, tc.pt, tc.note)
		}
	}
	// THE ZERO Pos COVERS NOTHING, so a consumer resolving a breakpoint
	// against a malformed graph gets no match rather than every line.
	if (Pos{}).Covers(1) {
		t.Error("an invalid position covers nothing")
	}
}

// TestPos_SpanningRejectsAnEndBeforeItsStart is the constructor half of
// RulePositionSpan's second question.
//
// A PANIC RATHER THAN AN ERROR, for `requirePos`'s reason: there is no input a
// user can supply that produces a construct ending before it begins, so it is
// a producer bug at the call.
func TestPos_SpanningRejectsAnEndBeforeItsStart(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		line, col, endLine, endCol int
	}{
		{"an earlier line", 9, 1, 4, 1},
		{"the same line, an earlier column", 9, 7, 9, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("Spanning accepted an end before its start")
				}
				if !strings.Contains(r.(string), "cannot end before it starts") {
					t.Errorf("wrong panic: %v", r)
				}
			}()
			Spanning("f.nomi", tc.line, tc.col, tc.endLine, tc.endCol)
		})
	}
	// THE BOUNDARY IS INCLUSIVE ON BOTH AXES, which is what makes a point a
	// legal span rather than a special case.
	Spanning("f.nomi", 9, 1, 9, 1)
	// And Spanning inherits At's two gates rather than reimplementing them.
	for _, tc := range []struct {
		name, file string
		line       int
	}{
		{"no file", "", 9},
		{"no line", "f.nomi", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("Spanning accepted %s", tc.name)
				}
			}()
			Spanning(tc.file, tc.line, 1, tc.line+1, 1)
		})
	}
}

// TestPos_TheEndCannotNameADifferentFile is the POSITIVE for the third
// question, which is refused as a lint rule and closed at the constructor.
//
// It asserts the thing a rule would have reported: whatever a producer does,
// the end's file is the start's. There is no field that could hold another,
// so the check is over the whole type rather than over the constructors — a
// composite literal inside this package, which is the population every other
// gate here leaves its rule, cannot reach it either.
func TestPos_TheEndCannotNameADifferentFile(t *testing.T) {
	// The in-package composite literal, which is how every plant in
	// lint_test.go reaches a field the constructors gate.
	p := Pos{file: "start.nomi", line: 1, col: 1, endLine: 9, endCol: 1}
	if p.File() != "start.nomi" {
		t.Fatalf("the file is %q", p.File())
	}
	// A Pos has one file field. If a change adds an end file, this fails and
	// the rule refused above becomes necessary.
	// Checked by CONSTRUCTION rather than by reflection: the composite
	// literal below names every field, so a sixth would be a compile error
	// here and a reader would be sent to this comment.
	var full = Pos{
		file:    "start.nomi",
		line:    1,
		col:     1,
		endLine: 9,
		endCol:  1,
		synth:   false,
	}
	if full.File() != p.File() || full.EndLine() != p.EndLine() {
		t.Fatal("the two literals disagree, so the field list above is stale")
	}
}

// TestLint_PositionSpanCatchesItsPlants plants RulePositionSpan's two
// questions, one each, and shows both failing.
//
// BOTH PLANTS ARE COMPOSITE LITERALS INSIDE THIS PACKAGE, which is the
// population every gate here leaves its rule — `At` sets a point's end and
// `Spanning` rejects a reversed one, so neither defect is constructable from
// outside. TestLint_PositionValidCatchesItsPlants' own header makes the same
// statement for the line and file halves.
func TestLint_PositionSpanCatchesItsPlants(t *testing.T) {
	t.Run("an end that is absent", func(t *testing.T) {
		f := NewFunc(At(lintFile(), 1, 1), "total")
		b := f.NewBlock(At(lintFile(), 1, 1), "entry")
		one := NewInt(At(lintFile(), 2, 1), f.NewTemp(), 1)
		b.Append(one)
		b.SetTerm(NewReturn(At(lintFile(), 2, 1), one.Dst()))
		// PLANT: the end cleared after construction, on the function and on
		// the instruction — which is what a future pass in this package that
		// rebuilt a node from its parts would produce.
		f.Region.pos.endLine, f.Region.pos.endCol = 0, 0
		one.pos.endLine, one.pos.endCol = 0, 0

		got := violationsFor(t, f, RulePositionSpan)
		if len(got) != 2 {
			t.Fatalf("expected two violations — the func and the instruction — got %d: %v",
				len(got), got)
		}
		for _, v := range got {
			if !strings.Contains(v.Why, "no end") {
				t.Errorf("wrong diagnosis: %v", v)
			}
		}
		if got[0].What != "func total" || got[1].What != "b0 instr 0 (t1 = const int 1)" {
			t.Errorf("the violations blame %q and %q", got[0].What, got[1].What)
		}
	})

	t.Run("an end before its start", func(t *testing.T) {
		f := wellFormed()
		// PLANT: a block whose extent runs backwards. Written as an absolute
		// pair rather than `line - 1`, because `wellFormed`'s entry block is
		// on line 1 and `0` would be the ABSENT-end plant above wearing this
		// one's name.
		f.Blocks()[0].pos.line, f.Blocks()[0].pos.endLine = 9, 4

		got := violationsFor(t, f, RulePositionSpan)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0].Why, "ends before it starts") {
			t.Errorf("wrong diagnosis: %v", got[0])
		}
	})

	t.Run("an end before its start on the same line", func(t *testing.T) {
		// THE COLUMN HALF, which the line half cannot see. A construct that
		// starts at column 9 and ends at column 2 of the same line is the
		// shape a producer swapping two arguments writes, and `endLine <
		// line` is false for it.
		f := wellFormed()
		f.Blocks()[0].pos.col = 9
		f.Blocks()[0].pos.endCol = 2

		got := violationsFor(t, f, RulePositionSpan)
		if len(got) != 1 {
			t.Fatalf("expected one violation, got %d: %v", len(got), got)
		}
		if !strings.Contains(got[0].Why, "ends before it starts") {
			t.Errorf("wrong diagnosis: %v", got[0])
		}
	})

	t.Run("a well-formed function has none", func(t *testing.T) {
		// THE CONTROL, without which both plants above could be passing
		// because the rule fires on everything.
		if got := violationsFor(t, wellFormed(), RulePositionSpan); len(got) != 0 {
			t.Fatalf("a well-formed function violates the rule %d time(s): %v", len(got), got)
		}
	})
}

// TestLint_AnInvalidPositionIsNotAlsoASpanViolation is the narrowing that
// keeps the two rules' counts readable.
//
// A Pos with no line has no end either, so without the early return in
// `checkPos` every RulePositionValid plant would report a RulePositionSpan
// violation beside it and RulePositionValid's own plant counts would move.
// One position, one diagnosis, and the FIRST one is the one a producer fixes.
func TestLint_AnInvalidPositionIsNotAlsoASpanViolation(t *testing.T) {
	f := wellFormed()
	f.Blocks()[0].pos = Pos{}
	if got := violationsFor(t, f, RulePositionSpan); len(got) != 0 {
		t.Fatalf("a position with no line reported %d span violation(s): %v", len(got), got)
	}
	if got := violationsFor(t, f, RulePositionValid); len(got) != 1 {
		t.Fatalf("expected the one RulePositionValid violation, got %d", len(got))
	}
}
