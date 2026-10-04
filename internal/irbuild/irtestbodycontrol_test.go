package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// irTestBodyVM lowers src as a `_test.nomi` file, requires exactly
// wantCases retained cases, and runs them in the VM. The report must equal the
// program's golden output in irbuild.expect (goldenReference), failing ones
// included.
// wantCases 0 skips the VM, for a source whose cases are not retained. It
// answers the golden report.
func irTestBodyVM(t *testing.T, src string, wantCases int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control_test.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	prevD := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prevD
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if wantCases > 0 && (module == nil || len(module.Tests()) != wantCases) {
		var names []string
		if module != nil {
			for _, c := range module.Tests() {
				names = append(names, c.Name())
			}
		}
		t.Fatalf("retained %q; want %d case(s) (declines: %v)", names, wantCases, declined)
	}
	interp := goldenReference(t, path)
	if wantCases > 0 {
		var out bytes.Buffer
		exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
		if reason != "" {
			t.Fatalf("the VM could not run the cases: %s", reason)
		}
		if out.String() != interp.stdout || exit != interp.exit {
			t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
				exit, out.String(), interp.exit, interp.stdout)
		}
	}
	return interp.stdout
}

// A Range operand's `values:` row is the structural rendering, not std's
// Debug `1..4`. The Debug form equals the operand text, so rt.RecordOperand
// would drop the row altogether. Debug.inspect of a Range keeps `1..4`.
func TestIRTestBody_RangeRowsAreStructural(t *testing.T) {
	const retained = `test "a range literal argument" {
  assert Iter.count(1..4) == 4
}

test "a bound range" {
  r = 1..=5
  assert Iter.count(r) == 4
}
`
	const native = `import std/io

test "two ranges compared" {
  assert 1..4 == 1..5
}

test "an unbounded range" {
  assert Iter.known_count(Range.from(3)) == Some(1)
}

test "a float interval" {
  assert Range.contains?(0.5..1.5, 2.0)
}

test "debug keeps the range syntax" {
  io.inspect(1..4)
  io.inspect([1..=2, 3..4])
  assert Debug.inspect(1..=3) == "1..=3"
}
`
	out := irTestBodyVM(t, retained, 2) + irTestBodyVM(t, native, 0)
	for _, want := range []string{
		"= Range{end: Some(4), inclusive: False, start: 1}",
		"= Range{end: Some(5), inclusive: True, start: 1}",
		"= Range{end: None, inclusive: False, start: 3}",
		"= Range{end: Some(1.5), inclusive: False, start: 0.5}",
		"1..4\n[1..=2, 3..4]\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

// `if` and `case` statements in test bodies, with assertions in their arms,
// and prelude and user enum constructors. Every case retains for the VM, and
// the VM prints the golden report — the failing
// cases included, so a row, a "defined as:" block or a line number that
// differed inside a branch would show.
const irTestBodyControlSource = `import std/io

enum Code {
  Num(Int)
  Word(String)
  Blank
}

fn parse(s: String): Result<Int, String> {
  case s {
    "one" -> Ok(1)
    _ -> Err("not a number: " + s)
  }
}

fn found(n: Int): Maybe<Int> {
  if n > 0 { Some(n) } else { None }
}

fn three(): Code {
  Code.Num(3)
}

test "an if statement asserts in its taken arm" {
  n = 4
  if n > 3 {
    assert n * 2 == 8
    io.print("then")
  } else {
    assert n == 0
    io.print("else")
  }
  io.print("after if")
}

test "a failing assertion inside an else arm" {
  n = 1
  if n > 3 {
    assert n == 4
  } else {
    doubled = n * 2
    matches = doubled == 3
    assert matches
  }
}

test "an if without else and an else-if chain" {
  n = 7
  if n == 7 {
    io.print("seven")
  }
  if n < 5 {
    io.print("small")
  } else if n < 10 {
    io.print("medium")
    assert n < 9
    io.print("checked")
  } else {
    io.print("large")
  }
}

test "a case statement over an Int" {
  n = 2
  case n {
    1 -> False
    2 -> {
      label = "two"
      assert label == "three"
    }
    _ -> True
  }
}

test "a case statement over a user enum" {
  c = Code.Word("hi")
  case c {
    Code.Num(n) -> {
      assert n == 3
      io.print("num")
    }
    Code.Word(w) -> {
      io.print(w)
      assert String.length(w) == 2
      io.print("word")
    }
    Code.Blank -> io.print("blank")
  }
  io.print("after case")
}

test "a case statement over Maybe and Result constructors" {
  case found(5) {
    Some(v) -> {
      assert v == 5
      io.print("some")
    }
    None -> io.print("none")
  }
  case parse("x") {
    Ok(v) -> assert v == 1
    Err(e) -> assert e == "not a number: y"
  }
}

test "constructors compared in assertions" {
  assert found(2) == Some(2)
  assert parse("one") == Ok(1)
  assert Code.Word(_) = three()
}

test "a pattern if statement and a pattern if value" {
  c = three()
  if Code.Num(n) = c {
    io.print(n)
  }
  got = if Code.Num(n) = c {
    n
  } else {
    0
  }
  assert got == 4
}

test "a case statement over a list" {
  xs = [1, 2, 3]
  case xs {
    [] -> io.print("empty")
    [h, ..t] -> {
      assert h == 1
      assert t == [3]
      io.print("unreached")
    }
  }
}

test "a case value binding" {
  n = 3
  word = case n {
    1 -> "one"
    3 -> "three"
    _ -> "many"
  }
  assert word == "tree"
}
`

func TestIRTestBody_ControlFlowAndConstructorsAgree(t *testing.T) {
	out := irTestBodyVM(t, irTestBodyControlSource, 10)
	for _, want := range []string{"then\nafter if", "seven\nmedium\nchecked", "defined as:", "hi\nword\nafter case",
		"Num(3)", "doubled", "not a number: x", "some\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

// irTestReportBlocks splits a `nomi test` transcript into each case's report
// block, keyed by the case's name: its `ok`/`FAIL` header and, for a failure,
// the indented lines under it.
func irTestReportBlocks(out string) map[string]string {
	blocks := map[string]string{}
	var name string
	var cur []string
	flush := func() {
		if name != "" {
			blocks[name] = strings.Join(cur, "\n")
		}
		name, cur = "", nil
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "FAIL ") {
			flush()
			if i := strings.Index(line, " :: "); i >= 0 {
				name = line[i+len(" :: "):]
			}
			cur = []string{line}
			continue
		}
		if name != "" && strings.HasPrefix(line, "  ") {
			cur = append(cur, line)
			continue
		}
		flush()
	}
	flush()
	return blocks
}

// TestIRTestBody_RetainedCasesAgree runs every corpus file's RETAINED
// cases in the VM and compares each case's report block with the golden
// record's block for the same case. A file whose cases are only partly
// retained cannot be compared whole, so this is the check that reaches the
// cases the report-shaped records cannot: a wrong failure message or a wrong
// pass in one retained case of a partly retained file.
//
// Printed output outside the report blocks is not compared, because a partial
// run cannot be aligned with the whole one.
func TestIRTestBody_RetainedCasesAgree(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs every corpus test file twice; -short")
	}
	_, files := corpusAnalysis(t)
	compared, limited := 0, 0
	for _, f := range files {
		if f.Res == nil {
			continue
		}
		var module *ir.Module
		for _, m := range f.Res.IR {
			if m.Name() == f.Path {
				module = m
			}
		}
		if module == nil || len(module.Tests()) == 0 {
			continue
		}
		if res, err := ffiPrepare(f.Path); err != nil || res != nil {
			// An FFI project registers its externs through the generated
			// wrapper, which this in-process run does not build.
			limited++
			t.Logf("LIMIT %s: an FFI project (%v)", f.Rel, err)
			continue
		}
		var out bytes.Buffer
		_, reason := vm.NewProgram(module, f.Res.IRModules(), &out).RunTests(f.Path)
		if reason != "" {
			limited++
			t.Logf("LIMIT %s: %s", f.Rel, reason)
			continue
		}
		// The golden report from corpus.expect, with the worktree root put
		// back: corpus reports spell their paths absolutely.
		golden, recorded := goldenRecord(t, "corpus", filepath.ToSlash(f.Rel))
		if !recorded {
			t.Errorf("%s has no record in corpus.expect", f.Rel)
			continue
		}
		stdout, _ := expectation.SplitTranscript(strings.ReplaceAll(golden.Transcript, "<root>", corpusGoldenRoot(t)))
		want := irTestReportBlocks(stdout)
		for name, got := range irTestReportBlocks(out.String()) {
			compared++
			if w, ok := want[name]; !ok || w != got {
				t.Errorf("%s :: %s: the VM's report differs from the golden record\nVM:\n%s\ngolden:\n%s",
					f.Rel, name, got, w)
			}
		}
	}
	if compared == 0 {
		t.Fatal("no retained case was compared, so this test checks nothing")
	}
	t.Logf("compared %d retained case(s); %d file(s) reached a machine limit", compared, limited)
}

// TestIRTestBody_WalkOnlyBoundaries pins what a walk-only test body still
// declines, each with its decline reason, so a boundary that starts retaining
// is widened deliberately.
func TestIRTestBody_WalkOnlyBoundaries(t *testing.T) {
	// `==` on two Vectors compares structurally (ir.Compare's container
	// shape, rt.Equal in the VM) and retains, and so does `==` over two
	// lists of a user distinct, whose literal backlog-types builds.
	const src = `type Id Int

test "two vectors compared with ==" {
  assert #[1, 2] == #[1, 2]
}

test "two lists of a distinct compared with ==" {
  assert [Id(1)] == [Id(1)]
}
`
	path := filepath.Join(t.TempDir(), "boundary_test.nomi")
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
	_, _, err = GenerateIR(p)
	IRDeclineObserved = prev
	if err != nil {
		t.Fatal(err)
	}
	for fn, want := range map[string]string{
		"test body: two vectors compared with ==":             "",
		"test body: two lists of a distinct compared with ==": "",
	} {
		if got := declined[fn]; got != want {
			t.Errorf("%s: first decline %q, want %q", fn, got, want)
		}
	}
}

// A walk-only body is built after the program's Go is assembled, and is still
// declared at its own place in the module's test table: between read-back
// cases, after a grouped case, and before a later walk-only one.
func TestIRTestBody_WalkOnlyCasesKeepDeclarationOrder(t *testing.T) {
	const src = `test "first, read back" {
  assert 1 + 1 == 2
}

test "second, walk-only" {
  x = 3
  if x > 2 { assert x == 3 } else { assert x == 0 }
}

tests "grouped" {
  setup {n: 5}

  test "third, grouped", {n} {
    assert n == 5
  }
}

test "fourth, read back" {
  assert "a" + "b" == "ab"
}

test "fifth, walk-only and failing" {
  y = 1
  case y {
    1 -> assert y == 2
    _ -> assert False
  }
}
`
	out := irTestBodyVM(t, src, 5)
	var order []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, ":: "); i >= 0 {
			order = append(order, line[i+3:])
		}
	}
	want := []string{"first, read back", "second, walk-only", "grouped / third, grouped",
		"fourth, read back", "fifth, walk-only and failing"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("report order %q; want %q\n%s", order, want, out)
	}
}
