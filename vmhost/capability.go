package vmhost

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
	"golang.org/x/mod/modfile"
)

// Capability classes and the targets that lack them.
//
// Every std host function that acts outside the machine in a way some
// target cannot carries a class on its binding row (stdlibbindings.Classes):
// `files`, `processes`, `network`, `connect` and `listen`. `nomi build`
// walks what the program reaches, as it does to prune the image, and
// refuses a target that lacks a class a reached call needs, naming each
// call. The tour's stdlib reference pages walk each `//!` prompt the same
// way (TestCapabilityUses) and show one that reaches a class the browser
// lacks as code it does not run. The walk
// over-approximates (a call in a branch the program never takes counts), so
// `--allow-unsupported <class>` builds anyway; such a call then answers
// `Err(.Unsupported{...})` when it runs.
//
// Every target has files today: a js/wasm module's are in memory, empty
// under Go's Node loader and seeded by `run`'s `files` option in the
// browser (internal/stdio's default_js.go), so `nomi build --web` notes that
// a program uses files rather than refusing it (WebFilesNote).
//
// Outside calls.
//
// OutsideCalls answers a wider question than the classes: every call the
// program reaches that acts on or reads from anything outside the machine
// (internal/stdlibbindings package doc, "Outside calls"), output, the clock
// and tasks included. The same reach walk finds them
// (vm.ReachedOutsideCalls), from the roots Roots names: main and the boot,
// the exported functions, or the test cases.
//
//   - A call is named by the std module the code outside std called, as an
//     import spells it: `net.dial(...)` is `std::net`, though the crossing
//     is http's host function, and a crossing the code makes itself
//     (`io.print`, `Task.spawn`, a std host function used as a value) by
//     the module that declares it. A `go` binding is `go:` and the import
//     path of the `gopkg` its declaration names, read from the declaring
//     file's syntax (ffirun.BindingImportPath), so no `go` command runs;
//     the name tells a reader which binding it is, and a list holds it by
//     its module, never by its package. An
//     embedder's own `host fn` is `host:` and its key. The entry boot that
//     takes a Startup reads the environment and the arguments: `std::startup`
//     at the boot.
//   - A call is charged to the module whose file holds it (moduleOf): the
//     program for its own files, a dependency, by its Go module path, for
//     the files under the nearest nomi.toml above them. A callback the
//     program passes into std or a dependency is the program's, and the
//     instance of a dependency's generic function stays in the
//     dependency's file.
//   - Each call carries the chain from a root to the function holding it,
//     and the Edge of each step: a call, a function used as a value, any
//     implementation a dispatched call may reach, any Display, Debug,
//     Equatable or Hashable body, a `once`'s initializer, a boot, a root or
//     a test case. The walk over-approximates as the target check does, and
//     the edges say why a call counts.
//
// OutsideCallsNotAllowed holds those calls to nomi.toml's capability
// tables (Capabilities), one rule over every call (capability.Allows),
// decided in one place (standingsOf). A call in the project's own files is
// held by [capabilities.self]: a Go binding by its go, a flag that lists
// the project by its own module path (projectModule), any other call by
// its std. A call in a dependency's files is held by [capabilities.deps]:
// a Go binding by its go, which lists dependencies by module path, any
// other call by its std, a ceiling when the table sets one, and by the
// project's entry for that module under [capabilities.deps.modules], a
// std list. It is held too to the [capabilities.self] in the dependency's
// own nomi.toml (moduleAt): its std, and for a Go binding its go, which
// declares that it binds Go and never grants it. A call over more than
// one list is reported once, over the first of the module's own list, the
// project's entry for it and [capabilities.deps]. A host's lists
// (WithCapabilities, WithGo) hold the whole program: they are tables whose
// self and deps hold the same names, held beside nomi.toml's, and each
// call over them is reported again, as the host's.
//
// CapabilityUses keeps its own report: it lifts each classed crossing to
// the first call outside std on the path the walk first took, so a std
// function two sites reach is reported once, where OutsideCalls reports
// each site. Building one on the other would change the target check's
// messages.

// ClassFiles is the class of a function that reads or writes files.
const ClassFiles = stdlibbindings.ClassFiles

// ClassProcesses is the class of a function that starts a program.
const ClassProcesses = stdlibbindings.ClassProcesses

// ClassNetwork is the class of a function that reaches the network as an
// HTTP client.
const ClassNetwork = stdlibbindings.ClassNetwork

// ClassConnect is the class of a function that opens a connection or asks
// a name resolver below HTTP.
const ClassConnect = stdlibbindings.ClassConnect

// ClassListen is the class of a function that accepts network connections.
const ClassListen = stdlibbindings.ClassListen

// TargetLacks is the capability classes a build for goos/goarch cannot
// provide, sorted: a WebAssembly module, for the browser or for WASI,
// cannot start a process.
func TargetLacks(goos, goarch string) []string {
	if lacks, ok := testTargets[goos+"/"+goarch]; ok {
		return lacks
	}
	return targetLacks[goos+"/"+goarch]
}

// targetLacks is each real target that lacks a class, with the classes.
var targetLacks = map[string][]string{
	"js/wasm":     {ClassConnect, ClassListen, ClassProcesses},
	"wasip1/wasm": {ClassConnect, ClassListen, ClassNetwork, ClassProcesses},
}

// testTargets are targets only tests build for, each with the classes it
// lacks (LackingTargetForTest), so the refusal is tested through
// CheckTarget before a real target lacks a class.
var testTargets map[string][]string

// WebFilesNote is the line `nomi build --web` prints when the program
// reaches the `files` class.
const WebFilesNote = `nomi build --web: this program uses files; in the browser it sees only the files run's "files" option gives it`

// Uses reports whether the program's reachable code calls a host function
// of class, counting the exported functions when withExports is set.
func (p *Program) Uses(class string, withExports bool) bool {
	return slices.ContainsFunc(p.CapabilityUses(withExports), func(u CapabilityUse) bool { return u.Class == class })
}

// CapabilityUse is one call a program's reachable code makes to a host
// function of a capability class, reported at the program's own call that
// reaches it: a crossing inside the standard library is reported at the
// call of the std function that leads to it (`fs.read_text(path)` in
// `main`), not inside std.
type CapabilityUse struct {
	// Class is the function's class (ClassFiles).
	Class string
	// Crossing is the host function's key (`std::fs::host.read_text`, the
	// crossing std/fs's `read_text` makes).
	Crossing string
	// Callee is what the program's own call at Site calls: a std
	// function (`fs.read_text`), or Crossing itself when the program's
	// code makes the crossing.
	Callee string
	// Site is that call's position, `file:line:col`, the file named as a
	// built image names it.
	Site string
	// Chain is the functions from a root to the one that holds the call
	// at Site, outermost first.
	Chain []string
}

// CapabilityUses is every call to a host function with a capability class
// that the program reaches from main, the boot and, when withExports is
// set, each exported function: what BuildImage or BuildExportsImage would
// keep. Call it on a program that builds.
func (p *Program) CapabilityUses(withExports bool) []CapabilityUse {
	roots, boots, _ := p.reachRoots(Roots{Exports: withExports})
	return p.capabilityUsesFrom(roots, boots)
}

// TestCapabilityUses is CapabilityUses for the entry's test cases opts
// selects: every call to a host function with a capability class that
// those tests, with their group's boot, reach. Call it on a program that
// loads.
func (p *Program) TestCapabilityUses(opts TestOptions) []CapabilityUse {
	roots, boots, _ := p.reachRoots(Roots{Tests: &opts})
	if len(roots) == 0 {
		return nil
	}
	return p.capabilityUsesFrom(roots, boots)
}

// Roots says which functions a walk over what a program reaches starts
// from.
type Roots struct {
	// Exports adds each exported function to main and the boot: what
	// BuildExportsImage keeps.
	Exports bool
	// Tests, when set, starts from the entry's test cases it selects, with
	// the entry's boot and each case's group boot, instead of main.
	Tests *TestOptions
	// Func, when set, starts from the entry's function of that name and
	// the boot, instead of main: what Call runs.
	Func string
	// Callable adds every function the entry file declares, any of which
	// Call may call.
	Callable bool
}

// reachRoots is the functions and boots r names, and the edge kind its
// functions are reached by.
func (p *Program) reachRoots(r Roots) ([]*ir.Func, []*ir.Symbol, vm.EdgeKind) {
	var roots []*ir.Func
	var boots []*ir.Symbol
	if p.entry != nil {
		if boot := p.entry.Boot(); boot != nil {
			boots = append(boots, boot)
		}
	}
	if r.Tests != nil {
		if p.entry == nil {
			return nil, nil, vm.EdgeTest
		}
		plan, _ := frontend.SelectTests(p.prog.Entry().Nodes, *r.Tests)
		retained := map[string]ir.TestCase{}
		for _, c := range p.entry.Tests() {
			retained[c.Name()] = c
		}
		for _, tc := range plan {
			c, ok := retained[tc.FullName()]
			if !ok {
				continue
			}
			roots = append(roots, c.Fn())
			if b := c.Group().Boot; b != nil {
				boots = append(boots, b, c.Group().Startup)
			}
		}
		return roots, boots, vm.EdgeTest
	}
	if r.Func != "" {
		if f, err := p.callable(r.Func); err == nil {
			roots = append(roots, f)
		}
		return roots, boots, vm.EdgeRoot
	}
	if p.HasMain() {
		if mainFn := p.entryFunc("main"); mainFn != nil {
			roots = vm.MainRoots(p.entry, mainFn)
		}
	}
	if r.Exports {
		for _, e := range p.res.Exports {
			if e.Func != nil {
				roots = append(roots, e.Func)
			}
		}
	}
	if r.Callable && p.entry != nil {
		for _, f := range p.entry.Funcs() {
			if p.declares(f.Name()) {
				roots = append(roots, f)
			}
		}
	}
	return roots, boots, vm.EdgeRoot
}

// capabilityUsesFrom is the classed calls reached from roots and boots.
func (p *Program) capabilityUsesFrom(roots []*ir.Func, boots []*ir.Symbol) []CapabilityUse {
	classes := stdlibbindings.Classes()
	sites := vm.ReachedCrossingSites(p.entry, p.res.IRModules(), roots, boots, func(name string) bool {
		return classes[name] != ""
	})
	out := make([]CapabilityUse, len(sites))
	for i, s := range sites {
		out[i] = CapabilityUse{
			Class:    classes[s.Name],
			Crossing: s.Name,
			Callee:   s.Via,
			Site:     fmt.Sprintf("%s:%d:%d", p.imageName(s.At.File()), s.At.Line(), s.At.Col()),
			Chain:    s.Chain,
		}
	}
	return out
}

// UsesNetwork reports whether the program reaches a crossing whose adapter
// needs the network (stdlibadapters.NetworkNames), from main, the boot and,
// when withExports is set, each exported function: whether `nomi build`
// must append it to the runner with the network rather than the smaller
// one built with the nomi_nonet tag.
func (p *Program) UsesNetwork(withExports bool) bool {
	roots, boots, _ := p.reachRoots(Roots{Exports: withExports})
	network := stdlibadapters.NetworkNames()
	for _, name := range vm.ReachedCrossings(p.entry, p.res.IRModules(), roots, boots) {
		if _, found := slices.BinarySearch(network, name); found {
			return true
		}
	}
	return false
}

// Unsupported is a build refused because the program reaches calls its
// target cannot make: Command is the command as the user wrote it (`nomi
// build --target js/wasm`), Target the target's name, and Uses the calls,
// each of a class in Lacking.
type Unsupported struct {
	Command string
	Target  string
	Lacking []string
	Uses    []CapabilityUse
}

// CheckTarget is the refusal of a build for goos/goarch whose program
// reaches a class the target lacks and allowed does not name, or nil.
// command names the build in the report.
func (p *Program) CheckTarget(command, goos, goarch string, withExports bool, allowed []string) *Unsupported {
	var lacking []string
	for _, class := range TargetLacks(goos, goarch) {
		if !slices.Contains(allowed, class) {
			lacking = append(lacking, class)
		}
	}
	return p.Refuse(command, goos+"/"+goarch, lacking, withExports)
}

// Refuse is the refusal of a build for target, which lacks the classes in
// lacking, or nil when the program reaches none of them.
func (p *Program) Refuse(command, target string, lacking []string, withExports bool) *Unsupported {
	if len(lacking) == 0 {
		return nil
	}
	var uses []CapabilityUse
	for _, u := range p.CapabilityUses(withExports) {
		if slices.Contains(lacking, u.Class) {
			uses = append(uses, u)
		}
	}
	if len(uses) == 0 {
		return nil
	}
	return &Unsupported{Command: command, Target: target, Lacking: lacking, Uses: uses}
}

// Error is the report's first line.
func (u *Unsupported) Error() string {
	var what []string
	for _, class := range u.Lacking {
		if slices.ContainsFunc(u.Uses, func(c CapabilityUse) bool { return c.Class == class }) {
			what = append(what, ClassDoing(class))
		}
	}
	return fmt.Sprintf("%s: this program %s, which a %s module cannot do", u.Command, strings.Join(what, " and "), u.Target)
}

// Write prints the report: the first line, each call with where it is
// reached from, and how to build anyway.
func (u *Unsupported) Write(w io.Writer) {
	fmt.Fprintf(w, "%s:\n", u.Error())
	var classes []string
	for _, c := range u.Uses {
		from := "fn " + c.Chain[0]
		if len(c.Chain) > 1 {
			from += " through " + strings.Join(c.Chain[1:], ", ")
		}
		what := "`" + c.Callee + "`"
		if c.Callee != c.Crossing {
			what += " calls `" + c.Crossing + "`"
		}
		fmt.Fprintf(w, "  %s: %s (reached from %s)\n", c.Site, what, from)
		if !slices.Contains(classes, c.Class) {
			classes = append(classes, c.Class)
		}
	}
	allow := make([]string, len(classes))
	for i, class := range classes {
		allow[i] = "--allow-unsupported " + class
	}
	if strings.Contains(u.Command, "--web") {
		fmt.Fprintf(w, "hint: pass %s to build anyway; the calls then answer Err(.Unsupported{...})\n", strings.Join(allow, " "))
		return
	}
	fmt.Fprintf(w, "hint: build for your platform without --target, or pass %s\n", strings.Join(allow, " "))
	fmt.Fprintf(w, "      to build anyway; the calls then answer Err(.Unsupported{...})\n")
}

// ClassDoing is what a program reaching class does, as a report says it
// ("serves network requests").
func ClassDoing(class string) string {
	switch class {
	case ClassFiles:
		return "reads or writes files"
	case ClassProcesses:
		return "starts programs"
	case ClassNetwork:
		return "sends network requests"
	case ClassConnect:
		return "opens network connections"
	case ClassListen:
		return "serves network requests"
	}
	return "uses " + class
}

// Edge is how the reach walk got from one function to the next: a direct
// call, a function used as a value, any implementation of an interface
// function, any Display, Debug, Equatable or Hashable body, a `once`'s
// initializer, a boot, a root or a test case (vm.EdgeKind).
type Edge = vm.EdgeKind

// ChainStep is one function on the way from a root to an outside call, and
// the Edge that reached it.
type ChainStep = vm.ChainStep

// OutsideCall is one outside call a program reaches: a call, in code outside
// the standard library, that acts on or reads from something outside the
// machine, through std or itself.
type OutsideCall struct {
	// Name is what the call is named by: the std module the code called,
	// as an import names it (`std::fs`, `std::net` for `net.dial` though it
	// crosses through http's host functions), or `go:` and the Go import
	// path for a `go` binding, which says which binding it is; a list
	// holds a binding by its Module. An embedder's own `host fn` is `host:`
	// and its key.
	Name string
	// Module is the module charged for the call, the one whose file holds
	// it: a dependency's Go module path, or "" for the program's own files.
	Module string
	// Site is the call's position, `file:line:col`, the file named as a
	// built image names it.
	Site string
	// Callee is what the code at Site calls: a std function
	// (`fs.read_text`), or the crossing itself when the code crosses.
	Callee string
	// Crossing is a crossing into Go the call leads to (`fs/host.read`),
	// or "" for a boot's Startup, which crosses nothing.
	Crossing string
	// Edge is how the code at Site reaches Callee.
	Edge Edge
	// Chain is the functions from a root to the one holding the call, each
	// with the Edge that reached it, outermost first.
	Chain []ChainStep
	// at is the call's position as the program names its file, where a
	// diagnostic about it goes, and index its place in its walk's calls
	// (Walk.reach), which answers what leads to it (leadsTo).
	at    ir.Pos
	index int
}

// ReachText says how the walk reached c, in the words a refusal's help
// line uses (vm.ReachText): from the root its chain starts at, each step
// with the kind of edge that took it.
func (c OutsideCall) ReachText() string {
	return vm.ReachText(c.Chain, c.Edge, c.Callee)
}

// OutsideCalls is every outside call the program reaches from the roots r
// names, in position order: each crossing of a std host function or VM
// intrinsic marked outside (internal/stdlibbindings, "Outside calls"), each
// `go` binding, and the entry boot's Startup, which holds the environment
// and the arguments. A call is named by the std module the program's or a
// dependency's own code called, and charged to the module whose file holds
// that call; a callback passed into std or into a dependency is the code
// of the module that wrote it. Like the build's walk, it over-approximates
// (vm.ReachedOutsideCalls), and each Chain says why a call counts. Call it
// on a program that loads.
func (p *Program) OutsideCalls(r Roots) []OutsideCall {
	return p.walk(r).Calls
}

// walk is the walk OutsideCalls answers the calls of, with what each
// function outside std can reach (Walk.reach).
func (p *Program) walk(r Roots) Walk {
	w := Walk{Tests: r.Tests != nil}
	roots, boots, kind := p.reachRoots(r)
	if len(roots) == 0 && len(boots) == 0 {
		return w
	}
	w.reach = vm.ReachedOutsideCalls(p.entry, p.res.IRModules(), roots, kind, boots, compilerCrossing)
	calls := w.reach.Calls
	out := make([]OutsideCall, len(calls))
	modules := map[string]string{}
	for i, c := range calls {
		file := c.At.File()
		module, ok := modules[file]
		if !ok {
			module = p.moduleOf(file)
			modules[file] = module
		}
		out[i] = OutsideCall{
			Name:     p.outsideName(c),
			Module:   module,
			Site:     fmt.Sprintf("%s:%d:%d", p.imageName(file), c.At.Line(), c.At.Col()),
			Callee:   hostadapt.DisplayName(c.Callee),
			Crossing: hostadapt.DisplayName(c.Crossing),
			Edge:     c.Edge,
			Chain:    c.Chain,
			at:       c.At,
			index:    i,
		}
	}
	w.Calls = out
	return w
}

// CapabilityList is a list an outside call can be over (NotAllowed.List).
type CapabilityList string

// The lists of nomi.toml's capability tables a call can be over.
const (
	// OverSelf is [capabilities.self], which holds the project's own files:
	// its std, with std_in_tests in tests, or for a Go binding its go,
	// with go_in_tests in tests.
	OverSelf CapabilityList = "self"
	// OverDeps is [capabilities.deps], which holds every dependency's
	// files: its std ceiling, with std_in_tests in tests, or for a Go
	// binding its go, with go_in_tests in tests.
	OverDeps CapabilityList = "deps"
	// OverEntry is the module's entry under [capabilities.deps.modules].
	OverEntry CapabilityList = "deps.modules"
	// OverDeclared is the [capabilities.self] in the module's own
	// nomi.toml, which it declares for its own files: its std, or for a
	// Go binding its go.
	OverDeclared CapabilityList = "declared"
)

// NotAllowed is an outside call a program reaches that nomi.toml's
// capability tables, or the host's lists, do not allow.
type NotAllowed struct {
	OutsideCall
	// List is the list that does not allow the call. The module it holds
	// is Module, the one whose file holds the call: the project for
	// OverSelf, a dependency for the others.
	List CapabilityList
	// Binder is, for a Go binding, the module whose code binds Go, by its
	// path: the project's own module path for its own files
	// (projectModule). It is "" for any other call.
	Binder string
	// Allowed is what List allows. For a Go binding, the dependencies
	// [capabilities.deps] lets bind Go, with go_in_tests when the roots are
	// tests; none for OverSelf and OverDeclared, whose go is a flag that
	// is false. Otherwise the std names it holds, with std_in_tests when
	// the roots are tests for OverSelf and OverDeps.
	Allowed []string
	// EntryDenies reports, for a call over a module's own list, that the
	// project's entry for the module does not allow it either.
	EntryDenies bool
	// DepsDenies reports, for a call over a module's own list or its
	// entry, that [capabilities.deps]' ceiling or go does not allow it
	// either.
	DepsDenies bool
	// Host reports that the list is the one the host holds the program to
	// (WithCapabilities, WithGo), not nomi.toml's.
	Host bool
	// Tests reports that the call is reached from test cases, so the
	// project's lists hold std_in_tests or go_in_tests too.
	Tests bool
}

// OutsideCallsNotAllowed is every outside call the program reaches from the
// roots r names (OutsideCalls) that lists, with the program's dependencies'
// own lists, does not allow, in the order OutsideCalls answers them. A
// call in the project's own files is held by Self, nothing when it is
// nil: a Go binding by Go, any other call by Std, each with its InTests
// list when r names tests. A call in a dependency's files is held, when
// Deps is not nil, by three lists:
//
//   - Deps: a Go binding's module is in Go, any other call's name is in
//     Std when it is set, each with its InTests list when r names tests;
//   - the project's entry for the module under Deps.Modules, which holds
//     every call but a Go binding;
//   - the module's own [capabilities.self], when its nomi.toml has one: a
//     Go binding needs its go to be true, any other call is in its std.
//     The rest of its tables are for its own builds.
//
// A call over more than one list is reported once: over the module's own
// list first, then over the project's entry for it, with EntryDenies and
// DepsDenies saying which others it is over. Nil lists allow everything,
// the dependencies' own included.
func (p *Program) OutsideCallsNotAllowed(r Roots, lists *Capabilities) []NotAllowed {
	if lists == nil {
		return nil
	}
	calls := p.OutsideCalls(r)
	return notAllowedBy(calls, r.Tests != nil, lists, p.declaredLists(calls), p.projectModule())
}

// notAllowedBy is each of calls, reached from tests when tests is set, that
// lists and declared do not allow (standingsOf): the refusals.
func notAllowedBy(calls []OutsideCall, tests bool, lists *Capabilities, declared map[string]*analysis.SelfCapabilities, project string) []NotAllowed {
	var out []NotAllowed
	for _, s := range standingsOf(calls, tests, lists, declared, project) {
		if s.refused {
			out = append(out, s.NotAllowed)
		}
	}
	return out
}

// standing is how the lists stand on one outside call (standingsOf):
// refused, with the NotAllowed that reports it, or allowed. in is where
// the project's nomi.toml says so, as an editor's hover names it
// (capability.ReachedLine): each table that allows the call, none when no
// table holds it, or the one list that refuses it.
type standing struct {
	NotAllowed
	refused bool
	in      []string
}

// standingsOf is how lists and declared stand on each of calls, reached
// from tests when tests is set, in order, by OutsideCallsNotAllowed's
// rules, which decide here: the one rule that holds a call to a list
// (capability.Allows), for every list. declared is each module's own
// [capabilities.self], by its Go module path (declaredLists); nil holds no
// module to a table of its own. project is the project's module path,
// which the program's own Go bindings are charged to. Nil for nil lists.
func standingsOf(calls []OutsideCall, tests bool, lists *Capabilities, declared map[string]*analysis.SelfCapabilities, project string) []standing {
	if lists == nil {
		return nil
	}
	// binding is a flag's list of the modules that may bind Go: module, or
	// none.
	binding := func(on bool, module string) []string {
		if on {
			return []string{module}
		}
		return nil
	}
	const selfTable, depsTable = "`[capabilities.self]`", "`[capabilities.deps]`"
	out := make([]standing, 0, len(calls))
	for _, c := range calls {
		s := standing{NotAllowed: NotAllowed{OutsideCall: c, Tests: tests}}
		n := &s.NotAllowed
		if capability.BindsGo(c.Name) {
			n.Binder = cmp.Or(c.Module, project)
		}
		// allowed is what a list of std names and a list of modules that
		// may bind Go allow the call: the one that holds it.
		allowed := func(std, goModules []string) []string {
			if n.Binder != "" {
				return goModules
			}
			return std
		}
		refuse := func(list CapabilityList, names []string, in string) {
			s.refused, n.List, n.Allowed = true, list, names
			if in != "" {
				s.in = []string{in}
			}
		}
		if c.Module == "" {
			if self := lists.Self; self != nil {
				std, goModules := self.StdFor(tests), binding(self.GoFor(tests), project)
				if capability.Allows(std, goModules, c.Name, n.Binder) {
					s.in = []string{selfTable}
				} else {
					refuse(OverSelf, allowed(std, nil), selfTable)
				}
			}
			out = append(out, s)
			continue
		}
		d := lists.Deps
		if d == nil {
			out = append(out, s)
			continue
		}
		ceiling, goModules := d.StdFor(tests), d.GoFor(tests)
		depsHolds := n.Binder != "" || ceiling != nil
		depsDenies := depsHolds && !capability.Allows(ceiling, goModules, c.Name, n.Binder)
		// An entry under [capabilities.deps.modules] holds std names; Go
		// is held by go alone.
		entry, hasEntry := d.Modules[c.Module]
		entryHolds := hasEntry && n.Binder == ""
		entryDenies := entryHolds && !capability.Allows(entry, nil, c.Name, n.Binder)
		entryIn := fmt.Sprintf("the entry for `%s`", c.Module)
		own, hasOwn := declared[c.Module]
		switch {
		case hasOwn && !capability.Allows(own.Std, binding(own.Go, c.Module), c.Name, n.Binder):
			refuse(OverDeclared, allowed(own.Std, nil), "")
			n.EntryDenies, n.DepsDenies = entryDenies, depsDenies
		case entryDenies:
			refuse(OverEntry, entry, entryIn)
			n.DepsDenies = depsDenies
		case depsDenies:
			refuse(OverDeps, allowed(ceiling, goModules), depsTable)
		default:
			if depsHolds {
				s.in = append(s.in, depsTable)
			}
			if entryHolds {
				s.in = append(s.in, entryIn)
			}
		}
		out = append(out, s)
	}
	return out
}

// Walk is the outside calls one walk over a program reaches
// (OutsideCalls): from main, the boot and the exported functions, or, with
// Tests, from the test cases.
type Walk struct {
	Tests bool
	Calls []OutsideCall
	// reach is the walk Calls come from, in the same order, which answers
	// what each function outside std can reach (reachedFuncs); nil when the
	// walk had no roots.
	reach *vm.OutsideWalk
}

// tightestLists is the smallest pair of capability tables under which
// notAllowedBy allows every call walks reach, beside no module's own
// table: removing any one name from them, or turning a flag false,
// refuses a call. Self's Std holds each name a walk from main reaches in
// the project's own files, and its Go says whether they bind Go there;
// StdInTests and GoInTests hold what only the tests reach. Deps' lists do
// the same for the dependencies' files, its Std the ceiling, and its Go
// the dependencies that bind Go. Its Modules has an entry for each of
// deps, the dependencies an entry may name, holding the std names that
// module's files reach from every walk. A dependency not among deps gets
// no entry.
func tightestLists(walks []Walk, deps []string) *Capabilities {
	self := &analysis.SelfCapabilities{Std: []string{}}
	d := &analysis.DepsCapabilities{Std: []string{}, Modules: map[string][]string{}}
	for _, m := range deps {
		d.Modules[m] = []string{}
	}
	add := func(list *[]string, name string) {
		if !slices.Contains(*list, name) {
			*list = append(*list, name)
		}
	}
	for _, w := range walks {
		for _, c := range w.Calls {
			bindsGo := capability.BindsGo(c.Name)
			switch {
			case c.Module == "" && bindsGo:
				self.Go = self.Go || !w.Tests
				self.GoInTests = self.GoInTests || w.Tests
			case c.Module == "" && w.Tests:
				add(&self.StdInTests, c.Name)
			case c.Module == "":
				add(&self.Std, c.Name)
			case bindsGo && w.Tests:
				add(&d.GoInTests, c.Module)
			case bindsGo:
				add(&d.Go, c.Module)
			default:
				if w.Tests {
					add(&d.StdInTests, c.Name)
				} else {
					add(&d.Std, c.Name)
				}
				if entry, ok := d.Modules[c.Module]; ok {
					add(&entry, c.Name)
					d.Modules[c.Module] = entry
				}
			}
		}
	}
	self.StdInTests = slices.DeleteFunc(self.StdInTests, func(n string) bool { return slices.Contains(self.Std, n) })
	self.GoInTests = self.GoInTests && !self.Go
	d.StdInTests = slices.DeleteFunc(d.StdInTests, func(n string) bool { return slices.Contains(d.Std, n) })
	d.GoInTests = slices.DeleteFunc(d.GoInTests, func(m string) bool { return slices.Contains(d.Go, m) })
	for _, list := range [][]string{self.Std, self.StdInTests, d.Std, d.StdInTests, d.Go, d.GoInTests} {
		slices.Sort(list)
	}
	for _, entry := range d.Modules {
		slices.Sort(entry)
	}
	return &Capabilities{Self: self, Deps: d}
}

// declaredLists is the own [capabilities.self] of each module whose files
// hold one of calls and whose own nomi.toml has one, by its Go module path
// (moduleAt).
func (p *Program) declaredLists(calls []OutsideCall) map[string]*analysis.SelfCapabilities {
	out := map[string]*analysis.SelfCapabilities{}
	for _, c := range calls {
		if _, done := out[c.Module]; done || c.Module == "" {
			continue
		}
		if m := p.moduleAt(c.at.File()); m != nil && m.declares() {
			out[c.Module] = m.manifest.Capabilities.Self
		}
	}
	return out
}

// outsideName is the name of an outside call (capability.Name): `std::`
// and its std module, or for a crossing no std module declares, `go:` and
// the Go import path its declaration's `gopkg` names, read from the
// declaring file. A crossing whose declaration binds no Go package is an
// embedder's host function, `host:` and its key.
func (p *Program) outsideName(c vm.OutsideCall) string {
	goPath := ""
	if c.Std == "" {
		goPath, _ = ffirun.BindingImportPath(p.fileNodes(c.At.File()), c.At.Line())
	}
	return capability.Name(c.Std, goPath, hostadapt.DisplayName(c.Crossing))
}

// fileNodes is the parsed file at path: the program's own nodes for it when
// it holds them, else the file read from disk (a program reopened from the
// artifact cache holds only its entry), or nil.
func (p *Program) fileNodes(path string) []ast.Node {
	for _, m := range p.prog.Modules {
		if m.Path == path && m.Nodes != nil {
			return m.Nodes
		}
	}
	if !filepath.IsAbs(path) {
		return nil
	}
	nodes, _ := ffirun.ParseFile(path)
	return nodes
}

// moduleOf is the module whose file path is: "" for the program's own
// files (an in-memory one among them) and the standard library's, else the
// Go module path of the dependency moduleAt finds.
func (p *Program) moduleOf(path string) string {
	if m := p.moduleAt(path); m != nil {
		return m.path
	}
	return ""
}

// projectModule is the module path the project's own files are charged to
// for a Go binding, the name the project's `go` and WithGo list it by: its
// go.mod's module path; without one, its nomi.toml's `[module].name`; with
// neither, its entry's module name, which is the name LoadSource is given
// (`LoadSource("rule", ...)` is `rule`) and a lone file's name without
// `.nomi`.
func (p *Program) projectModule() string {
	p.projectOnce.Do(func() {
		if path := goModulePath(p.prog.Root); path != "" {
			p.project = path
			return
		}
		if m := programManifest(p.prog.Root, p.cfg.manifest); m != nil && m.Name != "" {
			p.project = m.Name
			return
		}
		p.project = p.prog.Entry().Name
	})
	return p.project
}

// goModulePath is the module path the go.mod in root declares, or "" when
// it has none.
func goModulePath(root string) string {
	if root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	return modfile.ModulePath(data)
}

// requiredModule is a module other than the project whose files a program
// holds: a dependency.
type requiredModule struct {
	// path is its Go module path from the project's go.mod (modcache), or
	// its `[module].name` when go.mod does not list its directory, or its
	// directory when it has neither.
	path string
	// root is its directory, the one holding its nomi.toml.
	root string
	// manifest is its nomi.toml, nil when it cannot be read (the front
	// end reports that).
	manifest *analysis.Manifest
}

// declares reports whether m's own nomi.toml has a [capabilities.self]
// table, which declares m's own code: by its std, and by its go, which is
// true when m binds Go.
func (m *requiredModule) declares() bool {
	return m.manifest != nil && m.manifest.Capabilities != nil && m.manifest.Capabilities.Self != nil
}

// moduleAt is the dependency the file at path is in, the one whose
// nomi.toml is the nearest above it, or nil for the program's own files (an
// in-memory one among them) and the standard library's. Each dependency is
// worked out once.
func (p *Program) moduleAt(path string) *requiredModule {
	if _, isStd := std.ImageName(path); isStd || !filepath.IsAbs(path) {
		return nil
	}
	root, ok := manifestDirAbove(filepath.Dir(path))
	if !ok || p.prog.Root == "" || sameDir(root, p.prog.Root) {
		return nil
	}
	p.modulesMu.Lock()
	defer p.modulesMu.Unlock()
	if m, done := p.modules[root]; done {
		return m
	}
	m := &requiredModule{path: root, root: root}
	m.manifest, _ = analysis.LoadManifest(root)
	if m.manifest != nil {
		m.path = m.manifest.Name
	}
	if g, err := modcache.Load(p.prog.Root); err == nil && g != nil {
		for _, d := range g.Deps {
			if d.Dir != "" && sameDir(d.Dir, root) {
				m.path = d.Path
				break
			}
		}
	}
	if p.modules == nil {
		p.modules = map[string]*requiredModule{}
	}
	p.modules[root] = m
	return m
}

// requiredModules is each module other than the project whose Nomi files
// the program holds, by its Go module path: the modules the program's
// code comes from, whether or not the program reaches their code. A
// program reopened from the artifact cache holds only its entry, and so
// none; a program is kept only when its lists pass, and the record names
// each dependency's tree, its nomi.toml among them.
func (p *Program) requiredModules() []*requiredModule {
	seen := map[*requiredModule]bool{}
	var out []*requiredModule
	for _, file := range p.prog.Modules {
		if m := p.moduleAt(file.Path); m != nil && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b *requiredModule) int { return strings.Compare(a.path, b.path) })
	return out
}

// sameDir reports whether a and b are one directory.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}
