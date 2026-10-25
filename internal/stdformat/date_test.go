package stdformat

import (
	"testing"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
)

// A skeleton resolves to each locale's own order, punctuation and words,
// the nearest available format widened to the widths asked for, and a
// date and time no one format covers to the two glued.
func TestSkeletonsResolvePerLocale(t *testing.T) {
	d := cldrfull.Full()
	date := []int64{2026, 10, 20, 21, 5, 0, 0, 0, hasDate | hasTime}
	for _, c := range []struct{ locale, skeleton, pattern, text string }{
		{"en", "yMMMd", "MMM d, y", "Oct 20, 2026"},
		{"en", "MMMMEEEEd", "EEEE, MMMM d", "Tuesday, October 20"},
		{"en", "jm", "h:mm a", "9:05 PM"},
		{"en", "yMMMdjm", "MMM d, y, h:mm a", "Oct 20, 2026, 9:05 PM"},
		{"en-GB", "MMMMEEEEd", "EEEE d MMMM", "Tuesday 20 October"},
		{"pl", "MMMMd", "d MMMM", "20 października"},
		{"pl", "LLLL", "LLLL", "październik"},
		{"pl", "MMMMEEEEdjm", "EEEE, d MMMM HH:mm", "wtorek, 20 października 21:05"},
		{"de", "yMMMd", "d. MMM y", "20. Okt. 2026"},
		{"de", "hm", "h:mm a", "9:05 PM"},
		{"fr", "MMMMEEEEd", "EEEE d MMMM", "mardi 20 octobre"},
		// Japanese names a month by its number where a skeleton asks for
		// its name, and the pattern keeps it so.
		{"ja", "MMMMEEEEd", "M月d日EEEE", "10月20日火曜日"},
		{"ja", "LLLL", "M月", "10月"},
		{"ar-EG", "yMMMd", "d MMM y", "٢٠ أكتوبر ٢٠٢٦"},
	} {
		if got := ResolveSkeleton(d, c.locale, c.skeleton); got != c.pattern {
			t.Errorf("%s %s: resolves to %q, want %q", c.locale, c.skeleton, got, c.pattern)
		}
		if got := FormatSkeleton(d, c.locale, date, c.skeleton); got != c.text {
			t.Errorf("%s %s: writes %q, want %q", c.locale, c.skeleton, got, c.text)
		}
	}
}

// A bundle built with the program's skeletons carries each one's resolved
// pattern and no available formats, and writes every date as the full data
// does, for each listed locale and for one that falls back to it.
func TestAResolvedBundleWritesAsTheFullData(t *testing.T) {
	cldrfull.Resolver = ResolveSkeleton
	full := cldrfull.Full()
	skeletons := []string{"yMMMd", "MMMMEEEEd", "jm", "yMMMdjm"}
	listed := []string{"en", "pl", "de-CH", "ja"}
	parts := cldrdata.PartListedParents | cldrdata.PartNumbers | cldrdata.PartDates
	resolved, err := cldrdata.Decode(cldrfull.Bundle(listed, parts, skeletons))
	if err != nil {
		t.Fatal(err)
	}
	withFormats, err := cldrdata.Decode(cldrfull.Bundle(listed, parts|cldrdata.PartSkeletons, nil))
	if err != nil {
		t.Fatal(err)
	}
	if n, m := len(resolved.KeysOn([]string{"en"}, "skeleton\t")), len(withFormats.KeysOn([]string{"en"}, "skeleton\t")); n != len(skeletons) || m < 40 {
		t.Fatalf("en carries %d skeletons resolved and %d available formats; want %d and many", n, m, len(skeletons))
	}
	date := []int64{2026, 10, 20, 9, 30, 0, 0, 0, hasDate | hasTime}
	for _, tag := range append(listed, "de-AT", "en-AU") {
		for _, s := range skeletons {
			want := FormatSkeleton(withFormats, tag, date, s)
			if got := FormatSkeleton(resolved, tag, date, s); got != want {
				t.Errorf("%s %s: the resolved bundle writes %q, the available formats %q", tag, s, got, want)
			}
		}
	}
	// The listed locales write as the full data writes them.
	for _, tag := range listed {
		for _, s := range skeletons {
			if got, want := FormatSkeleton(resolved, tag, date, s), FormatSkeleton(full, tag, date, s); got != want {
				t.Errorf("%s %s: the bundle writes %q, the full data %q", tag, s, got, want)
			}
		}
	}
}

// Each style writes the locale's pattern for it, the time without a zone
// when the value has none, and the zone's offset in the locale's GMT
// format when it has one.
func TestStylesWriteTheLocalesPatterns(t *testing.T) {
	d := cldrfull.Full()
	date := []int64{2026, 10, 20, 0, 0, 0, 0, 0, hasDate}
	clock := []int64{1970, 1, 1, 21, 5, 30, 0, 0, hasTime}
	zoned := []int64{2026, 10, 20, 9, 30, 0, 0, -5 * 3600, hasDate | hasTime | hasZone}
	for _, c := range []struct {
		locale string
		fields []int64
		spec   string
		want   string
	}{
		{"en", date, "full/-", "Tuesday, October 20, 2026"},
		{"en", date, "short/-", "10/20/26"},
		{"pl", date, "long/-", "20 października 2026"},
		{"de", date, "medium/-", "20.10.2026"},
		{"ja", date, "full/-", "2026年10月20日火曜日"},
		{"en", clock, "-/full", "9:05:30 PM"},
		{"de", clock, "-/short", "21:05"},
		{"en", zoned, "medium/long", "Oct 20, 2026, 9:30:00 AM GMT-5"},
		{"en", zoned, "-/full", "9:30:00 AM GMT-05:00"},
		{"fr", zoned, "short/full", "20/10/2026 09:30:00 UTC−05:00"},
	} {
		if got := FormatStyle(d, c.locale, c.fields, c.spec); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.locale, c.spec, got, c.want)
		}
	}
}

// A two-digit year is 2000 plus its digits whether the locale's pattern
// writes the year as `yy` (German short, `dd.MM.yy`) or as `y` (German
// medium, `dd.MM.y`), strict or lenient. Three or four digits are the year
// itself, and so is a fixed pattern's `y`.
func TestATwoDigitYearIsInThisCentury(t *testing.T) {
	d := cldrfull.Full()
	for _, c := range []struct {
		text, locale, spec string
		lenient            bool
		want               string
	}{
		{"20.10.26", "de", "short/-", false, "=2026-10-20"},
		{"20.10.26", "de", "short/-", true, "=2026-10-20"},
		{"20.10.26", "de", "medium/-", false, "=2026-10-20"},
		{"20.10.26", "de", "medium/-", true, "=2026-10-20"},
		{"20.10.2026", "de", "medium/-", false, "=2026-10-20"},
		{"20.10.2026", "de", "short/-", true, "=2026-10-20"},
		{"20.10.926", "de", "medium/-", false, "=0926-10-20"},
		{"Oct 20, 26", "en", "medium/-", false, "=2026-10-20"},
		{"10/20/99", "en", "short/-", false, "=2099-10-20"},
	} {
		if got := ParseStyle(d, c.text, c.locale, c.spec, c.lenient); got != c.want {
			t.Errorf("%s %s %q lenient=%v: %q, want %q", c.locale, c.spec, c.text, c.lenient, got, c.want)
		}
	}
	if got := ParsePattern("26-10-20", "y-MM-dd", true, false); got != "=0026-10-20" {
		t.Errorf("fixed y-MM-dd: %q, want =0026-10-20", got)
	}
	if got := ParsePattern("26-10-20", "yy-MM-dd", true, false); got != "=2026-10-20" {
		t.Errorf("fixed yy-MM-dd: %q, want =2026-10-20", got)
	}
}

// Money writes the locale's currency pattern with the currency's symbol,
// the alphabetic-symbol form where letters meet digits, the currency's
// minor digits at least, and its name in the amount's plural form.
func TestMoneyIsWrittenAsTheLocaleWritesIt(t *testing.T) {
	d := cldrfull.Full()
	for _, c := range []struct {
		locale, amount, code string
		digits               int
		iso                  bool
		display              int
		want                 string
	}{
		{"en", "12.5", "EUR", 2, true, displaySymbol, "€12.50"},
		{"pl", "12.5", "EUR", 2, true, displaySymbol, "12,50 €"},
		{"en", "12.5", "EUR", 2, true, displayCode, "EUR 12.50"},
		{"en", "2", "EUR", 2, true, displayName, "2.00 euros"},
		{"pl", "5", "PLN", 0, true, displayName, "5 złotych polskich"},
		{"en", "0.5", "BTC", 8, false, displaySymbol, "BTC 0.50000000"},
		{"pl", "0.5", "BTC", 8, false, displayName, "0,50000000 BTC"},
		{"en", "12.505", "EUR", 2, true, displaySymbol, "€12.505"},
		{"en", "-0", "USD", 2, true, displaySymbol, "$0.00"},
	} {
		if got := Money(d, c.locale, c.amount, c.code, c.digits, c.iso, c.display); got != c.want {
			t.Errorf("%s %s %s: %q, want %q", c.locale, c.amount, c.code, got, c.want)
		}
	}
}

// What Money writes, ParseMoney reads back, strictly, in each display a
// strict reader takes.
func TestMoneyReadsBackWhatItWrites(t *testing.T) {
	d := cldrfull.Full()
	for _, locale := range []string{"en", "pl", "de", "fr", "ja", "ar-EG", "de-CH", "hi"} {
		for _, code := range []string{"EUR", "USD", "JPY", "PLN"} {
			digits, _ := d.CurrencyDigits(code)
			for _, display := range []int{displaySymbol, displayNarrow, displayCode} {
				amount, want := "-1234567.5", "-1234567.50"
				if digits == 0 {
					amount, want = "-1234567", "-1234567"
				}
				text := Money(d, locale, amount, code, digits, true, display)
				got, err := ParseMoney(d, text, locale, code, digits, true, false)
				if err != nil || got != want {
					t.Errorf("%s %s display %d: %q reads as %q, %v", locale, code, display, text, got, err)
				}
			}
		}
	}
}
