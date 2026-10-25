package cldrgen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/cldrdata"
)

// The currency and date data: currencyData.json (each ISO 4217 code's minor
// digits), and currencies.json, ca-gregorian.json, dateFields.json,
// timeZoneNames.json and units.json, which ./extract writes as each
// locale's own values, those its CLDR parent does not already give it.
// Reading one resolves a locale along the same chain the generated data is
// looked up along (resolvedOwn), so a locale's records are what cldr-json
// gives it.

// Counts are CLDR's plural categories, as record keys name them.
var Counts = []string{"zero", "one", "two", "few", "many", "other"}

// ReadCurrencyCodes reads every ISO 4217 code CLDR knows, current or
// historical, with its minor digits: the codes regions have used, the codes
// any locale names, and those CLDR's fractions list.
func ReadCurrencyCodes(dir string) (map[string]int, error) {
	var data struct {
		Version string            `json:"_cldrVersion"`
		Digits  map[string]string `json:"digits"`
		Codes   []string          `json:"codes"`
	}
	if err := readJSON(filepath.Join(dir, "currencyData.json"), &data); err != nil {
		return nil, err
	}
	if major, _, _ := strings.Cut(Release, "."); data.Version != major {
		return nil, fmt.Errorf("currencyData.json is CLDR %s, not the pinned %s", data.Version, Release)
	}
	names, err := readOwn(dir, "currencies.json")
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	add := func(code string) error {
		if !isoShaped(code) {
			return fmt.Errorf("%q is not an ISO 4217 code", code)
		}
		out[code] = cldrdata.DefaultCurrencyDigits
		return nil
	}
	for _, c := range data.Codes {
		if err := add(c); err != nil {
			return nil, fmt.Errorf("currencyData.json: %w", err)
		}
	}
	for _, byCode := range names {
		for c := range byCode {
			if err := add(c); err != nil {
				return nil, fmt.Errorf("currencies.json: %w", err)
			}
		}
	}
	for c, d := range data.Digits {
		if c == "DEFAULT" {
			continue
		}
		if err := add(c); err != nil {
			return nil, fmt.Errorf("currencyData.json: %w", err)
		}
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 || n > 18 {
			return nil, fmt.Errorf("currencyData.json: %s has %q digits", c, d)
		}
		out[c] = n
	}
	return out, nil
}

func isoShaped(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// readOwn reads one of the files ./extract writes as each locale's own
// values: main.<tag> for every locale.
func readOwn(dir, file string) (map[string]map[string]any, error) {
	var doc struct {
		Version string                    `json:"_cldrVersion"`
		Main    map[string]map[string]any `json:"main"`
	}
	if err := readJSON(filepath.Join(dir, file), &doc); err != nil {
		return nil, err
	}
	if major, _, _ := strings.Cut(Release, "."); doc.Version != major {
		return nil, fmt.Errorf("%s is CLDR %s, not the pinned %s", file, doc.Version, Release)
	}
	if doc.Main["und"] == nil {
		return nil, fmt.Errorf("%s has no root locale (und)", file)
	}
	return doc.Main, nil
}

// resolvedOwn is tag's values resolved: root's, then each ancestor's on its
// chain, farthest first, then its own, a nearer value replacing a farther
// one.
func resolvedOwn(byTag map[string]map[string]any, chain []string, tag string) map[string]any {
	out := map[string]any{}
	merge(out, byTag["und"])
	if tag == "und" {
		return out
	}
	for i := len(chain) - 1; i >= 0; i-- {
		merge(out, byTag[chain[i]])
	}
	return out
}

func merge(into, from map[string]any) {
	for k, v := range from {
		if m, ok := v.(map[string]any); ok {
			sub, _ := into[k].(map[string]any)
			if sub == nil {
				sub = map[string]any{}
				into[k] = sub
			}
			merge(sub, m)
			continue
		}
		into[k] = v
	}
}

// forEachLocale resolves every locale of file along chain and hands its
// values and records to add.
func forEachLocale(dir, file string, src *LocaleSource, chain func(string) []string,
	add func(tag string, v map[string]any, recs map[string]string) error) error {
	byTag, err := readOwn(dir, file)
	if err != nil {
		return err
	}
	for tag := range byTag {
		recs := src.Records[tag]
		if recs == nil {
			return fmt.Errorf("%s: %s has no number data", file, tag)
		}
		if err := add(tag, resolvedOwn(byTag, chain(tag), tag), recs); err != nil {
			return fmt.Errorf("%s: %s: %w", file, tag, err)
		}
	}
	return nil
}

// text is a value's text. A pattern CLDR gives with a numbering override
// (`{"_value": "d/M/yy", "_numbers": "M=romanlow"}`, Hawaiian's months in
// Roman numerals) is its pattern: the override is not read.
func text(v any) string {
	if m, ok := v.(map[string]any); ok {
		v = m["_value"]
	}
	s, _ := v.(string)
	return s
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// field is one record field: the text, which must not hold a tab or a line
// break.
func field(s string) (string, error) {
	if strings.ContainsAny(s, "\t\n") {
		return "", fmt.Errorf("%q holds a tab or a line break", s)
	}
	return s, nil
}

// readCurrencyFormats adds each locale's currency patterns, from
// numbers.json's currencyFormats: `currency-pattern <system> <standard>
// <alphaNextToNumber>` per numbering system, and `currency-unit` with the
// pattern an amount written with its currency's name takes (`{0} {1}`) in
// each plural form, from the default numbering system's block, else the
// Latin one's.
func readCurrencyFormats(src *LocaleSource, raw map[string]map[string]json.RawMessage) error {
	for tag, byKey := range raw {
		recs := src.Records[tag]
		def, _, _ := strings.Cut(recs[cldrdata.KeyNumbering], "\t")
		blocks := map[string]map[string]string{}
		for key, value := range byKey {
			system, ok := strings.CutPrefix(key, "currencyFormats-numberSystem-")
			if !ok {
				continue
			}
			var m map[string]string
			if err := json.Unmarshal(value, &m); err != nil {
				return fmt.Errorf("numbers.json: %s %s: %w", tag, key, err)
			}
			std, alpha := m["standard"], m["standard-alphaNextToNumber"]
			if alpha == "" {
				alpha = std
			}
			for _, p := range []string{std, alpha} {
				if _, err := cldrdata.ParseCurrencyPattern(p); err != nil {
					return fmt.Errorf("numbers.json: %s %s: %w", tag, key, err)
				}
			}
			recs[cldrdata.RecordKey("currency-pattern", system)] = std + "\t" + alpha
			blocks[system] = m
		}
		m := blocks[def]
		if m["unitPattern-count-other"] == "" {
			m = blocks["latn"]
		}
		var units []string
		for _, c := range Counts {
			p := m["unitPattern-count-"+c]
			if p != "" && (!strings.Contains(p, "{0}") || !strings.Contains(p, "{1}")) {
				return fmt.Errorf("numbers.json: %s: bad currency unit pattern %q", tag, p)
			}
			units = append(units, p)
		}
		if units[len(units)-1] == "" {
			return fmt.Errorf("numbers.json: %s has no currency unit pattern for other", tag)
		}
		recs["currency-unit"] = strings.Join(units, "\t")
	}
	return nil
}

// readCurrencies adds each locale's currency symbols and names:
// `currency-symbol <code> <symbol>`, `currency-narrow <code> <symbol>` and
// `currency-names <code> <zero> <one> <two> <few> <many> <other>`, the
// name in each plural form, empty where CLDR gives none. A symbol a locale
// does not give is root's, else the code; a name it does not give is the
// code.
func readCurrencies(dir string, src *LocaleSource, chain func(string) []string) error {
	return forEachLocale(dir, "currencies.json", src, chain, func(tag string, v map[string]any, recs map[string]string) error {
		for code, raw := range v {
			c := object(raw)
			for key, kind := range map[string]string{"symbol": "currency-symbol", "symbol-alt-narrow": "currency-narrow"} {
				if s := text(c[key]); s != "" {
					f, err := field(s)
					if err != nil {
						return err
					}
					recs[cldrdata.RecordKey(kind, code)] = f
				}
			}
			var forms []string
			named := false
			for _, count := range Counts {
				f, err := field(text(c["displayName-count-"+count]))
				if err != nil {
					return err
				}
				named = named || f != ""
				forms = append(forms, f)
			}
			if named {
				recs[cldrdata.RecordKey("currency-names", code)] = strings.Join(forms, "\t")
			}
		}
		return nil
	})
}

// The contexts and widths of the names readGregorian reads.
var (
	nameContexts = []string{"format", "stand-alone"}
	nameWidths   = []string{"abbreviated", "wide", "narrow", "short"}
	// Styles are CLDR's date and time format lengths, as std's DateStyle and
	// TimeStyle declare them.
	Styles   = []string{"full", "long", "medium", "short"}
	weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
)

// readGregorian adds each locale's Gregorian calendar data: `months
// <context>-<width>` (twelve names), `days <context>-<width>` (seven,
// Sunday first), `day-periods <context>-<width>` (AM and PM), `eras
// <width>` (BC and AD), `date-format`, `time-format` and `datetime-format
// <style>`, and `skeleton <skeleton> <pattern>` for each available format.
// An available format's `-alt-` and `-count-` variants are left out: no
// skeleton std accepts names them.
func readGregorian(dir string, src *LocaleSource, chain func(string) []string) error {
	return forEachLocale(dir, "ca-gregorian.json", src, chain, func(tag string, v map[string]any, recs map[string]string) error {
		for _, kind := range []string{"months", "days", "dayPeriods"} {
			byCtx := object(v[kind])
			for _, ctx := range nameContexts {
				for _, w := range nameWidths {
					names := object(object(byCtx[ctx])[w])
					if names == nil {
						continue
					}
					var keys []string
					switch kind {
					case "months":
						for i := 1; i <= 12; i++ {
							keys = append(keys, strconv.Itoa(i))
						}
					case "days":
						keys = weekdays
					default:
						keys = []string{"am", "pm"}
					}
					var fields []string
					for _, k := range keys {
						s := text(names[k])
						if s == "" {
							return fmt.Errorf("%s %s %s has no %s", kind, ctx, w, k)
						}
						f, err := field(s)
						if err != nil {
							return err
						}
						fields = append(fields, f)
					}
					rec := map[string]string{"months": "months", "days": "days", "dayPeriods": "day-periods"}[kind]
					recs[cldrdata.RecordKey(rec, ctx+"-"+w)] = strings.Join(fields, "\t")
				}
			}
		}
		eras := object(v["eras"])
		for w, key := range map[string]string{"abbreviated": "eraAbbr", "wide": "eraNames", "narrow": "eraNarrow"} {
			names := object(eras[key])
			bc, ad := text(names["0"]), text(names["1"])
			if bc == "" || ad == "" {
				return fmt.Errorf("eras %s are incomplete", key)
			}
			recs[cldrdata.RecordKey("eras", w)] = bc + "\t" + ad
		}
		for kind, key := range map[string]string{"date-format": "dateFormats", "time-format": "timeFormats", "datetime-format": "dateTimeFormats"} {
			byStyle := object(v[key])
			for _, style := range Styles {
				p := text(byStyle[style])
				if p == "" {
					return fmt.Errorf("%s has no %s", key, style)
				}
				if kind == "datetime-format" {
					if !strings.Contains(p, "{0}") || !strings.Contains(p, "{1}") {
						return fmt.Errorf("%s %s: bad pattern %q", key, style, p)
					}
				} else if _, err := cldrdata.ParseDatePattern(p); err != nil {
					return fmt.Errorf("%s %s: %w", key, style, err)
				}
				recs[cldrdata.RecordKey(kind, style)] = p
			}
		}
		for skel, raw := range object(object(v["dateTimeFormats"])["availableFormats"]) {
			if strings.Contains(skel, "-alt-") || strings.Contains(skel, "-count-") {
				continue
			}
			p := text(raw)
			if _, err := cldrdata.ParseDatePattern(p); err != nil {
				return fmt.Errorf("availableFormats %s: %w", skel, err)
			}
			f, err := field(p)
			if err != nil {
				return err
			}
			recs[cldrdata.RecordKey("skeleton", skel)] = f
		}
		return nil
	})
}

// RelativeOffsets are the offsets a `relative` record names a unit's words
// for, in its field order: the day before yesterday through the day after
// tomorrow.
var RelativeOffsets = []string{"-2", "-1", "0", "1", "2"}

// forms are the six plural forms of a pattern from m's keys prefix+count, in
// Counts' order, empty where CLDR gives none; other must be there.
func forms(m map[string]any, prefix string) ([]string, error) {
	var out []string
	for _, c := range Counts {
		f, err := field(text(m[prefix+c]))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if out[len(out)-1] == "" {
		return nil, fmt.Errorf("%sother is missing", prefix)
	}
	return out, nil
}

// readRelative adds each locale's relative-time forms of RelativeUnits:
// `relative <unit>` with its words for RelativeOffsets (`yesterday` is day
// -1), empty where the locale has none, and `relative-future` and
// `relative-past <unit>` with the pattern in each plural form. A form may
// leave the number out ("ntra un jornu", in a day, is scn's future `one`).
func readRelative(dir string, src *LocaleSource, chain func(string) []string) error {
	return forEachLocale(dir, "dateFields.json", src, chain, func(tag string, v map[string]any, recs map[string]string) error {
		for _, unit := range RelativeUnits {
			u := object(v[unit])
			var words []string
			for _, off := range RelativeOffsets {
				f, err := field(text(u["relative-type-"+off]))
				if err != nil {
					return err
				}
				words = append(words, f)
			}
			recs[cldrdata.RecordKey("relative", unit)] = strings.Join(words, "\t")
			for kind, key := range map[string]string{"relative-future": "relativeTime-type-future", "relative-past": "relativeTime-type-past"} {
				fs, err := forms(object(u[key]), "relativeTimePattern-count-")
				if err != nil {
					return fmt.Errorf("%s %s: %w", unit, key, err)
				}
				recs[cldrdata.RecordKey(kind, unit)] = strings.Join(fs, "\t")
			}
		}
		return nil
	})
}

// readZoneNames adds each locale's localized GMT format: `gmt <format>
// <zero format> <hour format>`.
func readZoneNames(dir string, src *LocaleSource, chain func(string) []string) error {
	return forEachLocale(dir, "timeZoneNames.json", src, chain, func(tag string, v map[string]any, recs map[string]string) error {
		gmt, zero, hour := text(v["gmtFormat"]), text(v["gmtZeroFormat"]), text(v["hourFormat"])
		if !strings.Contains(gmt, "{0}") || zero == "" || !strings.Contains(hour, ";") {
			return fmt.Errorf("bad GMT format %q, %q, %q", gmt, zero, hour)
		}
		recs[cldrdata.KeyGMT] = gmt + "\t" + zero + "\t" + hour
		return nil
	})
}

// UnitWidths are the widths of the duration unit patterns, in the order
// std's `ListWidth` declares Wide, Short and Narrow.
var UnitWidths = []string{"long", "short", "narrow"}

// readUnits adds each locale's duration unit patterns:
// `duration-unit <unit>-<width>` with the pattern in each plural form.
func readUnits(dir string, src *LocaleSource, chain func(string) []string) error {
	return forEachLocale(dir, "units.json", src, chain, func(tag string, v map[string]any, recs map[string]string) error {
		for _, w := range UnitWidths {
			for _, unit := range DurationUnits {
				fs, err := forms(object(object(v[w])[unit]), "unitPattern-count-")
				if err != nil {
					return fmt.Errorf("%s %s: %w", w, unit, err)
				}
				recs[cldrdata.RecordKey("duration-unit", unit+"-"+w)] = strings.Join(fs, "\t")
			}
		}
		return nil
	})
}
