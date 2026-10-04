package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// genStructSigKind is the kind a stdlib signature's `Range<Int>` / `Set<Int>`
// annotation names, reporting whether the annotation was a generic std struct
// at all.
//
// # Why std's own signatures need this arm
//
// `stdGenStructTypeOf` reads these annotations at `types.go`'s user-annotation
// path. Without this arm a generic std struct would be admissible in a USER'S
// annotation and inadmissible in std's own signature for it: `Range.from` and
// `Range.naturals` both return a CONCRETE `Range<Int>`, which is exactly what
// `sharedGenStructInstance` interns process-wide, and would refuse as
// `stdlib function outside the scalar subset` with the representation present.
//
// `stdTypeKind`'s sibling arms (opaques, monomorphic enums, opaque structs,
// `host type`s, stdlib interfaces, prelude instances, `List`, `Map`) each pair
// a spec family with a signature arm, and this is the generic-struct family's.
// A family with a spec and no arm is invisible from every source-level test,
// because another spelling of the same question works.
//
// # Resolved through the ANCHOR, not the name
//
// `anchors.genStructs` is `stdGenStructAnchorsOf`'s map, which requires the
// analyzer's (Origin, Name) identity AND `stdGenStructValidated`'s declaration
// shape — the same two conditions a gen's `g.genStructs` requires, from the same
// function. A module declaring its own `Range` therefore has no anchor and
// refuses, which is the identity rule every family here holds.
//
// Type ARGUMENTS go through the same restricted `stdTypeKind` the outer position
// uses, so `Range<Decimal>` refuses on the argument rather than admitting a kind
// this boundary cannot share, and `Set<List<Int>>` works without a second rule.
//
// Reported as handled even when it refuses, for `mapSigKind`'s reason: an
// unrepresentable argument must be the ANSWER rather than falling through to a
// lookup that would return the same kindInvalid for a different reason.
//
// A TYPE PARAMETER argument (`Range<T>` inside `impl Range<T>`) refuses here, and
// that is correct rather than a limitation: `stdTypeKind` has no arm for a bare
// `T` and cannot have one. Such a declaration is GENERIC, and
// `collectStdCandidates` says so through `analysis.ReceiverTypeParamNames`
// before this function is ever consulted — see stdlib.go's `sigNamesTypeParam`.
func genStructSigKind(gt *ast.GenericType, anchors stdAnchors) (kind, bool) {
	a := anchors.genStructs[gt.Name]
	if a == nil {
		return kindInvalid, false
	}
	if len(gt.Params) != len(a.spec.params) {
		// The checker rejects the program first; refusing rather than
		// instantiating at the wrong arity keeps this from emitting Go against a
		// type nobody wrote. `mapSigKind`'s arity rule, and the same reason.
		return kindInvalid, true
	}
	args := make([]kind, len(gt.Params))
	for i, p := range gt.Params {
		args[i] = stdTypeKind(p, anchors)
		// kindInvalid: propagates — the argument's own refusal is the answer.
		if args[i] == kindInvalid {
			return kindInvalid, true
		}
	}
	// `!shared` is a refusal rather than a decline, exactly as
	// stdGenStructTypeOf treats it: declining would fall through and report the
	// CONTAINER — which lowers — instead of the argument, which does not.
	//
	// The PROCESS-WIDE door and not the module-local one, and that is the whole
	// reason this call is spelled differently from its two neighbours. A
	// stdFunc's kinds are built ONCE and compared BY POINTER against a call
	// site's argument kinds in a different gen (stdprelude.go's header), so a
	// per-gen instance here would be a kind no call site could ever match. A
	// std signature mentioning `Channel<T>` at a package-relative argument is
	// therefore still outside the subset, and `stdlib generic function` keeps
	// that population.
	k, shared := sharedGenStructInstance(a.spec, args)
	if !shared {
		return kindInvalid, true
	}
	return k, true
}
