package vmhost

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
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

// DepsEffects is [effects.deps] with [effects.deps.packages]:
// the effects the files of the modules the project depends on may have.
type DepsEffects = analysis.DepsEffects

// manifestPath is the program's nomi.toml, where a diagnostic about its
// lists goes, and its text, nil for a manifest staged in memory.
func (p *Program) manifestPath() (string, []byte) {
	if p.cfg.manifest != nil || p.prog.Root == "" {
		return "nomi.toml", nil
	}
	data, _ := analysis.ReadManifest(p.prog.Root)
	return filepath.Join(p.prog.Root, "nomi.toml"), data
}

// effectListProblems is a diagnostic for each entry of the program's
// effects tables, and of the std lists each dependency's own
// [effects.self] declares, its entries' among them, that names
// nothing a list can allow: a name that is no std module; a key under
// [effects.self.modules] that is no module of the project
// (ownModuleProblem); and a key under [effects.deps.packages] that is not
// a package the project needs (dependencyProblem). A name that is valid and never reached is
// not a problem. With entries_required under [effects.self], each
// module of the project the program holds (ownModules) with no entry is
// one too, at that key, and under [effects.deps] each dependency with
// neither an entry nor an [effects.self] of its own
// (requiredPackages), each with the fix that writes its entry (entryFix).
// A dependency's own table is read only when the project has an
// [effects.deps] table.
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
	// entries checks the entries of list: each key one keyProblem accepts,
	// each name in an accepted entry a std module.
	entries := func(list string, keyProblem func(key string) (problem, hint string)) {
		table := "effects." + list
		keys := slices.Sorted(maps.Keys(c.Entries(list)))
		for _, key := range keys {
			if problem, hint := keyProblem(key); problem != "" {
				at(path, data, analysis.ManifestKeyLine(data, analysis.EntryKeyPath(list, key)...),
					fmt.Sprintf("[%s] names %s, %s", table, key, problem), hint)
				continue
			}
			checkNames(table, fmt.Sprintf("%q", key), c.Entries(list)[key], analysis.EntryKeyPath(list, key)...)
		}
	}
	// required is the error of a module entries_required under table finds
	// with no entry under list, with the fix that writes its entry.
	var tightest *Effects
	required := func(table, list, module string, hints ...string) {
		at(path, data, analysis.ManifestKeyLine(data, "effects", table, "entries_required"),
			fmt.Sprintf("[effects.%s] entries_required: %s has no entry", table, module), hints...)
		ds[len(ds)-1].Fix = p.entryFix(list, module, &tightest)
	}
	if s := c.Self; s != nil {
		checkNames("effects.self", "std", s.Std, "effects", "self", "std")
		checkNames("effects.self", "std_in_tests", s.StdInTests, "effects", "self", "std_in_tests")
		var modules []string
		entries(analysis.ListSelfModules, func(key string) (string, string) {
			if modules == nil {
				modules = p.projectModules()
			}
			return ownModuleProblem(key, modules)
		})
		if s.EntriesRequired {
			for _, m := range p.ownModules() {
				if _, listed := s.Modules[m]; !listed {
					required("self", analysis.ListSelfModules, m, fmt.Sprintf("add %q under [effects.self.modules] in nomi.toml", m))
				}
			}
		}
	}
	d := c.Deps
	if d == nil {
		return ds
	}
	checkNames("effects.deps", "std", d.Std, "effects", "deps", "std")
	checkNames("effects.deps", "std_in_tests", d.StdInTests, "effects", "deps", "std_in_tests")
	entries(analysis.ListDepsPackages, p.dependencyProblem)
	for _, m := range p.requiredPackages() {
		if m.declares() {
			ownPath := filepath.Join(m.root, "nomi.toml")
			ownData, _ := analysis.ReadManifest(m.root)
			own := m.manifest.Effects.Self
			checkNamesIn(ownPath, ownData, "effects.self", "std", own.Std, "effects", "self", "std")
			for _, key := range slices.Sorted(maps.Keys(own.Modules)) {
				checkNamesIn(ownPath, ownData, "effects.self.modules", fmt.Sprintf("%q", key), own.Modules[key], analysis.EntryKeyPath(analysis.ListSelfModules, key)...)
			}
			continue
		}
		if _, listed := d.Packages[m.path]; d.EntriesRequired && !listed {
			required("deps", analysis.ListDepsPackages, m.path,
				fmt.Sprintf("add %q under [effects.deps.packages] in nomi.toml, or give %s an [effects.self] table in its own nomi.toml", m.path, m.path))
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

// entryFix is the fix of a module entries_required finds with no entry
// under list, one of analysis.EntryLists: the edit to nomi.toml that
// writes module's entry with what its own files reach from the roots `nomi
// check` holds to the lists (tightestLists), as `nomi add` writes a
// dependency's. tightest holds the tightest tables once a fix has walked
// the program, and is walked once. Nil when the program's nomi.toml is
// staged in memory, or the edit cannot be written.
func (p *Program) entryFix(list, module string, tightest **Effects) *parser.Fix {
	path, data := p.manifestPath()
	if data == nil || !filepath.IsAbs(path) {
		return nil
	}
	if *tightest == nil {
		var deps []string
		for _, m := range p.requiredPackages() {
			deps = append(deps, m.path)
		}
		*tightest = tightestLists(p.walks(p.checkedRoots()), deps, p.ownModules())
	}
	entry := (*tightest).Entries(list)[module]
	to := p.effects.Clone()
	to.SetEntry(list, module, entry)
	quoted := make([]string, len(entry))
	for i, n := range entry {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	title := fmt.Sprintf("Write %q = [%s] under [effects.%s]", module, strings.Join(quoted, ", "), list)
	fix, err := analysis.EffectsFix(path, data, title, to)
	if err != nil {
		return nil
	}
	return fix
}

// requiredPaths is the name of each module the project needs
// (pkgcache.Dep.Name), sorted; nil for a program with no project root.
func (p *Program) requiredPaths() []string {
	root := p.prog.Root
	if root == "" {
		return nil
	}
	var required []string
	if g, err := pkgcache.Load(root); err == nil && g != nil {
		for _, d := range g.Deps {
			required = append(required, d.Name)
		}
	}
	sort.Strings(required)
	return required
}

// dependencyProblem is what is wrong with module, a key under
// [effects.deps.packages], when it is not the name of a module the project
// needs, with a hint; both are "" when it is one. The project's own name
// is not one: its own code is [effects.self]'s.
func (p *Program) dependencyProblem(module string) (problem, hint string) {
	required := p.requiredPaths()
	switch {
	case slices.Contains(required, module):
		return "", ""
	case module == p.projectPackage() || module == p.ownName():
		return "which is this project", "[effects.deps] holds the packages it depends on; its own code is held by [effects.self]"
	case len(required) == 0:
		return "which is not a dependency of this project", "the project has no dependencies"
	}
	// A dependency is named by its name in the program, the key the
	// project's [dependencies] gives it, never by its repository or its
	// own [package].name.
	if g, err := pkgcache.Load(p.prog.Root); err == nil && g != nil {
		for _, d := range g.Deps {
			if d.Name != module && (!d.ID.IsZero() && d.ID.String() == module || d.Self == module) {

				return "which is not a dependency's name in this project", fmt.Sprintf("did you mean %q? a dependency is named by its key under [dependencies]", d.Name)
			}
		}
	}
	if near := suggest.Closest(module, required); near != "" {
		return "which is not a dependency of this project", fmt.Sprintf("did you mean %q?", near)
	}
	return "which is not a dependency of this project", "its dependencies are " + strings.Join(required, ", ")
}

// ownName is the project's [package].name, or "".
func (p *Program) ownName() string {
	if m := programManifest(p.prog.Root, p.cfg.manifest); m != nil {
		return m.Name
	}
	return ""
}

// hostEffects is the list the host holds the program to (WithEffects),
// as effects tables that hold the whole program, or nil when the host
// gave none: Self and Deps' ceiling are WithEffects' names.
func (p *Program) hostEffects() *Effects {
	if !p.cfg.held {
		return nil
	}
	return &Effects{
		Self: &analysis.SelfEffects{Std: p.cfg.allowed},
		Deps: &analysis.DepsEffects{Std: append([]string{}, p.cfg.allowed...)},
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
