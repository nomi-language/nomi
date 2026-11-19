package analysis

import (
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
)

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
//     the call is instantiated at: the interface's own parameters as the
//     call solved them from its `self` (solveInterfaceArgs), read from the
//     call's record (callInterfaceInstance) once every body is checked.
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
			if call, ok := n.(*ast.Call); ok {
				c.recordCallInterface(call)
			}
			return true
		})
	}
	// A membership test's call is no child of the test (checkIn).
	for _, call := range c.fa.Memberships {
		c.recordCallInterface(call)
	}
}

// recordCallInterface records the interface instance call goes through,
// when it calls an interface function without a DispatchImpl.
func (c *checker) recordCallInterface(call *ast.Call) {
	pos, named := calleeKey(call.Func)
	if !named {
		return
	}
	ref := c.fa.References[pos]
	if ref == nil || ref.DispatchImpl != nil || ref.CallInterface != nil || ref.CallType == nil {
		return
	}
	base := interfaceMethodBase(ref)
	if base == nil {
		return
	}
	it := c.calledInterface(call.Func, base)
	if it == nil {
		return
	}
	inst := callInterfaceInstance(it, base.Name, ref)
	if inst == nil {
		return
	}
	cp := *ref
	cp.CallInterface = inst
	c.fa.References[pos] = &cp
}

// calleeInterface is the declared interface whose function callee names,
// however it is spelled: through the interface or its module, through an
// implementor that inherits the function, or bare. nil for any other
// callee.
func (c *checker) calleeInterface(callee ast.Node) *InterfaceType {
	if c.fa == nil || callee == nil {
		return nil
	}
	pos, named := calleeKey(callee)
	if !named {
		return nil
	}
	ref := c.fa.References[pos]
	if ref == nil {
		return nil
	}
	base := interfaceMethodBase(ref)
	if base == nil {
		return nil
	}
	return c.calledInterface(callee, base)
}

// solveInterfaceArgs solves a generic interface's own type parameters in
// subs from the type a call of one of its functions solved `self` to, as a
// bound `where self: I<T>` projects them (spec §12 *Generic interfaces*):
// the arguments of that type's impl of iface (`impl Scale<Int> for Money`
// gives N = Int for a Money; with several impls, the parameters already
// solved choose), a type parameter's bound, or an interface value's own
// arguments. `self` stays the type it was solved to. Nothing is solved
// before `self` is, for a non-generic interface, or when the type does
// not implement iface, which the call's conformance demand reports.
func (c *checker) solveInterfaceArgs(iface *InterfaceType, subs map[*TypeParam_]Type) {
	if iface == nil || iface.SelfParam == nil || len(iface.TypeParamDefs) == 0 {
		return
	}
	self, ok := subs[iface.SelfParam]
	if !ok || self == nil {
		return
	}
	if _, open := resolveTypeVar(self).(*TypeVar); open {
		return
	}
	trial := maps.Clone(subs)
	saved := saveTypeVars(self)
	if c.unifyInto(interfaceAtOwnParams(iface), self, trial) != nil {
		saved.restore()
		return
	}
	// A default's own `self` (or a value of iface at its own parameters)
	// gives each parameter itself, which unifying binds to nothing.
	if at, ok := resolveTypeVar(self).(*InterfaceType); ok && len(at.TypeArgs) == len(iface.TypeParamDefs) {
		for i, d := range iface.TypeParamDefs {
			if _, solved := trial[d]; !solved && at.TypeArgs[i] == Type(d) {
				trial[d] = d
			}
		}
	}
	maps.Copy(subs, trial)
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
// its function name is instantiated at, as ref, the call's reference,
// records it: each of its own parameters as the call's substitution solved
// it (Symbol.CallTypeArgs), or else as the instantiated signature
// (CallType) shows it against the declared one, which is what a call
// typed by its selected impl or its operator records. A bare call in one
// of its defaults is for the default's own `self` (recordCallSelf) and
// goes through the interface at its own parameters. nil when one is not
// solved.
func callInterfaceInstance(it *InterfaceType, name string, ref *Symbol) *InterfaceType {
	if len(it.TypeParamDefs) == 0 {
		return it
	}
	shown := map[*TypeParam_]Type{}
	decl := interfaceMethodFuncType(it, name)
	if inst, ok := resolveTypeVar(ref.CallType).(*FuncType); ok && decl != nil {
		for i := range decl.Params {
			if i < len(inst.Params) {
				matchTypeParams(decl.Params[i], inst.Params[i], shown)
			}
		}
		matchTypeParams(decl.Return, inst.Return, shown)
	}
	args := make([]Type, len(it.TypeParamDefs))
	for i, d := range it.TypeParamDefs {
		t := ref.CallTypeArgs[d.Name_]
		if t == nil {
			t = shown[d]
		}
		if t == nil && it.SelfParam != nil && ref.CallTypeArgs["self"] == Type(it.SelfParam) {
			t = d
		}
		if t == nil {
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
