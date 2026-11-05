package irbuild

// Listed std modules lowered once per process and linked by name.
//
// A std module in programStdModules is lowered with the program that reaches
// it (programstd.go): its types are the program's, keyed in the program's
// type registry. Its bodies need not be. Every listed module is lowered once
// for the process, as one program whose files are all of them, and kept
// (keptListedStd): its modules, the std instances its bodies asked for, its
// typed literals and refusals, and the linkage name of every declaration it
// defines (link.go). The std bundle carries that lowering (stdbundlelisted.go),
// so a process with one decodes it instead.
//
// A program that reaches listed modules links the part of the kept lowering
// those modules make (restrict): their own modules, the instances at their
// types, and their literal sites and refusals, so the program holds no type,
// impl or refusal of a listed module it does not reach. It still declares
// the modules it reaches (types, signatures, impls, so its own files can name
// them) but lowers none of their bodies: each of their gens is `linked`, and
// every table of the program resolves a linkage name the kept lowering
// defines to the kept symbol (ir.Table.SetLinkage). What a program asks of a
// linked module that the kept lowering lacks, a generic function's instance
// at one of the program's types, is lowered by the linked gen as before, and
// links beside the kept modules. A body both define (a std instance the
// program asks for too) is kept once, the kept lowering's (ir's link-once
// rule).
//
// One lowering serves every set of listed modules: the builder's declaration
// waves are whole-program, so one lowering gives each module the others'
// symbols, and every program names a declaration by the one linkage name.
// The expectation suites link it into every recorded program that reaches a
// listed module. A process that can neither decode the kept lowering nor
// cache it lowers its first such program's set with the program, and links
// from the second (linkedStdFor).
//
// Not linked: a program whose entry is a listed module (its `//!` cases), a
// shared module's cases that import listed modules (Program.stdCases), a
// listed module checked from edited source with its twins, whose sibling
// modules must name the edited declarations rather than the kept ones, and a
// program that reaches a test-only fixture module (std/_fixtures). A
// reference editor's case is the exception to the first two: its module is
// the shared analysis's, unedited, so every module links, the entry included
// (Program.linkAllStd), and only the case is lowered.

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/std"
)

// linkedStd is listed std modules lowered once for the process: all of them
// (listedStd.all), or the part a program links (listedStd.restrict).
type linkedStd struct {
	// mods are the modules and the std instances their bodies asked for, in
	// the order the lowering produced them.
	mods []*ir.Module
	// units are the listed modules' own modules, the first of mods.
	units []*ir.Module
	// defined is every linkage name the modules name, to its symbol: what
	// a program's tables import.
	defined map[string]*ir.Symbol
	// own is the linkage name of every function and cell the modules
	// define, for the link-once drop. A program reaches a kept symbol only
	// by its linkage name, so one with none is never also the program's.
	own map[string]bool
	// literals are the typed literals the bodies lowered, which a program
	// that links them checks at load.
	literals []LiteralSite
	// unsupported and masked are the refusals, reported with each program
	// that links them as they were when it lowered the modules itself.
	unsupported []Unsupported
	masked      []Suppression
	// synthRefused is each synthesized impl block whose speculative lowering
	// failed (gen.emitSynthImpls), so a program's linked gen refuses it
	// without lowering it again.
	synthRefused map[*ast.ImplBlock]bool
	err          error
}

// linkedStdFor is the part of the kept lowering a program that reaches the
// listed modules names (sorted) links, or nil when it lowers them itself.
//
// A process with no listed section in its bundle and no cache to write one
// to would lower every listed module for its first such program, about
// twice what lowering the program's own set with it costs, and a process
// that runs one program (`nomi run` with NOMI_ARTIFACT_CACHE=off) would earn
// nothing back. So its first program lowers its set itself, and the kept
// lowering is made for the second.
func linkedStdFor(names []string) *linkedStd {
	for _, name := range names {
		if !programStdModules[name] {
			return nil
		}
	}
	if listedStdAsked.Add(1) == 1 && !listedStdKeepable() {
		return nil
	}
	k := keptListedStd()
	if k == nil || k.all.err != nil {
		return nil
	}
	return k.restrict(names)
}

// listedStdAsked counts the programs that reached a listed module.
var listedStdAsked atomic.Int32

// listedStdKeepable reports whether the kept lowering of the listed modules
// costs this process no more than a program's own: its bundle has the
// listed section, or it can cache one for the next process.
func listedStdKeepable() bool {
	stdlibLowering()
	st := &stdBundleState
	return st.listed != nil || st.path != "" && st.shared != nil || keptListedStdMade()
}

// linkedStdNames is the listed modules p's lowering may link (p's modules
// past index from, added by withProgramStd), or nil when p links none.
func linkedStdNames(p *Program, from int) []string {
	if p.noLinkStd || from >= len(p.Modules) {
		return nil
	}
	if !p.linkAllStd && (p.stdCases != nil || strings.HasPrefix(p.Modules[0].Name, "std/")) {
		return nil
	}
	shared := programStdModuleNodes()
	var names []string
	for i := range p.Modules {
		m := &p.Modules[i]
		if m.twins != nil {
			return nil
		}
		if i < from {
			continue
		}
		name, inStd := strings.CutPrefix(m.Name, "std/")
		if !inStd || !sameNodes(shared[name], m.Nodes) {
			return nil
		}
		names = append(names, name)
	}
	if p.linkAllStd {
		// The entry is a listed module too, ahead of the ones it reaches.
		slices.Sort(names)
	}
	if !slices.IsSorted(names) {
		return nil
	}
	return names
}

// programStdModule is the listed module name as one Module of a program,
// from the shared analysis.
func programStdModule(name string) Module {
	lib := std.Shared()
	stdBodies(name)
	return Module{
		Path:  lib.SourcePath(name),
		Name:  "std/" + name,
		Nodes: programStdModuleNodes()[name],
		FA:    lib.Files[name],
	}
}

// dropLinkedDuplicates removes from each module of the program's lowering a
// function or cell the kept lowering defines: the same body, lowered twice
// (ir's link-once rule).
func dropLinkedDuplicates(mods []*ir.Module, ls *linkedStd) {
	for _, m := range mods {
		for _, f := range append([]*ir.Func(nil), m.Funcs()...) {
			if ls.defines(f.Sym()) {
				m.RemoveFunc(f)
			}
		}
		for _, c := range append([]*ir.Cell(nil), m.Cells()...) {
			if ls.defines(c.Sym()) {
				m.RemoveCell(c)
			}
		}
	}
}

// retains reports whether g's lowering, or the kept lowering of a linked
// module, holds fn's body.
func (g *gen) retains(fn *ir.Symbol) bool {
	if g == nil {
		return false
	}
	if g.irMod != nil && g.irMod.FuncFor(fn) != nil {
		return true
	}
	return g.reg != nil && g.reg.linked.defines(fn)
}

// defines reports whether the kept lowering defines sym's function or cell:
// sym is the one symbol it made under its linkage name.
func (ls *linkedStd) defines(sym *ir.Symbol) bool {
	if ls == nil || sym == nil || sym.Link() == "" || !ls.own[sym.Link()] {
		return false
	}
	return ls.defined[sym.Link()] == sym
}

// listedStd is the one lowering of every listed std module, from which each
// program takes the part it reaches (restrict).
type listedStd struct {
	all *linkedStd
	// needs is, for each module of all.mods, the listed modules a program
	// must reach to link it: a unit's own module, an instance's type
	// arguments' modules (stdInstances.needs).
	needs map[*ir.Module][]string
	// paths is each listed module's source path: a literal site's,
	// refusal's or suppression's file.
	paths map[string]string
	// links is the linkage name of each symbol a module of all.mods names,
	// and owns of each function and cell it defines.
	links, owns map[*ir.Module][]string

	mu    sync.Mutex
	bySet map[string]*linkedStd
}

// newListedStd keeps all, the lowering of every listed module, with each of
// its instance modules' needs, and files what each module names and defines.
func newListedStd(all *linkedStd, instNeeds [][]string) *listedStd {
	k := &listedStd{all: all, needs: map[*ir.Module][]string{}, paths: listedStdPaths(),
		links: map[*ir.Module][]string{}, owns: map[*ir.Module][]string{}, bySet: map[string]*linkedStd{}}
	if all.err != nil {
		return k
	}
	byPath := map[string]string{}
	for name, path := range k.paths {
		byPath[path] = name
	}
	for _, m := range all.units {
		name, ok := byPath[m.Name()]
		if !ok {
			// A unit that is no listed module's is linked by no program.
			name = "?" + m.Name()
		}
		k.needs[m] = []string{name}
	}
	for i, m := range all.mods[len(all.units):] {
		if i < len(instNeeds) {
			k.needs[m] = instNeeds[i]
		}
	}
	all.defined, all.own = map[string]*ir.Symbol{}, map[string]bool{}
	for _, m := range all.mods {
		for _, s := range ir.ModuleSymbols(m) {
			if s.Link() != "" {
				all.defined[s.Link()] = s
				k.links[m] = append(k.links[m], s.Link())
			}
		}
		for _, f := range m.Funcs() {
			if f.Sym() != nil && f.Sym().Link() != "" {
				k.owns[m] = append(k.owns[m], f.Sym().Link())
				all.own[f.Sym().Link()] = true
			}
		}
		for _, c := range m.Cells() {
			if c.Sym().Link() != "" {
				k.owns[m] = append(k.owns[m], c.Sym().Link())
				all.own[c.Sym().Link()] = true
			}
		}
	}
	return k
}

// restrict is the part of the lowering a program that reaches the listed
// modules names links: their own modules, the instances at their types,
// and their literal sites and refusals. A module of another listed module
// would put types and impls in the program that none of its files reach,
// and their refusals in its report.
func (k *listedStd) restrict(names []string) *linkedStd {
	key := strings.Join(names, ",")
	k.mu.Lock()
	defer k.mu.Unlock()
	if ls := k.bySet[key]; ls != nil {
		return ls
	}
	in := map[string]bool{}
	files := map[string]bool{}
	for _, name := range names {
		in[name] = true
		files[k.paths[name]] = true
	}
	out := &linkedStd{defined: map[string]*ir.Symbol{}, own: map[string]bool{}, synthRefused: k.all.synthRefused}
	for i, m := range k.all.mods {
		needs, known := k.needs[m]
		if !known {
			continue
		}
		reached := true
		for _, n := range needs {
			if !in[n] {
				reached = false
				break
			}
		}
		if !reached {
			continue
		}
		out.mods = append(out.mods, m)
		if i < len(k.all.units) {
			out.units = append(out.units, m)
		}
		for _, l := range k.links[m] {
			out.defined[l] = k.all.defined[l]
		}
		for _, l := range k.owns[m] {
			out.own[l] = true
		}
	}
	out.units = slices.Clip(out.units)
	for _, site := range k.all.literals {
		if files[site.Path] {
			out.literals = append(out.literals, site)
		}
	}
	for _, u := range k.all.unsupported {
		if files[u.File] {
			out.unsupported = append(out.unsupported, u)
		}
	}
	for _, s := range k.all.masked {
		if files[s.File] {
			out.masked = append(out.masked, s)
		}
	}
	k.bySet[key] = out
	return out
}

// synthRefused is each synthesized impl block of gens whose speculative
// lowering failed.
func synthRefused(gens []*gen) map[*ast.ImplBlock]bool {
	out := map[*ast.ImplBlock]bool{}
	for _, g := range gens {
		for _, d := range g.implOrder {
			if d.synth && !d.lowerable && d.why == "impl block" && d.decl != nil {
				out[d.decl] = true
			}
		}
	}
	return out
}

// settleLinkedSynthImpls is emitSynthImpls for a linked gen: the verdict of
// each synthesized block is the kept lowering's, which lowered its body.
func (g *gen) settleLinkedSynthImpls() {
	for _, d := range g.implOrder {
		if d.synth && d.lowerable && g.reg.linked.synthRefused[d.decl] {
			d.lowerable = false
			d.why = "impl block"
			d.whyDetail = d.label()
		}
	}
}
