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
	if got, want := out.String(), "arm\ndeferred\nhead 1\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
