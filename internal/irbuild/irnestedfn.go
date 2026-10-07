package irbuild

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// nestedFunc lowers a `fn` declared inside a body as the closure it is: a
// lambda over the same parameters and body, bound to the function's name for
// the statements after it. A nested FuncDef is a function value over the
// enclosing scope, so its free names are captures and `return`
// leaves the nested function, which is a lambda's rule too.
//
// A body that names the function itself is recursiveNestedFunc's.
//
// A generic one is lowered once per type-argument tuple its calls reach
// (genericNestedFunc).
//
// DECLINED: a parameter without an annotation, a name that is already bound
// here, and a lambda whose result is not the declared return type.
func (bl *irScalarBuilder) nestedFunc(fd *ast.FuncDef) bool {
	no := func(why string) bool {
		irDeclineNote("a nested fn: " + why)
		return false
	}
	switch {
	case fd.Body == nil || ast.IsDiscardName(fd.Name):
		return no("without a body or a name")
	case len(fd.Decorators) != 0 || len(fd.AttachedTests) != 0:
		return no("with decorators or attached tests")
	}
	for _, p := range fd.Params {
		if p.TypeAnnotation == nil || p.Destructure != nil {
			return no("a parameter without an annotation, or a destructuring one")
		}
	}
	if _, shadows := bl.g.lookup(fd.Name); shadows || bl.bound[fd.Name] != ir.NoTemp {
		return no("a name already bound: " + fd.Name)
	}
	if len(fd.TypeParams) != 0 {
		return bl.genericNestedFunc(fd)
	}
	return bl.nestedInstance(fd, fd.Name)
}

// nestedInstance lowers fd's body as a closure bound to name: fd's own name,
// or an instance's name under its substitution frame.
func (bl *irScalarBuilder) nestedInstance(fd *ast.FuncDef, name string) bool {
	no := func(why string) bool {
		irDeclineNote("a nested fn: " + why)
		return false
	}
	lam := &ast.Lambda{Params: fd.Params, Body: fd.Body, Line: fd.Line, Col: fd.Col}
	// A try in the body leaves the nested fn, whose boundary the checker
	// names "fn <name>". Only this builder's own lambda build reads it: the
	// lambdas inside the body are built by the nested fn's builder.
	saved := bl.nestedFn
	bl.nestedFn = fd.Name
	defer func() { bl.nestedFn = saved }()
	if fd.ReturnTypeExpr == nil || bl.g.typeOf(fd.ReturnTypeExpr) == kindUnit {
		savedUnit := bl.g.unitFnLambda
		bl.g.unitFnLambda = lam
		defer func() { bl.g.unitFnLambda = savedUnit }()
	}
	if astNamesIdent(fd.Body, fd.Name) {
		return bl.recursiveNestedFunc(fd, lam, name)
	}
	val, fk, _, ok := bl.lambda(lam)
	if !ok {
		return no("its body is outside the lambda shape")
	}
	if fd.ReturnTypeExpr != nil {
		if want := bl.g.typeOf(fd.ReturnTypeExpr); funcResult(fk) != want {
			return no("a result that is not the declared return type: " + funcResult(fk).nomi() + " vs " + want.nomi())
		}
	} else if funcResult(fk) != kindUnit {
		return no("an unannotated result that is not Unit")
	}
	bind := ir.NewBind(bl.g.irPos(fd.Line, fd.Col), bl.f.NewTemp(), val, bl.sh.localSym(name))
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: fk})
	bl.bound[name], bl.boundK[name] = bind.Dst(), fk
	return true
}

// genericNestedFunc lowers a generic nested fn as one closure per type
// argument tuple the program calls it at, each built where the fn is
// declared, so each captures the names the fn sees there. The tuples are
// read from the checker's instantiated signature at every call that
// resolves to fd (nestedGenericCalls), under the substitution frames active
// here, so a nested fn inside a generic function's instance is instantiated
// at that instance's arguments, and a call inside another generic nested
// fn's body at each of that fn's tuples (nestedInstanceSet). A call whose
// tuple is not among them (one two generic nested fns deep) declines at the
// call.
//
// A `where` bound needs nothing more: under the frame the bounded parameter
// is its concrete argument, and a call through the bound is that type's, as
// in a module-level generic's instance.
//
// A body that calls the fn itself at its own tuple calls its own closure
// (recursiveNestedFunc); a call at another tuple declines, as no other
// instance is in scope while one is built.
func (bl *irScalarBuilder) genericNestedFunc(fd *ast.FuncDef) bool {
	gn := &irGenericNested{fd: fd, names: map[string]string{}}
	insts := bl.g.nestedInstanceSet(fd, map[*ast.FuncDef]bool{})
	for i, args := range insts {
		// The instance's name, which no program can spell, so it shadows
		// nothing. Numbered, because two instances of a generic struct
		// render alike (`Box`).
		gn.names[kindsKey(args)] = fmt.Sprintf("%s<%s>#%d", fd.Name, kindsNomi(args), i)
	}
	// Registered before the instances are built, so a body's call to
	// itself resolves to the instance being built.
	if bl.genericNested == nil {
		bl.genericNested = map[string]*irGenericNested{}
	}
	bl.genericNested[fd.Name] = gn
	for _, args := range insts {
		bl.g.genericSubst = append(bl.g.genericSubst, nestedFrame(fd, args))
		ok := bl.nestedInstance(fd, gn.names[kindsKey(args)])
		bl.g.genericSubst = bl.g.genericSubst[:len(bl.g.genericSubst)-1]
		if !ok {
			return false
		}
	}
	return true
}

// irGenericNested is a generic nested fn and the local name each of its
// instances is bound under, by kindsKey of its type arguments.
type irGenericNested struct {
	fd    *ast.FuncDef
	names map[string]string
}

// kindsKey is a type argument tuple's identity: each kind's key.
func kindsKey(args []kind) string {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(a.key())
		b.WriteByte(0)
	}
	return b.String()
}

// nestedFrame is the substitution frame of fd's instance at args.
func nestedFrame(fd *ast.FuncDef, args []kind) map[string]kind {
	frame := make(map[string]kind, len(args))
	for i, tp := range fd.TypeParams {
		frame[tp.Name] = args[i]
	}
	return frame
}

// nestedInstanceSet is the type argument tuples the generic nested fn fd is
// called at, deduplicated in source order: each call's checked signature
// under the frames active here, and a call inside another generic nested
// fn's body once under each of that fn's own tuples. visiting guards the
// walk; a fn the front end lets call fd is declared after it, so the walk
// does not cycle, and a recursive fn is declined before it is asked.
func (g *gen) nestedInstanceSet(fd *ast.FuncDef, visiting map[*ast.FuncDef]bool) [][]kind {
	if visiting[fd] {
		return nil
	}
	visiting[fd] = true
	defer delete(visiting, fd)
	params := map[string]bool{}
	for _, tp := range fd.TypeParams {
		params[tp.Name] = true
	}
	var out [][]kind
	seen := map[string]bool{}
	add := func(ft *analysis.FuncType) {
		args, ok := g.nestedInstanceArgs(fd, params, ft)
		if key := kindsKey(args); ok && !seen[key] {
			seen[key] = true
			out = append(out, args)
		}
	}
	for _, ref := range g.nestedGenericCalls(fd) {
		if ref.within == nil || ref.within == fd {
			add(ref.ft)
			continue
		}
		for _, outer := range g.nestedInstanceSet(ref.within, visiting) {
			g.genericSubst = append(g.genericSubst, nestedFrame(ref.within, outer))
			add(ref.ft)
			g.genericSubst = g.genericSubst[:len(g.genericSubst)-1]
		}
	}
	return out
}

// nestedInstanceArgs solves fd's type parameters from one call's checked
// signature, or reports false when a parameter is unsolved or outside the
// domain.
func (g *gen) nestedInstanceArgs(fd *ast.FuncDef, params map[string]bool, ft *analysis.FuncType) ([]kind, bool) {
	if len(ft.Params) != len(fd.Params) {
		return nil, false
	}
	if irUnsolvedType(ft) {
		// `apply(1, |x| Ok(x + 1))` leaves the error type open and makes no
		// value of it (spec, "Determined type arguments"): the holes are
		// filled as a module-level generic call's are, so the instance the
		// declaration builds and the one this call names agree.
		ft = irFillHolesByPosition(ft, false).(*analysis.FuncType)
	}
	solved := map[string]kind{}
	for i, p := range fd.Params {
		g.unifyTypeParams(p.TypeAnnotation, g.project(ft.Params[i]), params, solved)
	}
	if fd.ReturnTypeExpr != nil {
		g.unifyTypeParams(fd.ReturnTypeExpr, g.project(ft.Return), params, solved)
	}
	args := make([]kind, len(fd.TypeParams))
	for i, tp := range fd.TypeParams {
		k, found := solved[tp.Name]
		if !found || !(irCallableValueKind(k) || k == kindUnit) {
			return nil, false
		}
		args[i] = k
	}
	return args, true
}

// nestedCallRef is one reference to a generic fn that the checker
// instantiated: its signature there, and the innermost generic nested fn
// whose body holds it, or nil.
type nestedCallRef struct {
	ft     *analysis.FuncType
	within *ast.FuncDef
}

// nestedGenericCalls is every reference that resolves to the generic fn fd
// with an instantiated signature, in source order. The module's references
// are indexed once.
func (g *gen) nestedGenericCalls(fd *ast.FuncDef) []nestedCallRef {
	if g.fa == nil {
		return nil
	}
	if g.nestedCalls == nil {
		g.nestedCalls = map[*ast.FuncDef][]nestedCallRef{}
		within := nestedGenericBodies(g.nodes)
		g.nestedWithin = within
		var positions []analysis.Pos
		for pos, sym := range g.fa.References {
			if def, isFn := sym.Node.(*ast.FuncDef); isFn && len(def.TypeParams) != 0 && sym.CallType != nil {
				positions = append(positions, pos)
			}
		}
		sort.Slice(positions, func(i, j int) bool {
			if positions[i].Line != positions[j].Line {
				return positions[i].Line < positions[j].Line
			}
			return positions[i].Col < positions[j].Col
		})
		for _, pos := range positions {
			sym := g.fa.References[pos]
			if ft, isFunc := sym.CallType.(*analysis.FuncType); isFunc {
				def := sym.Node.(*ast.FuncDef)
				g.nestedCalls[def] = append(g.nestedCalls[def], nestedCallRef{ft: ft, within: within[pos]})
			}
		}
	}
	return g.nestedCalls[fd]
}

// nestedGenericBodies maps the position of every identifier inside a generic
// fn declared in a body to the innermost such fn. A module-level fn and an
// impl block's items are not nested.
func nestedGenericBodies(nodes []ast.Node) map[analysis.Pos]*ast.FuncDef {
	out := map[analysis.Pos]*ast.FuncDef{}
	var walk func(n ast.Node, within *ast.FuncDef)
	walk = func(n ast.Node, within *ast.FuncDef) {
		switch t := n.(type) {
		case *ast.Ident:
			if within != nil {
				out[analysis.Pos{Line: t.Line, Col: t.Col}] = within
			}
			return
		case *ast.FuncDef:
			if len(t.TypeParams) != 0 {
				within = t
			}
		}
		ast.Children(n, func(c ast.Node) { walk(c, within) })
	}
	top := func(n ast.Node) {
		// A top-level declaration's body is walked as not yet nested.
		ast.Children(n, func(c ast.Node) { walk(c, nil) })
	}
	for _, n := range nodes {
		if ib, isImpl := n.(*ast.ImplBlock); isImpl {
			for _, item := range ib.Items {
				top(item)
			}
			continue
		}
		top(n)
	}
	return out
}

// genericNestedFn is the generic nested fn a bare callee names in this
// scope, or nil: the nearest enclosing builder that binds the name decides,
// so a binding of the same name nearer the call is what it calls.
func (bl *irScalarBuilder) genericNestedFn(id *ast.Ident) *irGenericNested {
	for scope := bl; scope != nil; scope = scope.parent {
		if gn := scope.genericNested[id.Name]; gn != nil {
			return gn
		}
		if _, bound := scope.bound[id.Name]; bound {
			return nil
		}
	}
	return nil
}

// genericNestedCall lowers a call to a generic nested fn as a call through
// the instance its checked signature selects, which genericNestedFunc bound
// where the fn was declared. handled is false when id names no generic
// nested fn.
func (bl *irScalarBuilder) genericNestedCall(t *ast.Call, id *ast.Ident) (ir.Temp, kind, bool, bool, bool) {
	inst, isNested := bl.genericNestedRef(id)
	if !isNested {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if inst == nil {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	call := *t
	call.Func = inst
	v, k, mobile, lowered := bl.indirectCall(&call)
	return v, k, mobile, lowered, true
}

// genericNestedRef is the instance a name that resolves to a generic nested
// fn stands for at this reference, read from the checker's instantiated
// signature there, as a name spelling the instance's binding. isNested is
// false when id names no generic nested fn; inst is nil, with the decline
// noted, when the reference's instance was not built.
func (bl *irScalarBuilder) genericNestedRef(id *ast.Ident) (inst *ast.Ident, isNested bool) {
	gn := bl.genericNestedFn(id)
	if gn == nil {
		return nil, false
	}
	fd := gn.fd
	params := map[string]bool{}
	for _, tp := range fd.TypeParams {
		params[tp.Name] = true
	}
	var args []kind
	ok := false
	if bl.g.fa != nil {
		pos := analysis.Pos{Line: id.Line, Col: id.Col}
		if sym := bl.g.fa.References[pos]; sym != nil {
			ft, isFunc := sym.CallType.(*analysis.FuncType)
			if !isFunc && bl.g.nestedGenericCalls(fd) != nil && bl.g.nestedWithin[pos] == fd {
				// A call in fd's own body, which the checker does not
				// instantiate: fd's own signature, at the instance being
				// built, whose frame is the innermost.
				ft, isFunc = sym.Type.(*analysis.FuncType)
			}
			if isFunc {
				args, ok = bl.g.nestedInstanceArgs(fd, params, ft)
			}
		}
	}
	if !ok {
		irDeclineNote("a generic nested fn whose type arguments are unsolved here: " + id.Name)
		return nil, true
	}
	name, built := gn.names[kindsKey(args)]
	if !built || !bl.reaches(name) {
		irDeclineNote("a generic nested fn at a type its declaration did not build: " + fd.Name + "<" + kindsNomi(args) + ">")
		return nil, true
	}
	return &ast.Ident{Name: name, Line: id.Line, Col: id.Col}, true
}

// reaches reports whether name is bound in this builder or one enclosing it.
func (bl *irScalarBuilder) reaches(name string) bool {
	for scope := bl; scope != nil; scope = scope.parent {
		if _, bound := scope.bound[name]; bound {
			return true
		}
	}
	return false
}

// irRecursiveFn is a nested fn whose body names itself, while its lambda is
// built: lambdaFunction binds name in the body to the closure's first capture
// parameter, which ir.NewRecursiveFuncValue fills with the closure itself.
type irRecursiveFn struct {
	lam  *ast.Lambda
	name string
	k    kind
}

// recursiveNestedFunc lowers a nested fn whose body names itself. The name is
// not captured from the enclosing scope, where it is bound only once the
// closure exists: the fn refers to itself through its own closure, and every
// other name it reads is captured by value as any closure's is. The front end
// rejects a nested fn calling one declared after it, so there are no mutually
// recursive nested fns to group.
func (bl *irScalarBuilder) recursiveNestedFunc(fd *ast.FuncDef, lam *ast.Lambda, name string) bool {
	params := make([]kind, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = bl.g.typeOf(p.TypeAnnotation)
	}
	result := kindUnit
	if fd.ReturnTypeExpr != nil {
		result = bl.g.typeOf(fd.ReturnTypeExpr)
	}
	fk := funcKindIn(bl.g, params, result)
	if !irCallableValueKind(fk) {
		irDeclineNote("a recursive nested fn whose signature is outside the domain: " + fd.Name)
		return false
	}
	saved := bl.recursive
	bl.recursive = &irRecursiveFn{lam: lam, name: name, k: fk}
	val, got, _, ok := bl.lambda(lam)
	bl.recursive = saved
	if !ok || got != fk {
		irDeclineNote("a recursive nested fn: its body is outside the lambda shape: " + fd.Name)
		return false
	}
	bind := ir.NewBind(bl.g.irPos(fd.Line, fd.Col), bl.f.NewTemp(), val, bl.sh.localSym(name))
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: fk})
	bl.bound[name], bl.boundK[name] = bind.Dst(), fk
	return true
}

// astNamesIdent reports whether any identifier under n is spelled name. It
// is conservative: a field or parameter of that name also counts, which only
// declines more.
func astNamesIdent(n ast.Node, name string) bool {
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok {
				if id.Name == name {
					found = true
				}
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.String:
			// A bare call's callee is an Ident, but a pipe stage or a named
			// argument may carry the name as a string; count it too.
			if v.String() == name {
				found = true
			}
		}
	}
	walk(reflect.ValueOf(n))
	return found
}
