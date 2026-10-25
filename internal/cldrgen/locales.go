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

// SymbolKeys are the number symbols cldrgen reads from a numbering system's
// symbols, in the order a `symbols` record writes them.
var SymbolKeys = []string{"decimal", "group", "percentSign", "plusSign", "minusSign", "infinity", "nan"}

// ListTypes are the list pattern types cldrgen reads, in the order std's
// `ListStyle` and `ListWidth` select them (style-major).
var ListTypes = []string{
	"standard", "standard-short", "standard-narrow",
	"or", "or-short", "or-narrow",
	"unit", "unit-short", "unit-narrow",
}

// RelativeUnits are the dateFields entries cldrgen reads, in the order std's
// `TimeUnit` declares them.
var RelativeUnits = []string{"second", "minute", "hour", "day", "week", "month", "quarter", "year"}

// DurationUnits are the duration units cldrgen reads, largest first, as
// `Duration.format` writes them.
var DurationUnits = []string{"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}

// LocalesFile is the generated per-locale data, relative to
// internal/cldrfull.
const LocalesFile = "locales.cldr"

// GrammarFile is the vendored grammatical features, cldr-core's
// supplemental/grammaticalFeatures.json unchanged.
const GrammarFile = "grammaticalFeatures.json"

// LocaleSource is the vendored per-locale data, read: each locale's records,
// keyed as cldrdata.Record keys them, fully resolved as cldr-json gives them.
type LocaleSource struct {
	// Records maps a locale tag to its records, by record key.
	Records map[string]map[string]string
	// Digits maps each numeric numbering system to its ten digits.
	Digits map[string]string
}

// ReadLocales reads the vendored number and list data from dir.
func ReadLocales(dir string) (*LocaleSource, error) {
	digits, err := readDigits(dir)
	if err != nil {
		return nil, err
	}
	src := &LocaleSource{Records: map[string]map[string]string{}, Digits: digits}
	var numbers struct {
		Main map[string]struct {
			Default     string            `json:"defaultNumberingSystem"`
			MinGrouping string            `json:"minimumGroupingDigits"`
			Other       map[string]string `json:"otherNumberingSystems"`
		} `json:"main"`
	}
	var rawNumbers struct {
		Main map[string]map[string]json.RawMessage `json:"main"`
	}
	if err := readJSON(filepath.Join(dir, "numbers.json"), &numbers); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "numbers.json"), &rawNumbers); err != nil {
		return nil, err
	}
	for tag, n := range numbers.Main {
		recs := map[string]string{}
		src.Records[tag] = recs
		if n.Default == "" {
			return nil, fmt.Errorf("numbers.json: %s has no default numbering system", tag)
		}
		native := n.Other["native"]
		if native == "" {
			native = n.Default
		}
		recs[cldrdata.KeyNumbering] = n.Default + "\t" + native
		if n.MinGrouping == "" {
			return nil, fmt.Errorf("numbers.json: %s has no minimumGroupingDigits", tag)
		}
		recs[cldrdata.KeyMinGrouping] = n.MinGrouping
		for key, raw := range rawNumbers.Main[tag] {
			kind, system, ok := strings.Cut(key, "-numberSystem-")
			if !ok {
				continue
			}
			var m map[string]string
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, fmt.Errorf("numbers.json: %s %s: %w", tag, key, err)
			}
			switch kind {
			case "symbols":
				fields := make([]string, len(SymbolKeys))
				for i, k := range SymbolKeys {
					v, ok := m[k]
					if !ok || v == "" || strings.ContainsAny(v, "\t\n") {
						return nil, fmt.Errorf("numbers.json: %s %s: symbol %s is %q", tag, key, k, v)
					}
					fields[i] = v
				}
				recs["symbols\t"+system] = strings.Join(fields, "\t")
			case "decimalFormats", "percentFormats":
				p := m["standard"]
				if p == "" {
					return nil, fmt.Errorf("numbers.json: %s %s has no standard pattern", tag, key)
				}
				if _, err := cldrdata.ParsePattern(p); err != nil {
					return nil, fmt.Errorf("numbers.json: %s %s: %w", tag, key, err)
				}
				recs[strings.TrimSuffix(kind, "Formats")+"\t"+system] = p
			}
		}
	}
	var lists struct {
		Main map[string]map[string]map[string]string `json:"main"`
	}
	if err := readJSON(filepath.Join(dir, "listPatterns.json"), &lists); err != nil {
		return nil, err
	}
	for tag, pats := range lists.Main {
		recs := src.Records[tag]
		if recs == nil {
			return nil, fmt.Errorf("listPatterns.json: %s has no number data", tag)
		}
		for _, typ := range ListTypes {
			p, ok := pats["listPattern-type-"+typ]
			if !ok {
				return nil, fmt.Errorf("listPatterns.json: %s has no %s pattern", tag, typ)
			}
			fields := []string{p["start"], p["middle"], p["end"], p["2"]}
			for _, f := range fields {
				if !strings.Contains(f, "{0}") || !strings.Contains(f, "{1}") || strings.ContainsAny(f, "\t\n") {
					return nil, fmt.Errorf("listPatterns.json: %s %s: bad pattern %q", tag, typ, f)
				}
			}
			if p["3"] != "" {
				return nil, fmt.Errorf("listPatterns.json: %s %s has a three-item pattern, which cldrdata does not read", tag, typ)
			}
			recs["list\t"+typ] = strings.Join(fields, "\t")
		}
	}
	for tag, recs := range src.Records {
		if _, ok := recs["list\tstandard"]; !ok {
			return nil, fmt.Errorf("numbers.json: %s has no list patterns", tag)
		}
		def, _, _ := strings.Cut(recs[cldrdata.KeyNumbering], "\t")
		if _, ok := recs["symbols\t"+def]; !ok {
			return nil, fmt.Errorf("numbers.json: %s has no symbols for its default system %s", tag, def)
		}
		if _, ok := recs["symbols\tlatn"]; !ok {
			return nil, fmt.Errorf("numbers.json: %s has no latn symbols", tag)
		}
	}
	if src.Records["und"] == nil {
		return nil, fmt.Errorf("the vendored data has no root locale (und)")
	}
	if err := readGrammar(dir, src); err != nil {
		return nil, err
	}
	if err := readCurrencyFormats(src, rawNumbers.Main); err != nil {
		return nil, err
	}
	root, err := Generate(dir)
	if err != nil {
		return nil, err
	}
	d, err := cldrdata.Decode(root)
	if err != nil {
		return nil, err
	}
	chain := func(tag string) []string { return stdlocale.Fallbacks(d, tag) }
	for _, read := range []func(string, *LocaleSource, func(string) []string) error{
		readCurrencies, readGregorian, readRelative, readZoneNames, readUnits,
	} {
		if err := read(dir, src, chain); err != nil {
			return nil, err
		}
	}
	return src, nil
}

// readDigits reads each numeric numbering system's ten digits.
func readDigits(dir string) (map[string]string, error) {
	var ns struct {
		Supplemental struct {
			NumberingSystems map[string]struct {
				Digits string `json:"_digits"`
				Type   string `json:"_type"`
			} `json:"numberingSystems"`
		} `json:"supplemental"`
	}
	if err := readJSON(filepath.Join(dir, "numberingSystems.json"), &ns); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, s := range ns.Supplemental.NumberingSystems {
		if s.Type == "numeric" {
			if n := len([]rune(s.Digits)); n != 10 {
				return nil, fmt.Errorf("numberingSystems.json: %s has %d digits", name, n)
			}
			out[name] = s.Digits
		}
	}
	return out, nil
}

// readGrammar adds each language's grammatical genders and cases, from
// grammaticalFeatures.json's `<language>-targets-nominal` entries, to its
// locale's records (cldrdata.KeyGenders, cldrdata.KeyCases): the values CLDR
// lists for nouns, in its order. A language CLDR gives neither has no
// record, and resolves to und's, which has none.
func readGrammar(dir string, src *LocaleSource) error {
	var g struct {
		Supplemental struct {
			Version struct {
				CLDR string `json:"_cldrVersion"`
			} `json:"version"`
			Data map[string]map[string]json.RawMessage `json:"grammaticalData"`
		} `json:"supplemental"`
	}
	if err := readJSON(filepath.Join(dir, GrammarFile), &g); err != nil {
		return err
	}
	major, _, _ := strings.Cut(Release, ".")
	if v := g.Supplemental.Version.CLDR; v != major && v != Release {
		return fmt.Errorf("%s is CLDR %s, not the pinned %s", GrammarFile, v, Release)
	}
	for key, features := range g.Supplemental.Data {
		lang, ok := strings.CutSuffix(key, "-targets-nominal")
		if !ok {
			continue
		}
		recs := src.Records[lang]
		if recs == nil {
			return fmt.Errorf("%s: %s has no number data to add its grammatical features to", GrammarFile, lang)
		}
		for field, rec := range map[string]string{"grammaticalGender": cldrdata.KeyGenders, "grammaticalCase": cldrdata.KeyCases} {
			raw, ok := features[field]
			if !ok {
				continue
			}
			var values []string
			if err := json.Unmarshal(raw, &values); err != nil {
				return fmt.Errorf("%s: %s %s: %w", GrammarFile, lang, field, err)
			}
			for _, v := range values {
				if v == "" || strings.ContainsAny(v, ",\t\n") {
					return fmt.Errorf("%s: %s %s: bad value %q", GrammarFile, lang, field, v)
				}
			}
			if len(values) > 0 {
				recs[rec] = strings.Join(values, ",")
			}
		}
	}
	return nil
}

func readJSON(path string, into any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, into); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

// GenerateLocales renders the per-locale blob from the vendored data in dir.
// Each locale's section holds only the records its CLDR parent chain does
// not already give it (cldrdata.Data resolves a locale along the same chain),
// so a regional locale is a few lines. The root locale, und, holds every
// record.
//
// collation is each locale's collation records, written as they are: a
// tailoring is its own, and a locale finds its parents' along the
// collation chain. A locale with collation rules and no other data
// (en-US-POSIX, whose tag BCP 47 spells en-US-u-va-posix) gets no section,
// so Locale.available does not grow: its rules are not carried.
func GenerateLocales(dir string, chain func(tag string) []string, collation map[string]map[string]string) ([]byte, error) {
	src, err := ReadLocales(dir)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "nomi-cldr\t%s\n", cldrdata.FormatVersion)
	fmt.Fprintf(&b, "cldr\t%s\n", Release)
	tags := sortedKeys(src.Records)
	// und first: every other section resolves against it.
	sort.SliceStable(tags, func(i, j int) bool { return tags[i] == "und" && tags[j] != "und" })
	for _, tag := range tags {
		recs := src.Records[tag]
		own := map[string]string{}
		for _, key := range sortedKeys(recs) {
			if tag != "und" && inherited(src, chain(tag), key) == recs[key] {
				continue
			}
			own[key] = recs[key]
		}
		for key, v := range collation[tag] {
			own[key] = v
		}
		fmt.Fprintf(&b, "locale\t%s\n", tag)
		writeRecords(&b, own)
	}
	return []byte(b.String()), nil
}

// inherited is the value key resolves to from a locale's parents: the first
// ancestor on chain (the locale itself first, so skipped) with data that has
// the key, else und's.
func inherited(src *LocaleSource, chain []string, key string) string {
	for _, anc := range chain[1:] {
		if recs, ok := src.Records[anc]; ok {
			if v, ok := recs[key]; ok {
				return v
			}
		}
	}
	return src.Records["und"][key]
}
