package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

// TestScalarArith_TheWrittenOutSpellingAnswersAsTheOperator runs the scalar
// operator impls written out by name (`Divide.divide(20, 5)`, `Int.add`)
// beside the operators, and needs every case to pass: interfaceCall lowers
// a call of an impl the machine computes as its operator (ifacecall.go),
// so both spellings are one rt call. A zero divisor is no
// case: the divisor of `/` and `%` is a NonZeroInt, so a checked program
// never reaches the VM's division-by-zero fault.
func TestScalarArith_TheWrittenOutSpellingAnswersAsTheOperator(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	got := vmReference(fixture("scalar_operator_impls.nomi"))
	if got.exit != 0 {
		t.Fatalf("every case of the fixture passes, so the run must exit 0: %s", got)
	}
	for _, want := range []string{
		"the written-out impls agree on every non-trapping answer",
		"the written-out impls are exact past 64 bits, as the operators are",
		"a Float divide by zero is IEEE and does not trap",
		"a divisor checked while the program runs divides in both spellings",
	} {
		if !strings.Contains(got.stdout, "ok ") || !strings.Contains(got.stdout, want) {
			t.Fatalf("the fixture did not report %q:\n%s", want, got.stdout)
		}
	}
}

// TestScalarArith_TheOperatorKeysStayUnbound is the SCOPE BOUNDARY, pinned
// rather than left as an absence.
//
// The written-out scalar operator impls are machine impls (machineImpls),
// which a call lowers as its operator (interfaceCall), and deliberately
// have no internal/stdlibbindings row, so their std keys are still refused
// by the index. They are the Int, Float and Decimal arithmetic operators
// plus String's `+`. None can be a registry row, because a row is a named
// rt symbol of exactly the Nomi arity and has nowhere to put the line a `/`
// fault must blame. stdKey instantiates these keys (`int.Int.Add<Int,
// Int>.add`), while a bare spelling (`int.Int.add`) is the other
// candidate, so a future registry row has to decide which it is keyed on;
// this test is where that fires.
//
// # THE CONTROL
//
// The host table contains no key with a `<` in it, so the registry cannot
// supply a same-shape positive. The control is taken from the STD INDEX
// instead: each key must NAME A REAL DECLARATION in
// `stdlibLowering().byKey`, so a typo fails directly and the check does not
// depend on any binding existing.
func TestScalarArith_TheOperatorKeysStayUnbound(t *testing.T) {
	keys := []string{
		"int.Int.Add<Int, Int>.add",
		"int.Int.Subtract<Int, Int>.subtract",
		"int.Int.Multiply<Int, Int>.multiply",
		"int.Int.Divide<NonZeroInt, Int>.divide",
		"float.Float.Add<Float, Float>.add",
		"float.Float.Subtract<Float, Float>.subtract",
		"float.Float.Multiply<Float, Float>.multiply",
		"float.Float.Divide<Float, Float>.divide",
		"strings.String.Add<String, String>.add",
		"decimal.Decimal.Add<Decimal, Decimal>.add",
		"decimal.Decimal.Subtract<Decimal, Decimal>.subtract",
		"decimal.Decimal.Multiply<Decimal, Decimal>.multiply",
		"decimal.Decimal.Divide<NonZeroDecimal, Decimal>.divide",
		"int.Int.Remainder<NonZeroInt, Int>.remainder",
		"float.Float.Remainder<Float, Float>.remainder",
		"decimal.Decimal.Remainder<NonZeroDecimal, Decimal>.remainder",
	}
	for _, key := range keys {
		if _, bound := stdlibHostRegistry().funcs[key]; bound {
			t.Errorf("%s has an internal/stdlibbindings row. A row is a named rt "+
				"symbol of exactly the Nomi arity, so it cannot carry the CALL'S LINE, "+
				"and every fault it reaches would blame whatever constant the rt symbol "+
				"was written with. A second obstacle: this family's host key is "+
				"`int.Int.add` / `float.Float.subtract` WITHOUT the instantiation while "+
				"stdKey adds it.", key)
		}
	}
	// The positive control: the map is populated and this loop is reading a real
	// index rather than a nil one, which would make every miss above vacuous.
	if len(stdlibHostRegistry().funcs) == 0 {
		t.Fatal("stdlibHostRegistry is empty, so every absence asserted above is vacuous")
	}
	// The SHAPE control, from the std index rather than from the registry: each
	// key above must name a declaration, or its absence from the registry says
	// nothing.
	idx := stdlibLowering()
	for _, key := range keys {
		if _, declared := idx.byKey[key]; !declared {
			t.Errorf("%q names no stdlib declaration, so asserting it is unbound is "+
				"vacuous. Either the key is a typo or std stopped declaring the impl.",
				key)
		}
	}
	// No registry row carries an instantiated interface. If one arrives, this
	// test's control can be a same-shape positive from the registry instead, and
	// this branch and the paragraph that explains it can go.
	for _, b := range append(stdlibbindings.RtFuncs(), stdlibbindings.Funcs()...) {
		if strings.Contains(b.Name, "<") {
			t.Errorf("%s is a registry row with an instantiated interface in its key, "+
				"which is the shape this test records as having no instances.", b.Name)
		}
	}
}
