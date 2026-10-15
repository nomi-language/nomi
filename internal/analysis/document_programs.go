package analysis

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/internal/ast"
)

// An open document is analyzed as part of a whole program, checked as the
// front end checks it (CheckProgram): the program of the first entry
// candidate under its project root that reaches it, or else the program it
// is the entry of. The manager keeps each such program between analyses
// (rootProgram), and the next analysis of a document it reaches rechecks it
// with every file whose text changed: when no changed file's interface did,
// only their bodies are rebuilt (rebuildEditedBodies), and otherwise the
// changed files and their importers are checked afresh beside the other
// files' kept analyses (recheckDependents).
//
// A recheck that rebuilds bodies rewrites the kept program's analyses,
// nodes and symbols in place, and one that checks importers afresh hands
// the kept analyses a new Program; documents have them installed. So a
// reader of an installed analysis holds the view lock (View) while it
// reads, and a recheck that rewrites installed objects takes it exclusively
// (lockView) from before it rewrites anything until the rewritten analysis
// is installed. A recheck never waits long for it: when readers hold it, the
// program is checked afresh instead, which writes nothing anyone reads.

// rootProgram is the program the manager last checked for one entry under
// one project root.
type rootProgram struct {
	prog *CheckedProgram
	// manifest is the root's nomi.toml as the check read it.
	manifest string
}

// programKey names a rootProgram: the project root and the entry's module
// key.
type programKey struct {
	root, entry string
}

// viewWait bounds how long a recheck waits for the readers of installed
// analyses before it checks the program afresh.
const viewWait = 50 * time.Millisecond

// View takes the view lock for reading and answers its release. Hold it
// while reading an installed analysis (a snapshot's Analysis, Nodes or
// Program, or anything reached from them), so that no recheck rewrites them
// meanwhile. It may be taken again while held.
//
// A reader that comes while a recheck waits for the lock waits for the
// recheck first, so a stream of requests does not keep it out; that wait
// ends when the recheck has the lock and is done, or gives up.
func (dm *DocumentManager) View() func() {
	for dm.viewWaiting.Load() > 0 {
		time.Sleep(50 * time.Microsecond)
	}
	dm.viewMu.RLock()
	return dm.viewMu.RUnlock
}

// lockView takes the view lock exclusively, waiting up to viewWait for the
// readers that hold it, and reports whether it did. It never blocks on the
// lock itself: a reader may wait for a build, so a recheck that waited for
// it without end could wait for itself.
func (dm *DocumentManager) lockView() bool {
	dm.viewWaiting.Add(1)
	defer dm.viewWaiting.Add(-1)
	deadline := time.Now().Add(viewWait)
	for !dm.viewMu.TryLock() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Microsecond)
	}
	return true
}

// programCounts counts how the manager answered the programs its open
// documents' analyses asked for.
type programCounts struct {
	bodies, dependents, full, reused atomic.Int64
	// reasons counts why whole programs were checked.
	mu      sync.Mutex
	reasons map[string]int
}

// note counts one whole-program check for reason.
func (c *programCounts) note(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reasons == nil {
		c.reasons = map[string]int{}
	}
	c.reasons[reason]++
}

// ProgramChecks reports how many of the open documents' analyses rebuilt
// only edited files' bodies, checked edited files and the files that import
// them afresh, checked a whole program, and reused a program as it was.
func (dm *DocumentManager) ProgramChecks() (bodies, dependents, full, reused int64) {
	c := &dm.programCounts
	return c.bodies.Load(), c.dependents.Load(), c.full.Load(), c.reused.Load()
}

// FullCheckReasons counts why the open documents' analyses checked whole
// programs.
func (dm *DocumentManager) FullCheckReasons() map[string]int {
	c := &dm.programCounts
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.reasons)
}

// forgetPrograms drops every kept program, so the next analysis of each
// checks its program afresh: a file appeared that an import may now
// resolve to, or the Go bindings changed.
func (dm *DocumentManager) forgetPrograms() {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	dm.programs = nil
}

// buildOpen is build for an open document's text, through the program it
// belongs to (see above). It answers the build and the release of the view
// lock when the build took it, which the caller calls once it has installed
// the build. The caller holds analyzeMu.
func (dm *DocumentManager) buildOpen(uri, content string) (builtAnalysis, func()) {
	none := func() {}
	docPath := strings.TrimPrefix(uri, "file://")
	root := dm.projectRootFromURI(uri)
	docKey := relModuleKey(root, docPath)
	if dm.isStdlibFile(uri) || !filepath.IsAbs(docPath) || filepath.Ext(docPath) != ".nomi" ||
		docKey == "" || strings.HasPrefix(docKey, "..") || IsStdlibKey(docKey) ||
		len(dm.syntheticExternsForRoot(root)) > 0 {
		return dm.build(uri, content), none
	}
	manifest := manifestText(root)
	// The project from an entry that reaches the document, as the document
	// of a file an entry imports sees its entry's boot; else the document's
	// own.
	for _, entryPath := range dm.entryCandidates(root) {
		if entryPath == docPath {
			continue
		}
		if _, err := os.Stat(entryPath); err != nil {
			continue
		}
		p, release := dm.programFor(root, relModuleKey(root, entryPath), docKey, uri, content, manifest)
		if p.Files[docKey] != nil {
			return dm.documentBuild(p, docKey, content, manifest), release
		}
		release()
	}
	p, release := dm.programFor(root, docKey, docKey, uri, content, manifest)
	return dm.documentBuild(p, docKey, content, manifest), release
}

// openTexts are the texts of the open documents under root, by module key:
// content for uri, and the analyzed text of every other (its latest before
// its first analysis), the text the loader gives a build.
func (dm *DocumentManager) openTexts(root, uri, content string) map[string]string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	texts := map[string]string{}
	if key := relModuleKey(root, strings.TrimPrefix(uri, "file://")); key != "" {
		texts[key] = content
	}
	for u, doc := range dm.docs {
		path := strings.TrimPrefix(u, "file://")
		if !strings.HasPrefix(u, "file://") || filepath.Ext(path) != ".nomi" {
			continue
		}
		key := relModuleKey(root, path)
		if key == "" || strings.HasPrefix(key, "..") || IsStdlibKey(key) {
			continue
		}
		switch {
		case u == uri:
			texts[key] = content
		case doc.AnalyzedVersion == 0:
			texts[key] = doc.Content
		default:
			texts[key] = doc.AnalyzedContent
		}
	}
	return texts
}

// programFor answers the program whose entry has module key entry under
// root, with uri's text content: the kept one when no file's text changed
// since it was checked, and otherwise rechecked with the changed texts
// (recheckIncremental), or checked afresh when that cannot answer. It
// answers the release of the view lock when it took it. The caller holds
// analyzeMu, which programFor may let go of and take again while it waits
// for the view lock.
func (dm *DocumentManager) programFor(root, entry, docKey, uri, content, manifest string) (*CheckedProgram, func()) {
	none := func() {}
	texts := dm.openTexts(root, uri, content)
	key := programKey{root, entry}
	reason := ""
	// parsed holds the changed texts parsed, which a full check takes.
	var parsed map[string]*parsedFile
	fresh := func() (*CheckedProgram, func()) {
		dm.programCounts.full.Add(1)
		dm.programCounts.note(reason)
		p := dm.checkProgramFrom(root, entry, texts, nil, nil, parsed)
		if dm.programs == nil {
			dm.programs = map[programKey]*rootProgram{}
		}
		dm.programs[key] = &rootProgram{prog: p, manifest: manifest}
		return p, none
	}
	kept := dm.programs[key]
	switch {
	case kept == nil:
		reason = "first check"
	case kept.manifest != manifest:
		reason = "the manifest changed"
	case kept.prog.consumed:
		reason = "consumed"
	case kept.prog.unread:
		reason = "reads files of other modules"
	}
	if reason != "" {
		return fresh()
	}
	prev := kept.prog
	var changed []string
	for k, old := range prev.Texts {
		cur, ok := texts[k]
		if !ok {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(k)+".nomi"))
			if err != nil {
				reason = "a file is gone"
				return fresh()
			}
			cur = string(data)
			texts[k] = cur
		}
		if cur != old {
			changed = append(changed, k)
		}
	}
	if len(changed) == 0 && prev.Files[docKey] == nil {
		// The program does not reach the document, so nothing of it is
		// installed for it.
		dm.programCounts.reused.Add(1)
		return prev, none
	}
	// The kept program's analyses are installed, and an answer from it
	// changes what they say: its files' diagnostics, at least. Readers must
	// be kept away from them until the answer is installed. While it waits
	// for them the build lets other builds run, since a reader may be
	// waiting for one (a request that analyzes a closed file).
	dm.analyzeMu.Unlock()
	locked := dm.lockView()
	dm.lockBuild(true)
	if !locked {
		reason = "readers held the view"
		return fresh()
	}
	release := func() { dm.viewMu.Unlock() }
	if dm.programs[key] != kept || kept.prog != prev {
		// Another build rechecked the program meanwhile, maybe for a newer
		// text of this document than content, so this build's answer may
		// never be installed. It must rewrite nothing that is.
		release()
		reason = "another build rechecked the program"
		return fresh()
	}
	if len(changed) == 0 {
		dm.programCounts.reused.Add(1)
		return prev, release
	}
	slices.Sort(changed)
	edits := make([]ProgramEdit, len(changed))
	for i, k := range changed {
		edits[i] = ProgramEdit{Key: k, Text: texts[k]}
	}
	parsed = parseEdits(edits)
	next, outcome := dm.recheckIncremental(prev, edits, parsed)
	switch {
	case next != nil && outcome.Path == RecheckBodies:
		dm.programCounts.bodies.Add(1)
	case next != nil:
		dm.programCounts.dependents.Add(1)
	case !prev.consumed:
		// A full check writes nothing anyone reads, so it runs without the
		// view lock.
		reason = outcome.Reason
		release()
		return fresh()
	default:
		// The bodies were rebuilt in place before a `once` turned out to
		// have a new type, and its readers could not be checked alone: the
		// installed analyses have changed, so the full check is installed
		// before readers come back.
		reason = outcome.Reason
		dm.programCounts.full.Add(1)
		dm.programCounts.note(reason)
		next = dm.checkProgramFrom(root, entry, texts, nil, nil, parsed)
	}
	kept.prog = next
	return next, release
}

// ownNodes are the nodes of p's file with module key key without what a
// build adds to them: the prelude imports before them and the impls
// synthesis appends after them. They are the file's declarations, derives
// lowered, as a document's analysis has always held them.
func ownNodes(p *CheckedProgram, key string) []ast.Node {
	nodes := p.Nodes[key]
	if key == p.Entry {
		return nodes[:min(p.entryOwn, len(nodes))]
	}
	start := 0
	for start < len(nodes) && isPreludeInjectLine(nodes[start].LineNum()) {
		start++
	}
	end := min(start+p.Files[key].ownNodes, len(nodes))
	return nodes[start:end]
}

// documentBuild is the build of the document with module key key from p,
// the program it belongs to: its nodes, syntax errors and repaired
// declarations, its analysis with the diagnostics the language server
// publishes for it in TypeErrors, and its EntryProgram when the program is
// offered: p's (entryProgramOf) when it is p's entry, else that of the
// files it reaches (memberProgramOf). The diagnostics are those the document's own
// analysis reported before programs were kept, in the same order:
//
//   - the entry: the build's, the program's own, CheckTypes', the
//     application-field roots', the iteration and concurrency checks',
//     coherence over every file, the unused whole-file imports, and the
//     derive-lowering and duplicate test name errors;
//   - another file: the build's (derive lowering's among them), CheckTypes',
//     the application-field roots', the iteration and concurrency checks
//     over its own nodes, the missing impls its own demands find, its
//     unused whole-file imports, and its duplicate test names.
//
// Diagnostics inside a declaration the parse repaired are dropped
// (suppressInDamagedSpans).
func (dm *DocumentManager) documentBuild(p *CheckedProgram, key, content, manifest string) builtAnalysis {
	fa := p.Files[key]
	c := p.checks
	own := ownNodes(p, key)
	errs := slices.Clone(c.build[key])
	if key == p.Entry {
		errs = append(errs, fa.Program().Errors...)
		errs = append(errs, c.check[key]...)
		errs = append(errs, c.appRoots[key]...)
		errs = append(errs, c.entryChecks...)
		errs = append(errs, entryCoherence(p)...)
		errs = append(errs, c.implImports[key]...)
		errs = append(errs, c.frontEnd...)
	} else {
		errs = append(errs, c.check[key]...)
		errs = append(errs, c.appRoots[key]...)
		errs = append(errs, AnalyzeIterSensitivity(fa, own)...)
		errs = append(errs, CheckConcurrentScope(fa, own)...)
		errs = append(errs, CheckBootScope(fa, own)...)
		errs = append(errs, CheckTaskLifetime(fa, own)...)
		errs = append(errs, MissingImpls(fa.Program(), []*FileAnalysis{fa})...)
		errs = append(errs, c.implImports[key]...)
		errs = append(errs, CheckDuplicateTestNames(own)...)
	}
	damaged := p.damaged[key]
	fa.TypeErrors = suppressInDamagedSpans(errs, damaged)
	b := builtAnalysis{
		nodes:      own,
		errs:       p.Syntax[key],
		damaged:    damaged,
		fa:         fa,
		stdDiffers: dm.stdDiffers(fa.FilePath, content),
	}
	if key == p.Entry {
		b.program = entryProgramOf(p, manifest)
	} else {
		b.program = memberProgramOf(p, key, manifest)
	}
	return b
}

// entryCoherence is the missing impls the entry document reports: those
// its own demands find, and those its program's other files demand in
// their declarations (a derive's), as before programs were kept, when the
// other files' bodies were not checked for the entry's analysis. A demand a
// body of another file makes is reported with that file.
//
// Every other file kept by the project build contributes its declarations'
// demands (keptDecls.manifest). The program keeps each file's part, so a
// call after a recheck rebuilt some files' bodies reads only those again.
func entryCoherence(p *CheckedProgram) []TypeError {
	entry := p.Files[p.Entry]
	return entry.Program().entryCoherence(entry)
}

// entryProgramOf is the EntryProgram of p's entry, or nil when it is not
// offered: when the entry reaches other project files, they and the entry
// must have no syntax error and no diagnostic, and the entry no derive
// lowering or duplicate test name error.
func entryProgramOf(p *CheckedProgram, manifest string) *EntryProgram {
	ep := &EntryProgram{Nodes: p.Nodes[p.Entry], Root: p.Root, Manifest: manifest}
	if len(p.Files) == 1 {
		return ep
	}
	if !programClean(p) {
		return nil
	}
	for key, fa := range p.Files {
		if key != p.Entry {
			ep.Files = append(ep.Files, EntryProgramFile{Key: key, Path: fa.FilePath, Nodes: p.Nodes[key], FA: fa, Text: p.Texts[key]})
		}
	}
	slices.SortFunc(ep.Files, func(a, b EntryProgramFile) int { return strings.Compare(a.Key, b.Key) })
	ep.ReachesEntry = p.reachesEntry
	return ep
}

// programClean reports whether p, a program of several files, may be
// lowered from its analyses: no file has a syntax error or a diagnostic,
// the entry no derive lowering or duplicate test name error, the build
// read no file of another module, and every file but the entry has an
// absolute path.
func programClean(p *CheckedProgram) bool {
	if len(p.checks.frontEnd) > 0 || p.unread {
		return false
	}
	for key, fa := range p.Files {
		if len(p.Syntax[key]) > 0 || len(p.damaged[key]) > 0 || len(p.Errors[key]) > 0 || len(fa.TypeErrors) > 0 && key == p.Entry {
			return false
		}
		if key != p.Entry && !filepath.IsAbs(fa.FilePath) {
			return false
		}
	}
	return true
}
