package rt

// `std/literals.Fragment<T>`, as a Go generic type.
//
// # Why it is here at all, and why it is not in prelude.go
//
// prelude.go's argument for putting `Maybe` and `Result` in rt is about
// IDENTITY and applies unchanged: the IR builder (internal/irbuild) gives a
// generic std enum one Go type, and that type has to live in a package every
// caller shares, or one Nomi type would be N mutually unassignable Go types.
//
// What does NOT carry over is the word "prelude". `Maybe` and `Result` are in
// every file's scope whether it imports them or not; `Fragment` is an ordinary
// `pub enum` in std/literals that a file has to import. The builder's
// `preludeSpecs` table is really "the generic std enums whose Go type rt
// declares by hand" — see internal/irbuild/prelude.go — and Fragment is the
// third row rather than a third prelude. The one observable consequence is
// stated where it bites: a module that does not have `Fragment` in scope gets
// no anchor for it and refuses every mention, which is the same rule that keeps
// a user's own `Maybe` out.
//
// # Why the corpus needs it
//
// `<Type>"…"` desugars to `<Type>.from_fragments([Fragment.Static(…),
// Fragment.Dynamic(…), …])`, so `Fragment` is the parameter type of every typed
// literal's handler. Measured over the repo at 0f2ee1de: all 14 `from_fragments`
// declarations take `List<Fragment<I>>` (the checker's `checkTaggedString`
// rejects any other parameter shape), 11 of them at `I = String`. Without this
// type the builder refused `Fragment` under `generic type` in 26 corpus files.
//
// # The layout is prelude.go's decision, with one thing prelude.go never had
//
// A tagged struct, tag 0 reserved invalid, one payload field per variant.
//
// The new thing is that Fragment MIXES payload kinds. std/literals.nomi
// declares
//
//	pub enum Fragment<T> {
//	  Static String
//	  Dynamic T
//	}
//
// so `Static`'s payload is CONCRETE and `Dynamic`'s is PARAMETRIC. `Maybe` and
// `Result` are uniformly parametric, so nothing before this had to tell the two
// apart — and at `T = String`, which is the only instantiation the corpus
// contains, they are INDISTINGUISHABLE by layout: both fields are `string`.
// Swap them and `Fragment[int64]` gets a `string` where it needs an `int64`,
// which is a wrong answer that no corpus program and no `Fragment[string]`
// fixture can see. That is why the builder checks the DECLARATION's payload
// type expression rather than the rendered field type, and why the layout guard
// (internal/irbuild's TestFragmentLayoutMatchesRT) reflects over
// `Fragment[int64]` and not `Fragment[string]`.
//
// # A corpus run cannot see a bug in here
//
// No passing corpus program distinguishes the two layouts. The tags below are
// pinned by absolute assertions in fragment_test.go, cross-checked against
// std/literals.nomi's declaration order by internal/irbuild's
// TestFragmentShapeMatchesStdSource, and against this struct by
// TestFragmentLayoutMatchesRT.

// Tag values, written out for the reason prelude.go's are: they are what a
// reader has to be able to check against std/literals.nomi by eye.
const (
	// TagStatic and TagDynamic are Fragment's variants, in declaration order.
	// TagInvalid (prelude.go) is 0 here too: one reserved-invalid value for
	// every tagged struct rt declares, not one per type.
	TagStatic  uint8 = 1
	TagDynamic uint8 = 2
)

// Fragment is Nomi's `std/literals.Fragment<T>`: `Static String` or
// `Dynamic T`.
//
// Both fields are always present, and `Static` is `string` for every T. A Go
// generic struct cannot dedupe storage the way the builder's monomorphic enums
// do — whether T is `string` is not known where the struct is declared — so
// `Fragment[string]` carries two strings where a monomorphic enum of the same
// shape would carry one. Dedup is a storage optimization and never a semantic
// rule; prelude.go pays the same price for `Result[int64, int64]`.
type Fragment[T any] struct {
	// Tag is 1 for Static and 2 for Dynamic. 0 means never constructed.
	Tag uint8
	// Static is the source text between interpolation slots. It is `string`
	// rather than `T` because std/literals.nomi declares it that way: a
	// literal's static segments are always text, and only the `${…}` slots
	// carry the interface `T` pins.
	Static string
	// Dynamic is a `${…}` slot's value. Reading either payload without
	// checking Tag is the caller's bug; `case` lowering never does.
	Dynamic T
}
