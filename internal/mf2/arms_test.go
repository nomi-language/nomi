package mf2_test

import (
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/internal/mf2"
)

// FitArms gives a text the arms a locale needs, copying the catch-all's
// pattern, and drops arms whose key the locale does not have; the result
// validates in that locale.
func TestFitArmsGivesATextTheArmsALocaleNeeds(t *testing.T) {
	cases := []struct {
		name, src, want string
		loc             mf2.Locale
	}{
		{
			"English source in Polish",
			".input {$count :integer}\n.match $count\none {{{$count} item}}\n* {{{$count} items}}",
			".input {$count :integer}\n.match $count\none {{{$count} item}}\nfew {{{$count} items}}\nmany {{{$count} items}}\n* {{{$count} items}}",
			locale("pl", "Polish", false),
		},
		{
			"indented arms keep their indentation",
			".input {$count :integer}\n.match $count\n  one {{a}}\n  * {{b}}",
			".input {$count :integer}\n.match $count\n  one {{a}}\n  few {{b}}\n  many {{b}}\n  * {{b}}",
			locale("pl", "Polish", false),
		},
		{
			"English source in Japanese loses `one`",
			".input {$count :integer}\n.match $count\none {{{$count} item}}\n* {{{$count} items}}",
			".input {$count :integer}\n.match $count\n* {{{$count} items}}",
			locale("ja", "Japanese", false),
		},
		{
			"an enum gets its missing variant",
			".input {$status :string}\n.match $status\nPending {{a}}\nShipped {{b}}\n* {{c}}",
			".input {$status :string}\n.match $status\nPending {{a}}\nShipped {{b}}\nDelivered {{c}}\n* {{c}}",
			locale("pl", "Polish", false),
		},
		{
			"two selectors",
			".input {$count :integer}\n.input {$status :string}\n.match $count $status\none Pending {{x}}\none * {{y}}\n* * {{z}}",
			".input {$count :integer}\n.input {$status :string}\n.match $count $status\none Pending {{x}}\none * {{y}}\nfew * {{z}}\nmany * {{z}}\n* Shipped {{z}}\n* Delivered {{z}}\n* * {{z}}",
			locale("pl", "Polish", false),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mf2.Apply(c.src, mf2.FitArms(c.src, shopSig, c.loc))
			if got != c.want {
				t.Fatalf("got\n%s\nwant\n%s", got, c.want)
			}
			if ds := validate(t, got, shopSig, c.loc); len(ds) > 0 {
				t.Errorf("the fitted text does not validate: %v", ds)
			}
			if again := mf2.FitArms(got, shopSig, c.loc); again != nil {
				t.Errorf("a fitted text still has edits: %v", again)
			}
		})
	}
}

func TestSelectorsNameTheKeysEachSelectorTakes(t *testing.T) {
	m, errs := mf2.Parse(".input {$count :integer}\n.input {$status :string}\n.match $count $status\n* * {{z}}")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got := mf2.Selectors(m, shopSig, locale("pl", "Polish", false))
	want := []mf2.SelectorKeys{
		{Name: "count", Needed: []string{"one", "few", "many"}, Known: []string{"one", "few", "many", "other"}, Plural: true},
		{Name: "status", Needed: []string{"Pending", "Shipped", "Delivered"}, Known: []string{"Pending", "Shipped", "Delivered"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
