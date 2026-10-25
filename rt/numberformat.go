package rt

// NumberParseError is `std/localize.NumberParseError`: why text is not a
// number in a locale.
//
// Here for LocaleError's reason (rt/localeerror.go): declared in a std
// module and named from user modules, so `Result<Int, NumberParseError>`
// needs a representation every generated package shares. The parsing itself
// is internal/stdformat's.
type NumberParseError struct {
	// Tag is 1..6 in std/localize.nomi's declaration order. 0 means never
	// constructed.
	Tag uint8
	// At is `Unexpected`'s and `Grouping`'s grapheme index.
	At int64
	// Text is `Unexpected`'s offending text.
	Text string
	// Max is `TooManyFractionDigits`' minor digits.
	Max int64
	// Expected and Found are `WrongCurrency`'s currency and the text found
	// where it goes.
	Expected Currency
	Found    string
}

// Tag values for NumberParseError, in std/localize.nomi's declaration order.
const (
	TagNumberParseEmpty uint8 = iota + 1
	TagNumberParseUnexpected
	TagNumberParseGrouping
	TagNumberParseOutOfRange
	TagNumberParseTooManyFractionDigits
	TagNumberParseWrongCurrency
)

// ListStyle is `std/strings.ListStyle`: which list pattern
// `String.join_list` uses (And, Or, Unit), a bare enum.
type ListStyle struct {
	Tag uint8
}

// ListWidth is `std/strings.ListWidth`: how long the list pattern's words
// are (Wide, Short, Narrow), a bare enum.
type ListWidth struct {
	Tag uint8
}

// DurationWidth is `std/duration.DurationWidth`: how long
// `Duration.format`'s unit words are (Long, Short, Narrow), a bare enum.
type DurationWidth struct {
	Tag uint8
}

// Tag values for DurationWidth, in std/duration.nomi's declaration order.
const (
	TagDurationLong uint8 = iota + 1
	TagDurationShort
	TagDurationNarrow
)

// Tag values for ListStyle and ListWidth, in std/strings.nomi's declaration
// order.
const (
	TagListAnd uint8 = iota + 1
	TagListOr
	TagListUnit
)

const (
	TagListWide uint8 = iota + 1
	TagListShort
	TagListNarrow
)
