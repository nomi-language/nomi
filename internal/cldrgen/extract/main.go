// Command extract writes the trimmed CLDR files internal/cldrgen/data holds
// from an unpacked cldr-json release, and with `collation` the collation
// files of data/collation from the unpacked CLDR source release (see
// collation.go).
//
//	go run ./internal/cldrgen/extract <dir>
//	go run ./internal/cldrgen/extract collation <dir>
//
// <dir> holds the npm packages cldr-core, cldr-numbers-full, cldr-misc-full,
// cldr-dates-full and cldr-units-full of the pinned release, each unpacked
// into a directory of its name (cldr-numbers-full/package/main/pl/numbers.json,
// ...). The files are on the npm registry, for example
// https://registry.npmjs.org/cldr-numbers-full/-/cldr-numbers-full-48.2.0.tgz.
//
// It copies cldr-core's numberingSystems.json unchanged and writes its
// currencyData.json cut to each currency's minor digits and the codes
// regions use. It writes numbers.json, listPatterns.json, currencies.json,
// ca-gregorian.json, dateFields.json, timeZoneNames.json and units.json:
// every locale's file of that name gathered into one, keeping only the keys
// cldrgen reads with their values unchanged. Each locale is one line, so a
// CLDR upgrade's diff reads per locale.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrgen"
	"github.com/nomi-language/nomi/internal/stdlocale"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "collation" {
		if err := extractCollation(os.Args[2], filepath.Join("internal", "cldrgen", "data", "collation")); err != nil {
			fmt.Fprintln(os.Stderr, "extract:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./internal/cldrgen/extract <dir of unpacked cldr-json packages>")
		fmt.Fprintln(os.Stderr, "       go run ./internal/cldrgen/extract collation <dir of the unpacked CLDR release>")
		os.Exit(2)
	}
	if err := run(os.Args[1], filepath.Join("internal", "cldrgen", "data")); err != nil {
		fmt.Fprintln(os.Stderr, "extract:", err)
		os.Exit(1)
	}
}

func run(src, out string) error {
	ns, err := os.ReadFile(filepath.Join(src, "cldr-core", "package", "supplemental", "numberingSystems.json"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "numberingSystems.json"), ns, 0o644); err != nil {
		return err
	}
	numbers, err := gather(filepath.Join(src, "cldr-numbers-full", "package", "main"), "numbers.json", "numbers", trimNumbers)
	if err != nil {
		return err
	}
	if err := write(filepath.Join(out, "numbers.json"), "cldr-numbers-full", "numbers", "", numbers); err != nil {
		return err
	}
	lists, err := gather(filepath.Join(src, "cldr-misc-full", "package", "main"), "listPatterns.json", "listPatterns",
		func(v map[string]any) map[string]any { return v })
	if err != nil {
		return err
	}
	if err := write(filepath.Join(out, "listPatterns.json"), "cldr-misc-full", "listPatterns", "", lists); err != nil {
		return err
	}
	current, err := currencyData(src, out)
	if err != nil {
		return err
	}
	// The files below are written as each locale's own values: a value its
	// CLDR parent already gives it is left out (own), as cldrgen would leave
	// it out of the generated data anyway. cldr-json repeats every
	// inherited value in every locale, which for currency names alone is
	// 22 MB.
	root, err := cldrgen.Generate(out)
	if err != nil {
		return err
	}
	d, err := cldrdata.Decode(root)
	if err != nil {
		return err
	}
	chain := func(tag string) []string { return stdlocale.Fallbacks(d, tag) }
	for _, f := range []struct {
		pkg, file string
		path      []string
		trim      func(map[string]any) map[string]any
	}{
		{"cldr-numbers-full", "currencies.json", []string{"numbers", "currencies"}, trimCurrencies(current)},
		{"cldr-dates-full", "ca-gregorian.json", []string{"dates", "calendars", "gregorian"}, trimGregorian},
		{"cldr-dates-full", "dateFields.json", []string{"dates", "fields"}, trimFields},
		{"cldr-dates-full", "timeZoneNames.json", []string{"dates", "timeZoneNames"}, trimZoneNames},
		{"cldr-units-full", "units.json", []string{"units"}, trimUnits},
	} {
		got, err := gatherPath(filepath.Join(src, f.pkg, "package", "main"), f.file, f.path, f.trim)
		if err != nil {
			return err
		}
		if err := write(filepath.Join(out, f.file), f.pkg, strings.TrimSuffix(f.file, ".json"), ", values a locale's parent gives it removed", own(got, chain)); err != nil {
			return err
		}
	}
	return nil
}

// own is every locale's values less those its CLDR parent already gives
// it: the nearest locale on its chain that the data has, else root (und).
// cldr-json writes each locale resolved, so the parent's values are what
// the locale inherits. Root keeps every value.
func own(byLocale map[string]map[string]any, chain func(tag string) []string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for tag, v := range byLocale {
		var parent map[string]any
		if tag != "und" {
			parent = byLocale["und"]
			for _, anc := range chain(tag)[1:] {
				if p, ok := byLocale[anc]; ok {
					parent = p
					break
				}
			}
		}
		out[tag] = diff(v, parent)
	}
	return out
}

// diff is v less the leaves parent holds with the same value.
func diff(v, parent map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		px, has := parent[k]
		if m, ok := x.(map[string]any); ok {
			pm, _ := px.(map[string]any)
			if d := diff(m, pm); len(d) > 0 {
				out[k] = d
			}
			continue
		}
		if has && reflect.DeepEqual(x, px) {
			continue
		}
		out[k] = x
	}
	return out
}

// currencyData writes cldr-core's supplemental currencyData.json with only
// what cldrgen reads: each currency's minor digits (`fractions`), every
// code a region has used, and the codes some region uses today (no `_to`
// date, not marked as no tender). It answers those current codes.
func currencyData(src, out string) (map[string]bool, error) {
	var doc struct {
		Supplemental struct {
			CurrencyData struct {
				Fractions map[string]map[string]string              `json:"fractions"`
				Region    map[string][]map[string]map[string]string `json:"region"`
			} `json:"currencyData"`
		} `json:"supplemental"`
	}
	b, err := os.ReadFile(filepath.Join(src, "cldr-core", "package", "supplemental", "currencyData.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("currencyData.json: %w", err)
	}
	digits := map[string]string{}
	for code, f := range doc.Supplemental.CurrencyData.Fractions {
		digits[code] = f["_digits"]
	}
	codes, current := map[string]bool{}, map[string]bool{}
	for _, uses := range doc.Supplemental.CurrencyData.Region {
		for _, use := range uses {
			for code, dates := range use {
				codes[code] = true
				if dates["_to"] == "" && dates["_tender"] != "false" {
					current[code] = true
				}
			}
		}
	}
	trimmed := map[string]any{
		"_source":      fmt.Sprintf("cldr-core %s.0, supplemental/currencyData.json: the fractions' digits, the codes regions have used, and those in use today; keys cldrgen does not read removed", cldrgen.Release),
		"_cldrVersion": strings.Split(cldrgen.Release, ".")[0],
		"digits":       digits,
		"codes":        sortedSet(codes),
		"current":      sortedSet(current),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(trimmed); err != nil {
		return nil, err
	}
	return current, os.WriteFile(filepath.Join(out, "currencyData.json"), buf.Bytes(), 0o644)
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// gatherPath is gather for a file whose locale value sits under a path of
// keys (`dates`, `calendars`, `gregorian`).
func gatherPath(dir, file string, path []string, trim func(map[string]any) map[string]any) (map[string]map[string]any, error) {
	return gather(dir, file, path[0], func(v map[string]any) map[string]any {
		for _, k := range path[1:] {
			next, _ := v[k].(map[string]any)
			v = next
		}
		if v == nil {
			v = map[string]any{}
		}
		return trim(v)
	})
}

// trimCurrencies keeps each currency's symbols, and the plural forms of
// the names of those in use today (current), which an amount written with
// its currency's name reads: a historical currency's name is its code.
// The name a currency has on its own (`displayName`) is not read.
func trimCurrencies(current map[string]bool) func(v map[string]any) map[string]any {
	return func(v map[string]any) map[string]any {
		out := map[string]any{}
		for code, raw := range v {
			m, _ := raw.(map[string]any)
			kept := map[string]any{}
			for k, x := range m {
				name := strings.HasPrefix(k, "displayName-count-")
				if k == "symbol" || k == "symbol-alt-narrow" || name && current[code] {
					kept[k] = x
				}
			}
			out[code] = kept
		}
		return out
	}
}

// trimGregorian keeps the month, day, day-period and era names, the date,
// time and date-time patterns and the available formats (skeletons).
func trimGregorian(v map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"months", "days", "eras", "dateFormats", "timeFormats"} {
		if x, ok := v[k]; ok {
			out[k] = x
		}
	}
	if dp, ok := v["dayPeriods"].(map[string]any); ok {
		periods := map[string]any{}
		for ctx, widths := range dp {
			ws, _ := widths.(map[string]any)
			keptCtx := map[string]any{}
			for w, names := range ws {
				ns, _ := names.(map[string]any)
				keptCtx[w] = keep(ns, []string{"am", "pm"})
			}
			periods[ctx] = keptCtx
		}
		out["dayPeriods"] = periods
	}
	if dt, ok := v["dateTimeFormats"].(map[string]any); ok {
		out["dateTimeFormats"] = keep(dt, []string{"full", "long", "medium", "short", "availableFormats"})
	}
	return out
}

// trimFields keeps the long relative-time forms of RelativeUnits.
func trimFields(v map[string]any) map[string]any {
	out := map[string]any{}
	for _, u := range cldrgen.RelativeUnits {
		m, _ := v[u].(map[string]any)
		kept := map[string]any{}
		for k, x := range m {
			if strings.HasPrefix(k, "relative-type-") || strings.HasPrefix(k, "relativeTime-type-") {
				kept[k] = x
			}
		}
		out[u] = kept
	}
	return out
}

// trimZoneNames keeps the localized GMT format.
func trimZoneNames(v map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"gmtFormat", "gmtZeroFormat", "hourFormat"} {
		if x, ok := v[k]; ok {
			out[k] = x
		}
	}
	return out
}

// trimUnits keeps the plural unit patterns of DurationUnits in each width.
func trimUnits(v map[string]any) map[string]any {
	out := map[string]any{}
	for _, width := range []string{"long", "short", "narrow"} {
		w, _ := v[width].(map[string]any)
		kept := map[string]any{}
		for _, u := range cldrgen.DurationUnits {
			m, _ := w["duration-"+u].(map[string]any)
			unit := map[string]any{}
			for k, x := range m {
				if strings.HasPrefix(k, "unitPattern-count-") {
					unit[k] = x
				}
			}
			kept[u] = unit
		}
		out[width] = kept
	}
	return out
}

// gather reads file from every locale directory under dir and answers each
// locale's value under key, trimmed.
func gather(dir, file, key string, trim func(map[string]any) map[string]any) (map[string]map[string]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), file))
		if err != nil {
			return nil, err
		}
		var doc struct {
			Main map[string]map[string]any `json:"main"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("%s/%s: %w", e.Name(), file, err)
		}
		loc, ok := doc.Main[e.Name()]
		if !ok {
			return nil, fmt.Errorf("%s/%s holds no main.%s", e.Name(), file, e.Name())
		}
		v, ok := loc[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s/%s has no %s", e.Name(), file, key)
		}
		out[e.Name()] = trim(v)
	}
	return out, nil
}

// trimNumbers keeps the keys cldrgen reads from one locale's numbers.
func trimNumbers(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, val := range v {
		switch {
		case k == "defaultNumberingSystem" || k == "minimumGroupingDigits":
			out[k] = val
		case k == "otherNumberingSystems":
			if m, ok := val.(map[string]any); ok && m["native"] != nil {
				out[k] = map[string]any{"native": m["native"]}
			}
		case strings.HasPrefix(k, "symbols-numberSystem-"):
			out[k] = keep(val, cldrgen.SymbolKeys)
		case strings.HasPrefix(k, "decimalFormats-numberSystem-"), strings.HasPrefix(k, "percentFormats-numberSystem-"):
			out[k] = keep(val, []string{"standard"})
		case strings.HasPrefix(k, "currencyFormats-numberSystem-"):
			m, _ := val.(map[string]any)
			kept := keep(val, []string{"standard", "standard-alphaNextToNumber", "accounting", "accounting-alphaNextToNumber"})
			for ck, cv := range m {
				if strings.HasPrefix(ck, "unitPattern-count-") {
					kept[ck] = cv
				}
			}
			out[k] = kept
		}
	}
	return out
}

func keep(v any, keys []string) map[string]any {
	m, _ := v.(map[string]any)
	out := map[string]any{}
	for _, k := range keys {
		if x, ok := m[k]; ok {
			out[k] = x
		}
	}
	return out
}

// write writes the gathered locales, one per line, sorted.
func write(path, pkg, key, note string, byLocale map[string]map[string]any) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n\"_source\": %q,\n\"_cldrVersion\": %q,\n\"main\": {\n",
		fmt.Sprintf("%s %s.0, main/*/%s.json, keys cldrgen does not read removed%s", pkg, cldrgen.Release, key, note),
		strings.Split(cldrgen.Release, ".")[0])
	locales := make([]string, 0, len(byLocale))
	for l := range byLocale {
		locales = append(locales, l)
	}
	sort.Strings(locales)
	for i, l := range locales {
		var line bytes.Buffer
		enc := json.NewEncoder(&line) // map keys sorted
		enc.SetEscapeHTML(false)
		if err := enc.Encode(byLocale[l]); err != nil {
			return err
		}
		v := bytes.TrimSuffix(line.Bytes(), []byte("\n"))
		comma := ","
		if i == len(locales)-1 {
			comma = ""
		}
		fmt.Fprintf(&b, "%q: %s%s\n", l, v, comma)
	}
	b.WriteString("}\n}\n")
	return os.WriteFile(path, b.Bytes(), 0o644)
}
