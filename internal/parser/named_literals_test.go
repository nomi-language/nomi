package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A named struct is built and matched by its name, with no `#`, and every
// form of the name parses: plain, module-qualified, owner-qualified and
// dot-leading, as a literal, an update and a pattern, and at the top of an
// `if` or `case` head, bare or in parentheses: no `{` opens a body there.
func TestNamedLiterals_ParseWithoutTheSymbol(t *testing.T) {
	for name, src := range map[string]string{
		"literal":           "fn main()\n    p = Point{x: 1, y: 2}\nend\n",
		"defaults":          "fn main()\n    p = Point{}\nend\n",
		"module-qualified":  "fn main()\n    p = geo.Point{x: 1}\nend\n",
		"variant":           "fn main()\n    s = Shape.Circle{radius: 1.0}\nend\n",
		"dot variant":       "fn main()\n    s: Shape = .Circle{radius: 1.0}\nend\n",
		"case arm pattern":  "fn f(s: Shape) -> Float\n    case s\n        .Circle{radius:} -> radius\n        Shape.Square{side:} -> side\n    end\nend\n",
		"binding pattern":   "fn main()\n    Point{x:, y:} = p\nend\n",
		"if binding":        "fn main()\n    if Point{x:, y:} = p then\n        dbg x\n    end\nend\n",
		"case head, bare":   "fn main()\n    case Point{x: 1}\n        _ -> 1\n    end\nend\n",
		"if head, bare":     "fn main()\n    if p == Point{x: 1} then\n        dbg 1\n    end\nend\n",
		"is head, bare":     "fn main()\n    if p is Point{x:} then\n        dbg x\n    end\nend\n",
		"case head, parens": "fn main()\n    case (Point{x: 1})\n        _ -> 1\n    end\nend\n",
		"if head, parens":   "fn main()\n    if p == (Point{x: 1}) then\n        dbg 1\n    end\nend\n",
		"inside a call":     "fn main()\n    if Iter.any_match(ps, fn q -> q == Point{x: 1}) then\n        dbg 1\n    end\nend\n",
		"head variant":      "fn main()\n    if s == .Paid then\n        dbg 1\n    end\nend\n",
		"head is variant":   "fn main()\n    if s is .Paid then\n        dbg 1\n    end\nend\n",
		"head type name":    "fn main()\n    case s\n        Paid -> 1\n    end\nend\n",
		"if binding value":  "fn main()\n    if x = True then\n        dbg x\n    end\nend\n",
		"if variant value":  "fn main()\n    if Bool.True = flag then\n        dbg 1\n    end\nend\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(lexer.Lex(src)); err != nil {
				t.Fatalf("Parse(%q): %v", src, err)
			}
		})
	}
}
