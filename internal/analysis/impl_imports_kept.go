package analysis

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
)

// keptImplUses is CheckImplImports' implUses for one list of program files,
// kept up to date file by file: the impl functions each file's references
// reach and the (type, interface) pairs each file demands. CheckImplImports
// builds one for its call; a program keeps one across rechecks
// (checkImplImportsKept). A file's part is valid while the list holds the same
// analysis with the same body part (the analysis pointer and
// FileBodies.generation), as programDemands' is. When the files or their
// programs differ from the ones it was built over, it is built again.
type keptImplUses struct {
	// program is the analyses of the files CheckImplImports was given,
	// programs the programs they belong to with each one's files
	// (Program.Files) when it was built, and demanders every file whose
	// demands count: those of program and of every one of programs.
	program   []*FileAnalysis
	programs  []keptProgramFiles
	demanders []*FileAnalysis
	inProgram map[*FileAnalysis]bool
	// base is collectImplUses' base: the stdlib base of the files'
	// programs, whose files' references it does not read.
	base *StdBase
	// parts is each demander's part.
	parts map[*FileAnalysis]implUsePart
	// uses is the union of the parts, and pairs how many files demand each
	// pair uses.pairs holds.
	uses  implUses
	pairs map[implPair]int
}

// keptProgramFiles is a program and its files when a keptImplUses was
// built.
type keptProgramFiles struct {
	p     *Program
	files []*FileAnalysis
}

// implUsePart is one file's part of a keptImplUses.
type implUsePart struct {
	generation uint64
	// pairs is the pairs the file demands, when the base does not answer
	// for it, and decls the impl functions its references reach, each once,
	// when it is one of the files CheckImplImports was given.
	pairs []implPair
	decls []ast.Node
}

// checkImplImportsKept is CheckImplImports(program), reading again only the
// files whose bodies were rebuilt since p's last call with the same files.
func (p *Program) checkImplImportsKept(program []ProgramFile) map[*FileAnalysis][]TypeError {
	if !implImportsPending(program) {
		return nil
	}
	p.demandsMu.Lock()
	defer p.demandsMu.Unlock()
	if p.implUses == nil || !p.implUses.update(program) {
		p.implUses = newKeptImplUses(program)
	}
	return p.implUses.settle(program)
}

// settle decides every whole-file import of program's files against k's
// uses (unusedImports), records each decision on its program, and answers
// the unused-import errors, by file.
func (k *keptImplUses) settle(program []ProgramFile) map[*FileAnalysis][]TypeError {
	byScope := make(map[*Scope]*FileAnalysis, len(program))
	nodes := make(map[*FileAnalysis][]ast.Node, len(program))
	for _, f := range program {
		if f.FA != nil && f.FA.ModuleScope != nil {
			byScope[f.FA.ModuleScope] = f.FA
			nodes[f.FA] = f.Nodes
		}
	}
	target := func(s *Scope) *FileAnalysis { return byScope[s] }
	foreignOf := func(fa *FileAnalysis) ([]ForeignImplFact[ast.Node], bool) {
		if fa == nil {
			return nil, false
		}
		return foreignImplsOf(fa, nodes[fa], func(n ast.Node) ast.Node { return n }), true
	}
	var out map[*FileAnalysis][]TypeError
	for _, f := range program {
		if f.FA == nil {
			continue
		}
		imports := implImportsOf(f.FA, target)
		for i, unused := range unusedImports(imports, foreignOf, k.uses.decls, k.uses.demanded) {
			f.FA.Program().settleImplImport(f.FA.ImplImports[i], unused)
			if !unused {
				continue
			}
			if out == nil {
				out = make(map[*FileAnalysis][]TypeError)
			}
			out[f.FA] = append(out[f.FA], imports[i].Err)
		}
	}
	return out
}

// programsOf answers the programs program's files belong to, in the order
// they first appear, and the last of their files' programs' stdlib bases.
func programsOf(program []ProgramFile) ([]*Program, *StdBase) {
	var programs []*Program
	var base *StdBase
	for _, f := range program {
		if f.FA == nil {
			continue
		}
		p := f.FA.Program()
		if !slices.Contains(programs, p) {
			programs = append(programs, p)
		}
		if p.stdBase != nil {
			base = p.stdBase
		}
	}
	return programs, base
}

// newKeptImplUses reads every file of program, and of their programs: the
// demands of every one the stdlib base does not answer for, and the
// references of each of program's own the base does not own.
func newKeptImplUses(program []ProgramFile) *keptImplUses {
	programs, base := programsOf(program)
	k := &keptImplUses{
		base:      base,
		inProgram: map[*FileAnalysis]bool{},
		parts:     map[*FileAnalysis]implUsePart{},
		uses:      implUses{decls: make(map[ast.Node]map[*FileAnalysis]bool), pairs: make(map[implPair]bool)},
		pairs:     map[implPair]int{},
	}
	seen := map[*FileAnalysis]bool{}
	demand := func(fa *FileAnalysis) {
		if !seen[fa] {
			seen[fa] = true
			k.demanders = append(k.demanders, fa)
		}
	}
	for _, f := range program {
		if f.FA != nil {
			k.program = append(k.program, f.FA)
			k.inProgram[f.FA] = true
			demand(f.FA)
		}
	}
	for _, p := range programs {
		files := slices.Clone(p.Files())
		k.programs = append(k.programs, keptProgramFiles{p: p, files: files})
		for _, fa := range files {
			demand(fa)
		}
	}
	if base.ownsAll(k.demanders) {
		k.uses.base = base
	}
	for _, fa := range k.demanders {
		k.add(fa, k.read(fa))
	}
	return k
}

// update brings k up to date with program, reading again the files whose
// bodies were rebuilt, and answers false when it must be built again
// instead: program's files, their programs or those programs' files are
// not the ones k was built over.
func (k *keptImplUses) update(program []ProgramFile) bool {
	at := 0
	for _, f := range program {
		if f.FA == nil {
			continue
		}
		if at >= len(k.program) || k.program[at] != f.FA {
			return false
		}
		at++
	}
	if at != len(k.program) {
		return false
	}
	programs, base := programsOf(program)
	if base != k.base || len(programs) != len(k.programs) {
		return false
	}
	for i, p := range programs {
		if k.programs[i].p != p || !slices.Equal(k.programs[i].files, p.Files()) {
			return false
		}
	}
	for _, fa := range k.demanders {
		if k.parts[fa].generation != fa.generation {
			k.remove(fa)
			k.add(fa, k.read(fa))
		}
	}
	return true
}

// read answers fa's part.
func (k *keptImplUses) read(fa *FileAnalysis) implUsePart {
	part := implUsePart{generation: fa.generation}
	if !k.uses.base.Owns(fa) {
		noteStdlibWork(fa.Origin)
		for iface, byType := range fa.ImplManifest {
			for typeName := range byType {
				part.pairs = append(part.pairs, implPair{typeName: typeName, iface: iface})
			}
		}
	}
	if !k.inProgram[fa] || k.base.Owns(fa) {
		return part
	}
	noteStdlibWork(fa.Origin)
	seen := map[ast.Node]bool{}
	implUsesOf(fa, func(n ast.Node) {
		if !seen[n] {
			seen[n] = true
			part.decls = append(part.decls, n)
		}
	})
	return part
}

// add enters part as fa's in the union.
func (k *keptImplUses) add(fa *FileAnalysis, part implUsePart) {
	k.parts[fa] = part
	for _, pair := range part.pairs {
		k.pairs[pair]++
		k.uses.pairs[pair] = true
	}
	for _, n := range part.decls {
		k.uses.addDecl(n, fa)
	}
}

// remove takes fa's part out of the union.
func (k *keptImplUses) remove(fa *FileAnalysis) {
	part := k.parts[fa]
	for _, pair := range part.pairs {
		if k.pairs[pair]--; k.pairs[pair] == 0 {
			delete(k.pairs, pair)
			delete(k.uses.pairs, pair)
		}
	}
	for _, n := range part.decls {
		delete(k.uses.decls[n], fa)
		if len(k.uses.decls[n]) == 0 {
			delete(k.uses.decls, n)
		}
	}
	delete(k.parts, fa)
}
