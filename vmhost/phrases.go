package vmhost

import (
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdphraseformat"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

// phraseTables are the phrase and noun tables the program carries, which
// a catalog loaded at run time is checked against: those of the functions
// a built image of it would hold, reached from `main`, its boot and its
// exported functions (vm.PruneImage's walk), so `nomi run` and a built
// program accept the same entries. A program with neither main nor
// exports, a test file, carries every table its files declare.
func (p *Program) phraseTables() (phrases, nouns []string) {
	p.tablesOnce.Do(func() {
		mods := p.res.IRModules()
		var roots []*ir.Func
		if p.entry != nil && p.HasMain() {
			if mainFn := p.entryFunc("main"); mainFn != nil {
				roots = vm.MainRoots(p.entry, mainFn)
			}
		}
		for _, e := range p.res.Exports {
			if e.Func != nil {
				roots = append(roots, e.Func)
			}
		}
		funcs := vm.ModuleFuncs(mods)
		if len(roots) > 0 {
			var boots []*ir.Symbol
			if p.entry != nil && p.entry.Boot() != nil {
				boots = append(boots, p.entry.Boot())
			}
			funcs = vm.ReachedFuncs(p.entry, mods, roots, boots)
		}
		p.tables[0], p.tables[1] = vm.PhraseTables(mods, funcs, stdPhrase)
	})
	return p.tables[0], p.tables[1]
}

// stdPhrase reports whether a module, by its name, is std/phrase: the
// module a built image names `std/phrase.nomi` (imageName).
func stdPhrase(module string) bool {
	name, ok := std.ImageName(module)
	return ok && name == vm.PhraseModule
}

// phrases is a fresh machine's phrases: the program's tables, and no
// locale files to load, since a program run in process carries every
// locale.
func (p *Program) phrases() *stdphraseformat.Runtime {
	return stdphraseformat.NewRuntime(p.phraseTables, nil)
}
