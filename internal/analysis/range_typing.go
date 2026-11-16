package analysis

import (
	"math/big"

	"github.com/nomi-language/nomi/internal/ast"
)

// Typing by range (ranges.go). As each expression is checked, the checker
// records the range of an arithmetic operation, a negation, a call of a std
// function whose result's range follows from its arguments', and a read of
// a binding with a range of its own, and types the expression as the
// narrowest std numeric subtype the range proves when the range is typed
// (it rests on something other than literals) and that subtype is below
// the type the expression has without it: `count + 1` with count a
// NonNegativeInt is a PositiveInt, and `size - 1` after `size > 1` is one
// too. The operands of an operator are their base's values (refinement.go),
// so the type is the range's alone.

// rangeTyped records n's range, checked as ty, where the checker computes
// one, and answers n's type (typeForRange).
func (c *checker) rangeTyped(n ast.Node, ty Type) Type {
	if ty == nil {
		return nil
	}
	fam := familyOfBase(RefinementBase(ty))
	if fam == nil {
		return ty
	}
	var r numRange
	switch v := n.(type) {
	case *ast.Ident:
		if c.fa == nil {
			return ty
		}
		if sym := c.fa.References[Pos{Line: v.Line, Col: v.Col}]; sym != nil {
			if fr, ok := c.symRanges[sym]; ok && fr.fam == fam {
				c.setRange(n, fam, fr.r)
			}
		}
		return ty
	case *ast.Unary:
		if v.Op != "-" || fam == durationFamily {
			return ty
		}
		r = neg(c.exprRange(v.Right, fam))
	case *ast.Binary:
		var ok bool
		if r, ok = c.binaryRange(v, fam, ty); !ok {
			return ty
		}
	case *ast.Call:
		var ok bool
		if r, ok = c.callRange(v, nil, fam, ty); !ok {
			return ty
		}
	default:
		return ty
	}
	c.setRange(n, fam, r)
	return c.typeForRange(fam, r, ty)
}

// typeForRange is the type of a value checked as ty whose range is r: the
// narrowest std numeric subtype r proves, when r is typed and that subtype
// is below ty; ty otherwise, and always when ty is a subtype with a rule
// the checker does not read (a user's `Small`).
func (c *checker) typeForRange(fam *numericFamily, r numRange, ty Type) Type {
	if !r.typed {
		return ty
	}
	cand := c.rangeSubtype(fam, r, ty)
	if cand == nil {
		return ty
	}
	if refinementOf(ty) != nil {
		if _, exact := fam.facts(ty); !exact || TypesIdentical(cand, ty) || !refinesInto(cand, ty) {
			return ty
		}
	}
	return cand
}

// binaryRange is the range of n, an operation of fam: `+`, `-` and `*` on
// Int and Decimal, exact; `+` and `-` of Durations and a Duration by an
// Int, exact on nanoseconds; `+` of Floats (floatSum); an Int's `/` and
// `%` (quotient, remainder); and a pipe into a call (callRange). ok is
// false for anything else, which is its type's.
func (c *checker) binaryRange(n *ast.Binary, fam *numericFamily, ty Type) (numRange, bool) {
	switch n.Op {
	case "|>":
		call, ok := ungroupExpr(n.Right).(*ast.Call)
		if !ok {
			return numRange{}, false
		}
		return c.callRange(call, n.Left, fam, ty)
	case "+", "-", "*":
	case "/", "%":
		// Int's `/` truncates toward zero and `%` takes the dividend's
		// sign (spec §8 *Integer division and remainder*).
		if fam != intFamily || familyOfBase(RefinementBase(c.fa.ExprTypes[n.Left])) != fam || familyOfBase(RefinementBase(c.fa.ExprTypes[n.Right])) != fam {
			return numRange{}, false
		}
		l, r := c.exprRange(n.Left, fam), c.exprRange(n.Right, fam)
		if n.Op == "/" {
			return quotient(fam, l, r), true
		}
		return remainder(fam, l, r), true
	default:
		return numRange{}, false
	}
	lfam := familyOfBase(RefinementBase(c.fa.ExprTypes[n.Left]))
	rfam := familyOfBase(RefinementBase(c.fa.ExprTypes[n.Right]))
	switch fam {
	case floatFamily:
		if n.Op != "+" || lfam != fam || rfam != fam {
			return numRange{}, false
		}
		return floatSum(c.exprRange(n.Left, fam), c.exprRange(n.Right, fam)), true
	case durationFamily:
		ok := lfam == fam && rfam == fam && n.Op != "*"
		ok = ok || (n.Op == "*" && ((lfam == fam && rfam == intFamily) || (lfam == intFamily && rfam == fam)))
		if !ok {
			return numRange{}, false
		}
	default:
		if lfam != fam || rfam != fam {
			return numRange{}, false
		}
	}
	l, r := c.exprRange(n.Left, lfam), c.exprRange(n.Right, rfam)
	switch n.Op {
	case "+":
		return add(fam, l, r), true
	case "-":
		return sub(fam, l, r), true
	}
	return mul(fam, l, r), true
}

// callRange is the range of a call of a std function whose result follows
// from its arguments (spec §4 *Value ranges*), with piped the value piped
// into it when the call is a pipe's stage, met with what the result's
// type ty states:
//
//   - `Int.abs`, `Decimal.abs` and `Duration.abs`: the absolute values of
//     the argument's range;
//   - `Int.max`, `Int.min` and their Decimal and Duration counterparts
//     (extremeRange);
//   - `Int.pow(b, e)` and `Decimal.pow(b, e)` (powRange);
//   - `Int.sqrt`: the roots of the argument's ends (isqrtRange);
//   - `Int.to_decimal` and `Duration.as_nanos`: the argument's range;
//   - `Duration.nanoseconds`, `microseconds`, `milliseconds`, `seconds`,
//     `minutes` and `hours`: the argument's range times the unit's
//     nanoseconds;
//   - `String.length`, `List.length`, `Vector.length`, `Map.size`,
//     `Set.size` and `Iter.count` of a non-empty subtype: positive.
//
// ok is false for any other call.
func (c *checker) callRange(call *ast.Call, piped ast.Node, fam *numericFamily, ty Type) (numRange, bool) {
	fa, ok := ungroupExpr(call.Func).(*ast.FieldAccess)
	if !ok || fa.Field == nil {
		return numRange{}, false
	}
	owner, ok := fa.Object.(*ast.TypeIdent)
	if !ok || owner == nil {
		return numRange{}, false
	}
	var args []ast.Node
	if piped != nil {
		args = append(args, piped)
	}
	for _, a := range call.Args {
		args = append(args, valueOfArg(a))
	}
	arg := func(i int, f *numericFamily) (numRange, bool) {
		if i >= len(args) || args[i] == nil {
			return numRange{}, false
		}
		if familyOfBase(RefinementBase(c.fa.ExprTypes[args[i]])) != f {
			if _, lit := literalRange(args[i], f); !lit {
				return numRange{}, false
			}
		}
		return c.exprRange(args[i], f), true
	}
	var r numRange
	switch name := fa.Field.Name; {
	case name == "abs" && len(args) == 1 && ((owner.Name == "Int" && fam == intFamily) || (owner.Name == "Decimal" && fam == decimalFamily) || (owner.Name == "Duration" && fam == durationFamily)):
		a, ok := arg(0, fam)
		if !ok {
			return numRange{}, false
		}
		r = abs(fam, a)
	case (name == "max" || name == "min") && len(args) == 2 && ((owner.Name == "Int" && fam == intFamily) || (owner.Name == "Decimal" && fam == decimalFamily) || (owner.Name == "Duration" && fam == durationFamily)):
		a, _ := arg(0, fam)
		b, _ := arg(1, fam)
		if !a.known && !b.known {
			return numRange{}, false
		}
		// The larger or smaller of two values is one of them, so it is
		// non-zero when both are; its bounds may say so too (normalize).
		r = extremeRange(name == "max", a, b)
		r.nonZero = a.known && b.known && a.nonZero && b.nonZero
		r = r.normalize(fam)
	case name == "pow" && len(args) == 2 && ((owner.Name == "Int" && fam == intFamily) || (owner.Name == "Decimal" && fam == decimalFamily)):
		b, ok := arg(0, fam)
		if !ok {
			return numRange{}, false
		}
		e, _ := arg(1, intFamily)
		r = powRange(fam, b, e)
	case name == "to_decimal" && len(args) == 1 && owner.Name == "Int" && fam == decimalFamily:
		a, ok := arg(0, intFamily)
		if !ok {
			return numRange{}, false
		}
		r = a
	case owner.Name == "Duration" && fam == durationFamily && len(args) == 1 && durationUnits[name] != nil:
		a, ok := arg(0, intFamily)
		if !ok {
			return numRange{}, false
		}
		r = mul(fam, a, pointRange(new(big.Rat).SetInt64(durationUnits[name].Int64())))
	case name == "sqrt" && len(args) == 1 && owner.Name == "Int" && fam == intFamily:
		a, ok := arg(0, fam)
		if !ok {
			return numRange{}, false
		}
		r = isqrtRange(a)
	case name == "as_nanos" && len(args) == 1 && owner.Name == "Duration" && fam == intFamily:
		a, ok := arg(0, durationFamily)
		if !ok {
			return numRange{}, false
		}
		r = a
	case fam == intFamily && len(args) == 1 && nonEmptyCounts[owner.Name] == name:
		if !c.holdsSomething(c.fa.ExprTypes[args[0]]) {
			return numRange{}, false
		}
		r = numRange{known: true, lo: closedBound(big.NewRat(1, 1)), hi: infBound(), nonZero: true}
	default:
		return numRange{}, false
	}
	r.typed = true
	declared, _ := typeRange(ty, fam)
	return meet(fam, r, declared), true
}

// extremeRange is the range of the larger of a and b, or the smaller when
// max is false: the larger is at least either one's lowest value and at
// most the higher of their highest; the smaller the other way round. A
// range that is not known bounds nothing.
func extremeRange(max bool, a, b numRange) numRange {
	lo := func(r numRange) rbound {
		if !r.known {
			return infBound()
		}
		return r.lo
	}
	hi := func(r numRange) rbound {
		if !r.known {
			return infBound()
		}
		return r.hi
	}
	if !max {
		// min(a, b) is -max(-a, -b).
		return neg(extremeRange(true, neg(a), neg(b)))
	}
	out := numRange{known: true, lo: maxLo(lo(a), lo(b)), hi: infBound()}
	if ah, bh := hi(a), hi(b); !ah.inf && !bh.inf {
		out.hi = ah
		if c := bh.v.Cmp(ah.v); c > 0 || (c == 0 && !bh.open) {
			out.hi = bh
		}
	}
	return out
}

// durationUnits are the nanoseconds of each unit a Duration constructor
// counts.
var durationUnits = map[string]*big.Int{
	"nanoseconds":  big.NewInt(1),
	"microseconds": big.NewInt(1_000),
	"milliseconds": big.NewInt(1_000_000),
	"seconds":      big.NewInt(1_000_000_000),
	"minutes":      big.NewInt(60_000_000_000),
	"hours":        big.NewInt(3_600_000_000_000),
}

// nonEmptyCounts are the std functions that count a collection's or a
// String's elements, by owner.
var nonEmptyCounts = map[string]string{
	"String": "length",
	"List":   "length",
	"Vector": "length",
	"Map":    "size",
	"Set":    "size",
	"Iter":   "count",
}

// holdsSomething reports whether t is a std non-empty subtype of a String,
// List, Vector, Map or Set, or below one.
func (c *checker) holdsSomething(t Type) bool {
	for _, s := range refinementChain(t) {
		dt := refinementOf(s)
		if dt == nil || dt.Intersection {
			continue
		}
		switch {
		case dt.Origin == "std/strings" && dt.Name == "NonEmptyString",
			dt.Origin == "std/lists" && dt.Name == "NonEmptyList",
			dt.Origin == "std/vectors" && dt.Name == "NonEmptyVector",
			dt.Origin == "std/maps" && dt.Name == "NonEmptyMap",
			dt.Origin == "std/sets" && dt.Name == "NonEmptySet":
			return true
		}
	}
	return false
}

// powRange is the range of b to the power e, e >= 0, exact: 1 when e is 0;
// the power when both are one value and it has at most maxExactPowBits
// bits; at least 1 for an Int base that is positive, and positive for a
// Decimal one; non-negative for a non-negative base; off zero for a base
// off zero.
func powRange(fam *numericFamily, b, e numRange) numRange {
	ev, eok := e.isPoint()
	if eok && ev.Sign() == 0 {
		return pointRange(big.NewRat(1, 1))
	}
	// A power of two constants is its value, while it is small enough to
	// work out: `Int.pow(2, 63) - 1` is 2^63 - 1.
	if bv, ok := b.isPoint(); ok && eok && ev.IsInt() && ev.Num().IsInt64() && bv.IsInt() {
		if k := ev.Num().Int64(); k > 0 && int64(bv.Num().BitLen())*k <= maxExactPowBits {
			return pointRange(new(big.Rat).SetInt(new(big.Int).Exp(bv.Num(), big.NewInt(k), nil)))
		}
	}
	switch {
	case b.positive():
		return numRange{known: true, lo: rbound{v: new(big.Rat), open: true}, hi: infBound(), nonZero: true}.normalize(fam)
	case b.nonNegative():
		return numRange{known: true, lo: closedBound(new(big.Rat)), hi: infBound()}
	case b.nonZero:
		return numRange{known: true, lo: infBound(), hi: infBound(), nonZero: true}
	}
	return numRange{}
}

// maxExactPowBits bounds the powers of constants powRange works out.
const maxExactPowBits = 1 << 16

// quotient is the range of a / b on Ints, which truncates toward zero: by
// a positive b, between 0 and a, and by a negative one, between 0 and -a.
func quotient(fam *numericFamily, a, b numRange) numRange {
	if !a.known || !b.known {
		return numRange{typed: a.typed || b.typed}
	}
	var src numRange
	switch {
	case b.positive():
		src = a
	case !b.hi.inf && b.hi.v.Sign() < 0:
		src = neg(a)
	default:
		return numRange{typed: a.typed || b.typed}
	}
	zero := closedBound(new(big.Rat))
	out := numRange{known: true, lo: src.lo, hi: src.hi, typed: a.typed || b.typed}
	if !src.lo.inf && src.lo.v.Sign() > 0 {
		out.lo = zero
	}
	if !src.hi.inf && src.hi.v.Sign() < 0 {
		out.hi = zero
	}
	return out.normalize(fam)
}

// remainder is the range of a % b on Ints, which takes a's sign and is
// smaller in size than b: between 0 and a, and below b's size when b is
// bounded.
func remainder(fam *numericFamily, a, b numRange) numRange {
	if !a.known {
		return numRange{typed: a.typed || b.typed}
	}
	// size is one less than the largest size b can have, or unbounded.
	size := infBound()
	if b.known && !b.lo.inf && !b.hi.inf {
		m := new(big.Rat).Abs(b.lo.v)
		if h := new(big.Rat).Abs(b.hi.v); h.Cmp(m) > 0 {
			m = h
		}
		size = closedBound(m.Sub(m, ratOne))
	}
	zero := closedBound(new(big.Rat))
	out := numRange{known: true, typed: a.typed || b.typed}
	switch {
	case a.nonNegative():
		out.lo, out.hi = zero, minHi(a.hi, size)
	case !a.hi.inf && a.hi.v.Sign() <= 0:
		lo := infBound()
		if !size.inf {
			lo = closedBound(new(big.Rat).Neg(size.v))
		}
		out.lo, out.hi = maxLo(a.lo, lo), zero
	case !size.inf:
		out.lo, out.hi = closedBound(new(big.Rat).Neg(size.v)), size
	default:
		return numRange{typed: a.typed || b.typed}
	}
	return out.normalize(fam)
}

// isqrtRange is the range of Int.sqrt(n), the greatest Int whose square
// is at most n, for n in r: it grows with n.
func isqrtRange(r numRange) numRange {
	if !r.nonNegative() {
		return numRange{known: true, lo: closedBound(new(big.Rat)), hi: infBound()}
	}
	root := func(v *big.Rat) *big.Rat {
		return new(big.Rat).SetInt(new(big.Int).Sqrt(ratFloor(v).Num()))
	}
	out := numRange{known: true, lo: closedBound(root(r.lo.v)), hi: infBound()}
	if !r.hi.inf {
		out.hi = closedBound(root(r.hi.v))
	}
	return out.normalize(intFamily)
}
