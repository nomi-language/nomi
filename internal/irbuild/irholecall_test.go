package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const irHoleDisplaySource = `import std::io

fn main()
  io.print(Maybe.to_string(None))
  io.print(Maybe.to_string(Some(4)))
  io.print(Display.to_string(Err("x")))
  c = Ok(3)
  io.print("${c}")
end
`

// A type argument inference leaves open under a bound Unit lacks is closed
// at Int, which meets it (analysis's closed_vars.go): `Maybe.to_string(None)`
// demands Display of its T through the derived impl, as
// `Display.to_string(Err("x"))` and an interpolated `Ok(3)` do of the other
// side. So the instance is built at Int, the Some arm's
// `Display.to_string(payload)` dispatches to Int's impl, and the program
// runs; no instance holds a Unit that a bound would have to answer.
func TestIRHoleCall_ABoundedOpenTypeIsClosedAtAnImplementingType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(irHoleDisplaySource), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range res.IRModules() {
		for _, f := range m.Funcs() {
			names = append(names, f.Name())
		}
	}
	all := strings.Join(names, " ")
	if !strings.Contains(all, "Maybe.to_string<Int>") || strings.Contains(all, "Maybe.to_string<Unit>") {
		t.Fatalf("Maybe.to_string(None) is not instantiated at Int alone: %q", names)
	}
	verifyLambdaProgram(t, irHoleDisplaySource, "None\nSome(4)\nErr(x)\nOk(3)\n")
}

// And the front end is the other wall: a program whose T the checker SOLVES to
// a type that does not meet Hashable never reaches the builder. A type with
// equality meets it with
// no impl (spec §5 *Equality bounds*), so the type here is a function's.
func TestIRHoleCall_ASolvedTypeWithNoImplIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import std::io

fn main()
  x: Maybe<(Int) -> Int> = None
  io.print(Maybe.hash(x))
end
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Analyze(path)
	if err == nil {
		t.Fatal("the front end now ADMITS a Hashable call on a function type, so a checker-solved " +
			"type with no impl can reach the builder")
	}
	// The derived `impl Hashable for Maybe<T>` carries `where T: Hashable`,
	// and the bound check at the call names it.
	if !strings.Contains(err.Error(), "(Int) -> Int does not implement Hashable (required by `where T: Hashable`)") {
		t.Fatalf("rejected for another reason, so the wall is not the one this test names: %v", err)
	}
	// It points at the call, and says why a function cannot meet it.
	if !strings.Contains(err.Error(), "main.nomi:5:12:") || !strings.Contains(err.Error(), "a function value has no equality") {
		t.Fatalf("the bound error lost its position or its help: %v", err)
	}
}

const irHoleCompareSource = `import std::io

fn main()
  io.inspect(Iter.min([]))
  io.inspect(Iter.max([]))
  io.inspect(Iter.min([3, 1, 2]))
end
`

// `Iter.min([])` leaves T unconstrained: the callee makes no value of T for
// an empty source, so the checker admits it as it admits `List.head([])`
// (analysis/undetermined.go). T's `Comparable` bound refuses Unit, so the
// checker closes T at Int and the body's `item < m` is an ordering on Int.
func TestIRHoleCall_AComparableOpenTypeIsClosedAtInt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(irHoleCompareSource), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range res.IRModules() {
		for _, f := range m.Funcs() {
			names = append(names, f.Name())
		}
	}
	all := strings.Join(names, " ")
	if !strings.Contains(all, "min<Int>") || !strings.Contains(all, "max<Int>") || strings.Contains(all, "<Unit>") {
		t.Fatalf("Iter.min([]) and Iter.max([]) are not instantiated at Int: %q", names)
	}
	verifyLambdaProgram(t, irHoleCompareSource, "None\nNone\nSome(1)\n")
}

// The other wall: an element type the checker SOLVES to Unit is no hole,
// and Unit is not Comparable, so the call never reaches the builder.
func TestIRHoleCall_ASolvedUnitIsNotComparable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import std::io

fn main()
  xs: List<Unit> = []
  io.inspect(Iter.min(xs))
end
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Analyze(path)
	if err == nil {
		t.Fatal("the front end now ADMITS Iter.min over List<Unit>, so a checker-solved " +
			"Unit reaches the builder")
	}
	if !strings.Contains(err.Error(), "Unit does not implement Comparable (required by `where T: Comparable`)") {
		t.Fatalf("rejected for another reason, so the wall is not the one this test names: %v", err)
	}
}
