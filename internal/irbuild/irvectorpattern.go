package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// VECTOR AND SET PATTERNS.
//
// A vector pattern, `[first, ..rest, last]` over a vector, is a list pattern over a
// vector: a length test (MatchVectorLen without a `..`, MatchVectorMin with
// one), then each head read from the front and each tail from the end
// (ProjVectorElem), and the rest a window over the elements between them
// (ProjVectorSlice), which shares the vector's storage. An element's own
// pattern is tested as a nested pattern is (patternTest).
//
// A set pattern, `["admin", ..rest]` over a set, tests membership as a map pattern
// tests keys: each element is an expression, evaluated in pattern order, and
// `Set.contains` asks for it. Without a `..` the set holds exactly those
// elements: the rest, the set without them, must be empty. A named rest is
// the set without them.

// irVectorCaseKind is a `case` subject a vector pattern tests.
func irVectorCaseKind(k kind) bool {
	_, ok := vectorElem(k)
	return ok
}

// irDistinctOverVector is a distinct type over a vector, which `Path[...]`
// or a bare `[...]` takes apart through its inner vector.
func irDistinctOverVector(k kind) bool {
	return k.tag == tagNamed && irCompositeDistinct(k.def) && irVectorCaseKind(k.def.inner)
}

// irDistinctOverSet is a distinct type over a set, `Tags[...]`'s.
func irDistinctOverSet(k kind) bool {
	return k.tag == tagNamed && irCompositeDistinct(k.def) && irSetCaseKind(k.def.inner)
}

// irSetCaseKind is a `case` subject a set pattern tests.
func irSetCaseKind(k kind) bool {
	return irRetainedSetKind(k)
}

// vectorCaseTest tests a vector pattern with no prefix over subj, leaving
// the builder where its last test passed with the pattern's names bound and
// branching to next on any failed test.
func (bl *irScalarBuilder) vectorCaseTest(pat *ast.ListPattern, subj ir.Temp, k kind, next *ir.Block) bool {
	fixed := len(pat.Heads) + len(pat.Tails)
	elem, isVector := vectorElem(k)
	if !isVector {
		return false
	}
	pos := bl.g.irNodePos(pat)
	var match *ir.Match
	switch {
	case pat.TailSpread == nil:
		match = ir.NewMatchVectorLenInto(pos, bl.f.NewTemp(), subj, fixed)
	case fixed > 0:
		match = ir.NewMatchVectorMinInto(pos, bl.f.NewTemp(), subj, fixed)
	}
	if match != nil {
		// `[..rest]` matches every vector, so it has no length to test.
		bl.b.Append(match)
		bl.side(match.Dst(), irScalarSide{k: kindBool})
		bl.passOrFail(pat, match.Dst(), next)
	}
	test := func(sub ast.Node, at int) bool {
		if _, wild := sub.(*ast.WildcardPattern); wild {
			return true
		}
		read := ir.NewProjVectorElem(pos, bl.f.NewTemp(), subj, at, irParamShape(elem))
		bl.b.Append(read)
		bl.side(read.Dst(), irScalarSide{k: elem})
		return bl.patternTest(sub, read.Dst(), elem, next)
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
	if id, named := pat.Rest().(*ast.IdentPattern); named {
		rest := ir.NewProjVectorSlice(pos, bl.f.NewTemp(), subj, len(pat.Heads), len(pat.Tails), irParamShape(k))
		bl.b.Append(rest)
		bl.side(rest.Dst(), irScalarSide{k: k})
		bl.patternBinding(id, id.Name, rest.Dst(), k)
	}
	return true
}

// setCaseTest tests a set pattern with no prefix over subj, leaving the
// builder with its rest bound and branching to next when the set lacks an
// element, or, without a `..`, holds another.
func (bl *irScalarBuilder) setCaseTest(pat *ast.ListPattern, subj ir.Temp, k kind, next *ir.Block) bool {
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
	_, namedRest := pat.Rest().(*ast.IdentPattern)
	keepRest := pat.TailSpread == nil || namedRest
	rest := subj
	for _, e := range pat.Heads {
		v, vk, _, ok := bl.lower(e)
		if !ok {
			return false
		}
		if vk != elem {
			if v, vk, ok = bl.convertTo(e, v, vk, elem); !ok {
				bl.g.dc.note("a set pattern element of kind " + vk.nomi() + " over " + k.nomi())
				return false
			}
		}
		bl.passOrFail(e, host(e, "Set.contains", kindBool, subj, v), next)
		if keepRest {
			rest = host(e, "Set.remove", k, rest, v)
		}
	}
	if pat.TailSpread == nil {
		size := host(pat, "Set.size", kindInt, rest)
		zero := ir.NewInt(pos, bl.f.NewTemp(), 0)
		bl.b.Append(zero)
		bl.side(zero.Dst(), irScalarSide{k: kindInt})
		empty := ir.NewMatchLitInto(pos, bl.f.NewTemp(), size, zero.Dst())
		bl.b.Append(empty)
		bl.side(empty.Dst(), irScalarSide{k: kindBool})
		bl.passOrFail(pat, empty.Dst(), next)
		return true
	}
	if id, named := pat.Rest().(*ast.IdentPattern); named {
		bl.patternBinding(id, id.Name, rest, k)
	}
	return true
}
