package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// callDefaults follows fillParamDefaults: explicit values are already evaluated,
// then defaults see module functions and earlier parameters, never caller locals.
// Module value references remain outside this retained path.
func (bl *irScalarBuilder) callDefaults(sig *fnSig, args []ir.Temp) bool {
	if len(sig.decl.Params) != len(args) {
		return false
	}
	bound, kinds, parent, defaults := bl.bound, bl.boundK, bl.parent, bl.defaultScope
	scopes := bl.g.scopes
	bl.bound, bl.boundK = map[string]ir.Temp{}, map[string]kind{}
	bl.parent, bl.defaultScope = nil, true
	bl.g.scopes = []map[string]local{scopes[0], {}}
	defer func() {
		bl.bound, bl.boundK, bl.parent, bl.defaultScope = bound, kinds, parent, defaults
		bl.g.scopes = scopes
	}()
	for i, p := range sig.decl.Params {
		if args[i] == ir.NoTemp {
			if p.Default == nil {
				return false
			}
			restore := func() {}
			if unit, foreign := bl.g.declaredIn[p.Default]; foreign {
				restore = bl.g.lowerNamesFrom(unit)
			}
			src, k, _, ok := bl.lowerWant(p.Default, sig.params[i])
			restore()
			if !ok || k != sig.params[i] || !irCallOperandKind(k) {
				return false
			}
			args[i] = src
		}
		if p.Destructure == nil && !ast.IsDiscardName(p.Name) {
			bl.bound[p.Name], bl.boundK[p.Name] = args[i], sig.params[i]
		}
	}
	return true
}
