package irbuild

import "strings"

// An INTERFACE-qualified call whose implementation lives in a SIBLING FILE:
// `Display.to_string(shapes.origin())` in one file, `impl Display for Point` in
// another.
//
// # One gap under three keys, and the keys are the reason it was invisible
//
// `implCall`'s interface routes all read `g.implsByIface`, which is the impl
// blocks of the file BEING LOWERED. A sibling's block is not in it, so the call
// fell out of the bottom of the chain — and WHICH refusal it landed on depended
// on where the QUALIFIER resolved, not on the gap:
//
//   - a STDLIB interface (`Display`, `Debug`) resolves nowhere in this package,
//     so `ifaceNamed` misses, every arm declines, and native.go's qualifiedCall
//     reports `qualified call`;
//   - `Display.to_string` specifically reaches `textQualifiedCall` first, which
//     hands a named receiver to `g.text` — a per-file `implsByIface["Display"]`
//     lookup — so it reports `rendering a non-scalar value` naming the TYPE;
//   - a USER interface declared in a sibling resolves to a MIRROR, so
//     `interfaceCall` runs, finds no local impl for the receiver, and reports
//     `call to an unlowered impl`.
//
// All three are one gap: a stdlib or user interface call whose impl lives in a
// sibling file. testdata/sibiface/main.nomi carries all three at once.
//
// # Why the receiver is found by matching the WHOLE argument list
//
// The qualifier names an INTERFACE, so it does not say which argument is the
// receiver, and for a stdlib interface this package has no declaration to read
// a self-position out of. foreignIfaceCall solved that inside one file by
// matching each candidate's own declared parameters against the arguments; this
// is the same rule one file boundary out. `fn tag(label: String, target: self)`
// therefore needs no special case: the String argument is not a named type and
// contributes no candidate, and the Point argument is.
//
// # Why this route cannot be ambiguous, where the TYPE-qualified one can
//
// siblingImplCall needs foreignImplRivals because `Thing.go` names a method and
// two files may each provide one, and the recorded answer resolves that by
// file order. Naming the interface removes exactly that freedom: two impls of
// ONE interface for ONE receiver is a coherence error the analyzer rejects, so
// (receiver declaration, interface, method) has at most one written provider
// program-wide. The loop below still refuses on a second match rather than
// asserting the analyzer's rule, because a refusal is the safe answer to a
// premise that has become false and a silent choice is not.
//
// # What it does NOT change
//
// A LOCAL impl still wins: every local route — interfaceCall, stdIfaceCall,
// foreignIfaceCall, typeQualifiedCall — has already declined by the time this
// runs, and a site in this file's own unit is skipped. So is the SYNTHESIZED
// universal Debug when anybody WROTE one, which is writtenImplMembers' rule and
// what `12-derives-and-standard-interfaces/cross_file_debug` pins.

// locallyAliased reports whether this file binds `d` under a name that is not
// the name its declaration carries — an `import other.{Gadget as Doodad}`.
//
// The def is the wrong place to ask: a mirror is built with the OWNER's `nomi`,
// so `d.nomi` reads `Gadget` for both spellings. What differs is the KEY
// `g.types` binds it under, which is the local scope's answer and the only
// place the alias exists. A local declaration and a plain import both bind
// their declared name, and a module-qualified spelling binds it behind its
// qualifier (`types.Meters`, the whole spelling typeOf registers): none of the
// three is an alias.
//
// A scan rather than a reverse map, because it runs once per candidate receiver
// on a route that has already fallen through fifteen arms, and a second index
// would be a second definition of a fact `g.types` already holds.
func (g *gen) locallyAliased(d *typeDef) bool {
	declared := typeDeclName(d.decl)
	if declared == "" {
		return false
	}
	for name, td := range g.types {
		if td == d && name != declared && !strings.HasSuffix(name, "."+declared) {
			return true
		}
	}
	return false
}
