package vmhost

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/suggest"
)

// Effects is nomi.toml's effects tables: the effects a
// project's own code, and the code of the modules it depends on, may have
// (EffectCallsNotAllowed).
type Effects = analysis.Effects

// SelfEffects is [effects.self]: the effects the project's own
// files may have.
type SelfEffects = analysis.SelfEffects

// DependenciesEffects is [effects.dependencies] with the effects keys of
// the [dependencies] entries: the effects the files of the packages the
// project depends on may have.
type DependenciesEffects = analysis.DependenciesEffects

// manifestPath is the program's nomi.toml, where a diagnostic about its
// lists goes, and its text, nil for a manifest staged in memory.
func (p *Program) manifestPath() (string, []byte) {
	if p.cfg.manifest != nil || p.prog.Root == "" {
		return "nomi.toml", nil
	}
	data, _ := analysis.ReadManifest(p.prog.Root)
	return filepath.Join(p.prog.Root, "nomi.toml"), data
}

// effectListProblems is a diagnostic for each name of the program's
// effects lists, and of the std lists each dependency's own [effects.self]
// declares, its entries' among them, that names nothing a list can allow:
// a name that is no std module, and a key under [effects.self.modules] that
// is no module of the project (ownModuleProblem). A name that is valid and
// never reached is not a problem. With entries_required under
// [effects.self], each module of the project the program holds
// (ownModules) with no entry is one too, at that key, and under
// [effects.dependencies] each [dependencies] entry through which the
// program loads Nomi files (dependencyEntries) with no `effects` key and no
// [effects.self] in its package's own nomi.toml, each with the fix that
// writes its entry (entryFix). A dependency's own table is read only when
// the project holds its dependencies (Effects.Dependencies).
func (p *Program) effectListProblems() Diagnostics {
	c := p.effects
	if c == nil {
		return nil
	}
	path, data := p.manifestPath()
	var ds Diagnostics
	at := func(path string, data []byte, line int, message string, hints ...string) {
		src := string(data)
		if data == nil {
			src = "\n"
		}
		d := frontend.NewDiagnostic(path, src, line, 0, message)
		d.Hints = append(d.Hints, hints...)
		ds = append(ds, d)
	}
	checkNamesIn := func(path string, data []byte, table, list string, names []string, key ...string) {
		for _, name := range names {
			if problem, hint := effects.UnknownStdName(name); problem != "" {
				at(path, data, analysis.ManifestEntryLine(data, name, key...), fmt.Sprintf("[%s] %s names %s, %s", table, list, name, problem), hint)
			}
		}
	}
	checkNames := func(table, list string, names []string, key ...string) {
		checkNamesIn(path, data, table, list, names, key...)
	}
	// required is the error of a key entries_required under table finds
	// with no entry in list, with the fix that writes its entry.
	var tightest *Effects
	required := func(table, list, key, message string, hints ...string) {
		at(path, data, analysis.ManifestKeyLine(data, append(strings.Split(table, "."), "entries_required")...),
			fmt.Sprintf("[%s] entries_required: %s", table, message), hints...)
		ds[len(ds)-1].Fix = p.entryFix(list, key, &tightest)
	}
	if s := c.Self; s != nil {
		checkNames(analysis.ListSelf, "effects", s.Effects, "effects", "self", "effects")
		checkNames(analysis.ListSelf, "effects_in_tests", s.EffectsInTests, "effects", "self", "effects_in_tests")
		var modules []string
		for _, key := range slices.Sorted(maps.Keys(s.Modules)) {
			if modules == nil {
				modules = p.projectModules()
			}
			keyPath := analysis.EntryKeyPath(analysis.ListSelfModules, key)
			if problem, hint := ownModuleProblem(key, modules); problem != "" {
				at(path, data, analysis.ManifestKeyLine(data, keyPath...),
					fmt.Sprintf("[%s] names %s, %s", analysis.ListSelfModules, key, problem), hint)
				continue
			}
			checkNames(analysis.ListSelfModules, fmt.Sprintf("%q", key), s.Modules[key], keyPath...)
		}
		if s.EntriesRequired {
			for _, m := range p.ownModules() {
				if _, listed := s.Modules[m]; !listed {
					required(analysis.ListSelf, analysis.ListSelfModules, m, m+" has no entry",
						fmt.Sprintf("add %q under [effects.self.modules] in nomi.toml", m))
				}
			}
		}
	}
	d := c.Dependencies
	if d == nil {
		return ds
	}
	checkNames(analysis.ListDependencies, "effects", d.Effects, "effects", "dependencies", "effects")
	checkNames(analysis.ListDependencies, "effects_in_tests", d.EffectsInTests, "effects", "dependencies", "effects_in_tests")
	for _, list := range []string{analysis.ListEntryEffects, analysis.ListEntryEffectsInTests} {
		entries := c.Entries(list)
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			keyPath := analysis.EntryKeyPath(list, key)
			checkNames("dependencies", key+"."+keyPath[2], entries[key], keyPath...)
		}
	}
	for _, m := range p.requiredPackages() {
		if m.declares() {
			ownPath := filepath.Join(m.root, "nomi.toml")
			ownData, _ := analysis.ReadManifest(m.root)
			own := m.manifest.Effects.Self
			checkNamesIn(ownPath, ownData, analysis.ListSelf, "effects", own.Effects, "effects", "self", "effects")
			for _, key := range slices.Sorted(maps.Keys(own.Modules)) {
				checkNamesIn(ownPath, ownData, analysis.ListSelfModules, fmt.Sprintf("%q", key), own.Modules[key], analysis.EntryKeyPath(analysis.ListSelfModules, key)...)
			}
		}
	}
	if d.EntriesRequired {
		for _, key := range p.dependencyEntries() {
			if _, has := d.Entries[key]; has {
				continue
			}
			if m := p.entryPackage(key); m != nil && m.declares() {
				continue
			}
			required(analysis.ListDependencies, analysis.ListEntryEffects, key, fmt.Sprintf("the [dependencies] entry %s has no effects key", key),
				fmt.Sprintf("add effects = [...] to %s under [dependencies] in nomi.toml, or give %s an [effects.self] table in its own nomi.toml", key, key))
		}
	}
	return ds
}

// ownModuleProblem is what is wrong with key, a key under
// [effects.self.modules], when it names none of modules, the
// project's (projectModules), with a hint naming the nearest, or else
// every one; both are "" when it names one.
func ownModuleProblem(key string, modules []string) (problem, hint string) {
	if slices.Contains(modules, key) {
		return "", ""
	}
	hint = "a key is a module of this project as its own code imports it: " + strings.Join(modules, ", ")
	if near := suggest.Closest(key, modules); near != "" {
		hint = fmt.Sprintf("did you mean %q?", near)
	}
	return "which is no module of this project", hint
}

// entryFix is the fix of a key entries_required finds with no entry in
// list, [effects.self.modules] or a [dependencies] entry's `effects`: the
// edit to nomi.toml that writes key's entry with what the code it holds
// reaches from the roots `nomi check` holds to the lists (tightestLists),
// as `nomi add` writes a dependency's, and for a dependency its
// `effects_in_tests` too when the tests reach more. tightest holds the
// tightest tables once a fix has walked the program, and is walked once.
// Nil when the program's nomi.toml is staged in memory, or the edit cannot
// be written.
func (p *Program) entryFix(list, key string, tightest **Effects) *parser.Fix {
	path, data := p.manifestPath()
	if data == nil || !filepath.IsAbs(path) {
		return nil
	}
	if *tightest == nil {
		*tightest = tightestLists(p.walks(p.checkedRoots()), p.dependencyEntries(), p.ownModules())
	}
	entry := (*tightest).Entries(list)[key]
	to := p.effects.Clone()
	to.SetEntry(list, key, entry)
	title := fmt.Sprintf("Write %q = [%s] under [%s]", key, tomlNames(entry), list)
	if list == analysis.ListEntryEffects {
		title = fmt.Sprintf("Write effects = [%s] on %s under [dependencies]", tomlNames(entry), key)
		if inTests := (*tightest).Entries(analysis.ListEntryEffectsInTests)[key]; len(inTests) > 0 {
			to.SetEntry(analysis.ListEntryEffectsInTests, key, inTests)
			title += fmt.Sprintf(", with effects_in_tests = [%s]", tomlNames(inTests))
		}
	}
	fix, err := analysis.EffectsFix(path, data, title, to)
	if err != nil {
		return nil
	}
	return fix
}

// tomlNames is names quoted and joined as a TOML list holds them.
func tomlNames(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}

// dependencyEntries is the key of each [dependencies] entry of the project
// through which the program loads Nomi files (requiredPackages'
// requiredPackage.through), sorted: the entries an `effects` key holds
// code for, which entries_required asks one of.
func (p *Program) dependencyEntries() []string {
	var out []string
	for _, m := range p.requiredPackages() {
		for _, key := range m.through {
			if !slices.Contains(out, key) {
				out = append(out, key)
			}
		}
	}
	slices.Sort(out)
	return out
}

// entryPackage is the package the project's [dependencies] entry key names,
// or nil when the package graph does not hold it.
func (p *Program) entryPackage(key string) *requiredPackage {
	if p.prog.Root == "" {
		return nil
	}
	g, err := pkgcache.Load(p.prog.Root)
	if err != nil || g == nil {
		return nil
	}
	d, ok := g.Named(key)
	if !ok || d.Dir == "" {
		return nil
	}
	return p.packageAt(filepath.Join(d.Dir, "nomi.toml"))
}

// hostEffects is the list the host holds the program to (WithEffects),
// as effects tables that hold the whole program, or nil when the host
// gave none: Self and Dependencies' ceiling are WithEffects' names.
func (p *Program) hostEffects() *Effects {
	if !p.cfg.held {
		return nil
	}
	return &Effects{
		Self:         &analysis.SelfEffects{Effects: p.cfg.allowed},
		Dependencies: &analysis.DependenciesEffects{Effects: append([]string{}, p.cfg.allowed...)},
	}
}

// hostEffectProblem is what is wrong with the names the host passed to
// WithEffects, or nil. They are held to what an entry of nomi.toml's std
// lists is held to, with the same words (effects.CheckHostList), except
// that `host:` and a key a table passed with WithHosts answers is a name
// too, which only the host can allow.
func (p *Program) hostEffectProblem() error {
	if !p.cfg.held {
		return nil
	}
	return effects.CheckHostList("vmhost.WithEffects", "vmhost.WithHosts", p.cfg.allowed,
		func() (map[string]bool, error) { return hostKeys(p.cfg.hosts) })
}
