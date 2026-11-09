package analysis_test

import (
	"strings"
	"testing"
)

// A lambda of no parameters does not fit a function type that takes some: a
// function type's parameters pair with the function's (spec §5
// *Assignability*), and only a stateless `Iter.loop(fn -> ...)` takes one. The
// checker refused a named function of no parameters there but admitted the
// lambda, which the IR builder then declined (`Route.to(route, fn -> r)` was
// "this call to `Route.to` is not supported yet"). Each position a function
// value flows into reports the one error at the lambda, with help that
// writes a `_` for each parameter.
func TestLambdaOfNoParams_RefusedWhereParamsAreExpected(t *testing.T) {
	const one = "take the parameter and ignore it: `fn _ -> ...`"
	cases := []struct {
		name, src, msg, help string
		line, col            int
	}{
		{"argument", `fn apply(f: (Int) -> Int) -> Int {
  f(3)
}

fn main() {
  dbg apply(fn -> 7)
}`, "this lambda takes no parameters, but a (Int) -> Int takes 1 parameter", one, 6, 13},
		{"two parameters", `fn apply(f: (Int, String) -> Int) -> Int {
  f(3, "a")
}

fn main() {
  dbg apply(fn -> 7)
}`, "this lambda takes no parameters, but a (Int, String) -> Int takes 2 parameters", "take the parameters and ignore them: `fn _, _ -> ...`", 6, 13},
		{"generic", `fn same<T>(f: (T) -> T) -> (T) -> T {
  f
}

fn main() {
  dbg same(fn -> 1)
}`, "this lambda takes no parameters, but a (T) -> T takes 1 parameter", one, 6, 12},
		{"annotated binding", `fn main() {
  f: (Int) -> Int = fn -> 7
  dbg f(1)
}`, "this lambda takes no parameters, but a (Int) -> Int takes 1 parameter", one, 2, 21},
		{"list element", `fn main() {
  fs: List<(Int) -> Int> = [fn x -> x, fn -> 7]
  dbg Iter.map(fs, fn f -> f(1)) |> Iter.to_list()
}`, "this lambda takes no parameters, but a (Int) -> Int takes 1 parameter", one, 2, 40},
		{"struct field", `struct Hook {
  run: (Int) -> Int
}

fn main() {
  h = Hook{run: fn -> 7}
  dbg h.run(1)
}`, "this lambda takes no parameters, but a (Int) -> Int takes 1 parameter", one, 6, 17},
		{"return", `fn make() -> (Int) -> Int {
  fn -> 7
}

fn main() {
  dbg make()(1)
}`, "this lambda takes no parameters, but a (Int) -> Int takes 1 parameter", one, 2, 3},
		{"route handler", `import std::http.{Body, Response, Route}

fn main() {
  dbg Route.to(Route` + "`GET /c`" + `, fn -> Response{body: Body.Text("c")})
}`, "this lambda takes no parameters, but a (Request) -> ToResponse takes 1 parameter", one, 4, 31},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := checkLoopSource(t, c.src)
			if len(errs) != 1 {
				msgs := make([]string, len(errs))
				for i, e := range errs {
					msgs[i] = e.Error()
				}
				t.Fatalf("the front end admits this, or reports more than the one error; got %d:\n  %s", len(errs), strings.Join(msgs, "\n  "))
			}
			e := errs[0]
			if e.Message != c.msg || e.Line != c.line || e.Col != c.col {
				t.Fatalf("got %q at %d:%d, want %q at %d:%d", e.Message, e.Line, e.Col, c.msg, c.line, c.col)
			}
			if len(e.Hints) != 1 || e.Hints[0] != c.help {
				t.Fatalf("hints: got %q, want [%q]", e.Hints, c.help)
			}
		})
	}
}

// A named function of no parameters is refused there too, as a mismatch of
// the argument's type, and the `_` lambda the help writes is admitted.
func TestLambdaOfNoParams_NamedFunctionAndIgnoredParams(t *testing.T) {
	expectErrorContaining(t, checkLoopSource(t, `fn seven() -> Int {
  7
}

fn apply(f: (Int) -> Int) -> Int {
  f(3)
}

fn main() {
  dbg apply(seven)
}`), "argument 1: expected (Int) -> Int, got () -> Int")
	expectClean(t, checkLoopSource(t, `import std::http.{Body, Response, Route}

fn apply(f: (Int, String) -> Int) -> Int {
  f(3, "a")
}

fn main() {
  dbg apply(fn _, _ -> 7)
  dbg Route.to(Route`+"`GET /c`"+`, fn _ -> Response{body: Body.Text("c")})
  Iter.loop(fn -> { break })
}`))
}
