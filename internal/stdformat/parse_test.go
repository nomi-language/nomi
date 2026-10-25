package stdformat

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

func errText(e rt.NumberParseError) string {
	switch e.Tag {
	case rt.TagNumberParseEmpty:
		return "Empty"
	case rt.TagNumberParseUnexpected:
		return fmt.Sprintf("Unexpected{at: %d, text: %q}", e.At, e.Text)
	case rt.TagNumberParseGrouping:
		return fmt.Sprintf("Grouping{at: %d}", e.At)
	case rt.TagNumberParseOutOfRange:
		return "OutOfRange"
	}
	return fmt.Sprintf("tag %d", e.Tag)
}

func intResult(r rt.Result[int64, rt.NumberParseError]) string {
	if r.Tag == rt.TagOk {
		return fmt.Sprint(r.Ok)
	}
	return errText(r.Err)
}

func decResult(r rt.Result[string, rt.NumberParseError]) string {
	if r.Tag == rt.TagOk {
		return r.Ok
	}
	return errText(r.Err)
}

func TestParseInt(t *testing.T) {
	fr := full()
	for _, c := range []struct {
		locale, text string
		lenient      bool
		want         string
	}{
		{"en", "1,234,567", false, "1234567"},
		{"en", "1234567", false, "1234567"}, // separators may be left out
		{"en", "-42", false, "-42"},
		{"de", "1.234.567", false, "1234567"},
		{"fr", "1 234 567", false, "1234567"}, // any space for U+202F
		{"fr", "1 234", false, "1234"},
		{"pl", "1 234", false, "1234"},
		{"hi", "12,34,567", false, "1234567"},
		{"hi", "1,234,567", false, "Grouping{at: 1}"},
		{"en", "12,34,567", false, "Grouping{at: 2}"},
		{"en", "1,23", false, "Grouping{at: 1}"},
		{"en", "1234,567", false, "Grouping{at: 4}"},
		{"en", "1,2345", false, "Grouping{at: 1}"},
		{"en", ",123", false, `Unexpected{at: 0, text: ","}`},
		{"en", "12a", false, `Unexpected{at: 2, text: "a"}`},
		{"en", "12.5", false, `Unexpected{at: 2, text: "."}`},
		{"en", "", false, "Empty"},
		{"en", " 12", false, `Unexpected{at: 0, text: " "}`},
		{"en", "+12", false, `Unexpected{at: 0, text: "+"}`},
		{"en", "-", false, `Unexpected{at: 1, text: ""}`},
		{"en", "9223372036854775807", false, "9223372036854775807"},
		{"en", "-9223372036854775808", false, "-9223372036854775808"},
		{"en", "9223372036854775808", false, "OutOfRange"},
		{"ar-EG", "١٬٢٣٤", false, "1234"},
		{"ar-EG", "1234", false, "1234"}, // ASCII digits for the locale's
		{"ar-EG", "؜-٥", false, "-5"},
		{"sv", "−42", false, "-42"},
		{"sv", "-42", false, `Unexpected{at: 0, text: "-"}`},
		// Lenient.
		{"en", "  12,34,567 ", true, "1234567"},
		{"en", "+12", true, "12"},
		{"en", "(12)", true, "-12"},
		{"en", "12-", true, "-12"},
		{"sv", "-42", true, "-42"},
		{"ar-EG", "-٥", true, "-5"},
		{"en", "   ", true, "Empty"},
		{"en", "12x", true, `Unexpected{at: 2, text: "x"}`},
	} {
		if got := intResult(FFIParseInt(fr, c.text, c.locale, c.lenient)); got != c.want {
			t.Errorf("Int.parse_in(%q, %s, lenient: %v) = %s, want %s", c.text, c.locale, c.lenient, got, c.want)
		}
	}
}

func TestParseDecimal(t *testing.T) {
	fr := full()
	for _, c := range []struct {
		locale, text string
		lenient      bool
		want         string
	}{
		{"de", "1.234,5", false, "1234.5"},
		{"en", "1.234,5", false, `Unexpected{at: 5, text: ","}`}, // no grouping after the decimal point
		{"pl", "12,50", false, "12.50"},                          // the typed scale is kept
		{"en", "0.000", false, "0.000"},
		{"en", ".5", false, `Unexpected{at: 0, text: "."}`},
		{"en", "12.", false, `Unexpected{at: 2, text: "."}`},
		{"en", ".5", true, "0.5"},
		{"en", "-1,234.50", false, "-1234.50"},
		{"de-CH", "1'234.5", false, "1234.5"},
		{"en", "∞", false, "OutOfRange"},
	} {
		if got := decResult(FFIParseDecimal(fr, c.text, c.locale, c.lenient)); got != c.want {
			t.Errorf("Decimal.parse_in(%q, %s, lenient: %v) = %s, want %s", c.text, c.locale, c.lenient, got, c.want)
		}
	}
	for _, c := range []struct {
		locale, text string
		lenient      bool
		want         string
	}{
		{"en", "12.5%", false, "0.125"},
		{"en", "50%", false, "0.50"},
		{"fr", "12,5 %", false, "0.125"},
		{"fr", "12,5 %", false, "0.125"},
		{"de", "-3 %", false, "-0.03"},
		{"en", "12.5", false, `Unexpected{at: 4, text: ""}`},
		{"en", "12.5 %", false, `Unexpected{at: 4, text: " "}`},
		{"tr", "%50", false, "0.50"},
		{"en", "12.5", true, `Unexpected{at: 4, text: ""}`},
		{"en", "12.5 %", true, "0.125"},
		{"en", "% 12.5", true, "0.125"},
		{"ar-EG", "٥٠٪؜", false, "0.50"},
		{"ar-EG", "٥٠٪", true, "0.50"},
	} {
		if got := decResult(FFIParseDecimalPercent(fr, c.text, c.locale, c.lenient)); got != c.want {
			t.Errorf("Decimal.parse_percent_in(%q, %s, lenient: %v) = %s, want %s", c.text, c.locale, c.lenient, got, c.want)
		}
	}
}

func TestParseFloat(t *testing.T) {
	fr := full()
	r := FFIParseFloat(fr, "1.234.567,89", "de", false)
	if r.Tag != rt.TagOk || r.Ok != 1234567.89 {
		t.Errorf("Float.parse_in de: %+v", r)
	}
	if r := FFIParseFloat(fr, "-∞", "en", false); r.Tag != rt.TagOk || !math.IsInf(r.Ok, -1) {
		t.Errorf("-∞: %+v", r)
	}
	if r := FFIParseFloat(fr, "NaN", "en", false); r.Tag != rt.TagOk || !math.IsNaN(r.Ok) {
		t.Errorf("NaN: %+v", r)
	}
	if r := FFIParseFloat(fr, "1"+string(make([]byte, 0))+"e400", "en", false); r.Tag != rt.TagErr {
		t.Errorf("1e400: %+v", r)
	}
	big := "1"
	for range 400 {
		big += "0"
	}
	if r := FFIParseFloat(fr, big, "en", false); r.Tag != rt.TagErr || r.Err.Tag != rt.TagNumberParseOutOfRange {
		t.Errorf("10^400: %+v", r)
	}
}

// Every format reads back as the value it was made from, in every locale
// of a sample and both modes.
func TestFormatThenParseRoundTrips(t *testing.T) {
	fr := full()
	locales := []string{"en", "de", "fr", "pl", "hi", "ar", "ar-EG", "ar-EG-u-nu-latn", "fa", "bn", "ja", "sv",
		"de-CH", "es", "pt", "ru", "he", "tr", "my", "mr", "ne", "ps", "ur-IN"}
	ints := []int64{0, 7, -7, 1234, -1234, 12345, 1234567, -1234567890, math.MaxInt64, math.MinInt64}
	decimals := []string{"0", "0.5", "-0.05", "1234.50", "-1234567.125", "0.000"}
	for _, loc := range locales {
		for _, n := range ints {
			for _, grouping := range []bool{true, false} {
				text := FFIFormatInt(fr, loc, n, grouping, 1)
				for _, lenient := range []bool{false, true} {
					if got := intResult(FFIParseInt(fr, text, loc, lenient)); got != fmt.Sprint(n) {
						t.Errorf("%s: %d formats as %q, which parses (lenient %v) as %s", loc, n, text, lenient, got)
					}
				}
			}
		}
		for _, d := range decimals {
			text := FFIFormatDecimal(fr, loc, d, 0, 100, true)
			if got := decResult(FFIParseDecimal(fr, text, loc, false)); got != d {
				t.Errorf("%s: %s formats as %q, which parses as %s", loc, d, text, got)
			}
			pct := FFIFormatDecimalPercent(fr, loc, timesHundred(d), 0, 100)
			if got := decResult(FFIParseDecimalPercent(fr, pct, loc, false)); got != stripScaleTo(d) {
				t.Errorf("%s: %s formats as the percentage %q, which parses as %s", loc, d, pct, got)
			}
		}
		f := -1234567.25
		text := FFIFormatFloat(fr, loc, f, 0, 3, true)
		if r := FFIParseFloat(fr, text, loc, false); r.Tag != rt.TagOk || r.Ok != f {
			t.Errorf("%s: %v formats as %q, which parses as %+v", loc, f, text, r)
		}
	}
}

// stripScaleTo is the Decimal a percentage of d reads back as: d with at
// least two fraction digits, since 1d is 100% and 100% is 1.00.
// timesHundred is the Decimal text d times 100, as std/decimal hands
// `Decimal.format_percent`'s crossing its percentage: 0.125 is 12.5.
func timesHundred(d string) string {
	neg, i, frac := decimalParts(d)
	for len(frac) < 2 {
		frac += "0"
	}
	i = strings.TrimLeft(i+frac[:2], "0")
	if i == "" {
		i = "0"
	}
	s := i
	if frac[2:] != "" {
		s += "." + frac[2:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

func stripScaleTo(d string) string {
	neg, i, frac := decimalParts(d)
	for len(frac) < 2 {
		frac += "0"
	}
	s := i + "." + frac
	if neg {
		s = "-" + s
	}
	return s
}
