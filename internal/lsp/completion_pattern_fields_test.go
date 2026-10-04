package lsp

import (
	"slices"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const patternDecls = `struct Point {
    x: Int
    y: Int
    z: Int = 0
}

struct Circle {
    radius: Float
}

enum Shape {
    Dot
    Ring(Float)
    Rect{w: Float, h: Float}
    embeds Circle
}
`

func TestCompletion_StructPatternOffersUnwrittenFields(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string // exactly, in order
	}{
		{"case arm", "fn f(p: Point): Int {\n    case p {\n        Point{‸} -> 1\n    }\n}\n", []string{"x", "y", "z"}},
		{"case arm after a punned field", "fn f(p: Point): Int {\n    case p {\n        Point{x, ‸} -> 1\n    }\n}\n", []string{"y", "z"}},
		{"case arm after a matched field", "fn f(p: Point): Int {\n    case p {\n        Point{x: 0, ‸} -> 1\n        _ -> 0\n    }\n}\n", []string{"y", "z"}},
		{"case arm, unclosed", "fn f(p: Point): Int {\n    case p {\n        Point{y, ‸\n    }\n}\n", []string{"x", "z"}},
		{"nested, unclosed", "fn f(m: Maybe<Point>): Int {\n    case m {\n        .Some(Point{x, ‸\n    }\n}\n", []string{"y", "z"}},
		{"anonymous pattern in a case arm", "fn f(p: Point): Int {\n    case p {\n        {x, ‸} -> 1\n    }\n}\n", []string{"y", "z"}},
		{"dot struct variant", "fn f(s: Shape): Int {\n    case s {\n        .Rect{‸} -> 1\n        _ -> 0\n    }\n}\n", []string{"w", "h"}},
		{"qualified struct variant", "fn f(s: Shape): Int {\n    case s {\n        Shape.Rect{w, ‸} -> 1\n        _ -> 0\n    }\n}\n", []string{"h"}},
		{"embedded struct variant", "fn f(s: Shape): Int {\n    case s {\n        .Circle{‸} -> 1\n        _ -> 0\n    }\n}\n", []string{"radius"}},
		{"nested in a variant payload", "fn f(m: Maybe<Point>): Int {\n    case m {\n        .Some(Point{x, ‸}) -> 1\n        _ -> 0\n    }\n}\n", []string{"y", "z"}},
		{"anonymous, nested in a generic payload", "fn f(m: Maybe<Point>): Int {\n    case m {\n        .Some({‸}) -> 1\n        _ -> 0\n    }\n}\n", []string{"x", "y", "z"}},
		{"in a tuple", "fn f(t: (Point, Int)): Int {\n    case t {\n        ({y, ‸}, _) -> 1\n    }\n}\n", []string{"x", "z"}},
		{"destructuring binding", "fn f(p: Point): Int {\n    Point{x, ‸} = p\n    x\n}\n", []string{"y", "z"}},
		{"anonymous destructuring binding", "fn f(p: Point): Int {\n    {‸} = p\n    1\n}\n", []string{"x", "y", "z"}},
		{"binding with else", "fn f(s: Shape): Float {\n    .Rect{‸} = s else {\n        return 0.0\n    }\n    1.0\n}\n", []string{"w", "h"}},
		{"assert pattern", "fn f(p: Point): Result<Int, AssertionFailure> {\n    assert Point{z, ‸} = p\n    Ok(1)\n}\n", []string{"x", "y"}},
		{"function parameter", "fn f(Point{x, ‸}): Int {\n    x\n}\n", []string{"y", "z"}},
		{"lambda parameter", "fn f(ps: List<Point>): Int {\n    g = |Point{‸}| 1\n    0\n}\n", []string{"x", "y", "z"}},
		{"typed prefix", "fn f(p: Point): Int {\n    case p {\n        Point{x, y‸} -> 1\n    }\n}\n", []string{"y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, patternDecls+"\n"+tt.src)
			got := itemLabels(items)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("labels = %v, want %v", got, tt.want)
			}
			for _, it := range items {
				if it.Kind == nil || *it.Kind != protocol.CompletionItemKindField {
					t.Errorf("%s: kind %v, want Field", it.Label, it.Kind)
				}
				if te, ok := it.TextEdit.(protocol.TextEdit); !ok || te.NewText != it.Label {
					t.Errorf("%s inserts %#v, want the bare name", it.Label, it.TextEdit)
				}
			}
		})
	}
}

// The `{` after a pattern's type name opens the list, as it does for a
// struct literal.
func TestCompletion_StructPatternOpensOnBrace(t *testing.T) {
	src := patternDecls + "\nfn f(p: Point): Int {\n    case p {\n        Point{‸} -> 1\n    }\n}\n"
	content, pos := splitCursor(t, src)
	s := NewServer()
	uri := "file:///pattern_brace.nomi"
	s.docs.Open(uri, content)
	trigger := "{"
	res, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
		Context: &protocol.CompletionContext{TriggerKind: protocol.CompletionTriggerKindTriggerCharacter, TriggerCharacter: &trigger},
	})
	if err != nil {
		t.Fatal(err)
	}
	labelsInclude(t, completionItemsOf(t, res), "x", "y", "z")
}
