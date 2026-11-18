package irbuild

import (
	"github.com/nomi-language/nomi/internal/ir"
)

// irModule owns this compilation unit's retained declarations.
func (g *gen) irModule() *ir.Module {
	if g.irMod == nil {
		g.irMod = ir.NewModule(g.nomiPath)
	}
	return g.irMod
}
