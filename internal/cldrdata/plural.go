package cldrdata

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Category is a CLDR plural category, numbered as std/plural's
// `PluralCategory` declares its variants: Zero is 0 and Other is 5.
type Category uint8

const (
	Zero Category = iota
	One
	Two
	Few
	Many
	Other
)

var categoryNames = [...]string{"zero", "one", "two", "few", "many", "other"}

func (c Category) String() string {
	if int(c) < len(categoryNames) {
		return categoryNames[c]
	}
	return fmt.Sprintf("Category(%d)", uint8(c))
}

// CategoryNamed is the category CLDR spells name ("few"), and false for
// any other text.
func CategoryNamed(name string) (Category, bool) {
	for i, n := range categoryNames {
		if n == name {
			return Category(i), true
		}
	}
	return 0, false
}

// Operands are CLDR's plural operands of one number as a reader sees it
// (UTS #35 Part 3, "Plural Operand Meanings"), kept as digits so a value of
// any size is exact.
//
//   - I holds the integer digits of the absolute value, with no leading
//     zeros ("0" for a value below one).
//   - F holds the visible fraction digits, trailing zeros included, so
//     "1.50" has F "50". v is len(F), w is len(F) without trailing zeros, f
//     is F as an integer and t is F without trailing zeros as an integer.
//   - E is the compact decimal exponent (CLDR's c, also called e). Nomi's
//     own numbers never have one; CLDR's samples do ("1.1c6").
//
// n, the absolute value, is I.F.
type Operands struct {
	I string
	F string
	E int
}

// ParseOperands reads a number as CLDR's samples and Nomi's Decimal Display
// write it: an optional sign, digits, an optional fraction, and an optional
// compact exponent `c<k>` (or `e<k>`). The sign is dropped, since operands
// describe the absolute value.
func ParseOperands(s string) (Operands, error) {
	text := strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	exp := 0
	if k := strings.IndexAny(text, "ce"); k >= 0 {
		n, err := strconv.Atoi(text[k+1:])
		if err != nil || n < 0 {
			return Operands{}, fmt.Errorf("plural operands: bad exponent in %q", s)
		}
		exp = n
		text = text[:k]
	}
	intPart, frac, _ := strings.Cut(text, ".")
	if intPart == "" || !allDigits(intPart) || !allDigits(frac) {
		return Operands{}, fmt.Errorf("plural operands: %q is not a decimal number", s)
	}
	// Shift the exponent's digits from the fraction into the integer part.
	shift := min(exp, len(frac))
	intPart += frac[:shift] + strings.Repeat("0", exp-shift)
	frac = frac[shift:]
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	return Operands{I: intPart, F: frac, E: exp}, nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// V is the number of visible fraction digits.
func (o Operands) V() int { return len(o.F) }

// W is the number of visible fraction digits without trailing zeros.
func (o Operands) W() int { return len(strings.TrimRight(o.F, "0")) }

// FDigits is f, the visible fraction digits as an integer, in digits.
func (o Operands) FDigits() string { return digitsOf(o.F) }

// TDigits is t, the visible fraction digits without trailing zeros as an
// integer, in digits.
func (o Operands) TDigits() string { return digitsOf(strings.TrimRight(o.F, "0")) }

func digitsOf(s string) string {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}

// value is one operand's integer value, and whether it is an integer at
// all (only n can fail to be one).
func (o Operands) value(operand byte) (*big.Int, bool) {
	var digits string
	switch operand {
	case 'n':
		if o.W() > 0 {
			return nil, false
		}
		digits = o.I
	case 'i':
		digits = o.I
	case 'v':
		return big.NewInt(int64(o.V())), true
	case 'w':
		return big.NewInt(int64(o.W())), true
	case 'f':
		digits = o.FDigits()
	case 't':
		digits = o.TDigits()
	case 'c', 'e':
		return big.NewInt(int64(o.E)), true
	}
	v, _ := new(big.Int).SetString(digits, 10)
	return v, true
}

// A Rule is one category's condition, parsed from CLDR's rule syntax
// (UTS #35 Part 3, "Plural rules syntax").
type Rule struct {
	text string
	or   [][]relation
}

type relation struct {
	operand byte
	mod     int64 // 0 when the expression has no modulus
	negate  bool  // `!=`
	ranges  [][2]int64
}

// Text is the rule as CLDR writes it, without samples.
func (r *Rule) Text() string { return r.text }

// ParseRule parses one rule's condition. Samples (`@integer`, `@decimal`)
// must already be removed.
func ParseRule(text string) (*Rule, error) {
	r := &Rule{text: text}
	if strings.TrimSpace(text) == "" {
		return r, nil
	}
	for _, orPart := range strings.Split(text, " or ") {
		var and []relation
		for _, relText := range strings.Split(orPart, " and ") {
			rel, err := parseRelation(strings.TrimSpace(relText))
			if err != nil {
				return nil, fmt.Errorf("plural rule %q: %w", text, err)
			}
			and = append(and, rel)
		}
		r.or = append(r.or, and)
	}
	return r, nil
}

func parseRelation(s string) (relation, error) {
	var rel relation
	lhs, rhs, found := strings.Cut(s, "!=")
	if found {
		rel.negate = true
	} else if lhs, rhs, found = strings.Cut(s, "="); !found {
		return rel, fmt.Errorf("relation %q has no = or !=", s)
	}
	fields := strings.Fields(lhs)
	switch {
	case len(fields) == 1:
	case len(fields) == 3 && (fields[1] == "%" || fields[1] == "mod"):
		m, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || m <= 0 {
			return rel, fmt.Errorf("relation %q has a bad modulus", s)
		}
		rel.mod = m
	default:
		return rel, fmt.Errorf("relation %q has a bad expression", s)
	}
	if len(fields[0]) != 1 || !strings.Contains("niwvftce", fields[0]) {
		return rel, fmt.Errorf("relation %q has unknown operand %q", s, fields[0])
	}
	rel.operand = fields[0][0]
	for _, item := range strings.Split(rhs, ",") {
		item = strings.TrimSpace(item)
		lo, hi, isRange := strings.Cut(item, "..")
		if !isRange {
			hi = lo
		}
		a, err1 := strconv.ParseInt(lo, 10, 64)
		b, err2 := strconv.ParseInt(hi, 10, 64)
		if err1 != nil || err2 != nil || a > b {
			return rel, fmt.Errorf("relation %q has a bad range %q", s, item)
		}
		rel.ranges = append(rel.ranges, [2]int64{a, b})
	}
	return rel, nil
}

// Matches reports whether o satisfies the rule. An empty rule matches
// nothing (CLDR writes `other` with no condition, and `other` is never
// tested).
func (r *Rule) Matches(o Operands) bool {
	for _, and := range r.or {
		all := true
		for _, rel := range and {
			if !rel.holds(o) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func (rel relation) holds(o Operands) bool {
	v, isInt := o.value(rel.operand)
	in := false
	if isInt {
		if rel.mod > 0 {
			v = new(big.Int).Mod(v, big.NewInt(rel.mod))
		}
		for _, rg := range rel.ranges {
			if v.Cmp(big.NewInt(rg[0])) >= 0 && v.Cmp(big.NewInt(rg[1])) <= 0 {
				in = true
				break
			}
		}
	}
	return in != rel.negate
}

// A RuleSet is one locale's rules of one kind (cardinal or ordinal): a rule
// for each category it uses other than Other, which is what matches when
// none does.
type RuleSet struct {
	categories []Category
	rules      []*Rule
}

// NewRuleSet pairs rules with their categories. Other must not be among
// them.
func NewRuleSet(categories []Category, rules []*Rule) *RuleSet {
	return &RuleSet{categories: categories, rules: rules}
}

// Select is the category o falls in.
func (rs *RuleSet) Select(o Operands) Category {
	for i, r := range rs.rules {
		if r.Matches(o) {
			return rs.categories[i]
		}
	}
	return Other
}

// Categories lists the categories the rules can produce, in Category order,
// Other last.
func (rs *RuleSet) Categories() []Category {
	out := append([]Category(nil), rs.categories...)
	return append(out, Other)
}

// Rule is the rule for c, or nil when the set has none (always for Other).
func (rs *RuleSet) Rule(c Category) *Rule {
	for i, have := range rs.categories {
		if have == c {
			return rs.rules[i]
		}
	}
	return nil
}

// CategoryOf is the name of the category the number written as decimal
// text falls in ("few"), or "other" for text that is not a number.
func (rs *RuleSet) CategoryOf(number string) string {
	o, err := ParseOperands(number)
	if err != nil {
		return Other.String()
	}
	return rs.Select(o).String()
}

// CategoryNames are Categories' names.
func (rs *RuleSet) CategoryNames() []string {
	var out []string
	for _, c := range rs.Categories() {
		out = append(out, c.String())
	}
	return out
}
