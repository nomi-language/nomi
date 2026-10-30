package stdformat

import (
	"context"
	"math"
	"testing"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/rt"
)

// full is a frame over every locale's data.
func full() *rt.Frame { return rt.NewFrame(cldrdata.WithData(context.Background(), cldrfull.Full())) }

// only is a frame over a program's data that lists locales.
func only(locales ...string) *rt.Frame {
	return rt.NewFrame(cldrdata.WithData(context.Background(), cldrfull.ForProgram(locales, "")))
}

func TestFormatInt(t *testing.T) {
	fr := full()
	for _, c := range []struct {
		locale string
		n      int64
		want   string
	}{
		{"en", 1234567, "1,234,567"},
		{"de", 1234567, "1.234.567"},
		{"fr", 1234567, "1 234 567"},
		{"pl", 1234567, "1 234 567"},
		{"pl", 1234, "1234"}, // minimumGroupingDigits 2
		{"pl", 12345, "12 345"},
		{"hi", 1234567, "12,34,567"},
		{"en-IN", 123456789, "12,34,56,789"},
		{"de-CH", 1234567, "1'234'567"},
		{"en", -42, "-42"},
		{"sv", -42, "−42"},
		{"ar-EG", 1234567, "١٬٢٣٤٬٥٦٧"},
		{"ar-EG", -5, "؜-٥"},
		{"ar-EG-u-nu-latn", 1234567, "1,234,567"},
		{"en-u-nu-arab", 12, "١٢"},
		{"hi-u-nu-native", 1234, "१,२३४"},
		{"ja", 1234567, "1,234,567"},
		{"en", math.MinInt64, "-9,223,372,036,854,775,808"},
		{"qaa", 1234567, "1,234,567"}, // no data: root
	} {
		if got := FFIFormatInt(fr, c.locale, rt.SmallInt(c.n), true, 1); got != c.want {
			t.Errorf("Int.format(%d, %s) = %q, want %q", c.n, c.locale, got, c.want)
		}
	}
	if got := FFIFormatInt(fr, "en", rt.SmallInt(1234567), false, 1); got != "1234567" {
		t.Errorf("grouping: False gives %q", got)
	}
	if got := FFIFormatInt(fr, "en", rt.SmallInt(42), true, 5); got != "00,042" {
		t.Errorf("min_digits: 5 gives %q", got)
	}
}

func TestFormatFloat(t *testing.T) {
	fr := full()
	for _, c := range []struct {
		locale     string
		f          float64
		minF, maxF int64
		want       string
	}{
		{"en", 1234567.891, 0, 3, "1,234,567.891"},
		{"de", 1234567.89, 0, 3, "1.234.567,89"},
		{"en", 1.5, 2, 2, "1.50"},
		{"en", 2.5, 0, 0, "2"},   // half to even
		{"en", 3.5, 0, 0, "4"},   // half to even
		{"en", 1.005, 0, 2, "1"}, // the shortest decimal is 1.005: half to even
		{"en", 1.015, 0, 2, "1.02"},
		{"en", 0.1 + 0.2, 0, 3, "0.3"},
		{"en", 999.9999, 0, 3, "1,000"},
		{"en", 1e21, 0, 0, "1,000,000,000,000,000,000,000"},
		{"en", 1.5e-7, 0, 8, "0.00000015"},
		{"en", math.Copysign(0, -1), 0, 3, "-0"},
		{"en", math.Inf(-1), 0, 3, "-∞"},
		{"en", math.NaN(), 0, 3, "NaN"},
		{"fr", 1234.5, 0, 3, "1 234,5"},
	} {
		if got := FFIFormatFloat(fr, c.locale, c.f, c.minF, c.maxF, true); got != c.want {
			t.Errorf("Float.format(%v, %s, %d, %d) = %q, want %q", c.f, c.locale, c.minF, c.maxF, got, c.want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	fr := full()
	for _, c := range []struct {
		locale string
		f      float64
		maxF   int64
		want   string
	}{
		{"en", 0.256, 0, "26%"},
		{"fr", 0.256, 0, "26 %"}, // CLDR 48's fr percent pattern has U+00A0, its group U+202F
		{"de", 0.256, 0, "26 %"},
		{"en", 0.1234, 1, "12.3%"},
		{"en", -0.5, 0, "-50%"},
		{"tr", 0.5, 0, "%50"},
		{"ar-EG", 0.5, 0, "٥٠٪؜"},
		{"en", 12.345, 0, "1,234%"},
	} {
		if got := FFIFormatFloatPercent(fr, c.locale, c.f, c.maxF); got != c.want {
			t.Errorf("Float.format_percent(%v, %s) = %q, want %q", c.f, c.locale, got, c.want)
		}
	}
	for _, c := range []struct{ locale, d, want string }{
		{"en", "12.5", "12.5%"},
		{"en", "100", "100%"},
		{"en", "50", "50%"},
		{"en", "-0.01", "-0.01%"},
		{"de", "1234.56", "1.234,56 %"},
	} {
		if got := FFIFormatDecimalPercent(fr, c.locale, c.d, 0, 100); got != c.want {
			t.Errorf("Decimal.format_percent(%s, %s) = %q, want %q", c.d, c.locale, got, c.want)
		}
	}
}

func TestFormatDecimal(t *testing.T) {
	fr := full()
	for _, c := range []struct{ locale, d, want string }{
		{"de", "1234.50", "1.234,50"},
		{"en", "1234567.89", "1,234,567.89"},
		{"en", "0.000", "0.000"},
		{"en", "-1234", "-1,234"},
		{"hi", "1234567.5", "12,34,567.5"},
		{"en", "123456789012345678901234567890.25", "123,456,789,012,345,678,901,234,567,890.25"},
	} {
		if got := FFIFormatDecimal(fr, c.locale, c.d, 0, 100, true); got != c.want {
			t.Errorf("Decimal.format(%s, %s) = %q, want %q", c.d, c.locale, got, c.want)
		}
	}
}

func TestFormatTrapsOnBadOptions(t *testing.T) {
	for name, f := range map[string]func(){
		"min_digits 0":                 func() { FFIFormatInt(full(), "en", rt.SmallInt(1), true, 0) },
		"min > max":                    func() { FFIFormatFloat(full(), "en", 1, 3, 2, true) },
		"negative max":                 func() { FFIFormatFloatPercent(full(), "en", 1, -1) },
		"over the bound":               func() { FFIFormatFloat(full(), "en", 1, 0, 101, true) },
		"decimal min > max":            func() { FFIFormatDecimal(full(), "en", "1.5", 2, 1, true) },
		"decimal percent negative min": func() { FFIFormatDecimalPercent(full(), "en", "1", -1, 0) },
	} {
		func() {
			defer func() {
				if _, ok := recover().(*rt.Error); !ok {
					t.Errorf("%s: no trap", name)
				}
			}()
			f()
		}()
	}
}

func TestUnlistedLocalesFallBack(t *testing.T) {
	fr := only("de")
	for _, c := range []struct{ locale, want string }{
		{"de", "1.234.567"},
		{"de-CH", "1.234.567"}, // de-CH is not carried: its parent de is
		{"fr", "1,234,567"},    // nothing on fr's chain is carried, and no source: root
	} {
		if got := FFIFormatInt(fr, c.locale, rt.SmallInt(1234567), true, 1); got != c.want {
			t.Errorf("listing only de, Int.format(1234567, %s) = %q, want %q", c.locale, got, c.want)
		}
	}
	if got := FFIJoinList(fr, "pl", []string{"a", "b"}, int64(rt.TagListAnd), int64(rt.TagListWide)); got != "a, b" {
		t.Errorf("listing only de, a Polish list is %q, want root's", got)
	}
}

// A locale the program does not carry formats as its source locale: with
// en (the source) and de listed, fr writes dates, money and lists as
// English does, not as root's bare patterns; de-CH, whose parent de is
// carried, stays German; and with de the source, fr writes as German.
func TestALocaleTheProgramDoesNotCarryFormatsAsTheSource(t *testing.T) {
	date := []int64{2026, 10, 20, 0, 0, 0, 0, 0, hasDate}
	for _, c := range []struct {
		source, locale    string
		date, money, list string
	}{
		{"en", "fr", "October 20, 2026", "€12.50", "a, b, and c"},
		{"en", "de-CH", "20. Oktober 2026", "12,50 €", "a, b und c"},
		// en-GB is not carried, and its parent en is: en's, as before.
		{"en", "en-GB", "October 20, 2026", "€12.50", "a, b, and c"},
		{"de", "fr", "20. Oktober 2026", "12,50 €", "a, b und c"},
		// No source record: root, as a program that names none.
		{"", "fr", "2026 M10 20", "€ 12.50", "a, b, c"},
	} {
		d := cldrfull.ForProgram([]string{"en", "de"}, c.source)
		fr := rt.NewFrame(cldrdata.WithData(context.Background(), d))
		if got := FormatStyle(d, c.locale, date, "long/-"); got != c.date {
			t.Errorf("source %q: a long date in %s is %q, want %q", c.source, c.locale, got, c.date)
		}
		if got := Money(d, c.locale, "12.5", "EUR", 2, true, displaySymbol, false); got != c.money {
			t.Errorf("source %q: 12.5 EUR in %s is %q, want %q", c.source, c.locale, got, c.money)
		}
		if got := FFIJoinList(fr, c.locale, []string{"a", "b", "c"}, int64(rt.TagListAnd), int64(rt.TagListWide)); got != c.list {
			t.Errorf("source %q: a list in %s is %q, want %q", c.source, c.locale, got, c.list)
		}
	}
}

// A browser build's locale whose file is not loaded yet formats as the
// source locale, and as itself once the file is decoded over the data.
func TestALocaleWhoseFileIsNotLoadedFormatsAsTheSource(t *testing.T) {
	image, err := cldrdata.Decode(cldrfull.Select(cldrdata.Selection{
		Locales: []string{"en", "pl"}, Embedded: []string{"en"}, Parts: cldrdata.AllParts, Source: "en"}))
	if err != nil {
		t.Fatal(err)
	}
	list := func(d *cldrdata.Data) string {
		fr := rt.NewFrame(cldrdata.WithData(context.Background(), d))
		return FFIJoinList(fr, "pl", []string{"a", "b", "c"}, int64(rt.TagListAnd), int64(rt.TagListWide))
	}
	if got := list(image); got != "a, b, and c" {
		t.Errorf("before pl's file is loaded, a Polish list is %q, want English's", got)
	}
	loaded, err := image.With(cldrfull.FileBundle([]string{"pl"}, cldrdata.AllParts, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := list(loaded); got != "a, b i c" {
		t.Errorf("once pl's file is loaded, a Polish list is %q, want Polish's", got)
	}
	if got := list(image); got != "a, b, and c" {
		t.Errorf("loading changed the data it was decoded over: %q", got)
	}
}

func TestJoinList(t *testing.T) {
	fr := full()
	and, or, unit := int64(rt.TagListAnd), int64(rt.TagListOr), int64(rt.TagListUnit)
	wide, short, narrow := int64(rt.TagListWide), int64(rt.TagListShort), int64(rt.TagListNarrow)
	for _, c := range []struct {
		locale       string
		items        []string
		style, width int64
		want         string
	}{
		{"en", nil, and, wide, ""},
		{"en", []string{"a"}, and, wide, "a"},
		{"en", []string{"a", "b"}, and, wide, "a and b"},
		{"en", []string{"a", "b", "c"}, and, wide, "a, b, and c"},
		{"en", []string{"a", "b", "c", "d"}, and, wide, "a, b, c, and d"},
		{"en", []string{"a", "b", "c"}, or, wide, "a, b, or c"},
		{"en", []string{"a", "b", "c"}, and, short, "a, b, & c"},
		{"en", []string{"3 quarters", "1 dime", "2 pennies"}, unit, wide, "3 quarters, 1 dime, 2 pennies"},
		{"en", []string{"5 ft", "3 in"}, unit, narrow, "5 ft 3 in"},
		{"en-GB", []string{"a", "b", "c"}, and, wide, "a, b and c"},
		{"pl", []string{"a", "b", "c"}, and, wide, "a, b i c"},
		{"pl", []string{"3 ćwierćdolarówki", "1 dziesięciocentówka", "2 centy"}, unit, wide,
			"3 ćwierćdolarówki, 1 dziesięciocentówka i 2 centy"},
		{"pl", []string{"a", "b"}, or, wide, "a lub b"},
		{"ja", []string{"a", "b", "c"}, and, wide, "a、b、c"},
		{"ja", []string{"a", "b", "c"}, or, wide, "a、b、またはc"},
		{"de", []string{"a", "b", "c"}, and, wide, "a, b und c"},
	} {
		if got := FFIJoinList(fr, c.locale, c.items, c.style, c.width); got != c.want {
			t.Errorf("join_list(%q, %s) = %q, want %q", c.items, c.locale, got, c.want)
		}
	}
}
