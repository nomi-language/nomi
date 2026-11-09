package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// Source written as another language writes it is rejected with an error
// that names Nomi's spelling, and the error's fix rewrites it into source
// that parses. Each source here was rejected before these errors existed,
// so none of them changes what the parser accepts.
func TestConfusion_ParserNamesNomisSpelling(t *testing.T) {
	for _, tc := range []struct {
		name, src, message, fixed string
	}{
		{"let", "fn main() {\n    let x = 1\n    dbg x\n}\n", "Nomi has no `let`", "fn main() {\n    x = 1\n    dbg x\n}\n"},
		{"let mut", "fn main() {\n    let mut x = 1\n    dbg x\n}\n", "Nomi has no `let mut`", "fn main() {\n    x = 1\n    dbg x\n}\n"},
		{"var", "fn main() {\n    var x = 1\n    dbg x\n}\n", "Nomi has no `var`", "fn main() {\n    x = 1\n    dbg x\n}\n"},
		{"top-level const", "const limit = 3\n", "a file-level value is `once limit = ...`", "once limit = 3\n"},
		{":=", "fn main() {\n    x := 1\n    dbg x\n}\n", "not `:=`", "fn main() {\n    x = 1\n    dbg x\n}\n"},
		{"def", "def main() {\n    dbg 1\n}\n", "with `fn`, not `def`", "fn main() {\n    dbg 1\n}\n"},
		{"func", "func main() {\n    dbg 1\n}\n", "with `fn`, not `func`", "fn main() {\n    dbg 1\n}\n"},
		{"++", "fn main() {\n    x = 1\n    x++\n    dbg x\n}\n", "no `++`", "fn main() {\n    x = 1\n    x = x + 1\n    dbg x\n}\n"},
		{"+=", "fn main() {\n    x = 1\n    x += 2\n    dbg x\n}\n", "no `+=`", "fn main() {\n    x = 1\n    x = x + 2\n    dbg x\n}\n"},
		{"# comment", "fn main() {\n    # one more\n    dbg 1\n}\n", "starts with `//`", "fn main() {\n    // one more\n    dbg 1\n}\n"},
		{"match", "fn main() {\n    x = 1\n    y = match x {\n        _ -> 2\n    }\n    dbg y\n}\n", "matches with `case`", "fn main() {\n    x = 1\n    y = case x {\n        _ -> 2\n    }\n    dbg y\n}\n"},
		{"switch", "fn main() {\n    x = 1\n    switch x {\n        _ -> dbg 2\n    }\n}\n", "not `switch`", "fn main() {\n    x = 1\n    case x {\n        _ -> dbg 2\n    }\n}\n"},
		{"%Point{", "fn main() {\n    p = %Point{x: 1}\n}\n", "write `Point{...}`", "fn main() {\n    p = Point{x: 1}\n}\n"},
		{"{x: 1}", "fn main() {\n    p = {x: 1}\n}\n", "write `.{x: 1}`", "fn main() {\n    p = .{x: 1}\n}\n"},
		{"%{x: 1}", "fn main() {\n    p = %{x: 1}\n}\n", "`key => value`", "fn main() {\n    p = .{x: 1}\n}\n"},
		{`{"k": 1}`, "fn main() {\n    m = {\"a\": 1, \"b\": [2]}\n}\n", "a map literal is", "fn main() {\n    m = %{\"a\" => 1, \"b\" => [2]}\n}\n"},
		{"Map{", "fn main() {\n    m = Map{\"a\" => 1}\n}\n", "a map literal is", "fn main() {\n    m = %{\"a\" => 1}\n}\n"},
		{"#(a, b)", "fn main() {\n    t = #(1, 2)\n}\n", "a tuple is written without `#`", "fn main() {\n    t = (1, 2)\n}\n"},
		{"#(a, b) =", "fn main() {\n    #(a, b) = (1, 2)\n}\n", "a tuple is written without `#`", "fn main() {\n    (a, b) = (1, 2)\n}\n"},
		{"#(a, b) ->", "fn main() {\n    x = case (1, 2) {\n        #(a, _) -> a\n    }\n}\n", "a tuple is written without `#`", "fn main() {\n    x = case (1, 2) {\n        (a, _) -> a\n    }\n}\n"},
		{"#(Int, String)", "fn f(p: #(Int, String)) -> Int {\n    1\n}\n", "a tuple is written without `#`", "fn f(p: (Int, String)) -> Int {\n    1\n}\n"},
		{"& &1.field", "fn main() {\n    xs = [.{amount: 1}] |> Iter.map(& &1.amount) |> Iter.to_list()\n}\n", "the field accessor `.amount`", "fn main() {\n    xs = [.{amount: 1}] |> Iter.map(.amount) |> Iter.to_list()\n}\n"},
		{"& &1 * 2", "fn main() {\n    xs = [1] |> Iter.map(& &1 * 2) |> Iter.to_list()\n}\n", "holds its body in parentheses", "fn main() {\n    xs = [1] |> Iter.map(&(&1 * 2)) |> Iter.to_list()\n}\n"},
		{"& &1 in a call", "fn main() {\n    xs = Iter.reduce([1], 0, & &1 + &2)\n}\n", "holds its body in parentheses", "fn main() {\n    xs = Iter.reduce([1], 0, &(&1 + &2))\n}\n"},
		{"t.0", "fn main() {\n    t = (1, 2)\n    dbg t.0\n}\n", "write `t.first`", "fn main() {\n    t = (1, 2)\n    dbg t.first\n}\n"},
		{"t.1.0", "fn main() {\n    dbg t.1.0 + 1\n}\n", "write `t.second.first`", "fn main() {\n    dbg t.second.first + 1\n}\n"},
		{"&1.1", "fn main() {\n    xs = Iter.map(ps, &(&1.1 * 2))\n}\n", "write `&1.second`", "fn main() {\n    xs = Iter.map(ps, &(&1.second * 2))\n}\n"},
		{"accessor .1", "fn main() {\n    xs = Iter.map(ps, .1)\n}\n", "write `.second`", "fn main() {\n    xs = Iter.map(ps, .second)\n}\n"},
		{"accessor .x.2.0", "fn main() {\n    xs = Iter.map(ps, .x.2.0)\n}\n", "write `.third.first`", "fn main() {\n    xs = Iter.map(ps, .x.third.first)\n}\n"},
		{"vec!", "fn main() {\n    v = vec![1, 2]\n}\n", "Nomi has no macros", "fn main() {\n    v = [1, 2]\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			if err == nil {
				t.Fatalf("the parser accepts %q", tc.src)
			}
			var pe ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("error %v is not a ParseError", err)
			}
			if !strings.Contains(pe.Message+"\n"+strings.Join(pe.Hints, "\n"), tc.message) {
				t.Fatalf("error = %q (hints %q), want it to say %q", pe.Message, pe.Hints, tc.message)
			}
			if pe.Fix == nil || len(pe.Fix.Edits) == 0 || pe.Fix.Title == "" {
				t.Fatalf("error %q carries no titled edit", pe.Message)
			}
			got := ApplyEdits(tc.src, pe.Fix.Edits)
			if got != tc.fixed {
				t.Fatalf("fixed source:\n%s\nwant:\n%s", got, tc.fixed)
			}
			if _, err := Parse(lexer.Lex(got)); err != nil {
				t.Fatalf("the fixed source does not parse: %v", err)
			}
		})
	}
}

// A brace holding a comma list is another language's tuple or set. Which
// one is not knowable from the source, so the error names both and carries
// no fix.
func TestConfusion_BraceListNamesTupleAndSet(t *testing.T) {
	_, err := Parse(lexer.Lex("fn main() {\n    a = 1\n    p = {a, 2}\n}\n"))
	var pe ParseError
	if err == nil || !errors.As(err, &pe) {
		t.Fatalf("Parse: %v, want a ParseError", err)
	}
	if !strings.Contains(pe.Message, "`(a, b)`") || !strings.Contains(pe.Message, "`%[a, b]`") || pe.Fix != nil {
		t.Fatalf("error = %q, fix %+v; want both spellings and no fix", pe.Message, pe.Fix)
	}
}

// The checks match only what other languages write: Nomi's own forms that
// start the same way still parse.
func TestConfusion_NomisOwnFormsStillParse(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n    let = 1\n    dbg let\n}\n",
		"fn main() {\n    match = 1\n    dbg match(2)\n}\n",
		"fn main() {\n    x: Int = 1\n    dbg x\n}\n",
		"fn main() {\n    p = geo.Point{x: 1}\n}\n",
		"fn main() {\n    b = {\n        x: Int = 1\n        x\n    }\n}\n",
		"fn main() {\n    m = %{\"a\" => 1}\n    t = (1, 2)\n    g = (1)\n}\n",
		"fn f(g: (Int, String) -> Int) -> Int {\n    1\n}\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}
