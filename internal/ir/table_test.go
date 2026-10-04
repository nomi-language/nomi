package ir_test

// Tests for the type and declaration table (table.go).
//
// The table exists so that a rule which chooses between two same-named
// declarations by their SIGNATURES is expressible over the representation. So
// the properties worth testing are the ones a `*Symbol` could not have held:
// identity that survives two declarations sharing a name, a subtyping relation
// with all three of its cases reachable, a tiered scoring whose tiers are
// distinguishable, and an ambiguity answer rather than a silent pick.

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
			"producer's cue to declare subtyping edges exactly once, so a second true " +
			"would duplicate them")
	}

	// A declaration is the same: one token is one Decl with one signature,
	// and the signature offered on a repeat call is ignored rather than
	// overwriting.
	add := f.tbl.Declare("Day+Days", "Day.add", []*ir.Type{f.score, f.tick}, f.score)
	repeat := f.tbl.Declare("Day+Days", "Day.add", []*ir.Type{f.score}, f.score)
	if add != repeat {
		t.Error("one declaration token interned to two Decls")
	}
	if got := repeat.Sig().Arity(); got != 2 {
		t.Errorf("a repeat Declare replaced the signature (arity now %d, want 2); a "+
			"declaration whose signature can be rewritten is not an identity a selection "+
			"can rest on", got)
	}
	other := f.tbl.Declare("Day+Weeks", "Day.add", []*ir.Type{f.score, f.tick}, f.score)
	if other == add {
		t.Error("two `Day.add` declarations with identical signatures interned to one " +
			"Decl. Two `impl Add` blocks on one receiver ARE two declarations, and " +
			"collapsing them is what makes `Day + Days` and `Day + Weeks` one answer")
	}
}

// TestTable_WidensHasThreeCasesAndAllThreeAreReachable is the subtyping
// relation, with every case exercised in both directions.
//
// Three cases and no more, and the reason it is three is Nomi's: a value is
// accepted where a type is declared when it IS that type, when the declared
// type is an interface it implements, or when the declared type is an enum one
// of whose variants embeds it. A biconditional over a population where one
// side is empty holds vacuously, so each case is checked with a NEGATIVE
// beside it.
func TestTable_WidensHasThreeCasesAndAllThreeAreReachable(t *testing.T) {
	f := newTableFixture(t)
	cases := []struct {
		name       string
		want, have *ir.Type
		accept     bool
	}{
		{"identity", f.score, f.score, true},
		{"identity, two same-named declarations", f.calErr, f.jsonErr, false},
		{"erasure into an implemented interface", f.display, f.score, true},
		{"erasure with no recorded impl", f.display, f.tick, false},
		{"widening into an enum that embeds it", f.event, f.tick, true},
		{"widening into an enum that does not", f.event, f.score, false},
		{"an existential is never re-erased", f.display, f.display, true},
		{"nothing widens to a nil type", f.score, nil, false},
		{"a nil declared type accepts nothing", nil, f.score, false},
	}
	accepted := 0
	for _, c := range cases {
		if got := f.tbl.Widens(c.want, c.have); got != c.accept {
			t.Errorf("%s: Widens(%s, %s) = %v, want %v",
				c.name, c.want, c.have, got, c.accept)
		}
		if c.accept {
			accepted++
		}
	}
	if accepted == 0 || accepted == len(cases) {
		t.Fatalf("%d of %d cases are accepted, so the table above is one-sided and would "+
			"pass for a relation that answered the same thing everywhere",
			accepted, len(cases))
	}

	// The re-erasure rule needs its own case, because the row above is
	// identity rather than erasure. An existential with a RECORDED
	// conformance to a second existential is still not accepted there.
	other, _ := f.tbl.Existential("std/debug.Debug", "Debug")
	f.tbl.Conforms(f.score, other)
	if !f.tbl.Widens(other, f.score) {
		t.Fatal("the recorded conformance did not take, so the check below is vacuous")
	}
	if f.tbl.Widens(other, f.display) {
		t.Error("an already-erased value was accepted where a second interface was " +
			"declared. Erasing twice would put a dispatch table inside a dispatch table " +
			"with nothing left to recover the concrete type from")
	}
}

// TestTable_AcceptsTiersAreDistinguishable checks the scoring's three answers.
//
// The one bit FitWidened adds over FitExact is what separates an impl whose
// parameter is the argument's own type from one whose parameter merely accepts
// it, and it is the bit `SelectOverload` breaks ties with. A scorer that
// answered a boolean would make `Day + Days` and `Day + Weeks` rivals.
func TestTable_AcceptsTiersAreDistinguishable(t *testing.T) {
	f := newTableFixture(t)
	exact := f.tbl.Declare("exact", "f", []*ir.Type{f.score, f.score}, f.score)
	widened := f.tbl.Declare("widened", "f", []*ir.Type{f.display, f.score}, f.score)
	wrongArity := f.tbl.Declare("arity", "f", []*ir.Type{f.score}, f.score)
	unaccepted := f.tbl.Declare("unaccepted", "f", []*ir.Type{f.tick, f.score}, f.score)

	args := []*ir.Type{f.score, f.score}
	for _, c := range []struct {
		d    *ir.Decl
		want ir.Fit
	}{
		{exact, ir.FitExact},
		{widened, ir.FitWidened},
		{wrongArity, ir.FitNone},
		{unaccepted, ir.FitNone},
	} {
		if got := f.tbl.Accepts(c.d, args); got != c.want {
			t.Errorf("Accepts(%s) = %s, want %s", c.d.Name(), got, c.want)
		}
	}
	if ir.FitExact <= ir.FitWidened || ir.FitWidened <= ir.FitNone {
		t.Error("the tiers are not ordered, so SelectOverload's `<` and `>` compare " +
			"nothing meaningful")
	}
	// An argument whose own lowering was refused arrives with no type. It is
	// "not a candidate" rather than a crash, matching what the builder does
	// with kindInvalid — a candidate filter is the wrong place to turn an
	// already-recorded refusal into a panic.
	if got := f.tbl.Accepts(exact, []*ir.Type{nil, f.score}); got != ir.FitNone {
		t.Errorf("a nil argument type scored %s, want none", got)
	}
}

// TestTable_SelectOverloadPrefersTheBetterTierAndRefusesEquals is the
// selection rule.
//
// A strictly better tier discards every rival found so far and two candidates
// at one tier is an AMBIGUITY, not a pick. A refusal is never a wrong answer
// and a silent pick between equals is.
func TestTable_SelectOverloadPrefersTheBetterTierAndRefusesEquals(t *testing.T) {
	f := newTableFixture(t)
	exact := f.tbl.Declare("exact", "Day.add", []*ir.Type{f.score, f.score}, f.score)
	widened := f.tbl.Declare("widened", "Day.add", []*ir.Type{f.display, f.score}, f.score)
	otherWidened := f.tbl.Declare("otherWidened", "Day.add", []*ir.Type{f.score, f.display}, f.score)
	miss := f.tbl.Declare("miss", "Day.add", []*ir.Type{f.tick, f.tick}, f.score)
	args := []*ir.Type{f.score, f.score}

	// ORDER-INDEPENDENT. The answer is the highest tier reached and whether
	// more than one candidate reached it, and neither is a function of the
	// order. Both orders are asserted because the loop READS as though it
	// depended on one.
	for _, cands := range [][]*ir.Decl{
		{widened, exact, miss},
		{miss, exact, widened},
		{exact, widened, miss},
	} {
		got, ambiguous := f.tbl.SelectOverload(cands, args)
		if ambiguous {
			t.Errorf("an exact fit beside a widened one was reported ambiguous")
			continue
		}
		if got != exact {
			t.Errorf("selected %v, want the exact fit; a strictly better tier must discard "+
				"every rival found so far", got)
		}
	}

	// Two at one tier.
	if got, ambiguous := f.tbl.SelectOverload([]*ir.Decl{widened, otherWidened}, args); !ambiguous {
		t.Errorf("two candidates at the widened tier selected %v instead of reporting "+
			"ambiguity", got)
	} else if got != nil {
		t.Errorf("an ambiguous selection also returned %v; a caller that ignored the "+
			"second result would emit a silent pick between equals", got)
	}

	// Nothing fits.
	if got, ambiguous := f.tbl.SelectOverload([]*ir.Decl{miss}, args); got != nil || ambiguous {
		t.Errorf("a miss answered (%v, %v), want (nil, false) — a miss and an ambiguity "+
			"are different answers and the producer reports them differently",
			got, ambiguous)
	}
	if got, _ := f.tbl.SelectOverload(nil, args); got != nil {
		t.Errorf("an empty candidate set selected %v", got)
	}

	// PLANT A POSITIVE on the ambiguity check: the same two candidates against
	// arguments only ONE of them accepts must select rather than refuse, or
	// the assertion above would hold for a table that reported ambiguity
	// whenever it saw two candidates.
	oneFits := []*ir.Type{f.score, f.tick}
	if got, ambiguous := f.tbl.SelectOverload([]*ir.Decl{widened, otherWidened}, oneFits); ambiguous || got != nil {
		t.Errorf("expected neither candidate to fit (%s, %s) but got (%v, %v)",
			f.score, f.tick, got, ambiguous)
	}
	if got, ambiguous := f.tbl.SelectOverload([]*ir.Decl{exact, miss}, args); ambiguous || got != exact {
		t.Errorf("two candidates of which one fits answered (%v, %v), want the fitting "+
			"one; ambiguity must be about the TIER and not about the count",
			got, ambiguous)
	}
}

// TestTable_TypesFromTwoTablesCannotBeCompared pins the ownership check.
//
// Two tables are two pointer sets, so a cross-table comparison silently
// answers "different type" for one declaration — which would make a selection
// decline where it should have chosen, with nothing reporting it. A panic is
// the only outcome that is not a wrong answer.
func TestTable_TypesFromTwoTablesCannotBeCompared(t *testing.T) {
	a := newTableFixture(t)
	b := ir.NewTable()
	bScore, _ := b.Concrete("Score", "Score")

	for _, c := range []struct {
		name string
		call func()
	}{
		{"Widens want", func() { a.tbl.Widens(bScore, a.score) }},
		{"Widens have", func() { a.tbl.Widens(a.score, bScore) }},
		{"Embeds", func() { a.tbl.Embeds(a.event, bScore) }},
		{"Conforms", func() { a.tbl.Conforms(bScore, a.display) }},
		{"SelectOverload", func() {
			d := b.Declare("f", "f", []*ir.Type{bScore}, bScore)
			a.tbl.SelectOverload([]*ir.Decl{d}, []*ir.Type{a.score})
		}},
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
// A type or a declaration with no producer identity has nothing to be
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
		{"Declare", func() { tbl.Declare(nil, "f", nil, ty) }},
	} {
		if got := recovered(c.call); got == "" {
			t.Errorf("%s accepted a nil identity", c.name)
		}
	}
	// One token cannot be two forms. An existential and a concrete type
	// differ in exactly the thing Widens reads, so a token that answered both
	// would make the relation depend on which call happened first.
	if got := recovered(func() { tbl.Existential("T", "T") }); got == "" {
		t.Error("a token interned as concrete was re-interned as existential")
	}
	// Only an existential has implementors.
	other, _ := tbl.Concrete("U", "U")
	if got := recovered(func() { tbl.Conforms(other, ty) }); got == "" {
		t.Error("Conforms accepted a concrete type in the interface position")
	}
	if tbl.Types() != 2 || tbl.Decls() != 0 {
		t.Errorf("the table holds %d types and %d decls; every call above either interned "+
			"T and U or panicked, so a different count means a rejected call left an entry",
			tbl.Types(), tbl.Decls())
	}
}
