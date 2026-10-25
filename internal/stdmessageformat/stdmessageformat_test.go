package stdmessageformat_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdmessageformat"
)

var cartItems = mf2.EncodeTable(mf2.Table{
	Name:   "cart.cart_items",
	Params: []mf2.Param{{Name: "count", Kind: mf2.KindInt, Type: "Int", Localize: true}},
	Source: "en",
	Texts: map[string]string{
		"en": ".input {$count :integer}\n.match $count\none {{{$count} item}}\n* {{{$count} items}}",
		"pl": ".input {$count :integer}\n.match $count\none {{{$count} produkt}}\nfew {{{$count} produkty}}\nmany {{{$count} produktów}}\n* {{{$count} produktu}}",
		"ar": "{$count} منتجات",
	},
})

func format(locale, count string) string {
	d := cldrfull.ForProgram([]string{"en", "pl", "ar", "pt"}, "en")
	return stdmessageformat.Format(d, nil, cartItems, locale, nil, []int64{0}, []string{count}, []string{""}, []string{count})
}

func TestFormatPicksTheTextAlongTheLocalesChain(t *testing.T) {
	for _, c := range []struct{ locale, count, want string }{
		{"en", "1", "1 item"},
		{"en", "1234", "1,234 items"},
		{"pl", "22", "22 produkty"},
		{"pl", "25", "25 produktów"},
		{"pl-PL", "3", "3 produkty"},
		// pt has no text: the source's, with pt's number symbols.
		{"pt", "1234", "1.234 items"},
		{"ar", "3", "\u20683\u2069 منتجات"},
		{"en-XA", "3", "[3 îţéɱš one]"},
		{"ar-XB", "3", "\u20683\u2069\u202e items\u202c"},
	} {
		if got := format(c.locale, c.count); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.locale, c.count, got, c.want)
		}
	}
	if got := stdmessageformat.Format(cldrfull.Full(), nil, "not a table", "en", nil, nil, nil, nil, nil); got != stdmessageformat.Fallback {
		t.Errorf("a table that does not decode formats as %q", got)
	}
}

func TestRightToLeft(t *testing.T) {
	for tag, want := range map[string]bool{"ar": true, "he": true, "fa-IR": true, "ur": true, "en": false, "pl": false, "az-Arab": true} {
		if got := stdmessageformat.RightToLeft(tag); got != want {
			t.Errorf("%s: %v, want %v", tag, got, want)
		}
	}
}
