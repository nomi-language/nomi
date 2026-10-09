package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A std function's hover ends with its `//!` test as an example, rendered as
// the reference renders it: the `//! ` prefix gone and the blank `//!` line
// between the import and the asserts kept.
func TestHover_StdFunctionShowsItsExamples(t *testing.T) {
	got := hoverText(t, "examples_std", `fn main() {
  _ = String.▮split("a,b", ",")
}
`)
	want := "Inverse of `String.join`.\n\n**Examples**\n\n```nomi\n" +
		"import std/regex.Regex\n\n" +
		"assert String.split(\"a,b,c\", \",\") == [\"a\", \"b\", \"c\"]\n"
	if !strings.Contains(got, want) {
		t.Fatalf("hover on String.split lacks its example after the doc:\n%s", got)
	}
	if !strings.HasSuffix(got, "assert \"one  two\\tthree\" |> String.split(Regex`\\s+`) == [\"one\", \"two\", \"three\"]\n```") {
		t.Fatalf("hover on String.split does not end with its last example line:\n%s", got)
	}
	if strings.Contains(got, "//!") {
		t.Fatalf("hover kept the //! prefix:\n%s", got)
	}
}

const manyExamplesSource = `/// Adds one.
//! assert inc(0) == 1
//
//! assert inc(1) == 2
//
//! x = inc(2)
//!
//! assert x == 3
//
//! assert inc(3) == 4
//
//! assert inc(4) == 5
//
//! assert inc(5) == 6
//
//! assert inc(6) == 7
//
fn inc(n: Int): Int {
  n + 1
}

fn main() {
  _ = inc(1)
}
`

// A user function's `//!` blocks are separate examples; the hover shows the
// first five, each in its own block, and counts the rest.
func TestHover_UserFunctionShowsFiveExamplesAndCountsTheRest(t *testing.T) {
	want := "```nomi\nfn inc(n: Int): Int\n```\n\nAdds one.\n\n**Examples**\n\n" +
		"```nomi\nassert inc(0) == 1\n```\n\n" +
		"```nomi\nassert inc(1) == 2\n```\n\n" +
		"```nomi\nx = inc(2)\n\nassert x == 3\n```\n\n" +
		"```nomi\nassert inc(3) == 4\n```\n\n" +
		"```nomi\nassert inc(4) == 5\n```\n\n" +
		"*2 more examples at the declaration*"
	for name, input := range map[string]string{
		"call":        strings.Replace(manyExamplesSource, "_ = inc(1)", "_ = ▮inc(1)", 1),
		"declaration": strings.Replace(manyExamplesSource, "fn inc(", "fn ▮inc(", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if got := hoverText(t, "examples_user_"+name, input); got != want {
				t.Fatalf("hover = \n%s\n\nwant:\n%s", got, want)
			}
		})
	}
}

// Signature help on the same call carries the doc and none of the examples.
func TestSignatureHelp_LeavesOutExamples(t *testing.T) {
	input := strings.Replace(manyExamplesSource, "_ = inc(1)", "_ = inc(", 1)
	uri := "file:///signature_help_examples.nomi"
	s := NewServer()
	s.docs.Open(uri, input)
	lines := strings.Split(input, "\n")
	line := -1
	for i, l := range lines {
		if strings.Contains(l, "_ = inc(") {
			line = i
		}
	}
	res, err := s.textDocumentSignatureHelp(nil, &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: uint32(line), Character: uint32(len(lines[line]))},
		},
	})
	if err != nil {
		t.Fatalf("signature help error: %v", err)
	}
	if res == nil || len(res.Signatures) != 1 {
		t.Fatalf("no signature help on `inc(`: %+v", res)
	}
	mc, ok := res.Signatures[0].Documentation.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("expected MarkupContent documentation, got %T", res.Signatures[0].Documentation)
	}
	if mc.Value != "Adds one." {
		t.Fatalf("signature help documentation = %q, want only the doc", mc.Value)
	}
}

// An impl function, an interface impl reached through an interface-qualified
// call, and a type each show their own examples.
func TestHover_ImplFunctionsAndTypesShowTheirExamples(t *testing.T) {
	const source = `/// A point.
//! assert Point{x: 1}.x == 1
//
struct Point {
  x: Int
}

impl Point {
  /// The origin.
  //! assert Point.origin().x == 0
  //
  fn origin(): Point {
    Point{x: 0}
  }
}

impl Display for Point {
  //! assert Display.to_string(Point{x: 2}) == "P2"
  //
  fn to_string(p: Point): String {
    "P${p.x}"
  }
}

fn main() {
  p = Point.origin()
  _ = Display.to_string(p)
}
`
	cases := []struct {
		name, at, marked, want string
	}{
		{"inherent impl function", "p = Point.origin()", "p = Point.▮origin()",
			"The origin.\n\n**Examples**\n\n```nomi\nassert Point.origin().x == 0\n```"},
		{"interface impl function", "_ = Display.to_string(p)", "_ = Display.▮to_string(p)",
			"**Examples**\n\n```nomi\nassert Display.to_string(Point{x: 2}) == \"P2\"\n```"},
		{"interface impl function declaration", "fn to_string(p: Point)", "fn ▮to_string(p: Point)",
			"**Examples**\n\n```nomi\nassert Display.to_string(Point{x: 2}) == \"P2\"\n```"},
		{"struct", "struct Point {", "struct ▮Point {",
			"A point.\n\n**Examples**\n\n```nomi\nassert Point{x: 1}.x == 1\n```"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hoverText(t, "examples_impl", strings.Replace(source, c.at, c.marked, 1))
			if !strings.HasSuffix(got, c.want) {
				t.Fatalf("hover = \n%s\n\nwant it to end with:\n%s", got, c.want)
			}
		})
	}
}

// A parameter's hover names no declaration with tests, so it has no examples
// even inside a function that has some.
func TestHover_ParameterShowsNoExamples(t *testing.T) {
	got := hoverText(t, "examples_param", strings.Replace(manyExamplesSource, "  n + 1", "  ▮n + 1", 1))
	if strings.Contains(got, "Examples") {
		t.Fatalf("parameter hover shows examples:\n%s", got)
	}
}
