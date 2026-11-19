package vmhost

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/std"
)

// Capabilities is nomi.toml's [capabilities] table: what a program, and
// each module it requires, may reach outside the machine
// (OutsideCallsNotAllowed).
type Capabilities = analysis.Capabilities

// stdNames is StdCapabilityNames, worked out once.
var stdNames struct {
	once  sync.Once
	names []string
}

// StdCapabilityNames is every std name a [capabilities] list may hold,
// sorted: `std::` and each std module a program can import, as the import
// writes it, which is what an outside call into std is named by
// (OutsideCall.Name). A module none of whose functions reaches outside is
// among them: telling those apart needs the whole stdlib lowered, which a
// check does not do, and allowing one allows nothing.
func StdCapabilityNames() []string {
	stdNames.once.Do(func() {
		for _, m := range std.Shared().ModuleNames() {
			if _, internal := analysis.InternalStdModule(m); internal || m == "prelude" {
				continue
			}
			stdNames.names = append(stdNames.names, "std::"+strings.ReplaceAll(m, "/", "::"))
		}
		sort.Strings(stdNames.names)
	})
	return stdNames.names
}

// manifestPath is the program's nomi.toml, where a diagnostic about its
// lists goes, and its text, nil for a manifest staged in memory.
func (p *Program) manifestPath() (string, []byte) {
	if p.cfg.manifest != nil || p.prog.Root == "" {
		return "nomi.toml", nil
	}
	path := filepath.Join(p.prog.Root, "nomi.toml")
	data, _ := os.ReadFile(path)
	return path, data
}

// capabilityListProblems is a diagnostic for each entry of the program's
// [capabilities] lists, and of the std list each dependency's own
// nomi.toml declares, that names nothing a list can allow: a std module
// with no outside functions, and a module, in `go` or under
// [capabilities.modules], that is neither the project nor one go.mod
// requires (unknownModule). A name that is valid and never reached is not a problem. With
// require_module_lists, each dependency with no std list
// (requiredModules) is one too, at that key; being in `go` is not a std
// list.
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
	checkNamesIn := func(path string, data []byte, list string, names []string, key ...string) {
		for _, name := range names {
			if problem, hint := unknownStdName(name); problem != "" {
				at(path, data, analysis.ManifestEntryLine(data, name, key...), fmt.Sprintf("[capabilities] %s names %s, %s", list, name, problem), hint)
			}
		}
	}
	checkNames := func(list string, names []string, key ...string) {
		checkNamesIn(path, data, list, names, key...)
	}
	checkNames("std", c.Std, "capabilities", "std")
	checkNames("std_in_tests", c.StdInTests, "capabilities", "std_in_tests")
	for _, list := range []struct {
		key     string
		modules []string
	}{{"go", c.Go}, {"go_in_tests", c.GoInTests}} {
		for _, module := range list.modules {
			if problem, hint := p.unknownModule(module); problem != "" {
				at(path, data, analysis.ManifestEntryLine(data, module, "capabilities", list.key),
					fmt.Sprintf("[capabilities] %s names %s, %s", list.key, module, problem), hint)
			}
		}
	}
	paths := make([]string, 0, len(c.Modules))
	for module := range c.Modules {
		paths = append(paths, module)
	}
	sort.Strings(paths)
	for _, module := range paths {
		if problem, hint := p.unknownModule(module); problem != "" {
			at(path, data, analysis.ManifestKeyLine(data, "capabilities", "modules", module),
				fmt.Sprintf("[capabilities.modules] names %s, %s", module, problem), hint)
			continue
		}
		checkNames(fmt.Sprintf("modules %q", module), c.Modules[module], "capabilities", "modules", module)
	}
	for _, m := range p.requiredModules() {
		if m.declares() {
			ownPath := filepath.Join(m.root, "nomi.toml")
			ownData, _ := os.ReadFile(ownPath)
			checkNamesIn(ownPath, ownData, "std", m.manifest.Capabilities.Std, "capabilities", "std")
			if entry, ok := m.manifest.Capabilities.Modules[m.path]; ok {
				checkNamesIn(ownPath, ownData, fmt.Sprintf("modules %q", m.path), entry, "capabilities", "modules", m.path)
			}
			continue
		}
		if _, listed := c.Modules[m.path]; c.RequireModuleLists && !listed {
			at(path, data, analysis.ManifestKeyLine(data, "capabilities", "require_module_lists"),
				fmt.Sprintf("[capabilities] require_module_lists: %s has no list", m.path),
				fmt.Sprintf("add %q under [capabilities.modules] in nomi.toml, or give %s a [capabilities] table with its own std in its nomi.toml", m.path, m.path))
		}
	}
	return ds
}

// requiredPaths is the path of each module the project's go.mod requires,
// sorted.
func (p *Program) requiredPaths() []string {
	if p.prog.Root == "" {
		return nil
	}
	var required []string
	if g, err := modcache.Load(p.prog.Root); err == nil && g != nil {
		for _, d := range g.Deps {
			required = append(required, d.Path)
		}
	}
	sort.Strings(required)
	return required
}

// unknownStdName is what is wrong with name, an entry of the right form
// (capability.NameProblem), when it names no std module a program can
// import, as the end of a message that names it, with a hint; both are ""
// when name names one.
func unknownStdName(name string) (problem, hint string) {
	valid := StdCapabilityNames()
	if slices.Contains(valid, name) {
		return "", ""
	}
	// The nearest module by its own name, so the shared `std::` does not
	// make a far one look close.
	modules := make([]string, len(valid))
	for i, v := range valid {
		modules[i] = strings.TrimPrefix(v, "std::")
	}
	hint = "name a std module as an import writes it, such as std::fs"
	if near := analysis.ClosestName(strings.TrimPrefix(name, "std::"), modules); near != "" {
		hint = fmt.Sprintf("did you mean %q?", "std::"+near)
	}
	return "which is not a std module", hint
}

// unknownModule is what is wrong with module, an entry of a list of the
// modules that may bind Go (nomi.toml's `go` and `go_in_tests`, WithGo) or
// a key under [capabilities.modules], when it is neither the project
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

// hostCapabilities is the lists the host holds the program to
// (WithCapabilities, WithGo), as a [capabilities] table with no module
// lists, or nil when the host gave neither. With WithGo alone the table's
// std holds nothing (hostHolds).
func (p *Program) hostCapabilities() *Capabilities {
	if !p.cfg.held && !p.cfg.goHeld {
		return nil
	}
	return &Capabilities{Std: p.cfg.allowed, Go: p.cfg.goModules}
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
			func() (map[string]bool, error) { return hostKeys(p.cfg.hosts) },
			unknownStdName)
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
