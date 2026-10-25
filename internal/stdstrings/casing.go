package stdstrings

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

//go:generate go run ./casinggen/gen

// Case mapping as Unicode's SpecialCasing.txt gives it: the full mappings
// (ß upper-cases to SS, İ lower-cases to i̇), the final sigma, and the
// rules of the languages it names. Turkish and Azeri pair dotted and
// dotless i (I↔ı, İ↔i); Lithuanian keeps the dot of i under an accent
// (Í lower-cases to i̇́, and the dot is dropped again when upper-casing).
// Everything else is Go's unicode package, Unicode 17 like the tables in
// casing_tables.go.

// caseTable is code points and what they map to, sorted by code point:
// static data, which a map literal would build when the program starts.
type caseTable []struct {
	r rune
	s string
}

// lookup is what r maps to, and whether the table lists it.
func (t caseTable) lookup(r rune) (string, bool) {
	i := sort.Search(len(t), func(i int) bool { return t[i].r >= r })
	if i < len(t) && t[i].r == r {
		return t[i].s, true
	}
	return "", false
}

// FFIToUpperIn is `String.to_upper_in`.
func FFIToUpperIn(s, locale string) string { return UpperIn(s, languageOf(locale)) }

// FFIToLowerIn is `String.to_lower_in`.
func FFIToLowerIn(s, locale string) string { return LowerIn(s, languageOf(locale)) }

// FFIFoldCase is `String.fold_case`.
func FFIFoldCase(s string) string { return FoldCase(s) }

// languageOf is a tag's language subtag.
func languageOf(tag string) string {
	lang, _, _ := strings.Cut(tag, "-")
	return strings.ToLower(lang)
}

func turkic(lang string) bool { return lang == "tr" || lang == "az" }

// UpperIn is s upper-cased as a reader of lang (a language subtag) writes
// it.
func UpperIn(s, lang string) string {
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		switch {
		case turkic(lang) && r == 'i':
			b.WriteRune('İ')
			continue
		case lang == "lt" && r == 0x0307 && afterSoftDotted(rs, i):
			continue
		}
		if m, ok := fullUpper.lookup(r); ok {
			b.WriteString(m)
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// LowerIn is s lower-cased as a reader of lang writes it.
func LowerIn(s, lang string) string {
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		switch {
		case turkic(lang) && r == 0x0130:
			b.WriteRune('i')
			continue
		case turkic(lang) && r == 0x0307 && afterI(rs, i):
			continue
		case turkic(lang) && r == 'I' && !beforeDot(rs, i):
			b.WriteRune('ı')
			continue
		case lang == "lt" && (r == 'I' || r == 'J' || r == 0x012E) && moreAbove(rs, i):
			b.WriteRune(unicode.ToLower(r))
			b.WriteRune(0x0307)
			continue
		case lang == "lt" && r > 0x7f && lithuanianDotted(rs, i) != "":
			b.WriteString(lithuanianDotted(rs, i))
			continue
		case r == 0x03A3:
			if finalSigma(rs, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
			continue
		}
		if m, ok := fullLower.lookup(r); ok {
			b.WriteString(m)
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// lithuanianDotted is the Lithuanian lower case of rs[i] when it is Ì, Í
// or Ĩ, which SpecialCasing lists, or any other capital I or J whose
// decomposition carries a mark above (Ȋ), as the More_Above rule reads its
// canonical equivalent: the small letter, its marks below, a dot above,
// and its marks above. Otherwise it is "".
func lithuanianDotted(rs []rune, i int) string {
	d := []rune(norm.NFD.String(string(rs[i])))
	if len(d) < 2 || (d[0] != 'I' && d[0] != 'J') || !moreAbove(append(d, rs[i+1:]...), 0) {
		return ""
	}
	out := []rune{unicode.ToLower(d[0])}
	k := 1
	for k < len(d) && ccc(d[k]) < 230 {
		out = append(out, d[k])
		k++
	}
	out = append(out, 0x0307)
	return string(append(out, d[k:]...))
}

// FoldCase is s with Unicode's full case folding, the same for every
// language: two strings that differ only in case fold to the same string
// (Straße and STRASSE both fold to strasse).
func FoldCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if m, ok := fullFold.lookup(r); ok {
			b.WriteString(m)
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func ccc(r rune) uint8 {
	if r < 0x300 {
		return 0
	}
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	return norm.NFD.Properties(buf[:n]).CCC()
}

// afterSoftDotted: a Soft_Dotted character (i, j, ...) comes before rs[i],
// with no character of combining class 0 or 230 between.
func afterSoftDotted(rs []rune, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if unicode.Is(unicode.Soft_Dotted, rs[j]) {
			return true
		}
		if c := ccc(rs[j]); c == 0 || c == 230 {
			return false
		}
	}
	return false
}

// afterI: a capital I comes before rs[i], with no character of combining
// class 0 or 230 between.
func afterI(rs []rune, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if rs[j] == 'I' {
			return true
		}
		if c := ccc(rs[j]); c == 0 || c == 230 {
			return false
		}
	}
	return false
}

// beforeDot: a combining dot above follows rs[i], with only characters of
// combining classes other than 0 and 230 between.
func beforeDot(rs []rune, i int) bool {
	for j := i + 1; j < len(rs); j++ {
		if rs[j] == 0x0307 {
			return true
		}
		if c := ccc(rs[j]); c == 0 || c == 230 {
			return false
		}
	}
	return false
}

// moreAbove: a character of combining class 230 follows rs[i], with no
// character of class 0 or 230 between.
func moreAbove(rs []rune, i int) bool {
	for j := i + 1; j < len(rs); j++ {
		c := ccc(rs[j])
		if c == 230 {
			return true
		}
		if c == 0 {
			return false
		}
	}
	return false
}

// finalSigma: a cased letter and then any case-ignorable characters come
// before rs[i], and no case-ignorable characters and then a cased letter
// come after it.
func finalSigma(rs []rune, i int) bool {
	// A character may be both cased and case-ignorable (ᶻ); it counts as
	// the cased letter.
	before := false
	for j := i - 1; j >= 0; j-- {
		if cased(rs[j]) {
			before = true
			break
		}
		if !caseIgnorable(rs[j]) {
			break
		}
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(rs); j++ {
		if cased(rs[j]) {
			return false
		}
		if !caseIgnorable(rs[j]) {
			return true
		}
	}
	return true
}

func cased(r rune) bool {
	return unicode.IsLower(r) || unicode.IsUpper(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

// wordMid are the characters Word_Break gives MidLetter, MidNumLet or
// Single_Quote, which are case-ignorable.
var wordMid = map[rune]bool{
	0x0027: true, 0x002E: true, 0x003A: true, 0x00B7: true, 0x0387: true, 0x055F: true, 0x05F4: true,
	0x2018: true, 0x2019: true, 0x2024: true, 0x2027: true, 0xFE13: true, 0xFE52: true, 0xFE55: true,
	0xFF07: true, 0xFF0E: true, 0xFF1A: true,
}

func caseIgnorable(r rune) bool {
	return wordMid[r] || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}
