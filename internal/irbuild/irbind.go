package irbuild

// `ir.Bind` and `ir.Copy` in a destructuring prologue.
//
//	a FRESH name      ir.Bind   introduces a declaration
//	a REBIND or hold  ir.Copy   writes a temporary of the function's own
//
// The Bind's symbol is MINTED: a `Bind` is not a read of a declaration, it IS
// the declaration, and two bindings of one name in two scopes are two
// declarations.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irBindNode binds src to the Nomi name nomi and appends the node to the open
// prologue.
func (g *gen) irBindNode(at ast.Node, nomi string, src ir.Temp, k kind) *ir.Bind {
	b := ir.NewBind(g.irNodePos(at), g.irNewTemp(k), src, ir.NewSymbol(nomi))
	g.irAppend(b)
	return b
}

// irCopyNode copies src into a fresh temporary and appends the node to the
// open prologue.
func (g *gen) irCopyNode(at ast.Node, k kind, src ir.Temp) *ir.Copy {
	c := ir.NewCopy(g.irNodePos(at), g.irNewTemp(k), src)
	g.irAppend(c)
	return c
}

// irNewTemp is a fresh temporary of the open prologue's function holding a
// value of kind k.
func (g *gen) irNewTemp(k kind) ir.Temp { return g.irHold(expr{k: k}) }
