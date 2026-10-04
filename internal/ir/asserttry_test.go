package ir

import (
	"strings"
	"testing"
)

// The `assert` and `try` classes, and the rule that an assertion's rendered
// source text is a field on the node. See assert.go and try.go, which carry
// the argument.

func assertPos() Pos { return At("t.nomi", 11, 3) }

// TestAssert_TheSourceTEXTIsAFieldAndItIsREQUIRED states point 3 of ir.go's
// package header as a test.
//
// What makes the rule real is not that a field exists but that a node cannot
// be built without it: a producer that has no rendered source has not
// finished lowering the assertion, and a report with an empty `Expr` prints a
// bare keyword. All three nodes (Assert, Record, Try) enforce it.
func TestAssert_TheSourceTEXTIsAFieldAndItIsREQUIRED(t *testing.T) {
	a := NewAssert(assertPos(), NoTemp, 4, KeywordAssert, "found")
	if a.Text() != "found" {
		t.Errorf("Assert.Text is %q", a.Text())
	}
	r := NewRecordOperand(assertPos(), 4, "xs |> Iter.count()", true)
	if r.Text() != "xs |> Iter.count()" {
		t.Errorf("Record.Text is %q", r.Text())
	}
	tr := NewTry(assertPos(), 4, "try parse(-1)")
	if tr.Text() != "try parse(-1)" {
		t.Errorf("Try.Text is %q", tr.Text())
	}

	for _, tc := range []struct {
		name string
		make func()
	}{
		{"assert", func() { NewAssert(assertPos(), NoTemp, 4, KeywordAssert, "") }},
		{"record operand", func() { NewRecordOperand(assertPos(), 4, "", true) }},
		{"record stage", func() { NewRecordStage(assertPos(), 4, "") }},
		{"try", func() { NewTry(assertPos(), 4, "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("a node with no rendered source was built")
				}
			}()
			tc.make()
		})
	}
}

// TestAssert_ONEJudgementTHREEDeliveries is the fault/delivery division at a
// class with no trap in it.
//
// `assert`, `refute` and `testing.check` are one judgement. The node carries
// WHICH SPELLING, because the report prints it; it carries nothing about where
// the verdict goes. irbuild's `assertionExit`, the thing that differs, belongs
// to the `jump` class.
//
// The one asymmetry is `Dst`, and it is the DELIVERY's: `check` answers a
// `Result` VALUE, so its judgement writes a temporary, while `assert` and
// `refute` evaluate to their own subject and write nothing. That is enforced
// in both directions so neither producer can drift into the other's shape.
func TestAssert_ONEJudgementTHREEDeliveries(t *testing.T) {
	for _, kw := range []AssertKeyword{KeywordAssert, KeywordRefute} {
		a := NewAssert(assertPos(), NoTemp, 4, kw, "x")
		if a.Dst() != NoTemp {
			t.Errorf("%s writes %v", kw, a.Dst())
		}
		if a.Refuted() != (kw == KeywordRefute) {
			t.Errorf("%s reports Refuted()=%v", kw, a.Refuted())
		}
	}
	c := NewAssert(assertPos(), 7, 4, KeywordCheck, "x")
	if c.Dst() != 7 {
		t.Errorf("check writes %v", c.Dst())
	}
	if c.Refuted() {
		t.Error("check is refuting")
	}

	// Neither shape can be built the other way round. A `check` with no
	// destination has nowhere to put its Result; an `assert` with one claims a
	// value the language does not give it.
	for _, tc := range []struct {
		name string
		make func()
	}{
		{"check with no destination", func() { NewAssert(assertPos(), NoTemp, 4, KeywordCheck, "x") }},
		{"assert with a destination", func() { NewAssert(assertPos(), 7, 4, KeywordAssert, "x") }},
		{"refute with a destination", func() { NewAssert(assertPos(), 7, 4, KeywordRefute, "x") }},
		{"no subject", func() { NewAssert(assertPos(), NoTemp, NoTemp, KeywordAssert, "x") }},
		{"unknown keyword", func() { NewAssert(assertPos(), NoTemp, 4, AssertKeyword(9), "x") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("the producer built it")
				}
			}()
			tc.make()
		})
	}
}

// TestAssert_NoFieldCarriesAConsumerDecision, read field by field.
//
// Nothing on an `Assert` is: the subject's SHAPE (Bool, `Maybe`/`Result`,
// `Assertable`), which is read off the operand's type; a reason
// string, which rt picks from the polarity; a trace buffer, which is a
// consumer's local; a boundary, which is `assertionExit`'s; or an
// `rt.AssertionSite` field name.
func TestAssert_NoFieldCarriesAConsumerDecision(t *testing.T) {
	a := NewAssert(assertPos(), NoTemp, 4, KeywordRefute, "ok?(x)")
	if got := a.String(); got != `refute t4 "ok?(x)"` {
		t.Errorf("String is %q", got)
	}
	if uses := a.AppendUses(nil); len(uses) != 1 || uses[0] != 4 {
		t.Errorf("an assertion reads %v", uses)
	}
	// The keyword is the ONLY discriminator, and it has exactly three states.
	// A fourth would be a consumer's spelling rather than a source's.
	for _, kw := range []AssertKeyword{KeywordAssert, KeywordRefute, KeywordCheck} {
		if kw.String() == "keyword?" {
			t.Errorf("%d has no name", kw)
		}
	}
	if AssertKeyword(0).String() != "keyword?" || AssertKeyword(4).String() != "keyword?" {
		t.Error("an out-of-range keyword names itself")
	}
}

// TestRecord_OneOperationTwoPositions is the `match`/`destructure` finding one
// class over: an operand row and a pipeline-stage row are the same operation —
// bind a rendered source text to an observed value — differing in DELIVERY.
//
// rt says so by building the same pair of fields twice. What is
// checked here is that the two kinds are the same node and that only the
// operand kind is offered the suppression request.
func TestRecord_OneOperationTwoPositions(t *testing.T) {
	op := NewRecordOperand(assertPos(), 4, "n", true)
	stage := NewRecordStage(assertPos(), 4, "n")
	if op.Kind() != RecordOperand || stage.Kind() != RecordStage {
		t.Fatalf("kinds are %s / %s", op.Kind(), stage.Kind())
	}
	if op.Val() != stage.Val() || op.Text() != stage.Text() || op.Pos() != stage.Pos() {
		t.Error("the two kinds disagree about the fields they share")
	}
	if op.Dst() != NoTemp || stage.Dst() != NoTemp {
		t.Error("a report row writes a temporary")
	}
	// A stage is never suppressed. A pipe's first stage is
	// usually redundant by that rule and printing it is the point.
	if stage.SuppressRedundant() {
		t.Error("a stage row asks to be suppressed")
	}
	if !op.SuppressRedundant() {
		t.Error("an operand row did not carry the request it was built with")
	}
	// The predicate VARIES over the population it is about, which is what
	// keeps it from being a constant field that carries no information: a
	// predicate's literal arguments are recorded WITHOUT it.
	if NewRecordOperand(assertPos(), 4, "5", false).SuppressRedundant() {
		t.Error("the request is constant")
	}
	if got := op.String(); got != `record operand "n" t4 suppress-redundant` {
		t.Errorf("String is %q", got)
	}
	if got := stage.String(); got != `record stage "n" t4` {
		t.Errorf("String is %q", got)
	}
}

// TestTry_CarriesNoBoundary is the `try` class's open question, answered.
//
// A `try` unwinds to the enclosing ACTIVATION and there are four of them, so
// it is tempting to put the answer on the node. It is not there, because the
// consumer reads no field for it: a `try` returns from the innermost
// activation, which is the node's POSITION IN THE PROGRAM.
//
// Stated as a read of the whole node rather than as a comment: a Try has three
// facts and none of them is a boundary, a boundary type, a lambda bit or a
// test bit.
func TestTry_CarriesNoBoundary(t *testing.T) {
	tr := NewTry(assertPos(), 4, "try parse(-1)")
	if tr.Src() != 4 || tr.Text() != "try parse(-1)" || tr.Pos().Line() != 11 {
		t.Errorf("try is %+v", tr)
	}
	if tr.Dst() != NoTemp {
		t.Error("a try writes a destination; the unwrapped payload is an ir.Proj")
	}
	if uses := tr.AppendUses(nil); len(uses) != 1 || uses[0] != 4 {
		t.Errorf("a try reads %v", uses)
	}
	if got := tr.String(); got != `try t4 "try parse(-1)"` {
		t.Errorf("String is %q", got)
	}
	if tr.Src() == NoTemp {
		t.Error("a try with no operand was built")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a try with no operand was built")
		}
	}()
	NewTry(assertPos(), NoTemp, "try x")
}

// TestAssertTry_EveryNodeCarriesItsOwnPosition holds the assert and try
// classes' nodes to the per-node position rule.
func TestAssertTry_EveryNodeCarriesItsOwnPosition(t *testing.T) {
	pos := At("t.nomi", 12, 5)
	b := NewRegion(pos, "r").NewBlock(pos, "entry")
	b.Append(NewAssert(pos, NoTemp, 1, KeywordAssert, "x"))
	b.Append(NewRecordOperand(pos, 1, "x", true))
	b.Append(NewRecordStage(pos, 1, "x"))
	b.Append(NewTry(pos, 1, "try x"))
	if len(b.Instrs()) != 4 {
		t.Fatalf("%d instructions entered the block", len(b.Instrs()))
	}
	for _, in := range b.Instrs() {
		if got := in.Pos(); got.Line() != 12 || got.Col() != 5 || got.File() != "t.nomi" {
			t.Errorf("%s carries %s", in, got)
		}
	}
	// And an invalid one cannot: the zero Pos is the only one a composite
	// literal outside this package can name.
	for _, tc := range []struct {
		name string
		make func()
	}{
		{"assert", func() { NewAssert(Pos{}, NoTemp, 1, KeywordAssert, "x") }},
		{"record", func() { NewRecordOperand(Pos{}, 1, "x", true) }},
		{"stage", func() { NewRecordStage(Pos{}, 1, "x") }},
		{"try", func() { NewTry(Pos{}, 1, "try x") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("a node with no position was built")
				}
				if !strings.Contains(r.(string), "position") {
					t.Fatalf("panicked for another reason: %v", r)
				}
			}()
			tc.make()
		})
	}
}
