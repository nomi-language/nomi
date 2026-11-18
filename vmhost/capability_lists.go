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
	"github.com/nomi-language/nomi/internal/ffirun"
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
// [capabilities] lists that names nothing a list can allow: a std module
// with no outside functions, a Go package no `gopkg` declaration in the
// program or its dependencies imports, and a module go.mod does not
// require. A name that is valid and never reached is not a problem.
func (p *Program) capabilityListProblems() Diagnostics {
	c := p.capabilities
	if c == nil {
		return nil
	}
	path, data := p.manifestPath()
	src := string(data)
	if data == nil {
		src = "\n"
	}
	var ds Diagnostics
	at := func(line int, message string, hints ...string) {
		d := frontend.NewDiagnostic(path, src, line, 0, message)
		d.Hints = append(d.Hints, hints...)
		ds = append(ds, d)
	}
	bound := p.boundOnce()
	checkNames := func(list string, names []string, key ...string) {
		for _, name := range names {
			if problem, hint := unknownCapabilityName(name, bound); problem != "" {
				at(analysis.ManifestEntryLine(data, name, key...), fmt.Sprintf("[capabilities] %s names %s, %s", list, name, problem), hint)
			}
		}
	}
	checkNames("allow", c.Allow, "capabilities", "allow")
	checkNames("allow_in_tests", c.AllowInTests, "capabilities", "allow_in_tests")
	paths := make([]string, 0, len(c.Modules))
	for module := range c.Modules {
		paths = append(paths, module)
	}
	sort.Strings(paths)
	var required []string
	if len(paths) > 0 && p.prog.Root != "" {
		if g, err := modcache.Load(p.prog.Root); err == nil && g != nil {
			for _, d := range g.Deps {
				required = append(required, d.Path)
			}
		}
		sort.Strings(required)
	}
	for _, module := range paths {
		if !slices.Contains(required, module) {
			line := analysis.ManifestKeyLine(data, "capabilities", "modules", module)
			hint := "go.mod requires no module"
			if len(required) > 0 {
				hint = "go.mod requires " + strings.Join(required, ", ")
			}
			at(line, fmt.Sprintf("[capabilities.modules] names %s, which go.mod does not require", module), hint)
			continue
		}
		checkNames(fmt.Sprintf("modules %q", module), c.Modules[module], "capabilities", "modules", module)
	}
	return ds
}

// unknownCapabilityName is what is wrong with name, an entry of the right
// form (analysis.CapabilityNameProblem), when it names nothing a list can
// allow, as the end of a message that names it, with a hint: a `std::`
// name that is no std module a program can import, or a `go:` package no
// `gopkg` declaration in the program or its dependencies imports. Both are
// "" when name names something. bound answers those packages.
func unknownCapabilityName(name string, bound func() []string) (problem, hint string) {
	if module, ok := strings.CutPrefix(name, "std::"); ok {
		valid := StdCapabilityNames()
		if slices.Contains(valid, name) {
			return "", ""
		}
		// The nearest module by its own name, so the shared `std::` does
		// not make a far one look close.
		modules := make([]string, len(valid))
		for i, v := range valid {
			modules[i] = strings.TrimPrefix(v, "std::")
		}
		hint = "name a std module as an import writes it, such as std::fs"
		if near := analysis.ClosestName(module, modules); near != "" {
			hint = fmt.Sprintf("did you mean %q?", "std::"+near)
		}
		return "which is not a std module", hint
	}
	if pkg, ok := strings.CutPrefix(name, "go:"); ok {
		packages := bound()
		if slices.Contains(packages, pkg) {
			return "", ""
		}
		hint = "the program and its dependencies bind no Go package"
		if len(packages) > 0 {
			names := make([]string, len(packages))
			for i, b := range packages {
				names[i] = "go:" + b
			}
			hint = "the Go packages they bind are " + strings.Join(names, ", ")
		}
		return "which no `gopkg` declaration in the program or its dependencies imports", hint
	}
	return "", ""
}

// boundOnce is boundGoPackages, worked out on the first call.
func (p *Program) boundOnce() func() []string {
	var bound []string
	done := false
	return func() []string {
		if !done {
			bound, done = p.boundGoPackages(), true
		}
		return bound
	}
}

// hostCapabilities is the list the host holds the program to
// (WithCapabilities), as a [capabilities] table with no module lists, or
// nil when the host gave none.
func (p *Program) hostCapabilities() *Capabilities {
	if !p.cfg.held {
		return nil
	}
	return &Capabilities{Allow: p.cfg.allowed}
}

// hostCapabilityProblem is what is wrong with the names the host passed to
// WithCapabilities, or nil. Each is held to what an entry of nomi.toml's
// lists is held to, with the same words, except that `host:` and a key a
// table passed with WithHosts answers is a name too, which only the host
// can allow.
func (p *Program) hostCapabilityProblem() error {
	var keys []string
	keysRead := false
	bound := p.boundOnce()
	seen := map[string]bool{}
	for _, name := range p.cfg.allowed {
		if seen[name] {
			return fmt.Errorf("vmhost.WithCapabilities lists %s twice", name)
		}
		seen[name] = true
		if strings.HasPrefix(name, "host:") {
			if !keysRead {
				answered, err := hostKeys(p.cfg.hosts)
				if err != nil {
					return err
				}
				for k := range answered {
					keys = append(keys, "host:"+k)
				}
				sort.Strings(keys)
				keysRead = true
			}
			if slices.Contains(keys, name) {
				continue
			}
			hint := "no table is passed with vmhost.WithHosts"
			if len(keys) > 0 {
				hint = "its tables answer " + strings.Join(keys, ", ")
			}
			return fmt.Errorf("vmhost.WithCapabilities names %s, which no table passed with vmhost.WithHosts answers (%s)", name, hint)
		}
		if problem := analysis.CapabilityNameProblem(name); problem != "" {
			return fmt.Errorf("vmhost.WithCapabilities: %q %s", name, problem)
		}
		if problem, hint := unknownCapabilityName(name, bound); problem != "" {
			return fmt.Errorf("vmhost.WithCapabilities names %s, %s (%s)", name, problem, hint)
		}
	}
	return nil
}

// boundGoPackages is the Go import path of each `gopkg` declaration in the
// program's files and its dependencies', sorted.
func (p *Program) boundGoPackages() []string {
	set := map[string]bool{}
	for _, m := range p.prog.Modules {
		if _, isStd := std.ImageName(m.Path); isStd {
			continue
		}
		for _, path := range ffirun.BoundImportPaths(m.Nodes) {
			set[path] = true
		}
	}
	out := make([]string, 0, len(set))
	for path := range set {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
