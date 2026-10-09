package lsp

import (
	"context"
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// buildRenameEdits returns TextEdits for every occurrence (definition + reference)
// of the target symbol within a single file analysis.
//
// A punned struct-literal field (`{context}`) is one token that is both the
// field's label and a read of the variable, so renaming either one unpuns
// it: the field to `sink: context`, the variable to `context: sink`.
//
// An occurrence in a synthesized body (a derive or the universal Debug
// impl, analysis.IsSynthesizedLine) is not in the text and is not edited.
func buildRenameEdits(fa *analysis.FileAnalysis, target *analysis.Symbol, id symIdentity, newName string) []protocol.TextEdit {
	var edits []protocol.TextEdit
	for pos, sym := range fa.References {
		if !analysis.IsSynthesizedLine(pos.Line) && matchesSymbol(sym, target, id) {
			if _, punned := fa.PunnedFieldLabels[pos]; punned {
				edits = append(edits, makeTextEdit(pos, sym.Name, sym.Name+": "+newName))
				continue
			}
			edits = append(edits, makeTextEdit(pos, sym.Name, newName))
		}
	}
	for pos, field := range fa.PunnedFieldLabels {
		if !analysis.IsSynthesizedLine(pos.Line) && matchesSymbol(field, target, id) {
			edits = append(edits, makeTextEdit(pos, field.Name, newName+": "+field.Name))
		}
	}
	for pos, sym := range fa.Definitions {
		if !analysis.IsSynthesizedLine(pos.Line) && matchesSymbol(sym, target, id) {
			edits = append(edits, makeTextEdit(pos, sym.Name, newName))
		}
	}
	return edits
}

func makeTextEdit(pos analysis.Pos, name, newName string) protocol.TextEdit {
	startLine := toZeroBased(pos.Line)
	startChar := toZeroBased(pos.Col)
	return protocol.TextEdit{
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startChar},
			End:   protocol.Position{Line: startLine, Character: startChar + uint32(len(name))},
		},
		NewText: newName,
	}
}

// collectWorkspaceRename scans .nomi files in the project directory, gathers every
// occurrence of the symbol at `pos` in `currentURI`, and returns a WorkspaceEdit
// that rewrites each to `newName`. Returns nil if no symbol sits at the position.
func collectWorkspaceRename(ctx context.Context, dm *analysis.DocumentManager, currentURI string, pos analysis.Pos, newName string) *protocol.WorkspaceEdit {
	doc := dm.Snapshot(currentURI)
	if doc == nil || doc.Analysis == nil {
		return nil
	}

	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		return nil
	}

	currentPath := uriToPath(currentURI)
	projectRoot := dm.FindProjectRoot(currentPath)

	targets := symbolTargets(sym, currentPath)
	files := loadProjectFiles(ctx, dm, projectRoot, targets[0].sym.Name)

	changes := map[protocol.DocumentUri][]protocol.TextEdit{}
	for _, f := range files {
		fileURI := pathToURI(f.path)
		fa, content := f.fa, f.content
		var edits []protocol.TextEdit
		seen := map[protocol.Range]bool{}
		for _, t := range targets {
			for _, e := range buildRenameEdits(fa, t.sym, t.id, newName) {
				if !seen[e.Range] {
					seen[e.Range] = true
					edits = append(edits, e)
				}
			}
		}
		if len(edits) > 0 {
			// Convert each edit's byte-column range to UTF-16 so a rename of a
			// symbol that sits after a non-ASCII character on its line replaces
			// the correct span (a byte-based range would corrupt the edit).
			if content != "" {
				lines := newLineIndex(content)
				for i := range edits {
					edits[i].Range = lines.utf16Range(edits[i].Range)
				}
			}
			changes[protocol.DocumentUri(fileURI)] = edits
		}
	}

	if len(changes) == 0 {
		return nil
	}

	return &protocol.WorkspaceEdit{Changes: changes}
}

// textDocumentRename refuses what prepareRename refuses (renameTarget)
// and a new name that is not one identifier.
func (s *Server) textDocumentRename(gctx *glsp.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil || doc.Analysis == nil {
		return nil, nil
	}
	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  s.lines.get(uri, doc.Content).byteCol(params.Position.Line, params.Position.Character),
	}
	if _, _, err := renameTarget(s.docs, doc, pos); err != nil {
		return nil, err
	}
	if !isImportSegment(params.NewName) {
		return nil, fmt.Errorf("'%s' is not a name", params.NewName)
	}
	return collectWorkspaceRename(s.requestContext(gctx), s.docs, uri, pos, params.NewName), nil
}
