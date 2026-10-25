package stdstrings

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

func TestCaseMappingInALanguage(t *testing.T) {
	for _, tc := range []struct {
		fn         func(string, string) string
		lang, s, w string
	}{
		{UpperIn, "tr", "istanbul", "İSTANBUL"},
		{UpperIn, "az", "iı", "İI"},
		{UpperIn, "en", "istanbul", "ISTANBUL"},
		{LowerIn, "tr", "İSTANBUL", "istanbul"},
		{LowerIn, "tr", "DİYARBAKIR", "diyarbakır"},
		{LowerIn, "tr", "İ", "i"},
		{LowerIn, "en", "İ", "i̇"},
		{LowerIn, "en", "DIYARBAKIR", "diyarbakir"},
		{UpperIn, "de", "straße", "STRASSE"},
		{UpperIn, "en", "ﬀ", "FF"},
		// Lithuanian keeps the dot of i under an accent, and drops it again.
		{LowerIn, "lt", "Í", "i̇́"},
		{LowerIn, "lt", "Ì", "i̇̀"},
		{LowerIn, "lt", "IS", "is"},
		{UpperIn, "lt", "i̇́", "Í"},
		{UpperIn, "en", "i̇́", "İ́"},
		// A capital sigma ends a word as ς.
		{LowerIn, "el", "ΟΔΟΣ", "οδος"},
		{LowerIn, "el", "ΟΔΟΣ ΣΟΦΙΑΣ.", "οδος σοφιας."},
		{LowerIn, "el", "Σ", "σ"},
	} {
		if got := tc.fn(tc.s, tc.lang); got != tc.w {
			t.Errorf("%q in %s: %q (%U), want %q (%U)", tc.s, tc.lang, got, []rune(got), tc.w, []rune(tc.w))
		}
	}
	if got := FoldCase("Straße ẞ ΣΑΣ ﬁ K"); got != "strasse ss σασ fi k" {
		t.Errorf("fold: %q", got)
	}
}

// The mappings agree with x/text's cases package, which implements the same
// SpecialCasing rules, on strings of characters both know (Unicode 15).
func TestCaseMappingAgreesWithXText(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var pool []rune
	for r := rune(0x20); r < 0x2000; r++ {
		// Modifier letters are cased and case-ignorable at once, where
		// x/text's final sigma reads them as case-ignorable only.
		if unicode.In(r, unicode.L, unicode.M) && !unicode.Is(unicode.Lm, r) {
			pool = append(pool, r)
		}
	}
	pool = append(pool, 'I', 'i', 'J', 'j', 0x0130, 0x0131, 0x0307, 0x0301, 0x0300, 0x03A3, 0x00DF, 'Į', ' ', '.', '\'')
	for _, lang := range []string{"en", "tr", "az", "lt", "de"} {
		tag := language.MustParse(lang)
		up, lo, fold := cases.Upper(tag), cases.Lower(tag), cases.Fold()
		for n := 0; n < 20000; n++ {
			var b strings.Builder
			for k := rng.Intn(6); k >= 0; k-- {
				b.WriteRune(pool[rng.Intn(len(pool))])
			}
			s := norm.NFC.String(b.String())
			if got, want := UpperIn(s, lang), up.String(s); got != want {
				t.Errorf("upper %s %U: %U, x/text %U", lang, []rune(s), []rune(got), []rune(want))
			}
			if got, want := LowerIn(s, lang), lo.String(s); norm.NFC.String(got) != norm.NFC.String(want) {
				t.Errorf("lower %s %U: %U, x/text %U", lang, []rune(s), []rune(got), []rune(want))
			}
			// CaseFolding.txt folds Cherokee to its capitals, for stability;
			// x/text folds it to lower case.
			cherokee := strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Cherokee, r) }) >= 0
			if got, want := FoldCase(s), fold.String(s); lang == "en" && !cherokee && got != want {
				t.Errorf("fold %U: %U, x/text %U", []rune(s), []rune(got), []rune(want))
			}
			if t.Failed() {
				return
			}
		}
	}
}
