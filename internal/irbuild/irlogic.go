package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irNodePos is a node's own position as an IR position.
//
// Many constructs need a position for a node that is not the one being
// lowered: `ir.BeginShortCircuit` takes three, the operator's on the branch
// test and each operand's on its own copy.
func (g *gen) irNodePos(n ast.Node) ir.Pos {
	line, col := nodePos(n)
	return g.irPos(line, col)
}
