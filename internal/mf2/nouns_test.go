package mf2_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/mf2"
)

func TestWeekdayOfADate(t *testing.T) {
	for date, want := range map[string]string{
		"1970-01-01": "thursday",
		"2026-10-20": "tuesday",
		"2026-10-25": "sunday",
		"2000-02-29": "tuesday",
		"1969-12-31": "wednesday",
		"0001-01-01": "monday",
		"not a date": "",
	} {
		if got := mf2.Weekday(date); got != want {
			t.Errorf("Weekday(%q) = %q, want %q", date, got, want)
		}
	}
}

// A noun is written in a case and for a count, and selected by its gender;
// a list is written joined and selected by its length.
func TestFormatNounsAndLists(t *testing.T) {
	book := mf2.EncodeNoun(mf2.NounTable{Name: "shop.book", Source: "en",
		Genders: map[string]string{"pl": "feminine"},
		Forms: map[string]map[string]string{
			"en": {"nominative": ".input {$count :integer}\n.match $count\none {{book}}\n* {{books}}"},
			"pl": {
				"nominative": ".input {$count :integer}\n.match $count\none {{książka}}\nfew {{książki}}\nmany {{książek}}\n* {{książki}}",
				"genitive":   ".input {$count :integer}\n.match $count\none {{książki}}\nfew {{książek}}\nmany {{książek}}\n* {{książki}}",
			},
		}})
	noun := map[string]mf2.Value{"product": {Kind: mf2.KindNoun, Text: book}}
	for _, c := range []struct{ tag, text, want string }{
		{"pl", ".input {$product :nomi:gender} .match $product feminine {{Twoja {$product :nomi:noun}}} * {{Twój {$product :nomi:noun}}}", "Twoja książka"},
		{"en", ".input {$product :nomi:gender} .match $product feminine {{f}} * {{Your {$product :nomi:noun}}}", "Your book"},
		{"pl", "Brak {$product :nomi:noun case=genitive count=5}.", "Brak książek."},
		{"pl", "{$product}", "książka"},
	} {
		m, errs := mf2.Parse(c.text)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		if got := mf2.Format(m, noun, env{tag: c.tag}); got != c.want {
			t.Errorf("%s %q = %q, want %q", c.tag, c.text, got, c.want)
		}
	}
	m, _ := mf2.Parse(".input {$people :nomi:list} .match $people one {{{$people} ogląda}} * {{{$people} oglądają}}")
	for n, want := range map[string]string{"1": "Anna ogląda", "3": "Anna oglądają"} {
		args := map[string]mf2.Value{"people": {Kind: mf2.KindList, Number: n, Text: "Anna"}}
		if got := mf2.Format(m, args, env{tag: "pl"}); got != want {
			t.Errorf("a list of %s: %q, want %q", n, got, want)
		}
	}
}

func TestValidateNounsListsAndWeekdays(t *testing.T) {
	sig := mf2.Signature{Name: "m", Params: []mf2.Param{
		{Name: "product", Kind: mf2.KindNoun, Type: "Noun", Localize: true},
		{Name: "people", Kind: mf2.KindList, Type: "List<String>"},
		{Name: "date", Kind: mf2.KindDate, Type: "Date"},
	}}
	pl := mf2.Locale{Tag: "pl", Language: "Polish", Genders: []string{"animate", "inanimate", "personal", "feminine", "neuter"}, Cases: []string{"nominative", "genitive"}}
	for _, c := range []struct{ text, want string }{
		{"{$product :nomi:noun case=dative}", "pl: `dative` is not a case in Polish"},
		{"{$product :nomi:noun count=x}", "pl: `count` is a whole number of zero or more, or an `Int` parameter, not `x`"},
		{"{$product :nomi:noun count=$people}", "pl: `count` is an `Int`, and `$people` is a `List<String>`"},
		{"{$people :nomi:noun}", "pl: `:nomi:noun` takes a `Noun`, and `$people` is a `List<String>`"},
		{"{$product :nomi:list}", "pl: `:nomi:list` formats a `List<String>`, and `$product` is a `Noun`"},
		{".input {$product :nomi:gender} .match $product male {{a}} * {{b}}", "pl: `male` is not a gender in Polish"},
		{".input {$product :nomi:noun} .match $product * {{b}}", "pl: `:nomi:noun` writes a noun and selects nothing"},
		{".input {$date :nomi:weekday} .match $date monday {{a}} * {{b}}", "pl: `m` has no `tuesday` arm; a `.match` on `$date`'s day of the week that names one day needs all seven"},
		{".input {$date :nomi:weekday} .match $date funday {{a}} * {{b}}", "pl: `funday` is not a day of the week"},
		{"{$date :nomi:weekday}", "pl: `:nomi:weekday` selects by the day of the week and writes nothing"},
	} {
		m, errs := mf2.Parse(c.text)
		if len(errs) > 0 {
			t.Fatalf("%q: %v", c.text, errs)
		}
		found := false
		for _, d := range mf2.Validate(m, sig, pl) {
			found = found || d.Message == c.want
		}
		if !found {
			t.Errorf("%q: no %q among %v", c.text, c.want, mf2.Validate(m, sig, pl))
		}
	}
}
