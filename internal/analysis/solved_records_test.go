package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// solvedRecordAt is the type the checker recorded for the one node of type T
// on line that pick accepts, failing the test when there is not exactly one.
func solvedRecordAt[T ast.Node](t *testing.T, fa *analysis.FileAnalysis, records map[ast.Node]analysis.Type, line int, pick func(T) bool) analysis.Type {
	t.Helper()
	var found []analysis.Type
	for n, ty := range records {
		if m, ok := n.(T); ok && n.LineNum() == line && pick(m) {
			found = append(found, ty)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d nodes match on line %d, want 1", len(found), line)
	}
	return found[0]
}

// An inherited interface function called through a type, whose `self` no
// argument names (`Pot.zero()` with `fn zero() -> self`), is the function
// at that type: the call is a Pot, and a binding of it needs no annotation.
func TestSolvedRecords_ATypeQualifiedDefaultSolvesSelfFromTheType(t *testing.T) {
	fa, _, errs := checkSourceNodesWithStdlib(`interface Sized
    fn make(n: Int) -> self

    fn zero() -> self
        Sized.make(0)
    end
end

struct Pot
    n: Int
end

impl Sized for Pot
    fn make(n: Int) -> Pot
        Pot{n:}
    end
end

fn main()
    p = Pot.zero()
    _ = p.n + Pot.zero().n
end
`)
	for _, e := range errs {
		t.Errorf("%d:%d: %s", e.Line, e.Col, e.Message)
	}
	for _, line := range []int{20, 21} {
		ty := solvedRecordAt(t, fa, fa.ExprTypes, line, func(c *ast.Call) bool {
			acc, ok := c.Func.(*ast.FieldAccess)
			return ok && acc.Field.Name == "zero"
		})
		if got := analysis.ResolveTypeVar(ty); got == nil || got.String() != "Pot" {
			t.Errorf("Pot.zero() on line %d is recorded as %v, want Pot", line, ty)
		}
	}
}

// A generic checked type's constructor over a literal solves its type
// arguments from the literal: `Loud(["a"])` is a Loud<String>.
func TestSolvedRecords_AGenericCheckedConstructorSolvesFromItsLiteral(t *testing.T) {
	fa, _, errs := checkSourceNodesWithStdlib(`type Loud<T> List<T> when fn xs -> not Iter.is_empty(xs)

fn main()
    _ = Loud(["a"])
end
`)
	for _, e := range errs {
		t.Errorf("%d:%d: %s", e.Line, e.Col, e.Message)
	}
	ty := solvedRecordAt(t, fa, fa.ExprTypes, 4, func(c *ast.Call) bool {
		id, ok := c.Func.(*ast.TypeIdent)
		return ok && id.Name == "Loud"
	})
	if got := analysis.ResolveTypeVar(ty); got == nil || got.String() != "Loud<String>" {
		t.Errorf("Loud([\"a\"]) is recorded as %v, want Loud<String>", ty)
	}
}

// An `if` whose every branch traps, used where its position decides the
// type (`not (...)`), is recorded at the type the position settled, as its
// branches' `todo`s are.
func TestSolvedRecords_AnInfallibleRegionIsSettledAtItsPosition(t *testing.T) {
	fa, _, errs := checkSourceNodesWithStdlib(`fn main()
    x = not (if True then todo else todo)
    _ = x
end
`)
	for _, e := range errs {
		t.Errorf("%d:%d: %s", e.Line, e.Col, e.Message)
	}
	ty := solvedRecordAt(t, fa, fa.ExpectedTypes, 2, func(*ast.If) bool { return true })
	if ty == nil || ty.String() != "Bool" {
		t.Errorf("the if is expected at %v, want Bool", ty)
	}
}
