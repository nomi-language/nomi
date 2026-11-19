package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// An operator over a type parameter records OperatorPerInstance and its
// bound. At an instance's type arguments OperatorUseAt answers the record an
// operator written at that type has: the impl's function (written or
// derived, or std's for a primitive), a structural comparison for a type
// that meets Equatable with no impl, and dispatch for an interface value.
func TestOperatorUseAt_AnswersARecordAtAnInstancesTypes(t *testing.T) {
	fa, errs := checkEntry(t, `interface Named
    fn name(value: self) -> String
end

struct P
    key: Int
end

impl Comparable for P
    fn compare(a: P, b: P) -> Ordering
        Int.compare(a.key, b.key)
    end
end

struct W
    n: Int
end

derive Equatable for W

struct S
    n: Int
end

fn bigger<T>(a: T, b: T) -> Bool where T: Comparable
    a > b
end

fn same<T>(a: T, b: T) -> Bool where T: Equatable
    a == b
end

fn at_p(p: P) -> Bool
    bigger(p, p)
end

fn at_int() -> Bool
    bigger(1, 2)
end

fn at_w(w: W) -> Bool
    same(w, w)
end

fn at_s(s: S) -> Bool
    same(s, s)
end

fn at_named(n: Named) -> Bool
    same(n, n)
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	ops := map[int]ast.Node{}
	for n := range fa.Operators {
		if b, ok := n.(*ast.Binary); ok {
			ops[b.Line] = b
		}
	}
	calls := map[int]*analysis.ResolvedCall{}
	for n, rc := range fa.Calls {
		if c, ok := n.(*ast.Call); ok {
			calls[c.Line] = rc
		}
	}
	const biggerLine, sameLine = 26, 30
	at := func(op, use int) (*analysis.OperatorUse, bool) {
		t.Helper()
		n := ops[op]
		if u := fa.Operators[n]; n == nil || u == nil || u.Form != analysis.OperatorPerInstance {
			t.Fatalf("line %d: the generic body's operator is %+v, want OperatorPerInstance", op, u)
		}
		rc := calls[use]
		if rc == nil || rc.Callee.Ref == nil {
			t.Fatalf("line %d: no record of the generic's call", use)
		}
		args := rc.Callee.Ref.CallTypeArgs
		return fa.OperatorUseAt(fa, n, func(p *analysis.TypeParam_) (analysis.Type, bool) {
			ty, ok := args[p.Name_]
			return ty, ok && ty != nil
		})
	}
	byImpl := func(what string) func(*analysis.OperatorUse, bool) ast.Node {
		return func(use *analysis.OperatorUse, resolved bool) ast.Node {
			t.Helper()
			if !resolved || use == nil || use.Form != analysis.OperatorByImpl || use.Impl == nil {
				t.Fatalf("%s: %+v (resolved %v), want OperatorByImpl", what, use, resolved)
			}
			return use.Impl.Method
		}
	}
	if fd, ok := byImpl("> at P")(at(biggerLine, 34)).(*ast.FuncDef); !ok || fd.Name != "compare" || analysis.IsSynthesizedLine(fd.Line) {
		t.Errorf("> at P: the impl's function is %v, want P's written compare", fd)
	}
	if m := byImpl("> at Int")(at(biggerLine, 38)); fa.StdFuncOf(m).IsZero() {
		t.Errorf("> at Int: the impl's function is %v, want std's compare for Int", m)
	}
	if fd, ok := byImpl("== at W")(at(sameLine, 42)).(*ast.FuncDef); !ok || !analysis.IsSynthesizedLine(fd.Line) {
		t.Errorf("== at W: the impl's function is %v, want the derived equal", fd)
	}
	if use, resolved := at(sameLine, 46); !resolved || use == nil || use.Form != analysis.OperatorStructural {
		t.Errorf("== at S: %+v (resolved %v), want OperatorStructural", use, resolved)
	}
	if use, resolved := at(sameLine, 50); !resolved || use == nil || use.Form != analysis.OperatorDispatched {
		t.Errorf("== at an interface value: %+v (resolved %v), want OperatorDispatched", use, resolved)
	}
	if use, resolved := fa.OperatorUseAt(fa, ops[sameLine], func(*analysis.TypeParam_) (analysis.Type, bool) { return nil, false }); resolved || use.Form != analysis.OperatorPerInstance {
		t.Errorf("with no type for T: %+v (resolved %v), want the record unchanged", use, resolved)
	}
}

// Inside an interface default, `self` is the implementor, which each
// instance of the default binds: an operator over it records
// OperatorPerInstance and its bound, as one over a type parameter does,
// and OperatorUseAt answers it at the implementor. A value annotated with
// the interface's name is an interface value, which dispatches.
func TestOperatorUseAt_ADefaultsSelfIsAnsweredAtTheImplementor(t *testing.T) {
	fa, errs := checkEntry(t, `interface Scale
    fn bigger(a: self, b: self) -> Bool where self: Comparable and Equatable
        a > b and a != b
    end
end

fn same(a: Scale, b: Scale) -> Bool
    a == b
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	forms := map[string]*analysis.OperatorUse{}
	for n, use := range fa.Operators {
		if b, ok := n.(*ast.Binary); ok {
			forms[b.Op] = use
		}
	}
	for op, bound := range map[string]string{">": "Comparable", "!=": "Equatable"} {
		if use := forms[op]; use == nil || use.Form != analysis.OperatorPerInstance || use.Bound != bound {
			t.Errorf("%s over a default's self: %+v, want OperatorPerInstance through %s", op, use, bound)
		}
	}
	if use := forms["=="]; use == nil || use.Form != analysis.OperatorDispatched {
		t.Errorf("== over two interface values: %+v, want OperatorDispatched", use)
	}
}
