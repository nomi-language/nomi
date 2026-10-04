package irbuild

import (
	"reflect"

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
// DECLINED: a generic or bounded function, a parameter without an
// annotation, a name that is already bound here, and a lambda whose result is
// not the declared return type.
func (bl *irScalarBuilder) nestedFunc(fd *ast.FuncDef) bool {
	no := func(why string) bool {
		irDeclineNote("a nested fn: " + why)
		return false
	}
	switch {
	case fd.Body == nil || ast.IsDiscardName(fd.Name):
		return no("without a body or a name")
	case len(fd.TypeParams) != 0 || len(fd.WhereClauses) != 0:
		return no("generic")
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
	lam := &ast.Lambda{Params: fd.Params, Body: fd.Body, Line: fd.Line, Col: fd.Col}
	// A try in the body leaves the nested fn, whose boundary the checker
	// names "fn <name>". Only this builder's own lambda build reads it: the
	// lambdas inside the body are built by the nested fn's builder.
	saved := bl.nestedFn
	bl.nestedFn = fd.Name
	defer func() { bl.nestedFn = saved }()
	if astNamesIdent(fd.Body, fd.Name) {
		return bl.recursiveNestedFunc(fd, lam)
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
	bind := ir.NewBind(bl.g.irPos(fd.Line, fd.Col), bl.f.NewTemp(), val, bl.sh.localSym(fd.Name))
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: fk})
	bl.bound[fd.Name], bl.boundK[fd.Name] = bind.Dst(), fk
	return true
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
func (bl *irScalarBuilder) recursiveNestedFunc(fd *ast.FuncDef, lam *ast.Lambda) bool {
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
	bl.recursive = &irRecursiveFn{lam: lam, name: fd.Name, k: fk}
	val, got, _, ok := bl.lambda(lam)
	bl.recursive = saved
	if !ok || got != fk {
		irDeclineNote("a recursive nested fn: its body is outside the lambda shape: " + fd.Name)
		return false
	}
	bind := ir.NewBind(bl.g.irPos(fd.Line, fd.Col), bl.f.NewTemp(), val, bl.sh.localSym(fd.Name))
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: fk})
	bl.bound[fd.Name], bl.boundK[fd.Name] = bind.Dst(), fk
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
