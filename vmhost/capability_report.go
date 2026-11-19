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
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/vm"
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
		out[i] = Walk{Tests: r.Tests != nil, Calls: p.OutsideCalls(r)}
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
	add := func(n NotAllowed, tests bool) {
		key := fmt.Sprint(n.Site, "\x00", n.Name, "\x00", n.Host)
		if seen[key] {
			return
		}
		seen[key] = true
		calls = append(calls, n)
		ds = append(ds, p.notAllowedDiagnostic(n, tests, fixes))
	}
	for _, w := range walks {
		var declared map[string]*analysis.SelfCapabilities
		if p.capabilities != nil {
			declared = p.declaredLists(w.Calls)
		}
		for _, n := range notAllowedBy(w.Calls, w.Tests, p.capabilities, declared, p.projectModule()) {
			add(n, w.Tests)
		}
		for _, n := range notAllowedBy(w.Calls, w.Tests, host, nil, p.projectModule()) {
			if !p.hostHolds(n) {
				continue
			}
			n.Host = true
			add(n, w.Tests)
		}
	}
	return ds, calls
}

// reachedCalls is each outside call walks reach, once per call and name,
// in the walks' order, as an editor's hover shows it
// (analyzedlowering.Reached): where a diagnostic about it points and its
// callee's span (shownSpan), and the line saying what it reaches and
// whether nomi.toml's lists allow it (capability.ReachedLine), as
// standingsOf decides. A call a walk's lists refuse reads as refused,
// whatever the other walks decide.
func (p *Program) reachedCalls(walks []Walk) []analyzedlowering.Reached {
	type key struct {
		at   ir.Pos
		name string
	}
	index := map[key]int{}
	var out []analyzedlowering.Reached
	for _, w := range walks {
		var standings []standing
		if p.capabilities != nil {
			standings = standingsOf(w.Calls, w.Tests, p.capabilities, p.declaredLists(w.Calls), p.projectModule())
		}
		for i, c := range w.Calls {
			allowed, own := true, ""
			var in []string
			if standings != nil {
				s := standings[i]
				allowed, in = !s.refused, s.in
				if s.refused && s.List == OverDeclared {
					own = c.Module
				}
			}
			text := capability.ReachedLine(c.Name, standings != nil, allowed, in, own)
			k := key{c.at, c.Name}
			if j, seen := index[k]; seen {
				if !allowed {
					out[j].Text = text
				}
				continue
			}
			index[k] = len(out)
			path, line, col, endLine, endCol := p.shownSpan(c)
			out = append(out, analyzedlowering.Reached{Path: path, Line: line, Col: col, EndLine: endLine, EndCol: endCol, Text: text})
		}
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
// help line counting the others; every diagnostic when AllCallsEnv is
// set. A Go binding's group is its package and the module that binds it,
// and a host's list is one list over the whole program.
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
	if len(order) == len(e.Calls) {
		return e.Diagnostics
	}
	out := make(Diagnostics, len(order))
	for i, k := range order {
		d := e.Diagnostics[first[k]]
		if n := more[k]; n > 0 {
			d.Hints = append(slices.Clone(d.Hints), moreCalls(n, e.Calls[first[k]].Name))
		}
		out[i] = d
	}
	return out
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
	path, line, col, _, _ = p.shownSpan(c)
	return path, line, col
}

// shownSpan is shownAt with the end of the call's callee, one past its last
// byte; the end is the start when the program holds no syntax for the call.
func (p *Program) shownSpan(c OutsideCall) (path string, line, col, endLine, endCol int) {
	path, line, col = c.at.File(), c.at.Line(), c.at.Col()
	endLine, endCol = line, col
	if call, ok := nodeAt(p.modulePath(path), line, col).(*ast.Call); ok {
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
