package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A function value whose signature is wider than the function type its
// position expects: `fn cnt<T>(xs: Iter<T>): Int` passed where a
// `(List<Int>) -> Int` is expected, `Iter.count` given to
// `Map.map_values` over list values, `fn show(x: Display): String` given to
// `Iter.map` over Ints. The checker accepts these because each expected
// parameter enters the function's own parameter as an argument of a direct
// call does (a List is viewed as an `Iter`, a concrete value erases into its
// interface), and each result enters the expected result the same way.
//
// A call through the value cannot do that entry itself: it holds only the
// expected type. So the value is wrapped in an adapter closure of the
// expected type that converts each argument (coerceEmpty, the conversion a
// direct call's argument goes through), calls the function, and converts its
// result.

// funcWiden answers fn, a function value of kind have, as a function value
// of kind want, through an adapter closure. It answers false, and names no
// decline (the caller does, or keeps fn), when the arities differ, when an
// argument or the result does not convert, or when fn is an iter-sensitive
// callback whose break or continue the adapter would hide.
func (bl *irScalarBuilder) funcWiden(at ast.Node, fn ir.Temp, have, want kind) (ir.Temp, bool) {
	if have.tag != tagFunc || want.tag != tagFunc || have.comp == nil || want.comp == nil {
		return ir.NoTemp, false
	}
	hp, wp := funcParams(have), funcParams(want)
	if len(hp) != len(wp) || !irCallableValueKind(want) {
		return ir.NoTemp, false
	}
	if int(fn) < len(bl.sides) && bl.sides[fn].ctl != irCtlNone {
		return ir.NoTemp, false
	}
	pos := bl.g.irNodePos(at)
	f := ir.NewFunc(pos, "widen")
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(pos, "entry")
	child := &irScalarBuilder{g: bl.g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	in := make([]ir.Temp, len(wp))
	for i, k := range wp {
		in[i] = bl.g.irAddParam(f, ir.NewSymbol("_"), k)
	}
	inner := bl.g.irAddParam(f, ir.NewSymbol("_"), have)
	args := make([]ir.Temp, len(wp))
	for i := range wp {
		v, k, ok := child.coerceEmpty(at, in[i], wp[i], hp[i])
		if !ok || k != hp[i] {
			return ir.NoTemp, false
		}
		args[i] = v
	}
	call := ir.NewIndirectCall(pos, f.NewTemp(), ir.OrdinaryCall, inner, args...)
	child.b.Append(call)
	child.side(call.Dst(), irScalarSide{k: funcResult(have), deferrable: true})
	result, rk, ok := child.coerceEmpty(at, call.Dst(), funcResult(have), funcResult(want))
	if !ok || rk != funcResult(want) {
		return ir.NoTemp, false
	}
	child.b.SetTerm(ir.NewReturn(pos, result))
	if err := ir.Lint(f); err != nil {
		return ir.NoTemp, false
	}
	n := ir.NewFuncValue(pos, bl.f.NewTemp(), f, fn)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: want})
	return n.Dst(), true
}
