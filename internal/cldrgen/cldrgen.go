// Package cldrgen generates Nomi's locale data from one pinned CLDR release.
//
// The source is cldr-json at Release, vendored under data/ with the Unicode
// license it is published under:
//
//   - from cldr-core, unchanged: supplemental/plurals.json, ordinals.json,
//     parentLocales.json, numberingSystems.json and grammaticalFeatures.json
//     (each language's grammatical genders and cases);
//   - from cldr-core, cut to what is read: currencyData.json (each
//     currency's minor digits, the codes regions have used and those in use
//     today);
//   - numbers.json and listPatterns.json, every locale's
//     main/<tag>/numbers.json (cldr-numbers-full) and listPatterns.json
//     (cldr-misc-full) gathered into one file each, keeping only the keys
//     this package reads, with their values unchanged;
//   - currencies.json (cldr-numbers-full: symbols, and the plural names of
//     the currencies in use today), ca-gregorian.json, dateFields.json and
//     timeZoneNames.json (cldr-dates-full: Gregorian names and patterns,
//     available formats, relative-time forms, the GMT format) and
//     units.json (cldr-units-full: the duration units), gathered the same
//     way but holding each locale's own values only, those its CLDR parent
//     does not already give it (sources.go reads them back resolved along
//     the same chain).
//
// ./extract writes them from the unpacked npm packages. Building Nomi needs
// no network; upgrading CLDR is replacing those files with a newer
// release's, changing Release, and running `go generate
// ./internal/cldrfull`.
//
// It writes internal/cldrfull's root.cldr (plural rules, parent locales,
// numbering-system digits, and ISO 4217 codes with their minor digits) and
// locales.cldr (every locale's number symbols, decimal, percent and
// currency patterns, list patterns, grammatical genders and cases, currency
// symbols and names, calendar names and patterns, available formats,
// relative-time forms and duration units), in the format
// internal/cldrdata's comment gives.
//
// The collation data comes from CLDR's source release instead, as
// cldr-json has none: data/collation holds common/uca/FractionalUCA_SHORT.txt
// unchanged and rules.txt, the collation/*.xml rules of the types
// CollationTypes names (`./extract collation`). collation.go compiles them
// with internal/collation/collgen into the root collation (in root.cldr), each
// locale's tailorings (in its section of locales.cldr) and the large Chinese
// and Korean tailorings (collation-cjk.cldr).
//
// The plural rules are kept as CLDR writes them,
// without samples; every one must parse with cldrdata.ParseRule, and every
// number pattern with cldrdata.ParsePattern. The samples stay in the
// vendored files, where the tests check every one against the generated
// rules.
package cldrgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
)

// Release is the pinned CLDR release, and SourceURL where its files were
// taken from.
const (
	Release   = "48.2"
	SourceURL = "https://github.com/unicode-org/cldr-json/tree/48.2.0/cldr-json/cldr-core/supplemental"
)

// RootFile is the generated root blob, relative to internal/cldrfull.
const RootFile = "root.cldr"

// DataDir is the vendored data's directory relative to internal/cldrfull,
// which `go generate` runs the generator from.
const DataDir = "../cldrgen/data"

// Rule is one category's rule as CLDR's JSON writes it.
type Rule struct {
	Category cldrdata.Category
	// Condition is the rule without samples.
	Condition string
	// Integer and Decimal are the `@integer` and `@decimal` samples, as
	// written (ranges `a~b`, and `…`).
	Integer []string
	Decimal []string
}

// Source is the vendored data, read.
type Source struct {
	Version       string
	Cardinal      map[string][]Rule
	Ordinal       map[string][]Rule
	Parents       map[string]string
	PluralParents map[string]string
	// CollationParents is the collations component's parentLocales.
	CollationParents map[string]string
	// NonlikelyScriptParent is `_localeRules.parentLocale.nonlikelyScript`.
	NonlikelyScriptParent string
}

// Read reads the vendored data from dir.
func Read(dir string) (*Source, error) {
	src := &Source{}
	var plurals, ordinals struct {
		Supplemental struct {
			Version struct {
				CLDR string `json:"_cldrVersion"`
			} `json:"version"`
			Cardinal map[string]map[string]string `json:"plurals-type-cardinal"`
			Ordinal  map[string]map[string]string `json:"plurals-type-ordinal"`
		} `json:"supplemental"`
	}
	var parents struct {
		Supplemental struct {
			Version struct {
				CLDR string `json:"_cldrVersion"`
			} `json:"version"`
			ParentLocales struct {
				ParentLocale map[string]string `json:"parentLocale"`
				Plurals      map[string]string `json:"plurals"`
				Collations   map[string]string `json:"collations"`
				Rules        struct {
					ParentLocale map[string]string `json:"parentLocale"`
				} `json:"_localeRules"`
			} `json:"parentLocales"`
		} `json:"supplemental"`
	}
	for _, f := range []struct {
		name string
		into any
	}{{"plurals.json", &plurals}, {"ordinals.json", &ordinals}, {"parentLocales.json", &parents}} {
		b, err := os.ReadFile(filepath.Join(dir, f.name))
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, f.into); err != nil {
			return nil, fmt.Errorf("%s: %w", f.name, err)
		}
	}
	major, _, _ := strings.Cut(Release, ".")
	for name, v := range map[string]string{
		"plurals.json":       plurals.Supplemental.Version.CLDR,
		"ordinals.json":      ordinals.Supplemental.Version.CLDR,
		"parentLocales.json": parents.Supplemental.Version.CLDR,
	} {
		if v != major && v != Release {
			return nil, fmt.Errorf("%s is CLDR %s, not the pinned %s", name, v, Release)
		}
	}
	src.Version = Release
	var err error
	if src.Cardinal, err = readRules(plurals.Supplemental.Cardinal); err != nil {
		return nil, fmt.Errorf("plurals.json: %w", err)
	}
	if src.Ordinal, err = readRules(ordinals.Supplemental.Ordinal); err != nil {
		return nil, fmt.Errorf("ordinals.json: %w", err)
	}
	pl := parents.Supplemental.ParentLocales
	src.Parents = pl.ParentLocale
	src.PluralParents = pl.Plurals
	src.CollationParents = pl.Collations
	src.NonlikelyScriptParent = pl.Rules.ParentLocale["nonlikelyScript"]
	if src.NonlikelyScriptParent == "root" {
		src.NonlikelyScriptParent = "und"
	}
	return src, nil
}

func readRules(byLocale map[string]map[string]string) (map[string][]Rule, error) {
	out := map[string][]Rule{}
	for locale, rules := range byLocale {
		for key, text := range rules {
			name, ok := strings.CutPrefix(key, "pluralRule-count-")
			if !ok {
				return nil, fmt.Errorf("%s: unknown key %q", locale, key)
			}
			c, ok := cldrdata.CategoryNamed(name)
			if !ok {
				return nil, fmt.Errorf("%s: unknown category %q", locale, name)
			}
			r := Rule{Category: c}
			cond, samples, _ := strings.Cut(text, "@")
			r.Condition = strings.Join(strings.Fields(cond), " ")
			if samples != "" {
				samples = "@" + samples
			}
			for _, part := range strings.Split(samples, "@")[1:] {
				kind, list, _ := strings.Cut(part, " ")
				var vals []string
				for _, v := range strings.Split(list, ",") {
					if v = strings.TrimSpace(v); v != "" {
						vals = append(vals, v)
					}
				}
				switch kind {
				case "integer":
					r.Integer = vals
				case "decimal":
					r.Decimal = vals
				default:
					return nil, fmt.Errorf("%s %s: unknown sample kind %q", locale, name, kind)
				}
			}
			if c == cldrdata.Other && r.Condition != "" {
				return nil, fmt.Errorf("%s: other has a condition", locale)
			}
			if c != cldrdata.Other {
				if _, err := cldrdata.ParseRule(r.Condition); err != nil {
					return nil, fmt.Errorf("%s %s: %w", locale, name, err)
				}
			}
			out[locale] = append(out[locale], r)
		}
		sort.Slice(out[locale], func(i, j int) bool { return out[locale][i].Category < out[locale][j].Category })
	}
	return out, nil
}

// GenerateAll renders every blob from the vendored data in dir, by file
// name: the root blob with the root collation, then the locales blob,
// whose sections leave out what each locale's parents give it along the
// chain stdlocale.Fallbacks walks over the root blob's parents, with each
// locale's collation tailorings under LargeCollation bytes, and the CJK
// blob with the larger ones.
func GenerateAll(dir string) (map[string][]byte, error) {
	root, err := Generate(dir)
	if err != nil {
		return nil, err
	}
	coll, err := GenerateCollations(dir)
	if err != nil {
		return nil, err
	}
	root = fmt.Appendf(root, "collation-root\t%d\n%s\n", len(coll.Root), coll.Root)
	d, err := cldrdata.Decode(root)
	if err != nil {
		return nil, err
	}
	small, large := map[string]map[string]string{}, map[string]map[string]string{}
	for tag, types := range coll.Tailorings {
		for typ, b := range types {
			into := small
			if len(b) > LargeCollation {
				into = large
			}
			if into[tag] == nil {
				into[tag] = map[string]string{}
			}
			into[tag][cldrdata.RecordKey(cldrdata.KeyCollation, typ)] = string(b)
		}
	}
	for tag, typ := range coll.Defaults {
		if small[tag] == nil {
			small[tag] = map[string]string{}
		}
		small[tag][cldrdata.KeyCollationDefault] = typ
	}
	locales, err := GenerateLocales(dir, func(tag string) []string { return stdlocale.Fallbacks(d, tag) }, small)
	if err != nil {
		return nil, err
	}
	var cjk strings.Builder
	fmt.Fprintf(&cjk, "nomi-cldr\t%s\ncldr\t%s\n", cldrdata.FormatVersion, Release)
	for _, tag := range sortedKeys(large) {
		fmt.Fprintf(&cjk, "locale\t%s\n", tag)
		writeRecords(&cjk, large[tag])
	}
	return map[string][]byte{RootFile: root, LocalesFile: locales, CollationCJKFile: []byte(cjk.String())}, nil
}

// LargeCollation is the size over which a collation tailoring goes in the
// CJK blob, which a program carries only for the locales it lists: Chinese
// (pinyin and stroke) and Korean, 160 KB to 250 KB each.
const LargeCollation = 64 << 10

// CollationCJKFile is the generated blob of the large tailorings,
// relative to internal/cldrfull.
const CollationCJKFile = "collation-cjk.cldr"

// writeRecords writes a section's records sorted by key, a collation
// table with its length.
func writeRecords(b *strings.Builder, recs map[string]string) {
	for _, key := range sortedKeys(recs) {
		if kind, _, _ := strings.Cut(key, "\t"); kind == cldrdata.KeyCollation {
			fmt.Fprintf(b, "%s\t%d\n%s\n", key, len(recs[key]), recs[key])
			continue
		}
		fmt.Fprintf(b, "%s\t%s\n", key, recs[key])
	}
}

// Generate renders the root blob from the vendored data in dir.
func Generate(dir string) ([]byte, error) {
	src, err := Read(dir)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "nomi-cldr\t%s\n", cldrdata.FormatVersion)
	fmt.Fprintf(&b, "cldr\t%s\n", src.Version)
	for _, k := range sortedKeys(src.Parents) {
		fmt.Fprintf(&b, "parent\t%s\t%s\n", k, src.Parents[k])
	}
	for _, k := range sortedKeys(src.PluralParents) {
		fmt.Fprintf(&b, "plural-parent\t%s\t%s\n", k, src.PluralParents[k])
	}
	if src.NonlikelyScriptParent != "" {
		fmt.Fprintf(&b, "nonlikely-script-parent\t%s\n", src.NonlikelyScriptParent)
	}
	for _, k := range sortedKeys(src.CollationParents) {
		fmt.Fprintf(&b, "collation-parent\t%s\t%s\n", k, src.CollationParents[k])
	}
	writeSets(&b, "cardinal", src.Cardinal)
	writeSets(&b, "ordinal", src.Ordinal)
	digits, err := readDigits(dir)
	if err != nil {
		return nil, err
	}
	for _, sys := range sortedKeys(digits) {
		fmt.Fprintf(&b, "digits\t%s\t%s\n", sys, digits[sys])
	}
	codes, err := ReadCurrencyCodes(dir)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "currencies\t%s\n", strings.Join(sortedKeys(codes), ","))
	byDigits := map[int][]string{}
	for _, code := range sortedKeys(codes) {
		if n := codes[code]; n != cldrdata.DefaultCurrencyDigits {
			byDigits[n] = append(byDigits[n], code)
		}
	}
	for n := 0; n <= 18; n++ {
		if len(byDigits[n]) > 0 {
			fmt.Fprintf(&b, "currency-digits\t%d\t%s\n", n, strings.Join(byDigits[n], ","))
		}
	}
	return []byte(b.String()), nil
}

// writeSets writes one line per distinct rule set, naming every locale that
// shares it, in the order of each set's first locale.
func writeSets(b *strings.Builder, kind string, byLocale map[string][]Rule) {
	var order []string
	sharing := map[string][]string{}
	for _, locale := range sortedKeys(byLocale) {
		var fields []string
		for _, r := range byLocale[locale] {
			if r.Category != cldrdata.Other {
				fields = append(fields, r.Category.String()+" "+r.Condition)
			}
		}
		key := strings.Join(fields, "\t")
		if _, seen := sharing[key]; !seen {
			order = append(order, key)
		}
		sharing[key] = append(sharing[key], locale)
	}
	for _, key := range order {
		fmt.Fprintf(b, "%s\t%s", kind, strings.Join(sharing[key], ","))
		if key != "" {
			b.WriteString("\t" + key)
		}
		b.WriteString("\n")
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
