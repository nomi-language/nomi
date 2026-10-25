package stdformat

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdplural"
	"github.com/nomi-language/nomi/rt"
)

// RELATIVE TIME AND DURATIONS. `TimeUnit.format_relative` writes "in 3
// days" or "yesterday" from CLDR's relative-time forms, and
// `Duration.format` writes "1 hour, 30 minutes" from its duration unit
// patterns joined by the unit list pattern. Both pick a form by the plural
// category the number takes in the locale and write the number as
// `Int.format` does.

// relativeUnits are std/calendar's TimeUnit variants in order, as CLDR's
// dateFields name them.
var relativeUnits = []string{"second", "minute", "hour", "day", "week", "month", "quarter", "year"}

// FFIFormatRelative is `calendar.relative_format_raw`: n units from now in
// locale, unit by its position in TimeUnit; with numeric false, the
// locale's word for it when it has one (`yesterday`, `tomorrow`).
func FFIFormatRelative(fr *rt.Frame, locale string, n, unit int64, numeric bool) string {
	return Relative(stdlocale.Data(fr), locale, n, unit, numeric)
}

// Relative writes n units from now in locale.
func Relative(d *cldrdata.Data, locale string, n, unit int64, numeric bool) string {
	if unit < 0 || int(unit) >= len(relativeUnits) {
		unit = 3
	}
	name := relativeUnits[unit]
	chain := stdlocale.Fallbacks(d, locale)
	get := func(key string) string {
		v, _, _ := d.Resolve(chain, key)
		return v
	}
	if !numeric && n >= -2 && n <= 2 {
		if words := strings.Split(get(cldrdata.RecordKey("relative", name)), "\t"); len(words) == 5 && words[n+2] != "" {
			return words[n+2]
		}
	}
	kind := "relative-future"
	if n < 0 {
		kind = "relative-past"
	}
	abs := absInt(n)
	count := strconv.FormatUint(abs, 10)
	category := stdplural.Rules(d, locale, false).CategoryOf(count)
	pattern := pluralForm(get(cldrdata.RecordKey(kind, name)), category)
	if pattern == "" {
		pattern = "{0}"
	}
	nums := numbersFor(d, locale)
	return strings.Replace(pattern, "{0}", nums.render(false, count, "", nums.decimal, true), 1)
}

// durationUnits are the units Duration.format writes, largest first, and
// each one's length in nanoseconds.
var durationUnits = []struct {
	name  string
	nanos int64
}{
	{"hour", 3_600_000_000_000}, {"minute", 60_000_000_000}, {"second", 1_000_000_000},
	{"millisecond", 1_000_000}, {"microsecond", 1_000}, {"nanosecond", 1},
}

// unitWidths are CLDR's unit widths by std's DurationWidth position
// (1-based).
var unitWidths = map[int64]string{int64(rt.TagDurationLong): "long", int64(rt.TagDurationShort): "short", int64(rt.TagDurationNarrow): "narrow"}

// FFIFormatDuration is `duration.duration_format_raw`: nanos as a reader of
// locale reads a length of time, at width (DurationWidth's tag).
func FFIFormatDuration(fr *rt.Frame, locale string, nanos, width int64) string {
	return Duration(stdlocale.Data(fr), locale, nanos, width)
}

// Duration writes a length of time in nanoseconds in locale: each unit it
// has from hours down, with the unit's pattern for the number's plural
// form, joined as the locale joins the parts of one measure. Zero is zero
// seconds; a negative length has the locale's minus sign before its first
// number.
func Duration(d *cldrdata.Data, locale string, nanos, width int64) string {
	w, ok := unitWidths[width]
	if !ok {
		w = "long"
	}
	chain := stdlocale.Fallbacks(d, locale)
	nums := numbersFor(d, locale)
	rules := stdplural.Rules(d, locale, false)
	neg := nanos < 0
	rest := absInt(nanos)
	var parts []string
	part := func(unit string, n uint64) {
		count := strconv.FormatUint(n, 10)
		v, _, _ := d.Resolve(chain, cldrdata.RecordKey("duration-unit", unit+"-"+w))
		pattern := pluralForm(v, rules.CategoryOf(count))
		if pattern == "" {
			pattern = "{0} " + unit
		}
		number := nums.render(neg && len(parts) == 0, count, "", nums.decimal, true)
		parts = append(parts, strings.Replace(pattern, "{0}", number, 1))
	}
	if rest == 0 {
		part("second", 0)
	}
	for _, u := range durationUnits {
		if n := rest / uint64(u.nanos); n > 0 {
			rest -= n * uint64(u.nanos)
			part(u.name, n)
		}
	}
	typ := "unit"
	if w != "long" {
		typ = "unit-" + w
	}
	return JoinList(d, locale, parts, typ)
}

// JoinList joins items by locale's list pattern of type (standard,
// unit-short, ...).
func JoinList(d *cldrdata.Data, locale string, items []string, typ string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	start, middle, end, two := listPatterns(d, locale, typ)
	if len(items) == 2 {
		return fill(two, items[0], items[1])
	}
	out := fill(end, items[len(items)-2], items[len(items)-1])
	for i := len(items) - 3; i >= 1; i-- {
		out = fill(middle, items[i], out)
	}
	return fill(start, items[0], out)
}
