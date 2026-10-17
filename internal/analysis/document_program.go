package analysis

import (
	"maps"
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
	// implFingerprints holds each such file's impls fingerprint
	// (implsFingerprint), by module key.
	implFingerprints map[string]string
	// externs reports that the root declared Go bindings, which the build
	// injected into the files' nodes.
	externs bool
	// damaged holds the declarations the resilient parse repaired in each
	// file, by module key (parser.ParseResilient).
	damaged map[string][]parser.Span
	// reachesEntry reports that a file of the program imports the entry,
	// and unread that the build loaded a file whose text Texts does not
	// hold: one of another module, or one no text and no file answered.
	reachesEntry, unread bool
	// entryOwn is how many of the entry's nodes are its declarations, as
	// lowered, before the impls synthesis appends.
	entryOwn int
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
// leaves the file's interface as it was (interfaceFingerprint), and the type
// of each of its unannotated `once` bindings, rebuilds that file's bodies
// alone (rebuildEditedBodies). Any other checks the file and the files that
// import it afresh (recheckDependents), or, when that would not be sound,
// the whole program.
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
	return dm.RecheckEdits(prev, []ProgramEdit{edit})
}

// RecheckEdits is RecheckWithOutcome for edits to several files at once, as
// after a save of every file or a checkout: the program after all of them.
// Each file appears in edits at most once.
func (dm *DocumentManager) RecheckEdits(prev *CheckedProgram, edits []ProgramEdit) (*CheckedProgram, RecheckOutcome) {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	return dm.recheckLocked(prev, edits)
}

// recheckLocked is RecheckEdits for a caller that holds analyzeMu.
func (dm *DocumentManager) recheckLocked(prev *CheckedProgram, edits []ProgramEdit) (*CheckedProgram, RecheckOutcome) {
	if prev.consumed {
		panic("analysis: Recheck of a program an earlier Recheck consumed")
	}
	var changed []ProgramEdit
	for _, e := range edits {
		if text, ok := prev.Texts[e.Key]; !ok || text != e.Text {
			changed = append(changed, e)
		}
	}
	if len(changed) == 0 {
		return prev, RecheckOutcome{Path: RecheckUnchanged}
	}
	parsed := parseEdits(changed)
	next, outcome := dm.recheckIncremental(prev, changed, parsed)
	if next != nil {
		return next, outcome
	}
	texts := maps.Clone(prev.Texts)
	for _, e := range changed {
		texts[e.Key] = e.Text
	}
	prev.consumed = true
	return dm.checkProgramFrom(prev.Root, prev.Entry, texts, nil, nil, parsed), outcome
}

// parseEdits parses each edited text once, for whichever way of
// rechecking answers.
func parseEdits(edits []ProgramEdit) map[string]*parsedFile {
	parsed := make(map[string]*parsedFile, len(edits))
	for _, e := range edits {
		parsed[e.Key] = parseFile(e.Text)
	}
	return parsed
}

// recheckIncremental answers the program after edits, each of which changes
// its file's text, without checking every file: by rebuilding the edited
// files' bodies (rebuildEditedBodies), or else by checking them and the
// files that import them afresh (recheckDependents). It answers nil with
// the outcome of a full check, and the reason one is needed, when neither
// can answer; prev is then consumed exactly when the bodies were rebuilt
// in place first. parsed holds the edited texts parsed (parseEdits); what
// is left of it after a nil answer is a full check's to take.
func (dm *DocumentManager) recheckIncremental(prev *CheckedProgram, edits []ProgramEdit, parsed map[string]*parsedFile) (*CheckedProgram, RecheckOutcome) {
	next, reason := dm.rebuildEditedBodies(prev, edits, parsed)
	if next != nil {
		return next, RecheckOutcome{Path: RecheckBodies}
	}
	// When prev is consumed, the bodies were rebuilt and an unannotated
	// `once` may have a new type, which the files that import its file
	// read. The graft took the parsed edited files from parsed, and they
	// are parsed again.
	next, why := dm.recheckDependents(prev, edits, parsed)
	if next != nil {
		return next, RecheckOutcome{Path: RecheckDependents, Reason: reason}
	}
	return nil, RecheckOutcome{Path: RecheckFull, Reason: why}
}

// parsedFile is one text parsed as CheckProgram parses a project file, with
// its fingerprints when it parsed without error.
type parsedFile struct {
	text                         string
	nodes                        []ast.Node
	errs                         []parser.ParseError
	damaged                      []parser.Span
	fingerprint, implFingerprint string
}

// parseFile parses text resiliently.
func parseFile(text string) *parsedFile {
	nodes, errs, damaged := parser.ParseResilient(lexer.Lex(text))
	pf := &parsedFile{text: text, nodes: nodes, errs: errs, damaged: damaged}
	if len(errs) == 0 {
		pf.fingerprint, pf.implFingerprint = interfaceFingerprint(nodes), implsFingerprint(nodes)
	}
	return pf
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
	return dm.checkProgramFrom(root, entry, texts, nil, nil, nil)
}

// checkProgramFrom is checkProgramLocked that takes the analyses of the
// files keep names from from, a program checked earlier from the same
// root and entry, as they are (reusedFile): it neither parses, builds nor
// checks them. Every other file is parsed, built and checked afresh, and the
// program's reductions run over every file. The caller has made sure that
// nothing a kept file's analysis holds depends on a file that changed
// (recheckDependents), and that the root declares no Go bindings, which a
// program with kept files does not look for again. With keep nil it is a
// check of every file. parsed holds texts already parsed, by module key,
// each of which the check takes instead of parsing its text again; nothing
// else may hold their nodes.
func (dm *DocumentManager) checkProgramFrom(root, entry string, texts map[string]string, from *CheckedProgram, keep map[string]bool, parsed map[string]*parsedFile) *CheckedProgram {
	dm.mu.RLock()
	primitives, modules, stdlibFAs := dm.primitives, dm.modules, dm.stdlibFAs
	dm.mu.RUnlock()

	p := &CheckedProgram{
		Root: root, Entry: entry,
		Texts:            make(map[string]string, len(texts)),
		Files:            map[string]*FileAnalysis{},
		Nodes:            map[string][]ast.Node{},
		Syntax:           map[string][]parser.ParseError{},
		Errors:           map[string][]TypeError{},
		fingerprints:     map[string]string{},
		implFingerprints: map[string]string{},
	}
	for key, text := range texts {
		p.Texts[key] = text
	}
	var reused map[string]reusedFile
	var onces *OnceTable
	keptFAs := map[*FileAnalysis]bool{}
	revision := 0
	if len(keep) > 0 {
		reused = make(map[string]reusedFile, len(keep))
		for key := range keep {
			reused[key] = reusedFile{fa: from.Files[key], nodes: from.Nodes[key]}
			keptFAs[from.Files[key]] = true
			if errs := from.Syntax[key]; len(errs) > 0 {
				p.Syntax[key] = errs
			}
			if f, ok := from.fingerprints[key]; ok {
				p.fingerprints[key] = f
			}
			if f, ok := from.implFingerprints[key]; ok {
				p.implFingerprints[key] = f
			}
			if d := from.damaged[key]; len(d) > 0 {
				if p.damaged == nil {
					p.damaged = map[string][]parser.Span{}
				}
				p.damaged[key] = d
			}
		}
		prog := from.Files[from.Entry].Program()
		onces, revision = prog.Onces, prog.revision
		funcs := map[*FileAnalysis][]*ast.FuncDef{}
		for fn, fa := range prog.ScopedFunctionFiles {
			if keptFAs[fa] {
				funcs[fa] = append(funcs[fa], fn)
			}
		}
		for key, r := range reused {
			r.funcs = funcs[r.fa]
			reused[key] = r
		}
	}
	var mu sync.Mutex
	// text is the text of the file with module key, and whether there is one.
	text := func(key string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := p.Texts[key]; ok {
			return t, true
		}
		data, err := dm.readProjectFile(filepath.Join(root, filepath.FromSlash(key)+".nomi"))
		if err != nil {
			return "", false
		}
		p.Texts[key] = data
		return data, true
	}
	parse := func(key, src string) []ast.Node {
		mu.Lock()
		pf := parsed[key]
		delete(parsed, key)
		mu.Unlock()
		if pf == nil || pf.text != src {
			pf = parseFile(src)
		}
		mu.Lock()
		if len(pf.errs) > 0 {
			p.Syntax[key] = pf.errs
		} else {
			p.fingerprints[key] = pf.fingerprint
			p.implFingerprints[key] = pf.implFingerprint
		}
		if len(pf.damaged) > 0 {
			if p.damaged == nil {
				p.damaged = map[string][]parser.Span{}
			}
			p.damaged[key] = pf.damaged
		}
		mu.Unlock()
		return pf.nodes
	}

	entryText, _ := text(entry)
	nodes := parse(entry, entryText)
	var externs []syntheticextern.Decl
	if reused == nil {
		externs = dm.syntheticExternsForRoot(root)
	}
	p.externs = len(externs) > 0
	entryPath := filepath.Join(root, filepath.FromSlash(entry)+".nomi")
	if injected, err := syntheticextern.Inject(nodes, externs, isFFIEntryPath(entryPath)); err == nil {
		nodes = injected
	}
	nodes, lowerErrs := LowerDerives(nodes)
	p.entryOwn = len(nodes)
	testNameErrs := CheckDuplicateTestNames(nodes)
	prog, _ := SynthesizeDerives(nodes)
	prog = SynthesizeUniversalDebug(prog)

	base := dm.makeLoader()
	// elsewhere loads a file whose text Texts does not hold, and notes it
	// when there is one.
	elsewhere := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		nodes, err := base(projectRoot, modulePath)
		if err == nil {
			mu.Lock()
			p.unread = true
			mu.Unlock()
		}
		return nodes, err
	}
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		if _, _, std := dm.stdlibLoad(projectRoot, modulePath); std || IsStdlibKey(key) {
			return base(projectRoot, modulePath)
		}
		if filepath.Clean(projectRoot) != filepath.Clean(root) {
			// A file of another module.
			return elsewhere(projectRoot, modulePath)
		}
		if key == entry {
			mu.Lock()
			p.reachesEntry = true
			mu.Unlock()
			return nodes, nil
		}
		src, ok := text(key)
		if !ok {
			return elsewhere(projectRoot, modulePath)
		}
		// A file of root: its bindings are the entry's, looked up once.
		return syntheticextern.Inject(parse(key, src), externs, false)
	}
	entryFA, siblings, siblingNodes := buildProjectWithCache(prog, primitives, modules, stdlibFAs, root, entryPath, entry, true, loader, nil, reused)
	if reused != nil {
		// The kept files' `once` bindings keep the types the earlier check
		// inferred, and so do the cycles their values read themselves
		// through: none of them reads a file that changed. The program's
		// inference phase infers the others'.
		now := entryFA.Program()
		for n, site := range onces.sites {
			if site.fa == nil || !keptFAs[site.fa] {
				continue
			}
			if onces.checked[n] {
				now.Onces.checked[n] = true
			}
			if cycle := onces.cycles[n]; cycle != nil {
				now.Onces.cycles[n] = cycle
			}
			if reads := onces.reads[n]; reads != nil {
				if now.Onces.reads == nil {
					now.Onces.reads = map[*ast.OnceBinding][]*ast.OnceBinding{}
				}
				now.Onces.reads[n] = reads
			}
		}
		// A cache of facts read from a kept analysis holds the analysis and
		// its revision (FileAnalysis.Revision), and its program is now this.
		now.revision = revision + 1
	}

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
		if keep[key] {
			// Built and checked by the earlier check, whose diagnostics hold.
			c.build[key], c.check[key] = from.checks.build[key], from.checks.check[key]
		} else {
			c.build[key] = slices.Clone(fa.TypeErrors)
			c.check[key] = CheckTypes(fa, siblingNodes[key])
		}
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
		if !keep[key] {
			MarkTailCalls(siblingNodes[key])
		}
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
	// appRootsFrom is what each file's appRoots entry was worked out from.
	appRootsFrom map[string]appRootsFrom
	// The program's coherence errors and the entry's own checks: iteration
	// sensitivity, then the concurrency, boot-scope and task-lifetime rules.
	coherence, entryChecks []TypeError
	// frontEnd holds the derive-lowering and duplicate test name errors the
	// front end reports before analysis.
	frontEnd []TypeError
}

// appRootsFrom is the analysis and body part a file's application-field
// root errors were worked out from, and whether the file has a root whose
// reads the check followed into other files (checkAppRootsOf).
type appRootsFrom struct {
	fa         *FileAnalysis
	generation uint64
	rooted     bool
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
//
// A file's application-field root errors are kept with the analysis and
// body part they came from (FileBodies.generation), and a file with no
// root whose reads they follow is not checked again until its bodies are
// rebuilt: its errors read nothing of another file's bodies. Coherence
// keeps each file's part the same way (FinalizeCoherence).
func (c *programChecks) reduce(p *CheckedProgram) {
	if c.appRoots == nil {
		c.appRoots = map[string][]TypeError{}
		c.appRootsFrom = map[string]appRootsFrom{}
	}
	for _, key := range append([]string{p.Entry}, c.checked...) {
		fa, nodes := c.fileOf(p, key)
		if from, ok := c.appRootsFrom[key]; ok && from.fa == fa && from.generation == fa.generation && !from.rooted {
			continue
		}
		var rooted bool
		c.appRoots[key], rooted = checkAppRootsOf(fa, nodes)
		c.appRootsFrom[key] = appRootsFrom{fa: fa, generation: fa.generation, rooted: rooted}
	}
	unused := c.entry.Program().checkImplImportsKept(c.program)
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
