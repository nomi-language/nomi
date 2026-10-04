package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestStdEnumRowTagsMatchRtConstants holds the two new rows' tags against the
// constants rt declares for them.
//
// It exists because without it those constants are DEAD. `TestStdEnumTagsMatchRT`
// is written against `stdEnumOrdering` specifically — deliberately, since the `<`
// lowering reads `rt.TagLess` by name — so rt's `TagRoundHalfEven` and `TagNFD`
// would be text nothing reads and nothing checks. That is worse than having no
// constants at all: a reader seeing them beside Ordering's would reasonably
// assume the same guard covers them, and renumbering one would be silent.
//
// The pairing below is written out by NAME AND NUMBER rather than derived from
// the spec, for rt/prelude_test.go's reason: this is the second of two
// independent encodings of one fact, and deriving it from the first would make
// the test an identity. The first encoding is stdEnumSpecs' variant ORDER, which
// is what the emitted composite literals come from; a permuted table would be a
// wrong answer with no Go compile error, so somebody has to state the expected
// numbering somewhere that a permutation contradicts.
func TestStdEnumRowTagsMatchRtConstants(t *testing.T) {
	cases := []struct {
		nomi     string
		expected map[string]int
	}{{
		nomi: "RoundingMode",
		expected: map[string]int{
			"Up": int(rt.TagRoundUp), "Down": int(rt.TagRoundDown),
			"Ceiling": int(rt.TagRoundCeiling), "Floor": int(rt.TagRoundFloor),
			"HalfUp": int(rt.TagRoundHalfUp), "HalfDown": int(rt.TagRoundHalfDown),
			"HalfEven": int(rt.TagRoundHalfEven), "Unnecessary": int(rt.TagRoundUnnecessary),
		},
	}, {
		nomi: "NormalForm",
		expected: map[string]int{
			"NFC": int(rt.TagNFC), "NFD": int(rt.TagNFD),
			"NFKC": int(rt.TagNFKC), "NFKD": int(rt.TagNFKD),
		},
	}, {
		// std/supervisors' Restart. Three bare variants, so the guard here is
		// entirely about the ORDER agreeing with rt's constants: the spec's
		// variant list is what the emitted literals follow and rt's constants
		// are an independently-written second encoding of the same numbering.
		nomi: "Restart",
		expected: map[string]int{
			"Temporary": int(rt.TagTemporary),
			"Transient": int(rt.TagTransient),
			"Permanent": int(rt.TagPermanent),
		},
	}}
	defs := stdEnumDefs()
	for _, tc := range cases {
		t.Run(tc.nomi, func(t *testing.T) {
			var d *typeDef
			for _, cand := range defs {
				if cand.nomi == tc.nomi {
					d = cand
				}
			}
			if d == nil {
				t.Fatalf("no shared def for %s, so the spec row is gone", tc.nomi)
			}
			if len(d.variants) != len(tc.expected) {
				t.Fatalf("%d variants, want %d — a variant was added or removed in std "+
					"and rt's constants were not moved with it", len(d.variants), len(tc.expected))
			}
			for _, v := range d.variants {
				want, known := tc.expected[v.nomi]
				if !known {
					t.Errorf("variant %q is not one rt declares a tag for", v.nomi)
					continue
				}
				if v.tag != want {
					t.Errorf("%s.%s has tag %d, rt says %d — the spec's variant ORDER and "+
						"rt's constants disagree, and the emitted literals follow the spec",
						tc.nomi, v.nomi, v.tag, want)
				}
			}
			// Tag 0 stays reserved invalid, so a Go zero value is detectably
			// never-constructed. Asserted rather than inferred from the map.
			for _, v := range d.variants {
				if v.tag == int(rt.TagInvalid) {
					t.Errorf("%s.%s was assigned the reserved invalid tag", tc.nomi, v.nomi)
				}
			}
		})
	}
}
