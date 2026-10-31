package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// not lowers `not operand`, whose operand's value is src: one ir.Not.
func (bl *irScalarBuilder) not(t *ast.Unary, src ir.Temp, k kind) (ir.Temp, kind, bool, bool) {
	if k != kindBool {
		return ir.NoTemp, kindInvalid, false, false
	}
	n := ir.NewNot(bl.g.irNodePos(t), bl.f.NewTemp(), src)
	bl.b.Append(n)
	return n.Dst(), kindBool, false, true
}

// notStage lowers a pipe whose stage is negated, `x |> not f()`: the pipe
// `x |> f()`, then its result negated.
func (bl *irScalarBuilder) notStage(t *ast.Binary, stage *ast.Unary) (ir.Temp, kind, bool, bool) {
	plain := *t
	plain.Right = stage.Right
	src, k, _, ok := bl.pipe(&plain)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.not(stage, src, k)
}
