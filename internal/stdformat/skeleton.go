package stdformat

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/cldrdata"
)

// SKELETONS. A skeleton (`DateFields`yMMMd``) names the fields a date shows
// and their widths, and each locale's CLDR data gives the pattern: the
// order, the punctuation and the words. This is ICU's
// DateTimePatternGenerator over the locale's available formats, by the
// matching rules of UTS #35 Part 4, "Matching Skeletons": the available
// skeleton with the same fields and the nearest widths, its fields then
// widened or narrowed to the ones asked for. A skeleton with date and time
// fields that no available format covers is split into the two, each
// matched, and glued by the date-time format of the date's length. A field
// no available format of its half has is appended after a space.
//
// A program whose skeletons are all literals carries, for each listed
// locale, only the pattern each resolves to (cldrdata.Selection.Skeletons):
// an exact `skeleton` record, which is taken as it is.

// fieldType is one kind of field a skeleton or pattern names.
type fieldType byte

const (
	ftEra fieldType = iota
	ftYear
	ftQuarter
	ftMonth
	ftDay
	ftDayOfYear
	ftWeekday
	ftPeriod
	ftHour
	ftMinute
	ftSecond
	ftFraction
	ftZone
	ftNone
)

// fieldTypeOf is the kind of field a pattern letter is.
func fieldTypeOf(r rune) fieldType {
	switch r {
	case 'G':
		return ftEra
	case 'y', 'Y', 'u', 'U', 'r':
		return ftYear
	case 'Q', 'q':
		return ftQuarter
	case 'M', 'L':
		return ftMonth
	case 'd':
		return ftDay
	case 'D':
		return ftDayOfYear
	case 'E', 'c', 'e':
		return ftWeekday
	case 'a', 'b', 'B':
		return ftPeriod
	case 'h', 'H', 'K', 'k', 'j', 'J', 'C':
		return ftHour
	case 'm':
		return ftMinute
	case 's':
		return ftSecond
	case 'S', 'A':
		return ftFraction
	case 'z', 'Z', 'O', 'v', 'V', 'X', 'x':
		return ftZone
	}
	return ftNone
}

// dateHalf reports whether a field is part of the date.
func (t fieldType) dateHalf() bool { return t <= ftWeekday }

// textual reports whether a field of letter r and count n writes words, not
// digits.
func textual(r rune, n int) bool {
	switch fieldTypeOf(r) {
	case ftEra, ftWeekday, ftPeriod, ftZone:
		return true
	case ftMonth, ftQuarter:
		return n >= 3
	}
	return false
}

// skelField is one field of a skeleton.
type skelField struct {
	letter rune
	count  int
}

// readSkeleton reads a skeleton's fields by type; the hour's `j` is
// answered by the locale's preferred hour, from its short time format.
func readSkeleton(s string, hour rune) map[fieldType]skelField {
	out := map[fieldType]skelField{}
	rs := []rune(s)
	for i := 0; i < len(rs); {
		r := rs[i]
		n := 1
		for i+n < len(rs) && rs[i+n] == r {
			n++
		}
		i += n
		t := fieldTypeOf(r)
		if t == ftNone {
			continue
		}
		if r == 'j' || r == 'J' || r == 'C' {
			r = hour
		}
		out[t] = skelField{r, n}
	}
	return out
}

// preferredHour is the hour letter of a locale's short time format (`h` in
// en, `H` in de).
func preferredHour(c calendarIn) rune {
	p := c.rec(cldrdata.RecordKey("time-format", "short"))
	for _, f := range parseDate(p) {
		switch f.Letter {
		case 'h', 'H', 'K', 'k':
			return f.Letter
		}
	}
	return 'H'
}

// ResolveSkeleton is the pattern a skeleton resolves to in locale's data.
func ResolveSkeleton(d *cldrdata.Data, locale, skeleton string) string {
	c := calendarFor(d, locale)
	if p := c.rec(cldrdata.RecordKey("skeleton", skeleton)); p != "" {
		return p
	}
	hour := preferredHour(c)
	want := readSkeleton(skeleton, hour)
	if len(want) == 0 {
		return ""
	}
	avail := c.available(hour)
	if p, ok := c.match(want, avail); ok {
		return p
	}
	// Split into the date's fields and the time's, and glue.
	date, tm := map[fieldType]skelField{}, map[fieldType]skelField{}
	for t, f := range want {
		if t.dateHalf() {
			date[t] = f
		} else {
			tm[t] = f
		}
	}
	if len(date) > 0 && len(tm) > 0 {
		dp, _ := c.matchAppending(date, avail)
		tp, _ := c.matchAppending(tm, avail)
		style := "short"
		switch m, e := date[ftMonth], date[ftWeekday]; {
		case m.count >= 4 && e.count > 0:
			style = "full"
		case m.count >= 4:
			style = "long"
		case m.count == 3:
			style = "medium"
		}
		format := c.rec(cldrdata.RecordKey("datetime-format", style))
		if format == "" {
			format = "{1} {0}"
		}
		return cldrdata.FormatDatePattern(glue(format, parseDate(dp), parseDate(tp)))
	}
	p, _ := c.matchAppending(want, avail)
	return p
}

// available is the locale's available formats, each with its fields.
type availableFormat struct {
	skeleton string
	fields   map[fieldType]skelField
	pattern  string
}

func (c calendarIn) available(hour rune) []availableFormat {
	var out []availableFormat
	if c.d == nil {
		return nil
	}
	for _, key := range c.d.KeysOn(c.chain, "skeleton\t") {
		skel := strings.TrimPrefix(key, "skeleton\t")
		p := c.rec(key)
		if p == "" {
			continue
		}
		out = append(out, availableFormat{skel, readSkeleton(skel, hour), p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].skeleton < out[j].skeleton })
	return out
}

// distance is how far an available format is from the fields asked for:
// -1 when it has a field they do not, else the count of fields it lacks,
// most significant, then the width mismatches.
func distance(want, have map[fieldType]skelField) (missing, cost int, ok bool) {
	for t := range have {
		if _, asked := want[t]; !asked {
			return 0, 0, false
		}
	}
	for t, w := range want {
		h, found := have[t]
		if !found {
			missing++
			continue
		}
		if t == ftHour && (w.letter == 'h' || w.letter == 'K') != (h.letter == 'h' || h.letter == 'K') {
			cost += 0x1000
		}
		if textual(w.letter, w.count) != textual(h.letter, h.count) {
			cost += 0x100
		}
		cost += absDiff(w.count, h.count)
	}
	return missing, cost, true
}

func absDiff(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}

// match is the best available format covering every field asked for, its
// widths adjusted; false when none covers them all.
func (c calendarIn) match(want map[fieldType]skelField, avail []availableFormat) (string, bool) {
	best, bestCost := -1, 0
	for i, a := range avail {
		missing, cost, ok := distance(want, a.fields)
		if !ok || missing > 0 {
			continue
		}
		if best < 0 || cost < bestCost {
			best, bestCost = i, cost
		}
	}
	if best < 0 {
		return "", false
	}
	return adjust(avail[best], want), true
}

// matchAppending is match, or the nearest format that covers some fields
// with each field it lacks appended after a space.
func (c calendarIn) matchAppending(want map[fieldType]skelField, avail []availableFormat) (string, bool) {
	if p, ok := c.match(want, avail); ok {
		return p, true
	}
	best, bestMissing, bestCost := -1, 0, 0
	for i, a := range avail {
		missing, cost, ok := distance(want, a.fields)
		if !ok || missing == len(want) {
			continue
		}
		if best < 0 || missing < bestMissing || missing == bestMissing && cost < bestCost {
			best, bestMissing, bestCost = i, missing, cost
		}
	}
	var fields []cldrdata.DateField
	have := map[fieldType]skelField{}
	if best >= 0 {
		fields = parseDate(adjust(avail[best], want))
		have = avail[best].fields
	}
	var types []int
	for t := range want {
		if _, ok := have[t]; !ok {
			types = append(types, int(t))
		}
	}
	sort.Ints(types)
	for _, t := range types {
		f := want[fieldType(t)]
		if len(fields) > 0 {
			fields = append(fields, cldrdata.DateField{Text: " "})
		}
		fields = append(fields, cldrdata.DateField{Letter: f.letter, Count: f.count})
	}
	return cldrdata.FormatDatePattern(fields), false
}

// adjust is a's pattern with each field the skeleton asks for written at
// the width asked: `MMM d` for `MMMMd` is `MMMM d`. A pattern's own letter
// is kept (its stand-alone `L`, its hour cycle); a numeric hour, minute or
// second is padded only when asked; and a field the locale writes in
// another form than its skeleton names (ja's `MMMd` is `M月d日`, digits
// for a month named in words) is left as the locale writes it.
func adjust(a availableFormat, want map[fieldType]skelField) string {
	fields := parseDate(a.pattern)
	for i, f := range fields {
		if f.Letter == 0 {
			continue
		}
		t := fieldTypeOf(f.Letter)
		w, ok := want[t]
		if !ok || t == ftPeriod || t == ftZone {
			continue
		}
		if h, named := a.fields[t]; named && textual(h.letter, h.count) != textual(f.Letter, f.Count) {
			continue
		}
		switch t {
		case ftHour, ftMinute, ftSecond:
			if w.count > f.Count {
				fields[i].Count = w.count
			}
		default:
			fields[i].Count = w.count
		}
	}
	return cldrdata.FormatDatePattern(fields)
}

// The skeletons std/calendar's DateFields, TimeFields and DateTimeFields
// accept, by their position in calendar's `fields_check`.
const (
	skeletonDate = iota
	skeletonTime
	skeletonDateTime
)

// skeletonLetters are the letters each kind of skeleton may name, and the
// most of each.
var skeletonLetters = map[rune]int{
	'G': 5, 'y': 4, 'M': 5, 'L': 5, 'd': 2, 'E': 5,
	'j': 2, 'h': 2, 'H': 2, 'm': 2, 's': 2, 'S': 9,
}

// CheckSkeleton is why a skeleton is not one of kind, or "": a letter
// UTS #35 does not define or std does not read, a field the kind does not
// hold (an hour in a DateFields), a field named twice or written too wide.
func CheckSkeleton(kind int, s string) string {
	if s == "" {
		return "a skeleton names at least one field"
	}
	seen := map[fieldType]rune{}
	rs := []rune(s)
	for i := 0; i < len(rs); {
		r := rs[i]
		n := 1
		for i+n < len(rs) && rs[i+n] == r {
			n++
		}
		i += n
		most, known := skeletonLetters[r]
		t := fieldTypeOf(r)
		switch {
		case !known && t == ftNone:
			return fmt.Sprintf("`%c` is not a field letter: a skeleton names fields with %s", r, skeletonLettersFor(kind))
		case !known:
			return fmt.Sprintf("`%c` is not a field a skeleton here names: it names fields with %s", r, skeletonLettersFor(kind))
		case kind == skeletonDate && !t.dateHalf():
			return fmt.Sprintf("`%c` is a time of day, which a DateFields does not hold: use TimeFields or DateTimeFields", r)
		case kind == skeletonTime && t.dateHalf():
			return fmt.Sprintf("`%c` is part of a date, which a TimeFields does not hold: use DateFields or DateTimeFields", r)
		case n > most:
			return fmt.Sprintf("`%s` is wider than a skeleton writes `%c`, at most %d", strings.Repeat(string(r), n), r, most)
		}
		if prev, twice := seen[t]; twice {
			return fmt.Sprintf("`%c` and `%c` both name the %s; a skeleton names each field once", prev, r, fieldTypeName(t))
		}
		seen[t] = r
	}
	return ""
}

func skeletonLettersFor(kind int) string {
	switch kind {
	case skeletonDate:
		return "G (era), y (year), M or L (month), d (day) and E (weekday)"
	case skeletonTime:
		return "j (hour as the locale writes it), h or H (12- or 24-hour), m (minute), s (second) and S (fraction)"
	}
	return "G, y, M, L, d and E for the date and j, h, H, m, s and S for the time"
}

func fieldTypeName(t fieldType) string {
	return [...]string{"era", "year", "quarter", "month", "day", "day of the year", "weekday", "period", "hour",
		"minute", "second", "fraction of a second", "zone", "field"}[t]
}

// SkeletonOf is the skeleton a resolution's key names, as std's literal
// texts write them: what `nomi build` resolves ahead of time.
func SkeletonOf(text string) (string, bool) {
	for _, kind := range []int{skeletonDate, skeletonTime, skeletonDateTime} {
		if CheckSkeleton(kind, text) == "" {
			return text, true
		}
	}
	return "", false
}
