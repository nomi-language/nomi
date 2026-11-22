package irbuild

// The listed std modules' section of a std bundle (stdbundle.go).
//
// keptListedStd is the process's one lowering of every listed module
// (programstdlink.go). A bundle's listed section holds it: the modules,
// units first and then the std instances their bodies asked for, each an
// object whose symbols name the shared modules' by linkage name, decoded
// with a linker over the one the shared modules' objects were
// (stdObjects.linkerOver); each instance module's needs; and the lowering's
// literal sites, refusals, suppressions and refused synthesized impl blocks.
//
// The layout: uvarint the count of shared modules the section needs decoded
// first and each one's name (listedSharedDeps); uvarint the module count
// and bytes each module's object;
// uvarint the unit count; per instance module, uvarint its needs' count and
// each name; uvarint the literal sites' count and each site; the refusals;
// the suppressions; and the refused blocks, each as its module's name and
// its index among the module's nodes. A literal site's cell, which only its
// module names, is written as its module's index and the cell's index in
// it, its renderer as its module's index plus one (0 for none) and its
// index in that module's functions, and its check declaration by its
// declaration name.

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/std"
)

// keptListed is the process's one lowering of every listed module.
var keptListed struct {
	once sync.Once
	v    *listedStd
	made atomic.Bool
}

// keptListedStdMade reports whether keptListedStd has answered.
func keptListedStdMade() bool { return keptListed.made.Load() }

// keptListedStd is the process's one lowering of every listed module:
// decoded from the bundle's listed section, else lowered, and then written
// into the cached bundle for the next process.
func keptListedStd() *listedStd {
	keptListed.once.Do(func() {
		// The kept lowering is of every listed module, which the stdlib
		// builds, with what they import, when a program first reaches one.
		std.Shared().Ensure(listedStdNames()...)
		stdlibLowering()
		st := &stdBundleState
		if st.listed != nil {
			ls, err := decodeListedSection(st.listed)
			if err == nil {
				keptListed.v = ls
				return
			}
			stdBundleDebug("listed section not used: %v", err)
		}
		all, needs := lowerListedStd()
		if all.all.err != nil {
			keptListed.v = all
			return
		}
		data, err := encodeListedSection(all, needs)
		if err != nil {
			if testing.Testing() {
				panic("irbuild: the listed std lowering does not encode as a bundle section: " + err.Error())
			}
			stdBundleDebug("no listed section: %v", err)
			keptListed.v = all
			return
		}
		if st.shared != nil {
			writeStdBundle(st.shared, data)
		}
		// This process runs on the decoded section too, as every later one
		// does: decoding links two instance modules that define one instance
		// to one body (ir.Linker.Define), which the lowering keeps apart.
		dec, err := decodeListedSection(frameSection(data))
		if err != nil {
			if testing.Testing() {
				panic("irbuild: the listed section this process wrote does not decode: " + err.Error())
			}
			stdBundleDebug("the listed section does not decode: %v", err)
			keptListed.v = all
			return
		}
		keptListed.v = dec
	})
	keptListed.made.Store(true)
	return keptListed.v
}

// listedStdNames is every listed module, sorted.
func listedStdNames() []string {
	names := make([]string, 0, len(programStdModules))
	for name := range programStdModules {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// lowerListedStd lowers every listed module as a program of their own, and
// answers the needs of each of its instance modules.
func lowerListedStd() (*listedStd, [][]string) {
	// p has no decline hooks: the host's belong to the program being
	// loaded, which is not this one (stdlibLowering's reason).
	p := &Program{noLinkStd: true}
	for _, name := range listedStdNames() {
		p.Modules = append(p.Modules, programStdModule(name))
	}
	res, refusal, err := GenerateIR(p)
	if err != nil {
		return &listedStd{all: &linkedStd{err: err}}, nil
	}
	ls := &linkedStd{literals: res.Literals, synthRefused: res.synthRefused}
	if u, ok := refusal.(*UnsupportedError); ok && u != nil {
		ls.unsupported, ls.masked = u.Items, u.Suppressed
	}
	ls.mods = append(append(ls.mods, res.IR...), res.instMods...)
	ls.units = ls.mods[:len(res.IR):len(res.IR)]
	return newListedStd(ls, res.instNeeds), res.instNeeds
}

// encodeListedSection writes the lowering of every listed module as a
// bundle's listed section.
func encodeListedSection(k *listedStd, instNeeds [][]string) ([]byte, error) {
	all := k.all
	shared := stdlibLowering().sortedModules()
	if err := ir.CheckLinkage(append(slices.Clip(shared), all.mods...), nil); err != nil {
		return nil, err
	}
	deps, err := listedSharedDeps(all.mods)
	if err != nil {
		return nil, err
	}
	var bad []string
	w := &bundleWriter{}
	w.uv(uint64(len(deps)))
	for _, d := range deps {
		w.str(d)
	}
	w.uv(uint64(len(all.mods)))
	for _, m := range all.mods {
		obj, err := ir.EncodeObject(m, ir.WithFileNames(stdImageName(&bad)))
		if err != nil {
			return nil, err
		}
		w.str(string(obj))
	}
	type at struct{ mod, i int }
	funcAt := map[*ir.Func]at{}
	cellAt := map[*ir.Cell]at{}
	for mi, m := range all.mods {
		for i, f := range m.Funcs() {
			funcAt[f] = at{mi, i}
		}
		for i, c := range m.Cells() {
			cellAt[c] = at{mi, i}
		}
	}
	file := stdImageName(&bad)
	w.uv(uint64(len(all.units)))
	if len(instNeeds) != len(all.mods)-len(all.units) {
		return nil, fmt.Errorf("%d instance modules and %d needs", len(all.mods)-len(all.units), len(instNeeds))
	}
	for _, needs := range instNeeds {
		w.uv(uint64(len(needs)))
		for _, n := range needs {
			w.str(n)
		}
	}
	names := stdLinkNames()
	var missing []string
	w.uv(uint64(len(all.literals)))
	for _, s := range all.literals {
		w.str(file(s.Path))
		for _, v := range []int{s.Line, s.Col, s.EndLine, s.EndCol} {
			w.uv(uint64(v))
		}
		w.str(s.Text)
		if p, ok := cellAt[s.Cell]; ok {
			w.uv(uint64(p.mod))
			w.uv(uint64(p.i))
		} else {
			missing = append(missing, "the cell of "+s.Text)
		}
		if s.Render == nil {
			w.uv(0)
		} else if p, ok := funcAt[s.Render]; ok {
			w.uv(uint64(p.mod + 1))
			w.uv(uint64(p.i))
		} else {
			missing = append(missing, "the renderer of "+s.Text)
		}
		w.bool1(s.Locale)
		w.bool1(s.Currency)
		w.bool1(s.Checked)
		w.bool1(s.Plain)
		w.str(s.Tag)
		w.uv(uint64(len(s.Statics)))
		for _, t := range s.Statics {
			w.str(t)
		}
		w.uv(uint64(len(s.Slots)))
		for _, sl := range s.Slots {
			w.uv(uint64(sl.Line))
			w.uv(uint64(sl.Col))
		}
		switch check := s.Check.(type) {
		case nil:
			w.str("")
		case *ast.FuncDef:
			d, ok := names[check]
			if !ok {
				missing = append(missing, "the check of "+s.Text)
			}
			w.str(d.String())
		default:
			missing = append(missing, fmt.Sprintf("the check of %s, a %T", s.Text, check))
		}
		w.str(s.CheckName)
		w.uv(uint64(s.CheckLine))
		w.uv(uint64(s.CheckCol))
	}
	w.uv(uint64(len(all.unsupported)))
	for _, u := range all.unsupported {
		w.str(u.Construct)
		w.str(file(u.File))
		w.uv(uint64(u.Line))
		w.uv(uint64(u.Col))
		w.str(u.Detail)
	}
	w.uv(uint64(len(all.masked)))
	for _, s := range all.masked {
		w.str(file(s.File))
		w.uv(uint64(s.Line))
	}
	type blockAt struct {
		module string
		i      int
	}
	var blocks []blockAt
	for name, nodes := range programStdModuleNodes() {
		for i, n := range nodes {
			if b, ok := n.(*ast.ImplBlock); ok && all.synthRefused[b] {
				blocks = append(blocks, blockAt{name, i})
			}
		}
	}
	if len(blocks) != len(all.synthRefused) {
		missing = append(missing, "a refused impl block")
	}
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].module < blocks[j].module || blocks[i].module == blocks[j].module && blocks[i].i < blocks[j].i
	})
	w.uv(uint64(len(blocks)))
	for _, b := range blocks {
		w.str(b.module)
		w.uv(uint64(b.i))
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("the listed std lowering names files outside std: %s", strings.Join(bad, ", "))
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the listed std lowering points outside its modules: %s", strings.Join(missing, ", "))
	}
	return w.b, nil
}

// listedSharedDeps is the shared modules a listed section of mods names,
// sorted, which a process decodes before the section: each module whose
// object defines a linkage name mods' objects write, and, for a name no
// shared module defines, each module whose object writes it too, so that
// a shared module decoded after the section links that name to the
// section's symbol rather than one of its own. It decodes every shared
// module to read what each defines and names.
func listedSharedDeps(mods []*ir.Module) ([]string, error) {
	o := stdBundleState.objects
	if o == nil {
		return nil, errors.New("the shared stdlib is not decoded from its objects")
	}
	if err := o.decodeAll(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	definer := map[string]string{}
	writers := map[string][]string{}
	for _, rec := range o.recs {
		if rec.mod == nil {
			continue
		}
		for _, f := range rec.mod.Funcs() {
			if s := f.Sym(); s != nil && s.Link() != "" {
				definer[s.Link()] = rec.name
			}
		}
		for _, c := range rec.mod.Cells() {
			if s := c.Sym(); s.Link() != "" {
				definer[s.Link()] = rec.name
			}
		}
		for _, s := range ir.ModuleSymbols(rec.mod) {
			if s.Link() != "" {
				writers[s.Link()] = append(writers[s.Link()], rec.name)
			}
		}
	}
	need := map[string]bool{}
	for _, m := range mods {
		for _, s := range ir.ModuleSymbols(m) {
			link := s.Link()
			if link == "" {
				continue
			}
			if d, ok := definer[link]; ok {
				need[d] = true
				continue
			}
			for _, w := range writers[link] {
				need[w] = true
			}
		}
	}
	out := make([]string, 0, len(need))
	for name := range need {
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// decodeListedSection decodes a listed section, framed as a bundle holds it
// (frameSection), against this process's shared lowering.
func decodeListedSection(framed []byte) (k *listedStd, err error) {
	defer func() {
		if r := recover(); r != nil {
			k, err = nil, fmt.Errorf("decoding the listed section: %v", r)
		}
	}()
	data, err := unframeSection(framed)
	if err != nil {
		return nil, err
	}
	r := &bundleReader{b: data}
	stdlibLowering()
	objects := stdBundleState.objects
	if objects == nil {
		return nil, errors.New("the shared stdlib is not decoded from its objects")
	}
	var deps []string
	for n := r.count(); n > 0 && r.err == nil; n-- {
		deps = append(deps, r.str())
	}
	if r.err != nil {
		return nil, r.err
	}
	// The shared modules the section names are decoded first, so its
	// objects link to their symbols, and the lowering taken after holds
	// them.
	if err := objects.decode(deps...); err != nil {
		return nil, err
	}
	shared := stdlibLowering().sortedModules()
	var badFile string
	rename := ir.WithDecodedFileNames(stdSourceName(&badFile))
	var mods []*ir.Module
	err = objects.linkOver(shared, func(l *ir.Linker) error {
		for n := r.count(); n > 0 && r.err == nil; n-- {
			obj := r.bytes()
			if r.err != nil {
				break
			}
			m, err := decodeObject(l, obj, rename)
			if err != nil {
				return err
			}
			mods = append(mods, m)
		}
		return r.err
	})
	if err != nil {
		return nil, err
	}
	file := stdSourceName(&badFile)
	ls := &linkedStd{mods: mods}
	units := r.count()
	if units > len(mods) {
		r.fail("unit count")
		units = 0
	}
	ls.units = mods[:units:units]
	instNeeds := make([][]string, len(mods)-units)
	for i := range instNeeds {
		for n := r.count(); n > 0 && r.err == nil; n-- {
			instNeeds[i] = append(instNeeds[i], r.str())
		}
	}
	byName := stdDeclsByName()
	n := r.count()
	for ; n > 0 && r.err == nil; n-- {
		s := LiteralSite{Path: file(r.str()), Line: int(r.uv()), Col: int(r.uv()), EndLine: int(r.uv()), EndCol: int(r.uv()), Text: r.str()}
		if m, i := r.uv(), r.uv(); r.err == nil && m < uint64(len(mods)) && i < uint64(len(mods[m].Cells())) {
			s.Cell = mods[m].Cells()[i]
		} else {
			r.fail("cell reference")
		}
		if m := r.uv(); m > 0 {
			i := r.uv()
			if r.err == nil && m <= uint64(len(mods)) && i < uint64(len(mods[m-1].Funcs())) {
				s.Render = mods[m-1].Funcs()[i]
			} else {
				r.fail("function reference")
			}
		}
		s.Locale, s.Currency, s.Checked, s.Plain, s.Tag = r.bool1(), r.bool1(), r.bool1(), r.bool1(), r.str()
		for n := r.count(); n > 0 && r.err == nil; n-- {
			s.Statics = append(s.Statics, r.str())
		}
		for n := r.count(); n > 0 && r.err == nil; n-- {
			s.Slots = append(s.Slots, SlotPos{Line: int(r.uv()), Col: int(r.uv())})
		}
		if name := r.str(); name != "" {
			fd, ok := byName[name].(*ast.FuncDef)
			if !ok {
				r.fail("check declaration " + name)
			}
			s.Check = fd
		}
		s.CheckName, s.CheckLine, s.CheckCol = r.str(), int(r.uv()), int(r.uv())
		ls.literals = append(ls.literals, s)
	}
	for n := r.count(); n > 0 && r.err == nil; n-- {
		ls.unsupported = append(ls.unsupported, Unsupported{Construct: r.str(), File: file(r.str()),
			Line: int(r.uv()), Col: int(r.uv()), Detail: r.str()})
	}
	for n := r.count(); n > 0 && r.err == nil; n-- {
		ls.masked = append(ls.masked, Suppression{File: file(r.str()), Line: int(r.uv())})
	}
	ls.synthRefused = map[*ast.ImplBlock]bool{}
	nodes := programStdModuleNodes()
	for n := r.count(); n > 0 && r.err == nil; n-- {
		module, i := r.str(), r.uv()
		b, ok := (*ast.ImplBlock)(nil), false
		if i < uint64(len(nodes[module])) {
			b, ok = nodes[module][i].(*ast.ImplBlock)
		}
		if !ok {
			r.fail("impl block reference")
			continue
		}
		ls.synthRefused[b] = true
	}
	if r.err == nil && len(r.b) != 0 {
		r.fail("trailing bytes")
	}
	if r.err != nil {
		return nil, r.err
	}
	if badFile != "" {
		return nil, fmt.Errorf("the listed section names %s, which is not a std file", badFile)
	}
	return newListedStd(ls, instNeeds), nil
}

// stdDeclsByName is every std declaration by its name: stdLinkNames
// inverted.
var stdDeclsByName = perStd(func() map[string]ast.Node {
	out := map[string]ast.Node{}
	for n, d := range stdLinkNames() {
		out[d.String()] = n
	}
	return out
})

// sortedModules is the index's modules in package order: the order GenerateIR
// links them in.
func (x *stdlibIndex) sortedModules() []*ir.Module {
	pkgs := make([]string, 0, len(x.irModules))
	for pkg := range x.irModules {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	mods := make([]*ir.Module, len(pkgs))
	for i, pkg := range pkgs {
		mods[i] = x.irModules[pkg]
	}
	return mods
}

// listedStdPaths is each listed module's source path, by name.
func listedStdPaths() map[string]string {
	lib := std.Shared()
	out := map[string]string{}
	for name := range programStdModules {
		out[name] = lib.SourcePath(name)
	}
	return out
}
