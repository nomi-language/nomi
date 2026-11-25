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
	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
)

// Reach is the effects a project's programs have, from the roots `nomi
// check` holds to nomi.toml's lists (checkedRoots), with the module each
// call is charged to: what `nomi effects` reports and writes. One program's Reach is Program.Reach; a project of several
// entries and test files adds each one's (Add).
type Reach struct {
	// Root is the project's directory, the one holding its nomi.toml, or
	// "" for a program with none.
	Root string
	// Project is the project's own package (projectPackage), whose own
	// files [effects.self] holds.
	Project string
	// Packages is each dependency an entry under [effects.deps.packages]
	// may name that the programs' code comes from: each package the project
	// needs whose Nomi files the programs hold (requiredPackages), by its name,
	// sorted.
	Packages []string
	// OwnModules is each module of the project whose files the programs
	// hold, by its import path (ownModules), sorted: the keys an entry
	// under [effects.self.modules] is written for.
	OwnModules []string
	// Lists is nomi.toml's effects tables, nil when it has neither.
	Lists *Effects
	// Walks is each walk's calls, the walks from main before the walks
	// from the tests for each program.
	Walks []Walk
	// ListProblems is what `nomi check` reports about the lists themselves
	// before it holds any call to them (effectListProblems): a name
	// that is no std module, a name that is no dependency, a
	// dependency entries_required finds with no entry. Each is given once.
	ListProblems Diagnostics
	// Undetermined is, when [effects.self] holds the project's own files, an
	// error for each public generic function of them whose effects for
	// every type it may be called at are not known (undeterminedDiagnostics),
	// which `nomi check` reports too. Each is given once.
	Undetermined Diagnostics
	// declared is each dependency's own [effects.self]
	// (declaredLists).
	declared map[string]*analysis.SelfEffects
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

// Reached loads the program at path as Check does, holds it to no list,
// and answers what it reaches (Program.Reach). The errors are
// Check's, but for the lists' own.
func Reached(path string, opts ...Option) (*Reach, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, _, ok, err := frontend.StdlibFile(abs); err != nil {
		return nil, err
	} else if ok {
		return nil, fmt.Errorf("%s is a std module, which no nomi.toml holds", path)
	}
	p, err := checkedProgram(newConfig(opts), abs)
	if err != nil {
		return nil, err
	}
	return p.Reach(), nil
}

// Reach is the effects the program has from the roots
// `nomi check` holds to the lists: main, the boot and the exported
// functions, and the test cases.
func (p *Program) Reach() *Reach {
	r := &Reach{Root: p.prog.Root, Project: p.projectPackage(), Lists: p.effects, ListProblems: p.effectListProblems(),
		declared: map[string]*analysis.SelfEffects{}, sites: map[ir.Pos]shownSite{}}
	for _, m := range p.requiredPackages() {
		if problem, _ := p.dependencyProblem(m.path); problem == "" && !slices.Contains(r.Packages, m.path) {
			r.Packages = append(r.Packages, m.path)
		}
	}
	slices.Sort(r.Packages)
	r.OwnModules = p.ownModules()
	r.Walks = p.walks(p.checkedRoots())
	if p.effects != nil && p.effects.Self != nil {
		r.Undetermined = p.undeterminedDiagnostics()
	}
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
	for _, into := range []struct {
		list *Diagnostics
		from Diagnostics
	}{{&r.ListProblems, o.ListProblems}, {&r.Undetermined, o.Undetermined}} {
		for _, d := range into.from {
			if !slices.ContainsFunc(*into.list, func(e Diagnostic) bool { return e.String() == d.String() }) {
				*into.list = append(*into.list, d)
			}
		}
	}
	for _, into := range []struct {
		list *[]string
		from []string
	}{{&r.Packages, o.Packages}, {&r.OwnModules, o.OwnModules}} {
		for _, m := range into.from {
			if !slices.Contains(*into.list, m) {
				*into.list = append(*into.list, m)
			}
		}
		slices.Sort(*into.list)
	}
	if r.declared == nil {
		r.declared = map[string]*analysis.SelfEffects{}
	}
	maps.Copy(r.declared, o.declared)
	if r.sites == nil {
		r.sites = map[ir.Pos]shownSite{}
	}
	maps.Copy(r.sites, o.sites)
}

// SiteOf is where a report shows c, `file:line:col` with the file named
// as Site names it: the call's callee, where a diagnostic about it points.
func (r *Reach) SiteOf(c EffectCall) string {
	return cmp.Or(r.sites[c.at].text, c.Site)
}

// PackageOf is the package c is charged to, by its path: the dependency
// whose files hold it, or the project.
func (r *Reach) PackageOf(c EffectCall) string {
	return cmp.Or(c.Package, r.Project)
}

// Tightest is the smallest pair of effects tables the walks pass
// (tightestLists): with it as nomi.toml's, NotAllowed answers only the
// calls a dependency's own table refuses. Its [effects.deps.packages]
// has an entry for each dependency, and its [effects.self.modules] one
// for each module of the project when nomi.toml has that table or
// entries_required under [effects.self], and is left out otherwise:
// an entry for a module of the project is a choice the file makes.
func (r *Reach) Tightest() *Effects {
	var own []string
	if s := r.Lists; s != nil && s.Self != nil && (s.Self.Modules != nil || s.Self.EntriesRequired) {
		own = r.OwnModules
	}
	return tightestLists(r.Walks, r.Packages, own)
}

// EntryKeys is the keys an entry of list, one of analysis.EntryLists, may
// have for the programs' code: OwnModules for [effects.self.modules],
// Modules for [effects.deps.packages].
func (r *Reach) EntryKeys(list string) []string {
	if list == analysis.ListSelfModules {
		return r.OwnModules
	}
	return r.Packages
}

// Entries is the tightest entry of each of list's keys (EntryKeys): the std
// names the code of that key reaches from every walk, whether or not
// nomi.toml has the table.
func (r *Reach) Entries(list string) map[string][]string {
	return tightestLists(r.Walks, r.Packages, r.OwnModules).Entries(list)
}

// NotAllowed is each call the walks reach that lists, with each
// dependency's own table, does not allow (notAllowedBy), each call once
// per list, as `nomi check` reports them; nil lists allow everything.
func (r *Reach) NotAllowed(lists *Effects) []NotAllowed {
	var out []NotAllowed
	seen := map[string]bool{}
	for _, w := range r.Walks {
		for _, n := range notAllowedBy(w.Calls, w.Tests, lists, r.declared) {
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
// Reached, added (Add), as `nomi effects` reports a project whose
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

// WhyCall is one call that has an effect, or that a package's or module's
// files make (Reach.Why): the effect's name, the package charged for it
// (PackageOf: the dependency whose files hold it, or the project), where a
// report shows it (SiteOf), its callee, whether a test case reaches it, and
// how it is reached in the words a refusal's help uses (ReachText). Import
// is its file as its own package's code imports it (EffectCall.Import).
// Path is its file as the program names it, and Line, Col, EndLine and
// EndCol the span of its callee, 1-based with byte columns, the end one
// past the last byte.
type WhyCall struct {
	Name    string `json:"name"`
	Package string `json:"package"`
	Site    string `json:"site"`
	Callee  string `json:"callee"`
	Tests   bool   `json:"tests"`
	Reach   string `json:"reach"`
	Import  string `json:"-"`
	Path    string `json:"-"`
	Line    int    `json:"-"`
	Col     int    `json:"-"`
	EndLine int    `json:"-"`
	EndCol  int    `json:"-"`
}

// Why is each call the walks reach that has the effect name, a std name
// (`std::fs`), or, for a dependency by its name in the program, that its
// files make, or, for a module of the project by its import path
// (`pricing`), that its files make, once per site and name, in the walks'
// order: what `nomi effects --why name` prints. A name nothing has
// is an error that names the effects the walks find and the modules their
// code comes from.
func (r *Reach) Why(name string) ([]WhyCall, error) {
	byName := strings.HasPrefix(name, "std::")
	var found []WhyCall
	seen := map[string]bool{}
	var names []string
	for _, wk := range r.Walks {
		for _, c := range wk.Calls {
			if !slices.Contains(names, c.Name) {
				names = append(names, c.Name)
			}
			module := r.PackageOf(c)
			if byName && c.Name != name || !byName && module != name && (c.Package != "" || c.Import != name) {
				continue
			}
			key := c.Site + "\x00" + c.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			site := r.sites[c.at]
			found = append(found, WhyCall{Name: c.Name, Package: module, Site: r.SiteOf(c), Callee: c.Callee, Tests: wk.Tests, Reach: c.ReachText(),
				Import: c.Import, Path: site.path, Line: site.line, Col: site.col, EndLine: site.endLine, EndCol: site.endCol})
		}
	}
	if len(found) == 0 {
		slices.Sort(names)
		lacks := fmt.Sprintf("this program does not have %s", effects.EffectList([]string{name}))
		if !byName {
			lacks = fmt.Sprintf("nothing in %s has effects in this program", name)
		}
		return nil, fmt.Errorf("%s; it has %s, and its code comes from %s",
			lacks, effects.EffectList(names), strings.Join(append([]string{r.Project}, r.Packages...), ", "))
	}
	return found, nil
}

// WriteWhy prints calls, what Why answered for name, as `nomi effects
// --why` prints them: a line counting them, then each call's site, with the
// module when it is a dependency's (or, for a dependency, the effect
// the call has), and how it is reached. With limit above 0 it prints at
// most limit calls and a line counting the rest.
func (r *Reach) WriteWhy(w io.Writer, name string, calls []WhyCall, limit int) {
	byName := strings.HasPrefix(name, "std::")
	verb, has := "have", "have effects"
	if len(calls) == 1 {
		verb, has = "has", "has an effect"
	}
	switch {
	case byName:
		fmt.Fprintf(w, "%s %s %s:\n", callCount(len(calls)), verb, effects.EffectList([]string{name}))
	case name == r.Project:
		fmt.Fprintf(w, "%s in this project's own files %s:\n", callCount(len(calls)), has)
	default:
		fmt.Fprintf(w, "%s in %s %s:\n", callCount(len(calls)), name, has)
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
		case c.Package != r.Project:
			where += " in " + c.Package
		}
		fmt.Fprintf(w, "  %s\n    %s\n", where, c.Reach)
	}
}

// HeldChanges is changes, from lists to the tightest tables
// (analysis.EffectChanges), without those `nomi effects --check`
// does not hold the tables to, since leaving them out is a choice and not
// a difference: a table the file leaves out, [effects.deps]' ceiling
// when the table leaves out its std, and the entry of a module of the
// project or a dependency the table of entries gives none, which only the
// table above it (and a dependency's own table) holds, unless
// entries_required there asks every one for an entry. A module the table
// does give an entry is held to the tightest one, like every list. Nil
// lists hold nothing back.
func HeldChanges(changes []analysis.EffectChange, lists *Effects) []analysis.EffectChange {
	if lists == nil {
		return changes
	}
	var out []analysis.EffectChange
	for _, c := range changes {
		self := c.List == analysis.ListSelf || strings.HasPrefix(c.List, analysis.ListSelf+".")
		switch {
		case self && lists.Self == nil:
		case !self && lists.Deps == nil:
		case (c.List == analysis.ListDepsStd || c.List == analysis.ListDepsStdInTests) && lists.Deps.Std == nil:
		case slices.Contains(analysis.EntryLists, c.List) && c.Change == "add" && !entriesRequired(lists, c.List) && !hasEntry(lists, c.List, c.Key):
		default:
			out = append(out, c)
		}
	}
	return out
}

// entriesRequired reports whether lists set entries_required in the table
// list, one of analysis.EntryLists, is under.
func entriesRequired(lists *Effects, list string) bool {
	if list == analysis.ListSelfModules {
		return lists.Self.EntriesRequired
	}
	return lists.Deps.EntriesRequired
}

// hasEntry reports whether lists give key an entry in list, one of
// analysis.EntryLists.
func hasEntry(lists *Effects, list, key string) bool {
	_, has := lists.Entries(list)[key]
	return has
}

// PackageReach is the effects one dependency's own files have, as `nomi
// effects` prints it under deps' packages (WritePackageReach).
type PackageReach struct {
	Path string `json:"path"`
	// Std is each std name the module's files have; StdTestsOnly those
	// of them only the tests have.
	Std          []string `json:"std"`
	StdTestsOnly []string `json:"std_tests_only"`
}

// PackageReaches is the effects each dependency the programs' code comes
// from has, sorted by path.
func (r *Reach) PackageReaches() []PackageReach {
	return r.EntryReaches(analysis.ListDepsPackages)
}

// EntryReaches is the effects the code of each key of list, one of
// analysis.EntryLists, has, sorted by key: each module of the project,
// by its import path, for [effects.self.modules], and each dependency
// the programs' code comes from, by its path, for
// [effects.deps.packages]. An effect no list names (a `dbg`) is not
// among them.
func (r *Reach) EntryReaches(list string) []PackageReach {
	byPath := map[string]*PackageReach{}
	use := func(path string) *PackageReach {
		if u, ok := byPath[path]; ok {
			return u
		}
		u := &PackageReach{Path: path, Std: []string{}, StdTestsOnly: []string{}}
		byPath[path] = u
		return u
	}
	for _, m := range r.EntryKeys(list) {
		use(m)
	}
	// keyOf is the key of list c is charged to, false when list holds
	// none of its module's code.
	keyOf := func(c EffectCall) (string, bool) {
		if list == analysis.ListSelfModules {
			return c.Import, c.Package == ""
		}
		return c.Package, c.Package != ""
	}
	// fromMain is each module and std name that a walk from main reaches.
	type key struct{ module, name string }
	fromMain := map[key]bool{}
	for _, w := range r.Walks {
		for _, c := range w.Calls {
			k, ok := keyOf(c)
			if !ok || !effects.Listed(c.Name) {
				continue
			}
			u := use(k)
			if !slices.Contains(u.Std, c.Name) {
				u.Std = append(u.Std, c.Name)
			}
			if !w.Tests {
				fromMain[key{u.Path, c.Name}] = true
			}
		}
	}
	out := []PackageReach{}
	for _, u := range byPath {
		slices.Sort(u.Std)
		for _, n := range u.Std {
			if !fromMain[key{u.Path, n}] {
				u.StdTestsOnly = append(u.StdTestsOnly, n)
			}
		}
		out = append(out, *u)
	}
	slices.SortFunc(out, func(a, b PackageReach) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// WritePackageReach prints, under a dependency's name, indented by indent, the
// effects its own files have: its std names, with (tests only) on those
// only the tests have, or no effects.
func WritePackageReach(w io.Writer, indent string, u PackageReach) {
	var names []string
	for _, n := range u.Std {
		if slices.Contains(u.StdTestsOnly, n) {
			n += " (tests only)"
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		fmt.Fprintln(w, indent+"no effects")
		return
	}
	fmt.Fprintln(w, effects.Wrapped(indent, names))
}

// callCount is "1 call" or "n calls".
func callCount(n int) string {
	if n == 1 {
		return "1 call"
	}
	return fmt.Sprintf("%d calls", n)
}
