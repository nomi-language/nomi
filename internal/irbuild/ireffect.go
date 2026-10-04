package irbuild

import (
	"github.com/nomi-language/nomi/internal/ir"
)

// The observer counts declaration writes independently of the builder's Go-spelled text.
var irEffectObserved func(string, *ir.Symbol)

// irModule owns this compilation unit's retained declarations.
func (g *gen) irModule() *ir.Module {
	if g.irMod == nil {
		g.irMod = ir.NewModule(g.nomiPath)
	}
	return g.irMod
}
