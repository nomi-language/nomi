package cldrdata

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
)

// Parts are the parts of the data a program reaches, which decide what a
// bundle carries: a program that never formats a number carries no number
// symbols, and one that touches no locale data carries nothing.
type Parts uint16

const (
	// PartParents is CLDR's parent locales, every one: what
	// Locale.fallbacks answers from for any tag.
	PartParents Parts = 1 << iota
	// PartListedParents is the parent locales a chain can take to reach a
	// selected locale: those whose locale or parent has the language of a
	// selected one. Formatting a locale of another language finds no
	// selected locale on its chain however it walks, so it needs none.
	PartListedParents
	// PartPlurals is the plural rules of the selected locales.
	PartPlurals
	// PartNumbers is the numbering-system digits and each selected locale's
	// number symbols and patterns.
	PartNumbers
	// PartLists is each selected locale's list patterns.
	PartLists
	// PartAvailable is the list of locales the program names
	// (Locale.available).
	PartAvailable
	// PartGrammar is each selected locale's grammatical genders and cases.
	PartGrammar
	// PartCurrencyCodes is the ISO 4217 codes and their minor digits.
	PartCurrencyCodes
	// PartCurrencies is each selected locale's currency symbols and
	// patterns.
	PartCurrencies
	// PartCurrencyNames is each selected locale's currency names and the
	// pattern that writes an amount with one: what `Money.format` reads for
	// `display: .Name`, and `:currency` for `currencyDisplay=name`.
	PartCurrencyNames
	// PartDates is each selected locale's month, weekday, day-period and
	// era names, its date, time and date-time patterns and its GMT format.
	PartDates
	// PartSkeletons is each selected locale's available formats: the
	// patterns a skeleton (`DateFields`) is resolved against while the
	// program runs. A program whose skeletons are all known when it is
	// built carries their patterns, resolved (Selection.Skeletons), instead.
	PartSkeletons
	// PartRelative is each selected locale's relative-time forms.
	PartRelative
	// PartUnits is each selected locale's duration unit patterns.
	PartUnits
	// PartCollation is the root collation table and the collations
	// component's parent locales, and each selected locale's collation
	// tailorings and default collation type.
	PartCollation

	// AllParts is every part.
	AllParts = PartParents | PartListedParents | PartPlurals | PartNumbers | PartLists | PartAvailable | PartGrammar |
		PartCurrencyCodes | PartCurrencies | PartCurrencyNames | PartDates | PartSkeletons | PartRelative | PartUnits |
		PartCollation

	// sectionParts are the parts that are records of locale sections.
	sectionParts = PartNumbers | PartLists | PartGrammar | PartCurrencies | PartCurrencyNames | PartDates | PartSkeletons |
		PartRelative | PartUnits | PartCollation
)

// currencyRecord is the code a per-currency record (a symbol, narrow
// symbol or names) is keyed by, and whether key is one.
func currencyRecord(key string) (string, bool) {
	kind, rest, _ := strings.Cut(key, "\t")
	switch kind {
	case "currency-symbol", "currency-narrow", "currency-names":
		return rest, true
	}
	return "", false
}

// partOf is the part a locale record belongs to.
func partOf(key string) Parts {
	kind, _, _ := strings.Cut(key, "\t")
	if spec, ok := localeKinds[kind]; ok {
		return spec.part
	}
	return PartNumbers
}

// Selection is what a bundle carries.
type Selection struct {
	// Locales are the locales a program lists, canonical. Nil selects every
	// locale the data has, each section as it is.
	Locales []string
	// Parts are the parts the program reaches.
	Parts Parts
	// Chain answers a locale's fallbacks, nearest first, itself included,
	// root excluded (stdlocale.Fallbacks over the same data), and
	// PluralChain the chain plural rules are looked up along.
	Chain       func(tag string) []string
	PluralChain func(tag string) []string
	// CollationChain is the chain collation records are looked up along
	// (stdlocale.CollationFallbacks), which a selected locale's collation
	// records are resolved along.
	CollationChain func(tag string) []string
	// Embedded, when not nil, are the locales whose number and list
	// records the bundle holds: a browser build's image. The other
	// selected locales keep their plural rules and grammatical features
	// and are named in a `remote` record; their numbers and lists are in
	// locale files.
	Embedded []string
	// File writes a locale file's bundle: only the selected locales'
	// sections, which the program's own data already has the rest for.
	File bool
	// Skeletons are the skeletons the program formats dates with, known
	// when it is built, and Resolve the pattern a locale's data, d's, gives
	// one (the date-time pattern generator). With Skeletons and no
	// PartSkeletons, each selected locale's section holds a `skeleton`
	// record per skeleton with its resolved pattern, which the generator
	// takes as it is, in place of the locale's available formats.
	Skeletons []string
	Resolve   func(d *Data, tag, skeleton string) string
	// Currencies, when not nil, are the ISO codes of every currency the
	// program can format: each selected locale's section holds the
	// symbols and names of those currencies and no others.
	Currencies []string
	// Source is the program's source locale (`[i18n].source_locale`). When
	// Locales lists it, the bundle names it in a `source` record, and a
	// locale the program does not carry formats as it (Data.Resolve).
	Source string
}

// Select writes the bundle a program carries: the data of sel's locales,
// cut to sel's parts.
//
// With a list of locales, each listed locale's section is written resolved
// (every record it reaches along its chain, so it needs no ancestor beside
// it), then und's; plural rule sets name only the locales on a listed
// locale's plural chain. Without one, every section is written as it is.
// Parents are written whole, since any tag's chain walks them. A bundle with
// no parts is empty.
func (d *Data) Select(sel Selection) []byte {
	if sel.Parts == 0 && sel.Embedded == nil {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "nomi-cldr\t%s\ncldr\t%s\n", FormatVersion, d.Version)
	if sel.File {
		sel.Parts &= sectionParts &^ PartGrammar
	}
	if sel.Embedded != nil {
		var remote []string
		for _, tag := range sel.Locales {
			if !slices.Contains(sel.Embedded, tag) {
				remote = append(remote, tag)
			}
		}
		fmt.Fprintf(&b, "remote\t%s\n", strings.Join(remote, ","))
	}
	if sel.Source != "" && !sel.File && slices.Contains(sel.Locales, sel.Source) && sel.Parts&(sectionParts|PartPlurals) != 0 {
		fmt.Fprintf(&b, "source\t%s\n", sel.Source)
	}
	if sel.Parts&PartAvailable != 0 {
		avail := sel.Locales
		if avail == nil {
			avail = d.Locales()
			// und is root, not a locale a program offers.
			avail = removeTag(avail, "und")
		}
		fmt.Fprintf(&b, "available\t%s\n", strings.Join(avail, ","))
	}
	if sel.Parts&(PartParents|PartListedParents) != 0 {
		keepParent := func(string, string) bool { return true }
		if sel.Parts&PartParents == 0 && sel.Locales != nil {
			languages := map[string]bool{}
			for _, tag := range sel.Locales {
				languages[language(tag)] = true
			}
			keepParent = func(tag, parent string) bool { return languages[language(tag)] || languages[language(parent)] }
		}
		for _, k := range sortedKeys(d.parents) {
			if keepParent(k, d.parents[k]) {
				fmt.Fprintf(&b, "parent\t%s\t%s\n", k, d.parents[k])
			}
		}
		for _, k := range sortedKeys(d.pluralParents) {
			if keepParent(k, d.pluralParents[k]) {
				fmt.Fprintf(&b, "plural-parent\t%s\t%s\n", k, d.pluralParents[k])
			}
		}
		if d.NonlikelyScriptParent != "" {
			fmt.Fprintf(&b, "nonlikely-script-parent\t%s\n", d.NonlikelyScriptParent)
		}
	}
	if sel.Parts&PartPlurals != 0 {
		keep := func(string) bool { return true }
		if sel.Locales != nil {
			wanted := map[string]bool{"und": true}
			for _, tag := range sel.Locales {
				for _, t := range sel.PluralChain(tag) {
					wanted[t] = true
				}
			}
			keep = func(tag string) bool { return wanted[tag] }
		}
		for _, kind := range []struct {
			name string
			sets []setRecord
		}{{"cardinal", d.cardinalSets}, {"ordinal", d.ordinalSets}} {
			for _, s := range kind.sets {
				var tags []string
				for _, t := range s.tags {
					if keep(t) {
						tags = append(tags, t)
					}
				}
				if len(tags) == 0 {
					continue
				}
				fmt.Fprintf(&b, "%s\t%s", kind.name, strings.Join(tags, ","))
				if s.rules != "" {
					b.WriteString("\t" + s.rules)
				}
				b.WriteString("\n")
			}
		}
	}
	if sel.Parts&PartCurrencyCodes != 0 && !sel.File {
		byDigits := map[int][]string{}
		for _, code := range d.Currencies() {
			if n := d.currencies[code]; n != DefaultCurrencyDigits {
				byDigits[n] = append(byDigits[n], code)
			}
		}
		fmt.Fprintf(&b, "currencies\t%s\n", strings.Join(d.Currencies(), ","))
		digits := make([]int, 0, len(byDigits))
		for n := range byDigits {
			digits = append(digits, n)
		}
		sort.Ints(digits)
		for _, n := range digits {
			fmt.Fprintf(&b, "currency-digits\t%d\t%s\n", n, strings.Join(byDigits[n], ","))
		}
	}
	if sel.Parts&PartCollation != 0 && !sel.File {
		for _, k := range sortedKeys(d.collationParents) {
			fmt.Fprintf(&b, "collation-parent\t%s\t%s\n", k, d.collationParents[k])
		}
		if d.collationRoot != "" {
			fmt.Fprintf(&b, "collation-root\t%d\n%s\n", len(d.collationRoot), d.collationRoot)
		}
	}
	resolved := sel.Skeletons != nil && sel.Parts&PartSkeletons == 0 && sel.Parts&PartDates != 0 && sel.Resolve != nil
	sections := sel.Parts & sectionParts
	if sections == 0 {
		return []byte(b.String())
	}
	type section struct {
		tag  string
		recs map[string]string
	}
	var out []section
	if sel.Locales == nil {
		for _, tag := range d.Locales() {
			out = append(out, section{tag, d.locales[tag]})
		}
	} else {
		tags := sel.Locales
		if !slices.Contains(tags, "und") && !sel.File {
			tags = append(append([]string(nil), tags...), "und")
		}
		for _, tag := range tags {
			recs := map[string]string{}
			for _, key := range d.keysOn(sel.Chain(tag)) {
				if v, _, ok := d.Resolve(sel.Chain(tag), key); ok && !isCollationKey(key) {
					recs[key] = v
				}
			}
			// Collation records follow their own chain (zh-Hant's
			// tailorings are zh's).
			if sel.Parts&PartCollation != 0 && sel.CollationChain != nil {
				chain := sel.CollationChain(tag)
				for _, key := range d.keysOn(chain) {
					if v, _, ok := d.Resolve(chain, key); ok && isCollationKey(key) {
						recs[key] = v
					}
				}
			}
			out = append(out, section{tag, recs})
		}
	}
	systems := map[string]bool{}
	for _, s := range out {
		// A locale a browser build leaves in a file keeps only its
		// grammatical features here.
		keep := sections
		if sel.Embedded != nil && s.tag != "und" && !slices.Contains(sel.Embedded, s.tag) {
			keep &= PartGrammar
		}
		var keys []string
		for _, key := range sortedKeys(s.recs) {
			if partOf(key)&keep == 0 {
				continue
			}
			if code, ok := currencyRecord(key); ok && sel.Currencies != nil && !slices.Contains(sel.Currencies, code) {
				continue
			}
			keys = append(keys, key)
		}
		if len(keys) == 0 && s.tag != "und" && sel.Embedded != nil {
			continue
		}
		fmt.Fprintf(&b, "locale\t%s\n", s.tag)
		for _, key := range keys {
			if sys, ok := strings.CutPrefix(key, "symbols\t"); ok {
				systems[sys] = true
			}
			if kind, _, _ := strings.Cut(key, "\t"); binaryKinds[kind] {
				fmt.Fprintf(&b, "%s\t%d\n%s\n", key, len(s.recs[key]), s.recs[key])
				continue
			}
			fmt.Fprintf(&b, "%s\t%s\n", key, s.recs[key])
		}
		if resolved && keep&PartDates != 0 {
			for _, skel := range sortedUnique(sel.Skeletons) {
				if p := sel.Resolve(d, s.tag, skel); p != "" {
					fmt.Fprintf(&b, "skeleton\t%s\t%s\n", skel, p)
				}
			}
		}
	}
	if sections&PartNumbers != 0 {
		for _, sys := range sortedKeys(systems) {
			if ds, ok := d.digits[sys]; ok {
				fmt.Fprintf(&b, "digits\t%s\t%s\n", sys, strings.Join(ds, ""))
			}
		}
	}
	return []byte(b.String())
}

func isCollationKey(key string) bool {
	kind, _, _ := strings.Cut(key, "\t")
	return kind == KeyCollation || kind == KeyCollationDefault
}

func sortedUnique(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return slices.Compact(out)
}

// language is a tag's language subtag.
func language(tag string) string {
	lang, _, _ := strings.Cut(tag, "-")
	return lang
}

// keysOn is every record key a locale on chain, or und, holds.
func (d *Data) keysOn(chain []string) []string {
	seen := map[string]bool{}
	for _, tag := range append(append([]string(nil), chain...), "und") {
		for k := range d.locales[tag] {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func removeTag(tags []string, tag string) []string {
	out := tags[:0:0]
	for _, t := range tags {
		if t != tag {
			out = append(out, t)
		}
	}
	return out
}

type dataKey struct{}

// WithData is ctx carrying d, the data a machine's std adapters read.
func WithData(ctx context.Context, d *Data) context.Context {
	if d == nil {
		return ctx
	}
	return context.WithValue(ctx, dataKey{}, d)
}

// From is the data ctx carries, the data in its Store first, or Default's
// when it carries none.
func From(ctx context.Context) *Data {
	if s := StoreFrom(ctx); s != nil {
		return s.Data()
	}
	if ctx != nil {
		if d, ok := ctx.Value(dataKey{}).(*Data); ok {
			return d
		}
	}
	return Default()
}

var defaultData atomic.Pointer[func() *Data]

// SetDefault makes load's data the data a machine that was given none
// reads, loaded when first asked for. internal/cldrfull sets every locale's
// data; a runner, which links no cldrfull, has none and reads Empty.
func SetDefault(load func() *Data) { defaultData.Store(&load) }

var empty = Empty()

// Default is the data SetDefault set, or Empty.
func Default() *Data {
	if load := defaultData.Load(); load != nil {
		return (*load)()
	}
	return empty
}
