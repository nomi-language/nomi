package analysis

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/syntheticextern"
)

// ProgramEdit replaces the whole text of one project file of a checked
// program.
type ProgramEdit struct {
	// Key is the file's module key ("shapes", "lib/geo").
	Key  string
	Text string
}

// CheckedProgram is one whole program checked as the front end checks it
// (frontend.Checker.Analyze): every project file the entry reaches is built,
// type-checked, has its application-field roots checked, and has its
// whole-file imports settled, and the entry then gets the coherence, iteration
// and concurrency checks. It is the state an incremental check starts from
// (DocumentManager.Recheck). Nothing writes it after CheckProgram returns it.
type CheckedProgram struct {
	// Root is the project root imports resolve against, and Entry the entry
	// file's module key.
	Root  string
	Entry string
	// Texts holds the text of every project file the build read, by module
	// key: the texts it was given, and any file it read from disk.
	Texts map[string]string
	// Files and Nodes are every project file the entry reaches, the entry
	// included and the stdlib excluded, by module key. The entry's nodes
	// include its synthesized derive and universal Debug impls.
	Files map[string]*FileAnalysis
	Nodes map[string][]ast.Node
	// Syntax holds each file's syntax errors, by module key.
	Syntax map[string][]parser.ParseError
	// Errors holds each file's diagnostics by module key, in the order the
	// front end reports them. The program's own (Program.Errors, coherence)
	// are with the entry's, where the front end reports them.
	Errors map[string][]TypeError
}

// Keys answers the program's module keys, the entry first and then the
// others in key order: the order the front end reports files in.
func (p *CheckedProgram) Keys() []string {
	keys := make([]string, 0, len(p.Files))
	for key := range p.Files {
		if key != p.Entry {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return append([]string{p.Entry}, keys...)
}

// Recheck answers the program prev becomes when edit replaces the text of
// one of its files. It is the seam an incremental check implements: its
// answer must be the program CheckProgram builds from the edited texts, fact
// for fact (TestIncrementalMatchesFull). Today it is that full check.
func (dm *DocumentManager) Recheck(prev *CheckedProgram, edit ProgramEdit) *CheckedProgram {
	texts := make(map[string]string, len(prev.Texts)+1)
	for key, text := range prev.Texts {
		texts[key] = text
	}
	texts[edit.Key] = edit.Text
	return dm.CheckProgram(prev.Root, prev.Entry, texts)
}

// CheckProgram checks the program whose entry is the file with module key
// entry under root. A project file's text is texts' when it has one, and
// otherwise the file on disk. Every file is parsed resiliently, as the
// language server parses an open document, and checked even when it has a
// syntax error.
func (dm *DocumentManager) CheckProgram(root, entry string, texts map[string]string) *CheckedProgram {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	dm.mu.RLock()
	primitives, modules, stdlibFAs := dm.primitives, dm.modules, dm.stdlibFAs
	dm.mu.RUnlock()

	p := &CheckedProgram{
		Root: root, Entry: entry,
		Texts:  make(map[string]string, len(texts)),
		Files:  map[string]*FileAnalysis{},
		Nodes:  map[string][]ast.Node{},
		Syntax: map[string][]parser.ParseError{},
		Errors: map[string][]TypeError{},
	}
	for key, text := range texts {
		p.Texts[key] = text
	}
	var mu sync.Mutex
	// text is the text of the file with module key, and whether there is one.
	text := func(key string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := p.Texts[key]; ok {
			return t, true
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(key)+".nomi"))
		if err != nil {
			return "", false
		}
		p.Texts[key] = string(data)
		return string(data), true
	}
	parse := func(key, src string) []ast.Node {
		nodes, errs, _ := parser.ParseResilient(lexer.Lex(src))
		mu.Lock()
		if len(errs) > 0 {
			p.Syntax[key] = errs
		}
		mu.Unlock()
		return nodes
	}

	entryText, _ := text(entry)
	nodes := parse(entry, entryText)
	externs := dm.syntheticExternsForRoot(root)
	entryPath := filepath.Join(root, filepath.FromSlash(entry)+".nomi")
	if injected, err := syntheticextern.Inject(nodes, externs, isFFIEntryPath(entryPath)); err == nil {
		nodes = injected
	}
	nodes, lowerErrs := LowerDerives(nodes)
	testNameErrs := CheckDuplicateTestNames(nodes)
	prog, _ := SynthesizeDerives(nodes)
	prog = SynthesizeUniversalDebug(prog)

	base := dm.makeLoader()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		if _, _, std := dm.stdlibLoad(projectRoot, modulePath); std || filepath.Clean(projectRoot) != filepath.Clean(root) || IsStdlibKey(key) {
			return base(projectRoot, modulePath)
		}
		if key == entry {
			return nodes, nil
		}
		src, ok := text(key)
		if !ok {
			return base(projectRoot, modulePath)
		}
		return syntheticextern.Inject(parse(key, src), dm.syntheticExternsForRoot(projectRoot), false)
	}
	entryFA, siblings, siblingNodes := BuildProjectFromEntryWithManifest(entryPath, prog, primitives, modules, stdlibFAs, root, loader, nil, entry, true)

	// What the front end runs, in its order (frontend.Checker.Analyze).
	errs := append([]TypeError{}, entryFA.TypeErrors...)
	errs = append(errs, entryFA.Program().Errors...)
	errs = append(errs, CheckTypes(entryFA, prog)...)
	p.Files[entry], p.Nodes[entry] = entryFA, prog
	program := []ProgramFile{{FA: entryFA, Nodes: prog}}
	sibKeys := make([]string, 0, len(siblings))
	for key := range siblings {
		sibKeys = append(sibKeys, key)
	}
	sort.Strings(sibKeys)
	var checked []string
	for _, key := range sibKeys {
		fa := siblings[key]
		if fa == nil {
			continue
		}
		if IsStdlibKey(key) && stdlibFAs[strings.TrimPrefix(key, "std/")] == fa {
			// The shared stdlib's analysis, which std.Load checked once.
			program = append(program, ProgramFile{FA: fa, Nodes: siblingNodes[key]})
			continue
		}
		p.Errors[key] = append(append([]TypeError{}, fa.TypeErrors...), CheckTypes(fa, siblingNodes[key])...)
		program = append(program, ProgramFile{FA: fa, Nodes: siblingNodes[key]})
		checked = append(checked, key)
		if !IsStdlibKey(key) {
			p.Files[key], p.Nodes[key] = fa, siblingNodes[key]
		}
	}
	errs = append(errs, CheckAppRoots(entryFA, prog)...)
	for _, key := range checked {
		p.Errors[key] = append(p.Errors[key], CheckAppRoots(siblings[key], siblingNodes[key])...)
	}
	implImportErrs := CheckImplImports(program)
	errs = append(errs, implImportErrs[entryFA]...)
	for _, key := range checked {
		p.Errors[key] = append(p.Errors[key], implImportErrs[siblings[key]]...)
	}
	errs = append(errs, FinalizeCoherence(entryFA)...)
	errs = append(errs, AnalyzeIterSensitivity(entryFA, prog)...)
	errs = append(errs, CheckConcurrentScope(entryFA, prog)...)
	errs = append(errs, CheckBootScope(entryFA, prog)...)
	errs = append(errs, CheckTaskLifetime(entryFA, prog)...)
	MarkTailCalls(prog)
	for _, key := range checked {
		MarkTailCalls(siblingNodes[key])
	}
	// The front end reports these before analysis; the language server
	// adds them to the entry's.
	errs = append(errs, lowerErrs...)
	p.Errors[entry] = append(errs, testNameErrs...)
	for _, key := range checked {
		if IsStdlibKey(key) {
			delete(p.Errors, key)
		}
	}
	return p
}
