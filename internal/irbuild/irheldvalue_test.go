package irbuild

// The held-value mapping that decides an `ir.Copy`, and the planted positive
// for the VM coverage LINKING classifier.

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// plantPos is a position for a planted graph. `ir.Pos` has unexported fields,
// so `ir.At` is the only way to make one a constructor will accept.
var plantPos = ir.At("plant.nomi", 1, 1)

// TestIRHeldValue_PerInstructionClass pins which temporaries `irHeldValue`
// counts as already held, per defining instruction class. It decides whether a
// `case` scrutinee, a pattern-`if` subject and a nested tuple in a
// destructuring prologue get an `ir.Copy`, so a change here moves IR in every
// body over that class.
func TestIRHeldValue_PerInstructionClass(t *testing.T) {
	cases := []struct {
		class string
		held  bool
		build func(f *ir.Func, b *ir.Block) ir.Temp
	}{
		{"Ref RefLocal", true, func(f *ir.Func, b *ir.Block) ir.Temp {
			r := ir.NewRefLocal(plantPos, f.NewTemp(), ir.NewSymbol("x"))
			b.Append(r)
			return r.Dst()
		}},
		{"Const Int", false, func(f *ir.Func, b *ir.Block) ir.Temp {
			c := ir.NewInt(plantPos, f.NewTemp(), 3)
			b.Append(c)
			return c.Dst()
		}},
		{"Proj ProjField", false, func(f *ir.Func, b *ir.Block) ir.Temp {
			src := ir.NewInt(plantPos, f.NewTemp(), 0)
			b.Append(src)
			p := ir.NewProjField(plantPos, f.NewTemp(), src.Dst(),
				ir.NewSymbol("value"), "value", ir.ValUnknown)
			b.Append(p)
			return p.Dst()
		}},
		{"Const Bool true", true, func(f *ir.Func, b *ir.Block) ir.Temp {
			c := ir.NewBool(plantPos, f.NewTemp(), true)
			b.Append(c)
			return c.Dst()
		}},
		{"Const Bool false", true, func(f *ir.Func, b *ir.Block) ir.Temp {
			c := ir.NewBool(plantPos, f.NewTemp(), false)
			b.Append(c)
			return c.Dst()
		}},
		{"Copy", true, func(f *ir.Func, b *ir.Block) ir.Temp {
			src := ir.NewInt(plantPos, f.NewTemp(), 0)
			b.Append(src)
			c := ir.NewCopy(plantPos, f.NewTemp(), src.Dst())
			b.Append(c)
			return c.Dst()
		}},
		{"Arith", false, func(f *ir.Func, b *ir.Block) ir.Temp {
			l := ir.NewInt(plantPos, f.NewTemp(), 1)
			b.Append(l)
			r := ir.NewInt(plantPos, f.NewTemp(), 1)
			b.Append(r)
			a := ir.NewArith(plantPos, f.NewTemp(), ir.OpAdd, ir.IntArith(ir.OverflowFaults),
				l.Dst(), r.Dst())
			b.Append(a)
			return a.Dst()
		}},
		{"Concat", false, func(f *ir.Func, b *ir.Block) ir.Temp {
			l := ir.NewString(plantPos, f.NewTemp(), "a")
			b.Append(l)
			r := ir.NewString(plantPos, f.NewTemp(), "b")
			b.Append(r)
			c := ir.NewConcat(plantPos, f.NewTemp(), l.Dst(), r.Dst())
			b.Append(c)
			return c.Dst()
		}},
	}
	for _, c := range cases {
		f := ir.NewFunc(plantPos, "probe")
		b := f.NewBlock(plantPos, "entry")
		if got := irHeldValue(f, c.build(f, b), nil); got != c.held {
			t.Errorf("%s: irHeldValue answered %v, want %v", c.class, got, c.held)
		}
	}
	// A PARAMETER is the one temporary nothing defines, and it is held.
	f := ir.NewFunc(plantPos, "param")
	p := f.AddParam(ir.NewSymbol("x"), ir.ValUnknown)
	if !irHeldValue(f, p, nil) {
		t.Error("a parameter is held in its own temporary")
	}
}

// TestVMCoverage_TheLinkingClassifierCatchesItsPlant is the planted positive
// for `vmWantLinkingFailures`.
//
// A low count that nothing validates is indistinguishable from a classifier
// that stopped recognising the failure. So this builds a module whose function
// calls a callee the module does not hold and asks the classifier about the
// error the machine produces for it, with the same module plus the callee as
// the clean control.
func TestVMCoverage_TheLinkingClassifierCatchesItsPlant(t *testing.T) {
	build := func(withCallee bool) error {
		// ONE SYMBOL, NAMED BY THE CALL AND OWNED BY THE CALLEE. `FuncFor`
		// resolves by the Symbol a `NewFuncFor` function carries, which is
		// the identity `ir.Lint`'s RuleModuleDeclaredOnce keys on and the
		// one `callInstr` probes. A second symbol with the same printed name
		// would make the plant pass for the wrong reason.
		callee := ir.NewSymbol("inner")

		inner := ir.NewFuncFor(plantPos, callee)
		ip := inner.AddParam(ir.NewSymbol("x"), ir.ValUnknown)
		ib := inner.NewBlock(plantPos, "entry")
		ib.SetTerm(ir.NewReturn(plantPos, ip))

		outer := ir.NewFunc(plantPos, "outer")
		op := outer.AddParam(ir.NewSymbol("x"), ir.ValUnknown)
		ob := outer.NewBlock(plantPos, "entry")
		c := ir.NewCall(plantPos, outer.NewTemp(), ir.OrdinaryCall, callee, op)
		ob.Append(c)
		ob.SetTerm(ir.NewReturn(plantPos, c.Dst()))

		mod := ir.NewModule("linkplant")
		mod.AddFunc(outer)
		if withCallee {
			mod.AddFunc(inner)
		}
		_, err := vmRunV(vm.New(mod, io.Discard), "outer", int64(1))
		return err
	}

	if err := build(true); err != nil {
		t.Fatalf("THE CONTROL FAILED: a module holding both functions must run, or the "+
			"plant below proves nothing: %v", err)
	}
	err := build(false)
	if err == nil {
		t.Fatal("a call naming a callee the module does not hold RAN. The machine's " +
			"FuncFor answered, which would make the LINKING bucket unreachable")
	}
	if got := vmClassify(err); got != "LINKING: a declaration this module did not retain" {
		t.Errorf("vmClassify answered %q for %v; the LINKING pin reads that exact "+
			"string, so a zero in the pin would be a zero in a bucket nothing can "+
			"reach", got, err)
	}
}
