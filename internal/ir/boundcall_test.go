package ir_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// boundCallModule is a module of one function, `read`, whose body calls
// FromSql.from_sql through a type parameter's bound (ir.NewBoundCall): what
// an abstract body of `fn read<T>(v: Value) -> T where T: FromSql` holds.
func boundCallModule(abstract bool) *ir.Module {
	at := func(line int) ir.Pos { return ir.At("bound.nomi", line, 1) }
	m := ir.NewModule("bound.nomi")
	if abstract {
		m.SetAbstract()
	}
	f := ir.NewFuncFor(at(1), ir.NewLinkedSymbol("read", "bound.read"))
	v := f.AddParam(ir.NewSymbol("v"), ir.ValInt)
	f.SetType(v, ir.IntType)
	b := f.NewBlock(at(1), "entry")
	got := f.NewTemp()
	b.Append(ir.NewBoundCall(at(2), got, ir.OrdinaryCall, ir.NewLinkedSymbol("FromSql.from_sql", "std.FromSql.from_sql"), v))
	f.SetType(got, ir.IntType)
	b.SetTerm(ir.NewReturn(at(3), got))
	m.AddFunc(f)
	return m
}

func TestBoundCall_IsAFormOfItsOwn(t *testing.T) {
	c := ir.NewBoundCall(callPos(), 1, ir.OrdinaryCall, ir.NewTable().Symbol("FromSql.from_sql", "FromSql.from_sql"), 2)
	if c.Form() != ir.CalleeBound || c.KeyAt() != -1 || c.Fn() != ir.NoTemp {
		t.Fatalf("a bound call has form %v, key %d, callee operand %v; want bound, -1, none", c.Form(), c.KeyAt(), c.Fn())
	}
	if got, want := c.String(), "t1 = call bound FromSql.from_sql(t2)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// Lint admits a bound call only in an abstract module, and the plant beside
// the refusal shows the rule is the module's mark and nothing else.
func TestBoundCall_LintAdmitsItOnlyInAnAbstractModule(t *testing.T) {
	if err := ir.LintModule(boundCallModule(true)); err != nil {
		t.Fatalf("lint refuses a bound call in an abstract module: %v", err)
	}
	err := ir.LintModule(boundCallModule(false))
	var lerr *ir.LintError
	if !errors.As(err, &lerr) || len(lerr.Violations) != 1 || lerr.Violations[0].Rule != ir.RuleBoundCallAbstract {
		t.Fatalf("lint of a bound call in a runnable module = %v, want one %q violation", err, ir.RuleBoundCallAbstract)
	}
	if !strings.Contains(err.Error(), "no machine runs") {
		t.Fatalf("the violation does not say why: %v", err)
	}
	if err := ir.LintModuleAdded(boundCallModule(false)); err == nil {
		t.Fatal("the incremental lint admits a bound call in a runnable module")
	}
}

func TestBoundCall_TheEncoderWritesNone(t *testing.T) {
	_, err := ir.EncodeObject(boundCallModule(false))
	if err == nil || !strings.Contains(err.Error(), "a call through a bound, FromSql.from_sql, runs on no machine") {
		t.Fatalf("encoding a bound call = %v, want the encoder's refusal", err)
	}
	// An abstract module is refused for what it is, with or without one.
	empty := ir.NewModule("abstract.nomi")
	empty.SetAbstract()
	for _, m := range []*ir.Module{empty, boundCallModule(true)} {
		if _, err := ir.EncodeObject(m); err == nil || !strings.Contains(err.Error(), "is a module of abstract bodies") {
			t.Fatalf("encoding abstract module %s = %v, want the encoder's refusal", m.Name(), err)
		}
	}
	if _, err := ir.EncodeObject(ir.NewModule("plain.nomi")); err != nil {
		t.Fatalf("the plant: a plain empty module does not encode: %v", err)
	}
}

func TestBoundCall_TheLinkerDefinesNoAbstractModule(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "links into no program") {
			t.Fatalf("Linker.Define of an abstract module = %v, want its panic", r)
		}
	}()
	ir.NewLinker().Define(boundCallModule(true))
}
