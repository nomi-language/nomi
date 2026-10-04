package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// expectedType is the type the cursor's position expects, or nil.
//
// The checker's answer comes first. It recorded the expected type of every
// node it checked against one, and the reparsed tree's nodes before the
// cursor sit where the snapshot's do, so once the user has typed part of a
// name (`paint(.Re`) the snapshot holds the node and its expectation. While
// nothing is typed yet (`paint(.`) the statement does not parse, the
// checker never saw it, and the expectation is read off the reparsed tree
// instead: the slot the cursor fills (a call's parameter, an annotated
// binding's value, a returned or tail value, a struct literal's field, the
// other side of `==`, a case arm's pattern) gives the type through the
// snapshot's symbols. Transparent constructs (a case arm's or an if's body,
// a block's tail, parentheses, a named argument, a list item) pass the
// question to their parent, as the checker passes the expectation down.
func (r *completionRequest) expectedType() analysis.Type {
	if r.ctx.groupClock {
		return r.stdType("testing", "Clock")
	}
	h := r.ctx.hit
	if h == nil {
		return nil
	}
	for i := 0; i < len(h.path); i++ {
		node, field := h.at(i)
		parent, _ := h.at(i + 1)
		if n, ok := node.(ast.Node); ok {
			if t := r.recordedExpected(n); t != nil {
				return t
			}
		}
		switch p := parent.(type) {
		case *ast.Call:
			if field != "Args" {
				return nil
			}
			idx := h.path[len(h.path)-1-i].index
			if _, ok := node.(*ast.FieldAccessor); ok {
				if t := r.accessorParamType(h, i+1, p, idx); t != nil {
					return t
				}
			}
			return r.paramType(h, i+1, p, idx, "")
		case *ast.FieldAccessor:
			// A segment of `.address.ci‸`: the accessor's own expectation.
			if field != "Path" {
				return nil
			}
		case *ast.NamedArg:
			if call, ok := r.parentCall(h, i+1); ok {
				return r.paramType(h, i+2, call, -1, p.Name)
			}
			return nil
		case *ast.Binding:
			if field == "Value" && p.TypeAnnotation != nil {
				return r.typeOfTypeExpr(p.TypeAnnotation)
			}
			return nil
		case *ast.Return:
			return r.enclosingReturnType(h, i+1)
		case *ast.StructFieldVal:
			if field != "Value" {
				return nil
			}
			lit, _ := h.at(i + 2)
			if sl, ok := lit.(*ast.StructLit); ok {
				if f, ok := fieldOf(r.structLitType(h, i+2, sl), p.Name); ok {
					return f.Type
				}
			}
			return nil
		case *ast.Binary:
			switch {
			case (p.Op == "==" || p.Op == "!=") && field == "Right":
				return r.exprType(p.Left)
			case p.Op == "|>" && field == "Right":
				return nil
			}
			return nil
		case *ast.CaseBranch:
			if field == "Pattern" {
				grand, _ := h.at(i + 2)
				if c, ok := grand.(*ast.Case); ok && c.Value != nil {
					return r.exprType(c.Value)
				}
				return nil
			}
			if field != "Body" {
				return nil
			}
		case *ast.Case:
			if field != "Branches" {
				return nil
			}
		case *ast.If:
			if field != "Then" && field != "Else" {
				return nil
			}
		case *ast.Block:
			if !isTail(p, node) {
				return nil
			}
		case *ast.FuncDef:
			if field == "Body" {
				return r.typeOfTypeExpr(p.ReturnTypeExpr)
			}
			return nil
		case *ast.ExprStmt, *ast.GroupedExpr:
		case *ast.EnumPattern:
			// `.Re‸` in a case arm: the pattern's head names a variant of
			// the subject's type.
			if field != "Variant" {
				return nil
			}
		case *ast.ListLit:
			if field != "Items" {
				return nil
			}
			list := r.outerExpected(h, i+1)
			if lt, ok := analysis.ResolveTypeVar(list).(*analysis.ListType); ok {
				return lt.Elem
			}
			return nil
		case *ast.With:
			if field == "Value" {
				return r.exprType(p.Target)
			}
			return nil
		case *ast.TestDecl:
			if field == "Clock" {
				return r.stdType("testing", "Clock")
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

// outerExpected asks expectedType's question for hop i of the path, the
// enclosing construct, by walking from there.
func (r *completionRequest) outerExpected(h *sentinelHit, i int) analysis.Type {
	sub := &sentinelHit{path: h.path[:len(h.path)-i], field: "", roots: h.roots}
	saved := r.ctx.hit
	r.ctx.hit = sub
	defer func() { r.ctx.hit = saved }()
	return r.expectedType()
}

// isTail reports whether stmt is the value a block ends with.
func isTail(b *ast.Block, stmt any) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	last := b.Stmts[len(b.Stmts)-1]
	return any(last) == stmt
}

// parentCall returns the call whose argument hop i is.
func (r *completionRequest) parentCall(h *sentinelHit, i int) (*ast.Call, bool) {
	_, field := h.at(i)
	parent, _ := h.at(i + 1)
	call, ok := parent.(*ast.Call)
	return call, ok && field == "Args"
}

// paramType is the declared type of the parameter an argument fills: by
// position idx, or by name for a named argument. A pipe stage's arguments
// start at the second parameter. callHop is the call's hop on the path.
func (r *completionRequest) paramType(h *sentinelHit, callHop int, call *ast.Call, idx int, name string) analysis.Type {
	ft, ok := analysis.ResolveTypeVar(r.exprType(call.Func)).(*analysis.FuncType)
	if !ok {
		return nil
	}
	if name != "" {
		idx = paramIndexByName(r.calleeSymbol(call.Func), name)
	} else {
		_, field := h.at(callHop)
		parent, _ := h.at(callHop + 1)
		if b, ok := parent.(*ast.Binary); ok && b.Op == "|>" && field == "Right" {
			idx++
		}
	}
	if idx < 0 || idx >= len(ft.Params) {
		return nil
	}
	return ft.Params[idx]
}

// calleeSymbol is the function symbol a call's callee names.
func (r *completionRequest) calleeSymbol(fn ast.Node) *analysis.Symbol {
	switch v := fn.(type) {
	case *ast.Ident:
		return realSymbol(r.scope.Lookup(v.Name))
	case *ast.FieldAccess:
		return r.memberSymbol(v)
	}
	return nil
}

// paramIndexByName finds a named parameter's position in fn's declaration.
func paramIndexByName(fn *analysis.Symbol, name string) int {
	if fn == nil {
		return -1
	}
	var params []ast.Param
	switch n := fn.Node.(type) {
	case *ast.FuncDef:
		params = n.Params
	case *ast.ExternFunc:
		params = n.Params
	case *ast.InterfaceMethod:
		params = n.Params
	}
	for i, p := range params {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// enclosingReturnType is the declared return type of the function a
// `return` at hop i leaves: the nearest enclosing fn. A `return` inside a
// lambda leaves the lambda, whose type the tree does not declare.
func (r *completionRequest) enclosingReturnType(h *sentinelHit, i int) analysis.Type {
	for ; i < len(h.path); i++ {
		node, _ := h.at(i)
		switch n := node.(type) {
		case *ast.FuncDef:
			return r.typeOfTypeExpr(n.ReturnTypeExpr)
		case *ast.Lambda:
			return nil
		}
	}
	return nil
}

// structLitType is the type a struct literal builds: its named type, its
// spread's type, or, for an anonymous literal, the type its position
// expects.
func (r *completionRequest) structLitType(h *sentinelHit, hop int, lit *ast.StructLit) analysis.Type {
	switch {
	case lit.TypeName != nil:
		if dv, ok := lit.TypeName.(*ast.DotVariantType); ok {
			return r.variantPayloadType(r.outerExpected(h, hop), dv.Name)
		}
		return r.typeOfTypeExpr(lit.TypeName)
	case lit.Spread != nil:
		return r.exprType(lit.Spread)
	}
	// The expected type first: an anonymous literal's own recorded type
	// holds only the fields it already writes.
	if t := r.outerExpected(h, hop); t != nil {
		return t
	}
	return r.recordedType(lit)
}

// variantPayloadType is the anonymous struct of a struct variant's fields
// (`.Rect{w: 1}` against Shape).
func (r *completionRequest) variantPayloadType(enum analysis.Type, variant string) analysis.Type {
	et, ok := analysis.ResolveTypeVar(enum).(*analysis.EnumType)
	if !ok {
		return nil
	}
	for _, v := range et.Variants {
		if v.Name == variant && v.Kind == analysis.VariantStruct {
			return &analysis.AnonStructType{Fields: v.Fields}
		}
	}
	return nil
}

// typeOfTypeExpr resolves a written type through the names visible at the
// cursor. A generic type carries the arguments it is written with when each
// resolves (instantiatedGeneric), and is otherwise its declaration without
// arguments: enough to name its owner, its variants and its fields.
func (r *completionRequest) typeOfTypeExpr(te ast.TypeExpr) analysis.Type {
	switch v := te.(type) {
	case *ast.SimpleType:
		return typeOfTypeSymbol(r.scope.Lookup(v.Name))
	case *ast.GenericType:
		return r.instantiatedGeneric(v, typeOfTypeSymbol(r.scope.Lookup(v.Name)))
	case *ast.QualifiedType:
		if mod := realSymbol(r.scope.Lookup(v.Module)); mod != nil && mod.ModuleScope != nil {
			switch m := v.Member.(type) {
			case *ast.SimpleType:
				return typeOfTypeSymbol(mod.ModuleScope.LookupLocal(m.Name))
			case *ast.GenericType:
				return r.instantiatedGeneric(m, typeOfTypeSymbol(mod.ModuleScope.LookupLocal(m.Name)))
			}
		}
	}
	return nil
}

func typeOfTypeSymbol(sym *analysis.Symbol) analysis.Type {
	sym = realSymbol(sym)
	if sym == nil || !typeSymbol(sym) {
		return nil
	}
	return sym.Type
}

// stdType is a standard-library file's type by name.
func (r *completionRequest) stdType(module, name string) analysis.Type {
	if r.s.std == nil || r.s.std.Modules[module] == nil {
		return nil
	}
	return typeOfTypeSymbol(r.s.std.Modules[module].LookupLocal(name))
}

// candidateType is the type a candidate contributes where it is inserted:
// a called function's return type, a value's type, a variant's enum.
func (r *completionRequest) candidateType(c candidate) analysis.Type {
	t := c.typ
	if t == nil && c.sym != nil {
		real := realSymbol(c.sym)
		switch real.Kind {
		case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType,
			analysis.SymbolTypeAlias, analysis.SymbolInterface, analysis.SymbolModule:
			return nil
		}
		t = real.Type
	}
	t = analysis.ResolveTypeVar(t)
	if ft, ok := t.(*analysis.FuncType); ok && !r.expectsFunction() {
		return analysis.ResolveTypeVar(ft.Return)
	}
	return t
}

// typeFits reports whether a value of type have can stand where want is
// expected: the same type, a type that implements an expected interface,
// or the same owner where either side is still generic (`Maybe<T>` for an
// expected `Maybe<Int>`).
func (r *completionRequest) typeFits(have, want analysis.Type) bool {
	have, want = analysis.ResolveTypeVar(have), analysis.ResolveTypeVar(want)
	if have == nil || want == nil {
		return false
	}
	if _, ok := want.(*analysis.TypeVar); ok {
		return false
	}
	if _, ok := want.(*analysis.TypeParam_); ok {
		return false
	}
	if analysis.TypesEqual(have, want) {
		return true
	}
	if iface, ok := want.(*analysis.InterfaceType); ok {
		return analysis.ImplementsInterface(r.fa, have, iface.Name)
	}
	if analysis.ContainsTypeParam(have) || analysis.ContainsTypeParam(want) {
		name := analysis.TypeOwnerName(want)
		return name != "" && name == analysis.TypeOwnerName(have)
	}
	return false
}
