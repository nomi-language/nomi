package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// `assert pattern = value` and bare-name subjects' "defined as:" blocks,
// retained for the VM and compared against the golden record of `nomi test`,
// failing reports included.

// irPatternAssertVM lowers src as `<name>_test.nomi`, requires wantCases
// retained cases and runs them in the VM; the output must equal the golden
// output in irbuild.expect (goldenReference). It answers the VM's transcript.
func irPatternAssertVM(t *testing.T, src string, wantCases int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pattern_test.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
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
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil || len(module.Tests()) != wantCases {
		var names []string
		if module != nil {
			for _, c := range module.Tests() {
				names = append(names, c.Name())
			}
		}
		t.Fatalf("retained %q; want %d case(s)", names, wantCases)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	interp := goldenReference(t, path)
	if out.String() != interp.stdout || exit != interp.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			exit, out.String(), interp.exit, interp.stdout)
	}
	return out.String()
}

const irPatternAssertUngroupedSource = `fn compute(): Int {
  7
}

fn label(): String {
  "ok"
}

fn parse(s: String): Result<Int, String> {
  Err("not a number: " + s)
}

fn found(n: Int): Maybe<Int> {
  Some(n)
}

enum Shape {
  Circle Float
  Square Int
  Dot
}

fn square(n: Int): Shape {
  Shape.Square(n)
}

test "an identifier pattern binds into the enclosing scope" {
  assert n = compute()
  assert n == 7
  assert n * 2 == 14
}

test "literal patterns that match bind nothing and hold" {
  assert 7 = compute()
  assert "ok" = label()
}

test "a literal pattern that does not match reports the value it saw" {
  assert 3 = compute()
}

test "a String that does not match shows its quotes" {
  assert "no" = label()
}

test "a prelude pattern that matches binds its payload" {
  assert Some(v) = found(4)
  assert v == 4
}

test "a prelude pattern that does not match shows the value it saw" {
  assert Ok(d) = parse("x")
  assert d == 1
}

test "a bare-name value shows its definition" {
  parsed = parse("y")
  assert Ok(d) = parsed
  assert d == 1
}

test "a user enum pattern with a literal payload" {
  shape = square(3)
  assert Shape.Square(4) = shape
}

test "a bare-name Bool subject shows its definition" {
  holds = 3 > 4
  assert holds
}

test "a refuted bare name shows its definition" {
  holds = 3 < 4
  refute holds
}

test "a passing bare name" {
  holds = 3 < 4
  assert holds
}
`

// Ungrouped cases: every pattern shape, passing and failing, and bare-name
// Bool subjects with their "defined as:" blocks.
func TestIRPatternAssert_UngroupedCasesAgree(t *testing.T) {
	out := irPatternAssertVM(t, irPatternAssertUngroupedSource, 11)
	for _, want := range []string{"pattern did not match", "defined as:", "parse(\"y\")",
		"3 > 4", "square(3)", "\"ok\""} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

const irPatternAssertGroupedSource = `import {
  std/duration.Duration
  std/instant.Instant
  std/tasks.Task
  std/testing.Clock
  std/timer
}

fn long_sleep(): Unit {
  timer.sleep(Duration.seconds(5))
}

fn short_fail(): Result<Int, String> {
  Err("boom")
}

fn pair_of(): (Int, String) {
  (1, "one")
}

tests "sibling cancellation under a virtual clock" {
  clock Clock.Virtual

  test "a short-circuit cancels its siblings instead of waiting for them" {
    before = Instant.now()

    outcome = concurrent {
      short = Task.spawn(|| short_fail())
      long = Task.spawn(|| long_sleep())
      _l = long
      Ok(try Task.await(short))
    }

    elapsed = Instant.to_seconds(Instant.now()) - Instant.to_seconds(before)

    assert Err(e) = outcome
    assert e == "boom"
    assert elapsed == 0
  }

  test "a failing pattern over a concurrent block shows its definition" {
    outcome = concurrent {
      short = Task.spawn(|| short_fail())
      Ok(try Task.await(short))
    }

    assert Ok(n) = outcome
    assert n == 1
  }

  test "a tuple pattern binds both parts" {
    pair = pair_of()
    assert (n, word) = pair
    assert n == 1
    assert word == "one"
  }

  test "a list pattern that does not match shows the whole list" {
    assert [a, b] = ["one"]
    assert a == b
  }
}
`

// concurrent_runtime_test.nomi's virtual-clock shape, plus tuple and list
// patterns and a failing pattern whose value is a concurrent block's result.
func TestIRPatternAssert_GroupedCasesAgree(t *testing.T) {
	out := irPatternAssertVM(t, irPatternAssertGroupedSource, 4)
	for _, want := range []string{"outcome", "concurrent {", "[\"one\"]", "2 passed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

// A bare name bound by a pipe carries `pipeline values:` rows. The builder
// records one stage per prefix, so that case is retained for the VM too, and
// retaining it moves no Source.
func TestIRPatternAssert_APipeBindingCarriesItsStages(t *testing.T) {
	const src = `fn parse(s: String): Result<Int, String> {
  Err("not a number: " + s)
}

fn one(): Result<Int, String> {
  Ok(1)
}

test "retained" {
  assert Ok(_) = one()
}

test "a pipe binding" {
  r = "x" |> parse()
  assert Ok(_) = r
}
`
	path := filepath.Join(t.TempDir(), "pipe_test.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
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
	for _, m := range res.IR {
		if m.Name() == path {
			for _, c := range m.Tests() {
				names = append(names, c.Name())
			}
		}
	}
	if !reflect.DeepEqual(names, []string{"retained", "a pipe binding"}) {
		t.Fatalf("retained %q; want both cases", names)
	}
}
