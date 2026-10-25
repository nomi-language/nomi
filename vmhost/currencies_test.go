package vmhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

// pointsToml is a module that lists two custom currencies.
const pointsToml = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[money]\ncustom_currencies = [\"PTS:0\", \"BTC:8\"]\n"

// currencyProgram prints each of exprs, each a Currency.
func currencyProgram(exprs ...string) string {
	all := strings.Join(exprs, " ")
	src := "import {\n    std/io\n"
	if strings.Contains(all, "Currency") {
		src += "    std/currency.Currency\n"
	}
	if strings.Contains(all, "Money") {
		src += "    std/money.Money\n"
	}
	src += "}\n\nfn main() {\n"
	for _, e := range exprs {
		src += "    io.print(Debug.inspect(" + e + "))\n"
	}
	return src + "}\n"
}

// oneDiagnostic is the single diagnostic err holds.
func oneDiagnostic(t *testing.T, err error) Diagnostic {
	t.Helper()
	var ds Diagnostics
	if !errors.As(err, &ds) || len(ds) != 1 {
		t.Fatalf("the load failed with %T %v; want one diagnostic", err, err)
	}
	return ds[0]
}

// A listed custom currency, and any ISO one, may be written in a literal.
func TestCurrencyLiteral_ListedIsAccepted(t *testing.T) {
	src := currencyProgram("Currency`PTS:0`", "Money`1200 PTS:0`.currency", "Money`0.5 BTC:8`.currency", "Currency`EUR`")
	p, err := loadManifest(t, src, pointsToml)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "Currency`PTS:0`\nCurrency`PTS:0`\nCurrency`BTC:8`\nCurrency`EUR`\n"; out.String() != want {
		t.Errorf("prints %q, want %q", out.String(), want)
	}
}

// An unlisted custom currency is a compile error at the literal naming the
// list's table and file, with a close listed one as a hint.
func TestCurrencyLiteral_UnlistedIsACompileError(t *testing.T) {
	for _, c := range []struct{ lit, want, hint string }{
		{"Currency`PST:0`",
			"typed literal Currency`PST:0`: `PST:0` is a custom currency this module does not list: add it to [money].custom_currencies in nomi.toml",
			"did you mean `PTS:0`? nomi.toml lists it"},
		{"Money`5 PTS:2`.currency",
			"typed literal Money`5 PTS:2`: `PTS:2` is a custom currency this module does not list: add it to [money].custom_currencies in nomi.toml",
			"did you mean `PTS:0`? nomi.toml lists it"},
		{"Currency`GEMS:0`",
			"typed literal Currency`GEMS:0`: `GEMS:0` is a custom currency this module does not list: add it to [money].custom_currencies in nomi.toml",
			""},
	} {
		_, err := loadManifest(t, currencyProgram(c.lit), pointsToml)
		if err == nil {
			t.Errorf("%s loads; want a compile error", c.lit)
			continue
		}
		d := oneDiagnostic(t, err)
		if d.Message != c.want || d.Line != 7 || d.Code != InvalidLiteralCode {
			t.Errorf("diagnostic at %d (%s): %q\nwant at line 7: %q", d.Line, d.Code, d.Message, c.want)
		}
		var want []string
		if c.hint != "" {
			want = []string{c.hint}
		}
		if !slices.Equal(d.Hints, want) {
			t.Errorf("%s: hints %q, want %q", c.lit, d.Hints, want)
		}
	}
}

// A module with no [money] table, or an empty list, allows no custom
// currency literal; a program with no module allows any.
func TestCurrencyLiteral_NoListAndNoModule(t *testing.T) {
	src := currencyProgram("Currency`PTS:0`")
	for _, toml := range []string{
		"[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[money]\ncustom_currencies = []\n",
	} {
		if _, err := loadManifest(t, src, toml); err == nil || !strings.Contains(err.Error(), "`PTS:0` is a custom currency this module does not list") {
			t.Errorf("under %q: %v; want the literal unlisted", toml, err)
		}
	}
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatalf("with no module: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil || out.String() != "Currency`PTS:0`\n" {
		t.Errorf("with no module prints %q (%v)", out.String(), err)
	}
}

// A currency made from values while the program runs is not checked, nor
// is one Currency.parse reads.
func TestCurrency_RuntimeIsUnrestricted(t *testing.T) {
	src := "import {\n    std/io\n    std/currency.Currency\n}\n\nfn main() {\n" +
		"    code = \"GEMS\"\n    digits = 3\n" +
		"    io.print(Debug.inspect(Currency.custom(code, digits)))\n" +
		"    io.print(Debug.inspect(Currency.parse(\"PST:0\")))\n}\n"
	p, err := loadManifest(t, src, pointsToml)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "Ok(Currency`GEMS:3`)\nOk(Currency`PST:0`)\n"; out.String() != want {
		t.Errorf("prints %q, want %q", out.String(), want)
	}
	problems, err := CheckLowering(context.Background(), "main.nomi", src, WithVirtualManifest(mustManifest(t, pointsToml)))
	if err != nil || problems != nil {
		t.Errorf("CheckLowering answers %v, %v; want nothing for calls with values", problems, err)
	}
}

// A Currency.custom call whose code and digits are literals is checked as
// the literal is: unlisted, it is a compile error at the call naming the
// literal form.
func TestCurrencyCustomCall_LiteralArgumentsUnlistedIsACompileError(t *testing.T) {
	src := currencyProgram("Currency.custom(\"PST\", 0)")
	_, err := loadManifest(t, src, pointsToml)
	if err == nil {
		t.Fatal("the program loads; want the call unlisted")
	}
	d := oneDiagnostic(t, err)
	want := "Currency.custom(\"PST\", 0): `PST:0` is a custom currency this module does not list: add it to [money].custom_currencies in nomi.toml"
	if d.Message != want || d.Line != 7 || d.Col != 28 || d.Code != UnlistedCurrencyCode {
		t.Errorf("diagnostic at %d:%d (%s): %q\nwant at 7:28: %q", d.Line, d.Col, d.Code, d.Message, want)
	}
	wantHints := []string{"did you mean `PTS:0`? nomi.toml lists it", "write Currency`PST:0`, which the compiler checks"}
	if !slices.Equal(d.Hints, wantHints) {
		t.Errorf("hints %q, want %q", d.Hints, wantHints)
	}
	// Named arguments are literals too.
	named := currencyProgram("Currency.custom(code: \"PST\", minor_digits: 0)")
	if _, err := loadManifest(t, named, pointsToml); err == nil {
		t.Error("the named-argument call loads; want it unlisted")
	}
}

// A listed literal call, and any literal call outside a module, runs; the
// language server's lowering answers a hint naming the literal form.
func TestCurrencyCustomCall_LiteralArgumentsListedIsAHint(t *testing.T) {
	src := currencyProgram("Currency.custom(\"PTS\", 0)")
	for _, c := range []struct {
		name string
		opts []Option
	}{
		{"listed", []Option{WithVirtualManifest(mustManifest(t, pointsToml))}},
		{"no module", nil},
	} {
		p, err := LoadSource("main", src, c.opts...)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if err := p.Run(context.Background(), io.Discard, nil, false); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		problems, err := CheckLowering(context.Background(), "main.nomi", src, c.opts...)
		if err != nil {
			t.Fatal(err)
		}
		var ds Diagnostics
		if !errors.As(problems, &ds) || len(ds) != 1 {
			t.Fatalf("%s: CheckLowering answers %v; want one hint", c.name, problems)
		}
		want := "Currency.custom(\"PTS\", 0) makes a fixed currency: write Currency`PTS:0`, which the compiler checks"
		if ds[0].Message != want || ds[0].Code != CustomCurrencyCallCode || ds[0].Line != 7 {
			t.Errorf("%s: %d (%s) %q, want at line 7 %q", c.name, ds[0].Line, ds[0].Code, ds[0].Message, want)
		}
	}
	// A call that does not make a custom currency is no hint.
	for _, call := range []string{"Currency.custom(\"EUR\", 2)", "Currency.custom(\"POINTS\", 19)"} {
		problems, err := CheckLowering(context.Background(), "main.nomi", currencyProgram(call),
			WithVirtualManifest(mustManifest(t, pointsToml)))
		if err != nil || problems != nil {
			t.Errorf("%s: CheckLowering answers %v, %v; want nothing", call, problems, err)
		}
	}
}

// currencyModules is an application and a library it requires, each with
// its own list: the library lists GEMS:2 and writes it, and the
// application lists PTS:0 and writes it.
const currencyModules = `// FILE: app/main.nomi
import {
    std/io
    std/currency.Currency
    gems/shop
}

fn main() {
    io.print(Debug.inspect(Currency` + "`PTS:0`" + `))
    io.print(Debug.inspect(shop.gems))
}
// FILE: app/nomi.toml
[module]
name = "app"
entry_points = ["main"]

[money]
custom_currencies = ["PTS:0"]
// FILE: app/go.mod
module app

go 1.27.0

require gems v0.0.0

replace gems => ../gems
// FILE: gems/shop.nomi
import std/currency.Currency

pub once gems = Currency` + "`GEMS:2`" + `
// FILE: gems/nomi.toml
[module]
name = "gems"

[money]
custom_currencies = ["GEMS:2"]
// FILE: gems/go.mod
module gems

go 1.27.0
`

// Each module's literals answer to its own list: the application's list
// does not need the library's currency, and does not allow it in the
// library either.
func TestCurrencyLiteral_TheListIsPerModule(t *testing.T) {
	entry, err := writeInput(t.TempDir(), currencyModules)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Load(entry, WithOutput(io.Discard), WithErrorOutput(io.Discard))
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "Currency`PTS:0`\nCurrency`GEMS:2`\n"; out.String() != want {
		t.Errorf("prints %q, want %q", out.String(), want)
	}

	// The library writing the application's currency is an error in the
	// library, naming the library's nomi.toml.
	swapped := strings.Replace(currencyModules, "pub once gems = Currency`GEMS:2`", "pub once gems = Currency`PTS:0`", 1)
	entry, err = writeInput(t.TempDir(), swapped)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(entry, WithOutput(io.Discard), WithErrorOutput(io.Discard))
	if err == nil {
		t.Fatal("the program loads; want the library's PTS:0 unlisted")
	}
	d := oneDiagnostic(t, err)
	if !strings.HasSuffix(d.Path, "gems/shop.nomi") || !strings.Contains(d.Message, "`PTS:0` is a custom currency this module does not list: add it to [money].custom_currencies in ") ||
		!strings.HasSuffix(d.Message, "gems/nomi.toml") {
		t.Errorf("diagnostic in %s: %q; want the library's, naming its nomi.toml", d.Path, d.Message)
	}
}
