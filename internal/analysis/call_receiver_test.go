package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Every call of an interface's function records the type it is for
// (Callee.For) and how it is answered (Callee.Form): a type with no
// Debug block rendered by its shape, a receiver whose
// type holds a variable the checker closed, an operator interface's
// function called by name, a type-qualified call of an impl's function,
// a call with no argument for its receiver, and a call the checker
// selected among a type's impls at several type arguments.
func TestCallReceiver_EveryInterfaceCallRecordsItsReceiverAndForm(t *testing.T) {
	fa, errs := checkEntry(t, `interface Holds<T>
    fn holds(b: self, x: T) -> Bool

    fn holds_none(b: self, xs: List<T>) -> Bool
        Iter.all_match(xs, fn x -> not Holds.holds(b, x))
    end
end

struct Box
    n: Int
    s: String
end

impl Holds<Int> for Box
    fn holds(b: Box, x: Int) -> Bool
        b.n == x
    end
end

impl Holds<String> for Box
    fn holds(b: Box, x: String) -> Bool
        b.s == x
    end
end

fn run() -> (String, String, List<Int>, Int, Bool, String, Bool, Maybe<Int>)
    (
        Debug.inspect(None),
        Debug.inspect(Ok("s")),
        Add.add([1], [2]),
        Int.add(1, 2),
        List.equal([1], [2]),
        Int.to_string(3),
        Box.holds_none(Box{n: 1, s: "a"}, [2, 3]),
        Int.from_missing(),
    )
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	// The receiver each line's call is for, by the start of its type.
	want := map[int]string{
		28: "Maybe<",
		29: "Result<String, ",
		30: "List<Int>",
		31: "Int",
		32: "List<Int>",
		33: "Int",
		34: "Box",
		35: "Int",
	}
	// Maybe and Result have no Debug block, so their `Debug.inspect` is
	// Debug's own function answered by the type's shape, with no impl.
	byShape := map[int]bool{28: true, 29: true}
	for n, rc := range fa.Calls {
		call, ok := n.(*ast.Call)
		if !ok {
			continue
		}
		if _, qualified := call.Func.(*ast.FieldAccess); !qualified {
			continue
		}
		wantFor, listed := want[call.Line]
		if !listed {
			continue
		}
		delete(want, call.Line)
		c := rc.Callee
		if c.For == nil {
			t.Errorf("line %d: no For recorded (%+v)", call.Line, c)
			continue
		}
		if got := analysis.Elaborate(c.For).String(); !strings.HasPrefix(got, wantFor) {
			t.Errorf("line %d: For %s, want %s", call.Line, got, wantFor)
		}
		if byShape[call.Line] {
			if c.Form != analysis.OperatorStructural || c.Kind != analysis.CalleeInterface || c.Impl != nil {
				t.Errorf("line %d: kind %d form %d impl %v, want Debug's own function answered by shape", call.Line, c.Kind, c.Form, c.Impl != nil)
			}
			continue
		}
		if c.Form != analysis.OperatorByImpl || c.Kind != analysis.CalleeImpl || c.Impl == nil {
			t.Errorf("line %d: kind %d form %d impl %v, want the impl block it reaches", call.Line, c.Kind, c.Form, c.Impl != nil)
		}
	}
	for line := range want {
		t.Errorf("line %d: no qualified call found", line)
	}
}

// A use of an interface's function on a receiver whose type holds the
// generic body's type parameter (`List<T>`, a derived `Box<T>`) is
// answered at each instance, written as an operator or called by name,
// as on a type parameter itself, and the instance's answer is the impl
// the same use written at the instance's type reaches.
func TestCallReceiver_AReceiverHoldingATypeParameterIsPerInstance(t *testing.T) {
	fa, errs := checkEntry(t, `struct Box<T>
    v: T
end

derive Equatable for Box<T> where T: Equatable

fn same<T>(xs: List<T>, ys: List<T>, a: Box<T>, b: Box<T>) -> (Bool, Bool, Bool, Bool) where T: Equatable
    (
        xs == ys,
        Equatable.equal(xs, ys),
        a == b,
        Equatable.equal(a, b),
    )
end

fn run() -> (Bool, Bool, Bool, Bool)
    same([1], [1], Box{v: 1}, Box{v: 1})
end

fn written() -> (Bool, Bool)
    (Equatable.equal([1], [1]), Equatable.equal(Box{v: 1}, Box{v: 1}))
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := callsByLine(fa)
	instance := byLine[17]
	args := instance.Ref.CallTypeArgs
	by := func(p *analysis.TypeParam_) (analysis.Type, bool) {
		ty, ok := args[p.Name_]
		return ty, ok && ty != nil
	}
	var written [2]analysis.Callee
	for n, rc := range fa.Calls {
		c, ok := n.(*ast.Call)
		if !ok || c.Line != 21 || rc.Callee.For == nil {
			continue
		}
		if strings.HasPrefix(rc.Callee.For.String(), "List") {
			written[0] = rc.Callee
		} else if strings.HasPrefix(rc.Callee.For.String(), "Box") {
			written[1] = rc.Callee
		}
	}
	for i, line := range []int{10, 12} {
		var op ast.Node
		for n := range fa.Operators {
			if b, ok := n.(*ast.Binary); ok && b.Line == line-1 {
				op = n
			}
		}
		if use := fa.Operators[op]; use == nil || use.Form != analysis.OperatorPerInstance {
			t.Errorf("the operator on line %d records %+v, want per instance", line-1, use)
		}
		call := byLine[line]
		if call.Kind != analysis.CalleeInterface || call.Form != analysis.OperatorPerInstance || call.Impl != nil {
			t.Errorf("the call on line %d: kind %d form %d, want the interface's function, per instance", line, call.Kind, call.Form)
		}
		at := fa.CalleeAt(call, by)
		if at.Kind != analysis.CalleeImpl || at.Impl != written[i].Impl || at.Decl != written[i].Decl {
			t.Errorf("the call on line %d at the instance reaches %+v, want the record written at the type: %+v", line, at, written[i])
		}
		use, resolved := fa.OperatorUseAt(fa, op, by)
		if !resolved || use == nil || use.Form != analysis.OperatorByImpl || use.Impl == nil || ast.Node(use.Impl.Method) != written[i].Decl {
			t.Errorf("the operator on line %d at the instance answers %+v, want the impl the call written at the type reaches", line-1, use)
		}
	}
}
