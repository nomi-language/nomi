package frontend_test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// shopManifest lists English (the source), Polish and Portuguese, and
// German as a partial locale.
const shopManifest = `[module]
name = "shop"
entry_points = ["main"]

[i18n]
source_locale = "en"
locales = ["en", "pl", "pt", "pt-PT"]
partial_locales = ["de"]
`

const shopCart = `pub enum Status {
    Pending
    Shipped
}

pub message cart_items(count: Int) ` + "`" + `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    ` + "`" + `

pub message order_status(status: Status) ` + "`" + `
    .input {$status :string}
    .match $status
    Pending {{Waiting}}
    Shipped {{Shipped}}
    * {{Shipped}}
    ` + "`" + `
`

const cartItemsSource = `.input {$count :integer}
.match $count
one {{{$count} item in your cart}}
* {{{$count} items in your cart}}`

const orderStatusSource = `.input {$status :string}
.match $status
Pending {{Waiting}}
Shipped {{Shipped}}
* {{Shipped}}`

// entry renders a catalog entry with its recorded source and text.
func entry(name, source, text string) string {
	return "[" + name + "]\nsource = '''" + source + "'''\ntext = '''" + text + "'''\n\n"
}

// shopFiles is a project whose catalogs are complete and current.
func shopFiles() map[string]string {
	return map[string]string{
		"nomi.toml": shopManifest,
		"cart.nomi": shopCart,
		"main.nomi": "import {\n    std/io\n    std/locale.Locale\n    std/message.Message\n    cart\n}\n\nfn main() {\n    io.print(Message.format(cart.cart_items(3), Locale`pl`))\n    io.print(Message.format(cart.order_status(cart.Status.Pending), Locale`pl`))\n}\n",
		"locales/pl/cart.toml": entry("cart_items", cartItemsSource,
			".input {$count :integer}\n.match $count\none {{{$count} produkt}}\nfew {{{$count} produkty}}\nmany {{{$count} produktów}}\n* {{{$count} produktu}}") +
			entry("order_status", orderStatusSource, ".input {$status :string}\n.match $status\nPending {{Czeka}}\nShipped {{Wysłane}}\n* {{Wysłane}}"),
		"locales/pt/cart.toml": entry("cart_items", cartItemsSource,
			".input {$count :integer}\n.match $count\none {{{$count} item}}\nmany {{{$count} itens}}\n* {{{$count} itens}}") +
			entry("order_status", orderStatusSource, ".input {$status :string}\n.match $status\nPending {{Aguarda}}\nShipped {{Enviado}}\n* {{Enviado}}"),
	}
}

// checkShop writes files into a project and checks main.nomi, answering
// each diagnostic as `file:line:col: message` with a `help:` line per
// hint, file relative to the project.
func checkShop(t *testing.T, files map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := frontend.New(frontend.Config{}).CheckFile(filepath.Join(dir, "main.nomi"), frontend.Mode{})
	if err == nil {
		return nil
	}
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("not diagnostics: %v", err)
	}
	var out []string
	real, _ := filepath.EvalSymlinks(dir)
	for _, d := range ds {
		rel := d.Path
		for _, root := range []string{dir, real} {
			if r, err := filepath.Rel(root, d.Path); err == nil && !strings.HasPrefix(r, "..") {
				rel = filepath.ToSlash(r)
			}
		}
		line := rel + ":" + itoa(d.Line) + ":" + itoa(d.Col) + ": " + d.Message
		for _, h := range d.Hints {
			line += "\n  help: " + h
		}
		out = append(out, line)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func TestMessages_ACompleteProjectChecks(t *testing.T) {
	if ds := checkShop(t, shopFiles()); len(ds) > 0 {
		t.Fatalf("a complete, current project is refused:\n%s", strings.Join(ds, "\n"))
	}
}

// Each check of a message and its catalogs, with the text it reports and
// where. Each case changes the complete project in one way.
func TestMessages_EachCheckRefuses(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]string)
		want   string
	}{
		{"missing translation", func(f map[string]string) {
			f["locales/pt/cart.toml"] = entry("order_status", orderStatusSource, ".input {$status :string}\n.match $status\nPending {{A}}\nShipped {{E}}\n* {{E}}")
		}, "cart.nomi:6:13: pt: `cart_items` has no translation"},
		{"stray entry", func(f map[string]string) {
			f["locales/pl/cart.toml"] += entry("ghost", "x", "y")
		}, "locales/pl/cart.toml:25:1: pl: `ghost` is not a message or noun of cart.nomi"},
		{"catalog with no Nomi file", func(f map[string]string) {
			f["locales/pl/gone.toml"] = entry("x", "x", "x")
		}, "locales/pl/gone.toml:1:1: pl: this catalog is for gone.nomi, which does not exist"},
		{"unlisted locale", func(f map[string]string) {
			f["locales/fr/cart.toml"] = entry("cart_items", cartItemsSource, "x")
		}, "locales/fr/cart.toml:1:1: fr is not a locale `[i18n]` lists: its catalog directory is read by nothing"},
		{"source locale catalog", func(f map[string]string) {
			f["locales/en/cart.toml"] = entry("cart_items", cartItemsSource, "x")
		}, "locales/en/cart.toml:1:1: en is the source locale, whose text is in the `message` declarations: its catalog directory is read by nothing"},
		{"unknown parameter in a translation", func(f map[string]string) {
			f["locales/pl/cart.toml"] = strings.Replace(f["locales/pl/cart.toml"], "{{{$count} produkt}}", "{{{$cnt} produkt}}", 1)
		}, "locales/pl/cart.toml:8:8: pl: `$cnt` is not a parameter of `cart_items`"},
		{"unused parameter in the source", func(f map[string]string) {
			f["cart.nomi"] = strings.Replace(f["cart.nomi"], "pub message cart_items(count: Int)", "pub message cart_items(count: Int, name: String)", 1)
		}, "cart.nomi:7:5: en: the source text of `cart_items` does not use `$name`"},
		{"type", func(f map[string]string) {
			f["cart.nomi"] = strings.Replace(f["cart.nomi"], ".input {$status :string}", ".input {$status :integer}", 1)
		}, "cart.nomi:14:21: en: `:integer` formats an `Int`, and `$status` is a `Status`"},
		{"plural arm", func(f map[string]string) {
			f["locales/pl/cart.toml"] = strings.Replace(f["locales/pl/cart.toml"], "few {{{$count} produkty}}\n", "", 1)
		}, "locales/pl/cart.toml:7:8: pl: `cart_items` has no `few` arm; 2, 3 and 4 select `few` in Polish"},
		{"enum arm", func(f map[string]string) {
			f["locales/pl/cart.toml"] = strings.Replace(f["locales/pl/cart.toml"], "Shipped {{Wysłane}}\n", "", 1)
		}, "locales/pl/cart.toml:20:8: pl: `order_status` has no `Shipped` arm; `.match $status` needs an arm for every variant of `Status`"},
		{"enum key", func(f map[string]string) {
			f["locales/pl/cart.toml"] = strings.Replace(f["locales/pl/cart.toml"], "Shipped {{Wysłane}}", "Shipped {{Wysłane}}\nLost {{Zgubione}}", 1)
		}, "locales/pl/cart.toml:23:1: pl: `Lost` is not a variant of `Status`"},
		{"MF2 syntax", func(f map[string]string) {
			f["locales/pl/cart.toml"] = strings.Replace(f["locales/pl/cart.toml"], "{{{$count} produkt}}", "{{{$count produkt}}", 1)
		}, "locales/pl/cart.toml:8:15: pl: expected `}` to close the expression"},
		{"catch-all", func(f map[string]string) {
			f["locales/pl/cart.toml"] = strings.Replace(f["locales/pl/cart.toml"], "* {{{$count} produktu}}", "other {{{$count} produktu}}", 1)
		}, "locales/pl/cart.toml:6:11: pl: `cart_items` has no variant whose keys are all `*`"},
		{"stale in a listed locale", func(f map[string]string) {
			f["cart.nomi"] = strings.Replace(f["cart.nomi"], "items in your cart", "things in your cart", 1)
		}, "locales/pl/cart.toml:2:1: pl: `cart_items` was translated from an older English text\n  was:\n        .input {$count :integer}"},
		{"no source recorded", func(f map[string]string) {
			f["locales/pl/cart.toml"] = strings.Replace(f["locales/pl/cart.toml"], "source = '''"+cartItemsSource+"'''\n", "", 1)
		}, "locales/pl/cart.toml:1:1: pl: `cart_items` does not record the English text it was translated from"},
		{"catalog TOML", func(f map[string]string) {
			f["locales/pl/cart.toml"] += "[order_status.extra]\n"
		}, "locales/pl/cart.toml:25:1: pl: a catalog's tables are messages: `[name]`, with no dotted name"},
		{"noun function", func(f map[string]string) {
			f["locales/pl/cart.toml"] = strings.Replace(f["locales/pl/cart.toml"], "{{{$count} produkt}}", "{{{$count :nomi:noun}}}", 1)
		}, "locales/pl/cart.toml:8:15: pl: `:nomi:noun` takes a `Noun`, and `$count` is an `Int`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := maps.Clone(shopFiles())
			c.change(files)
			ds := checkShop(t, files)
			for _, d := range ds {
				if strings.HasPrefix(d, c.want) {
					return
				}
			}
			t.Fatalf("no diagnostic starting %q among:\n%s", c.want, strings.Join(ds, "\n"))
		})
	}
}

// A partial locale may lack a message, and a stale entry there is left
// out, not an error.
func TestMessages_APartialLocaleMayLackOrBeStale(t *testing.T) {
	files := shopFiles()
	files["locales/de/cart.toml"] = entry("cart_items", "{$count :integer} items", "{$count :integer} Artikel")
	if ds := checkShop(t, files); len(ds) > 0 {
		t.Fatalf("refused:\n%s", strings.Join(ds, "\n"))
	}
}

// es-419 may leave out what es says, through CLDR's fallback chain.
func TestMessages_ALocaleFallsBackToAListedAncestor(t *testing.T) {
	files := shopFiles()
	files["nomi.toml"] = strings.Replace(shopManifest, `"pt-PT"]`, `"pt-PT", "es", "es-419"]`, 1)
	files["locales/es/cart.toml"] = entry("cart_items", cartItemsSource,
		".input {$count :integer}\n.match $count\none {{{$count} artículo}}\nmany {{{$count} artículos}}\n* {{{$count} artículos}}") +
		entry("order_status", orderStatusSource, ".input {$status :string}\n.match $status\nPending {{Pendiente}}\nShipped {{Enviado}}\n* {{Enviado}}")
	if ds := checkShop(t, files); len(ds) > 0 {
		t.Fatalf("refused:\n%s", strings.Join(ds, "\n"))
	}
}
