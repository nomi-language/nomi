package rt

// `std/type.Type<T>`: a Nomi type passed as a runtime value.
//
// std/type.nomi declares exactly `pub host type Type<T>` and nothing else — no
// operation, no interface, no constructor — so the whole of the surface is
// "a witness exists, and something else consults it". The one consumer today is
// `std/context`'s value store, which is keyed by type rather than by name.
//
// # Why the payload is a *TypeID and not a name
//
// An identity spelled as a string and re-parsed is a known source of silent
// bugs. A name-keyed witness has two ends
// that can disagree about what the string is: one side may key by a
// module-qualified runtime name while the other keys by the bare source
// spelling at the call, so a type reached through a module qualifier would key
// one way going in and the other coming out. This witness takes identity from
// the declaration, so the question cannot arise.
//
// Here identity is the address of the `TypeID` variable the declaration
// produced, established by the Go linker — the same rule `Method.impls`
// (dispatch.go) and `onceID` (once.go) already key on, and the two hazards
// dispatch.go designed against apply unchanged: `TypeID` is non-empty so two
// package-level variables cannot legally share an address, and its diagnostic
// name is module-qualified so two distinct types are never byte-identical
// merge candidates.
//
// # Why T is on the Go type and carries no storage
//
// `T` is phantom: a `Type[T]` is one pointer at run time, whatever T is. It is
// on the Go type anyway, and that is the same trade rt/channel.go states for
// `Sender[T]`/`Receiver[T]` — two Go types over one runtime object, so a
// wrong-direction lowering is a Go compile error rather than a wrong answer.
// Passing a `Type<Marker>` where a `Type<TraceId>` is wanted does not compile,
// and `ContextValue` can therefore return `Maybe[T]` with T read off the witness
// instead of taking a second type argument nothing checks against the first.
//
// The zero value is deliberately not a valid witness: `TID` is nil, which
// `ContextValue` cannot match against any stored node because a value node
// always carries a non-nil TID. Every construction site writes the field, and
// a nil TID reaching a lookup
// answers None rather than matching everything.
type Type[T any] struct {
	// TID is the identity of the Nomi type this witnesses. Its address is the
	// identity; nothing reads the value.
	TID *TypeID
}
