package stdformat

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/cldrdata"
)

// READING DATES. `Date.parse_in` and `Time.parse_in` read text by the
// pattern of a style in the locale, and `parse_with` by a fixed pattern.
// Strict reads what the formatter writes, with three allowances: a
// two-digit field (`dd`, `MM`, `HH`) may be written with one digit, any
// space stands for a space the pattern has, and ASCII digits stand for the
// locale's. Lenient also reads names in any width and either case, with or
// without their final period, any run of spaces and punctuation where the
// pattern has literal text, a four-digit year where it has two, and space
// around the text. A two-digit year is 2000 plus its digits, whether the
// locale's pattern writes the year as `yy` or as `y`. Neither guesses the
// locale.
//
// The answer crosses as text: `=` and the value's ISO 8601 form, or `!`
// and why the text does not read (calendar's Error.InvalidFormat), or `?`
// and why the value it reads is not one (Error.InvalidValue).

// ParseStyle reads text as the value a style spec writes in locale.
func ParseStyle(d *cldrdata.Data, text, locale, spec string, lenient bool) string {
	c := calendarFor(d, locale)
	ds, ts, _ := strings.Cut(spec, "/")
	v := civil{date: ds != "-", time: ts != "-"}
	if ds == "-" {
		ds = ""
	}
	if ts == "-" {
		ts = ""
	}
	return c.read(text, c.stylePattern(ds, ts, v), v.date, v.time, lenient)
}

// ParsePattern reads text by a fixed pattern; date and time say which
// parts the value has.
func ParsePattern(text, pattern string, date, time bool) string {
	return fixedCalendar().read(text, parseDate(pattern), date, time, false)
}

// reader reads text by a pattern's fields.
type reader struct {
	c       calendarIn
	items   []item
	text    string
	at      int
	lenient bool
}

func (r *reader) peek() string {
	if r.at < len(r.items) {
		return r.items[r.at].g
	}
	return ""
}

// where is what the reader found at its position, as an error says it:
// found `x`, or found the end of the text.
func (r *reader) where() string {
	if r.at >= len(r.items) {
		return "found the end of the text"
	}
	return fmt.Sprintf("found `%s`", r.items[r.at].g)
}

// read reads text as a value with the parts date and time say, by fields.
func (c calendarIn) read(text string, fields []cldrdata.DateField, date, time, lenient bool) string {
	r := &reader{c: c, items: graphemes(text), text: text, lenient: lenient}
	if lenient {
		for len(r.items) > 0 && space(r.items[0].g) {
			r.items = r.items[1:]
		}
		for len(r.items) > 0 && space(r.items[len(r.items)-1].g) {
			r.items = r.items[:len(r.items)-1]
		}
	}
	v := civil{year: 1970, month: 1, day: 1}
	pm, period, bc := false, false, false
	h12 := int64(-1)
	for i, f := range fields {
		if f.Letter == 0 {
			if err := r.literal(f.Text); err != "" {
				return "!" + err
			}
			continue
		}
		// A numeric field right before another reads exactly its width.
		exact := i+1 < len(fields) && fields[i+1].Letter != 0 && !textual(fields[i+1].Letter, fields[i+1].Count)
		switch f.Letter {
		case 'G':
			k, err := r.name("the era", [][]string{c.names("eras", "format", "abbreviated"), c.names("eras", "format", "wide"), c.names("eras", "format", "narrow")}, f.Count)
			if err != "" {
				return "!" + err
			}
			bc = k == 0
		case 'y', 'Y', 'u':
			n, digits, err := r.number("the year", 1, 9, f.Count, exact)
			if err != "" {
				return "!" + err
			}
			// A locale's `y` and `yy` read two digits as a two-digit year,
			// as ICU does: German's medium `dd.MM.y` reads `20.10.26` as
			// 2026. A fixed pattern's `y` reads the year itself, so
			// parse_with reads back what to_string_with writes for year 26.
			if digits == 2 && (f.Count == 2 || f.Count == 1 && c.d != nil) {
				n += 2000
			}
			v.year = n
		case 'M', 'L':
			if f.Count <= 2 {
				n, _, err := r.number("the month", 1, 2, f.Count, exact)
				if err != "" {
					return "!" + err
				}
				v.month = n
				continue
			}
			k, err := r.name("the month", c.nameSets("months", f.Letter == 'L', f.Count), f.Count)
			if err != "" {
				return "!" + err
			}
			v.month = int64(k + 1)
		case 'd':
			n, _, err := r.number("the day", 1, 2, f.Count, exact)
			if err != "" {
				return "!" + err
			}
			v.day = n
		case 'E', 'c', 'e':
			if _, err := r.name("the day of the week", c.nameSets("days", f.Letter == 'c', f.Count), f.Count); err != "" {
				return "!" + err
			}
		case 'a', 'b', 'B':
			k, err := r.name("AM or PM", c.nameSets("day-periods", false, f.Count), f.Count)
			if err != "" {
				return "!" + err
			}
			period, pm = true, k == 1
		case 'h', 'K':
			n, _, err := r.number("the hour", 1, 2, f.Count, exact)
			if err != "" {
				return "!" + err
			}
			if f.Letter == 'h' && n == 12 {
				n = 0
			}
			h12 = n
		case 'H', 'k':
			n, _, err := r.number("the hour", 1, 2, f.Count, exact)
			if err != "" {
				return "!" + err
			}
			if f.Letter == 'k' && n == 24 {
				n = 0
			}
			v.hour = n
		case 'm':
			n, _, err := r.number("the minute", 1, 2, f.Count, exact)
			if err != "" {
				return "!" + err
			}
			v.minute = n
		case 's':
			n, _, err := r.number("the second", 1, 2, f.Count, exact)
			if err != "" {
				return "!" + err
			}
			v.second = n
		case 'S':
			start := r.at
			n, digits, err := r.number("the fraction of a second", 1, 9, f.Count, exact)
			if err != "" {
				return "!" + err
			}
			_ = start
			for ; digits < 9; digits++ {
				n *= 10
			}
			v.nano = n
		default:
			return fmt.Sprintf("!the pattern's `%s` is not read", strings.Repeat(string(f.Letter), f.Count))
		}
	}
	if r.lenient {
		for r.at < len(r.items) && (space(r.peek()) || punctuation(r.peek())) {
			r.at++
		}
	}
	if r.at < len(r.items) {
		return fmt.Sprintf("!unexpected `%s` at %d after the %s", r.items[r.at].g, r.items[r.at].at, whatOf(date, time))
	}
	if bc {
		v.year = 1 - v.year
	}
	if h12 >= 0 {
		v.hour = h12
		if period && pm {
			v.hour += 12
		}
	}
	return c.answer(v, date, time)
}

func whatOf(date, time bool) string {
	switch {
	case date && time:
		return "date and time"
	case time:
		return "time"
	}
	return "date"
}

// answer is a value read off text, checked: `=` and its ISO 8601 form, or
// `?` and why it is not one.
func (c calendarIn) answer(v civil, date, time bool) string {
	var b strings.Builder
	if date {
		switch {
		case v.month < 1 || v.month > 12:
			return fmt.Sprintf("?there is no month %d", v.month)
		case v.day < 1 || v.day > monthDays(v.year, v.month):
			return fmt.Sprintf("?month %d of %d has no day %d", v.month, v.year, v.day)
		}
		y := strconv.FormatInt(absInt64(v.year), 10)
		if len(y) < 4 {
			y = strings.Repeat("0", 4-len(y)) + y
		}
		if v.year < 0 {
			y = "-" + y
		}
		fmt.Fprintf(&b, "%s-%02d-%02d", y, v.month, v.day)
	}
	if time {
		switch {
		case v.hour > 23:
			return fmt.Sprintf("?there is no hour %d", v.hour)
		case v.minute > 59:
			return fmt.Sprintf("?there is no minute %d", v.minute)
		case v.second > 59:
			return fmt.Sprintf("?there is no second %d", v.second)
		}
		if date {
			b.WriteByte('T')
		}
		fmt.Fprintf(&b, "%02d:%02d:%02d", v.hour, v.minute, v.second)
		if v.nano > 0 {
			fmt.Fprintf(&b, ".%09d", v.nano)
		}
	}
	return "=" + b.String()
}

func punctuation(g string) bool {
	r, _ := utf8.DecodeRuneInString(g)
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// literal reads a pattern's literal text.
func (r *reader) literal(text string) string {
	want := graphemes(text)
	if r.lenient {
		// Any run of spaces and punctuation stands for literal text; a word
		// in it is read without regard to case.
		start := r.at
		k := 0
		for k < len(want) && r.at < len(r.items) {
			w, got := want[k].g, r.items[r.at].g
			switch {
			case strings.EqualFold(w, got) || space(w) && space(got):
				k++
				r.at++
			case space(w) || punctuation(w):
				k++
			case space(got) || punctuation(got):
				r.at++
			default:
				return fmt.Sprintf("expected `%s` at %d, %s", strings.TrimSpace(text), r.itemAt(start), r.where())
			}
		}
		for r.at < len(r.items) && (space(r.peek()) || punctuation(r.peek())) && r.at < len(r.items) {
			if k < len(want) {
				break
			}
			// Leave a sign or digit for the next field; take only spaces
			// and punctuation the pattern's text did not account for.
			if !space(r.peek()) && !punctuation(r.peek()) {
				break
			}
			r.at++
		}
		return ""
	}
	for _, w := range want {
		if r.at >= len(r.items) {
			return fmt.Sprintf("expected `%s` at %d, %s", w.g, len(r.items), r.where())
		}
		got := r.items[r.at].g
		if got != w.g && !(space(got) && space(w.g)) && !(directionMark(w.g) && !directionMark(got)) {
			return fmt.Sprintf("expected `%s` at %d, %s", w.g, r.items[r.at].at, r.where())
		}
		if directionMark(w.g) && !directionMark(got) {
			continue
		}
		r.at++
	}
	return ""
}

func (r *reader) itemAt(i int) int {
	if i < len(r.items) {
		return r.items[i].at
	}
	return len(r.items)
}

// number reads a numeric field: least to most digits, or exactly width
// when exact (a numeric field right after it). It answers the value and
// how many digits were read. Strict reads two digits or one for a field of
// width 2, and exactly two for a two-digit year; lenient reads a year's
// two or four.
func (r *reader) number(what string, least, most, width int, exact bool) (int64, int, string) {
	if exact && width > 1 {
		least, most = width, width
	}
	if width > 2 && most < width {
		most = width
	}
	if what == "the year" && width == 2 && !r.lenient {
		least, most = 2, 2
	}
	var digits []byte
	for r.at < len(r.items) && len(digits) < most {
		d, ok := r.c.num.digit(r.items[r.at].g)
		if !ok {
			break
		}
		digits = append(digits, d)
		r.at++
	}
	if len(digits) < least {
		return 0, 0, fmt.Sprintf("expected %s at %d, %s", what, r.itemAt(r.at-len(digits)), r.where())
	}
	if what == "the year" && width == 2 && len(digits) == 3 {
		return 0, 0, fmt.Sprintf("expected a two- or four-digit year, %s", r.where())
	}
	n, _ := strconv.ParseInt(string(digits), 10, 64)
	return n, len(digits), ""
}

// nameSets are the names a text field of count letters reads: in strict
// parsing the field's own width in either context, in lenient every width.
func (c calendarIn) nameSets(kind string, standAlone bool, count int) [][]string {
	ctx, other := "format", "stand-alone"
	if standAlone {
		ctx, other = other, ctx
	}
	var out [][]string
	for _, w := range []string{widthOf(count, kind == "days"), "wide", "abbreviated", "short", "narrow"} {
		for _, x := range []string{ctx, other} {
			if ns := c.names(kind, x, w); ns != nil {
				out = append(out, ns)
			}
		}
	}
	return out
}

// name reads one of the names in sets, the longest that matches, and
// answers its index. Strict reads only the first two sets (the field's
// width, in its context and the other) exactly; lenient reads any set,
// without regard to case, with or without a final period.
func (r *reader) name(what string, sets [][]string, count int) (int, string) {
	if !r.lenient && len(sets) > 2 {
		sets = sets[:2]
	}
	best, bestLen := -1, 0
	for _, set := range sets {
		for i, n := range set {
			for _, form := range []string{n, strings.TrimSuffix(n, ".")} {
				if form == "" {
					continue
				}
				if !r.lenient && form != n {
					continue
				}
				if k := r.matchName(form); k > bestLen {
					best, bestLen = i, k
				}
			}
		}
	}
	if best < 0 {
		return 0, fmt.Sprintf("expected %s at %d, %s", what, r.itemAt(r.at), r.where())
	}
	r.at += bestLen
	if r.lenient && r.peek() == "." {
		r.at++
	}
	return best, ""
}

// matchName is how many items from the reader's position spell name, or 0.
func (r *reader) matchName(name string) int {
	want := graphemes(name)
	if r.at+len(want) > len(r.items) {
		return 0
	}
	for k, w := range want {
		got := r.items[r.at+k].g
		same := got == w.g || space(got) && space(w.g) || r.lenient && strings.EqualFold(got, w.g)
		if !same {
			return 0
		}
	}
	return len(want)
}

// The fixed patterns std/calendar's DatePattern, TimePattern and
// DateTimePattern hold, by their position in calendar's `pattern_check`.
const (
	patternDate = iota
	patternTime
	patternDateTime
)

// fixedLetters are the letters a fixed pattern may hold, and the most of
// each.
var fixedLetters = map[rune]int{'y': 9, 'M': 2, 'd': 2, 'D': 3, 'H': 2, 'm': 2, 's': 2, 'S': 9}

// CheckFixedPattern is why text is not a fixed pattern of kind, or "": a
// fixed pattern writes the same text in every locale, so it holds only
// numeric fields and literal text. A month or weekday name, AM or PM and
// an era are words of a language, and are an error that points to a
// skeleton.
func CheckFixedPattern(kind int, text string) string {
	fields, err := cldrdata.ParseDatePattern(text)
	if err != nil {
		return strings.TrimPrefix(err.Error(), fmt.Sprintf("pattern %q: ", text))
	}
	skel := map[int]string{patternDate: "Date.format_fields(date, locale, DateFields`yMMMd`)",
		patternTime: "Time.format_fields(time, locale, TimeFields`jm`)", patternDateTime: "DateTime.format_fields(moment, locale, DateTimeFields`yMMMdjm`)"}[kind]
	words := func(f cldrdata.DateField, what string) string {
		return fmt.Sprintf("`%s` writes %s, which is a word of a language: a fixed pattern writes numbers, the same in every locale; write it for a reader with a skeleton and a locale, as %s",
			strings.Repeat(string(f.Letter), f.Count), what, skel)
	}
	seen := map[fieldType]bool{}
	fieldsSeen := 0
	for _, f := range fields {
		if f.Letter == 0 {
			continue
		}
		fieldsSeen++
		t := fieldTypeOf(f.Letter)
		switch {
		case (f.Letter == 'M' || f.Letter == 'L') && f.Count >= 3:
			return words(f, "the month's name")
		case f.Letter == 'L':
			return "`L` is the stand-alone month, which a fixed pattern writes as `M` or `MM`"
		case t == ftWeekday:
			return words(f, "the day of the week")
		case t == ftPeriod:
			return words(f, "AM or PM")
		case t == ftEra:
			return words(f, "the era")
		case f.Letter == 'h' || f.Letter == 'K' || f.Letter == 'k':
			return fmt.Sprintf("`%c` is an hour of a 12-hour clock, which needs AM or PM, a word: a fixed pattern writes the 24-hour `HH`", f.Letter)
		case t == ftZone:
			return fmt.Sprintf("`%c` writes a time zone, which a fixed pattern does not hold", f.Letter)
		}
		most, ok := fixedLetters[f.Letter]
		switch {
		case !ok:
			return fmt.Sprintf("`%c` is not a field a fixed pattern holds: it holds y, M, d and D for the date and H, m, s and S for the time", f.Letter)
		case kind == patternDate && !t.dateHalf():
			return fmt.Sprintf("`%c` is a time of day, which a DatePattern does not hold: use TimePattern or DateTimePattern", f.Letter)
		case kind == patternTime && t.dateHalf():
			return fmt.Sprintf("`%c` is part of a date, which a TimePattern does not hold: use DatePattern or DateTimePattern", f.Letter)
		case f.Count > most:
			return fmt.Sprintf("`%s` is wider than a fixed pattern writes `%c`, at most %d", strings.Repeat(string(f.Letter), f.Count), f.Letter, most)
		case seen[t]:
			return fmt.Sprintf("the pattern names the %s twice", fieldTypeName(t))
		}
		seen[t] = true
	}
	if fieldsSeen == 0 {
		return "a pattern names at least one field"
	}
	return ""
}

// FFICheckFields is `calendar.fields_check`: why text is not a skeleton of
// kind (0 DateFields, 1 TimeFields, 2 DateTimeFields), or "".
func FFICheckFields(kind int64, text string) string { return CheckSkeleton(int(kind), text) }

// FFICheckPattern is `calendar.pattern_check`: why text is not a fixed
// pattern of kind (0 DatePattern, 1 TimePattern, 2 DateTimePattern), or "".
func FFICheckPattern(kind int64, text string) string { return CheckFixedPattern(int(kind), text) }
