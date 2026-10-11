package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/parser"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// buildParseFixActions offers the fix a parse error carries (parser.Fix)
// for each diagnostic in diags that is that error:
//
//   - "Put the arm's body in braces" (or the lambda's) for a body written
//     over several lines without braces. The braces go around the body
//     from after its `->` or `|` through its last line, which is indented
//     one level past the line the arm or lambda starts on, as `nomi fmt`
//     writes it.
//   - "Use `//` comments" for a `///` or `//!` run inside a body, which
//     turns each of its markers into `//`.
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
		case e.Fix.Wrap != nil:
			edit, ok := wrapBodyEdit(lines, *e.Fix.Wrap)
			if !ok {
				continue
			}
			title = "Put the arm's body in braces"
			if strings.Contains(e.Message, "lambda") {
				title = "Put the lambda's body in braces"
			}
			edits = []protocol.TextEdit{edit}
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

// wrapBodyEdit is the edit putting braces around w's body. The body's
// lines keep their indentation relative to one another, shifted to one
// level past the indentation of the line it starts after. When only
// whitespace separates the body from a `)` closing the call it is an
// argument of, the `}` goes right before the `)`.
func wrapBodyEdit(lines *lineIndex, w parser.BodyWrap) (protocol.TextEdit, bool) {
	first, ok := lines.line(uint32(w.After.Line - 1))
	if !ok || w.End.Line < w.After.Line {
		return protocol.TextEdit{}, false
	}
	indent := first[:len(first)-len(strings.TrimLeft(first, " \t"))]
	var segs []string
	for l := w.After.Line; l <= w.End.Line; l++ {
		text, ok := lines.line(uint32(l - 1))
		if !ok {
			return protocol.TextEdit{}, false
		}
		if l == w.End.Line {
			if w.End.Col-1 > len(text) {
				return protocol.TextEdit{}, false
			}
			text = text[:w.End.Col-1]
		}
		if l == w.After.Line {
			if w.After.Col-1 > len(text) {
				return protocol.TextEdit{}, false
			}
			text = text[w.After.Col-1:]
		}
		segs = append(segs, text)
	}
	ref := -1
	for _, s := range segs[1:] {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if n := len(s) - len(strings.TrimLeft(s, " \t")); ref < 0 || n < ref {
			ref = n
		}
	}
	inner := indent + "    "
	var out []string
	if head := strings.TrimSpace(segs[0]); head != "" {
		out = append(out, inner+head)
	}
	for _, s := range segs[1:] {
		if strings.TrimSpace(s) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, inner+strings.TrimRight(s[ref:], " \t"))
	}
	end := w.End
	if last, ok := lines.line(uint32(w.End.Line - 1)); ok && strings.TrimSpace(last[w.End.Col-1:]) == "" {
		// The `)` on a line of its own below the body comes up to the `}`.
		for l := w.End.Line + 1; ; l++ {
			text, ok := lines.line(uint32(l - 1))
			if !ok {
				break
			}
			trimmed := strings.TrimLeft(text, " \t")
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, ")") {
				end = parser.Pos{Line: l, Col: len(text) - len(trimmed) + 1}
			}
			break
		}
	}
	return protocol.TextEdit{
		Range:   lines.utf16Range(posRange(w.After.Line, w.After.Col, end.Line, end.Col)),
		NewText: " {\n" + strings.Join(out, "\n") + "\n" + indent + "}",
	}, true
}
