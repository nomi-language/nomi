package analysis

import (
	"os"
	"path/filepath"
	"slices"
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
// (DocumentManager.Recheck).
//
// Recheck consumes the program it is given: when it rebuilds only an edited
// file's bodies, its answer is made of the given program's analyses, nodes
// and symbols, rewritten in place, so the given program is no longer the
// program before the edit and must not be read or rechecked again. A
// program is written only by the Recheck that consumes it, and by the
// test-only RebuildBodiesForTest, which rebuilds one file in place.
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
	// checks holds the diagnostics Errors is assembled from.
	checks *programChecks
	// fingerprints holds each project file's interface fingerprint, by
	// module key, for a text the parser read without error.
	fingerprints map[string]string
	// externs reports that the root declared Go bindings, which the build
	// injected into the files' nodes.
	externs bool
	// consumed is set once a Recheck has taken the program's analyses for
	// its answer.
	consumed bool
}

// Consumed reports whether a Recheck has consumed the program.
func (p *CheckedProgram) Consumed() bool {
	return p.consumed
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
// one of its files. Its answer is the program CheckProgram builds from the
// edited texts, fact for fact (TestIncrementalMatchesFull). An edit that
// leaves the file's interface as it was (interfaceFingerprint) rebuilds that
// file's bodies alone (rebuildEditedBodies); any other is a full check.
//
// Recheck consumes prev (see CheckedProgram): the caller must not read prev
// or recheck it again afterwards. The caller serializes Rechecks of one
// program, and keeps readers of prev's analyses away while one runs.
func (dm *DocumentManager) Recheck(prev *CheckedProgram, edit ProgramEdit) *CheckedProgram {
	next, _ := dm.RecheckWithOutcome(prev, edit)
	return next
}

// RecheckWithOutcome is Recheck, also answering which way it went.
func (dm *DocumentManager) RecheckWithOutcome(prev *CheckedProgram, edit ProgramEdit) (*CheckedProgram, RecheckOutcome) {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	return dm.recheckLocked(prev, edit)
}

// recheckLocked is RecheckWithOutcome for a caller that holds analyzeMu.
func (dm *DocumentManager) recheckLocked(prev *CheckedProgram, edit ProgramEdit) (*CheckedProgram, RecheckOutcome) {
	if prev.consumed {
		panic("analysis: Recheck of a program an earlier Recheck consumed")
	}
	if text, ok := prev.Texts[edit.Key]; ok && text == edit.Text {
		return prev, RecheckOutcome{Path: RecheckUnchanged}
	}
	next, reason := dm.rebuildEditedBodies(prev, edit)
	if next != nil {
		return next, RecheckOutcome{Path: RecheckBodies}
	}
	texts := make(map[string]string, len(prev.Texts)+1)
	for key, text := range prev.Texts {
		texts[key] = text
	}
	texts[edit.Key] = edit.Text
	prev.consumed = true
	return dm.checkProgramLocked(prev.Root, prev.Entry, texts), RecheckOutcome{Path: RecheckFull, Reason: reason}
}

// CheckProgram checks the program whose entry is the file with module key
// entry under root. A project file's text is texts' when it has one, and
// otherwise the file on disk. Every file is parsed resiliently, as the
// language server parses an open document, and checked even when it has a
// syntax error.
func (dm *DocumentManager) CheckProgram(root, entry string, texts map[string]string) *CheckedProgram {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	return dm.checkProgramLocked(root, entry, texts)
}

// checkProgramLocked is CheckProgram for a caller that holds analyzeMu.
func (dm *DocumentManager) checkProgramLocked(root, entry string, texts map[string]string) *CheckedProgram {
	dm.mu.RLock()
	primitives, modules, stdlibFAs := dm.primitives, dm.modules, dm.stdlibFAs
	dm.mu.RUnlock()

	p := &CheckedProgram{
		Root: root, Entry: entry,
		Texts:        make(map[string]string, len(texts)),
		Files:        map[string]*FileAnalysis{},
		Nodes:        map[string][]ast.Node{},
		Syntax:       map[string][]parser.ParseError{},
		Errors:       map[string][]TypeError{},
		fingerprints: map[string]string{},
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
		fingerprint := ""
		if len(errs) == 0 {
			fingerprint = interfaceFingerprint(nodes)
		}
		mu.Lock()
		if len(errs) > 0 {
			p.Syntax[key] = errs
		} else {
			p.fingerprints[key] = fingerprint
		}
		mu.Unlock()
		return nodes
	}

	entryText, _ := text(entry)
	nodes := parse(entry, entryText)
	externs := dm.syntheticExternsForRoot(root)
	p.externs = len(externs) > 0
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
	c := &programChecks{
		entry:   entryFA,
		build:   map[string][]TypeError{entry: slices.Clone(entryFA.TypeErrors)},
		check:   map[string][]TypeError{},
		program: []ProgramFile{{FA: entryFA, Nodes: prog}},
	}
	c.check[entry] = CheckTypes(entryFA, prog)
	p.Files[entry], p.Nodes[entry] = entryFA, prog
	sibKeys := make([]string, 0, len(siblings))
	for key := range siblings {
		sibKeys = append(sibKeys, key)
	}
	sort.Strings(sibKeys)
	for _, key := range sibKeys {
		fa := siblings[key]
		if fa == nil {
			continue
		}
		if IsStdlibKey(key) && stdlibFAs[strings.TrimPrefix(key, "std/")] == fa {
			// The shared stdlib's analysis, which std.Load checked once.
			c.program = append(c.program, ProgramFile{FA: fa, Nodes: siblingNodes[key]})
			continue
		}
		c.build[key] = slices.Clone(fa.TypeErrors)
		c.check[key] = CheckTypes(fa, siblingNodes[key])
		c.program = append(c.program, ProgramFile{FA: fa, Nodes: siblingNodes[key]})
		c.checked = append(c.checked, key)
		if !IsStdlibKey(key) {
			p.Files[key], p.Nodes[key] = fa, siblingNodes[key]
		}
	}
	c.siblings, c.siblingNodes = siblings, siblingNodes
	p.checks = c
	c.reduce(p)
	c.checkEntry(p)
	MarkTailCalls(prog)
	for _, key := range c.checked {
		MarkTailCalls(siblingNodes[key])
	}
	// The front end reports these before analysis; the language server
	// adds them to the entry's.
	c.frontEnd = append(lowerErrs, testNameErrs...)
	c.assemble(p)
	return p
}

// programChecks holds the diagnostics CheckProgram assembles each file's
// from, by the check that found them, so that the checks over one file's
// bodies can run again (RebuildBodiesForTest).
type programChecks struct {
	entry *FileAnalysis
	// checked holds the keys of the files other than the entry that
	// CheckTypes ran on, in key order, siblings and siblingNodes every file
	// the build reached but the entry, and program every file of the
	// program with its nodes, the entry first.
	checked      []string
	siblings     map[string]*FileAnalysis
	siblingNodes map[string][]ast.Node
	program      []ProgramFile
	// Each checked file's build errors, CheckTypes errors, application-field
	// root errors and unused whole-file imports, by key.
	build, check, appRoots, implImports map[string][]TypeError
	// The program's coherence errors and the entry's own checks: iteration
	// sensitivity, then the concurrency, boot-scope and task-lifetime rules.
	coherence, entryChecks []TypeError
	// frontEnd holds the derive-lowering and duplicate test name errors the
	// front end reports before analysis.
	frontEnd []TypeError
}

// fileOf answers the analysis and nodes of the checked file with key.
func (c *programChecks) fileOf(p *CheckedProgram, key string) (*FileAnalysis, []ast.Node) {
	if key == p.Entry {
		return c.entry, p.Nodes[p.Entry]
	}
	return c.siblings[key], c.siblingNodes[key]
}

// reduce runs what the program reduces over every checked file's bodies:
// each file's application-field roots, the whole-file imports nothing
// names, and coherence.
func (c *programChecks) reduce(p *CheckedProgram) {
	c.appRoots = map[string][]TypeError{}
	for _, key := range append([]string{p.Entry}, c.checked...) {
		fa, nodes := c.fileOf(p, key)
		c.appRoots[key] = CheckAppRoots(fa, nodes)
	}
	unused := CheckImplImports(c.program)
	c.implImports = map[string][]TypeError{}
	for _, key := range append([]string{p.Entry}, c.checked...) {
		fa, _ := c.fileOf(p, key)
		c.implImports[key] = unused[fa]
	}
	c.coherence = FinalizeCoherence(c.entry)
}

// checkEntry runs the entry's own checks over its bodies.
func (c *programChecks) checkEntry(p *CheckedProgram) {
	nodes := p.Nodes[p.Entry]
	c.entryChecks = AnalyzeIterSensitivity(c.entry, nodes)
	c.entryChecks = append(c.entryChecks, CheckConcurrentScope(c.entry, nodes)...)
	c.entryChecks = append(c.entryChecks, CheckBootScope(c.entry, nodes)...)
	c.entryChecks = append(c.entryChecks, CheckTaskLifetime(c.entry, nodes)...)
}

// assemble sets p.Errors from the checks, in the order the front end reports
// them.
func (c *programChecks) assemble(p *CheckedProgram) {
	p.Errors = map[string][]TypeError{}
	errs := slices.Clone(c.build[p.Entry])
	errs = append(errs, c.entry.Program().Errors...)
	errs = append(errs, c.check[p.Entry]...)
	errs = append(errs, c.appRoots[p.Entry]...)
	errs = append(errs, c.implImports[p.Entry]...)
	errs = append(errs, c.coherence...)
	errs = append(errs, c.entryChecks...)
	p.Errors[p.Entry] = append(errs, c.frontEnd...)
	for _, key := range c.checked {
		if IsStdlibKey(key) {
			continue
		}
		errs := slices.Clone(c.build[key])
		errs = append(errs, c.check[key]...)
		errs = append(errs, c.appRoots[key]...)
		p.Errors[key] = append(errs, c.implImports[key]...)
	}
}
