package vmhost

import (
	"cmp"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
)

// Reach is what a project's programs reach outside the machine, from the
// roots `nomi check` holds to nomi.toml's lists (checkedRoots), with the
// module each call is charged to: what `nomi capabilities` reports and
// writes. One program's Reach is Program.Reach; a project of several
// entries and test files adds each one's (Add).
type Reach struct {
	// Root is the project's directory, the one holding its nomi.toml, or
	// "" for a program with none.
	Root string
	// Project is the project's own module path (projectModule): what `go`
	// and its own entry under [capabilities.modules] name it by.
	Project string
	// Modules is each module an entry under [capabilities.modules] may
	// name that the programs' code comes from: the project's own path and
	// each module go.mod requires whose Nomi files the programs hold
	// (requiredModules), sorted.
	Modules []string
	// Lists is nomi.toml's [capabilities] table, nil when it has none.
	Lists *Capabilities
	// Walks is each walk's calls, the walks from main before the walks
	// from the tests for each program.
	Walks []Walk
	// declared is each dependency's own table (declaredLists).
	declared map[string]*Capabilities
	// sites is where a report shows each call (shownAt), by its position.
	sites map[ir.Pos]string
}

// Reached loads the program at path as Check does, a program with Go
// bindings among them (their Go is neither built nor run), holds it to no
// list, and answers what it reaches (Program.Reach). The errors are
// Check's, but for the lists' own.
func Reached(path string, opts ...Option) (*Reach, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, _, ok := frontend.StdlibFile(abs); ok {
		return nil, fmt.Errorf("%s is a std module, which no nomi.toml holds", path)
	}
	p, err := checkedProgram(newConfig(opts), abs)
	if err != nil {
		return nil, err
	}
	return p.Reach(), nil
}

// Reach is what the program reaches outside the machine from the roots
// `nomi check` holds to the lists: main, the boot and the exported
// functions, and the test cases.
func (p *Program) Reach() *Reach {
	r := &Reach{Root: p.prog.Root, Project: p.projectModule(), Lists: p.capabilities, declared: map[string]*Capabilities{}, sites: map[ir.Pos]string{}}
	r.Modules = []string{r.Project}
	for _, m := range p.requiredModules() {
		if problem, _ := p.unknownModule(m.path); problem == "" && !slices.Contains(r.Modules, m.path) {
			r.Modules = append(r.Modules, m.path)
		}
	}
	slices.Sort(r.Modules)
	for _, roots := range p.checkedRoots() {
		calls := p.OutsideCalls(roots)
		r.Walks = append(r.Walks, Walk{Tests: roots.Tests != nil, Calls: calls})
		maps.Copy(r.declared, p.declaredLists(calls))
		for _, c := range calls {
			if _, done := r.sites[c.at]; !done {
				_, line, col := p.shownAt(c)
				r.sites[c.at] = fmt.Sprintf("%s:%d:%d", p.imageName(c.at.File()), line, col)
			}
		}
	}
	return r
}

// Add adds o, the Reach of another program of the same project, to r.
func (r *Reach) Add(o *Reach) {
	r.Walks = append(r.Walks, o.Walks...)
	for _, m := range o.Modules {
		if !slices.Contains(r.Modules, m) {
			r.Modules = append(r.Modules, m)
		}
	}
	slices.Sort(r.Modules)
	if r.declared == nil {
		r.declared = map[string]*Capabilities{}
	}
	maps.Copy(r.declared, o.declared)
	if r.sites == nil {
		r.sites = map[ir.Pos]string{}
	}
	maps.Copy(r.sites, o.sites)
}

// SiteOf is where a report shows c, `file:line:col` with the file named
// as Site names it: the call's callee, where a diagnostic about it points.
func (r *Reach) SiteOf(c OutsideCall) string {
	return cmp.Or(r.sites[c.at], c.Site)
}

// ModuleOf is the module c is charged to, by its path: the dependency
// whose files hold it, or the project.
func (r *Reach) ModuleOf(c OutsideCall) string {
	return cmp.Or(c.Module, r.Project)
}

// Tightest is the smallest [capabilities] table the walks pass
// (tightestLists): with it as nomi.toml's, NotAllowed answers only the
// calls a dependency's own table refuses.
func (r *Reach) Tightest() *Capabilities {
	return tightestLists(r.Walks, r.Project, r.Modules)
}

// NotAllowed is each call the walks reach that lists, with each
// dependency's own table, does not allow (notAllowedBy), each call once
// per list, as `nomi check` reports them; nil lists allow everything.
func (r *Reach) NotAllowed(lists *Capabilities) []NotAllowed {
	var out []NotAllowed
	seen := map[string]bool{}
	for _, w := range r.Walks {
		for _, n := range notAllowedBy(w.Calls, w.Tests, lists, r.declared, r.Project) {
			key := fmt.Sprint(n.Site, "\x00", n.Name)
			if !seen[key] {
				seen[key] = true
				out = append(out, n)
			}
		}
	}
	return out
}
