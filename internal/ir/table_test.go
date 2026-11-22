package ir_test

// Tests for the type table (table.go): identity that survives two
// declarations sharing a name, one table's types kept apart from another's,
// and no entity interned without an identity.

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// tableFixture is a small program's types: two same-named `Error` declarations
// from different modules, an interface, an implementor of it, and an enum with
// an `embeds` variant.
type tableFixture struct {
	tbl *ir.Table
	// calErr and jsonErr are two DIFFERENT declarations both printed `Error`.
	calErr, jsonErr *ir.Type
	// display is an existential; score implements it, tick does not.
	display, score, tick *ir.Type
	// event is an enum one of whose variants embeds tick.
	event *ir.Type
}

func newTableFixture(t *testing.T) tableFixture {
	t.Helper()
	tbl := ir.NewTable()
	f := tableFixture{tbl: tbl}
	f.calErr, _ = tbl.Concrete("calendar.Error", "Error")
	f.jsonErr, _ = tbl.Concrete("json.Error", "Error")
	f.display, _ = tbl.Existential("std/display.Display", "Display")
	f.score, _ = tbl.Concrete("Score", "Score")
	f.tick, _ = tbl.Concrete("Tick", "Tick")
	f.event, _ = tbl.Concrete("Event", "Event")
	tbl.Conforms(f.score, f.display)
	tbl.Embeds(f.event, f.tick)
	return f
}

// TestTable_IdentityIsTheProducerTokenNotThePrintedName is the property the
// whole table rests on.
//
// Comparing declarations by printed name is a defect this repository has
// already had: two same-named `Error` types compared equal and produced
// `expected Error, got Error`, a diagnostic naming no difference. Two tokens
// are two types however identically they print, and one token is one type
// however many times it is asked for.
func TestTable_IdentityIsTheProducerTokenNotThePrintedName(t *testing.T) {
	f := newTableFixture(t)
	if f.calErr == f.jsonErr {
		t.Fatal("two `Error` declarations from two modules interned to one type, so the " +
			"table compares types by printed name")
	}
	if f.calErr.Name() != f.jsonErr.Name() {
		t.Fatal("the two fixtures do not share a printed name, so this test is not " +
			"measuring the defect it was written for")
	}
	again, minted := f.tbl.Concrete("calendar.Error", "Error")
	if again != f.calErr {
		t.Error("one token interned to two types, so the table is not an identity")
	}
	if minted {
		t.Error("a repeat call reported that it minted the type; `minted` is the " +
			"producer's cue to state the type's edges exactly once, so a second true " +
			"would duplicate them")
	}
}

// TestTable_TypesFromTwoTablesCannotBeCompared pins the ownership check.
//
// Two tables are two pointer sets, so a fact recorded across them relates
// types no reader of either holds, with nothing reporting it. A panic is the
// only outcome that is not a wrong answer.
func TestTable_TypesFromTwoTablesCannotBeCompared(t *testing.T) {
	a := newTableFixture(t)
	b := ir.NewTable()
	bScore, _ := b.Concrete("Score", "Score")

	for _, c := range []struct {
		name string
		call func()
	}{
		{"Embeds", func() { a.tbl.Embeds(a.event, bScore) }},
		{"Conforms", func() { a.tbl.Conforms(bScore, a.display) }},
	} {
		got := recovered(c.call)
		if got == "" {
			t.Errorf("%s accepted an entity from another table", c.name)
			continue
		}
		if !strings.Contains(got, "another table") {
			t.Errorf("%s panicked with %q, which does not say what was wrong", c.name, got)
		}
	}
}

// TestTable_AnIdentitylessEntityIsRejectedAtConstruction is the other half of
// the identity guarantee.
//
// A type with no producer identity has nothing to be
// interned on, and admitting one would make the table answer "same" for two
// entities that are not. There is no valid input that reaches it, so it is a
// producer bug and a panic.
func TestTable_AnIdentitylessEntityIsRejectedAtConstruction(t *testing.T) {
	tbl := ir.NewTable()
	ty, _ := tbl.Concrete("T", "T")
	for _, c := range []struct {
		name string
		call func()
	}{
		{"Concrete", func() { tbl.Concrete(nil, "T") }},
		{"Existential", func() { tbl.Existential(nil, "I") }},
	} {
		if got := recovered(c.call); got == "" {
			t.Errorf("%s accepted a nil identity", c.name)
		}
	}
	// One token cannot be two forms: a token that answered both would make
	// the type depend on which call happened first.
	if got := recovered(func() { tbl.Existential("T", "T") }); got == "" {
		t.Error("a token interned as concrete was re-interned as existential")
	}
	// Only an existential has implementors.
	other, _ := tbl.Concrete("U", "U")
	if got := recovered(func() { tbl.Conforms(other, ty) }); got == "" {
		t.Error("Conforms accepted a concrete type in the interface position")
	}
	if tbl.Types() != 2 {
		t.Errorf("the table holds %d types; every call above either interned "+
			"T and U or panicked, so a different count means a rejected call left an entry",
			tbl.Types())
	}
}
