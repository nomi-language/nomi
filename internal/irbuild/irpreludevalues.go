package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func preludeValueName(at ast.Node) (string, int, int, bool) {
	switch n := at.(type) {
	case *ast.TypeIdent:
		return n.Name, n.Line, n.Col, true
	case *ast.FieldAccess:
		if _, ok := n.Object.(*ast.TypeIdent); ok && n.Field != nil {
			return n.Field.Name, n.Field.Line, n.Field.Col, true
		}
	}
	return "", 0, 0, false
}

func (bl *irScalarBuilder) preludeCall(t *ast.Call) (ir.Temp, kind, bool, bool) {
	// kindInvalid: sentinel — no expected type; the checker's solution decides.
	return bl.preludeCallWant(t, kindInvalid)
}

// preludeCallWant is preludeCall with the position's expected type. An
// expected instance of the same prelude enum is the constructor's type:
// the checker solved the constructor against it, and a type argument the
// constructor's own reference leaves open (`Ok(x)` beside a `break
// Err(e)` arm) is the expected one rather than Unit.
func (bl *irScalarBuilder) preludeCallWant(t *ast.Call, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Args) != 1 || hasNamedArg(t.Args) {
		return no()
	}
	if dot, isDot := t.Func.(*ast.DotVariant); isDot {
		// `.Completed(42)` where an Outcome<Int> is expected: the analyzer
		// records no reference at a dot, so the variant resolves from the
		// checked enum's name, and the expected type is its instance.
		a, anchored := bl.g.preludeNamed(dot.ResolvedEnum)
		// A position that hands down no expected type (a nested fn's or a
		// lambda's tail, an arm of either): the instance is the type the
		// checker gave the call.
		// kindInvalid: sentinel — the caller passed no expected type, not an operand's kind.
		if want == kindInvalid {
			want = bl.g.project(bl.g.checkedExprType(t))
		}
		if !anchored || want.tag != tagNamed || want.def == nil || want.def.preludeOf == nil ||
			a.spec != want.def.preludeOf.spec || !irRetainedEnumKind(want.def) {
			return no()
		}
		vs, found := a.spec.variant(dot.Name)
		if !found || !vs.carries() {
			return no()
		}
		return bl.preludePayloadValue(t, want, vs)
	}
	name, line, col, ok := preludeValueName(t.Func)
	if !ok {
		return no()
	}
	a, sym, vs, ok := bl.g.resolvedPreludeVariant(name, line, col)
	if !ok || !vs.carries() {
		return no()
	}
	var k kind
	if want.tag == tagNamed && want.def != nil && want.def.preludeOf != nil && want.def.preludeOf.spec == a.spec {
		k = want
	} else {
		args, ok := bl.g.checkedPreludeArgs(a, sym)
		if !ok {
			args, ok = bl.g.checkedPreludeArgsUnitHoles(a, sym)
		}
		if !ok {
			return no()
		}
		k = bl.g.preludeInstance(a, args)
	}
	if k.tag != tagNamed || !irRetainedEnumKind(k.def) {
		return no()
	}
	return bl.preludePayloadValue(t, k, vs)
}

// preludePayloadValue builds the payload-carrying prelude variant vs of k from
// the call's one argument.
func (bl *irScalarBuilder) preludePayloadValue(t *ast.Call, k kind, vs preludeVariantSpec) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	v := k.def.variant(vs.nomi)
	if v == nil || len(v.payloads) != 1 {
		return no()
	}
	src, actual, pure, ok := bl.lowerTypedOperand(t.Args[0], v.payloads[0].k)
	if ok && actual != v.payloads[0].k && pure {
		// An empty literal (`Ok([])`) takes the payload's type.
		src, actual, ok = bl.coerceEmpty(t.Args[0], src, actual, v.payloads[0].k)
	}
	if !ok || actual != v.payloads[0].k {
		return no()
	}
	return bl.variantValue(t, k.def, v, []ir.Temp{src}, pure)
}

func (bl *irScalarBuilder) preludeBareValue(at ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	// As in preludeCallWant: with no expected type, the checker's type of
	// the shorthand is its instance.
	// kindInvalid: sentinel — the caller passed no expected type, not an operand's kind.
	if _, isDot := at.(*ast.DotVariant); isDot && want == kindInvalid {
		want = bl.g.project(bl.g.checkedExprType(at))
	}
	if want.tag != tagNamed || !irRetainedEnumKind(want.def) || want.def.preludeOf == nil {
		return no()
	}
	var vs preludeVariantSpec
	if dot, isDot := at.(*ast.DotVariant); isDot {
		// `.Cancelled`: the analyzer records no reference at a dot, so this
		// resolves it from the checked enum's name.
		a, anchored := bl.g.preludeNamed(dot.ResolvedEnum)
		if !anchored || a.spec != want.def.preludeOf.spec {
			return no()
		}
		found := false
		if vs, found = a.spec.variant(dot.Name); !found || vs.carries() {
			return no()
		}
	} else {
		name, line, col, ok := preludeValueName(at)
		if !ok {
			return no()
		}
		a, _, resolved, ok := bl.g.resolvedPreludeVariant(name, line, col)
		if !ok || resolved.carries() || a.spec != want.def.preludeOf.spec {
			return no()
		}
		vs = resolved
	}
	v := want.def.variant(vs.nomi)
	if v == nil {
		return no()
	}
	return bl.variantValue(at, want.def, v, nil, true)
}
