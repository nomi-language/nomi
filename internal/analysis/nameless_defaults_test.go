package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// Defaults belong to a declaration, as parameter names do: a call through a
// function value with none behind it passes every argument. A partial bound
// to a name keeps its callee's defaults, and so does a copy of it.
func TestDefaultsThroughANamelessValue_AreAnError(t *testing.T) {
	errs := checkWithStdlib(`
fn connect(host: String, port: Int = 8080): String {
  host
}

fn id<T>(x: T): T {
  x
}

fn main() {
  c = connect(_, port: _)
  _ = c("a")
  r = id(c)
  _ = r("b")
  _ = r("b", 1)
  h = c
  _ = h("d")
  p = || connect(_, port: _)
  _ = p()("e")
  _ = case Some(c) {
    Some(k) -> k("f")
    None -> ""
  }
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s %v", e.Line, e.Col, e.Message, e.Hints))
	}
	hint := "[a function type's parameters have no defaults; pass every argument]"
	want := []string{
		"14:7 expected 2 arguments, got 1 " + hint,
		"19:7 expected 2 arguments, got 1 " + hint,
		"21:16 expected 2 arguments, got 1 " + hint,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
