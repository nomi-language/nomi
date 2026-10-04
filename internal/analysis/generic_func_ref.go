package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A generic function NAMED as a value: `Iter.each(io.print)`,
// `Iter.map(xs, ident)`, `f: (Int) -> Int = ident`.
//
// A call instantiates a generic function from its arguments. A reference has
// no arguments, so it is instantiated from the function type the position
// expects instead: the function's own type parameters are solved by unifying
// its signature against that type, its interface bounds are checked at the
// solved types, and the instantiated signature is recorded on the reference
// (attachCallType) as a call's is, which is what the IR builder reads to pick
// the instance.
//
// When the expected type leaves a type parameter unsolved, or there is no
// expected type at all (`f = ident`), the reference is rejected and asks for
// an annotation. Nothing else could pick the instance.
//
// An interface function named as a value (`Iter.map(xs, Display.to_string)`)
// is instantiated the same way: its `self` is a type parameter of its own, so
// the expected type solves it, and the instantiated signature names the
// receiver type whose impl the value calls. With nothing to solve `self`
// from (`f = Display.to_string`), it is rejected as a generic function is.

// genericFuncRef reports whether node, in value position, names a function
// with type parameters of its own, and returns its signature and those
// parameters. A type parameter of the enclosing declaration is an answer, not
// a hole, so a generic body naming itself is not generic here. An interface
// function (`Display.to_string`) has its `self` as such a type parameter.
func (c *checker) genericFuncRef(node ast.Node, ty Type) (*FuncType, []*TypeParam_, string, bool) {
	ft, isFunc := ty.(*FuncType)
	if !isFunc || node == c.calleeNode || c.fa == nil {
		return nil, nil, "", false
	}
	var pos Pos
	name, qualified := "", ""
	switch n := node.(type) {
	case *ast.Ident:
		pos, name = Pos{Line: n.Line, Col: n.Col}, n.Name
	case *ast.FieldAccess:
		if n.Field == nil {
			return nil, nil, "", false
		}
		pos, name = Pos{Line: n.Field.Line, Col: n.Field.Col}, n.Field.Name
		if owner, isType := n.Object.(*ast.TypeIdent); isType {
			qualified = owner.Name + "." + n.Field.Name
		}
	default:
		return nil, nil, "", false
	}
	sym := c.fa.References[pos]
	if sym == nil {
		return nil, nil, "", false
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	iface := qualified != "" && sym.Kind == SymbolInterfaceMethod
	if sym.Kind != SymbolFunction && !iface {
		return nil, nil, "", false
	}
	if iface {
		// The message names it as written: `to_string` alone names nothing.
		name = qualified
	}
	var own []*TypeParam_
	for _, tp := range collectOrderedTypeParams(ft) {
		if c.fnTypeParams[tp.Name_] != tp {
			own = append(own, tp)
		}
	}
	for _, wb := range ft.WhereBounds {
		if wb.Param != nil && c.fnTypeParams[wb.Param.Name_] != wb.Param && !containsParam(own, wb.Param) {
			own = append(own, wb.Param)
		}
	}
	if len(own) == 0 {
		return nil, nil, "", false
	}
	return ft, own, name, true
}

func containsParam(ps []*TypeParam_, p *TypeParam_) bool {
	for _, q := range ps {
		if q == p {
			return true
		}
	}
	return false
}

// rejectUninstantiatedFuncRef reports a generic function named in a position
// with no expected function type to instantiate it against.
func (c *checker) rejectUninstantiatedFuncRef(node ast.Node, ty Type) Type {
	_, own, name, ok := c.genericFuncRef(node, ty)
	if !ok {
		return ty
	}
	line, col := nodeLineCol(node)
	c.addError(line, col, uninstantiatedRefMessage(name, own))
	return nil
}

func uninstantiatedRefMessage(name string, own []*TypeParam_) string {
	names := make([]string, len(own))
	for i, tp := range own {
		names[i] = tp.Name_
	}
	return fmt.Sprintf(
		"cannot infer type parameter %s of generic function '%s' used as a value: "+
			"nothing here gives it a function type; annotate one (`f: (Int) -> Int = %s`) or pass it where a function type is expected",
		strings.Join(names, ", "), name, name)
}

// instantiateFuncRef instantiates a generic function reference against the
// function type the position expects. Anything else is returned unchanged.
func (c *checker) instantiateFuncRef(node ast.Node, ty, expected Type) Type {
	ft, own, name, ok := c.genericFuncRef(node, ty)
	if !ok {
		return ty
	}
	line, col := nodeLineCol(node)
	want, isFunc := resolveTypeVar(expected).(*FuncType)
	if !isFunc {
		c.addError(line, col, uninstantiatedRefMessage(name, own))
		return nil
	}
	// The expected type may still hold the enclosing call's own unsolved
	// parameters (`Iter.map`'s result `B`). They are holes here, not types, so
	// they become fresh inference variables before the unify; the enclosing
	// call solves them from the instance this returns.
	want = instantiateUnboundCalleeParams(want, c.fnTypeParams, c).(*FuncType)
	subs := map[*TypeParam_]Type{}
	unifyErr := c.unify(ft, want, subs)
	var unsolved []*TypeParam_
	for _, tp := range own {
		got, bound := subs[tp]
		if !bound || containsTypeVar(got) || c.containsForeignTypeParam(got) {
			unsolved = append(unsolved, tp)
		}
	}
	if len(unsolved) > 0 {
		if unifyErr == nil {
			c.addError(line, col, uninstantiatedRefMessage(name, unsolved))
			return nil
		}
		// A shape the expected type cannot take: the enclosing check reports
		// the mismatch against the signature as written.
		return ty
	}
	for _, tp := range own {
		concrete := resolveTypeVar(subs[tp])
		subs[tp] = concrete
		for _, bound := range tp.Bounds {
			if !typeImplementsInterface(c, concrete, bound, c.recPos(line, col), RecordingKindGenericBoundCheck) {
				c.boundError(line, col, concrete, bound.Name, tp.Name_)
				continue
			}
			c.recordBoundConformance(concrete, bound.Name, c.recPos(line, col), RecordingKindGenericBoundCheck)
		}
	}
	c.recordWhereBoundDemands(ft, subs, line, col, col)
	params := make([]Type, len(ft.Params))
	for i, p := range ft.Params {
		params[i] = Substitute(p, subs)
	}
	inst := &FuncType{Params: params, Return: Substitute(ft.Return, subs), DefaultCount: ft.DefaultCount}
	c.attachCallType(node, inst)
	return inst
}
