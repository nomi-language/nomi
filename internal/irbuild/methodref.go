package irbuild

import "github.com/nomi-language/nomi/internal/ast"

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
// `assert Codepoint.to_int(a) == 65` lowers with no refusal, from the
// function the call's record names (recordedCallPlan).
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
// The function is the one the checker resolved the reference to, among
// several declarations of one spelling too (`String.contains`). One shape
// stays refused and is named: a std function with defaulted parameters,
// whose plain function value would not carry the defaults.

// stdMethodRefTarget selects the std function the reference at names, as
// the checker resolved it (stdFuncAtRef), and builds nothing.
func (g *gen) stdMethodRefTarget(at *ast.FieldAccess) (*stdFunc, string, string, bool) {
	f := g.stdFuncByID(g.stdFuncAtRef(at))
	if f == nil {
		return nil, "", "", false
	}
	// An owner the checker resolved to this module's own declaration
	// resolves against this module's tables. Declining rather than refusing
	// keeps that site's answer the one its declaration deserves.
	if g.ownOwner(at) {
		return nil, "", "", false
	}
	switch {
	case !f.lowerable():
		// The callee is the obstacle and the reference is moot, so its own
		// reason is the honest report — see the file comment.
		return nil, f.why, f.key, true
	// callDefaultFill, not `f.defaultFill`: a sibling snapshot freezes the
	// field before the lowering pass sets it, so reading it directly would miss
	// a trailing default whose accessor IS built. See stdFunc.canon.
	case callDefaultFill(f, len(f.params)-1):
		return nil, "function reference with a defaulted parameter", f.key, true
	default:
		return f, "", "", true
	}
}
