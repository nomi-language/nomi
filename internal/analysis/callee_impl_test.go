package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A call of an interface function whose receiver's type the checker knows
// names the impl block it reaches and the function that runs: the block's
// `fn`, its `host fn`, a derive's synthesized `fn` (a derived Comparable;
// a derived Debug, Display, Equatable or Hashable writes none and is
// answered by shape), or the interface's default the block inherits. A
// call through a type parameter names none.
func TestCalleeImpl_NamesTheBlockAndTheFunctionThatRuns(t *testing.T) {
	fa, errs := checkEntry(t, `interface Greet
    fn name(value: self) -> String

    open fn greet(value: self) -> String
        "hi " + Greet.name(value)
    end
end

struct P
    n: String
end

impl Greet for P
    fn name(p: P) -> String
        p.n
    end
end

struct W
    n: Int
end

derive Comparable for W

fn written(p: P) -> String
    Greet.name(p)
end

fn inherited(p: P) -> String
    Greet.greet(p)
end

fn host() -> String
    Display.to_string(5)
end

fn derived(w: W) -> Ordering
    Comparable.compare(w, w)
end

fn typed(p: P) -> String
    P.name(p)
end

fn generic<T>(x: T) -> String where T: Greet
    T.name(x)
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := map[int]analysis.Callee{}
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok && call.Line >= 25 {
			byLine[call.Line] = rc.Callee
		}
	}
	impl := func(line int, what string) analysis.Callee {
		t.Helper()
		c := byLine[line]
		if c.Kind != analysis.CalleeImpl {
			t.Fatalf("%s: kind %d, want CalleeImpl", what, c.Kind)
		}
		if _, ok := c.Impl.(*ast.ImplBlock); !ok {
			t.Fatalf("%s: Impl %T, want the impl block", what, c.Impl)
		}
		return c
	}
	if c := impl(26, "a written fn"); c.Decl.(*ast.FuncDef).Name != "name" || !c.InProgram {
		t.Errorf("a written fn: Decl %v, InProgram %v", c.Decl, c.InProgram)
	}
	if c := impl(30, "an inherited default"); c.Decl.(*ast.InterfaceMethod).Name != "greet" || c.For == nil || c.For.String() != "P" {
		t.Errorf("an inherited default: Decl %T, For %v", c.Decl, c.For)
	}
	if c := impl(34, "a host fn"); c.Std.IsZero() || c.Std.Impl == "" {
		t.Errorf("a host fn: Std %+v, want the impl's host fn", c.Std)
	} else if _, ok := c.Decl.(*ast.ExternFunc); !ok {
		t.Errorf("a host fn: Decl %T, want *ast.ExternFunc", c.Decl)
	}
	if c := impl(38, "a derived fn"); !analysis.IsSynthesizedLine(c.Decl.(*ast.FuncDef).Line) {
		t.Errorf("a derived fn: Decl at line %d, want the synthesized fn", c.Decl.(*ast.FuncDef).Line)
	}
	if c := impl(42, "a type-qualified call"); c.Decl.(*ast.FuncDef).Name != "name" {
		t.Errorf("a type-qualified call: Decl %v", c.Decl)
	}
	if c := byLine[46]; c.Kind != analysis.CalleeInterface || c.Impl != nil {
		t.Errorf("a call through a type parameter: kind %d, Impl %v; want CalleeInterface and none", c.Kind, c.Impl)
	}
}
