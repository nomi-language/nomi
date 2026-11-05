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
// and the documents whose lowering found nothing. A record is written only
// for a program with nothing to report: no file has a syntax error or a
// diagnostic, and no file is read from another module, takes Go bindings
// or expands a keyword declaration from an input, so a program as the
// record holds it reports nothing, and a file as its text is answers what
// its check against the others' interfaces answers
// (FuzzCheckFromInterfacesMatchesFull).
//
// Opened from its record, a program is checked one open document at a
// time, each against the interfaces of the files it reads
// (ProgramInterfaces.Check over NewStoredInterfaces). Every document's
// diagnostics are none, as the record's program's were. A request that
// needs a closed file's analysis checks that file against interfaces too,
// once (the closed files' cache). The document gets no EntryProgram, since
// its program's other files have no analysis to lower from; its lowering
// reads whether the record's program lowered it with nothing to report
// (StoredLoweredClean), and otherwise lowers it from its text.
//
// The record answers only while every file of its program holds the text
// it was written for, open documents included. The first analysis that
// sees another text (an edit, a file changed on disk) checks the whole
// program, as an open without a record does, and from then on the program
// is kept and rechecked as edits come (document_programs.go). So an edit
// costs one check of the program, once, where opening cost it before.
//
// A record is written when a document's lowering, with its program kept
// and every text on disk, finds nothing to report (NoteLoweredClean): the
// server's background lowering after an open or a save. The program's
// interfaces are extracted then, from its analyses.

// storedKind names the language server's program records in the artifact
// cache.
const storedKind = "lsp-program"

// storedMagic heads a record. It changes with storedRecord's layout.
const storedMagic = "NOMILSP1"

// storedRecord is what a program's record holds.
type storedRecord struct {
	// Root is the project root and Entry the entry's module key.
	Root, Entry string
	// Files are the program's project files by module key.
	Files map[string]storedFile
	// AppTypes are the program's application types.
	AppTypes []AppTypeName
	// Lowered holds the module keys of the documents whose lowering, as
	// the server lowers a document, found nothing to report with every
	// file as the record holds it.
	Lowered map[string]bool
}

// storedFile is one project file of a record.
type storedFile struct {
	Iface StoredInterface
	// Sum is the SHA-256 of the file's text.
	Sum [32]byte
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
}

// storedFor answers the program whose entry has module key entry under
// root, opened from its record, or nil when there is none or a file no
// longer holds the text it was written for. texts are the open documents'
// texts by module key (openTexts). The caller holds analyzeMu.
func (dm *DocumentManager) storedFor(root, entry string, texts map[string]string) *storedProgram {
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
	if !dm.storedCurrent(sp, texts) {
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
	sp := &storedProgram{rec: rec, texts: map[string]string{}, cache: e}
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
	dm.programCounts.built.Add(1)
	c := sp.pi.Check(key)
	if p := c.FA.Program(); p != nil {
		p.sources = &declarationSources{root: sp.rec.Root, texts: maps.Clone(sp.texts)}
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
	c := sp.check(dm, key)
	if !c.clean() {
		return builtAnalysis{}, false
	}
	c.FA.TypeErrors = nil
	return builtAnalysis{
		nodes:      ownNodesOf(c.FA, c.Nodes, key == sp.rec.Entry, sp.entryOwn),
		fa:         c.FA,
		stdDiffers: dm.stdDiffers(c.FA.FilePath, content),
		fromRecord: true,
	}, true
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
		sp := dm.storedFor(root, pk.entry, dm.openTexts(root, "", ""))
		if sp == nil {
			return nil
		}
		if _, ok := sp.rec.Files[key]; !ok {
			continue
		}
		c := sp.check(dm, key)
		if !c.clean() {
			return nil
		}
		c.FA.TypeErrors = nil
		return &DocSnapshot{
			URI:             uri,
			Content:         sp.texts[key],
			Text:            sp.texts[key],
			Nodes:           ownNodesOf(c.FA, c.Nodes, key == sp.rec.Entry, sp.entryOwn),
			Analysis:        c.FA,
			Version:         1,
			AnalyzedVersion: 1,
			StdDiffers:      dm.stdDiffers(path, sp.texts[key]),
		}
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
func (dm *DocumentManager) NoteLoweredClean(uri, content string) {
	dm.lockBuild(false)
	defer dm.analyzeMu.Unlock()
	if sp, key := dm.storedOf(uri); sp != nil {
		if sp.rec.Lowered[key] || sp.texts[key] != content || !dm.storedCurrent(sp, dm.openTexts(sp.rec.Root, "", "")) {
			return
		}
		rec := *sp.rec
		rec.Lowered = map[string]bool{key: true}
		for k := range sp.rec.Lowered {
			rec.Lowered[k] = true
		}
		sp.rec = &rec
		sp.cache.Store([]progcache.Tree{{Dir: rec.Root}}, nil, rec.encode())
		return
	}
	path := fileuri.ToPath(uri)
	root := dm.projectRootFromURI(uri)
	docKey := relModuleKey(root, path)
	for _, entry := range dm.programEntries(root, path) {
		kept := dm.programs[programKey{root, entry}]
		if kept == nil || kept.prog.consumed || kept.prog.Files[docKey] == nil {
			continue
		}
		if kept.prog.Texts[docKey] == content {
			dm.keepStored(kept, docKey)
		}
		return
	}
}

// keepStored writes the record of kept's program, listing the document
// with module key lowered among those lowered, when the program has
// nothing to report and every file holds on disk the text it read. The
// caller holds analyzeMu.
func (dm *DocumentManager) keepStored(kept *rootProgram, lowered string) {
	p := kept.prog
	if p.factsFrom != nil || !storable(p) || kept.manifest != manifestText(p.Root) {
		return
	}
	entryPath := filepath.Join(p.Root, filepath.FromSlash(p.Entry)+".nomi")
	// The snapshot is taken before the texts are compared with the disk,
	// so a file written after the comparison is one the store sees moved.
	e := progcache.Open(storedKind, entryPath, []progcache.Tree{{Dir: p.Root}})
	if e == nil {
		return
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
	for key, si := range stored {
		rec.Files[key] = storedFile{Iface: si, Sum: sha256.Sum256([]byte(p.Texts[key]))}
	}
	if was, err := decodeStoredRecord(e.Previous()); err == nil && sameSums(was, rec) {
		for k := range was.Lowered {
			rec.Lowered[k] = true
		}
	}
	if payload := rec.encode(); payload != nil {
		e.Store([]progcache.Tree{{Dir: p.Root}}, nil, payload)
		dm.programCounts.stored.Add(1)
	}
}

// storable reports whether p is a program a record is kept for: nothing
// to report in any file, and nothing read that the record's walk of the
// root does not see.
func storable(p *CheckedProgram) bool {
	if p.unread || p.externs || len(p.Inputs) > 0 || len(p.checks.frontEnd) > 0 || p.Root == "" {
		return false
	}
	for key := range p.Files {
		if len(p.Syntax[key]) > 0 || len(p.damaged[key]) > 0 || len(documentErrors(p, key)) > 0 {
			return false
		}
		for _, n := range p.Nodes[key] {
			// An `embed` reads files the record's walk of the root does
			// not track.
			if o, ok := n.(*ast.OnceBinding); ok && o.Expansion != nil && o.Expansion.Keyword == "embed" {
				return false
			}
		}
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
