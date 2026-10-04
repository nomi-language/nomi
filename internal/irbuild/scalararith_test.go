package irbuild

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScalarArith_TheWrittenOutSpellingBlamesTheCallsOwnLine is the ABSOLUTE
// half, and it is not redundant with the golden comparison above.
//
// A golden comparison holds the output to whatever was last recorded, so it
// cannot see a recording that is wrong: a written-out impl that blamed `line 0`
// would pass it. This test checks the lines themselves.
//
// WHAT IT PINS IS A RELATION, NOT SIX LITERALS. The property that survives an
// edit anywhere in the fixture is that the six trapping cases blame six
// DISTINCT real lines: each spelling blames its own source position, so no two
// of them can coincide and none of them can be 0. Pinning the numbers
// themselves would make an unrelated edit above line 38 present as a
// regression.
func TestScalarArith_TheWrittenOutSpellingBlamesTheCallsOwnLine(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	got := vmReference(fixture("scalar_operator_traps.nomi"))
	if got.exit != 1 {
		t.Fatalf("six cases in the fixture trap on purpose, so the run must exit 1: %s", got)
	}
	// The POSITIVE control: the two non-trapping cases must have passed, or the
	// trap assertions below are being satisfied by a fixture that fell over
	// before it reached them.
	for _, want := range []string{
		"ok ", "the written-out impls agree on every non-trapping answer",
		"a Float divide by zero is IEEE and does not trap",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("the fixture did not get as far as its trapping cases (%q missing):\n%s",
				want, got.stdout)
		}
	}
	// Every fault row, by its TEXT rather than by its line, so the set is
	// derived from the report and the lines are read off it. The four impl rows
	// and their two operator twins are the whole trapping population; the impl
	// and operator forms of one fault share a text, which is the point — only
	// the LINE distinguishes them.
	faultTexts := []string{
		"integer overflow: 9223372036854775807 + 1",
		"division by zero",
		"integer overflow: -9223372036854775807 - 2",
		"integer overflow: 9223372036854775807 * 2",
	}
	var faultLines []int
	zeros := 0
	for _, line := range strings.Split(got.stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		matched := false
		for _, text := range faultTexts {
			if strings.HasSuffix(trimmed, text) {
				matched = true
				break
			}
		}
		if !matched || !strings.HasPrefix(trimmed, "line ") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(trimmed, "line %d:", &n); err != nil {
			t.Errorf("a fault row does not start with a line number: %q", trimmed)
			continue
		}
		if n == 0 {
			zeros++
		}
		faultLines = append(faultLines, n)
	}
	if len(faultLines) != 6 {
		t.Fatalf("want 6 fault rows (four impl spellings and two operator twins), got %d:\n%s",
			len(faultLines), got.stdout)
	}
	if zeros > 0 {
		t.Errorf("%d fault row(s) blame line 0, which is a position no file has.\n"+
			"The builder passes the call's own line through t.Line in "+
			"scalararith.go; a fault row at line 0 means that stopped.\nreport was:\n%s",
			zeros, got.stdout)
	}
	seen := map[int]bool{}
	for _, n := range faultLines {
		if seen[n] {
			t.Errorf("two fault rows blame line %d. Each of the six traps is on its own "+
				"source line, so a repeat means a position came from the wrong node — "+
				"the enclosing function, say, rather than the call.\nreport was:\n%s",
				n, got.stdout)
		}
		seen[n] = true
	}
}

// TestScalarArith_TheThirteenKeysStayUnbound is the SCOPE BOUNDARY, pinned
// rather than left as an absence.
//
// The written-out scalar operator impls lower through a BUILDER ARM and
// deliberately have no stdlibBindings row, so their std keys are still refused
// by the index and still exempted in TestStdlibRefusesUnmappedHostFnsByName.
// The thirteen are the Int, Float and Decimal operators plus String's `+`. None
// can be a registry row, because a row is a named rt symbol of exactly the Nomi
// arity and has nowhere to put the line a `/` fault must blame. Two things
// follow and both are checkable here:
//
//   - The chain POSITION is what makes the calls lower, so an edit that moved
//     scalarArithCall below stdlibCall would put every one of them straight back
//     onto `stdlib host function` with nothing else failing.
//   - stdKey instantiates these keys (`int.Int.Add<Int, Int>.add`), while a
//     bare spelling (`int.Int.add`) is the other candidate, so a future
//     registry row has to decide which it is keyed on. That is the expiry
//     condition scalararith.go's header names, and this test is where it fires.
//
// # THE CONTROL
//
// The host table contains no key with a `<` in it, so the registry cannot
// supply a same-shape positive. The control is taken from the STD INDEX
// instead: each of the thirteen keys must NAME A REAL DECLARATION in
// `stdlibLowering().byKey`, so a typo fails directly and the check does not
// depend on any binding existing.
func TestScalarArith_TheThirteenKeysStayUnbound(t *testing.T) {
	keys := []string{
		"int.Int.Add<Int, Int>.add",
		"int.Int.Subtract<Int, Int>.subtract",
		"int.Int.Multiply<Int, Int>.multiply",
		"int.Int.Divide<Int, Int>.divide",
		"float.Float.Add<Float, Float>.add",
		"float.Float.Subtract<Float, Float>.subtract",
		"float.Float.Multiply<Float, Float>.multiply",
		"float.Float.Divide<Float, Float>.divide",
		"strings.String.Add<String, String>.add",
		"decimal.Decimal.Add<Decimal, Decimal>.add",
		"decimal.Decimal.Subtract<Decimal, Decimal>.subtract",
		"decimal.Decimal.Multiply<Decimal, Decimal>.multiply",
		"decimal.Decimal.Divide<Decimal, Decimal>.divide",
	}
	for _, key := range keys {
		if _, bound := stdlibHostFuncs[key]; bound {
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
	if len(stdlibHostFuncs) == 0 {
		t.Fatal("stdlibHostFuncs is empty, so every absence asserted above is vacuous")
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

// TestScalarArith_TheSevenOwnerNamesAreReserved is the SAFETY ARGUMENT for
// selecting on an owner STRING, recorded as a fact about the language rather
// than as a claim about this package.
//
// scalarArithCall keys on seven names — `Add`, `Subtract`, `Multiply`, `Divide`
// as interfaces and `Int`, `Float`, `String` as types — and a name is the one
// thing a user could in principle collide with. The obvious hazard is a module
// declaring its own generic `interface Add<Rhs, Out>` whose `add` does something
// else: implCall's ifaceNamed arm resolves a LOWERABLE local interface before
// this arm is reached, but a NON-LOWERABLE local one with no `foreign` falls
// through it, so on that path a user's call could have been claimed here.
//
// IT CANNOT HAPPEN, and the reason is upstream of this package: all seven names
// are RESERVED BY THE FRONT END. Each redeclaration is rejected:
//
//	pub interface Add<Rhs, Out> { … }
//	  -> type name 'Add' is reserved by the language and cannot be redeclared
//	     as an interface; pick a different name (e.g. `MyAdd`)
//	pub type Int Bool
//	  -> type name 'Int' is reserved by the language and cannot be redeclared
//	     as a distinct type; …
//
// So this test is the REACHABILITY WITNESS for that argument, and its firing
// condition is precisely the event that would void the argument: the front end
// relaxing one of the seven. It does NOT assert anything about irbuild, which is
// the point — a guard inside irbuild could not see the change that breaks it.
//
// Related but NOT the same claim: scalarArithCall also declines a kindInvalid
// operand rather than claiming the call and suppressing, which is stricter than
// scalarEqualCall. That is about keeping the chain's existing report for a call
// whose operands refused, and it is checked by the corpus rather than here —
// nothing in the corpus reaches it, and inventing a fixture whose operand
// refuses would be asserting over a position the sweep already reads.
func TestScalarArith_TheSevenOwnerNamesAreReserved(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const tail = "\ntest \"t\" {\n  assert 1 == 1\n}\n"
	cases := []struct{ name, src string }{
		{"Add", "pub interface Add<Rhs, Out> {\n  fn m(lhs: self, rhs: Rhs): Out\n}\n" + tail},
		{"Subtract", "pub interface Subtract<Rhs, Out> {\n  fn m(lhs: self, rhs: Rhs): Out\n}\n" + tail},
		{"Multiply", "pub interface Multiply<Rhs, Out> {\n  fn m(lhs: self, rhs: Rhs): Out\n}\n" + tail},
		{"Divide", "pub interface Divide<Rhs, Out> {\n  fn m(lhs: self, rhs: Rhs): Out\n}\n" + tail},
		{"Int", "pub type Int Bool\n" + tail},
		{"Float", "pub type Float Bool\n" + tail},
		{"String", "pub type String Bool\n" + tail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "reserved_test.nomi")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Analyze(path)
			if err == nil {
				t.Fatalf("the front end ACCEPTED a redeclaration of %q. scalarArithCall keys "+
					"on that name, so a user's own declaration can now be claimed by it — "+
					"the arm must start asking whether the name resolves locally instead of "+
					"relying on the reservation. See scalararith.go.", tc.name)
			}
			if !strings.Contains(err.Error(), "reserved by the language") {
				t.Fatalf("%q was rejected for a DIFFERENT reason, so this row does not "+
					"test the reservation:\n%v", tc.name, err)
			}
		})
	}
}
