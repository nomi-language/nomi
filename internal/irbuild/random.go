package irbuild

import (
	"reflect"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

// `std/random` — the REPRESENTATION half of a co-located adapter module.
//
// # What this file is, and what it deliberately is not
//
// It supplies `Generator<T>`'s kind and nothing else. There is no owner-keyed
// operation arm here — no `generatorCall` beside `setCall`, `rangeCall` and
// `channelCall` — and that absence is the design decision this file exists to
// record, because the shape of the surrounding family invites the opposite.
//
// sets.go, ranges.go, channels.go and vectors.go each pair a generic std type's
// kind with a hand-written arm that intercepts its qualified calls before the
// stdlib index refuses them. channels.go's header states why the interception is
// needed and it is exactly true of `Generator` too:
//
//	EVERY channel declaration is a `pub host fn ...<T>` — so `stdCandidateFor`
//	short-circuits at `case generic:` before it asks anything about a
//	signature, and the index carries a refusal for all five.
//
// `random.Generator.step`, `.list`, `.uniform`, `.flat_map`, `.weighted`,
// `.map` and `.constant` all carry that same `stdlib generic function`
// refusal for that same reason, so the mechanism matches.
//
// It still must not be copied, and the reason is a property of `std/random`
// rather than of the pattern. Channel's, Set's, Range's and Vector's operations
// are `pub host fn`: declarations with NO Nomi body, for which an rt
// implementation is the only possible answer. `Generator`'s operations are
// ordinary Nomi source whose entire content is threading a seed through
// closures. rt/random.go refuses the copy for `Seed` and the argument is
// stronger one level up:
//
//	adding a splitmix64 step beside the adapter's would be two implementations
//	of one sequence — the divergence rt/opaque.go's header warns about, over a
//	generator where disagreement is silent because both answers look random.
//
// A hand-written `rt.GeneratorStep` would duplicate the draw ORDER as well as
// the arithmetic, and a wrong draw order is exactly as invisible as a wrong
// step: `random_test.nomi` pins `hand == [1, 5, 2]`, which is a fact about the
// order three draws consume the register in. So the combinators lower AS NOMI or
// they stay refused, and this file gives them the type they need in order to be
// lowerable at all.
//
// # What the kind does not cover
//
// The row below admits the members whose only obstacle is the missing TYPE. It
// does not admit the members on the `generic` arm, because `stdCandidateFor`
// reaches `case generic:` BEFORE it consults any signature. Of the eight
// members `random_test.nomi` calls, five are on that arm.

// randomSeedKind is `std/random.Seed`'s kind, resolved lazily.
//
// LAZILY and never at package-var initialization, which is a hard constraint
// rather than a style choice: `opaqueDefs()` sits downstream of the opaque spec
// table, and stdprelude.go's packageNeutral header records the shape of the
// failure when a std table is read while another is still building — an
// INITIALIZATION CYCLE the compiler rejects by name. Every caller here is a
// `kindOf` closure, invoked long after every table is built.
//
// Resolved by rt GO TYPE rather than by (origin, name), reusing
// opaqueKindOfGoType: `rt.Seed` is the thing this file actually needs to agree
// with, and naming it makes an rt rename a compile error here instead of a
// silent nil kind.
var randomSeedKind = sync.OnceValue(func() kind {
	return opaqueKindOfGoType(reflect.TypeFor[rt.Seed]())
})

// generatorFieldKind is `Generator<T>`'s one field: `(Seed) -> (T, Seed)`.
//
// The first field in either struct family whose kind is a FUNCTION, and it
// composes two structural constructors over an anchored opaque newtype. Both
// doors are honoured the way the `Range` row's `Maybe<T>` field honours its
// `!shared`: with no gen, only the process-wide instance is reachable, so a
// non-neutral type argument has NO kind and says so rather than panicking
// inside internComp.
func generatorFieldKind(g *gen, arg kind) kind {
	seed := randomSeedKind()
	// kindInvalid: propagates — the opaque row is absent, so nothing over it has a kind.
	if seed == kindInvalid {
		return kindInvalid
	}
	// kindInvalid: propagates — the type argument declined upstream and its own position reports it.
	if arg == kindInvalid {
		return kindInvalid
	}
	if g == nil && !arg.packageNeutral() {
		// The stdlib signature boundary. Honoured rather than asserted.
		return kindInvalid
	}
	return funcKindIn(g, []kind{seed}, g.tupleKind([]kind{arg, seed}))
}
