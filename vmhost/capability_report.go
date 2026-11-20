package vmhost

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

// CapabilityNotAllowedCode is the Code of the diagnostic for an outside
// call nomi.toml's capability tables, or a host's lists, do not allow. Its
// Args are the call's name (`std::fs`, or for a Go binding `go:` and the
// package it binds); the list it is over: `self` for [capabilities.self],
// `deps` for [capabilities.deps], `deps.modules` for the module's entry
// under [capabilities.deps.modules], or `host` for the host's lists; the
// module whose files hold the call, by its path, the project's own for its
// own files; and then what that list allows: each std name, or for a Go
// binding each dependency [capabilities.deps] lets bind Go, and nothing
// for [capabilities.self], whose go is false. Over nomi.toml's tables
// (not a host's), it carries the fix that adds what its help says to add,
// an edit to nomi.toml (parser.Fix's Path).
const CapabilityNotAllowedCode = "capability-not-allowed"

// CapabilityNotDeclaredCode is the Code of the diagnostic for an outside
// call in a dependency's files that the [capabilities.self] in the
// dependency's own nomi.toml does not allow (OverDeclared): the module
// declares less than its code reaches, and the fix is in that module. Its
// Args are CapabilityNotAllowedCode's, with the list `self`: the call's
// name, `self`, the module, and each std name its own std holds, none for
// a Go binding.
const CapabilityNotDeclaredCode = "capability-not-declared"

// capabilityProblems is the program held to its nomi.toml capability
// tables, and to the host's list when the host gave one
// (WithCapabilities), from the roots in rs: a diagnostic for each list
// entry that names nothing (capabilityListProblems), or, when every entry
// names something, one for each outside call the roots reach that a list
// does not allow, each call once per list, in position order, with the
// calls. Nil when there are no lists, and then nothing is walked.
func (p *Program) capabilityProblems(rs ...Roots) (Diagnostics, []NotAllowed) {
	if p.capabilities == nil && p.hostCapabilities() == nil {
		return nil, nil
	}
	return p.walkedProblems(p.walks(rs))
}

// walks is the outside calls each of rs reaches, one walk each
// (OutsideCalls).
func (p *Program) walks(rs []Roots) []Walk {
	out := make([]Walk, len(rs))
	for i, r := range rs {
		out[i] = p.walk(r)
	}
	return out
}

// walkedProblems is capabilityProblems over walks already walked.
func (p *Program) walkedProblems(walks []Walk) (Diagnostics, []NotAllowed) {
	host := p.hostCapabilities()
	if p.capabilities == nil && host == nil {
		return nil, nil
	}
	if ds := p.capabilityListProblems(); len(ds) > 0 {
		return ds, nil
	}
	var ds Diagnostics
	var calls []NotAllowed
	seen := map[string]bool{}
	fixes := map[string]*parser.Fix{}
	own := map[string]bool{}
	add := func(n NotAllowed, w Walk) {
		key := fmt.Sprint(n.Site, "\x00", n.Name, "\x00", n.Host)
		if seen[key] {
			return
		}
		seen[key] = true
		calls = append(calls, n)
		d := p.notAllowedDiagnostic(n, w.Tests, fixes)
		d.Related = p.leadsTo(n, w, own)
		ds = append(ds, d)
	}
	for _, w := range walks {
		var declared map[string]*analysis.SelfCapabilities
		if p.capabilities != nil {
			declared = p.declaredLists(w.Calls)
		}
		for _, n := range notAllowedBy(w.Calls, w.Tests, p.capabilities, declared, p.projectModule()) {
			add(n, w)
		}
		for _, n := range notAllowedBy(w.Calls, w.Tests, host, nil, p.projectModule()) {
			if !p.hostHolds(n) {
				continue
			}
			n.Host = true
			add(n, w)
		}
	}
	return ds, calls
}

// reachedCalls is what an editor's hover says code reaches outside the
// machine (analyzedlowering.Reached), from walks. First each outside call
// walks reach, once per call and name, in the walks' order: where a
// diagnostic about it points and its callee's span (shownSpan), and the
// line saying what it reaches and whether nomi.toml's lists allow it
// (capability.ReachedLine), as standingsOf decides. Then the lines of the
// functions the calls are in, and of those that call them
// (reachedFuncs). A call a walk's lists refuse reads as refused, whatever
// the other walks decide.
func (p *Program) reachedCalls(walks []Walk) []analyzedlowering.Reached {
	type key struct {
		at   ir.Pos
		name string
	}
	index := map[key]int{}
	var out []analyzedlowering.Reached
	listed := p.capabilities != nil
	standings := make([][]standing, len(walks))
	for wi, w := range walks {
		if listed {
			standings[wi] = standingsOf(w.Calls, w.Tests, p.capabilities, p.declaredLists(w.Calls), p.projectModule())
		}
		for i, c := range w.Calls {
			r := reachedOf(c, standings[wi], i)
			text := capability.ReachedLine(r, listed)
			k := key{c.at, c.Name}
			if j, seen := index[k]; seen {
				if !r.Allowed {
					out[j].Text = text
				}
				continue
			}
			index[k] = len(out)
			path, line, col, endLine, endCol := p.shownSpan(c.at)
			out = append(out, analyzedlowering.Reached{Path: path, Line: line, Col: col, EndLine: endLine, EndCol: endCol, Text: text})
		}
	}
	return append(out, p.reachedFuncs(walks, standings)...)
}

// reachedOf is how standings, the standings of a walk's calls, nil when
// the program has no lists, stand on c, its call at i, as a hover says it.
func reachedOf(c OutsideCall, standings []standing, i int) capability.Reached {
	r := capability.Reached{Name: c.Name, Allowed: true}
	if standings != nil {
		s := standings[i]
		r.Allowed, r.In = !s.refused, s.in
		if s.refused && s.List == OverDeclared {
			r.Own = c.Module
		}
	}
	return r
}

// reachedFuncs is the hover lines of the functions of the program and its
// dependencies that can reach outside the machine: for each function a
// walk reached whose edges lead to an outside call (vm.OutsideWalk.Reaches),
// the line saying what it can reach, through which functions, and whether
// nomi.toml's lists allow it (capability.FunctionReach), on its
// declaration's name and on each call of it, or use of it as a value, in
// code outside std. Each name counts once a function: the call of it the
// fewest edges away, unless a list refuses one, in which case the line
// says refused and goes through the refused one, as a call's line does;
// the tables allowing it in one module's files and in another's are named
// together. A generic function's instances share a declaration, whose line
// holds what any of them can reach, and a call of several functions (a
// call through an interface) holds what any of them can. A function with
// no declaration in source (a lambda, a synthesized body) has none.
// standings are each walk's standings (reachedCalls).
func (p *Program) reachedFuncs(walks []Walk, standings [][]standing) []analyzedlowering.Reached {
	// reach is what one declaration, or one call, can reach: each name's
	// line, in the order the names were first found.
	type reach struct {
		names []string
		by    map[string]capability.Reached
	}
	add := func(to *reach, r capability.Reached) {
		old, seen := to.by[r.Name]
		switch {
		case !seen:
			to.names = append(to.names, r.Name)
		case old.Allowed && r.Allowed:
			for _, in := range r.In {
				if !slices.Contains(old.In, in) {
					old.In = append(slices.Clone(old.In), in)
				}
			}
			r = old
		case !old.Allowed:
			r = old
		}
		to.by[r.Name] = r
	}
	type span struct {
		path                       string
		line, col, endLine, endCol int
	}
	var order []span
	at := map[span]*reach{}
	use := func(s span) *reach {
		if r := at[s]; r != nil {
			return r
		}
		r := &reach{by: map[string]capability.Reached{}}
		at[s] = r
		order = append(order, s)
		return r
	}
	for wi, w := range walks {
		if w.reach == nil {
			continue
		}
		key := func(i int) string { return w.Calls[i].Name + "\x00" + w.Calls[i].Module }
		for _, f := range w.reach.Reaches(key) {
			path := f.Func.Pos().File()
			def := p.funcDefAt(path, f.Func.Pos().Line(), f.Func.Pos().Col())
			if def == nil {
				continue
			}
			var lines []capability.Reached
			for _, rc := range f.Reached {
				r := reachedOf(w.Calls[rc.Call], standings[wi], rc.Call)
				r.Through = rc.Through
				lines = append(lines, r)
			}
			spans := []span{{path, def.Line, def.Col, def.Line, def.Col + len(def.Name)}}
			for _, site := range f.Sites {
				sp, line, col, endLine, endCol := p.shownSpan(site)
				spans = append(spans, span{sp, line, col, endLine, endCol})
			}
			for _, s := range spans {
				to := use(s)
				for _, r := range lines {
					add(to, r)
				}
			}
		}
	}
	listed := p.capabilities != nil
	out := make([]analyzedlowering.Reached, 0, len(order))
	for _, s := range order {
		r := at[s]
		lines := make([]capability.Reached, len(r.names))
		for i, n := range r.names {
			lines[i] = r.by[n]
		}
		out = append(out, analyzedlowering.Reached{Path: s.path, Line: s.line, Col: s.col, EndLine: s.endLine, EndCol: s.endCol,
			Text: capability.FunctionReach(lines, listed)})
	}
	return out
}

// CapabilityError is a program refused because it reaches outside calls
// the lists it is held to do not allow: nomi.toml's [capabilities] lists,
// or the host's (WithCapabilities). Calls and Diagnostics hold every call;
// its text gives one per name and list (Shown).
type CapabilityError struct {
	// Calls is each call a list does not allow, with whose list it is
	// over and the chain from a root that reaches it.
	Calls []NotAllowed
	// Diagnostics is the error for each of Calls, in the same order.
	Diagnostics Diagnostics
}

// AllCallsEnv names the environment variable that makes a
// *CapabilityError's text give every call; `--all-calls` on `nomi check`,
// `run`, `test` and `build` sets it. An environment variable rather than a
// flag alone, as frontend.DiagnosticsFormatEnv is, so an FFI program's
// wrapper process, which prints its own errors, prints them the same way.
const AllCallsEnv = "NOMI_ALL_CALLS"

func (e *CapabilityError) Error() string { return e.Shown().Error() }

// Unwrap is the diagnostics the text gives (Shown), which a caller that
// renders a load's errors finds with errors.As.
func (e *CapabilityError) Unwrap() error { return e.Shown() }

// Shown is the diagnostics the error's text gives: for each name and the
// list it is over, the first of its calls in Diagnostics' order, with a
// help line counting the others, and of the places the project's code
// leads to it (leadsTo) the first, counting the others; every diagnostic,
// with every place, when AllCallsEnv is set. A Go binding's group is its
// package and the module that binds it, and a host's list is one list over
// the whole program.
func (e *CapabilityError) Shown() Diagnostics {
	if os.Getenv(AllCallsEnv) != "" {
		return e.Diagnostics
	}
	key := func(n NotAllowed) string {
		if n.Host {
			return fmt.Sprint(n.Name, "\x00", n.Binder, "\x00", n.Host, n.Tests)
		}
		return fmt.Sprint(n.Name, "\x00", n.List, "\x00", n.Module, "\x00", n.Binder, "\x00", n.Tests)
	}
	first := map[string]int{}
	more := map[string]int{}
	var order []string
	for i, n := range e.Calls {
		k := key(n)
		if _, seen := first[k]; seen {
			more[k]++
			continue
		}
		first[k] = i
		order = append(order, k)
	}
	out := make(Diagnostics, len(order))
	for i, k := range order {
		d := e.Diagnostics[first[k]]
		if n := more[k]; n > 0 {
			d.Hints = append(slices.Clone(d.Hints), moreCalls(n, e.Calls[first[k]].Name))
		}
		if n := len(d.Related) - 1; n > 0 {
			r := d.Related[0]
			r.Message += "; " + morePlaces(n)
			d.Related = []frontend.Related{r}
		}
		out[i] = d
	}
	return out
}

// morePlaces is what a refusal's text adds to the first place the
// project's code leads to the call (leadsTo) when n more do.
func morePlaces(n int) string {
	if n == 1 {
		return "and 1 more place in your code; --all-calls lists it"
	}
	return fmt.Sprintf("and %d more places in your code; --all-calls lists them", n)
}

// moreCalls is the help line of a grouped refusal that stands for n more
// calls of name over the same list, and says how to see them.
func moreCalls(n int, name string) string {
	if n == 1 {
		return fmt.Sprintf("and 1 more call reaches %s; --all-calls lists it", name)
	}
	return fmt.Sprintf("and %d more calls reach %s; --all-calls lists them", n, name)
}

// capabilityError is capabilityProblems as an error, nil when there are
// none: a *CapabilityError for calls the lists do not allow, and the
// diagnostics alone for list entries that name nothing.
func (p *Program) capabilityError(rs ...Roots) error {
	ds, calls := p.capabilityProblems(rs...)
	switch {
	case len(calls) > 0:
		return &CapabilityError{Calls: calls, Diagnostics: ds}
	case len(ds) > 0:
		return ds
	}
	return nil
}

// holdToHost refuses the program when the host holds it to a list
// (WithCapabilities, WithGo) and the list has a name that is not one, or the
// program reaches a call the lists do not allow from anything a host can
// run: main and the boot, every function the entry declares, which Call
// may call, and the test cases.
func (p *Program) holdToHost() error {
	if !p.cfg.held && !p.cfg.goHeld {
		return nil
	}
	if err := p.hostCapabilityProblem(); err != nil {
		return err
	}
	rs := []Roots{{Exports: true, Callable: true}}
	if p.prog.HasTests {
		rs = append(rs, Roots{Tests: &TestOptions{}})
	}
	return p.capabilityError(rs...)
}

// checkedRoots is what `nomi check` holds to the lists, as it lowers: main
// and the boot with each exported function, and each test case.
func (p *Program) checkedRoots() []Roots {
	rs := []Roots{{Exports: p.HasExports()}}
	if p.prog.HasTests {
		rs = append(rs, Roots{Tests: &TestOptions{}})
	}
	return rs
}

// notAllowedDiagnostic is the error for n, reached from tests when tests is
// set: at the call, in the program's or a dependency's file, saying who
// reaches what and what the list allows, how the call is reached, and
// which key of which table to add what to (notAllowedHelp). A call over
// nomi.toml's tables carries the fix that adds it (allowFix), kept in
// fixes for the calls after it.
func (p *Program) notAllowedDiagnostic(n NotAllowed, tests bool, fixes map[string]*parser.Fix) frontend.Diagnostic {
	path, line, col := p.shownAt(n.OutsideCall)
	module := cmp.Or(n.Module, p.projectModule())
	var message string
	list, allowed := string(n.List), n.Allowed
	switch {
	case n.Host && n.Binder != "":
		// The host's WithGo is one list over the whole program.
		list, allowed = "host", p.cfg.goModules
		message = capability.GoMessage(n.Binder, n.Name, "the host", "module", allowed)
	case n.Host && tests:
		list = "host"
		message = capability.Message("this program's tests reach", n.Name, "the host", " them", n.Allowed)
	case n.Host:
		list = "host"
		message = capability.Message("this program reaches", n.Name, "the host", "", n.Allowed)
	case n.List == OverSelf && n.Binder != "":
		message = capability.GoFlagMessage("this module's own code", n.Name, "[capabilities.self]")
	case n.List == OverSelf && tests:
		message = capability.Message("this module's tests reach", n.Name, "[capabilities.self]", " them", n.Allowed)
	case n.List == OverSelf:
		message = capability.Message("this module's own code reaches", n.Name, "[capabilities.self]", " it", n.Allowed)
	case n.List == OverDeps && n.Binder != "":
		message = capability.GoMessage(n.Module, n.Name, "[capabilities.deps]", "dependency", n.Allowed)
	case n.List == OverDeps:
		message = capability.Message(n.Module+" reaches", n.Name, "[capabilities.deps]", " dependencies", n.Allowed)
	case n.List == OverEntry:
		message = capability.Message(n.Module+" reaches", n.Name, "its entry under [capabilities.deps.modules]", " it", n.Allowed)
	case n.Binder != "":
		list = string(OverSelf)
		message = capability.GoFlagMessage(n.Module, n.Name, "its own [capabilities.self]")
	default:
		list = string(OverSelf)
		message = capability.Message(n.Module+" reaches", n.Name, "its own [capabilities.self]", " it", n.Allowed)
	}
	d := frontend.NewDiagnostic(path, p.sources[path], line, col, message)
	d.Code = CapabilityNotAllowedCode
	if n.List == OverDeclared && !n.Host {
		d.Code = CapabilityNotDeclaredCode
	}
	d.Args = append([]string{n.Name, list, module}, allowed...)
	d.Hints = append(append(d.Hints, n.ReachText()), p.notAllowedHelp(n, tests)...)
	if !n.Host && n.List != OverDeclared {
		d.Fix = p.allowFix(n, tests, fixes)
	}
	return d
}

// leadsTo is where the project's own code leads to n, a refused call in a
// dependency's files, by w, the walk that found it: each place a function
// in the project's files last steps out of them toward the call
// (vm.OutsideWalk.Leads), at the callee it calls or the value it uses
// (shownSpan), saying what the project's code reaches there and how. None
// for a call in the project's own files. own keeps, by file, whether a file
// is the project's.
func (p *Program) leadsTo(n NotAllowed, w Walk, own map[string]bool) []frontend.Related {
	if n.Module == "" || w.reach == nil {
		return nil
	}
	within := func(f *ir.Func) bool {
		file := f.Pos().File()
		if file == "" || f.Pos().Stdlib() {
			return false
		}
		is, done := own[file]
		if !done {
			is = p.moduleOf(file) == ""
			own[file] = is
		}
		return is
	}
	reached := fmt.Sprintf("%s in %s", n.Name, n.Module)
	if n.Binder != "" {
		reached = fmt.Sprintf("%s's Go binding %s", n.Module, n.Name)
	}
	var out []frontend.Related
	for _, l := range w.reach.Leads(n.index, within) {
		path, line, col, endLine, endCol := p.shownSpan(l.At)
		out = append(out, frontend.Related{Path: path, Line: line, Col: col, EndLine: endLine, EndCol: endCol,
			Message: fmt.Sprintf("your code reaches %s here: %s", reached, l.Text)})
	}
	return out
}

// keys is the keys of a table that hold a call reached from tests when
// tests is set: its std list and its go, with _in_tests for tests.
func keys(tests bool) (std, goKey string) {
	if tests {
		return "std_in_tests", "go_in_tests"
	}
	return "std", "go"
}

// addition is how a help line says to allow n in the project's tables:
// what to add (`add "std::fs"`) and the lists to add it to (`to std under
// [capabilities.deps]`), the list n names first, then [capabilities.deps]
// when it denies the call too; or for a flag, a key to set, with no list.
func addition(n NotAllowed, tests bool) (item string, lists []string) {
	std, goKey := keys(tests)
	item, key := n.Name, std
	if n.Binder != "" {
		item, key = n.Binder, goKey
	}
	switch {
	case n.List == OverSelf && n.Binder != "":
		return fmt.Sprintf("set %s = true under [capabilities.self]", goKey), nil
	case n.List == OverSelf:
		return fmt.Sprintf("add %q", item), []string{fmt.Sprintf("to %s under [capabilities.self]", key)}
	case n.List == OverEntry || n.List == OverDeclared && n.EntryDenies:
		lists = append(lists, fmt.Sprintf("to %q under [capabilities.deps.modules]", n.Module))
	}
	if n.List == OverDeps || n.DepsDenies {
		lists = append(lists, fmt.Sprintf("to %s under [capabilities.deps]", key))
	}
	return fmt.Sprintf("add %q", item), lists
}

// notAllowedHelp is the help for n after how it is reached: which key of
// which table to add what to, or remove the call. For a call over a
// dependency's own [capabilities.self], what the project's tables say of it
// when they do not allow it either, and that the fix is in that module's
// own nomi.toml, named by its path.
func (p *Program) notAllowedHelp(n NotAllowed, tests bool) []string {
	remove := ", or " + capability.RemoveHint(n.Edge == vm.EdgeBoot)
	switch {
	case n.Host && n.Binder != "":
		return []string{capability.HostAddHint(n.Binder, "vmhost.WithGo") + remove}
	case n.Host:
		return []string{capability.HostAddHint(n.Name, "vmhost.WithCapabilities") + remove}
	case n.List == OverDeclared:
		var hints []string
		if item, lists := addition(n, tests); len(lists) > 0 {
			hints = append(hints, fmt.Sprintf("the project's nomi.toml does not allow %s to it either; to allow it there, %s %s",
				n.Name, item, strings.Join(lists, " and ")))
		}
		toml := "its nomi.toml"
		if m := p.moduleAt(n.at.File()); m != nil {
			toml = p.tomlName(m.root)
		}
		own := fmt.Sprintf("add %q to std under [capabilities.self]", n.Name)
		if n.Binder != "" {
			own = "set go = true under [capabilities.self]"
		}
		return append(hints, fmt.Sprintf("%s declares less than its code reaches, and the fix is in that module: %s in %s%s",
			n.Module, own, toml, remove))
	}
	var hints []string
	if n.List == OverEntry && n.DepsDenies {
		std, _ := keys(tests)
		hints = append(hints, fmt.Sprintf("%s under [capabilities.deps] does not allow %s either", std, n.Name))
	}
	item, lists := addition(n, tests)
	if len(lists) > 0 {
		item += " " + strings.Join(lists, " and ")
	}
	return append(hints, item+" in nomi.toml"+remove)
}

// allowFix is the fix of n, a call over nomi.toml's tables reached from
// tests when tests is set: the edit to nomi.toml that adds what the help
// says to add to each list the call is over (analysis.AllowCapabilities),
// titled by what it allows. It is nil when the program's nomi.toml is
// staged in memory, or the edit cannot be written. fixes keeps each fix by
// what it adds.
func (p *Program) allowFix(n NotAllowed, tests bool, fixes map[string]*parser.Fix) *parser.Fix {
	path, data := p.manifestPath()
	if data == nil || !filepath.IsAbs(path) {
		return nil
	}
	selfStd, selfGo, depsStd, depsGo, inTests := analysis.ListSelfStd, analysis.ListSelfGo, analysis.ListDepsStd, analysis.ListDepsGo, ""
	if tests {
		selfStd, selfGo, depsStd, depsGo, inTests = analysis.ListSelfStdInTests, analysis.ListSelfGoInTests, analysis.ListDepsStdInTests, analysis.ListDepsGoInTests, " in tests"
	}
	var adds []analysis.CapabilityAdd
	var title string
	switch {
	case n.List == OverSelf && n.Binder != "":
		adds = []analysis.CapabilityAdd{{List: selfGo}}
		title = "Allow this module to bind Go in nomi.toml"
		if tests {
			title = "Allow this module's tests to bind Go in nomi.toml"
		}
	case n.List == OverSelf:
		adds = []analysis.CapabilityAdd{{List: selfStd, Name: n.Name}}
		title = fmt.Sprintf("Allow %s for this module in nomi.toml", n.Name)
		if tests {
			title = fmt.Sprintf("Allow %s for this module's tests in nomi.toml", n.Name)
		}
	case n.Binder != "":
		adds = []analysis.CapabilityAdd{{List: depsGo, Name: n.Binder}}
		title = fmt.Sprintf("Allow %s to bind Go%s in nomi.toml", n.Binder, inTests)
	case n.List == OverEntry:
		adds = []analysis.CapabilityAdd{{List: analysis.ListDepsModules, Module: n.Module, Name: n.Name}}
		if n.DepsDenies {
			adds = append(adds, analysis.CapabilityAdd{List: depsStd, Name: n.Name})
		}
		title = fmt.Sprintf("Allow %s for %s in nomi.toml", n.Name, n.Module)
	default:
		adds = []analysis.CapabilityAdd{{List: depsStd, Name: n.Name}}
		title = fmt.Sprintf("Allow %s for dependencies%s in nomi.toml", n.Name, inTests)
	}
	key := fmt.Sprint(adds)
	if fix, done := fixes[key]; done {
		return fix
	}
	fix, _ := analysis.AllowCapabilities(path, data, p.capabilities, title, adds)
	fixes[key] = fix
	return fix
}

// shownAt is where a report about c points: its file as the program names
// it, and the line and column of the call's callee, where the author
// reads the call, rather than the call's own position, its parenthesis.
func (p *Program) shownAt(c OutsideCall) (path string, line, col int) {
	path, line, col, _, _ = p.shownSpan(c.at)
	return path, line, col
}

// shownSpan is where a report shows the call at, as shownAt says, with the
// end of the call's callee, one past its last byte; the end is the start
// when there is no syntax for a call there (syntaxOf).
func (p *Program) shownSpan(at ir.Pos) (path string, line, col, endLine, endCol int) {
	path, line, col = at.File(), at.Line(), at.Col()
	endLine, endCol = line, col
	if call, ok := p.indexOf(path).at[[2]int{line, col}].(*ast.Call); ok {
		if l, c := leftmost(call.Func); l > 0 {
			line, col = l, c
			endLine, endCol = l, c
			if s, ok := call.Func.(ast.HasSpan); ok && !s.GetSpan().IsZero() {
				endLine, endCol = s.GetSpan().EndLine, s.GetSpan().EndCol
			}
		}
	}
	return path, line, col, endLine, endCol
}

// syntaxIndex is where a file's syntax (syntaxOf) has what a report shows:
// at is the node nodeAtIn answers for each position some node starts at,
// and funcs each function the file declares, by the position of its name
// and of its `fn`, either of which its lowered body's position is.
type syntaxIndex struct {
	at    map[[2]int]ast.Node
	funcs map[[2]int]*ast.FuncDef
}

// indexOf is the syntaxIndex of the program's file at path, built once.
func (p *Program) indexOf(path string) *syntaxIndex {
	nodes := p.syntaxOf(path)
	p.parsedMu.Lock()
	defer p.parsedMu.Unlock()
	if ix := p.indexed[path]; ix != nil {
		return ix
	}
	ix := &syntaxIndex{at: map[[2]int]ast.Node{}, funcs: map[[2]int]*ast.FuncDef{}}
	for _, top := range nodes {
		analysis.WalkNodes(top, func(n ast.Node) {
			if l, c := nodeStart(n); l > 0 {
				if _, seen := ix.at[[2]int{l, c}]; !seen {
					ix.at[[2]int{l, c}] = n
				}
			}
			if fd, ok := n.(*ast.FuncDef); ok && fd.Line > 0 {
				ix.funcs[[2]int{fd.Line, fd.Col}] = fd
				ix.funcs[[2]int{fd.Line, fd.FnCol}] = fd
			}
		})
	}
	if p.indexed == nil {
		p.indexed = map[string]*syntaxIndex{}
	}
	p.indexed[path] = ix
	return ix
}

// funcDefAt is the function the program's file at path declares at line
// and col (syntaxIndex), or nil.
func (p *Program) funcDefAt(path string, line, col int) *ast.FuncDef {
	return p.indexOf(path).funcs[[2]int{line, col}]
}

// syntaxOf is the syntax of the program's file at path: its module's, or,
// for a file whose syntax the program does not hold (a program relinked
// from kept objects holds at most its entry's), its text (sourceOf)
// parsed, once. Nil for a std file and for a text that does not parse.
func (p *Program) syntaxOf(path string) []ast.Node {
	if m := p.modulePath(path); m != nil && m.Nodes != nil {
		return m.Nodes
	}
	if path == "" || std.IsSourcePath(path) {
		return nil
	}
	p.parsedMu.Lock()
	defer p.parsedMu.Unlock()
	if nodes, done := p.parsed[path]; done {
		return nodes
	}
	var nodes []ast.Node
	if text, err := p.sourceOf(path); err == nil {
		if parsed, err := parser.Parse(lexer.Lex(text)); err == nil {
			nodes = parsed
		}
	}
	if p.parsed == nil {
		p.parsed = map[string][]ast.Node{}
	}
	p.parsed[path] = nodes
	return nodes
}
