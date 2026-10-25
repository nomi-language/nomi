// Package collation compares strings as the reader of a language orders
// them: the Unicode Collation Algorithm (UTS #10) over CLDR's root
// collation, with CLDR's per-locale tailorings (UTS #35 part 5).
//
// The design is ICU's, as ICU4X ports it: one root table every locale
// shares, and per locale a tailoring that overlays it. internal/cldrgen
// builds both from the pinned CLDR release (FractionalUCA.txt and the
// collation/*.xml rules, compiled by package collgen) and writes them into
// the locale data a program carries; this package decodes them and
// compares. It links no generator and no rule parser.
//
// A string is normalized to NFD, mapped to collation elements (CEs) through
// the tailoring and then the root (contractions, discontiguous contractions,
// prefix contexts, expansions, implicit weights for Han and unassigned code
// points, and numeric ordering when asked), and the CEs are compared level
// by level as the options say: strength, alternate handling, case first,
// case level, backwards secondary, and script reordering.
package collation

// CE is a collation element, laid out as ICU lays out its 64-bit CEs:
//
//	bits 63..32  primary weight, left-aligned (FractionalUCA's bytes)
//	bits 31..16  secondary weight, left-aligned
//	bits 15..14  case bits: 0 lowercase or uncased, 1 mixed, 2 uppercase
//	bits 13..8   tertiary weight's first byte (6 bits)
//	bits  7..6   quaternary weight (tailored `<<<<` differences)
//	bits  5..0   tertiary weight's second byte (6 bits)
//
// A weight of zero at a level means the CE is ignorable at that level.
type CE uint64

// The masks and common weights of a CE's low 16 bits, ICU's.
const (
	// CaseMask is the case bits.
	CaseMask = 0xc000
	// OnlyTertiaryMask is the tertiary weight without case or quaternary
	// bits.
	OnlyTertiaryMask = 0x3f3f
	// QuaternaryMask is the quaternary bits.
	QuaternaryMask = 0xc0
	// CommonWeight16 is the common secondary and tertiary weight (05).
	CommonWeight16 = 0x0500
	// BeforeWeight16 is the weight a `[before 2]` or `[before 3]`
	// tailoring inserts below common.
	BeforeWeight16 = 0x0100
	// CommonSecTer is a common secondary and tertiary, as a CE's low 32
	// bits.
	CommonSecTer = CommonWeight16<<16 | CommonWeight16
)

// MakeCE is the CE of a primary, secondary and low 16 bits.
func MakeCE(p uint32, s uint16, lower16 uint16) CE {
	return CE(uint64(p)<<32 | uint64(s)<<16 | uint64(lower16))
}

// PrimaryCE is the CE with primary p and common secondary and tertiary.
func PrimaryCE(p uint32) CE { return MakeCE(p, CommonWeight16, CommonWeight16) }

// Primary is the CE's primary weight.
func (c CE) Primary() uint32 { return uint32(c >> 32) }

// Secondary is the CE's secondary weight.
func (c CE) Secondary() uint16 { return uint16(c >> 16) }

// Lower16 is the CE's case, tertiary and quaternary bits.
func (c CE) Lower16() uint16 { return uint16(c) }

// Tertiary is the CE's tertiary weight without case or quaternary bits.
func (c CE) Tertiary() uint16 { return uint16(c) & OnlyTertiaryMask }

// Case is the CE's case bits, 0 to 2.
func (c CE) Case() uint8 { return uint8(uint16(c) >> 14) }

// Quaternary is the CE's quaternary bits, 0 to 3.
func (c CE) Quaternary() uint8 { return uint8(uint16(c)>>6) & 3 }

// The strengths, as ICU numbers them: a smaller one is stronger.
const (
	Primary = iota
	Secondary
	Tertiary
	Quaternary
	Identical = 15
)

// Strength is the strongest level at which the CE is not ignorable:
// Primary for a CE with a primary weight, and so on; Identical for a
// completely ignorable CE.
func (c CE) Strength() int {
	switch {
	case c.Primary() != 0:
		return Primary
	case c.Secondary() != 0:
		return Secondary
	case c.Lower16() != 0:
		return Tertiary
	}
	return Identical
}

// The primaries the root collation leaves to code points it does not list
// (UTS #10 section 10.1): a Han ideograph of the core blocks, any other
// Han ideograph, and an unassigned code point, in that order, each in code
// point order. Each Han primary leaves a gap after it, so a tailoring can
// put characters after a Han character.
const (
	// FirstImplicitPrimary is CLDR's [first implicit].
	FirstImplicitPrimary = 0xe0000000
	// coreHanBase is a core Han ideograph's primary less its code point
	// shifted by coreHanShift.
	coreHanBase  = 0xe0000000
	coreHanShift = 8
	// otherHanBase is any other Han ideograph's.
	otherHanBase  = 0xe1000000
	otherHanShift = 6
	// UnassignedPrimary is the first unassigned code point's primary less
	// its code point: CLDR's "unassigned first primary".
	UnassignedPrimary = 0xe4000000
	// numericLead is the first byte of the primaries numeric ordering
	// gives a run of digits: FractionalUCA's "lead byte for numeric
	// sorting".
	numericLead = 0x10
	// tempLead marks a CE that stands for a node of a tailoring being
	// built (package collgen): no real primary starts with this byte.
	tempLead = 0x01
)

// IsTemp reports whether c stands for a node of a tailoring being built.
func IsTemp(c CE) bool { return c>>56 == tempLead }
