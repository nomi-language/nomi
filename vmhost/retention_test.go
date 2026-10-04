package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A `try` in a `tests` group's setup ends each case the setup serves, as a
// `try` in the case's body does: an Err or None fails the case with the
// early-return report, and nothing of the case's body runs.
func TestTest_TryInSetupEndsTheCase(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `import std/io

fn parse(s: String): Result<Int, String> {
  case s {
    "" -> Err("empty")
    _ -> Ok(String.length(s))
  }
}

fn find(s: String): Maybe<Int> {
  case s {
    "" -> None
    _ -> Some(String.length(s))
  }
}

tests "an Err in setup" {
  setup {
    n = try parse("")
    {n: n}
  }

  test "fails", {n} {
    io.print("unreached ${n}")
    assert n > 0
  }
}

tests "a None in setup" {
  setup {
    n = try find("")
    {n: n}
  }

  test "fails", {n} {
    io.print("unreached ${n}")
    assert n > 0
  }
}

tests "an Ok in setup" {
  setup {
    n = try parse("abc")
    {n: n}
  }

  test "passes", {n} {
    io.print("n ${n}")
    assert n == 3
  }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cases := p.Cases(&out, vmhost.TestOptions{})
	if len(cases) != 3 {
		t.Fatalf("%d cases, want 3: %+v", len(cases), cases)
	}
	for i, c := range cases {
		if c.Blocked != nil {
			t.Fatalf("case %q is blocked: %v", c.Name, c.Blocked)
		}
		if fails := i < 2; (c.Err != nil) != fails {
			t.Fatalf("case %q: err %v, want failing=%v", c.Name, c.Err, fails)
		}
	}
	// Each report blames the setup's own try line.
	for i, want := range []string{"line 19: test returned early", "line 31: test returned early"} {
		if got := cases[i].Err.Error(); got != want {
			t.Fatalf("case %q reports %q, want %q", cases[i].Name, got, want)
		}
	}
	if got, want := out.String(), "n 3\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A group's setup and clock lines written after its tests run as they do
// written first: the setup runs before each test's body and its value is
// what the test binds.
func TestTest_GroupLinesAfterTheTestsRunFirst(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `import std/io

import std/testing.Clock

tests "lines after the tests" {
  test "reads setup", n {
    io.print("test ${n}")
    assert n == 2
  }

  setup {
    io.print("setup")
    2
  }
  clock Clock.Virtual
}
`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cases := p.Cases(&out, vmhost.TestOptions{})
	if len(cases) != 1 {
		t.Fatalf("%d cases, want 1: %+v", len(cases), cases)
	}
	if c := cases[0]; c.Blocked != nil || c.Err != nil {
		t.Fatalf("case %q: blocked %v, err %v", c.Name, c.Blocked, c.Err)
	}
	if got, want := out.String(), "setup\ntest 2\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A `try` in a nested fn leaves the nested fn, the boundary the checker
// records for it, in `main` as in any other body.
func TestRun_TryInANestedFnLeavesTheNestedFn(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `import std/io

fn parse(s: String): Result<Int, String> {
  case s {
    "" -> Err("empty")
    _ -> Ok(String.length(s))
  }
}

fn main() {
  fn doubled(s: String): Result<Int, String> {
    n = try parse(s)
    Ok(n * 2)
  }

  doubled("ab") |> io.inspect()
  doubled("") |> io.inspect()
}
`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := out.String(), "Ok(4)\nErr(\"empty\")\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// `Iter.sort_by` chooses its key's comparator as `Iter.sort` chooses its
// element's, and both take a std type's own compare, called from `main`.
func TestRun_SortByTakesEveryOrderedKey(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `import std/io
import std/duration.Duration

fn main() {
  Iter.sort_by([3, 1, 2], |n| [n]) |> io.inspect()
  Iter.sort_by([3, 1, 2], |n| Int.to_float(0 - n)) |> io.inspect()
  Iter.sort_by([1, 2, 3], |n| n != 2) |> io.inspect()
  Iter.sort_by([2, 1], |n| Duration.seconds(n)) |> io.inspect()
  Iter.sort([Duration.seconds(2), Duration.seconds(1)]) |> io.inspect()
}
`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := out.String(), "[1, 2, 3]\n[3, 2, 1]\n[2, 1, 3]\n[1, 2]\n[1s, 2s]\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
