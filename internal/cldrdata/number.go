package cldrdata

import (
	"fmt"
	"strconv"
	"strings"
)

// Pattern is a CLDR number pattern (UTS #35 Part 3, "Number Patterns"), as
// the standard decimal, percent and currency patterns use it: affixes,
// grouping sizes and digit counts. Scientific, significant-digit and
// padding patterns are not read.
type Pattern struct {
	// Positive and Negative are the two subpatterns' affixes. A pattern with
	// no negative subpattern has the positive one's with a minus sign before
	// the prefix.
	PositivePrefix, PositiveSuffix Affix
	NegativePrefix, NegativeSuffix Affix
	// Primary is the grouping size nearest the decimal point, 0 for none;
	// Secondary the size of every group above it (Primary when the pattern
	// gives one size).
	Primary, Secondary int
	// MinInteger is the fewest integer digits written, MinFraction and
	// MaxFraction the fewest and most fraction digits.
	MinInteger, MinFraction, MaxFraction int
}

// Affix is a prefix or suffix: literal text and the symbols it places.
type Affix []AffixPart

// AffixPart is literal Text, or one of the symbols '%', '-' and '+' (the
// locale's percent, minus and plus signs) or 'C' (a currency pattern's `¤`,
// the currency's symbol) when Symbol is set.
type AffixPart struct {
	Symbol byte
	Text   string
}

// Render is the affix with each symbol replaced by the locale's.
func (a Affix) Render(percent, minus, plus string) string {
	return a.RenderWith(percent, minus, plus, "")
}

// RenderWith is Render with the currency symbol in place of a `¤`.
func (a Affix) RenderWith(percent, minus, plus, currency string) string {
	var b strings.Builder
	for _, p := range a {
		switch p.Symbol {
		case 'C':
			b.WriteString(currency)
		case '%':
			b.WriteString(percent)
		case '-':
			b.WriteString(minus)
		case '+':
			b.WriteString(plus)
		default:
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// Has reports whether the affix places symbol.
func (a Affix) Has(symbol byte) bool {
	for _, p := range a {
		if p.Symbol == symbol {
			return true
		}
	}
	return false
}

func (a Affix) String() string {
	var b strings.Builder
	for _, p := range a {
		if p.Symbol != 0 {
			b.WriteByte(p.Symbol)
		} else {
			b.WriteString(strconv.Quote(p.Text))
		}
	}
	return b.String()
}

// ParsePattern reads a decimal or percent pattern.
func ParsePattern(text string) (*Pattern, error) { return parsePattern(text, false) }

// ParseCurrencyPattern reads a currency pattern, whose affixes place the
// currency symbol (`¤`).
func ParseCurrencyPattern(text string) (*Pattern, error) { return parsePattern(text, true) }

func parsePattern(text string, currency bool) (*Pattern, error) {
	pos, neg, hasNeg, err := splitPattern(text)
	if err != nil {
		return nil, err
	}
	p := &Pattern{}
	var body string
	if p.PositivePrefix, body, p.PositiveSuffix, err = subpattern(pos, currency); err != nil {
		return nil, fmt.Errorf("pattern %q: %w", text, err)
	}
	if err := p.readBody(body); err != nil {
		return nil, fmt.Errorf("pattern %q: %w", text, err)
	}
	if hasNeg {
		if p.NegativePrefix, _, p.NegativeSuffix, err = subpattern(neg, currency); err != nil {
			return nil, fmt.Errorf("pattern %q: %w", text, err)
		}
	} else {
		p.NegativePrefix = append(Affix{{Symbol: '-'}}, p.PositivePrefix...)
		p.NegativeSuffix = p.PositiveSuffix
	}
	return p, nil
}

// splitPattern splits text at its unquoted `;`.
func splitPattern(text string) (pos, neg string, hasNeg bool, err error) {
	quoted := false
	for i, r := range text {
		switch {
		case r == '\'':
			quoted = !quoted
		case r == ';' && !quoted:
			return text[:i], text[i+1:], true, nil
		}
	}
	if quoted {
		return "", "", false, fmt.Errorf("pattern %q: an unclosed quote", text)
	}
	return text, "", false, nil
}

func isBodyRune(r rune) bool { return r == '#' || r == '0' || r == ',' || r == '.' }

// subpattern splits one subpattern into its prefix, number body and suffix.
func subpattern(s string, currency bool) (prefix Affix, body string, suffix Affix, err error) {
	rs := []rune(s)
	i := 0
	prefix, i, err = affix(rs, i, currency)
	if err != nil {
		return nil, "", nil, err
	}
	start := i
	for i < len(rs) && isBodyRune(rs[i]) {
		i++
	}
	body = string(rs[start:i])
	if body == "" {
		return nil, "", nil, fmt.Errorf("no digits")
	}
	suffix, i, err = affix(rs, i, currency)
	if err != nil {
		return nil, "", nil, err
	}
	if i != len(rs) {
		return nil, "", nil, fmt.Errorf("digits after the suffix")
	}
	return prefix, body, suffix, nil
}

// affix reads an affix from rs at i up to the next unquoted body rune.
func affix(rs []rune, i int, currency bool) (Affix, int, error) {
	var out Affix
	text := func(s string) {
		if n := len(out); n > 0 && out[n-1].Symbol == 0 {
			out[n-1].Text += s
			return
		}
		out = append(out, AffixPart{Text: s})
	}
	for i < len(rs) {
		r := rs[i]
		switch {
		case r == '\'':
			if i+1 < len(rs) && rs[i+1] == '\'' {
				text("'")
				i += 2
				continue
			}
			end := i + 1
			for end < len(rs) && rs[end] != '\'' {
				end++
			}
			if end == len(rs) {
				return nil, i, fmt.Errorf("an unclosed quote")
			}
			text(string(rs[i+1 : end]))
			i = end + 1
		case isBodyRune(r):
			return out, i, nil
		case r == '%' || r == '-' || r == '+':
			out = append(out, AffixPart{Symbol: byte(r)})
			i++
		case r == '¤' && currency:
			for i < len(rs) && rs[i] == '¤' {
				i++
			}
			out = append(out, AffixPart{Symbol: 'C'})
		case r == '¤' || r == '‰' || r == '@' || r == 'E' || r == '*':
			return nil, i, fmt.Errorf("%q is not read in a decimal or percent pattern", r)
		default:
			text(string(r))
			i++
		}
	}
	return out, i, nil
}

func (p *Pattern) readBody(body string) error {
	intPart, frac, _ := strings.Cut(body, ".")
	if strings.ContainsAny(frac, ".,") {
		return fmt.Errorf("a separator in the fraction")
	}
	for _, r := range frac {
		if r == '0' {
			if p.MaxFraction != p.MinFraction {
				return fmt.Errorf("a 0 after a # in the fraction")
			}
			p.MinFraction++
		}
		p.MaxFraction++
	}
	groups := strings.Split(intPart, ",")
	for _, r := range intPart {
		if r == '0' {
			p.MinInteger++
		}
	}
	if n := len(groups); n > 1 {
		p.Primary = len(groups[n-1])
		p.Secondary = p.Primary
		if n > 2 {
			p.Secondary = len(groups[n-2])
		}
		if p.Primary == 0 || p.Secondary == 0 {
			return fmt.Errorf("an empty group")
		}
	}
	return nil
}
