package stdformat

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdplural"
	"github.com/nomi-language/nomi/rt"
)

// MONEY. std/currency's codes and std/money's formatting and parsing. An
// ISO currency's minor digits are the program's CLDR data's
// (cldrdata.PartCurrencyCodes); its symbols, names and the locale's
// currency patterns are the locale's (PartCurrencies). A custom currency
// has none in CLDR: every display writes its code, where the locale's
// pattern puts the symbol.

// The displays std/money's CurrencyDisplay names, by position.
const (
	displaySymbol = iota
	displayNarrow
	displayCode
	displayName
)

// The answers of currency_check.
const (
	currencyOK = iota
	currencyMalformed
	currencyIsoCode
	currencyDigits
	currencyNotIso
)

// maxCurrencyDigits are the most minor digits a custom currency may have.
const maxCurrencyDigits = 18

// FFICurrencyCheck is `currency.currency_check`: whether code (with digits,
// or -1 for an ISO code) makes a currency, by currency_check's answers.
func FFICurrencyCheck(fr *rt.Frame, code string, digits int64) int64 {
	return int64(CurrencyCheck(stdlocale.Data(fr), code, digits))
}

// CurrencyCheck checks a currency code against d's ISO codes: an ISO code
// when digits is -1, else a custom code with that many minor digits.
func CurrencyCheck(d *cldrdata.Data, code string, digits int64) int {
	if !currencyCodeShaped(code) {
		return currencyMalformed
	}
	_, iso := d.CurrencyDigits(code)
	switch {
	case digits < 0 && iso:
		return currencyOK
	case digits < 0:
		return currencyNotIso
	case iso:
		return currencyIsoCode
	case digits > maxCurrencyDigits:
		return currencyDigits
	}
	return currencyOK
}

// CustomCurrencyProblem says why text is not a custom currency as its
// literal writes it (`PTS:0`), against d's ISO codes, in the words
// std/currency's Error shows; "" when it is one.
func CustomCurrencyProblem(d *cldrdata.Data, text string) string {
	code, digits, custom := strings.Cut(text, ":")
	if !currencyCodeShaped(code) {
		return fmt.Sprintf("`%s` is not a currency code: a code is 1 to 12 of A-Z and 0-9, starting with a letter", code)
	}
	if !custom {
		if _, iso := d.CurrencyDigits(code); iso {
			return fmt.Sprintf("`%s` is an ISO 4217 currency, which is not a custom one", code)
		}
		return fmt.Sprintf("`%s` names no minor digits: write the code, a colon and the digits, as %s:2", code, code)
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 || digits != strconv.FormatInt(n, 10) {
		return fmt.Sprintf("`%s` is not a count of minor digits: write a whole number from 0 to 18, as %s:2", digits, code)
	}
	switch CurrencyCheck(d, code, n) {
	case currencyIsoCode:
		return fmt.Sprintf("`%s` is an ISO 4217 currency, which is not a custom one", code)
	case currencyDigits:
		return fmt.Sprintf("a currency has 0 to 18 minor digits, not %d", n)
	}
	return ""
}

// currencyCodeShaped reports whether code is 1 to 12 of A-Z and 0-9,
// starting with a letter.
func currencyCodeShaped(code string) bool {
	if len(code) == 0 || len(code) > 12 || code[0] < 'A' || code[0] > 'Z' {
		return false
	}
	for i := 0; i < len(code); i++ {
		if c := code[i]; !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// FFICurrencyCodeOf is `currency.currency_code_of`: the code of a
// currency's text, the part before a custom currency's `:`.
func FFICurrencyCodeOf(text string) string {
	code, _, _ := strings.Cut(text, ":")
	return code
}

// FFICurrencyDigitsOf is `currency.currency_digits_of`: the minor digits of
// a currency's text, those after a custom currency's `:` (-1 when they are
// not a whole number), or an ISO code's from the data, 2 for a code it does
// not list.
func FFICurrencyDigitsOf(fr *rt.Frame, text string) int64 {
	code, digits, custom := strings.Cut(text, ":")
	if custom {
		n, err := strconv.ParseInt(digits, 10, 64)
		if err != nil || n < 0 || digits != strconv.FormatInt(n, 10) {
			return -1
		}
		return n
	}
	n, ok := stdlocale.Data(fr).CurrencyDigits(code)
	if !ok {
		n = cldrdata.DefaultCurrencyDigits
	}
	return int64(n)
}

// FFIMoneyMixed is `money_format.money_mixed`: the trap two amounts in two
// currencies raise, naming both.
func FFIMoneyMixed(operation, a, b string) rt.Unit {
	rt.Trap(MixedText(operation, a, b))
	return rt.Unit{}
}

// MixedText is the trap `Money.add` raises for amounts in two currencies:
// `Money.add: the amounts are in two currencies, EUR and PLN`.
func MixedText(operation, a, b string) string {
	return fmt.Sprintf("%s: the amounts are in two currencies, %s and %s", operation, a, b)
}

// currencyIn is what formatting and parsing read of one currency in one
// locale.
type currencyIn struct {
	code, symbol, narrow string
	iso                  bool
	digits               int
	// std and alpha are the locale's currency patterns: the standard one,
	// and the one for a symbol made of letters next to the digits. acct and
	// acctAlpha are the accounting ones, which most locales write as the
	// standard ones and some put a negative amount in parentheses with.
	std, alpha, acct, acctAlpha *cldrdata.Pattern
}

var currencyPatterns = newPatternCache(cldrdata.ParseCurrencyPattern, "¤#,##0.00")

func currencyFor(d *cldrdata.Data, locale string, n numbers, code string, digits int, iso bool) currencyIn {
	chain := stdlocale.Fallbacks(d, locale)
	get := func(key string) string {
		v, _, _ := d.Resolve(chain, key)
		return v
	}
	c := currencyIn{code: code, symbol: code, narrow: code, iso: iso, digits: digits}
	if iso {
		if s := get(cldrdata.RecordKey("currency-symbol", code)); s != "" {
			c.symbol = s
		}
		c.narrow = c.symbol
		if s := get(cldrdata.RecordKey("currency-narrow", code)); s != "" {
			c.narrow = s
		}
	}
	pats := get(cldrdata.RecordKey("currency-pattern", n.system))
	if pats == "" {
		pats = get(cldrdata.RecordKey("currency-pattern", "latn"))
	}
	fields := strings.Split(pats, "\t")
	pattern := func(i int, otherwise *cldrdata.Pattern) *cldrdata.Pattern {
		if i < len(fields) && fields[i] != "" {
			return currencyPatterns.get(fields[i])
		}
		return otherwise
	}
	c.std = pattern(0, currencyPatterns.get(""))
	c.alpha = pattern(1, c.std)
	c.acct = pattern(2, c.std)
	c.acctAlpha = pattern(3, c.alpha)
	return c
}

// marker is the text a display writes for the currency.
func (c currencyIn) marker(display int) string {
	if !c.iso {
		return c.code
	}
	switch display {
	case displayNarrow:
		return c.narrow
	case displayCode:
		return c.code
	}
	return c.symbol
}

// patternFor is the pattern marker is written in: the alpha pattern when
// the standard one puts marker right against the digits and its character
// there is not a symbol or a space (UTS #35 currency spacing), so `BTC` is
// kept apart from `0.5`. With accounting it chooses between the accounting
// patterns the same way.
func (c currencyIn) patternFor(marker string, accounting bool) *cldrdata.Pattern {
	base, alpha := c.std, c.alpha
	if accounting {
		base, alpha = c.acct, c.acctAlpha
	}
	if marker == "" {
		return base
	}
	first, _ := utf8.DecodeRuneInString(marker)
	last, _ := utf8.DecodeLastRuneInString(marker)
	letterish := func(r rune) bool { return !unicode.IsSymbol(r) && !unicode.Is(unicode.Z, r) }
	pre, suf := base.PositivePrefix, base.PositiveSuffix
	if n := len(pre); n > 0 && pre[n-1].Symbol == 'C' && letterish(last) {
		return alpha
	}
	if len(suf) > 0 && suf[0].Symbol == 'C' && letterish(first) {
		return alpha
	}
	return base
}

// FFIFormatMoney is `money_format.money_format_raw`: amount (a Decimal's
// text) in the currency of code, written as display says, in the locale's
// accounting pattern when accounting is set.
func FFIFormatMoney(fr *rt.Frame, locale, amount, code string, digits int64, iso bool, display int64, accounting bool) string {
	return Money(stdlocale.Data(fr), locale, amount, code, int(digits), iso, int(display), accounting)
}

// Money writes amount (a Decimal's text) in the currency of code, with
// digits minor digits, as a reader of locale writes it: at least the
// currency's minor digits, every digit the amount has, and the currency as
// display shows it (0 symbol, 1 narrow symbol, 2 code, 3 name). With
// accounting it is written in the locale's accounting pattern, which in
// some locales puts a negative amount in parentheses (`($10.00)` in en);
// a name display has no accounting form and writes its sign as usual.
func Money(d *cldrdata.Data, locale, amount, code string, digits int, iso bool, display int, accounting bool) string {
	n := numbersFor(d, locale)
	c := currencyFor(d, locale, n, code, digits, iso)
	neg, intDigits, frac := decimalParts(amount)
	if len(frac) < digits {
		frac += strings.Repeat("0", digits-len(frac))
	}
	if neg && strings.Trim(intDigits+frac, "0") == "" {
		neg = false
	}
	if display == displayName && iso {
		return c.named(d, locale, n, neg, intDigits, frac)
	}
	marker := c.marker(display)
	p := c.patternFor(marker, accounting)
	if len(intDigits) < p.MinInteger {
		intDigits = strings.Repeat("0", p.MinInteger-len(intDigits)) + intDigits
	}
	body := n.group(intDigits, p, true)
	if frac != "" {
		body += n.sym.decimal + n.localDigits(frac)
	}
	prefix, suffix := p.PositivePrefix, p.PositiveSuffix
	if neg {
		prefix, suffix = p.NegativePrefix, p.NegativeSuffix
	}
	render := func(a cldrdata.Affix) string { return a.RenderWith(n.sym.percent, n.sym.minus, n.sym.plus, marker) }
	return render(prefix) + body + render(suffix)
}

// named writes an amount with its currency's name in the plural form the
// amount takes, by the locale's unit pattern (`12.50 euros`).
func (c currencyIn) named(d *cldrdata.Data, locale string, n numbers, neg bool, intDigits, frac string) string {
	chain := stdlocale.Fallbacks(d, locale)
	number := n.render(neg, intDigits, frac, n.decimal, true)
	plain := intDigits
	if frac != "" {
		plain += "." + frac
	}
	category := stdplural.Rules(d, locale, false).CategoryOf(plain)
	pick := func(key string) string {
		v, _, _ := d.Resolve(chain, key)
		return pluralForm(v, category)
	}
	name := pick(cldrdata.RecordKey("currency-names", c.code))
	if name == "" {
		name = c.code
	}
	pattern := pick("currency-unit")
	if pattern == "" {
		pattern = "{0} {1}"
	}
	return strings.NewReplacer("{0}", number, "{1}", name).Replace(pattern)
}

// pluralCounts are CLDR's categories in the order a record's six forms
// list them.
var pluralCounts = []string{"zero", "one", "two", "few", "many", "other"}

// pluralForm is the form of a six-form record (zero to other, tab-separated)
// for category, else its other form.
func pluralForm(record, category string) string {
	forms := strings.Split(record, "\t")
	if len(forms) != len(pluralCounts) {
		return ""
	}
	for i, c := range pluralCounts {
		if c == category && forms[i] != "" {
			return forms[i]
		}
	}
	return forms[len(forms)-1]
}

// concrete is p with marker written in place of its currency symbol, so the
// number parser reads it as literal affix text.
func concrete(p *cldrdata.Pattern, marker string) *cldrdata.Pattern {
	q := *p
	lit := func(a cldrdata.Affix) cldrdata.Affix {
		out := cldrdata.Affix{}
		for _, part := range a {
			if part.Symbol == 'C' {
				part = cldrdata.AffixPart{Text: marker}
			}
			if n := len(out); n > 0 && out[n-1].Symbol == 0 && part.Symbol == 0 {
				out[n-1].Text += part.Text
				continue
			}
			out = append(out, part)
		}
		return out
	}
	q.PositivePrefix, q.PositiveSuffix = lit(p.PositivePrefix), lit(p.PositiveSuffix)
	q.NegativePrefix, q.NegativeSuffix = lit(p.NegativePrefix), lit(p.NegativeSuffix)
	return &q
}

// FFIParseMoney is `money_format.money_parse_raw`: the amount, as a
// Decimal's text, that text writes in the currency of code.
func FFIParseMoney(fr *rt.Frame, text, locale, code string, digits int64, iso, lenient bool) rt.Result[string, rt.NumberParseError] {
	amount, err := ParseMoney(stdlocale.Data(fr), text, locale, code, int(digits), iso, lenient)
	if err != nil {
		return rt.Result[string, rt.NumberParseError]{Tag: rt.TagErr, Err: *err}
	}
	return rt.Result[string, rt.NumberParseError]{Tag: rt.TagOk, Ok: amount}
}

// ParseMoney reads an amount in the currency of code that a reader of
// locale wrote. Strict reads the locale's pattern with the currency's
// symbol, narrow symbol or code where the pattern puts it; lenient also
// takes any of them before or after the number, or none. Either way text
// that names something else where the currency goes is WrongCurrency, and
// more fraction digits than the currency has is TooManyFractionDigits.
func ParseMoney(d *cldrdata.Data, text, locale, code string, digits int, iso, lenient bool) (string, *rt.NumberParseError) {
	n := numbersFor(d, locale)
	c := currencyFor(d, locale, n, code, digits, iso)
	markers := []string{c.symbol}
	for _, m := range []string{c.narrow, c.code} {
		if !containsString(markers, m) {
			markers = append(markers, m)
		}
	}
	var v parsed
	var err *rt.NumberParseError
	if lenient {
		v, err = c.parseLenient(n, text, markers)
	} else {
		v, err = c.parseStrict(n, text, markers)
	}
	if err != nil {
		if found := foreignCurrency(n, text, markers); found != "" {
			want := c.code
			if !iso {
				want = fmt.Sprintf("%s:%d", code, digits)
			}
			return "", &rt.NumberParseError{Tag: rt.TagNumberParseWrongCurrency, Expected: rt.Currency(want), Found: found}
		}
		return "", err
	}
	if v.infinite || v.nan {
		e := outOfRange()
		return "", &e
	}
	if len(v.frac) > digits {
		return "", &rt.NumberParseError{Tag: rt.TagNumberParseTooManyFractionDigits, Max: int64(digits)}
	}
	return decimalText(v, 0), nil
}

func containsString(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// parseStrict reads text with each marker in the pattern a formatter would
// write it in; the error reported is the symbol's.
func (c currencyIn) parseStrict(n numbers, text string, markers []string) (parsed, *rt.NumberParseError) {
	var first *rt.NumberParseError
	for _, m := range markers {
		v, err := n.parse(text, concrete(c.patternFor(m, false), m), false, false)
		if err == nil {
			return v, nil
		}
		if first == nil {
			first = err
		}
	}
	return parsed{}, first
}

// parseLenient leaves out a marker before or after the number, if there is
// one, and reads the rest as lenient number parsing does.
func (c currencyIn) parseLenient(n numbers, text string, markers []string) (parsed, *rt.NumberParseError) {
	items := graphemes(text)
	first, last := numericSpan(n, items)
	rest := text
	if first >= 0 {
		before, after := joinItems(items[:first]), joinItems(items[last+1:])
		for _, m := range markers {
			if strings.TrimSpace(stripMarks(before)) == m || strings.HasSuffix(strings.TrimSpace(before), m) {
				before = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(before), m))
				break
			}
			if strings.TrimSpace(stripMarks(after)) == m || strings.HasPrefix(strings.TrimSpace(after), m) {
				after = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(after), m))
				break
			}
		}
		rest = before + joinItems(items[first:last+1]) + after
	}
	return n.parse(rest, n.decimal, true, false)
}

// numericSpan is the first and last items of text that can be part of a
// number: digits, separators and signs; -1 when there are none.
func numericSpan(n numbers, items []item) (first, last int) {
	first, last = -1, -1
	for i, it := range items {
		if _, ok := n.digit(it.g); ok {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return -1, -1
	}
	for first > 0 && numberSign(n, items[first-1].g) {
		first--
	}
	return first, last
}

func numberSign(n numbers, g string) bool {
	return g == "-" || g == "+" || g == "(" || stripMarks(n.sym.minus) == g || stripMarks(n.sym.plus) == g
}

func joinItems(items []item) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.g)
	}
	return b.String()
}

// foreignCurrency is the text before or after the number in text, when it
// is none of markers and holds something that is not a sign or a space:
// what WrongCurrency names as found. "" when there is none.
func foreignCurrency(n numbers, text string, markers []string) string {
	items := graphemes(text)
	first, last := numericSpan(n, items)
	if first < 0 {
		return ""
	}
	for _, side := range []string{joinItems(items[:first]), joinItems(items[last+1:])} {
		s := strings.TrimFunc(stripMarks(side), func(r rune) bool {
			return unicode.IsSpace(r) || unicode.Is(unicode.Z, r) || r == '(' || r == ')' || r == '-' || r == '+' || r == '−'
		})
		if s == "" || containsString(markers, s) {
			continue
		}
		if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsSymbol(r) }) {
			return s
		}
	}
	return ""
}
