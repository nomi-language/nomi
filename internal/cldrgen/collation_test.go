package cldrgen

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/collation"
)

var collations = sync.OnceValues(func() (*Collations, error) { return GenerateCollations("data") })

func collatorFor(t *testing.T, tag, typ string, change func(*collation.Settings)) *collation.Collator {
	t.Helper()
	c, err := collations()
	if err != nil {
		t.Fatal(err)
	}
	root, err := collation.DecodeRoot(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	if tag == "und" {
		return collation.New(root, nil, change)
	}
	b, ok := c.Tailorings[tag][typ]
	if !ok {
		t.Fatalf("no %s collation for %s", typ, tag)
	}
	tail, err := collation.DecodeTailoring(b)
	if err != nil {
		t.Fatal(err)
	}
	return collation.New(root, tail, change)
}

func sorted(c *collation.Collator, words []string) []string {
	out := slices.Clone(words)
	sort.SliceStable(out, func(i, j int) bool { return c.Compare(out[i], out[j]) < 0 })
	return out
}

func TestTailoringsOrderAsTheLanguageDoes(t *testing.T) {
	for _, tc := range []struct {
		tag, typ string
		want     string
	}{
		// The root keeps accented letters with their base letter.
		{"und", "", "a ä o ö z"},
		{"und", "", "Mueller Muller Müller"},
		// German phonebook sorts ü as ue.
		{"de", "phonebook", "Mueller Müller Muller"},
		// Swedish puts å, ä and ö after z; ü sorts with y.
		{"sv", "standard", "a o y ü z å ä ö"},
		{"sv", "standard", "Zorn Åberg Ärlig Öberg"},
		// Danish: æ, ø and å after z, aa as å, upper case first.
		{"da", "standard", "Zealand Æble Øl Århus Aarhus"},
		{"da", "standard", "A a B b"},
		// Norwegian (no, nb and nn) too.
		{"no", "standard", "z æ ø å"},
		// Spanish: ñ is a letter after n.
		{"es", "standard", "nube ñu o"},
		{"es", "traditional", "cuna chico dama"},
		// Polish: each accented letter after its base letter.
		{"pl", "standard", "a ą b c ć d e ę f l ł m n ń o ó p s ś t z ź ż"},
		{"pl", "standard", "Lis Łukasz Ślęk Śliwa Zając Źrebiec Żak"},
		// Czech: ch after h, and č, ř, š, ž after their letters.
		{"cs", "standard", "c č h ch i r ř s š z ž"},
		// Lithuanian: y sorts with i.
		{"lt", "standard", "ira yra jis"},
		// Turkish: dotless ı before i.
		{"tr", "standard", "h ı I i İ j"},
		// Chinese: pinyin, Han before Latin.
		{"zh", "pinyin", "阿 李 王 张 赵 a"},
		// Chinese, stroke order: one, two and three strokes.
		{"zh", "stroke", "一 二 三"},
	} {
		c := collatorFor(t, tc.tag, tc.typ, nil)
		want := strings.Fields(tc.want)
		rev := slices.Clone(want)
		slices.Reverse(rev)
		if got := sorted(c, rev); !slices.Equal(got, want) {
			t.Errorf("%s %s: sorted %v, want %v", tc.tag, tc.typ, got, want)
		}
	}
}

// TestCollationDataSizes logs the encoded size of the root, of each
// tailoring over 4 KB, and of the tailorings of a few languages.
func TestCollationDataSizes(t *testing.T) {
	c, err := collations()
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	named := map[string]bool{"de": true, "sv": true, "da": true, "es": true, "pl": true, "cs": true, "lt": true, "tr": true}
	for tag, types := range c.Tailorings {
		for typ, b := range types {
			total += len(b)
			if len(b) > 4000 || named[tag] {
				t.Logf("%s %s: %d bytes", tag, typ, len(b))
			}
		}
	}
	t.Logf("root %d bytes; %d locales' tailorings %d bytes", len(c.Root), len(c.Tailorings), total)
}

func TestCollatorSettings(t *testing.T) {
	root := func(change func(*collation.Settings)) *collation.Collator {
		return collatorFor(t, "und", "", change)
	}
	if got := sorted(root(func(s *collation.Settings) { s.Numeric = true }), []string{"file10", "file9", "file100"}); !slices.Equal(got, []string{"file9", "file10", "file100"}) {
		t.Errorf("numeric: %v", got)
	}
	if got := sorted(root(nil), []string{"file10", "file9"}); !slices.Equal(got, []string{"file10", "file9"}) {
		t.Errorf("not numeric: %v", got)
	}
	shifted := root(func(s *collation.Settings) { s.Shifted = true })
	if shifted.Compare("de luge", "deluge") != 0 || shifted.Compare("de-luge", "deluge") != 0 {
		t.Error("shifted: spaces and hyphens should be ignorable to the tertiary level")
	}
	if root(nil).Compare("de luge", "deluge") >= 0 {
		t.Error("non-ignorable: a space sorts before letters")
	}
	upper := root(func(s *collation.Settings) { s.CaseFirst = collation.CaseFirstUpper })
	if upper.Compare("A", "a") >= 0 || root(nil).Compare("a", "A") >= 0 {
		t.Error("case first")
	}
	primary := root(func(s *collation.Settings) { s.Strength = collation.Primary })
	if primary.Compare("a", "Á") != 0 || primary.Compare("a", "b") >= 0 {
		t.Error("primary strength")
	}
	secondary := root(func(s *collation.Settings) { s.Strength = collation.Secondary })
	if secondary.Compare("a", "A") != 0 || secondary.Compare("a", "á") >= 0 {
		t.Error("secondary strength")
	}
	// French as Canada sorts it compares accents from the end.
	frCA := collatorFor(t, "fr-CA", "standard", nil)
	if got := sorted(frCA, []string{"côté", "coté", "côte", "cote"}); !slices.Equal(got, []string{"cote", "côte", "coté", "côté"}) {
		t.Errorf("fr-CA: %v", got)
	}
	if got := sorted(root(nil), []string{"côté", "coté", "côte", "cote"}); !slices.Equal(got, []string{"cote", "coté", "côte", "côté"}) {
		t.Errorf("root: %v", got)
	}
	// Greek and Russian put their own script first.
	if collatorFor(t, "ru", "standard", nil).Compare("я", "a") >= 0 || root(nil).Compare("a", "я") >= 0 {
		t.Error("ru reorders Cyrillic first")
	}
	// Japanese: the length mark sorts as the vowel before it.
	ja := collatorFor(t, "ja", "standard", nil)
	if got := sorted(ja, []string{"カイ", "カー", "カア"}); !slices.Equal(got, []string{"カー", "カア", "カイ"}) {
		t.Errorf("ja: %v", got)
	}
}

func TestSortKeysCompareAsTheCollatorDoes(t *testing.T) {
	words := strings.Fields("a A á Á ä b ch c d de-luge deluge De Luge file9 file10 ﬀ ff Æble Øl Århus ñu 阿 一 カー カア")
	for _, tc := range []struct {
		tag, typ string
	}{{"und", ""}, {"da", "standard"}, {"es", "standard"}, {"fr-CA", "standard"}, {"zh", "pinyin"}, {"ja", "standard"}} {
		for _, change := range []func(*collation.Settings){
			nil,
			func(s *collation.Settings) { s.Shifted, s.Strength = true, collation.Quaternary },
			func(s *collation.Settings) { s.Strength = collation.Identical; s.Numeric = true },
			func(s *collation.Settings) { s.CaseFirst = collation.CaseFirstUpper },
			func(s *collation.Settings) { s.CaseLevel, s.Strength = true, collation.Primary },
		} {
			c := collatorFor(t, tc.tag, tc.typ, change)
			for _, a := range words {
				for _, b := range words {
					want := c.Compare(a, b)
					got := collation.CompareKeys(c.SortKey(a), c.SortKey(b))
					if got != want {
						t.Errorf("%s %s %+v: %q vs %q: keys %d, compare %d", tc.tag, tc.typ, c.Settings(), a, b, got, want)
					}
				}
			}
		}
	}
}
