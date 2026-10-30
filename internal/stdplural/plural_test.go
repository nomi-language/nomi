package stdplural

import (
	"context"
	"testing"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/rt"
)

// A program carries the plural rules of the locales it lists, and of
// nothing else: listing only en, Polish counts are root's (every number
// other), as a built program's would be.
func TestRulesAreTheProgramsLocales(t *testing.T) {
	fr := rt.NewFrame(cldrdata.WithData(context.Background(), cldrfull.ForProgram([]string{"en"}, "")))
	if got := cldrdata.Category(FFICardinalTag(fr, "pl", "3")); got != cldrdata.Other {
		t.Errorf("listing only en, cardinal(pl, 3) = %s, want other", got)
	}
	if got := cldrdata.Category(FFICardinalTag(fr, "en-GB", "1")); got != cldrdata.One {
		t.Errorf("listing en, cardinal(en-GB, 1) = %s, want one (en-GB's rules are en's)", got)
	}
	if got := cldrdata.Category(FFICardinalTag(nil, "pl", "3")); got != cldrdata.Few {
		t.Errorf("with every locale, cardinal(pl, 3) = %s, want few", got)
	}
}

// A locale the program does not carry counts as its source locale does:
// listing en and pl with pl the source, French 3 is Polish's few.
func TestALocaleTheProgramDoesNotCarryCountsAsTheSource(t *testing.T) {
	fr := rt.NewFrame(cldrdata.WithData(context.Background(), cldrfull.ForProgram([]string{"en", "pl"}, "pl")))
	if got := cldrdata.Category(FFICardinalTag(fr, "fr", "3")); got != cldrdata.Few {
		t.Errorf("source pl, cardinal(fr, 3) = %s, want few", got)
	}
	if got := cldrdata.Category(FFICardinalTag(fr, "en-GB", "1")); got != cldrdata.One {
		t.Errorf("source pl, cardinal(en-GB, 1) = %s, want one (en-GB's rules are en's)", got)
	}
}

func TestCardinal(t *testing.T) {
	for _, c := range []struct {
		locale, value string
		want          cldrdata.Category
	}{
		{"en", "1", cldrdata.One},
		{"en", "1.0", cldrdata.Other},
		{"en", "-1", cldrdata.One},
		{"en-US-u-nu-latn", "1", cldrdata.One},
		{"pl", "22", cldrdata.Few},
		{"pl", "12", cldrdata.Many},
		{"pl", "1000000000000000000000002", cldrdata.Few},
		{"pl", "1000000000000000000000012", cldrdata.Many},
		{"ru", "21", cldrdata.One},
		{"ar", "103", cldrdata.Few},
		{"cy", "6", cldrdata.Many},
		{"fr", "1.5", cldrdata.One},
		{"pt", "0", cldrdata.One},
		{"pt-PT", "0", cldrdata.Other},
		{"pt-AO", "0", cldrdata.One},
		{"zh-Hant-HK", "1", cldrdata.Other},
		{"ja", "1", cldrdata.Other},
		{"und", "1", cldrdata.Other},
		{"qaa", "1", cldrdata.Other},
	} {
		if got := cldrdata.Category(FFICardinalTag(nil, c.locale, c.value)); got != c.want {
			t.Errorf("cardinal(%s, %s) = %s, want %s", c.locale, c.value, got, c.want)
		}
	}
}

func TestOrdinal(t *testing.T) {
	want := map[int64]cldrdata.Category{1: cldrdata.One, 2: cldrdata.Two, 3: cldrdata.Few, 4: cldrdata.Other,
		11: cldrdata.Other, 12: cldrdata.Other, 13: cldrdata.Other, 21: cldrdata.One, 22: cldrdata.Two, 23: cldrdata.Few,
		-1: cldrdata.One}
	for n, c := range want {
		if got := cldrdata.Category(FFIOrdinalTag(nil, "en", rt.SmallInt(n))); got != c {
			t.Errorf("ordinal(en, %d) = %s, want %s", n, got, c)
		}
	}
}
