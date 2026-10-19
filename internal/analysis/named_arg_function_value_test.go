package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A value whose type is a function type has no parameter names: a
// parameter, a struct field, a binding that copies a function value, a
// call's result, a value taken out of a Maybe or a list. A named argument
// through one, full or `_`, is "no parameter named", at the name. The checker
// knew no names for such a callee and skipped the name, so a full call
// passed the checker and the IR builder declined it, and a named `_`
// was dropped, typing the partial as the callee's result ("`String` is not a
// function" at its next call). A parameter's symbol points at the function
// that declares it, so a call through `g` in `fn f(port: Int, g: ...)` was
// checked against f's names and `g(port: 1)` was admitted.

const namelessDecls = `
struct Holder {
  f: (String, Int) -> String
}

fn connect(host: String, port: Int) -> String {
  if port > 0 { host } else { "" }
}

fn make() -> (String, Int) -> String {
  connect
}
`

const namelessHint = "a function type's parameters have no names; pass the arguments by position"

func TestNamedArgThroughFunctionValue_IsAnError(t *testing.T) {
	for _, tc := range []struct{ name, src, at string }{
		{"struct field, full call", `fn main() {
  holder = #Holder{f: connect}
  x = holder.f(port: 1, host: "h")
  _ = x
}`, "port: 1"},
		{"struct field, named placeholder", `fn main() {
  holder = #Holder{f: connect}
  x = holder.f(port: _, host: "h")
  _ = x
}`, "port: _"},
		{"struct field, pipe", `fn main() {
  holder = #Holder{f: connect}
  x = "h" |> holder.f(port: 1)
  _ = x
}`, "port: 1"},
		{"parameter, full call", `fn through(g: (String, Int) -> String) -> String {
  g(port: 1, host: "h")
}
fn main() {
  _ = through(connect)
}`, "port: 1"},
		{"parameter, named placeholder", `fn through(g: (String, Int) -> String) -> (String) -> String {
  g(port: 1, host: _)
}
fn main() {
  _ = through(connect)
}`, "port: 1"},
		{"parameter named like the enclosing function's parameter", `fn through(port: Int, g: (Int) -> Int) -> Int {
  g(port: port)
}
fn main() {
  _ = through(1, fn n -> n)
}`, "port: port"},
		{"lambda parameter", `fn main() {
  f = fn g: (String, Int) -> String -> g(port: 1, host: "h")
  _ = f(connect)
}`, "port: 1"},
		{"generic parameter", `fn apply<T>(g: (T) -> T, v: T) -> T {
  g(value: v)
}
fn main() {
  _ = apply(fn n: Int -> n, 1)
}`, "value: v"},
		{"call result", `fn main() {
  x = make()(port: 1, host: "h")
  _ = x
}`, "port: 1"},
		{"Maybe element", `fn main() {
  x = case Some(connect) {
    Some(g) -> g(port: 1, host: "h")
    None -> ""
  }
  _ = x
}`, "port: 1"},
		{"list element", `fn main() {
  x = case [connect] {
    [g] -> g(port: 1, host: "h")
    _ -> ""
  }
  _ = x
}`, "port: 1"},
		{"alias of a function", `fn main() {
  g = connect
  x = g(port: 1, host: "h")
  _ = x
}`, "port: 1"},
		{"alias of a lambda", `fn main() {
  f = fn host: String, port: Int -> if port > 0 { host } else { "" }
  g = f
  x = g(port: 1, host: "h")
  _ = x
}`, "port: 1"},
		{"binding typed by a function type", `fn main() {
  g: (String, Int) -> String = connect
  x = g(port: 1, host: "h")
  _ = x
}`, "port: 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := namelessDecls + tc.src + "\n"
			line, col := lineColOf(t, src, tc.at)
			_, errs := checkSourceWithStdlib(src)
			want := "no parameter named '" + tc.at[:strings.Index(tc.at, ":")] + "'"
			var found *analysis.TypeError
			for i, e := range errs {
				if e.Message == want && e.Line == line && e.Col == col {
					found = &errs[i]
				}
				if strings.Contains(e.Message, "is not a function") {
					t.Errorf("a cascade error after the rejected name: %s", e.Error())
				}
			}
			if found == nil {
				t.Fatalf("the front end admits a named argument through a function value; want %q at %d:%d, got:\n%s",
					want, line, col, errorList(errs))
			}
			if !containsString(found.Hints, namelessHint) {
				t.Errorf("hints %q; want %q", found.Hints, namelessHint)
			}
		})
	}
}

// The callees that have names keep them: a declared function, a lambda bound
// to a name, a partial bound to a name, and a partial of a partial.
func TestNamedArgThroughFunctionValue_DeclaredCalleesKeepNames(t *testing.T) {
	_, errs := checkSourceWithStdlib(namelessDecls + `fn main() {
  a: String = connect(port: 1, host: "h")
  f = fn host: String, port: Int -> if port > 0 { host } else { "" }
  b: String = f(port: 1, host: "h")
  c = connect(_, port: _)
  d: String = c(port: 1, host: "h")
  e: String = c(port: _, host: "h")(9)
  holder = #Holder{f: connect}
  g: String = holder.f("h", 1)
  _ = #(a, b, d, e, g)
}
`)
	expectNoStdlibErrors(t, errs)
}

func lineColOf(t *testing.T, src, at string) (int, int) {
	t.Helper()
	i := strings.Index(src, at)
	if i < 0 || strings.Count(src, at) != 1 {
		t.Fatalf("%q must occur exactly once in the source", at)
	}
	line := strings.Count(src[:i], "\n") + 1
	col := i - strings.LastIndex(src[:i], "\n")
	return line, col
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
