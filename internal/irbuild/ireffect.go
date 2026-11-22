package irbuild

import (
	"github.com/nomi-language/nomi/internal/ir"
)

// irModule owns this compilation unit's retained declarations. One opened
// while the lowering builds abstract bodies is a module of them
// (abstract.go).
func (g *gen) irModule() *ir.Module {
	if g.irMod == nil {
		g.irMod = ir.NewModule(g.nomiPath)
		if g.dc != nil && g.dc.abstract {
			g.irMod.SetAbstract()
		}
	}
	return g.irMod
}
