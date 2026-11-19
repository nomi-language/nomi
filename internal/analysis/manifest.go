package analysis

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/internal/capability"
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
	Name string // [module].name
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
	// Capabilities is the [capabilities.self] and [capabilities.deps]
	// tables: what the module's own code, and the code of the modules it
	// depends on, may reach outside the machine. Nil when the manifest has
	// neither table, which holds nothing.
	Capabilities *Capabilities
}

// Capabilities is nomi.toml's capability tables: the outside calls the
// module's own files, and the files of the modules it depends on, may make
// (vmhost's OutsideCallsNotAllowed). A std list's names are `std::` and a
// std module as an import writes it (`std::fs`), or `std::startup`; a list
// of dependencies holds module paths as go.mod writes them. The parser
// checks each entry's form; whether a std module has outside functions and
// a module is one the project requires is the program's to answer
// (vmhost).
//
// When the project has a [capabilities.deps] table, a dependency's own
// [capabilities.self] holds the dependency too: its Std is the list it
// declares for its own files, and its Go says whether it binds Go. A
// dependency's Go never grants: the project's Deps.Go must list the
// module. Its StdInTests, GoInTests and Deps are for its own builds and
// are not read when it is a dependency.
type Capabilities struct {
	// Self is [capabilities.self], nil when the manifest leaves it out,
	// which holds the module's own code to nothing.
	Self *SelfCapabilities
	// Deps is [capabilities.deps] with [capabilities.deps.modules], nil
	// when the manifest leaves both out, which holds the dependencies to
	// nothing.
	Deps *DepsCapabilities
}

// SelfCapabilities is [capabilities.self]: what the calls written in the
// module's own files may reach.
type SelfCapabilities struct {
	// Std is the std names its own files may reach. Empty when the table
	// leaves it out, which allows no std call.
	Std []string
	// StdInTests is added to Std when the roots are test cases.
	StdInTests []string
	// Go says whether its own files may bind Go, and GoInTests whether
	// they may when the roots are test cases. Both are false when left
	// out.
	Go, GoInTests bool
}

// DepsCapabilities is [capabilities.deps] and [capabilities.deps.modules]:
// what the calls written in the files of the modules the project depends
// on, directly or transitively, may reach.
type DepsCapabilities struct {
	// EntriesRequired is entries_required: every dependency whose Nomi
	// files the program loads must have an entry under Modules or a
	// [capabilities.self] of its own.
	EntriesRequired bool
	// Std is the most any dependency's files may reach, a ceiling over
	// every entry. Nil when the table leaves it out, which sets no
	// ceiling; empty, not nil, for `std = []`, which allows nothing.
	Std []string
	// StdInTests is added to Std when the roots are test cases. The table
	// sets it only beside Std.
	StdInTests []string
	// Go is the dependencies whose Nomi code may bind Go, each by its path
	// as go.mod writes it. Empty when the table leaves it out, which lets
	// no dependency bind Go.
	Go []string
	// GoInTests is added to Go when the roots are test cases.
	GoInTests []string
	// Modules is [capabilities.deps.modules]: for a module the project
	// requires, by its path as go.mod writes it, the std names that
	// module's own files may reach. A dependency with no entry is held by
	// Std and its own [capabilities.self] alone.
	Modules map[string][]string
}

// StdFor is the std names s allows its own files, with StdInTests when
// tests is set.
func (s *SelfCapabilities) StdFor(tests bool) []string {
	if tests {
		return append(slices.Clone(s.Std), s.StdInTests...)
	}
	return s.Std
}

// GoFor reports whether s lets its own files bind Go, with GoInTests when
// tests is set.
func (s *SelfCapabilities) GoFor(tests bool) bool {
	return s.Go || tests && s.GoInTests
}

// StdFor is d's ceiling, with StdInTests when tests is set; nil when d sets
// none.
func (d *DepsCapabilities) StdFor(tests bool) []string {
	if tests && d.Std != nil {
		return append(slices.Clone(d.Std), d.StdInTests...)
	}
	return d.Std
}

// GoFor is the dependencies d lets bind Go, with GoInTests when tests is
// set.
func (d *DepsCapabilities) GoFor(tests bool) []string {
	if tests {
		return append(slices.Clone(d.Go), d.GoInTests...)
	}
	return d.Go
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
	Capabilities *struct {
		Self *struct {
			Std        []string `toml:"std"`
			StdInTests []string `toml:"std_in_tests"`
			Go         any      `toml:"go"`
			GoInTests  any      `toml:"go_in_tests"`
		} "toml:\"self\" keys:\"`std` lists the std modules this module's own code may reach and `std_in_tests` those its tests may also reach, and `go` and `go_in_tests` are true or false: whether its own code, or its tests, may bind Go\""
		Deps *struct {
			EntriesRequired bool                `toml:"entries_required"`
			Std             []string            `toml:"std"`
			StdInTests      []string            `toml:"std_in_tests"`
			Go              any                 `toml:"go"`
			GoInTests       any                 `toml:"go_in_tests"`
			Modules         map[string][]string `toml:"modules"`
		} "toml:\"deps\" keys:\"`std` and `std_in_tests` list the most std modules any dependency may reach, `go` and `go_in_tests` the dependencies whose Go may run, `entries_required` makes every dependency have an entry, and [capabilities.deps.modules] holds an entry for each dependency\""
	} "toml:\"capabilities\" keys:\"it holds two tables: [capabilities.self] is what this module's own code may reach, and [capabilities.deps] what the code of the modules it depends on may reach\""
}

// capabilityLists checks [capabilities.self] and [capabilities.deps]: each
// std list's names `std::<module>` (capability.NameProblem), each list of
// dependencies' module paths (capability.ModuleProblem), each listed once
// in its list, self's go and go_in_tests true or false, deps' go and
// go_in_tests lists, deps' std_in_tests only beside its std, and each
// module path under [capabilities.deps.modules] non-empty. A `go:` name in
// a std list is an error that says how Go is allowed, and a `host:` name
// is a function an embedding Go program supplies, which a manifest cannot
// allow.
func capabilityLists(raw manifestFile, data []byte, sourceLabel string) (*Capabilities, error) {
	if raw.Capabilities == nil || raw.Capabilities.Self == nil && raw.Capabilities.Deps == nil {
		return nil, nil
	}
	at := func(line int) *ManifestError { return &ManifestError{Path: sourceLabel, Line: line} }
	check := func(problemOf func(string) string, table, list string, names []string, path ...string) ([]string, error) {
		seen := map[string]bool{}
		out := []string{}
		for _, name := range names {
			at := at(ManifestEntryLine(data, name, path...))
			if problem := problemOf(name); problem != "" {
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
	stdName := func(goFix string) func(string) string {
		return func(name string) string { return capability.NameProblem(name, goFix) }
	}
	c := &Capabilities{}
	var err error
	if s := raw.Capabilities.Self; s != nil {
		c.Self = &SelfCapabilities{}
		std := stdName("Go is allowed by module, not by package: set go = true under [capabilities.self] to let this module's own code bind Go")
		if c.Self.Std, err = check(std, "capabilities.self", "std", s.Std, "capabilities", "self", "std"); err != nil {
			return nil, err
		}
		if c.Self.StdInTests, err = check(std, "capabilities.self", "std_in_tests", s.StdInTests, "capabilities", "self", "std_in_tests"); err != nil {
			return nil, err
		}
		for _, key := range []struct {
			name  string
			value any
			into  *bool
		}{{"go", s.Go, &c.Self.Go}, {"go_in_tests", s.GoInTests, &c.Self.GoInTests}} {
			if key.value == nil {
				continue
			}
			on, ok := key.value.(bool)
			if !ok {
				return nil, at(ManifestKeyLine(data, "capabilities", "self", key.name)).with(
					"[capabilities.self] %s is true or false: whether this module's own code may bind Go; the dependencies whose Go may run are listed in go under [capabilities.deps]", key.name)
			}
			*key.into = on
		}
	}
	if d := raw.Capabilities.Deps; d != nil {
		c.Deps = &DepsCapabilities{EntriesRequired: d.EntriesRequired, Modules: map[string][]string{}}
		std := stdName("Go is allowed by module, not by package: list the dependency that binds it in go under [capabilities.deps]")
		if d.Std != nil {
			if c.Deps.Std, err = check(std, "capabilities.deps", "std", d.Std, "capabilities", "deps", "std"); err != nil {
				return nil, err
			}
		}
		if d.StdInTests != nil && d.Std == nil {
			return nil, at(ManifestKeyLine(data, "capabilities", "deps", "std_in_tests")).with(
				"[capabilities.deps] std_in_tests adds to std, which the table leaves out; with no std, no ceiling holds the dependencies")
		}
		if c.Deps.StdInTests, err = check(std, "capabilities.deps", "std_in_tests", d.StdInTests, "capabilities", "deps", "std_in_tests"); err != nil {
			return nil, err
		}
		for _, key := range []struct {
			name  string
			value any
			into  *[]string
		}{{"go", d.Go, &c.Deps.Go}, {"go_in_tests", d.GoInTests, &c.Deps.GoInTests}} {
			names, ok := stringList(key.value)
			if !ok {
				return nil, at(ManifestKeyLine(data, "capabilities", "deps", key.name)).with(
					"[capabilities.deps] %s lists the dependencies whose Go may run, by module path; whether this module's own code may bind Go is go under [capabilities.self]", key.name)
			}
			if *key.into, err = check(capability.ModuleProblem, "capabilities.deps", key.name, names, "capabilities", "deps", key.name); err != nil {
				return nil, err
			}
		}
		paths := make([]string, 0, len(d.Modules))
		for path := range d.Modules {
			paths = append(paths, path)
		}
		slices.Sort(paths)
		for _, path := range paths {
			if strings.TrimSpace(path) == "" {
				return nil, at(ManifestKeyLine(data, "capabilities", "deps", "modules", path)).with(
					"[capabilities.deps.modules] has an empty module path; write a module's path as go.mod requires it")
			}
			if c.Deps.Modules[path], err = check(std, "capabilities.deps.modules", fmt.Sprintf("%q", path), d.Modules[path], "capabilities", "deps", "modules", path); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

// stringList is value, a TOML value decoded into an any, as a list of
// strings: ok when it is one, or when it is nil, which is no key.
func stringList(value any) ([]string, bool) {
	if value == nil {
		return nil, true
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(items))
	for i, item := range items {
		if out[i], ok = item.(string); !ok {
			return nil, false
		}
	}
	return out, true
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
// LoadManifest parses the file there: a table named for a module the
// project requires is its tools'.
func ParseManifestIn(dir string, data []byte) (*Manifest, error) {
	return parseManifestData(data, filepath.Join(dir, "nomi.toml"), isStdlibManifestDir(dir), func() []string { return toolTables(dir) })
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
	if m.Capabilities, err = capabilityLists(raw, data, sourceLabel); err != nil {
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
