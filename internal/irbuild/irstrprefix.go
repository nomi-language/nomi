package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// strPrefixPattern answers the parts of a string prefix pattern,
// `"/users/" + id`: the literal and the name the rest binds. The parser builds
// it as a Binary `+` whose left side is a string literal and whose right side
// is a name.
func strPrefixPattern(p ast.Node) (*ast.StringLit, *ast.IdentPattern, bool) {
	b, ok := p.(*ast.Binary)
	if !ok || b.Op != "+" {
		return nil, nil, false
	}
	lit, litOK := b.Left.(*ast.StringLit)
	rest, restOK := b.Right.(*ast.IdentPattern)
	return lit, rest, litOK && restOK
}

// strPrefixTest matches `"lit" + rest` against a String: the subject begins
// with lit's bytes, and rest binds the String of the bytes after them, so the
// subject is `lit + rest` exactly. A literal is a whole UTF-8 text, so the
// rest starts on a sequence boundary and is a valid String; it may start with
// a combining mark, as `"e" + rest` against "e\u{301}" binds "\u{301}".
func (bl *irScalarBuilder) strPrefixTest(pat ast.Node, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	lit, rest, ok := strPrefixPattern(pat)
	if !ok || k != kindString {
		return false
	}
	prefix, lk, _, ok := bl.litFor(lit, k)
	if !ok || lk != kindString {
		return false
	}
	pos := bl.g.irNodePos(pat)
	m := ir.NewMatchStrPrefixInto(pos, bl.f.NewTemp(), subj, prefix)
	bl.b.Append(m)
	bl.side(m.Dst(), irScalarSide{k: kindBool})
	bl.b.SetTerm(ir.NewBranch(pos, m.Dst(), arm.ID(), next.ID()))
	bl.b = arm
	if ast.IsDiscardName(rest.Name) {
		return true
	}
	suffix := ir.NewProjStrSuffix(bl.g.irNodePos(rest), bl.f.NewTemp(), subj, len(lit.Value))
	bl.b.Append(suffix)
	bl.side(suffix.Dst(), irScalarSide{k: kindString})
	bl.patternBinding(rest, rest.Name, suffix.Dst(), kindString)
	return true
}
