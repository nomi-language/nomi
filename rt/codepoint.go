package rt

// std/codepoints.Codepoint — a Unicode scalar value — and the two externs over
// it.
//
// # Why this is an int64 and not an int32
//
// std declares `pub opaque type Codepoint Int`, so the Nomi type's inner type is
// `Int`, which is int64 everywhere in this project. A Go `int32`/`rune` here
// would be a narrower representation than the declaration, and internal/irbuild's
// opaque spec asserts the inner kind is `Int` — so the two would disagree, and a
// `Codepoint(n)` built from an int64 expression would silently truncate. The
// domain fits in 21 bits either way; matching the declaration is what stops the
// two widths from being two facts.
//
// Nominally its own type, structurally int64 — the same shape rt/opaque.go gives
// Duration and Instant, and for the same reason: a Go defined type is exactly
// what a Nomi distinct type maps to, and rt is linked by everything, so a type
// declared here is package-neutral.
//
// # What this file does not do
//
// It does not validate. `Codepoint.from_int` is ordinary Nomi source in
// std/codepoints.nomi — the range test and the UTF-16 surrogate-block test are
// Nomi `if` expressions returning `Maybe<Codepoint>` — and it lowers, so the VM
// runs that predicate rather than a Go copy of it. A second validator here would be the divergence
// rt/opaque.go's header warns about for formatters.
//
// So every Codepoint reaching CodepointToString has been through that predicate,
// which is what makes `string(rune(cp))` total: the surrogate range D800–DFFF and
// everything above 0x10FFFF are unconstructible from Nomi.
type Codepoint int64

// CodepointToString is `Codepoint.to_string`: the one-codepoint string.
//
// One expression, so there is no formatting decision to keep in step, unlike
// DurationToString's unit ladder.
func CodepointToString(cp Codepoint) string { return string(rune(cp)) }

// StringToCodepoints is `String.to_codepoints`: the string's Unicode scalar
// values in order, as a `List<Codepoint>`.
//
// This decodes Unicode scalar values, independently of grapheme boundaries.
// The decoding is StringToCodepointCells.
//
// Built back-to-front so the shared tails are the natural ones and no cell is
// copied — StringSplit's shape. An empty string answers nil, which is the empty
// list rather than a special case.
//
// Ranging a Go string yields U+FFFD for each invalid UTF-8 byte, so a String
// holding arbitrary bytes produces replacement characters rather than an error.
// That is why this cannot be written as a decode that fails: a Nomi String is a Go string.
func StringToCodepoints(s string) *List[Codepoint] {
	return StringToCodepointCells[Codepoint, List[Codepoint]](s, func(cp Codepoint) Codepoint { return cp })
}

// StringToCodepointCells decodes UTF-8 and wraps each scalar in the consumer's
// value representation, preserving its recursive list-cell type.
func StringToCodepointCells[T any, N ListCellShape[T, N]](s string, wrap func(Codepoint) T) *N {
	runes := []rune(s)
	var out *N
	for i := len(runes) - 1; i >= 0; i-- {
		out = ConsCell[T, N](wrap(Codepoint(runes[i])), out)
	}
	return out
}
