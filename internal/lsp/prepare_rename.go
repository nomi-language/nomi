package lsp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocumentPrepareRename answers the identifier under the cursor and
// its name as the placeholder when rename can rename it, and an error
// saying why otherwise (VS Code and Neovim show the message).
func (s *Server) textDocumentPrepareRename(_ *glsp.Context, params *protocol.PrepareRenameParams) (any, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil || doc.Analysis == nil {
		return nil, nil
	}
	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  utf16ToByteCol(doc.Content, params.Position.Line, params.Position.Character),
	}
	sym, r, err := renameTarget(s.docs, doc, pos)
	if err != nil {
		return nil, err
	}
	return protocol.RangeWithPlaceholder{Range: utf16RangeFromContent(doc.Content, r), Placeholder: sym.Name}, nil
}

var errNothingToRename = errors.New("nothing to rename here")

// renameTarget is the symbol rename renames from pos, and the byte range
// of the name under the cursor. It refuses a position with no symbol (a
// keyword, a literal, punctuation), a keyword's hover-only symbol, a name
// declared outside this project (the standard library, the prelude,
// another module), an implicit `it`, an interface method or a function
// implementing one, and a file's import qualifier, which is the file's
// name.
func renameTarget(dm *analysis.DocumentManager, doc *analysis.DocSnapshot, pos analysis.Pos) (*analysis.Symbol, protocol.Range, error) {
	fa := doc.Analysis
	at, sym, ok := nameAt(fa, pos)
	if !ok {
		return nil, protocol.Range{}, errNothingToRename
	}
	switch sym.Kind {
	case analysis.SymbolArgHint, analysis.SymbolAssertion, analysis.SymbolTestSetup, analysis.SymbolTestDecl,
		analysis.SymbolTryOp, analysis.SymbolControlFlow, analysis.SymbolImplKeyword:
		return nil, protocol.Range{}, errNothingToRename
	}
	r := makeLocation("", at, sym.Name).Range
	if textAt(doc.Content, r) != sym.Name || !isImportSegment(sym.Name) {
		return nil, protocol.Range{}, errNothingToRename
	}
	target := sym
	if target.Resolved != nil {
		target = target.Resolved
	}
	if target.Kind == analysis.SymbolModule && isImportPathSegment(doc.Nodes, target.Pos) {
		return nil, protocol.Range{}, fmt.Errorf("'%s' is part of an import path; rename the file to change it", target.Name)
	}
	path := uriToPath(doc.URI)
	if target.SourceFile != "" {
		root := dm.FindProjectRoot(path)
		if rel, err := filepath.Rel(root, target.SourceFile); err != nil || strings.HasPrefix(rel, "..") || dm.FindProjectRoot(target.SourceFile) != root {
			return nil, protocol.Range{}, fmt.Errorf("'%s' is declared outside this project", target.Name)
		}
	} else {
		// A symbol with no SourceFile is this file's own or the standard
		// library's. This file's is recorded at its own position, spelled
		// as its name: as a definition, or, for a field an anonymous struct
		// type declares (`{logger: String}`), as a reference to itself.
		def, isDef := fa.Definitions[target.Pos]
		isDef = isDef && (def == target || def.Name == target.Name)
		if !isDef && fa.References[target.Pos] != target {
			return nil, protocol.Range{}, fmt.Errorf("'%s' is declared in the standard library", target.Name)
		}
		if textAt(doc.Content, makeLocation("", target.Pos, target.Name).Range) != target.Name {
			return nil, protocol.Range{}, fmt.Errorf("'%s' is implicit; name the parameter to rename it", target.Name)
		}
	}
	// Rename edits one symbol's occurrences, and an interface method and
	// its implementations are separate symbols, so renaming either would
	// break the conformance.
	if target.Kind == analysis.SymbolInterfaceMethod {
		return nil, protocol.Range{}, fmt.Errorf("'%s' is an interface method; renaming it would not rename its implementations", target.Name)
	}
	if target.ImplInterface != "" {
		return nil, protocol.Range{}, fmt.Errorf("'%s' implements %s.%s; it must keep the interface's name", target.Name, target.ImplInterface, target.Name)
	}
	return sym, r, nil
}

// nameAt finds the reference or definition whose name covers pos.
func nameAt(fa *analysis.FileAnalysis, pos analysis.Pos) (analysis.Pos, *analysis.Symbol, bool) {
	covers := func(at analysis.Pos, name string) bool {
		return at.Line == pos.Line && pos.Col >= at.Col && pos.Col <= at.Col+len(name)
	}
	var best analysis.Pos
	var bestSym *analysis.Symbol
	consider := func(at analysis.Pos, sym *analysis.Symbol) {
		// The cursor may sit just past a name; prefer a name it is inside.
		if sym == nil || !covers(at, sym.Name) {
			return
		}
		if bestSym == nil || (pos.Col < at.Col+len(sym.Name) && best.Col+len(bestSym.Name) == pos.Col) {
			best, bestSym = at, sym
		}
	}
	for at, sym := range fa.Definitions {
		consider(at, sym)
	}
	for at, sym := range fa.References {
		consider(at, sym)
	}
	return best, bestSym, bestSym != nil
}

// textAt is the text of a single-line byte range of content.
func textAt(content string, r protocol.Range) string {
	if r.Start.Line != r.End.Line {
		return ""
	}
	offs := lineOffsets(content)
	if int(r.Start.Line) >= len(offs) {
		return ""
	}
	start := offs[r.Start.Line] + int(r.Start.Character)
	end := offs[r.Start.Line] + int(r.End.Character)
	if end > len(content) || start > end {
		return ""
	}
	return content[start:end]
}

// isImportPathSegment reports whether pos is a path segment of one of the
// file's imports (not an `as` alias).
func isImportPathSegment(nodes []ast.Node, pos analysis.Pos) bool {
	for _, imp := range importEntries(nodes) {
		for _, seg := range imp.ModulePath {
			if line, col := identPos(seg); line == pos.Line && col == pos.Col {
				return true
			}
		}
	}
	return false
}
