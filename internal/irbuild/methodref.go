package irbuild

// A stdlib METHOD named without being called — the `Codepoint.to_int` in
// `Iter.map(Codepoint.to_int)`.
//
// # Why this is not the variant resolver's job
//
// `Type.member` is also how a variant is spelled, and a variant resolver's
// answer for a method reference would depend only on whether the builder has
// a def for the qualifier. In
// 04-scalars-and-text/strings_test.nomi, lines 25, 29 and 36 are
// `Iter.map(Byte.to_int)`, `Iter.map(Byte.to_int)`, `Iter.map(Codepoint.to_int)`;
// `Codepoint` has an opaqueSpecs anchor and `Byte` does not, and that would be
// the whole difference. So a stdlib method named without a call is resolved
// here, before the variant resolver sees it.
//
// A CALL of the same method is a different path and lowers:
// `assert Codepoint.to_int(a) == 65` lowers with no refusal
// (stdMethodCallPlan).
//
// # Why a NON-LOWERABLE callee keeps its own reason instead
//
// `Byte.to_int` is in the index and is NOT lowerable: its `why` is `stdlib
// function outside the scalar subset`, because `bytes.Byte` has no rt anchor.
// Reporting a reference refusal there would claim the reference is the
// obstacle, and it is not; the callee is. So an unlowerable candidate is
// refused with ITS OWN reason at the position somebody wrote, as a call of
// it is.
//
// # What this deliberately does NOT claim
//
// Only a method reference is resolved here. `User.to_json(user)` and
// `Day.add(a, b)` are CALLS, which the call path owns, and `Day.Hours(48)` is
// a namespaced distinct type's CONSTRUCTOR, not a member at all. The
// predicate below is therefore the stdlib index and nothing wider — a lookup
// that answers for exactly the spellings std really declares.

// # What the reference produces
//
// A stdlib function with a Nomi body is referenced as funcref.go references a
// module `fn`: an `ir.RefFunc` naming the body's symbol. A stdlib function that
// is an rt host has no `ir.Func` to name, so its value is an `ir.FuncValue`
// with no captures over a private body that forwards its parameters to the
// host (irHostForward, irstdmethodref.go).
//
// Two shapes stay refused and are named: a std function with defaulted
// parameters, whose plain function value would not carry the defaults, and a
// spelling with several declarations behind it (`NaiveDateTime.add` is eleven
// `impl Add<X, NaiveDateTime>` blocks), which selects one by argument kinds at
// a call and selects nothing at a reference.

// stdMethodRefTarget selects the function a reference names and builds
// nothing.
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
	// keeps that site's answer the one the local declaration deserves: a type
	// identity matched by name is a silent wrong answer.
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
	// callDefaultFill, not `only.defaultFill`: a sibling snapshot freezes the
	// field before the lowering pass sets it, so reading it directly would miss
	// a trailing default whose accessor IS built. See stdFunc.canon.
	case callDefaultFill(only, len(only.params)-1):
		return nil, "function reference with a defaulted parameter", only.key, true
	default:
		return only, "", "", true
	}
}
