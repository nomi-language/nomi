package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// codepointLiteral lowers `'a'` as std's `Codepoint(97)`: the Int constant
// wrapped by MakeDistinct, which is what `Codepoint.from_int`'s `Some` arm
// builds. The lexer admits only ASCII, so the value is a valid scalar value
// and there is no check to make here.
func (bl *irScalarBuilder) codepointLiteral(t *ast.CodepointLit) (ir.Temp, kind, bool, bool) {
	k := codepointKind()
	// kindInvalid: lookup — std's Codepoint declaration is missing only when std/codepoints is not in the program; the caller names the decline.
	if k == kindInvalid {
		return ir.NoTemp, kindInvalid, false, false
	}
	pos := bl.g.irNodePos(t)
	c := ir.NewInt(pos, bl.f.NewTemp(), int64(t.Value))
	bl.b.Append(c)
	m := ir.NewMakeDistinct(pos, bl.f.NewTemp(), bl.g.irTypeSym(k.def), c.Dst())
	bl.b.Append(m)
	bl.side(m.Dst(), irScalarSide{k: k, pureMake: true})
	return m.Dst(), k, true, true
}

// codepointCaseTest tests a codepoint literal arm against a Codepoint
// subject: the subject's Int against the literal's value, which is the VM's
// word comparison.
func (bl *irScalarBuilder) codepointCaseTest(pat *ast.CodepointLit, subj ir.Temp, sk kind, target, next *ir.Block) bool {
	if sk != codepointKind() {
		return false
	}
	inner := bl.distinctProjection(pat, subj, sk, false, "ircodepoint.go codepointCaseTest")
	pos := bl.g.irNodePos(pat)
	c := ir.NewInt(pos, bl.f.NewTemp(), int64(pat.Value))
	bl.b.Append(c)
	m := ir.NewMatchLitInto(pos, bl.f.NewTemp(), inner, c.Dst())
	bl.b.Append(m)
	bl.b.SetTerm(ir.NewBranch(pos, m.Dst(), target.ID(), next.ID()))
	bl.b = target
	return true
}
