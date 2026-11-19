package vmhost

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/modcache"
)

// Capabilities is nomi.toml's capability tables: what a project's own
// code, and the code of the modules it depends on, may reach outside the
// machine (OutsideCallsNotAllowed).
type Capabilities = analysis.Capabilities

// SelfCapabilities is [capabilities.self]: what the project's own files may
// reach.
type SelfCapabilities = analysis.SelfCapabilities

// DepsCapabilities is [capabilities.deps] with [capabilities.deps.modules]:
// what the files of the modules the project depends on may reach.
type DepsCapabilities = analysis.DepsCapabilities

// manifestPath is the program's nomi.toml, where a diagnostic about its
// lists goes, and its text, nil for a manifest staged in memory.
func (p *Program) manifestPath() (string, []byte) {
	if p.cfg.manifest != nil || p.prog.Root == "" {
		return "nomi.toml", nil
	}
	data, _ := analysis.ReadManifest(p.prog.Root)
	return filepath.Join(p.prog.Root, "nomi.toml"), data
}

// capabilityListProblems is a diagnostic for each entry of the program's
// capability tables, and of the std list each dependency's own
// [capabilities.self] declares, that names nothing a list can allow: a std
// module with no outside functions, and a module, in [capabilities.deps]'
// go or go_in_tests or under [capabilities.deps.modules], that is not one
// go.mod requires (dependencyProblem). A name that is valid and never
// reached is not a problem. With entries_required, each dependency with
// neither an entry nor a [capabilities.self] of its own (requiredModules)
// is one too, at that key. A dependency's own table is read only when the
// project has a [capabilities.deps] table.
func (p *Program) capabilityListProblems() Diagnostics {
	c := p.capabilities
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
			if problem, hint := capability.UnknownStdName(name); problem != "" {
				at(path, data, analysis.ManifestEntryLine(data, name, key...), fmt.Sprintf("[%s] %s names %s, %s", table, list, name, problem), hint)
			}
		}
	}
	checkNames := func(table, list string, names []string, key ...string) {
		checkNamesIn(path, data, table, list, names, key...)
	}
	if s := c.Self; s != nil {
		checkNames("capabilities.self", "std", s.Std, "capabilities", "self", "std")
		checkNames("capabilities.self", "std_in_tests", s.StdInTests, "capabilities", "self", "std_in_tests")
	}
	d := c.Deps
	if d == nil {
		return ds
	}
	checkNames("capabilities.deps", "std", d.Std, "capabilities", "deps", "std")
	checkNames("capabilities.deps", "std_in_tests", d.StdInTests, "capabilities", "deps", "std_in_tests")
	for _, list := range []struct {
		key     string
		modules []string
	}{{"go", d.Go}, {"go_in_tests", d.GoInTests}} {
		for _, module := range list.modules {
			if problem, hint := p.dependencyProblem(module); problem != "" {
				at(path, data, analysis.ManifestEntryLine(data, module, "capabilities", "deps", list.key),
					fmt.Sprintf("[capabilities.deps] %s names %s, %s", list.key, module, problem), hint)
			}
		}
	}
	paths := make([]string, 0, len(d.Modules))
	for module := range d.Modules {
		paths = append(paths, module)
	}
	sort.Strings(paths)
	for _, module := range paths {
		if problem, hint := p.dependencyProblem(module); problem != "" {
			at(path, data, analysis.ManifestKeyLine(data, "capabilities", "deps", "modules", module),
				fmt.Sprintf("[capabilities.deps.modules] names %s, %s", module, problem), hint)
			continue
		}
		checkNames("capabilities.deps.modules", fmt.Sprintf("%q", module), d.Modules[module], "capabilities", "deps", "modules", module)
	}
	for _, m := range p.requiredModules() {
		if m.declares() {
			ownPath := filepath.Join(m.root, "nomi.toml")
			ownData, _ := analysis.ReadManifest(m.root)
			checkNamesIn(ownPath, ownData, "capabilities.self", "std", m.manifest.Capabilities.Self.Std, "capabilities", "self", "std")
			continue
		}
		if _, listed := d.Modules[m.path]; d.EntriesRequired && !listed {
			at(path, data, analysis.ManifestKeyLine(data, "capabilities", "deps", "entries_required"),
				fmt.Sprintf("[capabilities.deps] entries_required: %s has no entry", m.path),
				fmt.Sprintf("add %q under [capabilities.deps.modules] in nomi.toml, or give %s a [capabilities.self] table in its own nomi.toml", m.path, m.path))
		}
	}
	return ds
}

// requiredPaths is the path of each module the project's go.mod requires,
// sorted.
func (p *Program) requiredPaths() []string {
	return requiredPathsAt(p.prog.Root)
}

// requiredPathsAt is the path of each module the go.mod in root requires,
// sorted; nil for no root.
func requiredPathsAt(root string) []string {
	if root == "" {
		return nil
	}
	var required []string
	if g, err := modcache.Load(root); err == nil && g != nil {
		for _, d := range g.Deps {
			required = append(required, d.Path)
		}
	}
	sort.Strings(required)
	return required
}

// DependencyModules is each module path a list of dependencies in the
// nomi.toml in root may name (dependencyProblem), sorted: each module its
// go.mod requires. The language server completes [capabilities.deps]' go
// and go_in_tests and the keys under [capabilities.deps.modules] from it.
func DependencyModules(root string) []string {
	return requiredPathsAt(root)
}

// unknownModule is what is wrong with module, an entry of WithGo's list of
// the modules that may bind Go, when it is neither the project
// (projectModule) nor a module go.mod requires, with a hint naming those;
// both are "" when it is one.
func (p *Program) unknownModule(module string) (problem, hint string) {
	project := p.projectModule()
	required := p.requiredPaths()
	if module == project || slices.Contains(required, module) {
		return "", ""
	}
	hint = "this project is " + project
	if len(required) > 0 {
		hint += ", and go.mod requires " + strings.Join(required, ", ")
	}
	return "which is neither this project nor a module go.mod requires", hint
}

// dependencyProblem is what is wrong with module, an entry of
// [capabilities.deps]' go or go_in_tests or a key under
// [capabilities.deps.modules], when it is not a module go.mod requires,
// with a hint; both are "" when it is one. The project's own path is not
// one: its own code is [capabilities.self]'s.
func (p *Program) dependencyProblem(module string) (problem, hint string) {
	required := p.requiredPaths()
	switch {
	case slices.Contains(required, module):
		return "", ""
	case module == p.projectModule():
		return "which is this project", "[capabilities.deps] holds the modules it depends on; its own code is held by [capabilities.self]"
	case len(required) == 0:
		return "which go.mod does not require", "go.mod requires no module"
	}
	return "which go.mod does not require", "go.mod requires " + strings.Join(required, ", ")
}

// hostCapabilities is the lists the host holds the program to
// (WithCapabilities, WithGo), as capability tables that hold the whole
// program, or nil when the host gave neither: Self and Deps' ceiling are
// WithCapabilities' names, and the modules WithGo lists may bind Go, the
// project among them when it is listed. With WithGo alone the names are
// none (hostHolds).
func (p *Program) hostCapabilities() *Capabilities {
	if !p.cfg.held && !p.cfg.goHeld {
		return nil
	}
	return &Capabilities{
		Self: &analysis.SelfCapabilities{Std: p.cfg.allowed, Go: slices.Contains(p.cfg.goModules, p.projectModule())},
		Deps: &analysis.DepsCapabilities{Std: append([]string{}, p.cfg.allowed...), Go: p.cfg.goModules},
	}
}

// hostHolds reports whether the host's lists hold n, a call over them: a
// Go binding always, and any other call when the host passed
// WithCapabilities, since WithGo alone holds only Go.
func (p *Program) hostHolds(n NotAllowed) bool {
	return n.Binder != "" || p.cfg.held
}

// hostCapabilityProblem is what is wrong with the names the host passed to
// WithCapabilities and WithGo, or nil. WithCapabilities' names are held to
// what an entry of nomi.toml's std lists is held to, with the same words
// (capability.CheckHostList), except that `host:` and a key a table passed
// with WithHosts answers is a name too, which only the host can allow.
// WithGo's are held to what an entry of nomi.toml's `go` is held to.
func (p *Program) hostCapabilityProblem() error {
	if p.cfg.held {
		err := capability.CheckHostList("vmhost.WithCapabilities", "vmhost.WithHosts", "Go is allowed by module, not by package: pass the module that binds it to vmhost.WithGo", p.cfg.allowed,
			func() (map[string]bool, error) { return hostKeys(p.cfg.hosts) })
		if err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, module := range p.cfg.goModules {
		if seen[module] {
			return fmt.Errorf("vmhost.WithGo lists %s twice", module)
		}
		seen[module] = true
		if problem := capability.ModuleProblem(module); problem != "" {
			return fmt.Errorf("vmhost.WithGo: %q %s", module, problem)
		}
		if problem, hint := p.unknownModule(module); problem != "" {
			return fmt.Errorf("vmhost.WithGo names %s, %s (%s)", module, problem, hint)
		}
	}
	return nil
}
