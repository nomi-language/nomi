package analysis

import (
	"math/big"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/rt"
)

// Value ranges (spec §4 *Value ranges*). The checker knows, of each value
// of Int, Decimal, Float and Duration where it can, an interval [lo, hi]
// its value lies in, with whether it is known to be off zero, and a
// value's std numeric refinement is the narrowest of NonZero, NonNegative
// and Positive its range proves (rangeRefinement). The facts come from
// literals, the declared refinement of a binding, parameter, field or
// result, comparisons with a constant in the code they guard
// (refinement_narrowing.go), arithmetic (+, -, * and unary - on exact values,
// the absolute value), a few std functions whose results follow from their
// arguments (callRange), and the elements a std Iter function passes a
// callback, for the callback's parameter (element_ranges.go). A value
// whose range comes from literals alone keeps its
// base type, so `1 + 2` is an Int, and converts where a refinement its range
// proves is expected (provenRefinement): `k = 60 * 60` passes where a
// PositiveInt is expected. Nothing relates two bindings: `i < n` proves
// nothing of `i`. Bindings are immutable, so a binding's range holds
// everywhere it is read and no loop or recursion needs a fixed point.
//
// A binding of a List, Vector, Set, Map or String keeps, the same way,
// whether its value holds something (holdsSomething), and converts where
// the std non-empty refinement is expected (provenNonEmpty); a `once` takes
// that refinement as its type (onceRangeType).
//
// A Float's range holds only of a value that is no NaN: a Float of no
// known range may be NaN, and only `+` of two non-negative values and
// negation keep a range, since a product or a difference can round to
// zero or be NaN (inf - inf, 0 * inf).
//
// Other code asks one thing: exprRange, the range of a checked expression.

// rbound is one end of a range: infinite, or a value that is or is not
// itself in the range.
type rbound struct {
	inf  bool
	v    *big.Rat
	open bool
}

// numRange is what the checker knows of a numeric value. A range that is
// not known holds no fact (and for a Float, admits NaN).
type numRange struct {
	known   bool
	lo, hi  rbound
	nonZero bool
	// excl is a value the range excludes that may lie inside it, from `x
	// != c`; meet applies it to the bounds it meets.
	excl *big.Rat
	// typed is set for a range that comes from something other than
	// literals: a binding's refinement, a comparison, a function's result.
	// A value whose range is not typed keeps its base type.
	typed bool
}

var (
	ratZero = new(big.Rat)
	ratOne  = big.NewRat(1, 1)
)

func infBound() rbound { return rbound{inf: true} }

func closedBound(v *big.Rat) rbound { return rbound{v: v} }

// fullRange is a known range with no bounds: a value that is no NaN.
func fullRange() numRange {
	return numRange{known: true, lo: infBound(), hi: infBound()}
}

// pointRange is the range of the one value v.
func pointRange(v *big.Rat) numRange {
	return numRange{known: true, lo: closedBound(v), hi: closedBound(v), nonZero: v.Sign() != 0}
}

// integral reports whether fam's values are whole numbers: Int, and
// Duration as an Int of nanoseconds.
func (fam *numericFamily) integral() bool {
	return fam == intFamily || fam == durationFamily
}

// isPoint reports whether r is one value, and which.
func (r numRange) isPoint() (*big.Rat, bool) {
	if !r.known || r.lo.inf || r.hi.inf || r.lo.open || r.hi.open || r.lo.v.Cmp(r.hi.v) != 0 {
		return nil, false
	}
	return r.lo.v, true
}

// nonNegative reports whether every value of r is >= 0.
func (r numRange) nonNegative() bool {
	return r.known && !r.lo.inf && r.lo.v.Sign() >= 0
}

// positive reports whether every value of r is > 0.
func (r numRange) positive() bool {
	return r.known && !r.lo.inf && (r.lo.v.Sign() > 0 || (r.lo.v.Sign() == 0 && r.lo.open))
}

// empty reports whether no value lies in r: the code it is the range of
// never runs.
func (r numRange) empty() bool {
	if !r.known || r.lo.inf || r.hi.inf {
		return false
	}
	c := r.lo.v.Cmp(r.hi.v)
	return c > 0 || (c == 0 && (r.lo.open || r.hi.open))
}

// normalize settles r: an Int's bounds are whole and closed, a range off
// zero whose end is zero opens that end, and a range that excludes zero
// is off zero.
func (r numRange) normalize(fam *numericFamily) numRange {
	if !r.known {
		return r
	}
	if fam.integral() {
		if !r.lo.inf {
			v := ratCeil(r.lo.v)
			if r.lo.open && v.Cmp(r.lo.v) == 0 {
				v.Add(v, ratOne)
			}
			r.lo = closedBound(v)
		}
		if !r.hi.inf {
			v := ratFloor(r.hi.v)
			if r.hi.open && v.Cmp(r.hi.v) == 0 {
				v.Sub(v, ratOne)
			}
			r.hi = closedBound(v)
		}
	}
	if r.excl != nil {
		applied := false
		if !r.lo.inf && !r.lo.open && r.lo.v.Cmp(r.excl) == 0 {
			r.lo.open, applied = true, true
		}
		if !r.hi.inf && !r.hi.open && r.hi.v.Cmp(r.excl) == 0 {
			r.hi.open, applied = true, true
		}
		if r.excl.Sign() == 0 {
			r.nonZero, applied = true, true
		}
		if applied {
			r.excl = nil
			return r.normalize(fam)
		}
	}
	if r.nonZero {
		if !r.lo.inf && !r.lo.open && r.lo.v.Sign() == 0 {
			r.lo.open = true
			return r.normalize(fam)
		}
		if !r.hi.inf && !r.hi.open && r.hi.v.Sign() == 0 {
			r.hi.open = true
			return r.normalize(fam)
		}
	}
	if r.positive() || (!r.hi.inf && (r.hi.v.Sign() < 0 || (r.hi.v.Sign() == 0 && r.hi.open))) {
		r.nonZero = true
	}
	return r
}

func ratCeil(v *big.Rat) *big.Rat {
	if v.IsInt() {
		return new(big.Rat).Set(v)
	}
	q := new(big.Int).Div(v.Num(), v.Denom()) // Euclidean: floor for a positive denominator
	return new(big.Rat).SetInt(q.Add(q, big.NewInt(1)))
}

func ratFloor(v *big.Rat) *big.Rat {
	if v.IsInt() {
		return new(big.Rat).Set(v)
	}
	q := new(big.Int).Div(v.Num(), v.Denom())
	return new(big.Rat).SetInt(q)
}

// maxLo is the higher of two lower bounds, minHi the lower of two upper
// ones; at a tie the open bound, which excludes the value, wins.
func maxLo(a, b rbound) rbound {
	switch {
	case a.inf:
		return b
	case b.inf:
		return a
	}
	switch c := a.v.Cmp(b.v); {
	case c > 0:
		return a
	case c < 0:
		return b
	}
	return rbound{v: a.v, open: a.open || b.open}
}

func minHi(a, b rbound) rbound {
	switch {
	case a.inf:
		return b
	case b.inf:
		return a
	}
	switch c := a.v.Cmp(b.v); {
	case c < 0:
		return a
	case c > 0:
		return b
	}
	return rbound{v: a.v, open: a.open || b.open}
}

// meet is what holds when both a and b do. An empty meet is unreachable
// code, which keeps a.
func meet(fam *numericFamily, a, b numRange) numRange {
	if !b.known {
		return a
	}
	if !a.known {
		return b
	}
	out := numRange{
		known:   true,
		lo:      maxLo(a.lo, b.lo),
		hi:      minHi(a.hi, b.hi),
		nonZero: a.nonZero || b.nonZero,
		excl:    a.excl,
		typed:   a.typed || b.typed,
	}
	if out.excl == nil {
		out.excl = b.excl
	} else if b.excl != nil {
		// One excluded value is kept; the other is applied to the bounds
		// now or dropped.
		out.excl = b.excl
		out = out.normalize(fam)
		out.excl = a.excl
	}
	out = out.normalize(fam)
	if out.empty() {
		return a
	}
	return out
}

// union is what holds when a or b does: the values of a value that is
// either.
func union(a, b numRange) numRange {
	if !a.known || !b.known {
		return numRange{typed: a.typed || b.typed}
	}
	lo := a.lo
	switch {
	case a.lo.inf || b.lo.inf:
		lo = infBound()
	default:
		switch c := a.lo.v.Cmp(b.lo.v); {
		case c > 0:
			lo = b.lo
		case c == 0:
			lo = rbound{v: a.lo.v, open: a.lo.open && b.lo.open}
		}
	}
	hi := a.hi
	switch {
	case a.hi.inf || b.hi.inf:
		hi = infBound()
	default:
		switch c := a.hi.v.Cmp(b.hi.v); {
		case c < 0:
			hi = b.hi
		case c == 0:
			hi = rbound{v: a.hi.v, open: a.hi.open && b.hi.open}
		}
	}
	return numRange{known: true, lo: lo, hi: hi, nonZero: a.nonZero && b.nonZero, typed: a.typed || b.typed}
}

// neg is the range of -x for x in r.
func neg(r numRange) numRange {
	if !r.known {
		return r
	}
	flip := func(b rbound) rbound {
		if b.inf {
			return b
		}
		return rbound{v: new(big.Rat).Neg(b.v), open: b.open}
	}
	out := numRange{known: true, lo: flip(r.hi), hi: flip(r.lo), nonZero: r.nonZero, typed: r.typed}
	if r.excl != nil {
		out.excl = new(big.Rat).Neg(r.excl)
	}
	return out
}

// addBounds adds two bounds of one side.
func addBounds(a, b rbound) rbound {
	if a.inf || b.inf {
		return infBound()
	}
	return rbound{v: new(big.Rat).Add(a.v, b.v), open: a.open || b.open}
}

// add is the range of a + b, exact.
func add(fam *numericFamily, a, b numRange) numRange {
	if !a.known || !b.known {
		return numRange{typed: a.typed || b.typed}
	}
	return numRange{known: true, lo: addBounds(a.lo, b.lo), hi: addBounds(a.hi, b.hi), typed: a.typed || b.typed}.normalize(fam)
}

// sub is the range of a - b, exact.
func sub(fam *numericFamily, a, b numRange) numRange {
	return add(fam, a, neg(b))
}

// ext is an end of a range as an extended number for products: sign -1 or
// +1 for an infinity, else v.
type ext struct {
	inf  int
	v    *big.Rat
	open bool
}

func (e ext) sign() int {
	if e.inf != 0 {
		return e.inf
	}
	return e.v.Sign()
}

func extMul(a, b ext) ext {
	// 0 times an unbounded end is 0: the end is never reached, and the
	// product of a value at 0 with any other is 0.
	if (a.inf == 0 && a.v.Sign() == 0) || (b.inf == 0 && b.v.Sign() == 0) {
		zeroOpen := (a.inf == 0 && a.v.Sign() == 0 && a.open) || (b.inf == 0 && b.v.Sign() == 0 && b.open)
		if (a.inf == 0 && a.v.Sign() == 0 && !a.open) || (b.inf == 0 && b.v.Sign() == 0 && !b.open) {
			zeroOpen = false
		}
		return ext{v: new(big.Rat), open: zeroOpen}
	}
	if a.inf != 0 || b.inf != 0 {
		return ext{inf: a.sign() * b.sign()}
	}
	return ext{v: new(big.Rat).Mul(a.v, b.v), open: a.open || b.open}
}

func extCmp(a, b ext) int {
	switch {
	case a.inf != 0 || b.inf != 0:
		ai, bi := a.inf, b.inf
		if ai == bi {
			return 0
		}
		if ai == 0 {
			return -bi
		}
		if bi == 0 {
			return ai
		}
		return ai - bi
	}
	return a.v.Cmp(b.v)
}

// mul is the range of a * b, exact: the least and greatest of the products
// of their ends, and off zero when both are.
func mul(fam *numericFamily, a, b numRange) numRange {
	if !a.known || !b.known {
		return numRange{nonZero: a.nonZero && b.nonZero && a.known && b.known, typed: a.typed || b.typed}
	}
	end := func(b rbound, side int) ext {
		if b.inf {
			return ext{inf: side}
		}
		return ext{v: b.v, open: b.open}
	}
	as := []ext{end(a.lo, -1), end(a.hi, 1)}
	bs := []ext{end(b.lo, -1), end(b.hi, 1)}
	var lo, hi ext
	first := true
	for _, x := range as {
		for _, y := range bs {
			p := extMul(x, y)
			if first {
				lo, hi, first = p, p, false
				continue
			}
			if c := extCmp(p, lo); c < 0 || (c == 0 && !p.open) {
				lo = p
			}
			if c := extCmp(p, hi); c > 0 || (c == 0 && !p.open) {
				hi = p
			}
		}
	}
	toBound := func(e ext) rbound {
		if e.inf != 0 {
			return infBound()
		}
		return rbound{v: e.v, open: e.open}
	}
	out := numRange{known: true, lo: toBound(lo), hi: toBound(hi), nonZero: a.nonZero && b.nonZero, typed: a.typed || b.typed}
	return out.normalize(fam)
}

// abs is the range of |x| for x in r.
func abs(fam *numericFamily, r numRange) numRange {
	if !r.known {
		return numRange{known: true, lo: closedBound(new(big.Rat)), hi: infBound(), nonZero: false, typed: r.typed}.normalize(fam)
	}
	if r.nonNegative() {
		return r
	}
	if !r.hi.inf && r.hi.v.Sign() <= 0 {
		return neg(r)
	}
	hi := infBound()
	if !r.lo.inf && !r.hi.inf {
		m := new(big.Rat).Neg(r.lo.v)
		if r.hi.v.Cmp(m) > 0 {
			m = r.hi.v
		}
		hi = closedBound(m)
	}
	return numRange{known: true, lo: closedBound(new(big.Rat)), hi: hi, nonZero: r.nonZero, typed: r.typed}.normalize(fam)
}

// floatSum is the range of a + b on Floats: only the sign, where both are
// non-negative and so the sum is no NaN (an overflow is +inf), and rounding
// to nearest takes no sum of a positive value and a non-negative one below
// the positive one.
func floatSum(a, b numRange) numRange {
	if !a.nonNegative() || !b.nonNegative() {
		return numRange{typed: a.typed || b.typed}
	}
	lo := closedBound(new(big.Rat))
	if a.positive() || b.positive() {
		lo.open = true
	}
	return numRange{known: true, lo: lo, hi: infBound(), nonZero: lo.open, typed: a.typed || b.typed}
}

// stdRangeOf is the range a std numeric refinement of fam named name states:
// Positive, NonNegative or NonZero.
func (fam *numericFamily) stdRangeOf(name string) (numRange, bool) {
	zero := new(big.Rat)
	switch name {
	case fam.positive:
		return numRange{known: true, lo: rbound{v: zero, open: true}, hi: infBound(), nonZero: true}.normalize(fam), true
	case fam.nonNegative:
		return numRange{known: true, lo: closedBound(zero), hi: infBound()}, true
	case fam.nonZero:
		return numRange{known: true, lo: infBound(), hi: infBound(), nonZero: true}, true
	}
	return numRange{}, false
}

// ruleBounds is the bounds an Int refinement's rule states and whether they
// are all it states (ast.IntRuleBounds), read from the rule as written
// (DistinctType.Rule) and kept per rule.
func ruleBounds(rule string) (min, max *big.Int, exact bool) {
	if rule == "" {
		return nil, nil, false
	}
	if v, ok := ruleBoundsCache.Load(rule); ok {
		b := v.(ruleBoundsEntry)
		return b.min, b.max, b.exact
	}
	var e ruleBoundsEntry
	if nodes, err := parser.Parse(lexer.Lex("once rule_bounds = " + rule + "\n")); err == nil && len(nodes) == 1 {
		if once, ok := nodes[0].(*ast.OnceBinding); ok {
			e.min, e.max, e.exact = ast.IntRuleBounds(once.Value)
		}
	}
	ruleBoundsCache.Store(rule, e)
	return e.min, e.max, e.exact
}

type ruleBoundsEntry struct {
	min, max *big.Int
	exact    bool
}

var ruleBoundsCache sync.Map

// typeRange is the range t states of a value of fam: the meet of the
// ranges of the std numeric refinements on its chain and of the bounds the
// rule of each Int refinement on it states. exact is set when that range is
// all t's rules say, so a value in it is a t; a NonZeroFloat or another
// refinement with a rule of another form makes it inexact. A t of another
// family, or with no fact, gives a range that is not known; typed is set.
func typeRange(t Type, fam *numericFamily) (r numRange, exact bool) {
	if t == nil || familyOfBase(RefinementBase(t)) != fam {
		return numRange{typed: true}, false
	}
	exact = true
	r = numRange{typed: true}
	for _, s := range refinementChain(t) {
		dt := refinementOf(s)
		if dt == nil {
			exact = false
			continue
		}
		if dt.Intersection {
			continue
		}
		if dt.Origin == fam.origin {
			if sr, ok := fam.stdRangeOf(dt.Name); ok {
				sr.typed = true
				if !r.known {
					r = sr
				} else {
					r = meet(fam, r, sr)
				}
				continue
			}
		}
		if fam == intFamily {
			if min, max, ok := ruleBounds(dt.Rule); min != nil || max != nil {
				br := numRange{known: true, lo: infBound(), hi: infBound(), typed: true}
				if min != nil {
					br.lo = closedBound(new(big.Rat).SetInt(min))
				}
				if max != nil {
					br.hi = closedBound(new(big.Rat).SetInt(max))
				}
				br = br.normalize(fam)
				if !r.known {
					r = br
				} else {
					r = meet(fam, r, br)
				}
				if !ok {
					exact = false
				}
				continue
			}
		}
		exact = false
	}
	return r, exact
}

// provableTarget reports whether a value of fam whose range is in want's
// converts to want: every refinement on want's chain is a std numeric refinement
// of fam or an intersection, or an Int refinement whose only parent is Int and
// whose rule is literal bounds joined by `and` (std::int's Int64, or a
// module's `refine Digit Int when fn n -> n >= 0 and n <= 9`).
func provableTarget(want Type, fam *numericFamily) bool {
	if refinementOf(want) == nil {
		return false
	}
	for _, s := range refinementChain(want) {
		dt := refinementOf(s)
		if dt == nil {
			return false
		}
		if dt.Intersection {
			continue
		}
		if dt.Origin == fam.origin {
			if _, ok := fam.stdRangeOf(dt.Name); ok {
				continue
			}
		}
		if fam != intFamily || len(dt.MoreParents) > 0 || dt.Inner != TypeInt {
			return false
		}
		if _, _, exact := ruleBounds(dt.Rule); !exact {
			return false
		}
	}
	return true
}

// within reports whether every value of r is in want: r's bounds lie
// inside want's, and r is off zero where want is.
func within(r, want numRange) bool {
	if !r.known {
		return false
	}
	if !want.known {
		return true
	}
	if !want.lo.inf {
		if r.lo.inf {
			return false
		}
		c := r.lo.v.Cmp(want.lo.v)
		if c < 0 || (c == 0 && want.lo.open && !r.lo.open) {
			return false
		}
	}
	if !want.hi.inf {
		if r.hi.inf {
			return false
		}
		c := r.hi.v.Cmp(want.hi.v)
		if c > 0 || (c == 0 && want.hi.open && !r.hi.open) {
			return false
		}
	}
	return !want.nonZero || r.nonZero
}

// rangeFacts is which of fam's std rules every value of r satisfies.
func rangeFacts(r numRange) numericFacts {
	if !r.known {
		return numericFacts{}
	}
	return numericFacts{nonZero: r.nonZero, nonNegative: r.nonNegative()}
}

// rangeRefinement is the narrowest std numeric refinement of fam a value of
// range r is, nil when r proves none: Positive, NonNegative or NonZero.
func (c *checker) rangeRefinement(fam *numericFamily, r numRange, near ...Type) Type {
	f := rangeFacts(r)
	if !f.any() {
		return nil
	}
	return c.numericFamilyType(fam, f, nil, near...)
}

// literalRange is the range of n, a literal of fam's base, possibly
// negated: the one value it is. ok is false for any other node, and for a
// Float NaN, which has no range.
func literalRange(n ast.Node, fam *numericFamily) (numRange, bool) {
	n = ungroupExpr(n)
	negated := false
	if u, ok := n.(*ast.Unary); ok && u.Op == "-" {
		negated = true
		n = ungroupExpr(u.Right)
	}
	var v *big.Rat
	switch lit := n.(type) {
	case *ast.IntLit:
		if fam != intFamily {
			return numRange{}, false
		}
		if lit.Big != nil {
			v = new(big.Rat).SetInt(lit.Big)
		} else {
			v = new(big.Rat).SetInt64(lit.Value)
		}
	case *ast.FloatLit:
		if fam != floatFamily {
			return numRange{}, false
		}
		v = new(big.Rat)
		if v.SetFloat64(lit.Value) == nil {
			// NaN or an infinity: no range (a NaN satisfies no rule).
			return numRange{}, true
		}
	case *ast.DecimalLit:
		if fam != decimalFamily {
			return numRange{}, false
		}
		d, err := rt.ParseDecimalLexeme(lit.Lexeme)
		if err != nil {
			return numRange{}, false
		}
		v = decimalRat(d)
	default:
		return numRange{}, false
	}
	if negated {
		v.Neg(v)
	}
	return pointRange(v), true
}

// decimalRat is d's exact value.
func decimalRat(d rt.Decimal) *big.Rat {
	if d.Mantissa == nil {
		return new(big.Rat)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt32(d.Scale))), nil)
	if d.Scale >= 0 {
		return new(big.Rat).SetFrac(d.Mantissa, scale)
	}
	return new(big.Rat).SetInt(new(big.Int).Mul(d.Mantissa, scale))
}

func absInt32(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}

// exprRange is the range of n, a checked expression of fam: a literal's
// value, the range recorded when n was checked (an operation, a call, a
// read of a binding with a range of its own), the union of an `if`'s or a
// `case`'s branches and a block's tail, else what n's type states.
func (c *checker) exprRange(n ast.Node, fam *numericFamily) numRange {
	if r, ok := literalRange(n, fam); ok {
		return r
	}
	if r, ok := c.ranges[n]; ok && r.fam == fam {
		return r.r
	}
	g := ungroupExpr(n)
	if r, ok := c.ranges[g]; ok && r.fam == fam {
		return r.r
	}
	if blk, ok := g.(*ast.Block); ok {
		if t := tailExpr(blk); t != nil {
			return c.exprRange(t, fam)
		}
		return numRange{}
	}
	branches, ok := valueBranches(g)
	if !ok {
		return c.typedExprRange(g, fam)
	}
	r := c.branchUnion(fam, branches)
	c.setRange(g, fam, r)
	return r
}

// valueBranches is the branches whose values are n's when n is an `if`
// with an `else` and no pattern, or a `case`: its value is one of theirs.
// ok is false for any other node.
func valueBranches(n ast.Node) ([]ast.Node, bool) {
	switch v := n.(type) {
	case *ast.If:
		if v.Else == nil || v.CondPattern != nil {
			return nil, false
		}
		return []ast.Node{v.Then, v.Else}, true
	case *ast.Case:
		bodies := make([]ast.Node, 0, len(v.Branches))
		for _, b := range v.Branches {
			bodies = append(bodies, b.Body)
		}
		return bodies, true
	}
	return nil, false
}

// typedExprRange is the range n's recorded type states.
func (c *checker) typedExprRange(n ast.Node, fam *numericFamily) numRange {
	r, _ := typeRange(c.fa.ExprTypes[n], fam)
	return r
}

// branchUnion is the union of the ranges of the values of branches that
// finish.
func (c *checker) branchUnion(fam *numericFamily, branches []ast.Node) numRange {
	finishing, ok := c.finishingBranches(branches)
	if !ok {
		return numRange{}
	}
	var out numRange
	for i, b := range finishing {
		r := c.exprRange(b, fam)
		if i == 0 {
			out = r
			continue
		}
		out = union(out, r)
	}
	return out
}

// finishingBranches is those of branches that finish: a branch whose block
// leaves, or whose value is Infallible, gives the value nothing. ok is
// false when a branch is missing.
func (c *checker) finishingBranches(branches []ast.Node) ([]ast.Node, bool) {
	out := make([]ast.Node, 0, len(branches))
	for _, b := range branches {
		if b == nil {
			return nil, false
		}
		if blk, ok := b.(*ast.Block); ok && c.blockLeaves(blk) {
			continue
		}
		if isInfallibleLike(c.fa.ExprTypes[b]) {
			continue
		}
		out = append(out, b)
	}
	return out, true
}

// famRange is what a value proves: a range and the numeric family it is
// of, or, with fam nil, that a List, Vector, Set, Map or String holds
// something (nonEmpty; holdsSomething).
type famRange struct {
	fam      *numericFamily
	r        numRange
	nonEmpty bool
}

func (c *checker) setRange(n ast.Node, fam *numericFamily, r numRange) {
	if c.ranges == nil {
		c.ranges = map[ast.Node]famRange{}
	}
	c.ranges[n] = famRange{fam: fam, r: r}
}

// bindingRange is sym's range where the checker is: narrowed, the range of
// the value it was bound to, or what its type states.
func (c *checker) bindingRange(sym *Symbol, fam *numericFamily) numRange {
	if r, ok := c.symRanges[sym]; ok && r.fam == fam {
		return r.r
	}
	r, _ := typeRange(c.bindingType(sym), fam)
	return r
}

// bindRange gives sym, bound to value with no type written, what value
// proves: its range, so `k = 60 * 60` holds 3600 wherever k is read, or,
// for a List, Vector, Set, Map or String, that it holds something
// (holdsSomething), so `names = ["a"]` passes where a NonEmptyList is
// expected and `name = "Ada"` where a NonEmptyString is. A binding is
// immutable, so the fact holds wherever it is in scope. An annotated
// binding holds what its type states, as a parameter does.
func (c *checker) bindRange(sym *Symbol, value ast.Node) {
	fact := famRange{fam: familyOfBase(RefinementBase(sym.Type))}
	switch {
	case fact.fam != nil:
		fact.r = c.exprRange(value, fact.fam)
		if !fact.r.known {
			return
		}
	case c.holdsSomething(value) && c.nonEmptyRefinementOf(sym.Type) != nil:
		fact.nonEmpty = true
	default:
		return
	}
	if c.symRanges == nil {
		c.symRanges = map[*Symbol]famRange{}
	}
	c.symRanges[sym] = fact
}

// onceRangeType is the type of a `once` bound to value, checked as ty:
// the narrowest std numeric refinement value's range proves, the literals'
// included, when it is below ty, and for a List, Vector, Set, Map or
// String whose value holds something (holdsSomething), its std non-empty
// refinement: `once words = ["a"]` is a NonEmptyList<String>, and `once name
// = "Ada"` a NonEmptyString.
func (c *checker) onceRangeType(value ast.Node, ty Type) Type {
	fam := familyOfBase(RefinementBase(ty))
	if fam == nil {
		if c.holdsSomething(value) {
			if ne := c.nonEmptyRefinementOf(ty); ne != nil {
				return ne
			}
		}
		return ty
	}
	r := c.exprRange(value, fam)
	r.typed = true
	return c.typeForRange(fam, r, ty)
}

// provenRefinement is want when n, checked as have, is of want's family
// and its range proves want's rules (provableTarget, within), or the std
// non-empty refinement want names when n holds something (provenNonEmpty):
// the value converts to it with nothing run, since a refinement is its base
// at run time. Nil otherwise.
func (c *checker) provenRefinement(n ast.Node, have, want Type) Type {
	if have == nil || want == nil || refinementOf(want) == nil {
		return nil
	}
	if isStdNonEmpty(refinementOf(want)) {
		return c.provenNonEmpty(n, have, want)
	}
	fam := familyOfBase(RefinementBase(want))
	if fam == nil || familyOfBase(RefinementBase(have)) != fam || refinesInto(have, want) {
		return nil
	}
	if _, open := resolveTypeVar(have).(*TypeVar); open {
		return nil
	}
	if !provableTarget(want, fam) {
		return nil
	}
	need, _ := typeRange(want, fam)
	if !within(c.exprRange(n, fam), need) {
		return nil
	}
	return want
}

// provenNonEmpty is the std non-empty refinement of have, a List, Vector,
// Set, Map or String, at have's type arguments, when that refinement is
// want's and n's value holds something (holdsSomething): with `names =
// ["a"]`, `names` passes where a NonEmptyList<String> is expected, and
// where a generic call's NonEmptyList<T> is, which it solves; `"a${x}"`
// and a binding of it pass where a NonEmptyString is. The value then
// meets want as a `once` of the refinement does, its type arguments
// converting or not as theirs do (a List<Circle> as a
// NonEmptyList<Shape>). Nil otherwise.
func (c *checker) provenNonEmpty(n ast.Node, have, want Type) Type {
	proven := c.nonEmptyRefinementOf(have)
	if proven == nil {
		return nil
	}
	if got, need := refinementOf(proven), refinementOf(want); got.Origin != need.Origin || got.Name != need.Name {
		return nil
	}
	if !c.holdsSomething(n) {
		return nil
	}
	return proven
}
