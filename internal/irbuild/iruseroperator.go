package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// userOperatorCall selects the impl through operImplFor. Exact parameter kinds
// need no coercion instructions; wider signatures are declined.
//
// Inside a std generic instance built at a program type (`total + x` in
// `Iter.sum<Cents>`), the impl is the program's: the instance interns the
// program's kinds in the calling gen's tables, so that gen's operator index
// selects it, as callerImplPlan selects an interface impl there.
func (bl *irScalarBuilder) userOperatorCall(at *ast.Binary, left, right ir.Temp, lk, rk kind) (ir.Temp, kind, bool, bool) {
	spec, found := operIfaceByOp[at.Op]
	if !found || lk.def == nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	og := bl.g
	it, _, rival := og.operImplFor(spec.nomi, lk, spec.method, []kind{lk, rk})
	params, result := []kind{lk, rk}, kindInvalid
	if it != nil {
		params, result = it.params, it.result
	}
	if s := bl.g.stdInsts; it == nil && !rival && s != nil && s.caller != nil && s.caller != bl.g && bl.g.stdModule != "" {
		og, it, params, result, rival = s.callerOperImpl(spec, lk, rk)
	}
	if rival || it == nil || !it.lowerable || len(params) != 2 || len(it.params0) != 2 || params[0] != lk || params[1] != rk || !irCallableValueKind(result) {
		return ir.NoTemp, kindInvalid, false, false
	}
	n := ir.NewCall(bl.g.irNodePos(at), bl.f.NewTemp(), irCallSite(at), og.irCalleeSym(it, spec.nomi+"."+spec.method), left, right)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: result, deferrable: true})
	return n.Dst(), result, false, true
}

// callerOperImpl is the program's operator impl for `lk op rk` that a std
// instance's operator names: the calling gen's own, or, for a type another
// file declares and implements the operator beside (lk is the caller's
// mirror of it), the declaring gen's, with its parameter and result kinds
// named in the caller's tables. The sibling route takes a closed operator
// only (rk is lk), whose operands are the one declaration.
func (s *stdInstances) callerOperImpl(spec *operIfaceSpec, lk, rk kind) (*gen, *implItem, []kind, kind, bool) {
	if it, _, rival := s.caller.operImplFor(spec.nomi, lk, spec.method, []kind{lk, rk}); it != nil || rival {
		if it == nil {
			return nil, nil, nil, kindInvalid, rival
		}
		return s.caller, it, it.params, it.result, rival
	}
	if lk != rk || lk.tag != tagNamed || lk.def == nil || lk.def.decl == nil || lk.def.instOrigin != nil || s.caller.reg == nil {
		return nil, nil, nil, kindInvalid, false
	}
	for _, g := range s.caller.reg.gens {
		if g == nil || g == s.caller {
			continue
		}
		for _, d := range g.operOrder {
			if d.ifaceName != spec.nomi || d.recv.tag != tagNamed || d.recv.def == nil || d.recv.def.decl != lk.def.decl {
				continue
			}
			it, _, rival := g.operImplFor(spec.nomi, d.recv, spec.method, []kind{d.recv, d.recv})
			if it == nil || rival {
				return nil, nil, nil, kindInvalid, rival
			}
			params := make([]kind, len(it.params))
			for i, p := range it.params {
				ip, ok := s.caller.importKind(p)
				if !ok {
					return nil, nil, nil, kindInvalid, false
				}
				params[i] = ip
			}
			result, ok := s.caller.importKind(it.result)
			if !ok {
				return nil, nil, nil, kindInvalid, false
			}
			return g, it, params, result, false
		}
	}
	return nil, nil, nil, kindInvalid, false
}
