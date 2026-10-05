package irbuild

import (
	"strings"
	"testing"
)

// A body whose last statement is a destructuring binding answers Unit, as a
// body ending in any binding does: the value is evaluated and matched, and
// nothing is returned. A tuple, a struct pattern and a named struct pattern,
// each with `_` parts, end a function, an `if` arm, a block statement, a
// lambda and `main` itself.
func TestIRDestructureTail_EndsABodyWithUnit(t *testing.T) {
	const src = `import std/io

struct Point {
  x: Int
  y: Int
}

fn make(n: Int): (Int, Int) {
  io.print("make ${n}")
  (n, n + 1)
}

fn point(): Point {
  io.print("point")
  Point{x: 1, y: 2}
}

fn tuple_tail() {
  (_, _) = make(20)
}

fn struct_tail() {
  {x: _, y: _} = point()
}

fn named_struct_tail() {
  Point{x: _, y: _} = point()
}

fn arm_tail(flag: Bool) {
  if flag {
    (_, _) = make(1)
  } else {
    (_, _) = make(2)
  }
}

fn block_tail() {
  {
    io.print("in block")
    (_, _) = make(3)
  }
}

fn main() {
  tuple_tail()
  struct_tail()
  named_struct_tail()
  arm_tail(True)
  arm_tail(False)
  block_tail()
  f = || {
    (_, _) = make(4)
  }
  f()
  (_, _) = make(5)
}
`
	names, _ := irRetainedNames(t, src)
	for _, name := range []string{"main", "tuple_tail", "struct_tail", "named_struct_tail", "arm_tail", "block_tail"} {
		if !names[name] {
			t.Errorf("%s was not retained", name)
		}
	}
	verifyLambdaProgram(t, src, "make 20\npoint\npoint\nmake 1\nmake 2\nin block\nmake 3\nmake 4\nmake 5\n")
}

// A test body ending in a destructure runs it and passes.
func TestIRDestructureTail_EndsATestBody(t *testing.T) {
	const src = `import std/io

fn make(n: Int): (Int, Int) {
  io.print("make ${n}")
  (n, n + 1)
}

test "a test body ending in a destructure" {
  assert True
  (_, _) = make(6)
}
`
	out, exit := irShapesRun(t, src, []string{"a test body ending in a destructure"})
	if exit != 0 || !strings.HasPrefix(out, "make 6\n") || !strings.Contains(out, "1 passed, 0 failed") {
		t.Errorf("want the destructure to run and the case to pass, exit %d:\n%s", exit, out)
	}
}
