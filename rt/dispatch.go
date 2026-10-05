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
// A Go interface used purely as a box for an erased value is a different
// question, and Dyn.V is exactly that.
//
// # Identity is a pointer
//
// A Nomi type's runtime identity must not be a string assembled at
// construction sites and re-parsed at lookup sites. A short-type-name helper
// that cuts a type name at its last dot turns the namespaced
// `json.Json.DecodeError` into `DecodeError`, which is `std/dynamic`'s type.
// Two impls then share one dispatch slot, Go's map iteration order decides
// which one wins, and the failure is intermittent with nothing raised.
//
// Here a type's identity is the address of its TypeID. A caller makes exactly
// one per type, keys the table on its address, and never compares a name. Two
// same-named types in two modules are two TypeIDs, so they are two addresses
// and cannot be confused however their names are spelled, shortened, or cut.
// There is no string in the path, so there is nothing to collapse.
//
// Two Go-specific hazards sit under that claim, both in the same family, and
// both are designed against rather than assumed away:
//
//  1. Two package-level variables of a zero-sized type may legally share an
//     address. A `struct{}` TypeID could therefore merge two types'
//     identities, which is the same bug. TypeID carries a field, so it is
//     never zero-sized.
//  2. Two variables with byte-identical contents are a candidate for merging by
//     a sufficiently aggressive toolchain. Go does not merge package-level
//     variables, but relying on that is the same reasoning hazard 1 defeats,
//     so a TypeID carries the module-qualified
//     name (`shapes.Point`, `geometry.Point`), which differs by construction
//     for any two distinct types and is the better diagnostic anyway.
//
// TestTypeIDsAreDistinctAddresses pins both.
//
// # Failure is loud
//
// A missing implementation traps and a duplicate binding panics. An identity
// that does not resolve should be an error, not a fallback, because a fallback
// turns a dispatch bug into a plausible-looking wrong answer.

// TypeID is one Nomi type's runtime identity. Its address is the identity; the
// value carries nothing a lookup reads.
//
// Nomi is the type's module-qualified Nomi name and is diagnostic only. Nothing
// in this file compares it, and nothing may start: once a name decides a
// lookup, the collapse described above can happen again. See the two hazards
// in the file comment for why the field exists at all and why it must be
// qualified.
type TypeID struct {
	Nomi string
}

// The identities of the types the compiler represents directly rather than
// declaring. They live here, once, because `impl Numberish for Int` is legal
// under the orphan rule in any module and every such impl must key on the same
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
// A second binding for one type panics rather than overwriting. An overwrite
// would hand the answer to the order the bindings ran in, which is the
// short-name collapse above. Nomi's
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
// The dictionary-driven route, and the reason it exists rather than being
// folded into Get. `T.method(x)` on a bounded type parameter is keyed by the
// concrete type argument the call site solved, not by a receiver — and for a
// method with no self-position parameter (`FromJson.from_json(json: Json)`,
// where self occurs only in the return) there is no receiver to key on at all.
//
// Get delegates here so there is exactly one map probe and one trap message in
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
// gives the Result's E side no information, so that position is an unsolved type
// argument and the tag switch still has to carry an `Err` arm. No value of an
// unsolved position exists in the program, so the arm is unreachable.
func NoImplFor(key, nomi string) { Trap(noImplText(key, nomi)) }

func noImplText(key, nomi string) string {
	return fmt.Sprintf("%s: no implementation for type '%s'", key, nomi)
}
