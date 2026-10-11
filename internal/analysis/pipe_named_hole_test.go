package analysis_test

import (
	"testing"
)

const pipeNamedHoleDecls = `fn tag(a: Int, w: String): String {
  "${a}:${w}"
}

fn spaced(a: Int = 1, b: Int = 2, w: String): String {
  "${a}/${b}/${w}"
}

fn copies<T>(n: Int, item: T): List<T> {
  if n > 1 { [item, item] } else { [item] }
}

fn halve(by: Int, n: Int): Result<Int, String> {
  if n % by == 0 { Ok(n / by) } else { Err("odd") }
}

`

// A pipe fills a named `_` with the piped value, as it fills a positional
// one, and the stage's type is its callee's result. The annotations hold
// each stage to that type.
func TestPipeNamedHole_FillsTheNamedArgument(t *testing.T) {
	src := pipeNamedHoleDecls + `fn run(): Result<Int, String> {
  n = 8 |> try halve(by: 2, n: _)
  Ok(n)
}

fn main() {
  a: String = "x" |> tag(1, w: _)
  b: String = "y" |> tag(w: _, a: 2)
  c: String = "z" |> spaced(w: _)
  d: String = "q" |> spaced(5, w: _)
  e: String = "3" |> String.pad_start(width: 6, s: _)
  f: List<String> = "s" |> copies(n: 2, item: _)
  g: String = 7 |> Display.to_string(value: _)
  h: Int = 8 |> halve(by: 2, n: _) |> Result.with_default(0)
  _ = (a, b, c, d, e, f, g, h, run())
}
`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		t.Errorf("%d:%d %s", e.Line, e.Col, diagText(e))
	}
}

// A stage may name more than one slot with `_`, positional or named, and
// the piped value fills each. Every slot is checked against the piped
// value, so a slot that does not take it is the error at its `_`, naming
// it. Each case is still rejected.
func TestPipeNamedHole_EveryHoleTakesThePipedValue(t *testing.T) {
	ok := pipeNamedHoleDecls + `fn add(a: Int, b: Int): Int {
  a + b
}

fn main() {
  a: Int = 1 |> add(_, b: _)
  b: Int = 1 |> add(a: _, b: _)
  _ = (a, b)
}
`
	_, errs := checkSourceWithStdlib(ok)
	for _, e := range errs {
		t.Errorf("%d:%d %s", e.Line, e.Col, diagText(e))
	}
	for _, tc := range []struct{ name, stage, want string }{
		{"positional then named", `tag(_, w: _)`,
			"`tag` takes a String as `w`, but the pipe passes an Int\n" +
				"help: Int implements Display; add `|> Display.to_string()` before this stage"},
		{"two named", `tag(a: _, w: _)`,
			"`tag` takes a String as `w`, but the pipe passes an Int\n" +
				"help: Int implements Display; add `|> Display.to_string()` before this stage"},
		{"two positional", `tag(_, _)`,
			"`tag` takes a String as its second argument, but the pipe passes an Int\n" +
				"help: Int implements Display; add `|> Display.to_string()` before this stage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := pipeNamedHoleDecls + "fn main() {\n  _ = 1 |> " + tc.stage + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the front end accepts an Int piped into a String slot")
			}
			expectDiagAt(t, src, errs, tc.want, "_")
		})
	}
}

// A named `_` in a call that is not a pipe stage is still a partial
// application.
func TestPipeNamedHole_OutsideAPipeIsAPartial(t *testing.T) {
	src := pipeNamedHoleDecls + `fn main() {
  p: (String) -> String = tag(1, w: _)
  _ = p("ok")
}
`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		t.Errorf("%d:%d %s", e.Line, e.Col, diagText(e))
	}
}
