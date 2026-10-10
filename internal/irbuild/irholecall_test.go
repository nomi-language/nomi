package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

const irHoleHashSource = `import std/io

fn main() {
  io.print(Maybe.hash(None) == Maybe.hash(None))
  io.print(Maybe.hash(Some(4)) == Maybe.hash(Some(4)))
}
`

// `Maybe.hash(None)` leaves T unconstrained, so the builder instantiates the
// derived hash at a hole it fills with Unit. The Some arm's
// `Hashable.hash(payload)` is PRESENT as the unreachable fault naming the
// hole, and the program runs because no execution reaches it.
func TestIRHoleCall_HoleTypedBoundCallIsPresentButUnreached(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(irHoleHashSource), 0600); err != nil {
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
	var faults []string
	for _, m := range res.IRModules() {
		for _, f := range m.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					c, isCall := in.(*ir.Call)
					if isCall && c.Crosses() && c.Callee().Name() == irUnreachableHost {
						faults = append(faults, f.Name())
					}
				}
			}
		}
	}
	if len(faults) != 1 || !strings.Contains(faults[0], "Maybe.hash<Unit>") {
		t.Fatalf("unreachable faults in %q; want exactly one, in the Maybe.hash<Unit> instance", faults)
	}
	verifyLambdaProgram(t, irHoleHashSource, "True\nTrue\n")
}

// ONLY THE BUILDER'S OWN HOLES QUALIFY. irCheckerHoles reads a type parameter
// as a hole only where the checker's instantiated signature holds an
// unresolved variable; one the checker solved, to Unit included, is not a
// hole, so a bound call on it takes the ordinary route and declines when
// there is no impl.
func TestIRHoleCall_ASolvedTypeIsNeverAHole(t *testing.T) {
	fd := &ast.FuncDef{Name: "hash", Params: []ast.Param{{Name: "value",
		TypeAnnotation: &ast.GenericType{Name: "Maybe", Params: []ast.TypeExpr{&ast.SimpleType{Name: "T"}}}}}}
	maybeOf := func(arg analysis.Type) *analysis.FuncType {
		return &analysis.FuncType{Params: []analysis.Type{&analysis.EnumType{Name: "Maybe", TypeArgs: []analysis.Type{arg}}},
			Return: analysis.TypeInt}
	}
	tps, unit := []string{"T"}, []kind{kindUnit}
	if holes := irCheckerHoles(fd, tps, unit, maybeOf(&analysis.TypeVar{})); !holes["T"] {
		t.Fatalf("an unresolved T filled with Unit is not a hole: %v", holes)
	}
	if holes := irCheckerHoles(fd, tps, unit, maybeOf(analysis.TypeUnit)); len(holes) != 0 {
		t.Fatalf("a T the checker solved to Unit reads as a hole: %v", holes)
	}
	if holes := irCheckerHoles(fd, tps, []kind{kindInt}, maybeOf(&analysis.TypeVar{})); len(holes) != 0 {
		t.Fatalf("a T the builder did not fill with Unit reads as a hole: %v", holes)
	}
}

// And the front end is the other wall: a program whose T the checker SOLVES to
// a type with no Hashable impl never reaches the builder, so nothing it
// solved can become an unreachable fault.
func TestIRHoleCall_ASolvedTypeWithNoImplIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import std/io

fn main() {
  x: Maybe<Unit> = None
  io.print(Maybe.hash(x))
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Analyze(path)
	if err == nil {
		t.Fatal("the front end now ADMITS a Hashable call on Unit, so a checker-solved " +
			"type with no impl can reach the builder: holeBoundCall must still decline it")
	}
	// The derived `impl Hashable for Maybe<T>` carries `where T: Hashable`,
	// and the bound check at the call names it.
	if !strings.Contains(err.Error(), "Unit does not implement Hashable (required by `where T: Hashable`)") {
		t.Fatalf("rejected for another reason, so the wall is not the one this test names: %v", err)
	}
	// It points at the call, and, since neither `Unit` nor `Hashable` is the
	// program's to extend, says only std can add the impl.
	if !strings.Contains(err.Error(), "main.nomi:5:12:") || !strings.Contains(err.Error(), "only std can implement `Hashable` for `Unit`") {
		t.Fatalf("the bound error lost its position or its help: %v", err)
	}
}

const irHoleCompareSource = `import std/io

fn main() {
  io.inspect(Iter.min([]))
  io.inspect(Iter.max([]))
  io.inspect(Iter.min([3, 1, 2]))
}
`

// `Iter.min([])` leaves T unconstrained: the callee makes no value of T for
// an empty source, so the checker admits it as it admits `List.head([])`
// (analysis/undetermined.go), and the builder instantiates `Iter.min` at a
// hole it fills with Unit. The body's `item < m` is an ordering on the hole,
// which Unit cannot answer; holeCompare lowers it to the unreachable fault
// naming the hole, and the program runs because no execution reaches it.
func TestIRHoleCall_HoleTypedComparisonIsPresentButUnreached(t *testing.T) {
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
	// The comparison is in the reduce callback, a lambda whose body is its
	// own Func under the instance's FuncValue; a fault there counts for the
	// instance.
	var faults []string
	var walk func(top string, f *ir.Func)
	walk = func(top string, f *ir.Func) {
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				if fv, isLambda := in.(*ir.FuncValue); isLambda {
					walk(top, fv.Body())
				}
				c, isCall := in.(*ir.Call)
				if isCall && c.Crosses() && c.Callee().Name() == irUnreachableHost {
					faults = append(faults, top)
				}
			}
		}
	}
	for _, m := range res.IRModules() {
		for _, f := range m.Funcs() {
			walk(f.Name(), f)
		}
	}
	if len(faults) != 2 || !strings.Contains(strings.Join(faults, " "), "min<Unit>") ||
		!strings.Contains(strings.Join(faults, " "), "max<Unit>") {
		t.Fatalf("unreachable faults in %q; want one in each of the Iter.min<Unit> and Iter.max<Unit> instances", faults)
	}
	verifyLambdaProgram(t, irHoleCompareSource, "None\nNone\nSome(1)\n")
}

// The other wall: an element type the checker SOLVES to Unit is no hole,
// and Unit is not Comparable, so the call never reaches the builder.
func TestIRHoleCall_ASolvedUnitIsNotComparable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import std/io

fn main() {
  xs: List<Unit> = []
  io.inspect(Iter.min(xs))
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Analyze(path)
	if err == nil {
		t.Fatal("the front end now ADMITS Iter.min over List<Unit>, so a checker-solved " +
			"Unit reaches the builder: holeCompare must still decline it")
	}
	if !strings.Contains(err.Error(), "Unit does not implement Comparable (required by `where T: Comparable`)") {
		t.Fatalf("rejected for another reason, so the wall is not the one this test names: %v", err)
	}
}
