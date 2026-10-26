package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A named argument to a concurrency intrinsic: `Task.await(task: t)`,
// `Sender.send(sender: s, value: 1)`, `Supervisor.spawn(sup, body: f)`,
// `Context.value(c, value_type: TraceId)`.
//
// Each intrinsic reads its operands positionally, so the call is planned as
// the same call written positionally in the declaration's parameter order,
// as namedQualCall plans an ordinary qualified call: every operand runs in
// argSlotPlan's order (positionals, then named arguments as written) into a
// temporary that a stand-in node answers (irScalarBuilder.placed). A type
// name runs nothing, so it stays in place, where Context.value reads its
// witness from the node. The intrinsics have no defaults, so the arguments
// must fill every parameter.

// namedIntrinsic reports whether t is a call with a named argument to one of
// the intrinsics this file places for.
func (bl *irScalarBuilder) namedIntrinsic(t *ast.Call, fa *ast.FieldAccess) bool {
	if fa.Field == nil || namedArgNode(t.Args) == nil {
		return false
	}
	switch bl.stdIntrinsicOwner(fa) {
	case "Task":
		// Task.spawn_all places its own arguments (taskSpawnAll).
		return fa.Field.Name != "spawn_all"
	case "Channel", "Sender", "Receiver":
		return true
	case "Supervisor":
		// `Supervisor.new` and `flush` are Nomi-bodied with defaults, and
		// take the ordinary qualified-call route.
		return fa.Field.Name == "spawn" || fa.Field.Name == "spawn_all"
	case "Reader":
		return fa.Field.Name == "from_chunks" || fa.Field.Name == "from_text_chunks"
	}
	ti, isType := fa.Object.(*ast.TypeIdent)
	if !isType || ti.Name != "Context" || (fa.Field.Name != "value" && fa.Field.Name != "with_value") {
		return false
	}
	d, found := bl.g.namedType(ti.Name)
	return found && irContextKind(named(d))
}

// namedIntrinsicCall lowers t, a namedIntrinsic call, as its positional form.
func (bl *irScalarBuilder) namedIntrinsicCall(t *ast.Call, fa *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.g.fa == nil {
		return no()
	}
	sym := bl.g.srcFA().References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym == nil {
		return no()
	}
	var params []ast.Param
	switch def := sym.Node.(type) {
	case *ast.ExternFunc:
		params = def.Params
	case *ast.FuncDef:
		params = def.Params
	default:
		return no()
	}
	names := make([]string, len(params))
	for i, p := range params {
		if p.Destructure != nil {
			return no()
		}
		names[i] = p.Name
	}
	plan, planned := argSlotPlan(t.Args, make([]kind, len(names)), names)
	if !planned || len(t.Args) != len(names) {
		return no()
	}
	if _, contiguous := contiguousSlots(plan); !contiguous {
		return no()
	}
	recording := bl.recording
	bl.recording = 0
	args := make([]ast.Node, len(names))
	temps := make([]ir.Temp, len(names))
	kinds := make([]kind, len(names))
	recorded := argPlan{slots: append([]int(nil), plan.slots...), order: plan.order}
	placed := map[ast.Node]irPlacedArg{}
	if bl.placed == nil {
		bl.placed = map[ast.Node]irPlacedArg{}
	}
	defer func() {
		for n := range placed {
			delete(bl.placed, n)
		}
		bl.recording = recording
	}()
	for _, i := range plan.order {
		a, slot := argExpr(t.Args[i]), plan.slots[i]
		if _, isType := a.(*ast.TypeIdent); isType {
			args[slot], temps[slot] = a, ir.NoTemp
			recorded.slots[i] = -1
			continue
		}
		v, k, _, ok := bl.lower(a)
		if !ok {
			return no()
		}
		line, col := nodePos(a)
		stand := &ast.Ident{Name: "\x00named", Line: line, Col: col}
		args[slot], temps[slot], kinds[slot] = stand, v, k
		placed[stand] = irPlacedArg{temp: v, k: k}
		bl.placed[stand] = placed[stand]
	}
	positional := &ast.Call{Func: t.Func, Args: args, IsTailCall: t.IsTailCall, Line: t.Line, Col: t.Col}
	v, k, mobile, ok := bl.call(positional)
	if ok && recording > 0 {
		// Inside an assertion subject the rows name the written arguments
		// by the slots they filled, as namedQualCall's do.
		bl.recording = recording
		bl.recordCallSlots(t.Args, recorded, names, temps, kinds, k)
	}
	return v, k, mobile, ok
}
