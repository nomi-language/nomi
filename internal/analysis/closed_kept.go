package analysis

import (
	"os"
	"path/filepath"
	"strings"

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
		if !dm.readsAsBuilt(p, entryPath) {
			return nil
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
// read what p's check read: see the comment above.
func (dm *DocumentManager) readsAsBuilt(p *CheckedProgram, entryPath string) bool {
	if p.unread || p.externs || len(p.Inputs) > 0 || len(p.checks.frontEnd) > 0 {
		return false
	}
	for key := range p.Files {
		if len(p.Syntax[key]) > 0 || len(p.damaged[key]) > 0 {
			return false
		}
	}
	open := dm.openTexts(p.Root, "", "")
	for key, was := range p.Texts {
		now, ok := open[key]
		path := filepath.Join(p.Root, filepath.FromSlash(key)+".nomi")
		if !ok || path == entryPath {
			data, err := dm.readProjectFile(path)
			if err != nil {
				return false
			}
			if ok && now != data {
				// The open entry's text is not the one on disk, which the
				// build reads.
				return false
			}
			now = data
		}
		if now != was {
			return false
		}
	}
	return true
}
