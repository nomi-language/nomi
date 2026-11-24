package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A `dbg` renders its operand as `Debug.inspect` does, and the checker
// records it as that use (FileAnalysis.Operators), by the rule a written
// `Debug.inspect(e)` is answered by: an impl at a scalar's type and at a
// struct's with a written Debug, by shape at a tuple's, at a struct's with
// a derived Debug (whose block writes no function) and at one with no Debug
// block, by dispatch at an interface value's, per instance at a type
// parameter's, and for a pipe stage's bare `dbg` at the type of the value
// piped in.
func TestOperatorUses_ADbgIsTheDebugUseAtItsOperand(t *testing.T) {
	fa, errs := checkEntry(t, `struct P
    x: Int
end

interface Named
    fn name(value: self) -> String
end

impl Named for P
    fn name(_: P) -> String
        "p"
    end
end

fn show<T>(x: T) -> T
    dbg x
end

fn main()
    n: Named = P{x: 1}
    dbg 1
    dbg P{x: 2}
    dbg (1, "a")
    dbg n
    _ = show(3)
    _ = P{x: 3} |> dbg
end

impl Debug for P
    fn inspect(_: P) -> String
        "P"
    end
end

struct Q
    y: Int
end

fn bare()
    dbg Q{y: 1}
end

struct R
    z: Int
end

derive Debug for R

fn derived()
    dbg R{z: 1}
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	want := map[int]analysis.OperatorForm{
		16: analysis.OperatorPerInstance,
		21: analysis.OperatorByImpl,
		22: analysis.OperatorByImpl,
		23: analysis.OperatorStructural,
		24: analysis.OperatorDispatched,
		26: analysis.OperatorByImpl,
		40: analysis.OperatorStructural,
		50: analysis.OperatorStructural,
	}
	got := map[int]*analysis.OperatorUse{}
	for n, use := range fa.Operators {
		if d, ok := n.(*ast.Dbg); ok {
			got[d.Line] = use
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d `dbg` records, want %d", len(got), len(want))
	}
	for line, form := range want {
		use := got[line]
		switch {
		case use == nil:
			t.Errorf("line %d: the `dbg` has no record", line)
		case use.Form != form:
			t.Errorf("line %d: Form %d, want %d", line, use.Form, form)
		case form == analysis.OperatorByImpl && (use.Impl == nil || use.Impl.Interface != "Debug"):
			t.Errorf("line %d: the record names %+v, want an impl of Debug", line, use.Impl)
		}
	}
	if use := got[26]; use != nil && use.Impl != nil && use.Impl.Receiver.String() != "P" {
		t.Errorf("the pipe stage's `dbg` is answered at %s, want the piped P", use.Impl.Receiver)
	}
}
