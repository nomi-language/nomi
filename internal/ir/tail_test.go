package ir_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// tailFixture builds `f` with one call to `g` at the given site, hands the
// caller the block after the call and the call's result, and lets it finish
// the graph.
func tailFixture(site ir.CallSite, finish func(f *ir.Func, b *ir.Block, v ir.Temp)) (*ir.Func, *ir.Call) {
	pos := ir.At(helperFile, 1, 1)
	f := ir.NewFunc(pos, "f")
	entry := f.NewBlock(pos, "entry")
	arg := f.NewTemp()
	entry.Append(ir.NewRefLocal(pos, arg, ir.NewSymbol("n")))
	call := ir.NewCall(ir.At(helperFile, 2, 3), f.NewTemp(), site, ir.NewSymbol("g"), arg)
	entry.Append(call)
	finish(f, entry, call.Dst())
	return f, call
}

// The rule: a tail-marked call whose value reaches a plain Return with only
// copies, slots, deferred-call runs and jumps between is a transfer; any other
// work, a control return, a fault edge or an ordinary site is not.
func TestTailTransfers_TheValueMustReachAPlainReturnUnchanged(t *testing.T) {
	pos := ir.At(helperFile, 3, 1)
	cases := []struct {
		name   string
		site   ir.CallSite
		finish func(f *ir.Func, b *ir.Block, v ir.Temp)
		want   bool
	}{
		{"returned directly", ir.TailCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			b.SetTerm(ir.NewReturn(pos, v))
		}, true},
		{"an ordinary site", ir.OrdinaryCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			b.SetTerm(ir.NewReturn(pos, v))
		}, false},
		{"copied into the result slot, deferred calls run, through a join", ir.TailCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			slot := f.NewTemp()
			b.Append(ir.NewCopy(pos, slot, v))
			b.Append(ir.NewRunDefer(pos, 1))
			join := f.NewBlock(pos, "join")
			b.SetTerm(ir.NewJump(pos, join.ID()))
			join.SetTerm(ir.NewReturn(pos, slot))
		}, true},
		{"a bare return", ir.TailCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			b.SetTerm(ir.NewReturnUnit(pos))
		}, true},
		{"work after the call", ir.TailCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			other := f.NewTemp()
			b.Append(ir.NewRefLocal(pos, other, ir.NewSymbol("m")))
			b.SetTerm(ir.NewReturn(pos, v))
		}, false},
		{"another value returned", ir.TailCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			slot := f.NewTemp()
			b.Append(ir.NewCopy(pos, slot, v))
			b.SetTerm(ir.NewReturn(pos, v+100))
		}, false},
		{"a control return", ir.TailCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			b.SetTerm(ir.NewReturnCtl(pos, v, ir.CtlEmitStop))
		}, false},
		{"a fault edge", ir.TailCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			handler := f.NewBlock(pos, "handler")
			handler.SetTerm(ir.NewReturnUnit(pos))
			b.SetFault(pos, handler.ID())
			b.SetTerm(ir.NewReturn(pos, v))
		}, false},
		{"a loop back edge", ir.TailCall, func(f *ir.Func, b *ir.Block, v ir.Temp) {
			b.SetTerm(ir.NewJump(pos, b.ID()))
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, call := tailFixture(c.site, c.finish)
			if got := ir.TailTransfers(f)[call]; got != c.want {
				t.Fatalf("TailTransfers answered %v for the call, want %v", got, c.want)
			}
		})
	}
}
