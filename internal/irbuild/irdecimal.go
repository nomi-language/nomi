package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"

	"github.com/nomi-language/nomi/rt"
)

func (bl *irScalarBuilder) decimalLiteral(t *ast.DecimalLit) (ir.Temp, kind, bool, bool) {
	if _, err := rt.ParseDecimalLexeme(t.Lexeme); err != nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	c := ir.NewDecimal(bl.g.irNodePos(t), bl.f.NewTemp(), t.Lexeme)
	bl.b.Append(c)
	return c.Dst(), decimalKind(), true, true
}
