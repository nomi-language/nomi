package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A call inside a generic body on a type parameter records the interface's
// function and no impl. At an instance's type arguments (here, those a call
// of the generic records), CalleeAt answers what a call written at that
// type records: the block and the function that runs, a written `fn`, a
// generic block's, an inherited default, a `host fn`, a derived `fn` (a
// derived Comparable's), and for a function with `self` only in its
// result, the one the type names. A type argument that is an interface
// type leaves the record as it is.
func TestCalleeAt_AnswersARecordAtAnInstancesTypes(t *testing.T) {
	fa, errs := checkEntry(t, `interface Greet
    fn name(value: self) -> String

    open fn greet(value: self) -> String
        "hi " + Greet.name(value)
    end
end

interface Make
    fn make() -> self
end

struct P
    n: String
end

impl Greet for P
    fn name(p: P) -> String
        p.n
    end
end

impl Make for P
    fn make() -> P
        P{n: "made"}
    end
end

struct Q
    n: Int
end

impl Greet for Q
    fn name(q: Q) -> String
        "q"
    end
end

struct Box<A>
    v: A
end

impl Greet for Box<A>
    fn name(b: Box<A>) -> String
        "box"
    end
end

struct W
    n: Int
end

derive Comparable for W

fn named<T>(x: T) -> String where T: Greet
    T.name(x)
end

fn greeted<T>(x: T) -> String where T: Greet
    Greet.greet(x)
end

fn shown<T>(x: T) -> String where T: Display
    Display.to_string(x)
end

fn same<T>(a: T, b: T) -> Ordering where T: Comparable
    Comparable.compare(a, b)
end

fn made<T>() -> T where T: Make
    T.make()
end

fn at_p(p: P) -> String
    named(p)
end

fn at_q(q: Q) -> String
    named(q)
end

fn at_box(b: Box<Int>) -> String
    named(b)
end

fn greeted_p(p: P) -> String
    greeted(p)
end

fn shown_int() -> String
    shown(5)
end

fn same_w(w: W) -> Ordering
    same(w, w)
end

fn made_p() -> P
    made<P>()
end

fn at_iface(g: Greet) -> String
    named(g)
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := map[int]analysis.Callee{}
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok {
			byLine[call.Line] = rc.Callee
		}
	}
	const (
		namedLine   = 56
		greetedLine = 60
		shownLine   = 64
		sameLine    = 68
		madeLine    = 72
	)
	// at is the record of the call on line body at the type arguments the
	// call on line use records for its generic callee.
	at := func(body, use int) analysis.Callee {
		t.Helper()
		c, ok := byLine[body]
		if !ok || c.Kind != analysis.CalleeInterface || c.Impl != nil {
			t.Fatalf("line %d: the generic body's call is %+v, want CalleeInterface with no impl", body, c)
		}
		u, ok := byLine[use]
		if !ok || u.Ref == nil {
			t.Fatalf("line %d: no record of the generic's call", use)
		}
		args := u.Ref.CallTypeArgs
		return fa.CalleeAt(c, func(p *analysis.TypeParam_) (analysis.Type, bool) {
			ty, ok := args[p.Name_]
			return ty, ok && ty != nil
		})
	}
	impl := func(c analysis.Callee, what, recv string) analysis.Callee {
		t.Helper()
		if c.Kind != analysis.CalleeImpl {
			t.Fatalf("%s: kind %d, want CalleeImpl", what, c.Kind)
		}
		b, ok := c.Impl.(*ast.ImplBlock)
		if !ok {
			t.Fatalf("%s: Impl %T, want the impl block", what, c.Impl)
		}
		if b.Receiver.TypeString() != recv {
			t.Errorf("%s: the block is for %s, want %s", what, b.Receiver.TypeString(), recv)
		}
		if c.For == nil || analysis.ContainsTypeParam(c.For) {
			t.Errorf("%s: For %v, want the instance's type", what, c.For)
		}
		return c
	}
	fnNamed := func(c analysis.Callee, what, name string) {
		t.Helper()
		fd, ok := c.Decl.(*ast.FuncDef)
		if !ok || fd.Name != name || !c.InProgram {
			t.Errorf("%s: Decl %T (InProgram %v), want the program's fn %s", what, c.Decl, c.InProgram, name)
		}
	}
	p := impl(at(namedLine, 76), "T.name at P", "P")
	fnNamed(p, "T.name at P", "name")
	q := impl(at(namedLine, 80), "T.name at Q", "Q")
	fnNamed(q, "T.name at Q", "name")
	if p.Decl == q.Decl {
		t.Errorf("T.name reaches one function at P and at Q")
	}
	fnNamed(impl(at(namedLine, 84), "T.name at Box<Int>", "Box<A>"), "T.name at Box<Int>", "name")
	if c := impl(at(greetedLine, 88), "an inherited default at P", "P"); c.Decl.(*ast.InterfaceMethod).Name != "greet" {
		t.Errorf("an inherited default: Decl %v", c.Decl)
	}
	if c := impl(at(shownLine, 92), "a host fn at Int", "Int"); c.Std.IsZero() || c.Std.Impl == "" {
		t.Errorf("a host fn: Std %+v, want the impl's host fn", c.Std)
	} else if _, ok := c.Decl.(*ast.ExternFunc); !ok {
		t.Errorf("a host fn: Decl %T, want *ast.ExternFunc", c.Decl)
	}
	if c := impl(at(sameLine, 96), "a derived fn at W", "W"); !analysis.IsSynthesizedLine(c.Decl.(*ast.FuncDef).Line) {
		t.Errorf("a derived fn: Decl at line %d, want the synthesized fn", c.Decl.(*ast.FuncDef).Line)
	}
	fnNamed(impl(at(madeLine, 100), "self only in the result, at P", "P"), "T.make at P", "make")
	if c := at(namedLine, 104); c.Kind != analysis.CalleeInterface || c.Impl != nil {
		t.Errorf("at an interface type: kind %d, Impl %v; want the record unchanged", c.Kind, c.Impl)
	}
	// A parameter the substitution does not answer for leaves it too.
	if c := fa.CalleeAt(byLine[namedLine], func(*analysis.TypeParam_) (analysis.Type, bool) { return nil, false }); c.Kind != analysis.CalleeInterface {
		t.Errorf("with no type for T: kind %d, want CalleeInterface", c.Kind)
	}
}
