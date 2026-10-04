package irbuild

// Arithmetic, lowered to `ir.Arith`. The operator, the operand domain, the
// overflow discipline, the operands and the position are the node's; the
// shape (checked Int, wrapping Int, Float, Decimal) is `ir.Arith.Shape()`,
// computed in `internal/ir` from them.
//
// This file also holds the destructuring prologue's value plumbing (irHold,
// irMint, irBind): a prologue works on `expr` values, each the temporary it
// is held in and its kind.

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ir"
)

// irArithOps is Nomi's arithmetic surface syntax as IR operators. Binary only:
// unary `-` is `ir.OpNeg` and reaches the IR through irUnaryArith.
var irArithOps = map[string]ir.ArithOp{
	"+": ir.OpAdd,
	"-": ir.OpSub,
	"*": ir.OpMul,
	"/": ir.OpDiv,
	"%": ir.OpRem,
}

// irHold is the temporary a value is held in inside the open destructuring
// prologue: the one it already names, or a fresh one of the function's.
//
// A destructuring prologue is the only place a value is held through an
// expr; every body is built by irScalarBuilder, which works on temporaries.
// Holding one with no prologue open is a producer bug.
func (g *gen) irHold(e expr) ir.Temp {
	if g.irPro == nil {
		panic("irbuild: irHold with no destructuring prologue open")
	}
	t := g.irPro.hold(e)
	g.irTypeTemp(g.irPro.fn, t, e.k)
	return t
}

// irMint reserves a temporary of the open prologue's function whose kind is
// not known yet; irBind records it.
func (g *gen) irMint() ir.Temp { return g.irHold(expr{}) }

// irBind records the kind a reserved temporary holds, and answers the value.
func (g *gen) irBind(t ir.Temp, k kind) expr {
	g.irPro.set(t, k)
	g.irTypeTemp(g.irPro.fn, t, k)
	return expr{t: t, k: k}
}

// irPos is an arith operator's position as an IR position.
//
// SYNTHESIZED POSITIONS GO THROUGH ir.AtSynthesized rather than ir.At, which
// is what `ir.Pos.Synthesized()` is for: derive synthesis allocates from a
// band around 2^30, which `emittableLine` refuses, so `gen.at` ignores such a
// line and leaves the cursor where it was. A synthesized IR position carries
// the origin and SAYS it is one, so a consumer can blame the `derive` rather
// than inheriting a cursor.
//
// THERE IS NO FALLBACK FOR A LINE BELOW 1, and the alternative was considered
// and rejected rather than overlooked. `emittableLine` gates on `line > 0`, so
// a non-positive line is a value this package knows can arrive in general, and
// `ir.At` panics on one by design — `requirePos`'s stated reason is that a
// node with no position is a producer bug with no user input that reaches it.
// Declining to the AST path instead would be worse than a panic here: an Int
// `+` that fell through `arith()` lands on `arithmetic on a non-numeric`,
// which REFUSES a construct that lowers today. That is a DIFF, and a silent
// one. So this holds the IR's contract, and
// TestIRArith_EveryArithSiteHasAPosition measures the premise directly over
// the corpus and the stdlib instead of inferring it from the absence of a
// crash.
func (g *gen) irPos(line, col int) ir.Pos {
	if analysis.IsSynthesizedLine(line) {
		return ir.AtSynthesized(g.nomiPath, line, col)
	}
	return ir.At(g.nomiPath, line, col)
}

// irArithKind is the IR operand domain and whatever that domain needs, for an
// operand of kind k, reporting whether the arith class models it.
//
// A named type's `+` is not arithmetic here: it is a call of its `Add.add`
// impl (see operimpl.go).
//
// STRING IS NOT A DOMAIN AT ALL. `"a" + "b"` is concatenation, and the IR's
// `Domain` has no String member, so `arith()`'s String arm is outside this
// class by construction rather than by omission.
func (g *gen) irArithKind(op ir.ArithOp, k kind, wrapping bool) (ir.ArithKind, bool) {
	switch {
	case k == kindInt:
		// The discipline, which is the one part of an Int operation the front
		// end does not decide. `derive Hashable`'s mixers are modular and a
		// programmer's `+` is checked, and both lower from the same AST shape.
		//
		// Restricted to `+ - *` because `rt` has no wrapping `/`, `%` or
		// unary `-`, and `ir.IntArith` panics rather than represent one. That
		// reproduces today's fall-through exactly: a wrapping node carrying
		// `/` reaches the checked helper, because the builder's wrapping
		// switch has three arms and no default.
		if wrapping && (op == ir.OpAdd || op == ir.OpSub || op == ir.OpMul) {
			return ir.IntArith(ir.OverflowWraps), true
		}
		return ir.IntArith(ir.OverflowFaults), true
	case k == kindFloat:
		return ir.FloatArith(), true
	case isDecimalKind(k):
		return ir.DecimalArith(), true
	}
	return ir.ArithKind{}, false
}
