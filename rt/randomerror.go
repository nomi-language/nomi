package rt

// RandomError is Nomi's `std/random.Error`: why a generator could not be
// constructed or drawn from.
//
// # Why this type exists, which is not "to represent an enum"
//
// It is the result half of every fallible `std/random` constructor —
// `Generator.int`, `Generator.float` and `Generator.weighted` all return
// `Result<Generator<T>, Error>` — so without a representation for it no random
// signature projects, whatever else is represented. `Generator<T>` has an
// `rt` type and a `stdGenStructSpecs` row; this is the other half.
//
// That is `CalendarError`'s reason and its header says so in the same words: a
// fallible constructor's error type is not optional scenery.
//
// # The slot layout, and why it is not one field
//
// `CalendarError` shares a single `Msg` across all five of its variants because
// all five carry exactly one String. This enum cannot, and internal/irbuild's
// dedup rule is what decides it: a slot is shared across variants whose payloads have
// the same Go type, and never within one variant, because a struct-shaped
// variant's fields are live at the same time.
//
//	InvalidIntRange   {from: Int,   to: Int}     two int64, both live
//	InvalidFloatRange {from: Float, to: Float}   two float64, both live
//	InvalidWeight     {weight: Float}           one float64
//	ZeroWeightTotal                             nothing
//	OsEntropy         {reason: String}          one string
//
// So `From`/`To` are int64 and `FloatFrom`/`FloatTo` are float64 rather than one
// pair reused: Int and Float are different Go types, and sharing would need a
// conversion at every read that makes a bound of 9223372036854775807
// unrepresentable through the float slot.
//
// A zero `Tag` means never constructed, which is what makes a Go zero value
// detectable rather than silently reading as the first variant — the property
// every enum here holds and the reason none starts at 0.
type RandomError struct {
	// Tag is 1..5 in declaration order. 0 means never constructed.
	Tag uint8
	// From and To are `InvalidIntRange`'s bounds.
	From int64
	To   int64
	// FloatFrom and FloatTo are `InvalidFloatRange`'s bounds.
	FloatFrom float64
	FloatTo   float64
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
	TagRandomInvalidIntRange uint8 = iota + 1
	TagRandomInvalidFloatRange
	TagRandomInvalidWeight
	TagRandomZeroWeightTotal
	TagRandomOsEntropy
)
