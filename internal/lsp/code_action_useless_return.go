package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// "Remove the `return`" answers the checker's error for a bare `return` at
// the end of a body. The diagnostic spans the keyword, and the fix is
// offered only when the text there is `return`:
//
//   - on a line of its own, the line goes;
//   - followed by a comment, the keyword goes and the comment stays;
//   - as a case arm's body (`0 -> return`), it becomes `{}`, since an arm
//     needs a body;
//   - inside a one-line branch (`if x { return } else { ... }`), the keyword
//     and the space before it go.
func buildUselessReturnActions(content, uri string, diags []protocol.Diagnostic) []protocol.CodeAction {
	var lines *lineIndex
	var actions []protocol.CodeAction
	for _, d := range diags {
		if !strings.HasPrefix(diagnosticHeadline(d.Message), analysis.UselessReturnPrefix+":") {
			continue
		}
		if d.Range.Start.Line != d.Range.End.Line {
			continue
		}
		if lines == nil {
			lines = newLineIndex(content)
		}
		text, ok := lines.line(d.Range.Start.Line)
		if !ok {
			continue
		}
		start := lines.byteCol(d.Range.Start.Line, d.Range.Start.Character) - 1
		end := start + len("return")
		if start < 0 || end > len(text) || text[start:end] != "return" {
			continue
		}
		edit, ok := uselessReturnEdit(d.Range, text[:start], text[end:])
		if !ok {
			continue
		}
		kind := protocol.CodeActionKindQuickFix
		preferred := true
		actions = append(actions, protocol.CodeAction{
			Title:       "Remove the `return`",
			Kind:        &kind,
			Diagnostics: []protocol.Diagnostic{d},
			IsPreferred: &preferred,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(uri): {edit},
				},
			},
		})
	}
	return actions
}

// uselessReturnEdit is the edit removing the `return` at r, given the text
// before and after it on its line.
func uselessReturnEdit(r protocol.Range, before, after string) (protocol.TextEdit, bool) {
	rest := strings.TrimSpace(after)
	switch {
	case strings.TrimSpace(before) == "" && rest == "":
		line := r.Start.Line
		return protocol.TextEdit{Range: protocol.Range{
			Start: protocol.Position{Line: line},
			End:   protocol.Position{Line: line + 1},
		}}, true
	case strings.TrimSpace(before) == "" && strings.HasPrefix(rest, "//"):
		gap := len(after) - len(strings.TrimLeft(after, " \t"))
		end := r.End
		end.Character += uint32(gap)
		return protocol.TextEdit{Range: protocol.Range{Start: r.Start, End: end}}, true
	case strings.HasSuffix(strings.TrimRight(before, " \t"), "->"):
		return protocol.TextEdit{Range: r, NewText: "{}"}, true
	case strings.HasSuffix(before, " ") && r.Start.Character > 0:
		start := r.Start
		start.Character--
		return protocol.TextEdit{Range: protocol.Range{Start: start, End: r.End}}, true
	}
	return protocol.TextEdit{}, false
}
