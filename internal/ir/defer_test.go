package ir

import (
	"strings"
	"testing"
)

// deferFunc builds `defer close(conn)` followed by its scope's run: the
// argument is evaluated, the call is moved behind a Defer, and a RunDefer
// closes the scope.
func deferFunc(runID int) (*Func, *Defer) {
	pos := shapePos()
	f := NewFunc(pos, "scoped")
	b := f.NewBlock(pos, "entry")
	arg := f.NewTemp()
	b.Append(NewString(pos, arg, "first"))
	c := NewCall(pos, f.NewTemp(), OrdinaryCall, NewSymbol("close"), arg)
	f.SetType(c.Dst(), UnitType)
	b.Append(c)
	d := b.DeferLastCall(pos, 1)
	b.Append(NewRunDefer(pos, runID))
	b.SetTerm(NewReturnUnit(pos))
	return f, d
}

func TestDefer_MovesTheCallBehindTheRegistration(t *testing.T) {
	f, d := deferFunc(1)
	if err := Lint(f); err != nil {
		t.Fatal(err)
	}
	instrs := f.Blocks()[0].Instrs()
	if len(instrs) != 3 || instrs[1] != d {
		t.Fatalf("instructions: %v", instrs)
	}
	if f.Def(d.Call().Dst()) != nil {
		t.Fatal("the deferred call still defines its destination")
	}
	if uses := d.AppendUses(nil); len(uses) != 1 || uses[0] != d.Call().Arg(0) {
		t.Fatalf("uses: %v", uses)
	}
	if got := d.String(); !strings.HasPrefix(got, "defer#1 ") || !strings.Contains(got, "close(") {
		t.Fatalf("String: %q", got)
	}
	if got := instrs[2].String(); got != "rundefer#1" {
		t.Fatalf("String: %q", got)
	}
}

func TestLint_DeferRegisteredCatchesItsPlants(t *testing.T) {
	f, _ := deferFunc(2)
	err := Lint(f)
	le, isLint := err.(*LintError)
	if !isLint || len(le.Violations) != 1 || le.Violations[0].Rule != RuleDeferRegistered {
		t.Fatalf("an unregistered run: %v", err)
	}

	pos := shapePos()
	f = NewFunc(pos, "twice")
	b := f.NewBlock(pos, "entry")
	for range 2 {
		c := NewCall(pos, f.NewTemp(), OrdinaryCall, NewSymbol("close"))
		f.SetType(c.Dst(), UnitType)
		b.Append(c)
		b.DeferLastCall(pos, 1)
	}
	b.Append(NewRunDefer(pos, 1))
	b.SetTerm(NewReturnUnit(pos))
	err = Lint(f)
	le, isLint = err.(*LintError)
	if !isLint || len(le.Violations) != 1 || le.Violations[0].Rule != RuleDeferRegistered {
		t.Fatalf("a second registration: %v", err)
	}
}

func TestLint_ADeferredOperandMustBeDefined(t *testing.T) {
	pos := shapePos()
	f := NewFunc(pos, "undefined")
	b := f.NewBlock(pos, "entry")
	b.Append(NewCall(pos, f.NewTemp(), OrdinaryCall, NewSymbol("close"), f.NewTemp()))
	b.DeferLastCall(pos, 1)
	b.Append(NewRunDefer(pos, 1))
	b.SetTerm(NewReturnUnit(pos))
	err := Lint(f)
	if err == nil || !strings.Contains(err.Error(), string(RuleTempDefinedBeforeUse)) {
		t.Fatalf("an undefined deferred operand: %v", err)
	}
}
