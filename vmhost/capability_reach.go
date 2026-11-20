package vmhost

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/capability"
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
	// Project is the project's own module path (projectModule), whose own
	// files [capabilities.self] holds.
	Project string
	// Modules is each dependency an entry under [capabilities.deps.modules]
	// may name that the programs' code comes from: each module go.mod
	// requires whose Nomi files the programs hold (requiredModules),
	// sorted.
	Modules []string
	// Lists is nomi.toml's capability tables, nil when it has neither.
	Lists *Capabilities
	// Walks is each walk's calls, the walks from main before the walks
	// from the tests for each program.
	Walks []Walk
	// ListProblems is what `nomi check` reports about the lists themselves
	// before it holds any call to them (capabilityListProblems): a name
	// that is no std module, a dependency go.mod does not require, a
	// dependency entries_required finds with no entry. Each is given once.
	ListProblems Diagnostics
	// declared is each dependency's own [capabilities.self]
	// (declaredLists).
	declared map[string]*analysis.SelfCapabilities
	// sites is where a report shows each call (shownSpan), by its position.
	sites map[ir.Pos]shownSite
}

// shownSite is where a report shows a call: text, `file:line:col` with the
// file named as Site names it, and the file as the program names it, with
// the span of the call's callee (shownSpan).
type shownSite struct {
	text                       string
	path                       string
	line, col, endLine, endCol int
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
	r := &Reach{Root: p.prog.Root, Project: p.projectModule(), Lists: p.capabilities, ListProblems: p.capabilityListProblems(),
		declared: map[string]*analysis.SelfCapabilities{}, sites: map[ir.Pos]shownSite{}}
	for _, m := range p.requiredModules() {
		if problem, _ := p.dependencyProblem(m.path); problem == "" && !slices.Contains(r.Modules, m.path) {
			r.Modules = append(r.Modules, m.path)
		}
	}
	slices.Sort(r.Modules)
	r.Walks = p.walks(p.checkedRoots())
	for _, w := range r.Walks {
		maps.Copy(r.declared, p.declaredLists(w.Calls))
		for _, c := range w.Calls {
			if _, done := r.sites[c.at]; !done {
				path, line, col, endLine, endCol := p.shownSpan(c.at)
				r.sites[c.at] = shownSite{text: fmt.Sprintf("%s:%d:%d", p.imageName(c.at.File()), line, col),
					path: path, line: line, col: col, endLine: endLine, endCol: endCol}
			}
		}
	}
	return r
}

// Add adds o, the Reach of another program of the same project, to r.
func (r *Reach) Add(o *Reach) {
	r.Walks = append(r.Walks, o.Walks...)
	for _, d := range o.ListProblems {
		if !slices.ContainsFunc(r.ListProblems, func(e Diagnostic) bool { return e.String() == d.String() }) {
			r.ListProblems = append(r.ListProblems, d)
		}
	}
	for _, m := range o.Modules {
		if !slices.Contains(r.Modules, m) {
			r.Modules = append(r.Modules, m)
		}
	}
	slices.Sort(r.Modules)
	if r.declared == nil {
		r.declared = map[string]*analysis.SelfCapabilities{}
	}
	maps.Copy(r.declared, o.declared)
	if r.sites == nil {
		r.sites = map[ir.Pos]shownSite{}
	}
	maps.Copy(r.sites, o.sites)
}

// SiteOf is where a report shows c, `file:line:col` with the file named
// as Site names it: the call's callee, where a diagnostic about it points.
func (r *Reach) SiteOf(c OutsideCall) string {
	return cmp.Or(r.sites[c.at].text, c.Site)
}

// ModuleOf is the module c is charged to, by its path: the dependency
// whose files hold it, or the project.
func (r *Reach) ModuleOf(c OutsideCall) string {
	return cmp.Or(c.Module, r.Project)
}

// Tightest is the smallest pair of capability tables the walks pass
// (tightestLists): with it as nomi.toml's, NotAllowed answers only the
// calls a dependency's own table refuses.
func (r *Reach) Tightest() *Capabilities {
	return tightestLists(r.Walks, r.Modules)
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

// ReachedAll is what the programs at files reach together: each one's
// Reached, added (Add), as `nomi capabilities` reports a project whose
// programs frontend.CheckedFiles names. failed is given each program that
// does not check, with its error, and the answer is then nil; nil for no
// files too.
func ReachedAll(files []string, failed func(file string, err error), opts ...Option) *Reach {
	var r *Reach
	ok := len(files) > 0
	for _, file := range files {
		one, err := Reached(file, opts...)
		if err != nil {
			ok = false
			if failed != nil {
				failed(file, err)
			}
			continue
		}
		if r == nil {
			r = one
		} else {
			r.Add(one)
		}
	}
	if !ok {
		return nil
	}
	return r
}

// WhyCall is one call that reaches a name, or that a module's files make
// (Reach.Why): the name it reaches, the module charged for it, where a
// report shows it (SiteOf), its callee, whether a test case reaches it, and
// how it is reached in the words a refusal's help uses (ReachText). Path
// is its file as the program names it, and Line, Col, EndLine and EndCol
// the span of its callee, 1-based with byte columns, the end one past the
// last byte.
type WhyCall struct {
	Name    string `json:"name"`
	Module  string `json:"module"`
	Site    string `json:"site"`
	Callee  string `json:"callee"`
	Tests   bool   `json:"tests"`
	Reach   string `json:"reach"`
	Path    string `json:"-"`
	Line    int    `json:"-"`
	Col     int    `json:"-"`
	EndLine int    `json:"-"`
	EndCol  int    `json:"-"`
}

// Why is each call the walks reach that reaches name, a std name
// (`std::fs`) or a `go:` name, or, for a module path, that the module's
// files make, once per site and name, in the walks' order: what `nomi
// capabilities --why name` prints. A name nothing reaches is an error that
// says what the walks reach and which modules their code comes from.
func (r *Reach) Why(name string) ([]WhyCall, error) {
	byName := strings.HasPrefix(name, "std::") || capability.BindsGo(name)
	var found []WhyCall
	seen := map[string]bool{}
	var names []string
	for _, wk := range r.Walks {
		for _, c := range wk.Calls {
			if !slices.Contains(names, c.Name) {
				names = append(names, c.Name)
			}
			module := r.ModuleOf(c)
			if byName && c.Name != name || !byName && module != name {
				continue
			}
			key := c.Site + "\x00" + c.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			site := r.sites[c.at]
			found = append(found, WhyCall{Name: c.Name, Module: module, Site: r.SiteOf(c), Callee: c.Callee, Tests: wk.Tests, Reach: c.ReachText(),
				Path: site.path, Line: site.line, Col: site.col, EndLine: site.endLine, EndCol: site.endCol})
		}
	}
	if len(found) == 0 {
		slices.Sort(names)
		reaches := "nothing outside the machine"
		if len(names) > 0 {
			reaches = strings.Join(names, ", ")
		}
		return nil, fmt.Errorf("nothing this program reaches is %s; it reaches %s, and its code comes from %s",
			name, reaches, strings.Join(append([]string{r.Project}, r.Modules...), ", "))
	}
	return found, nil
}

// WriteWhy prints calls, what Why answered for name, as `nomi capabilities
// --why` prints them: a line counting them, then each call's site, with the
// module when it is a dependency's (or, for a module path, the name the
// call reaches), and how it is reached. With limit above 0 it prints at
// most limit calls and a line counting the rest.
func (r *Reach) WriteWhy(w io.Writer, name string, calls []WhyCall, limit int) {
	byName := strings.HasPrefix(name, "std::") || capability.BindsGo(name)
	verb := "reach"
	if len(calls) == 1 {
		verb = "reaches"
	}
	switch {
	case byName:
		fmt.Fprintf(w, "%s %s %s:\n", callCount(len(calls)), verb, name)
	case name == r.Project:
		fmt.Fprintf(w, "%s in this project's own files %s outside the machine:\n", callCount(len(calls)), verb)
	default:
		fmt.Fprintf(w, "%s in %s %s outside the machine:\n", callCount(len(calls)), name, verb)
	}
	for i, c := range calls {
		if limit > 0 && i == limit {
			fmt.Fprintf(w, "  and %s more\n", callCount(len(calls)-limit))
			break
		}
		where := c.Site
		switch {
		case !byName:
			where += ": " + c.Name
		case c.Module != r.Project:
			where += " in " + c.Module
		}
		fmt.Fprintf(w, "  %s\n    %s\n", where, c.Reach)
	}
}

// HeldChanges is changes, from lists to the tightest tables
// (analysis.CapabilityChanges), without those `nomi capabilities --check`
// does not hold the tables to, since leaving them out is a choice and not
// a difference: a table the file leaves out, [capabilities.deps]' ceiling
// when the table leaves out its std, and the entry of a dependency the
// table gives none, which only the ceiling and the dependency's own table
// hold, unless entries_required asks every dependency for one. A dependency
// the table does give an entry is held to the tightest one, like every
// list. Nil lists hold nothing back.
func HeldChanges(changes []analysis.CapabilityChange, lists *Capabilities) []analysis.CapabilityChange {
	if lists == nil {
		return changes
	}
	var out []analysis.CapabilityChange
	for _, c := range changes {
		self := c.List == analysis.ListSelf || strings.HasPrefix(c.List, analysis.ListSelf+".")
		switch {
		case self && lists.Self == nil:
		case !self && lists.Deps == nil:
		case self:
			out = append(out, c)
		case (c.List == analysis.ListDepsStd || c.List == analysis.ListDepsStdInTests) && lists.Deps.Std == nil:
		case c.List == analysis.ListDepsModules && c.Change == "add" && !lists.Deps.EntriesRequired && !hasEntry(lists, c.Module):
		default:
			out = append(out, c)
		}
	}
	return out
}

// hasEntry reports whether lists give module an entry under
// [capabilities.deps.modules].
func hasEntry(lists *Capabilities, module string) bool {
	_, has := lists.Deps.Modules[module]
	return has
}

// ModuleReach is what one dependency's own files reach, as `nomi
// capabilities` prints it under deps' modules (WriteModuleReach).
type ModuleReach struct {
	Path string `json:"path"`
	// Std is each std name the module's files reach; StdTestsOnly those
	// of them only the tests reach.
	Std          []string `json:"std"`
	StdTestsOnly []string `json:"std_tests_only"`
	// BindsGo reports that the module's files bind Go in calls the walks
	// reach, and GoTestsOnly that only the tests reach them.
	BindsGo     bool `json:"binds_go"`
	GoTestsOnly bool `json:"go_tests_only,omitempty"`
}

// ModuleReaches is what each dependency the programs' code comes from
// reaches, sorted by path.
func (r *Reach) ModuleReaches() []ModuleReach {
	byPath := map[string]*ModuleReach{}
	use := func(path string) *ModuleReach {
		if u, ok := byPath[path]; ok {
			return u
		}
		u := &ModuleReach{Path: path, Std: []string{}, StdTestsOnly: []string{}}
		byPath[path] = u
		return u
	}
	for _, m := range r.Modules {
		use(m)
	}
	// fromMain is each module and std name, or "go" for a Go binding,
	// that a walk from main reaches.
	type key struct{ module, name string }
	fromMain := map[key]bool{}
	for _, w := range r.Walks {
		for _, c := range w.Calls {
			if c.Module == "" {
				continue
			}
			u := use(c.Module)
			name := c.Name
			if capability.BindsGo(c.Name) {
				u.BindsGo, name = true, "go"
			} else if !slices.Contains(u.Std, c.Name) {
				u.Std = append(u.Std, c.Name)
			}
			if !w.Tests {
				fromMain[key{u.Path, name}] = true
			}
		}
	}
	out := []ModuleReach{}
	for _, u := range byPath {
		slices.Sort(u.Std)
		for _, n := range u.Std {
			if !fromMain[key{u.Path, n}] {
				u.StdTestsOnly = append(u.StdTestsOnly, n)
			}
		}
		u.GoTestsOnly = u.BindsGo && !fromMain[key{u.Path, "go"}]
		out = append(out, *u)
	}
	slices.SortFunc(out, func(a, b ModuleReach) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// WriteModuleReach prints, under a module's path, indented by indent, what
// its own files reach: its std names, with (tests only) on those only the
// tests reach, and whether it binds Go, or nothing.
func WriteModuleReach(w io.Writer, indent string, u ModuleReach) {
	var names []string
	for _, n := range u.Std {
		if slices.Contains(u.StdTestsOnly, n) {
			n += " (tests only)"
		}
		names = append(names, n)
	}
	if len(names) > 0 {
		fmt.Fprintln(w, capability.Wrapped(indent, names))
	}
	switch {
	case u.BindsGo && u.GoTestsOnly:
		fmt.Fprintln(w, indent+"binds Go (tests only)")
	case u.BindsGo:
		fmt.Fprintln(w, indent+"binds Go")
	case len(names) == 0:
		fmt.Fprintln(w, indent+"nothing")
	}
}

// callCount is "1 call" or "n calls".
func callCount(n int) string {
	if n == 1 {
		return "1 call"
	}
	return fmt.Sprintf("%d calls", n)
}
