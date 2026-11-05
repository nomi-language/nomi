package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Lowering a program that grows one file at a time, as a REPL session's
// does: each new file is lowered on its own against the modules the files
// before it lowered, which stay in memory and are never lowered again, and
// its modules are linked into the machine that runs the earlier ones
// (vm.Machine.Extend).
//
// It is LowerFile's lowering (perfile.go) with what LowerFile reads from a
// kept whole lowering read from the session instead. Every earlier file's
// gen is `fromObjects`: declared from the interface the new file's check
// read, its bodies the earlier modules', resolved by linkage name. What
// LowerFile answers a miss for, the session takes as it comes: a file
// that holds erased an instance no earlier file did, or boxes an enum,
// adds it to the session's sets for the files after it, since nothing
// before it is lowered again; and its declines are its own, reported as
// any program's are.

// SessionObjects are what a session's earlier files lowered, which the
// next file is lowered against.
type SessionObjects struct {
	// defined is every linkage name an earlier module names, to its
	// symbol, and own the linkage names they define.
	defined map[string]*ir.Symbol
	own     map[string]bool
	mods    []*ir.Module
	// funcs are the earlier modules' functions by linkage name.
	funcs map[string]*ir.Func
	// erased and boxed are the registry's program-wide sets, grown by
	// each file, and synthRefused names each synthesized impl block an
	// earlier file's speculation refused.
	erased, boxed, synthRefused map[string]bool
	// Boots says the next file runs under a boot an earlier file declared
	// when it declares none.
	Boots bool
}

// NewSessionObjects opens an empty session.
func NewSessionObjects() *SessionObjects {
	return &SessionObjects{defined: map[string]*ir.Symbol{}, own: map[string]bool{}, funcs: map[string]*ir.Func{},
		erased: map[string]bool{}, boxed: map[string]bool{}, synthRefused: map[string]bool{}}
}

// Func is the function an earlier module defines under link, or nil.
func (s *SessionObjects) Func(link string) *ir.Func { return s.funcs[link] }

// sessionModules are the modules a session lowering made: the file's own
// and the instances it asked for.
func sessionModules(res *Result) []*ir.Module {
	return append(append([]*ir.Module(nil), res.IR...), res.instMods...)
}

// SessionFunctions is how many functions res, a session lowering, made:
// its file's module's and the instances it asked for.
func SessionFunctions(res *Result) int {
	n := 0
	for _, m := range sessionModules(res) {
		n += len(m.Funcs())
	}
	return n
}

// LowerSessionFile lowers p's first module, the session's newest file, as
// the program's entry, against s: p's other modules are the earlier files
// its check against interfaces read (analysis.FileCheck.Files), whose
// bodies are s's. The Result's modules are what the file adds: its own
// module, the std instances it asked for and the shared std modules, which
// a machine that already links them passes over. Commit adds them to s
// once they run.
func LowerSessionFile(p *Program, s *SessionObjects) (*Result, error) {
	objects := &linkedStd{defined: s.defined, own: s.own, mods: s.mods, synthRefused: map[*ast.ImplBlock]bool{}}
	if len(s.synthRefused) > 0 {
		for n, name := range programLinkNames(p) {
			if b, ok := n.(*ast.ImplBlock); ok && s.synthRefused[name.String()] {
				objects.synthRefused[b] = true
			}
		}
	}
	p.objects = objects
	p.perFile = &perFileState{isEntry: true, entryBoots: s.Boots, erased: sortedSet(s.erased), boxed: sortedSet(s.boxed)}
	res, _, err := GenerateIR(p)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Commit adds what res, a lowering LowerSessionFile made against s, holds
// to s, for the files after it.
func (s *SessionObjects) Commit(res *Result) {
	for _, m := range sessionModules(res) {
		s.mods = append(s.mods, m)
		for _, sym := range ir.ModuleSymbols(m) {
			if link := sym.Link(); link != "" {
				if _, known := s.defined[link]; !known {
					s.defined[link] = sym
				}
			}
		}
		// A module's own definitions name their symbols.
		for _, f := range m.Funcs() {
			if f.Sym() != nil && f.Sym().Link() != "" {
				s.defined[f.Sym().Link()] = f.Sym()
				s.own[f.Sym().Link()] = true
				s.funcs[f.Sym().Link()] = f
			}
		}
		for _, c := range m.Cells() {
			if c.Sym().Link() != "" {
				s.defined[c.Sym().Link()] = c.Sym()
				s.own[c.Sym().Link()] = true
			}
		}
	}
	for _, e := range res.perFileTables.erased {
		s.erased[e] = true
	}
	for _, b := range res.perFileTables.boxed {
		s.boxed[b] = true
	}
	for _, r := range res.perFileTables.synthRefused {
		s.synthRefused[r] = true
	}
}

// SessionProgram is the program LowerSessionFile lowers for the file with
// key at path that fc checked: the file first, then the files its check
// read.
func SessionProgram(fc *analysis.FileCheck, key, path string) *Program {
	return FileProgram(fc, key, path, "", false)
}
