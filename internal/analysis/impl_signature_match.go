package analysis

import "github.com/nomi-language/nomi/internal/ast"

// implSigMatcher compares one impl function's resolved signature against the
// interface function it implements, after the interface's type parameters
// have been replaced by the impl header's type arguments.
//
// The type parameters on the two sides are not all alike:
//
//   - The impl's own type parameters (the `T` of `impl Store<String, T> for
//     Cache<T>`) are rigid. Inside the impl, `T` is one fixed unknown type,
//     so where the interface requires `T` the function must take or return
//     `T` and nothing else. `Maybe<Int>` does not match `Maybe<T>`, and with
//     `impl Pair<A, B> for Two<A, B>`, `B` does not match `A`.
//   - The function-level type parameters (`fn map<U>` on the interface,
//     `fn map(…, f: (T) -> U)` in the impl) are bound per call. The interface
//     function's must correspond one to one with the impl function's: the
//     impl may rename them, but not fix one to a concrete type, merge two,
//     or replace one with a rigid parameter.
//   - `self` when it stays unsubstituted (a return-only `self`, as in
//     `FromJson`), and an interface parameter the header gives no argument
//     for (`impl Container for Shelf<T>`), are flexible: they bind to
//     whatever the impl writes. An unsubstituted interface parameter binds
//     the same way in every function of one block.
//
// One substitution map covers every parameter and the return type of a
// function, so a function-level parameter binds consistently across them.
type implSigMatcher struct {
	iface *InterfaceType
	// rigidExpected holds the impl's type parameters as they appear on the
	// interface side (the pointers the header's type arguments resolved to).
	rigidExpected map[*TypeParam_]bool
	// rigidNames is every impl type parameter's name. On the impl function's
	// side a name here is rigid unless the function declares its own
	// parameter of that name.
	rigidNames map[string]bool
	// ifaceParams are the interface's own type parameters. Left
	// unsubstituted (no header argument), they are flexible.
	ifaceParams map[*TypeParam_]bool
	// ifaceBindings holds what each unsubstituted interface parameter bound
	// to so far in this block.
	ifaceBindings map[*TypeParam_]Type
	// blockTP is the impl's type parameters by name, carrying the bounds the
	// block and the receiver's declaration give them.
	blockTP map[string]*TypeParam_
}

func newImplSigMatcher(iface *InterfaceType, blockTP map[string]*TypeParam_) *implSigMatcher {
	m := &implSigMatcher{
		iface:         iface,
		rigidExpected: map[*TypeParam_]bool{},
		rigidNames:    map[string]bool{},
		ifaceParams:   map[*TypeParam_]bool{},
		ifaceBindings: map[*TypeParam_]Type{},
		blockTP:       blockTP,
	}
	for name, tp := range blockTP {
		m.rigidNames[name] = true
		m.rigidExpected[tp] = true
	}
	for _, def := range iface.TypeParamDefs {
		m.ifaceParams[def] = true
	}
	return m
}

// implSigFunc is the per-function state: the shared substitution map and
// the impl function's own type parameters by name.
type implSigFunc struct {
	m *implSigMatcher
	// subs maps interface-side type parameters to what they matched on the
	// impl side (and, where unification bound one, an impl-side parameter to
	// an interface-side type).
	subs map[*TypeParam_]Type
	// fnLocal names the impl function's declared type parameters.
	fnLocal map[string]bool
	// canon maps each impl-side type parameter to the one pointer that
	// stands for its name, so two mentions of `U` are one parameter.
	canon map[*TypeParam_]Type
	// expectedSide and actualSide hold the parameters met on each side.
	expectedSide map[*TypeParam_]bool
	actualSide   map[*TypeParam_]bool
	byName       map[string]*TypeParam_
}

func (m *implSigMatcher) function(declared []ast.TypeParam) *implSigFunc {
	f := &implSigFunc{
		m:            m,
		subs:         map[*TypeParam_]Type{},
		fnLocal:      map[string]bool{},
		canon:        map[*TypeParam_]Type{},
		expectedSide: map[*TypeParam_]bool{},
		actualSide:   map[*TypeParam_]bool{},
		byName:       map[string]*TypeParam_{},
	}
	for _, tp := range declared {
		f.fnLocal[tp.Name] = true
	}
	return f
}

// match reports whether the impl's `actual` matches the interface's
// `expected` at one position, given the bindings the function's earlier
// positions made. A type with no type parameter on either side is compared
// as before: equal, or unifiable. A position that does not match leaves the
// bindings as they were, so it is reported alone.
func (f *implSigFunc) match(expected, actual Type) bool {
	if !ContainsTypeParam(expected) && !ContainsTypeParam(actual) {
		return TypesEqual(expected, actual) || UnifyWith(expected, actual, map[*TypeParam_]Type{}) == nil
	}
	collectTypeParamPtrs(expected, f.expectedSide)
	actual = f.canonical(actual)
	saved := make(map[*TypeParam_]Type, len(f.subs))
	for k, v := range f.subs {
		saved[k] = v
	}
	if UnifyWith(expected, actual, f.subs) == nil && f.consistent(false) {
		return true
	}
	f.subs = saved
	return false
}

// canonical rewrites every impl-side type parameter to the first pointer
// seen for its name.
func (f *implSigFunc) canonical(t Type) Type {
	found := map[*TypeParam_]bool{}
	collectTypeParamPtrs(t, found)
	for tp := range found {
		first, ok := f.byName[tp.Name_]
		if !ok {
			f.byName[tp.Name_] = tp
			first = tp
		}
		f.actualSide[first] = true
		if first != tp {
			f.canon[tp] = first
		}
	}
	return Substitute(t, f.canon)
}

// actualRigid reports whether an impl-side parameter is one of the impl's
// own type parameters rather than the function's.
func (f *implSigFunc) actualRigid(tp *TypeParam_) bool {
	return f.m.rigidNames[tp.Name_] && !f.fnLocal[tp.Name_]
}

// flexible reports whether an interface-side parameter binds to whatever
// the impl writes: `self` left unsubstituted, or an interface parameter the
// header gave no argument for.
func (f *implSigFunc) flexible(tp *TypeParam_) bool {
	return f.expectedSide[tp] && (tp == f.m.iface.SelfParam || f.m.ifaceParams[tp])
}

// consistent checks the bindings unification made against the rules in
// implSigMatcher's comment. With commit, it also records what the block's
// unsubstituted interface parameters bound to, for the next function.
func (f *implSigFunc) consistent(commit bool) bool {
	reverse := map[*TypeParam_]*TypeParam_{}
	for tp, bound := range f.subs {
		bound = resolveTV(bound)
		if f.flexible(tp) {
			if f.m.ifaceParams[tp] {
				if prev, ok := f.m.ifaceBindings[tp]; ok && !TypesEqual(prev, bound) {
					return false
				}
				if commit {
					f.m.ifaceBindings[tp] = bound
				}
			}
			continue
		}
		if f.actualSide[tp] && !f.expectedSide[tp] {
			// Unification bound an impl-side parameter, which happens only
			// when the interface side holds something other than a
			// parameter there. Rigid or function-local, an impl parameter
			// does not implement a fixed type.
			return false
		}
		other, ok := bound.(*TypeParam_)
		if !ok || !f.actualSide[other] {
			return false
		}
		if f.m.rigidExpected[tp] {
			if !f.actualRigid(other) || other.Name_ != tp.Name_ {
				return false
			}
			continue
		}
		// An interface function-level parameter: it must be an impl
		// function-level parameter, and no other one may map to it.
		if f.actualRigid(other) {
			return false
		}
		if prev, ok := reverse[other]; ok && prev != tp {
			return false
		}
		reverse[other] = tp
	}
	return true
}

// wherePosition is one `where` constraint the impl function adds that the
// interface function does not carry.
type wherePosition struct {
	param string
	iface string
	// implParam marks one of the impl's own type parameters, whose bound
	// belongs on the impl block.
	implParam bool
}

// extraWhereBounds lists the `where` bounds the impl function places on a
// type parameter beyond what that parameter already carries. A caller
// through the interface checks only the interface function's bounds, so an
// extra one is never checked for it, and the impl's body may then call a
// function the argument's type does not have.
//
// A function-level parameter may carry what the interface function's
// corresponding parameter carries. One of the impl's own parameters may
// carry what the impl block and the receiver's declaration give it
// (`impl Show<T> for Cache<T> where T: Named`), plus what the interface
// requires of the interface parameter it stands for.
func (f *implSigFunc) extraWhereBounds(sig *MethodSig, ft *FuncType) []wherePosition {
	var extra []wherePosition
	for _, wb := range ft.WhereBounds {
		if wb.Param == nil {
			continue
		}
		var have []*InterfaceType
		rigid := f.actualRigid(wb.Param)
		if rigid {
			have = f.rigidBounds(wb.Param.Name_, sig)
		} else {
			param := wb.Param
			if c, ok := f.canon[param].(*TypeParam_); ok {
				param = c
			}
			var counterpart *TypeParam_
			for tp, bound := range f.subs {
				if b, ok := resolveTV(bound).(*TypeParam_); ok && b == param && f.expectedSide[tp] && !f.flexible(tp) {
					counterpart = tp
					break
				}
			}
			if counterpart == nil {
				continue
			}
			have = append(have, counterpart.Bounds...)
			for _, req := range sig.WhereBounds {
				if req.Param == counterpart {
					have = append(have, req.Bounds...)
				}
			}
		}
		for _, b := range wb.Bounds {
			if !boundListHas(have, b) {
				extra = append(extra, wherePosition{param: wb.Param.Name_, iface: b.Name, implParam: rigid})
			}
		}
	}
	return extra
}

// rigidBounds is every bound one of the impl's own type parameters carries
// without the function's help: the impl block's and the receiver
// declaration's (both on the block's parameter), and the interface's, on
// its own parameter and in the interface function's `where`, wherever the
// header passes this parameter as that interface parameter's argument.
func (f *implSigFunc) rigidBounds(name string, sig *MethodSig) []*InterfaceType {
	var have []*InterfaceType
	if tp := f.m.blockTP[name]; tp != nil {
		have = append(have, tp.Bounds...)
	}
	iface := f.m.iface
	for i, def := range iface.TypeParamDefs {
		if i >= len(iface.TypeArgs) {
			break
		}
		arg, ok := resolveTV(iface.TypeArgs[i]).(*TypeParam_)
		if !ok || arg.Name_ != name || !f.m.rigidExpected[arg] {
			continue
		}
		have = append(have, def.Bounds...)
		for _, req := range sig.WhereBounds {
			if req.Param == def {
				have = append(have, req.Bounds...)
			}
		}
	}
	return have
}

func boundListHas(list []*InterfaceType, b *InterfaceType) bool {
	for _, have := range list {
		if have != nil && b != nil && sameNominalIdentity(have.Origin, have.Name, b.Origin, b.Name) {
			return true
		}
	}
	return false
}

// collectTypeParamPtrs adds every type parameter t mentions to out.
func collectTypeParamPtrs(t Type, out map[*TypeParam_]bool) {
	switch tt := resolveTV(t).(type) {
	case *TypeParam_:
		out[tt] = true
	case *ListType:
		collectTypeParamPtrs(tt.Elem, out)
	case *MapType:
		collectTypeParamPtrs(tt.Key, out)
		collectTypeParamPtrs(tt.Val, out)
	case *PartialType:
		collectTypeParamPtrs(tt.Inner, out)
	case *TupleType:
		for _, e := range tt.Elems {
			collectTypeParamPtrs(e, out)
		}
	case *FuncType:
		for _, p := range tt.Params {
			collectTypeParamPtrs(p, out)
		}
		collectTypeParamPtrs(tt.Return, out)
	case *StructType:
		for _, a := range tt.TypeArgs {
			collectTypeParamPtrs(a, out)
		}
	case *EnumType:
		for _, a := range tt.TypeArgs {
			collectTypeParamPtrs(a, out)
		}
	case *InterfaceType:
		for _, a := range tt.TypeArgs {
			collectTypeParamPtrs(a, out)
		}
	case *DistinctType:
		for _, a := range tt.TypeArgs {
			collectTypeParamPtrs(a, out)
		}
	case *AnonStructType:
		for _, fld := range tt.Fields {
			collectTypeParamPtrs(fld.Type, out)
		}
	}
}
