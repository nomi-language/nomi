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

// Spec §8 *Type Narrowing*: a name matched against an `embeds` variant reads
// as the embedded type inside that arm, and as the enum everywhere else.
func TestNarrowing_RecordsTheEmbeddedTypeInTheArm(t *testing.T) {
	src := `struct Circle { radius: Float }
type Tag String
enum Shape { embeds Circle; embeds Tag; Point }
fn take(_c: Circle): Int { 1 }
fn tag(_t: Tag): Int { 2 }
fn whole(_s: Shape): Int { 3 }
fn f(value: Shape): Int {
  case value {
    .Circle{radius: _} -> take(value)
    .Tag(_) -> tag(value)
    .Point -> whole(value)
  }
}
fn g(value: Shape): Int {
  if .Circle{radius: _} = value { take(value) } else { whole(value) }
}
fn h(value: Shape): Int {
  case value {
    .Circle{radius: _} as s -> whole(s)
    _ -> 0
  }
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	cases := []struct {
		line, col int
		want      string
	}{
		{9, 32, "Circle"},
		{10, 20, "Tag"},
		{11, 21, "Shape"},
		{15, 40, "Circle"},
		{15, 62, "Shape"},
		{19, 38, "Shape"},
	}
	for _, c := range cases {
		if got := identTypeAt(t, fa, c.line, c.col); got == nil || got.String() != c.want {
			t.Errorf("%d:%d: got %v, want %s", c.line, c.col, got, c.want)
		}
	}
}
