package rt

// NormalForm is Nomi's `std/strings.NormalForm`: which Unicode normalization
// form `String.normalize` targets.
//
// Here for RoundingMode's reason — declared in a std module, named from user
// modules, so no one module can own it. See internal/irbuild/stdenum.go.
//
// The normalization itself is deliberately NOT here. `String.normalize` is a
// `host fn` and binding it is a separate decision with a real dependency
// (`golang.org/x/text/unicode/norm`, which `rt` does not require today), so a
// half-implemented `Normalize` would be a Go function whose answer disagreed
// with Unicode normalization on the first composed character anybody passed
// it. What
// this type buys is that the FORM is nameable: measured at c0169d1a,
// 04-scalars-and-text/strings_test.nomi refuses `type reference | NFC` twice and
// `NFD` once, and 12-derives-and-standard-interfaces/display_debug_test.nomi
// refuses `type-qualified reference | NormalForm.NFC` — four sites over two
// files that were blocked by the absence of a three-field struct.
type NormalForm struct {
	// Tag is 1..4 in std/strings.nomi's declaration order. 0 means never
	// constructed.
	Tag uint8
}

// Tag values for NormalForm, in std/strings.nomi's declaration order. Two
// independently-written encodings of one fact, held equal by
// TestStdEnumTagsMatchRT; see rt/roundingmode.go.
const (
	TagNFC  uint8 = 1
	TagNFD  uint8 = 2
	TagNFKC uint8 = 3
	TagNFKD uint8 = 4
)
