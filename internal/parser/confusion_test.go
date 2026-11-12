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
		{"let", "fn main()\n    let x = 1\n    dbg x\nend\n", "Nomi has no `let`", "fn main()\n    x = 1\n    dbg x\nend\n"},
		{"let mut", "fn main()\n    let mut x = 1\n    dbg x\nend\n", "Nomi has no `let mut`", "fn main()\n    x = 1\n    dbg x\nend\n"},
		{"var", "fn main()\n    var x = 1\n    dbg x\nend\n", "Nomi has no `var`", "fn main()\n    x = 1\n    dbg x\nend\n"},
		{"top-level const", "const limit = 3\n", "a file-level value is `once limit = ...`", "once limit = 3\n"},
		{":=", "fn main()\n    x := 1\n    dbg x\nend\n", "not `:=`", "fn main()\n    x = 1\n    dbg x\nend\n"},
		{"def", "def main()\n    dbg 1\nend\n", "with `fn`, not `def`", "fn main()\n    dbg 1\nend\n"},
		{"func", "func main()\n    dbg 1\nend\n", "with `fn`, not `func`", "fn main()\n    dbg 1\nend\n"},
		{"++", "fn main()\n    x = 1\n    x++\n    dbg x\nend\n", "no `++`", "fn main()\n    x = 1\n    x = x + 1\n    dbg x\nend\n"},
		{"+=", "fn main()\n    x = 1\n    x += 2\n    dbg x\nend\n", "no `+=`", "fn main()\n    x = 1\n    x = x + 2\n    dbg x\nend\n"},
		{"# comment", "fn main()\n    # one more\n    dbg 1\nend\n", "starts with `//`", "fn main()\n    // one more\n    dbg 1\nend\n"},
		{"match", "fn main()\n    x = 1\n    y = match x\n        _ -> 2\n    end\n    dbg y\nend\n", "matches with `case`", "fn main()\n    x = 1\n    y = case x\n        _ -> 2\n    end\n    dbg y\nend\n"},
		{"switch", "fn main()\n    x = 1\n    switch x\n        _ -> dbg 2\n    end\nend\n", "not `switch`", "fn main()\n    x = 1\n    case x\n        _ -> dbg 2\n    end\nend\n"},
		{"%Point{", "fn main()\n    p = %Point{x: 1}\nend\n", "write `Point{...}`", "fn main()\n    p = Point{x: 1}\nend\n"},
		{".{x: 1}", "fn main()\n    p = .{x: 1}\nend\n", "with its brace alone", "fn main()\n    p = {x: 1}\nend\n"},
		{"%{x: 1}", "fn main()\n    p = %{x: 1}\nend\n", "`{x: 1}` for a struct", "fn main()\n    p = {x: 1}\nend\n"},
		{`{"k": 1}`, "fn main()\n    m = {\"a\": 1, \"b\": [2]}\nend\n", "a map literal is", "fn main()\n    m = {\"a\" => 1, \"b\" => [2]}\nend\n"},

		{"#(a, b)", "fn main()\n    t = #(1, 2)\nend\n", "a tuple is written without `#`", "fn main()\n    t = (1, 2)\nend\n"},
		{"#(a, b) =", "fn main()\n    #(a, b) = (1, 2)\nend\n", "a tuple is written without `#`", "fn main()\n    (a, b) = (1, 2)\nend\n"},
		{"#(a, b) ->", "fn main()\n    x = case (1, 2)\n        #(a, _) -> a\n    end\nend\n", "a tuple is written without `#`", "fn main()\n    x = case (1, 2)\n        (a, _) -> a\n    end\nend\n"},
		{"#(Int, String)", "fn f(p: #(Int, String)) -> Int\n    1\nend\n", "a tuple is written without `#`", "fn f(p: (Int, String)) -> Int\n    1\nend\n"},
		{"& &1.field", "fn main()\n    xs = [{amount: 1}] |> Iter.map(& &1.amount) |> Iter.to_list()\nend\n", "the field accessor `.amount`", "fn main()\n    xs = [{amount: 1}] |> Iter.map(.amount) |> Iter.to_list()\nend\n"},
		{"& &1 * 2", "fn main()\n    xs = [1] |> Iter.map(& &1 * 2) |> Iter.to_list()\nend\n", "holds its body in parentheses", "fn main()\n    xs = [1] |> Iter.map(&(&1 * 2)) |> Iter.to_list()\nend\n"},
		{"& &1 in a call", "fn main()\n    xs = Iter.reduce([1], 0, & &1 + &2)\nend\n", "holds its body in parentheses", "fn main()\n    xs = Iter.reduce([1], 0, &(&1 + &2))\nend\n"},
		{"t.0", "fn main()\n    t = (1, 2)\n    dbg t.0\nend\n", "write `t.first`", "fn main()\n    t = (1, 2)\n    dbg t.first\nend\n"},
		{"t.1.0", "fn main()\n    dbg t.1.0 + 1\nend\n", "write `t.second.first`", "fn main()\n    dbg t.second.first + 1\nend\n"},
		{"&1.1", "fn main()\n    xs = Iter.map(ps, &(&1.1 * 2))\nend\n", "write `&1.second`", "fn main()\n    xs = Iter.map(ps, &(&1.second * 2))\nend\n"},
		{"accessor .1", "fn main()\n    xs = Iter.map(ps, .1)\nend\n", "write `.second`", "fn main()\n    xs = Iter.map(ps, .second)\nend\n"},
		{"accessor .x.2.0", "fn main()\n    xs = Iter.map(ps, .x.2.0)\nend\n", "write `.third.first`", "fn main()\n    xs = Iter.map(ps, .x.third.first)\nend\n"},
		{"vec!", "fn main()\n    v = vec![1, 2]\nend\n", "Nomi has no macros", "fn main()\n    v = [1, 2]\nend\n"},
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
	_, err := Parse(lexer.Lex("fn main()\n    a = 1\n    p = {1, a}\nend\n"))
	var pe ParseError
	if err == nil || !errors.As(err, &pe) {
		t.Fatalf("Parse: %v, want a ParseError", err)
	}
	if !strings.Contains(pe.Message, "`(a, b)`") || !strings.Contains(pe.Message, "`Set[a, b]`") || pe.Fix != nil {
		t.Fatalf("error = %q, fix %+v; want both spellings and no fix", pe.Message, pe.Fix)
	}
}

// The checks match only what other languages write: Nomi's own forms that
// start the same way still parse.
func TestConfusion_NomisOwnFormsStillParse(t *testing.T) {
	for _, src := range []string{
		"fn main()\n    let = 1\n    dbg let\nend\n",
		"fn main()\n    match = 1\n    dbg match(2)\nend\n",
		"fn main()\n    x: Int = 1\n    dbg x\nend\n",
		"fn main()\n    p = geo.Point{x: 1}\nend\n",
		"fn main()\n    b = begin\n        x: Int = 1\n        x\n    end\nend\n",
		"fn main()\n    m = {\"a\" => 1}\n    t = (1, 2)\n    g = (1)\nend\n",
		"fn f(g: (Int, String) -> Int) -> Int\n    1\nend\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}

// A type with arguments written as a call's owner, `List<String>.from_json(json)`,
// reads as comparisons. The error names it at the `<` and its help gives the
// call Nomi writes, built from the source's names; a from_json call's fix
// rewrites it to the interface-qualified call with a type argument.
func TestTypeOwnerCall_NamesTheTypeArgumentForm(t *testing.T) {
	for _, tc := range []struct {
		name, src, message, help, fixed string
	}{
		{"List<String>.from_json",
			"fn f(json: Json)\n    tags = List<String>.from_json(json)\nend\n",
			"`List<String>.from_json(...)` names a type with arguments as a call's owner, which Nomi reads as comparisons",
			"name the target as a type argument, `FromJson.from_json<List<String>>(json)`, or annotate the result: `value: Result<List<String>, Json.ShapeError> = FromJson.from_json(json)`",
			"fn f(json: Json)\n    tags = FromJson.from_json<List<String>>(json)\nend\n"},
		{"Map<String, Int>.from_json",
			"fn f(j: Json)\n    m = Map<String, Int>.from_json(j)\nend\n",
			"`Map<String, Int>.from_json(...)` names a type with arguments as a call's owner, which Nomi reads as comparisons",
			"name the target as a type argument, `FromJson.from_json<Map<String, Int>>(j)`, or annotate the result: `value: Result<Map<String, Int>, Json.ShapeError> = FromJson.from_json(j)`",
			"fn f(j: Json)\n    m = FromJson.from_json<Map<String, Int>>(j)\nend\n"},
		{"Box<Int>.new",
			"fn f()\n    b = Box<Int>.new(1)\nend\n",
			"`Box<Int>.new(...)` names a type with arguments as a call's owner, which Nomi reads as comparisons",
			"name the type without its arguments, `Box.new(1)`, and annotate the result as `Box<Int>` if they are not inferred",
			""},
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
			if pe.Message != tc.message {
				t.Fatalf("message = %q, want %q", pe.Message, tc.message)
			}
			if len(pe.Hints) != 1 || pe.Hints[0] != tc.help {
				t.Fatalf("hints = %q, want [%q]", pe.Hints, tc.help)
			}
			if tc.fixed == "" {
				if pe.Fix != nil {
					t.Fatalf("fix %+v, want none", pe.Fix)
				}
				return
			}
			if pe.Fix == nil {
				t.Fatalf("no fix")
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

// Comparisons of values keep parsing as comparisons, `.name(...)` after the
// `>` included, and a type argument on a call is still a call.
func TestTypeOwnerCall_LeavesComparisonsAlone(t *testing.T) {
	for _, src := range []string{
		"fn f(a: Int, b: Int, c: Int) -> Bool\n    a < b and b > c\nend\n",
		"fn f(a: Int, b: Int, c: Int) -> Bool\n    a < b > .c(x)\nend\n",
		"fn f(a: Int, b: Int) -> Bool\n    a < b and b > .first(p)\nend\n",
		"fn f(json: Json)\n    tags = FromJson.from_json<List<String>>(json)\nend\n",
		"fn f()\n    x = Some<Int>(42)\nend\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}
