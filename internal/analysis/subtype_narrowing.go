package analysis

import "github.com/nomi-language/nomi/internal/ast"

// Narrowing (spec §4 *Narrowing*). A condition the checker recognizes
// proves a fact of a parameter or local binding, and the binding holds it
// in the code the condition guards: the branch it is true in, the branch it
// is false in, the right operand of an `and` or `or`, a `case` arm's body
// after its guard or condition, and the rest of a block after an `if` whose
// branch leaves or after an `assert`. In
//
//	if d == 0 then
//	    return None
//	end
//	Some(Int.divide(n, d))
//
// `d` is a NonZeroInt on the last line. A comparison of a numeric binding
// with a constant (comparisonFact) narrows its range (ranges.go), and the
// binding has the narrowest std numeric subtype the range proves;
// `Iter.is_empty` of a List, Vector, Map, Set or String, or an `==` or
// `!=` with an empty literal, proves it holds something; `not`, `and` and
// `or` combine them. A binding of a user's subtype keeps its type, since no
// std subtype is below it, though its range narrows. Bindings are
// immutable, so a narrowed binding holds its value everywhere in the
// region, a lambda's body included. A subtype is its base at run time, so
// a narrowed read is recorded with the subtype (FileAnalysis.ExprTypes) and
// lowers as any read does.

// subtypeFact is what a condition proves of one binding: a range its value
// lies in (fam set), or that it holds something.
type subtypeFact struct {
	sym      *Symbol
	fam      *numericFamily
	rng      numRange
	nonEmpty bool
}

// condFacts answers what cond proves of bindings where it evaluates to
// truth, at most one fact per binding:
//
//   - `not c` proves what c proves where it has the other value;
//   - `a and b` true proves what each proves true, and `a or b` false what
//     each proves false; the other two prove nothing;
//   - `x op k` or `k op x`, a comparison of a binding of Int, Float,
//     Decimal or Duration with a constant of its type (comparisonFact);
//   - `Iter.is_empty(x)` false, `x != empty` true and `x == empty` false,
//     with empty `[]`, `{}` or `""`, prove x holds something, when x is a
//     List, Vector, Map, Set or String.
func (c *checker) condFacts(cond ast.Node, truth bool) []subtypeFact {
	switch n := ungroupExpr(cond).(type) {
	case *ast.Unary:
		if n.Op == "not" {
			return c.condFacts(n.Right, !truth)
		}
	case *ast.Binary:
		switch n.Op {
		case "and", "or":
			if (n.Op == "and") != truth {
				return nil
			}
			return mergeSubtypeFacts(c.condFacts(n.Left, truth), c.condFacts(n.Right, truth))
		case "==", "!=", "<", "<=", ">", ">=":
			if f, ok := c.emptinessFact(n, truth); ok {
				return []subtypeFact{f}
			}
			if f, ok := c.comparisonFact(n, truth); ok {
				return []subtypeFact{f}
			}
		case "|>":
			// `xs |> Iter.is_empty()`.
			if call, ok := ungroupExpr(n.Right).(*ast.Call); ok && len(call.Args) == 0 && c.isIterIsEmpty(call.Func) && !truth {
				if sym := c.narrowableBinding(n.Left); sym != nil && c.nonEmptySubtypeOf(c.bindingType(sym)) != nil {
					return []subtypeFact{{sym: sym, nonEmpty: true}}
				}
			}
		}
	case *ast.Call:
		if len(n.Args) == 1 && c.isIterIsEmpty(n.Func) && !truth {
			if sym := c.narrowableBinding(n.Args[0]); sym != nil && c.nonEmptySubtypeOf(c.bindingType(sym)) != nil {
				return []subtypeFact{{sym: sym, nonEmpty: true}}
			}
		}
	}
	return nil
}

// mergeSubtypeFacts joins two lists of facts, one fact per binding.
func mergeSubtypeFacts(a, b []subtypeFact) []subtypeFact {
	out := append([]subtypeFact(nil), a...)
	for _, f := range b {
		merged := false
		for i := range out {
			if out[i].sym == f.sym {
				if f.fam != nil {
					if out[i].fam == nil {
						out[i].fam, out[i].rng = f.fam, f.rng
					} else if out[i].fam == f.fam {
						out[i].rng = meet(f.fam, out[i].rng, f.rng)
					}
				}
				out[i].nonEmpty = out[i].nonEmpty || f.nonEmpty
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, f)
		}
	}
	return out
}

// narrowableBinding is the parameter or local binding n reads by name, or
// nil.
func (c *checker) narrowableBinding(n ast.Node) *Symbol {
	id, ok := ungroupExpr(n).(*ast.Ident)
	if !ok || c.fa == nil {
		return nil
	}
	sym := c.fa.References[Pos{Line: id.Line, Col: id.Col}]
	if sym == nil || sym.Resolved != nil || (sym.Kind != SymbolParam && sym.Kind != SymbolBinding) {
		return nil
	}
	return sym
}

// bindingType is sym's type where the checker is: narrowed, or declared.
func (c *checker) bindingType(sym *Symbol) Type {
	if t, ok := c.narrowed[sym]; ok {
		return t
	}
	return sym.Type
}

// isIterIsEmpty reports whether callee names std's `Iter.is_empty`. A
// program cannot declare its own `Iter`.
func (c *checker) isIterIsEmpty(callee ast.Node) bool {
	switch f := ungroupExpr(callee).(type) {
	case *ast.FieldAccess:
		if f.Field == nil || f.Field.Name != "is_empty" {
			return false
		}
		owner, ok := f.Object.(*ast.TypeIdent)
		return ok && owner != nil && owner.Name == "Iter"
	case *ast.Ident:
		sym := resolvedSymbol(c.fa.References[Pos{Line: f.Line, Col: f.Col}])
		return sym != nil && sym.Kind == SymbolFunction && sym.OwningType == "Iter" && sym.Name == "is_empty"
	}
	return false
}

// emptinessFact is what `x == empty` or `x != empty` proves, with empty
// an empty list, map or string literal on either side.
func (c *checker) emptinessFact(n *ast.Binary, truth bool) (subtypeFact, bool) {
	if n.Op != "==" && n.Op != "!=" {
		return subtypeFact{}, false
	}
	binding, other := n.Left, n.Right
	if emptyLiteral(binding) {
		binding, other = other, binding
	}
	if !emptyLiteral(other) {
		return subtypeFact{}, false
	}
	// `x != []` true, or `x == []` false.
	if (n.Op == "!=") != truth {
		return subtypeFact{}, false
	}
	sym := c.narrowableBinding(binding)
	if sym == nil || c.nonEmptySubtypeOf(c.bindingType(sym)) == nil {
		return subtypeFact{}, false
	}
	return subtypeFact{sym: sym, nonEmpty: true}, true
}

// emptyLiteral reports whether n is `[]`, `{}` or `""`.
func emptyLiteral(n ast.Node) bool {
	switch l := ungroupExpr(n).(type) {
	case *ast.ListLit:
		return l.TypeName == nil && len(l.Items) == 0 && len(l.Spreads) == 0
	case *ast.MapLit:
		return l.TypeName == nil && len(l.Entries) == 0 && len(l.Spreads) == 0
	case *ast.StringLit:
		return l.Value == "" && !l.Triple
	}
	return false
}

// comparisonFact is what `x op k` proves where it is truth, with x a
// binding of Int, Float, Decimal or Duration, possibly one of their
// subtypes, and k a constant of its base on either side: an expression
// whose range is one value (a literal, possibly negated, or a binding of
// one). The comparison states a range: `x > k` is (k, +inf), `x <= k` is
// (-inf, k], `x == k` is k alone, and `x != k` excludes k. A false
// comparison of an Int, Decimal or Duration is its opposite one true. A
// Float comparison but `!=` is false for NaN, so a true one proves what it
// says and that x is no NaN, and of a false one only `x != k` false, which
// is `x == k`, proves anything, unless x's range already rules NaN out;
// `x != 0.0` true proves nothing of a Float that may be NaN, since NaN is
// no NonZeroFloat.
func (c *checker) comparisonFact(n *ast.Binary, truth bool) (subtypeFact, bool) {
	if sym := c.narrowableBinding(n.Left); sym != nil {
		if f, ok := c.comparisonFactOf(sym, n.Right, n.Op, truth); ok {
			return f, true
		}
	}
	if sym := c.narrowableBinding(n.Right); sym != nil {
		return c.comparisonFactOf(sym, n.Left, mirroredComparison(n.Op), truth)
	}
	return subtypeFact{}, false
}

// comparisonFactOf is comparisonFact with the binding sym on the left and
// other, which must be a constant, on the right.
func (c *checker) comparisonFactOf(sym *Symbol, other ast.Node, op string, truth bool) (subtypeFact, bool) {
	fam := familyOfBase(RefinementBase(c.bindingType(sym)))
	if fam == nil {
		return subtypeFact{}, false
	}
	k, ok := c.exprRange(other, fam).isPoint()
	if !ok {
		return subtypeFact{}, false
	}
	// A Float binding with a range of its own is no NaN, and compares as
	// an ordered value.
	ordered := fam != floatFamily || c.bindingRange(sym, fam).known
	if !truth {
		if !ordered && op != "!=" {
			return subtypeFact{}, false
		}
		op = negatedComparison(op)
	} else if !ordered && op == "!=" {
		return subtypeFact{}, false
	}
	r := fullRange()
	switch op {
	case ">":
		r.lo = rbound{v: k, open: true}
	case ">=":
		r.lo = closedBound(k)
	case "<":
		r.hi = rbound{v: k, open: true}
	case "<=":
		r.hi = closedBound(k)
	case "==":
		r = pointRange(k)
	case "!=":
		r.excl = k
	default:
		return subtypeFact{}, false
	}
	r.typed = true
	return subtypeFact{sym: sym, fam: fam, rng: r.normalize(fam)}, true
}

// mirroredComparison is op with its operands swapped: `0 < x` is `x > 0`.
func mirroredComparison(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return op
}

// negatedComparison is the comparison true where op is false, on values
// that are totally ordered: `not (x < 0)` is `x >= 0`.
func negatedComparison(op string) string {
	switch op {
	case "==":
		return "!="
	case "!=":
		return "=="
	case "<":
		return ">="
	case "<=":
		return ">"
	case ">":
		return "<="
	case ">=":
		return "<"
	}
	return op
}

// narrowedSubtype is the type f gives its binding, whose type is t, and
// the range it has there, or nil when f narrows neither: a numeric fact
// meets the binding's range, and the binding is the narrowest std numeric
// subtype the range proves when that is below t and t is the base or a
// std subtype of its family (a user's subtype keeps its type, since no std
// subtype is below it). The type rests on what the range has that rests
// on something other than literals: a binding whose range is its
// literals' (`x = 3`, or an element of `[1, 2, 3]`, spec §4 *Value
// ranges*) is typed by what t states met with f, so `if x > 2 then
// return ...` leaves x an Int, as it was, though its range is 1 to 2, and
// the range it has there still rests on literals.
func (c *checker) narrowedSubtype(t Type, f subtypeFact) (Type, *famRange) {
	if f.nonEmpty {
		return c.nonEmptySubtypeOf(t), nil
	}
	if f.fam == nil || familyOfBase(RefinementBase(t)) != f.fam {
		return nil, nil
	}
	br := c.bindingRange(f.sym, f.fam)
	r := meet(f.fam, br, f.rng)
	tr := r
	if br.known && !br.typed {
		// The range keeps resting on literals, so a later fact narrows
		// the binding's type from what this one proves, not from them.
		stated, _ := typeRange(t, f.fam)
		tr = meet(f.fam, stated, f.rng)
		r.typed = false
	}
	fr := &famRange{f.fam, r}
	if _, exact := f.fam.facts(t); !exact {
		return nil, fr
	}
	nt := c.typeForRange(f.fam, tr, t)
	if TypesIdentical(nt, t) {
		return nil, fr
	}
	return nt, fr
}

// nonEmptySubtypeOf is the std non-empty subtype of t, a List, Vector,
// Map, Set or String that is no subtype, at t's type arguments, or nil.
func (c *checker) nonEmptySubtypeOf(t Type) Type {
	t = resolveTypeVar(t)
	if t == nil || refinementOf(t) != nil {
		return nil
	}
	var origin, name string
	var args []Type
	switch tt := t.(type) {
	case *ListType:
		origin, name, args = "std/lists", "NonEmptyList", []Type{tt.Elem}
	case *MapType:
		origin, name, args = "std/maps", "NonEmptyMap", []Type{tt.Key, tt.Val}
	case *PrimitiveType:
		if tt != TypeString {
			return nil
		}
		origin, name = "std/strings", "NonEmptyString"
	default:
		if elem, ok := vectorElemType(t); ok {
			origin, name, args = "std/vectors", "NonEmptyVector", []Type{elem}
		} else if elem, ok := setElemType(t); ok {
			origin, name, args = "std/sets", "NonEmptySet", []Type{elem}
		} else {
			return nil
		}
	}
	decl := c.stdSubtypeDecl(origin, name)
	if decl == nil || len(decl.TypeParamDefs) != len(args) {
		return nil
	}
	if len(args) == 0 {
		return decl
	}
	inst := c.freshDistinct(decl)
	inst.TypeArgs = args
	if !TypesIdentical(RefinementBase(inst), t) {
		return nil
	}
	return inst
}

// stdSubtypeDecl is the std subtype name declared in the std file origin,
// looked up as numericFamilyType looks up a numeric one, or nil.
func (c *checker) stdSubtypeDecl(origin, name string) *DistinctType {
	is := func(t Type) *DistinctType {
		dt, ok := t.(*DistinctType)
		if !ok || !dt.Subtype || dt.Intersection || dt.Origin != origin || dt.Name != name {
			return nil
		}
		return dt
	}
	if fa := c.fa.Program().File(origin); fa != nil && fa.ModuleScope != nil {
		if sym := fa.ModuleScope.LookupLocal(name); sym != nil {
			if dt := is(sym.Type); dt != nil {
				return dt
			}
		}
	}
	if c.reg != nil {
		return is(c.reg.Lookup(name))
	}
	return nil
}

// narrowSubtypes narrows the bindings facts name until the returned func
// runs, which restores what they held before.
func (c *checker) narrowSubtypes(facts []subtypeFact) func() {
	type saved struct {
		sym        *Symbol
		t          Type
		r          famRange
		hadT, hadR bool
		setT, setR bool
	}
	var prev []saved
	for _, f := range facts {
		t := c.bindingType(f.sym)
		if t == nil {
			continue
		}
		nt, fr := c.narrowedSubtype(t, f)
		if nt == nil && fr == nil {
			continue
		}
		s := saved{sym: f.sym}
		s.t, s.hadT = c.narrowed[f.sym]
		s.r, s.hadR = c.symRanges[f.sym]
		if nt != nil {
			if c.narrowed == nil {
				c.narrowed = map[*Symbol]Type{}
			}
			c.narrowed[f.sym] = nt
			s.setT = true
		}
		if fr != nil {
			if c.symRanges == nil {
				c.symRanges = map[*Symbol]famRange{}
			}
			c.symRanges[f.sym] = *fr
			s.setR = true
		}
		prev = append(prev, s)
	}
	return func() {
		for i := len(prev) - 1; i >= 0; i-- {
			s := prev[i]
			if s.setT {
				if s.hadT {
					c.narrowed[s.sym] = s.t
				} else {
					delete(c.narrowed, s.sym)
				}
			}
			if s.setR {
				if s.hadR {
					c.symRanges[s.sym] = s.r
				} else {
					delete(c.symRanges, s.sym)
				}
			}
		}
	}
}

// withCondition runs check with the bindings cond narrows where it is
// truth narrowed.
func (c *checker) withCondition(cond ast.Node, truth bool, check func()) {
	if cond == nil {
		check()
		return
	}
	restore := c.narrowSubtypes(c.condFacts(cond, truth))
	defer restore()
	check()
}

// factsAfter answers what holds of bindings after stmt, a statement before
// a block's end, has run and the block goes on: after an `if` whose first
// branch leaves, its condition is false, and after one whose `else`
// leaves, true; after an `else if` chain with no final `else` whose
// branches all leave, every condition is false; after `assert c`, c is
// true, and after `refute c` false.
func (c *checker) factsAfter(stmt ast.Node) []subtypeFact {
	if es, ok := stmt.(*ast.ExprStmt); ok {
		stmt = es.Expr
	}
	switch n := ungroupExpr(stmt).(type) {
	case *ast.Assertion:
		if n.Check || n.Expr == nil {
			return nil
		}
		return c.condFacts(n.Expr, !n.Refute)
	case *ast.If:
		if n.CondPattern != nil || n.Cond == nil {
			return nil
		}
		if blk, ok := n.Else.(*ast.Block); ok {
			switch {
			case c.blockLeaves(n.Then):
				return c.condFacts(n.Cond, false)
			case c.blockLeaves(blk):
				return c.condFacts(n.Cond, true)
			}
			return nil
		}
		var facts []subtypeFact
		for _, link := range ifChainLinks(n) {
			if link.CondPattern != nil || link.Cond == nil || !c.blockLeaves(link.Then) {
				return nil
			}
			facts = mergeSubtypeFacts(facts, c.condFacts(link.Cond, false))
		}
		if !ifChainLacksElse(n) {
			return nil
		}
		return facts
	}
	return nil
}

// blockLeaves reports whether block, checked, never finishes: its last
// expression is `return`, `break`, `continue`, a trap or another
// expression of type Infallible.
func (c *checker) blockLeaves(block *ast.Block) bool {
	tail := tailExpr(block)
	return tail != nil && isInfallibleLike(c.fa.ExprTypes[tail])
}

// blockNarrowings narrows, for the rest of a block, what stmt proves
// (factsAfter), appending what restores it to undo.
func (c *checker) blockNarrowings(stmt ast.Node, undo []func()) []func() {
	facts := c.factsAfter(stmt)
	if len(facts) == 0 {
		return undo
	}
	return append(undo, c.narrowSubtypes(facts))
}

// undoNarrowings runs undo last first.
func undoNarrowings(undo []func()) {
	for i := len(undo) - 1; i >= 0; i-- {
		undo[i]()
	}
}
