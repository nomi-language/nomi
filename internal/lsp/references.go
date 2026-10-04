package lsp

import (
	"context"
	"github.com/nomi-language/nomi/internal/analysis"
	"os"
	"path/filepath"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// findNomiFiles walks a project directory and returns its .nomi file
// paths, leaving out the directories below root the workspace scan leaves
// out (analysis.SkipWorkspaceDir).
func findNomiFiles(root string) []string {
	var files []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != root && analysis.SkipWorkspaceDir(d.Name()) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".nomi") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// symIdentity is the unique identity of a definition: name + file + position.
type symIdentity struct {
	Name string
	File string // absolute file path where the symbol is defined
	Pos  analysis.Pos
}

// symbolIdentity extracts the identity of a symbol's definition.
// Follows Resolved pointers and uses SourceFile when set.
func symbolIdentity(sym *analysis.Symbol) symIdentity {
	s := sym
	if s.Resolved != nil {
		s = s.Resolved
	}
	return symIdentity{
		Name: s.Name,
		File: s.SourceFile,
		Pos:  s.Pos,
	}
}

// matchesIdentity checks if a symbol refers to the same definition.
func matchesIdentity(sym *analysis.Symbol, id symIdentity) bool {
	s := sym
	if s.Resolved != nil {
		s = s.Resolved
	}
	if s.Name != id.Name || s.Pos != id.Pos {
		return false
	}
	// SourceFile may be empty for locally-defined symbols — treat empty as matching any file
	if s.SourceFile == "" || id.File == "" {
		return true
	}
	return s.SourceFile == id.File
}

func (s *Server) textDocumentReferences(ctx *glsp.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil {
		return nil, nil
	}

	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  utf16ToByteCol(doc.Content, params.Position.Line, params.Position.Character),
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		return nil, nil
	}

	// Resolve to real symbol for identity
	target := sym
	if target.Resolved != nil {
		target = target.Resolved
	}
	var ifaceMethodImplLocations []protocol.Location
	if target.Kind == analysis.SymbolInterfaceMethod {
		ifaceName := s.findInterfaceForMethod(doc.Analysis, target)
		if ifaceName != "" {
			ifaceMethodImplLocations = s.implLocationsForMethod(doc.Analysis, ifaceName, target.Name, uri)
		}
	}

	// Determine project root and identity. Walks upward looking for
	// nomi.toml or main.nomi so that a file in a sub-directory of a
	// project finds references across the whole tree, not just its
	// immediate parent.
	currentPath := uriToPath(uri)
	projectRoot := s.docs.FindProjectRoot(currentPath)
	// Search all .nomi files in the project, open docs' analysis first.
	targets := symbolTargets(sym, currentPath)
	files := loadProjectFiles(s.requestContext(ctx), s.docs, projectRoot, targets[0].sym.Name)

	var locations []protocol.Location
	seen := map[protocol.Location]bool{}
	appendLoc := func(loc protocol.Location) {
		if !seen[loc] {
			seen[loc] = true
			locations = append(locations, loc)
		}
	}

	for _, f := range files {
		fileURI := pathToURI(f.path)
		fa := f.fa

		// Search references. Skip synthesized positions (the @derive
		// synthesis band, `IsSynthesizedLine`); those refs sit at line
		// numbers >= 1<<30 and would surface as phantom Locations past
		// EOF in the editor.
		for refPos, refSym := range fa.References {
			if analysis.IsSynthesizedLine(refPos.Line) {
				continue
			}
			if matchesAnyTarget(refSym, targets) {
				appendLoc(makeLocation(fileURI, refPos, refSym.Name))
			}
		}
		// A punned label (`{context}`) references its field as well as the
		// variable References holds there.
		for labelPos, field := range fa.PunnedFieldLabels {
			if !analysis.IsSynthesizedLine(labelPos.Line) && matchesAnyTarget(field, targets) {
				appendLoc(makeLocation(fileURI, labelPos, field.Name))
			}
		}

		// Search definitions if includeDeclaration is set. Same synth-band
		// guard as the References loop above — synthesized impl FuncDef
		// definitions also live in the >= 1<<30 line band.
		if params.Context.IncludeDeclaration {
			for defPos, defSym := range fa.Definitions {
				if analysis.IsSynthesizedLine(defPos.Line) {
					continue
				}
				if matchesAnyTarget(defSym, targets) {
					appendLoc(makeLocation(fileURI, defPos, defSym.Name))
				}
			}
		}
	}
	for _, loc := range ifaceMethodImplLocations {
		appendLoc(loc)
	}

	return s.toUTF16Locations(locations), nil
}

// projectFile is one .nomi file of the project with its analysis (the open
// document's when it is open) and the text that analysis was built from.
type projectFile struct {
	path, content string
	fa            *analysis.FileAnalysis
}

// loadProjectFiles returns the analyses of the project's files under root
// that can mention name: every open document, and each closed file whose
// text contains name. A reference or a definition of a symbol sits on a
// token spelled as its name, so a closed file without that text has none,
// and is never analyzed. Closed files' analyses come from the manager's
// bounded cache. It stops, returning nil, when ctx ends.
func loadProjectFiles(ctx context.Context, dm *analysis.DocumentManager, root, name string) []projectFile {
	var files []projectFile
	for _, filePath := range findNomiFiles(root) {
		if ctx.Err() != nil {
			return nil
		}
		uri := pathToURI(filePath)
		if !dm.IsOpen(uri) {
			data, err := os.ReadFile(filePath)
			if err != nil || !strings.Contains(string(data), name) {
				continue
			}
		}
		snap := dm.Analyzed(uri)
		if snap != nil && snap.Analysis != nil {
			files = append(files, projectFile{filePath, snap.Content, snap.Analysis})
		}
	}
	return files
}

// symbolTarget is one declaration that references and rename look for, with
// its identity.
type symbolTarget struct {
	sym *analysis.Symbol
	id  symIdentity
}

// symbolTargets returns the declaration the symbol at a cursor stands for.
// currentPath is the file the cursor is in, the home of a symbol with no
// SourceFile.
func symbolTargets(sym *analysis.Symbol, currentPath string) []symbolTarget {
	t := symbolTarget{sym: sym, id: symbolIdentity(sym)}
	if t.sym.Resolved != nil {
		t.sym = t.sym.Resolved
	}
	if t.id.File == "" {
		t.id.File = currentPath
	}
	return []symbolTarget{t}
}

func matchesAnyTarget(sym *analysis.Symbol, targets []symbolTarget) bool {
	for _, t := range targets {
		if matchesSymbol(sym, t.sym, t.id) {
			return true
		}
	}
	return false
}

// matchesSymbol checks if a symbol in a file matches the target.
// For same-file analysis, uses pointer equality. For cross-file, uses identity matching.
func matchesSymbol(candidate, target *analysis.Symbol, id symIdentity) bool {
	// Pointer equality works when symbols come from the same analysis
	if candidate == target {
		return true
	}
	// Follow Resolved pointer
	c := candidate
	if c.Resolved != nil {
		c = c.Resolved
	}
	if c == target {
		return true
	}
	// Cross-file: match by identity
	return matchesIdentity(candidate, id)
}

func makeLocation(uri string, pos analysis.Pos, name string) protocol.Location {
	startLine := toZeroBased(pos.Line)
	startChar := toZeroBased(pos.Col)
	return protocol.Location{
		URI: protocol.DocumentUri(uri),
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: startLine, Character: startChar + uint32(len(name))},
		},
	}
}
