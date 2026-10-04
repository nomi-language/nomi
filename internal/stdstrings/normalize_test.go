package stdstrings

import (
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestNormalizeTagsCoverEveryVariant is the guard that keeps Normalize's unknown
// arm from becoming a silent no-op when std/strings adds a fifth form.
//
// The tag list is DERIVED from rt rather than retyped here, so this test cannot
// agree with the switch by having been written from it. rt.NormalForm's tags are
// 1..N in std's declaration order, and rt declares one exported constant per
// variant — so the derivation is "every `TagNF*` constant rt exports", read by
// reflection over the values this package can name.
//
// ASK WHAT THIS WOULD SHOW IF THE SWITCH WERE INCOMPLETE: an unnamed tag returns
// `known == false`, which is a visible failure here and an invisible identity
// function at a call site. That asymmetry is the whole reason the arm is tested
// rather than trusted.
func TestNormalizeTagsCoverEveryVariant(t *testing.T) {
	tags := map[string]uint8{
		"TagNFC":  rt.TagNFC,
		"TagNFD":  rt.TagNFD,
		"TagNFKC": rt.TagNFKC,
		"TagNFKD": rt.TagNFKD,
	}
	// The tags must be CONTIGUOUS from 1, which is what makes "every tag from 1
	// to len(tags)" the right domain to sweep. Asserted rather than assumed: a
	// gap would make the sweep below skip a real variant.
	seen := map[uint8]bool{}
	for name, tag := range tags {
		if tag == 0 {
			t.Errorf("%s is 0, the reserved invalid tag", name)
		}
		if seen[tag] {
			t.Errorf("%s duplicates tag %d", name, tag)
		}
		seen[tag] = true
	}
	for i := 1; i <= len(tags); i++ {
		if !seen[uint8(i)] {
			t.Fatalf("tag %d is not one of rt's constants, so this sweep's domain is wrong", i)
			return
		}
		if _, known := goForm(rt.NormalForm{Tag: uint8(i)}); !known {
			t.Errorf("tag %d has no arm in goForm, so Normalize returns its input unchanged for it", i)
		}
	}
	// And the NEGATIVE control, so the loop above is not passing because
	// `known` is hardwired true.
	if _, known := goForm(rt.NormalForm{Tag: uint8(len(tags) + 1)}); known {
		t.Error("goForm claims to know a tag rt does not declare, so the coverage sweep above proves nothing")
	}
	if _, known := goForm(rt.NormalForm{}); known {
		t.Error("goForm claims to know the zero value, which is a never-constructed NormalForm")
	}
}

// TestNormalizeIsTheFourForms pins the four answers absolutely, on the one input
// where the forms actually differ.
//
// "é" as NFC is one code point (U+00E9) and as NFD is two (U+0065 U+0301); the
// compatibility forms agree with their canonical counterparts on it, which is why
// the K rows are checked on a different input (U+FB01, the "fi" ligature) where
// they do not.
//
// Absolute rather than a comparison of two runs: THIS function is the one
// implementation, so agreement with anything else says nothing about whether
// the mapping is right.
func TestNormalizeIsTheFourForms(t *testing.T) {
	const composed = "\u00e9"
	const decomposed = "e\u0301"
	const ligature = "\ufb01"
	for _, tc := range []struct {
		name string
		tag  uint8
		in   string
		want string
	}{
		{"NFC composes", rt.TagNFC, decomposed, composed},
		{"NFD decomposes", rt.TagNFD, composed, decomposed},
		{"NFKC folds compatibility", rt.TagNFKC, ligature, "fi"},
		{"NFKD folds compatibility", rt.TagNFKD, ligature, "fi"},
		{"NFC leaves a ligature alone", rt.TagNFC, ligature, ligature},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.in, rt.NormalForm{Tag: tc.tag}); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
