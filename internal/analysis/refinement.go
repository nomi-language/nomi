package analysis

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// RuleText renders a refinement's rule as diagnostics, hovers and the
// stdlib reference quote it: `fn port -> port >= 1`. Empty for no rule. A
// rule the formatter writes on one line stays as written. A lambda whose one
// expression the formatter put on the line after its arrow, as it does for a
// rule too long for the declaration's line, reads as the one-line lambda it
// is, with no `end`, and any other rule the formatter broke is joined onto
// one line. A rule that does not parse once joined, a body of several
// statements or a `case`, whose arms each start a line, keeps the
// formatter's lines.
func RuleText(rule ast.Node) string {
	if rule == nil {
		return ""
	}
	rendered := format.RenderNode(rule)
	if !strings.Contains(rendered, "\n") {
		return rendered
	}
	joined := strings.Join(strings.Fields(rendered), " ")
	if l, ok := rule.(*ast.Lambda); ok && l.Body != nil && len(l.Body.Stmts) == 1 {
		joined = strings.TrimSuffix(joined, " end")
	}
	if _, err := parser.Parse(lexer.Lex("rule = " + joined + "\n")); err != nil {
		return rendered
	}
	return joined
}

// refinementOf answers t as a refinement, a `refine` declaration or an
// intersection of refinements, or nil when it is none.
func refinementOf(t Type) *DistinctType {
	dt, ok := resolveTypeVar(t).(*DistinctType)
	if !ok || !dt.Refinement {
		return nil
	}
	return dt
}

// checkedDistinctOf answers t as a distinct type built only through a
// check: a validated type, `type Email String when ...`, or a `type` whose
// inner type is a refinement, `type UserId PositiveInt`. Nil for any other.
// Neither converts to anything; each has `try_from`, and its call form
// takes only a literal (spec §4 *Validated types*).
func checkedDistinctOf(t Type) *DistinctType {
	dt, ok := resolveTypeVar(t).(*DistinctType)
	if !ok || dt.Refinement || dt.Inner == nil {
		return nil
	}
	if dt.Rule != "" || refinementOf(DistinctInner(dt)) != nil {
		return dt
	}
	return nil
}

// IsCheckedDistinct reports whether t is a distinct type built only
// through a check of a rule (checkedDistinctOf).
func IsCheckedDistinct(t Type) bool {
	return checkedDistinctOf(t) != nil
}

// IsRefinement reports whether t's values are built through a check of a
// rule: a refinement, a struct that declares a `when` rule, or a checked
// distinct type (checkedDistinctOf).
func IsRefinement(t Type) bool {
	switch tt := resolveTypeVar(t).(type) {
	case *DistinctType:
		return tt.Refinement || checkedDistinctOf(tt) != nil
	case *StructType:
		return tt.Rule != ""
	}
	return false
}

// IsStructRefinement reports whether t is a struct with a `when` rule.
func IsStructRefinement(t Type) bool {
	st, ok := resolveTypeVar(t).(*StructType)
	return ok && st.Rule != ""
}

// RefinementRule answers t's rule as written, and "" for a type with none.
func RefinementRule(t Type) string {
	switch tt := resolveTypeVar(t).(type) {
	case *DistinctType:
		return tt.Rule
	case *StructType:
		return tt.Rule
	}
	return ""
}

// RefinementBase answers the type at the end of t's chain of parents: Float
// for `PositiveFloat` over `NonNegativeFloat` over `Float`. A type that is
// no refinement is its own base. Every parent of a refinement reaches the same
// base (checkRefinementParents), so the walk follows the first.
func RefinementBase(t Type) Type {
	for i := 0; i < maxRefinementChain; i++ {
		dt := refinementOf(t)
		if dt == nil || dt.Inner == nil {
			return t
		}
		t = DistinctInner(dt)
	}
	return t
}

// RefinementParents answers a refinement's parents in the order they are
// written, Inner first, and nil for a type that is none. A generic
// refinement's parents are at its type arguments: NonEmptyList<Int>'s is
// List<Int>. An intersection's parents are its members.
func RefinementParents(t Type) []Type {
	dt := refinementOf(t)
	if dt == nil || dt.Inner == nil {
		return nil
	}
	if dt.Intersection {
		return dt.TypeArgs
	}
	out := []Type{DistinctInner(dt)}
	subs := refinementSubs(dt)
	for _, p := range dt.MoreParents {
		if subs != nil {
			p = Substitute(p, subs)
		}
		out = append(out, p)
	}
	return out
}

// refinementSubs maps a generic refinement's type parameters to its type
// arguments, nil for one that is not generic or not instantiated.
func refinementSubs(dt *DistinctType) map[*TypeParam_]Type {
	if dt.Intersection || len(dt.TypeParamDefs) == 0 || len(dt.TypeArgs) != len(dt.TypeParamDefs) {
		return nil
	}
	subs := make(map[*TypeParam_]Type, len(dt.TypeParamDefs))
	for i, def := range dt.TypeParamDefs {
		subs[def] = dt.TypeArgs[i]
	}
	return subs
}

// refinementChain answers every refinement from t up, each parent's chain
// before its child, the parents in the order they are written, t last.
// A refinement two parents share is listed once, at its first place.
func refinementChain(t Type) []Type {
	var out []Type
	var visit func(t Type, depth int)
	visit = func(t Type, depth int) {
		dt := refinementOf(t)
		if dt == nil || depth > maxRefinementChain {
			return
		}
		for _, s := range out {
			if s == Type(dt) || TypesIdentical(s, dt) {
				return
			}
		}
		for _, p := range RefinementParents(dt) {
			visit(p, depth+1)
		}
		for _, s := range out {
			if TypesIdentical(s, dt) {
				return
			}
		}
		out = append(out, dt)
	}
	visit(t, 0)
	return out
}

// RefinementChecks answers the types whose rules a value of t satisfies,
// in the order `try_from` and the compile-time check of a literal
// evaluate them: each parent's, in the order the parents are written,
// then t's own. A refinement two parents share is listed once, at its first
// place: for `PositiveInt` over `NonNegativeInt and NonZeroInt`,
// [NonNegativeInt, NonZeroInt, PositiveInt]. A refinement with no rule of its
// own, an intersection among them, adds no check. A struct with a rule is
// its own one check; a checked distinct type has its inner type's checks
// and then its own rule's. A type with no rule anywhere has none.
func RefinementChecks(t Type) []Type {
	if IsStructRefinement(t) {
		return []Type{t}
	}
	if checked := checkedDistinctOf(t); checked != nil {
		out := RefinementChecks(DistinctInner(checked))
		if checked.Rule != "" {
			out = append(out, checked)
		}
		return out
	}
	var out []Type
	for _, s := range refinementChain(t) {
		if RefinementRule(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// RefinementAncestors answers every type a value of t converts up to: the
// refinements above t, nearest first, then the base. A type that is no
// refinement has none.
func RefinementAncestors(t Type) []Type {
	if refinementOf(t) == nil {
		return nil
	}
	chain := refinementChain(t)
	var out []Type
	for i := len(chain) - 2; i >= 0; i-- {
		out = append(out, chain[i])
	}
	if base := RefinementBase(t); refinementOf(base) == nil {
		out = append(out, base)
	}
	return out
}

// maxRefinementChain bounds a walk up a refinement's parents. A parent that
// reaches the refinement itself is a declaration cycle the type builder
// reports (distinctCycle) and leaves out.
const maxRefinementChain = 64

// refinesInto reports whether a value of have converts to want: want is
// one of have's ancestors or its base, by type identity, or want is a
// refinement with no rule of its own (an intersection among them) whose every
// parent have is or converts to, since its values are exactly the values
// of all its parents.
func refinesInto(have, want Type) bool {
	return refinesIntoDepth(have, want, 0)
}

func refinesIntoDepth(have, want Type, depth int) bool {
	if depth > maxRefinementChain {
		return false
	}
	for _, anc := range RefinementAncestors(have) {
		if TypesIdentical(anc, want) {
			return true
		}
	}
	return intoRulelessRefinement(have, want, func(have, parent Type) bool {
		return TypesIdentical(have, parent) || refinesIntoDepth(have, parent, depth+1)
	})
}

// intoRulelessRefinement reports whether want is a refinement with no rule of
// its own, have is not want, and have reaches every parent of want by
// reaches.
func intoRulelessRefinement(have, want Type, reaches func(have, parent Type) bool) bool {
	wt := refinementOf(want)
	if wt == nil || wt.Rule != "" || wt.Inner == nil || have == nil {
		return false
	}
	h := resolveTypeVar(have)
	if _, isVar := h.(*TypeVar); isVar {
		return false
	}
	if TypesIdentical(h, wt) {
		return false
	}
	parents := RefinementParents(wt)
	if len(parents) == 0 {
		return false
	}
	for _, p := range parents {
		if !reaches(h, p) {
			return false
		}
	}
	return true
}

// refinementParentsText spells a refinement's parents as its declaration
// writes them: `NonNegativeInt and NonZeroInt`.
func refinementParentsText(dt *DistinctType) string {
	var parts []string
	for _, p := range RefinementParents(dt) {
		parts = append(parts, formatTypeForError(p))
	}
	return strings.Join(parts, " and ")
}

// refinementBaseOf answers what a `try_from` builds a value of t from:
// the base at the end of a refinement's chain, the base of a distinct type's
// inner type (`String` for `type Email String when ...`, `Int` for `type
// UserId PositiveInt`), or the record of a struct's fields when the struct
// has a rule. Nil for any other type.
func refinementBaseOf(t Type) Type {
	switch tt := t.(type) {
	case *DistinctType:
		if tt.Refinement && tt.Inner != nil {
			return RefinementBase(tt)
		}
		if tt.Inner != nil {
			return RefinementBase(DistinctInner(tt))
		}
	case *StructType:
		if tt.Rule != "" {
			fields := tt.Fields
			if len(tt.TypeParamDefs) > 0 && len(tt.TypeArgs) == len(tt.TypeParamDefs) {
				// A generic struct refinement's fields at its arguments.
				subs := make(map[*TypeParam_]Type, len(tt.TypeParamDefs))
				for i, def := range tt.TypeParamDefs {
					subs[def] = tt.TypeArgs[i]
				}
				fields = make([]FieldDef, len(tt.Fields))
				for i, f := range tt.Fields {
					f.Type = Substitute(f.Type, subs)
					fields[i] = f
				}
			}
			return &AnonStructType{Fields: fields}
		}
	}
	return nil
}

// eraseRefinements is t with each distinct refinement in it replaced by its
// base: the type a value of t is at run time, which a comparison compares
// it as.
func eraseRefinements(t Type, depth int) Type {
	if t == nil || depth > 16 {
		return t
	}
	t = resolveTypeVar(t)
	if d, ok := t.(*DistinctType); ok && d.Refinement && d.Inner != nil {
		return eraseRefinements(RefinementBase(d), depth+1)
	}
	return mapType(t, func(p Type) Type { return eraseRefinements(p, depth+1) })
}

// refinementJoin is the type of a join whose sides have types have and
// next, two types that do not unify, when they differ only where a
// refinement meets an ancestor of it, or two refinements meet that share
// an ancestor (spec §4 *Chains and conversion*): the nearest ancestor the
// two share there (nearestSharedAncestor), at the top or nested at a
// position a value converts at (`List<NonNegativeInt>` beside `List<Int>`
// is a `List<Int>`). Each side must convert to the answer as an assignment
// would convert it, so an invariant position, where nothing converts,
// joins nothing. Nil when the two do not join this way.
func (c *checker) refinementJoin(have, next Type) Type {
	if have == nil || next == nil {
		return nil
	}
	joined, ok := refinementJoinShape(have, next, 0)
	if !ok || joined == nil {
		return nil
	}
	// A position both sides leave open stays open, and the unifications
	// below make the two one: `(List<?>, NonNegativeInt)` beside `(List<?>,
	// Int)` is a `(List<?>, Int)`.
	if c.unifyInto(joined, have, nil) != nil || c.unifyInto(joined, next, nil) != nil {
		return nil
	}
	return joined
}

// refinementJoinShape walks a and b in step and answers the type each
// position joins to: the type itself where the two are equal, the nearest
// shared ancestor where a refinement is on either side, and the same
// shape over the joined parts where the two have the same shape
// (pairedTypeChildren), a function's parameters excepted, which must be
// the same. ok is false where the two differ any other way.
func refinementJoinShape(a, b Type, depth int) (Type, bool) {
	if depth > 16 || a == nil || b == nil {
		return nil, false
	}
	a, b = resolveTypeVar(a), resolveTypeVar(b)
	if TypesEqual(a, b) {
		return embedsJoin(a, b)
	}
	// A position one side leaves open is the other's: `Ok(n)`, a
	// Result<PositiveInt, ?>, beside `Err(n)`, a Result<Int, Int>, joins
	// as Result<Int, Int>, and refinementJoin binds the open one.
	if _, open := a.(*TypeVar); open {
		if _, both := b.(*TypeVar); both {
			return a, true
		}
	}
	if _, open := a.(*TypeVar); open && !containsTypeVar(b) {
		return b, true
	}
	if _, open := b.(*TypeVar); open && !containsTypeVar(a) {
		return a, true
	}
	if refinementOf(a) != nil || refinementOf(b) != nil {
		j := nearestSharedAncestor(a, b)
		return j, j != nil
	}
	if st, ok := a.(*StructType); ok && st.Rule != "" {
		// A validated struct's instances are checked apart.
		return nil, false
	}
	as, bs, same := pairedTypeChildren(a, b)
	if !same {
		return nil, false
	}
	params := 0
	if at, ok := a.(*FuncType); ok {
		// A function's result joins; its parameters, which a value
		// enters the other way, must be the same.
		if at.DefaultCount != b.(*FuncType).DefaultCount {
			return nil, false
		}
		params = len(at.Params)
	}
	out := make([]Type, len(as))
	for i := range as {
		if i < params {
			if !TypesIdentical(as[i], bs[i]) {
				return nil, false
			}
			out[i] = as[i]
			continue
		}
		j, ok := refinementJoinShape(as[i], bs[i], depth+1)
		if !ok {
			return nil, false
		}
		out[i] = j
	}
	return withTypeChildren(a, out), true
}

// nearestSharedAncestor answers the nearest type a value of a and a value
// of b both convert up to: one of them when it is an ancestor of the other
// (PositiveInt and NonZeroInt meet at NonZeroInt), else the shared ancestor
// no other shared ancestor refines, and where several are that near
// (two refinements each of NonNegativeInt and NonZeroInt), the nearest
// ancestor those share, so the walk ends at the base at the latest
// (NonNegativeInt and NonZeroInt meet at Int). Nil when the two share
// nothing: refinements of different bases, or a refinement and an
// unrelated type.
func nearestSharedAncestor(a, b Type) Type {
	up := func(t Type) []Type {
		return append([]Type{t}, RefinementAncestors(t)...)
	}
	var shared []Type
	for _, x := range up(a) {
		for _, y := range up(b) {
			if TypesIdentical(x, y) {
				shared = append(shared, x)
				break
			}
		}
	}
	for len(shared) > 0 {
		var nearest []Type
		for _, x := range shared {
			below := false
			for _, y := range shared {
				if y != x && refinesInto(y, x) {
					below = true
					break
				}
			}
			if !below {
				nearest = append(nearest, x)
			}
		}
		if len(nearest) == 1 {
			return nearest[0]
		}
		if len(nearest) == 0 {
			return nil
		}
		// Several shared ancestors equally near: keep those every one of
		// them converts up to.
		var above []Type
		for _, x := range shared {
			all := true
			for _, n := range nearest {
				if !refinesInto(n, x) {
					all = false
					break
				}
			}
			if all {
				above = append(above, x)
			}
		}
		shared = above
	}
	return nil
}

// intersectionOrigin is the Origin of every intersection of refinements,
// which no declaration carries: two intersections are one type when their
// members are (NewRefinementIntersection).
const intersectionOrigin = "\x00intersection"

// NewRefinementIntersection is `A and B` of refinements of one base: the
// refinement whose parents are the members, with no rule of its own (spec §4
// *Intersections of refinements*). A member that another member converts to
// says nothing more and is dropped, so `PositiveInt and NonZeroInt` is
// `PositiveInt`; a member that is itself an intersection is spliced in.
// One member left is that member. The members are sorted by name, so an
// intersection is one type whatever order it is written in.
func NewRefinementIntersection(members []Type) Type {
	var flat []Type
	for _, m := range members {
		if dt := refinementOf(m); dt != nil && dt.Intersection {
			flat = append(flat, dt.TypeArgs...)
			continue
		}
		flat = append(flat, m)
	}
	var out []Type
	for i, m := range flat {
		redundant := false
		for j, o := range flat {
			if i == j {
				continue
			}
			if TypesIdentical(m, o) {
				redundant = redundant || j < i
				continue
			}
			if refinesInto(o, m) {
				redundant = true
			}
		}
		if !redundant {
			out = append(out, m)
		}
	}
	if len(out) == 0 && len(flat) > 0 {
		out = flat[:1]
	}
	if len(out) == 1 {
		return out[0]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	names := make([]string, len(out))
	for i, m := range out {
		names[i] = m.String()
	}
	return &DistinctType{
		Origin:       intersectionOrigin,
		Name:         strings.Join(names, " and "),
		Inner:        out[0],
		MoreParents:  out[1:],
		TypeArgs:     out,
		Refinement:   true,
		Intersection: true,
	}
}

// convertsUpTo reports whether a value of have converts to want through
// its refinements alone: the two join as want (refinementJoin), so a spread
// of NonEmptyStrings fits a List<String> literal.
func (c *checker) convertsUpTo(want, have Type) bool {
	j := c.refinementJoin(want, have)
	return j != nil && TypesIdentical(j, want)
}
