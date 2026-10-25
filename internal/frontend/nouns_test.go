package frontend_test

import (
	"maps"
	"strings"
	"testing"
)

// The agreement checks: a `noun` declaration and its catalog entries, and
// the whole-program checks that hold every noun to every message (check 7
// of the i18n design).

const nounManifest = `[module]
name = "shop"
entry_points = ["main"]

[i18n]
source_locale = "en"
locales = ["en", "pl"]
partial_locales = ["de"]
`

const bookSource = `.input {$count :integer} .match $count one {{book}} * {{books}}`
const laptopSource = `.input {$count :integer} .match $count one {{laptop}} * {{laptops}}`

const nounShop = "import std/message.Noun\n\n" +
	"pub noun book `" + bookSource + "`\n\n" +
	"pub noun laptop `" + laptopSource + "`\n\n" +
	"pub message has_shipped(product: Noun) `Your {$product :nomi:noun} has shipped.`\n\n" +
	"pub message sold_out(product: Noun) `No {$product :nomi:noun count=2} left.`\n"

// nounEntry renders a noun's catalog entry.
func nounEntry(name, source, gender string, forms ...string) string {
	s := "[" + name + "]\nsource = '''" + source + "'''\n"
	if gender != "" {
		s += "gender = \"" + gender + "\"\n"
	}
	for i := 0; i+1 < len(forms); i += 2 {
		s += forms[i] + " = '''" + forms[i+1] + "'''\n"
	}
	return s + "\n"
}

const (
	ksiazkaNom = ".input {$count :integer} .match $count one {{książka}} few {{książki}} many {{książek}} * {{książki}}"
	ksiazkaGen = ".input {$count :integer} .match $count one {{książki}} few {{książek}} many {{książek}} * {{książki}}"
	laptopNom  = ".input {$count :integer} .match $count one {{laptop}} few {{laptopy}} many {{laptopów}} * {{laptopa}}"
	laptopGen  = ".input {$count :integer} .match $count one {{laptopa}} few {{laptopów}} many {{laptopów}} * {{laptopa}}"
	shippedPl  = ".input {$product :nomi:gender} .match $product feminine {{Twoja {$product :nomi:noun} została wysłana.}} inanimate {{Twój {$product :nomi:noun} został wysłany.}} * {{Twoja przesyłka została wysłana.}}"
)

func nounFiles() map[string]string {
	return map[string]string{
		"nomi.toml": nounManifest,
		"shop.nomi": nounShop,
		"main.nomi": "import {\n    std/io\n    std/locale.Locale\n    std/message.Message\n    shop\n}\n\nfn main() {\n    io.print(Message.format(shop.has_shipped(shop.book), Locale`pl`))\n}\n",
		"locales/pl/shop.toml": nounEntry("book", bookSource, "feminine", "nominative", ksiazkaNom, "genitive", ksiazkaGen) +
			nounEntry("laptop", laptopSource, "inanimate", "nominative", laptopNom, "genitive", laptopGen) +
			entry("has_shipped", "Your {$product :nomi:noun} has shipped.", shippedPl) +
			entry("sold_out", "No {$product :nomi:noun count=2} left.", "Brak {$product :nomi:noun case=genitive count=5}."),
	}
}

func TestNouns_ACompleteProjectChecks(t *testing.T) {
	if ds := checkShop(t, nounFiles()); len(ds) > 0 {
		t.Fatalf("a complete, current project is refused:\n%s", strings.Join(ds, "\n"))
	}
}

// Each check of agreement, with the text it reports and where. Each case
// changes the complete project in one way, and fails if the project is
// then accepted.
func TestNouns_EachCheckRefuses(t *testing.T) {
	pl := "locales/pl/shop.toml"
	cases := []struct {
		name   string
		change func(map[string]string)
		want   string
	}{
		{"no gender", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], "gender = \"feminine\"\n", "", 1)
		}, "locales/pl/shop.toml:1:1: pl: `book` has no `gender`"},
		{"not a gender", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], `"feminine"`, `"masculine"`, 1)
		}, "locales/pl/shop.toml:3:10: pl: `masculine` is not a gender in Polish"},
		{"a gender where the language has none", func(f map[string]string) {
			f["nomi.toml"] = strings.Replace(nounManifest, `["en", "pl"]`, `["en", "pl", "ja"]`, 1)
			f["locales/ja/shop.toml"] = nounEntry("book", bookSource, "neuter", "nominative", "本") +
				nounEntry("laptop", laptopSource, "", "nominative", "ノートパソコン") +
				entry("has_shipped", "Your {$product :nomi:noun} has shipped.", "{$product :nomi:noun}を発送しました。") +
				entry("sold_out", "No {$product :nomi:noun count=2} left.", "{$product :nomi:noun}は売り切れです。")
		}, "locales/ja/shop.toml:3:1: ja: Japanese nouns have no grammatical gender, so `book` takes no `gender`"},
		{"no nominative", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], "nominative = '''"+laptopNom+"'''\n", "", 1)
		}, "locales/pl/shop.toml:7:1: pl: `laptop` has no `nominative` form"},
		{"not a case", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], "genitive = '''"+ksiazkaGen, "genetive = '''"+ksiazkaGen, 1)
		}, "locales/pl/shop.toml:5:1: pl: `genetive` is not a case in Polish, nor a key of a noun's entry"},
		{"a form's plural arms", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], " few {{laptopy}}", "", 1)
		}, "locales/pl/shop.toml:10:49: pl: `laptop` has no `few` arm; 2, 3 and 4 select `few` in Polish"},
		{"text in a noun's entry", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], "gender = \"inanimate\"\n", "gender = \"inanimate\"\ntext = '''laptop'''\n", 1)
		}, "locales/pl/shop.toml:10:1: pl: `laptop` is a noun, whose entry gives a form per case and no `text`"},
		{"missing translation", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], nounEntry("laptop", laptopSource, "inanimate", "nominative", laptopNom, "genitive", laptopGen), "", 1)
		}, "shop.nomi:5:10: pl: `laptop` has no translation"},
		{"stale", func(f map[string]string) {
			f["shop.nomi"] = strings.Replace(f["shop.nomi"], "{{books}}", "{{some books}}", 1)
		}, "locales/pl/shop.toml:2:1: pl: `book` was translated from an older English text"},
		{"a case form a message asks for", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], "genitive = '''"+laptopGen+"'''\n", "", 1)
		}, "locales/pl/shop.toml:7:1: pl: `laptop` has no `genitive` form, which `sold_out` asks for"},
		{"a case name in a message", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], "case=genitive", "case=genetive", 1)
		}, "locales/pl/shop.toml:19:42: pl: `genetive` is not a case in Polish"},
		{"a case in a language with none", func(f map[string]string) {
			f["shop.nomi"] = strings.Replace(f["shop.nomi"], "count=2} left", "case=genitive} left", 1)
		}, "shop.nomi:9:67: en: `genitive` is not a case in English"},
		{"a gender arm for a noun of the program", func(f map[string]string) {
			f["shop.nomi"] += "\npub noun phone_case `phone case`\n"
			f[pl] += nounEntry("phone_case", "phone case", "neuter", "nominative", "etui", "genitive", "etui")
		}, "locales/pl/shop.toml:15:49: pl: `has_shipped` has no `neuter` arm; the noun `shop.phone_case` is neuter in Polish"},
		{"a gender arm for a noun of another file", func(f map[string]string) {
			f["staff.nomi"] = "pub noun courier `courier`\n"
			f["locales/pl/staff.toml"] = nounEntry("courier", "courier", "personal", "nominative", "kurier", "genitive", "kuriera")
			f["main.nomi"] = strings.Replace(f["main.nomi"], "    shop\n", "    shop\n    staff\n", 1)
			f["main.nomi"] = strings.Replace(f["main.nomi"], "shop.book), Locale`pl`))", "staff.courier), Locale`pl`))", 1)
		}, "locales/pl/shop.toml:15:49: pl: `has_shipped` has no `personal` arm; the noun `staff.courier` is personal in Polish"},
		{"not a gender in a selector", func(f map[string]string) {
			f[pl] = strings.Replace(f[pl], "inanimate {{Twój", "masculine {{Twój", 1)
		}, "locales/pl/shop.toml:15:118: pl: `masculine` is not a gender in Polish"},
		{"a gender selector in English", func(f map[string]string) {
			f["shop.nomi"] = strings.Replace(f["shop.nomi"], "`Your {$product :nomi:noun} has shipped.`",
				"`.input {$product :nomi:gender} .match $product * {{Your {$product :nomi:noun} has shipped.}}`", 1)
		}, "shop.nomi:7:79: en: English nouns have no grammatical gender, so `has_shipped` cannot select on `$product`'s"},
		{"a noun function on a String", func(f map[string]string) {
			f["shop.nomi"] = strings.Replace(f["shop.nomi"], "has_shipped(product: Noun)", "has_shipped(product: String)", 1)
		}, "shop.nomi:7:58: en: `:nomi:noun` takes a `Noun`, and `$product` is a `String`"},
		{"a runtime string where a Noun is expected", func(f map[string]string) {
			f["main.nomi"] = strings.Replace(f["main.nomi"], "shop.has_shipped(shop.book)", `shop.has_shipped("The Pragmatic Programmer")`, 1)
		}, "main.nomi:9:46: argument 1: expected Noun, got String\n  help: a `Noun` is a `noun` declaration"},
		{"a noun with parameters", func(f map[string]string) {
			f["shop.nomi"] = strings.Replace(f["shop.nomi"], "pub noun book `", "pub noun book(n: Int) `", 1)
		}, "shop.nomi:3:10: `noun book` takes no parameters"},
		{"a noun with no text", func(f map[string]string) {
			f["shop.nomi"] += "\npub noun mug\n"
		}, "shop.nomi:11:10: `noun mug` has no text"},
		{"a noun's own plural arms", func(f map[string]string) {
			f["shop.nomi"] = strings.Replace(f["shop.nomi"], "one {{book}} * {{books}}", "* {{books}}", 1)
		}, "shop.nomi:3:48: en: `book` has no `one` arm; 1 selects `one` in English"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := maps.Clone(nounFiles())
			c.change(files)
			ds := checkShop(t, files)
			if len(ds) == 0 {
				t.Fatalf("the project is accepted; want %q", c.want)
			}
			for _, d := range ds {
				if strings.HasPrefix(d, c.want) {
					return
				}
			}
			t.Fatalf("no diagnostic starting %q among:\n%s", c.want, strings.Join(ds, "\n"))
		})
	}
}

// A partial locale may lack a noun; a noun it has must still have the
// forms its messages ask for.
func TestNouns_APartialLocaleMayLackANoun(t *testing.T) {
	files := nounFiles()
	files["locales/de/shop.toml"] = nounEntry("book", bookSource, "neuter", "nominative", ".input {$count :integer} .match $count one {{Buch}} * {{Bücher}}") +
		entry("has_shipped", "Your {$product :nomi:noun} has shipped.",
			".input {$product :nomi:gender} .match $product neuter {{Ihr {$product :nomi:noun} wurde versandt.}} * {{Ihre Bestellung wurde versandt.}}")
	if ds := checkShop(t, files); len(ds) > 0 {
		t.Fatalf("refused:\n%s", strings.Join(ds, "\n"))
	}
}

// A noun declared in a source language whose nouns have a gender is
// refused: the declaration has no place to give one.
func TestNouns_AGenderedSourceLocaleIsRefused(t *testing.T) {
	files := map[string]string{
		"nomi.toml": strings.Replace(nounManifest, `source_locale = "en"`, `source_locale = "pl"`, 1),
		"shop.nomi": "pub noun book `książka`\n",
		"main.nomi": "import {\n    std/io\n    std/locale.Locale\n    shop\n}\n\nfn main() {\n    io.print(Localize.localize(shop.book, Locale`pl`))\n}\n",
	}
	files["nomi.toml"] = strings.Replace(files["nomi.toml"], `locales = ["en", "pl"]`, `locales = ["pl"]`, 1)
	ds := checkShop(t, files)
	want := "shop.nomi:1:5: `noun book` is declared in Polish, whose nouns have a grammatical gender"
	for _, d := range ds {
		if strings.HasPrefix(d, want) {
			return
		}
	}
	t.Fatalf("no diagnostic starting %q among:\n%s", want, strings.Join(ds, "\n"))
}
