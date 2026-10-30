package stdformat

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/rt"
)

// PARSING. Strict parsing (the default) accepts what the formatter writes
// for the locale, with three allowances: grouping separators may be left
// out, any Unicode space stands for a space the locale writes (a reader
// cannot type U+202F), and ASCII digits stand for the locale's own. Lenient
// parsing also accepts grouping separators anywhere in the integer part,
// space around the text, a leading plus sign, a minus sign before or after
// the number or parentheses around it, ASCII `-` and `%` for the locale's
// signs, the percent sign on either side, and the invisible direction marks
// some locales' signs carry. Neither guesses the locale.
//
// An error's position is a grapheme index into the text as given.

// item is one grapheme of the text and its index in the text as given.
type item struct {
	g  string
	at int
}

func graphemes(s string) []item {
	var out []item
	state := -1
	for i := 0; s != ""; i++ {
		var g string
		g, s, _, state = uniseg.FirstGraphemeClusterInString(s, state)
		out = append(out, item{g, i})
	}
	return out
}

// space reports whether g is one Unicode space character.
func space(g string) bool {
	r, n := utf8.DecodeRuneInString(g)
	return n == len(g) && (unicode.Is(unicode.Zs, r) || r == '\t')
}

// directionMark reports whether g is an invisible bidi mark.
func directionMark(g string) bool {
	switch g {
	case "‎", "‏", "؜":
		return true
	}
	return false
}

// stripMarks is s without its direction marks.
func stripMarks(s string) string {
	var b strings.Builder
	for _, it := range graphemes(s) {
		if !directionMark(it.g) {
			b.WriteString(it.g)
		}
	}
	return b.String()
}

// matchAt reports how many items from i spell sym, a space in sym matching
// any space, or 0 when they do not. An empty sym never matches.
func matchAt(items []item, i int, sym string) int {
	if sym == "" {
		return 0
	}
	want := graphemes(sym)
	if i+len(want) > len(items) {
		return 0
	}
	for k, w := range want {
		got := items[i+k].g
		if got != w.g && !(space(got) && space(w.g)) {
			return 0
		}
	}
	return len(want)
}

// matchEnd reports whether items end with sym, and where sym starts.
func matchEnd(items []item, sym string) (int, bool) {
	n := len(graphemes(sym))
	if n > len(items) {
		return 0, false
	}
	if n == 0 {
		return len(items), true
	}
	if matchAt(items, len(items)-n, sym) == n {
		return len(items) - n, true
	}
	return 0, false
}

// parsed is a number read off text: its sign and ASCII digits, or a special
// value.
type parsed struct {
	neg            bool
	intDigits      string
	frac           string
	infinite, nan  bool
	decimalAt      int
	hasDecimalSign bool
}

func unexpected(it item) *rt.NumberParseError {
	return &rt.NumberParseError{Tag: rt.TagNumberParseUnexpected, At: int64(it.at), Text: it.g}
}

// unexpectedAtEnd is the error for text that ends where more was due: the
// position after its last grapheme, with no text.
func unexpectedAtEnd(text string) *rt.NumberParseError {
	return &rt.NumberParseError{Tag: rt.TagNumberParseUnexpected, At: int64(uniseg.GraphemeClusterCount(text)), Text: ""}
}

// digit is the value of g as an ASCII digit or one of the system's.
func (n numbers) digit(g string) (byte, bool) {
	if len(g) == 1 && g[0] >= '0' && g[0] <= '9' {
		return g[0], true
	}
	for i, d := range n.digits {
		if g == d {
			return byte('0' + i), true
		}
	}
	return 0, false
}

// parse reads text as a number under p.
func (n numbers) parse(text string, p *cldrdata.Pattern, lenient, percent bool) (parsed, *rt.NumberParseError) {
	all := graphemes(text)
	items := all
	if lenient {
		items = nil
		for _, it := range all {
			if !directionMark(it.g) {
				items = append(items, it)
			}
		}
		for len(items) > 0 && space(items[0].g) {
			items = items[1:]
		}
		for len(items) > 0 && space(items[len(items)-1].g) {
			items = items[:len(items)-1]
		}
	}
	if len(items) == 0 {
		return parsed{}, &rt.NumberParseError{Tag: rt.TagNumberParseEmpty}
	}
	if lenient {
		return n.parseLenient(text, items, p, percent)
	}
	render := func(a cldrdata.Affix) string { return a.Render(n.sym.percent, n.sym.minus, n.sym.plus) }
	type form struct {
		neg            bool
		prefix, suffix string
	}
	forms := []form{
		{true, render(p.NegativePrefix), render(p.NegativeSuffix)},
		{false, render(p.PositivePrefix), render(p.PositiveSuffix)},
	}
	// The form whose prefix and suffix both match wins; failing that, the
	// first whose prefix matches is the one the text is reported against.
	var best *form
	var bodyStart, bodyEnd int
	whole := false
	for i := range forms {
		f := &forms[i]
		if f.neg && f.prefix == forms[1].prefix && f.suffix == forms[1].suffix {
			continue // no sign to tell the two apart: positive
		}
		k := len(graphemes(f.prefix))
		if k > 0 && matchAt(items, 0, f.prefix) != k {
			continue
		}
		if end, ok := matchEnd(items[k:], f.suffix); ok {
			best, bodyStart, bodyEnd, whole = f, k, k+end, true
			break
		}
		if best == nil {
			best, bodyStart, bodyEnd = f, k, len(items)
		}
	}
	if best == nil {
		// No prefix matches: the first grapheme where the text and the
		// positive prefix part ways.
		return parsed{}, mismatch(items, graphemes(forms[1].prefix), text)
	}
	if bodyStart == bodyEnd {
		if bodyEnd < len(items) {
			return parsed{}, unexpected(items[bodyEnd])
		}
		return parsed{}, unexpectedAtEnd(text)
	}
	out, stop, err := n.body(items[bodyStart:bodyEnd], p, false)
	if err != nil {
		return parsed{}, err
	}
	switch {
	case whole && stop < bodyEnd-bodyStart:
		return parsed{}, unexpected(items[bodyStart+stop])
	case !whole:
		// The suffix does not end the text: report where the text after
		// the number parts from it.
		return parsed{}, mismatch(items[bodyStart+stop:], graphemes(best.suffix), text)
	}
	out.neg = best.neg
	return out, nil
}

// mismatch is the error at the first item where items part from want, or
// at the end of text when items run out first.
func mismatch(items, want []item, text string) *rt.NumberParseError {
	for i, it := range items {
		if i >= len(want) || (it.g != want[i].g && !(space(it.g) && space(want[i].g))) {
			return unexpected(it)
		}
	}
	return unexpectedAtEnd(text)
}

// body reads the number between the affixes: digits, group separators and
// one decimal separator, or the infinity or NaN symbol. It answers how many
// items it read; the caller reports the first one it did not.
func (n numbers) body(items []item, p *cldrdata.Pattern, lenient bool) (parsed, int, *rt.NumberParseError) {
	var out parsed
	if k := matchAt(items, 0, n.sym.infinity); k > 0 {
		out.infinite = true
		return out, k, nil
	}
	if k := matchAt(items, 0, n.sym.nan); k > 0 {
		out.nan = true
		return out, k, nil
	}
	var intB, fracB strings.Builder
	runs := []int{0}
	var groups []item
	inFrac := false
	i := 0
loop:
	for i < len(items) {
		it := items[i]
		if d, ok := n.digit(it.g); ok {
			if inFrac {
				fracB.WriteByte(d)
			} else {
				intB.WriteByte(d)
				runs[len(runs)-1]++
			}
			i++
			continue
		}
		if k := matchAt(items, i, n.sym.group); k > 0 {
			if inFrac || (!lenient && intB.Len() == 0) {
				return out, 0, unexpected(it)
			}
			groups = append(groups, it)
			runs = append(runs, 0)
			i += k
			continue
		}
		if k := matchAt(items, i, n.sym.decimal); k > 0 && !inFrac {
			inFrac = true
			out.hasDecimalSign = true
			out.decimalAt = i
			i += k
			continue
		}
		break loop
	}
	if i == 0 {
		return out, 0, unexpected(items[0])
	}
	if !lenient {
		switch {
		case intB.Len() == 0:
			return out, 0, unexpected(items[0])
		case out.hasDecimalSign && fracB.Len() == 0:
			return out, 0, unexpected(items[out.decimalAt])
		}
		if err := checkGrouping(runs, groups, p); err != nil {
			return out, 0, err
		}
	} else if intB.Len() == 0 && fracB.Len() == 0 {
		return out, 0, unexpected(items[0])
	}
	out.intDigits = trimLeadingZeros(intB.String())
	out.frac = fracB.String()
	return out, i, nil
}

// checkGrouping holds strict parsing's group separators to the pattern's
// positions: the last group the primary size, the ones above it the
// secondary size, the first no longer.
func checkGrouping(runs []int, groups []item, p *cldrdata.Pattern) *rt.NumberParseError {
	if len(groups) == 0 {
		return nil
	}
	bad := func(it item) *rt.NumberParseError {
		return &rt.NumberParseError{Tag: rt.TagNumberParseGrouping, At: int64(it.at)}
	}
	if p.Primary == 0 {
		return bad(groups[0])
	}
	last := len(runs) - 1
	if runs[0] < 1 || runs[0] > p.Secondary {
		return bad(groups[0])
	}
	for j := 1; j < last; j++ {
		if runs[j] != p.Secondary {
			return bad(groups[j-1])
		}
	}
	if runs[last] != p.Primary {
		return bad(groups[last-1])
	}
	return nil
}

// parseLenient strips what lenient parsing allows around the number (a
// sign in any of its forms, the percent sign on either side) and reads the
// rest.
func (n numbers) parseLenient(text string, items []item, p *cldrdata.Pattern, percent bool) (parsed, *rt.NumberParseError) {
	neg := false
	trimSpace := func() {
		for len(items) > 0 && space(items[0].g) {
			items = items[1:]
		}
		for len(items) > 0 && space(items[len(items)-1].g) {
			items = items[:len(items)-1]
		}
	}
	// The text has lost its direction marks; so do the signs it is read
	// against.
	minus := []string{stripMarks(n.sym.minus), "-", "−"}
	plus := []string{stripMarks(n.sym.plus), "+"}
	stripFront := func(syms []string) bool {
		for _, s := range syms {
			if k := matchAt(items, 0, s); k > 0 {
				items = items[k:]
				trimSpace()
				return true
			}
		}
		return false
	}
	stripBack := func(syms []string) bool {
		for _, s := range syms {
			if start, ok := matchEnd(items, s); ok && s != "" && start < len(items) {
				items = items[:start]
				trimSpace()
				return true
			}
		}
		return false
	}
	if len(items) >= 2 && items[0].g == "(" && items[len(items)-1].g == ")" {
		items = items[1 : len(items)-1]
		trimSpace()
		neg = true
	}
	percents := []string{stripMarks(n.sym.percent), "%"}
	hasPercent := false
	if percent {
		hasPercent = stripFront(percents) || stripBack(percents)
	}
	switch {
	case stripFront(minus), stripBack(minus):
		neg = true
	case stripFront(plus):
	}
	if percent && !hasPercent {
		hasPercent = stripFront(percents) || stripBack(percents)
	}
	if (percent && !hasPercent) || len(items) == 0 {
		return parsed{}, unexpectedAtEnd(text)
	}
	out, stop, err := n.body(items, p, true)
	if err != nil {
		return parsed{}, err
	}
	if stop < len(items) {
		return parsed{}, unexpected(items[stop])
	}
	out.neg = neg
	return out, nil
}

func outOfRange() rt.NumberParseError {
	return rt.NumberParseError{Tag: rt.TagNumberParseOutOfRange}
}

// FFIParseInt is `Int.parse_in`.
func FFIParseInt(fr *rt.Frame, text, locale string, lenient bool) rt.Result[rt.Int, rt.NumberParseError] {
	n := numbersFor(stdlocale.Data(fr), locale)
	v, err := n.parse(text, n.decimal, lenient, false)
	if err != nil {
		return rt.Result[rt.Int, rt.NumberParseError]{Tag: rt.TagErr, Err: *err}
	}
	if v.infinite || v.nan {
		return rt.Result[rt.Int, rt.NumberParseError]{Tag: rt.TagErr, Err: outOfRange()}
	}
	if v.hasDecimalSign {
		// An Int has no fraction, even a zero one.
		items := graphemes(text)
		at := firstMatch(items, n.sym.decimal)
		return rt.Result[rt.Int, rt.NumberParseError]{Tag: rt.TagErr, Err: *unexpected(items[at])}
	}
	digits := v.intDigits
	if v.neg {
		digits = "-" + digits
	}
	x, ok := rt.ParseIntValue(digits)
	if !ok {
		return rt.Result[rt.Int, rt.NumberParseError]{Tag: rt.TagErr, Err: outOfRange()}
	}
	return rt.Result[rt.Int, rt.NumberParseError]{Tag: rt.TagOk, Ok: rt.IntOf(x)}
}

// firstMatch is the index of the first item where sym starts, or 0.
func firstMatch(items []item, sym string) int {
	for i := range items {
		if matchAt(items, i, sym) > 0 {
			return i
		}
	}
	return 0
}

// decimalText is the Decimal text of a parsed number, every typed digit
// kept, divided by 10^shift.
func decimalText(v parsed, shift int) string {
	intDigits, frac := v.intDigits, v.frac
	if shift > 0 {
		pad := strings.Repeat("0", max(0, shift-len(intDigits)+1))
		intDigits = pad + intDigits
		cut := len(intDigits) - shift
		intDigits, frac = trimLeadingZeros(intDigits[:cut]), intDigits[cut:]+frac
	}
	s := intDigits
	if frac != "" {
		s += "." + frac
	}
	if v.neg {
		s = "-" + s
	}
	return s
}

// FFIParseDecimal is `Decimal.parse_in`: the Decimal's text, which keeps
// the typed scale (12,50 is 12.50).
func FFIParseDecimal(fr *rt.Frame, text, locale string, lenient bool) rt.Result[string, rt.NumberParseError] {
	n := numbersFor(stdlocale.Data(fr), locale)
	return decimalResult(n.parse(text, n.decimal, lenient, false))(0)
}

// FFIParseDecimalPercent is `Decimal.parse_percent_in`: the percentage as a
// fraction (12,5 % is 0.125).
func FFIParseDecimalPercent(fr *rt.Frame, text, locale string, lenient bool) rt.Result[string, rt.NumberParseError] {
	n := numbersFor(stdlocale.Data(fr), locale)
	return decimalResult(n.parse(text, n.percent, lenient, true))(2)
}

func decimalResult(v parsed, err *rt.NumberParseError) func(shift int) rt.Result[string, rt.NumberParseError] {
	return func(shift int) rt.Result[string, rt.NumberParseError] {
		if err != nil {
			return rt.Result[string, rt.NumberParseError]{Tag: rt.TagErr, Err: *err}
		}
		if v.infinite || v.nan {
			return rt.Result[string, rt.NumberParseError]{Tag: rt.TagErr, Err: outOfRange()}
		}
		return rt.Result[string, rt.NumberParseError]{Tag: rt.TagOk, Ok: decimalText(v, shift)}
	}
}

// FFIParseFloat is `Float.parse_in`.
func FFIParseFloat(fr *rt.Frame, text, locale string, lenient bool) rt.Result[float64, rt.NumberParseError] {
	return parseFloat(fr, text, locale, lenient, false)
}

// FFIParseFloatPercent is `Float.parse_percent_in`: the percentage as a
// fraction (12,5 % is 0.125).
func FFIParseFloatPercent(fr *rt.Frame, text, locale string, lenient bool) rt.Result[float64, rt.NumberParseError] {
	return parseFloat(fr, text, locale, lenient, true)
}

// parseFloat reads a number, or with percent a percentage as a fraction,
// that a reader of locale wrote.
func parseFloat(fr *rt.Frame, text, locale string, lenient, percent bool) rt.Result[float64, rt.NumberParseError] {
	n := numbersFor(stdlocale.Data(fr), locale)
	p, shift := n.decimal, 0
	if percent {
		p, shift = n.percent, 2
	}
	v, err := n.parse(text, p, lenient, percent)
	if err != nil {
		return rt.Result[float64, rt.NumberParseError]{Tag: rt.TagErr, Err: *err}
	}
	var f float64
	switch {
	case v.nan:
		f = math.NaN()
	case v.infinite:
		f = math.Inf(1)
	default:
		var perr error
		f, perr = strconv.ParseFloat(decimalText(parsed{intDigits: v.intDigits, frac: v.frac}, shift), 64)
		if errors.Is(perr, strconv.ErrRange) && math.IsInf(f, 0) {
			return rt.Result[float64, rt.NumberParseError]{Tag: rt.TagErr, Err: outOfRange()}
		}
	}
	if v.neg {
		f = -f
	}
	return rt.Result[float64, rt.NumberParseError]{Tag: rt.TagOk, Ok: f}
}
