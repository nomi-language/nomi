package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// A binding, an expression or another statement at file level is an error at
// the statement: a file binds a value only with `once`, and code runs inside
// a function. Before this was checked, `x = 3` resolved in the checker with
// no value for the IR builder to read, and `io.print(...)` or an undefined
// name was dropped silently.
func TestFileLevelStatement_IsAnErrorAtTheStatement(t *testing.T) {
	errs := checkWithStdlib(`import std/io

x = 3
_ = 4
(a, b) = (1, 2)
io.print("hi")
undefined_name
assert 1 == 1
once fine = 5

fn main() {
  io.inspect(x + a + b + fine)
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s | %s", e.Line, e.Col, e.Message, strings.Join(e.Hints, "; ")))
	}
	const binding = "a binding cannot be written at file level: a file binds a value only with `once`"
	const expr = "an expression cannot be written at file level: a file holds declarations, and code runs inside a function"
	const stmt = "this statement cannot be written at file level: a file holds declarations, and code runs inside a function"
	const destructure = "a destructuring binding cannot be written at file level: a file binds a value only with `once`"
	const moveIt = "move it into a function, such as `fn main`"
	want := []string{
		"3:1 " + binding + " | write `once x = ...` for a value the file computes once, or move the binding into a function",
		"4:1 " + binding + " | " + moveIt,
		"5:1 " + destructure + " | bind each value with its own `once name = ...`, or move the binding into a function",
		"6:1 " + expr + " | " + moveIt,
		"7:1 " + expr + " | " + moveIt,
		"8:1 " + stmt + " | " + moveIt,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
