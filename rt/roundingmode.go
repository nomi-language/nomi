package rt

import "fmt"

// RoundingMode is Nomi's `std/decimal.RoundingMode`: which way a decimal
// operation resolves a value it cannot represent exactly.
//
// Here for the reason Ordering and Direction are in rt rather than in a
// generated package: it is declared in a std module and named from user
// packages, so no generated package can own it without two packages owning one
// Nomi type. See internal/irbuild/stdenum.go.
//
// It carries no behaviour of its own. The MODE is an argument a rounding
// operation takes, and the arithmetic that reads it lives with the decimal
// representation — which, as of the Decimal anchor, is rt's: see decimal.go's
// Round and DivRound. Until then this comment said the arithmetic "belongs
// with the decimal representation, which this runtime does not have yet", and
// that is now discharged rather than deleted, because it is the reason the
// eight values below are HERE and not in a second enum beside the arithmetic.
//
// What the type buys, and it was true before the arithmetic arrived: measured
// at c0169d1a, six of the sixteen `type reference` sites in the corpus are
// `HalfEven` and two more are `Up` and `Floor`, and every one of them was
// refused because this enum had no representation.
type RoundingMode struct {
	// Tag is 1..8 in std/decimal.nomi's declaration order. 0 means never
	// constructed.
	Tag uint8
}

// Tag values for RoundingMode, in std/decimal.nomi's declaration order.
//
// Spelled out rather than derived, for rt/prelude_test.go's reason: a test
// that compares two computations cannot see a bug in a table both read.
// internal/irbuild
// derives its own tags from stdEnumSpecs' variant ORDER, so these constants and
// that list are two independently-written encodings of one fact and
// TestStdEnumTagsMatchRT holds them equal.
const (
	TagRoundUp          uint8 = 1
	TagRoundDown        uint8 = 2
	TagRoundCeiling     uint8 = 3
	TagRoundFloor       uint8 = 4
	TagRoundHalfUp      uint8 = 5
	TagRoundHalfDown    uint8 = 6
	TagRoundHalfEven    uint8 = 7
	TagRoundUnnecessary uint8 = 8
)

// The eight modes as VALUES, which is what a switch in decimal.go compares
// against. Vars rather than consts because the type is a struct — Go has no
// struct constants — and that is the price of the Tag representation being the
// one internal/irbuild lowers to.
//
// ONE encoding, deliberately: the process holds one type for the eight modes.
//
// The zero value is NOT a mode. With an iota encoding `RoundUp` would be 0, so
// a forgotten assignment would silently mean "round away from zero"; here Tag 0
// is never constructed and every switch over a mode falls through its cases, which
// is the truncating answer — wrong in a way a test can see rather than wrong in
// a way that looks deliberate.
var (
	RoundUp          = RoundingMode{Tag: TagRoundUp}
	RoundDown        = RoundingMode{Tag: TagRoundDown}
	RoundCeiling     = RoundingMode{Tag: TagRoundCeiling}
	RoundFloor       = RoundingMode{Tag: TagRoundFloor}
	RoundHalfUp      = RoundingMode{Tag: TagRoundHalfUp}
	RoundHalfDown    = RoundingMode{Tag: TagRoundHalfDown}
	RoundHalfEven    = RoundingMode{Tag: TagRoundHalfEven}
	RoundUnnecessary = RoundingMode{Tag: TagRoundUnnecessary}
)

// String renders a mode for diagnostics and test output, in std/decimal.nomi's
// spelling. The variant NAMES rather than the tags, because a rounding error
// message quoting "RoundingMode(7)" is unactionable.
func (m RoundingMode) String() string {
	switch m {
	case RoundUp:
		return "Up"
	case RoundDown:
		return "Down"
	case RoundCeiling:
		return "Ceiling"
	case RoundFloor:
		return "Floor"
	case RoundHalfUp:
		return "HalfUp"
	case RoundHalfDown:
		return "HalfDown"
	case RoundHalfEven:
		return "HalfEven"
	case RoundUnnecessary:
		return "Unnecessary"
	default:
		return fmt.Sprintf("RoundingMode(%d)", m.Tag)
	}
}
