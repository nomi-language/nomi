package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A `// comment` after a type-body member ends the member as a newline
// does, and lands on that member's Trailing slot rather than the next
// member's LeadingComments.

func parseOne(t *testing.T, src string) ast.Node {
	t.Helper()
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parse failed: %v\n%s", err, src)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	return nodes[0]
}

func wantTrailing(t *testing.T, what string, got []ast.Trivia, text string) {
	t.Helper()
	if text == "" {
		if len(got) != 0 {
			t.Errorf("%s: want no trailing comment, got %v", what, got)
		}
		return
	}
	if len(got) != 1 || got[0].Kind != ast.TriviaComment || got[0].Text != text {
		t.Errorf("%s: want trailing %q, got %v", what, text, got)
	}
}

func wantNoLeading(t *testing.T, what string, got []ast.Trivia) {
	t.Helper()
	if len(got) != 0 {
		t.Errorf("%s: want no leading comments, got %v", what, got)
	}
}

func TestParse_EnumVariant_TrailingComment(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string // trailing comment per variant, "" for none
	}{
		{"bare", "enum Op {\n    ToJson // note\n    FromJson\n}\n", []string{"// note", ""}},
		{"bare last", "enum Op {\n    ToJson\n    FromJson // note\n}\n", []string{"", "// note"}},
		{"type payload", "enum Op {\n    Add Int // a\n    Mul (Int, Int) // m\n}\n", []string{"// a", "// m"}},
		{"paren payload", "enum Op {\n    Add(Int) // a\n    Sub(Int)\n}\n", []string{"// a", ""}},
		{"generic and function payloads", "enum Op<T> {\n    A Maybe<T> // a\n    B (T) -> T // b\n}\n", []string{"// a", "// b"}},
		{"struct payload", "enum Shape {\n    Rect {w: Int, h: Int} // r\n    Circle {r: Int} // c\n}\n", []string{"// r", "// c"}},
		{"multi-line struct payload", "enum Shape {\n    Rect {\n        w: Int\n    } // r\n    Dot\n}\n", []string{"// r", ""}},
		{"embeds", "enum E {\n    A // a\n    embeds P // p\n}\n", []string{"// a", "// p"}},
		{"semicolon", "enum Op {\n    A; B // b\n}\n", []string{"", "// b"}},
		{"doc comment", "enum Op {\n    /// doc\n    A // a\n    B\n}\n", []string{"// a", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ed, ok := parseOne(t, tc.src).(*ast.EnumDef)
			if !ok {
				t.Fatalf("want *ast.EnumDef")
			}
			if len(ed.Variants) != len(tc.want) {
				t.Fatalf("want %d variants, got %d", len(tc.want), len(ed.Variants))
			}
			for i, v := range ed.Variants {
				wantTrailing(t, v.Name, v.Trailing, tc.want[i])
				wantNoLeading(t, v.Name, v.LeadingComments)
			}
			if len(ed.EndTrivia) != 0 {
				t.Errorf("want no EndTrivia, got %v", ed.EndTrivia)
			}
		})
	}
}

// A comment on its own line after a trailing one still belongs to the next
// variant (or to the enum's EndTrivia before `}`).
func TestParse_EnumVariant_TrailingThenOwnLineComment(t *testing.T) {
	src := "enum Op {\n    A // a\n\n    // above B\n    B\n    // tail\n}\n"
	ed := parseOne(t, src).(*ast.EnumDef)
	wantTrailing(t, "A", ed.Variants[0].Trailing, "// a")
	lead := ed.Variants[1].LeadingComments
	if len(lead) != 2 || lead[0].Kind != ast.TriviaBlankLine || lead[1].Text != "// above B" {
		t.Errorf("B leading = %v", lead)
	}
	if len(ed.EndTrivia) != 1 || ed.EndTrivia[0].Text != "// tail" {
		t.Errorf("EndTrivia = %v", ed.EndTrivia)
	}
}

func TestParse_StructVariantField_TrailingComment(t *testing.T) {
	for _, src := range []string{
		"enum Shape {\n    Rect {\n        w: Int, // w\n        h: Int, // h\n        // end\n    }\n}\n",
		"enum Shape {\n    Rect {\n        w: Int // w\n        h: Int // h\n        // end\n    }\n}\n",
	} {
		ed := parseOne(t, src).(*ast.EnumDef)
		rect := ed.Variants[0]
		if len(rect.Fields) != 2 {
			t.Fatalf("want 2 fields, got %d", len(rect.Fields))
		}
		wantTrailing(t, "w", rect.Fields[0].Trailing, "// w")
		wantTrailing(t, "h", rect.Fields[1].Trailing, "// h")
		wantNoLeading(t, "h", rect.Fields[1].LeadingComments)
		if len(rect.EndTrivia) != 1 || rect.EndTrivia[0].Text != "// end" {
			t.Errorf("EndTrivia = %v", rect.EndTrivia)
		}
	}
}

func TestParse_AnonStructTypeField_TrailingComment(t *testing.T) {
	src := "fn f(p: {\n    x: Int, // x\n    y: Int // y\n}): Int {\n    1\n}\n"
	fd := parseOne(t, src).(*ast.FuncDef)
	anon, ok := fd.Params[0].TypeAnnotation.(*ast.AnonStructType)
	if !ok {
		t.Fatalf("want *ast.AnonStructType, got %T", fd.Params[0].TypeAnnotation)
	}
	wantTrailing(t, "x", anon.Fields[0].Trailing, "// x")
	wantTrailing(t, "y", anon.Fields[1].Trailing, "// y")
	wantNoLeading(t, "y", anon.Fields[1].LeadingComments)
}

func TestParse_StructField_TrailingComment(t *testing.T) {
	src := "struct P {\n    x: Int // x\n    y: Int = 1 // y\n    z: Int\n}\n"
	sd := parseOne(t, src).(*ast.StructDef)
	wantTrailing(t, "x", sd.Fields[0].Trailing, "// x")
	wantTrailing(t, "y", sd.Fields[1].Trailing, "// y")
	wantTrailing(t, "z", sd.Fields[2].Trailing, "")
	for _, f := range sd.Fields {
		wantNoLeading(t, f.Name, f.LeadingComments)
	}
}

func TestParse_InterfaceMember_TrailingComment(t *testing.T) {
	src := "interface Shape {\n    field name: String // f\n    fn area(s: self): Int // a\n    fn twice(s: self): Int {\n        2\n    } // t\n    fn last(s: self): Int\n}\n"
	id := parseOne(t, src).(*ast.InterfaceDef)
	wantTrailing(t, "name", id.Fields[0].Trailing, "// f")
	if len(id.Methods) != 3 {
		t.Fatalf("want 3 methods, got %d", len(id.Methods))
	}
	wantTrailing(t, "area", id.Methods[0].GetTrailing(), "// a")
	wantTrailing(t, "twice", id.Methods[1].GetTrailing(), "// t")
	wantTrailing(t, "last", id.Methods[2].GetTrailing(), "")
	for i := range id.Methods {
		wantNoLeading(t, id.Methods[i].Name, id.Methods[i].GetLeading())
	}
}

// Positions whose same-line comment already ended the item before type
// bodies did. They must keep parsing.
func TestParse_TrailingComment_OtherItemPositions(t *testing.T) {
	for name, src := range map[string]string{
		"import block": "import {\n    std/math // m\n    std/strings // s\n}\n",
		"binding":      "fn main() {\n    x = 1 // x\n    y: Int = x + 1 // y\n}\n",
		"case arm":     "fn f(x: Int): Int {\n    case x {\n        1 -> 2 // one\n        _ -> 3 // other\n    }\n}\n",
		"tests group":  "tests \"g\" {\n    clock Clock.Virtual // c\n    boot server.boot() // b\n    setup {name: \"x\"} // s\n\n    test \"a\", {name} {\n        assert name == \"x\" // a\n    } // end\n}\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("%s: parse failed: %v", name, err)
		}
	}
}
