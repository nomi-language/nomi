package analysis

import (
	"math/big"

	"github.com/nomi-language/nomi/internal/ast"
)

// Typing by range (ranges.go). As each expression is checked, the checker
// records the range of an arithmetic operation, a negation, a call of a std
// function whose result's range follows from its arguments', and a read of
// a binding with a range of its own, and types the expression as the
// narrowest std numeric refinement the range proves when the range is typed
// (it rests on something other than literals) and that refinement is below
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
// narrowest std numeric refinement r proves, when r is typed and that refinement
// is below ty; ty otherwise, and always when ty is a refinement with a rule
// the checker does not read (a user's `Small`).
func (c *checker) typeForRange(fam *numericFamily, r numRange, ty Type) Type {
	if !r.typed {
		return ty
	}
	cand := c.rangeRefinement(fam, r, ty)
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

// binaryRange is the range of n, an operation of fam: an operator
// (opRange), or a pipe into a call (callRange). ok is false for anything
// else, which is its type's.
func (c *checker) binaryRange(n *ast.Binary, fam *numericFamily, ty Type) (numRange, bool) {
	if n.Op == "|>" {
		call, ok := ungroupExpr(n.Right).(*ast.Call)
		if !ok {
			return numRange{}, false
		}
		return c.callRange(call, n.Left, fam, ty)
	}
	return c.opRange(n.Op, n.Left, n.Right, fam)
}

// opRange is the range of left op right, an operation of fam: `+`, `-`
// and `*` on Int and Decimal, exact; `+` and `-` of Durations and a
// Duration by an Int, exact on nanoseconds; `+` of Floats (floatSum); and
// an Int's `/` and `%` (quotient, remainder). ok is false for anything
// else.
func (c *checker) opRange(op string, left, right ast.Node, fam *numericFamily) (numRange, bool) {
	lfam, rfam := c.valueFamily(left), c.valueFamily(right)
	switch op {
	case "+", "-", "*":
	case "/", "%":
		// Int's `/` truncates toward zero and `%` takes the dividend's
		// sign (spec §8 *Integer division and remainder*).
		if fam != intFamily || lfam != fam || rfam != fam {
			return numRange{}, false
		}
		l, r := c.exprRange(left, fam), c.exprRange(right, fam)
		if op == "/" {
			return quotient(fam, l, r, false), true
		}
		return remainder(fam, l, r), true
	default:
		return numRange{}, false
	}
	switch fam {
	case floatFamily:
		if op != "+" || lfam != fam || rfam != fam {
			return numRange{}, false
		}
		return floatSum(c.exprRange(left, fam), c.exprRange(right, fam)), true
	case durationFamily:
		ok := lfam == fam && rfam == fam && op != "*"
		ok = ok || (op == "*" && ((lfam == fam && rfam == intFamily) || (lfam == intFamily && rfam == fam)))
		if !ok {
			return numRange{}, false
		}
	default:
		if lfam != fam || rfam != fam {
			return numRange{}, false
		}
	}
	l, r := c.exprRange(left, lfam), c.exprRange(right, rfam)
	switch op {
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
//   - `Int.sign` and `Decimal.sign`: the signs of the argument's ends
//     (signRange);
//   - `Int.modulo` (modulo) and `Int.floor_div` (quotient, floored);
//   - an operator's function, `Int.remainder(a, b)` for `a % b`: the
//     operator's (opRange);
//   - `Int.clamp(v, a..=b)` and its Decimal and Duration counterparts, of
//     a range written there (closedRangeEnds): the smaller of b and the
//     larger of v and a;
//   - `Int.to_decimal` and `Duration.as_nanos`: the argument's range;
//     `Duration.as_micros`, `as_millis`, `as_seconds`, `as_minutes` and
//     `as_hours`: its quotient by the unit's nanoseconds;
//   - `Duration.nanoseconds`, `microseconds`, `milliseconds`, `seconds`,
//     `minutes` and `hours`: the argument's range times the unit's
//     nanoseconds;
//   - `String.length`, `List.length`, `Vector.length`, `Map.size`,
//     `Set.size` and `Iter.count` of a value that holds something
//     (holdsSomething): of a non-empty refinement, a literal that writes an
//     element, or a read of a binding bound to one: positive.
//
// The type a function is called on is the std type its qualifier names
// (qualifierOf), however it is written: `Dur.abs(d)` under `import
// std::duration.{Duration as Dur}` is `Duration.abs`, and a program's own
// type imported as `Duration` has none of these rules. ok is false for any
// other call.
func (c *checker) callRange(call *ast.Call, piped ast.Node, fam *numericFamily, ty Type) (numRange, bool) {
	fa, ok := ungroupExpr(call.Func).(*ast.FieldAccess)
	if !ok || fa.Field == nil {
		return numRange{}, false
	}
	owner := c.fa.qualifierOf(fa.Object).Std
	if owner.IsZero() {
		return numRange{}, false
	}
	args := callArgs(call, piped)
	rangeOf := func(n ast.Node, f *numericFamily) (numRange, bool) {
		if n == nil {
			return numRange{}, false
		}
		if familyOfBase(RefinementBase(c.fa.ExprTypes[n])) != f {
			if _, lit := literalRange(n, f); !lit {
				return numRange{}, false
			}
		}
		return c.exprRange(n, f), true
	}
	arg := func(i int, f *numericFamily) (numRange, bool) {
		if i >= len(args) {
			return numRange{}, false
		}
		return rangeOf(args[i], f)
	}
	// ofam is the family of the type the function is called on.
	ofam := rangeOwners[owner]
	var r numRange
	switch name := fa.Field.Name; {
	case name == "abs" && len(args) == 1 && ofam == fam:
		a, ok := arg(0, fam)
		if !ok {
			return numRange{}, false
		}
		r = abs(fam, a)
	case name == "clamp" && len(args) == 2 && ofam == fam:
		// `Comparable.clamp` takes a ClosedRange, whose start is at most
		// its end, so the result is min(max(v, start), end).
		start, end, ok := c.closedRangeEnds(args[1])
		if !ok {
			return numRange{}, false
		}
		v, _ := arg(0, fam)
		low, ok := rangeOf(start, fam)
		if !ok {
			return numRange{}, false
		}
		r = extremeRange(true, v, low)
		if end != nil {
			high, ok := rangeOf(end, fam)
			if !ok {
				return numRange{}, false
			}
			r = extremeRange(false, r, high)
		}
		r = r.normalize(fam)
	case name == "sign" && len(args) == 1 && fam == intFamily && (ofam == intFamily || ofam == decimalFamily):
		a, ok := arg(0, ofam)
		if !ok {
			return numRange{}, false
		}
		r = signRange(a)
	case (name == "modulo" || name == "floor_div") && len(args) == 2 && ofam == fam && fam == intFamily:
		a, _ := arg(0, fam)
		b, ok := arg(1, fam)
		if !ok {
			return numRange{}, false
		}
		if name == "modulo" {
			r = modulo(a, b)
		} else {
			r = quotient(fam, a, b, true)
		}
	case operatorOfMethod(name) != "" && len(args) == 2 && args[0] != nil && args[1] != nil && ofam != nil && ofam == c.valueFamily(args[0]):
		var ok bool
		if r, ok = c.opRange(operatorOfMethod(name), args[0], args[1], fam); !ok {
			return numRange{}, false
		}
	case (name == "max" || name == "min") && len(args) == 2 && ofam == fam:
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
	case name == "pow" && len(args) == 2 && ofam == fam && fam != durationFamily:
		b, ok := arg(0, fam)
		if !ok {
			return numRange{}, false
		}
		e, _ := arg(1, intFamily)
		r = powRange(fam, b, e)
	case name == "to_decimal" && len(args) == 1 && ofam == intFamily && fam == decimalFamily:
		a, ok := arg(0, intFamily)
		if !ok {
			return numRange{}, false
		}
		r = a
	case ofam == durationFamily && fam == durationFamily && len(args) == 1 && durationUnits[name] != nil:
		a, ok := arg(0, intFamily)
		if !ok {
			return numRange{}, false
		}
		r = mul(fam, a, pointRange(new(big.Rat).SetInt64(durationUnits[name].Int64())))
	case name == "sqrt" && len(args) == 1 && ofam == intFamily && fam == intFamily:
		a, ok := arg(0, fam)
		if !ok {
			return numRange{}, false
		}
		r = isqrtRange(a)
	case ofam == durationFamily && fam == intFamily && len(args) == 1 && durationCounts[name] != nil:
		a, ok := arg(0, durationFamily)
		if !ok {
			return numRange{}, false
		}
		// A count of nanoseconds is exact; a larger unit's truncates.
		r = a
		if unit := durationCounts[name]; unit.Cmp(big.NewInt(1)) != 0 {
			r = quotient(fam, a, pointRange(new(big.Rat).SetInt(unit)), false)
		}
	case fam == intFamily && len(args) == 1 && nonEmptyCounts[owner] == name:
		if !c.holdsSomething(args[0]) {
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

// rangeOwners are the std types whose functions callRange reads, by the
// type a call's qualifier names. Float is not one: its `max`, `min` and
// `clamp` pass a NaN on, and its `abs` and `pow` round.
var rangeOwners = map[StdType]*numericFamily{
	{Module: "std/int", Name: "Int"}:           intFamily,
	{Module: "std/decimal", Name: "Decimal"}:   decimalFamily,
	{Module: "std/duration", Name: "Duration"}: durationFamily,
}

// closedRangeEnds is the start and end of n, a ClosedRange written as
// `a..=b`, or as `Range.from(a)`, which has no end. ok is false for a
// range of any other form, a binding among them.
func (c *checker) closedRangeEnds(n ast.Node) (start, end ast.Node, ok bool) {
	switch v := ungroupExpr(n).(type) {
	case *ast.RangeLit:
		if v.Start != nil && v.End != nil && v.Inclusive {
			return v.Start, v.End, true
		}
	case *ast.Call:
		if owner, name := c.stdCallee(v); owner == "Range" && name == "from" && len(v.Args) == 1 {
			if a := valueOfArg(v.Args[0]); a != nil {
				return a, nil, true
			}
		}
	}
	return nil, nil, false
}

// operatorOfMethod is the operator whose interface declares method, "" for
// a name that is none's: `remainder` is `%`'s.
func operatorOfMethod(method string) string {
	for _, oi := range operatorInterfaces {
		if oi.Method == method {
			return oi.Op
		}
	}
	return ""
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

// durationCounts are the nanoseconds of each unit a Duration is counted
// in, by the function that counts.
var durationCounts = map[string]*big.Int{
	"as_nanos":   durationUnits["nanoseconds"],
	"as_micros":  durationUnits["microseconds"],
	"as_millis":  durationUnits["milliseconds"],
	"as_seconds": durationUnits["seconds"],
	"as_minutes": durationUnits["minutes"],
	"as_hours":   durationUnits["hours"],
}

// nonEmptyCounts are the std functions that count a collection's or a
// String's elements, by the std type they are called on.
var nonEmptyCounts = map[StdType]string{
	{Module: "std/strings", Name: "String"}: "length",
	{Module: "std/lists", Name: "List"}:     "length",
	{Module: "std/vectors", Name: "Vector"}: "length",
	{Module: "std/maps", Name: "Map"}:       "size",
	{Module: "std/sets", Name: "Set"}:       "size",
	{Module: "std/iter", Name: "Iter"}:      "count",
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
// With floored set it is `Int.floor_div(a, b)`'s, which rounds toward
// negative infinity: the same, and below zero when a and b differ in sign.
func quotient(fam *numericFamily, a, b numRange, floored bool) numRange {
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
		if floored {
			out.hi = closedBound(big.NewRat(-1, 1))
		}
	}
	return out.normalize(fam)
}

// modulo is the range of Int.modulo(a, b), which takes b's sign and is
// smaller in size than b: from 0 to below the highest b can be when that
// is above zero, and from above the lowest b can be to 0 when that is
// below zero. It is no further from zero than an a of b's sign. A b
// unbounded on each side it can lie on bounds nothing.
func modulo(a, b numRange) numRange {
	if !b.known {
		return numRange{}
	}
	zero := closedBound(new(big.Rat))
	out := numRange{known: true, lo: zero, hi: zero}
	switch {
	case b.lo.inf:
		out.lo = infBound()
	case b.lo.v.Sign() < 0:
		out.lo = rbound{v: b.lo.v, open: true}
	}
	switch {
	case b.hi.inf:
		out.hi = infBound()
	case b.hi.v.Sign() > 0:
		out.hi = rbound{v: b.hi.v, open: true}
	}
	if out.lo.inf && out.hi.inf {
		return numRange{}
	}
	if a.known {
		if b.positive() && a.nonNegative() {
			out.hi = minHi(out.hi, a.hi)
		}
		if !b.hi.inf && b.hi.v.Sign() < 0 && !a.hi.inf && a.hi.v.Sign() <= 0 {
			out.lo = maxLo(out.lo, a.lo)
		}
	}
	return out.normalize(intFamily)
}

// signRange is the range of the sign of a value in r, -1, 0 or 1: the
// signs of r's ends.
func signRange(r numRange) numRange {
	lo, hi := int64(-1), int64(1)
	if r.known {
		if !r.lo.inf {
			if s := r.lo.v.Sign(); s > 0 || (s == 0 && r.lo.open) {
				lo = 1
			} else if s == 0 {
				lo = 0
			}
		}
		if !r.hi.inf {
			if s := r.hi.v.Sign(); s < 0 || (s == 0 && r.hi.open) {
				hi = -1
			} else if s == 0 {
				hi = 0
			}
		}
	}
	out := numRange{known: true, lo: closedBound(big.NewRat(lo, 1)), hi: closedBound(big.NewRat(hi, 1)), nonZero: r.known && r.nonZero}
	return out.normalize(intFamily)
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
