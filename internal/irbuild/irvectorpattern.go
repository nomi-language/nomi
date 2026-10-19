package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// VECTOR AND SET PATTERNS.
//
// A vector pattern, `#[first, ..rest, last]`, is a list pattern over a
// vector: a length test (MatchVectorLen without a `..`, MatchVectorMin with
// one), then each head read from the front and each tail from the end
// (ProjVectorElem), and the rest a window over the elements between them
// (ProjVectorSlice), which shares the vector's storage. An element's own
// pattern is tested as a nested pattern is (patternValueTest).
//
// A set pattern, `%["admin", ..rest]`, tests membership as a map pattern
// tests keys: each element is an expression, evaluated in pattern order, and
// `Set.contains?` asks for it. Without a `..` the set holds exactly those
// elements: the rest, the set without them, must be empty. A named rest is
// the set without them.

// irVectorCaseKind is a `case` subject a vector pattern tests.
func irVectorCaseKind(k kind) bool {
	_, ok := vectorElem(k)
	return ok || k == kindEmptyVector
}

// irDistinctOverVector is a distinct type over a vector, which `#Path[...]`
// or a bare `#[...]` takes apart through its inner vector.
func irDistinctOverVector(k kind) bool {
	return k.tag == tagNamed && irCompositeDistinct(k.def) && irVectorCaseKind(k.def.inner)
}

// irDistinctOverSet is a distinct type over a set, `%Tags[...]`'s.
func irDistinctOverSet(k kind) bool {
	return k.tag == tagNamed && irCompositeDistinct(k.def) && irSetCaseKind(k.def.inner)
}

// irSetCaseKind is a `case` subject a set pattern tests.
func irSetCaseKind(k kind) bool {
	return irRetainedSetKind(k)
}

// vectorCaseTest tests a vector pattern over subj, ending in arm with the
// pattern's names bound and branching to next on any failed test.
func (bl *irScalarBuilder) vectorCaseTest(pattern ast.Node, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	pat, ok := pattern.(*ast.VectorPattern)
	if !ok || pat.TypeName != nil {
		return false
	}
	fixed := len(pat.Heads) + len(pat.Tails)
	if k == kindEmptyVector {
		// The untyped `#[]` as the subject: no element pattern matches it,
		// so no element is read at the kind it is coerced to.
		v, vk, ok := bl.coerceEmpty(pattern, subj, k, bl.g.vectorKindOf(kindInt))
		if !ok {
			return false
		}
		subj, k = v, vk
	}
	elem, isVector := vectorElem(k)
	if !isVector {
		return false
	}
	pos := bl.g.irNodePos(pat)
	var match *ir.Match
	switch {
	case !pat.Spread:
		match = ir.NewMatchVectorLenInto(pos, bl.f.NewTemp(), subj, fixed)
	case fixed > 0:
		match = ir.NewMatchVectorMinInto(pos, bl.f.NewTemp(), subj, fixed)
	}
	body := bl.f.NewBlock(pos, "vector elements")
	if match != nil {
		bl.b.Append(match)
		bl.side(match.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(pos, match.Dst(), body.ID(), next.ID()))
	} else {
		// `#[..rest]` matches every vector. The branch is on a constant so
		// the arms after it keep the edge they are lowered under, as a
		// list's `[..rest]` does.
		always := ir.NewBool(pos, bl.f.NewTemp(), true)
		bl.b.Append(always)
		bl.side(always.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(pos, always.Dst(), body.ID(), next.ID()))
	}
	bl.b = body
	test := func(sub ast.Node, at int) bool {
		read := ir.NewProjVectorElem(pos, bl.f.NewTemp(), subj, at, irParamShape(elem))
		bl.b.Append(read)
		bl.side(read.Dst(), irScalarSide{k: elem})
		passed := bl.f.NewBlock(bl.g.irNodePos(sub), "vector element")
		return bl.patternValueTest(sub, read.Dst(), elem, passed, next)
	}
	for i, h := range pat.Heads {
		if !test(h, i) {
			return false
		}
	}
	for j, t := range pat.Tails {
		if !test(t, j-len(pat.Tails)) {
			return false
		}
	}
	if id, named := pat.Rest.(*ast.IdentPattern); named {
		rest := ir.NewProjVectorSlice(pos, bl.f.NewTemp(), subj, len(pat.Heads), len(pat.Tails), irParamShape(k))
		bl.b.Append(rest)
		bl.side(rest.Dst(), irScalarSide{k: k})
		bl.patternBinding(id, id.Name, rest.Dst(), k)
	}
	bl.b.SetTerm(ir.NewJump(pos, arm.ID()))
	bl.b = arm
	return true
}

// setCaseTest tests a set pattern over subj, ending in arm with its rest
// bound and branching to next when the set lacks an element, or, without a
// `..`, holds another.
func (bl *irScalarBuilder) setCaseTest(pattern ast.Node, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	pat, ok := pattern.(*ast.SetPattern)
	if !ok || pat.TypeName != nil {
		return false
	}
	if k == kindEmptySet {
		if len(pat.Elems) == 0 {
			// `%[]` against the untyped `%[]`, which it matches whatever
			// the element type.
			bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(pat), arm.ID()))
			bl.b = arm
			return true
		}
		v, vk, ok := bl.coerceEmpty(pattern, subj, k, bl.g.setKindOf(kindInt))
		if !ok {
			return false
		}
		subj, k = v, vk
	}
	elem, isSet := setElem(k)
	if !isSet {
		return false
	}
	pos := bl.g.irNodePos(pat)
	host := func(at ast.Node, name string, result kind, args ...ir.Temp) ir.Temp {
		c := ir.NewHostCall(bl.g.irNodePos(at), bl.f.NewTemp(), ir.OrdinaryCall,
			bl.g.irCalleeSym(name, name), args...)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: result})
		return c.Dst()
	}
	_, namedRest := pat.Rest.(*ast.IdentPattern)
	keepRest := !pat.Spread || namedRest
	rest := subj
	for _, e := range pat.Elems {
		v, vk, _, ok := bl.lower(e)
		if !ok {
			return false
		}
		if vk != elem {
			if v, vk, ok = bl.coerceEmpty(e, v, vk, elem); !ok || vk != elem {
				irDeclineNote("a set pattern element of kind " + vk.nomi() + " over " + k.nomi())
				return false
			}
		}
		held := v
		found := host(e, "Set.contains?", kindBool, subj, held)
		present := bl.f.NewBlock(bl.g.irNodePos(e), "set element")
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(e), found, present.ID(), next.ID()))
		bl.b = present
		if keepRest {
			rest = host(e, "Set.remove", k, rest, held)
		}
	}
	if !pat.Spread {
		size := host(pat, "Set.size", kindInt, rest)
		zero := ir.NewInt(pos, bl.f.NewTemp(), 0)
		bl.b.Append(zero)
		bl.side(zero.Dst(), irScalarSide{k: kindInt})
		empty := ir.NewMatchLitInto(pos, bl.f.NewTemp(), size, zero.Dst())
		bl.b.Append(empty)
		bl.side(empty.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(pos, empty.Dst(), arm.ID(), next.ID()))
		bl.b = arm
		return true
	}
	if id, named := pat.Rest.(*ast.IdentPattern); named {
		bl.patternBinding(id, id.Name, rest, k)
	}
	bl.b.SetTerm(ir.NewJump(pos, arm.ID()))
	bl.b = arm
	return true
}
