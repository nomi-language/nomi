package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// The std numeric subtypes (spec §4 *Value ranges*). Each of Int, Float,
// Decimal and std::duration's Duration has three subtypes, NonZero,
// NonNegative and Positive (the intersection of the other two, with a rule
// that says the same), and the checker knows their rules: a value's range
// (ranges.go) decides which of them it is. A user-declared subtype's rule
// runs only when the program is built, so a value of one is its
// ancestors' for arithmetic: `small + small` with `subtype Small
// NonNegativeInt when ...` is a NonNegativeInt.

// numericFamily is one base and its three std subtypes.
type numericFamily struct {
	origin, base                   string
	nonZero, nonNegative, positive string
}

var (
	intFamily      = &numericFamily{"std/int", "Int", "NonZeroInt", "NonNegativeInt", "PositiveInt"}
	floatFamily    = &numericFamily{"std/float", "Float", "NonZeroFloat", "NonNegativeFloat", "PositiveFloat"}
	decimalFamily  = &numericFamily{"std/decimal", "Decimal", "NonZeroDecimal", "NonNegativeDecimal", "PositiveDecimal"}
	durationFamily = &numericFamily{"std/duration", "Duration", "NonZeroDuration", "NonNegativeDuration", "PositiveDuration"}
)

// numericFacts are the std rules a value is known to satisfy. Positive is
// both.
type numericFacts struct {
	nonZero, nonNegative bool
}

func (f numericFacts) any() bool { return f.nonZero || f.nonNegative }

// familyOfBase answers the family whose base t is, or nil.
func familyOfBase(t Type) *numericFamily {
	switch t := resolveTypeVar(t).(type) {
	case *PrimitiveType:
		switch t {
		case TypeInt:
			return intFamily
		case TypeFloat:
			return floatFamily
		case TypeDecimal:
			return decimalFamily
		}
	case *DistinctType:
		if !t.Subtype && t.Origin == durationFamily.origin && t.Name == durationFamily.base {
			return durationFamily
		}
	}
	return nil
}

// facts answers which of fam's rules sit on t's chain of subtypes, and
// whether those are all the rules t has: exact is false for a subtype
// with a rule of its own outside the family (`subtype Small
// NonNegativeInt when ...`), whose literals the checker cannot decide.
func (fam *numericFamily) facts(t Type) (f numericFacts, exact bool) {
	exact = true
	for _, s := range subtypeChain(t) {
		dt := subtypeOf(s)
		if dt == nil {
			exact = false
			continue
		}
		if dt.Intersection {
			continue
		}
		if dt.Origin != fam.origin {
			exact = false
			continue
		}
		switch dt.Name {
		case fam.nonZero:
			f.nonZero = true
		case fam.nonNegative:
			f.nonNegative = true
		case fam.positive:
			f.nonZero, f.nonNegative = true, true
		default:
			exact = false
		}
	}
	return f, exact
}

// numericLiteral reports whether n is an Int, Float or Decimal literal,
// possibly negated: a literal the checker decides the std numeric rules
// on (literalNumericFacts).
func numericLiteral(n ast.Node) bool {
	n = ungroupExpr(n)
	if u, ok := n.(*ast.Unary); ok && u.Op == "-" {
		n = ungroupExpr(u.Right)
	}
	switch n.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit:
		return true
	}
	return false
}

// literalFitsNumericSubtype reports whether the literal n satisfies every
// rule of t, decided when checking: t is one of the std numeric subtypes,
// or an intersection of them, and n a literal of its base whose value
// (literalRange) lies in t's range.
func literalFitsNumericSubtype(n ast.Node, t Type) bool {
	if subtypeOf(t) == nil {
		return false
	}
	fam := familyOfBase(SubtypeBase(t))
	if fam == nil {
		return false
	}
	need, exact := fam.facts(t)
	if !exact || !need.any() {
		return false
	}
	r, ok := literalRange(n, fam)
	if !ok {
		return false
	}
	want, _ := typeRange(t, fam)
	return within(r, want)
}

// numericFamilyType answers the subtype of fam whose rules are f, fam's
// base when f holds none, looked up among the types near (the operands,
// whose chains name the family's subtypes they reach), then the program's
// file of fam and the file's own names. Nil when none of them has it.
func (c *checker) numericFamilyType(fam *numericFamily, f numericFacts, base Type, near ...Type) Type {
	name := ""
	switch {
	case f.nonZero && f.nonNegative:
		name = fam.positive
	case f.nonNegative:
		name = fam.nonNegative
	case f.nonZero:
		name = fam.nonZero
	default:
		return base
	}
	is := func(t Type) bool {
		dt := subtypeOf(t)
		return dt != nil && !dt.Intersection && dt.Origin == fam.origin && dt.Name == name
	}
	for _, t := range near {
		for _, s := range subtypeChain(t) {
			if is(s) {
				return s
			}
		}
	}
	if fa := c.fa.Program().File(fam.origin); fa != nil && fa.ModuleScope != nil {
		if sym := fa.ModuleScope.LookupLocal(name); sym != nil && is(sym.Type) {
			return sym.Type
		}
	}
	if c.reg != nil {
		if t := c.reg.Lookup(name); is(t) {
			return t
		}
	}
	return nil
}

// narrowLiteralJoin is the type values at nodes, of types tys, join to
// where joined is what they joined to with no regard for their literals:
// when the values that are no literals (literalLeaves) join to a subtype
// below joined, the join is the narrowest std numeric subtype the union of
// their types' ranges and the literals' values proves (ranges.go), when
// that is the subtype they joined to or an ancestor of it, below joined:
// `if c then n else 0` with n a NonNegativeInt is a NonNegativeInt,
// whichever branch the literal is, and with s a user's `subtype Small
// NonNegativeInt when ...`, `if c then s else 1` is a NonNegativeInt.
// joined otherwise. Each literal is then checked as that subtype.
func (c *checker) narrowLiteralJoin(joined Type, nodes []ast.Node, tys []Type) Type {
	if joined == nil || len(nodes) != len(tys) {
		return joined
	}
	fam := familyOfBase(SubtypeBase(joined))
	if fam == nil {
		return joined
	}
	var typed Type
	var literals [][]ast.Node
	for i, node := range nodes {
		if tys[i] == nil || isInfallibleLike(tys[i]) {
			continue
		}
		if leaves, inner, ok := literalLeaves(node); ok {
			literals = append(literals, append(leaves, inner...))
			continue
		}
		switch {
		case typed == nil:
			typed = tys[i]
		case TypesIdentical(typed, tys[i]):
		default:
			typed = c.subtypeJoin(typed, tys[i])
			if typed == nil {
				return joined
			}
		}
	}
	if typed == nil || len(literals) == 0 || TypesIdentical(typed, joined) || subtypeOf(typed) == nil {
		return joined
	}
	if !subtypeWidensTo(typed, joined) {
		return joined
	}
	r, _ := typeRange(typed, fam)
	for _, group := range literals {
		for _, leaf := range group {
			if !subtypeLiteral(leaf) {
				continue
			}
			lr, ok := literalRange(leaf, fam)
			if !ok {
				return joined
			}
			r = union(r, lr)
		}
	}
	found := c.rangeSubtype(fam, r, typed)
	if found == nil || TypesIdentical(found, joined) || !subtypeWidensTo(found, joined) || (!TypesIdentical(typed, found) && !subtypeWidensTo(typed, found)) {
		return joined
	}
	for _, group := range literals {
		for _, node := range group {
			c.fa.recordExprType(node, found)
			if subtypeLiteral(node) {
				c.recordSubtypeLiteral(node, found)
			}
		}
	}
	return found
}

// literalLeaves answers the literals n's value is made of when every value
// it can have is a literal (subtypeLiteral): n itself, a block's tail
// after any statements,
// or each branch of an `if` with an `else` or of a `case`, with the
// blocks and conditionals passed through (inner). ok is false for any
// other value.
func literalLeaves(n ast.Node) (leaves, inner []ast.Node, ok bool) {
	var walk func(n ast.Node, depth int) bool
	walk = func(n ast.Node, depth int) bool {
		if n == nil || depth > 16 {
			return false
		}
		if subtypeLiteral(n) {
			leaves = append(leaves, ungroupExpr(n))
			if g := ungroupExpr(n); g != n {
				inner = append(inner, n)
			}
			return true
		}
		switch v := n.(type) {
		case *ast.Block:
			t := tailExpr(v)
			if t == nil {
				return false
			}
			inner = append(inner, v)
			return walk(t, depth+1)
		case *ast.If:
			if v.Else == nil {
				return false
			}
			inner = append(inner, v)
			return walk(v.Then, depth+1) && walk(v.Else, depth+1)
		case *ast.Case:
			if len(v.Branches) == 0 {
				return false
			}
			inner = append(inner, v)
			for _, b := range v.Branches {
				if !walk(b.Body, depth+1) {
					return false
				}
			}
			return true
		}
		return false
	}
	if !walk(n, 0) {
		return nil, nil, false
	}
	return leaves, inner, true
}
