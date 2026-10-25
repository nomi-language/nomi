// Package stdformat is the host implementation of locale-aware number and
// list formatting and number parsing: `Int.format`, `Float.format`,
// `Decimal.format` and their percent and `parse_in` forms, and
// `String.join_list`.
//
// Everything is read from the running program's CLDR data
// (stdlocale.Data): a locale's records are looked up along its fallback
// chain among the locales the program carries, then in root (CLDR's und),
// so a locale the program does not list formats as its nearest listed
// ancestor, or as root.
//
// The numbering system is the locale's default (`ar-EG` writes
// Arabic-Indic digits), or the one its `-u-nu-` extension names when the
// data has digits for it (`ar-EG-u-nu-latn`, or `-u-nu-native`). Symbols
// come from that system's block, else the Latin one's.
package stdformat

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/rt"
)

// symbols are a numbering system's number symbols in one locale.
type symbols struct {
	decimal, group, percent, plus, minus, infinity, nan string
}

// numbers is what formatting and parsing read for one locale: the
// numbering system's digits and symbols, the two patterns and CLDR's
// minimum grouping digits.
type numbers struct {
	// system is the numbering system the digits are from (`latn`, `arab`).
	system      string
	digits      [10]string
	ascii       bool
	sym         symbols
	decimal     *cldrdata.Pattern
	percent     *cldrdata.Pattern
	minGrouping int
}

// patternCache holds patterns parsed once, by their text.
type patternCache struct {
	m     sync.Map // pattern text -> *cldrdata.Pattern
	parse func(string) (*cldrdata.Pattern, error)
	def   string
}

func newPatternCache(parse func(string) (*cldrdata.Pattern, error), def string) *patternCache {
	return &patternCache{parse: parse, def: def}
}

func (c *patternCache) get(text string) *cldrdata.Pattern {
	if p, ok := c.m.Load(text); ok {
		return p.(*cldrdata.Pattern)
	}
	p, err := c.parse(text)
	if err != nil {
		// cldrgen parsed every pattern it wrote; data that does not parse
		// was not written by it.
		p, _ = c.parse(c.def)
	}
	c.m.Store(text, p)
	return p
}

var numberPatterns = newPatternCache(cldrdata.ParsePattern, "#,##0.###")

func pattern(text string) *cldrdata.Pattern { return numberPatterns.get(text) }

// numbersFor resolves locale's number data in d.
func numbersFor(d *cldrdata.Data, locale string) numbers {
	chain := stdlocale.Fallbacks(d, locale)
	get := func(key string) string {
		v, _, _ := d.Resolve(chain, key)
		return v
	}
	def, native, _ := strings.Cut(get(cldrdata.KeyNumbering), "\t")
	if def == "" {
		def = "latn"
	}
	system := def
	switch nu := stdlocale.Extension(locale, "nu"); {
	case nu == "native" && native != "":
		system = native
	case nu == "latn":
		system = "latn"
	case nu != "":
		if _, ok := d.Digits(nu); ok {
			system = nu
		}
	}
	var n numbers
	n.ascii = true
	for i := range n.digits {
		n.digits[i] = string(rune('0' + i))
	}
	if system != "latn" {
		if ds, ok := d.Digits(system); ok {
			copy(n.digits[:], ds)
			n.ascii = false
		} else {
			system = "latn"
		}
	}
	bySystem := func(kind string) string {
		if v := get(kind + "\t" + system); v != "" {
			return v
		}
		return get(kind + "\tlatn")
	}
	s := strings.Split(bySystem("symbols"), "\t")
	if len(s) != 7 {
		s = []string{".", ",", "%", "+", "-", "∞", "NaN"}
	}
	n.system = system
	n.sym = symbols{decimal: s[0], group: s[1], percent: s[2], plus: s[3], minus: s[4], infinity: s[5], nan: s[6]}
	n.decimal = pattern(orDefault(bySystem("decimal"), "#,##0.###"))
	n.percent = pattern(orDefault(bySystem("percent"), "#,##0%"))
	n.minGrouping, _ = strconv.Atoi(get(cldrdata.KeyMinGrouping))
	if n.minGrouping < 1 {
		n.minGrouping = 1
	}
	return n
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// localDigits writes ASCII digits in the system's digits.
func (n numbers) localDigits(ascii string) string {
	if n.ascii {
		return ascii
	}
	var b strings.Builder
	for i := 0; i < len(ascii); i++ {
		b.WriteString(n.digits[ascii[i]-'0'])
	}
	return b.String()
}

// group inserts the group separator into integer digits (ASCII) as p
// places them, when grouping is on and the number is long enough.
func (n numbers) group(digits string, p *cldrdata.Pattern, grouping bool) string {
	local := n.localDigits(digits)
	if !grouping || p.Primary == 0 || len(digits)-p.Primary < n.minGrouping {
		return local
	}
	// Work on the local digits as runes-per-digit: each ASCII digit is one
	// local digit string.
	parts := make([]string, len(digits))
	for i := range digits {
		parts[i] = n.digits[digits[i]-'0']
		if n.ascii {
			parts[i] = digits[i : i+1]
		}
	}
	var groups []string
	end := len(parts)
	size := p.Primary
	for end > 0 {
		start := max(end-size, 0)
		groups = append([]string{strings.Join(parts[start:end], "")}, groups...)
		end = start
		size = p.Secondary
	}
	return strings.Join(groups, n.sym.group)
}

// render writes a number from its ASCII integer and fraction digits.
func (n numbers) render(neg bool, intDigits, frac string, p *cldrdata.Pattern, grouping bool) string {
	if len(intDigits) < p.MinInteger {
		intDigits = strings.Repeat("0", p.MinInteger-len(intDigits)) + intDigits
	}
	body := n.group(intDigits, p, grouping)
	if frac != "" {
		body += n.sym.decimal + n.localDigits(frac)
	}
	return n.affixed(neg, body, p)
}

func (n numbers) affixed(neg bool, body string, p *cldrdata.Pattern) string {
	prefix, suffix := p.PositivePrefix, p.PositiveSuffix
	if neg {
		prefix, suffix = p.NegativePrefix, p.NegativeSuffix
	}
	return prefix.Render(n.sym.percent, n.sym.minus, n.sym.plus) + body + suffix.Render(n.sym.percent, n.sym.minus, n.sym.plus)
}

// FFIFormatInt is `Int.format`.
func FFIFormatInt(fr *rt.Frame, locale string, value int64, grouping bool, minDigits int64) string {
	if minDigits < 1 || minDigits > maxDigits {
		rt.Trap(fmt.Sprintf("Int.format: min_digits must be 1 to %d, got %d", maxDigits, minDigits))
	}
	n := numbersFor(stdlocale.Data(fr), locale)
	digits := strconv.FormatUint(absInt(value), 10)
	if len(digits) < int(minDigits) {
		digits = strings.Repeat("0", int(minDigits)-len(digits)) + digits
	}
	return n.render(value < 0, digits, "", n.decimal, grouping)
}

// maxDigits bounds the digit counts the options may ask for.
const maxDigits = 100

func absInt(v int64) uint64 {
	if v < 0 {
		return uint64(-(v + 1)) + 1
	}
	return uint64(v)
}

// FFIFormatFloat is `Float.format`.
func FFIFormatFloat(fr *rt.Frame, locale string, value float64, minFraction, maxFraction int64, grouping bool) string {
	checkFraction("Float.format", minFraction, maxFraction)
	n := numbersFor(stdlocale.Data(fr), locale)
	return n.float(value, 0, int(minFraction), int(maxFraction), n.decimal, grouping)
}

// FFIFormatFloatPercent is `Float.format_percent`.
func FFIFormatFloatPercent(fr *rt.Frame, locale string, value float64, maxFraction int64) string {
	checkFraction("Float.format_percent", 0, maxFraction)
	n := numbersFor(stdlocale.Data(fr), locale)
	return n.float(value, 2, 0, int(maxFraction), n.percent, true)
}

func checkFraction(fn string, minFraction, maxFraction int64) {
	if minFraction < 0 || maxFraction > maxDigits || minFraction > maxFraction {
		rt.Trap(fmt.Sprintf("%s: the fraction digits must satisfy 0 <= min_fraction <= max_fraction <= %d, got %d and %d",
			fn, maxDigits, minFraction, maxFraction))
	}
}

// float formats value times 10^shift, rounded half-even to maxFraction
// digits from its shortest decimal form, with at least minFraction.
func (n numbers) float(value float64, shift, minFraction, maxFraction int, p *cldrdata.Pattern, grouping bool) string {
	neg := math.Signbit(value) && !math.IsNaN(value)
	switch {
	case math.IsNaN(value):
		return n.affixed(false, n.sym.nan, p)
	case math.IsInf(value, 0):
		return n.affixed(neg, n.sym.infinity, p)
	}
	intDigits, frac := shortestDecimal(math.Abs(value), shift)
	intDigits, frac = roundHalfEven(intDigits, frac, maxFraction)
	for len(frac) > minFraction && strings.HasSuffix(frac, "0") {
		frac = frac[:len(frac)-1]
	}
	return n.render(neg, intDigits, frac, p, grouping)
}

// shortestDecimal is the shortest decimal text that reads back as v (v >=
// 0), times 10^shift, as integer and fraction digits.
func shortestDecimal(v float64, shift int) (intDigits, frac string) {
	e := strconv.FormatFloat(v, 'e', -1, 64) // d.ddddde±XX
	mant, expText, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mant, ".", "", 1)
	point := 1 + exp + shift // digits before the decimal point
	switch {
	case point <= 0:
		return "0", strings.Repeat("0", -point) + digits
	case point >= len(digits):
		return digits + strings.Repeat("0", point-len(digits)), ""
	}
	return digits[:point], digits[point:]
}

// roundHalfEven rounds a decimal given as ASCII digits to places fraction
// digits, half to even, and pads it to exactly places digits.
func roundHalfEven(intDigits, frac string, places int) (string, string) {
	if len(frac) <= places {
		return trimLeadingZeros(intDigits), frac + strings.Repeat("0", places-len(frac))
	}
	kept, rest := frac[:places], frac[places:]
	up := false
	switch {
	case rest[0] > '5':
		up = true
	case rest[0] == '5':
		if strings.TrimRight(rest[1:], "0") != "" {
			up = true
		} else {
			last := intDigits[len(intDigits)-1]
			if places > 0 {
				last = kept[places-1]
			}
			up = (last-'0')%2 == 1
		}
	}
	all := []byte(intDigits + kept)
	if up {
		i := len(all) - 1
		for ; i >= 0; i-- {
			if all[i] == '9' {
				all[i] = '0'
				continue
			}
			all[i]++
			break
		}
		if i < 0 {
			all = append([]byte{'1'}, all...)
		}
	}
	s := string(all)
	return trimLeadingZeros(s[:len(s)-places]), s[len(s)-places:]
}

func trimLeadingZeros(s string) string {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}

// decimalParts splits a Decimal's Display text (`-1234.50`) into its sign,
// integer digits and fraction digits.
func decimalParts(text string) (neg bool, intDigits, frac string) {
	text, neg = strings.CutPrefix(text, "-")
	intDigits, frac, _ = strings.Cut(text, ".")
	return neg, trimLeadingZeros(intDigits), frac
}

// FFIFormatDecimal is `Decimal.format`: every digit of the scale, none
// rounded. text is the Decimal's Display text.
func FFIFormatDecimal(fr *rt.Frame, locale, text string, minFraction, maxFraction int64, grouping bool) string {
	checkFraction("Decimal.format", minFraction, maxFraction)
	n := numbersFor(stdlocale.Data(fr), locale)
	neg, intDigits, frac := decimalParts(text)
	return n.render(neg, intDigits, frac, n.decimal, grouping)
}

// FFIFormatDecimalPercent is `Decimal.format_percent`: text is the
// percentage, the value times 100 that std/decimal rounded to the digits
// it shows (12.5 for 0.125d).
func FFIFormatDecimalPercent(fr *rt.Frame, locale, text string, minFraction, maxFraction int64) string {
	checkFraction("Decimal.format_percent", minFraction, maxFraction)
	n := numbersFor(stdlocale.Data(fr), locale)
	neg, intDigits, frac := decimalParts(text)
	return n.render(neg, intDigits, frac, n.percent, true)
}

// Number writes the decimal text number (`-1234.5`) as locale writes it,
// for a message's `:number`, `:integer` and `:percent`: with the percent
// pattern when percent (the number is already times 100), grouping as
// MessageFormat's `useGrouping` says (auto and always group as the
// locale's pattern does, min2 only from two digits more than a group,
// never not at all), at least minInteger integer digits, and between
// minFraction and maxFraction fraction digits, rounded half to even;
// maxFraction -1 keeps every digit the number has.
func Number(d *cldrdata.Data, locale, number string, percent bool, grouping string, minInteger, minFraction, maxFraction int) string {
	n := numbersFor(d, locale)
	p := n.decimal
	if percent {
		p = n.percent
	}
	neg, intDigits, frac := decimalParts(number)
	if maxFraction >= 0 {
		intDigits, frac = roundHalfEven(intDigits, frac, maxFraction)
		for len(frac) > minFraction && strings.HasSuffix(frac, "0") {
			frac = frac[:len(frac)-1]
		}
	}
	if len(frac) < minFraction {
		frac += strings.Repeat("0", minFraction-len(frac))
	}
	if len(intDigits) < minInteger {
		intDigits = strings.Repeat("0", minInteger-len(intDigits)) + intDigits
	}
	if neg && strings.Trim(intDigits+frac, "0") == "" {
		neg = false
	}
	group := grouping != "never"
	if grouping == "min2" && p.Primary > 0 && len(intDigits)-p.Primary < 2 {
		group = false
	}
	return n.render(neg, intDigits, frac, p, group)
}
