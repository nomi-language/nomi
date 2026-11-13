package analysis

import "github.com/nomi-language/nomi/internal/ast"

// Type arguments from the arguments first (spec §5 *Type arguments from
// the arguments*).
//
// A generic call's type arguments come from its arguments. The type the
// call's result is expected to have only fills the type parameters the
// arguments leave open, and the result then converts to the expected type
// wherever a value converts (an Int into Display, an Iter<Int> into an
// Iter<Display>). So `x: Iter<Display> = [1, 2] |> Iter.filter(fn n -> n > 1)`
// is filter at Int, whose lambda compares Ints, and its Iter<Int> result is
// relabelled as an Iter<Display>.
//
// The checker still solves what the expected type would give (the seed,
// computed as before from the call's result type), but holds it back.
// checkGenericCall and checkPipe hand each argument, before checking it,
// only the seed's solutions for the type parameters that argument cannot
// type itself and the earlier arguments left open (seedWantedBy):
//
//   - a dot-leading variant, an empty collection literal, a constructor or
//     generic function named as a value, `None` and a struct literal with
//     no name take every parameter their slot names;
//   - a lambda takes the parameters its slot's function type names in its
//     parameter list, since an unannotated parameter has no other source,
//     and those in its result only when its body ends in a value that
//     needs an expected type (`fn x -> .Red`);
//   - a bracket or map literal with items takes nothing in a slot that
//     names its collection (`Iter<T>`): the items type it. In a slot that
//     is a bare type parameter (`Some(T)`), the expected type also says
//     which collection it is, so it takes the seed;
//   - an `if` or `case` takes the seed in a bare type parameter's slot,
//     where its branches join as the expected type.
//
// The rule covers every type parameter the result holds where a value
// converts: at the top (Iter.loop's T), as an Iter's element (Iter.filter's
// T), and at any covariant or contravariant position below (a Maybe's or a
// List's element, a Result's error, a tuple's item, a function type's
// parameter or result, a declared generic type's parameter that is not
// invariant), since the result then converts element by element (spec §5
// *Covariant conversion*): `Ok(Memory.new())` where a Result<Store, Error>
// is expected builds a Result<Memory, Error>, which converts. A type
// parameter the result holds at an invariant position (a declared generic
// type's invariant parameter, a std struct's or host type's argument, an
// interface's other than Iter's) still takes the expected type ahead of the
// arguments, since a type argument there never converts (exactParams). A
// flat variant call is seeded whole, so a mismatched element is reported
// where it is written.

// expectedSeed is what the expected type solves of a generic call's type
// parameters, held back until an argument needs it.
type expectedSeed map[*TypeParam_]Type

// fill copies into subs the seed's solution for each of tps that subs
// leaves open.
func (s expectedSeed) fill(tps map[*TypeParam_]bool, subs map[*TypeParam_]Type) {
	for tp := range tps {
		if _, solved := subs[tp]; solved {
			continue
		}
		if t, ok := s[tp]; ok {
			subs[tp] = t
		}
	}
}

// fillAll copies every solution subs leaves open.
func (s expectedSeed) fillAll(subs map[*TypeParam_]Type) {
	for tp, t := range s {
		if _, solved := subs[tp]; !solved {
			subs[tp] = t
		}
	}
}

// seedFromTrial is the seed a tentative unification into trial solved,
// beyond what subs already held.
func seedFromTrial(trial, subs map[*TypeParam_]Type) expectedSeed {
	seed := expectedSeed{}
	for tp, t := range trial {
		if _, solved := subs[tp]; !solved {
			seed[tp] = t
		}
	}
	return seed
}

// fillExact fills from the seed the type parameters resultTy names only
// where no conversion reaches (exactParams).
func (s expectedSeed) fillExact(resultTy Type, subs map[*TypeParam_]Type) {
	if len(s) == 0 {
		return
	}
	exact := map[*TypeParam_]bool{}
	exactParams(resultTy, false, exact)
	s.fill(exact, subs)
}

// exactParams adds to out the type parameters t names at an invariant
// position: a declared generic type's invariant parameter, a std struct's
// or host type's argument, an interface's other than Iter's, or a
// `Partial<T>`'s. There a type argument never converts (spec §5 *Covariant
// conversion*), so one the arguments chose could leave a result the
// expected type does not take. Every other position converts as a value
// does, and is left to the arguments.
func exactParams(t Type, exact bool, out map[*TypeParam_]bool) {
	switch tt := resolveTV(t).(type) {
	case *TypeParam_:
		if exact {
			out[tt] = true
		}
	case *ListType:
		exactParams(tt.Elem, exact, out)
	case *MapType:
		exactParams(tt.Key, exact, out)
		exactParams(tt.Val, exact, out)
	case *TupleType:
		for _, e := range tt.Elems {
			exactParams(e, exact, out)
		}
	case *AnonStructType:
		for _, f := range tt.Fields {
			exactParams(f.Type, exact, out)
		}
	case *FuncType:
		for _, p := range tt.Params {
			exactParams(p, exact, out)
		}
		exactParams(tt.Return, exact, out)
	case *PartialType:
		exactParams(tt.Inner, true, out)
	case *InterfaceType:
		for _, a := range tt.TypeArgs {
			exactParams(a, exact || !isStdIterType(tt), out)
		}
	case *StructType:
		for i, a := range tt.TypeArgs {
			exactParams(a, exact || argVariance(tt, i) == VarianceInvariant, out)
		}
	case *EnumType:
		for i, a := range tt.TypeArgs {
			exactParams(a, exact || argVariance(tt, i) == VarianceInvariant, out)
		}
	case *DistinctType:
		for i, a := range tt.TypeArgs {
			exactParams(a, exact || argVariance(tt, i) == VarianceInvariant, out)
		}
	}
}

// seedWantedBy is the set of type parameters in param, the slot arg fills,
// that arg takes from the expected type's seed because it cannot type them
// itself. Empty when the argument's own type solves them.
func (c *checker) seedWantedBy(arg ast.Node, param Type, expected Type) map[*TypeParam_]bool {
	all := func() map[*TypeParam_]bool {
		out := map[*TypeParam_]bool{}
		collectTypeParamPtrs(param, out)
		return out
	}
	a := ungroupExpr(unwrapNamedArg(arg))
	_, bare := resolveTV(param).(*TypeParam_)
	if byteLiteralsTakeExpected([]ast.Node{a}, expected) {
		return all()
	}
	switch v := a.(type) {
	case *ast.Lambda:
		ft, ok := resolveTV(param).(*FuncType)
		if !ok {
			if needsExpectedType(v) || lambdaTailNeedsExpectedType(v) {
				return all()
			}
			return nil
		}
		out := map[*TypeParam_]bool{}
		if needsExpectedType(v) {
			for _, p := range ft.Params {
				collectTypeParamPtrs(p, out)
			}
		}
		if lambdaTailNeedsExpectedType(v) {
			collectTypeParamPtrs(ft.Return, out)
		}
		return out
	case *ast.ListLit:
		if v.TypeName == nil && len(v.Items) > 0 && !bare && !callArgsNeedExpectedType(v.Items) {
			return nil
		}
	case *ast.MapLit:
		if v.TypeName == nil && len(v.Entries) > 0 && !bare && !needsExpectedType(v) {
			return nil
		}
	case *ast.If, *ast.Case:
		if bare || needsExpectedType(v) {
			return all()
		}
		return nil
	}
	if needsExpectedType(a) || c.argTakesExpectedType(a) {
		return all()
	}
	return nil
}

// lambdaTailNeedsExpectedType reports whether a lambda's body ends in a
// value only an expected type can type, as `.Red` in `fn x -> .Red`.
func lambdaTailNeedsExpectedType(l *ast.Lambda) bool {
	return blockTailNeedsExpectedType(l.Body)
}
