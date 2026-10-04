package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// irShapesRun lowers src as a `_test.nomi` file, requires every case in want
// to be retained, runs the cases on the VM and answers the report with the
// file's path spelled `<path>`, and the exit status.
func irShapesRun(t *testing.T, src string, want []string) (string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shapes_test.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	prev := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prev
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil {
		t.Fatalf("no module for %s", path)
	}
	retained := map[string]bool{}
	for _, c := range module.Tests() {
		retained[c.Name()] = true
	}
	for _, name := range want {
		if !retained[name] {
			t.Errorf("case %q is not retained (declines: %v)", name, declined)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	return strings.ReplaceAll(out.String(), path, "<path>"), exit
}

// Statement shapes a test body retains for the VM: an assertion's value bound
// to a name, a tuple destructure, a rebinding whose `defined as:` block
// follows the latest binding, a block-valued binding and a single-branch
// `case`. The failing reports match the recorded ones, byte for byte.
func TestIRTestBody_StatementShapes(t *testing.T) {
	const src = `enum Shape {
  Circle(Int)
}

test "a rebound name's report follows the latest binding" {
  flag = True
  assert flag
  flag = 1 > 2
  assert flag
}

test "an assertion's value is its subject" {
  truth = assert True
  falsity = refute False
  refute truth == falsity
  wrong = refute 3 > 2
  assert wrong
}

test "tuple destructuring in a test body" {
  (a, b) = (3, 4)
  assert a * b == 12
}

test "a rebinding may change the kind" {
  v = 1
  assert v == 1
  v = "one"
  assert v == "one"
}

test "a block-valued binding" {
  total = {
    x = 2
    y = x * 10
    x + y
  }
  assert total == 22
}

test "a single-branch case" {
  n = case Shape.Circle(3) {
    Shape.Circle(r) -> r * 2
  }
  assert n == 6
}
`
	got, exit := irShapesRun(t, src, []string{
		"a rebound name's report follows the latest binding",
		"an assertion's value is its subject",
		"tuple destructuring in a test body",
		"a rebinding may change the kind",
		"a block-valued binding",
		"a single-branch case",
	})
	const want = `FAIL <path> :: a rebound name's report follows the latest binding
  line 9: assertion failed
    assert flag
    defined as:
      1 > 2
FAIL <path> :: an assertion's value is its subject
  line 16: refute failed
    refute 3 > 2
ok <path> :: tuple destructuring in a test body
ok <path> :: a rebinding may change the kind
ok <path> :: a block-valued binding
ok <path> :: a single-branch case
`
	if !strings.HasPrefix(got, want) || exit == 0 {
		t.Errorf("the VM's report (exit %d):\n%s\nwant it to begin:\n%s", exit, got, want)
	}
}

// A rebinding beside a lambda runs: a closure captures the value a name held
// when it was made, so a later rebinding is a fresh binding it never sees.
func TestIRTestBody_RebindingBesideALambdaRuns(t *testing.T) {
	const src = `test "a rebinding after a lambda" {
  x = 1
  seen = || x
  f = |y: Int| y + 1
  x = f(x)
  assert x == 2
  assert seen() == 1
}
`
	got, exit := irShapesRun(t, src, []string{"a rebinding after a lambda"})
	if want := "ok <path> :: a rebinding after a lambda\n"; !strings.HasPrefix(got, want) || exit != 0 {
		t.Errorf("the VM's report (exit %d):\n%s\nwant it to begin:\n%s", exit, got, want)
	}
}

// Iter.sort over a stdlib element type with no local Comparable impl takes
// std's retained Comparable.compare, as Int and String elements do.
func TestIRIterSort_StdElementTakesStdComparator(t *testing.T) {
	const src = `import std/calendar.Time

test "std elements sort by std's comparator" {
  assert Ok(a) = Time"09:30:00"
  assert Ok(b) = Time"08:15:00"
  sorted = Iter.sort([a, b])
  assert Debug.inspect(sorted) == "[08:15:00, 09:30:00]"
}
`
	got, exit := irShapesRun(t, src, []string{"std elements sort by std's comparator"})
	if want := "ok <path> :: std elements sort by std's comparator\n"; !strings.HasPrefix(got, want) || exit != 0 {
		t.Errorf("the VM's report (exit %d):\n%s\nwant it to begin:\n%s", exit, got, want)
	}
}

// A `fn` declared inside a body is a closure over the names before it, bound
// for the statements after it; its own body may rebind its parameters, and a
// `return` leaves the nested function only.
func TestIRNestedFn_IsAClosureBoundByName(t *testing.T) {
	const src = `fn outer(n: Int): Int {
  fn clamp(x: Int): Int {
    if x > n { return n }
    x
  }

  clamp(3) + clamp(100)
}

test "a nested fn rebinds its parameter" {
  fn double_then_inc(x: Int): Int {
    x = x + x
    x = x + 1
    x
  }

  assert double_then_inc(10) == 21
}

test "a nested fn captures and returns early" {
  limit = 5
  fn clamp(x: Int): Int {
    if x > limit { return limit }
    x
  }

  assert clamp(2) + clamp(9) == 7
  assert outer(10) == 13
}
`
	got, exit := irShapesRun(t, src, []string{
		"a nested fn rebinds its parameter",
		"a nested fn captures and returns early",
	})
	const want = `ok <path> :: a nested fn rebinds its parameter
ok <path> :: a nested fn captures and returns early
`
	if !strings.HasPrefix(got, want) || exit != 0 {
		t.Errorf("the VM's report (exit %d):\n%s\nwant it to begin:\n%s", exit, got, want)
	}
}

// A nested fn that names itself runs: the closure binds its own name, to a
// closure over the same body and the same captured values. A lambda inside
// the body captures that name like any other.
func TestIRNestedFn_RecursionRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  fn fact(n: Int): Int {
    if n <= 1 { return 1 }
    n * fact(n - 1)
  }

  step = 2
  fn down(n: Int): Int {
    if n <= 0 { return 0 }
    again = |m: Int| down(m)
    1 + again(n - step)
  }

  step = 100
  io.print(fact(5))
  io.print(down(7))
  io.print(step)
}
`, "120\n4\n100\n")
}

// A recursive nested fn's self reference is its closure's first capture,
// filled with the closure itself; its body builds no second closure over
// itself.
func TestIRNestedFn_RecursionIsASelfCapture(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", "import std/io\nfn main() {\n  fn fact(n: Int): Int {\n    if n <= 1 { return 1 }\n    n * fact(n - 1)\n  }\n\n  io.print(fact(5))\n}\n")
	if err != nil {
		t.Fatalf("the front end now rejects this, so nothing below is tested: %v", err)
	}
	got, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, mod := range got.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() != "main" {
				continue
			}
			for _, b := range fn.Blocks() {
				for _, in := range b.Instrs() {
					fv, ok := in.(*ir.FuncValue)
					if !ok {
						continue
					}
					found++
					if !fv.Self() || fv.Capture(0) != ir.NoTemp {
						t.Errorf("%s: want a self capture first", fv)
					}
					for _, bb := range fv.Body().Blocks() {
						for _, inner := range bb.Instrs() {
							if n, ok := inner.(*ir.FuncValue); ok && n.Body() == fv.Body() {
								t.Errorf("the body builds a closure over itself: %s", n)
							}
						}
					}
				}
			}
		}
	}
	if found != 1 {
		t.Fatalf("main builds %d closures; want 1 (is main retained?)", found)
	}
}

// An `if` without `else` is Unit whichever way it goes, and Unit `==` answers
// after both operands run; List.head over struct elements is a cell
// operation.
func TestIRTestBody_UnitIfAndStructListHead(t *testing.T) {
	const src = `struct P {
  x: Int
}

test "an if without else is Unit" {
  a = if True { Unit }
  b = if False { Unit }
  assert a == Unit
  assert b == Unit
  refute a != b
}

test "List.head over struct elements" {
  assert List.head([P{x: 1}, P{x: 2}]) == Some(P{x: 1})
}
`
	got, exit := irShapesRun(t, src, []string{"an if without else is Unit", "List.head over struct elements"})
	const want = `ok <path> :: an if without else is Unit
ok <path> :: List.head over struct elements
`
	if !strings.HasPrefix(got, want) || exit != 0 {
		t.Errorf("the VM's report (exit %d):\n%s\nwant it to begin:\n%s", exit, got, want)
	}
}
