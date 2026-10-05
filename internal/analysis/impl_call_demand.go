package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A TYPE-qualified call to an interface impl method records the same impl
// demand the INTERFACE-qualified spelling of that call records.
//
// `Comparable.compare(#["a"], #["b"])` and `Vector.compare(#["a"], #["b"])`
// invoke one impl body. The first records (Comparable, Vector<String>) —
// checkGenericCall's interface-object arm reads the interface's SelfParam
// binding out of `subs` and hands it to recordBoundConformance, whose
// recursion into type arguments then supplies (Comparable, String). The
// second goes through the type-method symbol instead, where there is no
// SelfParam to read, so without this file it would record NOTHING: neither
// the element pair nor the container pair.
//
// The consequence would be a runtime fault, because `impl Comparable for
// Vector<T>`'s body dispatches `Comparable.compare(ha, hb)` at the bare
// `T` and dispatch registration is demand-driven off the manifest.
// `Vector.compare(#["a"], #["b"])` would die with "Comparable.compare: no
// implementation for type 'String'" while `Iter.sort(#["c", "a", "b"])`, an
// ordinary free generic whose `<T: Comparable>` bound is checked and
// recorded, answers.
//
// The receiver is taken from the ARGUMENTS, not from the impl's
// ImplTypeArgs.Receiver template. Both would name `Vector<T>`, but the
// template's `*TypeParam_` pointers come from a separate
// buildImplBlockTypeParams call in recordImplTypeArgsFromBlock, so they
// are not the pointers `subs` was solved over; substituting `subs` into
// the template would silently substitute nothing. The parameter types of
// the callee we actually resolved carry the right pointers by
// construction.
//
// Recorded as RecordingKindCallSite, which is an ORDINARY (strong)
// demand: a genuinely absent impl now reports through
// DetectMissingImpls at compile time rather than faulting at dispatch,
// which is what that detector is for. Ordering has no structural
// fallback, so there is nothing weaker for `compare` to mean — see
// RecordingKindOrderingOperator's comment for the same argument at the
// `<` operator.
//
// Not covered, deliberately: a method that declares `self` only in its
// RETURN type (`FromJson.from_json(json: Json): Result<self, E>`) has no
// receiver-typed parameter for this to read, so nothing is recorded and
// the pre-existing behaviour stands. Deriving self positions from the
// impl signature rather than from a parameter scan is a separate piece
// of work.
func (c *checker) recordTypeQualifiedImplDemand(n *ast.Call, fa *ast.FieldAccess, ft *FuncType, subs map[*TypeParam_]Type) {
	if c == nil || c.fa == nil || n == nil || fa == nil || fa.Field == nil || ft == nil {
		return
	}
	ownerBase := typeQualifiedOwnerBaseName(c, fa.Object)
	if ownerBase == "" {
		return
	}
	// Exactly one provider. Zero means the method is inherent, not an
	// interface contract method, so there is no conformance to demand;
	// two or more is already reported as an ambiguity at the
	// type-qualified field-access site (spec 13.3) and picking one here
	// would demand a conformance the program does not resolve.
	providers := c.interfacesDeclaringMethod(ownerBase, fa.Field.Name)
	if len(providers) != 1 {
		return
	}
	ifaceName := providers[0]
	recv := receiverArgTypeForOwner(ft, subs, ownerBase)
	if recv == nil {
		return
	}
	// A DECLARED interface bound on the callee's type parameters already
	// owns this conformance BOTH ways: checkGenericCall's `subs` /
	// `tp.Bounds` enforcement loop reports when the bound is unsatisfied
	// and calls recordBoundConformance when it is satisfied. So this
	// function has nothing to add, and adding anything DOUBLE-REPORTS
	// one fix.
	//
	// Measured, and the measurement is what located the bound. The case
	// is `derive Display for DisplayBox<T> where T: Display` plus
	// `DisplayBox.to_string(DisplayBox{value: Plain{...}})`, where
	// `Plain` has no Display impl:
	// 11-interfaces-and-impls/interface_defaults/interface_defaults_test.nomi
	// :: "derive where clauses reject unsatisfied receiver arguments"
	// asserts exactly one diagnostic and saw two. Producing both is the
	// anti-pattern derive_synthesis.go names in its own words.
	//
	// TWO WRONG GUESSES ARE RECORDED HERE, because the corpus refuted
	// each and the sequence is the finding:
	//
	//   1. Read `ft.WhereBounds`. The second diagnostic's text is
	//      recordWhereBoundDemands's format string, so that looked like
	//      the enforcing site. It is not: a `derive ... where` clause
	//      arrives as a BOUND on the receiver's type parameter and is
	//      enforced by the `subs` / `tp.Bounds` loop, which SHARES that
	//      format string with two other sites. A message text is not an
	//      identification of its producer when three producers print it.
	//   2. Record the CONTAINER pair and skip the recursion. Still two
	//      diagnostics, because recording the container pair at all runs
	//      recordConformanceRecording -> recordImplReceiverBoundConformances,
	//      which unifies the impl's receiver template against the
	//      concrete receiver and records each type parameter's declared
	//      bounds UNCONDITIONALLY — so the element pair arrives by a
	//      route this function never names.
	//
	// Hence: record NOTHING when a bound is declared. That makes this
	// function an exact no-op wherever the parent already had a route,
	// and the fix only where the parent had none — which is the case of
	// `impl Comparable for Vector<T>`,
	// dispatching Comparable on a `T` that nothing declares.
	if declaresInterfaceBound(ft, subs, ifaceName) {
		return
	}
	c.recordBoundConformance(recv, ifaceName, c.recPos(n.Line, n.Col), RecordingKindCallSite)
}

// typeQualifiedOwnerBaseName returns the base name of the TYPE a
// type-qualified call is qualified by, or "" when the call's object is
// not a type qualifier. `Vector.compare(...)` yields "Vector";
// `calendar.Date.to_string(...)` yields "Date", because the member
// tables this feeds — interfacesDeclaringMethod, ImplTypeArgs — are
// keyed by base name (see interface_identity.go on why identity lives
// on the per-impl side rather than on these).
func typeQualifiedOwnerBaseName(c *checker, object ast.Node) string {
	var name string
	switch o := object.(type) {
	case *ast.TypeIdent:
		name = o.Name
	case *ast.FieldAccess:
		name = c.namespaceQualifiedTypeName(o)
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// receiverArgTypeForOwner returns the solved type of the first parameter
// whose type names `ownerBase` — the receiver position of a
// type-qualified impl-method call — or nil when the callee has none or
// the solved type is still open.
func receiverArgTypeForOwner(ft *FuncType, subs map[*TypeParam_]Type, ownerBase string) Type {
	for _, param := range ft.Params {
		if param == nil {
			continue
		}
		solved := resolveTypeVar(Substitute(param, subs))
		if concreteTypeName(solved) != ownerBase {
			continue
		}
		if ContainsTypeParam(solved) {
			return nil
		}
		return solved
	}
	return nil
}

// declaresInterfaceBound reports whether the callee declares `ifaceName`
// as a bound on any type parameter this call solved.
//
// BOTH populations, because checkGenericCall enforces two and they are
// not the same list: the `subs`/`tp.Bounds` loop (which is where a
// `derive ... where` clause on the impl receiver lands) and
// recordWhereBoundDemands over `ft.WhereBounds` (a method-level
// `where`). Reading one and not the other is the mistake this function
// exists to have stopped making — see the guard's comment.
func declaresInterfaceBound(ft *FuncType, subs map[*TypeParam_]Type, ifaceName string) bool {
	for tp := range subs {
		if tp == nil {
			continue
		}
		if boundsName(tp.Bounds, ifaceName) {
			return true
		}
	}
	for _, wb := range ft.WhereBounds {
		if boundsName(wb.Bounds, ifaceName) {
			return true
		}
	}
	return false
}

func boundsName(bounds []*InterfaceType, ifaceName string) bool {
	for _, bound := range bounds {
		if bound != nil && bound.Name == ifaceName {
			return true
		}
	}
	return false
}
