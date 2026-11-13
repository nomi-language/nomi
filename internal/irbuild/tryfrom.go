package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A `?` that converts its error (spec §11 *Propagating with `?`*, rule 3).
//
// The checker applies the boundary error type F's `impl From<E>` at a `?`
// on a `Result<T, E>` whose E does not inhabit F, and records it on the
// `?`'s hover symbol: TryOpInfo.Convert, with the impl's `from` as the
// symbol's DispatchImpl (analysis's try_from.go). The `?` lowers as
//
//	case x { Ok(v) -> v, Err(e) -> return Err(F.from(e)) }
//
// the Err edge calling the impl the checker chose, read off the `?`'s
// DispatchImpl: this file's block that writes it (implItemPlan), or for
// another file's, the owner's site that is that function
// (qualSiblingIfaceImplPlan, selecting by the same record), and leaving through an
// ir.Try of the rebuilt `Err`, so every delivery a plain `?` gets (a `defer`
// run on the way out, a `with` restored) is the same. The Try always
// propagates; the edge after it is never taken.
//
// A `?` whose operand's error type inhabits the boundary's without being it
// (spec §11 *Propagating with `?`*) lowers the same way, its Err edge
// widening the error (wideningTry) as any position of the wider type takes a
// value (coerceEmpty): into an interface the error implements, boxed where
// the value's runtime type does not name its impl (eraseBoxed); into the
// variant of an enum that `embeds` the error's type (embedWiden); or a
// structure rebuilt at wider type arguments (rebuildWidened).

// recordedTryConversion is the `?`'s hover symbol when the checker applies a
// `From` impl at it, else nil.
func (g *gen) recordedTryConversion(t *ast.TryOp) *analysis.Symbol {
	if g.fa == nil {
		return nil
	}
	sym := g.srcFA().References[analysis.Pos{Line: t.Line, Col: t.Col}]
	if sym == nil || sym.Kind != analysis.SymbolTryOp || sym.TryOp == nil || sym.TryOp.Convert == nil || sym.DispatchImpl == nil {
		return nil
	}
	return sym
}

// convertingTry lowers a converting `?` whose operand, a Result of kind k,
// is already in src.
func (bl *irScalarBuilder) convertingTry(t *ast.TryOp, operand, whole ast.Node, src ir.Temp, k kind, pure bool, conv *analysis.Symbol) (ir.Temp, kind, bool, bool) {
	return bl.errEdgeTry(t, operand, whole, src, k, pure, func(e ir.Temp, ek, fk kind) (ir.Temp, bool) {
		f, ok := bl.fromCall(t, conv, e, ek, fk)
		if !ok {
			bl.g.dc.note("a converting `?` whose `From` impl does not lower: " + conv.TryOp.Convert.Label())
		}
		return f, ok
	})
}

// irTryWidens reports whether an operand of d, whose error payloads are
// carried, exits to boundary by widening its error to boundary's: both are
// Results and their error types differ. The checker accepted the `?`, so
// the operand's error inhabits the boundary's.
func irTryWidens(boundary kind, d *typeDef, carried []kind) bool {
	if !irTryBoundaryKind(boundary) || d.preludeOf.spec != boundary.def.preludeOf.spec || len(carried) != 1 {
		return false
	}
	u := &boundary.def.variants[1]
	if len(u.payloads) != 1 {
		return false
	}
	return u.payloads[0].k != carried[0]
}

// wideningTry lowers a `?` whose operand's error widens to the boundary's
// (irTryWidens), the operand, a Result of kind k, already in src.
func (bl *irScalarBuilder) wideningTry(t *ast.TryOp, operand, whole ast.Node, src ir.Temp, k kind, pure bool) (ir.Temp, kind, bool, bool) {
	return bl.errEdgeTry(t, operand, whole, src, k, pure, func(e ir.Temp, ek, fk kind) (ir.Temp, bool) {
		v, _, ok := bl.coerceEmpty(t, e, ek, fk)
		if !ok {
			bl.g.dc.note("a `?` whose error does not widen to the boundary's: " + ek.nomi() + " as " + fk.nomi())
			return ir.NoTemp, false
		}
		return v, true
	})
}

// errEdgeTry lowers a `?` on src, a Result of kind k, whose Err edge maps
// the error to the boundary's error type with edge before it propagates.
// edge answers the mapped error, or false, having noted why it does not
// lower.
func (bl *irScalarBuilder) errEdgeTry(t *ast.TryOp, operand, whole ast.Node, src ir.Temp, k kind, pure bool, edge func(e ir.Temp, ek, fk kind) (ir.Temp, bool)) (ir.Temp, kind, bool, bool) {
	no := func(why string) (ir.Temp, kind, bool, bool) {
		bl.g.dc.note(why)
		return ir.NoTemp, kindInvalid, false, false
	}
	d := k.def
	if !irTryBoundaryKind(bl.returnKind) {
		return no("a `?` rebuilding its error whose boundary is not a Result")
	}
	bd := bl.returnKind.def
	if bd.preludeOf.spec != d.preludeOf.spec || len(bd.variants) != 2 || len(d.variants) != 2 {
		return no("a `?` rebuilding its error whose operand and boundary are not both Results")
	}
	okV, errV, bErr := &d.variants[0], &d.variants[1], &bd.variants[1]
	if len(okV.payloads) != 1 || len(errV.payloads) != 1 || len(bErr.payloads) != 1 {
		return no("a `?` rebuilding its error over a Result of another shape")
	}
	if !pure {
		cp := ir.NewCopy(bl.g.irNodePos(operand), bl.f.NewTemp(), src)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		src = cp.Dst()
	}
	pos := bl.g.irNodePos(t)
	test := bl.variantTest(t, src, d, errV)
	bl.b.Append(test)
	bl.side(test.Dst(), irScalarSide{k: kindBool})
	failed := bl.f.NewBlock(pos, "try convert")
	next := bl.f.NewBlock(pos, "try unwrap")
	bl.b.SetTerm(ir.NewBranch(pos, test.Dst(), failed.ID(), next.ID()))
	bl.b = failed
	e := bl.variantPayload(t, src, d, errV)
	f, ok := edge(e, errV.payloads[0].k, bErr.payloads[0].k)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	made, _, _, _ := bl.variantValue(t, bd, bErr, []ir.Temp{f}, false)
	bl.b.Append(ir.NewTry(pos, made, renderNode(whole)))
	bl.b.SetTerm(ir.NewJump(pos, next.ID()))
	bl.b = next
	v := bl.variantPayload(whole, src, d, okV)
	return v, okV.payloads[0].k, true, true
}

// fromCall calls on e, of kind ek, the `from` the checker chose for the
// `?` (conv's DispatchImpl), which makes a value of kind fk. The impl is
// this file's block that writes it, at fk; another file's is reached
// through its owner's sites, which take the same function: the call is
// written at the `?`, whose reference carries it (dispatchedImpl).
func (bl *irScalarBuilder) fromCall(t *ast.TryOp, conv *analysis.Symbol, e ir.Temp, ek, fk kind) (ir.Temp, bool) {
	fd, isFn := conv.DispatchImpl.(*ast.FuncDef)
	owner := kindOwnerName(fk)
	if !isFn || owner == "" {
		return ir.NoTemp, false
	}
	call := &ast.Call{
		Func: &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: owner, Line: t.Line, Col: t.Col},
			Field:  &ast.Ident{Name: fd.Name, Line: t.Line, Col: t.Col},
			Line:   t.Line, Col: t.Col,
		},
		Args: []ast.Node{t},
		Line: t.Line,
		Col:  t.Col,
	}
	args := irQualArgs{temps: []ir.Temp{e}, kinds: []kind{ek}, mobile: []bool{true}, ok: true}
	var plan *irQualPlan
	if d := bl.g.implWriting(fk, fd); d != nil {
		item := d.items[fd.Name]
		if item == nil {
			item = bl.genericMethodItem(call, d, fd.Name)
		}
		if item != nil {
			plan = bl.implItemPlan(call, args, item, owner+"."+fd.Name)
		}
	} else if fk.def != nil {
		plan = bl.qualSiblingIfaceImplPlan(call, args, fk.def, fd.Name, "From")
	}
	if plan == nil || plan.result != fk {
		return ir.NoTemp, false
	}
	v, rk, _, ok := bl.qualEmit(call, args, plan)
	return v, ok && rk == fk
}

// implWriting is this file's lowerable impl of receiver kind k whose block
// writes fd, or nil.
func (g *gen) implWriting(k kind, fd *ast.FuncDef) *implDef {
	for _, d := range g.implsOf(k) {
		if d.recv == k && d.lowerable && blockWrites(d.decl, fd) {
			return d
		}
	}
	return nil
}

// kindOwnerName is the name a type-qualified call on a value of kind k
// writes as its qualifier: a declared type's own name, or a scalar's.
func kindOwnerName(k kind) string {
	switch k {
	case kindInt:
		return "Int"
	case kindFloat:
		return "Float"
	case kindString:
		return "String"
	case kindBool:
		return "Bool"
	}
	if k.tag == tagNamed && k.def != nil {
		return k.def.nomi
	}
	return ""
}
