package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// This file is the coherence key's OPERATOR axis.
//
// # THE SHAPE
//
// `impl Add<Days, Day> for Day` beside `impl Add<Days, Days> for Day` differs
// only in the OUTPUT type. Both claim one dispatch slot, so the program must
// be rejected with a diagnostic rather than accepted.
//
// # WHY THE PROGRAM IS ILL-FORMED, FROM THE SPEC RATHER THAN FROM THE BACKEND
//
// docs/spec.md, "Generic Interfaces": a generic interface's type
// parameter "is *determined* by each implementor (one impl per type), so it is
// never written on the interface name where the implementor is already known …
// Where it earns a name is exactly where the implementor is *unknown* — an
// interface-typed parameter … or a bound". `Out` is on both sides of that
// sentence:
//
//	impl Add<Days, Day> for Day     determined by the impl -> not a discriminator
//	where L: Add<R, Out>            implementor unknown     -> earns its name
//
// So two impls agreeing on (receiver, right-hand type) and disagreeing on the
// output are the SAME IMPL DECLARED TWICE, and rejecting them is the existing
// duplicate-impl rule reaching a case it was not reaching — not a new special
// case for operators. `Out` stays a type parameter, and it has to: `add_test.nomi`
// names it in bound position four times.
//
// The implementation agrees with the spec on every side but this one.
// `checkOperatorBinary` -> `lookupOperatorImplTypeArgs` takes the result type
// FROM the impl it resolved, so nothing ever selects an impl BY its output; the
// runtime dispatch key omits it; the IR builder's `g.operImpls` omits it; and no
// impl in `std/` varies it at a fixed (receiver, right-hand type) — 66 impls, 66
// groups (TestStdlibOperatorOutputIsDeterminedByReceiverAndRhs). The analyzer's
// coherence key was the one place treating a determined thing as discriminating.
//
// # WHAT THE KEY MUST BE
//
// Dispatch tells impls apart by:
//
//	operator interface     -> receiver + rhs base name   (Out absent)
//	any other interface    -> receiver                   (every arg absent)
//
// So the coherence key must be the receiver plus exactly the part of the
// interface header dispatch can tell apart — which is what DispatchImplKey
// renders.
func TestDetectImplCollisions_OperatorImplsDifferingOnlyInOutputCollide(t *testing.T) {
	fnDay := implFuncDef("add", "Add", "Day")
	fnDays := implFuncDef("add", "Add", "Day")
	index := map[string]map[string][]*ast.FuncDef{
		"Add": {"add": {fnDay, fnDays}},
	}
	files := map[*ast.FuncDef]string{fnDay: "", fnDays: ""}
	receivers := map[*ast.FuncDef]string{fnDay: "Day", fnDays: "Day"}
	ifaceKeys := map[*ast.FuncDef]string{
		fnDay:  "Add<Days, Day>",
		fnDays: "Add<Days, Days>",
	}

	errs := detectImplCollisions(index, files, receivers, ifaceKeys, nil)
	if len(errs) != 1 {
		t.Fatalf("two `Add` impls for `Day` at right-hand type `Days` differing only in "+
			"the output type produced %d diagnostics, want 1. Unrejected, this program "+
			"has two bodies for one dispatch slot: %v", len(errs), errs)
	}
	msg := errs[0].Message
	// The message must name BOTH declarations, not just the receiver: the two
	// impls are in one module, so the modules list of the general duplicate-impl
	// diagnostic would name `<project entry>` once and say nothing about which
	// two headers are at fault.
	for _, want := range []string{"Add<Days, Day>", "Add<Days, Days>", "Day", "Days", "add"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic does not name %q, so the user cannot find the pair: %s", want, msg)
		}
	}
}

// TestDetectImplCollisions_OperatorLadderAtDistinctRhsIsFine is the population
// the check must keep ACCEPTING, and it is not hypothetical: `std/calendar`
// ships eleven `Add` impls for `NaiveDateTime`, eleven for `OffsetDateTime`,
// eleven for `DateTime` and four for `Date`, each at a different right-hand
// type. A census of every `impl` header under std/, tests/ and
// examples/ at f3e3c504 found 107 operator impl headers falling into 107
// distinct (interface, receiver, right-hand base) groups — so nothing shipping
// is a member of the rejected shape.
func TestDetectImplCollisions_OperatorLadderAtDistinctRhsIsFine(t *testing.T) {
	fnDays := implFuncDef("add", "Add", "Date")
	fnMonths := implFuncDef("add", "Add", "Date")
	fnYears := implFuncDef("add", "Add", "Date")
	index := map[string]map[string][]*ast.FuncDef{
		"Add": {"add": {fnDays, fnMonths, fnYears}},
	}
	files := map[*ast.FuncDef]string{fnDays: "std/calendar", fnMonths: "std/calendar", fnYears: "std/calendar"}
	receivers := map[*ast.FuncDef]string{fnDays: "calendar.Date", fnMonths: "calendar.Date", fnYears: "calendar.Date"}
	ifaceKeys := map[*ast.FuncDef]string{
		fnDays:   "Add<Days, Date>",
		fnMonths: "Add<Months, Date>",
		fnYears:  "Add<Years, Date>",
	}

	if errs := detectImplCollisions(index, files, receivers, ifaceKeys, nil); len(errs) != 0 {
		t.Fatalf("the calendar-shaped `Add` ladder was rejected; the right-hand type IS "+
			"part of operator dispatch identity and these three are distinguishable: %v", errs)
	}
}

// TestDetectImplCollisions_NonOperatorGenericInstantiationsCollide is the SECOND
// route to the same collision:
//
//	pub interface Holds<T> { fn hold(h: self): T }
//	impl Holds<Int> for Box { ... }
//	impl Holds<String> for Box { ... }
//
// Dispatch keys any interface that is not one of the four operator interfaces
// on the bare receiver, so a NON-operator generic interface drops EVERY type
// argument from the runtime key — not just the output. A coherence key that
// kept the full header would miss this shape too.
//
// A census over std/, tests/ and examples/ at f3e3c504 found that the
// only interfaces ever instantiated in an impl header are Add, Subtract,
// Multiply and Divide — 107 headers, four names — so widening this axis rejects
// no program in the tree. Had a fifth name existed the census would have listed
// it; the instrument reports the name set, and it does produce a non-empty
// collision list when pointed at the witness above.
func TestDetectImplCollisions_NonOperatorGenericInstantiationsCollide(t *testing.T) {
	fnInt := implFuncDef("hold", "Holds", "Box")
	fnString := implFuncDef("hold", "Holds", "Box")
	index := map[string]map[string][]*ast.FuncDef{
		"Holds": {"hold": {fnInt, fnString}},
	}
	files := map[*ast.FuncDef]string{fnInt: "", fnString: ""}
	receivers := map[*ast.FuncDef]string{fnInt: "Box", fnString: "Box"}
	ifaceKeys := map[*ast.FuncDef]string{
		fnInt:    "Holds<Int>",
		fnString: "Holds<String>",
	}

	errs := detectImplCollisions(index, files, receivers, ifaceKeys, nil)
	if len(errs) != 1 {
		t.Fatalf("two instantiations of a non-operator generic interface for one receiver "+
			"produced %d diagnostics, want 1. Unrejected, this program panics "+
			"`Holds.hold for Box registered twice` at LOAD: %v", len(errs), errs)
	}
	for _, want := range []string{"Holds<Int>", "Holds<String>", "Box", "hold"} {
		if !strings.Contains(errs[0].Message, want) {
			t.Errorf("diagnostic does not name %q: %s", want, errs[0].Message)
		}
	}
}

// TestDetectImplCollisions_TheTwoArmsAreDifferentKindsOfStatement is a wording
// test, and it is load-bearing rather than cosmetic.
//
// The two rejections this check now makes are NOT the same kind of claim, and a
// user cannot tell them apart from the fact of rejection alone:
//
//   - Operator impls differing only in `Out` are a SETTLED RULE. The spec says the
//     output is determined by the implementor; every other component agrees; no
//     stdlib impl varies it. There is nothing to lift.
//   - Two instantiations of a NON-operator generic interface for one receiver are
//     a LIMITATION of the current dispatch key. The shape is Rust's
//     `impl From<i32> for Foo` / `impl From<String> for Foo`, one of that
//     language's most-used patterns. Nobody decided Nomi forbids it; the runtime
//     key for a non-operator interface carries no type arguments, so both impls
//     land in one slot. Lifting it means the key carrying them, which is exactly
//     the shape of the IR builder's `g.operImpls` second index.
//
// If a later edit collapses the two messages into one "rule"-shaped sentence, the
// limitation gets remembered as a language fact, and the next person reads a wall
// where there is a costed option. That is what this test prevents. It also
// requires the limitation arm to say what to write INSTEAD, because a diagnostic
// that only says "no" about a shape the user's previous language supports is the
// kind that is remembered as arbitrary.
func TestDetectImplCollisions_TheTwoArmsAreDifferentKindsOfStatement(t *testing.T) {
	nonOperator := func() string {
		a := implFuncDef("hold", "Holds", "Box")
		b := implFuncDef("hold", "Holds", "Box")
		errs := detectImplCollisions(
			map[string]map[string][]*ast.FuncDef{"Holds": {"hold": {a, b}}},
			map[*ast.FuncDef]string{a: "", b: ""},
			map[*ast.FuncDef]string{a: "Box", b: "Box"},
			map[*ast.FuncDef]string{a: "Holds<Int>", b: "Holds<String>"},
			nil,
		)
		if len(errs) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(errs))
		}
		return errs[0].Message
	}()
	operator := func() string {
		a := implFuncDef("add", "Add", "Day")
		b := implFuncDef("add", "Add", "Day")
		errs := detectImplCollisions(
			map[string]map[string][]*ast.FuncDef{"Add": {"add": {a, b}}},
			map[*ast.FuncDef]string{a: "", b: ""},
			map[*ast.FuncDef]string{a: "Day", b: "Day"},
			map[*ast.FuncDef]string{a: "Add<Days, Day>", b: "Add<Days, Days>"},
			nil,
		)
		if len(errs) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(errs))
		}
		return errs[0].Message
	}()

	if !strings.Contains(nonOperator, "LIMITATION") {
		t.Errorf("the non-operator rejection no longer calls itself a limitation, so it reads as a "+
			"decided language rule. It is not one — the shape is Rust's `impl From<i32> for Foo` "+
			"pair and only the dispatch key stops it: %s", nonOperator)
	}
	if !strings.Contains(nonOperator, "type arguments") {
		t.Errorf("the non-operator rejection no longer names WHAT would lift it (the runtime key "+
			"carrying the interface's type arguments), so a reader gets a wall instead of a "+
			"costed option: %s", nonOperator)
	}
	for _, advice := range []string{"named function", "enum"} {
		if !strings.Contains(nonOperator, advice) {
			t.Errorf("the non-operator rejection does not mention %q, so it says `no` without "+
				"saying what to write instead: %s", advice, nonOperator)
		}
	}
	if strings.Contains(operator, "LIMITATION") {
		t.Errorf("the OPERATOR rejection now calls itself a limitation. It is a settled rule — the "+
			"spec makes the output determined by the implementor and no stdlib impl varies it — "+
			"and inviting someone to lift it would be inviting them to make `a + b` ambiguous: %s",
			operator)
	}
	if !strings.Contains(operator, "DETERMINES") {
		t.Errorf("the operator rejection no longer says the output is DETERMINED, which is the whole "+
			"reason it is a rule rather than a limit: %s", operator)
	}
}

// TestDetectImplCollisions_OperatorRhsIsComparedByBaseName pins the rhs
// reduction rather than leaving it implied. The right-hand type comes from the
// interface HEADER, which is source text, with its own type arguments
// stripped. So `Add<List<Int>, X>` and `Add<List<String>, X>` occupy ONE
// dispatch slot, and the coherence key has to agree or the collision returns
// under a shape nobody wrote a test for.
func TestDetectImplCollisions_OperatorRhsIsComparedByBaseName(t *testing.T) {
	fnA := implFuncDef("add", "Add", "Bag")
	fnB := implFuncDef("add", "Add", "Bag")
	index := map[string]map[string][]*ast.FuncDef{"Add": {"add": {fnA, fnB}}}
	files := map[*ast.FuncDef]string{fnA: "", fnB: ""}
	receivers := map[*ast.FuncDef]string{fnA: "Bag", fnB: "Bag"}
	ifaceKeys := map[*ast.FuncDef]string{
		fnA: "Add<List<Int>, Bag>",
		fnB: "Add<List<String>, Bag>",
	}

	if errs := detectImplCollisions(index, files, receivers, ifaceKeys, nil); len(errs) != 1 {
		t.Fatalf("two `Add` impls whose right-hand types share the base name `List` produced "+
			"%d diagnostics, want 1 — the runtime key carries the base name only: %v", len(errs), errs)
	}
}

// TestDetectImplCollisions_UnresolvableOperatorHeaderFailsOpen is the fail-safe
// posture the surrounding file already takes on the identity side ("an unknown
// origin on either side accepts"). An operator impl whose interface header did
// not reach the index has no right-hand type to key on; grouping it under the
// bare interface name would collapse a whole ladder onto one key and reject the
// calendar population on a missing map entry rather than on a real conflict.
func TestDetectImplCollisions_UnresolvableOperatorHeaderFailsOpen(t *testing.T) {
	fnA := implFuncDef("add", "Add", "Date")
	fnB := implFuncDef("add", "Add", "Date")
	index := map[string]map[string][]*ast.FuncDef{"Add": {"add": {fnA, fnB}}}
	files := map[*ast.FuncDef]string{fnA: "std/calendar", fnB: "std/calendar"}
	receivers := map[*ast.FuncDef]string{fnA: "calendar.Date", fnB: "calendar.Date"}
	// One header known, one absent. The known one keys on its rhs; the absent
	// one keys on what it has, and the two must not be forced together.
	ifaceKeys := map[*ast.FuncDef]string{fnA: "Add<Days, Date>"}

	if errs := detectImplCollisions(index, files, receivers, ifaceKeys, nil); len(errs) != 0 {
		t.Fatalf("a missing interface header produced a diagnostic; the check must only ever "+
			"reject on evidence it has, not on an index gap: %v", errs)
	}
}

// TestDispatchImplKey_MirrorsTheRuntimeKey is the specification of the shared
// helper, stated as a table so a later reader can see the whole rule at once.
func TestDispatchImplKey_MirrorsTheRuntimeKey(t *testing.T) {
	for _, row := range []struct{ iface, ifaceKey, want string }{
		// Operator interfaces keep the right-hand type and drop the output.
		{"Add", "Add<Days, Day>", "Add<Days>"},
		{"Add", "Add<Days, Days>", "Add<Days>"},
		{"Subtract", "Subtract<Duration, Instant>", "Subtract<Duration>"},
		{"Multiply", "Multiply<Int, Duration>", "Multiply<Int>"},
		{"Divide", "Divide<NonZeroInt, Int>", "Divide<NonZeroInt>"},
		// The right-hand type is compared by BASE name, as at registration.
		{"Add", "Add<List<Int>, Bag>", "Add<List>"},
		// A non-operator interface drops every type argument: the runtime key
		// for one of those is the receiver alone.
		{"Holds", "Holds<Int>", "Holds"},
		{"Holds", "Holds<String>", "Holds"},
		{"Display", "Display", "Display"},
		// An operator header with nothing to read falls back to what it has,
		// so an index gap cannot collapse a ladder.
		{"Add", "Add", "Add"},
		{"Add", "", "Add"},
	} {
		if got := DispatchImplKey(row.iface, row.ifaceKey); got != row.want {
			t.Errorf("DispatchImplKey(%q, %q) = %q, want %q", row.iface, row.ifaceKey, got, row.want)
		}
	}
}

// TestOperatorInterfaceRhs_ReadsTheFirstArgumentOnly pins the extraction the
// coherence key depends on. The nested-argument rows are the
// reason this is a depth-tracking walk rather than a split on the first comma.
func TestOperatorInterfaceRhs_ReadsTheFirstArgumentOnly(t *testing.T) {
	for _, row := range []struct{ in, want string }{
		{"Add<Days, Day>", "Days"},
		{"Add<Days>", "Days"},
		{"Divide<NonZeroInt, Int>", "NonZeroInt"},
		{"Add<Map<String, Int>, Bag>", "Map"},
		{"Add< Days , Day >", "Days"},
		{"Holds<Int>", ""}, // not an operator interface
		{"Display", ""},    // no type arguments at all
		{"", ""},           // nothing to read
		{"Add<>", ""},      // empty argument list
		// Unterminated, but the first argument still ENDS at the comma, so it
		// is complete and readable. A truncated header is a parse error long
		// before this point; the row records what the walk does, not a wish.
		{"Add<Days, Day", "Days"},
		// Unterminated with nothing after `<`: no argument boundary at all.
		{"Add<Days", ""},
	} {
		if got := OperatorInterfaceRhs(row.in); got != row.want {
			t.Errorf("OperatorInterfaceRhs(%q) = %q, want %q", row.in, got, row.want)
		}
	}
}
