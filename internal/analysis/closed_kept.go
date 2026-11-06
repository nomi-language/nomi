package analysis

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// A closed file of a program the manager keeps for its open documents
// (document_programs.go) already has an analysis there: the program's
// check built and checked every file it reaches. A request that needs a
// closed file's analysis (references and rename read every file that can
// mention a name; the closed files' diagnostics pass reads each) takes
// that one instead of building the file's project again, which at 50k
// lines cost about a second per file: references to a function 137 files
// call took 149 s.
//
// It is the analysis a build of the closed file (buildAnalysis) makes
// when the build would read what the program read: the first entry
// candidate that reaches the file is the program's entry, the closed
// files still hold the texts the program read, each open document's
// analyzed text is the one the program read, the entry is on disk as the
// program read it (the build reads the entry from disk), and no file has
// a syntax error (the build parses a closed file with ParseWithRecovery,
// the program resiliently). Its diagnostics are those the file would
// carry if it were opened (documentErrors), which are those its own
// build reports (TestOpenDocumentEditsMatchFreshBuilds).
//
// An open entry whose text is not the one on disk (an unsaved edit) leaves
// a closed file to the program when the edit cannot reach it
// (entryEditReaches): the two texts import the same files and bring the
// same impls, declare no `export fn` and no keyword declaration, the
// program declares no application type, the file does not import the
// entry, directly or through others, and its analysis holds nothing of the
// entry. A build of the file with the entry's text on disk then makes what
// the program made of it, as a recheck keeps a file that does not import an
// edited one (recheckDependents). Before, references after an unsaved edit
// of the entry built each closed file on its own: 104 s at 50k lines,
// where the program answers in 0.12 s.

// keptClosed is the snapshot of the closed file at uri from a kept
// program, or nil when no kept program answers it as a build of the file
// would. The caller holds analyzeMu, which keeps rechecks from rewriting
// the program while it is read; the caller of the snapshot reads its
// analysis under the view lock, as an open document's.
func (dm *DocumentManager) keptClosed(uri string) *DocSnapshot {
	path := fileuri.ToPath(uri)
	if dm.IsOpen(uri) || dm.isStdlibFile(uri) || !filepath.IsAbs(path) || filepath.Ext(path) != ".nomi" {
		return nil
	}
	root := dm.projectRootFromURI(uri)
	key := relModuleKey(root, path)
	if key == "" || strings.HasPrefix(key, "..") || IsStdlibKey(key) || len(dm.syntheticExternsForRoot(root)) > 0 {
		return nil
	}
	for _, entryPath := range dm.entryCandidates(root) {
		if entryPath == path {
			continue
		}
		if _, err := os.Stat(entryPath); err != nil {
			continue
		}
		kept := dm.programs[programKey{root, relModuleKey(root, entryPath)}]
		if kept == nil || kept.prog.consumed || kept.manifest != manifestText(root) {
			return nil
		}
		p := kept.prog
		if p.Files[key] == nil {
			// The build would go on to the next candidate only if this
			// one does not reach the file, which the kept program says.
			continue
		}
		ok, disk := dm.readsAsBuilt(p, entryPath)
		if !ok || disk != nil && dm.entryEditReaches(p, key, *disk) {
			return nil
		}
		if disk != nil {
			dm.programCounts.closedUnsaved.Add(1)
		}
		return &DocSnapshot{
			URI:             uri,
			Content:         p.Texts[key],
			Text:            p.Texts[key],
			Nodes:           ownNodes(p, key),
			Analysis:        p.Files[key],
			Version:         1,
			AnalyzedVersion: 1,
			StdDiffers:      dm.stdDiffers(path, p.Texts[key]),
			diagnostics:     documentErrors(p, key),
			fromProgram:     true,
		}
	}
	return nil
}

// readsAsBuilt reports whether a build of one of p's closed files would
// read what p's check read: see the comment above. When the open entry's
// text is not the one on disk and everything else is as p read it, it
// reports true with the entry's text on disk, which the caller holds to
// entryEditReaches.
func (dm *DocumentManager) readsAsBuilt(p *CheckedProgram, entryPath string) (bool, *string) {
	if p.unread || p.externs || len(p.Inputs) > 0 || len(p.checks.frontEnd) > 0 {
		return false, nil
	}
	for key := range p.Files {
		if len(p.Syntax[key]) > 0 || len(p.damaged[key]) > 0 {
			return false, nil
		}
	}
	var disk *string
	open := dm.openTexts(p.Root, "", "")
	for key, was := range p.Texts {
		now, ok := open[key]
		path := filepath.Join(p.Root, filepath.FromSlash(key)+".nomi")
		if !ok || path == entryPath {
			data, err := dm.readProjectFile(path)
			if err != nil {
				return false, nil
			}
			if ok && now != data {
				// The open entry's text is not the one on disk, which the
				// build reads.
				if now != was {
					return false, nil
				}
				disk = &data
				continue
			}
			now = data
		}
		if now != was {
			return false, nil
		}
	}
	return true, disk
}

// entryEditReaches reports whether a build of p's closed file with key,
// which reads p's entry from disk, where its text is disk, may make
// another analysis of the file than p's check, which read the open entry's
// text: see the comment above.
func (dm *DocumentManager) entryEditReaches(p *CheckedProgram, key, disk string) bool {
	if prog := p.Files[p.Entry].Program(); len(prog.AppTypes) > 0 || len(prog.Errors) > 0 {
		// The entry's boots decide which types are application types, and
		// the program's own errors may name positions in the entry.
		return true
	}
	e := p.entryEdit
	if e == nil || e.disk != disk {
		e = &entryEdit{disk: disk, reaches: entryTextsDiffer(disk, p.Texts[p.Entry])}
		if !e.reaches {
			// The files that import the entry, directly or through others.
			importers := map[string][]string{}
			for k, fa := range p.Files {
				for _, imported := range fa.ImportedModules {
					importers[imported] = append(importers[imported], k)
				}
			}
			e.importers = map[string]bool{p.Entry: true}
			queue := []string{p.Entry}
			for len(queue) > 0 {
				k := queue[0]
				queue = queue[1:]
				for _, importer := range importers[k] {
					if !e.importers[importer] {
						e.importers[importer] = true
						queue = append(queue, importer)
					}
				}
			}
			e.impls = map[ast.Node]bool{}
			for _, n := range p.Nodes[p.Entry] {
				if blk, ok := n.(*ast.ImplBlock); ok {
					for _, fn := range implBlockFuncDefs(blk) {
						e.impls[fn] = true
					}
					for _, ext := range implBlockExternFuncs(blk) {
						e.impls[ext] = true
					}
				}
			}
		}
		p.entryEdit = e
	}
	if e.reaches || e.importers[key] {
		return true
	}
	fa := p.Files[key]
	return fa == nil || holdsFrom(fa, map[string]bool{p.Entry: true}, map[string]bool{p.Files[p.Entry].FilePath: true, filepath.Join(p.Root, filepath.FromSlash(p.Entry)+".nomi"): true}, e.impls)
}

// entryEdit is what entryEditReaches works out of a kept program once per
// text of its entry on disk.
type entryEdit struct {
	disk string
	// reaches says the two texts of the entry differ in more than bodies a
	// file that does not import the entry can read nothing of.
	reaches bool
	// importers are the files that import the entry, directly or through
	// others, the entry among them, and impls the entry's impl functions.
	importers map[string]bool
	impls     map[ast.Node]bool
}

// entryTextsDiffer reports whether two texts of an entry differ in what a
// file that does not import it may read: the files they import, the impls
// they bring, an `export fn` (whose names every file's check reads) or a
// keyword declaration (whose expansion and agreement read other files), or
// a syntax error.
func entryTextsDiffer(a, b string) bool {
	pa, pb := parseFile(a), parseFile(b)
	if len(pa.errs)+len(pa.damaged)+len(pb.errs)+len(pb.damaged) > 0 {
		return true
	}
	for _, pf := range []*parsedFile{pa, pb} {
		if exportsFunctions(pf.nodes) || expandsKeywords(pf.nodes) {
			return true
		}
	}
	return pa.implFingerprint != pb.implFingerprint || !reflect.DeepEqual(walkImports(pa.nodes), walkImports(pb.nodes))
}

// ClosedPastUnsavedEntry counts the closed files a kept program answered
// while its open entry's text was not the one on disk.
func (dm *DocumentManager) ClosedPastUnsavedEntry() int64 {
	return dm.programCounts.closedUnsaved.Load()
}
