// Package stdplural is std/plural's host implementation: CLDR plural rule
// selection over Nomi's CLDR data (internal/cldrdata).
//
// A locale's rules are the first rule set found along its plural fallback
// chain (stdlocale.PluralFallbacks), then along the source locale's when
// the program does not carry the locale (cldrdata.Data.SourceFallback), and
// root's (`und`, every number is `other`) when none is. The data is the
// running program's
// (stdlocale.Data): the rules of the locales its nomi.toml lists. Categories
// cross as their position in std/plural's `PluralCategory` declaration,
// which is cldrdata.Category's numbering.
package stdplural

import (
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/rt"
)

// Rules is the rule set of one kind for locale in d.
func Rules(d *cldrdata.Data, locale string, ordinal bool) *cldrdata.RuleSet {
	lookup := d.Cardinal
	if ordinal {
		lookup = d.Ordinal
	}
	chain := stdlocale.PluralFallbacks(d, locale)
	if src, ok := d.SourceFallback(stdlocale.Fallbacks(d, locale)); ok {
		chain = append(chain, stdlocale.PluralFallbacks(d, src)...)
	}
	for _, tag := range append(chain, "und") {
		if rs, ok := lookup(tag); ok {
			return rs
		}
	}
	return cldrdata.NewRuleSet(nil, nil)
}

// FFICardinalTag is the cardinal category of value in locale. value is a
// Decimal's Display text, whose fraction digits are the visible ones.
func FFICardinalTag(fr *rt.Frame, locale, value string) int64 {
	ops, err := cldrdata.ParseOperands(value)
	if err != nil {
		return int64(cldrdata.Other)
	}
	return int64(Rules(stdlocale.Data(fr), locale, false).Select(ops))
}

// FFIOrdinalTag is the ordinal category of value in locale.
func FFIOrdinalTag(fr *rt.Frame, locale string, value rt.Int) int64 {
	ops, err := cldrdata.ParseOperands(value.String())
	if err != nil {
		return int64(cldrdata.Other)
	}
	return int64(Rules(stdlocale.Data(fr), locale, true).Select(ops))
}

// FFICardinalTags lists the cardinal categories locale's rules can select.
func FFICardinalTags(fr *rt.Frame, locale string) []int64 {
	return tags(Rules(stdlocale.Data(fr), locale, false))
}

// FFIOrdinalTags lists the ordinal categories locale's rules can select.
func FFIOrdinalTags(fr *rt.Frame, locale string) []int64 {
	return tags(Rules(stdlocale.Data(fr), locale, true))
}

func tags(rs *cldrdata.RuleSet) []int64 {
	var out []int64
	for _, c := range rs.Categories() {
		out = append(out, int64(c))
	}
	return out
}
