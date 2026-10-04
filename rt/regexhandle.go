package rt

// std/regex's compiled pattern, as an opaque handle. This is also where the
// type of any co-located adapter lives.
//
// # Why the handle type lives in rt
//
// A function crossing into an adapter is called from one place, and that
// place can import whatever it names. A value is stored: in a local, a struct
// field, a list element, a `Maybe` payload, in code that never calls the
// adapter. What every consumer can name is rt, so the handle type belongs here
// and only the implementation lives with the adapter. The handle mechanism
// (a Go value with no Nomi-visible structure, carried through a lowered
// signature, with an identity that survives the boundary) is shared; what sits
// behind each handle is not.
//
// # Why `any` and not the adapter's type
//
// rt imports only the standard library, uniseg and its own packages
// (TestRuntimeImportsOnlyItsAllowlist), so rt cannot import the regex adapter.
// The payload is therefore `any`, filled and read by `nomi/stdregex`, which
// may import both sides.
//
// rt could have imported `regexp` directly — it is the Go standard library, so
// it costs rt no require at all — and that is deliberately NOT what this is.
// It would make rt a SECOND implementation of std/regex beside the adapter, and
// the two could drift. One implementation with two marshalling surfaces is nomi/stdstrings' arrangement
// and it is the one that cannot drift.
//
// # Why a named one-field struct rather than a bare `any`
//
// rt.Dynamic's reason verbatim: `stdHostKindOfGoType` identifies this family by
// `reflect.Type`, so a binding whose parameter were `any` would project onto
// EVERY declaration whose parameter has no representation. Identity on the
// named type, never on the underlying shape.
//
// A `Regex` is COMPARABLE — `Impl` holds the adapter's pointer — so Go `==` on
// two of these answers pointer identity. Nothing in std/regex derives Equatable for it, so no
// Nomi program can observe that today; it is stated because a zero-width or
// non-comparable payload would make `==` a panic rather than a wrong answer.
type Regex struct {
	// Impl is the adapter's compiled pattern (`*nomi/std/regex.Regex`), held as
	// `any` because rt may not name that type. Exported for the same reason
	// Dynamic.Inner is: the package that fills it is a different one.
	//
	// The zero value is a nil payload, which is what a never-constructed handle
	// carries. nomi/stdregex answers every operation over one the way the
	// adapter answers it over a nil pointer, rather than trapping, because a
	// zero value can come from a declared-but-unassigned Go variable.
	Impl any
}
