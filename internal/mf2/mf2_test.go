package mf2_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/stdplural"
)

func TestParseAcceptsMessages(t *testing.T) {
	for _, src := range []string{
		"",
		"Hello, world!",
		"Hello, {$name}!",
		"{$count :integer} items in your cart",
		"{|3| :number minimumFractionDigits=2}",
		`Braces \{ and \} and a backslash \\`,
		"{{  leading space kept}}",
		".input {$count :integer}\n.match $count\none {{one item}}\n* {{{$count} items}}",
		".local $n = {$count :number select=exact}\n.match $n\n0 {{none}}\n1 {{one}}\n* {{many}}",
		".input {$a :string}\n.input {$b :string}\n.match $a $b\nx y {{xy}}\n* * {{other}}",
		"Click {#link href=|https://example.com|}here{/link}{#br/}",
		"{$x @translate=no}",
		"{:string}",
	} {
		if _, errs := mf2.Parse(src); len(errs) > 0 {
			t.Errorf("%q: %v", src, errs)
		}
	}
}

func TestParseRefusesWithOffsets(t *testing.T) {
	cases := []struct {
		src        string
		start, end int
		want       string
	}{
		{"Hello {$name", 6, 7, "this `{` is not closed"},
		{"stray } brace", 6, 7, "unexpected `}`: write `\\}` to mean the character"},
		{`bad \n escape`, 4, 6, "`\\` escapes only `\\`, `{`, `|` and `}`"},
		{".match $n\none {{x}}\n* {{y}", 25, 26, "unexpected `}`: write `\\}` to mean the character"},
		{".match $n\none {{x}}\n* {{y", 24, 25, "expected `}}` to close the pattern"},
		{".match $n", 8, 9, "a `.match` has at least one variant: `* {{...}}`"},
		{".each $n {{x}}", 0, 5, "unknown keyword `.each`: a message declares with `.input` and `.local` and selects with `.match`"},
		{"{$n :number digits}", 18, 19, "expected `=` after the option name `digits`"},
		{"{|open}", 1, 2, "this `|` literal is not closed"},
		{".input {|x|} {{a}}", 7, 12, "`.input` declares a variable: its expression starts with `$name`"},
	}
	for _, c := range cases {
		_, errs := mf2.Parse(c.src)
		if len(errs) != 1 {
			t.Errorf("%q: %v, want one error", c.src, errs)
			continue
		}
		e := errs[0]
		if e.Span.Start != c.start || e.Span.End != c.end || e.Message != c.want {
			t.Errorf("%q: %d-%d %q, want %d-%d %q", c.src, e.Span.Start, e.Span.End, e.Message, c.start, c.end, c.want)
		}
	}
}

// Print writes one canonical text per data model: printing it again is a
// fixed point, and two layouts of one message print alike.
func TestPrintIsCanonical(t *testing.T) {
	cases := []struct{ src, want string }{
		{"Hello, {  $name  }!", "Hello, {$name}!"},
		{"{{ lead}}", "{{ lead}}"},
		{".input   {$count   :integer}   .match $count one{{a}} *  {{b}}", ".input {$count :integer}\n.match $count\none {{a}}\n* {{b}}"},
		{"{|plain| :string}", "{plain :string}"},
		{"{|two words|}", "{|two words|}"},
		{`a \{ b`, `a \{ b`},
		{"{#b}bold{/b}", "{#b}bold{/b}"},
	}
	for _, c := range cases {
		m, errs := mf2.Parse(c.src)
		if len(errs) > 0 {
			t.Fatalf("%q: %v", c.src, errs)
		}
		got := mf2.Print(m)
		if got != c.want {
			t.Errorf("%q printed %q, want %q", c.src, got, c.want)
		}
		if again := mf2.Canonical(got); again != got {
			t.Errorf("%q printed again as %q", got, again)
		}
	}
	if !mf2.Same(".input {$a :string}\n.input {$b :string}\n{{{$a}{$b}}}", ".input {$b :string} .input {$a :string} {{{$a}{$b}}}") {
		t.Error("reordered declarations are not the same message")
	}
	if mf2.Same("{$count} items", "{$count} things") {
		t.Error("different text is the same message")
	}
}

func cardinal(tag string) mf2.Rules { return stdplural.Rules(cldrfull.Full(), tag, false) }
func ordinal(tag string) mf2.Rules  { return stdplural.Rules(cldrfull.Full(), tag, true) }

func locale(tag, language string, source bool) mf2.Locale {
	return mf2.Locale{Tag: tag, Language: language, Cardinal: cardinal(tag), Ordinal: ordinal(tag), Source: source}
}

var shopSig = mf2.Signature{Name: "cart_items", Params: []mf2.Param{
	{Name: "count", Kind: mf2.KindInt, Type: "Int", Localize: true},
	{Name: "status", Kind: mf2.KindEnum, Type: "Status", Variants: []string{"Pending", "Shipped", "Delivered"}},
	{Name: "name", Kind: mf2.KindString, Type: "String", Localize: true},
	{Name: "total", Kind: mf2.KindFloat, Type: "Float", Localize: true},
	{Name: "item", Kind: mf2.KindOther, Type: "Item"},
}}

func validate(t *testing.T, src string, sig mf2.Signature, loc mf2.Locale) []mf2.Diagnostic {
	t.Helper()
	m, errs := mf2.Parse(src)
	if len(errs) > 0 {
		t.Fatalf("%q: %v", src, errs)
	}
	return mf2.Validate(m, sig, loc)
}

func TestValidateAcceptsCheckedMessages(t *testing.T) {
	sig := mf2.Signature{Name: "cart_items", Params: shopSig.Params[:3]}
	for _, c := range []struct {
		src string
		loc mf2.Locale
	}{
		{".input {$count :integer}\n.input {$status :string}\n.match $count\none {{{$name}: {$count} item, {$status}}}\n* {{{$name}: {$count} items, {$status}}}", locale("en", "English", true)},
		{".input {$count :integer}\n.match $count\none {{{$count} produkt}}\nfew {{{$count} produkty}}\nmany {{{$count} produktów}}\n* {{{$count} produktu}}", locale("pl", "Polish", false)},
		{".input {$status :string}\n.match $status\nPending {{a}}\nShipped {{b}}\nDelivered {{c}}\n* {{d}}", locale("pl", "Polish", false)},
		{".input {$count :integer select=exact}\n.match $count\n0 {{none}}\n* {{some}}", locale("pl", "Polish", false)},
	} {
		if ds := validate(t, c.src, sig, c.loc); len(ds) > 0 {
			t.Errorf("%q: %v", c.src, ds)
		}
	}
}

// Each check Validate makes, with the text it reports and where.
func TestValidateRefuses(t *testing.T) {
	en := locale("en", "English", false)
	pl := locale("pl", "Polish", false)
	cases := []struct {
		name, src string
		loc       mf2.Locale
		at        string // the text the diagnostic starts at
		want      string
	}{
		{"unknown variable", "Hi {$nme}", en, "$nme", "en: `$nme` is not a parameter of `cart_items`"},
		{"unknown function", "{$count :money}", en, ":money", "en: unknown function `:money`"},
		{"later function", "{$total :offset}", en, ":offset", "en: `:offset` is not supported yet"},
		{"currency of a number", "{$total :currency}", en, ":currency", "en: `:currency` formats a `Money`, and `$total` is a `Float`"},
		{"date of a number", "{$total :date}", en, ":date", "en: `:date` formats a calendar value (std/calendar's `Date`, `Time`, `NaiveDateTime` or `DateTime`), and `$total` is a `Float`"},
		{"noun", "{$item :nomi:noun}", en, ":nomi:noun", "en: `:nomi:noun` takes a `Noun`, and `$item` is an `Item`"},
		{"option", "{$count :integer style=long}", en, "style", "en: `:integer` has no option `style`"},
		{"option value", "{$count :number select=many}", en, "many", "en: `select` is `plural`, `ordinal` or `exact`, not `many`"},
		{"option twice", "{$count :number useGrouping=never useGrouping=always}", en, "useGrouping=always", "en: the option `useGrouping` is given twice"},
		{"integer on String", "{$name :integer}", en, ":integer", "en: `:integer` formats an `Int`, and `$name` is a `String`"},
		{"number on enum", "{$status :number}", en, ":number", "en: `:number` formats an `Int`, `Float` or `Decimal`, and `$status` is a `Status`"},
		{"string on Int", "{$count :string}", en, ":string", "en: `:string` formats a `String` or an enum whose variants carry nothing, and `$count` is an `Int`"},
		{"no Localize", "Buy {$item}", en, "{$item}", "en: `$item` is an `Item`, which does not implement `Localize`"},
		{"no fallback", ".input {$count :integer}\n.match $count\none {{a}}\nother {{b}}", en, ".input", "en: `cart_items` has no variant whose keys are all `*`"},
		{"key count", ".input {$count :integer}\n.match $count\none two {{a}}\n* {{b}}", en, "one two", "en: this variant has 2 keys and the `.match` 1 selector"},
		{"duplicate variant", ".input {$count :integer}\n.match $count\none {{a}}\none {{b}}\n* {{c}}", en, "one {{b}}", "en: two variants have the keys `one`"},
		{"unannotated selector", ".match $count\none {{a}}\n* {{b}}", en, "$count", "en: the selector `$count` has no function to select by"},
		{"missing plural arm", ".input {$count :integer}\n.match $count\none {{a}}\n* {{b}}", pl, "$count\none", "pl: `cart_items` has no `few` arm; 2, 3 and 4 select `few` in Polish"},
		{"not a category", ".input {$count :integer}\n.match $count\none {{a}}\nmany {{b}}\n* {{c}}", en, "many", "en: `many` is not a plural category in English"},
		{"Float selector", ".input {$total :number}\n.match $total\none {{a}}\n* {{b}}", en, ":number", "en: a `Float` cannot select a plural category: its category depends on the digits it shows, which a `Float` does not keep"},
		{"enum missing arm", ".input {$status :string}\n.match $status\nPending {{a}}\nShipped {{b}}\n* {{c}}", pl, "$status\nPending", "pl: `cart_items` has no `Delivered` arm; `.match $status` needs an arm for every variant of `Status`"},
		{"enum stray key", ".input {$status :string}\n.match $status\nPending {{a}}\nShipped {{b}}\nDelivered {{b}}\nLost {{d}}\n* {{c}}", pl, "Lost", "pl: `Lost` is not a variant of `Status`"},
		{"unused parameter", "{$count :integer} items", mf2.Locale{Tag: "en", Language: "English", Cardinal: cardinal("en"), Source: true}, "{$count", "en: the source text of `cart_items` does not use `$name`"},
		{"declared twice", ".input {$count :integer}\n.input {$count :number}\n{{x}}", en, "$count :number", "en: `$count` is declared twice"},
		{"local names a parameter", ".local $count = {1 :integer}\n{{x}}", en, "$count =", "en: `.local $count` names a parameter of `cart_items`; give the local another name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sig := shopSig
			if c.name == "unused parameter" {
				sig = mf2.Signature{Name: "cart_items", Params: []mf2.Param{shopSig.Params[0], shopSig.Params[2]}}
			}
			ds := validate(t, c.src, sig, c.loc)
			at := strings.Index(c.src, c.at)
			for _, d := range ds {
				if d.Message == c.want {
					if d.Span.Start != at {
						t.Errorf("%q at %d, want %d (%q)", d.Message, d.Span.Start, at, c.at)
					}
					return
				}
			}
			t.Errorf("no %q among %v", c.want, ds)
		})
	}
}

type env struct {
	tag string
	rtl bool
}

func (e env) Plural(n string, ord bool) string {
	if ord {
		return ordinal(e.tag).CategoryOf(n)
	}
	return cardinal(e.tag).CategoryOf(n)
}
func (e env) Number(n string, f mf2.NumberFormat) string {
	if f.Percent {
		return n + "%"
	}
	return n
}
func (e env) RTL() bool            { return e.rtl }
func (e env) Text(s string) string { return s }

// Noun writes the noun's form in the env's locale, or its source's.
func (e env) Noun(noun, kase, count string) string {
	t, err := mf2.DecodeNoun(noun)
	if err != nil {
		return "{noun}"
	}
	for _, l := range []string{e.tag, t.Source} {
		if m, ok := mf2.NounForm(noun, l, kase); ok {
			return mf2.Format(m, map[string]mf2.Value{mf2.NounParam: {Kind: mf2.KindInt, Number: count}}, e)
		}
	}
	return "{noun}"
}

func (e env) Gender(noun string) string {
	t, err := mf2.DecodeNoun(noun)
	if err != nil {
		return ""
	}
	return t.Genders[e.tag]
}

// Money and Date show what the formatter asks for, which the stdmessageformat
// environment answers from CLDR (internal/stdmessageformat's tests).
func (e env) Money(amount, currency, display string) string {
	return amount + " " + currency + " (" + display + ")"
}

func (e env) Date(v mf2.Value, r mf2.DateRequest) string {
	return v.Key + " (" + r.DateStyle + "/" + r.TimeStyle + " " + r.Skeleton + ")"
}

func TestFormatSelectsAndRenders(t *testing.T) {
	plSrc := ".input {$count :integer}\n.match $count\none {{{$count} produkt w koszyku}}\nfew {{{$count} produkty w koszyku}}\nmany {{{$count} produktów w koszyku}}\n* {{{$count} produktu w koszyku}}"
	enSrc := ".input {$count :integer}\n.match $count\none {{{$count} item in your cart}}\n* {{{$count} items in your cart}}"
	pl, _ := mf2.Parse(plSrc)
	en, _ := mf2.Parse(enSrc)
	for _, c := range []struct {
		n      string
		en, pl string
	}{
		{"1", "1 item in your cart", "1 produkt w koszyku"},
		{"2", "2 items in your cart", "2 produkty w koszyku"},
		{"5", "5 items in your cart", "5 produktów w koszyku"},
		{"22", "22 items in your cart", "22 produkty w koszyku"},
		{"25", "25 items in your cart", "25 produktów w koszyku"},
	} {
		args := map[string]mf2.Value{"count": {Kind: mf2.KindInt, Number: c.n}}
		if got := mf2.Format(en, args, env{tag: "en"}); got != c.en {
			t.Errorf("en %s: %q, want %q", c.n, got, c.en)
		}
		if got := mf2.Format(pl, args, env{tag: "pl"}); got != c.pl {
			t.Errorf("pl %s: %q, want %q", c.n, got, c.pl)
		}
	}
	status, _ := mf2.Parse(".input {$status :string}\n.match $status\nPending {{waiting}}\nShipped {{on its way}}\n* {{here}}")
	if got := mf2.Format(status, map[string]mf2.Value{"status": {Kind: mf2.KindEnum, Key: "Shipped"}}, env{tag: "en"}); got != "on its way" {
		t.Errorf("enum: %q", got)
	}
	exact, _ := mf2.Parse(".input {$count :integer}\n.match $count\n0 {{empty}}\none {{one}}\n* {{many}}")
	if got := mf2.Format(exact, map[string]mf2.Value{"count": {Kind: mf2.KindInt, Number: "0"}}, env{tag: "en"}); got != "empty" {
		t.Errorf("an exact key beats the category: %q", got)
	}
	ar, _ := mf2.Parse("{$name} اشترى {$count :integer}")
	got := mf2.Format(ar, map[string]mf2.Value{"name": {Kind: mf2.KindString, Text: "Anna"}, "count": {Kind: mf2.KindInt, Number: "3"}}, env{tag: "ar", rtl: true})
	if got != "\u2068Anna\u2069 اشترى \u20683\u2069" {
		t.Errorf("RTL isolation: %q", got)
	}
	pct, _ := mf2.Parse("{$share :percent}")
	if got := mf2.Format(pct, map[string]mf2.Value{"share": {Kind: mf2.KindDecimal, Number: "0.25"}}, env{tag: "en"}); got != "25%" {
		t.Errorf("percent: %q", got)
	}
}
