package vmhost

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

// Effect classes and the targets that lack them.
//
// Every std host function that acts outside the machine in a way some
// target cannot carries a class on its binding row (stdlibbindings.Classes):
// `files`, `processes`, `network`, `connect` and `listen`. `nomi build`
// walks what the program reaches, as it does to prune the image, and
// refuses a target that lacks a class a reached call needs, naming each
// call. The tour's stdlib reference pages walk each `//!` prompt the same
// way (TestClassUses) and show one that reaches a class the browser
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
// Effects.
//
// EffectCalls answers a wider question than the classes: every call the
// program reaches that has an effect an effects list names
// (internal/stdlibbindings package doc, "Effects and World"), output, the
// clock, tasks and state made in the machine included. The same reach
// walk finds them (vm.ReachedEffects), from the roots Roots names: main
// and the boot, the exported functions, or the test cases.
//
//   - A call is named by the std module the code outside std called, as an
//     import spells it: `net.dial(...)` is `std::net`, though the crossing
//     is http's host function, and a crossing the code makes itself
//     (`io.print`, `Task.spawn`, a std host function used as a value) by
//     the module that declares it. An embedder's own `host fn` is `host:`
//     and its key. The entry boot that
//     takes a Startup reads the environment and the arguments: `std::startup`
//     at the boot.
//   - A call is charged to the module whose file holds it (moduleOf): the
//     program for its own files, a dependency, by its name in the program, for
//     the files under the nearest nomi.toml above them; and within that
//     module to the file, by its import path (importOf). A callback the
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
// EffectCallsNotAllowed holds those calls to nomi.toml's effect
// tables (Effects), one rule over every call (effects.Allows),
// decided in one place (standingsOf). Each call is held by one table and
// its entries, by the module whose file holds it. A call in the project's
// own files is held by [effects.self]: by its std, and by the entry for
// the file's module under [effects.self.modules], keyed by the file's
// import path (importOf), a std list. A call in a dependency's files is
// held by [effects.deps]: by its std, a ceiling when the table sets one,
// and by the project's entry for that module under
// [effects.deps.modules], a std list. It is held too to the
// [effects.self] in the dependency's own nomi.toml (moduleAt): its std
// and its entry for the file's module. A call over more than one list is
// reported once, over the first of the module's own list, the entry and
// the table the entry is under. A host's list (WithEffects) holds the
// whole program: it is a table whose self and deps hold the same names,
// held beside nomi.toml's, and each call over it is reported again, as
// the host's.
//
// ClassUses keeps its own report: it lifts each classed crossing to
// the first call outside std on the path the walk first took, so a std
// function two sites reach is reported once, where EffectCalls reports
// each site. Building one on the other would change the target check's
// messages.

// ClassFiles is the class of a function that reads or writes files.
const ClassFiles = stdlibbindings.ClassFiles

// ClassProcesses is the class of a function that starts a program.
const ClassProcesses = stdlibbindings.ClassProcesses

// ClassNetwork is the class of a function that makes an HTTP client, the
// handle a program sends network requests through.
const ClassNetwork = stdlibbindings.ClassNetwork

// ClassConnect is the class of a function that opens a connection or asks
// a name resolver below HTTP.
const ClassConnect = stdlibbindings.ClassConnect

// ClassListen is the class of a function that accepts network connections.
const ClassListen = stdlibbindings.ClassListen

// TargetLacks is the effect classes a build for goos/goarch cannot
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
	return slices.ContainsFunc(p.ClassUses(withExports), func(u ClassUse) bool { return u.Class == class })
}

// ClassUse is one call a program's reachable code makes to a host
// function of an effect class, reported at the program's own call that
// reaches it: a crossing inside the standard library is reported at the
// call of the std function that leads to it (`fs.read_text(path)` in
// `main`), not inside std.
type ClassUse struct {
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

// ClassUses is every call to a host function with an effect class
// that the program reaches from main, the boot and, when withExports is
// set, each exported function: what BuildImage or BuildExportsImage would
// keep. Call it on a program that builds.
func (p *Program) ClassUses(withExports bool) []ClassUse {
	roots, boots, _ := p.reachRoots(Roots{Exports: withExports})
	return p.classUsesFrom(roots, boots)
}

// TestClassUses is ClassUses for the entry's test cases opts
// selects: every call to a host function with an effect class that
// those tests, with their group's boot, reach. Call it on a program that
// loads.
func (p *Program) TestClassUses(opts TestOptions) []ClassUse {
	roots, boots, _ := p.reachRoots(Roots{Tests: &opts})
	if len(roots) == 0 {
		return nil
	}
	return p.classUsesFrom(roots, boots)
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
	// Public adds every function of the project's own files that another
	// module can call (publicFuncs), each walked from once the other roots
	// are, so a function they reach keeps their chain: what the project's
	// [effects.self] holds a library to, whatever its own roots reach.
	Public bool
	// Besides, when set, starts instead from each function of the
	// program's own lowering and of its abstract bodies
	// (irbuild.Result.Abstract), outside std, a `once`'s initializer among
	// them, that none of these walks reached, with no boot: what an
	// editor's hover and marks add to what the program runs (editorWalks),
	// a generic function nothing instantiates among it.
	Besides []Walk
}

// reachRoots is the functions and boots r names, and the edge kind its
// functions are reached by.
func (p *Program) reachRoots(r Roots) ([]*ir.Func, []*ir.Symbol, vm.EdgeKind) {
	var roots []*ir.Func
	if r.Besides != nil {
		walked := map[*ir.Func]bool{}
		for _, w := range r.Besides {
			if w.reach != nil {
				for _, f := range w.reach.Walked() {
					walked[f] = true
				}
			}
		}
		// The abstract bodies of the generic code nothing instantiates are
		// the program's own lowering too, for what a hover says of them.
		abstract, _ := p.res.Abstract()
		for _, m := range append(slices.Clip(p.res.IR), abstract...) {
			funcs := m.Funcs()
			for _, c := range m.Cells() {
				if f := c.Initializer(); f != nil {
					funcs = append(slices.Clip(funcs), f)
				}
			}
			for _, f := range funcs {
				if !f.Pos().Stdlib() && !walked[f] {
					roots = append(roots, f)
				}
			}
		}
		return roots, nil, vm.EdgeRoot
	}
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

// publicFuncs is each function of the program's lowering, in the
// project's own files, that another module can call (spec §14
// *Visibility*), as an unknown caller of the project would: a `pub fn`,
// at the top of a file or in an inherent impl, and each function of an
// interface's impl for a type they can build, a `pub` type that is not
// `opaque` or a `pub` interface, which they implement. None in a test file, whose test cases
// reach its code, and only impls in an `internal/` directory, whose
// functions and types no other module names. Each instance of a generic
// function is among them, and so is its abstract body (irbuild's
// abstract.go), one of abstract, which stands for every instance any
// caller makes. The walk from them also starts from the bodies a value of
// each type their code makes may run (vm.ReachedEffects).
func (p *Program) publicFuncs(abstract []*ir.Module) []*ir.Func {
	mods := append(slices.Clip(p.res.IR), abstract...)
	var files []string
	for _, m := range mods {
		for _, f := range m.Funcs() {
			if !f.Pos().Stdlib() {
				files = append(files, f.Pos().File())
			}
		}
	}
	public := p.publicAt(files)
	var out []*ir.Func
	for _, m := range mods {
		for _, f := range m.Funcs() {
			if public(f.Pos().File(), f.Pos().Line(), f.Pos().Col()) {
				out = append(out, f)
			}
		}
	}
	return out
}

// publicAt answers, for a function declared at a line and column of one of
// files, whether another module can call it (publicFuncs' rule).
func (p *Program) publicAt(files []string) func(file string, line, col int) bool {
	type own struct {
		nodes []ast.Node
		// published is false for a file in an internal/ directory, and own
		// is nil for a test file.
		published bool
	}
	held := map[string]*own{}
	var order []string
	for _, file := range files {
		if _, seen := held[file]; seen || file == "" || p.moduleOf(file) != "" {
			continue
		}
		if _, isStd := std.ImageName(file); isStd {
			continue
		}
		parts := strings.Split(p.importOf(file), "::")
		if strings.HasSuffix(parts[len(parts)-1], "_test") {
			held[file] = nil
			continue
		}
		held[file] = &own{nodes: p.syntaxOf(file), published: !slices.Contains(parts[:len(parts)-1], "internal")}
		order = append(order, file)
	}
	// built is the types another module can build a value of.
	built := map[string]bool{}
	for _, file := range order {
		if !held[file].published {
			continue
		}
		for _, n := range held[file].nodes {
			switch d := n.(type) {
			case *ast.StructDef:
				built[d.Name] = built[d.Name] || d.Public && !d.Opaque
			case *ast.EnumDef:
				built[d.Name] = built[d.Name] || d.Public && !d.Opaque
			case *ast.TypeDef:
				built[d.Name] = built[d.Name] || d.Public && !d.Opaque
			case *ast.InterfaceDef:
				// Another module implements a `pub` interface and holds its
				// values, so an impl for the interface type runs for them.
				built[d.Name] = built[d.Name] || d.Public
			}
		}
	}
	public := map[*ast.FuncDef]bool{}
	for _, file := range order {
		o := held[file]
		for _, n := range o.nodes {
			switch d := n.(type) {
			case *ast.FuncDef:
				public[d] = o.published && d.Public
			case *ast.ImplBlock:
				impl := d.Interface != nil && built[analysis.TypeExprBaseName(d.Receiver)]
				for _, item := range d.Items {
					if fd, ok := item.(*ast.FuncDef); ok {
						public[fd] = impl || o.published && fd.Public && d.Interface == nil
					}
				}
			}
		}
	}
	return func(file string, line, col int) bool {
		return held[file] != nil && public[p.funcDefAt(file, line, col)]
	}
}

// classUsesFrom is the classed calls reached from roots and boots.
func (p *Program) classUsesFrom(roots []*ir.Func, boots []*ir.Symbol) []ClassUse {
	classes := stdlibbindings.Classes()
	sites := vm.ReachedCrossingSites(p.entry, p.res.IRModules(), roots, boots, func(name string) bool {
		return classes[name] != ""
	})
	out := make([]ClassUse, len(sites))
	for i, s := range sites {
		out[i] = ClassUse{
			Class:    classes[s.Name],
			Crossing: s.Name,
			Callee:   s.Via,
			Site:     fmt.Sprintf("%s:%d:%d", p.imageName(s.At.File()), s.At.Line(), s.At.Col()),
			Chain:    s.Chain,
		}
	}
	return out
}

// UsesFamilies is the key of each optional family (stdlibadapters.Families:
// the network, WebAssembly modules) whose crossings the program reaches,
// from main, the boot and, when withExports is set, each exported
// function, in the families' order: none, and `nomi build` appends it to
// the lean runner, built with every family's tag; any, and to the runner
// that has them all.
func (p *Program) UsesFamilies(withExports bool) []string {
	roots, boots, _ := p.reachRoots(Roots{Exports: withExports})
	reached := vm.ReachedCrossings(p.entry, p.res.IRModules(), roots, boots)
	var out []string
	for _, fam := range stdlibadapters.Families() {
		if slices.ContainsFunc(reached, func(name string) bool {
			_, found := slices.BinarySearch(fam.Names, name)
			return found
		}) {
			out = append(out, fam.Key)
		}
	}
	return out
}

// Unsupported is a build refused because the program reaches calls its
// target cannot make: Command is the command as the user wrote it (`nomi
// build --target js/wasm`), Target the target's name, and Uses the calls,
// each of a class in Lacking.
type Unsupported struct {
	Command string
	Target  string
	Lacking []string
	Uses    []ClassUse
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
	var uses []ClassUse
	for _, u := range p.ClassUses(withExports) {
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
		if slices.ContainsFunc(u.Uses, func(c ClassUse) bool { return c.Class == class }) {
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

// ChainStep is one function on the way from a root to an effect call, and
// the Edge that reached it.
type ChainStep = vm.ChainStep

// EffectCall is one effect a program has: a call, in code outside the
// standard library, that has an effect an effects list names, through
// std or itself.
type EffectCall struct {
	// Name is what the call is named by: the std module the code called,
	// as an import names it (`std::fs`, `std::net` for `net.dial` though it
	// crosses through http's host functions). An embedder's own `host fn`
	// is `host:` and its key.
	Name string
	// Module is the module charged for the call, the one whose file holds
	// it: a dependency's name in the program (requiredModule.path), or "" for the program's own files.
	Module string
	// Import is the file that holds the call as the code of its own Nomi
	// module imports it (importOf): `pricing`, `reports::daily`. It is the
	// key of the file's entry under [effects.self.modules].
	Import string
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
func (c EffectCall) ReachText() string {
	return vm.ReachText(c.Chain, c.Edge, c.Callee)
}

// EffectCalls is every effect call the program reaches from the roots r
// names, in position order: each crossing of a std host function or VM
// intrinsic that is neither pure nor held (internal/stdlibbindings,
// "Effects and World"), each embedder's host function, and the entry
// boot's Startup, which holds the environment and the arguments. A call
// is named by the
// std module the program's or a dependency's own code called, and charged
// to the module whose file holds that call; a callback passed into std or
// into a dependency is the code of the module that wrote it. Like the
// build's walk, it over-approximates (vm.ReachedEffects), and each Chain
// says why a call counts. Call it on a program that loads.
func (p *Program) EffectCalls(r Roots) []EffectCall {
	return p.walk(r).Calls
}

// walk is the walk EffectCalls answers the calls of, with what each
// function outside std can reach (Walk.reach).
func (p *Program) walk(r Roots) Walk {
	w := Walk{Tests: r.Tests != nil, fills: r.Besides != nil, public: r.Public && r.Tests == nil, p: p}
	roots, boots, kind := p.reachRoots(r)
	var late []*ir.Func
	var abstract []*ir.Module
	if r.Public && r.Tests == nil || r.Besides != nil {
		abstract, _ = p.res.Abstract()
	}
	if r.Public && r.Tests == nil {
		late = p.publicFuncs(abstract)
	}
	if len(roots) == 0 && len(boots) == 0 && len(late) == 0 {
		return w
	}
	entry := p.entry
	if entry == nil {
		// A library whose own files retain no function, all of them
		// generic, has their abstract bodies.
		entry = ir.NewModule(p.prog.Entry().Path)
	}
	w.reach = vm.ReachedEffects(entry, append(p.res.IRModules(), abstract...), roots, late, kind, boots, compilerCrossing)
	calls := w.reach.Calls
	out := make([]EffectCall, len(calls))
	// modules is each file's module and import path.
	modules := map[string][2]string{}
	for i, c := range calls {
		file := c.At.File()
		module, ok := modules[file]
		if !ok {
			module = [2]string{p.moduleOf(file), p.importOf(file)}
			modules[file] = module
		}
		out[i] = EffectCall{
			Name:     p.effectName(c),
			Module:   module[0],
			Import:   module[1],
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

// EffectList is a list an effect call can be over (NotAllowed.List).
type EffectList string

// The lists of nomi.toml's effects tables a call can be over.
const (
	// OverSelf is [effects.self], which holds the project's own files:
	// its std, with std_in_tests in tests.
	OverSelf EffectList = "self"
	// OverDeps is [effects.deps], which holds every dependency's
	// files: its std ceiling, with std_in_tests in tests.
	OverDeps EffectList = "deps"
	// OverSelfEntry is the entry of one of the project's own modules
	// under [effects.self.modules], whose key is NotAllowed.Entry.
	OverSelfEntry EffectList = "self.modules"
	// OverEntry is the module's entry under [effects.deps.modules].
	OverEntry EffectList = "deps.modules"
	// OverDeclared is the [effects.self] in the module's own
	// nomi.toml, which it declares for its own files: its std, its entry
	// for the file's module under its [effects.self.modules]
	// (NotAllowed.Entry).
	OverDeclared EffectList = "declared"
)

// NotAllowed is an effect call a program reaches that nomi.toml's
// effects tables, or the host's lists, do not allow.
type NotAllowed struct {
	EffectCall
	// List is the list that does not allow the call. The module it holds
	// is Module, the one whose file holds the call: the project for
	// OverSelf and OverSelfEntry, a dependency for the others.
	List EffectList
	// Entry is, for a call over an entry under an [effects.self.modules]
	// table, the entry's key, the call's Import: the project's own table
	// for OverSelfEntry, the dependency's own for OverDeclared. It is ""
	// for any other list.
	Entry string
	// Allowed is the std names List holds, with std_in_tests when the
	// roots are tests for OverSelf and OverDeps.
	Allowed []string
	// EntryDenies reports, for a call over a module's own list, that the
	// project's entry for the module does not allow it either.
	EntryDenies bool
	// CeilingDenies reports, for a call over an entry or a module's own
	// list, that the table the entry is under does not allow it either:
	// [effects.self]'s std for OverSelfEntry, [effects.deps]'
	// ceiling for the others.
	CeilingDenies bool
	// OwnStdDenies reports, for a call over a dependency's own entry
	// (OverDeclared with Entry), that its own [effects.self]'s std
	// does not allow it either.
	OwnStdDenies bool
	// Host reports that the list is the one the host holds the program to
	// (WithEffects), not nomi.toml's.
	Host bool
	// Tests reports that the call is reached from test cases, so the
	// project's lists hold std_in_tests too.
	Tests bool
}

// EffectCallsNotAllowed is every effect call the program reaches from the
// roots r names (EffectCalls) that lists, with the program's dependencies'
// own lists, does not allow, in the order EffectCalls answers them. A
// call in the project's own files is held, when Self is not nil, by two
// lists:
//
//   - Self: its name is in Std, with StdInTests when r names tests;
//   - the entry for the file's module (its Import) under Self.Modules,
//     which holds the call from every root.
//
// A call in a dependency's files is held, when Deps is not nil, by three:
//
//   - Deps: its name is in Std when it is set, with StdInTests when r
//     names tests;
//   - the project's entry for the module under Deps.Modules;
//   - the module's own [effects.self], when its nomi.toml has one: its
//     name is in its std and in its entry for the file's module under its
//     own [effects.self.modules]. The rest of its tables are for its own
//     builds.
//
// A call over more than one list is reported once: over the module's own
// list first, then over the entry, then over the table the entry is under,
// with EntryDenies, CeilingDenies and OwnStdDenies saying which others it
// is over. Nil lists allow everything, the dependencies' own included.
func (p *Program) EffectCallsNotAllowed(r Roots, lists *Effects) []NotAllowed {
	if lists == nil {
		return nil
	}
	calls := p.EffectCalls(r)
	return notAllowedBy(calls, r.Tests != nil, lists, p.declaredLists(calls))
}

// notAllowedBy is each of calls, reached from tests when tests is set, that
// lists and declared do not allow (standingsOf): the refusals.
func notAllowedBy(calls []EffectCall, tests bool, lists *Effects, declared map[string]*analysis.SelfEffects) []NotAllowed {
	var out []NotAllowed
	for _, s := range standingsOf(calls, tests, lists, declared) {
		if s.refused {
			out = append(out, s.NotAllowed)
		}
	}
	return out
}

// standing is how the lists stand on one effect call (standingsOf):
// refused, with the NotAllowed that reports it, or allowed. in is where
// the project's nomi.toml says so, as an editor's hover names it
// (effects.ReachedLine): each table that allows the call, none when no
// table holds it, or the one list that refuses it.
type standing struct {
	NotAllowed
	refused bool
	in      []string
}

// standingsOf is how lists and declared stand on each of calls, reached
// from tests when tests is set, in order, by EffectCallsNotAllowed's
// rules, which decide here: the one rule that holds a call to a list
// (effects.Allows), for every list. declared is each module's own
// [effects.self], by its name in the program (declaredLists); nil holds no
// module to a table of its own. Nil for nil lists.
func standingsOf(calls []EffectCall, tests bool, lists *Effects, declared map[string]*analysis.SelfEffects) []standing {
	if lists == nil {
		return nil
	}
	const selfTable, depsTable = "`[effects.self]`", "`[effects.deps]`"
	out := make([]standing, 0, len(calls))
	for _, c := range calls {
		s := standing{NotAllowed: NotAllowed{EffectCall: c, Tests: tests}}
		n := &s.NotAllowed
		refuse := func(list EffectList, names []string, in string) {
			s.refused, n.List, n.Allowed = true, list, names
			if in != "" {
				s.in = []string{in}
			}
		}
		// The table that holds the call, by the module whose file holds it:
		// its ceiling, whether that ceiling holds the call, and its entries,
		// keyed by key. [effects.self] holds the project's own files, its
		// entries keyed by the file's module as the project imports it;
		// [effects.deps] a dependency's, its entries keyed by the module's
		// name, and its ceiling only when it sets one.
		var (
			ceiling                []string
			ceilingHolds           bool
			entries                map[string][]string
			key, table             string
			ceilingList, entryList EffectList
			own                    *analysis.SelfEffects
		)
		switch {
		case c.Module == "" && lists.Self != nil:
			self := lists.Self
			ceiling, ceilingHolds = self.StdFor(tests), true
			entries, key, table, ceilingList, entryList = self.Modules, c.Import, selfTable, OverSelf, OverSelfEntry
		case c.Module != "" && lists.Deps != nil:
			d := lists.Deps
			ceiling = d.StdFor(tests)
			ceilingHolds = ceiling != nil
			entries, key, table, ceilingList, entryList = d.Modules, c.Module, depsTable, OverDeps, OverEntry
			own = declared[c.Module]
		default:
			out = append(out, s)
			continue
		}
		ceilingDenies := ceilingHolds && !effects.Allows(ceiling, c.Name)
		entryDenies := func(entries map[string][]string, key string) ([]string, bool) {
			entry, has := entries[key]
			return entry, has && !effects.Allows(entry, c.Name)
		}
		entry, denied := entryDenies(entries, key)
		_, entryHolds := entries[key]
		entryIn := fmt.Sprintf("the entry for `%s`", key)
		// A dependency's own [effects.self] holds its files as the
		// project's holds the project's: by its std, and by its entry for
		// the file's module.
		var ownStdDenies, ownEntryDenies bool
		var ownEntry []string
		if own != nil {
			ownStdDenies = !effects.Allows(own.Std, c.Name)
			ownEntry, ownEntryDenies = entryDenies(own.Modules, c.Import)
		}
		switch {
		case ownEntryDenies:
			refuse(OverDeclared, ownEntry, "")
			n.Entry, n.OwnStdDenies = c.Import, ownStdDenies
			n.EntryDenies, n.CeilingDenies = denied, ceilingDenies
		case ownStdDenies:
			refuse(OverDeclared, own.Std, "")
			n.EntryDenies, n.CeilingDenies = denied, ceilingDenies
		case denied:
			refuse(entryList, entry, entryIn)
			n.CeilingDenies = ceilingDenies
			if entryList == OverSelfEntry {
				n.Entry = key
			}
		case ceilingDenies:
			refuse(ceilingList, ceiling, table)
		default:
			if ceilingHolds {
				s.in = append(s.in, table)
			}
			if entryHolds {
				s.in = append(s.in, entryIn)
			}
		}
		out = append(out, s)
	}
	return out
}

// Walk is the effect calls one walk over a program reaches
// (EffectCalls): from main, the boot and the exported functions, or, with
// Tests, from the test cases.
type Walk struct {
	Tests bool
	Calls []EffectCall
	// fills reports a walk from the functions other walks did not reach
	// (Roots.Besides): the hover lines it finds go only where theirs leave
	// none (reachedCalls), so a function they reach reads as they say.
	fills bool
	// public reports a walk from the project's public functions
	// (Roots.Public), their abstract bodies among them.
	public bool
	// reach is the walk Calls come from, in the same order, which answers
	// what each function outside std can reach (reachedFuncs); nil when the
	// walk had no roots.
	reach *vm.EffectWalk
	// p is the program walked, which names a function's module and file
	// (Reach.Trees).
	p *Program
}

// tightestLists is the smallest pair of effects tables under which
// notAllowedBy allows every call walks reach, beside no module's own
// table: removing any one name from them refuses a call. An unlisted call
// (a `dbg`) adds nothing, since every list allows it. Self's Std holds
// each name a walk from main reaches in the project's own files, and
// StdInTests what only the tests reach. Deps' lists do the same for the
// dependencies' files, its Std the ceiling. Its Modules has an entry for
// each of
// deps, the dependencies an entry may name, holding the std names that
// module's files reach from every walk. A dependency not among deps gets
// no entry. Self's Modules does the same for own, the project's modules by
// their import paths, and is nil for nil own.
func tightestLists(walks []Walk, deps, own []string) *Effects {
	self := &analysis.SelfEffects{Std: []string{}}
	d := &analysis.DepsEffects{Std: []string{}, Modules: map[string][]string{}}
	for _, m := range deps {
		d.Modules[m] = []string{}
	}
	if own != nil {
		self.Modules = map[string][]string{}
		for _, m := range own {
			self.Modules[m] = []string{}
		}
	}
	add := func(list *[]string, name string) {
		if !slices.Contains(*list, name) {
			*list = append(*list, name)
		}
	}
	// addTo adds name to the entry for key in entries, when it has one.
	addTo := func(entries map[string][]string, key, name string) {
		if entry, ok := entries[key]; ok {
			add(&entry, name)
			entries[key] = entry
		}
	}
	for _, w := range walks {
		for _, c := range w.Calls {
			switch {
			case !effects.Listed(c.Name):
			case c.Module == "":
				if w.Tests {
					add(&self.StdInTests, c.Name)
				} else {
					add(&self.Std, c.Name)
				}
				addTo(self.Modules, c.Import, c.Name)
			default:
				if w.Tests {
					add(&d.StdInTests, c.Name)
				} else {
					add(&d.Std, c.Name)
				}
				addTo(d.Modules, c.Module, c.Name)
			}
		}
	}
	self.StdInTests = slices.DeleteFunc(self.StdInTests, func(n string) bool { return slices.Contains(self.Std, n) })
	d.StdInTests = slices.DeleteFunc(d.StdInTests, func(n string) bool { return slices.Contains(d.Std, n) })
	for _, list := range [][]string{self.Std, self.StdInTests, d.Std, d.StdInTests} {
		slices.Sort(list)
	}
	for _, entries := range []map[string][]string{self.Modules, d.Modules} {
		for _, entry := range entries {
			slices.Sort(entry)
		}
	}
	return &Effects{Self: self, Deps: d}
}

// declaredLists is the own [effects.self] of each module whose files
// hold one of calls and whose own nomi.toml has one, by its name in the program
// (moduleAt).
func (p *Program) declaredLists(calls []EffectCall) map[string]*analysis.SelfEffects {
	out := map[string]*analysis.SelfEffects{}
	for _, c := range calls {
		if _, done := out[c.Module]; done || c.Module == "" {
			continue
		}
		if m := p.moduleAt(c.at.File()); m != nil && m.declares() {
			out[c.Module] = m.manifest.Effects.Self
		}
	}
	return out
}

// effectName is the name of an effect call (effects.Name): `std::`
// and its std module, or for a crossing no std module declares, an
// embedder's host function, `host:` and its key. An unlisted call (a
// `dbg`) is its key alone, which no list names (effects.Listed).
func (p *Program) effectName(c vm.EffectCall) string {
	if c.Unlisted {
		return hostadapt.DisplayName(c.Crossing)
	}
	return effects.Name(c.Std, hostadapt.DisplayName(c.Crossing))
}

// moduleOf is the module whose file path is: "" for the program's own
// files (an in-memory one among them) and the standard library's, else the
// name in the program of the dependency moduleAt finds.
func (p *Program) moduleOf(path string) string {
	if m := p.moduleAt(path); m != nil {
		return m.path
	}
	return ""
}

// importOf is the file at path as the code of its own Nomi module imports it
// (spec §14 *Files and modules*): its path from the module's root, the
// project's (imageName) or a dependency's (moduleAt), without `.nomi`,
// with `::` between directories: `pricing`, `reports::daily`,
// `pricing_test`. A file the program holds in memory is named by its path
// as given.
func (p *Program) importOf(path string) string {
	name := p.imageName(path)
	if m := p.moduleAt(path); m != nil {
		if rel, err := filepath.Rel(m.root, path); err == nil {
			name = filepath.ToSlash(rel)
		}
	}
	return strings.ReplaceAll(strings.TrimSuffix(name, ".nomi"), "/", "::")
}

// ownModules is each module of the project whose files the program holds,
// by its import path (importOf), sorted: what entries_required under
// [effects.self] asks an entry of. A program reopened from the
// artifact cache holds only its entry, as requiredModules says.
func (p *Program) ownModules() []string {
	var out []string
	for _, file := range p.prog.Modules {
		if _, isStd := std.ImageName(file.Path); isStd || file.Path == "" || p.moduleAt(file.Path) != nil {
			continue
		}
		if name := p.importOf(file.Path); !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// projectModules is each module of the project, by its import path, sorted:
// each .nomi file under its root outside another module's directory
// (analysis.WorkspaceNomiFiles), with the modules the program holds
// (ownModules), which a program held in memory has alone. A key under
// [effects.self.modules] names one of them.
func (p *Program) projectModules() []string {
	out := p.ownModules()
	if root := p.prog.Root; root != "" {
		for _, file := range analysis.WorkspaceNomiFiles(root) {
			if dir, ok := manifestDirAbove(filepath.Dir(file)); !ok || !sameDir(dir, root) {
				continue
			}
			if name := p.importOf(file); !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// projectModule is the name of the project whose own files [effects.self]
// holds, as `nomi effects` reports it: its nomi.toml's `[module].name`;
// without one, its entry's module name, which is the name LoadSource is
// given (`LoadSource("rule", ...)` is `rule`) and a lone file's name
// without `.nomi`.
func (p *Program) projectModule() string {
	p.projectOnce.Do(func() {
		if m := programManifest(p.prog.Root, p.cfg.manifest); m != nil && m.Name != "" {
			p.project = m.Name
			return
		}

		p.project = p.prog.Entry().Name
	})
	return p.project
}

// requiredModule is a module other than the project whose files a program
// holds: a dependency.
type requiredModule struct {
	// path is its name in the program (modcache.Dep.Name): the name the
	// project's nomi.toml gives it, which keys its entry under
	// [effects.deps.modules]; its `[module].name` when the module graph does not hold its directory,
	// or its directory when it has neither.
	path string
	// root is its directory, the one holding its nomi.toml.
	root string
	// manifest is its nomi.toml, nil when it cannot be read (the front
	// end reports that).
	manifest *analysis.Manifest
}

// declares reports whether m's own nomi.toml has an [effects.self]
// table, which declares m's own code by its std.
func (m *requiredModule) declares() bool {
	return m.manifest != nil && m.manifest.Effects != nil && m.manifest.Effects.Self != nil
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
		if d, ok := g.At(root); ok {
			m.path = d.Name
		}
	}
	if p.modules == nil {
		p.modules = map[string]*requiredModule{}
	}
	p.modules[root] = m
	return m
}

// requiredModules is each module other than the project whose Nomi files
// the program holds, by its name in the program: the modules the program's
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
