package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// userOperatorCall selects the impl through operImplFor. Exact parameter kinds
// need no coercion instructions; wider signatures are declined.
func (bl *irScalarBuilder) userOperatorCall(at *ast.Binary, left, right ir.Temp, lk, rk kind) (ir.Temp, kind, bool, bool) {
	spec, found := operIfaceByOp[at.Op]
	if !found || lk.def == nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	it, _, rival := bl.g.operImplFor(spec.nomi, lk, spec.method, []kind{lk, rk})
	if rival || it == nil || !it.lowerable || len(it.params) != 2 || len(it.params0) != 2 || it.params[0] != lk || it.params[1] != rk || !irCallableValueKind(it.result) {
		return ir.NoTemp, kindInvalid, false, false
	}
	n := ir.NewCall(bl.g.irNodePos(at), bl.f.NewTemp(), irCallSite(at), bl.g.irCalleeSym(it, spec.nomi+"."+spec.method), left, right)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: it.result, deferrable: true})
	return n.Dst(), it.result, false, true
}
