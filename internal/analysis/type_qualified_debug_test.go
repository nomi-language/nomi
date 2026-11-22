package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Every type implements Debug, so `T.inspect(x)` names Debug's function for
// a type with no Debug block, at module level or declared in a body, as
// `Debug.inspect(x)` does: the record is Debug's own function, answered by
// the type's shape.
// A type with a written Debug names its block's function, and a type
// declared in a body that is opaque names the name-only body its block
// holds.
func TestTypeMethod_DebugOfATypeWithNoBlockIsItsShape(t *testing.T) {
	fa, errs := checkEntry(t, `struct Point
    x: Int
end

struct Shown
    n: Int
end

impl Debug for Shown
    fn inspect(value: Shown) -> String
        "S${value.n}"
    end
end

fn shown() -> List<String>
    struct Local
        a: Int
    end

    struct Pair<T>
        a: T
    end

    opaque struct Hidden
        n: Int
    end

    [
        Point.inspect(Point{x: 0}),
        Local.inspect(Local{a: 1}),
        Pair.inspect(Pair{a: "x"}),
        Shown.inspect(Shown{n: 2}),
        Hidden.inspect(Hidden{n: 3}),
    ]
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	debug := analysis.StdType{Module: "std/debug", Name: "Debug"}
	got := map[string]analysis.Callee{}
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok && rc.Callee.Method == "inspect" {
			if acc, ok := call.Func.(*ast.FieldAccess); ok {
				if q, ok := acc.Object.(*ast.TypeIdent); ok {
					got[q.Name] = rc.Callee
				}
			}
		}
	}
	for _, name := range []string{"Point", "Local", "Pair"} {
		c := got[name]
		if c.Kind != analysis.CalleeInterface || c.Form != analysis.OperatorStructural || c.Owner.Std != debug || c.For == nil {
			t.Errorf("%s.inspect, a type with no Debug block: Kind %d Form %d Owner %v For %v, want Debug's own inspect answered by shape", name, c.Kind, c.Form, c.Owner.Std, c.For)
		}
	}
	if c := got["Shown"]; c.Kind != analysis.CalleeImpl || c.Form != analysis.OperatorByImpl || fa.CalleeInterfaceFunc(c) != debug.Func("inspect") {
		t.Errorf("Shown.inspect, a written Debug: Kind %d Form %d, want its block's function", c.Kind, c.Form)
	}
	if c := got["Hidden"]; c.Kind != analysis.CalleeImpl || c.Form != analysis.OperatorByImpl {
		t.Errorf("Hidden.inspect, an opaque type declared in a body: Kind %d Form %d, want its name-only block", c.Kind, c.Form)
	}
}

// `T.inspect` named as a value is a function of T, for a type with no
// Debug block as for an interface's inherited default
// (`Square.describe`).
func TestTypeMethod_DebugOfATypeNamedAsAValueIsAFunctionOfIt(t *testing.T) {
	fa, errs := checkEntry(t, `interface Shape
    fn area(value: self) -> Int

    open fn describe(value: self) -> String
        "area ${Shape.area(value)}"
    end
end

struct Square
    side: Int
end

impl Shape for Square
    fn area(value: Square) -> Int
        value.side * value.side
    end
end

fn shown() -> List<String>
    type Meters Int

    f = Meters.inspect
    g = Square.describe
    [f(Meters(1)), g(Square{side: 2})]
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	for _, want := range []struct {
		line, col int
		typ       string
	}{
		{22, 16, "(Meters) -> String"},
		{23, 16, "(Square) -> String"},
	} {
		ref := fa.References[analysis.Pos{Line: want.line, Col: want.col}]
		if ref == nil || ref.CallType == nil || ref.CallType.String() != want.typ {
			t.Errorf("the value at %d:%d is typed %v, want %s", want.line, want.col, ref, want.typ)
		}
	}
}

// Debug is among the interfaces that give `T.inspect` a meaning for every
// type, so an interface of the program that also declares `inspect` makes
// the type-qualified call ambiguous, and a type's own function named
// `inspect` collides with Debug's, with a Debug block or none.
func TestTypeMethod_DebugsInspectIsEveryTypesMember(t *testing.T) {
	_, errs := checkEntry(t, `interface Show
    fn inspect(value: self) -> String
end

struct P
    x: Int
end

impl Show for P
    fn inspect(value: P) -> String
        "${value.x}"
    end
end

fn shown() -> String
    P.inspect(P{x: 1})
end
`)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "type-qualified 'P.inspect(...)' is ambiguous") {
		t.Fatalf("errors %v, want the one that says P.inspect names Debug's and Show's", errs)
	}
	fa, errs := checkEntry(t, `struct Q
    x: Int
end

impl Q
    fn inspect(value: Q) -> String
        "${value.x}"
    end
end
`)
	errs = append(errs, fa.Program().Errors...)
	if len(errs) != 1 || errs[0].Message != "`Q.inspect` is already `Debug`'s `inspect` for `Q`, and Nomi does not overload, so a call `Q.inspect(...)` would name both; give this function another name" {
		t.Fatalf("errors %v, want the collision of Q's own inspect with Debug's", errs)
	}
}
