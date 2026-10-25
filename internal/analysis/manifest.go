package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/stdformat"
	"github.com/nomi-language/nomi/internal/stdlocale"
)

// ErrManifestMissing is returned by LoadManifest when nomi.toml is
// absent. Callers may treat this as "not a Nomi module" or as an
// error depending on context.
var ErrManifestMissing = errors.New("nomi.toml not found")

// Manifest is the in-memory representation of nomi.toml. Fields
// mirror the design doc's TOML schema:
//
//	[module]
//	name = "..."
//	entry_points = [...]
type Manifest struct {
	Name        string   // [module].name
	EntryPoints []string // [module].entry_points (paths under module root, .nomi implicit)
	// Locales is [i18n].locales, each tag canonical, in the manifest's
	// order: the locales whose CLDR data the program carries. Nil when the
	// manifest has no [i18n] table or no locales in it, which carries every
	// locale.
	Locales []string
	// SourceLocale is [i18n].source_locale, canonical: the locale a
	// `message` declaration's own text is in. "en" when unset.
	SourceLocale string
	// PartialLocales is [i18n].partial_locales, canonical: locales whose
	// catalogs may leave a message out, which then shows in the source
	// locale. The program carries their data too.
	PartialLocales []string
	// Catalogs is [i18n].catalogs: the directory under the module root
	// holding one directory of catalogs per locale. "locales" when unset.
	Catalogs string
	// WebEmbedded is [i18n].web_embedded, canonical: the carried locales
	// `nomi build --web` compiles into app.wasm, each other one going
	// into a locale file the page loads. Nil when unset, which is the
	// source locale alone (WebLocales).
	WebEmbedded []string
	// CustomCurrencies is [money].custom_currencies: the custom currencies
	// a “Currency`…` “ or “Money`…` “ literal in this module may name,
	// each as its literal writes it (`PTS:0`), in the manifest's order. Nil
	// or empty allows none.
	CustomCurrencies []string
	// Tables holds every table other than Nomi's own (NomiTables), by
	// name: a macro module's configuration is the table named after it
	// (MacroConfig).
	Tables map[string]map[string]any
}

// CarriedLocales are the locales whose CLDR data the program carries:
// Locales and PartialLocales, or nil (every locale) when Locales is nil.
func (m *Manifest) CarriedLocales() []string {
	if m == nil || m.Locales == nil {
		return nil
	}
	return append(append([]string(nil), m.Locales...), m.PartialLocales...)
}

// WebLocales are the locales a browser build embeds: WebEmbedded, or the
// source locale when it is unset; nil (every one) when the manifest
// lists no locales, since a program that carries every locale has no
// list to split.
func (m *Manifest) WebLocales() []string {
	if m == nil || m.Locales == nil {
		return nil
	}
	if m.WebEmbedded != nil {
		return slices.Clone(m.WebEmbedded)
	}
	return []string{m.Source()}
}

// Source is the source locale: SourceLocale, or the default for a
// manifest built in memory without one.
func (m *Manifest) Source() string {
	if m == nil || m.SourceLocale == "" {
		return DefaultSourceLocale
	}
	return m.SourceLocale
}

// CatalogDir is the catalog directory under the module root: Catalogs, or
// the default.
func (m *Manifest) CatalogDir() string {
	if m == nil || m.Catalogs == "" {
		return DefaultCatalogs
	}
	return m.Catalogs
}

// DefaultSourceLocale and DefaultCatalogs are [i18n]'s defaults.
const (
	DefaultSourceLocale = "en"
	DefaultCatalogs     = "locales"
)

// manifestFile is the TOML decoding shape. Separate from Manifest so
// the exported type doesn't expose nested anonymous structs.
type manifestFile struct {
	Module struct {
		Name        string   `toml:"name"`
		EntryPoints []string `toml:"entry_points"`
	} `toml:"module"`
	I18n struct {
		Locales        *[]string `toml:"locales"`
		SourceLocale   *string   `toml:"source_locale"`
		PartialLocales *[]string `toml:"partial_locales"`
		Catalogs       *string   `toml:"catalogs"`
		WebEmbedded    *[]string `toml:"web_embedded"`
	} `toml:"i18n"`
	Money struct {
		CustomCurrencies []string `toml:"custom_currencies"`
	} `toml:"money"`
}

// customCurrencies checks [money].custom_currencies: each entry a custom
// currency as its literal writes it (`PTS:0`, what Currency.custom accepts),
// listed once.
func customCurrencies(raw []string, sourceLabel string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	full := cldrfull.Full()
	out := make([]string, 0, len(raw))
	for _, text := range raw {
		if problem := stdformat.CustomCurrencyProblem(full, text); problem != "" {
			return nil, fmt.Errorf("%s: [money] custom_currencies: %q is not a custom currency: %s", sourceLabel, text, problem)
		}
		if slices.Contains(out, text) {
			return nil, fmt.Errorf("%s: [money] custom_currencies lists %s twice", sourceLabel, text)
		}
		out = append(out, text)
	}
	return out, nil
}

// i18nLocales checks [i18n].locales and answers each tag canonical: a
// well-formed BCP 47 tag, listed once, that is not root (und) and that CLDR
// has data for, on its own or through a parent other than root.
func i18nLocales(raw *[]string, sourceLabel string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	if len(*raw) == 0 {
		return nil, fmt.Errorf("%s: [i18n] locales is empty; list the locales the program carries, or remove it to carry every locale", sourceLabel)
	}
	full := cldrfull.Full()
	seen := map[string]string{}
	out := make([]string, 0, len(*raw))
	for _, tag := range *raw {
		a := stdlocale.FFIParse(tag)
		if a.Kind != stdlocale.KindOK {
			return nil, fmt.Errorf("%s: [i18n] locales: %q is not a locale tag%s", sourceLabel, tag, localeHint(a))
		}
		if prev, dup := seen[a.Value]; dup {
			return nil, fmt.Errorf("%s: [i18n] locales lists %s twice (%q and %q)", sourceLabel, a.Value, prev, tag)
		}
		if a.Value == "und" {
			return nil, fmt.Errorf("%s: [i18n] locales: und is CLDR's root, which every program carries, not a locale to list", sourceLabel)
		}
		known := false
		for _, t := range stdlocale.Fallbacks(full, a.Value) {
			if full.HasLocale(t) {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("%s: [i18n] locales: CLDR %s has no data for %s or a parent of it", sourceLabel, full.Version, a.Value)
		}
		seen[a.Value] = tag
		out = append(out, a.Value)
	}
	return out, nil
}

// i18nMessageSettings reads [i18n]'s settings for messages into m:
// source_locale, a tag that locales lists when it lists any; partial_locales,
// tags that locales does not list; and catalogs, a relative directory that
// stays under the module root.
func i18nMessageSettings(m *Manifest, raw manifestFile, sourceLabel string) error {
	if raw.I18n.SourceLocale != nil {
		tags, err := i18nLocales(&[]string{*raw.I18n.SourceLocale}, sourceLabel)
		if err != nil {
			return errors.New(strings.Replace(err.Error(), "[i18n] locales", "[i18n] source_locale", 1))
		}
		m.SourceLocale = tags[0]
	}
	if raw.I18n.PartialLocales != nil {
		if m.Locales == nil {
			return fmt.Errorf("%s: [i18n] partial_locales needs locales: list the locales every message is translated into", sourceLabel)
		}
		if len(*raw.I18n.PartialLocales) > 0 {
			tags, err := i18nLocales(raw.I18n.PartialLocales, sourceLabel)
			if err != nil {
				return errors.New(strings.Replace(err.Error(), "[i18n] locales", "[i18n] partial_locales", 1))
			}
			for _, t := range tags {
				if slices.Contains(m.Locales, t) {
					return fmt.Errorf("%s: [i18n] lists %s in both locales and partial_locales; a locale is complete or partial", sourceLabel, t)
				}
			}
			m.PartialLocales = tags
		}
	}
	if raw.I18n.WebEmbedded != nil {
		if m.Locales == nil {
			return fmt.Errorf("%s: [i18n] web_embedded needs locales: list the locales the program carries", sourceLabel)
		}
		tags := []string{}
		if len(*raw.I18n.WebEmbedded) > 0 {
			var err error
			if tags, err = i18nLocales(raw.I18n.WebEmbedded, sourceLabel); err != nil {
				return errors.New(strings.Replace(err.Error(), "[i18n] locales", "[i18n] web_embedded", 1))
			}
		}
		for _, t := range tags {
			if !slices.Contains(m.CarriedLocales(), t) {
				return fmt.Errorf("%s: [i18n] web_embedded lists %s, which neither locales nor partial_locales lists", sourceLabel, t)
			}
		}
		m.WebEmbedded = tags
	}
	if raw.I18n.Catalogs != nil {
		dir := filepath.ToSlash(filepath.Clean(*raw.I18n.Catalogs))
		if *raw.I18n.Catalogs == "" || filepath.IsAbs(*raw.I18n.Catalogs) || dir == "." || dir == ".." || strings.HasPrefix(dir, "../") {
			return fmt.Errorf("%s: [i18n] catalogs %q is not a directory under the module root", sourceLabel, *raw.I18n.Catalogs)
		}
		m.Catalogs = dir
	}
	return nil
}

func localeHint(a stdlocale.LocaleAttempt) string {
	if a.Kind == stdlocale.KindUnderscore {
		return fmt.Sprintf("; write %q", a.Detail)
	}
	return ""
}

// LoadManifest reads nomi.toml from the given directory and returns
// the parsed manifest. Returns ErrManifestMissing if the file doesn't
// exist. Returns a descriptive error if the file is malformed or
// fails the basic schema invariants. See ParseManifestData for the
// validation rules.
func LoadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, "nomi.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrManifestMissing
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseManifestData(data, path, isStdlibManifestDir(dir))
}

// ParseManifestData parses nomi.toml content from a byte slice and
// validates the basic schema invariants. The `sourceLabel` is the
// diagnostic prefix used in error messages — typically the file path
// when the data came from disk, or a synthetic label like
// "<virtual nomi.toml>" when staged in memory by the tour playground.
//
// Validation rules:
//
//   - [module].name is required and must not be whitespace-only.
//   - [module].name must not be "std" (reserved for the standard library).
//   - [module].entry_points entries must be non-empty strings.
//   - [module].entry_points must not contain duplicates.
func ParseManifestData(data []byte, sourceLabel string) (*Manifest, error) {
	return parseManifestData(data, sourceLabel, false)
}

func parseManifestData(data []byte, sourceLabel string, allowStdName bool) (*Manifest, error) {
	var raw manifestFile
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", sourceLabel, err)
	}
	if strings.TrimSpace(raw.Module.Name) == "" {
		return nil, fmt.Errorf("%s: [module] name is required (non-empty, non-whitespace)", sourceLabel)
	}
	if raw.Module.Name == "std" && !allowStdName {
		return nil, fmt.Errorf("%s: [module] name %q is reserved for the standard library; choose a different name", sourceLabel, raw.Module.Name)
	}
	seen := map[string]bool{}
	for _, e := range raw.Module.EntryPoints {
		if e == "" {
			return nil, fmt.Errorf("%s: entry_points contains an empty string — every entry must name a file", sourceLabel)
		}
		if seen[e] {
			return nil, fmt.Errorf("%s: entry_points contains duplicate %q", sourceLabel, e)
		}
		seen[e] = true
	}
	locales, err := i18nLocales(raw.I18n.Locales, sourceLabel)
	if err != nil {
		return nil, err
	}
	m := &Manifest{
		Name:         raw.Module.Name,
		EntryPoints:  append([]string(nil), raw.Module.EntryPoints...),
		Locales:      locales,
		SourceLocale: DefaultSourceLocale,
		Catalogs:     DefaultCatalogs,
	}
	if err := i18nMessageSettings(m, raw, sourceLabel); err != nil {
		return nil, err
	}
	if m.CustomCurrencies, err = customCurrencies(raw.Money.CustomCurrencies, sourceLabel); err != nil {
		return nil, err
	}
	var tables map[string]any
	if err := toml.Unmarshal(data, &tables); err == nil {
		for name, v := range tables {
			if t, ok := v.(map[string]any); ok && !slices.Contains(NomiTables, name) {
				if m.Tables == nil {
					m.Tables = map[string]map[string]any{}
				}
				m.Tables[name] = t
			}
		}
	}
	return m, nil
}

// NomiTables are the tables of nomi.toml that Nomi itself reads. A macro
// module's configuration is the table named after the module, so a macro
// of a module named like one of these cannot be used (expandMacro).
var NomiTables = []string{"module", "i18n", "money"}

// MacroConfig is the table a macro module named module reads
// from this manifest, `[module]`, as JSON text: "null" when there is none.
func (m *Manifest) MacroConfig(module string) string {
	if m == nil || m.Tables[module] == nil {
		return "null"
	}
	data, err := json.Marshal(m.Tables[module])
	if err != nil {
		return "null"
	}
	return string(data)
}

// IsForeignStdlibDir reports whether dir is the root of a standard library
// other than this binary's: its nomi.toml names the module "std", and it is
// not StdlibPath(). Another Nomi checkout's std is one.
func IsForeignStdlibDir(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "nomi.toml"))
	if err != nil {
		return false
	}
	var raw manifestFile
	if err := toml.Unmarshal(data, &raw); err != nil {
		return false
	}
	return raw.Module.Name == "std" && !isStdlibManifestDir(dir)
}

func isStdlibManifestDir(dir string) bool {
	stdPath, err := StdlibPath()
	if err != nil {
		return false
	}
	return sameDir(dir, stdPath)
}

func sameDir(a, b string) bool {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if realA, err := filepath.EvalSymlinks(absA); err == nil {
		absA = realA
	}
	if realB, err := filepath.EvalSymlinks(absB); err == nil {
		absB = realB
	}
	return absA == absB
}
