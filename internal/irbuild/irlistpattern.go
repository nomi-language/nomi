package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// listCaseTest admits flat bindings after a checked length predicate. The
// success arm alone reads elements and suffixes, preserving persistent cells.
//
// k is a list the pattern takes apart (irCaseListKind).
func (bl *irScalarBuilder) listCaseTest(pattern ast.Node, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	pat, ok := pattern.(*ast.ListPattern)
	if !ok || pat.TypeName != nil || !irCaseListKind(k) {
		return false
	}
	spread := !isNilNode(pat.TailSpread)
	simple := func(n ast.Node) bool {
		switch n.(type) {
		case *ast.IdentPattern, *ast.WildcardPattern:
			return true
		}
		return false
	}
	// A head may also be a flat tuple of names and wildcards over a tuple
	// element (`[(w, _), ..tail]`); its components are projected from the
	// element in the success arm.
	flatTuple := func(n ast.Node) bool {
		tp, isTuple := n.(*ast.TuplePattern)
		if !isTuple || k.comp == nil {
			return false
		}
		elem := k.comp.parts[0]
		if elem.tag != tagTuple || elem.comp == nil || len(elem.comp.parts) != len(tp.Patterns) {
			return false
		}
		for _, c := range tp.Patterns {
			if !simple(c) {
				return false
			}
		}
		return true
	}
	// A rest that is a pattern of its own (`[a, ..[b]]`) tests the suffix.
	nested := len(pat.Tails) > 0 || (spread && !simple(pat.TailSpread))
	for _, head := range pat.Heads {
		if !simple(head) && !flatTuple(head) {
			nested = true
		}
	}
	if nested {
		return bl.listCaseTestNested(pat, subj, k, arm, next)
	}
	if spread && len(pat.Heads) == 0 {
		// `[..rest]` matches every list, so there is no length test: the
		// arm runs, with rest the whole subject. The branch is on a
		// constant so the arms after it, which the checker admits, keep
		// the edge they are lowered under.
		pos := bl.g.irNodePos(pat)
		always := ir.NewBool(pos, bl.f.NewTemp(), true)
		bl.b.Append(always)
		bl.side(always.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(pos, always.Dst(), arm.ID(), next.ID()))
		bl.b = arm
		if id, ok := pat.TailSpread.(*ast.IdentPattern); ok {
			bl.patternBinding(id, id.Name, subj, k)
		}
		return true
	}
	var match *ir.Match
	if spread {
		match = ir.NewMatchListMinInto(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, len(pat.Heads))
	} else {
		match = ir.NewMatchListLenInto(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, len(pat.Heads))
	}
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(pat), match.Dst(), arm.ID(), next.ID()))
	bl.b = arm
	bind := func(at ast.Node, projection *ir.Proj, part kind) {
		bl.b.Append(projection)
		bl.side(projection.Dst(), irScalarSide{k: part})
		if id, ok := at.(*ast.IdentPattern); ok {
			bl.patternBinding(id, id.Name, projection.Dst(), part)
		}
	}
	for i, head := range pat.Heads {
		elem := k.comp.parts[0]
		proj := ir.NewProjElem(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, i, irParamShape(elem))
		bind(head, proj, elem)
		if tp, isTuple := head.(*ast.TuplePattern); isTuple {
			for j, c := range tp.Patterns {
				id, named := c.(*ast.IdentPattern)
				if !named {
					continue
				}
				part := elem.comp.parts[j]
				read := bl.tupleProjection(tp, proj.Dst(), j, part)
				bl.patternBinding(id, id.Name, read, part)
			}
		}
	}
	if spread {
		bind(pat.TailSpread, ir.NewProjSuffix(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, len(pat.Heads), irParamShape(k)), k)
	}
	return true
}

// listCaseTestNested is listCaseTest for heads that test their element
// (`[.Num(a), 0, ..rest]`), for a pattern with tails (`[first, ..mid,
// last]`) and for a rest at the end that is a pattern of its own (`[a,
// ..[b]]`): after the length test each head is projected and matched in
// order, then each tail, read from the list's last cells, and any failed
// element test selects the next arm. A rest before tails is a copy of the
// elements between them (ir.ProjListSlice). A rest at the end is the
// list's suffix, sharing its cells, and a pattern there is matched against
// it as any nested pattern is against its value. The builder ends in arm.
func (bl *irScalarBuilder) listCaseTestNested(pat *ast.ListPattern, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	spread := !isNilNode(pat.TailSpread)
	fixed := len(pat.Heads) + len(pat.Tails)
	restPattern := false
	if spread {
		switch pat.TailSpread.(type) {
		case *ast.IdentPattern, *ast.WildcardPattern:
		default:
			// The parser admits a pattern for the rest only at the end.
			if len(pat.Tails) > 0 {
				panic("irbuild: listCaseTestNested: the parser admits a pattern for the rest only at the end: with elements after the `..` it is \"a list pattern's `..` takes a name or nothing when elements follow it\"")
			}
			restPattern = true
		}
	}
	var passed ir.Temp
	switch {
	case spread && fixed == 0:
		// `[..[a, b]]` puts no length test before the rest's own. The
		// branch is on a constant, as `[..rest]`'s is (listCaseTest).
		always := ir.NewBool(bl.g.irNodePos(pat), bl.f.NewTemp(), true)
		bl.b.Append(always)
		passed = always.Dst()
	case spread:
		match := ir.NewMatchListMinInto(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, fixed)
		bl.b.Append(match)
		passed = match.Dst()
	default:
		match := ir.NewMatchListLenInto(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, fixed)
		bl.b.Append(match)
		passed = match.Dst()
	}
	bl.side(passed, irScalarSide{k: kindBool})
	body := bl.f.NewBlock(bl.g.irNodePos(pat), "list elements")
	bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(pat), passed, body.ID(), next.ID()))
	bl.b = body
	elem := k.comp.parts[0]
	for i, head := range pat.Heads {
		proj := ir.NewProjElem(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, i, irParamShape(elem))
		bl.b.Append(proj)
		bl.side(proj.Dst(), irScalarSide{k: elem})
		target := bl.f.NewBlock(bl.g.irNodePos(head), "list element")
		if !bl.patternValueTest(head, proj.Dst(), elem, target, next) {
			return false
		}
	}
	if len(pat.Tails) > 0 {
		last := ir.NewProjSuffix(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, -len(pat.Tails), irParamShape(k))
		bl.b.Append(last)
		bl.side(last.Dst(), irScalarSide{k: k})
		for j, tail := range pat.Tails {
			proj := ir.NewProjElem(bl.g.irNodePos(pat), bl.f.NewTemp(), last.Dst(), j, irParamShape(elem))
			bl.b.Append(proj)
			bl.side(proj.Dst(), irScalarSide{k: elem})
			target := bl.f.NewBlock(bl.g.irNodePos(tail), "list element")
			if !bl.patternValueTest(tail, proj.Dst(), elem, target, next) {
				return false
			}
		}
		if id, ok := pat.TailSpread.(*ast.IdentPattern); ok {
			mid := ir.NewProjListSlice(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, len(pat.Heads), len(pat.Tails), irParamShape(k))
			bl.b.Append(mid)
			bl.side(mid.Dst(), irScalarSide{k: k})
			bl.patternBinding(id, id.Name, mid.Dst(), k)
		}
	} else if spread {
		suffix := ir.NewProjSuffix(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, len(pat.Heads), irParamShape(k))
		bl.b.Append(suffix)
		bl.side(suffix.Dst(), irScalarSide{k: k})
		if id, ok := pat.TailSpread.(*ast.IdentPattern); ok {
			bl.patternBinding(id, id.Name, suffix.Dst(), k)
		}
		if restPattern {
			target := bl.f.NewBlock(bl.g.irNodePos(pat.TailSpread), "list rest")
			if !bl.patternValueTest(pat.TailSpread, suffix.Dst(), k, target, next) {
				return false
			}
		}
	}
	bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(pat), arm.ID()))
	bl.b = arm
	return true
}
