package rt

import "fmt"

// Existential dispatch over Go types: a table from a type's identity to its
// implementation, for the places the Go type system cannot answer "which type
// is this?" on its own.
//
// # Where a table is needed, and where it is not
//
// Nomi has three call shapes and only one of them needs this file.
//
//   - A concrete qualifier — `Date.from_fragments(…)`, `String.trim(s)`,
//     `Speech.speak(rex)` where `rex` is statically a `Dog` — names exactly one
//     function, so it is a direct call.
//   - An existential — an interface-typed parameter, field or element whose
//     concrete type is erased — is the only shape whose callee is decided at
//     run time. That is what Dyn and Method are for.
//   - A type-parameter qualifier (`T.from_json(x)`) is keyed by a dictionary
//     rather than by a receiver: the concrete type argument the call site
//     solved, arriving as a *TypeID parameter. That is Method.At, and it is the
//     one route that works for a method with no self-position parameter.
//
// # Why not a Go interface with one method per Nomi function
//
// It is not awkward, it is impossible, three independent ways. `Literal`,
// `FromJson` and `App` have no Self-typed parameter at all — self appears only
// inside a return type, or the interface declares no functions — so there is no
// receiver to hang a Go method on. Go cannot dispatch on a return type, which
// is the whole of `FromJson.from_json<Int>(…)`. And Nomi's orphan rule admits
// `impl Numberish for Int` in user code, while Go cannot add a method to a type
// in another package and cannot add one to a builtin at all.
//
// A Go interface used purely as a BOX for an erased value is a different
// question, and Dyn.V is exactly that.
//
// # Identity is a pointer, and that is the whole point
//
// Divergence-ledger row 16 records six silent bugs in one session, every one of
// them caused by a Nomi type's runtime identity being a STRING assembled at
// construction sites and re-parsed at lookup sites. `c2afa942` is the sharpest:
// a short-type-name helper cut a type name at its LAST dot, so the namespaced
// `json.Json.DecodeError` came out as `DecodeError` — which is `std/dynamic`'s
// type — two impls came to share one dispatch slot, and Go's map iteration
// order decided which one won. The failure was intermittent and nothing raised.
//
// Here a type's identity IS the address of its TypeID. A caller makes exactly
// one per type, keys the table on its address, and never compares a name. Two
// same-named types in two modules are two TypeIDs, so they are two addresses
// and cannot be confused however their names are spelled, shortened, or cut.
// There is no string in the path, so there is nothing to collapse.
//
// Two Go-specific hazards sit under that claim, both in the same family, and
// both are designed against rather than assumed away:
//
//  1. Two package-level variables of a ZERO-SIZED type may legally share an
//     address. A `struct{}` TypeID would therefore have silently merged two
//     types' identities — the same bug, reintroduced by the fix. TypeID carries
//     a field, so it is never zero-sized.
//  2. Two variables with byte-identical CONTENTS are a candidate for merging by
//     a sufficiently aggressive toolchain. Go does not merge package-level
//     variables today, and "does not today" is precisely the reasoning that
//     would have lost to hazard 1 — so a TypeID carries the MODULE-QUALIFIED
//     name (`shapes.Point`, `geometry.Point`), which differs by construction
//     for any two distinct types and is the better diagnostic anyway.
//
// TestTypeIDsAreDistinctAddresses pins both.
//
// # Failure is loud
//
// A missing implementation traps and a duplicate binding panics. Four of row
// 16's six bugs degraded to a plausible-looking wrong answer instead of
// failing, and the third property that would have removed the class was "an
// identity that does not resolve should be an error, not a fallback".

// TypeID is one Nomi type's runtime identity. Its ADDRESS is the identity; the
// value carries nothing a lookup reads.
//
// Nomi is the type's module-qualified Nomi name and is DIAGNOSTIC ONLY. Nothing
// in this file compares it, and nothing may start: the moment a name decides a
// lookup, this is `c2afa942` again. See the two hazards in the file comment for
// why the field exists at all and why it must be qualified.
type TypeID struct {
	Nomi string
}

// The identities of the types the compiler represents directly rather than
// declaring. They live here, once, because `impl Numberish for Int` is legal
// under the orphan rule in any module and every such impl must key on the SAME
// Int — one canonical variable is how that is guaranteed rather than hoped for.
var (
	TIDInt    = TypeID{Nomi: "Int"}
	TIDFloat  = TypeID{Nomi: "Float"}
	TIDString = TypeID{Nomi: "String"}
	TIDBool   = TypeID{Nomi: "Bool"}
	TIDUnit   = TypeID{Nomi: "Unit"}
)

// Dyn is a Nomi value whose static type was an interface: the erased value plus
// the identity erasing it lost.
//
// A value is boxed at the point the checker accepted the widening — an
// argument, a return, a binding with an interface annotation, a field — so by
// the time a dispatch site sees the value the identity is already attached and
// nothing has to be recovered.
type Dyn struct {
	TID *TypeID
	V   any
}

// Method is one interface function's dispatch table.
//
// F is the function type chosen for this method: `fr` first, then
// the method's parameters with every self-typed position erased to `any`, then
// the return. Generic rather than a `func(...any) any` so the call site pays no
// boxing beyond the receiver and Go type-checks each binding at its own site.
type Method[F any] struct {
	// key is the Nomi spelling `Iface.method`, for diagnostics only.
	key   string
	impls map[*TypeID]F
}

// NewMethod declares a table. One table serves one interface function, and each
// implementing type binds into it.
func NewMethod[F any](key string) *Method[F] {
	return &Method[F]{key: key, impls: make(map[*TypeID]F)}
}

// Bind registers one implementation.
//
// A second binding for one type PANICS rather than overwriting. An overwrite
// would hand the answer to the order the bindings ran in, which is the shape
// `c2afa942` failed in. Nomi's
// coherence check already rejects duplicate impls at compile time; this is the
// backstop that makes a hole in it loud instead of arbitrary.
func (m *Method[F]) Bind(tid *TypeID, fn F) {
	if _, dup := m.impls[tid]; dup {
		panic(&Error{Msg: fmt.Sprintf(
			"nomi: %s has two implementations bound for type '%s'", m.key, tid.Nomi)})
	}
	m.impls[tid] = fn
}

// Get returns the implementation d's type binds, trapping when there is none.
//
// The receiver-driven route: the box carries the identity, so this is the
// existential shape the file comment describes.
func (m *Method[F]) Get(d Dyn) F { return m.At(d.TID) }

// At returns the implementation tid binds, trapping when there is none.
//
// The DICTIONARY-driven route, and the reason it exists rather than being
// folded into Get. `T.method(x)` on a bounded type parameter is keyed by the
// concrete type argument the call site solved, not by a receiver — and for a
// method with no self-position parameter (`FromJson.from_json(json: Json)`,
// where self occurs only in the return) there is no receiver to key on at all.
//
// Get delegates here so there is exactly ONE map probe and ONE trap message in
// the runtime. Two encodings of a lookup is the shape whose failure mode is a
// later correction landing in only one of them.
//
// Unreachable on checked code: DetectMissingImpls rejects a program that
// dispatches an interface at a type supplying no impl. It is kept because the
// alternative to trapping is a zero-valued F and a nil-func panic naming
// nothing, and because "an identity that does not resolve should be an error,
// not a fallback" is the rule this file exists to hold.
func (m *Method[F]) At(tid *TypeID) F {
	fn, ok := m.impls[tid]
	if !ok {
		name := "<unset>"
		if tid != nil {
			name = tid.Nomi
		}
		NoImplFor(m.key, name)
	}
	return fn
}

// NoImplFor aborts with the dispatch miss above, for a call site resolved
// statically to a type with no implementation.
//
// A function rather than a literal at each site, for NoCaseMatchError's stated
// reason: an observable string has one copy. `At` prints it through here too.
//
// The caller that needs it is irbuild's preludeHashCall: `Result.hash(Ok(4))`
// gives the Result's E side no information, so that position is an UNSOLVED type
// argument and the tag switch still has to carry an `Err` arm. No value of an
// unsolved position exists in the program, so the arm is unreachable.
func NoImplFor(key, nomi string) { Trap(noImplText(key, nomi)) }

func noImplText(key, nomi string) string {
	return fmt.Sprintf("%s: no implementation for type '%s'", key, nomi)
}
