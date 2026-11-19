package vmhost

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/vm"
)

// CapabilityNotAllowedCode is the Code of the diagnostic for an outside
// call nomi.toml's [capabilities] lists do not allow. Its Args are the
// call's name (`std::fs`), the module over its list (its Go module path,
// the project's own for its own entry under [capabilities.modules], or ""
// for the program's list), and then each name that list allows. For
// a Go binding they are its name (`go:` and the package it binds), the
// module that binds Go (NotAllowed.Binder), the one to list under `go`,
// and then each module the list lets bind Go.
const CapabilityNotAllowedCode = "capability-not-allowed"

// CapabilityNotDeclaredCode is the Code of the diagnostic for an outside
// call in a dependency's files that the dependency's own nomi.toml does
// not allow (NotAllowed.Declared): the module declares less than its code
// reaches, and the fix is in that module. Its Args are
// CapabilityNotAllowedCode's: the call's name, the module, and each name
// the module's own allow holds.
const CapabilityNotDeclaredCode = "capability-not-declared"

// capabilityProblems is the program held to its nomi.toml [capabilities]
// lists, and to the host's list when the host gave one
// (WithCapabilities), from the roots in rs: a diagnostic for each list
// entry that names nothing (capabilityListProblems), or, when every entry
// names something, one for each outside call the roots reach that a list
// does not allow, each call once per list, in position order, with the
// calls. Nil when there are no lists.
func (p *Program) capabilityProblems(rs ...Roots) (Diagnostics, []NotAllowed) {
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
	add := func(n NotAllowed, tests bool) {
		key := fmt.Sprint(n.Site, "\x00", n.Name, "\x00", n.Host)
		if seen[key] {
			return
		}
		seen[key] = true
		calls = append(calls, n)
		ds = append(ds, p.notAllowedDiagnostic(n, tests))
	}
	for _, r := range rs {
		outside := p.OutsideCalls(r)
		var declared map[string]*Capabilities
		if p.capabilities != nil {
			declared = p.declaredLists(outside)
		}
		for _, n := range notAllowedBy(outside, r.Tests != nil, p.capabilities, declared, p.projectModule()) {
			add(n, r.Tests != nil)
		}
		for _, n := range notAllowedBy(outside, r.Tests != nil, host, nil, p.projectModule()) {
			if !p.hostHolds(n) {
				continue
			}
			n.Host = true
			add(n, r.Tests != nil)
		}
	}
	return ds, calls
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
// set. A Go binding's group is its package and the module that binds it.
func (e *CapabilityError) Shown() Diagnostics {
	if os.Getenv(AllCallsEnv) != "" {
		return e.Diagnostics
	}
	key := func(n NotAllowed) string {
		return fmt.Sprint(n.Name, "\x00", n.Over, "\x00", n.Binder, "\x00", n.Declared, n.Host, n.Tests)
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
// which list to add what to: the call's name, or for a Go binding the
// module that binds Go.
func (p *Program) notAllowedDiagnostic(n NotAllowed, tests bool) frontend.Diagnostic {
	path, line, col := p.shownAt(n.OutsideCall)
	holder := "nomi.toml"
	switch {
	case n.Host:
		holder = "the host"
	case n.Declared:
		holder = "its own nomi.toml"
	}
	// item is what a list gains to allow the call, and program the
	// program's list that holds it.
	item, program, hostOption := n.Name, "std", "vmhost.WithCapabilities"
	var message string
	if n.Binder != "" {
		item, program, hostOption = n.Binder, "go", "vmhost.WithGo"
		message = capability.GoMessage(n.Binder, n.Name, holder, n.Allowed)
	} else {
		who, them := "this program reaches", ""
		switch {
		case n.Host && tests:
			who = "this program's tests reach"
		case n.Over != "" && n.Over == p.projectModule():
			who, holder, them = "this project's own code reaches", "its entry under [capabilities.modules]", " it"
		case n.Over != "":
			who, them = n.Over+" reaches", " it"
		case tests:
			who, them = "this program's tests reach", " them"
		}
		message = capability.Message(who, n.Name, holder, them, n.Allowed)
	}
	if tests {
		program += "_in_tests"
	}
	program += " under [capabilities]"
	args := n.Over
	if n.Binder != "" {
		args = n.Binder
	}
	d := frontend.NewDiagnostic(path, p.sources[path], line, col, message)
	d.Code = CapabilityNotAllowedCode
	d.Args = append([]string{n.Name, args}, n.Allowed...)
	d.Hints = append(d.Hints, n.ReachText())
	if n.Declared {
		d.Code = CapabilityNotDeclaredCode
		d.Hints = append(d.Hints, p.notDeclaredHints(n, item, program)...)
		return d
	}
	if n.ProgramDenies {
		d.Hints = append(d.Hints, fmt.Sprintf("the program's own list, %s, does not allow %s either", program, n.Name))
	}
	add := fmt.Sprintf("add %q to %s in nomi.toml", item, program)
	switch {
	case n.Host:
		add = capability.HostAddHint(item, hostOption)
	case n.Over != "":
		add = fmt.Sprintf("add %q to %q under [capabilities.modules] in nomi.toml", item, n.Over)
	}
	if n.ProgramDenies {
		add += " and to " + program
	}
	d.Hints = append(d.Hints, add+", or "+capability.RemoveHint(n.Edge == vm.EdgeBoot))
	return d
}

// shownAt is where a report about c points: its file as the program names
// it, and the line and column of the call's callee, where the author
// reads the call, rather than the call's own position, its parenthesis.
func (p *Program) shownAt(c OutsideCall) (path string, line, col int) {
	path, line, col = c.at.File(), c.at.Line(), c.at.Col()
	if call, ok := nodeAt(p.modulePath(path), line, col).(*ast.Call); ok {
		if l, c := leftmost(call.Func); l > 0 {
			line, col = l, c
		}
	}
	return path, line, col
}

// notDeclaredHints is the help for n, a call over its module's own list,
// whose fix adds item to a list: what the project's lists say of it when
// they do not allow it either, program being the project's list, and that
// the module declares less than its code reaches, so the fix is in the
// module's own nomi.toml, named by its path: item in its `std`, or for a Go
// binding its own path in its `go`.
func (p *Program) notDeclaredHints(n NotAllowed, item, program string) []string {
	var hints []string
	if n.EntryDenies || n.ProgramDenies {
		var lists []string
		if n.EntryDenies {
			lists = append(lists, fmt.Sprintf("to %q under [capabilities.modules]", n.Over))
		}
		if n.ProgramDenies {
			lists = append(lists, "to "+program)
		}
		hints = append(hints, fmt.Sprintf("the project's nomi.toml does not allow %s to it either; to allow it there, add %q %s",
			n.Name, item, strings.Join(lists, " and ")))
	}
	toml := "its nomi.toml"
	if m := p.moduleAt(n.at.File()); m != nil {
		toml = p.tomlName(m.root)
	}
	own := "std"
	if n.Binder != "" {
		own = "go"
	}
	list := own + " under [capabilities]"
	if n.DeclaredEntry {
		list = fmt.Sprintf("%q under [capabilities.modules]", n.Over)
	}
	return append(hints, fmt.Sprintf("%s declares less than its code reaches, and the fix is in that module: add %q to %s in %s, or remove the call",
		n.Over, item, list, toml))
}
