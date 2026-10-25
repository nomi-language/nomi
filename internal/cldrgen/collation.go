package cldrgen

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/collation"
	"github.com/nomi-language/nomi/internal/collation/collgen"
)

// CollationTypes are the CLDR collation types cldrgen compiles. The
// private ones are imported by the others (zh's pinyin and stroke import
// private-pinyin's tone order; ja's standard private-kana). Left out:
// search and searchjl (for matching, not sorting), unihan and
// private-unihan (radical-stroke order, which needs the Unihan index),
// zhuyin (287 KB, and ICU ships a shorter list than CLDR's), eor and emoji
// (root's alternates), and the `alt` variants.
var CollationTypes = []string{
	"standard", "phonebook", "traditional", "pinyin", "stroke",
	"dictionary", "phonetic", "compat", "digits-after",
	"private-pinyin", "private-kana",
}

// CompilesCollationType reports whether cldrgen compiles collation type t.
func CompilesCollationType(t string) bool { return slices.Contains(CollationTypes, t) }

// CollationRules is data/collation/rules.txt, read.
type CollationRules struct {
	// Defaults is each locale's default type where it is not `standard`.
	Defaults map[string]string
	// Rules is each locale's rules by type; a locale is CLDR's file name
	// (zh_Hant).
	Rules map[string]map[string]string
}

// ReadCollationRules reads data/collation/rules.txt from dir.
func ReadCollationRules(dir string) (*CollationRules, error) {
	f, err := os.Open(filepath.Join(dir, "collation", "rules.txt"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cr := &CollationRules{Defaults: map[string]string{}, Rules: map[string]map[string]string{}}
	var locale, typ string
	var body strings.Builder
	flush := func() {
		if locale != "" {
			if cr.Rules[locale] == nil {
				cr.Rules[locale] = map[string]string{}
			}
			cr.Rules[locale][typ] = body.String()
		}
		body.Reset()
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "#") && locale == "":
		case strings.HasPrefix(line, "@default "):
			fs := strings.Fields(line)
			if len(fs) != 3 {
				return nil, fmt.Errorf("rules.txt: bad %q", line)
			}
			cr.Defaults[fs[1]] = fs[2]
		case strings.HasPrefix(line, "@rules "):
			flush()
			fs := strings.Fields(line)
			if len(fs) != 3 {
				return nil, fmt.Errorf("rules.txt: bad %q", line)
			}
			locale, typ = fs[1], fs[2]
		default:
			body.WriteString(line + "\n")
		}
	}
	flush()
	return cr, sc.Err()
}

// ReadFractional reads data/collation/FractionalUCA_SHORT.txt from dir.
func ReadFractional(dir string) (*collgen.Fractional, error) {
	f, err := os.Open(filepath.Join(dir, "collation", "FractionalUCA_SHORT.txt"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return collgen.ReadFractional(f)
}

// Collations is the compiled collation data: the root, and each locale's
// tailoring of each type, by BCP 47 tag.
type Collations struct {
	Root []byte
	// Tailorings maps a tag to its types' encoded tailorings.
	Tailorings map[string]map[string][]byte
	// Defaults is each tag's default type where it is not `standard`.
	Defaults map[string]string
}

// GenerateCollations compiles every locale's collation types.
func GenerateCollations(dir string) (*Collations, error) {
	fr, err := ReadFractional(dir)
	if err != nil {
		return nil, err
	}
	rules, err := ReadCollationRules(dir)
	if err != nil {
		return nil, err
	}
	out := &Collations{
		Root:       collation.EncodeRoot(fr.Root()),
		Tailorings: map[string]map[string][]byte{},
		Defaults:   map[string]string{},
	}
	load := func(locale, typ string) (string, bool) {
		r, ok := rules.Rules[locale][typ]
		return r, ok
	}
	locales := make([]string, 0, len(rules.Rules))
	for l := range rules.Rules {
		locales = append(locales, l)
	}
	sort.Strings(locales)
	for _, l := range locales {
		tag := cldrTag(l)
		for typ, r := range rules.Rules[l] {
			if strings.HasPrefix(typ, "private-") {
				continue
			}
			t, err := collgen.Build(fr, r, load)
			if err != nil {
				return nil, fmt.Errorf("collation %s %s: %w", l, typ, err)
			}
			if out.Tailorings[tag] == nil {
				out.Tailorings[tag] = map[string][]byte{}
			}
			out.Tailorings[tag][typ] = collation.EncodeTailoring(t)
		}
	}
	for l, d := range rules.Defaults {
		if d != "standard" {
			out.Defaults[cldrTag(l)] = d
		}
	}
	return out, nil
}

// cldrTag is the BCP 47 tag of a CLDR file name: zh_Hant is zh-Hant, root
// is und.
func cldrTag(name string) string {
	if name == "root" {
		return "und"
	}
	return strings.ReplaceAll(name, "_", "-")
}
