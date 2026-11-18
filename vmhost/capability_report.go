package vmhost

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/vm"
)

// CapabilityNotAllowedCode is the Code of the diagnostic for an outside
// call nomi.toml's [capabilities] lists do not allow. Its Args are the
// call's name (`std::fs`), the module over its list (its Go module path,
// or "" for the program's list), and then each name that list allows.
const CapabilityNotAllowedCode = "capability-not-allowed"

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
		for _, n := range notAllowedBy(outside, r.Tests != nil, p.capabilities) {
			add(n, r.Tests != nil)
		}
		for _, n := range notAllowedBy(outside, r.Tests != nil, host) {
			n.Host = true
			add(n, r.Tests != nil)
		}
	}
	return ds, calls
}

// CapabilityError is a program refused because it reaches outside calls
// the lists it is held to do not allow: nomi.toml's [capabilities] lists,
// or the host's (WithCapabilities). Its text is the diagnostics `nomi
// check` prints, one per call, at the call.
type CapabilityError struct {
	// Calls is each call a list does not allow, with whose list it is
	// over and the chain from a root that reaches it.
	Calls []NotAllowed
	// Diagnostics is the error for each of Calls, in the same order.
	Diagnostics Diagnostics
}

func (e *CapabilityError) Error() string { return e.Diagnostics.Error() }

// Unwrap is the diagnostics, which a caller that renders a load's
// errors finds with errors.As.
func (e *CapabilityError) Unwrap() error { return e.Diagnostics }

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
// (WithCapabilities) and the list has a name that is not one, or the
// program reaches a call the lists do not allow from anything a host can
// run: main and the boot, every function the entry declares, which Call
// may call, and the test cases.
func (p *Program) holdToHost() error {
	if !p.cfg.held {
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
// which list to add the name to.
func (p *Program) notAllowedDiagnostic(n NotAllowed, tests bool) frontend.Diagnostic {
	path, line, col := n.at.File(), n.at.Line(), n.at.Col()
	if call, ok := nodeAt(p.modulePath(path), line, col).(*ast.Call); ok {
		// A call's own position is its parenthesis; the author reads the
		// call at its callee.
		if l, c := leftmost(call.Func); l > 0 {
			line, col = l, c
		}
	}
	who, them, list := "this program reaches", "", "allow"
	switch {
	case n.Host && tests:
		who = "this program's tests reach"
	case n.Over != "":
		who, them, list = n.Over+" reaches", " it", fmt.Sprintf("%q under [capabilities.modules]", n.Over)
	case tests:
		who, them, list = "this program's tests reach", " them", "allow_in_tests"
	}
	holder := "nomi.toml"
	if n.Host {
		holder = "the host"
	}
	allows := fmt.Sprintf("%s allows%s only %s", holder, them, strings.Join(n.Allowed, ", "))
	if len(n.Allowed) == 0 {
		allows = fmt.Sprintf("%s allows%s nothing outside the program", holder, them)
	}
	d := frontend.NewDiagnostic(path, p.sources[path], line, col, fmt.Sprintf("%s %s, and %s", who, n.Name, allows))
	d.Code = CapabilityNotAllowedCode
	d.Args = append([]string{n.Name, n.Over}, n.Allowed...)
	d.Hints = append(d.Hints, reachedThrough(n.OutsideCall))
	if n.ProgramDenies {
		d.Hints = append(d.Hints, fmt.Sprintf("the program's own list, allow under [capabilities], does not allow %s either", n.Name))
	}
	add := fmt.Sprintf("add %q to %s in nomi.toml", n.Name, list)
	switch {
	case n.Host:
		add = fmt.Sprintf("add %q to the names the host passes to vmhost.WithCapabilities", n.Name)
	case n.Over == "" && !tests:
		add = fmt.Sprintf("add %q to allow under [capabilities] in nomi.toml", n.Name)
	}
	if n.ProgramDenies {
		add += " and to allow under [capabilities]"
	}
	remove := "remove the call"
	if n.Edge == vm.EdgeBoot {
		remove = "have the boot take no Startup"
	}
	d.Hints = append(d.Hints, add+", or "+remove)
	return d
}

// reachedThrough says how the walk reached c: from its root, each step
// with the kind of edge that took it, and how the code at the call reaches
// what it calls when that is not a call.
func reachedThrough(c OutsideCall) string {
	var b strings.Builder
	for i, s := range c.Chain {
		if i == 0 {
			b.WriteString("reached from " + chainRoot(s))
			continue
		}
		b.WriteString("; " + chainEdge(c.Chain[i-1].Func, s))
	}
	if len(c.Chain) > 0 && c.Edge == vm.EdgeValue {
		b.WriteString(fmt.Sprintf("; `%s` uses `%s` as a value here, which counts whether or not it is called", c.Chain[len(c.Chain)-1].Func, c.Callee))
	}
	return b.String()
}

// chainRoot names the function a chain starts from.
func chainRoot(s ChainStep) string {
	switch s.Edge {
	case vm.EdgeTest:
		return fmt.Sprintf("test %q", s.Func)
	case vm.EdgeBoot:
		return fmt.Sprintf("the boot `%s`", s.Func)
	}
	return "`" + s.Func + "`"
}

// chainEdge says how the walk got from the function from to s, in words for
// the program's author: why s counts as reached.
func chainEdge(from string, s ChainStep) string {
	typ, method := s.Func, s.Func
	if i := strings.LastIndex(s.Func, "."); i > 0 {
		typ, method = s.Func[:i], s.Func[i+1:]
	}
	switch s.Edge {
	case vm.EdgeCall:
		return fmt.Sprintf("`%s` calls `%s`", from, s.Func)
	case vm.EdgeValue:
		if s.Func == "lambda" {
			return fmt.Sprintf("`%s` makes a lambda, which counts whether or not it is called", from)
		}
		return fmt.Sprintf("`%s` uses `%s` as a value, which counts whether or not it is called", from, s.Func)
	case vm.EdgeDispatch:
		return fmt.Sprintf("`%s` calls `%s` through an interface, and any implementation of it counts, including `%s`", from, method, s.Func)
	case vm.EdgeDisplay:
		return fmt.Sprintf("`%s` displays a value, and any value may be displayed, so `impl Display for %s` counts", from, typ)
	case vm.EdgeDebug:
		return fmt.Sprintf("`%s` inspects a value, and any value may be inspected, so `impl Debug for %s` counts", from, typ)
	case vm.EdgeKeys:
		return fmt.Sprintf("`%s` compares or hashes values, and any type's may be, so `%s` counts", from, s.Func)
	case vm.EdgeOnce:
		return fmt.Sprintf("`%s` reads `%s`, so its initializer counts", from, s.Func)
	case vm.EdgeBoot:
		return fmt.Sprintf("the boot `%s` runs first", s.Func)
	}
	return fmt.Sprintf("`%s` reaches `%s`", from, s.Func)
}
