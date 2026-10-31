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
	if !found {
		return ir.NoTemp, kindInvalid, false, false
	}
	og := bl.g
	it, _, rival := og.operImplFor(spec.nomi, lk, spec.method, []kind{lk, rk}, bl.g.checkedKind(at))
	params, result := []kind{lk, rk}, kindInvalid
	if it != nil {
		params, result = it.params, it.result
	}
	if s := bl.g.stdInsts; it == nil && !rival && s != nil && s.caller != nil && s.caller != bl.g && bl.g.stdModule != "" {
		og, it, params, result, rival = s.callerOperImpl(spec, lk, rk)
	}
	if it == nil && !rival && bl.g.stdModule == "" {
		// An impl another file writes.
		og, it, params, result, rival = bl.g.siblingOperImpl(spec, lk, rk)
	}
	if rival || it == nil || !it.lowerable || len(params) != 2 || len(it.params0) != 2 || params[0] != lk || params[1] != rk || !irCallableValueKind(result) {
		return ir.NoTemp, kindInvalid, false, false
	}
	n := ir.NewCall(bl.g.irNodePos(at), bl.f.NewTemp(), irCallSite(at), og.irCalleeSym(it, spec.nomi+"."+spec.method), left, right)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: result, deferrable: true})
	return n.Dst(), result, false, true
}

// bitwiseNot lowers `~~~operand`: Int's is `ir.OpNot`, and any other type's
// is a call of its `BitwiseNot` impl, std's (Byte's) or the program's.
func (bl *irScalarBuilder) bitwiseNot(t *ast.Unary, src ir.Temp, k kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if ak, modelled := bl.g.irArithKind(ir.OpNot, k, false); modelled {
		a := ir.NewUnaryArith(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), ir.OpNot, ak, src)
		bl.b.Append(a)
		return a.Dst(), k, false, true
	}
	if f := bl.g.stdlibUnaryOperatorFor(k); f != nil {
		if f.canon != nil {
			f = f.canon
		}
		if f.irBody == nil || f.rtCall != "" || !irCallableValueKind(f.result) {
			return no()
		}
		n := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), f.irBody.Sym(), src)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: f.result, deferrable: true})
		return n.Dst(), f.result, false, true
	}
	spec := operIfaceByName["BitwiseNot"]
	og := bl.g
	it, _, rival := og.operImplFor(spec.nomi, k, spec.method, []kind{k}, bl.g.checkedKind(t))
	params, result := []kind{k}, kindInvalid
	if it != nil {
		params, result = it.params, it.result
	}
	if it == nil && !rival && bl.g.stdModule == "" {
		// An impl another file writes.
		og, it, params, result, rival = bl.g.siblingOperImpl(spec, k)
	}
	if rival || it == nil || !it.lowerable || len(params) != 1 || params[0] != k || !irCallableValueKind(result) {
		return no()
	}
	n := ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), og.irCalleeSym(it, spec.nomi+"."+spec.method), src)
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
	return s.caller.siblingOperImpl(spec, lk, rk)
}

// siblingOperImpl is the operator impl for `lk op rk` that another file
// writes for lk's type (`impl Add<Int, Int> for Bag` apart from the call),
// with its parameter and result kinds named in this gen's tables. Each other
// gen selects among its own impls at its kinds for the operands, as
// operImplFor does here. Reports a rival when two fit. A unary operator's
// impl (`~~~`) has no rk: rest is empty.
func (g *gen) siblingOperImpl(spec *operIfaceSpec, lk kind, rest ...kind) (*gen, *implItem, []kind, kind, bool) {
	named := lk.tag == tagNamed && lk.def != nil && lk.def.decl != nil
	// kindInvalid: lookup — an operand with no kind has no impl to find, and the caller declines.
	if g.reg == nil || lk == kindInvalid || (named && (lk.def.instOrigin != nil || lk.def.genericOf != nil)) {
		return nil, nil, nil, kindInvalid, false
	}
	var og *gen
	var chosen *implItem
	for _, gg := range g.reg.gens {
		if gg == nil || gg == g {
			continue
		}
		var recv kind
		writes := false
		for _, d := range gg.operOrder {
			if d.ifaceName == spec.nomi && sameOperReceiver(d.recv, lk, named) {
				recv, writes = d.recv, true
				break
			}
		}
		if !writes {
			continue
		}
		args := []kind{recv}
		imported := true
		for _, k := range rest {
			there, ok := gg.importKind(k)
			if !ok {
				imported = false
				break
			}
			args = append(args, there)
		}
		if !imported {
			continue
		}
		it, _, rival := gg.operImplFor(spec.nomi, recv, spec.method, args)
		if rival || (it != nil && chosen != nil) {
			return nil, nil, nil, kindInvalid, true
		}
		if it != nil {
			og, chosen = gg, it
		}
	}
	if chosen == nil {
		return nil, nil, nil, kindInvalid, false
	}
	params := make([]kind, len(chosen.params))
	for i, p := range chosen.params {
		ip, ok := g.importKind(p)
		if !ok {
			return nil, nil, nil, kindInvalid, false
		}
		params[i] = ip
	}
	result, ok := g.importKind(chosen.result)
	if !ok {
		return nil, nil, nil, kindInvalid, false
	}
	return og, chosen, params, result, false
}

// sameOperReceiver reports whether an operator impl's receiver, in its own
// gen's tables, is lk: the one declaration for a type a file declares, the
// one kind for a scalar or a std host type such as Decimal, whose kinds are
// process-wide (`impl Multiply<Rate, Decimal> for Decimal` in another
// module, which the orphan rule admits through `Rate`).
func sameOperReceiver(recv, lk kind, named bool) bool {
	if recv == lk {
		return true
	}
	return named && recv.tag == tagNamed && recv.def != nil && recv.def.decl == lk.def.decl
}
