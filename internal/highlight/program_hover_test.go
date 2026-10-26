package highlight_test

import (
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// TestProgramOfModulesAnswersEachTab holds a block that names modules (the
// tour's Macros chapter: a shop whose go.mod requires the csvdata macro
// module under macros/csvdata/) to an analysis of its files as written
// under a directory: a call in main.nomi into the table a macro declared
// hovers with the function the macro wrote, and the macro module's own
// file, analyzed as its module's, has tokens.
func TestProgramOfModulesAnswersEachTab(t *testing.T) {
	read := func(path string) string {
		data, err := os.ReadFile("../../testdata/macros/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	src := "// FILE: main.nomi\n" + read("shop/main.nomi") +
		"\n// FILE: tables.nomi\n" + read("shop/tables.nomi") +
		"\n// FILE: data/prices.csv\n" + read("shop/data/prices.csv") +
		"\n// FILE: data/names.csv\n" + read("shop/data/names.csv") +
		"\n// FILE: nomi.toml\n" + read("shop/nomi.toml") +
		"\n// FILE: go.mod\n" + strings.Replace(read("shop/go.mod"), "../csvdata", "./macros/csvdata", 1) +
		"\n// FILE: macros/csvdata/csvdata.nomi\n" + read("csvdata/csvdata.nomi") +
		"\n// FILE: macros/csvdata/nomi.toml\n[module]\nname = \"csvdata\"\n" +
		"\n// FILE: macros/csvdata/go.mod\n" + read("csvdata/go.mod")
	prog := highlight.AnalyzeProgram(src, t.TempDir(), std.Shared(), vmhost.Macros())

	got := prog.HoverIn("main.nomi", 9, colOf(t, prog, "main.nomi", 9, "price"))
	if !strings.Contains(got, "fn price(sku: String) -> Maybe<Int>") {
		t.Errorf("hover on tables.price in main.nomi = %q; want the function the macro declared", got)
	}
	for _, name := range []string{"main.nomi", "tables.nomi", "macros/csvdata/csvdata.nomi"} {
		if toks := prog.SemanticTokensIn(name); len(toks) == 0 {
			t.Errorf("%s has no semantic tokens", name)
		}
	}
}

// programHoverSrc is the tour's first translating-text example: a call in
// main.nomi into a message declared in cart.nomi, whose Polish text is a
// catalog and whose locales the manifest lists.
const programHoverSrc = `// FILE: main.nomi
import {
    std/io
    std/locale.Locale
    std/message.Message
    cart
}

fn main() {
    [1, 2]
    |> Iter.each(fn n -> {
        badge = cart.cart_count(n)
        io.print(Message.format(badge, Locale` + "`en`" + `))
    })
}

// FILE: cart.nomi
/// The cart badge, such as "3 items in your cart".
pub message cart_count(count: Int) ` + "`" + `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    ` + "`" + `

pub fn total(n: Int) -> Int { n * 2 }

// FILE: locales/pl/cart.toml
[cart_count]
source = '''
.input {$count :integer}
.match $count
one {{{$count} item in your cart}}
* {{{$count} items in your cart}}
'''
text = '''
.input {$count :integer}
.match $count
one {{{$count} produkt w koszyku}}
* {{{$count} produktu w koszyku}}
'''

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]

[i18n]
locales = ["en", "pl"]
`

// colOf is the 1-based column of the first occurrence of sub on line (1-based)
// of the file name's text in src.
func colOf(t *testing.T, prog *highlight.Program, name string, line int, sub string) int {
	t.Helper()
	f, ok := prog.File(name)
	if !ok {
		t.Fatalf("no analysis of %s", name)
	}
	lines := strings.Split(f.Source, "\n")
	i := strings.Index(lines[line-1], sub)
	if i < 0 {
		t.Fatalf("%s line %d (%q) has no %q", name, line, lines[line-1], sub)
	}
	return i + 1
}

// TestProgramHoverResolvesAcrossFiles holds the tour's multi-file hover to
// the whole program: a call in main.nomi into cart.nomi shows the
// message's signature and doc from cart.nomi, and a declaration inside
// cart.nomi answers too.
func TestProgramHoverResolvesAcrossFiles(t *testing.T) {
	prog := highlight.AnalyzeProgram(programHoverSrc, "", std.Shared(), nil)

	got := prog.HoverIn("main.nomi", 11, colOf(t, prog, "main.nomi", 11, "cart_count"))
	if !strings.Contains(got, "cart_count(count: Int)") || !strings.Contains(got, "The cart badge") {
		t.Errorf("hover on cart.cart_count in main.nomi = %q; want the message's signature and doc", got)
	}

	got = prog.HoverIn("cart.nomi", 9, colOf(t, prog, "cart.nomi", 9, "total"))
	if !strings.Contains(got, "fn total(n: Int) -> Int") {
		t.Errorf("hover on fn total in cart.nomi = %q; want its signature", got)
	}

	if got := prog.HoverIn("nomi.toml", 1, 2); got != "" {
		t.Errorf("hover in nomi.toml = %q; want none", got)
	}
	if toks := prog.SemanticTokensIn("main.nomi"); len(toks) == 0 {
		t.Error("main.nomi has no semantic tokens")
	}
}

// TestProgramHoverSurvivesAnError holds hover to the program's analyses when
// the program has a type error: the cross-file call still resolves.
func TestProgramHoverSurvivesAnError(t *testing.T) {
	src := strings.Replace(programHoverSrc, "fn main() {\n", "fn main() {\n    x: Int = \"no\"\n", 1)
	prog := highlight.AnalyzeProgram(src, "", std.Shared(), nil)
	got := prog.HoverIn("main.nomi", 12, colOf(t, prog, "main.nomi", 12, "cart_count"))
	if !strings.Contains(got, "cart_count(count: Int)") {
		t.Errorf("hover on cart.cart_count with a type error elsewhere = %q; want the message's signature", got)
	}
}
