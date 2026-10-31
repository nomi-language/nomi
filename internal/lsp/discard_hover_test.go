package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The hover on a discard `_` is the type of what it discards and, where the
// match may fail, what it matches.
func TestHover_Discard(t *testing.T) {
	const coin = `import std/io

enum Coin {
  Penny
  Dime
}

struct Point {
  x: Int
  y: Int
}

fn count() -> Int {
  3
}

`
	cases := []struct {
		name string
		src  string
		want string
		// broken is true for a program the checker rejects on purpose.
		broken bool
	}{
		{"a tuple element", coin + `fn main() {
  #(▮_, coins) = #(1, [Coin.Penny])
  io.inspect(coins)
}
`, "`Int`, discarded", false},
		{"a struct field", coin + `fn main() {
  #Point{x: ▮_, y} = #Point{x: 1, y: 2}
  io.inspect(y)
}
`, "`Int`, discarded", false},
		{"a variant payload in a case arm", coin + `fn main() {
  n = case Some("a") {
    Some(▮_) -> 1
    None -> 0
  }
  io.inspect(n)
}
`, "`String`, discarded: matches any `String`", false},
		{"a list element in a case arm", coin + `fn main() {
  n = case [1, 2] {
    [▮_] -> 1
    _ -> 0
  }
  io.inspect(n)
}
`, "`Int`, discarded: matches any `Int`", false},
		{"a vector element in a case arm", coin + `fn main() {
  n = case #[1, 2] {
    #[▮_, ..] -> 1
    _ -> 0
  }
  io.inspect(n)
}
`, "`Int`, discarded: matches any `Int`", false},
		{"a case arm's whole pattern", coin + `fn main() {
  n = case Coin.Dime {
    .Penny -> 1
    ▮_ -> 2
  }
  io.inspect(n)
}
`, "`Coin`, discarded: matches any `Coin`", false},
		{"an or-pattern alternative", coin + `fn main() {
  n = case 3 {
    1 or ▮_ -> 1
  }
  io.inspect(n)
}
`, "`Int`, discarded: matches any `Int`", false},
		{"a binding", coin + `fn main() {
  ▮_ = count()
  io.inspect(0)
}
`, "`Int`, discarded", false},
		{"an annotated binding", coin + `fn main() {
  ▮_: Int = count()
  io.inspect(0)
}
`, "`Int`, discarded", false},
		{"a binding with else", coin + `fn main() {
  Some(▮_) = Some(Coin.Dime) else {
    return
  }
  io.inspect(0)
}
`, "`Coin`, discarded: matches any `Coin`", false},
		{"an if pattern", coin + `fn main() {
  n = if Some(▮_) = Some(1.5) { 1 } else { 0 }
  io.inspect(n)
}
`, "`Float`, discarded: matches any `Float`", false},
		{"a lambda parameter", coin + `fn main() {
  n = Iter.reduce(["a", "b"], fn total = 0, ▮_ -> total + 1)
  io.inspect(n)
}
`, "`String`, discarded", false},
		{"a lone lambda parameter", coin + `fn main() {
  xs = ["a"] |> Iter.map(fn ▮_ -> 0) |> Iter.to_list()
  io.inspect(xs)
}
`, "`String`, discarded", false},
		{"a function parameter", coin + `fn second(▮_: String, b: Int) -> Int {
  b
}

fn main() {
  io.inspect(second("a", 2))
}
`, "`String`, discarded", false},
		{"a reduce seed pattern", coin + `fn main() {
  r = Iter.reduce([1, 2], fn #(▮_, acc) = #(0, []), x -> #(x, [x, ..acc]))
  io.inspect(r)
}
`, "`Int`, discarded", false},
		{"a subtests row pattern", coin + `test "rows" {
  subtests [#(1, "a")], #(n, ▮_) {
    assert n > 0
  }
  io.inspect(0)
}
`, "`String`, discarded", false},
		{"an assert pattern", coin + `test "assert" {
  assert Some(▮_) = Some(1)
  io.inspect(0)
}
`, "`Int`, discarded: matches any `Int`", false},
		{"a case with no subject", coin + `fn main() {
  d = count()
  n = case {
    d > 2 -> 1
    ▮_ -> 2
  }
  io.inspect(n)
}
`, "Otherwise: runs when no condition above holds.", false},
		{"a value whose type failed to infer", coin + `fn main() {
  ▮_ = nope()
}
`, "discarded", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, tc.src)
			s := NewServer()
			doc := s.docs.Open("file:///discard.nomi", src)
			if doc == nil || doc.Analysis == nil {
				t.Fatal("no analysis")
			}
			clean := len(doc.Errors) == 0 && len(doc.Analysis.TypeErrors) == 0
			if clean == tc.broken {
				t.Fatalf("the program checks clean = %v, want %v: %v %v", clean, !tc.broken, doc.Errors, doc.Analysis.TypeErrors)
			}
			got, rng := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)})
			if got != tc.want {
				t.Errorf("hover = %q, want %q", got, tc.want)
			}
			if rng == nil || rng.Start.Character != uint32(pos.Col-1) || rng.End.Character != uint32(pos.Col) {
				t.Errorf("hover range = %+v, want the `_`", rng)
			}
		})
	}
}

// A name that starts with `_` is a binding, with its own hover.
func TestHover_UnderscoreNameIsNotADiscard(t *testing.T) {
	src, pos := hoverMarkerPosition(t, `fn main() {
  ▮_unused = 3
}
`)
	got, _ := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)})
	if !strings.Contains(got, "_unused: Int") || strings.Contains(got, "discarded") {
		t.Errorf("hover on `_unused` = %q, want the binding's hover", got)
	}
}
