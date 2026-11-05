package irbuild

// The per-file object harness.
//
// With NOMI_LINK_OBJECTS=perfile, every whole-program lowering of a program
// the front end checked from files is kept (Result.Keep), written and read
// back, and each of the program's project files is then checked against
// its imports' interfaces and lowered on its own against the kept objects
// of the others (LowerFile). The file's module must encode as the whole
// lowering's did, parts in one order (ir.Canonical); a miss, where
// LowerFile declines to lower the file on its own, is allowed, since its
// caller then lowers the whole program. The program the lowering answers
// is then the one a lowering of one file linked: of its first file other
// than the entry, or of the entry for a program of one file, so the
// expectation suites run what linking kept objects makes and compare it
// with the golden files. NOMI_PERFILE_REPORT=1 writes each miss on stderr.

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ir"
)

// perFileHarness reports whether lowerings relower each file on its own:
// asked for in-process (RelowerEachFile), or by NOMI_LINK_OBJECTS=perfile
// for a whole test run.
func perFileHarness() bool {
	return relowerEachFile.Load() > 0 || os.Getenv("NOMI_LINK_OBJECTS") == "perfile"
}

// relowerEachFile counts the callers that asked for it (RelowerEachFile).
var relowerEachFile atomic.Int32

// RelowerEachFile makes every lowering relower each of its files on its
// own and answer the program a file's lowering links, until the returned
// function is called.
func RelowerEachFile() (restore func()) {
	relowerEachFile.Add(1)
	return func() { relowerEachFile.Add(-1) }
}

// Interfaces answers the program's files as interfaces, against which each
// can be checked on its own, or nil for a program the front end did not
// check from files.
func (p *Program) Interfaces() *analysis.ProgramInterfaces {
	if p.interfaces == nil {
		return nil
	}
	return p.interfaces()
}

// FileProgram is the Program LowerFile lowers for the file with module key
// key at path, from fc, its check against interfaces: the file first, then
// the other files its check read. root and hasTests are the program's.
func FileProgram(fc *analysis.FileCheck, key, path, root string, hasTests bool) *Program {
	p := &Program{Root: root, HasTests: hasTests}
	p.Modules = append(p.Modules, Module{Path: path, Name: key, Nodes: fc.Nodes, FA: fc.FA})
	others := make([]string, 0, len(fc.Files))
	for k := range fc.Files {
		others = append(others, k)
	}
	sort.Strings(others)
	for _, k := range others {
		fa := fc.Files[k]
		path := fa.FilePath
		if path == "" {
			path = k + ".nomi"
		}
		analysis.MarkTailCalls(fc.FileNodes[k])
		p.Modules = append(p.Modules, Module{Path: path, Name: k, Nodes: fc.FileNodes[k], FA: fa, bodies: slices.Contains(fc.Bodies, k)})
	}
	return p
}

// relowerEachFile is the harness over r, the whole lowering of p.
func (r *Result) relowerEachFile(p *Program) (err error) {
	if p.interfaces == nil || p.objects != nil || r.kept == nil {
		return nil
	}
	kept, kerr := r.Keep()
	if kerr != nil {
		return nil
	}
	kept, err = DecodeKeptLowering(kept.Encode())
	if err != nil {
		return fmt.Errorf("irbuild: a kept lowering does not read back: %w", err)
	}
	// Each file's lowering below is a GenerateIR of its own, with a census
	// of its own and no hooks, so nothing it declines reaches p's.
	pi := p.interfaces()
	if pi == nil {
		return nil
	}
	whole := map[string]*ir.Module{}
	for _, m := range r.IR {
		whole[m.Name()] = m
	}
	var chosen *Result
	func() {
		for _, u := range kept.Units {
			if u.Decls == nil {
				continue
			}
			var fc *analysis.FileCheck
			for _, key := range pi.Keys() {
				if key == u.Key {
					fc = pi.Check(key)
				}
			}
			if fc == nil {
				continue
			}
			for _, m := range p.Modules {
				if m.Path == u.Path && m.FA != nil && analysis.BodySummary(m.FA, m.Nodes) != analysis.BodySummary(fc.FA, fc.Nodes) {
					err = fmt.Errorf("irbuild: %s checked against interfaces summarizes its bodies differently from the program's check", u.Path)
					return
				}
			}
			fp := FileProgram(fc, u.Key, u.Path, p.Root, p.HasTests)
			fp.VirtualFiles = p.VirtualFiles
			res, lerr := LowerFile(fp, kept, false)
			var fm *FileMiss
			if errors.As(lerr, &fm) {
				if os.Getenv("NOMI_PERFILE_REPORT") == "1" {
					fmt.Fprintf(os.Stderr, "per-file miss: %s: %s\n", u.Path, fm.Why)
				}
				continue
			}
			if lerr != nil {
				err = lerr
				return
			}
			if derr := sameModule(whole[u.Path], irModuleNamed(res.IR, u.Path)); derr != nil {
				err = fmt.Errorf("irbuild: %s lowered on its own differs from the whole program's lowering: %w", u.Path, derr)
				return
			}
			if lost := lostDefinitions(r.IRModules(), res.IRModules()); lost != "" {
				err = fmt.Errorf("irbuild: the program %s lowered on its own links lacks %s, which the whole lowering defines", u.Path, lost)
				return
			}
			if split := splitDefinitions(res.IRModules()); split != "" {
				err = fmt.Errorf("irbuild: the program %s lowered on its own links names %s by a symbol no module defines, though one defines its name", u.Path, split)
				return
			}
			if chosen == nil || u.Path != kept.EntryPath && chosen.file != nil && kept.Units[chosen.file.unit].Path == kept.EntryPath {
				chosen = res
			}
		}
	}()
	if err != nil || chosen == nil {
		return err
	}
	r.IR, r.irLibraries, r.instMods, r.linkedMods = chosen.IR, chosen.irLibraries, chosen.instMods, chosen.linkedMods
	return nil
}

func irModuleNamed(mods []*ir.Module, name string) *ir.Module {
	for _, m := range mods {
		if m.Name() == name {
			return m
		}
	}
	return nil
}

// sameModule reports how two lowerings of one module differ, part by part
// (ir.Parts).
func sameModule(a, b *ir.Module) error {
	if a == nil || b == nil {
		if a != b && (a == nil || !a.Empty()) && (b == nil || !b.Empty()) {
			return fmt.Errorf("one lowering retained the module and the other did not")
		}
		return nil
	}
	pa, err := ir.Parts(a)
	if err != nil {
		return err
	}
	pb, err := ir.Parts(b)
	if err != nil {
		return err
	}
	var diff []string
	for k, v := range pa {
		w, ok := pb[k]
		switch {
		case !ok:
			diff = append(diff, "only whole: "+k)
		case v != w:
			diff = append(diff, "differs: "+k)
		}
	}
	for k := range pb {
		if _, ok := pa[k]; !ok {
			diff = append(diff, "only per file: "+k)
		}
	}
	if len(diff) == 0 {
		return nil
	}
	sort.Strings(diff)
	return errors.New(strings.Join(diff, "; "))
}

// lostDefinitions names a linkage name the modules of whole define and
// those of linked do not, or "".
func lostDefinitions(whole, linked []*ir.Module) string {
	have := map[string]bool{}
	for _, m := range linked {
		for _, l := range definedLinks(m) {
			have[l] = true
		}
	}
	var lost []string
	for _, m := range whole {
		for _, l := range definedLinks(m) {
			if !have[l] {
				lost = append(lost, l)
			}
		}
	}
	sort.Strings(lost)
	if len(lost) > 3 {
		lost = append(lost[:3], fmt.Sprintf("and %d more", len(lost)-3))
	}
	return strings.Join(lost, ", ")
}

// splitDefinitions names a linkage name some module defines that a module
// names by another symbol, which no module defines, or "".
func splitDefinitions(mods []*ir.Module) string {
	defined := map[*ir.Symbol]bool{}
	named := map[string]bool{}
	for _, m := range mods {
		for _, f := range m.Funcs() {
			if f.Sym() != nil {
				if defined[f.Sym()] {
					return f.Sym().Link() + " (defined twice, again in " + m.Name() + ")"
				}
				defined[f.Sym()] = true
				named[f.Sym().Link()] = true
			}
		}
		for _, c := range m.Cells() {
			defined[c.Sym()] = true
			named[c.Sym().Link()] = true
		}
	}
	for _, m := range mods {
		for _, s := range ir.ModuleSymbols(m) {
			if s.Link() != "" && named[s.Link()] && !defined[s] {
				return s.Link() + " (in " + m.Name() + ")"
			}
		}
	}
	return ""
}
