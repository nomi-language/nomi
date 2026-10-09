package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A function an OWNER declares, named as a value: `Todo.render` for an
// inherent function of `impl Todo`, `Display.to_string` for an interface
// function, `shapes.Point.area` for another file's impl.
//
// The value is a function of the reference's parameters whose body is the
// qualified call `Owner.member(p0, …, pn)` over them, at the kinds the checker
// typed the reference at. The call resolves exactly as the written call would:
// a local or sibling impl, a generic instance, or for an interface function
// the impl of the operand's kind, so `Iter.map(xs, Display.to_string)` over a
// `List<Int>` renders each Int as `Display.to_string(x)` does. This is
// outputRef's construction, over the whole qualified-call path instead of
// the output call.

// ownerFuncRef lowers `Owner.member` as a function value, or declines.
// `Shape.Circle` and `Maybe.Some`, a positional variant named through its
// enum, take this path too: the body is the constructor call
// `Shape.Circle(p0)`. The owner may be named through a file's qualifier,
// `leaf.Box.twice` or `leaf.Shape.Line`; the body is then that qualified
// call, which resolves as the written call does.
func (bl *irScalarBuilder) ownerFuncRef(t *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	owner, ok := ownerSpelling(t.Object)
	if !ok || t.Field == nil {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.callFuncValue(t, owner+"."+t.Field.Name)
}

// ownerSpelling is the owner of a qualified reference as written: a type name
// (`Box`), or one under qualifiers (`leaf.Box`, `Probe.Reading`). It only
// names the function value's IR function; the call callFuncValue builds is
// resolved through the checker's references, as the written call is.
func ownerSpelling(n ast.Node) (string, bool) {
	switch o := n.(type) {
	case *ast.TypeIdent:
		return o.Name, true
	case *ast.FieldAccess:
		if o.Field == nil {
			return "", false
		}
		if root, isIdent := o.Object.(*ast.Ident); isIdent {
			return root.Name + "." + o.Field.Name, true
		}
		q, ok := ownerSpelling(o.Object)
		return q + "." + o.Field.Name, ok
	}
	return "", false
}

// ctorFuncRef lowers a bare type name the checker typed as a constructor
// function, `Some`, `Ok`, `Err`, or `Id` for `type Id Int`, as that function
// value: its body is the call `Some(p0)`, built exactly as the written call
// is. A name the checker did not type as a function declines.
func (bl *irScalarBuilder) ctorFuncRef(t *ast.TypeIdent) (ir.Temp, kind, bool, bool) {
	return bl.callFuncValue(t, t.Name)
}

// callFuncValue builds the function value of ref, a name the checker typed as
// a function: a function whose body calls ref with its parameters.
func (bl *irScalarBuilder) callFuncValue(ref ast.Node, name string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	g := bl.g
	params, result, ok := g.checkedValueKinds(ref)
	if !ok {
		return no()
	}
	line, col := calleeRefPos(ref)
	at := g.irNodePos(ref)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	child := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{},
		boundK: map[string]kind{}, placed: map[ast.Node]irPlacedArg{}}
	args := make([]ast.Node, len(params))
	for i, k := range params {
		// Each operand is a stand-in the call lowers to the parameter's
		// temporary (irScalarBuilder.placed), so no name is bound that the
		// call's own resolution could see.
		stand := &ast.Ident{Name: "\x00arg" + strconv.Itoa(i), Line: line, Col: col}
		child.placed[stand] = irPlacedArg{temp: g.irAddParam(f, ir.NewSymbol("arg"+strconv.Itoa(i)), k), k: k}
		args[i] = stand
	}
	call := &ast.Call{Func: ref, Args: args, Line: line, Col: col}
	dst, k, _, ok := child.lower(call)
	if !ok || k != result || len(child.captures) != 0 {
		return no()
	}
	if result == kindUnit && dst == ir.NoTemp {
		u := ir.NewUnit(at, f.NewTemp())
		child.b.Append(u)
		dst = u.Dst()
	}
	child.b.SetTerm(ir.NewReturn(at, dst))
	if err := ir.Lint(f); err != nil {
		irDeclineNote("an owner function value that does not lint: " + err.Error())
		return no()
	}
	n := ir.NewFuncValue(at, bl.f.NewTemp(), f)
	bl.b.Append(n)
	fk := funcKindIn(g, params, result)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), fk, true, true
}

// checkedValueKinds is the function type the checker gave a reference used as
// a value, in this gen's kinds: the instantiated signature when the reference
// is generic, its declared type otherwise. ok is false when the type is not a
// function type, leaves anything unsolved, or names a kind outside the value
// domain.
func (g *gen) checkedValueKinds(ref ast.Node) ([]kind, kind, bool) {
	if g.fa == nil {
		return nil, kindInvalid, false
	}
	line, col := calleeRefPos(ref)
	sym := g.fa.References[analysis.Pos{Line: line, Col: col}]
	if sym == nil {
		return nil, kindInvalid, false
	}
	ft, _ := sym.CallType.(*analysis.FuncType)
	if ft == nil {
		ft, _ = sym.Type.(*analysis.FuncType)
	}
	if ft == nil || irUnsolvedType(ft) {
		return nil, kindInvalid, false
	}
	params := make([]kind, len(ft.Params))
	for i, pt := range ft.Params {
		params[i] = g.project(pt)
		if !irCallableValueKind(params[i]) {
			return nil, kindInvalid, false
		}
	}
	result := g.project(ft.Return)
	if result != kindUnit && !irCallableValueKind(result) {
		return nil, kindInvalid, false
	}
	return params, result, true
}

// distinctCtorRef reports whether ref names a distinct type the checker typed
// as its constructor function, `ids.UserId` for another file's
// `pub type UserId Int`.
func (g *gen) distinctCtorRef(ref ast.Node) bool {
	if g.fa == nil {
		return false
	}
	line, col := calleeRefPos(ref)
	sym := g.fa.References[analysis.Pos{Line: line, Col: col}]
	if sym == nil {
		return false
	}
	if _, isFunc := sym.CallType.(*analysis.FuncType); !isFunc {
		return false
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	_, isDistinct := sym.Type.(*analysis.DistinctType)
	return sym.Kind == analysis.SymbolType && isDistinct
}
