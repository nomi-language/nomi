package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/parser"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// buildParseFixActions offers the fix a parse error carries (parser.Fix)
// for each diagnostic in diags that is that error:
//
//   - "Use `//` comments" for a `///` or `//!` run inside a body, which
//     turns each of its markers into `//`.
//   - the text edits of a parse or type error written as another language
//     writes it (parser.Fix.Edits): `let`, `match`, `xs.len()`, `nil`.
func buildParseFixActions(content, uri string, errs []parser.ParseError, diags []protocol.Diagnostic) []protocol.CodeAction {
	var lines *lineIndex
	var actions []protocol.CodeAction
	for _, d := range diags {
		if lines == nil {
			lines = newLineIndex(content)
		}
		e, ok := parseErrorAt(lines, errs, d)
		if !ok {
			continue
		}
		var title string
		var edits []protocol.TextEdit
		switch {
		case len(e.Fix.Markers) > 0:
			for _, m := range e.Fix.Markers {
				text, ok := lines.line(uint32(m.Line - 1))
				if !ok || m.Col < 1 || m.Col+2 > len(text) || (text[m.Col-1:m.Col+2] != "///" && text[m.Col-1:m.Col+2] != "//!") {
					edits = nil
					break
				}
				edits = append(edits, protocol.TextEdit{
					Range:   lines.utf16Range(posRange(m.Line, m.Col, m.Line, m.Col+3)),
					NewText: "//",
				})
			}
			if edits == nil {
				continue
			}
			title = "Use `//` comments"
		case len(e.Fix.Edits) > 0:
			title = e.Fix.Title
			edits = fixTextEdits(lines, e.Fix.Edits)
		default:
			continue
		}
		kind := protocol.CodeActionKindQuickFix
		preferred := true
		actions = append(actions, protocol.CodeAction{
			Title:       title,
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{d},
			IsPreferred: &preferred,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(uri): edits,
				},
			},
		})
	}
	return actions
}

// fixTextEdits are edits as the protocol's text edits.
func fixTextEdits(lines *lineIndex, edits []parser.Edit) []protocol.TextEdit {
	out := make([]protocol.TextEdit, len(edits))
	for i, e := range edits {
		out[i] = protocol.TextEdit{
			Range:   lines.utf16Range(posRange(e.Start.Line, e.Start.Col, e.End.Line, e.End.Col)),
			NewText: e.Text,
		}
	}
	return out
}

// typeErrorFixes are the errors of errs that carry a fix, as parse errors
// at their start, so buildParseFixActions offers each fix the checker
// knows: the rewrite of a method call, `nil`, a list literal where a
// vector is expected.
func typeErrorFixes(errs []analysis.TypeError) []parser.ParseError {
	var out []parser.ParseError
	for _, e := range errs {
		if e.Fix != nil && e.File == "" {
			out = append(out, parser.ParseError{Line: e.Line, Col: e.Col, Message: e.Message, Fix: e.Fix})
		}
	}
	return out
}

// parseErrorAt is the parse error with a fix that diagnostic d reports: the
// same headline at the same start.
func parseErrorAt(lines *lineIndex, errs []parser.ParseError, d protocol.Diagnostic) (parser.ParseError, bool) {
	head := diagnosticHeadline(d.Message)
	line := int(d.Range.Start.Line) + 1
	col := lines.byteCol(d.Range.Start.Line, d.Range.Start.Character)
	for _, e := range errs {
		if e.Fix != nil && e.Line == line && e.Col == col && e.Message == head {
			return e, true
		}
	}
	return parser.ParseError{}, false
}

// posRange is the protocol range between two 1-based byte positions.
func posRange(startLine, startCol, endLine, endCol int) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: uint32(startLine - 1), Character: uint32(startCol - 1)},
		End:   protocol.Position{Line: uint32(endLine - 1), Character: uint32(endCol - 1)},
	}
}
