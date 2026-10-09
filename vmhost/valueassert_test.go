package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// An assertion stands as a statement or as a binding's value, and its value
// is the judged subject (spec §36): True for `assert` of a Bool, False for
// `refute`, and the subject itself for a Maybe or Result. In an ordinary
// `fn` a failure is the function's Err(AssertionFailure). Each form here
// ran into a lowering gap before; the parameter subject (`assert x`) did too.
func TestRun_AnAssertionAsAValueInAFunction(t *testing.T) {
	out := runSourceOutput(t, `import std/assertions.AssertionFailure

fn stmt(x: Bool): Result<Int, AssertionFailure> {
  assert x
  Ok(1)
}

fn bind(x: Bool): Result<Bool, AssertionFailure> {
  ok = assert x
  Ok(ok)
}

fn typed(x: Bool): Result<Bool, AssertionFailure> {
  ok: Bool = refute x
  Ok(ok)
}

fn wild(x: Bool): Result<Int, AssertionFailure> {
  _ = assert x
  Ok(2)
}

fn some(m: Maybe<Int>): Result<Int, AssertionFailure> {
  Some(n) = assert m else {
    return Ok(0)
  }
  Ok(n)
}

fn blk(x: Bool): Result<Bool, AssertionFailure> {
  ok = {
    assert x
  }
  Ok(ok)
}

fn iff(c: Bool, x: Bool): Result<Bool, AssertionFailure> {
  ok = if c {
    assert x
  } else {
    False
  }
  Ok(ok)
}

fn cas(c: Bool, x: Bool): Result<Bool, AssertionFailure> {
  ok = case c {
    True -> assert x
    False -> False
  }
  Ok(ok)
}

fn tail(r: Result<Int, AssertionFailure>): Result<Int, AssertionFailure> {
  assert r
}

fn show<T>(r: Result<T, AssertionFailure>): String {
  case r {
    Ok(_) -> "Ok"
    Err(f) -> "Err(line ${Int.to_string(f.line)})"
  }
}

fn main() {
  dbg(stmt(True))
  dbg(show(stmt(False)))
  dbg(bind(True))
  dbg(show(bind(False)))
  dbg(typed(False))
  dbg(show(typed(True)))
  dbg(wild(True))
  dbg(show(wild(False)))
  dbg(some(Some(3)))
  dbg(show(some(None)))
  dbg(blk(True))
  dbg(show(blk(False)))
  dbg(iff(True, True))
  dbg(iff(False, False))
  dbg(show(iff(True, False)))
  dbg(cas(True, True))
  dbg(show(cas(True, False)))
  dbg(tail(Ok(4)))
}
`)
	want := `dbg line 66: stmt(True) = Ok(1)
dbg line 67: show(stmt(False)) = "Err(line 4)"
dbg line 68: bind(True) = Ok(True)
dbg line 69: show(bind(False)) = "Err(line 9)"
dbg line 70: typed(False) = Ok(False)
dbg line 71: show(typed(True)) = "Err(line 14)"
dbg line 72: wild(True) = Ok(2)
dbg line 73: show(wild(False)) = "Err(line 19)"
dbg line 74: some(Some(3)) = Ok(3)
dbg line 75: show(some(None)) = "Err(line 24)"
dbg line 76: blk(True) = Ok(True)
dbg line 77: show(blk(False)) = "Err(line 32)"
dbg line 78: iff(True, True) = Ok(True)
dbg line 79: iff(False, False) = Ok(False)
dbg line 80: show(iff(True, False)) = "Err(line 39)"
dbg line 81: cas(True, True) = Ok(True)
dbg line 82: show(cas(True, False)) = "Err(line 48)"
dbg line 83: tail(Ok(4)) = Ok(4)
`
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// A failed assertion bound in `main` ends the program with its report.
func TestRun_AFailedAssertionBoundInMainEndsTheProgram(t *testing.T) {
	p, err := vmhost.LoadSource("main.nomi", `import std/assertions.AssertionFailure

fn main(): Result<Unit, AssertionFailure> {
  n = 0
  ok = assert n > 1
  dbg(ok)
  Ok(Unit)
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	if err == nil {
		t.Fatalf("the program passed; output:\n%s", out.String())
	}
	if out.Len() != 0 {
		t.Fatalf("the program ran past the failed assertion:\n%s", out.String())
	}
	for _, want := range []string{"line 5: assertion failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the report lacks %q:\n%v", want, err)
		}
	}
}

// The same forms in a test body: each passing one binds the subject, and
// each failing one fails its case with the assertion's report.
func TestTest_AnAssertionAsAValueInATestBody(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `fn flag(b: Bool): Bool {
  b
}

once ready = flag(True)

test "discarded" {
  _ = assert flag(True)
  _ = assert flag(False)
}

test "annotated" {
  o: Bool = refute flag(False)
  r: Result<Int, String> = assert Ok(2)
  assert o == False
  assert r == Ok(2)
}

test "destructured" {
  Some(n) = assert Some(1) else {
    return
  }
  assert n == 1
  none: Maybe<Int> = None
  Some(_m) = assert none else {
    return
  }
}

test "block tail" {
  v = {
    assert flag(True)
  }
  assert v
}

test "if arm" {
  w = if flag(True) {
    assert flag(False)
  } else {
    False
  }
  assert w
}

test "case arm" {
  w = case flag(True) {
    True -> refute flag(True)
    False -> False
  }
  refute w
}

test "leading in an arm" {
  w = if flag(True) {
    assert flag(True)
    7
  } else {
    0
  }
  assert w == 7
}

test "bare once" {
  assert ready
}
`))
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out bytes.Buffer
	cases := p.Cases(&out, vmhost.TestOptions{})
	fails := map[string]string{
		"discarded":    "line 9: assertion failed",
		"destructured": "line 25: assertion failed",
		"if arm":       "line 39: assertion failed",
		"case arm":     "line 48: refute failed",
	}
	if len(cases) != 8 {
		t.Fatalf("%d cases, want 8: %+v", len(cases), cases)
	}
	for _, c := range cases {
		if c.Blocked != nil {
			t.Fatalf("case %q is blocked: %v", c.Name, c.Blocked)
		}
		want, failing := fails[c.Name]
		if !failing {
			if c.Err != nil {
				t.Fatalf("case %q failed: %v", c.Name, c.Err)
			}
			continue
		}
		if c.Err == nil {
			t.Fatalf("case %q passed, want a failure", c.Name)
		}
		if !strings.Contains(c.Err.Error(), want) {
			t.Fatalf("case %q: report\n%v\nwant it to contain\n%s", c.Name, c.Err, want)
		}
	}
}
