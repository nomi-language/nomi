package cldrgen

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/nomi-language/nomi/internal/collation"
)

// TestCollationMatchesICU compares every compiled collation type, under
// five sets of options, with ICU's collator over the same CLDR release,
// pair by pair over strings made from the characters its rules name and
// common Latin letters, and the root order over random strings from all of
// Unicode. It runs when NOMI_ICU_ORACLE names a build of
// testdata/icuoracle.c, which links macOS's libicucore (ICU 78, CLDR 48);
// it is how a CLDR upgrade checks the rule compiler.
//
// Three differences are known and skipped, each Nomi following UTS #10:
// ICU4C's root orders Han by radical and stroke where Nomi, ICU4X's default
// and CLDR's conformance files order it by code point; ICU orders a string
// that needs canonical reordering (is not FCD) differently from its normalized form in some
// tailorings (Turkish `â̧`); and ICU keeps U+0901 and U+0A01 after a
// variable character in shifted mode, which UTS #10 makes ignorable.
func TestCollationMatchesICU(t *testing.T) {
	oracle := os.Getenv("NOMI_ICU_ORACLE")
	if oracle == "" {
		t.Skip("set NOMI_ICU_ORACLE to a build of testdata/icuoracle.c")
	}
	rules, err := ReadCollationRules("data")
	if err != nil {
		t.Fatal(err)
	}
	type job struct{ tag, typ, icu, text string }
	jobs := []job{{"und", "", "und", ""}}
	for l, types := range rules.Rules {
		for typ, text := range types {
			if !strings.HasPrefix(typ, "private-") {
				jobs = append(jobs, job{cldrTag(l), typ, l + "@collation=" + typ, text})
			}
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].tag+jobs[i].typ < jobs[j].tag+jobs[j].typ })
	settings := []struct {
		attrs  string
		change func(*collation.Settings)
	}{
		{"", nil},
		{"1 20 5 3", func(s *collation.Settings) { s.Shifted, s.Strength = true, collation.Quaternary }},
		{"2 25", func(s *collation.Settings) { s.CaseFirst = collation.CaseFirstUpper }},
		{"5 15 7 17", func(s *collation.Settings) { s.Strength, s.Numeric = collation.Identical, true }},
		{"5 1 3 17", func(s *collation.Settings) { s.Strength, s.CaseLevel = collation.Secondary, true }},
	}
	pairs, skippedLocales := 0, 0
	for _, j := range jobs {
		words := icuCorpus(rand.New(rand.NewSource(1)), j.text, 300)
		for _, st := range settings {
			c := collatorFor(t, j.tag, j.typ, st.change)
			signs, ok := icuSigns(t, oracle, j.icu, st.attrs, words)
			if !ok {
				skippedLocales++
				break
			}
			pairs += compareWithICU(t, fmt.Sprintf("%s %s %q", j.tag, j.typ, st.attrs), c, words, signs, st.change != nil)
		}
	}
	// The root order over random characters of every script but Han.
	rng := rand.New(rand.NewSource(7))
	var pool []rune
	for r := rune(0x20); r < 0x32000; r++ {
		if unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Zs) && !hanLike(r) {
			pool = append(pool, r)
		}
	}
	var words []string
	for len(words) < 1500 {
		var b strings.Builder
		for k := rng.Intn(3); k >= 0; k-- {
			b.WriteRune(pool[rng.Intn(len(pool))])
		}
		words = append(words, b.String())
	}
	for _, st := range settings[:2] {
		signs, _ := icuSigns(t, oracle, "und", st.attrs, words)
		pairs += compareWithICU(t, "root "+st.attrs, collatorFor(t, "und", "", st.change), words, signs, st.change != nil)
	}
	t.Logf("%d pairs agree with ICU; %d locales ICU has no data for", pairs, skippedLocales)
}

// hanLike reports whether r is Han, or a character the root maps to a Han
// character's CEs (a radical, a compatibility ideograph).
func hanLike(r rune) bool {
	return unicode.Is(unicode.Han, r) || (r >= 0x2E80 && r <= 0x2FDF) || (r >= 0x3190 && r <= 0x33FF) ||
		(r >= 0xF900 && r <= 0xFAFF) || (r >= 0x1F200 && r <= 0x1F2FF) || (r >= 0x2F800 && r <= 0x2FA1F)
}

func compareWithICU(t *testing.T, name string, c *collation.Collator, words []string, signs [][]byte, shifted bool) int {
	t.Helper()
	known := func(s string) bool {
		return !fcd(s) || (shifted && strings.ContainsAny(s, "ँਁ"))
	}
	bad, n := 0, 0
	for a := range words {
		for b := range words {
			if known(words[a]) || known(words[b]) {
				continue
			}
			n++
			got := byte('=')
			switch c.Compare(words[a], words[b]) {
			case -1:
				got = '<'
			case 1:
				got = '>'
			}
			if got != signs[a][b] {
				bad++
				if bad <= 3 {
					t.Errorf("%s: %q %c %q, ICU %c", name, words[a], got, words[b], signs[a][b])
				}
			}
		}
	}
	if bad > 3 {
		t.Errorf("%s: %d pairs differ in all", name, bad)
	}
	return n - bad
}

// fcd reports whether s needs no canonical reordering across its
// characters' decompositions (UTN #5's FCD), the strings ICU collates
// without normalizing them first.
func fcd(s string) bool {
	var each strings.Builder
	for _, r := range s {
		each.WriteString(norm.NFD.String(string(r)))
	}
	return each.String() == norm.NFD.String(s)
}

// icuSigns runs the oracle over words, and is false when ICU has no data
// for the locale.
func icuSigns(t *testing.T, oracle, loc, attrs string, words []string) ([][]byte, bool) {
	t.Helper()
	cmd := exec.Command(oracle)
	cmd.Stdin = strings.NewReader(loc + "\n" + attrs + "\n" + strings.Join(words, "\n") + "\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle: %v %s", err, errb.String())
	}
	if strings.Contains(errb.String(), "status -127") && loc != "und" {
		return nil, false
	}
	return bytes.Split(bytes.TrimSuffix(out.Bytes(), []byte("\n")), []byte("\n")), true
}

// icuCorpus is single characters and short random strings of the
// characters rules name and common Latin letters, digits and punctuation.
func icuCorpus(rng *rand.Rand, rules string, n int) []string {
	set := map[rune]bool{}
	for _, r := range rules {
		if unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) {
			set[r] = true
		}
	}
	for _, r := range "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZáàâäãåæçéèêëíìîïñóòôöõøœúùûüýÿßðþ0123456789 -'.,_" {
		set[r] = true
	}
	rs := make([]rune, 0, len(set))
	for r := range set {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	var words []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			words = append(words, s)
		}
	}
	for _, r := range rs {
		if len(words) < n/3 {
			add(string(r))
		}
	}
	for len(words) < n {
		var b strings.Builder
		for k := rng.Intn(4); k >= 0; k-- {
			b.WriteRune(rs[rng.Intn(len(rs))])
		}
		add(b.String())
	}
	return words
}
