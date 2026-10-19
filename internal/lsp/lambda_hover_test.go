package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// fnHoverCase is a program with a `▮` before the `fn` or `->` to hover, and
// the hover each of its two characters gives.
type fnHoverCase struct {
	name string
	src  string
	want string
}

func runFnHoverCases(t *testing.T, cases []fnHoverCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, tc.src)
			at := strings.Split(src, "\n")[pos.Line-1][pos.Col-1:]
			if !strings.HasPrefix(at, "fn") && !strings.HasPrefix(at, "->") {
				t.Fatalf("the marker is not before a `fn` or `->`")
			}
			for offset := 0; offset < 2; offset++ {
				got, rng := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1 + offset)})
				if got != tc.want {
					t.Errorf("hover on character %d = %q, want %q", offset+1, got, tc.want)
				}
				if tc.want != "" && (rng == nil || rng.Start.Character != uint32(pos.Col-1) || rng.End.Character != uint32(pos.Col+1)) {
					t.Errorf("hover range = %+v, want the two characters", rng)
				}
			}
		})
	}
}

const mapLambda = "```nomi\nfn n: Int -> List<Int>\n```\n\nFills `Maybe.map`'s `f: (T) -> U`, which gives its parameter types."

func TestHover_LambdaFnAndArrow(t *testing.T) {
	runFnHoverCases(t, []fnHoverCase{
		{"the fn of a lambda with inferred parameters", `import std/io

fn main() {
  io.inspect(Maybe.map(Some(1), ▮fn n -> [n]))
}
`, mapLambda},
		{"its ->", `import std/io

fn main() {
  io.inspect(Maybe.map(Some(1), fn n ▮-> [n]))
}
`, mapLambda},
		{"annotated parameters", `import std/io

fn main() {
  f = ▮fn a: Int, b: Int -> a * b
  io.inspect(f(2, 3))
}
`, "```nomi\nfn a: Int, b: Int -> Int\n```\n\nParameter types from the annotation."},
		{"annotated parameters of an argument", `import std/io

fn main() {
  io.inspect(Maybe.map(Some(1), fn n: Int ▮-> n + 1))
}
`, "```nomi\nfn n: Int -> Int\n```\n\nFills `Maybe.map`'s `f: (T) -> U`; parameter types from the annotation."},
		{"a seeded parameter", `import std/io

fn main() {
  io.inspect(Iter.reduce([1, 2], ▮fn total = 0, x -> total + x))
}
`, "```nomi\nfn total: Int = 0, x: Int -> Int\n```\n\nFills `Iter.reduce`'s `f: (U, T) -> U`, which gives its parameter types."},
		{"zero parameters", `import std/io

fn main() {
  f = ▮fn -> 42
  io.inspect(f())
}
`, "```nomi\nfn -> Int\n```"},
		{"the -> of zero parameters", `import std/io

fn main() {
  f = fn ▮-> 42
  io.inspect(f())
}
`, "```nomi\nfn -> Int\n```"},
		{"a pattern parameter", `import std/io

fn main() {
  pairs = [#(1, 2)] |> Iter.map(▮fn #(a, b) -> a + b) |> Iter.to_list()
  io.inspect(pairs)
}
`, "```nomi\nfn #(a, b): #(Int, Int) -> Int\n```\n\nFills `Iter.map`'s `f: (T) -> U`, which gives its parameter types."},
		{"a block body", `import std/io

fn main() {
  xs = [1, 2] |> Iter.map(fn n ▮-> {
    m = n * 2
    Int.to_string(m)
  }) |> Iter.to_list()
  io.inspect(xs)
}
`, "```nomi\nfn n: Int -> String\n```\n\nFills `Iter.map`'s `f: (T) -> U`, which gives its parameter types."},
		{"a then stage's lambda", `import std/io

fn main() {
  n = 3
  |> then ▮fn v -> v + 1
  io.inspect(n)
}
`, "```nomi\nfn v: Int -> Int\n```\n\nThe `then` stage's lambda; its parameter is the piped value."},
		{"a tap stage's lambda", `import std/io

fn main() {
  n = 3
  |> tap fn v ▮-> io.inspect(v)
  io.inspect(n)
}
`, "```nomi\nfn v: Int -> Unit\n```\n\nThe `tap` stage's lambda; its parameter is the piped value."},
		{"the expected type", `import std/io

fn main() {
  f: (Int) -> Int = ▮fn n -> n + 1
  io.inspect(f(1))
}
`, "```nomi\nfn n: Int -> Int\n```\n\nParameter types from the expected type `(Int) -> Int`."},
	})
}

// A declaration's `fn` answers as its name does, at the top level and
// nested in a body.
func TestHover_DeclarationFnKeyword(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a top-level function", `import std/io

/// Doubles n.
▮fn double(n: Int) -> Int {
  n * 2
}

fn main() {
  io.inspect(double(2))
}
`, "```nomi\nfn double(n: Int) -> Int\n```\n\nDoubles n."},
		{"a pub function", `import std/io

/// Doubles n.
pub ▮fn double(n: Int) -> Int {
  n * 2
}

fn main() {
  io.inspect(double(2))
}
`, "```nomi\nfn double(n: Int) -> Int\n```\n\nDoubles n."},
		{"a nested function", `import std/io

fn main() {
  ▮fn triple(n: Int) -> Int {
    n * 3
  }
  io.inspect(triple(2))
}
`, "```nomi\nfn triple(n: Int) -> Int\n```"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, tc.src)
			name, _ := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col + 3)})
			if name != tc.want {
				t.Fatalf("hover on the name = %q, want %q", name, tc.want)
			}
			for offset := 0; offset < 2; offset++ {
				got, rng := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1 + offset)})
				if got != name {
					t.Errorf("hover on character %d of `fn` = %q, want the name's %q", offset+1, got, name)
				}
				if rng == nil || rng.Start.Character != uint32(pos.Col-1) || rng.End.Character != uint32(pos.Col+1) {
					t.Errorf("hover range = %+v, want the `fn`", rng)
				}
			}
		})
	}
}

// The `->` of a return type, a function type and a `case` arm keep
// answering nothing.
func TestHover_OtherArrowsAnswerNothing(t *testing.T) {
	runFnHoverCases(t, []fnHoverCase{
		{"a return type's ->", `import std/io

fn double(n: Int) ▮-> Int {
  n * 2
}

fn main() {
  io.inspect(double(2))
}
`, ""},
		{"a function type's ->", `import std/io

fn apply(f: (Int) ▮-> Int) -> Int {
  f(1)
}

fn main() {
  io.inspect(apply(fn n -> n))
}
`, ""},
		{"a case arm's ->", `import std/io

fn main() {
  n = 1
  io.inspect(case n {
    1 ▮-> "one"
    _ -> "other"
  })
}
`, ""},
	})
}
