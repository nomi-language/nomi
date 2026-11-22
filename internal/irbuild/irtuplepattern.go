package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// tupleCaseTest tests a tuple pattern: each component but a `_` read off
// subj and matched in order, so a later component is read only once the
// tests before it passed. A tuple of names tests nothing.
func (bl *irScalarBuilder) tupleCaseTest(pat *ast.TuplePattern, subj ir.Temp, k kind, next *ir.Block) bool {
	if !irRetainedTupleKind(k) || len(pat.Patterns) != len(k.comp.parts) {
		// The checker types the pattern against the tuple's type.
		panic("irbuild: a tuple pattern of " + strconv.Itoa(len(pat.Patterns)) + " over " + k.nomi())
	}
	for i, component := range pat.Patterns {
		if _, wildcard := component.(*ast.WildcardPattern); wildcard {
			continue
		}
		part := bl.tupleProjection(pat, subj, i, k.comp.parts[i])
		if !bl.patternTest(component, part, k.comp.parts[i], next) {
			return false
		}
	}
	return true
}

// patternBinding binds name, which at declares, to part at kind k, and
// answers the key it is declared under: the position the name is written
// at in at, or, for a name the builder makes, a key of its own
// (madeLocal), which a read the builder writes carries (madeRead).
func (bl *irScalarBuilder) patternBinding(at ast.Node, name string, part ir.Temp, k kind) analysis.Pos {
	if ast.IsDiscardName(name) {
		return analysis.Pos{}
	}
	if bl.orJoin != nil {
		// An alternative of an `or` pattern: the name is declared once,
		// where the alternatives join (irorpattern.go).
		bl.orJoinCopy(at, name, part, k)
		return analysis.Pos{}
	}
	// Pattern declarations belong to this arm, including when they shadow an
	// enclosing declaration. Its caller owns the cloned lexical symbol tables.
	sym := ir.NewSymbol(name)
	bind := ir.NewBind(bl.g.irNodePos(at), bl.f.NewTemp(), part, sym)
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: k})
	line, col, written := irBindingSite(at, name)
	key := analysis.Pos{Line: line, Col: col}
	if !written {
		key = bl.g.madeLocal()
	}
	bl.declareKey(key, bind.Dst(), k)
	bl.declareBindAt(bind, k, line, col)
	return key
}
