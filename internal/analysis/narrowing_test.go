package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// identTypeAt is the type ExprTypes records for the identifier at line:col.
func identTypeAt(t *testing.T, fa *FileAnalysis, line, col int) Type {
	t.Helper()
	for n, ty := range fa.ExprTypes {
		if id, ok := n.(*ast.Ident); ok && id.Line == line && id.Col == col {
			return ty
		}
	}
	t.Fatalf("no type recorded for the identifier at %d:%d", line, col)
	return nil
}

// Spec §10 *Type Narrowing*: a name matched against an `embeds` variant reads
// as the embedded type inside that arm, and as the enum everywhere else.
func TestNarrowing_RecordsTheEmbeddedTypeInTheArm(t *testing.T) {
	src := `struct Circle
    radius: Float
end
type Tag String
enum Shape
    embeds Circle
    embeds Tag
    Point
end
fn take(_c: Circle) -> Int
    1
end
fn tag(_t: Tag) -> Int
    2
end
fn whole(_s: Shape) -> Int
    3
end
fn f(value: Shape) -> Int
  case value
    .Circle{radius: _} -> take(value)
    .Tag(_) -> tag(value)
    .Point -> whole(value)
  end
end
fn g(value: Shape) -> Int
  if .Circle{radius: _} = value then take(value) else whole(value)
end
fn h(value: Shape) -> Int
  case value
    .Circle{radius: _} as s -> whole(s)
    _ -> 0
  end
end`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	cases := []struct {
		line, col int
		want      string
	}{
		{21, 32, "Circle"},
		{22, 20, "Tag"},
		{23, 21, "Shape"},
		{27, 43, "Circle"},
		{27, 61, "Shape"},
		{31, 38, "Shape"},
	}
	for _, c := range cases {
		if got := identTypeAt(t, fa, c.line, c.col); got == nil || got.String() != c.want {
			t.Errorf("%d:%d: got %v, want %s", c.line, c.col, got, c.want)
		}
	}
}
