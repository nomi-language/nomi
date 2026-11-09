package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A named struct is built and matched by its name, with no `#`, and every
// form of the name parses: plain, module-qualified, owner-qualified and
// dot-leading, as a literal, an update and a pattern, and in parentheses at
// the top of an `if` or `case` head.
func TestNamedLiterals_ParseWithoutTheSymbol(t *testing.T) {
	for name, src := range map[string]string{
		"literal":           "fn main() {\n    p = Point{x: 1, y: 2}\n}\n",
		"defaults":          "fn main() {\n    p = Point{}\n}\n",
		"module-qualified":  "fn main() {\n    p = geo.Point{x: 1}\n}\n",
		"variant":           "fn main() {\n    s = Shape.Circle{radius: 1.0}\n}\n",
		"dot variant":       "fn main() {\n    s: Shape = .Circle{radius: 1.0}\n}\n",
		"case arm pattern":  "fn f(s: Shape) -> Float {\n    case s {\n        .Circle{radius} -> radius\n        Shape.Square{side} -> side\n    }\n}\n",
		"binding pattern":   "fn main() {\n    Point{x, y} = p\n}\n",
		"if binding":        "fn main() {\n    if Point{x, y} = p {\n        dbg x\n    }\n}\n",
		"case head, parens": "fn main() {\n    case (Point{x: 1}) {\n        _ -> 1\n    }\n}\n",
		"if head, parens":   "fn main() {\n    if p == (Point{x: 1}) {\n        dbg 1\n    }\n}\n",
		"inside a call":     "fn main() {\n    if Iter.any_match(ps, fn q -> q == Point{x: 1}) {\n        dbg 1\n    }\n}\n",
		"head variant":      "fn main() {\n    if s == .Paid {\n        dbg 1\n    }\n}\n",
		"head is variant":   "fn main() {\n    if s is .Paid {\n        dbg 1\n    }\n}\n",
		"head type name":    "fn main() {\n    case s {\n        Paid -> 1\n    }\n}\n",
		"if binding value":  "fn main() {\n    if x = True {\n        dbg x\n    }\n}\n",
		"if variant value":  "fn main() {\n    if Bool.True = flag {\n        dbg 1\n    }\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(lexer.Lex(src)); err != nil {
				t.Fatalf("Parse(%q): %v", src, err)
			}
		})
	}
}

// point is a named literal written bare, built from two strings so a
// source rewrite that parenthesizes literals in heads leaves it bare.
const point = "Point" + "{x: 1}"

// A named literal written bare at the top of a condition head is an error
// whose fix, parentheses, gives source that parses.
func TestNamedLiterals_ErrorsCarryTheirFix(t *testing.T) {
	for _, tc := range []struct {
		name, src, message, fixed string
	}{
		{"case head", "fn main() {\n    case " + point + " {\n        _ -> 1\n    }\n}\n", "written in parentheses", "fn main() {\n    case (Point{x: 1}) {\n        _ -> 1\n    }\n}\n"},
		{"if head", "fn main() {\n    if p == " + point + " {\n        dbg 1\n    }\n}\n", "written in parentheses", "fn main() {\n    if p == (Point{x: 1}) {\n        dbg 1\n    }\n}\n"},
		{"is head", "fn main() {\n    if p is " + point + " {\n        dbg 1\n    }\n}\n", "written in parentheses", "fn main() {\n    if p is (Point{x: 1}) {\n        dbg 1\n    }\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			var pe ParseError
			if err == nil || !errors.As(err, &pe) {
				t.Fatalf("Parse(%q) = %v, want a ParseError", tc.src, err)
			}
			if !strings.Contains(pe.Message, tc.message) {
				t.Fatalf("error = %q, want it to say %q", pe.Message, tc.message)
			}
			if pe.Fix == nil || len(pe.Fix.Edits) == 0 {
				t.Fatalf("error %q carries no fix", pe.Message)
			}
			if got := ApplyEdits(tc.src, pe.Fix.Edits); got != tc.fixed {
				t.Fatalf("fixed:\n%s\nwant:\n%s", got, tc.fixed)
			}
			if _, err := Parse(lexer.Lex(tc.fixed)); err != nil {
				t.Fatalf("the fixed source does not parse: %v", err)
			}
		})
	}
}
