package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// `return` in a test body ends the case (spec §36): its pending deferred calls
// run, nothing after it runs, and a returned value is the body's final value,
// so a returned failed `testing.check` fails the case.
func TestTest_ReturnEndsTheCase(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `import std/io
import std/testing

fn note(s: String) {
  io.print(s)
}

test "bare return from an arm" {
  defer note("deferred")
  x = 1
  if x == 1 {
    io.print("arm")
    return
  }
  io.print("unreached")
  assert x == 2
}

test "return from a case arm" {
  case [1, 2] {
    [a, .._] -> {
      io.print("head ${a}")
      return
    }
    [] -> io.print("empty")
  }
  assert False
}

test "return a passing check" {
  assert 2 == 2
  return testing.check(1 == 1)
}

test "return a failing check" {
  total = 3
  assert total == 3
  return testing.check(total == 10)
}

test "return a discarded value" {
  assert 2 == 2
  return 5
}
`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cases := p.Cases(&out, vmhost.TestOptions{})
	if len(cases) != 5 {
		t.Fatalf("%d cases, want 5: %+v", len(cases), cases)
	}
	for i, c := range cases {
		if c.Blocked != nil {
			t.Fatalf("case %q is blocked: %v", c.Name, c.Blocked)
		}
		fails := i == 3
		if (c.Err != nil) != fails {
			t.Fatalf("case %q: err %v, want failing=%v", c.Name, c.Err, fails)
		}
	}
	if !strings.Contains(cases[3].Err.Error(), "line 38: check failed") {
		t.Fatalf("the failing check reports %q", cases[3].Err)
	}
	if out.Len() != 0 {
		t.Fatalf("output outside every case: %q", out.String())
	}
	if got, want := caseOutput(cases), "arm\ndeferred\nhead 1\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A `return` that ends an arm of a region in value position in a test body, a
// binding's `case` or `if`, ends the case as a `return` statement does: the
// deferred calls pending there run, the rest of the body does not, and a
// returned failed check fails the case. The other arms bind as usual.
func TestTest_ReturnFromAValueArmEndsTheCase(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `import std/io
import std/testing

fn note(s: String) {
  io.print(s)
}

fn get(ok: Bool) -> Result<String, String> {
  if ok { Ok("x") } else { Err("no") }
}

test "an Err arm returns before a defer" {
  dir = case get(False) {
    Ok(d) -> d
    Err(e) -> return Err(e)
  }

  defer note(dir)
  assert False
}

test "an Ok arm binds and the defer runs" {
  defer note("first")
  dir = case get(True) {
    Ok(d) -> d
    Err(e) -> return Err(e)
  }

  defer note(dir)
  assert dir == "x"
}

test "a bare return arm after a defer" {
  defer note("pending")
  n = case get(False) {
    Ok(_) -> 1
    Err(_) -> return
  }

  assert n == 2
}

test "an if arm returns a failing check" {
  total = 3
  n = if total > 5 { total } else { return testing.check(total == 10) }
  assert n == 0
}
`))
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	var out bytes.Buffer
	cases := p.Cases(&out, vmhost.TestOptions{})
	if len(cases) != 4 {
		t.Fatalf("%d cases, want 4: %+v", len(cases), cases)
	}
	for i, c := range cases {
		if c.Blocked != nil {
			t.Fatalf("case %q is blocked: %v", c.Name, c.Blocked)
		}
		fails := i == 3
		if (c.Err != nil) != fails {
			t.Fatalf("case %q: err %v, want failing=%v", c.Name, c.Err, fails)
		}
	}
	if !strings.Contains(cases[3].Err.Error(), "check failed") {
		t.Fatalf("the failing check reports %q", cases[3].Err)
	}
	if got, want := caseOutput(cases), "x\nfirst\npending\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
