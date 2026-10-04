package irbuild

// A stdlib METHOD named without being called — the `Codepoint.to_int` in
// `Iter.map(Codepoint.to_int)`.
//
// # Why this is not the variant resolver's job
//
// bareVariant sends `Type.member` to lookupVariant, which is a VARIANT
// resolver, and the two arms it fails on carry two unrelated key names:
//
//   - `type-qualified member` when the qualifier resolves to a *typeDef that is
//     not an enum, and
//   - `type-qualified reference` when it resolves to no type this gen declares.
//
// Which of the two a method reference would get depends only on whether the
// builder has a def for the qualifier. In
// 04-scalars-and-text/strings_test.nomi, lines 25, 29 and 36 are
// `Iter.map(Byte.to_int)`, `Iter.map(Byte.to_int)`, `Iter.map(Codepoint.to_int)`;
// `Codepoint` has an opaqueSpecs anchor and `Byte` does not, and that would be
// the whole difference. So a stdlib method named without a call is resolved
// here, before the variant resolver sees it.
//
// A CALL of the same method is a different path and lowers:
// `assert Codepoint.to_int(a) == 65` generates with no refusal, and
// TestStdMethodCallStillLowers guards it.
//
// # Why a NON-LOWERABLE callee keeps its own reason instead
//
// `Byte.to_int` is in the index and is NOT lowerable: its `why` is `stdlib
// function outside the scalar subset`, because `bytes.Byte` has no rt anchor.
// Reporting a reference refusal there would claim the reference is the
// obstacle, and it is not; the callee is. So an unlowerable candidate is
// refused with ITS OWN reason at the position somebody wrote, which is
// stdOnceRef's rule for an unsettled `once` and stdlibCall's for a call.
//
// # What this deliberately does NOT claim
//
// `type-qualified member` covers four shapes and only this one is a method
// reference. The other three keep their key and must: `User.to_json(user)` and
// `Day.add(a, b)` are CALLS whose local impl did not lower (a cascade the call
// path owns), and `Day.Hours(48)` is a namespaced distinct type's CONSTRUCTOR,
// not a member at all. The predicate below is therefore the stdlib index and
// nothing wider — a lookup that answers for exactly the spellings std really
// declares. TestQualifiedMemberRefusalsKeptTheirOwnKeys is the negative half.

// # What the reference produces
//
// funcref.go hands out a free `fn` as its Go NAME, because funcDecl writes
// exactly the Go type funcKind renders. A stdlib function has no such
// guarantee: it is either a generated Nomi body in another package or an rt
// extern whose Go signature takes the frame only when hostFn.takesFrame says
// so. So the value here is a THUNK — one Go func literal closing over nothing,
// forwarding to whatever stdlibInvoke would have called. It costs a closure per
// reference SITE, which is the price of one shape instead of four, and Go's
// type checker rejects any mismatch at build time rather than silently.
//
// Two shapes stay refused and are named, for the same reasons funcref.go's
// walls are: a std function reached through an arity-reduced wrapper carries
// defaults a Go func value cannot, and a spelling with SEVERAL declarations
// behind it (`NaiveDateTime.add` is eleven `impl Add<X, NaiveDateTime>` blocks)
// selects one by ARGUMENT KINDS at a call and selects nothing at a reference.

// stdMethodRefTarget selects without emitting a thunk or mutating imports.
func (g *gen) stdMethodRefTarget(owner, member string) (*stdFunc, string, string, bool) {
	if g.std == nil {
		return nil, "", "", false
	}
	fs := g.std.byType[owner+"."+member]
	if len(fs) == 0 {
		return nil, "", "", false
	}
	// A local declaration of the same NAME owns the qualifier and resolves
	// against this module's tables. Declining rather than refusing
	// keeps that site's answer the one the local declaration deserves — the same
	// shadow rule bareOwnerCall applies, for the same reason: a type identity
	// matched by name is a silent wrong answer.
	if _, localType := g.types[owner]; localType {
		return nil, "", "", false
	}
	if _, localIface := g.ifaces[owner]; localIface {
		return nil, "", "", false
	}
	// A reference selects nothing, so an overload set cannot be narrowed here.
	// If ANY candidate lowers, a function with this spelling exists and the
	// reference is the sole obstacle. If none does, the callee is the obstacle
	// and the reference is moot, so the first candidate's own reason is the
	// honest report — see the file comment.
	var only *stdFunc
	lowerable := 0
	for _, f := range fs {
		if f.lowerable() {
			lowerable++
			only = f
		}
	}
	switch {
	case lowerable == 0:
		return nil, fs[0].why, fs[0].key, true
	case lowerable > 1:
		return nil, "ambiguous stdlib method reference", owner + "." + member, true
	// callArityMin, not `only.arityMin`: a sibling snapshot freezes the field
	// before the lowering pass lowers it, so reading it directly would refuse a
	// reference to a function whose defaults ARE reachable. See stdFunc.canon.
	case callArityMin(only) < len(only.params):
		return nil, "function reference with a defaulted parameter", only.key, true
	default:
		return only, "", "", true
	}
}
