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

// lambdaLink ends the hover of a lambda's `fn`, which the keyword adds
// below the lambda's type; its `->` answers with the type alone.
const lambdaLink = "\n\nMore in the [tour](https://nomi-lang.org/functions-and-lambdas/#lambdas)."

const mapLambda = "```nomi\nfn n: Int -> List<Int>\n```\n\nFills `Maybe.map`'s `f: (T) -> U`, which gives its parameter types."

func TestHover_LambdaFnAndArrow(t *testing.T) {
	runFnHoverCases(t, []fnHoverCase{
		{"the fn of a lambda with inferred parameters", `import std::io

fn main()
  io.inspect(Maybe.map(Some(1), ▮fn n -> [n]))
end
`, mapLambda + lambdaLink},
		{"its ->", `import std::io

fn main()
  io.inspect(Maybe.map(Some(1), fn n ▮-> [n]))
end
`, mapLambda},
		{"annotated parameters", `import std::io

fn main()
  f = ▮fn a: Int, b: Int -> a * b
  io.inspect(f(2, 3))
end
`, "```nomi\nfn a: Int, b: Int -> Int\n```\n\nParameter types from the annotation." + lambdaLink},
		{"annotated parameters of an argument", `import std::io

fn main()
  io.inspect(Maybe.map(Some(1), fn n: Int ▮-> n + 1))
end
`, "```nomi\nfn n: Int -> Int\n```\n\nFills `Maybe.map`'s `f: (T) -> U`; parameter types from the annotation."},
		{"a fold's callback", `import std::io

fn main()
  io.inspect(Iter.reduce([1, 2], 0, ▮fn total, x -> total + x))
end
`, "```nomi\nfn total: Int, x: Int -> Int\n```\n\nFills `Iter.reduce`'s `f: (U, T) -> U`, which gives its parameter types." + lambdaLink},
		{"zero parameters", `import std::io

fn main()
  f = ▮fn -> 42
  io.inspect(f())
end
`, "```nomi\nfn -> Int\n```" + lambdaLink},
		{"the -> of zero parameters", `import std::io

fn main()
  f = fn ▮-> 42
  io.inspect(f())
end
`, "```nomi\nfn -> Int\n```"},
		{"a pattern parameter", `import std::io

fn main()
  pairs = [(1, 2)] |> Iter.map(▮fn (a, b) -> a + b) |> Iter.to_list()
  io.inspect(pairs)
end
`, "```nomi\nfn (a, b): (Int, Int) -> Int\n```\n\nFills `Iter.map`'s `f: (T) -> U`, which gives its parameter types." + lambdaLink},
		{"a block body", `import std::io

fn main()
  xs = [1, 2] |> Iter.map(fn n ▮->
    m = n * 2
    Int.to_string(m)
  end) |> Iter.to_list()
  io.inspect(xs)
end
`, "```nomi\nfn n: Int -> String\n```\n\nFills `Iter.map`'s `f: (T) -> U`, which gives its parameter types."},
		{"a then stage's lambda", `import std::io

fn main()
  n = 3
  |> then ▮fn v -> v + 1
  io.inspect(n)
end
`, "```nomi\nfn v: Int -> Int\n```\n\nThe `then` stage's lambda; its parameter is the piped value." + lambdaLink},
		{"a tap stage's lambda", `import std::io

fn main()
  n = 3
  |> tap fn v ▮-> io.inspect(v)
  io.inspect(n)
end
`, "```nomi\nfn v: Int -> Unit\n```\n\nThe `tap` stage's lambda; its parameter is the piped value."},
		{"the expected type", `import std::io

fn main()
  f: (Int) -> Int = ▮fn n -> n + 1
  io.inspect(f(1))
end
`, "```nomi\nfn n: Int -> Int\n```\n\nParameter types from the expected type `(Int) -> Int`." + lambdaLink},
	})
}

// A declaration's `fn` answers as its name does, at the top level and
// nested in a body, and adds what the keyword declares and its link.
// declarationFnKeyword is what a declaration's `fn` adds below its name's
// hover.
const declarationFnKeyword = "\n\n---\n\nDeclares a function. Every parameter has a type, the result type is written unless it is `Unit`, and the body's last expression is the result.\n\nMore in the [tour](https://nomi-lang.org/functions-and-lambdas/#defining-a-function)."

func TestHover_DeclarationFnKeyword(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a top-level function", `import std::io

/// Doubles n.
▮fn double(n: Int) -> Int
  n * 2
end

fn main()
  io.inspect(double(2))
end
`, "```nomi\nfn double(n: Int) -> Int\n```\n\nDoubles n."},
		{"a pub function", `import std::io

/// Doubles n.
pub ▮fn double(n: Int) -> Int
  n * 2
end

fn main()
  io.inspect(double(2))
end
`, "```nomi\nfn double(n: Int) -> Int\n```\n\nDoubles n."},
		{"a nested function", `import std::io

fn main()
  ▮fn triple(n: Int) -> Int
    n * 3
  end
  io.inspect(triple(2))
end
`, "```nomi\nfn triple(n: Int) -> Int\n```"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, tc.src)
			name, _ := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col + 3)})
			if name != tc.want {
				t.Fatalf("hover on the name = %q, want %q", name, tc.want)
			}
			want := name + declarationFnKeyword
			for offset := 0; offset < 2; offset++ {
				got, rng := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1 + offset)})
				if got != want {
					t.Errorf("hover on character %d of `fn` = %q, want the name's hover and the keyword's %q", offset+1, got, want)
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
		{"a return type's ->", `import std::io

fn double(n: Int) ▮-> Int
  n * 2
end

fn main()
  io.inspect(double(2))
end
`, ""},
		{"a function type's ->", `import std::io

fn apply(f: (Int) ▮-> Int) -> Int
  f(1)
end

fn main()
  io.inspect(apply(fn n -> n))
end
`, ""},
		{"a case arm's ->", `import std::io

fn main()
  n = 1
  io.inspect(case n
    1 ▮-> "one"
    _ -> "other"
  end)
end
`, ""},
	})
}

// A lambda parameter a sibling element of its list types (an annotation at
// the same place in another element) shows that type on hover and as an
// inlay hint.
func TestHover_LambdaParamTypedByAListSibling(t *testing.T) {
	src := "struct Day\n    n: Int\nend\n\n" +
		"fn main()\n" +
		"  xs = [(\"a\", fn d: Day -> d.n), (\"b\", fn d -> d.n + 1)]\n" +
		"  _ = xs\n" +
		"end\n"
	col := strings.LastIndex(strings.Split(src, "\n")[5], "fn d") + 3
	got, _ := hoverAt(t, src, protocol.Position{Line: 5, Character: uint32(col)})
	if !strings.Contains(got, "d: Day") {
		t.Errorf("hover on the unannotated d = %q, want it to show `d: Day`", got)
	}
	hints := getInlayHints(t, src)
	found := false
	for _, h := range filterByKind(hints, InlayHintKindType) {
		if h.Position.Line == 5 && h.Position.Character == uint32(col+1) && h.Label == ": Day" {
			found = true
		}
	}
	if !found {
		t.Errorf("no `: Day` inlay hint after the unannotated d: %+v", hints)
	}
}
