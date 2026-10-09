package analysis_test

import (
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Spec §8 *Type Narrowing*: only an embedded struct or distinct type narrows.
// Bool's `embeds True` names a host singleton, and the prelude's and a user's
// plain, payload and struct-shaped variants (a plain one named like a type
// included) have no type of their own, so a
// name matched against any of them keeps its enum type in the arm.
func TestNarrowing_APlainVariantLeavesTheEnumType(t *testing.T) {
	src := `struct Circle { radius: Float }
enum Shape {
  Dot
  Round Float
  Rect { w: Int }
  Wrapped Circle
}
enum Named {
  Circle
  Int
}
fn b(_x: Bool): Int { 1 }
fn m(_x: Maybe<Int>): Int { 2 }
fn r(_x: Result<Int, String>): Int { 3 }
fn s(_x: Shape): Int { 4 }
fn f(bx: Bool): Int {
  case bx {
    True -> b(bx)
    False -> b(bx)
  }
}
fn g(mx: Maybe<Int>): Int {
  case mx {
    Some(_) -> m(mx)
    None -> m(mx)
  }
}
fn h(rx: Result<Int, String>): Int {
  case rx {
    Ok(_) -> r(rx)
    Err(_) -> r(rx)
  }
}
fn k(sx: Shape): Int {
  case sx {
    .Dot -> s(sx)
    .Round(_) -> s(sx)
    .Rect{w: _} -> s(sx)
    .Wrapped(_) -> s(sx)
  }
}
fn i(mi: Maybe<Int>, bi: Bool): Int {
  one = if Some(_) = mi { m(mi) } else { 0 }
  two = if True = bi { b(bi) } else { 0 }
  one + two
}
fn n(_x: Named): Int { 5 }
fn l(nx: Named): Int {
  case nx {
    .Circle -> n(nx)
    .Int -> n(nx)
  }
}
fn j(bj: Bool): Bool {
  case bj {
    True -> bj
    False -> bj
  }
}`
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) > 0 {
		t.Fatalf("the front end rejects this: %v", errs)
	}
	cases := []struct {
		name  string
		want  string
		reads int
	}{
		{"bx", "Bool", 3},
		{"mx", "Maybe<Int>", 3},
		{"rx", "Result<Int, String>", 3},
		{"sx", "Shape", 5},
		{"mi", "Maybe<Int>", 2},
		{"bi", "Bool", 2},
		{"bj", "Bool", 3},
		{"nx", "Named", 3},
	}
	for _, c := range cases {
		got := readTypes(fa, c.name)
		if len(got) != c.reads {
			t.Errorf("%s: %d reads recorded, want %d", c.name, len(got), c.reads)
		}
		for pos, ty := range got {
			if ty != c.want {
				t.Errorf("%s at %s: %s, want %s", c.name, pos, ty, c.want)
			}
		}
	}
}

// readTypes is the type ExprTypes records for each read of name, keyed by
// its position.
func readTypes(fa *analysis.FileAnalysis, name string) map[string]string {
	out := map[string]string{}
	for n, ty := range fa.ExprTypes {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != name {
			continue
		}
		s := "<nil>"
		if ty != nil {
			s = ty.String()
		}
		out[fmt.Sprintf("%d:%d", id.Line, id.Col)] = s
	}
	return out
}
