package irbuild

import (
	"strings"
	"testing"
)

// `std/codepoints.Codepoint` as an anchored stdlib opaque newtype.
//
// Nine of the eleven declarations over Codepoint are ordinary Nomi bodies that
// LOWER, so the program runs std's own validation predicate rather than a Go
// copy of it. The two rt functions and the nominal identity of the type are
// what the absolute pins below are for.

// TestCodepoint_PinnedText pins the reference bytes absolutely.
//
// Every group is a rule with a wrong implementation attached. For `Codepoint`
// the tempting wrong implementation is an unvalidated int64 newtype.
//
//	Some(65) … Some(57344)   from_int VALIDATES: the two range bounds
//	                         (1_114_112, -1) and both edges of the UTF-16
//	                         surrogate block (55_296, 57_343) answer None, and
//	                         both neighbours outside it (55_295, 57_344) answer
//	                         Some. An int64 newtype with no predicate answers
//	                         Some to all nine
//	A é 你 💩                 to_string is rt.CodepointToString, at one, two,
//	                         three and four UTF-8 bytes. A `byte` or an int32
//	                         truncation anywhere in the chain changes these
//	Codepoint(…)             inspect is a Nomi body over interpolation
//	Some(66) Some(57344) None   next SKIPS the surrogate block, so it is not
//	                         `n + 1`; and it is None at the top of the domain
//	Some(3) Some(1) Some(0)  steps_between DISCOUNTS the 2048 surrogates. Row 2
//	                         is chosen so a naive `b - a` and the correct answer
//	                         DISAGREE: 57_344 - 55_295 is 2_049 raw and 1 after
//	                         the skip
//	Some(67) Some(57344) Some(55295) None   step_by, forwards and backwards
//	                         across the block, and out of the domain
//	True False               equal? over the anchored def
//	104-…-.                  String.to_codepoints is rt.StringToCodepoints, and
//	                         the rendering carries ORDER and LENGTH. Row 2 is 6
//	                         BYTES and 5 code points, so a []byte walk prints a
//	                         different string; row 3 is the empty string, which
//	                         is nil rather than a special case; row 4 is one
//	                         astral point
//	True False False False   the rt-built chain against the builder-built one,
//	                         then one differing element, one short chain and one
//	                         long chain — the negative half, without which an
//	                         unconditional True would pass
func TestCodepoint_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"Some(65)", "Some(0)", "Some(1114111)", "None", "None",
		"Some(55295)", "None", "None", "Some(57344)",
		"A", "é", "你", "💩",
		"Codepoint(65)", "Codepoint(1114111)",
		"Some(66)", "Some(57344)", "None",
		"Some(3)", "Some(1)", "Some(0)",
		"Some(67)", "Some(57344)", "Some(55295)", "None",
		"True", "False",
		"104-101-108-108-111-.",
		"104-233-108-108-111-.",
		".",
		"128169-.",
		"True", "False", "False", "False",
		"",
	}, "\n")
	if got := vmReference(fixture("codepoint.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s",
			got, want)
	}
}

// TestOpaqueSpecsAreLoadBearing asserts opaqueSpecs' own stated bar, so a
// row's justification is evaluated rather than left in prose.
//
// The bar asserted is the table's own, and it is deliberately not
// stdEnumSpecs' "some corpus program mentions it": `NonZeroInt` is mentioned by
// ZERO corpus programs and its row is still load-bearing, because
// `Int.modulo(a: Int, b: NonZeroInt)` cannot be admitted without it. What a row
// is for is letting a SIGNATURE name the type — so that is what is measured:
// per spec, at least one std declaration whose parameter or result kind IS this
// spec's kind, and which therefore could not have been admitted without the row.
//
// Failure means one of two things and the message says both: the row is
// scaffolding (delete it), or std stopped naming the type (the row's reason
// moved, so re-derive it).
func TestOpaqueSpecsAreLoadBearing(t *testing.T) {
	idx := stdlibLowering()
	named := make([]map[string]bool, len(opaqueSpecs))
	for i := range opaqueSpecs {
		named[i] = map[string]bool{}
	}
	for key, f := range idx.byKey {
		for i := range opaqueSpecs {
			k := opaqueKind(i)
			if f.result == k {
				named[i][key] = true
			}
			for _, p := range f.params {
				if p == k {
					named[i][key] = true
				}
			}
		}
	}
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		if len(named[i]) == 0 {
			t.Errorf("%s.%s: no std declaration's signature names it, so the row buys nothing — "+
				"either delete it as scaffolding, or std stopped naming the type and the row's reason has moved",
				s.origin, s.nomi)
			continue
		}
		t.Logf("%s.%s named by %d std declaration(s)", s.origin, s.nomi, len(named[i]))
	}
}

// TestOpaqueGoWidthMatchesTheDeclaredInner checks the Go side of every
// opaqueSpec row.
//
// `opaqueSpec.inner` is a claim about two things. matches() asserts the Nomi
// side, `scalarKind(decl.InnerTypeExpr) == s.inner`, so a std edit to
// `opaque type Duration Float` produces no anchor. This test asserts the Go
// side, so `type Codepoint int32` in rt fails here.
//
// It is not caught by output, which is why it needs a guard rather than a
// fixture. Every Codepoint is ≤ 0x10FFFF by construction, so int32 answers
// correctly for every value the type can hold; the wrong width is a wrong
// representation with no wrong answer attached. `Duration` has no such
// bound, so the same mistake there is a silent truncation of any span past ~2.1
// seconds, and the row that would catch it is this one.
//
// Derived from the spec's own `inner` rather than listed per row, so a future row
// over a Float or String inner is covered without an edit here.
func TestOpaqueGoWidthMatchesTheDeclaredInner(t *testing.T) {
	want := goKindFor
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		goKind, known := want[s.inner.tag]
		if !known {
			t.Errorf("%s.%s: inner kind %s has no Go width in this table; add the row rather than skipping it",
				s.origin, s.nomi, s.inner.nomi())
			continue
		}
		if got := s.goType.Kind(); got != goKind {
			t.Errorf("%s.%s: rt's %s is a Go %s, but the spec declares inner %s (Go %s) — "+
				"the width is one fact and these are two places for it",
				s.origin, s.nomi, s.goType, got, s.inner.nomi(), goKind)
		}
	}
}

// TestCodepointLiteral_PinnedText pins codepoint literals end to end: the
// value is std's Codepoint (Display, Debug, equality with a from_int-built
// value), the literal orders, ranges over literals iterate, and literal
// patterns match at the top of a case, inside a variant payload and inside a
// tuple. Each escaped arm (newline, quote, backslash, NUL) matches only its
// own value, so a lexer or parser that decoded one escape as another
// prints the wrong row.
func TestCodepointLiteral_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"A", "Codepoint(65)", "127", "9",
		"True", "False", "True", "False", "True",
		"a", "newline", "quote", "backslash", "nul", "other",
		"some x", "some other", "none",
		"minus 3", "other 4",
		"abcde", "[48, 49, 50, 51, 52]", "abc",
		"",
	}, "\n")
	if got := vmReference(fixture("codepoint_literal.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s",
			got, want)
	}
}
