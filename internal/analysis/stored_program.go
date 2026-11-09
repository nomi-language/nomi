package analysis

import (
	"crypto/sha256"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/progcache"
)

// Opening a project from its record.
//
// Opening a document checks the whole program it belongs to: about a
// second at 50k lines, and 300 MB held. When the program was checked
// before, by this server or an earlier one of the same build, and its files
// are as they were, the manager reads a record of it from the artifact
// cache instead (progcache, kind storedKind, keyed by the entry's path):
// each project file's interface (StoredInterface), the SHA-256 of its text,
// and the documents whose lowering found nothing. A record is written for
// a program that reads no file of another module and takes no Go
// bindings, every input an expansion read (a phrase catalog) holding on
// disk what it read and named in the record, and every `embed`'s files as
// the snapshot found them. It keeps what each file reports
// (storedReportsOf), and is not written for a program that reports what
// only the program checked whole words. A file as its text is answers what
// its check against the others' interfaces answers
// (FuzzCheckFromInterfacesMatchesFull).
//
// Opened from its record, a program is checked one open document at a
// time, each against the interfaces of the files it reads
// (ProgramInterfaces.Check over NewStoredInterfaces). A document whose
// check reports something its record's file does not is checked in its
// program whole. A request that
// needs a closed file's analysis checks that file against interfaces too,
// once (the closed files' cache). The document gets no EntryProgram, since
// its program's other files have no analysis to lower from; its lowering
// reads whether the record's program lowered it with nothing to report
// (StoredLoweredClean), and otherwise lowers it from a kept lowering
// (stored_lowering.go).
//
// The record is opened only while every file of its program holds on disk
// the text it was written for. An edit after the open stays in the program
// opened from the record when it can (stored_edit.go); any other forgets
// it, and the whole program is checked, kept and rechecked as edits come
// (document_programs.go).
//
// A record is written when a document's lowering, with its program kept
// and every text on disk, finds nothing to report (NoteLoweredClean): the
// server's background lowering after an open or a save. The program's
// interfaces are extracted then, from its analyses.

// storedKind names the language server's program records in the artifact
// cache.
const storedKind = "lsp-program"

// storedMagic heads a record. It changes with storedRecord's layout.
const storedMagic = "NOMILSP4"

// storedRecord is what a program's record holds.
type storedRecord struct {
	// Root is the project root and Entry the entry's module key.
	Root, Entry string
	// Files are the program's project files by module key.
	Files map[string]storedFile
	// AppTypes are the program's application types.
	AppTypes []AppTypeName
	// Inputs are the inputs the files' expansions read (phrase catalogs), by
	// path, each with the SHA-256 of its text and whether it was found. The
	// record is stored with them as files it was built from, and a program
	// opened from it is forgotten once one holds another text.
	Inputs map[string]storedInput
	// Lowered holds the module keys of the documents whose lowering, as
	// the server lowers a document, found nothing to report with every
	// file as the record holds it.
	Lowered map[string]bool
	// Supply is the supply side of the program's missing-impl check, which
	// an edit's reductions over facts read (stored_edit.go).
	Supply *ProgramSupply
	// Kept is the entry's lowering kept per file (vmhost, irbuild.KeptLowering),
	// encoded, as the server lowered it with every file as the record holds
	// it, and KeptTests whether it lowered the entry's tests; nil when the
	// server has none (stored_lowering.go).
	Kept      []byte
	KeptTests bool
}

// storedInput is one input of a record: the SHA-256 of its text, and
// whether it was found.
type storedInput struct {
	Sum   [32]byte
	Found bool
}

// inputPaths are the paths of r's inputs, sorted.
func (r *storedRecord) inputPaths() []string { return mapKeys(r.Inputs) }

// storedFile is one project file of a record.
type storedFile struct {
	Iface StoredInterface
	// Sum is the SHA-256 of the file's text.
	Sum [32]byte
	// Facts is the file's facts for the program's reductions over bodies,
	// encoded, and Body the walk the application-field roots take of its
	// bodies (AppWalkSummary), kept only for a program with an application
	// type (stored_edit.go).
	Facts []byte
	Body  [32]byte
	// Refs is what references and rename read of the file's analysis
	// (StoredRefs, stored_refs.go), encoded: a record is decoded when a
	// program is opened, and a file's references when a request asks.
	Refs []byte
	// Report is what the file reports in the record's program, nil when it
	// reports nothing (storedReport).
	Report *storedReport
}

func (r *storedRecord) encode() []byte { return gobEncode(storedMagic, r) }

func decodeStoredRecord(data []byte) (*storedRecord, error) {
	r := &storedRecord{}
	if err := gobDecode(storedMagic, data, r); err != nil {
		return nil, err
	}
	return r, nil
}

// storedProgram is a program opened from its record: the record, the
// interfaces its files' checks read, and the checks made of it so far.
// Guarded by analyzeMu.
type storedProgram struct {
	rec *storedRecord
	pi  *ProgramInterfaces
	// texts holds the text each check reads, by module key: the open
	// documents' and the files on disk, each of which is the record's.
	texts map[string]string
	// entryOwn is how many of the entry's nodes its last check prepared
	// as its own (CheckedProgram.entryOwn).
	entryOwn int
	// cache is the record's place in the artifact cache, from the lookup
	// that found it: NoteLoweredClean writes the record again through it.
	cache *progcache.Entry
	// fresh holds the checks the last edit made (advanceStored), by module
	// key, which the next analysis of each file takes instead of checking
	// it again.
	fresh map[string]*FileCheck
	// base is the record as it was opened, whose kept lowering is of its
	// texts.
	base *storedRecord
	// edits counts the edits the program has taken since it was opened.
	edits int
	// gen numbers the program's states, one more for each edit taken;
	// textAt and changedAt hold, by module key, the state at which a file's
	// text, and how its interface is written, last changed.
	gen               int
	textAt, changedAt map[string]int
	// installed holds the open documents' installed analyses, by module
	// key (stored_inplace.go).
	installed map[string]*storedInstall
	// fingerprints holds the interface fingerprint of a file's text in the
	// program's state, by module key, once worked out.
	fingerprints map[string]string
	// facts holds the files' facts in the program's state, decoded, by module
	// key (factsOf).
	facts map[string]*FileFacts
	// reports holds what each file that reports something reports in the
	// program's state, by module key (stored_edit.go). The record's program
	// reports nothing.
	reports map[string]*storedReport
	// held releases the view lock, which an edit that rebuilt an installed
	// analysis in place took; the build that asked takes it, and releases
	// it once its answer is installed.
	held func()
}

// takeHeld answers the release of the view lock sp's last edit took, or a
// release that does nothing.
func (sp *storedProgram) takeHeld() func() {
	if sp == nil || sp.held == nil {
		return func() {}
	}
	h := sp.held
	sp.held = nil
	return h
}

// storedFor answers the program whose entry has module key entry under
// root, opened from its record, or nil when there is none or a file no
// longer holds the text the program's state was made for and an edit of
// it cannot be taken (advanceStored). texts are the open documents' texts
// by module key (openTexts), and building the module key of the open
// document whose analysis asks, "" for none. The caller holds analyzeMu.
func (dm *DocumentManager) storedFor(root, entry, building string, texts map[string]string) *storedProgram {
	key := programKey{root, entry}
	sp := dm.stored[key]
	if sp == nil {
		if dm.storedMissed[key] {
			return nil
		}
		sp = dm.openStored(root, entry)
		if sp == nil {
			if dm.storedMissed == nil {
				dm.storedMissed = map[programKey]bool{}
			}
			dm.storedMissed[key] = true
			return nil
		}
		if dm.stored == nil {
			dm.stored = map[programKey]*storedProgram{}
		}
		dm.stored[key] = sp
	}
	changed, ok := dm.storedChanges(sp, texts)
	why := ""
	switch {
	case !ok:
		why = "a file does not read, or an input of an expansion changed"
	case len(changed) > 0:
		why = dm.advanceStored(sp, changed, building)
	}
	if why != "" {
		dm.programCounts.note("from the record: " + why)
		// An installed analysis rebuilt in place stays away from readers
		// until the whole program's answer is installed.
		dm.storedHeld = sp.held
		sp.held = nil
		dm.dropStored(key)
		return nil
	}
	return sp
}

// openStored reads the record of the program whose entry has module key
// entry under root, when the artifact cache holds one for the files as
// they are on disk.
func (dm *DocumentManager) openStored(root, entry string) *storedProgram {
	if root == "" || len(dm.syntheticExternsForRoot(root)) > 0 {
		return nil
	}
	entryPath := filepath.Join(root, filepath.FromSlash(entry)+".nomi")
	e := progcache.Open(storedKind, entryPath, []progcache.Tree{{Dir: root}})
	payload := e.Payload()
	if payload == nil {
		return nil
	}
	rec, err := decodeStoredRecord(payload)
	if err != nil || rec.Root != root || rec.Entry != entry || rec.Files[entry].Sum == ([32]byte{}) {
		return nil
	}
	sp := &storedProgram{rec: rec, base: rec, texts: map[string]string{}, cache: e}
	for key, f := range rec.Files {
		if f.Report != nil {
			if sp.reports == nil {
				sp.reports = map[string]*storedReport{}
			}
			sp.reports[key] = f.Report
		}
	}
	ifaces := make(map[string]StoredInterface, len(rec.Files))
	for key, f := range rec.Files {
		ifaces[key] = f.Iface
	}
	text := func(key string) (string, bool) {
		t, ok := sp.texts[key]
		return t, ok
	}
	src := dm.textSourceProgram(root, entry, text, nil, &sp.entryOwn)
	src.AppTypeNames = slices.Clone(rec.AppTypes)
	if src.AppTypeNames == nil {
		src.AppTypeNames = []AppTypeName{}
	}
	sp.pi = NewStoredInterfaces(src, ifaces)
	dm.programCounts.opened.Add(1)
	return sp
}

// storedCurrent reports whether every file of sp's program holds the text
// its record was written for: the open documents' texts, and the others
// on disk. It notes each text in sp.texts, which its checks read.
func (dm *DocumentManager) storedCurrent(sp *storedProgram, open map[string]string) bool {
	for key, f := range sp.rec.Files {
		text, ok := open[key]
		if !ok {
			data, err := dm.readProjectFile(filepath.Join(sp.rec.Root, filepath.FromSlash(key)+".nomi"))
			if err != nil {
				return false
			}
			text = data
		}
		if was, seen := sp.texts[key]; seen && was == text {
			continue
		}
		if sha256.Sum256([]byte(text)) != f.Sum {
			return false
		}
		sp.texts[key] = text
	}
	return true
}

// dropStored forgets the program opened from its record under key: the
// next analysis checks the whole program.
func (dm *DocumentManager) dropStored(key programKey) {
	delete(dm.stored, key)
	if dm.storedMissed == nil {
		dm.storedMissed = map[programKey]bool{}
	}
	// A record found stale stays so until the program is written again.
	dm.storedMissed[key] = true
}

// check is the file with module key key checked against the others'
// interfaces. A closed file's is kept by the closed files' cache, an open
// document's by its installed analysis.
func (sp *storedProgram) check(dm *DocumentManager, key string) *FileCheck {
	if c := sp.fresh[key]; c != nil {
		delete(sp.fresh, key)
		return c
	}
	dm.programCounts.built.Add(1)
	c := sp.pi.Check(key)
	if p := c.FA.Program(); p != nil {
		root, manifest := sp.rec.Root, sp.pi.sp.Manifest
		p.sources = &declarationSources{root: root, texts: maps.Clone(sp.texts),
			expand: func(key string, nodes []ast.Node) []ast.Node {
				return ExpandDeclarations(nodes, dm.expansionSite(root, key, manifest, dm.readInput))
			}}
		delete(p.sources.texts, key)
	}
	return c
}

// clean reports whether c reports nothing of its own, as the record's
// program did not.
func (c *FileCheck) clean() bool {
	return len(c.Build)+len(c.Check)+len(c.EntryChecks)+len(c.FrontEnd) == 0
}

// storedBuild is the build of the open document with module key key from
// sp, or false when its check reports something the record's program did
// not, which a check of the whole program then words.
func (dm *DocumentManager) storedBuild(sp *storedProgram, key, content string) (builtAnalysis, bool) {
	if inst := sp.installed[key]; inst != nil && sp.fresh[key] == nil && sp.texts[key] == content && sp.current(inst, key) {
		// Nothing the installed analysis read changed.
		return inst.b, true
	}
	c := sp.check(dm, key)
	rep := sp.reports[key]
	if rep == nil && !c.clean() {
		return builtAnalysis{}, false
	}
	b := builtAnalysis{
		nodes:      ownNodesOf(c.FA, c.Nodes, key == sp.rec.Entry, sp.entryOwn),
		fa:         c.FA,
		stdDiffers: dm.stdDiffers(c.FA.FilePath, content),
		fromRecord: true,
	}
	c.FA.TypeErrors = nil
	if rep != nil {
		c.FA.TypeErrors = slices.Clone(rep.Errs)
		b.errs, b.damaged = rep.Syntax, rep.Damaged
	}
	settleStoredImplImports(c.FA, rep.reductions())
	if sp.installed == nil {
		sp.installed = map[string]*storedInstall{}
	}
	sp.installed[key] = &storedInstall{c: c, b: b, gen: sp.gen}
	return b, true
}

// CheckWhole analyzes the open document at uri again in its program
// checked whole, when its installed analysis was made in a program opened
// from its record, and reports whether it did. A request that reads what
// only the whole program holds (every impl of an interface, which a file
// checked against interfaces sees only the part of it can see) calls it
// first; the analysis it installs is the one every later edit rechecks.
// A caller holding the view lock may call it: a whole check rewrites
// nothing a reader holds.
func (dm *DocumentManager) CheckWhole(uri string) bool {
	doc := dm.Get(uri)
	if doc == nil {
		return false
	}
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	dm.mu.RLock()
	fromRecord := doc.FromRecord
	dm.mu.RUnlock()
	if !fromRecord {
		return false
	}
	root := dm.projectRootFromURI(uri)
	for key := range dm.stored {
		if key.root == root {
			dm.dropStored(key)
		}
	}
	dm.programCounts.whole.Add(1)
	dm.analyzeLocked(doc)
	return true
}

// storedClosed is the snapshot of the closed file at uri from a program
// opened from its record that holds it, its file checked against the
// others' interfaces, or nil. The caller holds analyzeMu.
func (dm *DocumentManager) storedClosed(uri string) *DocSnapshot {
	path := fileuri.ToPath(uri)
	root := dm.projectRootFromURI(uri)
	key := relModuleKey(root, path)
	if key == "" || strings.HasPrefix(key, "..") {
		return nil
	}
	for _, entryPath := range dm.entryCandidates(root) {
		if entryPath == path {
			continue
		}
		pk := programKey{root, relModuleKey(root, entryPath)}
		if dm.programs[pk] != nil {
			// The program is kept: keptClosed answers, or a build does.
			return nil
		}
		if _, err := os.Stat(entryPath); err != nil {
			continue
		}
		sp := dm.storedFor(root, pk.entry, "", dm.openTexts(root, "", ""))
		if sp == nil {
			return nil
		}
		if _, ok := sp.rec.Files[key]; !ok {
			continue
		}
		c := sp.check(dm, key)
		rep := sp.reports[key]
		if rep == nil && !c.clean() {
			return nil
		}
		snap := &DocSnapshot{
			URI:             uri,
			Content:         sp.texts[key],
			Text:            sp.texts[key],
			Nodes:           ownNodesOf(c.FA, c.Nodes, key == sp.rec.Entry, sp.entryOwn),
			Analysis:        c.FA,
			Version:         1,
			AnalyzedVersion: 1,
			StdDiffers:      dm.stdDiffers(path, sp.texts[key]),
		}
		c.FA.TypeErrors = nil
		if rep != nil {
			c.FA.TypeErrors = slices.Clone(rep.Errs)
			snap.Errors, snap.Damaged = rep.Syntax, rep.Damaged
		}
		settleStoredImplImports(c.FA, rep.reductions())
		return snap
	}
	return nil
}

// StoredLoweredClean reports whether the open document at uri, whose text
// is content, belongs to a program opened from its record whose lowering
// of the document found nothing to report: what the server's lowering of
// content would find.
func (dm *DocumentManager) StoredLoweredClean(uri, content string) bool {
	dm.lockBuild(false)
	defer dm.analyzeMu.Unlock()
	sp, key := dm.storedOf(uri)
	return sp != nil && sp.rec.Lowered[key] && sp.texts[key] == content && dm.storedCurrent(sp, dm.openTexts(sp.rec.Root, "", ""))
}

// storedOf is the program opened from its record that the document at uri
// was analyzed in, with the document's module key, or nil.
func (dm *DocumentManager) storedOf(uri string) (*storedProgram, string) {
	path := fileuri.ToPath(uri)
	root := dm.projectRootFromURI(uri)
	key := relModuleKey(root, path)
	for _, entry := range dm.programEntries(root, path) {
		pk := programKey{root, entry}
		if sp := dm.stored[pk]; sp != nil && dm.programs[pk] == nil {
			if _, ok := sp.rec.Files[key]; ok {
				return sp, key
			}
		}
	}
	return nil, ""
}

// programEntries are the module keys of the entries whose programs an
// open document at path under root is analyzed in, in the order buildOpen
// tries them: each entry candidate but the document, then the document.
func (dm *DocumentManager) programEntries(root, path string) []string {
	var out []string
	for _, entryPath := range dm.entryCandidates(root) {
		if entryPath != path {
			out = append(out, relModuleKey(root, entryPath))
		}
	}
	return append(out, relModuleKey(root, path))
}

// NoteLoweredClean records that the server's lowering of the open document
// at uri, whose text is content, found nothing to report. When the
// document's program is kept, with nothing to report, and every one of its
// files holds on disk the text the program read, its record is written,
// listing the document among those lowered (with those an earlier record
// of the same texts listed); when the document belongs to a program opened
// from its record, the record is written again with the document listed.
// It runs at background priority.
func (dm *DocumentManager) NoteLoweredClean(uri, content string, kept KeptLoweringFunc) {
	dm.lockBuild(false)
	defer dm.analyzeMu.Unlock()
	if sp, key := dm.storedOf(uri); sp != nil {
		if sp.rec.Lowered[key] || sp.texts[key] != content || !dm.storedCurrent(sp, dm.openTexts(sp.rec.Root, "", "")) {
			return
		}
		rec := *sp.rec
		rec.Files = maps.Clone(sp.rec.Files)
		for k, f := range rec.Files {
			f.Report = sp.reports[k]
			rec.Files[k] = f
		}
		rec.Lowered = map[string]bool{key: true}
		for k := range sp.rec.Lowered {
			rec.Lowered[k] = true
		}
		if key == rec.Entry && rec.Kept == nil && sp.clean() {
			rec.Kept, rec.KeptTests = keptOfState(kept, &rec)
		}
		sp.rec = &rec
		e := sp.cache
		if sp.edits > 0 {
			// The state is an edit's: the record is written only when every
			// file holds it on disk, against a snapshot taken before the
			// texts are read.
			entryPath := filepath.Join(rec.Root, filepath.FromSlash(rec.Entry)+".nomi")
			if e = progcache.Open(storedKind, entryPath, []progcache.Tree{{Dir: rec.Root}}); e == nil {
				return
			}
			for k, f := range rec.Files {
				data, err := dm.readProjectFile(filepath.Join(rec.Root, filepath.FromSlash(k)+".nomi"))
				if err != nil || sha256.Sum256([]byte(data)) != f.Sum {
					return
				}
			}
		}
		e.Store([]progcache.Tree{{Dir: rec.Root}}, rec.inputPaths(), rec.encode())
		dm.programCounts.stored.Add(1)
		return
	}
	path := fileuri.ToPath(uri)
	root := dm.projectRootFromURI(uri)
	docKey := relModuleKey(root, path)
	for _, entry := range dm.programEntries(root, path) {
		prog := dm.programs[programKey{root, entry}]
		if prog == nil || prog.prog.consumed || prog.prog.Files[docKey] == nil {
			continue
		}
		if prog.prog.Texts[docKey] == content {
			dm.keepStored(prog, docKey, kept)
		}
		return
	}
}

// keepStored writes the record of kept's program, listing the document
// with module key lowered among those lowered, when the program has
// nothing to report and every file holds on disk the text it read. The
// caller holds analyzeMu.
func (dm *DocumentManager) keepStored(kept *rootProgram, lowered string, keptOf KeptLoweringFunc) {
	p := kept.prog
	if p.factsFrom != nil || !storable(p) || kept.manifest != manifestText(p.Root) {
		return
	}
	entryPath := filepath.Join(p.Root, filepath.FromSlash(p.Entry)+".nomi")
	// The snapshot is taken before the texts are compared with the disk,
	// so a file written after the comparison is one the store sees moved.
	// An `embed`'s trees are snapshotted with the root's and its files read
	// again after (sameOnDisk).
	embedTrees, embedFiles, embeds := embedInputs(p)
	e := progcache.Open(storedKind, entryPath, append([]progcache.Tree{{Dir: p.Root}}, embedTrees...))
	if e == nil {
		return
	}
	manifest, _ := LoadManifest(p.Root)
	for _, info := range embeds {
		if !info.sameOnDisk(manifest) {
			return
		}
	}
	open := dm.openTexts(p.Root, "", "")
	for key, text := range p.Texts {
		data, err := dm.readProjectFile(filepath.Join(p.Root, filepath.FromSlash(key)+".nomi"))
		if err != nil || data != text {
			return
		}
		if o, ok := open[key]; ok && o != text {
			return
		}
	}
	rec := &storedRecord{Root: p.Root, Entry: p.Entry, Files: map[string]storedFile{}, Lowered: map[string]bool{lowered: true}}
	for _, st := range p.Files[p.Entry].Program().AppTypes {
		rec.AppTypes = append(rec.AppTypes, AppTypeName{Origin: st.Origin, Name: st.Name})
	}
	pi := NewProgramInterfaces(dm.sourceProgram(p))
	stored, ok := pi.Stored()
	if !ok || len(stored) != len(p.Files) {
		return
	}
	entryFA := p.Files[p.Entry]
	appTypes := len(rec.AppTypes) > 0
	namer := NewFactsNamer(p.Nodes, p.Entry)
	for key, si := range stored {
		f := storedFile{Iface: si, Sum: sha256.Sum256([]byte(p.Texts[key])),
			Facts: FileFactsOf(key, p.Files[key], p.Nodes[key], namer, appTypes).Encode(), Refs: storedRefsOf(p.Files[key], p.Root).encode()}
		if appTypes {
			f.Body, _ = AppWalkSummary(p.Files[key], p.Nodes[key])
		}
		rec.Files[key] = f
	}
	rec.Supply = SupplyOf(entryFA)
	reports, ok := storedReportsOf(p, rec, appTypes)
	if !ok {
		return
	}
	for key, r := range reports {
		f := rec.Files[key]
		f.Report = r
		rec.Files[key] = f
	}
	for path, in := range p.Inputs {
		// An input open with unsaved text is not what the record's next
		// reader finds on disk.
		if data, err := os.ReadFile(path); in.Found != (err == nil) || err == nil && string(data) != in.Text {
			return
		}
		if rec.Inputs == nil {
			rec.Inputs = map[string]storedInput{}
		}
		rec.Inputs[path] = storedInput{Sum: sha256.Sum256([]byte(in.Text)), Found: in.Found}
	}
	if was, err := decodeStoredRecord(e.Previous()); err == nil && sameSums(was, rec) {
		for k := range was.Lowered {
			rec.Lowered[k] = true
		}
		rec.Kept, rec.KeptTests = was.Kept, was.KeptTests
	}
	if lowered == p.Entry {
		if data, tests := keptOfState(keptOf, rec); data != nil {
			rec.Kept, rec.KeptTests = data, tests
		}
	}
	if payload := rec.encode(); payload != nil {
		e.Store(append([]progcache.Tree{{Dir: p.Root}}, embedTrees...), append(rec.inputPaths(), embedFiles...), payload)
		dm.programCounts.stored.Add(1)
	}
}

// embedInputs are the trees and files p's `embed` declarations read, which
// a record of p is kept with, as a kept program's are (vmhost's
// Program.inputs): each pattern's base walked for every file, and each
// embedded file stamped on its own, since a walk skips hidden names. The
// record is not opened once one changed. The server finds the files and
// their sizes without reading them and tracks none of them while a
// program is open, from its record or not (SkipEmbedContents).
func embedInputs(p *CheckedProgram) ([]progcache.Tree, []string, []*EmbedInfo) {
	var trees []progcache.Tree
	var files []string
	var infos []*EmbedInfo
	for _, key := range mapKeys(p.Nodes) {
		for _, n := range p.Nodes[key] {
			o, ok := n.(*ast.OnceBinding)
			if !ok {
				continue
			}
			info := EmbedOf(o)
			if info == nil || !filepath.IsAbs(info.Root) {
				continue
			}
			infos = append(infos, info)
			for _, base := range info.Bases() {
				trees = append(trees, progcache.Tree{Dir: filepath.Join(info.Root, filepath.FromSlash(base)), All: true})
			}
			for _, f := range info.Files {
				files = append(files, filepath.Join(info.Root, filepath.FromSlash(f.Path)))
			}
		}
	}
	return trees, files, infos
}

// storedReportsOf answers what each file of p reports, for a record of p
// (storedReport), by module key, and false when p reports something a
// program opened from its record cannot answer as the program checked
// whole does: the program's own errors about impls, a missing impl, a
// noun's or phrase's disagreement, a catalog nothing reads, a diagnostic
// in a program with an application type, or one in a program the
// reductions over facts cannot reduce.
func storedReportsOf(p *CheckedProgram, rec *storedRecord, appTypes bool) (map[string]*storedReport, bool) {
	out := map[string]*storedReport{}
	for key := range rec.Files {
		r := &storedReport{Syntax: p.Syntax[key], Damaged: p.damaged[key], Errs: documentErrors(p, key)}
		if !r.empty() {
			out[key] = r
		}
	}
	if len(out) == 0 {
		return nil, true
	}
	c := p.checks
	entry := p.Files[p.Entry]
	if appTypes || len(entry.Program().Errors) > 0 || len(c.coherence) > 0 || len(c.catalogTree) > 0 {
		return nil, false
	}
	for _, errs := range c.declChecks {
		if len(errs) > 0 {
			return nil, false
		}
	}
	std, ok := StdContextOf(entry)
	if !ok {
		return nil, false
	}
	facts := map[string]*FileFacts{}
	for key, f := range rec.Files {
		ff, err := DecodeFileFacts(f.Facts)
		if err != nil {
			return nil, false
		}
		facts[key] = ff
	}
	red := ReduceFacts(facts, rec.Supply, std)
	if len(red.Coherence) > 0 || !red.AppExact {
		return nil, false
	}
	for key := range rec.Files {
		d := red.of(key)
		if len(d.Boot)+len(d.App)+len(d.Imports) == 0 {
			continue
		}
		if out[key] == nil {
			// The reductions report what the file's document does not.
			return nil, false
		}
		out[key].Reduced = d
	}
	return out, true
}

// storable reports whether p is a program a record is kept for: nothing
// to report in any file, and nothing read that the record's walk of the
// root and its inputs do not see.
func storable(p *CheckedProgram) bool {
	if p.unread || p.externs || p.Root == "" {
		return false
	}
	for key := range p.Files {
		if _, ok := p.Texts[key]; !ok {
			return false
		}
	}
	return true
}

// sameSums reports whether two records hold the same files with the same
// texts.
func sameSums(a, b *storedRecord) bool {
	if len(a.Files) != len(b.Files) {
		return false
	}
	for key, f := range a.Files {
		if g, ok := b.Files[key]; !ok || g.Sum != f.Sum {
			return false
		}
	}
	return true
}

// StoredPrograms counts the programs the manager opened from their
// records, and the records it wrote.
func (dm *DocumentManager) StoredPrograms() (opened, stored int64) {
	return dm.programCounts.opened.Load(), dm.programCounts.stored.Load()
}

// MayName reports whether text holds name as a whole word: a run of
// letters, digits and underscores that is name, the only way a reference
// or a definition of a symbol named name is written.
func MayName(text, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if !wordRune(before) && !wordRune(after) {
			return true
		}
		i = start + 1
	}
}

// wordRune reports whether r continues a word, as an identifier's
// characters do.
func wordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
