package vmhost

import (
	"context"
	"strings"
	"testing"
)

// Currency, skeleton and fixed-pattern literals are values of their type,
// checked when the program loads.
func TestMoneyAndDateLiterals_AreTheirValues(t *testing.T) {
	const src = "import {\n" +
		"    std/calendar.{Date, DateFields, DatePattern}\n" +
		"    std/currency.Currency\n" +
		"    std/io\n" +
		"    std/locale.Locale\n" +
		"    std/money.Money\n" +
		"}\n\n" +
		"once bitcoin = Currency`BTC:8`\n\n" +
		"fn main() {\n" +
		"    io.print(Debug.inspect(Currency`EUR`))\n" +
		"    io.print(Currency.minor_digits(bitcoin))\n" +
		"    io.print(Date.format_fields(Date`2026-10-20`, Locale`pl`, DateFields`yMMMMd`))\n" +
		"    io.print(Date.to_string_with(Date`2026-10-20`, DatePattern`yyyyMMdd`))\n" +
		"    io.print(Money`1299.50 USD` == #Money{amount: 1299.50d, currency: Currency`USD`})\n" +
		"    io.print(Money`-5.00 USD`)\n" +
		"    io.print(Money`12.5 EUR`)\n" +
		"    io.print(Money`1200 PTS:0`)\n" +
		"    amount = \"12\"\n" +
		"    io.print(Display.to_string(Money\"${amount} USD\"))\n" +
		"}\n"
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects valid literals: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "Currency`EUR`\n8\n20 października 2026\n20261020\n" +
		"True\n-5.00 USD\n12.5 EUR\n1200 PTS:0\n" +
		"Err(a Money literal does not interpolate: build the value from its fields, as #Money{amount: amount, currency: Currency`EUR`})\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A malformed currency, skeleton or fixed pattern is a compile error at the
// literal, worded by the error its handler returns.
func TestMoneyAndDateLiterals_InvalidOnesAreCompileErrors(t *testing.T) {
	for _, c := range []struct{ imports, literal, message string }{
		{"std/currency.Currency", "Currency`EUR:2`",
			"typed literal Currency`EUR:2` is invalid: `EUR` is an ISO 4217 currency, whose minor digits CLDR gives: write Currency`EUR`"},
		{"std/currency.Currency", "Currency`BTC`",
			"typed literal Currency`BTC` is invalid: `BTC` is not an ISO 4217 currency: a custom currency names its minor digits, as Currency`BTC:2`"},
		{"std/currency.Currency", "Currency`eur`",
			"typed literal Currency`eur` is invalid: `eur` is not a currency code: a code is 1 to 12 of A-Z and 0-9, starting with a letter"},
		{"std/currency.Currency", "Currency`PTS:19`",
			"typed literal Currency`PTS:19` is invalid: a currency has 0 to 18 minor digits, not 19"},
		{"std/money.Money", "Money`12,50 EUR`",
			"typed literal Money`12,50 EUR` is invalid: `12,50` is not an amount: write digits with an optional sign and decimal point, as Money`-12.50 EUR`"},
		{"std/money.Money", "Money`12.50`",
			"typed literal Money`12.50` is invalid: `12.50` has no currency: write the amount, one space and the currency, as Money`12.50 EUR`"},
		{"std/money.Money", "Money`12.50  EUR`",
			"typed literal Money`12.50  EUR` is invalid: `12.50  EUR` is not an amount and a currency: write the amount, one space and the currency, as Money`12.50 EUR`"},
		{"std/money.Money", "Money`EUR`",
			"typed literal Money`EUR` is invalid: `EUR` is not an amount and a currency: write the amount, one space and the currency, as Money`12.50 EUR`"},
		{"std/money.Money", "Money`5 ABC`",
			"typed literal Money`5 ABC` is invalid: `ABC` is not an ISO 4217 currency: a custom currency names its minor digits, as Money`5 ABC:2`"},
		{"std/money.Money", "Money`5 EUR:2`",
			"typed literal Money`5 EUR:2` is invalid: `EUR` is an ISO 4217 currency, whose minor digits CLDR gives: write Money`5 EUR`"},
		{"std/money.Money", "Money`5 eur`",
			"typed literal Money`5 eur` is invalid: `eur` is not a currency code: a code is 1 to 12 of A-Z and 0-9, starting with a letter"},
		{"std/money.Money", "Money`5 PTS:19`",
			"typed literal Money`5 PTS:19` is invalid: a currency has 0 to 18 minor digits, not 19"},
		{"std/calendar.DateFields", "DateFields`yMMMdq`",
			"typed literal DateFields`yMMMdq` is invalid: invalid format: `q` is not a field a skeleton here names: it names fields with G (era), y (year), M or L (month), d (day) and E (weekday)"},
		{"std/calendar.DateFields", "DateFields`yMMMx`",
			"typed literal DateFields`yMMMx` is invalid: invalid format: `x` is not a field a skeleton here names: it names fields with G (era), y (year), M or L (month), d (day) and E (weekday)"},
		{"std/calendar.DateFields", "DateFields`yMMMdHH`",
			"typed literal DateFields`yMMMdHH` is invalid: invalid format: `H` is a time of day, which a DateFields does not hold: use TimeFields or DateTimeFields"},
		{"std/calendar.DateFields", "DateFields`yMMMdM`",
			"typed literal DateFields`yMMMdM` is invalid: invalid format: `M` and `M` both name the month; a skeleton names each field once"},
		{"std/calendar.TimeFields", "TimeFields`yjm`",
			"typed literal TimeFields`yjm` is invalid: invalid format: `y` is part of a date, which a TimeFields does not hold: use DateFields or DateTimeFields"},
		{"std/calendar.DatePattern", "DatePattern`dd MMM yyyy`",
			"typed literal DatePattern`dd MMM yyyy` is invalid: invalid format: `MMM` writes the month's name, which is a word of a language: a fixed pattern writes numbers, the same in every locale; write it for a reader with a skeleton and a locale, as Date.format_fields(date, locale, DateFields`yMMMd`)"},
		{"std/calendar.DatePattern", "DatePattern`EEEE dd`",
			"typed literal DatePattern`EEEE dd` is invalid: invalid format: `EEEE` writes the day of the week, which is a word of a language: a fixed pattern writes numbers, the same in every locale; write it for a reader with a skeleton and a locale, as Date.format_fields(date, locale, DateFields`yMMMd`)"},
		{"std/calendar.TimePattern", "TimePattern`hh:mm a`",
			"typed literal TimePattern`hh:mm a` is invalid: invalid format: `h` is an hour of a 12-hour clock, which needs AM or PM, a word: a fixed pattern writes the 24-hour `HH`"},
		{"std/calendar.DateTimePattern", "DateTimePattern`yyyy-MM-dd'T'HH:mm z`",
			"typed literal DateTimePattern`yyyy-MM-dd'T'HH:mm z` is invalid: invalid format: `z` writes a time zone, which a fixed pattern does not hold"},
	} {
		src := "import " + c.imports + "\n\nfn main() {\n    _ = " + c.literal + "\n}\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.literal, len(ds), ds)
		}
		if ds[0].Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.literal, ds[0].Message, c.message)
		}
		if ds[0].Code != InvalidLiteralCode {
			t.Errorf("%s: code = %q, want %q", c.literal, ds[0].Code, InvalidLiteralCode)
		}
	}
}
