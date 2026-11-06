package analysis

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/fileuri"
)

// References from a record.
//
// References and rename read every closed file whose text names the
// symbol (internal/lsp, loadProjectFiles). In a program opened from its
// record each such file was checked against interfaces when asked for:
// 1.8 s at 50k lines for a function 137 files call. So the record keeps,
// for each file, what those requests read of its analysis (StoredRefs):
// each reference, punned field label and definition, by position, with
// the symbol there and the declaration it resolves to, named by name,
// source file, kind and position, as references match symbols across
// files. A closed file's references are answered from it while the file
// holds the record's text and no file a stored symbol is declared in has
// changed how its interface is written since the program was opened
// (storedProgram.changedAt), so every position a stored symbol holds is
// the program's now; a file an edit checked again drops its stored
// references.

// StoredRefs is what references and rename read of one file's analysis.
type StoredRefs struct {
	// Syms are the symbols the file's references, labels and definitions
	// name, each with its Resolved symbol by index (-1 for none).
	Syms []StoredRefSym
	// Refs, Punned and Defs are fa.References, fa.PunnedFieldLabels and
	// fa.Definitions, stand-in references and the synthesized band left
	// out.
	Refs, Punned, Defs []StoredRefAt
	// Files are the module keys of the project files the symbols are
	// declared in.
	Files []string
}

// StoredRefSym is one symbol of a StoredRefs.
type StoredRefSym struct {
	Name, SourceFile string
	Kind             SymbolKind
	Pos              Pos
	Resolved         int
}

// StoredRefAt is one position of a StoredRefs and the symbol there.
type StoredRefAt struct {
	Pos Pos
	Sym int
}

// storedRefsOf reads what references and rename read of fa, a file of the
// program under root.
func storedRefsOf(fa *FileAnalysis, root string) *StoredRefs {
	r := &StoredRefs{}
	index := map[*Symbol]int{}
	files := map[string]bool{}
	var add func(sym *Symbol) int
	add = func(sym *Symbol) int {
		if sym == nil {
			return -1
		}
		if i, ok := index[sym]; ok {
			return i
		}
		i := len(r.Syms)
		index[sym] = i
		r.Syms = append(r.Syms, StoredRefSym{Name: sym.Name, SourceFile: sym.SourceFile, Kind: sym.Kind, Pos: sym.Pos, Resolved: -1})
		if sym.SourceFile != "" {
			if key := relModuleKey(root, sym.SourceFile); key != "" && !strings.HasPrefix(key, "..") {
				files[key] = true
			}
		}
		res := add(sym.Resolved)
		r.Syms[i].Resolved = res
		return i
	}
	for pos, sym := range fa.References {
		if sym == nil || IsSynthesizedLine(pos.Line) || fa.StandInReference(pos) {
			continue
		}
		r.Refs = append(r.Refs, StoredRefAt{Pos: pos, Sym: add(sym)})
	}
	for pos, sym := range fa.PunnedFieldLabels {
		if sym == nil || IsSynthesizedLine(pos.Line) {
			continue
		}
		r.Punned = append(r.Punned, StoredRefAt{Pos: pos, Sym: add(sym)})
	}
	for pos, sym := range fa.Definitions {
		if sym == nil || IsSynthesizedLine(pos.Line) {
			continue
		}
		r.Defs = append(r.Defs, StoredRefAt{Pos: pos, Sym: add(sym)})
	}
	r.Files = mapKeys(files)
	return r
}

// analysis answers a FileAnalysis holding r's references, labels and
// definitions alone, for the file at path.
func (r *StoredRefs) analysis(path string) *FileAnalysis {
	syms := make([]*Symbol, len(r.Syms))
	for i, s := range r.Syms {
		syms[i] = &Symbol{Name: s.Name, SourceFile: s.SourceFile, Kind: s.Kind, Pos: s.Pos}
	}
	for i, s := range r.Syms {
		if s.Resolved >= 0 {
			syms[i].Resolved = syms[s.Resolved]
		}
	}
	fa := &FileAnalysis{FilePath: path}
	fill := func(at []StoredRefAt) map[Pos]*Symbol {
		m := make(map[Pos]*Symbol, len(at))
		for _, a := range at {
			m[a.Pos] = syms[a.Sym]
		}
		return m
	}
	fa.References, fa.PunnedFieldLabels, fa.Definitions = fill(r.Refs), fill(r.Punned), fill(r.Defs)
	return fa
}

// AnalyzedForReferences is Analyzed for references and rename, which read
// only a file's references, punned field labels and definitions: a closed
// file of a program opened from its record whose stored references hold
// (see above) is answered from them, with no check.
func (dm *DocumentManager) AnalyzedForReferences(uri string) *DocSnapshot {
	if !dm.IsOpen(uri) {
		if snap := dm.storedRefs(uri); snap != nil {
			return snap
		}
	}
	return dm.Analyzed(uri)
}

// storedRefs is the snapshot of the closed file at uri answered from its
// stored references, or nil.
func (dm *DocumentManager) storedRefs(uri string) *DocSnapshot {
	path := fileuri.ToPath(uri)
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	root := dm.projectRootFromURI(uri)
	key := relModuleKey(root, path)
	if key == "" || strings.HasPrefix(key, "..") {
		return nil
	}
	for _, entryPath := range dm.entryCandidates(root) {
		pk := programKey{root, relModuleKey(root, entryPath)}
		if dm.programs[pk] != nil {
			return nil
		}
		if _, err := os.Stat(entryPath); err != nil {
			continue
		}
		sp := dm.storedFor(root, pk.entry, "", dm.openTexts(root, "", ""))
		if sp == nil {
			return nil
		}
		f, ok := sp.rec.Files[key]
		if !ok {
			continue
		}
		if f.Refs == nil || sp.textAt[key] > 0 || sp.changedAt[key] > 0 {
			return nil
		}
		refs, err := decodeStoredRefs(f.Refs)
		if err != nil {
			return nil
		}
		for _, k := range refs.Files {
			if sp.changedAt[k] > 0 {
				return nil
			}
		}
		dm.programCounts.refsFromRecord.Add(1)
		text := sp.texts[key]
		return &DocSnapshot{URI: uri, Content: text, Text: text, Analysis: refs.analysis(filepath.Clean(path)), Version: 1, AnalyzedVersion: 1}
	}
	return nil
}

// RefsFromRecord counts the closed files whose references a record
// answered (AnalyzedForReferences).
func (dm *DocumentManager) RefsFromRecord() int64 { return dm.programCounts.refsFromRecord.Load() }

// storedRefsMagic heads an encoded StoredRefs.
const storedRefsMagic = "NOMIREFS1"

func (r *StoredRefs) encode() []byte { return gobEncode(storedRefsMagic, r) }

func decodeStoredRefs(data []byte) (*StoredRefs, error) {
	r := &StoredRefs{}
	if err := gobDecode(storedRefsMagic, data, r); err != nil {
		return nil, err
	}
	return r, nil
}
