package vmhost

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/vmrunner"
)

// moneyProgram is a program over std/money and std/currency with body's
// declarations.
func moneyProgram(body string) string {
	money := "Money"
	if strings.Contains(body, "CurrencyDisplay") {
		money = "{CurrencyDisplay, Money}"
	}
	return "import {\n    std/currency.Currency\n    std/io\n    std/locale.Locale\n    std/money." + money + "\n}\n\n" + body
}

// moneyLiteralProgram formats literal, a Money literal, in pl and de.
func moneyLiteralProgram(literal string) string {
	return "import {\n    std/io\n    std/locale.Locale\n    std/money.Money\n}\n\n" +
		"fn main() {\n    price = " + literal + "\n" +
		"    io.print(Money.format(price, Locale`pl`))\n    io.print(Money.format(price, Locale`de`))\n}\n"
}

// passedDisplayProgram formats a euro price in pl and de with
// Money.format, the display handed in by main through a function of the
// program's own, so the display is a constant only where main builds it.
func passedDisplayProgram(display string) string {
	return moneyProgram("fn show(price: Money, locale: Locale, display: CurrencyDisplay) {\n" +
		"    io.print(Money.format(price, locale, display: display))\n}\n\n" +
		"fn main() {\n    price = #Money{amount: 12.5d, currency: Currency`EUR`}\n" +
		"    show(price, Locale`pl`, CurrencyDisplay." + display + ")\n" +
		"    show(price, Locale`de`, CurrencyDisplay." + display + ")\n}\n")
}

// currencyRecords counts the image data's records of each currency kind.
func currencyRecords(data []byte) map[string]int {
	counts := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		kind, _, _ := strings.Cut(line, "\t")
		if strings.HasPrefix(kind, "currency") || kind == "cardinal" {
			counts[kind]++
		}
	}
	return counts
}

// runsAsInProcess builds p, runs the image and requires it to print what
// the program prints in process, and want.
func runsAsInProcess(t *testing.T, name string, p *Program, image []byte, want string) {
	t.Helper()
	var inProcess bytes.Buffer
	if err := p.Run(context.Background(), &inProcess, nil, false); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if inProcess.String() != want {
		t.Errorf("%s in process prints:\n%s\nwant:\n%s", name, inProcess.String(), want)
	}
	var built, stderr bytes.Buffer
	if code := vmrunner.Run(image, &built, &stderr, nil, nil, false); code != 0 || built.String() != want {
		t.Errorf("%s: the built image exits %d and prints:\n%s%s\nwant:\n%s", name, code, built.String(), stderr.String(), want)
	}
}

// A program carries currency names only when it can write one, and the
// symbols of only the currencies its literals name when it makes no other.
func TestLocaleImage_CarriesTheCurrencyDataItCanFormat(t *testing.T) {
	const two = "fn main() {\n    price = #Money{amount: 12.5d, currency: Currency`EUR`}\n    io.print(Money.format(price, Locale`pl`%s))\n    io.print(Money.format(price, Locale`de`%s))\n}\n"
	main := func(display string) string {
		return moneyProgram(strings.ReplaceAll(two, "%s", display))
	}
	for _, c := range []struct {
		name, src, want string
		// names says whether the image carries names; symbols is how
		// many currency-symbol records each locale section holds, 0 for
		// every currency's.
		names   bool
		symbols int
	}{
		{"symbol", main(""), "12,50 €\n12,50 €\n", false, 1},
		{"narrow", main(", display: .Narrow"), "12,50 €\n12,50 €\n", false, 1},
		{"code", main(", display: CurrencyDisplay.Code"), "12,50 EUR\n12,50 EUR\n", false, 1},
		{"name", main(", display: .Name"), "12,50 euro\n12,50 Euro\n", true, 1},
		// A display the program picks while it runs may be the name.
		{"chosen display", moneyProgram(
			"fn shown(long: Bool) -> CurrencyDisplay {\n    if long { .Name } else { .Symbol }\n}\n\n" +
				"fn main() {\n    price = #Money{amount: 12.5d, currency: Currency`EUR`}\n" +
				"    io.print(Money.format(price, Locale`pl`, display: shown(False)))\n" +
				"    io.print(Money.format(price, Locale`de`, display: shown(False)))\n}\n"),
			"12,50 €\n12,50 €\n", true, 1},
		// A currency read while the program runs may be any of them.
		{"parsed currency", moneyProgram(
			"fn main() {\n    case Currency.parse(\"EUR\") {\n" +
				"        Ok(c) -> {\n            io.print(Money.format(#Money{amount: 12.5d, currency: c}, Locale`pl`))\n" +
				"            io.print(Money.format(#Money{amount: 12.5d, currency: c}, Locale`de`))\n        }\n" +
				"        Err(_) -> io.print(\"no\")\n    }\n}\n"),
			"12,50 €\n12,50 €\n", false, 0},
		// A custom currency has no CLDR symbol: the literal names none.
		{"custom", moneyProgram(
			"fn main() {\n    points = #Money{amount: 12d, currency: Currency`PTS:0`}\n" +
				"    io.print(Money.format(points, Locale`pl`))\n    io.print(Money.format(points, Locale`de`))\n}\n"),
			"12 PTS\n12 PTS\n", false, -1},
		// A Money literal names its currency as a Currency literal does.
		{"money literal", moneyLiteralProgram("Money`12.5 EUR`"), "12,50\u00a0€\n12,50\u00a0€\n", false, 1},
		{"custom money literal", moneyLiteralProgram("Money`12 PTS:0`"), "12\u00a0PTS\n12\u00a0PTS\n", false, -1},
		// A display passed through the program's own function: a `Code`
		// writes no name, and a `Name` does.
		{"passed code", passedDisplayProgram("Code"), "12,50\u00a0EUR\n12,50\u00a0EUR\n", false, 1},
		{"passed name", passedDisplayProgram("Name"), "12,50 euro\n12,50 Euro\n", true, 1},
	} {
		p := loadWithLocales(t, c.src, "pl", "de")
		data, image := imageLocales(t, p)
		counts := currencyRecords(data)
		if got := counts["currency-names"] > 0 || counts["currency-unit"] > 0; got != c.names {
			t.Errorf("%s: the image carries currency names: %v, want %v (%v)", c.name, got, c.names, counts)
		}
		if got := counts["cardinal"] > 0; got != c.names {
			t.Errorf("%s: the image carries plural rules: %v, want %v", c.name, got, c.names)
		}
		// pl, de and root.
		switch sections := 3; {
		case c.symbols < 0 && counts["currency-symbol"] != 0:
			t.Errorf("%s: %d currency-symbol records, want none", c.name, counts["currency-symbol"])
		case c.symbols > 0 && counts["currency-symbol"] != c.symbols*sections:
			t.Errorf("%s: %d currency-symbol records, want %d", c.name, counts["currency-symbol"], c.symbols*sections)
		case c.symbols == 0 && counts["currency-narrow"] < 100*sections:
			t.Errorf("%s: %d currency-narrow records, want every currency's", c.name, counts["currency-narrow"])
		}
		if c.names && counts["currency-names"] != c.symbols*2 && c.symbols > 0 {
			t.Errorf("%s: %d currency-names records, want one for EUR in pl and de", c.name, counts["currency-names"])
		}
		t.Logf("%s: %d bytes of locale data", c.name, len(data))
		runsAsInProcess(t, c.name, p, image, c.want)
	}
}

// The bytes a program that formats one euro price in two locales carries:
// its symbol alone, against every currency's symbols and names.
func TestLocaleImage_OneCurrencyCarriesAFewBytes(t *testing.T) {
	literal := moneyProgram("fn main() {\n    price = #Money{amount: 12.5d, currency: Currency`EUR`}\n" +
		"    io.print(Money.format(price, Locale`pl`))\n    io.print(Money.format(price, Locale`de`))\n}\n")
	any := moneyProgram("fn main() {\n    case Currency.parse(\"EUR\") {\n" +
		"        Ok(c) -> io.print(Money.format(#Money{amount: 12.5d, currency: c}, Locale`pl`, display: .Name))\n" +
		"        Err(_) -> io.print(\"no\")\n    }\n}\n")
	small, _ := imageLocales(t, loadWithLocales(t, literal, "pl", "de"))
	large, _ := imageLocales(t, loadWithLocales(t, any, "pl", "de"))
	if len(small) > 3000 {
		t.Errorf("one euro price in pl and de carries %d bytes of locale data, more than 3000", len(small))
	}
	if len(small)*10 > len(large) {
		t.Errorf("one euro price carries %d bytes, more than a tenth of every currency's symbols and names (%d)", len(small), len(large))
	}
	t.Logf("locale data: %d bytes for one currency's symbol, %d for every currency's symbols and names", len(small), len(large))
}

// A message's `:currency` carries names only when its text asks for them.
func TestLocaleImage_MessageCurrencyNames(t *testing.T) {
	for _, c := range []struct {
		name, option, want string
		names              bool
	}{
		{"symbol", "", "Razem: 12,50 €\n", false},
		{"code", " currencyDisplay=code", "Razem: 12,50 EUR\n", false},
		{"name", " currencyDisplay=name", "Razem: 12,50 euro\n", true},
	} {
		src := "import {\n    std/currency.Currency\n    std/io\n    std/locale.Locale\n    std/message.Message\n    std/money.Money\n}\n\n" +
			"message total(amount: Money) `Razem: {$amount :currency" + c.option + "}`\n\n" +
			"fn main() {\n    io.print(total(#Money{amount: 12.5d, currency: Currency`EUR`}) |> Message.format(Locale`pl`))\n}\n"
		m, err := analysis.ParseManifestData([]byte("[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[i18n]\nsource_locale = \"pl\"\nlocales = [\"pl\"]\n"), "<virtual nomi.toml>")
		if err != nil {
			t.Fatal(err)
		}
		p, err := LoadSource("main", src, WithVirtualManifest(m))
		if err != nil {
			t.Fatalf("%s: the front end rejects the program: %v", c.name, err)
		}
		data, image := imageLocales(t, p)
		counts := currencyRecords(data)
		if got := counts["currency-names"] > 0; got != c.names {
			t.Errorf("%s: the image carries currency names: %v, want %v (%v)", c.name, got, c.names, counts)
		}
		if counts["currency-symbol"] != 2 {
			t.Errorf("%s: %d currency-symbol records, want EUR's in pl and root", c.name, counts["currency-symbol"])
		}
		runsAsInProcess(t, c.name, p, image, c.want)
	}
}
