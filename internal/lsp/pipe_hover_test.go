package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The declarations every pipe-hover case reads.
const pipeHoverDecls = `import std/io

struct Point {
  x: Int
  y: Int
}

enum Shape {
  Dot(Int)
  Circle { r: Int }
}

type UserId String

fn divide(a: Int, b: Int) -> Int {
  a / b
}

fn is_ready(n: Int) -> Bool {
  n > 1
}

`

// pipeHoverCase is a program with a `▮` before the `|>` to hover, and the
// hover both of its characters give; an empty want asks for no hover.
type pipeHoverCase struct {
	name string
	body string
	want string
}

func runPipeHoverCases(t *testing.T, cases []pipeHoverCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, pipeHoverDecls+tc.body)
			if !strings.HasPrefix(strings.Split(src, "\n")[pos.Line-1][pos.Col-1:], "|>") {
				t.Fatalf("the marker is not before a `|>`")
			}
			for offset := 0; offset < 2; offset++ {
				got, rng := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1 + offset)})
				if got != tc.want {
					t.Errorf("hover on character %d of `|>` = %q, want %q", offset+1, got, tc.want)
				}
				if tc.want != "" && (rng == nil || rng.Start.Character != uint32(pos.Col-1) || rng.End.Character != uint32(pos.Col+1)) {
					t.Errorf("hover range = %+v, want the `|>`", rng)
				}
			}
		})
	}
}

// hoverAt is the server's hover over src at at, and its range.
func hoverAt(t *testing.T, src string, at protocol.Position) (string, *protocol.Range) {
	t.Helper()
	uri := "file:///pipe_hover.nomi"
	s := NewServer()
	s.docs.Open(uri, src)
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     at,
		},
	})
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if res == nil {
		return "", nil
	}
	return res.Contents.(protocol.MarkupContent).Value, res.Range
}

func TestHover_PipeCallStages(t *testing.T) {
	runPipeHoverCases(t, []pipeHoverCase{
		{"an owner function, multi-line", `fn main() {
  lines = ["1", "x"]
  total = lines
  |> Iter.map(fn s -> String.to_int(s))
  ▮|> Maybe.values()
  |> Iter.sum()
  io.inspect(total)
}
`, "`Iter<Maybe<Int>>` → `Maybe.values(maybes)` → `List<Int>`\n\n```nomi\nfn values(maybes: Iter<Maybe<Int>>) -> List<Int>\n```\n\nThe piped value fills `maybes`."},
		{"a call with a lambda, on one line", `fn main() {
  io.inspect([1, 2, 3] ▮|> Iter.filter(fn x -> x > 2) |> Iter.to_list())
}
`, "`List<Int>` → `Iter.filter(source, pred)` → `Iter<Int>`\n\n```nomi\nfn filter(source: Iter<Int>, pred: (Int) -> Bool) -> Iter<Int>\n```\n\nThe piped value fills `source`."},
		{"a then stage's capture", `fn main() {
  io.inspect(10 ▮|> then &divide(100, &1))
}
`, "`Int` → `then &divide(100, &1)` → `Int`\n\n`&1` is the `Int`; the stage answers the capture's result."},
		{"a pipe inside a lambda body", `fn main() {
  xs = [1, 2] |> Iter.map(fn n -> n ▮|> Int.to_string()) |> Iter.to_list()
  io.inspect(xs)
}
`, "`Int` → `Int.to_string(n)` → `String`\n\n```nomi\nfn to_string(n: Int) -> String\n```\n\nThe piped value fills `n`."},
	})
}

func TestHover_PipeValueStages(t *testing.T) {
	runPipeHoverCases(t, []pipeHoverCase{
		{"a function value", `fn main() {
  f = fn n: Int -> n * 2
  io.inspect(3 ▮|> f())
}
`, "`Int` → `f(Int)` → `Int`\n\n```nomi\nf: (n: Int) -> Int\n```\n\nThe piped value fills argument 1."},
		{"a struct's call form", `fn main() {
  io.inspect(#{x: 1, y: 2} ▮|> Point())
}
`, "`#{x: Int, y: Int}` → `Point(…)` → `Point`\n\n```nomi\nstruct Point {\n    x: Int\n    y: Int\n}\n```\n\nBuilds the type from the piped value."},
		{"a distinct type's constructor", `fn main() {
  io.inspect("bob" ▮|> UserId())
}
`, "`String` → `UserId(…)` → `UserId`\n\n```nomi\ntype UserId String\n```\n\nBuilds the type from the piped value."},
		{"a variant constructor", `fn main() {
  io.inspect(4 ▮|> Shape.Dot())
}
`, "`Int` → `Shape.Dot(…)` → `Shape`\n\n```nomi\nShape.Dot(Int)\n```\n\nThe piped value is the payload."},
	})
}

func TestHover_PipeKeywordStages(t *testing.T) {
	runPipeHoverCases(t, []pipeHoverCase{
		{"then", `fn main() {
  n = 3
  ▮|> then fn v -> v + 1
  io.inspect(n)
}
`, "`Int` → `then fn v -> …` → `Int`\n\n`v` is the `Int`; the stage answers the lambda's result."},
		{"then with a pattern", `fn main() {
  n = #(1, 2)
  ▮|> then fn #(a, b) -> a + b
  io.inspect(n)
}
`, "`#(Int, Int)` → `then fn #(a, b) -> …` → `Int`\n\n`#(a, b)` matches the `#(Int, Int)`; the stage answers the lambda's result."},
		{"tap", `fn main() {
  n = 3
  ▮|> tap fn v -> io.inspect(v)
  io.inspect(n)
}
`, "`Int` → `tap fn v -> …` → `Int`\n\n`v` is the `Int`; the lambda runs for its effect and the value passes on unchanged."},
		{"a ? in a function", `fn parse(s: String) -> Result<Int, String> {
  n = s |> String.to_int() ▮|> Maybe.to_result("bad")?
  Ok(n)
}

fn main() {
  io.inspect(parse("1"))
}
`, "`Maybe<Int>` → `Maybe.to_result(maybe, error)` → `Result<Int, String>` → `?` → `Int`\n\n```nomi\nfn to_result(maybe: Maybe<Int>, error: String) -> Result<Int, String>\n```\n\nThe piped value fills `maybe`.\n\nAnswers the success value, or returns the `Err` from `fn parse`."},
		{"a ? in a lambda", `fn main() {
  f = fn s: String -> {
    n = s ▮|> String.to_int()?
    Some(n)
  }
  io.inspect(f("1"))
}
`, "`String` → `String.to_int(s)` → `Maybe<Int>` → `?` → `Int`\n\n```nomi\nfn to_int(s: String) -> Maybe<Int>\n```\n\nThe piped value fills `s`.\n\nAnswers the success value, or returns `None` from the enclosing lambda."},
		{"a ? in a test body", `fn main() {}

test "parses" {
  n = "4" |> String.to_int() ▮|> Maybe.to_result("no")?
  assert n == 4
}
`, "`Maybe<Int>` → `Maybe.to_result(maybe, error)` → `Result<Int, String>` → `?` → `Int`\n\n```nomi\nfn to_result(maybe: Maybe<Int>, error: String) -> Result<Int, String>\n```\n\nThe piped value fills `maybe`.\n\nAnswers the success value, or returns the `Err` from `test \"parses\"`."},
		{"a ? after a call", `fn parse(s: String) -> Maybe<Int> {
  n = s ▮|> String.to_int()?
  Some(n)
}

fn main() {
  io.inspect(parse("1"))
}
`, "`String` → `String.to_int(s)` → `Maybe<Int>` → `?` → `Int`\n\n```nomi\nfn to_int(s: String) -> Maybe<Int>\n```\n\nThe piped value fills `s`.\n\nAnswers the success value, or returns `None` from `fn parse`."},
		{"dbg", `fn main() {
  n = 3 ▮|> dbg
  io.inspect(n)
}
`, "`Int` → `dbg` → `Int`\n\nPrints the value with its source line and passes it on unchanged."},
		{"a bare if", `fn main() {
  io.inspect(3 |> is_ready() ▮|> if { 1 } else { 2 })
}
`, "`Bool` → `if` → `Int`\n\nBranches on the piped `Bool`."},
		{"if prefixing its condition", `fn main() {
  io.inspect(3 ▮|> if is_ready() { "y" } else { "n" })
}
`, "`Int` → `is_ready(n)` → `Bool` → `if` → `String`\n\n```nomi\nfn is_ready(n: Int) -> Bool\n```\n\nThe piped value fills `n`.\n\nBranches on the piped `Bool`."},
		{"case prefixing its subject", `fn main() {
  t = 3
  ▮|> case divide(1) {
    0 -> "zero"
    _ -> "other"
  }
  io.inspect(t)
}
`, "`Int` → `divide(a, b)` → `Int` → `case` → `String`\n\n```nomi\nfn divide(a: Int, b: Int) -> Int\n```\n\nThe piped value fills `a`.\n\nMatches the piped `Int` against the arms."},
		{"todo", `fn later() -> Int {
  3 ▮|> todo
}

fn main() {
  io.inspect(later())
}
`, "`Int` → `todo`\n\nStops the program here when it runs; the pipeline has the type its position expects."},
	})
}

// A stage that fails to type shows the incoming type and the stage, and a
// pipe whose incoming value has no type shows nothing.
func TestHover_PipeThatFailsToType(t *testing.T) {
	runPipeHoverCases(t, []pipeHoverCase{
		{"an undefined stage", `fn main() {
  io.inspect("x" ▮|> nope())
}
`, "`String` → `nope()`"},
		{"a bare name stage", `fn main() {
  io.inspect(3 ▮|> Int.to_string)
}
`, "`Int` → `Int.to_string`"},
		{"an unknown head", `fn main() {
  io.inspect(nope() ▮|> Int.to_string())
}
`, ""},
	})
}
