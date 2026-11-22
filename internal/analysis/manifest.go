package analysis

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/modcache"
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
//	path = "..."
//	entry_points = [...]
//
//	[dependencies]
//	name = { module = "...", version = "..." }
type Manifest struct {
	Name string // [module].name
	// Path is [module].path, the module path the module is published
	// under, or "".
	Path string
	// Dependencies is the [dependencies] table as modcache.ParseModuleFile
	// reads it, sorted by name: each module the module's files import, by
	// the name they import it by.
	Dependencies []modcache.Requirement
	// EntryPoints is [module].entry_points: paths under the module root,
	// .nomi implicit. The module's tools are entry points too without being
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
	// Effects is the [effects.self] and [effects.deps]
	// tables: the effects the module's own code, and the code of the
	// modules it depends on, may have. Nil when the manifest has neither
	// table, which holds nothing.
	Effects *Effects
}

// Effects is nomi.toml's effects tables: the effects the
// module's own files, and the files of the modules it depends on, may have
// (vmhost's EffectCallsNotAllowed). A std list's names are `std::` and a
// std module as an import writes it (`std::fs`), or `std::startup`; a list
// of dependencies holds their names as [dependencies] writes them. The parser
// checks each entry's form; whether a std module has functions with
// effects and a module is one the project requires is the program's to
// answer (vmhost).
//
// When the project has an [effects.deps] table, a dependency's own
// [effects.self] holds the dependency too: its Std is the list it
// declares for its own files, and its Modules the entries it declares for
// its own modules. Its StdInTests, EntriesRequired and Deps are for its
// own builds and are not read when it is a dependency.
type Effects struct {
	// Self is [effects.self] with [effects.self.modules], nil
	// when the manifest leaves both out, which holds the module's own code
	// to nothing.
	Self *SelfEffects
	// Deps is [effects.deps] with [effects.deps.modules], nil
	// when the manifest leaves both out, which holds the dependencies to
	// nothing.
	Deps *DepsEffects
}

// EntryLists is the lists that hold entries, each a table under
// [effects] keyed by the code an entry is for:
// [effects.self.modules] by a module of the project as its own code
// imports it, and [effects.deps.modules] by a dependency's name as
// [dependencies] names it.
var EntryLists = []string{ListSelfModules, ListDepsModules}

// Entries is the table of entries list names, one of EntryLists: each
// entry's key and its std names, nil when c leaves the table out.
func (c *Effects) Entries(list string) map[string][]string {
	switch {
	case list == ListSelfModules && c.Self != nil:
		return c.Self.Modules
	case list == ListDepsModules && c.Deps != nil:
		return c.Deps.Modules
	}
	return nil
}

// EntryKeyPath is the path of the key of list's entry for key, as
// ManifestKeyLine reads it: `effects`, the list's table, and the key.
func EntryKeyPath(list, key string) []string {
	return append(append([]string{"effects"}, strings.Split(list, ".")...), key)
}

// SelfEffects is [effects.self] and [effects.self.modules]:
// the effects the calls written in the module's own files may have.
type SelfEffects struct {
	// Std is the effects its own files may have, by std name. Empty when
	// the table leaves it out, which allows no std call.
	Std []string
	// StdInTests is added to Std when the roots are test cases.
	StdInTests []string
	// EntriesRequired is entries_required: every module of the project
	// whose files the program loads must have an entry under Modules.
	EntriesRequired bool
	// Modules is [effects.self.modules]: for a module of the project,
	// by its import path as the project's own code writes it (`pricing`,
	// `reports::daily`, `pricing_test`), the std names that module's files
	// may have, within Std. A module with no entry is held by Std alone.
	// Nil when the manifest has no such table.
	Modules map[string][]string
}

// DepsEffects is [effects.deps] and [effects.deps.modules]:
// the effects the calls written in the files of the modules the project
// depends on, directly or transitively, may have.
type DepsEffects struct {
	// EntriesRequired is entries_required: every dependency whose Nomi
	// files the program loads must have an entry under Modules or an
	// [effects.self] of its own.
	EntriesRequired bool
	// Std is the most any dependency's files may have, a ceiling over
	// every entry. Nil when the table leaves it out, which sets no
	// ceiling; empty, not nil, for `std = []`, which allows nothing.
	Std []string
	// StdInTests is added to Std when the roots are test cases. The table
	// sets it only beside Std.
	StdInTests []string
	// Modules is [effects.deps.modules]: for a module the project
	// needs, by its name as [dependencies] names it, the std names that
	// module's own files may have. A dependency with no entry is held by
	// Std and its own [effects.self] alone.
	Modules map[string][]string
}

// StdFor is the std names s allows its own files, with StdInTests when
// tests is set.
func (s *SelfEffects) StdFor(tests bool) []string {
	if tests {
		return append(slices.Clone(s.Std), s.StdInTests...)
	}
	return s.Std
}

// StdFor is d's ceiling, with StdInTests when tests is set; nil when d sets
// none.
func (d *DepsEffects) StdFor(tests bool) []string {
	if tests && d.Std != nil {
		return append(slices.Clone(d.Std), d.StdInTests...)
	}
	return d.Std
}

// ToolsDir is the directory under a module's root that holds its tools,
// the programs `nomi tool <module> <name>` runs.
const ToolsDir = "tools"

// IsToolFile reports whether modRel, a file's module-relative path with
// "/" separators and without .nomi, is one of its module's tools: a file
// under tools/ that is not a test file and is in no internal/ directory,
// where a tool's helpers go. Every tool is an entry point.
func IsToolFile(modRel string) bool {
	rest, ok := strings.CutPrefix(modRel, ToolsDir+"/")
	if !ok || rest == "" || strings.HasSuffix(rest, "_test") {
		return false
	}
	return !slices.Contains(strings.Split(rest, "/")[:strings.Count(rest, "/")], "internal")
}

// IsEntry reports whether modRel, a file's module-relative path without
// .nomi, is an entry point of the module: listed in entry_points, or a
// tool (IsToolFile).
func (m *Manifest) IsEntry(modRel string) bool {
	return IsToolFile(modRel) || slices.Contains(m.EntryPoints, modRel)
}

// Entries is every entry point of the module rooted at root: entry_points
// in the manifest's order, then each tool under root's tools/ directory
// that entry_points does not list, by path. Each is module-relative, as
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
		Path        string   `toml:"path"`
		EntryPoints []string `toml:"entry_points"`
	} `toml:"module"`
	Dependencies map[string]modcache.DependencyEntry `toml:"dependencies"`
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
		Self *struct {
			EntriesRequired bool                `toml:"entries_required"`
			Std             []string            `toml:"std"`
			StdInTests      []string            `toml:"std_in_tests"`
			Modules         map[string][]string `toml:"modules"`
		} "toml:\"self\" keys:\"`std` lists the effects this module's own code may have, by std module, and `std_in_tests` those its tests may also have, `entries_required` makes every module of the project have an entry, and [effects.self.modules] holds an entry for each of its modules\""
		Deps *struct {
			EntriesRequired bool                `toml:"entries_required"`
			Std             []string            `toml:"std"`
			StdInTests      []string            `toml:"std_in_tests"`
			Modules         map[string][]string `toml:"modules"`
		} "toml:\"deps\" keys:\"`std` and `std_in_tests` list the most effects any dependency may have, `entries_required` makes every dependency have an entry, and [effects.deps.modules] holds an entry for each dependency\""
	} "toml:\"effects\" keys:\"it holds two tables: [effects.self] is the effects this module's own code may have, and [effects.deps] the effects the code of the modules it depends on may have\""
}

// effectLists checks [effects.self] and [effects.deps]: each
// std list's names `std::<module>` (effects.NameProblem), each listed
// once in its list, deps' std_in_tests only beside its std, and each
// module path under [effects.deps.modules] non-empty. A `host:` name is a
// function an embedding Go program supplies, which a manifest cannot
// allow.
func effectLists(raw manifestFile, data []byte, sourceLabel string) (*Effects, error) {
	if raw.Effects == nil || raw.Effects.Self == nil && raw.Effects.Deps == nil {
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
	// entries is the table of entries list holds, raw as decoded: each key
	// non-empty, and each entry a list of std names. writeKey says how a
	// key is written, for the error at an empty one.
	entries := func(list string, raw map[string][]string, writeKey string) (map[string][]string, error) {
		table := "effects." + list
		out := make(map[string][]string, len(raw))
		keys := make([]string, 0, len(raw))
		for key := range raw {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return nil, at(ManifestKeyLine(data, EntryKeyPath(list, key)...)).with("[%s] has an empty key; %s", table, writeKey)
			}
			var err error
			if out[key], err = check(table, fmt.Sprintf("%q", key), raw[key], EntryKeyPath(list, key)...); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	c := &Effects{}
	var err error
	if s := raw.Effects.Self; s != nil {
		c.Self = &SelfEffects{EntriesRequired: s.EntriesRequired}
		if s.Modules != nil {
			if c.Self.Modules, err = entries(ListSelfModules, s.Modules, "write a module as the project's own code imports it, such as \"pricing\""); err != nil {
				return nil, err
			}
		}
		if c.Self.Std, err = check("effects.self", "std", s.Std, "effects", "self", "std"); err != nil {
			return nil, err
		}
		if c.Self.StdInTests, err = check("effects.self", "std_in_tests", s.StdInTests, "effects", "self", "std_in_tests"); err != nil {
			return nil, err
		}
	}
	if d := raw.Effects.Deps; d != nil {
		c.Deps = &DepsEffects{EntriesRequired: d.EntriesRequired}
		if d.Std != nil {
			if c.Deps.Std, err = check("effects.deps", "std", d.Std, "effects", "deps", "std"); err != nil {
				return nil, err
			}
		}
		if d.StdInTests != nil && d.Std == nil {
			return nil, at(ManifestKeyLine(data, "effects", "deps", "std_in_tests")).with(
				"[effects.deps] std_in_tests adds to std, which the table leaves out; with no std, no ceiling holds the dependencies")
		}
		if c.Deps.StdInTests, err = check("effects.deps", "std_in_tests", d.StdInTests, "effects", "deps", "std_in_tests"); err != nil {
			return nil, err
		}
		if c.Deps.Modules, err = entries(ListDepsModules, d.Modules, "write a dependency's name as [dependencies] names it"); err != nil {
			return nil, err
		}
	}
	return c, nil
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
		a := stdlocale.HostParse(tag)
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

// i18nMessageSettings reads [i18n]'s settings for phrases into m:
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
			return fmt.Errorf("%s: [i18n] partial_locales needs locales: list the locales every phrase is translated into", sourceLabel)
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
		if *raw.I18n.Catalogs == "" || rootedPath(dir) || dir == "." || dir == ".." || strings.HasPrefix(dir, "../") {
			return fmt.Errorf("%s: [i18n] catalogs %q is not a directory under the module root", sourceLabel, *raw.I18n.Catalogs)
		}
		m.Catalogs = dir
	}
	return nil
}

// rootedPath reports whether p, a manifest path in slash form, names a
// place from a root rather than from the module root: `/abs` or a drive
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
// LoadManifest parses the file there: a table named for a module data's
// [dependencies] names is its tools'.
func ParseManifestIn(dir string, data []byte) (*Manifest, error) {
	return parseManifestData(data, filepath.Join(dir, "nomi.toml"), isStdlibManifestDir(dir), func() []string { return toolTables(data) })
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
//   - Every table and key is one manifestFile declares (unknownManifestKey),
//     except a top-level table named for a module the project requires,
//     which belongs to that module's tools; a manifest given as bytes has
//     no project, so none.
func ParseManifestData(data []byte, sourceLabel string) (*Manifest, error) {
	return parseManifestData(data, sourceLabel, false, nil)
}

// parseManifestData is ParseManifestData. tools answers the names of the
// tables that belong to other modules' tools (toolTables), asked only when
// the manifest has a table it does not know.
func parseManifestData(data []byte, sourceLabel string, allowStdName bool, tools func() []string) (*Manifest, error) {
	var raw manifestFile
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", sourceLabel, err)
	}
	if err := unknownManifestKey(data, md.Undecoded(), md, sourceLabel, tools); err != nil {
		return nil, err
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
	mf, err := modcache.ParseModuleFile(data)
	if err != nil {
		var problem *modcache.ManifestProblem
		if errors.As(err, &problem) {
			return nil, (&ManifestError{Path: sourceLabel, Line: ManifestKeyLine(data, problem.Key...)}).with("%s", problem.Error())
		}
		return nil, fmt.Errorf("parse %s: %w", sourceLabel, err)
	}
	m := &Manifest{
		Name:         raw.Module.Name,
		Path:         mf.Path,
		Dependencies: mf.Requires,
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
	if m.Effects, err = effectLists(raw, data, sourceLabel); err != nil {
		return nil, err
	}
	return m, nil
}

// IsForeignStdlibDir reports whether dir is the root of a standard library
// other than this binary's: its nomi.toml names the module "std", and it is
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
