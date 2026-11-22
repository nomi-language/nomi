package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// listCaseTest tests a list pattern with no prefix over subj, a list the
// pattern takes apart (irCaseListKind): the length test, then on its success edge each
// head read and matched in order, then each tail, read from the list's last
// cells, then the rest. A rest before tails is a copy of the elements
// between them (ir.ProjListSlice). A rest at the end is the list's suffix,
// sharing its cells, and a pattern there (`[a, ..[b]]`) is matched against
// it as any sub-pattern is against its value. `[..rest]` has no length to
// test. Any failed test branches to next.
func (bl *irScalarBuilder) listCaseTest(pat *ast.ListPattern, subj ir.Temp, k kind, next *ir.Block) bool {
	if !irCaseListKind(k) {
		return false
	}
	spread := !isNilNode(pat.TailSpread)
	fixed := len(pat.Heads) + len(pat.Tails)
	if spread && len(pat.Tails) > 0 {
		switch pat.TailSpread.(type) {
		case *ast.IdentPattern, *ast.WildcardPattern:
		default:
			// The parser admits a pattern for the rest only at the end.
			panic("irbuild: listCaseTest: the parser admits a pattern for the rest only at the end: with elements after the `..` it is \"a list pattern's `..` takes a name or nothing when elements follow it\"")
		}
	}
	pos := bl.g.irNodePos(pat)
	if !spread || fixed > 0 {
		var match *ir.Match
		if spread {
			match = ir.NewMatchListMinInto(pos, bl.f.NewTemp(), subj, fixed)
		} else {
			match = ir.NewMatchListLenInto(pos, bl.f.NewTemp(), subj, fixed)
		}
		bl.b.Append(match)
		bl.side(match.Dst(), irScalarSide{k: kindBool})
		bl.passOrFail(pat, match.Dst(), next)
	}
	elem := k.comp.parts[0]
	element := func(sub ast.Node, list ir.Temp, at int) bool {
		if _, wild := sub.(*ast.WildcardPattern); wild {
			return true
		}
		proj := ir.NewProjElem(pos, bl.f.NewTemp(), list, at, irParamShape(elem))
		bl.b.Append(proj)
		bl.side(proj.Dst(), irScalarSide{k: elem})
		return bl.patternTest(sub, proj.Dst(), elem, next)
	}
	for i, head := range pat.Heads {
		if !element(head, subj, i) {
			return false
		}
	}
	if len(pat.Tails) > 0 {
		last := ir.NewProjSuffix(pos, bl.f.NewTemp(), subj, -len(pat.Tails), irParamShape(k))
		bl.b.Append(last)
		bl.side(last.Dst(), irScalarSide{k: k})
		for j, tail := range pat.Tails {
			if !element(tail, last.Dst(), j) {
				return false
			}
		}
		if id, ok := pat.TailSpread.(*ast.IdentPattern); ok {
			mid := ir.NewProjListSlice(pos, bl.f.NewTemp(), subj, len(pat.Heads), len(pat.Tails), irParamShape(k))
			bl.b.Append(mid)
			bl.side(mid.Dst(), irScalarSide{k: k})
			bl.patternBinding(id, id.Name, mid.Dst(), k)
		}
		return true
	}
	if _, wild := pat.TailSpread.(*ast.WildcardPattern); !spread || wild {
		return true
	}
	rest := subj
	if len(pat.Heads) > 0 {
		suffix := ir.NewProjSuffix(pos, bl.f.NewTemp(), subj, len(pat.Heads), irParamShape(k))
		bl.b.Append(suffix)
		bl.side(suffix.Dst(), irScalarSide{k: k})
		rest = suffix.Dst()
	}
	return bl.patternTest(pat.TailSpread, rest, k, next)
}
