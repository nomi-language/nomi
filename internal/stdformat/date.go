package stdformat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/rt"
)

// DATES AND TIMES. std/calendar's `format`, `format_fields`, `parse_in`,
// `to_string_with` and `parse_with`, Gregorian only. A value crosses as its
// fields (civilFields); a style or a skeleton (`DateFields`) is resolved to
// a CLDR date pattern for the locale, and the pattern is written field by
// field (UTS #35 Part 4, "Date Format Patterns") in the locale's names and
// digits. A fixed pattern (`DatePattern`) is written the same way with no
// locale: it holds only numeric fields, in ASCII digits.

// civil is a calendar value's fields, as a pattern writes them.
type civil struct {
	year, month, day           int64
	hour, minute, second, nano int64
	// offset is the UTC offset in seconds east, when zone is set.
	offset           int64
	date, time, zone bool
}

// The flags of civilFields' last element.
const (
	hasDate = 1 << iota
	hasTime
	hasZone
)

// civilOf reads the fields std/calendar passes: year, month, day, hour,
// minute, second, nanosecond, offset seconds, and the flags saying which
// of the date, the time and the offset the value has.
func civilOf(fields []int64) civil {
	f := make([]int64, 9)
	copy(f, fields)
	return civil{year: f[0], month: f[1], day: f[2], hour: f[3], minute: f[4], second: f[5], nano: f[6], offset: f[7],
		date: f[8]&hasDate != 0, time: f[8]&hasTime != 0, zone: f[8]&hasZone != 0}
}

// daysFromCivil is the days from 1970-01-01 to the proleptic Gregorian
// date (Howard Hinnant's days_from_civil).
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// weekday is the day of the week, 0 Sunday to 6 Saturday.
func weekday(y, m, d int64) int {
	return int(((daysFromCivil(y, m, d) % 7) + 7 + 4) % 7)
}

func leap(y int64) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

func monthDays(y, m int64) int64 {
	switch m {
	case 2:
		if leap(y) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// calendarIn is what writing a date reads of one locale: its records, along
// its chain, and its digits. A fixed pattern writes with none (locale
// "").
type calendarIn struct {
	d      *cldrdata.Data
	chain  []string
	num    numbers
	locale string
}

func calendarFor(d *cldrdata.Data, locale string) calendarIn {
	return calendarIn{d: d, chain: stdlocale.Fallbacks(d, locale), num: numbersFor(d, locale), locale: locale}
}

// fixedCalendar writes a fixed pattern: ASCII digits and no names.
func fixedCalendar() calendarIn {
	var n numbers
	n.ascii = true
	for i := range n.digits {
		n.digits[i] = string(rune('0' + i))
	}
	return calendarIn{num: n}
}

func (c calendarIn) rec(key string) string {
	if c.d == nil {
		return ""
	}
	v, _, _ := c.d.Resolve(c.chain, key)
	return v
}

// names are a locale's names of kind (months, days, day-periods, eras) in a
// context and width, falling back to the other context and to wider
// widths.
func (c calendarIn) names(kind, ctx, width string) []string {
	try := func(ctx, width string) []string {
		key := cldrdata.RecordKey(kind, ctx+"-"+width)
		if kind == "eras" {
			key = cldrdata.RecordKey(kind, width)
		}
		if v := c.rec(key); v != "" {
			return strings.Split(v, "\t")
		}
		return nil
	}
	other := "stand-alone"
	if ctx == "stand-alone" {
		other = "format"
	}
	for _, w := range []string{width, "abbreviated", "wide"} {
		if ns := try(ctx, w); ns != nil {
			return ns
		}
		if ns := try(other, w); ns != nil {
			return ns
		}
	}
	return nil
}

// widthOf is the name width a text field of count letters writes.
func widthOf(count int, short bool) string {
	switch {
	case count == 4:
		return "wide"
	case count == 5:
		return "narrow"
	case count == 6 && short:
		return "short"
	}
	return "abbreviated"
}

// number writes n in the calendar's digits, padded with zeros to width.
func (c calendarIn) number(n int64, width int) string {
	neg := n < 0
	s := strconv.FormatInt(absInt64(n), 10)
	if len(s) < width {
		s = strings.Repeat("0", width-len(s)) + s
	}
	s = c.num.localDigits(s)
	if neg {
		minus := c.num.sym.minus
		if minus == "" {
			minus = "-"
		}
		s = minus + s
	}
	return s
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// write writes v by the pattern's fields.
func (c calendarIn) write(fields []cldrdata.DateField, v civil) string {
	var b strings.Builder
	for _, f := range fields {
		if f.Letter == 0 {
			b.WriteString(f.Text)
			continue
		}
		b.WriteString(c.field(f, v))
	}
	return b.String()
}

// field writes one pattern field of v.
func (c calendarIn) field(f cldrdata.DateField, v civil) string {
	n := f.Count
	eraYear := v.year
	if eraYear <= 0 {
		eraYear = 1 - eraYear
	}
	switch f.Letter {
	case 'G':
		ns := c.names("eras", "format", map[int]string{4: "wide", 5: "narrow"}[n])
		if ns == nil {
			ns = c.names("eras", "format", "abbreviated")
		}
		if len(ns) == 2 {
			if v.year <= 0 {
				return ns[0]
			}
			return ns[1]
		}
		return ""
	case 'y', 'Y':
		if n == 2 {
			return c.number(eraYear%100, 2)
		}
		return c.number(eraYear, n)
	case 'u', 'r':
		return c.number(v.year, n)
	case 'Q', 'q':
		return c.number((v.month-1)/3+1, min(n, 2))
	case 'M', 'L':
		if n <= 2 {
			return c.number(v.month, n)
		}
		ctx := "format"
		if f.Letter == 'L' {
			ctx = "stand-alone"
		}
		if ns := c.names("months", ctx, widthOf(n, false)); len(ns) == 12 {
			return ns[v.month-1]
		}
		return c.number(v.month, 1)
	case 'd':
		return c.number(v.day, n)
	case 'D':
		doy := daysFromCivil(v.year, v.month, v.day) - daysFromCivil(v.year, 1, 1) + 1
		return c.number(doy, n)
	case 'E', 'c', 'e':
		wd := weekday(v.year, v.month, v.day)
		if n <= 2 && f.Letter != 'E' {
			return c.number(int64((wd+6)%7+1), n)
		}
		ctx := "format"
		if f.Letter == 'c' {
			ctx = "stand-alone"
		}
		if ns := c.names("days", ctx, widthOf(n, true)); len(ns) == 7 {
			return ns[wd]
		}
		return c.number(int64(wd), 1)
	case 'a', 'b', 'B':
		ns := c.names("day-periods", "format", widthOf(n, false))
		if len(ns) != 2 {
			ns = []string{"AM", "PM"}
		}
		if v.hour >= 12 {
			return ns[1]
		}
		return ns[0]
	case 'h':
		h := v.hour % 12
		if h == 0 {
			h = 12
		}
		return c.number(h, n)
	case 'H':
		return c.number(v.hour, n)
	case 'K':
		return c.number(v.hour%12, n)
	case 'k':
		h := v.hour
		if h == 0 {
			h = 24
		}
		return c.number(h, n)
	case 'm':
		return c.number(v.minute, n)
	case 's':
		return c.number(v.second, n)
	case 'S':
		frac := fmt.Sprintf("%09d", v.nano)
		if n <= 9 {
			frac = frac[:n]
		} else {
			frac += strings.Repeat("0", n-9)
		}
		return c.num.localDigits(frac)
	case 'z', 'O', 'v', 'V':
		if !v.zone {
			return ""
		}
		return c.gmt(v.offset, n >= 4)
	case 'Z':
		if !v.zone {
			return ""
		}
		switch {
		case n == 4:
			return c.gmt(v.offset, true)
		case n == 5:
			return isoOffset(v.offset, true, true)
		}
		return isoOffset(v.offset, false, false)
	case 'X', 'x':
		if !v.zone {
			return ""
		}
		if f.Letter == 'X' && v.offset == 0 {
			return "Z"
		}
		switch n {
		case 1:
			s := isoOffset(v.offset, false, false)
			if strings.HasSuffix(s, "00") {
				return s[:3]
			}
			return s
		case 2, 4:
			return isoOffset(v.offset, false, false)
		}
		return isoOffset(v.offset, true, false)
	}
	return ""
}

// isoOffset writes an offset as `+0200`, or `+02:00` with colon.
func isoOffset(offset int64, colon, _ bool) string {
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	h, m := offset/3600, offset%3600/60
	if colon {
		return fmt.Sprintf("%s%02d:%02d", sign, h, m)
	}
	return fmt.Sprintf("%s%02d%02d", sign, h, m)
}

// gmt writes an offset in the locale's localized GMT format (`GMT+2`,
// long `GMT+02:00`), `GMT` itself for zero.
func (c calendarIn) gmt(offset int64, long bool) string {
	format, zero, hour := "GMT{0}", "GMT", "+HH:mm;-HH:mm"
	if r := c.rec(cldrdata.KeyGMT); r != "" {
		if parts := strings.Split(r, "\t"); len(parts) == 3 {
			format, zero, hour = parts[0], parts[1], parts[2]
		}
	}
	if offset == 0 {
		return zero
	}
	pos, neg, _ := strings.Cut(hour, ";")
	p := pos
	if offset < 0 {
		p, offset = neg, -offset
	}
	h, m := offset/3600, offset%3600/60
	fields, err := cldrdata.ParseDatePattern(p)
	if err != nil {
		return format
	}
	var b strings.Builder
	for i, f := range fields {
		switch {
		case f.Letter == 'H':
			w := f.Count
			if !long {
				w = 1
			}
			b.WriteString(c.number(h, w))
		case f.Letter == 'm':
			if !long && m == 0 {
				continue
			}
			b.WriteString(c.number(m, f.Count))
		case f.Letter == 0:
			// The separator before the minutes goes with them in the short
			// form.
			if !long && m == 0 && i+1 < len(fields) && fields[i+1].Letter == 'm' {
				continue
			}
			b.WriteString(f.Text)
		}
	}
	return strings.Replace(format, "{0}", b.String(), 1)
}

// withoutZone is the pattern less its zone fields and the space before
// each: what a value with no offset writes a style's pattern as.
func withoutZone(fields []cldrdata.DateField) []cldrdata.DateField {
	var out []cldrdata.DateField
	for _, f := range fields {
		if strings.ContainsRune("zZOvVXx", f.Letter) && f.Letter != 0 {
			if n := len(out); n > 0 && out[n-1].Letter == 0 {
				out[n-1].Text = strings.TrimRight(out[n-1].Text, "   ")
				if out[n-1].Text == "" {
					out = out[:n-1]
				}
			}
			continue
		}
		out = append(out, f)
	}
	return out
}

func parseDate(p string) []cldrdata.DateField {
	fields, err := cldrdata.ParseDatePattern(p)
	if err != nil {
		return []cldrdata.DateField{{Text: p}}
	}
	return fields
}

// The style names std's DateStyle and TimeStyle declare, by position.
var styleNames = []string{"full", "long", "medium", "short"}

// stylePattern is the pattern a value of v's parts takes in the locale for
// a date style and a time style ("" for a part the value lacks): the date
// format, the time format, or both glued by the date-time format of the
// date's style.
func (c calendarIn) stylePattern(dateStyle, timeStyle string, v civil) []cldrdata.DateField {
	def := map[string]string{
		"date-format\tfull": "y MMMM d, EEEE", "date-format\tlong": "y MMMM d", "date-format\tmedium": "y MMM d",
		"date-format\tshort": "y-MM-dd", "time-format\tfull": "HH:mm:ss zzzz", "time-format\tlong": "HH:mm:ss z",
		"time-format\tmedium": "HH:mm:ss", "time-format\tshort": "HH:mm",
	}
	get := func(kind, style string) string {
		key := cldrdata.RecordKey(kind, style)
		if p := c.rec(key); p != "" {
			return p
		}
		if p, ok := def[key]; ok {
			return p
		}
		return "{1} {0}"
	}
	var date, tm []cldrdata.DateField
	if dateStyle != "" && v.date {
		date = parseDate(get("date-format", dateStyle))
	}
	if timeStyle != "" && v.time {
		tm = parseDate(get("time-format", timeStyle))
		if !v.zone {
			tm = withoutZone(tm)
		}
	}
	switch {
	case date == nil:
		return tm
	case tm == nil:
		return date
	}
	return glue(get("datetime-format", dateStyle), date, tm)
}

// glue puts a date pattern and a time pattern into a date-time format's
// `{1}` and `{0}`.
func glue(format string, date, tm []cldrdata.DateField) []cldrdata.DateField {
	var out []cldrdata.DateField
	for format != "" {
		i := strings.IndexByte(format, '{')
		if i < 0 || i+2 >= len(format) || format[i+2] != '}' {
			out = append(out, cldrdata.DateField{Text: unquote(format)})
			break
		}
		if i > 0 {
			out = append(out, cldrdata.DateField{Text: unquote(format[:i])})
		}
		switch format[i+1] {
		case '1':
			out = append(out, date...)
		case '0':
			out = append(out, tm...)
		}
		format = format[i+3:]
	}
	return out
}

// unquote is a date-time format's literal text with its quotes read
// (`'at'` is at).
func unquote(s string) string {
	if !strings.Contains(s, "'") {
		return s
	}
	fields, err := cldrdata.ParseDatePattern(s)
	if err != nil {
		return s
	}
	var b strings.Builder
	for _, f := range fields {
		if f.Letter == 0 {
			b.WriteString(f.Text)
		} else {
			b.WriteString(strings.Repeat(string(f.Letter), f.Count))
		}
	}
	return b.String()
}

// FFIFormatCivilStyle is `calendar.civil_format_style`: a value's fields in
// locale by a style spec, `<date style>/<time style>`, `-` for a part not
// written (`medium/short`).
func FFIFormatCivilStyle(fr *rt.Frame, locale string, fields []int64, spec string) string {
	return FormatStyle(stdlocale.Data(fr), locale, fields, spec)
}

// FormatStyle writes a value's fields in locale by a style spec.
func FormatStyle(d *cldrdata.Data, locale string, fields []int64, spec string) string {
	c := calendarFor(d, locale)
	ds, ts, _ := strings.Cut(spec, "/")
	if ds == "-" {
		ds = ""
	}
	if ts == "-" {
		ts = ""
	}
	v := civilOf(fields)
	return c.write(c.stylePattern(ds, ts, v), v)
}

// FFIFormatCivilFields is `calendar.civil_format_fields`: a value's fields
// in locale by a skeleton.
func FFIFormatCivilFields(fr *rt.Frame, locale string, fields []int64, skeleton string) string {
	return FormatSkeleton(stdlocale.Data(fr), locale, fields, skeleton)
}

// FormatSkeleton writes a value's fields in locale by a skeleton.
func FormatSkeleton(d *cldrdata.Data, locale string, fields []int64, skeleton string) string {
	c := calendarFor(d, locale)
	v := civilOf(fields)
	p := parseDate(ResolveSkeleton(d, locale, skeleton))
	if !v.zone {
		p = withoutZone(p)
	}
	return c.write(p, v)
}

// FFIFormatCivilPattern is `calendar.civil_format_pattern`: a value's
// fields by a fixed pattern, in ASCII digits.
func FFIFormatCivilPattern(fields []int64, pattern string) string {
	return fixedCalendar().write(parseDate(pattern), civilOf(fields))
}
