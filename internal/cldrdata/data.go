// Package cldrdata holds the locale data Nomi takes from CLDR, and reads it.
//
// internal/cldrgen writes the data from the pinned CLDR release vendored in
// internal/cldrgen/data, as blobs in internal/cldrfull: root.cldr (plural
// rules, parent locales, numbering-system digits, ISO 4217 codes and their
// minor digits, the root collation), locales.cldr (each locale's number
// symbols and patterns, list patterns, grammatical features, currency
// symbols, names and patterns, calendar names and patterns, relative-time
// forms, duration units and collation tailorings) and collation-cjk.cldr
// (the large Chinese and Korean collation tailorings, which a program
// carries only when it lists those locales). This package decodes a blob
// from any source and answers questions of the decoded Data; it embeds
// nothing.
//
// # Where a program's data comes from
//
// A program carries the data of the locales its nomi.toml lists
// (`[i18n].locales`), cut down to the parts it reaches, in its image
// (Select writes that bundle). A built executable decodes the bundle from its
// image; `nomi run`, `nomi test` and the REPL build the same bundle from the
// data internal/cldrfull compiles into `nomi`. A machine hands its Data to the
// std adapters through the frames it runs on (WithData, From), so the
// runner, which links no cldrfull, never carries data a program did not ask
// for.
//
// # Format
//
// Text, one record per line, fields separated by tabs:
//
//	nomi-cldr	6                         format version
//	cldr	48.2                              CLDR release
//	parent	<tag>	<parent tag>             CLDR parentLocales ("und" is root)
//	plural-parent	<tag>	<parent tag>     parentLocales for the plurals component
//	nonlikely-script-parent	<tag>          a tag whose script is not the language's likely one has this parent
//	cardinal	<tag,tag,...>	<category> <rule>	...   cardinal rules, shared by the listed locales
//	ordinal	<tag,tag,...>	<category> <rule>	...    ordinal rules
//	digits	<system>	<ten digits>       a numeric numbering system's digits, zero first
//	currencies	<code,code,...>          every ISO 4217 code CLDR knows, current or historical
//	currency-digits	<n>	<code,code,...>  the codes whose minor digits are n, not 2
//	available	<tag,tag,...>            the locales a program lists (Locale.available)
//	locale	<tag>                           opens a locale's section; the records below are its own
//	numbering	<default system>	<native system>
//	min-grouping	<n>                     CLDR's minimumGroupingDigits
//	symbols	<system>	<decimal>	<group>	<percent>	<plus>	<minus>	<infinity>	<nan>
//	decimal	<system>	<pattern>          the standard decimal pattern
//	percent	<system>	<pattern>          the standard percent pattern
//	list	<type>	<start>	<middle>	<end>	<two>   a list pattern (standard, or-short, unit-narrow, ...)
//	genders	<gender,gender,...>        the grammatical genders of the language's nouns
//	cases	<case,case,...>            the grammatical cases of the language's nouns
//	currency-symbol	<code>	<symbol>        a currency's symbol (the code when none)
//	currency-narrow	<code>	<symbol>        its narrow symbol (the symbol when none)
//	currency-names	<code>	<zero>	<one>	<two>	<few>	<many>	<other>   its name in each plural form
//	currency-pattern	<system>	<standard>	<alphaNextToNumber>   the currency patterns
//	currency-unit	<zero>	...	<other>      an amount with its currency's name (`{0} {1}`)
//	months	<context>-<width>	<12 names>   format or stand-alone; abbreviated, wide, narrow
//	days	<context>-<width>	<7 names>     Sunday first; also short
//	day-periods	<context>-<width>	<am>	<pm>
//	eras	<width>	<before>	<after>      abbreviated, wide, narrow
//	date-format	<style>	<pattern>      full, long, medium, short
//	time-format	<style>	<pattern>
//	datetime-format	<style>	<pattern>  how a date ({1}) and a time ({0}) join
//	skeleton	<skeleton>	<pattern>      an available format, or a program's skeleton resolved
//	gmt	<format>	<zero>	<hour format>   the localized GMT format
//	relative	<unit>	<-2>	<-1>	<0>	<1>	<2>   a unit's words for days near now (yesterday)
//	relative-future	<unit>	<zero>	...	<other>   `in {0} days`
//	relative-past	<unit>	<zero>	...	<other>     `{0} days ago`
//	duration-unit	<unit>-<width>	<zero>	...	<other>   `{0} hours`
//	collation	<type>	<n>              a collation type's tailoring: n bytes on the next line
//	collation-default	<type>          the collation type a locale uses when its tag names none
//	collation-parent	<tag>	<parent>   the collations component's parentLocales
//	collation-root	<n>                 the root collation table: n bytes on the next line
//	remote	<tag,tag,...>             locales whose numbers and lists are in locale files (store.go)
//	source	<tag>                    the program's source locale, which a locale it does not carry formats as
//	build	<id>                       the build a locale file must name to be loaded
//
// A rule set lists every category but `other`, which is what matches when
// no rule does. A locale section may hold only the records its CLDR parents
// do not already give it; Resolve looks a record up along the locale's chain
// and then in und, which always holds every record.
//
// # A locale the program does not carry
//
// A program that lists its locales carries their sections and und's, and
// its bundle names its source locale (`[i18n].source_locale`) when it lists
// that too. A locale whose chain reaches none of the carried sections
// (`fr` in a program that lists `en` and `de`, or a browser build's `pl`
// before its file is loaded) is looked up in the source locale's section
// before und's, so it formats as the source language rather than as root's
// bare patterns (`2026 M10 20`). A chain that reaches a carried locale never
// reads the source's records, and data with no source record (every
// locale's, or a program that lists none) is looked up as before.
package cldrdata

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// FormatVersion is the blob format this package reads.
const FormatVersion = "7"

// Record keys of a locale section's single records. The others are keyed by
// kind and their first field ("symbols\tlatn", "list\tor-short").
const (
	KeyNumbering   = "numbering"
	KeyMinGrouping = "min-grouping"
	// KeyGenders and KeyCases are a language's grammatical genders and
	// cases (CLDR's grammaticalFeatures for nouns), comma-separated in
	// CLDR's order.
	KeyGenders = "genders"
	KeyCases   = "cases"
	// KeyGMT is a locale's localized GMT format: CLDR's gmtFormat,
	// gmtZeroFormat and hourFormat.
	KeyGMT = "gmt"
	// KeyCollation is a locale's tailoring of a collation type
	// (`collation\tstandard`), and KeyCollationDefault the type its
	// collator uses when the locale names none.
	KeyCollation        = "collation"
	KeyCollationDefault = "collation-default"
)

// DefaultCurrencyDigits are the minor digits of a currency CLDR's
// fractions do not list.
const DefaultCurrencyDigits = 2

// Data is decoded CLDR data: the root records every locale shares, and the
// sections of the locales it carries.
type Data struct {
	// Version is the CLDR release the data was generated from.
	Version string
	// NonlikelyScriptParent is the parent of a language-script tag whose
	// script is not the language's likely script ("und": root).
	NonlikelyScriptParent string

	parents       map[string]string
	pluralParents map[string]string
	cardinal      map[string]*RuleSet
	ordinal       map[string]*RuleSet
	// cardinalSets and ordinalSets are the rule-set records as written, for
	// Select to write them again.
	cardinalSets []setRecord
	ordinalSets  []setRecord
	digits       map[string][]string
	// currencies are the ISO 4217 codes CLDR knows, with their minor digits.
	currencies   map[string]int
	locales      map[string]map[string]string
	available    []string
	hasAvailable bool
	// remote and build are a browser build's split (store.go), and
	// pending the remote locales no locale file has been decoded over the
	// data for yet.
	remote  []string
	build   string
	pending map[string]bool
	// source is the program's source locale, "" when the data names none.
	source string
	// collationRoot is the root collation's table (internal/collation's
	// encoding), and collationParents the collations component's parent
	// locales.
	collationRoot    string
	collationParents map[string]string
	// memo holds what readers build from the data once (a decoded
	// collator), keyed as they choose.
	memo sync.Map
}

type setRecord struct {
	tags  []string
	rules string
}

func newData() *Data {
	return &Data{
		parents:       map[string]string{},
		pluralParents: map[string]string{},
		cardinal:      map[string]*RuleSet{},
		ordinal:       map[string]*RuleSet{},
		digits:        map[string][]string{},
		currencies:    map[string]int{},
		locales:       map[string]map[string]string{},

		collationParents: map[string]string{},
	}
}

// CollationRoot is the root collation table, empty when the data carries
// none.
func (d *Data) CollationRoot() []byte { return []byte(d.collationRoot) }

// CollationParent is tag's parent for collation, when the collations
// component of CLDR's parentLocales names one.
func (d *Data) CollationParent(tag string) (string, bool) {
	p, ok := d.collationParents[tag]
	return p, ok
}

// Memo is what build answers for key, built once per Data: a reader's
// cache of something it makes from the data, such as a decoded collation
// table. A Data a locale file was decoded over (With) starts with none.
func (d *Data) Memo(key any, build func() any) any {
	if v, ok := d.memo.Load(key); ok {
		return v
	}
	v, _ := d.memo.LoadOrStore(key, build())
	return v
}

// Empty is data with nothing in it: no parents, no plural rules (every
// number is `other`) and no locales.
func Empty() *Data { return newData() }

// Decode reads blobs, each a whole blob with its own header, into one Data.
// A later blob's record replaces an earlier one's.
func Decode(blobs ...[]byte) (*Data, error) {
	d := newData()
	for _, blob := range blobs {
		if err := d.decode(blob); err != nil {
			return nil, err
		}
	}
	if d.Version == "" {
		return nil, fmt.Errorf("the data names no CLDR release")
	}
	return d, nil
}

// kindSpec is one locale record kind: how many fields it has after its
// kind, how many of them (from the first) key it beside the kind, and the
// part it belongs to.
type kindSpec struct {
	fields, keys int
	part         Parts
}

// localeKinds are the records a locale section holds.
var localeKinds = map[string]kindSpec{
	KeyNumbering: {2, 0, PartNumbers}, KeyMinGrouping: {1, 0, PartNumbers},
	"symbols": {8, 1, PartNumbers}, "decimal": {2, 1, PartNumbers}, "percent": {2, 1, PartNumbers},
	"list":     {5, 1, PartLists},
	KeyGenders: {1, 0, PartGrammar}, KeyCases: {1, 0, PartGrammar},
	"currency-symbol": {2, 1, PartCurrencies}, "currency-narrow": {2, 1, PartCurrencies},
	"currency-names": {7, 1, PartCurrencyNames}, "currency-pattern": {5, 1, PartCurrencies},
	"currency-unit": {6, 0, PartCurrencyNames},
	"months":        {13, 1, PartDates}, "days": {8, 1, PartDates}, "day-periods": {3, 1, PartDates},
	"eras": {3, 1, PartDates}, "date-format": {2, 1, PartDates}, "time-format": {2, 1, PartDates},
	"datetime-format": {2, 1, PartDates}, KeyGMT: {3, 0, PartDates},
	"skeleton": {2, 1, PartSkeletons},
	"relative": {6, 1, PartRelative}, "relative-future": {7, 1, PartRelative}, "relative-past": {7, 1, PartRelative},
	"duration-unit": {7, 1, PartUnits},
	KeyCollation:    {2, 1, PartCollation}, KeyCollationDefault: {1, 0, PartCollation},
}

// binaryKinds are the records whose last field is a length, the bytes it
// counts following on the next line (a collation table).
var binaryKinds = map[string]bool{"collation-root": true, KeyCollation: true}

// RecordKey is the key a locale record of kind is found by: the kind and
// its key fields, tab-separated ("symbols\tlatn", "months\tformat-wide",
// "currency-name\tEUR\tone").
func RecordKey(kind string, keys ...string) string {
	return strings.Join(append([]string{kind}, keys...), "\t")
}

func (d *Data) decode(blob []byte) error {
	rest := string(blob)
	header, rest, _ := strings.Cut(rest, "\n")
	if header != "nomi-cldr\t"+FormatVersion {
		return fmt.Errorf("not a version %s CLDR blob", FormatVersion)
	}
	var section map[string]string
	// ownRemote is whether this blob names the remote locales: an image's
	// bundle, whose sections of them are not their files'.
	ownRemote := false
	for n := 2; rest != ""; n++ {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		fields := strings.Split(line, "\t")
		bad := func(err string) error { return fmt.Errorf("CLDR blob, line %d: %s", n, err) }
		if binaryKinds[fields[0]] {
			// The last field is the length of the bytes on the lines after.
			size, err := strconv.Atoi(fields[len(fields)-1])
			if err != nil || size < 0 || size+1 > len(rest) || rest[size] != '\n' {
				return bad("bad length of " + fields[0])
			}
			fields[len(fields)-1] = rest[:size]
			n += strings.Count(rest[:size], "\n")
			rest = rest[size+1:]
		}
		if spec, ok := localeKinds[fields[0]]; ok {
			if section == nil {
				return bad(fields[0] + " outside a locale section")
			}
			if len(fields) != spec.fields+1 {
				return bad(fmt.Sprintf("%s takes %d fields", fields[0], spec.fields))
			}
			key := strings.Join(fields[:spec.keys+1], "\t")
			section[key] = strings.Join(fields[spec.keys+1:], "\t")
			continue
		}
		switch fields[0] {
		case "cldr":
			if len(fields) != 2 {
				return bad("cldr takes one field")
			}
			if d.Version != "" && d.Version != fields[1] {
				return bad(fmt.Sprintf("CLDR %s, beside CLDR %s", fields[1], d.Version))
			}
			d.Version = fields[1]
		case "parent", "plural-parent":
			if len(fields) != 3 {
				return bad(fields[0] + " takes two fields")
			}
			table := d.parents
			if fields[0] == "plural-parent" {
				table = d.pluralParents
			}
			table[fields[1]] = fields[2]
		case "nonlikely-script-parent":
			if len(fields) != 2 {
				return bad("nonlikely-script-parent takes one field")
			}
			d.NonlikelyScriptParent = fields[1]
		case "cardinal", "ordinal":
			if len(fields) < 2 {
				return bad(fields[0] + " names no locale")
			}
			var cats []Category
			var rules []*Rule
			for _, f := range fields[2:] {
				name, text, _ := strings.Cut(f, " ")
				c, ok := CategoryNamed(name)
				if !ok || c == Other {
					return bad(fmt.Sprintf("bad category %q", name))
				}
				rule, err := ParseRule(text)
				if err != nil {
					return bad(err.Error())
				}
				cats = append(cats, c)
				rules = append(rules, rule)
			}
			set := NewRuleSet(cats, rules)
			table, records := d.cardinal, &d.cardinalSets
			if fields[0] == "ordinal" {
				table, records = d.ordinal, &d.ordinalSets
			}
			tags := strings.Split(fields[1], ",")
			for _, tag := range tags {
				table[tag] = set
			}
			*records = append(*records, setRecord{tags: tags, rules: strings.Join(fields[2:], "\t")})
		case "digits":
			if len(fields) != 3 {
				return bad("digits takes two fields")
			}
			ds := strings.Split(fields[2], "")
			if len(ds) != 10 {
				return bad(fmt.Sprintf("%s has %d digits", fields[1], len(ds)))
			}
			d.digits[fields[1]] = ds
		case "currencies":
			if len(fields) != 2 {
				return bad("currencies takes one field")
			}
			for _, code := range strings.Split(fields[1], ",") {
				if _, ok := d.currencies[code]; !ok {
					d.currencies[code] = DefaultCurrencyDigits
				}
			}
		case "currency-digits":
			if len(fields) != 3 {
				return bad("currency-digits takes two fields")
			}
			n, err := strconv.Atoi(fields[1])
			if err != nil || n < 0 {
				return bad(fmt.Sprintf("bad digits %q", fields[1]))
			}
			for _, code := range strings.Split(fields[2], ",") {
				d.currencies[code] = n
			}
		case "available":
			if len(fields) != 2 {
				return bad("available takes one field")
			}
			d.available, d.hasAvailable = nil, true
			if fields[1] != "" {
				d.available = strings.Split(fields[1], ",")
			}
		case "collation-root":
			if len(fields) != 2 {
				return bad("collation-root takes one field")
			}
			d.collationRoot = fields[1]
		case "collation-parent":
			if len(fields) != 3 {
				return bad("collation-parent takes two fields")
			}
			d.collationParents[fields[1]] = fields[2]
		case "remote", "build":
			if err := d.decodeSplit(fields); err != nil {
				return bad(err.Error())
			}
			if fields[0] == "remote" {
				ownRemote = true
			}
		case "source":
			if len(fields) != 2 || fields[1] == "" {
				return bad("source takes one field")
			}
			d.source = fields[1]
		case "locale":
			if len(fields) != 2 || fields[1] == "" {
				return bad("locale takes one field")
			}
			if !ownRemote && d.pending[fields[1]] {
				// A locale file's section: the locale is loaded.
				delete(d.pending, fields[1])
			}
			section = d.locales[fields[1]]
			if section == nil {
				section = map[string]string{}
				d.locales[fields[1]] = section
			}
		default:
			return bad(fmt.Sprintf("unknown record %q", fields[0]))
		}
	}
	return nil
}

// Parent is CLDR's explicit parent of tag, if parentLocales names one.
func (d *Data) Parent(tag string) (string, bool) {
	p, ok := d.parents[tag]
	return p, ok
}

// PluralParent is the parent the plurals component gives tag, if any.
func (d *Data) PluralParent(tag string) (string, bool) {
	p, ok := d.pluralParents[tag]
	return p, ok
}

// Cardinal is the cardinal rule set CLDR keys by tag exactly.
func (d *Data) Cardinal(tag string) (*RuleSet, bool) {
	rs, ok := d.cardinal[tag]
	return rs, ok
}

// Ordinal is the ordinal rule set CLDR keys by tag exactly.
func (d *Data) Ordinal(tag string) (*RuleSet, bool) {
	rs, ok := d.ordinal[tag]
	return rs, ok
}

// CardinalTags lists every tag with cardinal rules, sorted.
func (d *Data) CardinalTags() []string { return sortedKeys(d.cardinal) }

// OrdinalTags lists every tag with ordinal rules, sorted.
func (d *Data) OrdinalTags() []string { return sortedKeys(d.ordinal) }

// CurrencyDigits are the minor digits of the ISO 4217 currency code, and
// whether CLDR knows the code.
func (d *Data) CurrencyDigits(code string) (int, bool) {
	n, ok := d.currencies[code]
	return n, ok
}

// Currencies are every ISO 4217 code the data knows, sorted.
func (d *Data) Currencies() []string { return sortedKeys(d.currencies) }

// Digits are a numeric numbering system's ten digits, zero first.
func (d *Data) Digits(system string) ([]string, bool) {
	ds, ok := d.digits[system]
	return ds, ok
}

// Locales lists every locale with a section, sorted.
func (d *Data) Locales() []string { return sortedKeys(d.locales) }

// HasLocale reports whether tag has a section.
func (d *Data) HasLocale(tag string) bool {
	_, ok := d.locales[tag]
	return ok
}

// Available is the locales the program lists, in its order, and false when
// the data does not say (no `available` record).
func (d *Data) Available() ([]string, bool) {
	return append([]string(nil), d.available...), d.hasAvailable
}

// Grammar is the grammatical genders and cases of the first locale on
// chain whose section names them, else und's, which names none: what a
// noun of the language may be and the forms it may take. chain is as
// Resolve's.
func (d *Data) Grammar(chain []string) (genders, cases []string) {
	if v, _, ok := d.Resolve(chain, KeyGenders); ok && v != "" {
		genders = strings.Split(v, ",")
	}
	if v, _, ok := d.Resolve(chain, KeyCases); ok && v != "" {
		cases = strings.Split(v, ",")
	}
	return genders, cases
}

// KeysOn is every record key with prefix that a locale on chain, the
// source locale when Resolve would read it, or und, holds, sorted.
func (d *Data) KeysOn(chain []string, prefix string) []string {
	var out []string
	for _, k := range d.keysOn(d.lookupChain(chain)) {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

// Source is the program's source locale, and whether the data names one.
func (d *Data) Source() (string, bool) { return d.source, d.source != "" }

// SourceFallback is the locale a lookup along chain falls back to before
// und: the source locale, when the data names one and no locale on chain
// is carried (has a section, and is not a browser build's locale whose
// file is not loaded yet).
func (d *Data) SourceFallback(chain []string) (string, bool) {
	if d.source == "" {
		return "", false
	}
	for _, tag := range chain {
		if _, ok := d.locales[tag]; ok && !d.pending[tag] {
			return "", false
		}
	}
	return d.source, true
}

// lookupChain is chain, then the source locale when the lookup falls back
// to it (SourceFallback).
func (d *Data) lookupChain(chain []string) []string {
	if src, ok := d.SourceFallback(chain); ok {
		return append(append([]string(nil), chain...), src)
	}
	return chain
}

// Resolve is the record key of the first locale on chain whose section has
// it, then the source locale's when chain reaches no carried locale
// (SourceFallback), else und's, and the locale that gave it. chain is a
// locale's fallbacks, nearest first; locales without a section are skipped.
func (d *Data) Resolve(chain []string, key string) (value, from string, ok bool) {
	for _, tag := range d.lookupChain(chain) {
		if v, ok := d.locales[tag][key]; ok {
			return v, tag, true
		}
	}
	v, ok := d.locales["und"][key]
	return v, "und", ok
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
