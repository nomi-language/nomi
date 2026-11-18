package lexer

import (
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// The lexer records each `$name` in the text of a quoted or triple-quoted
// string without a tag, on the token that holds the text, with the position
// of its `$`, and keeps a one-line literal's source on its opening token.
// A `$` after a `$` or a `{`, one an escape writes, one in a slot's
// expression, and one in a raw or typed literal are none.
func TestDollarRefs(t *testing.T) {
	type tok struct {
		Type    token.TokenType
		Dollars []token.DollarRef
		Source  string
	}
	ref := func(name string, line, col int) token.DollarRef {
		return token.DollarRef{Name: name, Line: line, Col: col}
	}
	cases := []struct {
		name, src string
		want      []tok
	}{
		{"a literal", `"a $x and $y_2."`, []tok{
			{token.STRING_LITERAL, []token.DollarRef{ref("x", 1, 4), ref("y_2", 1, 11)}, `"a $x and $y_2."`},
		}},
		{"none", `"$5 $ $$x {$x} \${x} \u{24}x"`, []tok{{token.STRING_LITERAL, nil, ""}}},
		{"after an escaped backslash", `"\\$x"`, []tok{
			{token.STRING_LITERAL, []token.DollarRef{ref("x", 1, 4)}, `"\\$x"`},
		}},
		{"each text of an interpolated string, and a string in its slot", `"$a ${f("$b")} $c"`, []tok{
			{token.STRING_START, []token.DollarRef{ref("a", 1, 2)}, `"$a ${f("$b")} $c"`},
			{token.STRING_LITERAL, []token.DollarRef{ref("b", 1, 10)}, `"$b"`},
			{token.STRING_END, []token.DollarRef{ref("c", 1, 16)}, ""},
		}},
		{"a triple-quoted string", "\"\"\"\n  $a ${b}\n  \\$c $d\n  \"\"\"", []tok{
			{token.TRIPLE_STRING_START, []token.DollarRef{ref("a", 2, 3)}, ""},
			{token.TRIPLE_STRING_END, []token.DollarRef{ref("d", 3, 7)}, ""},
		}},
		{"a raw string", "`$x`", []tok{{token.RAW_STRING_LITERAL, nil, ""}}},
		{"a typed literal", `Sql"$x ${y} $z"`, []tok{
			{token.TAGGED_STRING_START, nil, ""},
			{token.STRING_END, nil, ""},
		}},
		{"a raw typed literal", "Regex`$x`", []tok{{token.RAW_TAGGED_STRING_LITERAL, nil, ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []tok
			for _, tk := range Lex(tc.src) {
				switch tk.Type {
				case token.STRING_LITERAL, token.STRING_START, token.STRING_PART, token.STRING_END,
					token.TRIPLE_STRING_LITERAL, token.TRIPLE_STRING_START, token.TRIPLE_STRING_PART, token.TRIPLE_STRING_END,
					token.RAW_STRING_LITERAL, token.TAGGED_STRING_LITERAL, token.TAGGED_STRING_START, token.RAW_TAGGED_STRING_LITERAL:
					got = append(got, tok{tk.Type, tk.Dollars, tk.Source})
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}
