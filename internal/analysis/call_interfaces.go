package analysis

import "github.com/nomi-language/nomi/internal/ast"

// A call of an interface function the checker selected no impl for (the
// reference carries no DispatchImpl) is dispatched where it runs: on a
// receiver of a type parameter, an interface value, or, for a function
// with no receiver, the type its result is expected at. The IR builder
// then picks, among the impls a type has of a generic interface at
// several type arguments, the one at the interface instance the call goes
// through, so the checker records that instance on the call's reference
// (Symbol.CallInterface):
//
//   - `C.holds(c, x)` under `C: Holds<T>` goes through the bound, which
//     checkTypeParamQualifiedCall records as it types the call;
//   - `Holds.holds(b, x)` goes through the interface at the type arguments
//     the call is instantiated at: the interface's function signature, its
//     self the interface applied to its own parameters, matched against the
//     call's (CallType), recorded here once every body is checked.
//
// A non-generic interface's instance is the interface itself.

// recordCallInterfaces records the interface instance each call of an
// interface function without a DispatchImpl goes through, where
// checkTypeParamQualifiedCall did not.
func (c *checker) recordCallInterfaces(nodes []ast.Node) {
	if c.fa == nil {
		return
	}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			call, ok := n.(*ast.Call)
			if !ok {
				return true
			}
			pos, named := calleeKey(call.Func)
			if !named {
				return true
			}
			ref := c.fa.References[pos]
			if ref == nil || ref.DispatchImpl != nil || ref.CallInterface != nil || ref.CallType == nil {
				return true
			}
			base := interfaceMethodBase(ref)
			if base == nil {
				return true
			}
			it := c.calledInterface(call.Func, base)
			if it == nil {
				return true
			}
			inst := callInterfaceInstance(it, base.Name, ref.CallType)
			if inst == nil {
				return true
			}
			cp := *ref
			cp.CallInterface = inst
			c.fa.References[pos] = &cp
			return true
		})
	}
}

// interfaceMethodBase is the interface function symbol ref resolves to, or
// nil when it resolves to something else.
func interfaceMethodBase(ref *Symbol) *Symbol {
	base := ref
	for base.Resolved != nil && base.Resolved != base {
		base = base.Resolved
	}
	if base.Kind != SymbolInterfaceMethod || base.OwningInterface == "" {
		return nil
	}
	return base
}

// calledInterface is the declared interface whose function base a call
// through callee names: the interface its qualifier resolves to, else the
// one the file's scope binds to the function's owning interface's name.
func (c *checker) calledInterface(callee ast.Node, base *Symbol) *InterfaceType {
	declares := func(t Type) *InterfaceType {
		it, ok := t.(*InterfaceType)
		if !ok || it.Name != base.OwningInterface || interfaceMethodFuncType(it, base.Name) == nil {
			return nil
		}
		return it
	}
	if fa, ok := callee.(*ast.FieldAccess); ok && fa.Object != nil {
		line, col := nodePos(fa.Object)
		if q := c.fa.References[Pos{Line: line, Col: col}]; q != nil {
			for q.Resolved != nil && q.Resolved != q {
				q = q.Resolved
			}
			if it := declares(q.Type); it != nil {
				return it
			}
		}
	}
	inScope := func(scope *Scope) *InterfaceType {
		if scope == nil {
			return nil
		}
		sym := scope.Lookup(base.OwningInterface)
		for sym != nil && sym.Resolved != nil && sym.Resolved != sym {
			sym = sym.Resolved
		}
		if sym == nil {
			return nil
		}
		if m := sym.Members[base.Name]; m == nil || m.Node != base.Node {
			return nil
		}
		return declares(sym.Type)
	}
	if it := inScope(c.fa.ModuleScope); it != nil {
		return it
	}
	// The interface may be bound by no name here (`Square.describe(sq)` in
	// a file that imports Square and not Shape): the file that declares
	// the function binds it.
	p := c.fa.Program()
	if p == nil {
		return nil
	}
	for _, other := range p.Files() {
		if other != nil && other != c.fa {
			if it := inScope(other.ModuleScope); it != nil {
				return it
			}
		}
	}
	for _, scope := range p.StdlibModuleScopes {
		if it := inScope(scope); it != nil {
			return it
		}
	}
	return nil
}

// callInterfaceInstance is interface it at the type arguments a call of
// its function name is instantiated at, ct: its own parameters are read
// off ct by matching the function's signature against it (elabMatch).
// nil when one is not found there.
func callInterfaceInstance(it *InterfaceType, name string, ct Type) *InterfaceType {
	if len(it.TypeParamDefs) == 0 {
		return it
	}
	decl := interfaceMethodFuncType(it, name)
	inst, ok := resolveTypeVar(ct).(*FuncType)
	if decl == nil || !ok {
		return nil
	}
	solved := map[*TypeParam_]Type{}
	for i := range decl.Params {
		if i < len(inst.Params) {
			elabMatch(decl.Params[i], inst.Params[i], solved, 0)
		}
	}
	elabMatch(decl.Return, inst.Return, solved, 0)
	args := make([]Type, len(it.TypeParamDefs))
	for i, d := range it.TypeParamDefs {
		t, found := solved[d]
		if !found || t == nil {
			return nil
		}
		args[i] = t
	}
	return &InterfaceType{
		Origin:        it.Origin,
		Name:          it.Name,
		Methods:       it.Methods,
		TypeParams:    it.TypeParams,
		TypeParamDefs: it.TypeParamDefs,
		TypeArgs:      args,
		SelfParam:     it.SelfParam,
	}
}
