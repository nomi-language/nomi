package rt

// The two prelude enums, as Go generic types.
//
// `Maybe<T>` (std/maybe.nomi) and `Result<T, E>` (std/results.nomi) are
// ordinary Nomi enums — no compiler magic in the language — but they are the
// two the whole corpus is written in terms of. Measured at 1a7af9b3: `Some`
// blocked 44 of 213 corpus files, `None` 28, `Ok` 26, `Err` 12.
//
// # Why these two live in rt
//
// The tagged-struct representation itself needs nothing new: Go has generics,
// so `Maybe<T>` is `struct { Tag uint8; Some T }` with the type parameter
// carried directly, and no type-parameter DICTIONARY is involved. What rt
// gives is IDENTITY. A named type's identity in internal/irbuild is the
// `*typeDef` its declaration produced, and a std type needs one Go type that
// every module's code can name.
//
// `List[T]` is the same answer to the same question one file over. So these
// are declared once, here, and every module names the same type.
//
// # The layout is the shared enum representation, not a new one
//
// A tagged struct, with
// tag 0 RESERVED INVALID so a Go zero value is detectably never-constructed.
// `var m Maybe[int64]` has Tag 0, which is neither Some nor None, and
// `make([]Maybe[int64], n)` produces n detectably-invalid values rather than n
// copies of the first variant. Nomi has no zero values, so the reservation
// costs nothing and converts a silent wrong answer into a detectable one.
//
// One deliberate departure, and it is forced rather than chosen: internal/irbuild
// DEDUPES an enum's payload slots by identical underlying Go type, because
// only one variant is live at a time. A Go generic struct cannot do that —
// whether `T` and `E` are the same type is not known where the struct is
// declared — so `Result[int64, int64]` carries two 8-byte fields where a
// monomorphic Nomi enum of the same shape would carry one. Dedup is a storage
// optimization and never a semantic rule, so paying it here is sound; it is
// stated because internal/irbuild's own comment says slots are deduped and this
// is the one enum family where they are not.
//
// # Fields are exported, and that is the whole interface
//
// Callers are in other Go packages, so `Tag`, `Some`, `Ok` and `Err` have to
// be exported for them to build a value and read a payload. They are named
// after the Nomi variants rather than after internal/irbuild's `p0`/`p1` slot
// spelling, because this file is read by people.
//
// # A golden file cannot see a bug in here
//
// A tag that is consistently wrong prints the same text, so a fixture that
// only compares output cannot fail on it. The tags below are therefore pinned by
// ABSOLUTE assertions in prelude_test.go, and cross-checked against the
// declaration order in std/maybe.nomi and std/results.nomi by
// internal/irbuild's TestPreludeShapeMatchesStdSource. Neither test compares two
// implementations; both spell out the expected answer.

// Tag values. Written out rather than left implicit at the construction sites
// because they are the one thing a reader has to be able to check against
// std/maybe.nomi and std/results.nomi by eye.
const (
	// TagInvalid is the Go zero value: an enum that was never constructed.
	TagInvalid uint8 = 0
	// TagSome and TagNone are Maybe's variants, in declaration order.
	TagSome uint8 = 1
	TagNone uint8 = 2
	// TagOk and TagErr are Result's variants, in declaration order.
	TagOk  uint8 = 1
	TagErr uint8 = 2
)

// Maybe is Nomi's `std/maybe.Maybe<T>`: `Some T` or `None`.
//
// `None` carries nothing, so it needs no field of its own — a Maybe is one tag
// byte plus one T, and Go packs it exactly as the monomorphic form would.
type Maybe[T any] struct {
	// Tag is 1 for Some and 2 for None. 0 means never constructed.
	Tag uint8
	// Some is the payload of the Some variant, and is the T-typed zero value
	// in a None. Reading it without checking Tag is the caller's bug; `case`
	// lowering never does, because it tests the tag first.
	Some T
}

// Result is Nomi's `std/results.Result<T, E>`: `Ok T` or `Err E`.
type Result[T, E any] struct {
	// Tag is 1 for Ok and 2 for Err. 0 means never constructed.
	Tag uint8
	Ok  T
	Err E
}

// Some builds a present Maybe.
//
// These four exist for rt's OWN use, where a hand-written host function
// returns a Maybe or a Result. Writing the tag by hand at each of those sites is exactly
// the drift this file exists to prevent.
func Some[T any](v T) Maybe[T] { return Maybe[T]{Tag: TagSome, Some: v} }

// None builds an absent Maybe. The type argument is explicit because there is
// no value to infer it from: `rt.None[int64]()`.
func None[T any]() Maybe[T] { return Maybe[T]{Tag: TagNone} }

// Ok builds a successful Result.
func Ok[T, E any](v T) Result[T, E] { return Result[T, E]{Tag: TagOk, Ok: v} }

// Err builds a failed Result.
func Err[T, E any](e E) Result[T, E] { return Result[T, E]{Tag: TagErr, Err: e} }

// ResultFromMaybe reads Tag and never assumes the complement: a
// never-constructed `Maybe[T]` has Tag 0, which is neither Some nor None, and
// answering Err for it is the only answer that does not invent a payload out
// of a Go zero value. The absolute pins in prelude_test.go cover that case,
// which no Nomi program can reach.

// ResultFromMaybe is std/results' `from_maybe`: Some becomes Ok, None becomes
// Err carrying e.
func ResultFromMaybe[T, E any](m Maybe[T], e E) Result[T, E] {
	if m.Tag == TagSome {
		return Result[T, E]{Tag: TagOk, Ok: m.Some}
	}
	return Result[T, E]{Tag: TagErr, Err: e}
}
