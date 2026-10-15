package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A qualified call with named arguments, `Time.new(minute: m, hour: h)`.
//
// The operands are evaluated in Nomi's order — positionals first,
// then named arguments, each partition in written order (argSlotPlan) — and
// then the call is planned as the same call
// written positionally in parameter order, each operand a synthetic node
// that answers the temporary it was already evaluated into (irScalarBuilder.
// placed). So every qualified-call planner serves the named form unchanged.
//
// The parameter names come from the callee's declaration, which the checker
// resolved the field reference to. A slot the named arguments leave out
// before a supplied one takes its default before the call is planned.

// irPlacedArg is one operand a named-argument call already evaluated.
type irPlacedArg struct {
	temp ir.Temp
	k    kind
}

func (bl *irScalarBuilder) namedQualCall(t *ast.Call, fa *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if fa.Field == nil || bl.g.fa == nil {
		return no()
	}
	sym := bl.g.srcFA().References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym == nil {
		return no()
	}
	if ti, isType := fa.Object.(*ast.TypeIdent); isType && ti.Name == "Struct" && fa.Field.Name == "update" && !bl.g.isBoundTypeParam("Struct") {
		// `Struct.update(updates: {x: 1}, original: p)`: the patch must stay
		// a literal the merge reads, which a stand-in for an evaluated
		// temporary is not, so the update places its own arguments.
		if bl.recording > 0 {
			return bl.recordedQualCall(t, func() (ir.Temp, kind, bool, bool) { return bl.structUpdateCall(t) })
		}
		return bl.structUpdateCall(t)
	}
	// An interface-qualified call (`Display.to_string(value: x)`) names the
	// interface method's parameters; the planned positional call dispatches
	// as any other.
	// A host function (`String.length(s: x)`, `Map.get(m: c, key: k)`)
	// names its parameters as a Nomi-bodied one does, and its defaults are
	// written the same way, so it takes the same path.
	var params []ast.Param
	var fd *ast.FuncDef
	switch def := sym.Node.(type) {
	case *ast.FuncDef:
		params, fd = def.Params, def
	case *ast.ExternFunc:
		params, fd = def.Params, &ast.FuncDef{Name: def.Name, Params: def.Params, ReturnTypeExpr: def.ReturnTypeExpr}
	case *ast.InterfaceMethod:
		params = def.Params
	default:
		return no()
	}
	isFunc := fd != nil
	// A destructuring parameter's name is the parser's synthesized one,
	// which no named argument can spell, so only a positional fills it.
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	plan, planned := argSlotPlan(t.Args, make([]kind, len(names)), names)
	if !planned {
		return no()
	}
	if len(t.Args) == 1 && len(names) == 1 && bl.recording == 0 {
		// `io.print(value: x)` is `io.print(x)`: one operand has one order.
		// The output primitives render their operand from its own node, so
		// the call keeps the written expression rather than a stand-in.
		positional := &ast.Call{Func: t.Func, Args: []ast.Node{argExpr(t.Args[0])}, IsTailCall: t.IsTailCall, Line: t.Line, Col: t.Col}
		if dst, k, mobile, ok := bl.hostOutput(positional, fa); ok {
			return dst, k, mobile, true
		}
	}
	filled := make([]bool, len(names))
	last := -1
	for _, s := range plan.slots {
		if s < 0 || s >= len(names) {
			return no()
		}
		filled[s] = true
		last = max(last, s)
	}
	temps := make([]ir.Temp, len(names))
	kinds := make([]kind, len(names))
	for i := range temps {
		temps[i] = ir.NoTemp
	}
	for _, i := range plan.order {
		a := argExpr(t.Args[i])
		src, k, _, ok := bl.lower(a)
		if !ok || !irCallOperandKind(k) {
			return no()
		}
		temps[plan.slots[i]], kinds[plan.slots[i]] = src, k
	}
	gap := false
	for i := 0; i < last; i++ {
		gap = gap || !filled[i]
	}
	if gap && !isFunc {
		// `Shape.scale(c, by: 2)` leaving a defaulted slot of an interface
		// function empty: the declaration the call reaches gives the
		// default, as for a call omitting trailing slots.
		sig, found := bl.ifaceGapDefaults(t, sym.Node.(*ast.InterfaceMethod), kinds, temps)
		if !found {
			return no()
		}
		for i, k := range sig.params {
			if temps[i] == ir.NoTemp {
				kinds[i] = k
			}
		}
		if !bl.callDefaults(sig, temps) {
			return no()
		}
		last = len(temps) - 1
	}
	if gap && isFunc {
		// `Date.new(2026, day: 5)`: a slot omitted before a supplied one
		// takes its default here, evaluated in the callee's scope after the
		// written operands, as a direct call's is (callDefaults). The
		// planners then see every slot up to the last supplied one. A
		// generic callee's slot (`other: Maybe<T> = None`) is read at the
		// call's instantiation, which the checker recorded.
		checked := bl.g.checkedCallSignature(t)
		if checked != nil && len(checked.Params) != len(fd.Params) {
			checked = nil
		}
		for i, p := range fd.Params {
			if temps[i] == ir.NoTemp {
				if p.TypeAnnotation == nil {
					return no()
				}
				if checked != nil {
					kinds[i] = bl.g.projectFilledHoles(checked.Params[i])
				} else {
					kinds[i] = bl.g.typeOf(p.TypeAnnotation)
				}
				if !irCallOperandKind(kinds[i]) {
					return no()
				}
			}
		}
		if !bl.callDefaults(&fnSig{decl: fd, params: kinds}, temps) {
			return no()
		}
		last = len(temps) - 1
	}
	args := make([]ast.Node, last+1)
	placed := make(map[ast.Node]irPlacedArg, len(args))
	for slot := range args {
		line, col := t.Line, t.Col
		for i, s := range plan.slots {
			if s == slot {
				line, col = nodePos(argExpr(t.Args[i]))
			}
		}
		stand := &ast.Ident{Name: "\x00named", Line: line, Col: col}
		args[slot] = stand
		placed[stand] = irPlacedArg{temp: temps[slot], k: kinds[slot]}
	}
	if bl.placed == nil {
		bl.placed = map[ast.Node]irPlacedArg{}
	}
	for n, p := range placed {
		bl.placed[n] = p
	}
	defer func() {
		for n := range placed {
			delete(bl.placed, n)
		}
	}()
	positional := &ast.Call{Func: t.Func, Args: args, IsTailCall: t.IsTailCall, Line: t.Line, Col: t.Col}
	if bl.recording == 0 {
		return bl.qualCall(positional, fa)
	}
	// Inside an assertion subject the rows name the written arguments, by
	// the slots they filled, as a direct call's do (recordCallSlots); the
	// stand-ins record nothing.
	recording := bl.recording
	bl.recording = 0
	v, k, mobile, ok := bl.qualCall(positional, fa)
	bl.recording = recording
	if ok {
		bl.recordCallSlots(t.Args, plan, names, temps, kinds, k)
	}
	return v, k, mobile, ok
}

// ifaceGapDefaults is the declaration whose defaults fill the slots an
// interface function's named call leaves empty before a supplied one, with
// its parameter kinds. A concrete receiver's is the implementation the call
// reaches, whose defaults a call omitting trailing slots takes
// (implDefaultArgs): an override's own, or the interface's default copied
// into the impl. An erased receiver's is the interface's declaration, as in
// erasedInterfacePlan.
func (bl *irScalarBuilder) ifaceGapDefaults(t *ast.Call, im *ast.InterfaceMethod, kinds []kind, temps []ir.Temp) (*fnSig, bool) {
	recvAt := selfShapeOf(im.Params, im.ReturnTypeExpr).recvAt()
	if recvAt < 0 || recvAt >= len(temps) || temps[recvAt] == ir.NoTemp {
		return nil, false
	}
	recv := kinds[recvAt]
	if recv.tag == tagIface {
		m := recv.iface.methods[im.Name]
		if m == nil || m.decl != im || len(m.params) != len(temps) {
			return nil, false
		}
		params := make([]kind, len(m.params))
		for i := range params {
			if m.shape.selfTyped(i) {
				params[i] = recv
				continue
			}
			params[i] = m.params[i]
		}
		return &fnSig{decl: &ast.FuncDef{Name: m.name, Params: im.Params}, params: params}, true
	}
	for _, d := range bl.g.ifaces {
		if m := d.methods[im.Name]; m == nil || m.decl != im {
			continue
		}
		impl := bl.g.implsByIface[d.nomi][recv]
		if impl == nil || !impl.lowerable {
			return nil, false
		}
		it := impl.items[im.Name]
		if it == nil {
			it = bl.genericMethodItem(t, impl, im.Name)
		}
		if it == nil || !it.lowerable {
			return nil, false
		}
		fd := irImplSource(it)
		if fd == nil || len(fd.Params) != len(temps) || len(it.params) != len(temps) {
			return nil, false
		}
		return &fnSig{decl: fd, params: append([]kind(nil), it.params...)}, true
	}
	return nil, false
}
