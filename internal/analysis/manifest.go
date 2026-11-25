package analysis

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/stdformat"
	"github.com/nomi-language/nomi/internal/stdlocale"
)

// ErrManifestMissing is returned by LoadManifest when nomi.toml is
// absent. Callers may treat this as "not a package" or as an
// error depending on context.
var ErrManifestMissing = errors.New("nomi.toml not found")

// Manifest is the in-memory representation of nomi.toml. Fields
// mirror the design doc's TOML schema:
//
//	[package]
//	name = "..."
//	entry_points = [...]
//
//	[dependencies]
//	name = { repo = "...", version = "..." }
type Manifest struct {
	Name string // [package].name
	// Dependencies is the [dependencies] table as pkgcache.ParseManifest
	// reads it, sorted by name: each package the package's files import, by
	// the name they import it by.
	Dependencies []pkgcache.Requirement
	// EntryPoints is [package].entry_points: paths under the package root,
	// .nomi implicit. The package's tools are entry points too without being
	// listed (IsEntry, Entries).
	EntryPoints []string
	// Locales is [i18n].locales, each tag canonical, in the manifest's
	// order: the locales whose CLDR data the program carries. Nil when the
	// manifest has no [i18n] table or no locales in it, which carries every
	// locale.
	Locales []string
	// SourceLocale is [i18n].source_locale, canonical: the locale a
	// `phrase` declaration's own text is in. "en" when unset.
	SourceLocale string
	// PartialLocales is [i18n].partial_locales, canonical: locales whose
	// catalogs may leave a phrase out, which then shows in the source
	// locale. The program carries their data too.
	PartialLocales []string
	// Catalogs is [i18n].catalogs: the directory under the package root
	// holding one directory of catalogs per locale. "locales" when unset.
	Catalogs string
	// WebEmbedded is [i18n].web_embedded, canonical: the carried locales
	// `nomi build --web` compiles into app.wasm, each other one going
	// into a locale file the page loads. Nil when unset, which is the
	// source locale alone (WebLocales).
	WebEmbedded []string
	// CustomCurrencies is [money].custom_currencies: the custom currencies
	// a “Currency`…` “ or “Money`…` “ literal in this package may name,
	// each as its literal writes it (`PTS:0`), in the manifest's order. Nil
	// or empty allows none.
	CustomCurrencies []string
	// Effects is the [effects.self] and [effects.dependencies] tables and
	// the `effects` keys of the [dependencies] entries: the effects the
	// package's own code, and the code of the packages it depends on, may
	// have. Nil when the manifest has none of them, which holds nothing.
	Effects *Effects
}

// Effects is nomi.toml's effects lists: the effects the package's own
// files, and the files of the packages it depends on, may have (vmhost's
// EffectCallsNotAllowed). A list's names are `std::` and a std module as an
// import writes it (`std::fs`), or `std::startup`. The parser checks each
// name's form; whether a std module has functions with effects, and a key
// is a module of the project, is the program's to answer (vmhost).
//
// When the project holds its dependencies (Dependencies is not nil), a
// dependency's own [effects.self] holds the dependency too: its Effects is
// the list it declares for its own files, and its Modules the entries it
// declares for its own modules. Its EffectsInTests, EntriesRequired and
// Dependencies are for its own builds and are not read when it is a
// dependency.
type Effects struct {
	// Self is [effects.self] with [effects.self.modules], nil when the
	// manifest leaves both out, which holds the package's own code to
	// nothing.
	Self *SelfEffects
	// Dependencies is [effects.dependencies] with the `effects` and
	// `effects_in_tests` keys of the [dependencies] entries, nil when the
	// manifest has neither the table nor such a key, which holds the
	// dependencies to nothing.
	Dependencies *DependenciesEffects
}

// EntryLists is the lists that hold entries, each keyed by the code an
// entry is for: [effects.self.modules] by a module of the project as its
// own code imports it, and the `effects` and `effects_in_tests` keys of
// the [dependencies] entries by the entry's key, a dependency's name.
var EntryLists = []string{ListSelfModules, ListEntryEffects, ListEntryEffectsInTests}

// Entries is the entries list names, one of EntryLists: each entry's key
// and its std names, nil when c has no such entry.
func (c *Effects) Entries(list string) map[string][]string {
	switch {
	case list == ListSelfModules && c.Self != nil:
		return c.Self.Modules
	case list == ListEntryEffects && c.Dependencies != nil:
		return c.Dependencies.Entries
	case list == ListEntryEffectsInTests && c.Dependencies != nil:
		return c.Dependencies.EntriesInTests
	}
	return nil
}

// EntryKeyPath is the path of the key of list's entry for key, as
// ManifestKeyLine reads it: the table of [effects.self.modules] and the
// key, or for a dependency's entry `dependencies`, the key and the entry's
// own key (`effects`).
func EntryKeyPath(list, key string) []string {
	parts := strings.Split(list, ".")
	if parts[0] == "dependencies" {
		return []string{"dependencies", key, parts[1]}
	}
	return append(parts, key)
}

// SelfEffects is [effects.self] and [effects.self.modules]:
// the effects the calls written in the package's own files may have.
type SelfEffects struct {
	// Effects is the effects its own files may have, by std name. Empty
	// when the table leaves it out, which allows no std call.
	Effects []string
	// EffectsInTests is added to Effects when the roots are test cases.
	EffectsInTests []string
	// EntriesRequired is entries_required: every module of the project
	// whose files the program loads must have an entry under Modules.
	EntriesRequired bool
	// Modules is [effects.self.modules]: for a module of the project,
	// by its import path as the project's own code writes it (`pricing`,
	// `reports::daily`, `pricing_test`), the std names that module's files
	// may have, within Effects. A module with no entry is held by Effects
	// alone. Nil when the manifest has no such table.
	Modules map[string][]string
}

// DependenciesEffects is [effects.dependencies] and the effects keys of
// the [dependencies] entries: the effects the calls written in the files
// of the packages the project depends on, directly or through other
// packages, may have.
type DependenciesEffects struct {
	// EntriesRequired is entries_required: every [dependencies] entry
	// through which the program loads Nomi files must have an `effects` key,
	// or its package an [effects.self] of its own.
	EntriesRequired bool
	// Effects is the most any dependency's files may have, a ceiling over
	// every entry. Nil when the table leaves it out, which sets no
	// ceiling; empty, not nil, for `effects = []`, which allows nothing.
	Effects []string
	// EffectsInTests is added to Effects when the roots are test cases. The
	// table sets it only beside Effects.
	EffectsInTests []string
	// Entries is the `effects` key of each [dependencies] entry that has
	// one, by the entry's key: the most the files of that package, and of
	// every package the project reaches through it, may have. A package the
	// project reaches through an entry with no `effects` key is held by
	// Effects and its own [effects.self] alone (vmhost's standingsOf).
	Entries map[string][]string
	// EntriesInTests is the `effects_in_tests` key of each entry that has
	// one: added to its Entries entry when the roots are test cases. An
	// entry sets it only beside `effects`.
	EntriesInTests map[string][]string
}

// EffectsFor is the std names s allows its own files, with EffectsInTests
// when tests is set.
func (s *SelfEffects) EffectsFor(tests bool) []string {
	if tests {
		return append(slices.Clone(s.Effects), s.EffectsInTests...)
	}
	return s.Effects
}

// EffectsFor is d's ceiling, with EffectsInTests when tests is set; nil
// when d sets none.
func (d *DependenciesEffects) EffectsFor(tests bool) []string {
	if tests && d.Effects != nil {
		return append(slices.Clone(d.Effects), d.EffectsInTests...)
	}
	return d.Effects
}

// EntryFor is the entry for key, a [dependencies] key, with its
// EntriesInTests entry when tests is set, and whether the entry has an
// `effects` key.
func (d *DependenciesEffects) EntryFor(key string, tests bool) ([]string, bool) {
	entry, has := d.Entries[key]
	if tests && has {
		entry = append(slices.Clone(entry), d.EntriesInTests[key]...)
	}
	return entry, has
}

// ToolsDir is the directory under a package's root that holds its tools,
// the programs `nomi tool <package> <name>` runs.
const ToolsDir = "tools"

// IsToolFile reports whether modRel, a file's package-relative path with
// "/" separators and without .nomi, is one of its package's tools: a file
// under tools/ that is not a test file and is in no internal/ directory,
// where a tool's helpers go. Every tool is an entry point.
func IsToolFile(modRel string) bool {
	rest, ok := strings.CutPrefix(modRel, ToolsDir+"/")
	if !ok || rest == "" || strings.HasSuffix(rest, "_test") {
		return false
	}
	return !slices.Contains(strings.Split(rest, "/")[:strings.Count(rest, "/")], "internal")
}

// IsEntry reports whether modRel, a file's package-relative path without
// .nomi, is an entry point of the package: listed in entry_points, or a
// tool (IsToolFile).
func (m *Manifest) IsEntry(modRel string) bool {
	return IsToolFile(modRel) || slices.Contains(m.EntryPoints, modRel)
}

// Entries is every entry point of the package rooted at root: entry_points
// in the manifest's order, then each tool under root's tools/ directory
// that entry_points does not list, by path. Each is package-relative, as
// entry_points writes it.
func (m *Manifest) Entries(root string) []string {
	out := slices.Clone(m.EntryPoints)
	listed := map[string]bool{}
	for _, e := range m.EntryPoints {
		listed[strings.TrimSuffix(e, ".nomi")] = true
	}
	var tools []string
	dir := filepath.Join(root, ToolsDir)
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "internal") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".nomi" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		key := strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
		if IsToolFile(key) && !listed[key] {
			tools = append(tools, key)
		}
		return nil
	})
	slices.Sort(tools)
	return append(out, tools...)
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

// CatalogDir is the catalog directory under the package root: Catalogs, or
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
	Package struct {
		Name        string   `toml:"name"`
		Nomi        string   `toml:"nomi"`
		EntryPoints []string `toml:"entry_points"`
	} `toml:"package"`
	Dependencies map[string]pkgcache.DependencyEntry `toml:"dependencies"`
	I18n         struct {
		Locales        *[]string `toml:"locales"`
		SourceLocale   *string   `toml:"source_locale"`
		PartialLocales *[]string `toml:"partial_locales"`
		Catalogs       *string   `toml:"catalogs"`
		WebEmbedded    *[]string `toml:"web_embedded"`
	} `toml:"i18n"`
	Money struct {
		CustomCurrencies []string `toml:"custom_currencies"`
	} `toml:"money"`
	Effects *struct {
		Self         *selfTable         "toml:\"self\" keys:\"`effects` lists the effects this package's own code may have, by std module, and `effects_in_tests` those its tests may also have, `entries_required` makes every module of the project have an entry, and [effects.self.modules] holds an entry for each of its modules\""
		Dependencies *dependenciesTable "toml:\"dependencies\" keys:\"`effects` and `effects_in_tests` list the most effects any dependency may have, and `entries_required` makes every [dependencies] entry have an `effects` key; a dependency's own allowance is the `effects` key of its [dependencies] entry\""
	} "toml:\"effects\" keys:\"it holds two tables: [effects.self] is the effects this package's own code may have, and [effects.dependencies] the effects the code of the packages it depends on may have\""
}

// selfTable is [effects.self] as it decodes, with [effects.self.modules].
type selfTable struct {
	EntriesRequired bool                `toml:"entries_required"`
	Effects         []string            `toml:"effects"`
	EffectsInTests  []string            `toml:"effects_in_tests"`
	Modules         map[string][]string `toml:"modules"`
}

// dependenciesTable is [effects.dependencies] as it decodes.
type dependenciesTable struct {
	EntriesRequired bool     `toml:"entries_required"`
	Effects         []string `toml:"effects"`
	EffectsInTests  []string `toml:"effects_in_tests"`
}

// effectLists checks [effects.self], [effects.dependencies] and the
// effects keys of deps, the [dependencies] entries as pkgcache reads them:
// each list's names `std::<module>` (effects.NameProblem), each listed once
// in its list, each `effects_in_tests` only beside its `effects`, and each
// key under [effects.self.modules] non-empty. A `host:` name is a function
// an embedding Go program supplies, which a manifest cannot allow.
func effectLists(raw manifestFile, deps []pkgcache.Requirement, data []byte, sourceLabel string) (*Effects, error) {
	entryKeys := slices.ContainsFunc(deps, func(r pkgcache.Requirement) bool { return r.Effects != nil || r.EffectsInTests != nil })
	if (raw.Effects == nil || raw.Effects.Self == nil && raw.Effects.Dependencies == nil) && !entryKeys {
		return nil, nil
	}
	at := func(line int) *ManifestError { return &ManifestError{Path: sourceLabel, Line: line} }
	check := func(table, list string, names []string, path ...string) ([]string, error) {
		seen := map[string]bool{}
		out := []string{}
		for _, name := range names {
			at := at(ManifestEntryLine(data, name, path...))
			if problem := effects.NameProblem(name); problem != "" {
				return nil, at.with("[%s] %s: %q %s", table, list, name, problem)
			}
			if seen[name] {
				return nil, at.with("[%s] %s lists %s twice", table, list, name)
			}
			seen[name] = true
			out = append(out, name)
		}
		return out, nil
	}
	c := &Effects{}
	var err error
	var self *selfTable
	var dependencies *dependenciesTable
	if raw.Effects != nil {
		self, dependencies = raw.Effects.Self, raw.Effects.Dependencies
	}
	if s := self; s != nil {
		c.Self = &SelfEffects{EntriesRequired: s.EntriesRequired}
		if s.Modules != nil {
			c.Self.Modules = make(map[string][]string, len(s.Modules))
			for _, key := range slices.Sorted(maps.Keys(s.Modules)) {
				path := EntryKeyPath(ListSelfModules, key)
				if strings.TrimSpace(key) == "" {
					return nil, at(ManifestKeyLine(data, path...)).with("[%s] has an empty key; write a module as the project's own code imports it, such as \"pricing\"", ListSelfModules)
				}
				if c.Self.Modules[key], err = check(ListSelfModules, fmt.Sprintf("%q", key), s.Modules[key], path...); err != nil {
					return nil, err
				}
			}
		}
		if c.Self.Effects, err = check(ListSelf, "effects", s.Effects, "effects", "self", "effects"); err != nil {
			return nil, err
		}
		if c.Self.EffectsInTests, err = check(ListSelf, "effects_in_tests", s.EffectsInTests, "effects", "self", "effects_in_tests"); err != nil {
			return nil, err
		}
	}
	if d := dependencies; d != nil || entryKeys {
		c.Dependencies = &DependenciesEffects{}
		if d != nil {
			c.Dependencies.EntriesRequired = d.EntriesRequired
			if d.Effects != nil {
				if c.Dependencies.Effects, err = check(ListDependencies, "effects", d.Effects, "effects", "dependencies", "effects"); err != nil {
					return nil, err
				}
			}
			if d.EffectsInTests != nil && d.Effects == nil {
				return nil, at(ManifestKeyLine(data, "effects", "dependencies", "effects_in_tests")).with(
					"[%s] effects_in_tests adds to effects, which the table leaves out; with no effects, no ceiling holds the dependencies", ListDependencies)
			}
			if c.Dependencies.EffectsInTests, err = check(ListDependencies, "effects_in_tests", d.EffectsInTests, "effects", "dependencies", "effects_in_tests"); err != nil {
				return nil, err
			}
		}
		// Each [dependencies] entry's own effects keys, in the entries'
		// order (by name).
		for _, r := range deps {
			if r.EffectsInTests != nil && r.Effects == nil {
				return nil, at(ManifestKeyLine(data, EntryKeyPath(ListEntryEffectsInTests, r.Name)...)).with(
					"[dependencies] %s.effects_in_tests adds to its effects, which the entry leaves out; with no effects, only [%s] holds %s", r.Name, ListDependencies, r.Name)
			}
			for _, list := range []struct {
				key   string
				names []string
				into  *map[string][]string
			}{{"effects", r.Effects, &c.Dependencies.Entries}, {"effects_in_tests", r.EffectsInTests, &c.Dependencies.EntriesInTests}} {
				if list.names == nil {
					continue
				}
				names, err := check("dependencies", r.Name+"."+list.key, list.names, "dependencies", r.Name, list.key)
				if err != nil {
					return nil, err
				}
				if *list.into == nil {
					*list.into = map[string][]string{}
				}
				(*list.into)[r.Name] = names
			}
		}
	}
	return c, nil
}

// manifestAt is the ManifestError at the line of data, the text of the
// nomi.toml at path, that writes value in the list at key, or the key's line
// when value is "" or no line quotes it (manifestLine): where each value
// error parseManifestData finds is, as an unknown key's is.
func manifestAt(path string, data []byte, value string, key ...string) *ManifestError {
	return &ManifestError{Path: path, Line: manifestLine(data, key, value)}
}

// customCurrencies checks [money].custom_currencies: each entry a custom
// currency as its literal writes it (`PTS:0`, what Currency.custom accepts),
// listed once.
func customCurrencies(raw []string, data []byte, sourceLabel string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	full := cldrfull.Full()
	out := make([]string, 0, len(raw))
	for _, text := range raw {
		at := manifestAt(sourceLabel, data, text, "money", "custom_currencies")
		if problem := stdformat.CustomCurrencyProblem(full, text); problem != "" {
			return nil, at.with("[money] custom_currencies: %q is not a custom currency: %s", text, problem)
		}
		if slices.Contains(out, text) {
			return nil, at.with("[money] custom_currencies lists %s twice", text)
		}
		out = append(out, text)
	}
	return out, nil
}

// i18nLocales checks the list of locale tags at [i18n].key and answers each
// tag canonical: a well-formed BCP 47 tag, listed once, that is not root
// (und) and that CLDR has data for, on its own or through a parent other
// than root. Each error is at the line that writes its tag.
func i18nLocales(raw *[]string, key string, data []byte, sourceLabel string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	if len(*raw) == 0 {
		return nil, manifestAt(sourceLabel, data, "", "i18n", key).with("[i18n] %s is empty; list the locales the program carries, or remove it to carry every locale", key)
	}
	full := cldrfull.Full()
	seen := map[string]string{}
	out := make([]string, 0, len(*raw))
	for _, tag := range *raw {
		at := manifestAt(sourceLabel, data, tag, "i18n", key)
		a := stdlocale.HostParse(tag)
		if a.Kind != stdlocale.KindOK {
			return nil, at.with("[i18n] %s: %q is not a locale tag%s", key, tag, localeHint(a))
		}
		if prev, dup := seen[a.Value]; dup {
			return nil, at.with("[i18n] %s lists %s twice (%q and %q)", key, a.Value, prev, tag)
		}
		if a.Value == "und" {
			return nil, at.with("[i18n] %s: und is CLDR's root, which every program carries, not a locale to list", key)
		}
		known := false
		for _, t := range stdlocale.Fallbacks(full, a.Value) {
			if full.HasLocale(t) {
				known = true
				break
			}
		}
		if !known {
			return nil, at.with("[i18n] %s: CLDR %s has no data for %s or a parent of it", key, full.Version, a.Value)
		}
		seen[a.Value] = tag
		out = append(out, a.Value)
	}
	return out, nil
}

// i18nMessageSettings reads [i18n]'s settings for phrases into m:
// source_locale, a tag that locales lists when it lists any; partial_locales,
// tags that locales does not list; and catalogs, a relative directory that
// stays under the package root.
func i18nMessageSettings(m *Manifest, raw manifestFile, data []byte, sourceLabel string) error {
	at := func(value, key string) *ManifestError { return manifestAt(sourceLabel, data, value, "i18n", key) }
	if raw.I18n.SourceLocale != nil {
		tags, err := i18nLocales(&[]string{*raw.I18n.SourceLocale}, "source_locale", data, sourceLabel)
		if err != nil {
			return err
		}
		m.SourceLocale = tags[0]
	}
	if raw.I18n.PartialLocales != nil {
		if m.Locales == nil {
			return at("", "partial_locales").with("[i18n] partial_locales needs locales: list the locales every phrase is translated into")
		}
		if len(*raw.I18n.PartialLocales) > 0 {
			tags, err := i18nLocales(raw.I18n.PartialLocales, "partial_locales", data, sourceLabel)
			if err != nil {
				return err
			}
			for _, t := range tags {
				if slices.Contains(m.Locales, t) {
					return at(t, "partial_locales").with("[i18n] lists %s in both locales and partial_locales; a locale is complete or partial", t)
				}
			}
			m.PartialLocales = tags
		}
	}
	if raw.I18n.WebEmbedded != nil {
		if m.Locales == nil {
			return at("", "web_embedded").with("[i18n] web_embedded needs locales: list the locales the program carries")
		}
		tags := []string{}
		if len(*raw.I18n.WebEmbedded) > 0 {
			var err error
			if tags, err = i18nLocales(raw.I18n.WebEmbedded, "web_embedded", data, sourceLabel); err != nil {
				return err
			}
		}
		for _, t := range tags {
			if !slices.Contains(m.CarriedLocales(), t) {
				return at(t, "web_embedded").with("[i18n] web_embedded lists %s, which neither locales nor partial_locales lists", t)
			}
		}
		m.WebEmbedded = tags
	}
	if raw.I18n.Catalogs != nil {
		dir := filepath.ToSlash(filepath.Clean(*raw.I18n.Catalogs))
		if *raw.I18n.Catalogs == "" || rootedPath(dir) || dir == "." || dir == ".." || strings.HasPrefix(dir, "../") {
			return at("", "catalogs").with("[i18n] catalogs %q is not a directory under the package root", *raw.I18n.Catalogs)
		}
		m.Catalogs = dir
	}
	return nil
}

// rootedPath reports whether p, a manifest path in slash form, names a
// place from a root rather than from the package root: `/abs` or a drive
// (`C:/x`, `C:x`). A nomi.toml means the same on every system, so a path
// that is absolute on any of them is refused on all of them; Windows alone
// would read `/abs` as under the current drive.
func rootedPath(p string) bool {
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return true
	}
	return len(p) >= 2 && p[1] == ':' && ('a' <= p[0] && p[0] <= 'z' || 'A' <= p[0] && p[0] <= 'Z')
}

func localeHint(a stdlocale.LocaleAttempt) string {
	if a.Kind == stdlocale.KindUnderscore {
		return fmt.Sprintf("; write %q", a.Detail)
	}
	return ""
}

// LoadManifest reads nomi.toml from the given directory, as an editor
// holds it when one does (ReadManifest), and returns the parsed manifest. Returns ErrManifestMissing if the file doesn't
// exist. Returns a descriptive error if the file is malformed or
// fails the basic schema invariants. See ParseManifestData for the
// validation rules.
func LoadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, "nomi.toml")
	data, err := ReadManifest(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrManifestMissing
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return ParseManifestIn(dir, data)
}

// ParseManifestIn parses data as the nomi.toml of the directory dir, as
// LoadManifest parses the file there: a table named for a package data's
// [dependencies] names is its tools'.
func ParseManifestIn(dir string, data []byte) (*Manifest, error) {
	return parseManifestData(data, filepath.Join(dir, "nomi.toml"), func() bool { return isStdlibManifestDir(dir) }, func() []string { return toolTables(data) })
}

// ParseManifestData parses nomi.toml content from a byte slice and
// validates the basic schema invariants. The `sourceLabel` is the
// diagnostic prefix used in error messages — typically the file path
// when the data came from disk, or a synthetic label like
// "<virtual nomi.toml>" when staged in memory by the tour playground.
//
// Validation rules:
//
//   - [package].name is required and must not be whitespace-only.
//   - [package].name must not be "std" (reserved for the standard library).
//   - [package].entry_points entries must be non-empty strings.
//   - [package].entry_points must not contain duplicates.
//   - Every table and key is one manifestFile declares (unknownManifestKey),
//     except a top-level table named for a package the project requires,
//     which belongs to that package's tools; a manifest given as bytes has
//     no project, so none.
func ParseManifestData(data []byte, sourceLabel string) (*Manifest, error) {
	return parseManifestData(data, sourceLabel, nil, nil)
}

// parseManifestData is ParseManifestData. isStd answers whether the
// manifest is this nomi's standard library's, which alone may take the
// name std, asked only of a manifest that does; nil answers no. tools
// answers the names of the tables that belong to other packages' tools
// (toolTables), asked only when the manifest has a table it does not know.
func parseManifestData(data []byte, sourceLabel string, isStd func() bool, tools func() []string) (*Manifest, error) {
	var raw manifestFile
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, notTOML(sourceLabel, err)
	}
	// The package's [package] and [dependencies], as the package graph reads
	// them. A package that needs a later nomi is refused before anything
	// else is checked, since it may write the rest in a form this nomi
	// does not read; any other problem there is reported in its turn.
	mf, mfErr := pkgcache.ParseManifest(data)
	if tooOld := (*pkgcache.NomiVersionError)(nil); errors.As(mfErr, &tooOld) {
		tooOld.Path = sourceLabel
		return nil, tooOld
	}
	if err := unknownManifestKey(data, md.Undecoded(), md, sourceLabel, tools, packageSchema); err != nil {
		return nil, err
	}
	name := manifestAt(sourceLabel, data, "", "package", "name")
	if name.Line == 0 {
		name.Line = ManifestKeyLine(data, "package")
	}
	if strings.TrimSpace(raw.Package.Name) == "" {
		return nil, name.with("[package] name is required (non-empty, non-whitespace)")
	}
	if raw.Package.Name == "std" && (isStd == nil || !isStd()) {
		return nil, name.with("[package] name %q is reserved for the standard library; choose a different name", raw.Package.Name)
	}
	seen := map[string]bool{}
	for _, e := range raw.Package.EntryPoints {
		at := manifestAt(sourceLabel, data, e, "package", "entry_points")
		if e == "" {
			return nil, at.with("entry_points contains an empty string — every entry must name a file")
		}
		if seen[e] {
			return nil, at.with("entry_points contains duplicate %q", e)
		}
		seen[e] = true
	}
	locales, err := i18nLocales(raw.I18n.Locales, "locales", data, sourceLabel)
	if err != nil {
		return nil, err
	}
	if mfErr != nil {
		var problem *pkgcache.ManifestProblem
		if errors.As(mfErr, &problem) {
			return nil, placeManifestProblem(sourceLabel, data, problem)
		}
		return nil, fmt.Errorf("parse %s: %w", sourceLabel, mfErr)
	}
	m := &Manifest{
		Name:         raw.Package.Name,
		Dependencies: mf.Requires,

		EntryPoints:  append([]string(nil), raw.Package.EntryPoints...),
		Locales:      locales,
		SourceLocale: DefaultSourceLocale,
		Catalogs:     DefaultCatalogs,
	}
	if err := i18nMessageSettings(m, raw, data, sourceLabel); err != nil {
		return nil, err
	}
	if m.CustomCurrencies, err = customCurrencies(raw.Money.CustomCurrencies, data, sourceLabel); err != nil {
		return nil, err
	}
	if m.Effects, err = effectLists(raw, mf.Requires, data, sourceLabel); err != nil {
		return nil, err
	}
	return m, nil
}

// IsForeignStdlibDir reports whether dir is the root of a standard library
// other than this binary's: its nomi.toml names the package "std", and it is
// not StdlibPath(). Another Nomi checkout's std is one.
func IsForeignStdlibDir(dir string) bool {
	data, err := ReadManifest(dir)
	if err != nil {
		return false
	}
	var raw manifestFile
	if err := toml.Unmarshal(data, &raw); err != nil {
		return false
	}
	return raw.Package.Name == "std" && !isStdlibManifestDir(dir)
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
