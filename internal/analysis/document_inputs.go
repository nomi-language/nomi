package analysis

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// The language server's side of expansion inputs (expansion.go). A program
// the DocumentManager keeps records every input its expansions read
// (CheckedProgram.Inputs). The next analysis that reaches the program reads
// each again, from an open editor buffer (SetInputOverlay) or the disk, and
// a changed one is an edit to the files whose expansions read it, with
// their text unchanged (programFor): the recheck re-expands them and takes
// the paths an edit to their own text would, so a catalog edit that keeps
// every message's signature rebuilds the declaring file's bodies alone.

// inputOverlays holds the text of the inputs open in an editor, by path.
type inputOverlays struct {
	mu    sync.RWMutex
	texts map[string]string
	// read holds the path of every input a program read (IsInput).
	read sync.Map
}

// SetInputOverlay makes text the content of the input file at path, as an
// editor holds it; ClearInputOverlay returns the input to the disk's.
func (dm *DocumentManager) SetInputOverlay(path, text string) {
	dm.inputs.mu.Lock()
	defer dm.inputs.mu.Unlock()
	if dm.inputs.texts == nil {
		dm.inputs.texts = map[string]string{}
	}
	dm.inputs.texts[filepath.Clean(path)] = text
}

// ClearInputOverlay forgets an editor's text of the input at path.
func (dm *DocumentManager) ClearInputOverlay(path string) {
	dm.inputs.mu.Lock()
	defer dm.inputs.mu.Unlock()
	delete(dm.inputs.texts, filepath.Clean(path))
}

// readInput reads an input: an editor's text, else the disk's.
func (dm *DocumentManager) readInput(path string) (string, bool) {
	dm.inputs.read.Store(filepath.Clean(path), true)
	dm.inputs.mu.RLock()
	text, open := dm.inputs.texts[filepath.Clean(path)]
	dm.inputs.mu.RUnlock()
	if open {
		return text, true
	}
	return DiskInputs(path)
}

// expansionSite is the site of the file with module key under root, whose
// manifest is m, reading inputs with read.
func (dm *DocumentManager) expansionSite(root, key string, m *Manifest, read InputReader) *ExpansionSite {
	s := &ExpansionSite{ModulePath: key, Manifest: m, Read: read}
	if m != nil {
		s.Root = root
	}
	return s
}

// expandLoaded expands the keyword declarations of a project file the
// language server loaded from projectRoot.
func (dm *DocumentManager) expandLoaded(projectRoot string, modulePath []string, nodes []ast.Node) []ast.Node {
	m, _ := LoadManifest(projectRoot)
	return ExpandDeclarations(nodes, dm.expansionSite(projectRoot, strings.Join(modulePath, "/"), m, dm.readInput))
}

// expandDocument expands the keyword declarations of an open document at
// docPath under root.
func (dm *DocumentManager) expandDocument(root, docPath string, nodes []ast.Node) []ast.Node {
	m, _ := LoadManifest(root)
	return ExpandDeclarations(nodes, SiteFor(root, docPath, m, dm.readInput))
}

// IsInput reports whether a program the manager keeps read the file at
// path as an input of an expansion: a catalog.
func (dm *DocumentManager) IsInput(path string) bool {
	_, ok := dm.inputs.read.Load(filepath.Clean(path))
	return ok
}

// recordingInputs is a reader over dm's inputs that records each read in
// p.Inputs, under mu.
func (dm *DocumentManager) recordingInputs(p *CheckedProgram, mu *sync.Mutex) InputReader {
	return func(path string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if in, ok := p.Inputs[path]; ok {
			return in.Text, in.Found
		}
		text, found := dm.readInput(path)
		p.Inputs[path] = ExpansionInput{Path: path, Text: text, Found: found}
		return text, found
	}
}

// changedInputs reads p's inputs again and answers the module keys of the
// files whose expansions read one that changed, sorted, recording each
// input's new text in p.Inputs.
func (dm *DocumentManager) changedInputs(p *CheckedProgram) []string {
	changed := map[string]bool{}
	for path, old := range p.Inputs {
		text, found := dm.readInput(path)
		if text == old.Text && found == old.Found {
			continue
		}
		p.Inputs[path] = ExpansionInput{Path: path, Text: text, Found: found}
		changed[path] = true
	}
	if len(changed) == 0 {
		return nil
	}
	var keys []string
	for key, nodes := range p.Nodes {
		for _, in := range ExpansionInputs(nodes) {
			if changed[in.Path] {
				keys = append(keys, key)
				break
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// InputsChanged re-analyzes every open document after an input file
// changed on disk, as rechecks that read the input again, and answers the
// open documents and the closed files under the input's project root,
// whose cached analyses are dropped.
func (dm *DocumentManager) InputsChanged(path string) (open []*Document, closed []string) {
	dm.mu.Lock()
	dm.closed.clear()
	for _, doc := range dm.docs {
		open = append(open, doc)
	}
	for uri, entry := range dm.index {
		if _, isOpen := dm.docs[uri]; !isOpen && entry.Root != "" && strings.HasPrefix(filepath.Clean(path), filepath.Clean(entry.Root)+string(filepath.Separator)) {
			closed = append(closed, uri)
		}
	}
	dm.mu.Unlock()
	sort.Strings(closed)
	for _, doc := range open {
		dm.analyze(doc)
	}
	return open, closed
}
