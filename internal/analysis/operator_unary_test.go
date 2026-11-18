package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A prefix `~~~` over a declared type records the BitwiseNot impl it
// calls, as a binary operator records its impl; over a type parameter it
// records the bound each instance resolves, and over an Int (a primitive)
// nothing.
func TestOperatorUses_BitwiseNotRecordsItsImpl(t *testing.T) {
	fa, errs := checkEntry(t, `type Flags Int

impl BitwiseNot<Flags> for Flags
    fn bitwise_not(value: Flags) -> Flags
        Flags(x) = value
        Flags(~~~x)
    end
end

fn flip(f: Flags) -> Flags
    ~~~f
end

fn flip_any<T>(x: T) -> T where T: BitwiseNot<T>
    ~~~x
end

fn main()
    dbg flip(Flags(1))
    dbg flip_any(Flags(2))
end
`)
	if len(errs) > 0 {
		t.Fatalf("the program is rejected: %v", errs)
	}
	// The `~~~` at each line: 6 over an Int, 11 over Flags, 15 over T.
	got := map[int]*analysis.OperatorUse{}
	nots := 0
	for n := range fa.ExprTypes {
		if u, ok := n.(*ast.Unary); ok && u.Op == "~~~" {
			nots++
			got[u.Line] = fa.Operators[u]
		}
	}
	if nots != 3 {
		t.Fatalf("found %d `~~~` operators, want 3", nots)
	}
	if use := got[6]; use != nil {
		t.Errorf("`~~~x` over an Int has a record (%+v), want none", *use)
	}
	use := got[11]
	var fd *ast.FuncDef
	if use != nil && use.Form == analysis.OperatorByImpl && use.Impl != nil {
		fd, _ = use.Impl.Method.(*ast.FuncDef)
	}
	if fd == nil || fd.Name != "bitwise_not" || fd.Line != 4 {
		t.Errorf("`~~~f` over Flags records %+v, want its impl's `fn bitwise_not`", use)
	}
	if use := got[15]; use == nil || use.Form != analysis.OperatorPerInstance || use.Bound != "BitwiseNot" {
		t.Errorf("`~~~x` over a type parameter records %+v, want its bound, BitwiseNot", use)
	}
}
