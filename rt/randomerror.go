package rt

// RandomError is Nomi's `std/random.Error`: why weights could not make a
// generator, or why OS entropy could not seed one.
//
// # Why this type exists, which is not "to represent an enum"
//
// It is the result half of `std/random`'s fallible calls —
// `Generator.weighted` returns `Result<Generator<T>, Error>` and
// `Seed.from_os` returns `Result<Seed, Error>` — so without a representation
// for it neither signature projects, whatever else is represented.
// `Generator<T>` has an `rt` type and a `stdGenStructSpecs` row; this is the
// other half.
//
// That is `CalendarError`'s reason and its header says so in the same words: a
// fallible constructor's error type is not optional scenery.
//
// # The slot layout
//
// `CalendarError` shares a single `Msg` across all five of its variants because
// all five carry exactly one String. This enum's payloads differ in Go type,
// and internal/irbuild's dedup rule shares a slot only across variants whose
// payloads have the same Go type:
//
//	InvalidWeight     {weight: Float}           one float64
//	ZeroWeightTotal                             nothing
//	OsEntropy         {reason: String}          one string
//
// A zero `Tag` means never constructed, which is what makes a Go zero value
// detectable rather than silently reading as the first variant — the property
// every enum here holds and the reason none starts at 0.
type RandomError struct {
	// Tag is 1..3 in declaration order. 0 means never constructed.
	Tag uint8
	// Weight is `InvalidWeight`'s rejected weight.
	Weight float64
	// Reason is `OsEntropy`'s underlying failure text.
	Reason string
}

// The tag values, in declaration order. Named rather than written as literals at
// each construction site, for the reason the other enums here give: a reordered
// declaration is a one-line change here and a silent misinterpretation
// everywhere else.
const (
	TagRandomInvalidWeight uint8 = iota + 1
	TagRandomZeroWeightTotal
	TagRandomOsEntropy
)
