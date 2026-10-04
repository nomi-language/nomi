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
	sym := bl.g.fa.References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym == nil {
		return no()
	}
	fd, isFunc := sym.Node.(*ast.FuncDef)
	if !isFunc {
		return no()
	}
	names := make([]string, len(fd.Params))
	for i, p := range fd.Params {
		if p.Destructure != nil {
			return no()
		}
		names[i] = p.Name
	}
	plan, planned := argSlotPlan(t.Args, make([]kind, len(names)), names)
	if !planned {
		return no()
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
	if gap {
		// `Date.new(2026, day: 5)`: a slot omitted before a supplied one
		// takes its default here, evaluated in the callee's scope after the
		// written operands, as a direct call's is (callDefaults). The
		// planners then see every slot up to the last supplied one.
		for i, p := range fd.Params {
			if p.TypeAnnotation == nil {
				return no()
			}
			if temps[i] == ir.NoTemp {
				kinds[i] = bl.g.typeOf(p.TypeAnnotation)
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
