package lsp

import (
	"path/filepath"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Macros in the editor. A declaration a macro expanded is a
// declaration like any: what it expands to is checked with its file, so
// hover, definition and completion over the names it declares work as for
// any, and a definition of one lands on the declaration (expansionOrigin).
// Inside the declaration's body, the server asks the macro's
// Navigation impl, on the VM with the expansion's limits
// (analysis.MacroNavigate): hover, go to definition, completion and the
// quick fixes for the diagnostics it reported.

// macroDeclAt is the declaration of doc a macro expanded whose
// body holds pos, or nil.
func macroDeclAt(doc *analysis.DocSnapshot, pos analysis.Pos) *ast.KeywordDecl {
	if doc == nil {
		return nil
	}
	return analysis.MacroDeclAt(doc.Nodes, pos.Line, pos.Col)
}

// macroHover is the macro's hover inside a declaration's body.
func (s *Server) macroHover(doc *analysis.DocSnapshot, pos analysis.Pos) (*protocol.Hover, bool) {
	kd := macroDeclAt(doc, pos)
	if kd == nil {
		return nil, false
	}
	a := analysis.MacroNavigate(kd, "hover", pos.Line, pos.Col, nil)
	if a == nil || a.Hover == "" {
		return nil, false
	}
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: a.Hover}}, true
}

// macroDefinition is the macro's definition of a position inside
// a declaration's body: a span of the declaring file or of an input.
func (s *Server) macroDefinition(doc *analysis.DocSnapshot, pos analysis.Pos) (*protocol.Location, bool) {
	kd := macroDeclAt(doc, pos)
	if kd == nil {
		return nil, false
	}
	a := analysis.MacroNavigate(kd, "definition", pos.Line, pos.Col, nil)
	if a == nil || a.Definition == nil {
		return nil, false
	}
	path, ok := analysis.MacroSpanPath(kd, *a.Definition)
	if !ok {
		return nil, false
	}
	d := a.Definition
	return &protocol.Location{URI: protocol.DocumentUri(pathToURI(path)), Range: errorRange(d.Line, d.Col, d.EndLine, d.EndCol)}, true
}

// macroCompletions are the macro's completions inside a
// declaration's body.
func (s *Server) macroCompletions(doc *analysis.DocSnapshot, pos analysis.Pos) ([]protocol.CompletionItem, bool) {
	kd := macroDeclAt(doc, pos)
	if kd == nil {
		return nil, false
	}
	a := analysis.MacroNavigate(kd, "complete", pos.Line, pos.Col, nil)
	if a == nil {
		return nil, true
	}
	items := make([]protocol.CompletionItem, 0, len(a.Completions))
	for _, c := range a.Completions {
		detail, insert := c.Detail, c.Insert
		items = append(items, protocol.CompletionItem{Label: c.Label, Detail: &detail, InsertText: &insert})
	}
	return items, true
}

// macroActions are the quick fixes the macros that expanded the
// declarations of the open documents offer for diagnostics, which are in
// the file at uri: a declaring file or one of its inputs.
func (s *Server) macroActions(uri string, diagnostics []protocol.Diagnostic) []protocol.CodeAction {
	if len(diagnostics) == 0 {
		return nil
	}
	path := uriToPath(uri)
	var out []protocol.CodeAction
	for _, docURI := range s.docs.OpenURIs() {
		doc := s.docs.Snapshot(docURI)
		if doc == nil {
			continue
		}
		docPath := uriToPath(docURI)
		for _, n := range doc.Nodes {
			kd, ok := n.(*ast.KeywordDecl)
			if !ok {
				continue
			}
			for _, te := range analysis.MacroErrors(kd) {
				at := te.File
				if at == "" {
					at = docPath
				}
				if filepath.Clean(at) != filepath.Clean(path) {
					continue
				}
				for _, d := range diagnostics {
					// A published diagnostic carries the error's hints after
					// its message.
					if (d.Message != te.Message && !strings.HasPrefix(d.Message, te.Message+"\n")) || d.Range.Start.Line != uint32(max(te.Line-1, 0)) {
						continue
					}
					a := analysis.MacroNavigate(kd, "actions", te.Line, te.Col, &te)
					if a == nil {
						continue
					}
					for _, act := range a.Actions {
						if ca, ok := s.macroAction(kd, act, d); ok {
							out = append(out, ca)
						}
					}
				}
			}
		}
	}
	return out
}

// macroAction is act as a quick fix for d, which the declaration kd
// reported: its edits, each in the declaring module, and the command that
// runs its tool, if it names one (macro_tools.go).
func (s *Server) macroAction(kd *ast.KeywordDecl, act analysis.MacroAction, d protocol.Diagnostic) (protocol.CodeAction, bool) {
	changes := map[protocol.DocumentUri][]protocol.TextEdit{}
	for _, e := range act.Edits {
		path, ok := analysis.MacroSpanPath(kd, e.Span)
		if !ok {
			return protocol.CodeAction{}, false
		}
		target := protocol.DocumentUri(pathToURI(path))
		r := errorRange(e.Span.Line, e.Span.Col, e.Span.EndLine, e.Span.EndCol)
		if content, ok := s.contentForURI(target); ok && content != "" {
			r = newLineIndex(content).utf16Range(r)
		}
		changes[target] = append(changes[target], protocol.TextEdit{Range: r, NewText: e.Text})
	}
	kind := protocol.CodeActionKindQuickFix
	ca := protocol.CodeAction{
		Title:       act.Title,
		Kind:        &kind,
		Diagnostics: []protocol.Diagnostic{d},
	}
	if len(changes) > 0 {
		ca.Edit = &protocol.WorkspaceEdit{Changes: changes}
	}
	if act.Tool != nil {
		root, ok := analysis.MacroRoot(kd)
		_, module, named := analysis.MacroOf(kd)
		if !ok || !named || act.Tool.Name == "" {
			return protocol.CodeAction{}, false
		}
		ca.Command = s.tools.offer(act.Title, root, module, act.Tool.Name, act.Tool.Args)
	}
	if ca.Edit == nil && ca.Command == nil {
		return protocol.CodeAction{}, false
	}
	return ca, true
}

// expansionOrigin is where a declaration at pos in the band of a
// macro's expansion in the file at uri was made from: the declaration
// it expanded, or the span its map names in the file.
func (s *Server) expansionOrigin(uri protocol.DocumentUri, pos analysis.Pos) (analysis.Pos, bool) {
	if !analysis.IsExpansionLine(pos.Line) {
		return analysis.Pos{}, false
	}
	doc := s.docs.Snapshot(string(uri))
	if doc == nil {
		return analysis.Pos{}, false
	}
	line, col, ok := analysis.ExpansionTarget(doc.Nodes, pos.Line, pos.Col)
	if !ok {
		return analysis.Pos{}, false
	}
	return analysis.Pos{Line: line, Col: col}, true
}
