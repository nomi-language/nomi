package irbuild

import (
	"reflect"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

func irByteKind() kind  { return stdHostKindOfGoType(reflect.TypeFor[rt.Byte]()) }
func irBytesKind() kind { return stdHostKindOfGoType(reflect.TypeFor[rt.Bytes]()) }
func irByteValueKind(k kind) bool {
	// kindInvalid: sentinel — an unanchored runtime type must not match a refused operand.
	return k != kindInvalid && (k == irByteKind() || k == irBytesKind())
}

// irIntLiteralKind is a kind an Int literal pattern tests: Int, or Byte,
// whose literal patterns the checker types as Bytes.
func irIntLiteralKind(k kind) bool {
	// kindInvalid: sentinel — a program with no std has no Byte kind, which must not match a refused operand.
	return k == kindInt || (k != kindInvalid && k == irByteKind())
}

// byteLiteral reports whether the checker typed the Int literal t as a Byte,
// which it does where a Byte is expected (analysis/byte_literal.go), and held
// it to 0 to 255.
func (g *gen) byteLiteral(t *ast.IntLit) bool {
	if g.fa == nil || t.Big != nil || t.Value < 0 || t.Value > 255 {
		return false
	}
	ty := g.srcFA().ExprTypes[t]
	return ty != nil && analysis.ResolveTypeVar(ty) == analysis.TypeByte
}

// byteLiteral lowers an Int literal the checker typed as a Byte: a Byte
// constant.
func (bl *irScalarBuilder) byteLiteral(t *ast.IntLit) (ir.Temp, kind, bool, bool) {
	c := ir.NewByte(bl.g.irNodePos(t), bl.f.NewTemp(), uint8(t.Value))
	bl.b.Append(c)
	return c.Dst(), irByteKind(), true, true
}
