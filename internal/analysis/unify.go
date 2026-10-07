package analysis

import "fmt"

// Unify attempts to make two types equal by resolving type variables.
// It also handles TypeParam_ bindings via the optional subs map: when a TypeParam_
// is encountered, it's bound in subs (or checked for consistency if already bound).
// Pass nil for subs if no type param resolution is needed.
func Unify(a, b Type) error {
	return unifyFull(a, b, nil, nil, nil)
}

// UnifyWith is like Unify but also resolves TypeParam_ pointers into the subs
// map. Subs is keyed by *TypeParam_ pointer identity rather than name so that
// distinct scopes carrying same-named type parameters (e.g. Maybe<T> and
// Result<T, E>) don't collide.
func UnifyWith(a, b Type, subs map[*TypeParam_]Type) error {
	return unifyFull(a, b, subs, nil, nil)
}

// UnifyWithImpls is like UnifyWith but also knows which types implement which
// interfaces, enabling concrete-vs-interface unification (e.g. List<Int> against
// Iter<T> binds T to Int). Pass a slice of impls maps in lookup order
// (e.g. file-local impls first, then stdlib impls). `implTypeArgs` is the
// parallel slice of file-local + stdlib interface type-argument templates
// ([type][iface]→template), used by `unifyInterfaceAgainstConcrete` to bind any
// generic interface's type parameters from a concrete receiver (Iter's
// element type is one ordinary entry — its `T` solves from `each_while`'s
// declared `yield: (T) -> Bool` parameter like every other interface).
func UnifyWithImpls(a, b Type, subs map[*TypeParam_]Type, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs) error {
	return unifyFull(a, b, subs, impls, implTypeArgs)
}

func unifyFull(a, b Type, subs map[*TypeParam_]Type, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs) error {
	return unifyIn(unifySym, a, b, subs, impls, implTypeArgs)
}

// UnifyInto unifies have, the type of a value, with want, the type of the
// position it flows into (a parameter, an annotated binding, a field, a list
// element, a result), and admits a function value whose type is assignable to
// want's: a function type `(P1) -> R1` is assignable to `(P2) -> R2` when each
// P2 is assignable to its P1 (parameters are contravariant) and R1 is
// assignable to R2 (results are covariant). Inside a function type,
// "assignable" is exact, or a concrete type where its interface is expected (a
// List where an Iter is, an Int where a Display is), or an embedded type where
// its enum is, or a function type by this same rule: the conversions the IR
// builder's adapter closure performs (irbuild/irfuncwiden.go). Outside
// function types it is unifyFull.
func UnifyInto(want, have Type, subs map[*TypeParam_]Type, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs) error {
	return unifyIn(unifyAssign, want, have, subs, impls, implTypeArgs)
}

// unifyMode is how unifyIn compares two types.
type unifyMode uint8

const (
	// unifySym has no direction: a join of two branches, two list elements,
	// two operands. Outside function types it admits interface against
	// concrete and enum against embedded type either way round. Two function
	// types meet strictly, since neither may be widened into the other
	// without knowing which flows where.
	unifySym unifyMode = iota
	// unifyStrict equates two types up to inference variables and type
	// parameters: no interface admits a concrete type and no enum an
	// embedded one. A function's parameter or result is compared this way
	// when nothing says which way the value flows, and so is a type argument
	// inside a function type, since the builder converts no container.
	unifyStrict
	// unifyAssign is UnifyInto: a is the expected type and b the actual one.
	// Outside function types it is unifySym.
	unifyAssign
	// unifyAssignIn is unifyAssign inside a function type: a is expected and
	// b actual, and only b into a is admitted.
	unifyAssignIn
)

// nested is the mode for the type arguments of a container or nominal type,
// a tuple's elements and a record's fields.
func (m unifyMode) nested() unifyMode {
	switch m {
	case unifyStrict, unifyAssignIn:
		return unifyStrict
	}
	return unifySym
}

// rebound is the mode a type parameter's existing binding meets another type
// in. A parameter bound to a function type joins another function type
// strictly: `pick(c, f, g)` with `fn pick<T>(c: Bool, a: T, b: T): T` widens
// neither function's parameters into the other's.
func (m unifyMode) rebound(existing Type) unifyMode {
	if _, isFunc := resolveTV(existing).(*FuncType); isFunc {
		return unifyStrict
	}
	return m
}

func unifyIn(m unifyMode, a, b Type, subs map[*TypeParam_]Type, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs) error {
	if a == nil || b == nil {
		return nil
	}

	a = resolveTV(a)
	b = resolveTV(b)

	// `()` and Unit are two spellings of "no value" — the checker infers an
	// empty tuple for a FuncType return whose body is Unit-typed. Collapse the
	// empty tuple to the Unit singleton on both sides so the PrimitiveType case
	// below sees Unit ≡ Unit instead of TupleType vs PrimitiveType (which would
	// spuriously fail, e.g. `(Display) -> ()` vs `(Display) -> Unit`).
	if isEmptyTuple(a) {
		a = TypeUnit
	}
	if isEmptyTuple(b) {
		b = TypeUnit
	}

	// Same TypeParam_ on both sides — trivially equal when pointer-identical.
	if tpa, ok1 := a.(*TypeParam_); ok1 {
		if tpb, ok2 := b.(*TypeParam_); ok2 && tpa == tpb {
			return nil
		}
	}

	// When there's no subs map (caller doesn't want TypeParam_ recording),
	// TypeVar binding takes precedence over TypeParam_. Without this, a
	// nil-subs unify between a TypeVar and a TypeParam_ would silently
	// no-op (TypeParam_ branch requires subs, TypeVar branch is reached
	// later but only fires when neither side is a TypeParam_).
	if subs == nil {
		if _, isVarA := a.(*TypeVar); isVarA {
			if _, isTPB := b.(*TypeParam_); isTPB {
				a.(*TypeVar).Resolved = b
				return nil
			}
		}
		if _, isVarB := b.(*TypeVar); isVarB {
			if _, isTPA := a.(*TypeParam_); isTPA {
				b.(*TypeVar).Resolved = a
				return nil
			}
		}
	}

	// An interface against a type parameter bounded by it solves the
	// interface's type arguments from the bound's before the parameter binds.
	if iface, ok := a.(*InterfaceType); ok && subs != nil {
		if tp, ok := b.(*TypeParam_); ok {
			projectThroughBound(iface, tp, subs, impls, implTypeArgs)
		}
	}
	if iface, ok := b.(*InterfaceType); ok && subs != nil {
		if tp, ok := a.(*TypeParam_); ok {
			projectThroughBound(iface, tp, subs, impls, implTypeArgs)
		}
	}

	// TypeParam_ on either side: bind or check in subs map. Keyed by pointer
	// identity so distinct TypeParam_ nodes with the same name (e.g. callee T
	// vs caller T) each get their own binding.
	if tp, ok := a.(*TypeParam_); ok && subs != nil {
		if existing, bound := subs[tp]; bound {
			// Already bound — pass nil subs so TypeVar binding (via the
			// nil-subs reorder above) takes precedence when unifying the
			// existing binding against the new side.
			return unifyIn(m.rebound(existing), existing, b, nil, impls, implTypeArgs)
		}
		if occursInIdent(tp, b) {
			return nil // occurs check: would create infinite type
		}
		subs[tp] = b
		return nil
	}
	if tp, ok := b.(*TypeParam_); ok && subs != nil {
		if existing, bound := subs[tp]; bound {
			return unifyIn(m.rebound(existing), a, existing, nil, impls, implTypeArgs)
		}
		if occursInIdent(tp, a) {
			return nil // occurs check: would create infinite type
		}
		subs[tp] = a
		return nil
	}

	// True/False as values are members of the Bool enum: accept `True` or
	// `False` where `Bool` is expected and vice versa. This is the one
	// subtyping relation Nomi's type system admits today — `Bool` is defined
	// as `enum Bool { embeds True | embeds False }`, so a value of a variant
	// primitive type conforms to the enum type.
	if isBoolVariantVsBool(a, b) || isBoolVariantVsBool(b, a) {
		return nil
	}

	// `embeds` subtype coercion: a value of an embedded type X (struct or
	// distinct) is also a value of the enum E that embeds it. The runtime
	// already handles bare-embedded values in EnumPattern matches; this rule
	// teaches the unifier to admit the same coercion at type-check time, so
	// `id: Identifier = uid` and `[uid, Identifier.Anonymous]` typecheck
	// without explicit `Identifier.UserId(uid)` wrapping. Symmetric so the
	// unifier doesn't care which side carries the expected type.
	//
	// Inside a function type an embedded type enters its enum only from the
	// actual side (unifyAssignIn), and under unifyStrict not at all.
	if et, ok := a.(*EnumType); ok {
		if isEmbeddedTypeOf(b, et) {
			if m == unifyStrict {
				return typeErrorf("cannot unify %s with %s", a, b)
			}
			return nil
		}
	}
	if et, ok := b.(*EnumType); ok {
		if isEmbeddedTypeOf(a, et) {
			if m == unifyStrict || m == unifyAssignIn {
				return typeErrorf("cannot unify %s with %s", a, b)
			}
			return nil
		}
	}

	// If either side is a type variable, bind it.
	if tv, ok := a.(*TypeVar); ok {
		if tv2, ok2 := b.(*TypeVar); ok2 && tv.ID == tv2.ID {
			return nil
		}
		tv.Resolved = b
		return nil
	}
	if tv, ok := b.(*TypeVar); ok {
		tv.Resolved = a
		return nil
	}

	// Any is compatible with everything.
	if a == TypeAny || b == TypeAny {
		return nil
	}

	// Infallible (bottom/never type) unifies with everything.
	// It represents divergent expressions (break, continue, return).
	if a == TypeInfallible {
		return nil
	}
	if b == TypeInfallible {
		return nil
	}

	// Interface vs concrete: if the concrete type implements the interface,
	// unify the interface's type arguments against the element type(s) the
	// concrete type produces for that interface.
	if ifaceA, ok := a.(*InterfaceType); ok {
		if ifaceB, ok2 := b.(*InterfaceType); ok2 {
			if !sameNominalIdentity(ifaceA.Origin, ifaceA.Name, ifaceB.Origin, ifaceB.Name) || len(ifaceA.TypeArgs) != len(ifaceB.TypeArgs) {
				return typeErrorf("cannot unify %s with %s", a, b)
			}
			for i := range ifaceA.TypeArgs {
				if err := unifyIn(m.nested(), ifaceA.TypeArgs[i], ifaceB.TypeArgs[i], subs, impls, implTypeArgs); err != nil {
					return err
				}
			}
			return nil
		}
		if m == unifyStrict {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		return unifyInterfaceAgainstConcrete(m, ifaceA, b, subs, impls, implTypeArgs)
	}
	if ifaceB, ok := b.(*InterfaceType); ok {
		// Inside a function type the actual side never narrows from an
		// interface to a concrete type.
		if m == unifyStrict || m == unifyAssignIn {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		return unifyInterfaceAgainstConcrete(m, ifaceB, a, subs, impls, implTypeArgs)
	}

	// Both are concrete — structural comparison.
	switch at := a.(type) {
	case *PrimitiveType:
		bt, ok := b.(*PrimitiveType)
		if !ok || !samePrimitive(at, bt) {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		return nil

	case *ListType:
		bt, ok := b.(*ListType)
		if !ok {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		return unifyIn(m.nested(), at.Elem, bt.Elem, subs, impls, implTypeArgs)

	case *MapType:
		bt, ok := b.(*MapType)
		if !ok {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		if err := unifyIn(m.nested(), at.Key, bt.Key, subs, impls, implTypeArgs); err != nil {
			return err
		}
		return unifyIn(m.nested(), at.Val, bt.Val, subs, impls, implTypeArgs)

	case *FuncType:
		bt, ok := b.(*FuncType)
		if !ok || len(at.Params) != len(bt.Params) {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		if m == unifyAssign || m == unifyAssignIn {
			// b's function is called with a's arguments, so each of a's
			// parameters flows into b's (contravariant), and b's result
			// into a's (covariant).
			for i := range at.Params {
				if err := unifyIn(unifyAssignIn, bt.Params[i], at.Params[i], subs, impls, implTypeArgs); err != nil {
					return err
				}
			}
			return unifyIn(unifyAssignIn, normalizeReturn(at.Return), normalizeReturn(bt.Return), subs, impls, implTypeArgs)
		}
		for i := range at.Params {
			if err := unifyIn(unifyStrict, at.Params[i], bt.Params[i], subs, impls, implTypeArgs); err != nil {
				return err
			}
		}
		return unifyIn(unifyStrict, normalizeReturn(at.Return), normalizeReturn(bt.Return), subs, impls, implTypeArgs)

	case *TupleType:
		bt, ok := b.(*TupleType)
		if !ok || len(at.Elems) != len(bt.Elems) {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		for i := range at.Elems {
			if err := unifyIn(m.nested(), at.Elems[i], bt.Elems[i], subs, impls, implTypeArgs); err != nil {
				return err
			}
		}
		return nil

	case *StructType:
		bt, ok := b.(*StructType)
		if !ok || !sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) || len(at.TypeArgs) != len(bt.TypeArgs) {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		for i := range at.TypeArgs {
			if err := unifyIn(m.nested(), at.TypeArgs[i], bt.TypeArgs[i], subs, impls, implTypeArgs); err != nil {
				return err
			}
		}
		return nil

	case *AnonStructType:
		bt, ok := b.(*AnonStructType)
		if !ok || len(at.Fields) != len(bt.Fields) {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		bByName := make(map[string]Type, len(bt.Fields))
		for _, f := range bt.Fields {
			bByName[f.Name] = f.Type
		}
		for _, f := range at.Fields {
			bTy, ok := bByName[f.Name]
			if !ok {
				return typeErrorf("cannot unify %s with %s", a, b)
			}
			if err := unifyIn(m.nested(), f.Type, bTy, subs, impls, implTypeArgs); err != nil {
				return err
			}
		}
		return nil

	case *EnumType:
		bt, ok := b.(*EnumType)
		if !ok || !sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) || len(at.TypeArgs) != len(bt.TypeArgs) {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		for i := range at.TypeArgs {
			if err := unifyIn(m.nested(), at.TypeArgs[i], bt.TypeArgs[i], subs, impls, implTypeArgs); err != nil {
				return err
			}
		}
		return nil

	case *DistinctType:
		bt, ok := b.(*DistinctType)
		if !ok || !sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) {
			return typeErrorf("cannot unify %s with %s", a, b)
		}
		// Recurse into TypeArgs for generic opaque externs
		// (`host type Task<T>`, `host type Channel<T>`). Non-generic
		// distinct types leave TypeArgs nil on both sides, so the loop
		// is a no-op for them.
		if len(at.TypeArgs) != len(bt.TypeArgs) {
			// One side carries instantiation, the other doesn't. Treat
			// as compatible to avoid false negatives during partial
			// inference: the shell-form Task with no TypeArgs is what
			// the extern declaration carries; concrete instantiations
			// carry TypeArgs. Unification leaves T unsolved on the
			// shell side, which is the existing nil-args behavior.
			return nil
		}
		for i := range at.TypeArgs {
			if err := unifyIn(m.nested(), at.TypeArgs[i], bt.TypeArgs[i], subs, impls, implTypeArgs); err != nil {
				return err
			}
		}
		return nil

	default:
		return typeErrorf("cannot unify %s with %s", a, b)
	}
}

// unifyInterfaceAgainstConcrete succeeds when `concrete` implements `iface`,
// unifying the interface's type arguments against the ones `concrete` produces
// for that interface. The type args are read from the general ImplTypeArgs
// registry — for each `(type, iface)` pair, a template solved at build time
// from the impl's method signatures (see recordImplTypeArgsFromBlock): the
// interface's type args expressed in the impl type's own formal params. We
// substitute the concrete receiver's type args into that template and unify
// the result against the interface's type args.
//
// This binds *any* generic interface's type parameters from a concrete
// receiver — `Iter<T>` against `List<Int>` binds `T → Int` exactly like
// before (its template's single arg `T` solves from `next`'s declared
// `Maybe<(T, self)>` shape), and so does a user `Chooser<T>` whose default
// returns bare `T`. Non-generic interfaces (no type args) and pairs with no
// recorded template fall through to accepting the conformance without binding
// — the same permissive fallback as the prior "unknown element" path.
func unifyInterfaceAgainstConcrete(m unifyMode, iface *InterfaceType, concrete Type, subs map[*TypeParam_]Type, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs) error {
	// Universal struct interface: `Struct` is satisfied structurally by every
	// struct — named or anonymous — with no impl-table entry. Handled before
	// the impl-table lookups below, both because there is no entry to find and
	// because an anonymous struct has no nominal name to key on. `Struct`
	// declares no type args, so there is nothing further to unify. This is the
	// central conformance point: list elements, fields, returns, and existential
	// params all reach it, so `List<Struct>` / `field s: Struct` / `(): Struct`
	// accept any struct uniformly.
	if iface.Name == "Struct" {
		if isStructShaped(concrete) {
			return nil
		}
		return fmt.Errorf("%s does not implement Struct", concrete)
	}
	concreteName := interfaceImplName(concrete)
	if concreteName == "" {
		return typeErrorf("cannot unify %s with %s", iface, concrete)
	}
	if !interfaceImplemented(concreteName, iface.Name, iface.Origin, impls) {
		return typeErrorf("%s does not implement %s", concrete, iface)
	}
	info := lookupImplTypeArgs(concreteName, iface.Name, implTypeArgs)
	if info != nil && !implReceiverMatches(info, concrete, impls, implTypeArgs) {
		return fmt.Errorf("%s does not implement %s", concrete, iface.Name)
	}
	if len(iface.TypeArgs) == 0 {
		return nil
	}
	if info == nil || len(info.Args) != len(iface.TypeArgs) {
		// No recorded template (or an arity mismatch from a malformed impl):
		// accept the conformance without binding the interface's type args,
		// leaving them as fresh vars at the use site.
		return nil
	}
	cargs := concreteTypeArgs(concrete)
	for i := range iface.TypeArgs {
		arg := substituteTypeParamDefs(info.TypeParamDefs, cargs, info.Args[i])
		if err := unifyIn(m.nested(), iface.TypeArgs[i], arg, subs, impls, implTypeArgs); err != nil {
			return err
		}
	}
	return nil
}

func implReceiverMatches(info *ImplTypeArgs, concrete Type, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs) bool {
	return implReceiverMatchesSeen(info, concrete, impls, implTypeArgs, nil)
}

func implReceiverMatchesSeen(info *ImplTypeArgs, concrete Type, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs, seen map[string]bool) bool {
	if info == nil || info.Receiver == nil || concrete == nil {
		return true
	}
	subs := map[*TypeParam_]Type{}
	if unifyFull(info.Receiver, concrete, subs, impls, implTypeArgs) != nil {
		return false
	}
	for tp, boundConcrete := range subs {
		for _, bound := range tp.Bounds {
			if !typeSatisfiesInterfaceForImplMatch(boundConcrete, bound, impls, implTypeArgs, seen) {
				return false
			}
		}
	}
	return true
}

func typeSatisfiesInterfaceForImplMatch(concrete Type, iface *InterfaceType, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs, seen map[string]bool) bool {
	if iface == nil {
		return true
	}
	if tp, ok := concrete.(*TypeParam_); ok {
		for _, bound := range tp.Bounds {
			if bound != nil && sameNominalIdentity(bound.Origin, bound.Name, iface.Origin, iface.Name) {
				return true
			}
		}
		return false
	}
	if concreteIface, ok := concrete.(*InterfaceType); ok {
		return sameNominalIdentity(concreteIface.Origin, concreteIface.Name, iface.Origin, iface.Name)
	}
	if iface.Name == "Struct" {
		return isStructShaped(concrete)
	}
	concreteName := interfaceImplName(concrete)
	if concreteName == "" || !interfaceImplemented(concreteName, iface.Name, iface.Origin, impls) {
		return false
	}
	key := concrete.String() + "|" + iface.Name
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[key] {
		return true
	}
	seen[key] = true
	defer delete(seen, key)

	info := lookupImplTypeArgs(concreteName, iface.Name, implTypeArgs)
	if info != nil && !implReceiverMatchesSeen(info, concrete, impls, implTypeArgs, seen) {
		return false
	}
	return true
}

// interfaceImplName returns the lookup key for impl-table queries.
func interfaceImplName(t Type) string {
	switch ty := t.(type) {
	case *PrimitiveType:
		return ty.Name_
	case *StructType:
		return ty.Name
	case *EnumType:
		return ty.Name
	case *DistinctType:
		return ty.Name
	case *ListType:
		return "List"
	case *MapType:
		return "Map"
	}
	return ""
}

// interfaceImplemented reports whether `concreteName` implements the interface
// `ifaceName` DECLARED BY `ifaceOrigin`.
//
// The origin is what makes this a nominal question rather than a spelling one.
// Two sibling files may each declare `interface Renderer`, and an impl written
// against one of them does not satisfy a bound naming the other — measured, it
// used to, and the program then ran and dispatched into the wrong interface's
// method. See interface_identity.go, including why an unknown origin on either
// side accepts.
func interfaceImplemented(concreteName, ifaceName, ifaceOrigin string, impls []ImplTables) bool {
	for _, table := range impls {
		if table.Impls == nil {
			continue
		}
		ifaces, ok := table.Impls[concreteName]
		if !ok || !ifaces[ifaceName] {
			continue
		}
		if sameNominalIdentity(ifaceOrigin, ifaceName, table.IfaceOrigins[concreteName][ifaceName], ifaceName) {
			return true
		}
	}
	return false
}

// lookupImplTypeArgs consults the ImplTypeArgs tables in the order supplied by
// the caller (checker.implTypeArgsContext threads FileAnalysis.ImplTypeArgs
// first, then fa.ProjectImpls.ImplTypeArgs — the project-level union built by
// buildProjectImplIndex over every reachable FA). The first hit wins, so
// file-local user impls shadow a same-named project-wide entry — matching how
// `interfaceImplemented` ranges its `impls` slice.
func lookupImplTypeArgs(typeName, ifaceName string, tables []map[string]map[string]*ImplTypeArgs) *ImplTypeArgs {
	for _, table := range tables {
		if table == nil {
			continue
		}
		if byIface, ok := table[typeName]; ok {
			if info, ok := byIface[ifaceName]; ok {
				return info
			}
		}
	}
	return nil
}

func lookupImplTypeArgSets(typeName, ifaceName string, tables []map[string]map[string][]*ImplTypeArgs) []*ImplTypeArgs {
	var out []*ImplTypeArgs
	for _, table := range tables {
		if table == nil {
			continue
		}
		if byIface, ok := table[typeName]; ok {
			out = append(out, byIface[ifaceName]...)
		}
	}
	return out
}

// concreteTypeArgs returns the type-argument list of a concrete type, in the
// order the ImplTypeArgs template's TypeParamDefs were recorded against (the
// impl type's formal params): List<T> → [T], Map<K, V> → [K, V], and a generic
// struct/enum/distinct → its TypeArgs. Non-generic types (scalars, String,
// Range, a bare distinct) return nil — substituteTypeParamDefs then returns the
// template unchanged, which is correct because those templates carry no formal
// params to substitute.
func concreteTypeArgs(t Type) []Type {
	switch ty := t.(type) {
	case *ListType:
		return []Type{ty.Elem}
	case *MapType:
		return []Type{ty.Key, ty.Val}
	case *StructType:
		return ty.TypeArgs
	case *EnumType:
		return ty.TypeArgs
	case *DistinctType:
		return ty.TypeArgs
	}
	return nil
}

// substituteTypeParamDefs applies the substitution typeParamDefs[i] -> args[i]
// to template, matching by *TypeParam_ pointer identity. If args is empty
// (use site has no concrete type args), the template is returned unchanged so
// any TypeParam_ leaks are visible to the caller (which will typically fail
// to unify — a clearer signal than a wrong answer).
func substituteTypeParamDefs(typeParamDefs []*TypeParam_, args []Type, template Type) Type {
	if len(typeParamDefs) == 0 || len(args) == 0 {
		return template
	}
	n := len(typeParamDefs)
	if len(args) < n {
		n = len(args)
	}
	subs := make(map[*TypeParam_]Type, n)
	for i := 0; i < n; i++ {
		subs[typeParamDefs[i]] = args[i]
	}
	return Substitute(template, subs)
}

// occursInIdent checks whether the exact TypeParam_ pointer `p` appears
// inside `t`. Pointer identity (not name) matters because multiple distinct
// TypeParam_ nodes can carry the same name across different scopes (e.g. a
// function's T and an enum's T); treating them as equal would wrongly refuse
// valid bindings. A true infinite type only arises when the bound TypeParam_
// literally points back at itself within the structure.
func occursInIdent(p *TypeParam_, t Type) bool {
	if t == nil || p == nil {
		return false
	}
	switch t := t.(type) {
	case *TypeParam_:
		return t == p
	case *ListType:
		return occursInIdent(p, t.Elem)
	case *MapType:
		return occursInIdent(p, t.Key) || occursInIdent(p, t.Val)
	case *FuncType:
		for _, pp := range t.Params {
			if occursInIdent(p, pp) {
				return true
			}
		}
		return occursInIdent(p, t.Return)
	case *TupleType:
		for _, e := range t.Elems {
			if occursInIdent(p, e) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, a := range t.TypeArgs {
			if occursInIdent(p, a) {
				return true
			}
		}
		return false
	case *StructType:
		for _, a := range t.TypeArgs {
			if occursInIdent(p, a) {
				return true
			}
		}
		return false
	case *InterfaceType:
		for _, a := range t.TypeArgs {
			if occursInIdent(p, a) {
				return true
			}
		}
		return false
	case *PartialType:
		return occursInIdent(p, t.Inner)
	default:
		return false
	}
}

// resolveTV follows the Resolved chain of TypeVars to their terminal type.
func resolveTV(t Type) Type {
	for {
		tv, ok := t.(*TypeVar)
		if !ok || tv.Resolved == nil {
			return t
		}
		t = tv.Resolved
	}
}

// Substitute replaces TypeParam_ nodes in a type with their bindings from subs.
// Subs is keyed by *TypeParam_ pointer identity — not by name — because two
// distinct generic scopes (e.g. Maybe<T> and Result<T, E>) can each have a
// type parameter named "T" without meaning the same thing. Keying by pointer
// keeps each scope's substitution self-contained so nested instantiations
// don't wrongly replace an inner scope's T with the outer scope's binding.
// TypeParam_ bindings are followed transitively (with cycle detection) so
// that chains like T_a → T_b → Int fully resolve to Int at the TypeParam_
// leaf; structural containers aren't re-walked, only the leaf resolution
// follows the chain.
// Returns a new type; the original is not mutated.
func Substitute(t Type, subs map[*TypeParam_]Type) Type {
	if t == nil || len(subs) == 0 {
		return t
	}
	return substituteWith(t, subs, nil)
}

func substituteWith(t Type, subs map[*TypeParam_]Type, visiting map[*TypeParam_]bool) Type {
	return substituteWithNominals(t, subs, visiting, nil)
}

// substituteWithNominals is the cycle-safe core of substitution. The
// `nominals` set tracks Enum/Struct pointers we've entered along the
// current walk; recursive types (`enum Json { Arr List<Json>
// | Obj Map<String, Json> | ... }`) cycle back through their own
// EnumType pointer via List/Map carriers, and without this guard the
// recursive descent through variant DataTypes blows the Go stack.
// When a nominal is re-entered the function returns it unchanged —
// substitution into a recursive position can't change the pointer
// identity (the inner reference is literally the same node we came
// from), so returning t preserves correctness.
//
// Known limitation — generic-recursive corner: substituting a generic
// recursive nominal like `Tree<T>` with `T → Int` returns a freshly-
// built `Tree<Int>` at the outer entry (TypeArgs are walked and
// re-substituted), but the inner self-reference reached via a carrier
// (e.g. inside `List<Tree<T>>`) hits the cycle-break and returns the
// original `Tree<T>` pointer unchanged — its TypeArgs are stale
// (still hold the un-substituted `T`). A "fully-substitute every nested
// generic instantiation" pass would need to additionally rewrite the
// inner reference's TypeArgs. In practice this hasn't surfaced because the
// outer entry's substituted TypeArgs are what callers observe; the
// stale inner pointer is only reachable by destructuring the variant
// payload, which currently re-runs substitution at the use site.
func substituteWithNominals(t Type, subs map[*TypeParam_]Type, visiting map[*TypeParam_]bool, nominals map[any]bool) Type {
	if t == nil {
		return t
	}
	switch t := t.(type) {
	case *TypeParam_:
		resolved, ok := subs[t]
		if !ok {
			return t
		}
		// Follow bindings transitively so chains like T_a → T_b → Int, or
		// T_a → StructX<T_b> where T_b → Int, fully resolve. Guard against
		// cycles (direct or indirect) by marking each TypeParam_ we've
		// entered along the resolution path.
		if visiting == nil {
			visiting = map[*TypeParam_]bool{}
		}
		if visiting[t] {
			return resolved
		}
		visiting[t] = true
		defer delete(visiting, t)
		return substituteWithNominals(resolved, subs, visiting, nominals)
	case *ListType:
		elem := substituteWithNominals(t.Elem, subs, visiting, nominals)
		if elem == t.Elem {
			return t
		}
		return &ListType{Elem: elem}
	case *MapType:
		key := substituteWithNominals(t.Key, subs, visiting, nominals)
		val := substituteWithNominals(t.Val, subs, visiting, nominals)
		if key == t.Key && val == t.Val {
			return t
		}
		return &MapType{Key: key, Val: val}
	case *PartialType:
		inner := substituteWithNominals(t.Inner, subs, visiting, nominals)
		if inner == t.Inner {
			return t
		}
		return &PartialType{Inner: inner}
	case *FuncType:
		params := make([]Type, len(t.Params))
		changed := false
		for i, p := range t.Params {
			params[i] = substituteWithNominals(p, subs, visiting, nominals)
			if params[i] != p {
				changed = true
			}
		}
		ret := substituteWithNominals(t.Return, subs, visiting, nominals)
		if ret != t.Return {
			changed = true
		}
		if !changed {
			return t
		}
		return &FuncType{Params: params, Return: ret, DefaultCount: t.DefaultCount}
	case *TupleType:
		elems := make([]Type, len(t.Elems))
		changed := false
		for i, e := range t.Elems {
			elems[i] = substituteWithNominals(e, subs, visiting, nominals)
			if elems[i] != e {
				changed = true
			}
		}
		if !changed {
			return t
		}
		return &TupleType{Elems: elems}
	case *EnumType:
		// Cycle break: if we've already entered THIS enum pointer during
		// the current walk, return it unchanged. The recursive position
		// can only refer back to the same pointer, and any substitution
		// of type args applicable here will have been performed at the
		// outer entry — descending again would just repeat the work
		// forever for recursive carriers like List<Json>.
		if nominals != nil && nominals[t] {
			return t
		}
		if nominals == nil {
			nominals = map[any]bool{}
		}
		nominals[t] = true
		defer delete(nominals, t)
		args := make([]Type, len(t.TypeArgs))
		changed := false
		for i, a := range t.TypeArgs {
			args[i] = substituteWithNominals(a, subs, visiting, nominals)
			if args[i] != a {
				changed = true
			}
		}
		// Variants speak the enum's own type params; where TypeArgs already
		// bind them, they are not free for an outer substitution to touch.
		inner := maskBoundParams(subs, t.TypeParamDefs, t.TypeArgs)
		variants := make([]VariantDef, len(t.Variants))
		for i, v := range t.Variants {
			dt := substituteWithNominals(v.DataType, inner, visiting, nominals)
			fields := substituteFieldsWithNominals(v.Fields, inner, visiting, nominals)
			emb := substituteWithNominals(v.Embedded, inner, visiting, nominals)
			if dt != v.DataType || emb != v.Embedded || !fieldsSamePointers(fields, v.Fields) {
				changed = true
			}
			variants[i] = VariantDef{Name: v.Name, Kind: v.Kind, DataType: dt, Fields: fields, Embedded: emb}
		}
		if !changed {
			return t
		}
		return &EnumType{
			Origin:        t.Origin,
			Name:          t.Name,
			TypeArgs:      args,
			Variants:      variants,
			TypeParams:    t.TypeParams,
			TypeParamDefs: t.TypeParamDefs,
		}
	case *StructType:
		// Cycle break — same rationale as EnumType above; recursive
		// structs (a struct field referencing the struct via List<Self>,
		// say) would otherwise recurse forever.
		if nominals != nil && nominals[t] {
			return t
		}
		if nominals == nil {
			nominals = map[any]bool{}
		}
		nominals[t] = true
		defer delete(nominals, t)
		args := make([]Type, len(t.TypeArgs))
		changed := false
		for i, a := range t.TypeArgs {
			args[i] = substituteWithNominals(a, subs, visiting, nominals)
			if args[i] != a {
				changed = true
			}
		}
		// Same capture rule as EnumType above: fields are written in terms of
		// the struct's own params, and TypeArgs already bind them.
		inner := maskBoundParams(subs, t.TypeParamDefs, t.TypeArgs)
		fields := substituteFieldsWithNominals(t.Fields, inner, visiting, nominals)
		if !fieldsSamePointers(fields, t.Fields) {
			changed = true
		}
		if !changed {
			return t
		}
		return &StructType{
			Origin:        t.Origin,
			Name:          t.Name,
			TypeArgs:      args,
			Fields:        fields,
			TypeParams:    t.TypeParams,
			TypeParamDefs: t.TypeParamDefs,
		}
	case *AnonStructType:
		fields := substituteFieldsWithNominals(t.Fields, subs, visiting, nominals)
		if fieldsSamePointers(fields, t.Fields) {
			return t
		}
		return &AnonStructType{Fields: fields}
	case *InterfaceType:
		if len(t.TypeArgs) == 0 {
			return t
		}
		args := make([]Type, len(t.TypeArgs))
		changed := false
		for i, a := range t.TypeArgs {
			args[i] = substituteWithNominals(a, subs, visiting, nominals)
			if args[i] != a {
				changed = true
			}
		}
		if !changed {
			return t
		}
		return &InterfaceType{
			Origin:        t.Origin,
			Name:          t.Name,
			Methods:       t.Methods,
			Fields:        t.Fields,
			TypeParams:    t.TypeParams,
			TypeParamDefs: t.TypeParamDefs,
			TypeArgs:      args,
			SelfParam:     t.SelfParam,
		}
	case *DistinctType:
		// Generic opaque externs (`host type Task<T>`,
		// `host type Channel<T>`) carry TypeArgs at use sites. Recurse
		// into them so substitution at call sites (`await<T>` solving
		// T=Int from `Task<T>` vs `Task<Int>`) flows through the
		// wrapper. Non-generic distinct types leave TypeArgs nil and
		// the loop is a no-op.
		if len(t.TypeArgs) == 0 {
			return t
		}
		args := make([]Type, len(t.TypeArgs))
		changed := false
		for i, a := range t.TypeArgs {
			args[i] = substituteWithNominals(a, subs, visiting, nominals)
			if args[i] != a {
				changed = true
			}
		}
		if !changed {
			return t
		}
		return &DistinctType{
			Origin:           t.Origin,
			Name:             t.Name,
			Inner:            t.Inner,
			Opaque:           t.Opaque,
			OwningSourceFile: t.OwningSourceFile,
			TypeParams:       t.TypeParams,
			TypeParamDefs:    t.TypeParamDefs,
			TypeArgs:         args,
		}
	default:
		return t
	}
}

// maskBoundParams drops from subs any of the nominal's own type params that
// its TypeArgs already bind, and returns the reduced map for use when
// descending into fields or variants.
//
// A nominal node carrying both TypeParamDefs and TypeArgs means the
// instantiation defs↦args; its fields stay written in terms of the defs and
// are read through the args at the use site. Those defs are bound, so an
// outer substitution that happens to key on the same *TypeParam_ pointer must
// not reach them — otherwise `Chan<Int>` nested inside a value being
// substituted comes back as `Chan<Int>{sender: Channel<Whatever>}`, its args
// and its fields disagreeing about the same parameter. That is variable
// capture, and it is reachable: substituting a param whose binding is a
// nominal re-walks that whole nominal with the same subs.
//
// A def that still appears in the args is not bound (the partially-applied
// form a generic recursive type takes when it references itself), so it stays
// substitutable.
func maskBoundParams(subs map[*TypeParam_]Type, defs []*TypeParam_, args []Type) map[*TypeParam_]Type {
	if len(subs) == 0 || len(defs) == 0 || len(defs) != len(args) {
		return subs
	}
	var reduced map[*TypeParam_]Type
	for _, d := range defs {
		if _, shadowed := subs[d]; !shadowed {
			continue
		}
		if anyMentionsParam(args, d) {
			continue
		}
		if reduced == nil {
			reduced = make(map[*TypeParam_]Type, len(subs))
			for k, v := range subs {
				reduced[k] = v
			}
		}
		delete(reduced, d)
	}
	if reduced == nil {
		return subs
	}
	return reduced
}

func anyMentionsParam(args []Type, p *TypeParam_) bool {
	for _, a := range args {
		if mentionsParam(a, p, nil) {
			return true
		}
	}
	return false
}

// mentionsParam reports whether p occurs anywhere in t. The `seen` set guards
// the same recursive-nominal cycles substitution itself has to guard.
func mentionsParam(t Type, p *TypeParam_, seen map[any]bool) bool {
	switch t := t.(type) {
	case nil:
		return false
	case *TypeParam_:
		return t == p
	case *TypeVar:
		if t.Resolved == nil {
			return false
		}
		return mentionsParam(t.Resolved, p, seen)
	case *ListType:
		return mentionsParam(t.Elem, p, seen)
	case *MapType:
		return mentionsParam(t.Key, p, seen) || mentionsParam(t.Val, p, seen)
	case *PartialType:
		return mentionsParam(t.Inner, p, seen)
	case *FuncType:
		for _, param := range t.Params {
			if mentionsParam(param, p, seen) {
				return true
			}
		}
		return mentionsParam(t.Return, p, seen)
	case *TupleType:
		for _, e := range t.Elems {
			if mentionsParam(e, p, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		if seen == nil {
			seen = map[any]bool{}
		} else if seen[t] {
			return false
		}
		seen[t] = true
		defer delete(seen, t)
		for _, a := range t.TypeArgs {
			if mentionsParam(a, p, seen) {
				return true
			}
		}
		return false
	case *StructType:
		if seen == nil {
			seen = map[any]bool{}
		} else if seen[t] {
			return false
		}
		seen[t] = true
		defer delete(seen, t)
		for _, a := range t.TypeArgs {
			if mentionsParam(a, p, seen) {
				return true
			}
		}
		return false
	case *InterfaceType:
		for _, a := range t.TypeArgs {
			if mentionsParam(a, p, seen) {
				return true
			}
		}
		return false
	case *DistinctType:
		for _, a := range t.TypeArgs {
			if mentionsParam(a, p, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// fieldsSamePointers reports whether two field slices have the same types by
// pointer identity — used to skip allocating a fresh slice when substitution
// produced no change.
func fieldsSamePointers(a, b []FieldDef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type {
			return false
		}
	}
	return true
}

func substituteFieldsWithNominals(fields []FieldDef, subs map[*TypeParam_]Type, visiting map[*TypeParam_]bool, nominals map[any]bool) []FieldDef {
	if len(fields) == 0 {
		return fields
	}
	result := make([]FieldDef, len(fields))
	for i, f := range fields {
		result[i] = f
		result[i].Type = substituteWithNominals(f.Type, subs, visiting, nominals)
	}
	return result
}

// isBoolVariantVsBool reports whether a is the primitive True/False and b is
// the Bool enum. Used as a one-off subtyping check in unifyFull: Nomi treats
// True and False as members of the Bool enum (`enum Bool { embeds True | embeds False }`),
// so a value of a variant primitive conforms to the enum type.
func isBoolVariantVsBool(a, b Type) bool {
	if a != TypeTrue && a != TypeFalse {
		return false
	}
	if bt, ok := b.(*EnumType); ok && bt.Name == "Bool" {
		return true
	}
	return false
}

func isBoolVariantPair(a, b Type) bool {
	return (a == TypeTrue || a == TypeFalse) && (b == TypeTrue || b == TypeFalse)
}

// isEmbeddedTypeOf reports whether t is the data type of one of et's `embeds`
// variants. Used by unifyFull to admit subtype coercion from an embedded type
// to its containing enum (e.g. UserId is an Identifier; Circle is a Shape).
// Only embedded variants qualify — positional/struct/bare variants always
// require explicit qualified construction at the use site.
//
// For embedded variants the variant's Name equals the embedded type's name
// (by construction), so we compare against `v.Name` rather than reading the
// payload type. This decouples the subtype rule from the variant's payload
// shape — wrapping-distinct embeds carry the inner type as their payload
// (e.g. (String) -> Identifier) but still admit `UserId ≤ Identifier`.
func isEmbeddedTypeOf(t Type, et *EnumType) bool {
	name := embeddableTypeName(t)
	if name == "" {
		return false
	}
	for _, v := range et.Variants {
		if v.Kind != VariantEmbedded {
			continue
		}
		if v.Name == name {
			return true
		}
	}
	return false
}

// embeddableTypeName returns the name of a type that can appear on the
// right-hand side of `embeds` (struct or distinct), or "" for anything else.
func embeddableTypeName(t Type) string {
	switch tt := t.(type) {
	case *StructType:
		return tt.Name
	case *DistinctType:
		return tt.Name
	}
	return ""
}

// projectThroughBound solves an interface's type arguments from a type
// parameter bounded by that interface: `Iter<T>` against `I` where
// `I: Iter<Elem>` binds T to Elem. The parameter is the caller's, so its
// bound is the one fact about I's elements the call can use; without it the
// callee's T stayed unsolved and a lambda over the elements saw it rigid.
// Errors are ignored: the binding of the parameter itself, which follows,
// decides whether the unification succeeds.
func projectThroughBound(iface *InterfaceType, tp *TypeParam_, subs map[*TypeParam_]Type, impls []ImplTables, implTypeArgs []map[string]map[string]*ImplTypeArgs) {
	if len(iface.TypeArgs) == 0 {
		return
	}
	for _, bound := range tp.Bounds {
		if bound == nil || len(bound.TypeArgs) != len(iface.TypeArgs) ||
			!sameNominalIdentity(bound.Origin, bound.Name, iface.Origin, iface.Name) {
			continue
		}
		for i := range iface.TypeArgs {
			_ = unifyFull(iface.TypeArgs[i], bound.TypeArgs[i], subs, impls, implTypeArgs)
		}
		return
	}
}
