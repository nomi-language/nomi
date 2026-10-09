package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A pattern `if` whose pattern is one of Bool's variants runs, as the same
// test written as a `case` arm does: with and without `else`, as a value and
// as a statement, spelled bare, `.True` and `Bool.False`.
func TestRun_BoolVariantPatternIfRuns(t *testing.T) {
	out, err := runVM(t, `import std/io

fn f(y: Bool): String {
  if False = y { "f" } else { "t" }
}

fn g(y: Bool): Int {
  if True = y {
    io.print("yes")
  }
  1
}

fn h(y: Bool): String {
  if .True = y { "T" } else { "F" }
}

fn k(y: Bool): String {
  if Bool.False = y { "F" } else { "T" }
}

fn main() {
  io.print(f(False))
  io.print(f(True))
  _ = g(True)
  _ = g(False)
  io.print(h(True))
  io.print(h(False))
  io.print(k(True))
  io.print(k(False))
}
`)
	want := "f\nt\nyes\nT\nF\nT\nF\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// The same tests in a test body, with an `as` name and a bound singleton.
func TestRun_BoolVariantPatternIfInATestBodyRuns(t *testing.T) {
	const name = "main_test.nomi"
	p, err := vmhost.LoadSource(name, `import std/io

test "bool pattern if" {
  y = False
  if True = y {
    io.print("no")
  }
  s = if False = y { "f" } else { "t" }
  assert s == "f"
  if Bool.True = True {
    io.print("yes")
  } else {
    io.print("no")
  }
  if False as b = y {
    io.print("as ${b}")
  }
  if False(m) = y {
    io.print("bound ${Display.to_string(m)}")
  }
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the test: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if !strings.HasPrefix(out, "yes\nas False\nbound False\n") || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("want the three lines and one passing case:\n%s", out)
	}
}
